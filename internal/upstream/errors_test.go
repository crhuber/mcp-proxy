package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-proxy/internal/respmap"
)

func testSpec(response respmap.Template, redactor *Redactor) *ToolSpec {
	return &ToolSpec{
		Name:     "billing_getInvoice",
		Upstream: testUpstream(AuthConfig{Type: "none"}, redactor),
		Response: response,
	}
}

func fakeResponse(status int, body string, headers map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     h,
	}
}

func TestMapUpstreamResponse4xxNeverShapedByResponseSelect(t *testing.T) {
	tmpl, err := respmap.Compile(map[string]any{"id": "{id}"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	spec := testSpec(tmpl, nil)

	result, err := mapUpstreamResponse(spec, fakeResponse(404, `{"id":"abc","error":"not found"}`, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for a 404")
	}
	eb, ok := result.StructuredContent.(errBody)
	if !ok {
		t.Fatalf("expected errBody structured content, got %T", result.StructuredContent)
	}
	if eb.Error != "upstream_error_status" || eb.Status != 404 {
		t.Fatalf("unexpected errBody: %+v", eb)
	}
	// The raw body (including fields NOT in the response.select template)
	// must be visible, proving 4xx responses are never passed through
	// respmap.Render.
	if !strings.Contains(eb.Message, "not found") {
		t.Errorf("expected raw error body in message, got %q", eb.Message)
	}
}

func TestMapUpstreamResponseSuccessWithResponseSelect(t *testing.T) {
	tmpl, err := respmap.Compile(map[string]any{
		"invoiceId": "{id}",
		"total":     "{totals.grandTotal}",
		"missing":   "{doesNotExist}",
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	spec := testSpec(tmpl, nil)

	result, err := mapUpstreamResponse(spec, fakeResponse(200,
		`{"id":"inv_1","totals":{"grandTotal":42},"secretInternalField":"should not appear"}`,
		map[string]string{"Content-Type": "application/json"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error result")
	}
	mapped, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any structured content, got %T", result.StructuredContent)
	}
	if mapped["invoiceId"] != "inv_1" {
		t.Errorf("invoiceId = %v", mapped["invoiceId"])
	}
	if mapped["total"] != json.Number("42") {
		t.Errorf("total = %v", mapped["total"])
	}
	if mapped["missing"] != nil {
		t.Errorf("expected missing path to resolve to nil, got %v", mapped["missing"])
	}
	if _, present := mapped["secretInternalField"]; present {
		t.Errorf("response.select must reduce the shape — unselected fields must not appear")
	}
}

func TestMapUpstreamResponseNoSelectFallsBackToRawPassthrough(t *testing.T) {
	spec := testSpec(nil, nil)
	result, err := mapUpstreamResponse(spec, fakeResponse(200, `{"a":1}`, map[string]string{"Content-Type": "application/json"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success")
	}
	if result.Content[0].(*mcp.TextContent).Text != `{"a":1}` {
		t.Errorf("expected raw passthrough body, got %v", result.Content[0])
	}
}

func TestMapUpstreamResponseNonJSONBodyWithSelectYieldsResponseUnparseable(t *testing.T) {
	tmpl, err := respmap.Compile(map[string]any{"x": "{id}"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	spec := testSpec(tmpl, nil)

	result, err := mapUpstreamResponse(spec, fakeResponse(200, "not json at all", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected an error result")
	}
	eb := result.StructuredContent.(errBody)
	if eb.Error != "response_unparseable" {
		t.Errorf("expected response_unparseable, got %q", eb.Error)
	}
}

func TestMapTransportErrorTimeoutVsUnreachable(t *testing.T) {
	spec := testSpec(nil, nil)

	timeoutResult := mapTransportError(spec, context.DeadlineExceeded, "https://api.example.com/x")
	eb := timeoutResult.StructuredContent.(errBody)
	if eb.Error != "upstream_timeout" {
		t.Errorf("expected upstream_timeout, got %q", eb.Error)
	}

	dnsErr := &net.DNSError{Err: "no such host", Name: "nowhere.invalid", IsNotFound: true}
	unreachableResult := mapTransportError(spec, dnsErr, "https://nowhere.invalid/x")
	eb2 := unreachableResult.StructuredContent.(errBody)
	if eb2.Error != "upstream_unreachable" {
		t.Errorf("expected upstream_unreachable, got %q", eb2.Error)
	}

	genericErr := errors.New("connection refused")
	genericResult := mapTransportError(spec, genericErr, "https://api.example.com/x")
	eb3 := genericResult.StructuredContent.(errBody)
	if eb3.Error != "upstream_unreachable" {
		t.Errorf("expected upstream_unreachable for a generic network error, got %q", eb3.Error)
	}
}

func TestMapTransportErrorNeverLeaksQuerySecret(t *testing.T) {
	spec := testSpec(nil, NewRedactor("q-secret"))
	// displayURL passed in must already be pre-auth, but even if a secret
	// somehow ended up in the message text, the Redactor must still catch it.
	result := mapTransportError(spec, errors.New("boom"), "https://api.example.com/x?api_key=q-secret")
	eb := result.StructuredContent.(errBody)
	if strings.Contains(eb.Message, "q-secret") {
		t.Errorf("secret leaked through error message: %q", eb.Message)
	}
}

func TestMapUpstreamResponse3xxIsErrorAndIncludesLocation(t *testing.T) {
	spec := testSpec(nil, nil)
	result, err := mapUpstreamResponse(spec, fakeResponse(302, "", map[string]string{
		"Location": "https://evil.example.com/steal",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for a 302; following it would leak credentials / hit a different host")
	}
	eb := result.StructuredContent.(errBody)
	if eb.Error != "upstream_error_status" || eb.Status != 302 {
		t.Fatalf("unexpected errBody: %+v", eb)
	}
	if !strings.Contains(eb.Message, "https://evil.example.com/steal") {
		t.Errorf("expected Location in the error message, got %q", eb.Message)
	}
}

func TestMapUpstreamResponse3xxRedactsSecretInLocation(t *testing.T) {
	spec := testSpec(nil, NewRedactor("q-secret"))
	result, err := mapUpstreamResponse(spec, fakeResponse(301, "", map[string]string{
		"Location": "https://api.example.com/x?api_key=q-secret",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	eb := result.StructuredContent.(errBody)
	if strings.Contains(eb.Message, "q-secret") {
		t.Errorf("secret leaked through redirect Location: %q", eb.Message)
	}
}

type errAfterReader struct {
	data []byte
	err  error
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestMapUpstreamResponseIncompleteBodyIsErrorNotSuccess(t *testing.T) {
	spec := testSpec(nil, nil)
	partial := `{"id":"inv_1","totals":`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(&errAfterReader{data: []byte(partial), err: io.ErrUnexpectedEOF}),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
	result, err := mapUpstreamResponse(spec, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected a truncated-by-network-error body to be a tool error, not a successful partial document; got %+v", result.StructuredContent)
	}
	eb := result.StructuredContent.(errBody)
	if eb.Error != "upstream_unreachable" {
		t.Errorf("expected upstream_unreachable, got %q", eb.Error)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if strings.Contains(text, partial) {
		t.Errorf("partial body must not be returned as the tool result: %q", text)
	}
}

func TestMapUpstreamResponseTruncatesOversizedBody(t *testing.T) {
	spec := testSpec(nil, nil)
	big := strings.Repeat("a", defaultMaxResponseBytes+1000)
	result, err := mapUpstreamResponse(spec, fakeResponse(200, big, map[string]string{"Content-Type": "text/plain"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "truncated") {
		t.Errorf("expected a truncation marker in oversized response text")
	}
	if len(text) >= len(big) {
		t.Errorf("expected the returned text to be smaller than the original oversized body")
	}
}

func TestMapUpstreamResponseEmptyBodyWithSelectResolvesToNulls(t *testing.T) {
	tmpl, err := respmap.Compile(map[string]any{"x": "{id}"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	spec := testSpec(tmpl, nil)
	result, err := mapUpstreamResponse(spec, fakeResponse(204, "", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("an empty 2xx body with response.select configured should degrade gracefully, not error")
	}
	mapped := result.StructuredContent.(map[string]any)
	if mapped["x"] != nil {
		t.Errorf("expected nil for unresolved path against an empty response, got %v", mapped["x"])
	}
}

func TestMapUpstreamResponseSelectPreservesLargeIntegers(t *testing.T) {
	tmpl, err := respmap.Compile(map[string]any{"id": "{id}"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	result, err := mapUpstreamResponse(testSpec(tmpl, nil), fakeResponse(200,
		`{"id":9007199254740993}`, map[string]string{"Content-Type": "application/json"}))
	if err != nil || result.IsError {
		t.Fatalf("unexpected failure: %v %+v", err, result)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if text != `{"id":9007199254740993}` {
		t.Errorf("large integer not preserved exactly: %s", text)
	}
}

func TestMapUpstreamResponseSelectRejectsTrailingData(t *testing.T) {
	tmpl, _ := respmap.Compile(map[string]any{"id": "{id}"})
	result, _ := mapUpstreamResponse(testSpec(tmpl, nil), fakeResponse(200,
		`{"id":1} garbage`, map[string]string{"Content-Type": "application/json"}))
	if !result.IsError {
		t.Fatal("expected response_unparseable for trailing data")
	}
}

func TestMapSuccessRawContentTypeIsCaseInsensitive(t *testing.T) {
	result, err := mapUpstreamResponse(testSpec(nil, nil), fakeResponse(200,
		`{"ok":true}`, map[string]string{"Content-Type": "Application/JSON; charset=UTF-8"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Errorf("expected mixed-case JSON content type to yield structured content, got %+v", result.Content)
	}
}
