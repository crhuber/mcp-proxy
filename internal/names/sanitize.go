// Package names derives the final, SDK-safe MCP tool name for a config tool
// entry by combining its upstream name and tool name.
package names

import (
	"fmt"
	"regexp"
)

var disallowed = regexp.MustCompile(`[^A-Za-z0-9_.\-]+`)

// MaxToolNameLength mirrors the MCP go-sdk's tool name length limit.
const MaxToolNameLength = 128

// DeriveToolName combines an upstream name and a tool name into the final
// name registered with the MCP server, sanitizing any character outside the
// SDK's allowed charset ([A-Za-z0-9_.-]) to an underscore.
func DeriveToolName(upstreamName, toolName string) (string, error) {
	raw := upstreamName + "_" + toolName
	sanitized := disallowed.ReplaceAllString(raw, "_")
	if sanitized == "" {
		return "", fmt.Errorf("tool name %q sanitizes to an empty string", raw)
	}
	if len(sanitized) > MaxToolNameLength {
		return "", fmt.Errorf("tool name %q is %d characters after prefixing; the MCP SDK's max is %d", raw, len(sanitized), MaxToolNameLength)
	}
	return sanitized, nil
}
