package space_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// writeProvides lays out a mirror carrying one directory per name under
// <system>/provides/, each with a contract descriptor unless withDescriptor
// says otherwise.
func writeProvides(t *testing.T, system string, slugs map[string]bool) string {
	t.Helper()
	mirror := t.TempDir()
	layout, err := space.NewLayout(system)
	if err != nil {
		t.Fatalf("NewLayout(%q): %v", system, err)
	}
	for slug, withDescriptor := range slugs {
		dir := filepath.Join(mirror, system, "provides", slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if !withDescriptor {
			continue
		}
		descriptor := filepath.Join(mirror, layout.ProvidesContract(slug))
		if err := os.MkdirAll(filepath.Dir(descriptor), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(descriptor, []byte("---\nid: x\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return mirror
}

func TestProvidedContractIDsIsSortedAndSkipsDirectoriesWithNoDescriptor(t *testing.T) {
	t.Parallel()

	// "halfwritten" is the case the convention exists for: a provides/<slug>/
	// directory with no descriptor is NOT a provided contract, and must not
	// appear in a report that claims to name everything this system
	// publishes.
	mirror := writeProvides(t, "beta", map[string]bool{
		"zulu":        true,
		"alpha":       true,
		"halfwritten": false,
	})

	ids, err := space.ProvidedContractIDs("beta", mirror)
	if err != nil {
		t.Fatalf("ProvidedContractIDs: %v", err)
	}
	want := []string{"XC-beta-alpha", "XC-beta-zulu"}
	if !slices.Equal(ids, want) {
		t.Fatalf("ProvidedContractIDs = %v, want %v (sorted, descriptor-less slug excluded)", ids, want)
	}
}

// TestProvidedContractIDsAbsentTreeIsNotAnError is the asymmetry `a2a contract
// verify-published` rests on: a system that has published nothing is a
// legitimate state, and a caller that cannot tell it from a failed walk is the
// exact confusion the verb exists to remove.
func TestProvidedContractIDsAbsentTreeIsNotAnError(t *testing.T) {
	t.Parallel()

	ids, err := space.ProvidedContractIDs("beta", t.TempDir())
	if err != nil {
		t.Fatalf("an absent provides/ tree must not be an error, got %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ProvidedContractIDs = %v, want none", ids)
	}
}

func TestProvidedContractIDsRefusesAnUnusableSystem(t *testing.T) {
	t.Parallel()

	if _, err := space.ProvidedContractIDs("", t.TempDir()); err == nil {
		t.Fatal("an empty system must refuse — NewLayout owns that rule and it must not be swallowed here")
	}
}

// TestProvidedContractIDsIgnoresPlainFiles guards the one branch a slug-shaped
// FILE would otherwise slip through.
func TestProvidedContractIDsIgnoresPlainFiles(t *testing.T) {
	t.Parallel()

	mirror := writeProvides(t, "beta", map[string]bool{"real": true})
	stray := filepath.Join(mirror, "beta", "provides", "README.md")
	if err := os.WriteFile(stray, []byte("not a contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ids, err := space.ProvidedContractIDs("beta", mirror)
	if err != nil {
		t.Fatalf("ProvidedContractIDs: %v", err)
	}
	if !slices.Equal(ids, []string{"XC-beta-real"}) {
		t.Fatalf("ProvidedContractIDs = %v, want only XC-beta-real", ids)
	}
}
