package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-proxy/internal/config"
	"mcp-proxy/internal/proxyauth"
	"mcp-proxy/internal/server"
	"mcp-proxy/internal/toolgen"
	"mcp-proxy/internal/upstream"
)

// buildProxy runs the same pipeline as main.run() (config validate ->
// toolgen.Generate -> server.Build -> server.BuildHandler) against an
// in-memory *config.Config, and serves it via httptest.NewServer. It returns
// the server (caller must Close it) and the /mcp endpoint URL.
func buildProxy(t *testing.T, cfg *config.Config, bearerToken string) (*httptest.Server, string) {
	t.Helper()
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("config.Validate: %v", err)
	}
	tools, err := toolgen.Generate(cfg)
	if err != nil {
		t.Fatalf("toolgen.Generate: %v", err)
	}
	redactor := upstream.NewRedactor(bearerToken)
	mcpServer, err := server.Build(tools, upstream.NewClient(), redactor, "test")
	if err != nil {
		t.Fatalf("server.Build: %v", err)
	}
	var verifier auth.TokenVerifier
	if bearerToken != "" {
		verifier = proxyauth.NewStaticBearerVerifier(bearerToken)
	}
	handler := server.BuildHandler(mcpServer, verifier, false)
	srv := httptest.NewServer(handler)
	return srv, srv.URL + "/mcp"
}

// connectClient dials the proxy's /mcp endpoint with a real MCP client over
// the real Streamable HTTP transport, optionally with a bearer token.
func connectClient(ctx context.Context, endpoint, bearerToken string) (*mcp.ClientSession, error) {
	httpClient := http.DefaultClient
	if bearerToken != "" {
		httpClient = &http.Client{Transport: bearerRoundTripper{token: bearerToken, base: http.DefaultTransport}}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient}
	return client.Connect(ctx, transport, nil)
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(req)
}

func mustCallTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return result
}

func structuredMap(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	m, ok := result.StructuredContent.(map[string]any)
	if !ok {
		// The client unmarshals StructuredContent generically as
		// map[string]interface{} coming off the wire; handle both shapes.
		b, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured content: %v", err)
		}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal structured content: %v", err)
		}
	}
	return m
}

