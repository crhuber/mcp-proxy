package server

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BuildHandler wraps mcpServer in a stateless StreamableHTTPHandler mounted
// at /mcp, plus an unauthenticated /healthz for liveness probes. Stateless
// mode is correct here: the proxy never needs a server-initiated request
// back to the client (no sampling/elicitation use case), so there's no
// session-affinity requirement.
//
// If bearerVerifier is non-nil, /mcp is wrapped with bearer-token auth;
// /healthz is never protected, so orchestrators can probe liveness without a
// token.
//
// disableLocalhostProtection turns off the SDK's DNS-rebinding Host-header
// check. That check assumes a loopback-bound server is only ever reached by
// local clients (e.g. a browser on the same machine), so it rejects requests
// whose Host header isn't also loopback. It misfires behind a same-host/pod
// reverse proxy or service-mesh sidecar that dials the proxy over
// 127.0.0.1 but forwards the original external Host header — mcp-proxy has
// no browser-based attack surface, so disabling it in that topology is safe.
func BuildHandler(mcpServer *mcp.Server, bearerVerifier auth.TokenVerifier, disableLocalhostProtection bool) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{
			Stateless:                    true,
			PropagateRequestCancellation: true,
			DisableLocalhostProtection:   disableLocalhostProtection,
		},
	)

	var mcpRoute http.Handler = mcpHandler
	if bearerVerifier != nil {
		// AllowMissingExpiration is required: a static shared-secret token
		// has no expiration, and without this the middleware rejects every
		// request regardless of whether the token is correct.
		mw := auth.RequireBearerToken(bearerVerifier, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})
		mcpRoute = mw(mcpHandler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/mcp", mcpRoute)
	return mux
}
