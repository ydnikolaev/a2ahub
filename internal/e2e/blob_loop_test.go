// P10 (agent-exchange-2026-08) spec 10's closing wave, driven through the
// real binary per spec 10 §5's own instruction ("through the real binary,
// not by direct construction... using it is not optional here").
// internal/cli/cmd_attach_test.go and cmd_fetch_test.go already cover both
// verbs' behaviour in process; this file proves the composition nobody has
// driven yet — `a2a attach` and `a2a submit`, as two separate binary
// invocations against the SAME staged draft, landing the exact sequence
// B32 proved refused — plus AC3 (fetch round-trips an unpinned attachment,
// digest-verified), AC4 (a ceiling refusal names which ceiling) and AC7 (a
// digest mismatch is refused on both the write and the read side).
//
// One finding, recorded rather than worked around (spec 10 §3 seam 4's own
// ceilings, blob_delivery.go): datapackage.DefaultBounds().MaxEntries is
// 2048 (bounds.go), the EXACT SAME number space's own
// maxBlobEntries-1 = 2048 ceiling names (blob_delivery.go's own doc
// comment). `a2a attach --from <dir>` walks the source through
// datapackage.WalkLocalDirectory BEFORE ever building the payload
// space.DeliverBlob would receive (cmd_attach.go calls
// NewAttachmentFromDirectoryWithPayload, which calls WalkLocalDirectory,
// which enforces bounds.CheckEntryCount as it walks) — so a source over
// 2048 entries always trips the DATAPACKAGE-layer bound (ErrBoundExceeded,
// bounds.go) first, entirely locally, and space.DeliverBlob's own
// checkBlobPayloadCeilings (and therefore ErrBlobTooManyEntries) can never
// fire through `a2a attach` as currently wired. The per-entry-byte
// (128 MiB) and total-byte (2 GiB) ceilings are NOT similarly shadowed —
// datapackage's own CheckEntryBytes/CheckPackageBytes bounds are larger
// (bounds.go) — but driving either through the real binary means
// committing 128 MiB+ fixtures to a fake git remote, which this file does
// not do; both are covered at the unit tier instead
// (TestCheckBlobPayloadCeilings, TestDeliverBlobRefusesTooManyEntries,
// internal/space/blob_delivery_test.go — the exact three-way split this
// package's own defaultBlobCeilingBounds indirection exists to make
// testable without gigabyte fixtures). TestAttachRefusesEntryCountCeiling
// BeforeAnyNetworkWrite below drives the ceiling that IS reachable through
// the binary — the datapackage-layer one — and says so in its own name and
// doc comment rather than claiming it proves space's ErrBlobTooManyEntries.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stageAttachWorkRequest writes a schema-valid envelope/v2 work_request
// into staging, addressed to `to`, carrying no attachments yet (`a2a
// attach` adds those) and no contract/fulfills obligation at all — the
// general US-1 case this wave exists to prove, not the contract-pinned
// `DP-` specialisation data_loop_test.go already covers. category "other"
// (not contract-change/process-change) keeps this out of that branch's own
// required refs[]/proposed_change; blocking: false carries the
// interim_behavior the schema requires whenever blocking is explicit and
// false (mirroring data_loop_test.go's own stageDataLoopWorkRequest, a
// differently-shaped sibling for a differently-shaped fixture need).
func (r *hostRig) stageAttachWorkRequest(id, to string) (path string) {
	r.t.Helper()
	content := "---\n" +
		"schema: envelope/v2\n" +
		"id: " + id + "\n" +
		"type: work_request\n" +
		"title: attach and submit without a contract pin\n" +
		"space: " + r.spaceID + "\n" +
		"from: " + r.system + "\n" +
		"to: [" + to + "]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-08-11T10:00:00Z\n" +
		"category: other\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"interim_behavior: " + to + " may continue other work until this is answered\n" +
		"classification: internal\n" +
		"acceptance_criteria: [\"the attached bytes are reviewed\"]\n" +
		"---\n" +
		"Please review the attached bytes.\n"
	path = filepath.Join(r.projectDir, ".a2a", "staging", id+".md")
	mustWrite(r.t, path, content)
	return path
}

