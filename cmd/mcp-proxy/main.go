// Command mcp-proxy is a generic MCP proxy: it loads a YAML config
// describing one or more upstream REST APIs, dynamically registers MCP
// tools for them, and translates each tool call into an HTTP request
// against the real upstream API and back.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/urfave/cli/v3"

	"mcp-proxy/internal/config"
	"mcp-proxy/internal/proxyauth"
	"mcp-proxy/internal/server"
	"mcp-proxy/internal/toolgen"
	"mcp-proxy/internal/upstream"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cmd := &cli.Command{
		Name:  "mcp-proxy",
		Usage: "MCP proxy that translates MCP tool calls into upstream REST API calls",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "config",
				Required: true,
				Sources:  cli.EnvVars("MCP_PROXY_CONFIG"),
				Usage:    "path to the proxy config YAML file",
			},
			&cli.StringFlag{
				Name:    "listen",
				Value:   ":8080",
				Sources: cli.EnvVars("MCP_PROXY_LISTEN_ADDR"),
				Usage:   "address to listen on",
			},
			&cli.StringFlag{
				Name:    "auth-mode",
				Value:   "none",
				Sources: cli.EnvVars("MCP_PROXY_AUTH_MODE"),
				Usage:   "none|bearer — whether the proxy's own /mcp endpoint requires a bearer token",
			},
			&cli.StringFlag{
				Name:    "log-level",
				Value:   "info",
				Sources: cli.EnvVars("MCP_PROXY_LOG_LEVEL"),
				Usage:   "debug|info|warn|error",
			},
			&cli.DurationFlag{
				Name:    "shutdown-grace",
				Value:   15 * time.Second,
				Sources: cli.EnvVars("MCP_PROXY_SHUTDOWN_GRACE"),
				Usage:   "how long to wait for in-flight requests to finish on shutdown",
			},
			&cli.BoolFlag{
				Name:    "disable-localhost-protection",
				Value:   false,
				Sources: cli.EnvVars("MCP_PROXY_DISABLE_LOCALHOST_PROTECTION"),
				Usage:   "disable the MCP SDK's DNS-rebinding Host header check; needed behind a same-host/pod reverse proxy or sidecar that connects over 127.0.0.1 but forwards a different Host header",
			},
		},
		Action: run,
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-proxy: "+err.Error())
		os.Exit(1)
	}
}

func run(_ context.Context, cmd *cli.Command) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(cmd.String("log-level"))}))

	authMode := cmd.String("auth-mode")
	if authMode != "none" && authMode != "bearer" {
		return fmt.Errorf("invalid -auth-mode %q (must be \"none\" or \"bearer\")", authMode)
	}

	// The proxy's own bearer token is deliberately NOT a cli.Flag — a secret
	// shouldn't be visible in --help output or process argv.
	var bearerToken string
	if authMode == "bearer" {
		bearerToken = os.Getenv("MCP_PROXY_BEARER_TOKEN")
		if bearerToken == "" {
			return fmt.Errorf("MCP_PROXY_AUTH_MODE=bearer but MCP_PROXY_BEARER_TOKEN is unset or empty")
		}
	}

	cfg, err := config.Load(cmd.String("config"))
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	generatedTools, err := toolgen.Generate(cfg)
	if err != nil {
		return fmt.Errorf("generating tools: %w", err)
	}

	redactor := upstream.NewRedactor(collectSecrets(cfg, bearerToken)...)
	httpClient := upstream.NewClient()

	mcpServer, err := server.Build(generatedTools, httpClient, redactor, version)
	if err != nil {
		return fmt.Errorf("building server: %w", err)
	}
	logger.Info("registered tools", "count", len(generatedTools))

	var verifier auth.TokenVerifier
	if authMode == "bearer" {
		verifier = proxyauth.NewStaticBearerVerifier(bearerToken)
	}
	handler := server.LoggingMiddleware(server.BuildHandler(mcpServer, verifier, cmd.Bool("disable-localhost-protection")), logger)

	httpSrv := &http.Server{
		Addr:              cmd.String("listen"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", httpSrv.Addr, "auth_mode", authMode)
		serveErr <- httpSrv.ListenAndServe()
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("listen failed: %w", err)
		}
		return nil
	case <-sigCtx.Done():
		stop()
		grace := cmd.Duration("shutdown-grace")
		logger.Info("shutdown signal received, draining in-flight requests", "grace", grace)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown did not complete in time: %w", err)
		}
		logger.Info("shutdown complete")
		return nil
	}
}

// collectSecrets gathers every secret the redactor must scrub from any
// tool-error message or log line: the proxy's own bearer token plus every
// upstream's resolved auth secret.
func collectSecrets(cfg *config.Config, proxyToken string) []string {
	secrets := []string{proxyToken}
	for _, ep := range cfg.Endpoints {
		if ep.Upstream.Auth.Type != "none" && ep.Upstream.Auth.Env != "" {
			secrets = append(secrets, os.Getenv(ep.Upstream.Auth.Env))
		}
	}
	return secrets
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
