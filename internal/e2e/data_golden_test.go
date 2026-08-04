package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"gopkg.in/yaml.v3"
)

// TestDataGoldenSequence is spec 05a §6.6's golden fixture, THREE of the
// five documents the spec names: the request, the first package, and the
// superseding package. `03-report-fail.json` and `05-report-pass.json` are
// deliberately ABSENT — see testdata/golden/data-loop/README.md's own
// "why only three" section: `a2a data verify` cannot resolve any real
// package's contract today (a confirmed product defect, named in this
// brief's deviations, not routed around here). The numbering gap (01, 02,
// 04 — no 03, no 05) is intentional: it is where the two report fixtures
// slot back in once that defect is fixed, without renaming anything.
//
// Every document here is produced by a REAL exec of the built `a2a` binary
// via hostRig (host_loop_test.go), through the real
// pack -> deliver -> sync -> pack(--supersedes) -> deliver -> sync
// sequence an operator would run — never a direct construction of dataCore
// or internal/datapackage.
func TestDataGoldenSequence(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")
	contractID := publishDataAdversarialContract(t, r, "goldenexport", "01K1A2B3C4D5E6F7G8H9J0KAG1")
	r.mustRun("sync")

	const thread = "thread:axon-20260804-g001"
	const requestID = "XW-axon-20260804-g001"
	draft := r.stageWorkRequest(requestID, thread, "beta")
	r.mustRun("submit", draft)
	r.mustRun("sync")

	// --- 01: the request, read back from origin/main (not from the staged
	// draft) — the fixture proves the request actually LANDED, byte-for-
	// byte what a2a submit committed, not what this test authored in
	// memory.
	requestJSON := dataGoldenReadCommittedEnvelopeAsJSON(t, r, "axon/exchanges/"+requestID+".md")

	// --- attempt 1: packed, then provenance-patched before delivery (see
	// patchStagedManifestProvenance's own doc comment — a confirmed
	// product defect, not this test's doing: without the patch, EVERY
	// real `a2a data deliver` invocation refuses).
	from1 := t.TempDir()
	mustWrite(t, filepath.Join(from1, "orders.json"), `{"example":"widget-1"}`+"\n")
	pkg1ID, manifest1Raw := dataGoldenPack(t, r, contractID, from1, requestID, "")
	stagingRoot1 := filepath.Join(r.projectDir, ".a2a", "staging", "data", pkg1ID)
	patchStagedManifestProvenance(t, stagingRoot1)
	r.mustRun("data", "deliver", stagingRoot1, "--fulfills", requestID)
	r.mustRun("sync")

	// --- attempt 2: supersedes attempt 1 — `pack --supersedes` resolves
	// the prior package from origin/main (space.ResolveDataPackage), not
	// from local staging, so attempt 1 must already be merged (the sync
	// above) before this pack call can even succeed.
	from2 := t.TempDir()
	mustWrite(t, filepath.Join(from2, "orders.json"), `{"example":"widget-2"}`+"\n")
	pkg2ID, manifest2Raw := dataGoldenPack(t, r, contractID, from2, requestID, pkg1ID)
	stagingRoot2 := filepath.Join(r.projectDir, ".a2a", "staging", "data", pkg2ID)
	patchStagedManifestProvenance(t, stagingRoot2)
	r.mustRun("data", "deliver", stagingRoot2, "--fulfills", requestID, "--supersedes", pkg1ID)
	r.mustRun("sync")

	// --- masking: every id this run minted (packages via crypto/rand +
	// wall-clock date) is replaced by a fixed, unmistakably-redacted
	// placeholder BEFORE comparison — never blindly by regex: each
	// substitution targets the EXACT string this run observed, and each is
	// shape-checked against the real parser first
	// (dataGoldenAssertPackageIDShape) so a regression in the ID FORMAT
	// itself would fail loudly here rather than silently vanish into a
	// placeholder. See testdata/golden/data-loop/README.md for the full
	// account of what is masked and why; everything else — every digest,
	// every count, every content byte, the pinned contract ref — is
	// asserted unmasked, which is the whole point of a golden.
	dataGoldenAssertPackageIDShape(t, pkg1ID)
	dataGoldenAssertPackageIDShape(t, pkg2ID)

	mask := dataGoldenMasker{
		ids: map[string]string{
			pkg1ID: "DP-axon-REDACTED-0001",
			pkg2ID: "DP-axon-REDACTED-0002",
		},
	}

	dataGoldenCompare(t, "01-request.json", mask.apply(t, requestJSON))
	dataGoldenCompare(t, "02-package-attempt-1.json", mask.apply(t, manifest1Raw))
	dataGoldenCompare(t, "04-package-attempt-2.json", mask.apply(t, manifest2Raw))
}

