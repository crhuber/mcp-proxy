package config

import (
	"fmt"
	"strings"
)

// ValidationError is one problem found while validating a Config.
type ValidationError struct {
	Path string
	Msg  string
}

// ValidationErrors aggregates every problem found during validation, so a
// user can fix them all in one pass rather than one-at-a-time.
type ValidationErrors []ValidationError

func (v ValidationErrors) Error() string {
	lines := make([]string, len(v))
	for i, e := range v {
		lines[i] = fmt.Sprintf("  - %s: %s", e.Path, e.Msg)
	}
	return fmt.Sprintf("config validation failed with %d error(s):\n%s", len(v), strings.Join(lines, "\n"))
}
