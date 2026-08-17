// Package config loads, env-interpolates, and validates the mcp-proxy YAML
// configuration file.
package config

// Config is the root of the proxy's YAML configuration file.
type Config struct {
	Endpoints []EndpointConfig `yaml:"endpoints"`
}

// EndpointConfig describes one upstream REST API and the tools generated
// from it.
type EndpointConfig struct {
	Name     string         `yaml:"name"`
	Upstream UpstreamConfig `yaml:"upstream"`
	Tools    []ToolConfig   `yaml:"tools"`
}

// UpstreamConfig describes how the proxy connects to and authenticates with
// the upstream API.
type UpstreamConfig struct {
	BaseURL string     `yaml:"base_url"`
	Timeout string     `yaml:"timeout"`
	Auth    AuthConfig `yaml:"auth"`
}

// AuthConfig describes how the proxy authenticates itself to an upstream
// when calling it. The actual secret is always read from an environment
// variable named by Env — never stored literally in the config file.
type AuthConfig struct {
	Type       string `yaml:"type"` // bearer | header | query | none
	Env        string `yaml:"env,omitempty"`
	Header     string `yaml:"header,omitempty"`
	QueryParam string `yaml:"query_param,omitempty"`
}

// ToolConfig describes one MCP tool generated from a single upstream HTTP
// endpoint.
type ToolConfig struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Parameters  map[string]any `yaml:"parameters"`
	HTTP        HTTPConfig     `yaml:"http"`
}

// HTTPConfig describes the upstream HTTP call a tool makes: the
// method/path template, the optional request body template, and the
// optional response-shaping rule.
type HTTPConfig struct {
	Method   string          `yaml:"method"`
	Path     string          `yaml:"path"`
	Body     map[string]any  `yaml:"body,omitempty"`
	Response *ResponseConfig `yaml:"response,omitempty"`
}

// ResponseConfig optionally reduces the upstream's JSON response to a
// purpose-built shape before it's returned to the calling LLM.
type ResponseConfig struct {
	Select any `yaml:"select"`
}
