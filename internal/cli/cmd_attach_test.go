package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// fakeAttachOps is the test double for cli.AttachOperations: it records the
// payload DeliverBlob was called with and returns a fixed blob id, so tests
// can assert on both the "network write happened" side effect and the
// draft's own resulting attachments[].ref without a real space.WriteFunnel.
type fakeAttachOps struct {
	blobID  string
	write   space.WriteResult
	err     error
	calls   int
	payload map[string][]byte
}

func (f *fakeAttachOps) DeliverBlob(_ context.Context, req cli.AttachBlobRequest) (cli.AttachBlobResult, error) {
	f.calls++
	f.payload = req.Payload
	if f.err != nil {
		return cli.AttachBlobResult{}, f.err
	}
	return cli.AttachBlobResult{BlobID: f.blobID, Write: f.write}, nil
}

func newFakeAttachOps(blobID string) *fakeAttachOps {
	return &fakeAttachOps{
		blobID: blobID,
		write:  space.WriteResult{State: space.WriteStatePendingMerge, Branch: "a2a/axon/attach/" + blobID, PRURL: "https://example.invalid/pr/1"},
	}
}

// poisonAttachOps fails the test the moment DeliverBlob is called — used by
// every test asserting a LOCAL refusal (a missing source, bounds exceeded,
// a non-possession draft, a missing --verification): none of those must
// ever reach the network write (advisor's "every local refusal precedes the
// network write" — this command's own Run doc comment).
type poisonAttachOps struct{ t *testing.T }

func (p poisonAttachOps) DeliverBlob(context.Context, cli.AttachBlobRequest) (cli.AttachBlobResult, error) {
	p.t.Helper()
	p.t.Fatal("DeliverBlob must not be called: this refusal must be entirely local, before any network write")
	return cli.AttachBlobResult{}, fmt.Errorf("unreachable")
}

// attachTestDraft is a minimal, syntactically-valid envelope/v2 work_request
// draft — real enough for cmd_attach.go's own type/schema guard (it reads
// only `type` and `schema`), not asserted schema-valid end-to-end (that is
// internal/validate's own territory, out of this file's scope).
const attachTestDraft = `---
schema: envelope/v2
id: XW-axon-20260808-0001
type: work_request
title: exercise attach
category: data
acceptance_criteria:
  - something happens
from: axon
to:
  - other
---
Body text.
`

func writeAttachDraft(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write draft %s: %v", path, err)
	}
	return path
}

type attachDoc struct {
	Attachments []struct {
		Ref          string `yaml:"ref"`
		Digest       string `yaml:"digest"`
		Role         string `yaml:"role"`
		ConformsTo   string `yaml:"conforms_to"`
		Verification string `yaml:"verification"`
		Retention    string `yaml:"retention"`
	} `yaml:"attachments"`
}

// TestAttachSuccessWritesEntryWithRightDigest is this wave's own AC: a
// successful `a2a attach` writes attachments[] onto the draft with the
// digest internal/artifact.Digest computes over the exact source bytes —
// not a different, invented identifier.
func TestAttachSuccessWritesEntryWithRightDigest(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	draftPath := writeAttachDraft(t, stagingDir, "XW-axon-20260808-0001.md", attachTestDraft)

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "payload.bin")
	payload := []byte("the exact bytes being attached")
	if err := os.WriteFile(sourcePath, payload, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	wantDigest := artifact.Digest(payload)
	ops := newFakeAttachOps("BL-axon-20260811-ab12")

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds(), ops)
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"XW-axon-20260808-0001", "--from", sourcePath, "--verification", "offered",
	}, io)
	if code != 0 {
		t.Fatalf("attach: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if ops.calls != 1 {
		t.Fatalf("DeliverBlob calls = %d, want 1", ops.calls)
	}
	if len(ops.payload) != 1 {
		t.Fatalf("DeliverBlob payload = %v, want exactly 1 entry", ops.payload)
	}
	for _, raw := range ops.payload {
		if string(raw) != string(payload) {
			t.Fatalf("DeliverBlob payload bytes = %q, want %q", raw, payload)
		}
	}
	// stdout must say the write happened and name the blob id — attach
	// stopped being a local-only draft edit (this command's own doc
	// comment).
	if !strings.Contains(out.String(), ops.blobID) || !strings.Contains(out.String(), "written to the space") {
		t.Fatalf("stdout must name the blob id and that it was written to the space, got %q", out.String())
	}

	raw, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("read draft back: %v", err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("parse frontmatter: %v", err)
	}
	var doc attachDoc
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		t.Fatalf("decode frontmatter: %v", err)
	}
	if len(doc.Attachments) != 1 {
		t.Fatalf("want 1 attachment, got %d (%+v)", len(doc.Attachments), doc.Attachments)
	}
	got := doc.Attachments[0]
	if got.Digest != wantDigest {
		t.Fatalf("attachments[0].digest = %q, want %q", got.Digest, wantDigest)
	}
	if got.Ref != ops.blobID {
		t.Fatalf("attachments[0].ref = %q, want %q (the minted blob id — spec 10 §2, no longer the digest)", got.Ref, ops.blobID)
	}
	if got.Ref == got.Digest {
		t.Fatalf("ref and digest must no longer coincide now that ref is a minted BL- id: both are %q", got.Ref)
	}
	if got.Verification != "offered" {
		t.Fatalf("attachments[0].verification = %q, want %q", got.Verification, "offered")
	}
	// Asserted against the literal spec value, not cli.AttachDefaultRetention
	// itself — comparing to that constant would be tautological against
	// whatever the production code happens to hold.
	if got.Retention != "168h" {
		t.Fatalf("attachments[0].retention = %q, want default %q", got.Retention, "168h")
	}
	// The body must survive untouched — attach only ever edits frontmatter.
	if !strings.Contains(string(fm.Body), "Body text.") {
		t.Fatalf("draft body was altered: %q", fm.Body)
	}
}

