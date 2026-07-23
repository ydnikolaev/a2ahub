package e2e

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestE2ECoverageParity is spec 26 §1.3/§8 AC-1's self-enforcing gate: (a)
// every LIVE catalog CLI verb (read from the BUILT binary's own `a2a
// __catalog` output — never a second, hand-maintained list) is named by at
// least one coverageManifest row (evidence or skip); (b) every row's
// evidence actually resolves — a Txtar row to an existing, verb-invoking
// testdata/t3/*.txtar file, a GoTest row via `go test -list` (cccoverage's
// own resolveTestRef, reused verbatim); (c) every row has exactly one of
// Txtar/GoTest/Skip set, and a Skip row's justification is therefore
// non-empty by construction. Bidirectional (mirrors
// cmd/a2a/catalog_test.go's TestCatalogCommandNameParity rigor): a manifest/
// skip row naming a verb that ISN'T a real catalog verb also fails — a
// stale row surviving a rename/removal is caught, not silently ignored.
func TestE2ECoverageParity(t *testing.T) {
	root := repoRootForTest(t)
	bin := filepath.Join(binDir, "a2a")

	catalogOutput, err := runCatalogBinary(bin)
	if err != nil {
		t.Fatalf("exec `a2a __catalog`: %v", err)
	}
	catalogVerbs := parseCatalogVerbs(catalogOutput)
	if len(catalogVerbs) == 0 {
		t.Fatal("parseCatalogVerbs: got zero verbs from `a2a __catalog` output — parsing likely broken")
	}

	// (a) every catalog verb is named by the manifest (evidence or skip).
	if missing := missingVerbs(catalogVerbs, coverageManifest); len(missing) > 0 {
		t.Errorf("catalog verb(s) with NO coverage manifest row (mapped or skipped): %v", missing)
	}

	// Bidirectional: no stale manifest/skip row names a verb that no longer
	// exists in the catalog.
	if stale := staleEntries(catalogVerbs, coverageManifest); len(stale) > 0 {
		for _, e := range stale {
			t.Errorf("coverage manifest row for verb %q does not match any real catalog verb (stale row)", e.Verb)
		}
	}

	// (b)+(c) every row resolves. GoTest refs are deduped across rows —
	// several verbs (e.g. every OP-211 lifecycle verb TestT3LifecycleVerbs
	// covers) share one Go test func; resolving it once per distinct ref
	// keeps subprocess fan-out (`go test -list`) sane under -count=2.
	t3Dir := filepath.Join(root, "internal", "e2e", "testdata", "t3")
	goTestCache := map[string]error{}
	for _, e := range coverageManifest {
		kind, err := e.evidenceKind()
		if err != nil {
			t.Errorf("verb %q: %v", e.Verb, err)
			continue
		}
		switch kind {
		case "txtar":
			if err := resolveTxtarEntry(t3Dir, e.Txtar, e.Verb); err != nil {
				t.Errorf("verb %q: %v", e.Verb, err)
			}
		case "goTest":
			if _, ok := goTestCache[e.GoTest]; !ok {
				goTestCache[e.GoTest] = resolveTestRef(root, e.GoTest)
			}
			if err := goTestCache[e.GoTest]; err != nil {
				t.Errorf("verb %q: GoTest ref %q does not resolve: %v", e.Verb, e.GoTest, err)
			}
		case "skip":
			// evidenceKind already proved Skip is non-empty.
		}
	}
}

// --- teeth: this gate's own self-tests (spec 26 §6/§8 AC-2) --------------
// Every check below drives the PURE resolution helpers directly against a
// synthetic fixture or a throwaway manifest slice — never the real
// coverageManifest — so a broken gate (one that would pass ANY input) is
// caught independently of whether today's real manifest happens to be
// clean.

// TestE2ECoverageGateCatchesMissingVerb proves missingVerbs flags a real
// catalog verb absent from every manifest row (AC-2's "adding a catalog
// verb without a scenario turns CI red").
func TestE2ECoverageGateCatchesMissingVerb(t *testing.T) {
	catalogVerbs := []string{"alpha", "beta", "gamma"}
	entries := []coverageEntry{
		{Verb: "alpha", Skip: "not covered yet"},
		{Verb: "beta", Skip: "not covered yet"},
		// "gamma" deliberately absent from entries.
	}
	got := missingVerbs(catalogVerbs, entries)
	want := []string{"gamma"}
	sort.Strings(got)
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("missingVerbs(%v, entries) = %v, want %v", catalogVerbs, got, want)
	}

	// And the gate is not a no-op the other way: a fully-covered set must
	// report nothing missing.
	if got := missingVerbs(catalogVerbs, append(entries, coverageEntry{Verb: "gamma", Skip: "now covered"})); len(got) != 0 {
		t.Fatalf("expected no missing verbs once every catalog verb has a row, got %v", got)
	}
}

// TestE2ECoverageGateCatchesStaleManifestEntry proves staleEntries flags a
// manifest/skip row naming a verb that is NOT a real catalog verb (the
// reverse-direction half of AC-1's bidirectional check).
func TestE2ECoverageGateCatchesStaleManifestEntry(t *testing.T) {
	catalogVerbs := []string{"alpha", "beta"}
	entries := []coverageEntry{
		{Verb: "alpha", Skip: "fine"},
		{Verb: "beta", Skip: "fine"},
		{Verb: "zzz-removed-verb", Skip: "stale — this verb no longer ships"},
	}
	stale := staleEntries(catalogVerbs, entries)
	if len(stale) != 1 || stale[0].Verb != "zzz-removed-verb" {
		t.Fatalf("staleEntries(%v, entries) = %v, want exactly one stale row for zzz-removed-verb", catalogVerbs, stale)
	}
}

