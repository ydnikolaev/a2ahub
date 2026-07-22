package main

import (
	"bytes"
	"reflect"
	"sort"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// catalog_test.go is the P13 catalog seam's own guard (spec 13 §8 AC #3 +
// §11 wave-7 amendment): a name-parity guard (catalog CLI section ⇆
// buildCommands() keys, two-level, mirroring mcp_parity_test.go's
// designatedCLIVerbs() shape), a determinism check, and an MCP-section
// parity check against the SAME emptyRegistry() this file's sibling
// mcp_parity_test.go already defines (package main, one definition).

// expectedCatalogCommandNames independently recomputes the "## Commands"
// section's expected verb-name set from buildCommands() +
// cli.ContractSubcommands() — the SAME expansion rule catalog.go's
// catalogCommandRows() applies, written a second time here so a drift in
// either place (a verb added to dispatch but not handled in catalog.go, or
// vice versa) is caught independently rather than the test trivially
// agreeing with the implementation it is meant to guard.
func expectedCatalogCommandNames() []string {
	var out []string
	for name := range buildCommands() {
		if name == "contract" {
			for _, sub := range cli.ContractSubcommands() {
				out = append(out, "contract-"+sub.Name)
			}
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestCatalogCommandNameParity is the missing/orphan-verb guard: a verb
// added to buildCommands() but not handled in catalog.go's
// catalogCommandRows()/catalogCLICommand (missing catalogHandTypedSynopsis
// entry AND no cli.Command constructor case) makes catalogCommandRows()
// panic, failing this test; a name set drift between the two independent
// expansions also fails it directly.
func TestCatalogCommandNameParity(t *testing.T) {
	t.Parallel()
	want := expectedCatalogCommandNames()

	rows := catalogCommandRows()
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Name)
	}
	sort.Strings(got)

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("catalog CLI-section verb-name set mismatch:\nwant: %v\ngot:  %v", want, got)
	}
}

// TestCatalogCommandRowsHaveSynopsis proves every row's Synopsis is
// non-empty — a construction that silently yields a blank Synopsis() would
// still pass the name-parity check above but produce a useless catalog
// row.
func TestCatalogCommandRowsHaveSynopsis(t *testing.T) {
	t.Parallel()
	for _, r := range catalogCommandRows() {
		if r.Synopsis == "" {
			t.Errorf("catalog row %q has an empty synopsis", r.Name)
		}
	}
}

// TestCatalogMCPSectionParity is the MCP-section parity check: the
// catalog's "## MCP tools" name set equals emptyRegistry().ToolNames() —
// the SAME registry construction mcp_parity_test.go's own bijection tests
// already use (package-shared emptyRegistry(), defined once).
func TestCatalogMCPSectionParity(t *testing.T) {
	t.Parallel()
	want := emptyRegistry().ToolNames() // already sorted

	rows := catalogMCPRows()
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Name)
	}
	sort.Strings(got)

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("catalog MCP-section name set mismatch:\nwant: %v\ngot:  %v", want, got)
	}
}

// TestCatalogDeterministic is spec 13 §8 AC #3: calling the catalog
// renderer twice yields byte-identical output (no timestamp, no map-
// iteration-order leak, no absolute path, no version/sha).
func TestCatalogDeterministic(t *testing.T) {
	t.Parallel()
	a := renderCatalog()
	b := renderCatalog()
	if a != b {
		t.Fatalf("renderCatalog() is not deterministic across two calls")
	}
}

// TestRunCatalogExitCodeAndOutput proves the dispatched verb itself (not
// just the internal renderer) writes exactly renderCatalog()'s output to
// stdout, nothing to stderr, and exits 0.
func TestRunCatalogExitCodeAndOutput(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runCatalog(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runCatalog exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("runCatalog wrote to stderr: %q", stderr.String())
	}
	if stdout.String() != renderCatalog() {
		t.Fatalf("runCatalog stdout does not match renderCatalog() output")
	}
}

// TestCatalogRegisteredInDispatch proves `__catalog` is a real
// buildCommands() key (wire.go registration), not just a standalone
// function — dispatch never sees it unless registered.
func TestCatalogRegisteredInDispatch(t *testing.T) {
	t.Parallel()
	if _, ok := buildCommands()["__catalog"]; !ok {
		t.Fatal("expected \"__catalog\" to be registered in buildCommands()")
	}
}
