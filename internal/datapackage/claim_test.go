package datapackage

import (
	"strings"
	"testing"
	"time"
)

// TestProjectAttachmentClaim_VerificationNoneRendersItsOwnClaim is spec 04
// (agent-exchange-2026-08) §8 AC4: "verification: none renders as 'no
// verdict is defined', never as a missing package." Moved here from
// internal/html/delivery.go by the §11 amendment; the original assertions
// carry over unchanged.
func TestProjectAttachmentClaim_VerificationNoneRendersItsOwnClaim(t *testing.T) {
	t.Parallel()

	entry := AttachmentManifestEntry{
		Attachment: Attachment{
			Ref: "sha256:aaaa", Digest: "sha256:aaaa",
			Verification: VerificationNone,
			Retention:    RetentionPinned,
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
	if claim.Lapsed {
		t.Fatalf("pinned retention must never report Lapsed=true, got %+v", claim)
	}
	if claim.Ref != "sha256:aaaa" || claim.Digest != "sha256:aaaa" {
		t.Fatalf("raw attachment fields did not carry through: %+v", claim)
	}
}

// TestProjectAttachmentClaim_LapsedRetentionNamesTheDate is spec 04 §8 AC5.
func TestProjectAttachmentClaim_LapsedRetentionNamesTheDate(t *testing.T) {
	t.Parallel()

	entry := AttachmentManifestEntry{
		Attachment: Attachment{
			Ref: "sha256:bbbb", Digest: "sha256:bbbb",
			Verification: VerificationRequired,
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
	if strings.Contains(claim.LapseClaim, "datapackage: Fetch") || strings.Contains(claim.LapseClaim, "ErrExpired") {
		t.Fatalf("claim.LapseClaim %q leaks a raw fetch error instead of the reader-facing claim", claim.LapseClaim)
	}
}

// TestProjectAttachmentClaim_NotYetLapsedStaysUnlapsed guards the other side
// of AC5's date comparison.
func TestProjectAttachmentClaim_NotYetLapsedStaysUnlapsed(t *testing.T) {
	t.Parallel()

	entry := AttachmentManifestEntry{
		Attachment: Attachment{
			Ref: "sha256:cccc", Digest: "sha256:cccc",
			Verification: VerificationOffered,
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
// boundary condition: retention:pinned resolves to ExpiresAt == "", and this
// projection must treat empty exactly as "never lapses".
func TestProjectAttachmentClaim_PinnedRetentionNeverLapses(t *testing.T) {
	t.Parallel()

	entry := AttachmentManifestEntry{
		Attachment: Attachment{
			Ref: "sha256:dddd", Digest: "sha256:dddd",
			Verification: VerificationNone,
			Retention:    RetentionPinned,
		},
	}
	now := time.Date(2027, 8, 8, 0, 0, 0, 0, time.UTC)

	claim := ProjectAttachmentClaim(entry, now)

	if claim.Lapsed {
		t.Fatalf("pinned retention reported Lapsed=true a year later, want false")
	}
}

// TestProjectAttachmentClaim_MalformedExpiresAtIsNotClaimedLapsed mirrors
// TestFetch_MalformedExpiresAtIsNotClaimedExpired (fetch_test.go): a
// malformed expires_at must never be silently treated as lapsed.
func TestProjectAttachmentClaim_MalformedExpiresAtIsNotClaimedLapsed(t *testing.T) {
	t.Parallel()

	entry := AttachmentManifestEntry{
		Attachment: Attachment{
			Ref: "sha256:eeee", Digest: "sha256:eeee",
			Verification: VerificationRequired,
			Retention:    "1h",
		},
		ExpiresAt: "not-a-timestamp",
	}

	claim := ProjectAttachmentClaim(entry, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))

	if claim.Lapsed {
		t.Fatalf("claim.Lapsed = true for a malformed expires_at, want false (visibly wrong beats silently wrong, but never silently LAPSED)")
	}
}
