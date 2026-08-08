package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"gopkg.in/yaml.v3"
)

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

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds())
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"XW-axon-20260808-0001", "--from", sourcePath, "--verification", "offered",
	}, io)
	if code != 0 {
		t.Fatalf("attach: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
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
	if got.Ref != wantDigest {
		t.Fatalf("attachments[0].ref = %q, want %q (content-addressed: ref IS the digest)", got.Ref, wantDigest)
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

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds())
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
	cmd := cli.NewAttachCommand(stagingDir, tinyBounds)
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

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds())
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

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds())
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

	cmd := cli.NewAttachCommand(stagingDir, datapackage.DefaultBounds())
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
}
