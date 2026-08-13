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

// TestDataGoldenSequence is spec 05a §6.6's golden fixture, all FIVE of the
// documents the spec names: the request, the first package, its failing
// report, the superseding package, and its passing report.
// `03-report-fail.json` and `05-report-pass.json` were ABSENT until
// 2026-08-13 — see testdata/golden/data-loop/README.md's own "why only
// three" section (kept, as the AS-FOUND record of what the two-party proof
// caught) for the full account: the paragraph there described a confirmed
// product defect — `a2a data verify` could not resolve any real package's
// contract — but that defect was RESOLVED on 2026-08-04, by the very same
// commit (`9f02f261`) that wrote the sentence describing it:
// `splitDataContractReference` (internal/space) now cuts on "#" first and
// CHECKS the digest instead of refusing it. Re-verified 2026-08-13: all
// seven `TestDataLoop*` tests in data_loop_test.go pass, including the
// three that comment once called blocked. The numbering gap (01, 02, 04 —
// no 03, no 05) was deliberate for exactly this: the two report fixtures
// now slot back in without renaming anything.
//
// Every document here is produced by a REAL exec of the built `a2a` binary
// via hostRig (host_loop_test.go), through the real
// pack -> deliver -> sync -> ack -> verify --record -> sync
// (attempt 1, failing) -> pack(--supersedes) -> deliver -> sync -> ack ->
// verify --record -> sync (attempt 2, passing) sequence an operator would
// run — never a direct construction of dataCore or internal/datapackage,
// and never a report built in memory: both report fixtures are read back
// from the committed document `a2a data verify --record` actually landed
// in the space (dataGoldenReadCommittedJSON), the same "prove it landed,
// not merely that the process printed it" discipline `01-request.json`
// already uses.
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
	// Corrupt the staged manifest's own declared record_count for
	// "orders.json" (dataLoopCorruptRecordCount, this package's own
	// data_loop_test.go — the exact mechanism TestDataLoopVerifyFailing-
	// PayloadNamesEntryAndRule and TestDataLoopFailSupersedePassCloseInbox-
	// NeverAccumulates already use for their own fail leg): the digest is
	// untouched, so `a2a data deliver`'s own schema/digest checks still
	// accept the package, and only `a2a data verify`'s consumer-side
	// recount can catch the disagreement — applied AFTER manifest1Raw was
	// already captured, so 02-package-attempt-1.json keeps freezing pack's
	// own pristine output, exactly as patchStagedManifestProvenance already
	// does one line above.
	dataLoopCorruptRecordCount(t, stagingRoot1, "orders.json", 1)
	handoff1 := dataGoldenDeliver(t, r, stagingRoot1, requestID)
	r.mustRun("sync")
	r.mustRun("ack", handoff1)

	// --- verify attempt 1: a real `a2a data verify --record`, expected to
	// FAIL (record-count disagreement on orders.json) — captured from the
	// committed verification-report/v1 document, never from the report
	// this call printed.
	report1ID, report1JSON := dataGoldenVerifyRecord(t, r, pkg1ID, 1)

	// --- attempt 2: supersedes attempt 1 — `pack --supersedes` resolves
	// the prior package from origin/main (space.ResolveDataPackage), not
	// from local staging, so attempt 1 must already be merged (the sync
	// above) before this pack call can even succeed.
	from2 := t.TempDir()
	mustWrite(t, filepath.Join(from2, "orders.json"), `{"example":"widget-2"}`+"\n")
	pkg2ID, manifest2Raw := dataGoldenPack(t, r, contractID, from2, requestID, pkg1ID)
	stagingRoot2 := filepath.Join(r.projectDir, ".a2a", "staging", "data", pkg2ID)
	patchStagedManifestProvenance(t, stagingRoot2)
	// Supersession is already in the manifest pack produced; deliver only
	// ships it, and refuses the flag rather than ignoring it.
	handoff2 := dataGoldenDeliver(t, r, stagingRoot2, requestID)
	r.mustRun("sync")
	r.mustRun("ack", handoff2)

	// --- verify attempt 2: a real `a2a data verify --record`, expected to
	// PASS (a clean payload, no corruption) — same read-back discipline.
	report2ID, report2JSON := dataGoldenVerifyRecord(t, r, pkg2ID, 0)

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
	dataGoldenAssertReportIDShape(t, report1ID)
	dataGoldenAssertReportIDShape(t, report2ID)

	mask := dataGoldenMasker{
		ids: map[string]string{
			pkg1ID:    "DP-axon-REDACTED-0001",
			pkg2ID:    "DP-axon-REDACTED-0002",
			report1ID: "VR-axon-REDACTED-0001",
			report2ID: "VR-axon-REDACTED-0002",
		},
	}

	dataGoldenCompare(t, "01-request.json", mask.apply(t, requestJSON))
	dataGoldenCompare(t, "02-package-attempt-1.json", mask.apply(t, manifest1Raw))
	dataGoldenCompare(t, "03-report-fail.json", mask.apply(t, report1JSON))
	dataGoldenCompare(t, "04-package-attempt-2.json", mask.apply(t, manifest2Raw))
	dataGoldenCompare(t, "05-report-pass.json", mask.apply(t, report2JSON))
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

