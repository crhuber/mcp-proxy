// Package upstream executes MCP tool calls against upstream REST APIs: it
// builds the outgoing HTTP request from a tool's resolved spec and the
// caller's arguments, and maps the HTTP response (or any failure) into an
// MCP CallToolResult.
package upstream

import (
	"net/http"
	"time"

	"mcp-proxy/internal/bodytmpl"
	"mcp-proxy/internal/respmap"
)

// ParamLocation is where a routed argument is placed on the outgoing HTTP
// request.
type ParamLocation string

const (
	ParamPath   ParamLocation = "path"
	ParamQuery  ParamLocation = "query"
	ParamHeader ParamLocation = "header"
)

// ParamDef describes one path/query/header-routed argument. The config
// format uses a single property name for both the argument key and the wire
// key, so there is no separate "wire name" concept.
type ParamDef struct {
	Name     string
	Location ParamLocation
	Required bool
}

// AuthConfig is the resolved (secret already read from its env var) auth
// configuration the proxy uses when calling one upstream.
type AuthConfig struct {
	Type       string // bearer | header | query | none
	Secret     string // resolved value; never logged, never echoed
	HeaderName string // used when Type == "header"; overrides the default "Authorization" header name when Type == "bearer" and non-empty
	QueryParam string // used when Type == "query"
}

// Upstream holds everything needed to call one upstream REST API. Multiple
// ToolSpecs for the same upstream share one Upstream instance (and so share
// one *http.Client and one *Redactor).
type Upstream struct {
	Name     string
	BaseURL  string
	Auth     AuthConfig
	Timeout  time.Duration
	Client   *http.Client
	Redactor *Redactor
}

// ToolSpec is everything the generic ToolHandler needs to execute one
// specific tool call.
type ToolSpec struct {
	Name         string
	Upstream     *Upstream
	Method       string
	PathTemplate string
	Params       []ParamDef
	Body         bodytmpl.Template // nil => no request body is ever sent
	Response     respmap.Template  // nil => fall back to raw response passthrough
}
