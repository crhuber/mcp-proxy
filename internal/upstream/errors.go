package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
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
// Redirect (3xx) and error-status (4xx/5xx) responses are NEVER shaped by
// response.select — debugging a failure needs more information, not less —
// and are always shown raw (truncated). 3xx is treated as an error because
// the HTTP client does not follow redirects (see NewClient).
func mapUpstreamResponse(spec *ToolSpec, resp *http.Response) (*mcp.CallToolResult, error) {
	body, truncated, totalRead, err := readCapped(resp.Body, defaultMaxResponseBytes)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return toolError(spec, "upstream_timeout",
				fmt.Sprintf("reading the response from upstream %q timed out after %s", spec.Upstream.Name, spec.Upstream.Timeout)), nil
		}
		return toolError(spec, "upstream_unreachable",
			fmt.Sprintf("failed to read the full response from upstream %q", spec.Upstream.Name)), nil
	}

	if resp.StatusCode >= 300 {
		snippet := string(body)
		if truncated {
			snippet += fmt.Sprintf(" [...truncated: response exceeded %d bytes, showing first %d...]", defaultMaxResponseBytes, len(body))
		}
		msg := fmt.Sprintf("upstream %q returned HTTP %d: %s", spec.Upstream.Name, resp.StatusCode, snippet)
		if loc := resp.Header.Get("Location"); loc != "" {
			msg = fmt.Sprintf("upstream %q returned HTTP %d (Location: %s): %s", spec.Upstream.Name, resp.StatusCode, loc, snippet)
		}
		msg = spec.Upstream.Redactor.Redact(msg)
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
			if err := decodeJSONNumbers(body, &parsed); err != nil {
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

// decodeJSONNumbers is json.Unmarshal with UseNumber, so numbers survive a
// decode/re-encode round trip exactly (a 64-bit ID isn't rounded via
// float64). Like Unmarshal, trailing data after the value is an error.
func decodeJSONNumbers(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid character after top-level value")
	}
	return nil
}

// mediaType returns ct's lowercased media type without parameters; media
// types are case-insensitive.
func mediaType(ct string) string {
	if mt, _, err := mime.ParseMediaType(ct); err == nil {
		return mt
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

func isJSONContentType(ct string) bool {
	return strings.Contains(mediaType(ct), "json")
}

func isTextContentType(ct string) bool {
	mt := mediaType(ct)
	return mt == "" || strings.HasPrefix(mt, "text/")
}

// readCapped reads at most max bytes from r (plus one byte to detect
// truncation without buffering an unbounded body). totalRead reports how
// many bytes were actually read (which, when truncated, only tells you the
// body was AT LEAST that large, not its true total size).
//
// A non-nil error means the body could not be read in full (connection
// reset, timeout, etc.). Callers must not treat a partial body as a
// successful upstream response: unlike the size cap, there is no
// truncation marker, and the bytes may be an incomplete JSON document.
func readCapped(r io.Reader, max int) (data []byte, truncated bool, totalRead int, err error) {
	limited := io.LimitReader(r, int64(max)+1)
	data, err = io.ReadAll(limited)
	totalRead = len(data)
	if err != nil {
		return data, false, totalRead, err
	}
	if len(data) > max {
		truncated = true
		data = data[:max]
	}
	return data, truncated, totalRead, nil
}
