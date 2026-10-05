package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-proxy/internal/respmap"
)

func callToolRequest(t *testing.T, args map[string]any) *mcp.CallToolRequest {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: raw}}
}

func TestNewToolHandlerHappyPathWithResponseSelect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/invoices/inv_1" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("expand"); got != "true" {
			t.Errorf("expand query = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"inv_1","totals":{"grandTotal":42},"internal":"hide-me"}`))
	}))
	defer srv.Close()

	tmpl, err := respmap.Compile(map[string]any{"invoiceId": "{id}", "total": "{totals.grandTotal}"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	up := testUpstream(AuthConfig{Type: "bearer", Secret: "test-token"}, nil)
	up.BaseURL = srv.URL
	spec := &ToolSpec{
		Name:         "billing_getInvoice",
		Upstream:     up,
		Method:       "GET",
		PathTemplate: "/invoices/{invoiceId}",
		Params: []ParamDef{
			{Name: "invoiceId", Location: ParamPath, Required: true},
			{Name: "expand", Location: ParamQuery, Required: false},
		},
		Response: tmpl,
	}

	handler := NewToolHandler(spec)
	result, err := handler(context.Background(), callToolRequest(t, map[string]any{"invoiceId": "inv_1", "expand": true}))
	if err != nil {
		t.Fatalf("handler returned a Go error (should never happen for expected failures): %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error result: %+v", result.StructuredContent)
	}
	mapped := result.StructuredContent.(map[string]any)
	if mapped["invoiceId"] != "inv_1" || mapped["total"] != json.Number("42") {
		t.Errorf("unexpected mapped result: %v", mapped)
	}
	if _, present := mapped["internal"]; present {
		t.Errorf("response.select must not leak unselected fields")
	}
}

func TestNewToolHandlerMissingRequiredArgNeverCallsUpstream(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	up := testUpstream(AuthConfig{Type: "none"}, nil)
	up.BaseURL = srv.URL
	spec := &ToolSpec{
		Name:         "billing_getInvoice",
		Upstream:     up,
		Method:       "GET",
		PathTemplate: "/invoices/{invoiceId}",
		Params:       []ParamDef{{Name: "invoiceId", Location: ParamPath, Required: true}},
	}

	handler := NewToolHandler(spec)
	result, err := handler(context.Background(), callToolRequest(t, map[string]any{}))
	if err != nil {
		t.Fatalf("expected a tool-level error, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for a missing required argument")
	}
	if called {
		t.Errorf("upstream must never be called when required arguments are missing")
	}
}

func TestNewToolHandlerUpstreamTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	up := testUpstream(AuthConfig{Type: "none"}, nil)
	up.BaseURL = srv.URL
	up.Timeout = 5 * time.Millisecond
	spec := &ToolSpec{Name: "billing_slow", Upstream: up, Method: "GET", PathTemplate: "/slow"}

	handler := NewToolHandler(spec)
	result, err := handler(context.Background(), callToolRequest(t, map[string]any{}))
	if err != nil {
		t.Fatalf("expected a tool-level error, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected a timeout to surface as a tool-level error")
	}
	eb := result.StructuredContent.(errBody)
	if eb.Error != "upstream_timeout" {
		t.Errorf("expected upstream_timeout, got %q", eb.Error)
	}
}

func TestNewToolHandlerDoesNotFollowCrossOriginRedirectOrForwardAuth(t *testing.T) {
	stolen := false
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stolen = true
		if got := r.Header.Get("X-API-Key"); got != "" {
			t.Errorf("custom auth header leaked to redirect target: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"from":"redirect-target"}`))
	}))
	defer dest.Close()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "hdr-secret" {
			t.Errorf("source Authorization/API key = %q", got)
		}
		http.Redirect(w, r, dest.URL+"/stolen", http.StatusFound)
	}))
	defer src.Close()

	up := testUpstream(AuthConfig{Type: "header", HeaderName: "X-API-Key", Secret: "hdr-secret"}, nil)
	up.BaseURL = src.URL
	spec := &ToolSpec{Name: "svc_ping", Upstream: up, Method: "GET", PathTemplate: "/ping"}

	result, err := NewToolHandler(spec)(context.Background(), callToolRequest(t, map[string]any{}))
	if err != nil {
		t.Fatalf("expected a tool-level error, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected a 302 to be a tool error, got success: %+v", result.StructuredContent)
	}
	if stolen {
		t.Errorf("redirect target must never be fetched (SSRF / credential leak)")
	}
	eb := result.StructuredContent.(errBody)
	if eb.Status != http.StatusFound {
		t.Errorf("expected status 302, got %+v", eb)
	}
}

func TestNewToolHandlerPostRedirectIsNotRewrittenToSuccessfulGet(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/invoices":
			http.Redirect(w, r, "/v1/invoices/", http.StatusFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/invoices/":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"listed-not-created"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer src.Close()

	up := testUpstream(AuthConfig{Type: "none"}, nil)
	up.BaseURL = src.URL
	spec := &ToolSpec{Name: "billing_createInvoice", Upstream: up, Method: "POST", PathTemplate: "/v1/invoices"}

	result, err := NewToolHandler(spec)(context.Background(), callToolRequest(t, map[string]any{}))
	if err != nil {
		t.Fatalf("expected a tool-level error, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("POST 302 must not be followed as GET and reported as success; got %+v", result.StructuredContent)
	}
	eb := result.StructuredContent.(errBody)
	if eb.Status != http.StatusFound {
		t.Errorf("expected status 302, got %+v", eb)
	}
}

func TestNewToolHandlerUpstream500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	up := testUpstream(AuthConfig{Type: "none"}, nil)
	up.BaseURL = srv.URL
	spec := &ToolSpec{Name: "billing_fail", Upstream: up, Method: "GET", PathTemplate: "/fail"}

	handler := NewToolHandler(spec)
	result, err := handler(context.Background(), callToolRequest(t, map[string]any{}))
	if err != nil {
		t.Fatalf("expected a tool-level error, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected a 500 to surface as a tool-level error")
	}
	eb := result.StructuredContent.(errBody)
	if eb.Error != "upstream_error_status" || eb.Status != 500 {
		t.Errorf("unexpected errBody: %+v", eb)
	}
}

func TestDecodeArgumentsPreservesLargeIntegers(t *testing.T) {
	args, err := decodeArguments(json.RawMessage(`{"id":9007199254740993}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, err := stringifyScalar(args["id"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "9007199254740993" {
		t.Errorf("large integer argument rounded: %s", s)
	}
}
