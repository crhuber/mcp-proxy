package bodytmpl

import (
	"reflect"
	"testing"
)

func mustCompile(t *testing.T, raw map[string]any, known map[string]bool) (Template, map[string]bool) {
	t.Helper()
	tmpl, used, err := Compile(raw, known)
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}
	return tmpl, used
}

func TestCompileReference(t *testing.T) {
	known := map[string]bool{"customerId": true}
	tmpl, used := mustCompile(t, map[string]any{"id": "{customerId}"}, known)
	if !used["customerId"] {
		t.Fatal("expected customerId to be marked used")
	}
	rendered := Render(tmpl, map[string]any{"customerId": "cus_123"})
	want := map[string]any{"id": "cus_123"}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("got %#v, want %#v", rendered, want)
	}
}

func TestCompileUndeclaredReferenceErrors(t *testing.T) {
	_, _, err := Compile(map[string]any{"id": "{nope}"}, map[string]bool{"customerId": true})
	if err == nil {
		t.Fatal("expected error for undeclared reference")
	}
}

func TestCompileEscapedLiteral(t *testing.T) {
	tmpl, used := mustCompile(t, map[string]any{"note": "{{customerId}}"}, map[string]bool{"customerId": true})
	if len(used) != 0 {
		t.Fatalf("escaped literal should not be marked used, got %#v", used)
	}
	rendered := Render(tmpl, map[string]any{})
	want := map[string]any{"note": "{customerId}"}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("got %#v, want %#v", rendered, want)
	}
}

func TestCompilePlainLiteral(t *testing.T) {
	tmpl, _ := mustCompile(t, map[string]any{"source": "mcp-proxy"}, map[string]bool{})
	rendered := Render(tmpl, map[string]any{})
	want := map[string]any{"source": "mcp-proxy"}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("got %#v, want %#v", rendered, want)
	}
}

func TestCompileNestedAndRenaming(t *testing.T) {
	known := map[string]bool{"lineItems": true}
	raw := map[string]any{
		"items": "{lineItems}",
		"meta":  map[string]any{"nested": "{lineItems}"},
		"list":  []any{"{lineItems}", "literal"},
	}
	tmpl, used := mustCompile(t, raw, known)
	if !used["lineItems"] {
		t.Fatal("expected lineItems to be used")
	}
	args := map[string]any{"lineItems": []any{"a", "b"}}
	rendered := Render(tmpl, args)
	want := map[string]any{
		"items": []any{"a", "b"},
		"meta":  map[string]any{"nested": []any{"a", "b"}},
		"list":  []any{[]any{"a", "b"}, "literal"},
	}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("got %#v, want %#v", rendered, want)
	}
}

func TestRenderOmitsMissingOptionalReference(t *testing.T) {
	tmpl, _ := mustCompile(t, map[string]any{"currency": "{currency}", "customerId": "{customerId}"}, map[string]bool{"currency": true, "customerId": true})
	rendered := Render(tmpl, map[string]any{"customerId": "cus_1"})
	want := map[string]any{"customerId": "cus_1"}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("got %#v, want %#v (currency should be omitted, not null)", rendered, want)
	}
}
