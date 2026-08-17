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
	if mapped["invoiceId"] != "inv_1" || mapped["total"] != float64(42) {
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