// TestAttachMissingSourceIsCleanRefusal is this wave's own AC: a --from
// path that does not exist is a clean, non-zero refusal naming the path —
// never a panic.
func TestAttachMissingSourceIsCleanRefusal(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	writeAttachDraft(t, stagingDir, "XW-axon-20260808-0002.md", attachTestDraft)

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds(), poisonAttachOps{t: t})
	io, _, errOut := newIO()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("attach panicked on a missing source: %v", r)
		}
	}()
	code := cmd.Run(context.Background(), []string{
		"XW-axon-20260808-0002", "--from", filepath.Join(stagingDir, "does-not-exist"), "--verification", "none",
	}, io)
	if code == 0 {
		t.Fatalf("want non-zero exit for a missing source, got 0")
	}
	if !strings.Contains(errOut.String(), "does-not-exist") {
		t.Fatalf("refusal must name the offending path, got stderr=%q", errOut.String())
	}
}

// TestAttachBoundsRefusalIsUsableMessage exercises wave B's own
// bounds.CheckEntryBytes refusal, surfaced through this verb as a message
// naming the actual and the allowed size — never a raw/unwrapped error, and
// never silently accepted. A tiny injected Bounds keeps the fixture small
// (no 128 MiB file is written to prove the same code path).
func TestAttachBoundsRefusalIsUsableMessage(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	writeAttachDraft(t, stagingDir, "XW-axon-20260808-0003.md", attachTestDraft)

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "payload.bin")
	if err := os.WriteFile(sourcePath, []byte("this is more than four bytes"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	tinyBounds := datapackage.DefaultBounds()
	tinyBounds.MaxEntryBytes = 4
	cmd := cli.NewAttachCommand(stagingDir, tinyBounds, poisonAttachOps{t: t})
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"XW-axon-20260808-0003", "--from", sourcePath, "--verification", "required",
	}, io)
	if code == 0 {
		t.Fatalf("want non-zero exit for an oversized source, got 0")
	}
	msg := errOut.String()
	if !strings.Contains(msg, "exceeds") || !strings.Contains(msg, "4 byte") {
		t.Fatalf("refusal must name the bound exceeded, got stderr=%q", msg)
	}

	// Refused: the draft must be left untouched, not partially written.
	raw, err := os.ReadFile(filepath.Join(stagingDir, "XW-axon-20260808-0003.md"))
	if err != nil {
		t.Fatalf("read draft back: %v", err)
	}
	if string(raw) != attachTestDraft {
		t.Fatalf("draft was modified by a refused attach:\n%s", raw)
	}
}

// TestAttachRefusesNonPossessionDraft guards the type/schema check: writing
// attachments[] onto a draft whose type or generation does not declare that
// field would pass this command silently and then fail SCH-003 at the next
// `a2a validate` — exactly the drift AC1/Q1 exist to catch before any pull
// request, not after.
func TestAttachRefusesNonPossessionDraft(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	const v1Draft = `---
schema: envelope/v1
id: XW-axon-20260808-0004
type: work_request
title: v1 draft, no attachments field
category: data
---
Body text.
`
	writeAttachDraft(t, stagingDir, "XW-axon-20260808-0004.md", v1Draft)

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "payload.bin")
	if err := os.WriteFile(sourcePath, []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds(), poisonAttachOps{t: t})
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"XW-axon-20260808-0004", "--from", sourcePath, "--verification", "none",
	}, io)
	if code == 0 {
		t.Fatalf("want non-zero exit for a non-v2/work_request draft, got 0")
	}
	if !strings.Contains(errOut.String(), "envelope/v1") {
		t.Fatalf("refusal must name the actual schema generation, got stderr=%q", errOut.String())
	}
}

