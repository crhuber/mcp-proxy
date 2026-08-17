package server

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-proxy/internal/toolgen"
	"mcp-proxy/internal/upstream"
)

func genTool(name string) toolgen.GeneratedTool {
	return toolgen.GeneratedTool{
		MCPTool: &mcp.Tool{
			Name:        name,
			Description: "test tool",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		UpstreamName: "up",
		BaseURL:      "https://api.example.com",
		Method:       "GET",
		PathTemplate: "/x",
	}
}

func TestBuildRejectsDuplicateToolNames(t *testing.T) {
	tools := []toolgen.GeneratedTool{genTool("up_getX"), genTool("up_getX")}
	_, err := Build(tools, upstream.NewClient(), nil, "test")
	if err == nil {
		t.Fatalf("expected an error for duplicate tool names")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected a duplicate-related error message, got %q", err.Error())
	}
}

func TestBuildRegistersDistinctTools(t *testing.T) {
	tools := []toolgen.GeneratedTool{genTool("up_getX"), genTool("up_getY")}
	mcpServer, err := Build(tools, upstream.NewClient(), nil, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mcpServer == nil {
		t.Fatalf("expected a non-nil server")
	}
}

func TestBuildHandlerServesHealthzUnauthenticated(t *testing.T) {
	tools := []toolgen.GeneratedTool{genTool("up_getX")}
	mcpServer, err := Build(tools, upstream.NewClient(), nil, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	handler := BuildHandler(mcpServer, nil, false)
	if handler == nil {
		t.Fatalf("expected a non-nil handler")
	}
}