// attachResultProbe is `a2a attach --json`'s wire shape (cmd_attach.go's
// attachJSONResult), narrowed to what these tests need.
type attachResultProbe struct {
	Draft      string `json:"draft"`
	Attachment struct {
		Ref          string `json:"ref"`
		Digest       string `json:"digest"`
		Verification string `json:"verification"`
		Retention    string `json:"retention"`
	} `json:"attachment"`
}

// attachJSON runs `a2a attach ... --json` and decodes its result.
func (r *hostRig) attachJSON(t *testing.T, draftPath, from, verification string) attachResultProbe {
	t.Helper()
	out := r.mustRun("attach", draftPath, "--from", from, "--verification", verification, "--json")
	var probe attachResultProbe
	if err := json.Unmarshal([]byte(out), &probe); err != nil {
		t.Fatalf("decode attach --json result: %v\nstdout=%s", err, out)
	}
	return probe
}

// --- AC1 + AC2: the composition nobody has driven yet -----------------

// TestAttachSubmitLandsWithoutContractOrFulfillsPin drives the exact
// sequence B32 proved refused: `a2a attach` on a work_request with neither
// `--contract` (there is no data-package contract in play at all) nor
// `--fulfills`, then `a2a submit` against the SAME staged draft, as two
// separate binary invocations — and it must land.
//
// AC2 is proven concretely, not merely inferred from a passing submit: the
// local source file `a2a attach` read is REMOVED from disk after attach and
// before submit. If submit's possession check needed the author's working
// tree, it would refuse here (the file is gone); it must not, because
// CheckAttachmentPossession (internal/space/possession.go) resolves
// exclusively through the mirror's own origin/main.
func TestAttachSubmitLandsWithoutContractOrFulfillsPin(t *testing.T) {
	t.Parallel()
	r := newHostRig(t, "axon", "axon", "beta")
	draftPath := r.stageAttachWorkRequest("XW-axon-20260811-at01", "beta")

	payload := filepath.Join(r.projectDir, "src", "payload.csv")
	mustMkdirAll(t, filepath.Dir(payload))
	mustWrite(t, payload, "a,b\n1,2\n")

	attached := r.attachJSON(t, draftPath, payload, "offered")
	blobID := attached.Attachment.Ref
	if !strings.HasPrefix(blobID, "BL-axon-") {
		t.Fatalf("attach ref = %q, want a minted BL-axon- id", blobID)
	}

	// The blob's own commit already landed as its own PR — spec 10 §1's
	// decided ordering: the bytes are in the space BEFORE the artifact
	// referencing them is ever submitted.
	if got := len(r.gh.PRs()); got != 1 {
		t.Fatalf("PRs after attach = %d, want exactly 1 (the blob's own commit)", got)
	}
	if got := gitOutput(t, r.fx.RemoteURL(), "show", "--name-only", "--pretty=format:", "main"); !strings.Contains(got, "axon/blobs/"+blobID+"/blob.json") {
		t.Fatalf("origin main does not carry the blob's own digest sidecar after attach:\n%s", got)
	}

	// AC2: remove the local source AFTER attach, BEFORE submit. A
	// possession check reading the author's working tree would refuse the
	// submit below; the space's own resolution path must not care.
	if err := os.Remove(payload); err != nil {
		t.Fatalf("remove local source: %v", err)
	}

	stdout, stderr, code := r.run("submit", draftPath)
	if code != 0 {
		t.Fatalf("submit after attach: exit %d, want 0 (this exact sequence must land)\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "space: attachment does not resolve through the space's own resolution path") {
		t.Fatalf("submit output carries the exact B32 refusal text, which spec 10 exists to remove:\nstdout=%s\nstderr=%s", stdout, stderr)
	}

	if got := len(r.gh.PRs()); got != 2 {
		t.Fatalf("PRs after attach+submit = %d, want exactly 2 (the blob's commit, then the artifact's)", got)
	}
	if got := gitOutput(t, r.fx.RemoteURL(), "show", "--name-only", "--pretty=format:", "main"); !strings.Contains(got, "XW-axon-20260811-at01.md") {
		t.Fatalf("origin main does not carry the submitted work_request:\n%s", got)
	}
}

// --- AC7, write side: a digest mismatch is refused at submit ------------

// TestAttachSubmitRefusesDeclaredDigestMismatch tampers the LOCAL draft's
// own declared `digest:` after a successful attach — a stale or hand-edited
// value that no longer matches what the space actually resolves the blob
// to, even though the `ref:` itself still resolves fine. AC7's write-side
// half: `ErrAttachmentDigestMismatch` must refuse this at submit.
func TestAttachSubmitRefusesDeclaredDigestMismatch(t *testing.T) {
	t.Parallel()
	r := newHostRig(t, "axon", "axon", "beta")
	draftPath := r.stageAttachWorkRequest("XW-axon-20260811-at02", "beta")

	payload := filepath.Join(r.projectDir, "payload.txt")
	mustWrite(t, payload, "immutable bytes\n")
	r.mustRun("attach", draftPath, "--from", payload, "--verification", "offered")

	before := mustReadFile(t, draftPath)
	wrongDigest := "sha256:" + strings.Repeat("0", 64)
	digestPattern := regexp.MustCompile(`digest: sha256:[a-f0-9]{64}`)
	after := digestPattern.ReplaceAllString(before, "digest: "+wrongDigest)
	if after == before {
		t.Fatalf("tamper regexp did not match the draft's declared digest:\n%s", before)
	}
	mustWrite(t, draftPath, after)

	stdout, stderr, code := r.run("submit", draftPath)
	if code == 0 {
		t.Fatalf("submit with a tampered declared digest succeeded, want a refusal\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "resolves to bytes that do not match its declared digest") {
		t.Fatalf("refusal is not the digest mismatch (AC7 write-side, ErrAttachmentDigestMismatch): stdout=%s stderr=%s", stdout, stderr)
	}
}

// --- AC3: fetch round-trips an attachment that was never contract-pinned -

// TestFetchRoundTripsUnpinnedAttachment is `fetch`'s own coverage-manifest
// evidence (coverage.go: a new top-level verb this phase shipped, with no
// row before this wave — TestE2ECoverageParity was red for exactly that
// reason, confirmed empirically before this file was written). It proves
// AC3's happy path: `a2a fetch <BL-id> --to <dir>` reproduces bytes for an
// attachment that carries no `--conforms-to` and was never submitted at
// all — spec 10 §6's own design note, "works for any attachment,
// contract-pinned or not" — read back by a SECOND system after only a
// sync, never the attaching system's own working tree.
func TestFetchRoundTripsUnpinnedAttachment(t *testing.T) {
	t.Parallel()
	r := newHostRig(t, "axon", "axon", "beta")
	reader := r.peer("beta")

	draftPath := r.stageAttachWorkRequest("XW-axon-20260811-at03", "beta")
	payload := filepath.Join(r.projectDir, "payload.bin")
	const body = "fetch me byte for byte\n"
	mustWrite(t, payload, body)

	attached := r.attachJSON(t, draftPath, payload, "none")
	blobID := attached.Attachment.Ref
	if !strings.HasPrefix(blobID, "BL-axon-") {
		t.Fatalf("attach ref = %q, want a minted BL-axon- id", blobID)
	}

	reader.mustRun("sync")
	dest := filepath.Join(reader.projectDir, "fetched")
	stdout, stderr, code := reader.run("fetch", blobID, "--to", dest, "--json")
	if code != 0 {
		t.Fatalf("fetch: exit %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var result struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode fetch result: %v\nstdout=%s", err, stdout)
	}
	if result.Outcome != "installed" {
		t.Fatalf("fetch outcome = %q, want %q", result.Outcome, "installed")
	}

	got, err := os.ReadFile(filepath.Join(dest, "payload.bin"))
	if err != nil {
		t.Fatalf("read fetched payload: %v", err)
	}
	if string(got) != body {
		t.Fatalf("fetched bytes = %q, want byte-identical %q", got, body)
	}
}

// --- AC3 + AC7 read side: a corrupted blob refuses fetch -----------------

// TestFetchRefusesCorruptedBlob mutates one byte of the committed blob
// DIRECTLY on origin, as its own commit (mirroring data_loop_test.go's own
// publishDataLoopContract idiom: a direct commit outside DeliverBlob, so the
// digest sidecar DeliverBlob wrote no longer matches what is actually
// committed beside it). AC3's own second half ("mutate one byte in the
// space and watch the fetch refuse") and AC7's read side
// (ErrBlobDigestMismatch) are the same proof.
func TestFetchRefusesCorruptedBlob(t *testing.T) {
	t.Parallel()
	r := newHostRig(t, "axon", "axon", "beta")
	reader := r.peer("beta")

	draftPath := r.stageAttachWorkRequest("XW-axon-20260811-at04", "beta")
	payload := filepath.Join(r.projectDir, "payload.bin")
	mustWrite(t, payload, "clean bytes\n")

	attached := r.attachJSON(t, draftPath, payload, "none")
	blobID := attached.Attachment.Ref

	mirror := t.TempDir()
	gitRun(t, "", "clone", r.fx.RemoteURL(), mirror)
	writeMirrorFile(t, mirror, "axon/blobs/"+blobID+"/payload.bin", "CORRUPTED bytes\n")
	gitRun(t, mirror, "add", "-A")
	gitRun(t, mirror, "commit", "-m", "corrupt the blob's own payload (test fixture, direct commit)")
	gitRun(t, mirror, "push", "origin", "main")

	reader.mustRun("sync")
	dest := filepath.Join(reader.projectDir, "fetched")
	stdout, stderr, code := reader.run("fetch", blobID, "--to", dest, "--json")
	if code == 0 {
		t.Fatalf("fetch of a corrupted blob succeeded, want a refusal\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "resolves to bytes that do not match its own committed digest sidecar") {
		t.Fatalf("refusal is not the digest-sidecar mismatch (AC7 read side, ErrBlobDigestMismatch): stdout=%s stderr=%s", stdout, stderr)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Errorf("fetch created the destination despite the refusal: %s", dest)
	}
}

// --- AC4: a ceiling refuses attach, before any network write ------------

// TestAttachRefusesEntryCountCeilingBeforeAnyNetworkWrite drives the ONE
// ceiling reachable through the real binary at a cheap size — an entry
// count over the bound, not gigabyte fixtures for the per-entry or
// aggregate byte ceilings (see this file's own package doc comment for why,
// and for why this is the DATAPACKAGE-layer bound rather than space's own
// ErrBlobTooManyEntries).
func TestAttachRefusesEntryCountCeilingBeforeAnyNetworkWrite(t *testing.T) {
	t.Parallel()
	r := newHostRig(t, "axon", "axon", "beta")
	draftPath := r.stageAttachWorkRequest("XW-axon-20260811-at05", "beta")

	sourceDir := filepath.Join(r.projectDir, "toomany")
	mustMkdirAll(t, sourceDir)
	const fileCount = 2049 // datapackage.DefaultBounds().MaxEntries is 2048
	for i := 0; i < fileCount; i++ {
		mustWrite(t, filepath.Join(sourceDir, fmt.Sprintf("f%04d.txt", i)), "x")
	}

	stdout, stderr, code := r.run("attach", draftPath, "--from", sourceDir, "--verification", "offered")
	if code == 0 {
		t.Fatalf("attach of a %d-entry source succeeded, want a refusal\nstdout=%s", fileCount, stdout)
	}
	msg := stdout + stderr
	if !strings.Contains(msg, "2049 entries") || !strings.Contains(msg, "2048 entry bound") {
		t.Fatalf("refusal does not name the entry-count ceiling and its value: %s", msg)
	}

	if got := len(r.gh.PRs()); got != 0 {
		t.Fatalf("PRs after a refused attach = %d, want 0 (refused before any network write)", got)
	}
	if got := mustReadFile(t, draftPath); strings.Contains(got, "attachments:") {
		t.Fatalf("draft was mutated by a refused attach:\n%s", got)
	}
}
