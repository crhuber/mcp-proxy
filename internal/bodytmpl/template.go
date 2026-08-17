// Package bodytmpl compiles and renders httpRequestBody templates: literal JSON
// values where a string of the form "{paramName}" is a reference to a tool
// parameter, substituted with that parameter's runtime argument value.
package bodytmpl

import (
	"fmt"
	"regexp"
)

// Template is a compiled body template: a tree of map[string]any / []any /
// paramRef / literal (string, number, bool, nil), produced once at boot.
type Template any

type paramRef struct{ Name string }

var refPattern = regexp.MustCompile(`^\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
var escapedLiteral = regexp.MustCompile(`^\{\{(.*)\}\}$`)

// Compile walks a raw httpRequestBody map (from YAML) turning every "{name}"
// string into a paramRef (erroring if name isn't in knownParams), "{{...}}"
// into a literal with one layer of braces restored, and everything else
// into a literal as-is. It also returns the set of parameter names actually
// referenced, so config.Validate can enforce "every declared parameter is
// used somewhere."
func Compile(raw map[string]any, knownParams map[string]bool) (tmpl Template, used map[string]bool, err error) {
	used = map[string]bool{}
	tmpl, err = compileValue(raw, knownParams, used)
	return tmpl, used, err
}

func compileValue(raw any, knownParams map[string]bool, used map[string]bool) (Template, error) {
	switch v := raw.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range v {
			c, err := compileValue(val, knownParams, used)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = c
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			c, err := compileValue(val, knownParams, used)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case string:
		if m := escapedLiteral.FindStringSubmatch(v); m != nil {
			return "{" + m[1] + "}", nil // "{{invoiceId}}" -> literal "{invoiceId}"
		}
		if m := refPattern.FindStringSubmatch(v); m != nil {
			name := m[1]
			if !knownParams[name] {
				return nil, fmt.Errorf("httpRequestBody references undeclared parameter %q", v)
			}
			used[name] = true
			return paramRef{Name: name}, nil
		}
		return v, nil // literal (any string not of the form "{name}")
	default:
		return v, nil // literal number/bool/nil
	}
}

// Render substitutes runtime argument values into a compiled Template. A
// paramRef whose argument was not supplied by the caller is DROPPED from its
// parent object (not sent as null) — keeps optional fields cleanly absent
// from the upstream request rather than sending an explicit null.
func Render(tmpl Template, args map[string]any) any {
	v, _ := renderValue(tmpl, args)
	return v
}

func renderValue(tmpl Template, args map[string]any) (any, bool) {
	switch v := tmpl.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range v {
			if rendered, ok := renderValue(val, args); ok {
				out[k] = rendered
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(v))
		for _, val := range v {
			if rendered, ok := renderValue(val, args); ok {
				out = append(out, rendered)
			}
		}
		return out, true
	case paramRef:
		val, present := args[v.Name]
		return val, present
	default:
		return v, true
	}
}
