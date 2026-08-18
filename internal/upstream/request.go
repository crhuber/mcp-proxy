package upstream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/crhuber/mcp-proxy/internal/bodytmpl"
)

// buildRequest constructs the outgoing HTTP request for a tool call. It
// performs no I/O and returns an error for anything that should be reported
// as an "invalid_arguments" tool error rather than attempted against the
// network.
//
// displayURL is the request URL WITHOUT upstream auth attached — safe to use
// in human-readable error/log text even when auth.type is "query", so a
// query-param secret can never leak into an error message.
func buildRequest(spec *ToolSpec, args map[string]any) (req *http.Request, displayURL string, err error) {
	// rawPath accumulates the WIRE-escaped path: literal template characters
	// untouched, but each substituted value run through url.PathEscape first.
	// This is deliberate — escaping the value (not just relying on Go's
	// default path escaping later) ensures a "/" or other reserved character
	// inside a parameter's value can never introduce an extra path segment
	// or otherwise change the URL's structure; it's always opaque data for
	// this one segment, never path syntax.
	rawPath := spec.PathTemplate
	for _, p := range spec.Params {
		if p.Location != ParamPath {
			continue
		}
		v, present := args[p.Name]
		if !present || v == nil {
			if p.Required {
				return nil, "", fmt.Errorf("missing required parameter %q (path)", p.Name)
			}
			continue
		}
		s, err := stringifyScalar(v)
		if err != nil {
			return nil, "", fmt.Errorf("parameter %q (path): %w", p.Name, err)
		}
		rawPath = strings.ReplaceAll(rawPath, "{"+p.Name+"}", url.PathEscape(s))
	}
	if strings.ContainsAny(rawPath, "{}") {
		return nil, "", fmt.Errorf("unresolved path placeholder(s) remain in %q", rawPath)
	}
	// Path (decoded) must be kept consistent with RawPath (encoded) or
	// url.URL silently ignores RawPath and re-escapes from Path alone —
	// which would re-introduce the very problem rawPath's escaping avoids.
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		return nil, "", fmt.Errorf("invalid characters in path after substitution: %w", err)
	}

	base, err := url.Parse(spec.Upstream.BaseURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid upstream base_url: %w", err)
	}
	u := *base
	u.Path = joinPath(base.Path, decodedPath)
	u.RawPath = joinPath(base.EscapedPath(), rawPath)

	query := u.Query()
	for _, p := range spec.Params {
		if p.Location != ParamQuery {
			continue
		}
		v, present := args[p.Name]
		if !present || v == nil {
			if p.Required {
				return nil, "", fmt.Errorf("missing required parameter %q (query)", p.Name)
			}
			continue
		}
		s, err := stringifyScalar(v)
		if err != nil {
			return nil, "", fmt.Errorf("parameter %q (query): %w", p.Name, err)
		}
		query.Set(p.Name, s)
	}

	headers := http.Header{}
	for _, p := range spec.Params {
		if p.Location != ParamHeader {
			continue
		}
		v, present := args[p.Name]
		if !present || v == nil {
			if p.Required {
				return nil, "", fmt.Errorf("missing required parameter %q (header)", p.Name)
			}
			continue
		}
		s, err := stringifyScalar(v)
		if err != nil {
			return nil, "", fmt.Errorf("parameter %q (header): %w", p.Name, err)
		}
		if strings.ContainsAny(s, "\r\n") {
			return nil, "", fmt.Errorf("parameter %q (header) contains illegal control characters", p.Name)
		}
		headers.Set(p.Name, s)
	}

	// Captured BEFORE upstream auth is attached below.
	display := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath, RawQuery: query.Encode()}
	displayURL = display.String()

	applyUpstreamAuth(spec.Upstream.Auth, query, headers)
	u.RawQuery = query.Encode()

	var body io.Reader
	if spec.Body != nil {
		rendered := bodytmpl.Render(spec.Body, args)
		b, err := json.Marshal(rendered)
		if err != nil {
			return nil, "", fmt.Errorf("encoding request body: %w", err)
		}
		body = bytes.NewReader(b)
	}

	httpReq, err := http.NewRequest(spec.Method, u.String(), body)
	if err != nil {
		return nil, "", fmt.Errorf("building request: %w", err)
	}
	httpReq.Header = headers
	if spec.Body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	return httpReq, displayURL, nil
}

// applyUpstreamAuth mutates query/headers in place to attach the upstream's
// configured credential. query is url.Values (a map), so mutation via .Set
// is visible to the caller without a pointer receiver.
func applyUpstreamAuth(a AuthConfig, query url.Values, headers http.Header) {
	switch a.Type {
	case "bearer":
		name := a.HeaderName
		if name == "" {
			name = "Authorization"
		}
		headers.Set(name, "Bearer "+a.Secret)
	case "header":
		headers.Set(a.HeaderName, a.Secret)
	case "query":
		query.Set(a.QueryParam, a.Secret)
	case "none":
		// no-op
	}
}

// stringifyScalar converts a decoded JSON value into its string form for use
// in a path segment, query value, or header value. Only scalar JSON types
// are supported here — arrays/objects can't meaningfully become part of a
// URL or header, and config validation already restricts path/query/header
// parameters to scalar types; this is defense in depth.
func stringifyScalar(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), nil
		}
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("unsupported value type %T for a path/query/header parameter", v)
	}
}

func joinPath(base, extra string) string {
	if base == "" || base == "/" {
		return extra
	}
	return strings.TrimSuffix(base, "/") + extra
}
