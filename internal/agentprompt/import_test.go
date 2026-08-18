package agentprompt

import (
	"go/build"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

const repositoryImportPrefix = "github.com/ydnikolaev/a2ahub/"

// TestImportsOnlyFold is spec 02 AC1: internal/agentprompt imports
// internal/fold and no other internal package, so a CI-side caller (a chat
// notifier, later phases) can import this package to compose the same prompt
// the dashboard shows without inverting the ADR-001 boundary the presentation
// package's import set protects.
//
// This asserts DIRECT PRODUCTION imports only (go/build's pkg.Imports, which
// excludes TestImports) — the moved tests import "skill" for the doc-pointer
// gate, and that must never count against the package's own boundary.
func TestImportsOnlyFold(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate agentprompt test source")
	}
	packageDir := filepath.Dir(filename)

	pkg, err := build.Default.ImportDir(packageDir, 0)
	if err != nil {
		t.Fatalf("inspect production imports for internal/agentprompt: %v", err)
	}

	var internal []string
	for _, importPath := range pkg.Imports {
		relative, found := strings.CutPrefix(importPath, repositoryImportPrefix)
		if !found {
			continue
		}
		internal = append(internal, strings.TrimPrefix(relative, "internal/"))
	}
	sort.Strings(internal)

	want := []string{"fold"}
	if !slices.Equal(internal, want) {
		t.Errorf("internal/agentprompt production imports = %v, want exactly %v", internal, want)
	}
}
