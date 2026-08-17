// Package toolgen converts a validated config.Config into the fully-resolved
// data a generic MCP tool handler needs: an *mcp.Tool ready for
// Server.AddTool, plus path/query/header routing and compiled httpRequestBody /
// response.select templates.
package toolgen

import (
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-proxy/internal/bodytmpl"
	"mcp-proxy/internal/config"
	"mcp-proxy/internal/respmap"
)

// ParamDef describes one path/query/header-routed argument.
type ParamDef struct {
	Name     string
	Location string // path | query | header
	Required bool
}

// GeneratedTool is the handoff contract from config+toolgen to the
// server/upstream packages: everything needed to register and execute one
// MCP tool, with no dependency on *mcp.Server or any network I/O.
type GeneratedTool struct {
	MCPTool      *mcp.Tool
	UpstreamName string
	BaseURL      string            // env-interpolated
	Auth         config.AuthConfig // type/env/header/query_param — NOT the resolved secret
	Timeout      time.Duration
	Method       string
	PathTemplate string
	Params       []ParamDef        // path/query/header routing
	Body         bodytmpl.Template // nil if the tool has no httpRequestBody
	Response     respmap.Template  // nil if the tool has no response.select
}