// dataGoldenPack runs `a2a data pack --json` for real and returns the
// minted package id and the EXACT bytes the staged manifest.json held
// immediately afterward — captured BEFORE patchStagedManifestProvenance
// ever touches it, so the golden freezes pack's own real output (including
// the confirmed provenance defect — see patchStagedManifestProvenance's
// doc comment — which is a wire-shape fact worth freezing, not one to
// paper over in a fixture).
func dataGoldenPack(t *testing.T, r *hostRig, contractID, from, requestID, supersedes string) (packageID string, manifestRaw []byte) {
	t.Helper()
	args := []string{
		"data", "pack", "--contract", contractID + "@1.0.0", "--from", from,
		"--profile", "synthetic", "--format", "json", "--fulfills", requestID,
		"--expires", "24h", "--json",
	}
	if supersedes != "" {
		args = append(args, "--supersedes", supersedes)
	}
	out := r.mustRun(args...)
	var result cli.DataResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode pack --json: %v\n%s", err, out)
	}
	if result.Manifest == nil {
		t.Fatalf("pack --json produced no manifest: %s", out)
	}
	packageID = result.Manifest.ID
	stagingRoot := filepath.Join(r.projectDir, ".a2a", "staging", "data", packageID)
	raw, err := os.ReadFile(filepath.Join(stagingRoot, "manifest.json"))
	if err != nil {
		t.Fatalf("read freshly staged manifest: %v", err)
	}
	return packageID, raw
}

// dataGoldenReadCommittedEnvelopeAsJSON reads relPath off origin/main (the
// space the rig's fake host actually merged into — not the local staged
// draft) and converts its YAML frontmatter into a small, deterministic JSON
// projection: every value here is one this test itself authored into the
// draft (stageWorkRequest's own literal fields), so nothing needs masking —
// the read-back is what proves the request really landed as written, which
// is the point (spec 05a §6.6's "the request").
func dataGoldenReadCommittedEnvelopeAsJSON(t *testing.T, r *hostRig, relPath string) []byte {
	t.Helper()
	raw := gitOutput(t, r.fx.RemoteURL(), "show", "main:"+relPath)
	fm, err := artifact.ParseFrontmatter([]byte(raw))
	if err != nil {
		t.Fatalf("parse committed envelope %s frontmatter: %v", relPath, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		t.Fatalf("decode committed envelope %s YAML: %v", relPath, err)
	}
	body := strings.TrimSpace(string(fm.Body))
	doc["body"] = body
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode committed envelope %s as JSON: %v", relPath, err)
	}
	return out
}

func dataGoldenAssertPackageIDShape(t *testing.T, id string) {
	t.Helper()
	if _, err := datapackage.ParsePackageID(id); err != nil {
		t.Fatalf("minted package id %q does not have the real DP- shape: %v", id, err)
	}
}

// dataGoldenTimestampPattern matches an RFC3339 UTC timestamp — every
// created_at/expires_at value this sequence's own wall clock produced.
// Masked generically (unlike the ids above, whose exact runtime value this
// test already knows and substitutes by exact match) because nothing about
// a producer clock's exact value is a protocol property (§T2.1: "never a
// protocol ordering key") this golden should ever assert.
var dataGoldenTimestampPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`)

type dataGoldenMasker struct {
	ids map[string]string
}

// apply replaces every exact id this run minted with its fixed placeholder,
// then generically masks every timestamp, then pretty-prints the result —
// json.Indent, not this test's own string surgery, so field order is the
// product's own encoding/json struct-tag order (a real wire property) and
// the committed fixture is diffable line by line.
func (m dataGoldenMasker) apply(t *testing.T, raw []byte) []byte {
	t.Helper()
	text := string(raw)
	for from, to := range m.ids {
		text = strings.ReplaceAll(text, from, to)
	}
	text = dataGoldenTimestampPattern.ReplaceAllString(text, "REDACTED-TIMESTAMP")
	var indented bytes.Buffer
	if err := json.Indent(&indented, []byte(text), "", "  "); err != nil {
		t.Fatalf("indent masked document: %v\n%s", err, text)
	}
	return append(indented.Bytes(), '\n')
}

const dataGoldenDir = "testdata/golden/data-loop"

// dataGoldenCompare compares got against the committed fixture
// testdata/golden/data-loop/name byte for byte, printing the first
// differing line (not just "not equal") on failure.
//
// UPDATE_GOLDEN=1 regenerates every fixture from this run's own real output
// instead of comparing — the mechanism testdata/golden/data-loop/README.md
// documents as this convention's own regeneration path. It is deliberately
// gated behind an explicit env var, never a default: a golden that silently
// rewrites itself on a failing run would stop being a golden.
func dataGoldenCompare(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(dataGoldenDir, name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("UPDATE_GOLDEN: write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v (run with UPDATE_GOLDEN=1 to create it)", path, err)
	}
	if string(want) == string(got) {
		return
	}
	t.Fatalf("%s does not match its golden fixture:\n%s", name, dataGoldenFirstDiff(string(want), string(got)))
}

// dataGoldenFirstDiff renders the first line at which want and got
// disagree — a real diff, not a bare "not equal": spec 05a §6.6's own
// acceptance line is "the test ... prints a real diff on failure ... a
// golden that fails with 'not equal' and no diff costs more than it saves."
func dataGoldenFirstDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	max := len(wantLines)
	if len(gotLines) > max {
		max = len(gotLines)
	}
	for i := 0; i < max; i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			return fmt.Sprintf("first difference at line %d:\n  want: %s\n  got:  %s\n(want has %d lines, got has %d lines)",
				i+1, w, g, len(wantLines), len(gotLines))
		}
	}
	return "(strings differ but no line-level difference was found — likely a trailing-newline mismatch)"
}
