// Package respmap compiles and renders response.select templates: literal
// JSON values where a string like "{dot.path}" extracts a field from the
// upstream's parsed JSON response, "{arrayField[].path}" maps a sub-path
// over every element of an array, and "{truncate(path, maxLen)}" truncates a
// resolved string (or every string element of a resolved array).
package respmap

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Template is a compiled response template: a tree of map[string]any /
// []any / literal / pathRef / funcCall, produced once at boot.
type Template any

// pathStep is one "."-separated segment of a path. Array==true means the
// segment was written "key[]" — the value at that key is expected to be a
// JSON array, and the REMAINING steps are resolved independently against
// each element, producing a new array of results (one per element).
type pathStep struct {
	Key   string
	Array bool
}

type pathRef struct{ Steps []pathStep }
type funcCall struct {
	Func   string
	Steps  []pathStep
	MaxLen int // only meaningful for "truncate"
}

// Each "."-separated token may end in "[]"; the whole expression is wrapped
// in braces — the same style as the path template's "{invoiceId}" and
// httpRequestBody's "{paramName}". Examples: {id}, {customer.name},
// {orders[].id} (single-field extraction per element), {tips[]} (bare array
// passthrough), {truncate(notes, 500)} (a function call).
var pathPattern = regexp.MustCompile(`^\{((?:[A-Za-z_][A-Za-z0-9_]*(?:\[\])?)(?:\.[A-Za-z_][A-Za-z0-9_]*(?:\[\])?)*)\}$`)
var funcPattern = regexp.MustCompile(`^\{([A-Za-z_][A-Za-z0-9_]*)\(([^()]*)\)\}$`)
var escapedLiteral = regexp.MustCompile(`^\{\{(.*)\}\}$`)

func parsePath(raw string) []pathStep {
	tokens := strings.Split(raw, ".")
	steps := make([]pathStep, len(tokens))
	for i, tok := range tokens {
		if key, ok := strings.CutSuffix(tok, "[]"); ok {
			steps[i] = pathStep{Key: key, Array: true}
		} else {
			steps[i] = pathStep{Key: tok}
		}
	}
	return steps
}

// Compile only validates syntax (well-formed {path} / {func(...)}
// expressions, known function names, correct arg shapes) — it CANNOT
// validate that a path will actually resolve, or that a "key[]" segment
// will actually find an array at runtime, since that depends on the
// upstream's runtime response shape, which isn't known at boot. The root of
// `raw` may be an object, an array, or (rarely) a bare string.
func Compile(raw any) (Template, error) { return compileValue(raw) }

func compileValue(raw any) (Template, error) {
	switch v := raw.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range v {
			c, err := compileValue(val)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = c
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			c, err := compileValue(val)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case string:
		if m := escapedLiteral.FindStringSubmatch(v); m != nil {
			return "{" + m[1] + "}", nil // "{{...}}" -> literal "{...}"
		}
		if m := funcPattern.FindStringSubmatch(v); m != nil {
			fn, rawArgs := m[1], m[2]
			args := splitArgs(rawArgs)
			switch fn {
			case "truncate":
				if len(args) != 2 {
					return nil, fmt.Errorf("truncate expects (path, maxLen), got %d args in %q", len(args), v)
				}
				n, err := strconv.Atoi(args[1])
				if err != nil || n <= 0 {
					return nil, fmt.Errorf("truncate's second arg must be a positive integer in %q", v)
				}
				return funcCall{Func: "truncate", Steps: parsePath(args[0]), MaxLen: n}, nil
			default:
				return nil, fmt.Errorf("unknown response function %q in %q (supported: truncate)", fn, v)
			}
		}
		if m := pathPattern.FindStringSubmatch(v); m != nil {
			return pathRef{Steps: parsePath(m[1])}, nil
		}
		return v, nil // literal
	default:
		return v, nil
	}
}

func splitArgs(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// Render resolves a compiled Template against the upstream's parsed JSON
// response. Every path/function reference resolves from the RESPONSE ROOT,
// regardless of how deeply it's nested in the output template — only the
// output shape nests, not the input path. A path that doesn't resolve
// (missing key, wrong type at any segment, or a non-array where "[]" was
// expected) yields nil for that field, rather than failing the whole call —
// the tool result still comes back, just with that one field null.
func Render(tmpl Template, response any) any {
	switch v := tmpl.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range v {
			out[k] = Render(val, response)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = Render(val, response)
		}
		return out
	case pathRef:
		return resolveSteps(response, v.Steps)
	case funcCall:
		return applyTruncate(resolveSteps(response, v.Steps), v.MaxLen)
	default:
		return v
	}
}

// resolveSteps walks path steps against v. Hitting an Array step requires
// the value at that key to be a []any; the rest of the steps are then
// resolved independently against each element, producing a same-length
// []any of results (each possibly nil if that element's sub-path misses).
func resolveSteps(v any, steps []pathStep) any {
	if len(steps) == 0 {
		return v
	}
	step := steps[0]
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	val, present := m[step.Key]
	if !present {
		return nil
	}
	if !step.Array {
		return resolveSteps(val, steps[1:])
	}
	arr, ok := val.([]any)
	if !ok {
		return nil
	}
	out := make([]any, len(arr))
	for i, elem := range arr {
		out[i] = resolveSteps(elem, steps[1:])
	}
	return out
}

// applyTruncate is a no-op on anything but a string or a []any of strings —
// this lets truncate() compose naturally with an array-valued path (e.g.
// {truncate(orders[].description, 100)} truncates every element).
func applyTruncate(val any, maxLen int) any {
	switch v := val.(type) {
	case string:
		return truncateString(v, maxLen)
	case []any:
		out := make([]any, len(v))
		for i, elem := range v {
			if s, ok := elem.(string); ok {
				out[i] = truncateString(s, maxLen)
			} else {
				out[i] = elem
			}
		}
		return out
	default:
		return val
	}
}

func truncateString(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "...[truncated]"
}
