package upstream

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewToolHandler returns a single generic mcp.ToolHandler closed over spec.
// Every registered tool shares this same function body — only spec differs.
//
// Every EXPECTED failure (bad arguments, unreachable upstream, timeout,
// upstream error status) is returned as a normal, non-error
// *mcp.CallToolResult{IsError: true, ...}, never as a Go error — the go-sdk
// treats a non-nil error return from this low-level ToolHandler as an opaque
// JSON-RPC protocol error, which would hide the failure from the calling
// LLM. A Go error is reserved for a genuine internal bug (guarded against
// below by recovering from any panic).
func NewToolHandler(spec *ToolSpec) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				result = toolError(spec, "internal_error", fmt.Sprintf("panic handling tool call: %v", r))
				err = nil
			}
		}()

		args, decodeErr := decodeArguments(req.Params.Arguments)
		if decodeErr != nil {
			return toolError(spec, "invalid_arguments", fmt.Sprintf("could not parse tool arguments: %v", decodeErr)), nil
		}

		httpReq, displayURL, buildErr := buildRequest(spec, args)
		if buildErr != nil {
			return toolError(spec, "invalid_arguments", buildErr.Error()), nil
		}

		callCtx, cancel := context.WithTimeout(ctx, spec.Upstream.Timeout)
		defer cancel()

		resp, doErr := spec.Upstream.Client.Do(httpReq.WithContext(callCtx))
		if doErr != nil {
			return mapTransportError(spec, doErr, displayURL), nil
		}
		defer resp.Body.Close()

		return mapUpstreamResponse(spec, resp)
	}
}

// decodeArguments unmarshals the raw JSON-RPC arguments into a generic map.
// Absent arguments (a tool called with no parameters at all) decode to an
// empty map rather than an error.
func decodeArguments(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	return args, nil
}
