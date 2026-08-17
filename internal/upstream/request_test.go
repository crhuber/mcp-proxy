package upstream

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mcp-proxy/internal/bodytmpl"
)

func testUpstream(auth AuthConfig, redactor *Redactor) *Upstream {
	return &Upstream{
		Name:     "test",
		BaseURL:  "https://api.example.com/v1",
		Auth:     auth,
		Timeout:  time.Second,
		Client:   NewClient(),
		Redactor: redactor,
	}
}

func TestBuildRequestPathQueryHeader(t *testing.T) {
	spec := &ToolSpec{
		Name:         "test_getInvoice",
		Upstream:     testUpstream(AuthConfig{Type: "none"}, nil),
		Method:       "GET",
		PathTemplate: "/invoices/{invoiceId}",
		Params: []ParamDef{
			{Name: "invoiceId", Location: ParamPath, Required: true},
			{Name: "expand", Location: ParamQuery, Required: false},
			{Name: "traceId", Location: ParamHeader, Required: false},
		},
	}

	req, displayURL, err := buildRequest(spec, map[string]any{
		"invoiceId": "in v/123", // needs escaping
		"expand":    true,
		"traceId":   "abc-123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.URL.Path != "/v1/invoices/in v/123" {
		t.Errorf("path = %q", req.URL.Path)
	}
	if !strings.Contains(req.URL.EscapedPath(), "in%20v%2F123") {
		t.Errorf("expected the space and slash in the path param to be percent-escaped on the wire (so the literal \"/\" can't introduce an extra path segment), got %q", req.URL.EscapedPath())
	}
	if got := req.URL.Query().Get("expand"); got != "true" {
		t.Errorf("expand query = %q", got)
	}
	if got := req.Header.Get("traceId"); got != "abc-123" {
		t.Errorf("traceId header = %q", got)
	}
	if displayURL == "" {
		t.Errorf("displayURL should not be empty")
	}
}

func TestBuildRequestPathParamCannotInjectExtraSegment(t *testing.T) {
	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "none"}, nil),
		Method:       "GET",
		PathTemplate: "/customers/{customerId}/orders",
		Params:       []ParamDef{{Name: "customerId", Location: ParamPath, Required: true}},
	}
	req, _, err := buildRequest(spec, map[string]any{"customerId": "../admin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The decoded Path is allowed to contain the literal value (that's just
	// what the parameter's value is)...
	if req.URL.Path != "/v1/customers/../admin/orders" {
		t.Errorf("path = %q", req.URL.Path)
	}
	// ...but on the wire, "/" within the substituted value must be escaped
	// (%2F) so it can never act as a real path separator and change which
	// resource is actually requested.
	if !strings.Contains(req.URL.EscapedPath(), "..%2Fadmin") {
		t.Errorf("expected \"/\" inside the path parameter's value to be escaped on the wire, got %q", req.URL.EscapedPath())
	}
	if strings.Contains(req.URL.EscapedPath(), "/../") {
		t.Errorf("a literal, unescaped \"/../\" must never appear in the wire path: %q", req.URL.EscapedPath())
	}
}

func TestBuildRequestMissingRequiredParam(t *testing.T) {
	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "none"}, nil),
		Method:       "GET",
		PathTemplate: "/invoices/{invoiceId}",
		Params:       []ParamDef{{Name: "invoiceId", Location: ParamPath, Required: true}},
	}
	_, _, err := buildRequest(spec, map[string]any{})
	if err == nil {
		t.Fatalf("expected an error for missing required path param")
	}
}

func TestBuildRequestOptionalParamOmitted(t *testing.T) {
	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "none"}, nil),
		Method:       "GET",
		PathTemplate: "/things",
		Params:       []ParamDef{{Name: "filter", Location: ParamQuery, Required: false}},
	}
	req, _, err := buildRequest(spec, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.URL.Query().Has("filter") {
		t.Errorf("optional query param should be absent when not supplied")
	}
}

