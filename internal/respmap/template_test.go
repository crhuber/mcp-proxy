package respmap

import (
	"reflect"
	"testing"
)

func mustCompile(t *testing.T, raw any) Template {
	t.Helper()
	tmpl, err := Compile(raw)
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}
	return tmpl
}

func TestCompileAndRenderSimplePath(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"invoiceId": "{id}", "customer": "{customer.name}"})
	response := map[string]any{"id": "inv_1", "customer": map[string]any{"name": "Acme"}}
	got := Render(tmpl, response)
	want := map[string]any{"invoiceId": "inv_1", "customer": "Acme"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestUnresolvablePathYieldsNil(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"missing": "{a.b.c}"})
	got := Render(tmpl, map[string]any{"a": map[string]any{}})
	want := map[string]any{"missing": nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestArraySingleFieldExtraction(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"orderIds": "{orders[].id}"})
	response := map[string]any{
		"orders": []any{
			map[string]any{"id": "o1"},
			map[string]any{"id": "o2"},
		},
	}
	got := Render(tmpl, response)
	want := map[string]any{"orderIds": []any{"o1", "o2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestBareArrayPassthrough(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"tips": "{tips[]}"})
	response := map[string]any{"tips": []any{"a", "b"}}
	got := Render(tmpl, response)
	want := map[string]any{"tips": []any{"a", "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestArraySegmentOnNonArrayYieldsNil(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"x": "{orders[].id}"})
	got := Render(tmpl, map[string]any{"orders": "not-an-array"})
	want := map[string]any{"x": nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTruncateFunction(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"notes": "{truncate(notes, 5)}"})
	got := Render(tmpl, map[string]any{"notes": "hello world"})
	want := map[string]any{"notes": "hello...[truncated]"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTruncateNoOpOnShortString(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"notes": "{truncate(notes, 50)}"})
	got := Render(tmpl, map[string]any{"notes": "short"})
	want := map[string]any{"notes": "short"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTruncateOverArray(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"descs": "{truncate(orders[].description, 3)}"})
	response := map[string]any{
		"orders": []any{
			map[string]any{"description": "hello"},
			map[string]any{"description": "hi"},
		},
	}
	got := Render(tmpl, response)
	want := map[string]any{"descs": []any{"hel...[truncated]", "hi"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTruncateNoOpOnNonString(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"x": "{truncate(count, 3)}"})
	got := Render(tmpl, map[string]any{"count": 42})
	want := map[string]any{"x": 42}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestCompileRejectsUnknownFunction(t *testing.T) {
	_, err := Compile(map[string]any{"x": "{frobnicate(a, 1)}"})
	if err == nil {
		t.Fatal("expected error for unknown function")
	}
}

func TestCompileRejectsWrongArgCount(t *testing.T) {
	_, err := Compile(map[string]any{"x": "{truncate(a)}"})
	if err == nil {
		t.Fatal("expected error for wrong arg count")
	}
}

func TestCompileRejectsNonIntegerMaxLen(t *testing.T) {
	_, err := Compile(map[string]any{"x": "{truncate(a, notanumber)}"})
	if err == nil {
		t.Fatal("expected error for non-integer maxLen")
	}
}

func TestEscapedLiteral(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"note": "{{id}}"})
	got := Render(tmpl, map[string]any{"id": "should-not-be-used"})
	want := map[string]any{"note": "{id}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestPlainLiteralPassthrough(t *testing.T) {
	tmpl := mustCompile(t, map[string]any{"source": "static-value"})
	got := Render(tmpl, map[string]any{})
	want := map[string]any{"source": "static-value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestBareArrayRootTemplate(t *testing.T) {
	tmpl := mustCompile(t, "{orders[].id}")
	response := map[string]any{"orders": []any{map[string]any{"id": "o1"}}}
	got := Render(tmpl, response)
	want := []any{"o1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
