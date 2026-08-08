package html

import (
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// TestProjectAttachmentClaim_VerificationNoneRendersItsOwnClaim is spec 04
// (agent-exchange-2026-08) §8 AC4: "verification: none renders as 'no
// verdict is defined', never as a missing package." The counter-assertion
// (asserting the missing-package text is ABSENT) is what makes the
// mutation named in this wave's brief — "fall back to the missing-package
// rendering" — actually red: it names the literal text
// internal/cache/delivery.go:241 produces for an unresolved data-package
// delivery ("could not be resolved"), not a paraphrase, so a regression
// that reuses that vocabulary for an attachment is caught by name.
func TestProjectAttachmentClaim_VerificationNoneRendersItsOwnClaim(t *testing.T) {
	t.Parallel()

	entry := datapackage.AttachmentManifestEntry{
		Attachment: datapackage.Attachment{
			Ref: "sha256:aaaa", Digest: "sha256:aaaa",
			Verification: datapackage.VerificationNone,
			Retention:    datapackage.RetentionPinned,
		},
	}

	claim := ProjectAttachmentClaim(entry, time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC))

	const want = "no verdict is defined for these bytes"
	if claim.VerificationClaim != want {
		t.Fatalf("VerificationClaim = %q, want %q", claim.VerificationClaim, want)
	}
	if strings.Contains(claim.VerificationClaim, "could not be resolved") {
		t.Fatalf("VerificationClaim %q renders verification:none as a missing/unresolved package — the exact confusion AC4 removes", claim.VerificationClaim)
	}
	if strings.Contains(claim.VerificationClaim, "missing") || strings.Contains(claim.VerificationClaim, "not found") {
		t.Fatalf("VerificationClaim %q reads as a missing package, not a deliberate claim", claim.VerificationClaim)
	}
	if claim.Lapsed {
		t.Fatalf("pinned retention must never report Lapsed=true, got %+v", claim)
	}
}

// TestProjectAttachmentClaim_LapsedRetentionNamesTheDate is spec 04 §8
// AC5: "a lapsed attachment reads as 'references bytes whose retention
// lapsed on <date>', never as a failed fetch." The brief's own mutation —
// "return the raw expiry error unwrapped, as today" — is what the second
// assertion below guards: the claim must never surface
// datapackage.ErrExpired's own wrapped text (see errors.go /
// datapackage.Fetch's error string, which names "datapackage: Fetch"),
// which is the "failed fetch" framing AC5 exists to replace.
func TestProjectAttachmentClaim_LapsedRetentionNamesTheDate(t *testing.T) {
	t.Parallel()

	entry := datapackage.AttachmentManifestEntry{
		Attachment: datapackage.Attachment{
			Ref: "sha256:bbbb", Digest: "sha256:bbbb",
			Verification: datapackage.VerificationRequired,
			Retention:    "1h",
		},
		ExpiresAt: "2026-08-01T00:00:00Z",
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	claim := ProjectAttachmentClaim(entry, now)

	if !claim.Lapsed {
		t.Fatalf("claim.Lapsed = false, want true for an ExpiresAt in the past of now")
	}
	const wantDate = "2026-08-01"
	if claim.LapsedOn != wantDate {
		t.Fatalf("claim.LapsedOn = %q, want %q", claim.LapsedOn, wantDate)
	}
	const want = "references bytes whose retention lapsed on 2026-08-01"
	if claim.LapseClaim != want {
		t.Fatalf("claim.LapseClaim = %q, want %q", claim.LapseClaim, want)
	}
	if !strings.Contains(claim.LapseClaim, wantDate) {
		t.Fatalf("claim.LapseClaim %q does not name the lapse date", claim.LapseClaim)
	}
	if strings.Contains(claim.LapseClaim, "datapackage: Fetch") || strings.Contains(claim.LapseClaim, "ErrExpired") {
		t.Fatalf("claim.LapseClaim %q leaks a raw fetch error instead of the reader-facing claim", claim.LapseClaim)
	}
}

// TestProjectAttachmentClaim_NotYetLapsedStaysUnlapsed guards the other
// side of AC5's date comparison: an attachment whose resolved expiry is
// still in the future must never be reported as lapsed.
func TestProjectAttachmentClaim_NotYetLapsedStaysUnlapsed(t *testing.T) {
	t.Parallel()

	entry := datapackage.AttachmentManifestEntry{
		Attachment: datapackage.Attachment{
			Ref: "sha256:cccc", Digest: "sha256:cccc",
			Verification: datapackage.VerificationOffered,
			Retention:    "168h",
		},
		ExpiresAt: "2026-08-15T00:00:00Z",
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	claim := ProjectAttachmentClaim(entry, now)

	if claim.Lapsed {
		t.Fatalf("claim.Lapsed = true, want false: ExpiresAt %s is still in now's future", entry.ExpiresAt)
	}
	if claim.LapseClaim != "" {
		t.Fatalf("claim.LapseClaim = %q, want empty when not lapsed", claim.LapseClaim)
	}
}

// TestProjectAttachmentClaim_PinnedRetentionNeverLapses is AC6's own
// boundary condition, asserted here because ProjectAttachmentClaim is the
// one place that reads ExpiresAt: retention:pinned resolves to
// ExpiresAt == "" (datapackage.ResolveAttachmentExpiry's own documented
// behaviour), and this projection must treat empty exactly as "never
// lapses", not as an unparsable date that happens to also read as
// unlapsed for the wrong reason.
func TestProjectAttachmentClaim_PinnedRetentionNeverLapses(t *testing.T) {
	t.Parallel()

	entry := datapackage.AttachmentManifestEntry{
		Attachment: datapackage.Attachment{
			Ref: "sha256:dddd", Digest: "sha256:dddd",
			Verification: datapackage.VerificationNone,
			Retention:    datapackage.RetentionPinned,
		},
	}
	// A full year later — pinned must still never lapse.
	now := time.Date(2027, 8, 8, 0, 0, 0, 0, time.UTC)

	claim := ProjectAttachmentClaim(entry, now)

	if claim.Lapsed {
		t.Fatalf("pinned retention reported Lapsed=true a year later, want false")
	}
}
