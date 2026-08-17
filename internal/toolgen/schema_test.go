package toolgen

import (
	"reflect"
	"testing"
)

func TestDeriveParams(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"invoiceId"},
		"properties": map[string]any{
			"invoiceId":  map[string]any{"type": "string", "in": "path"},
			"status":     map[string]any{"type": "string", "in": "query"},
			"customerId": map[string]any{"type": "string"}, // untagged -> not routed
		},
	}

	params, known := deriveParams(schema)

	if len(known) != 3 {
		t.Fatalf("expected 3 known names, got %d: %v", len(known), known)
	}
	if !known["invoiceId"] || !known["status"] || !known["customerId"] {
		t.Fatalf("known names missing expected entries: %v", known)
	}

	byName := map[string]ParamDef{}
	for _, p := range params {
		byName[p.Name] = p
	}
	if len(byName) != 2 {
		t.Fatalf("expected 2 routed params, got %d: %v", len(byName), params)
	}
	if byName["invoiceId"].Location != "path" || !byName["invoiceId"].Required {
		t.Errorf("invoiceId param wrong: %+v", byName["invoiceId"])
	}
	if byName["status"].Location != "query" || byName["status"].Required {
		t.Errorf("status param wrong: %+v", byName["status"])
	}
	if _, ok := byName["customerId"]; ok {
		t.Errorf("customerId should not be routed (no \"in\" tag)")
	}
}

func TestBuildInputSchemaStripsInTagWithoutMutatingOriginal(t *testing.T) {
	original := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"invoiceId": map[string]any{"type": "string", "in": "path", "description": "id"},
		},
	}

	cleaned := buildInputSchema(original)

	cleanedProps := cleaned["properties"].(map[string]any)
	cleanedProp := cleanedProps["invoiceId"].(map[string]any)
	if _, ok := cleanedProp["in"]; ok {
		t.Errorf("expected \"in\" to be stripped from the LLM-facing schema, got %v", cleanedProp)
	}
	if cleanedProp["description"] != "id" {
		t.Errorf("expected description to survive stripping, got %v", cleanedProp)
	}

	// Original must be untouched.
	originalProps := original["properties"].(map[string]any)
	originalProp := originalProps["invoiceId"].(map[string]any)
	if _, ok := originalProp["in"]; !ok {
		t.Errorf("buildInputSchema must not mutate the original config map")
	}
}

func TestDeepCopyMapIsIndependent(t *testing.T) {
	original := map[string]any{
		"nested": map[string]any{"a": 1},
		"list":   []any{map[string]any{"b": 2}},
	}
	cp := deepCopyMap(original)

	cp["nested"].(map[string]any)["a"] = 999
	cp["list"].([]any)[0].(map[string]any)["b"] = 999

	if original["nested"].(map[string]any)["a"] != 1 {
		t.Errorf("mutating the copy affected the original nested map")
	}
	if original["list"].([]any)[0].(map[string]any)["b"] != 2 {
		t.Errorf("mutating the copy affected the original nested slice element")
	}
	if !reflect.DeepEqual(cp["nested"], map[string]any{"a": 999}) {
		t.Errorf("copy not mutated as expected")
	}
}

func TestToStringSet(t *testing.T) {
	set := toStringSet([]any{"a", "b", "a"})
	if len(set) != 2 || !set["a"] || !set["b"] {
		t.Errorf("unexpected set: %v", set)
	}
	if toStringSet(nil) == nil {
		t.Errorf("toStringSet(nil) should return an empty, non-nil map")
	}
}