// TestE2ECoverageGateCatchesBadTxtarRef proves resolveTxtarEntry FAILS both
// when the named file doesn't exist and when it exists but never actually
// invokes the claimed verb (AC-1(b)'s grep check) — using a t.TempDir()
// fixture, never the real testdata/t3 tree.
func TestE2ECoverageGateCatchesBadTxtarRef(t *testing.T) {
	dir := t.TempDir()

	if err := resolveTxtarEntry(dir, "does_not_exist.txtar", "connect"); err == nil {
		t.Fatal("expected a missing txtar file to fail resolution, but it resolved")
	}

	writeTempTxtar(t, dir, "no_invoke.txtar", "exec a2a doctor\nstdout 'PASS'\n")
	if err := resolveTxtarEntry(dir, "no_invoke.txtar", "connect"); err == nil {
		t.Fatal("expected a txtar that never invokes `a2a connect` to fail resolution, but it resolved")
	}

	// And the gate isn't broken the other way: a genuinely invoking script
	// must resolve, including the contract-<sub> -> `a2a contract <sub>`
	// two-word rewrite (catalog.go's own documentation-only hyphenation of
	// the single "contract" dispatch verb).
	writeTempTxtar(t, dir, "invokes.txtar", "exec a2a connect $ORIGIN\nstdout 'registered'\n")
	if err := resolveTxtarEntry(dir, "invokes.txtar", "connect"); err != nil {
		t.Fatalf("expected a genuinely invoking txtar to resolve, got: %v", err)
	}

	writeTempTxtar(t, dir, "contract_new.txtar", "exec a2a contract new widget\nstdout 'staged'\n")
	if err := resolveTxtarEntry(dir, "contract_new.txtar", "contract-new"); err != nil {
		t.Fatalf("expected `a2a contract new` to resolve the contract-new verb, got: %v", err)
	}
}

// TestE2ECoverageGateCatchesBrokenGoTestRef proves a GoTest ref that does
// not resolve (a renamed/removed test func) fails — reusing
// cccoverage_test.go's own resolveTestRef, never a re-implementation.
func TestE2ECoverageGateCatchesBrokenGoTestRef(t *testing.T) {
	root := repoRootForTest(t)
	if err := resolveTestRef(root, "internal/e2e.TestDoesNotExistNoReally"); err == nil {
		t.Fatal("expected a deliberately-broken GoTest ref to FAIL resolution, but it resolved")
	}
	if err := resolveTestRef(root, "internal/e2e.TestT3Submit"); err != nil {
		t.Fatalf("expected a genuine GoTest ref to resolve, got: %v", err)
	}
}

// TestE2ECoverageGateCatchesEmptySkipJustification proves evidenceKind
// rejects a row with no Txtar/GoTest AND no Skip justification (AC-2's
// "empty skip justification" teeth clause) — and, symmetrically, a row with
// MORE than one of the three set (an equally malformed, ambiguous row).
func TestE2ECoverageGateCatchesEmptySkipJustification(t *testing.T) {
	empty := coverageEntry{Verb: "ghost"}
	if _, err := empty.evidenceKind(); err == nil {
		t.Fatal("expected an all-empty coverage entry (empty skip justification) to fail evidenceKind, but it passed")
	}

	ambiguous := coverageEntry{Verb: "ghost", Txtar: "x.txtar", Skip: "also skipped?"}
	if _, err := ambiguous.evidenceKind(); err == nil {
		t.Fatal("expected a coverage entry with BOTH Txtar and Skip set to fail evidenceKind, but it passed")
	}

	valid := coverageEntry{Verb: "ghost", Skip: "a real one-line reason"}
	kind, err := valid.evidenceKind()
	if err != nil || kind != "skip" {
		t.Fatalf("expected a valid skip-only entry to resolve to kind \"skip\", got (%q, %v)", kind, err)
	}
}

// TestE2ECoverageParseCatalogVerbsExcludesHiddenAndMCP proves
// parseCatalogVerbs drops the hidden `__catalog` row and any `a2a_*` MCP
// tool row, against a synthetic catalog document (never the real binary's
// output — that's TestE2ECoverageParity's job).
func TestE2ECoverageParseCatalogVerbsExcludesHiddenAndMCP(t *testing.T) {
	doc := "# a2a command / MCP tool catalog\n\n" +
		"## Commands\n\n" +
		"- `__catalog` — hidden\n" +
		"- `connect` — register a space\n" +
		"- `submit` — validate and submit\n\n" +
		"## MCP tools\n\n" +
		"- `a2a_submit` — validate and submit staged drafts\n"

	got := parseCatalogVerbs(doc)
	want := []string{"connect", "submit"}
	if len(got) != len(want) {
		t.Fatalf("parseCatalogVerbs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseCatalogVerbs = %v, want %v", got, want)
		}
	}
}

func writeTempTxtar(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp txtar %s: %v", path, err)
	}
}
