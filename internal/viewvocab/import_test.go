package viewvocab

import (
	"go/build"
	"path/filepath"
	"runtime"
	"testing"
)

// TestImportsNothing is spec 02 AC6: internal/viewvocab imports nothing, so
// any caller (a chat notifier, the CI plane) can read the RU/EN dashboard
// vocabulary without inverting the ADR-001 boundary the presentation
// package's import set protects.
//
// This checks DIRECT PRODUCTION imports (go/build's pkg.Imports, which
// excludes TestImports and this file's own stdlib imports) — vocab.go itself
// carries no import block at all.
func TestImportsNothing(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate viewvocab test source")
	}
	packageDir := filepath.Dir(filename)

	pkg, err := build.Default.ImportDir(packageDir, 0)
	if err != nil {
		t.Fatalf("inspect production imports for internal/viewvocab: %v", err)
	}
	if len(pkg.Imports) != 0 {
		t.Errorf("internal/viewvocab production imports = %v, want none", pkg.Imports)
	}
}