// dataGoldenDeliver runs a real `a2a data deliver --json` and returns the
// handoff id it minted — needed so this test's own attempt-1/attempt-2
// verify steps can `ack` the exact handoff each delivery created before
// `a2a data verify --record` (the fold table only fires verify-pass/
// verify-fail from `acknowledged`, the same precondition
// TestDataLoopFailSupersedePassCloseInboxNeverAccumulates already proves).
// `--json` changes nothing about 02/04's own fixtures: both are captured
// from the staged manifest.json (dataGoldenPack), never from deliver's own
// stdout, mirroring data_loop_test.go's own dataLoopDeliver precedent.
func dataGoldenDeliver(t *testing.T, r *hostRig, stagingRoot, requestID string) (handoffID string) {
	t.Helper()
	out := r.mustRun("data", "deliver", stagingRoot, "--fulfills", requestID, "--json")
	var result cli.DataResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode deliver --json: %v\n%s", err, out)
	}
	if result.HandoffID == "" {
		t.Fatalf("deliver --json produced no handoff id: %s", out)
	}
	return result.HandoffID
}

// dataGoldenVerifyRecord runs a real `a2a data verify --record --json`
// against pkgID, asserts it exits wantCode (1 for a failing verdict, 0 for
// a passing one — cmd_data.go's own runVerify), asserts the report's own
// Result() agrees, and returns the report's own VR- id (for
// dataGoldenAssertReportIDShape/masking, mirroring pkg1ID/pkg2ID) alongside
// the EXACT bytes committed to the space — read back via
// dataGoldenReadCommittedJSON, never assembled from what this call printed
// (the same "prove it landed" discipline dataGoldenReadCommittedEnvelopeAs-
// JSON already applies to the request).
func dataGoldenVerifyRecord(t *testing.T, r *hostRig, pkgID string, wantCode int) (reportID string, reportJSON []byte) {
	t.Helper()
	out, stderr, code := r.run("data", "verify", pkgID, "--record", "--json")
	if code != wantCode {
		t.Fatalf("verify --record %s: exit %d, want %d\nstdout=%s\nstderr=%s", pkgID, code, wantCode, out, stderr)
	}
	var result cli.DataResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode verify --record --json: %v\n%s", err, out)
	}
	if result.Report == nil {
		t.Fatalf("verify --record %s produced no report: %s", pkgID, out)
	}
	wantResult := datapackage.ResultPass
	if wantCode != 0 {
		wantResult = datapackage.ResultFail
	}
	if result.Report.Result() != wantResult {
		t.Fatalf("verify --record %s: report result = %q, want %q\nstdout=%s", pkgID, result.Report.Result(), wantResult, out)
	}
	r.mustRun("sync")
	relPath := r.system + "/data/" + pkgID + "/report.json"
	return result.Report.ID, dataGoldenReadCommittedJSON(t, r, relPath)
}

// dataGoldenReadCommittedJSON reads relPath off origin/main and returns its
// raw committed bytes verbatim — unlike
// dataGoldenReadCommittedEnvelopeAsJSON, relPath here already IS the wire
// document (verification-report/v1's own report.json,
// space.RecordVerificationReport's dataPackageReportPath), never a markdown
// envelope with YAML frontmatter to project, so no parsing happens before
// masking.
func dataGoldenReadCommittedJSON(t *testing.T, r *hostRig, relPath string) []byte {
	t.Helper()
	raw := gitOutput(t, r.fx.RemoteURL(), "show", "main:"+relPath)
	return []byte(raw)
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

// dataGoldenAssertReportIDShape mirrors dataGoldenAssertPackageIDShape for
// verification-report/v1's own VR- id (datapackage.MintReportIDAt), the
// same "round-trip through the real parser before masking" discipline.
func dataGoldenAssertReportIDShape(t *testing.T, id string) {
	t.Helper()
	if _, err := datapackage.ParseReportID(id); err != nil {
		t.Fatalf("minted report id %q does not have the real VR- shape: %v", id, err)
	}
}

// dataGoldenTimestampPattern matches an RFC3339 UTC timestamp — every
// created_at/expires_at value this sequence's own wall clock produced.
// Masked generically (unlike the ids above, whose exact runtime value this
// test already knows and substitutes by exact match) because nothing about
// a producer clock's exact value is a protocol property (§T2.1: "never a
// protocol ordering key") this golden should ever assert.
var dataGoldenTimestampPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`)

// dataGoldenDurationPattern matches verification-report/v1's own
// `observed.duration_ms` field — the third value (besides package/report
// ids and timestamps) this sequence's own wall clock produces and no
// `--clock` override exists to pin (datapackage/verify.go's own doc
// comment: "StartedAt, FinishedAt and the duration derived from them are
// the ONLY three values L-4 permits to differ between two runs"). Masked
// to the literal digit `0`, a number the field's own schema (§T2.3,
// `observed.duration_ms`, presumably minimum 0) accepts, so the masked
// document stays schema-shaped JSON rather than merely diffable text —
// unlike the timestamp mask above, which substitutes inside an existing
// string, a numeric field's replacement must itself be a valid JSON number
// for json.Indent (below) to still succeed.
var dataGoldenDurationPattern = regexp.MustCompile(`"duration_ms":\d+`)

type dataGoldenMasker struct {
	ids map[string]string
}

// apply replaces every exact id this run minted with its fixed placeholder,
// then generically masks every timestamp and every measured duration, then
// pretty-prints the result — json.Indent, not this test's own string
// surgery, so field order is the product's own encoding/json struct-tag
// order (a real wire property) and the committed fixture is diffable line
// by line.
func (m dataGoldenMasker) apply(t *testing.T, raw []byte) []byte {
	t.Helper()
	text := string(raw)
	for from, to := range m.ids {
		text = strings.ReplaceAll(text, from, to)
	}
	text = dataGoldenTimestampPattern.ReplaceAllString(text, "REDACTED-TIMESTAMP")
	text = dataGoldenDurationPattern.ReplaceAllString(text, `"duration_ms":0`)
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
