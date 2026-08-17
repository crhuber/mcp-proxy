// Package server wires generated tools onto an *mcp.Server and exposes it
// over Streamable HTTP.
package server

import (
	"fmt"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-proxy/internal/toolgen"
	"mcp-proxy/internal/upstream"
)

// Build constructs an *mcp.Server with every generated tool registered. Each
// unique upstream (by name) gets exactly one *upstream.Upstream, sharing the
// given http.Client and Redactor, so its auth secret is read from its
// environment variable exactly once.
//
// AddTool itself silently overwrites same-named tools rather than erroring
// (confirmed against the go-sdk source), so Build does its own duplicate
// check as the real safety net — config.Validate should already have caught
// this earlier, but Build does not trust that invariant blindly.
func Build(tools []toolgen.GeneratedTool, httpClient *http.Client, redactor *upstream.Redactor, version string) (*mcp.Server, error) {
	seen := map[string]bool{}
	for _, t := range tools {
		if seen[t.MCPTool.Name] {
			return nil, fmt.Errorf("duplicate tool name %q after generation (config validation should have caught this)", t.MCPTool.Name)
		}
		seen[t.MCPTool.Name] = true
	}

	mcpServer := mcp.NewServer(
		&mcp.Implementation{Name: "mcp-proxy", Version: version},
		&mcp.ServerOptions{
			// Set explicitly rather than left nil, so the server doesn't
			// advertise the default {"logging":{}} capability it doesn't
			// implement, and doesn't advertise tools/list_changed (v1 has
			// no hot-reload, so that notification would never fire anyway).
			Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: false}},
		},
	)

	upstreams := map[string]*upstream.Upstream{}
	for _, t := range tools {
		up, ok := upstreams[t.UpstreamName]
		if !ok {
			secret := ""
			if t.Auth.Type != "none" {
				secret = os.Getenv(t.Auth.Env)
			}
			up = &upstream.Upstream{
				Name:    t.UpstreamName,
				BaseURL: t.BaseURL,
				Auth: upstream.AuthConfig{
					Type:       t.Auth.Type,
					Secret:     secret,
					HeaderName: t.Auth.Header,
					QueryParam: t.Auth.QueryParam,
				},
				Timeout:  t.Timeout,
				Client:   httpClient,
				Redactor: redactor,
			}
			upstreams[t.UpstreamName] = up
		}

		params := make([]upstream.ParamDef, len(t.Params))
		for i, p := range t.Params {
			params[i] = upstream.ParamDef{
				Name:     p.Name,
				Location: upstream.ParamLocation(p.Location),
				Required: p.Required,
			}
		}

		spec := &upstream.ToolSpec{
			Name:         t.MCPTool.Name,
			Upstream:     up,
			Method:       t.Method,
			PathTemplate: t.PathTemplate,
			Params:       params,
			Body:         t.Body,
			Response:     t.Response,
		}
		mcpServer.AddTool(t.MCPTool, upstream.NewToolHandler(spec))
	}

	return mcpServer, nil
}
