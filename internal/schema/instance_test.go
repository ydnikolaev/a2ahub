package schema

import (
	"reflect"
	"testing"
)

func TestDecodeYAMLInstance(t *testing.T) {
	t.Parallel()
	raw := []byte(`
str: hello
date: 2026-08-20
count: 3
ratio: 1.5
flag: true
nothing: null
list: [a, b]
nested:
  inner: x
`)
	got, err := DecodeYAMLInstance(raw)
	if err != nil {
		t.Fatalf("DecodeYAMLInstance: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected a map, got %T", got)
	}

	// The critical assertion: a bare date scalar keeps its EXACT authored
	// text ("2026-08-20"), never yaml.v3's implicit !!timestamp
	// resolution reformatted as a date-time string.
	if m["date"] != "2026-08-20" {
		t.Errorf("date = %#v, want the literal string \"2026-08-20\"", m["date"])
	}
	if m["str"] != "hello" {
		t.Errorf("str = %#v", m["str"])
	}
	if m["count"] != int64(3) {
		t.Errorf("count = %#v, want int64(3)", m["count"])
	}
	if m["ratio"] != 1.5 {
		t.Errorf("ratio = %#v, want 1.5", m["ratio"])
	}
	if m["flag"] != true {
		t.Errorf("flag = %#v, want true", m["flag"])
	}
	if m["nothing"] != nil {
		t.Errorf("nothing = %#v, want nil", m["nothing"])
	}
	if !reflect.DeepEqual(m["list"], []any{"a", "b"}) {
		t.Errorf("list = %#v", m["list"])
	}
	nested, ok := m["nested"].(map[string]any)
	if !ok || nested["inner"] != "x" {
		t.Errorf("nested = %#v", m["nested"])
	}
}

func TestDecodeYAMLInstance_Empty(t *testing.T) {
	t.Parallel()
	got, err := DecodeYAMLInstance([]byte(""))
	if err != nil {
		t.Fatalf("DecodeYAMLInstance: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty input, got %#v", got)
	}
}
