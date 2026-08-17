package toolgen

import (
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-proxy/internal/bodytmpl"
	"mcp-proxy/internal/config"
	"mcp-proxy/internal/names"
	"mcp-proxy/internal/respmap"
)

// defaultUpstreamTimeout is used when an upstream's config omits `timeout`.
const defaultUpstreamTimeout = 30 * time.Second

// Generate builds the full set of MCP tools from an already-validated
// config.Config. It is pure — no dependency on *mcp.Server, no I/O — so it
// can be re-run later (e.g. for a future hot-reload) without touching a live
// server.
func Generate(cfg *config.Config) ([]GeneratedTool, error) {
	var out []GeneratedTool

	for _, ep := range cfg.Endpoints {
		timeout := defaultUpstreamTimeout
		if ep.Upstream.Timeout != "" {
			d, err := time.ParseDuration(ep.Upstream.Timeout)
			if err != nil {
				return nil, fmt.Errorf("upstream %q: invalid timeout: %w", ep.Name, err)
			}
			timeout = d
		}

		for _, tool := range ep.Tools {
			finalName, err := names.DeriveToolName(ep.Name, tool.Name)
			if err != nil {
				return nil, err
			}

			schema := tool.Parameters
			if schema == nil {
				schema = emptyObjectSchema()
			}
			params, knownNames := deriveParams(schema)
			inputSchema := buildInputSchema(schema)

			var body bodytmpl.Template
			if tool.HTTP.Body != nil {
				compiled, _, err := bodytmpl.Compile(tool.HTTP.Body, knownNames)
				if err != nil {
					return nil, fmt.Errorf("tool %q: %w", finalName, err)
				}
				body = compiled
			}

			var response respmap.Template
			if tool.HTTP.Response != nil {
				compiled, err := respmap.Compile(tool.HTTP.Response.Select)
				if err != nil {
					return nil, fmt.Errorf("tool %q: %w", finalName, err)
				}
				response = compiled
			}

			out = append(out, GeneratedTool{
				MCPTool: &mcp.Tool{
					Name:        finalName,
					Description: tool.Description,
					InputSchema: inputSchema,
				},
				UpstreamName: ep.Name,
				BaseURL:      ep.Upstream.BaseURL,
				Auth:         ep.Upstream.Auth,
				Timeout:      timeout,
				Method:       tool.HTTP.Method,
				PathTemplate: tool.HTTP.Path,
				Params:       params,
				Body:         body,
				Response:     response,
			})
		}
	}

	return out, nil
}
