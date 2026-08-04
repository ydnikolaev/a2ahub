package livee2e

import (
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

func TestStableConformanceLocationAcceptsTheWholeDocumentPointer(t *testing.T) {
	t.Parallel()
	// The exact shape LE-OC-08's known-invalid payload produces: `{}` against a
	// root `required` keyword fails the whole document, so both pointers are
	// RFC 6901's empty root pointer. Pinned here because the live scenario used
	// to read that as "no location" and failed a correct product result.
	root := contract.InstanceViolation{Message: "JSON Schema keyword required rejected the instance"}
	if err := stableConformanceLocation(root); err != nil {
		t.Fatalf("root violation rejected: %v", err)
	}
	nested := contract.InstanceViolation{
		InstancePointer: "/items/0/id", SchemaPointer: "/properties/items/items/properties/id",
		Message: "JSON Schema keyword type rejected the instance",
	}
	if err := stableConformanceLocation(nested); err != nil {
		t.Fatalf("nested violation rejected: %v", err)
	}
	escaped := contract.InstanceViolation{
		InstancePointer: "/a~0b/c~1d", SchemaPointer: "/properties/a~0b", Message: "escaped tokens are well-formed",
	}
	if err := stableConformanceLocation(escaped); err != nil {
		t.Fatalf("escaped violation rejected: %v", err)
	}
}

func TestStableConformanceLocationRefusesUnusableViolations(t *testing.T) {
	t.Parallel()
	for name, violation := range map[string]contract.InstanceViolation{
		"unrooted instance pointer": {InstancePointer: "id", Message: "m"},
		"unrooted schema pointer":   {SchemaPointer: "required", Message: "m"},
		"unescaped tilde":           {InstancePointer: "/a~b", Message: "m"},
		"trailing tilde":            {SchemaPointer: "/properties/a~", Message: "m"},
		"no message":                {InstancePointer: "/id", SchemaPointer: "/properties/id"},
	} {
		if err := stableConformanceLocation(violation); !errors.Is(err, ErrConformanceLocationUnstable) {
			t.Fatalf("%s: error = %v, want %v", name, err, ErrConformanceLocationUnstable)
		}
	}
}