// TestAttachMissingVerificationIsUsageError is the fused-axes guard this
// wave's own brief names: attach must never synthesize a verification
// claim, so an omitted --verification is a usage error, not a default.
func TestAttachMissingVerificationIsUsageError(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	writeAttachDraft(t, stagingDir, "XW-axon-20260808-0005.md", attachTestDraft)

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "payload.bin")
	if err := os.WriteFile(sourcePath, []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds(), poisonAttachOps{t: t})
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"XW-axon-20260808-0005", "--from", sourcePath}, io)
	if code != 2 {
		t.Fatalf("want usage exit 2 for a missing --verification, got %d", code)
	}
}

// TestAttachJSONOutputCarriesDigestAndDraft exercises --json's shape: the
// draft path and the attached digest are both present, machine-readable.
func TestAttachJSONOutputCarriesDigestAndDraft(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	draftPath := writeAttachDraft(t, stagingDir, "XW-axon-20260808-0006.md", attachTestDraft)

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "payload.bin")
	payload := []byte("json output payload")
	if err := os.WriteFile(sourcePath, payload, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	wantDigest := artifact.Digest(payload)
	ops := newFakeAttachOps("BL-axon-20260811-cd34")

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds(), ops)
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"XW-axon-20260808-0006", "--from", sourcePath, "--verification", "none", "--json",
	}, io)
	if code != 0 {
		t.Fatalf("attach --json: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var result struct {
		Draft      string `json:"draft"`
		Attachment struct {
			Ref    string `json:"ref"`
			Digest string `json:"digest"`
		} `json:"attachment"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode --json output: %v (stdout=%s)", err, out.String())
	}
	if result.Draft != draftPath {
		t.Fatalf("draft = %q, want %q", result.Draft, draftPath)
	}
	if result.Attachment.Digest != wantDigest {
		t.Fatalf("attachment.digest = %q, want %q", result.Attachment.Digest, wantDigest)
	}
	if result.Attachment.Ref != ops.blobID {
		t.Fatalf("attachment.ref = %q, want %q (the minted blob id)", result.Attachment.Ref, ops.blobID)
	}
}

// TestAttachDeliverBlobFailureIsRefused proves the network write's own
// error is surfaced as a refusal (never a panic, never a silently-written
// draft) and that the draft is left untouched on that failure — the network
// write is the LAST thing that can fail before anything is written locally,
// so a failure there must roll back nothing (there is nothing to roll back:
// the draft was never touched).
func TestAttachDeliverBlobFailureIsRefused(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	writeAttachDraft(t, stagingDir, "XW-axon-20260808-0007.md", attachTestDraft)

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "payload.bin")
	if err := os.WriteFile(sourcePath, []byte("bytes that never make it to the space"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	ops := newFakeAttachOps("BL-axon-20260811-ef56")
	ops.err = fmt.Errorf("simulated offline transport failure")

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds(), ops)
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"XW-axon-20260808-0007", "--from", sourcePath, "--verification", "required",
	}, io)
	if code == 0 {
		t.Fatalf("want non-zero exit when the space write fails, got 0")
	}
	if !strings.Contains(errOut.String(), "simulated offline transport failure") {
		t.Fatalf("refusal must carry the underlying transport error, got stderr=%q", errOut.String())
	}

	raw, err := os.ReadFile(filepath.Join(stagingDir, "XW-axon-20260808-0007.md"))
	if err != nil {
		t.Fatalf("read draft back: %v", err)
	}
	if string(raw) != attachTestDraft {
		t.Fatalf("draft was modified even though the network write failed:\n%s", raw)
	}
}

// TestAttachServiceUnavailableWhenOpsNil mirrors DataCommand's own nil-ops
// degraded-registration precedent (cmd_data.go): a nil AttachOperations
// must not panic, and must refuse with a clear message once every local
// check has already passed.
func TestAttachServiceUnavailableWhenOpsNil(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	writeAttachDraft(t, stagingDir, "XW-axon-20260808-0008.md", attachTestDraft)

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "payload.bin")
	if err := os.WriteFile(sourcePath, []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds(), nil)
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"XW-axon-20260808-0008", "--from", sourcePath, "--verification", "required",
	}, io)
	if code == 0 {
		t.Fatalf("want non-zero exit for a nil ops, got 0")
	}
	if !strings.Contains(errOut.String(), "not configured") {
		t.Fatalf("refusal must say the service is not configured, got stderr=%q", errOut.String())
	}
}