func TestBuildRequestHeaderInjectionRejected(t *testing.T) {
	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "none"}, nil),
		Method:       "GET",
		PathTemplate: "/things",
		Params:       []ParamDef{{Name: "x", Location: ParamHeader, Required: true}},
	}
	_, _, err := buildRequest(spec, map[string]any{"x": "evil\r\nX-Injected: 1"})
	if err == nil {
		t.Fatalf("expected header injection to be rejected")
	}
}

func TestBuildRequestUnresolvedPathPlaceholder(t *testing.T) {
	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "none"}, nil),
		Method:       "GET",
		PathTemplate: "/invoices/{invoiceId}",
		Params:       nil, // invoiceId not declared as a routed param at all
	}
	_, _, err := buildRequest(spec, map[string]any{})
	if err == nil {
		t.Fatalf("expected an error for an unresolved path placeholder")
	}
}

func TestApplyUpstreamAuthBearer(t *testing.T) {
	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "bearer", Secret: "s3cr3t"}, nil),
		Method:       "GET",
		PathTemplate: "/things",
	}
	req, displayURL, err := buildRequest(spec, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer s3cr3t" {
		t.Errorf("Authorization header = %q", got)
	}
	if strings.Contains(displayURL, "s3cr3t") {
		t.Errorf("displayURL must never contain the secret: %q", displayURL)
	}
}

func TestApplyUpstreamAuthHeader(t *testing.T) {
	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "header", HeaderName: "X-API-Key", Secret: "hdr-secret"}, nil),
		Method:       "GET",
		PathTemplate: "/things",
	}
	req, _, err := buildRequest(spec, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("X-API-Key"); got != "hdr-secret" {
		t.Errorf("X-API-Key header = %q", got)
	}
}

func TestApplyUpstreamAuthQueryNotInDisplayURL(t *testing.T) {
	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "query", QueryParam: "api_key", Secret: "q-secret"}, nil),
		Method:       "GET",
		PathTemplate: "/things",
	}
	req, displayURL, err := buildRequest(spec, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.URL.Query().Get("api_key"); got != "q-secret" {
		t.Errorf("api_key query param = %q", got)
	}
	if strings.Contains(displayURL, "q-secret") {
		t.Errorf("displayURL (pre-auth) must never contain the query-auth secret: %q", displayURL)
	}
}

func TestBuildHTTPRequestBodyRenderedAndJSONContentType(t *testing.T) {
	tmpl, used, err := bodytmpl.Compile(map[string]any{
		"customerId": "{customerId}",
		"source":     "mcp-proxy",
	}, map[string]bool{"customerId": true})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !used["customerId"] {
		t.Fatalf("expected customerId to be marked used")
	}

	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "none"}, nil),
		Method:       "POST",
		PathTemplate: "/invoices",
		Body:         tmpl,
	}
	req, _, err := buildRequest(spec, map[string]any{"customerId": "cus_1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	b, _ := io.ReadAll(req.Body)
	got := string(b)
	if !strings.Contains(got, `"customerId":"cus_1"`) || !strings.Contains(got, `"source":"mcp-proxy"`) {
		t.Errorf("unexpected body: %s", got)
	}
}

func TestBuildRequestNoBodySentWhenSpecBodyNil(t *testing.T) {
	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "none"}, nil),
		Method:       "POST",
		PathTemplate: "/things",
		Body:         nil,
	}
	req, _, err := buildRequest(spec, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Body != nil {
		t.Errorf("expected no request body, got one")
	}
	if req.ContentLength > 0 {
		t.Errorf("expected zero content length, got %d", req.ContentLength)
	}
}

func TestBuildRequestAgainstRealServerURLShape(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()

	spec := &ToolSpec{
		Upstream:     testUpstream(AuthConfig{Type: "none"}, nil),
		Method:       "GET",
		PathTemplate: "/x",
	}
	spec.Upstream.BaseURL = srv.URL

	req, _, err := buildRequest(spec, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.URL.String() != srv.URL+"/x" {
		t.Errorf("url = %q, want %q", req.URL.String(), srv.URL+"/x")
	}
}