func TestIntegrationGetWithResponseSelectAndArrayPaths(t *testing.T) {
	var gotPath, gotQuery string
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("status")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"orders": [{"id":"o1","total":10},{"id":"o2","total":20}],
			"appliedPromoCodes": ["SAVE10","WELCOME"],
			"internalDebug": "should not appear"
		}`))
	}))
	defer upstreamSrv.Close()

	cfg := &config.Config{
		Endpoints: []config.EndpointConfig{{
			Name: "orders",
			Upstream: config.UpstreamConfig{
				BaseURL: upstreamSrv.URL,
				Auth:    config.AuthConfig{Type: "none"},
			},
			Tools: []config.ToolConfig{{
				Name:        "listOrders",
				Description: "List orders for a customer.",
				Parameters: map[string]any{
					"type":     "object",
					"required": []any{"customerId"},
					"properties": map[string]any{
						"customerId": map[string]any{"type": "string", "in": "path"},
						"status":     map[string]any{"type": "string", "in": "query"},
					},
				},
				HTTP: config.HTTPConfig{
					Method: "GET",
					Path:   "/v2/customers/{customerId}/orders",
					Response: &config.ResponseConfig{Select: map[string]any{
						"orderIds":    "{orders[].id}",
						"orderTotals": "{orders[].total}",
						"promoCodes":  "{appliedPromoCodes[]}",
					}},
				},
			}},
		}},
	}

	srv, endpoint := buildProxy(t, cfg, "")
	defer srv.Close()

	cs, err := connectClient(context.Background(), endpoint, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	result := mustCallTool(t, cs, "orders_listOrders", map[string]any{"customerId": "cus_1", "status": "pending"})
	if result.IsError {
		t.Fatalf("expected success, got error: %+v", result.Content)
	}
	if gotPath != "/v2/customers/cus_1/orders" {
		t.Errorf("upstream received path %q", gotPath)
	}
	if gotQuery != "pending" {
		t.Errorf("upstream received status query %q", gotQuery)
	}

	mapped := structuredMap(t, result)
	orderIDs, _ := mapped["orderIds"].([]any)
	if len(orderIDs) != 2 || orderIDs[0] != "o1" || orderIDs[1] != "o2" {
		t.Errorf("orderIds = %v", mapped["orderIds"])
	}
	promoCodes, _ := mapped["promoCodes"].([]any)
	if len(promoCodes) != 2 || promoCodes[0] != "SAVE10" {
		t.Errorf("promoCodes = %v", mapped["promoCodes"])
	}
	if _, present := mapped["internalDebug"]; present {
		t.Errorf("response.select must reduce the shape — internalDebug must not leak through")
	}
}

func TestIntegrationPostWithHTTPRequestBodyRenamingAndStaticLiteral(t *testing.T) {
	var gotBody map[string]any
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"inv_99","status":"draft"}`))
	}))
	defer upstreamSrv.Close()

	cfg := &config.Config{
		Endpoints: []config.EndpointConfig{{
			Name: "billing",
			Upstream: config.UpstreamConfig{
				BaseURL: upstreamSrv.URL,
				Auth:    config.AuthConfig{Type: "none"},
			},
			Tools: []config.ToolConfig{{
				Name:        "createInvoice",
				Description: "Create an invoice.",
				Parameters: map[string]any{
					"type":     "object",
					"required": []any{"customerId", "lineItems"},
					"properties": map[string]any{
						"customerId": map[string]any{"type": "string"},
						"lineItems":  map[string]any{"type": "array"},
					},
				},
				HTTP: config.HTTPConfig{
					Method: "POST",
					Path:   "/v1/invoices",
					Body: map[string]any{
						"customerId": "{customerId}",
						"items":      "{lineItems}",
						"source":     "mcp-proxy",
					},
					Response: &config.ResponseConfig{Select: map[string]any{
						"invoiceId": "{id}",
						"status":    "{status}",
					}},
				},
			}},
		}},
	}

	srv, endpoint := buildProxy(t, cfg, "")
	defer srv.Close()

	cs, err := connectClient(context.Background(), endpoint, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	result := mustCallTool(t, cs, "billing_createInvoice", map[string]any{
		"customerId": "cus_1",
		"lineItems":  []any{map[string]any{"description": "Widget", "amountCents": float64(500)}},
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %+v", result.Content)
	}
	if gotBody["customerId"] != "cus_1" {
		t.Errorf("upstream body customerId = %v", gotBody["customerId"])
	}
	if gotBody["source"] != "mcp-proxy" {
		t.Errorf("upstream body source = %v", gotBody["source"])
	}
	if _, present := gotBody["lineItems"]; present {
		t.Errorf("httpRequestBody renamed \"lineItems\" to \"items\" — the old key must not also appear")
	}
	items, _ := gotBody["items"].([]any)
	if len(items) != 1 {
		t.Errorf("upstream body items = %v", gotBody["items"])
	}

	mapped := structuredMap(t, result)
	if mapped["invoiceId"] != "inv_99" {
		t.Errorf("mapped invoiceId = %v", mapped["invoiceId"])
	}
}

func TestIntegrationMissingRequiredArgNeverCallsUpstream(t *testing.T) {
	called := false
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer upstreamSrv.Close()

	cfg := &config.Config{
		Endpoints: []config.EndpointConfig{{
			Name: "billing",
			Upstream: config.UpstreamConfig{
				BaseURL: upstreamSrv.URL,
				Auth:    config.AuthConfig{Type: "none"},
			},
			Tools: []config.ToolConfig{{
				Name:        "getInvoice",
				Description: "Get an invoice.",
				Parameters: map[string]any{
					"type":     "object",
					"required": []any{"invoiceId"},
					"properties": map[string]any{
						"invoiceId": map[string]any{"type": "string", "in": "path"},
					},
				},
				HTTP: config.HTTPConfig{
					Method: "GET",
					Path:   "/v1/invoices/{invoiceId}",
				},
			}},
		}},
	}

	srv, endpoint := buildProxy(t, cfg, "")
	defer srv.Close()
	cs, err := connectClient(context.Background(), endpoint, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	result := mustCallTool(t, cs, "billing_getInvoice", map[string]any{})
	if !result.IsError {
		t.Fatalf("expected an error result for a missing required argument")
	}
	if called {
		t.Errorf("upstream must never be called when a required argument is missing")
	}
}

func TestIntegrationUpstream404NeverShapedByResponseSelect(t *testing.T) {
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"invoice not found","id":"missing"}`))
	}))
	defer upstreamSrv.Close()

	cfg := &config.Config{
		Endpoints: []config.EndpointConfig{{
			Name: "billing",
			Upstream: config.UpstreamConfig{
				BaseURL: upstreamSrv.URL,
				Auth:    config.AuthConfig{Type: "none"},
			},
			Tools: []config.ToolConfig{{
				Name:        "getInvoice",
				Description: "Get an invoice.",
				Parameters: map[string]any{
					"type":     "object",
					"required": []any{"invoiceId"},
					"properties": map[string]any{
						"invoiceId": map[string]any{"type": "string", "in": "path"},
					},
				},
				HTTP: config.HTTPConfig{
					Method:   "GET",
					Path:     "/v1/invoices/{invoiceId}",
					Response: &config.ResponseConfig{Select: map[string]any{"invoiceId": "{id}"}},
				},
			}},
		}},
	}

	srv, endpoint := buildProxy(t, cfg, "")
	defer srv.Close()
	cs, err := connectClient(context.Background(), endpoint, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	result := mustCallTool(t, cs, "billing_getInvoice", map[string]any{"invoiceId": "missing"})
	if !result.IsError {
		t.Fatalf("expected an error result for a 404")
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "invoice not found") {
		t.Errorf("expected the raw upstream error body to be visible (not response.select-shaped), got %q", text)
	}
}

func TestIntegrationAuthAttachmentMatrix(t *testing.T) {
	tests := []struct {
		name string
		auth config.AuthConfig
		env  map[string]string
		want func(t *testing.T, r *http.Request)
	}{
		{
			name: "bearer",
			auth: config.AuthConfig{Type: "bearer", Env: "TEST_BEARER_SECRET"},
			env:  map[string]string{"TEST_BEARER_SECRET": "bearer-tok"},
			want: func(t *testing.T, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer bearer-tok" {
					t.Errorf("Authorization = %q", got)
				}
			},
		},
		{
			name: "header",
			auth: config.AuthConfig{Type: "header", Env: "TEST_HEADER_SECRET", Header: "X-API-Key"},
			env:  map[string]string{"TEST_HEADER_SECRET": "header-tok"},
			want: func(t *testing.T, r *http.Request) {
				if got := r.Header.Get("X-API-Key"); got != "header-tok" {
					t.Errorf("X-API-Key = %q", got)
				}
			},
		},
		{
			name: "query",
			auth: config.AuthConfig{Type: "query", Env: "TEST_QUERY_SECRET", QueryParam: "api_key"},
			env:  map[string]string{"TEST_QUERY_SECRET": "query-tok"},
			want: func(t *testing.T, r *http.Request) {
				if got := r.URL.Query().Get("api_key"); got != "query-tok" {
					t.Errorf("api_key = %q", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.want(t, r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstreamSrv.Close()

			cfg := &config.Config{
				Endpoints: []config.EndpointConfig{{
					Name: "svc",
					Upstream: config.UpstreamConfig{
						BaseURL: upstreamSrv.URL,
						Auth:    tc.auth,
					},
					Tools: []config.ToolConfig{{
						Name:        "ping",
						Description: "Ping.",
						HTTP:        config.HTTPConfig{Method: "GET", Path: "/ping"},
					}},
				}},
			}

			srv, endpoint := buildProxy(t, cfg, "")
			defer srv.Close()
			cs, err := connectClient(context.Background(), endpoint, "")
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer cs.Close()

			result := mustCallTool(t, cs, "svc_ping", map[string]any{})
			if result.IsError {
				t.Fatalf("expected success, got error: %+v", result.Content)
			}
		})
	}
}

func TestIntegrationProxyBearerAuth(t *testing.T) {
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstreamSrv.Close()

	cfg := &config.Config{
		Endpoints: []config.EndpointConfig{{
			Name: "svc",
			Upstream: config.UpstreamConfig{
				BaseURL: upstreamSrv.URL,
				Auth:    config.AuthConfig{Type: "none"},
			},
			Tools: []config.ToolConfig{{
				Name:        "ping",
				Description: "Ping.",
				HTTP:        config.HTTPConfig{Method: "GET", Path: "/ping"},
			}},
		}},
	}

	srv, endpoint := buildProxy(t, cfg, "proxy-secret")
	defer srv.Close()

	// No token at all: the real assertion is that the raw HTTP request
	// itself is rejected before ever reaching the MCP/JSON-RPC layer.
	resp, err := http.Post(endpoint, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unauthenticated POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for an unauthenticated request, got %d", resp.StatusCode)
	}

	// Correct token: a full MCP session should work normally.
	cs, err := connectClient(context.Background(), endpoint, "proxy-secret")
	if err != nil {
		t.Fatalf("connect with correct token: %v", err)
	}
	defer cs.Close()
	result := mustCallTool(t, cs, "svc_ping", map[string]any{})
	if result.IsError {
		t.Fatalf("expected success with the correct proxy bearer token, got error: %+v", result.Content)
	}
}

func TestIntegrationMultiUpstreamNoCrossContamination(t *testing.T) {
	svcA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"from":"A"}`))
	}))
	defer svcA.Close()
	svcB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"from":"B"}`))
	}))
	defer svcB.Close()

	cfg := &config.Config{
		Endpoints: []config.EndpointConfig{
			{
				Name:     "svcA",
				Upstream: config.UpstreamConfig{BaseURL: svcA.URL, Auth: config.AuthConfig{Type: "none"}},
				Tools:    []config.ToolConfig{{Name: "ping", Description: "Ping A.", HTTP: config.HTTPConfig{Method: "GET", Path: "/ping"}}},
			},
			{
				Name:     "svcB",
				Upstream: config.UpstreamConfig{BaseURL: svcB.URL, Auth: config.AuthConfig{Type: "none"}},
				Tools:    []config.ToolConfig{{Name: "ping", Description: "Ping B.", HTTP: config.HTTPConfig{Method: "GET", Path: "/ping"}}},
			},
		},
	}

	srv, endpoint := buildProxy(t, cfg, "")
	defer srv.Close()
	cs, err := connectClient(context.Background(), endpoint, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	resultA := mustCallTool(t, cs, "svcA_ping", map[string]any{})
	resultB := mustCallTool(t, cs, "svcB_ping", map[string]any{})
	mappedA := structuredMap(t, resultA)
	mappedB := structuredMap(t, resultB)
	if mappedA["from"] != "A" {
		t.Errorf("svcA_ping returned %v, want from=A", mappedA)
	}
	if mappedB["from"] != "B" {
		t.Errorf("svcB_ping returned %v, want from=B", mappedB)
	}
}
