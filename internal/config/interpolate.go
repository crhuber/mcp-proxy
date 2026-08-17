package config

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
)

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// interpolateEnv rewrites every plain string field reachable from cfg,
// substituting ${ENV_VAR} references with the corresponding environment
// variable's value. It deliberately does NOT recurse into map[string]any or
// `any`-typed fields (Parameters, HTTP.Body, HTTP.Response.Select) — those use
// their own distinct {name}/{dot.path} template syntax and must be left
// untouched here.
func interpolateEnv(cfg *Config) error {
	var missing []string
	walkStrings(reflect.ValueOf(cfg), func(s string) string {
		return envRef.ReplaceAllStringFunc(s, func(m string) string {
			name := envRef.FindStringSubmatch(m)[1]
			if v, ok := os.LookupEnv(name); ok {
				return v
			}
			missing = append(missing, name)
			return m
		})
	})
	if len(missing) > 0 {
		return fmt.Errorf("undefined environment variable(s) referenced in config: %s", strings.Join(dedupe(missing), ", "))
	}
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// walkStrings recursively visits addressable string fields reachable from v,
// replacing each with transform(s). Only structs, slices/arrays, and
// pointers are traversed; maps and interface-typed (`any`) fields are left
// alone on purpose.
func walkStrings(v reflect.Value, transform func(string) string) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			walkStrings(v.Elem(), transform)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			walkStrings(v.Field(i), transform)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkStrings(v.Index(i), transform)
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString(transform(v.String()))
		}
	default:
		// map[string]any, any (interface{}), etc. — intentionally skipped.
	}
}
