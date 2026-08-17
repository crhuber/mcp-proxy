package toolgen

// emptyObjectSchema is used for a tool whose config omits `parameters`
// entirely (a tool that takes no arguments).
func emptyObjectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// deriveParams walks the top-level properties of a tool's `parameters`
// schema, extracting those tagged with `in: path|query|header` into
// ParamDef routing entries. It also returns the full set of declared
// top-level property names, which bodytmpl.Compile uses to distinguish a
// valid {paramName} reference from an undeclared one.
func deriveParams(schema map[string]any) (params []ParamDef, knownNames map[string]bool) {
	knownNames = map[string]bool{}
	props, _ := schema["properties"].(map[string]any)
	required := toStringSet(schema["required"])
	for name, raw := range props {
		knownNames[name] = true
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		in, tagged := prop["in"].(string)
		if !tagged {
			continue
		}
		params = append(params, ParamDef{Name: name, Location: in, Required: required[name]})
	}
	return params, knownNames
}

// buildInputSchema returns the LLM-facing schema: a deep copy of the
// authored `parameters` block with the internal `in` routing key stripped
// from every top-level property, so the model never sees our routing
// metadata.
func buildInputSchema(schema map[string]any) map[string]any {
	cleaned := deepCopyMap(schema)
	if props, ok := cleaned["properties"].(map[string]any); ok {
		for _, raw := range props {
			if prop, ok := raw.(map[string]any); ok {
				delete(prop, "in")
			}
		}
	}
	return cleaned
}

func toStringSet(v any) map[string]bool {
	set := map[string]bool{}
	arr, _ := v.([]any)
	for _, item := range arr {
		if s, ok := item.(string); ok {
			set[s] = true
		}
	}
	return set
}

func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = deepCopyValue(item)
		}
		return out
	default:
		return v
	}
}
