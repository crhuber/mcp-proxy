package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-proxy/internal/respmap"
)

// defaultMaxResponseBytes bounds how much of an upstream response body the
// proxy will ever read into memory / return to the model.
const defaultMaxResponseBytes = 256 * 1024

// errBody is the StructuredContent shape for every tool-level error result,
// giving callers a stable "kind" field to branch on alongside the
// human-readable message in Content.
type errBody struct {
	Error      string `json:"error"`
	Message    string `json:"message"`
	Tool       string `json:"tool"`
	Upstream   string `json:"upstream"`
	Status     int    `json:"status,omitempty"`
	RetryAfter string `json:"retryAfter,omitempty"`
}

func toolError(spec *ToolSpec, kind, msg string) *mcp.CallToolResult {
	msg = spec.Upstream.Redactor.Redact(msg)
	eb := errBody{Error: kind, Message: msg, Tool: spec.Name, Upstream: spec.Upstream.Name}
	return &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: msg}},
		StructuredContent: eb,
	}
}

// mapTransportError classifies a network-level failure (DNS, connection
// refused, TLS handshake, timeout) from http.Client.Do. It never echoes
// err.Error() directly: for query-auth upstreams, Go's own *url.Error text
// embeds the request URL — which, unlike displayURL, has the real secret
// attached — so only displayURL (captured pre-auth) is used here.
func mapTransportError(spec *ToolSpec, err error, displayURL string) *mcp.CallToolResult {
	if errors.Is(err, context.DeadlineExceeded) {
		return toolError(spec, "upstream_timeout",
			fmt.Sprintf("calling upstream %q timed out after %s (%s)", spec.Upstream.Name, spec.Upstream.Timeout, displayURL))
	}
	reason := "network error connecting to upstream"
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		reason = "could not resolve upstream host"
	}
	return toolError(spec, "upstream_unreachable",
		fmt.Sprintf("%s %q (%s)", reason, spec.Upstream.Name, displayURL))
}

// mapUpstreamResponse maps a completed HTTP response into a CallToolResult.
// Error-status responses (4xx/5xx) are NEVER shaped by response.select —
// debugging a failure needs more information, not less — and are always
// shown raw (truncated).
func mapUpstreamResponse(spec *ToolSpec, resp *http.Response) (*mcp.CallToolResult, error) {
	body, truncated, totalRead := readCapped(resp.Body, defaultMaxResponseBytes)

	if resp.StatusCode >= 400 {
		snippet := string(body)
		if truncated {
			snippet += fmt.Sprintf(" [...truncated: response exceeded %d bytes, showing first %d...]", defaultMaxResponseBytes, len(body))
		}
		msg := spec.Upstream.Redactor.Redact(
			fmt.Sprintf("upstream %q returned HTTP %d: %s", spec.Upstream.Name, resp.StatusCode, snippet))
		eb := errBody{
			Error:    "upstream_error_status",
			Message:  msg,
			Tool:     spec.Name,
			Upstream: spec.Upstream.Name,
			Status:   resp.StatusCode,
		}
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			eb.RetryAfter = ra
		}
		return &mcp.CallToolResult{
			IsError:           true,
			Content:           []mcp.Content{&mcp.TextContent{Text: msg}},
			StructuredContent: eb,
		}, nil
	}

	if spec.Response != nil {
		var parsed any
		if len(body) > 0 {
			if err := json.Unmarshal(body, &parsed); err != nil {
				return toolError(spec, "response_unparseable",
					fmt.Sprintf("upstream %q returned a body that could not be parsed as JSON for response field-selection: %v", spec.Upstream.Name, err)), nil
			}
		}
		mapped := respmap.Render(spec.Response, parsed)
		b, err := json.Marshal(mapped)
		if err != nil {
			return toolError(spec, "response_unparseable", fmt.Sprintf("could not encode mapped response: %v", err)), nil
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
			StructuredContent: mapped,
		}, nil
	}

	return mapSuccessRaw(spec, resp, body, truncated, totalRead), nil
}

// mapSuccessRaw is the fallback used when a tool has no response.select
// configured: pass the upstream body through, with a size cap.
func mapSuccessRaw(spec *ToolSpec, resp *http.Response, body []byte, truncated bool, totalRead int) *mcp.CallToolResult {
	ct := resp.Header.Get("Content-Type")

	switch {
	case len(body) == 0:
		text := fmt.Sprintf("(upstream returned HTTP %d with an empty body)", resp.StatusCode)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
	case isJSONContentType(ct):
		text := appendTruncationNote(string(body), truncated, len(body))
		result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
		if !truncated && json.Valid(body) {
			result.StructuredContent = json.RawMessage(body)
		}
		return result
	case isTextContentType(ct):
		text := appendTruncationNote(string(body), truncated, len(body))
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
	default:
		text := fmt.Sprintf(
			"upstream %q returned binary content (content-type %q, at least %d bytes); this proxy does not return binary content to the model in v1",
			spec.Upstream.Name, ct, totalRead)
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: text}},
			StructuredContent: map[string]any{"contentType": ct, "bytes": totalRead, "supported": false},
		}
	}
}

func appendTruncationNote(text string, truncated bool, shownBytes int) string {
	if !truncated {
		return text
	}
	return text + fmt.Sprintf("\n\n[...truncated: response exceeded %d bytes, showing first %d...]", defaultMaxResponseBytes, shownBytes)
}

func isJSONContentType(ct string) bool {
	return strings.Contains(ct, "json")
}

func isTextContentType(ct string) bool {
	return ct == "" || strings.HasPrefix(ct, "text/")
}

// readCapped reads at most max bytes from r (plus one byte to detect
// truncation without buffering an unbounded body). totalRead reports how
// many bytes were actually read (which, when truncated, only tells you the
// body was AT LEAST that large, not its true total size).
func readCapped(r io.Reader, max int) (data []byte, truncated bool, totalRead int) {
	limited := io.LimitReader(r, int64(max)+1)
	data, _ = io.ReadAll(limited)
	totalRead = len(data)
	if len(data) > max {
		truncated = true
		data = data[:max]
	}
	return data, truncated, totalRead
}
