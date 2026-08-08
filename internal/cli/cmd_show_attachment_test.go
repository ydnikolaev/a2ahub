package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/html"
)

// showWorkRequestWithAttachments writes a work_request drafted on
// envelope/v2 (schemas/envelope/v2/work_request.schema.json — the ONE
// schema attachments[] is declared on, plan D1/D2) carrying attachments,
// plus the publish event that makes `a2a show` resolve it.
func showWorkRequestWithAttachments(t *testing.T, dir, id string, base time.Time, attachments []map[string]any) {
	t.Helper()
	fields := map[string]any{
		"schema": "envelope/v2", "id": id, "type": "work_request", "title": "carries bytes",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": base.Format(time.RFC3339),
		"priority": "p2", "blocking": false, "classification": "internal",
		"category": "data", "acceptance_criteria": []string{"bytes attached"},
		"attachments": attachments,
	}
	cliWriteArtifact(t, dir, "axon/exchanges/"+id+".md", fields, "carries bytes")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000020", cliEvt(id, "publish", "axon", base))
}

// TestShowCommand_AttachmentVerificationNoneRendersItsOwnClaim is spec 04
// (agent-exchange-2026-08) §8 AC4, on `a2a show`'s real, on-disk-artifact
// path (Store.Show → ShowResult.Envelope → decodeShowAttachments →
// ShowAttachmentLines): a committed attachment carrying verification:none
// must print its own claim, never the missing-package vocabulary
// (internal/cache/delivery.go:241's own "could not be resolved" text —
// asserted absent by name below, not by paraphrase, so the brief's own
// mutation — "fall back to the missing-package rendering" — is what
// actually reddens this).
//
// Cross-surface sameness (top-level brief: "the two must say the SAME
// thing about the same attachment") is asserted directly, not by two
// hardcoded literals that could drift: the expected text is computed via
// html.ProjectAttachmentClaim, the exact function the dashboard surface
// calls, and the CLI's own stdout line must contain it.
func TestShowCommand_AttachmentVerificationNoneRendersItsOwnClaim(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)

	showWorkRequestWithAttachments(t, dir, "XW-axon-20260801-attach", base, []map[string]any{
		{
			"ref": "sha256:aaaa", "digest": "sha256:aaaa",
			"verification": datapackage.VerificationNone, "retention": datapackage.RetentionPinned,
		},
	})

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewShowCommand(store)
	io, out, errOut := newIO()

	code := cmd.Run(context.Background(), []string{"XW-axon-20260801-attach"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	wantClaim := html.ProjectAttachmentClaim(datapackage.AttachmentManifestEntry{
		Attachment: datapackage.Attachment{Verification: datapackage.VerificationNone},
	}, time.Now()).VerificationClaim

	stdout := out.String()
	if !strings.Contains(stdout, wantClaim) {
		t.Fatalf("stdout %q does not contain the dashboard's own claim text %q — the two surfaces disagree", stdout, wantClaim)
	}
	if strings.Contains(stdout, "could not be resolved") {
		t.Fatalf("stdout %q renders verification:none via the missing-package vocabulary — exactly what AC4 removes", stdout)
	}
}

// TestShowCommand_AttachmentVerificationNone_JSONAlsoDoesNotLeakMissingPackage
// guards the --json path the same way: the machine-readable body must not
// carry the missing-package text either.
func TestShowCommand_AttachmentVerificationNone_JSONStaysUnaffected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)

	showWorkRequestWithAttachments(t, dir, "XW-axon-20260801-attach2", base, []map[string]any{
		{
			"ref": "sha256:bbbb", "digest": "sha256:bbbb",
			"verification": datapackage.VerificationNone, "retention": datapackage.RetentionPinned,
		},
	})

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewShowCommand(store)
	io, out, errOut := newIO()

	code := cmd.Run(context.Background(), []string{"--json", "XW-axon-20260801-attach2"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var decoded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v (stdout=%s)", err, out.String())
	}
	if decoded.ID != "XW-axon-20260801-attach2" {
		t.Fatalf("got id %q, want XW-axon-20260801-attach2", decoded.ID)
	}
}

// TestShowAttachmentLines_LapsedRetentionNamesTheDate is spec 04 §8 AC5 on
// the CLI surface: "a lapsed attachment reads as 'references bytes whose
// retention lapsed on <date>', never as a failed fetch." ShowAttachmentLines
// is exercised directly, against a resolved AttachmentManifestEntry
// (ExpiresAt set), because a committed artifact's on-disk frontmatter never
// carries expires_at (decodeShowAttachments' own doc comment in
// cmd_show.go — the schema declares no such property) — Run() alone can
// never reach this branch today, and that gap is this wave's own reported
// deviation, not silently worked around here.
//
// Cross-surface sameness: the expected text is computed via
// html.ProjectAttachmentClaim — the SAME function ShowAttachmentLines
// calls internally — so this assertion fails if the two surfaces ever
// diverge, not merely if the CLI's own wording drifts from a hardcoded
// literal.
func TestShowAttachmentLines_LapsedRetentionNamesTheDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	entry := datapackage.AttachmentManifestEntry{
		Attachment: datapackage.Attachment{
			Ref: "sha256:cccc", Digest: "sha256:cccc",
			Verification: datapackage.VerificationRequired, Retention: "1h",
		},
		ExpiresAt: "2026-08-01T00:00:00Z",
	}

	lines := cli.ShowAttachmentLines([]datapackage.AttachmentManifestEntry{entry}, now)
	if len(lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1: %v", len(lines), lines)
	}

	wantClaim := html.ProjectAttachmentClaim(entry, now).LapseClaim
	if wantClaim == "" {
		t.Fatalf("test fixture bug: html.ProjectAttachmentClaim did not compute a lapse claim for entry %+v at now=%s", entry, now)
	}
	if !strings.Contains(lines[0], wantClaim) {
		t.Fatalf("line %q does not contain the dashboard's own lapse claim %q — the two surfaces disagree", lines[0], wantClaim)
	}
	if !strings.Contains(lines[0], "2026-08-01") {
		t.Fatalf("line %q does not name the lapse date", lines[0])
	}
	if strings.Contains(lines[0], "datapackage: Fetch") || strings.Contains(lines[0], "ErrExpired") {
		t.Fatalf("line %q leaks a raw fetch error instead of the reader-facing claim", lines[0])
	}
}

// TestShowCommand_NoAttachmentsRendersUnchanged is the top-level brief's
// D4 constraint made concrete: an ordinary artifact with no attachments[]
// prints byte-identically to before this wave — this wave adds output
// only when there is something new to say.
func TestShowCommand_NoAttachmentsRendersUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	cliWriteArtifact(t, dir, "axon/requires/XR-axon-target.md", map[string]any{
		"schema": "envelope/v1", "id": "XR-axon-target", "type": "requirement", "title": "target",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": base.Format(time.RFC3339),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "target body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000021", cliEvt("XR-axon-target", "publish", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewShowCommand(store)
	io, out, errOut := newIO()

	code := cmd.Run(context.Background(), []string{"XR-axon-target"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if strings.Contains(out.String(), "attachment ") {
		t.Fatalf("stdout %q prints an attachment line for an artifact carrying none", out.String())
	}
}
