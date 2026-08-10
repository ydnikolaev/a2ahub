package datapackage

import (
	"fmt"
	"time"
)

// This file holds AttachmentClaim and ProjectAttachmentClaim — moved here
// from internal/html/delivery.go by spec 04's (agent-exchange-2026-08) §11
// amendment "the attachment claim is structured, not rendered-only"
// (2026-08-09). internal/datapackage is the cycle-free common ground:
// internal/html and internal/cache both already import this package, and it
// imports neither of them, so a typed claim minted here reaches
// cache.ShowResult, `a2a show --json`, MCP and the dashboard from ONE
// derivation instead of a decode repeated per surface.

// AttachmentClaim is one attachment's own reader-facing claim — spec 04 §6/§8
// AC4 and AC5, the two statements a reader has to be able to read correctly
// about a DELIBERATE state, never as a failure. It carries the attachment's
// own committed fields (embedded AttachmentManifestEntry: ref, digest, role,
// conforms_to, verification, retention, expires_at) PLUS the two claims
// derived from them, so a consumer never has to re-decode the raw entry to
// get the sentence a person reads.
//
// verification:none or a lapsed retention are never a resolution FAILURE —
// they are the producer's own deliberate statement, correctly formed, and
// rendering either one through the unavailable/missing-package vocabulary
// (cache.Delivery's own "could not be resolved" text) is exactly the
// confusion AC4 and AC5 exist to remove (attach.go's own doc comment on
// VerificationNone says this first; this type is what makes it renderable).
//
// Verification and lapse are orthogonal (spec §7's axis table: "claim" and a
// retention's lifecycle are two different questions about the same bytes),
// so both are carried as independent fields rather than folded into one
// string — an attachment can be verification:none AND lapsed, and a reader
// needs both statements, not whichever one a collapsed string happened to
// pick.
type AttachmentClaim struct {
	AttachmentManifestEntry

	// VerificationClaim is always non-empty: it is what an attachment's own
	// verification enum member asserts about these bytes, independent of
	// whether the attachment has lapsed.
	VerificationClaim string `json:"verification_claim"`

	// Lapsed and LapsedOn/LapseClaim are AC5's own claim. Lapsed is false,
	// and LapsedOn/LapseClaim empty, whenever the attachment carries no
	// resolved expiry (retention: pinned resolves to no expiry at all,
	// ResolveAttachmentExpiry's own documented behaviour) or its resolved
	// expiry has not yet passed as of the caller's reference clock.
	Lapsed   bool   `json:"lapsed"`
	LapsedOn string `json:"lapsed_on,omitempty"`
	// LapseClaim is the full reader-facing sentence, non-empty exactly when
	// Lapsed is true: "references bytes whose retention lapsed on <date>" —
	// never a fetch error (Fetch's own ErrExpired, which this projection
	// never surfaces to a reader).
	LapseClaim string `json:"lapse_claim,omitempty"`
}

// ProjectAttachmentClaim turns one attachment's own Verification plus its
// ALREADY-RESOLVED ExpiresAt (entry.ExpiresAt) into the reader-facing claim
// every consumer prints or renders: `a2a show`, `a2a show --json`, MCP's
// a2a_show, and the dashboard detail panel all read the SAME
// AttachmentClaim off cache.ShowResult.Attachments — one derivation, no
// second vocabulary. now is the caller's own reference clock (the cache
// Store's injected Clock on the read path, never time.Now() directly, so
// the lapse verdict is deterministic and testable).
//
// entry.ExpiresAt reaches here from a COMMITTED artifact today:
// schemas/envelope/v2/work_request.schema.json requires attachments[]
// expires_at whenever retention is not "pinned", and `a2a attach`
// (internal/cli/cmd_attach.go's attachAppendEntry) writes it onto the
// draft's frontmatter at attach time. An earlier version of this doc
// comment recorded expires_at as unreachable from a committed artifact —
// that was true before the schema declared the property and before `a2a
// attach` wrote it; it is stale now, corrected here rather than repeated.
func ProjectAttachmentClaim(entry AttachmentManifestEntry, now time.Time) AttachmentClaim {
	claim := AttachmentClaim{
		AttachmentManifestEntry: entry,
		VerificationClaim:       attachmentVerificationClaim(entry.Verification),
	}

	if entry.ExpiresAt == "" {
		return claim
	}
	expires, err := time.Parse(time.RFC3339, entry.ExpiresAt)
	if err != nil || !now.After(expires) {
		return claim
	}
	claim.Lapsed = true
	claim.LapsedOn = expires.Format("2006-01-02")
	claim.LapseClaim = fmt.Sprintf("references bytes whose retention lapsed on %s", claim.LapsedOn)
	return claim
}

// attachmentVerificationClaim is the ONE place a
// AttachmentManifestEntry.Verification value becomes reader-facing text.
// VerificationNone's text is copied verbatim from attach.go's own doc
// comment on that constant ("makes the explicit claim 'no verdict is
// defined for these bytes'") — not reinvented here, per this file's own "no
// second vocabulary" rule. An unrecognized value renders visibly as such,
// mirroring cache/html's own projectVerdict "unknown:<value>" convention,
// rather than silently collapsing to one of the three known claims.
func attachmentVerificationClaim(verification string) string {
	switch verification {
	case VerificationNone:
		return "no verdict is defined for these bytes"
	case VerificationRequired:
		return "a verdict is required for these bytes"
	case VerificationOffered:
		return "a verdict is offered for these bytes"
	default:
		return "verification: unknown:" + verification
	}
}
