package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func dummyHandler(_ context.Context, _ json.RawMessage) (any, string, error) {
	return "ok", "", nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(ToolSpec{Name: "a2a_test", Description: "test tool", Handler: dummyHandler})

	spec, ok := r.Get("a2a_test")
	if !ok {
		t.Fatalf("expected tool to be registered")
	}
	if spec.Description != "test tool" {
		t.Errorf("description = %q", spec.Description)
	}
	if _, ok := r.Get("missing"); ok {
		t.Errorf("expected missing tool to be absent")
	}
}

func TestRegistryToolNamesSorted(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(ToolSpec{Name: "a2a_zeta", Handler: dummyHandler})
	r.Register(ToolSpec{Name: "a2a_alpha", Handler: dummyHandler})
	r.Register(ToolSpec{Name: "a2a_mu", Handler: dummyHandler})

	names := r.ToolNames()
	want := []string{"a2a_alpha", "a2a_mu", "a2a_zeta"}
	if len(names) != len(want) {
		t.Fatalf("ToolNames() = %v, want %v", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("ToolNames()[%d] = %q, want %q", i, n, want[i])
		}
	}
}

func TestRegistryListMatchesToolNames(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(ToolSpec{Name: "a2a_b", Handler: dummyHandler})
	r.Register(ToolSpec{Name: "a2a_a", Handler: dummyHandler})

	list := r.List()
	names := r.ToolNames()
	if len(list) != len(names) {
		t.Fatalf("List() len = %d, ToolNames() len = %d", len(list), len(names))
	}
	for i, spec := range list {
		if spec.Name != names[i] {
			t.Errorf("List()[%d].Name = %q, want %q", i, spec.Name, names[i])
		}
	}
}

func TestRegistryRegisterPanicsOnEmptyName(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on empty tool name")
		}
	}()
	NewRegistry().Register(ToolSpec{Handler: dummyHandler})
}

func TestRegistryRegisterPanicsOnNilHandler(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil handler")
		}
	}()
	NewRegistry().Register(ToolSpec{Name: "a2a_x"})
}

func TestRegistryRegisterPanicsOnDuplicate(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(ToolSpec{Name: "a2a_dup", Handler: dummyHandler})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate tool name")
		}
	}()
	r.Register(ToolSpec{Name: "a2a_dup", Handler: dummyHandler})
}
