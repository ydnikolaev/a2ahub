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
// path (Store.Show → ShowResult.Attachments → ShowAttachmentLines): a
// committed attachment carrying verification:none must print its own
// claim, never the missing-package vocabulary (internal/cache/delivery.go's
// own "could not be resolved" text — asserted absent by name below, not by
// paraphrase, so the brief's own mutation — "fall back to the
// missing-package rendering" — is what actually reddens this).
//
// Cross-surface sameness (top-level brief: "the two must say the SAME
// thing about the same attachment") is asserted directly, not by two
// hardcoded literals that could drift: the expected text is computed via
// datapackage.ProjectAttachmentClaim, the exact function
// internal/cache/store.go calls to populate ShowResult.Attachments, and
// the CLI's own stdout line must contain it.
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

	wantClaim := datapackage.ProjectAttachmentClaim(datapackage.AttachmentManifestEntry{
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

// TestShowCommand_AttachmentVerificationNone_JSONStaysUnaffected guards the
// --json path the same way: the machine-readable body must not carry the
// missing-package text either.
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
		ID          string `json:"id"`
		Attachments []struct {
			Ref               string `json:"ref"`
			VerificationClaim string `json:"verification_claim"`
			Lapsed            bool   `json:"lapsed"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v (stdout=%s)", err, out.String())
	}
	if decoded.ID != "XW-axon-20260801-attach2" {
		t.Fatalf("got id %q, want XW-axon-20260801-attach2", decoded.ID)
	}
	if strings.Contains(out.String(), "could not be resolved") {
		t.Fatalf("stdout %q renders verification:none via the missing-package vocabulary — exactly what AC4 removes", out.String())
	}
	// AC4/§11's own point: --json now CARRIES the claim (it did not
	// before this wave — `a2a show`'s human-text branch was the only
	// consumer). Guard the positive, not just the absence of the old text.
	if len(decoded.Attachments) != 1 || decoded.Attachments[0].Ref != "sha256:bbbb" {
		t.Fatalf("decoded.Attachments = %+v, want exactly one entry naming sha256:bbbb", decoded.Attachments)
	}
	if decoded.Attachments[0].VerificationClaim == "" {
		t.Fatalf("decoded.Attachments[0].VerificationClaim is empty, want a non-empty claim")
	}
}

// TestShowCommand_JSONOmitsAttachmentsKeyWhenThereAreNone is the additive
// half of the top-level brief's "the new key is additive and omitempty"
// constraint: an artifact carrying no attachments[] must not gain an
// `"attachments"` key in `a2a show --json` at all — not `"attachments":
// null` or `"attachments": []` — so this wave's change is byte-invisible
// to any existing `--json` consumer of an attachment-free artifact.
func TestShowCommand_JSONOmitsAttachmentsKeyWhenThereAreNone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	cliWriteArtifact(t, dir, "axon/requires/XR-axon-nokey.md", map[string]any{
		"schema": "envelope/v1", "id": "XR-axon-nokey", "type": "requirement", "title": "target",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": base.Format(time.RFC3339),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "target body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000022", cliEvt("XR-axon-nokey", "publish", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewShowCommand(store)
	io, out, errOut := newIO()

	code := cmd.Run(context.Background(), []string{"--json", "XR-axon-nokey"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if strings.Contains(out.String(), `"attachments"`) {
		t.Fatalf("stdout %s carries an \"attachments\" key for an artifact with none — omitempty regression", out.String())
	}
}

// TestShowAttachmentLines_LapsedRetentionNamesTheDate is a fixture-level
// unit check of ShowAttachmentLines' own formatting, kept alongside
// TestShowCommand_AttachmentLapseIsReachableFromACommittedArtifact (below,
// the real end-to-end AC5 proof) rather than replaced by it: this test
// isolates line-formatting from derivation — it hand-builds the
// AttachmentClaim ShowAttachmentLines receives, so a formatting regression
// here is distinguishable from a derivation regression in
// datapackage.ProjectAttachmentClaim (already covered by
// internal/datapackage/claim_test.go) or in the cache-side decode (covered
// by the end-to-end test below).
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
	claim := datapackage.ProjectAttachmentClaim(entry, now)
	if claim.LapseClaim == "" {
		t.Fatalf("test fixture bug: datapackage.ProjectAttachmentClaim did not compute a lapse claim for entry %+v at now=%s", entry, now)
	}

	lines := cli.ShowAttachmentLines([]datapackage.AttachmentClaim{claim})
	if len(lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], claim.LapseClaim) {
		t.Fatalf("line %q does not contain the derived lapse claim %q", lines[0], claim.LapseClaim)
	}
	if !strings.Contains(lines[0], "2026-08-01") {
		t.Fatalf("line %q does not name the lapse date", lines[0])
	}
	if strings.Contains(lines[0], "datapackage: Fetch") || strings.Contains(lines[0], "ErrExpired") {
		t.Fatalf("line %q leaks a raw fetch error instead of the reader-facing claim", lines[0])
	}
}

// TestShowCommand_AttachmentLapseIsReachableFromACommittedArtifact is spec
// 04 §8 AC5's ARTIFACT-LEVEL proof — the thing the top-level brief exists
// to make possible: a real work_request draft, carrying `retention` and a
// past `expires_at` exactly as `a2a attach` writes them, run through
// cache.NewStore + cli.NewShowCommand.Run(), asserting the lapse sentence
// on real stdout.
//
// Before this wave, decodeShowAttachments never read expires_at off a
// committed artifact, so this exact scenario always produced Lapsed=false
// — the Lapsed branch was reachable only via ShowAttachmentLines/
// html.ProjectAttachmentClaim called directly against a hand-built
// AttachmentManifestEntry (the test above), never from Run() on real
// bytes. This test is what closes that gap; see this wave's own report for
// the seeded-red proof (deleting the cache-side expires_at read reddens
// this test, not the fixture-level one above).
func TestShowCommand_AttachmentLapseIsReachableFromACommittedArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	// created well before the expiry so the artifact's own `created`
	// timestamp could never be mistaken for the lapse anchor.
	created := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	const expiresAt = "2026-08-01T00:00:00Z"
	// The store's own injected clock is what decides "lapsed", not real
	// wall-clock time (top-level brief: "using the store's own injected
	// clock rather than time.Now()"). Set it well past expiresAt.
	storeNow := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)

	showWorkRequestWithAttachments(t, dir, "XW-axon-20260701-lapsed", created, []map[string]any{
		{
			"ref": "sha256:dddd", "digest": "sha256:dddd",
			"verification": datapackage.VerificationOffered, "retention": "1h",
			"expires_at": expiresAt,
		},
	})

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return storeNow }, 0)
	cmd := cli.NewShowCommand(store)
	io, out, errOut := newIO()

	code := cmd.Run(context.Background(), []string{"XW-axon-20260701-lapsed"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	const wantSentence = "references bytes whose retention lapsed on 2026-08-01"
	stdout := out.String()
	if !strings.Contains(stdout, wantSentence) {
		t.Fatalf("stdout %q does not contain AC5's lapse sentence %q — the Lapsed branch is not reachable from a committed artifact", stdout, wantSentence)
	}
	if strings.Contains(stdout, "datapackage: Fetch") || strings.Contains(stdout, "ErrExpired") {
		t.Fatalf("stdout %q leaks a raw fetch error instead of the reader-facing claim", stdout)
	}

	// --json must carry the same two derived claims.
	io2, out2, errOut2 := newIO()
	code2 := cmd.Run(context.Background(), []string{"--json", "XW-axon-20260701-lapsed"}, io2)
	if code2 != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code2, out2.String(), errOut2.String())
	}
	var decoded struct {
		Attachments []struct {
			Lapsed     bool   `json:"lapsed"`
			LapsedOn   string `json:"lapsed_on"`
			LapseClaim string `json:"lapse_claim"`
			ExpiresAt  string `json:"expires_at"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(out2.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v (stdout=%s)", err, out2.String())
	}
	if len(decoded.Attachments) != 1 {
		t.Fatalf("decoded.Attachments = %+v, want exactly one entry", decoded.Attachments)
	}
	got := decoded.Attachments[0]
	if !got.Lapsed || got.LapsedOn != "2026-08-01" || got.LapseClaim != wantSentence || got.ExpiresAt != expiresAt {
		t.Fatalf("decoded.Attachments[0] = %+v, want Lapsed=true LapsedOn=2026-08-01 LapseClaim=%q ExpiresAt=%q", got, wantSentence, expiresAt)
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
