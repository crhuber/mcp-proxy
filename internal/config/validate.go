package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"mcp-proxy/internal/bodytmpl"
	"mcp-proxy/internal/names"
	"mcp-proxy/internal/respmap"
)

var pathTokenPattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

var validMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
var validAuthTypes = map[string]bool{"bearer": true, "header": true, "query": true, "none": true}
var validParamTypes = map[string]bool{"string": true, "integer": true, "number": true, "boolean": true}
var validParamLocations = map[string]bool{"path": true, "query": true, "header": true}

// Validate runs every boot-time check against cfg, collecting ALL problems
// found (never failing on the first) and returning one aggregated error if
// any exist.
func Validate(cfg *Config) error {
	var errs ValidationErrors

	endpointNames := map[string]bool{}
	finalToolNames := map[string][]string{} // final name -> "endpoint.tools[i]" paths that produced it

	for ei, ep := range cfg.Endpoints {
		epPath := fmt.Sprintf("endpoints[%d]", ei)
		if ep.Name != "" {
			epPath = fmt.Sprintf("%s (%s)", epPath, ep.Name)
		}

		switch {
		case ep.Name == "":
			errs = append(errs, ValidationError{Path: epPath, Msg: "name is required"})
		case endpointNames[ep.Name]:
			errs = append(errs, ValidationError{Path: epPath, Msg: fmt.Sprintf("duplicate endpoint name %q", ep.Name)})
		default:
			endpointNames[ep.Name] = true
		}

		upPath := epPath + ".upstream"

		if ep.Upstream.BaseURL == "" {
			errs = append(errs, ValidationError{Path: upPath, Msg: "base_url is required"})
		} else if u, err := url.Parse(ep.Upstream.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, ValidationError{Path: upPath, Msg: fmt.Sprintf("base_url %q must be an absolute URL with scheme and host", ep.Upstream.BaseURL)})
		}

		if ep.Upstream.Timeout != "" {
			if d, err := time.ParseDuration(ep.Upstream.Timeout); err != nil || d <= 0 {
				errs = append(errs, ValidationError{Path: upPath, Msg: fmt.Sprintf("timeout %q must be a positive Go duration (e.g. \"10s\")", ep.Upstream.Timeout)})
			}
		}

		errs = append(errs, validateAuth(upPath, ep.Upstream.Auth)...)

		toolNamesInEndpoint := map[string]bool{}
		for ti, tool := range ep.Tools {
			toolPath := fmt.Sprintf("%s.tools[%d]", epPath, ti)
			if tool.Name != "" {
				toolPath = fmt.Sprintf("%s (%s)", toolPath, tool.Name)
			}

			switch {
			case tool.Name == "":
				errs = append(errs, ValidationError{Path: toolPath, Msg: "name is required"})
			case toolNamesInEndpoint[tool.Name]:
				errs = append(errs, ValidationError{Path: toolPath, Msg: fmt.Sprintf("duplicate tool name %q within upstream %q", tool.Name, ep.Name)})
			default:
				toolNamesInEndpoint[tool.Name] = true
			}

			toolErrs, finalName := validateTool(toolPath, ep.Name, tool)
			errs = append(errs, toolErrs...)
			if finalName != "" {
				finalToolNames[finalName] = append(finalToolNames[finalName], toolPath)
			}
		}
	}

	for finalName, paths := range finalToolNames {
		if len(paths) > 1 {
			sorted := append([]string(nil), paths...)
			sort.Strings(sorted)
			errs = append(errs, ValidationError{
				Path: strings.Join(sorted, ", "),
				Msg:  fmt.Sprintf("multiple tools derive the same final registered name %q", finalName),
			})
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func validateAuth(path string, auth AuthConfig) ValidationErrors {
	var errs ValidationErrors
	authPath := path + ".auth"

	if !validAuthTypes[auth.Type] {
		errs = append(errs, ValidationError{Path: authPath, Msg: fmt.Sprintf("unknown auth type %q (must be bearer|header|query|none)", auth.Type)})
		return errs
	}

	switch auth.Type {
	case "none":
		if auth.Env != "" || auth.Header != "" || auth.QueryParam != "" {
			errs = append(errs, ValidationError{Path: authPath, Msg: `auth.type is "none" but env/header/query_param is set`})
		}
	case "bearer":
		if auth.Env == "" {
			errs = append(errs, ValidationError{Path: authPath, Msg: "env is required when auth.type is bearer"})
		}
	case "header":
		if auth.Env == "" {
			errs = append(errs, ValidationError{Path: authPath, Msg: "env is required when auth.type is header"})
		}
		if auth.Header == "" {
			errs = append(errs, ValidationError{Path: authPath, Msg: "header is required when auth.type is header"})
		}
	case "query":
		if auth.Env == "" {
			errs = append(errs, ValidationError{Path: authPath, Msg: "env is required when auth.type is query"})
		}
		if auth.QueryParam == "" {
			errs = append(errs, ValidationError{Path: authPath, Msg: "query_param is required when auth.type is query"})
		}
	}

	if auth.Type != "none" && auth.Env != "" {
		if v, ok := os.LookupEnv(auth.Env); !ok || v == "" {
			errs = append(errs, ValidationError{Path: authPath, Msg: fmt.Sprintf("environment variable %q referenced by auth.env is not set (or is empty)", auth.Env)})
		}
	}

	return errs
}

// validateTool checks one tool config entry and returns both any problems
// found and the tool's final derived registered name (empty if derivation
// itself failed).
func validateTool(path, upstreamName string, tool ToolConfig) (ValidationErrors, string) {
	var errs ValidationErrors

	if strings.TrimSpace(tool.Description) == "" {
		errs = append(errs, ValidationError{Path: path, Msg: "description is required"})
	}
	if !validMethods[tool.HTTP.Method] {
		errs = append(errs, ValidationError{Path: path, Msg: fmt.Sprintf("unknown method %q (must be GET|POST|PUT|PATCH|DELETE)", tool.HTTP.Method)})
	}
	if !strings.HasPrefix(tool.HTTP.Path, "/") {
		errs = append(errs, ValidationError{Path: path, Msg: fmt.Sprintf("path %q must start with \"/\"", tool.HTTP.Path)})
	}

	schema := tool.Parameters
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if t, _ := schema["type"].(string); t != "object" {
		errs = append(errs, ValidationError{Path: path + ".parameters", Msg: `parameters must be a JSON Schema object with "type": "object" at the root`})
	}

	props, _ := schema["properties"].(map[string]any)
	routedIn := map[string]string{} // property name -> in
	knownNames := map[string]bool{}
	for name, raw := range props {
		knownNames[name] = true
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		inVal, tagged := prop["in"]
		if !tagged {
			continue
		}
		inStr, ok := inVal.(string)
		propPath := fmt.Sprintf("%s.parameters.properties.%s", path, name)
		if !ok || !validParamLocations[inStr] {
			errs = append(errs, ValidationError{Path: propPath, Msg: fmt.Sprintf("invalid \"in\" value %v (must be path|query|header)", inVal)})
			continue
		}
		propType, _ := prop["type"].(string)
		if !validParamTypes[propType] {
			errs = append(errs, ValidationError{Path: propPath, Msg: fmt.Sprintf("properties tagged \"in\" must have a scalar type (string|integer|number|boolean), got %q", propType)})
			continue
		}
		routedIn[name] = inStr
	}

	// Path template <-> in:path cross-check.
	pathTokens := map[string]bool{}
	for _, m := range pathTokenPattern.FindAllStringSubmatch(tool.HTTP.Path, -1) {
		pathTokens[m[1]] = true
	}
	for token := range pathTokens {
		if routedIn[token] != "path" {
			errs = append(errs, ValidationError{Path: path, Msg: fmt.Sprintf("path placeholder {%s} has no matching parameter tagged in:path", token)})
		}
	}
	for name, in := range routedIn {
		if in == "path" && !pathTokens[name] {
			errs = append(errs, ValidationError{Path: path, Msg: fmt.Sprintf("parameter %q is tagged in:path but does not appear as {%s} in path %q", name, name, tool.HTTP.Path)})
		}
	}

	// http.body rules.
	var bodyUsed map[string]bool
	if tool.HTTP.Body != nil {
		if tool.HTTP.Method == "GET" || tool.HTTP.Method == "DELETE" {
			errs = append(errs, ValidationError{Path: path + ".http.body", Msg: fmt.Sprintf("http.body is not allowed on %s tools", tool.HTTP.Method)})
		}
		_, used, err := bodytmpl.Compile(tool.HTTP.Body, knownNames)
		if err != nil {
			errs = append(errs, ValidationError{Path: path + ".http.body", Msg: err.Error()})
		} else {
			bodyUsed = used
		}
	}

	// Every declared parameter must be used somewhere.
	for name := range knownNames {
		_, taggedOk := routedIn[name]
		_, bodyOk := bodyUsed[name]
		if !taggedOk && !bodyOk {
			errs = append(errs, ValidationError{
				Path: fmt.Sprintf("%s.parameters.properties.%s", path, name),
				Msg:  fmt.Sprintf("parameter %q is declared but never routed anywhere (tag it with \"in\" or reference it in http.body)", name),
			})
		}
	}

	// http.response.select syntax rules (path *resolvability* can't be checked here — see plan §1).
	if tool.HTTP.Response != nil {
		if _, err := respmap.Compile(tool.HTTP.Response.Select); err != nil {
			errs = append(errs, ValidationError{Path: path + ".http.response.select", Msg: err.Error()})
		}
	}

	finalName, err := names.DeriveToolName(upstreamName, tool.Name)
	if err != nil {
		errs = append(errs, ValidationError{Path: path, Msg: err.Error()})
		finalName = ""
	}

	return errs, finalName
}
