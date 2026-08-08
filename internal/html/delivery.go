package html

import (
	"fmt"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// This file projects internal/cache's Delivery read model (spec 05a AC-7:
// package id, attempt number, verdict, failing entry/rule, supersede
// chain — thread-side, under the handoff that carries it, plan decision
// 2) into the dashboard's own render vocabulary. It is deliberately NOT
// wired into Data/Thread/ThreadView here: model.go, assemble.go and
// dashboard_renderer.go are not in this wave's allowlist, so a later wave
// composes ProjectDeliveries' output into the page. This file's own test
// is fixture-driven for exactly that reason — it proves the projection
// end to end without needing the composition it will eventually sit
// inside.
//
// JSON keys are camelCase, matching model.go's own documented convention
// ("consumed by the page's client JS"), unlike internal/cache's
// snake_case CLI-facing shape.

// DeliveryResolution says whether a delivery's data-package/v1 manifest
// could be resolved — the render-layer mirror of cache.DeliveryStatus.
type DeliveryResolution string

// A manifest either resolved or it did not; an unresolved delivery is
// rendered as unresolved rather than omitted, so a reader is never shown a
// thread that silently lost an attempt.
const (
	DeliveryResolved   DeliveryResolution = "resolved"
	DeliveryUnresolved DeliveryResolution = "unresolved"
)

// DeliveryVerdict is the dashboard's own closed rendering vocabulary for a
// Delivery's verdict — one non-empty string per state, so "not yet
// verified" can never be mistaken for "passed" by a template that merely
// checks truthiness. This project has already shipped exactly that
// defect once (a missing report rounded down to a settled state); every
// branch here is named for that reason.
type DeliveryVerdict string

// One non-empty string per state, including the unverified one.
const (
	DeliveryVerdictPassed     DeliveryVerdict = "passed"
	DeliveryVerdictFailed     DeliveryVerdict = "failed"
	DeliveryVerdictErrored    DeliveryVerdict = "errored"
	DeliveryVerdictUnverified DeliveryVerdict = "unverified"
)

// DeliveryFailure is one non-passing check, projected for render.
type DeliveryFailure struct {
	EntryPath string `json:"entryPath"`
	Rule      string `json:"rule"`
	Record    *int64 `json:"record,omitempty"`
}

// DeliveryChainEntry is one attempt in a delivery's supersede chain,
// oldest first.
type DeliveryChainEntry struct {
	PackageID  string             `json:"packageId"`
	Attempt    int                `json:"attempt,omitempty"`
	Resolution DeliveryResolution `json:"resolution"`
	Verdict    DeliveryVerdict    `json:"verdict,omitempty"`
	ReportID   string             `json:"reportId,omitempty"`
}

// Delivery is one handoff deliverable of kind "data", projected for
// render under its handoff.
//
// NOTE on the id-prefix badge: the dashboard's TypeBadge convention
// (web/src/components/TypeBadge.astro) is keyed on a closed X-prefix set
// and lives outside this wave's allowlist (it is not Go at all). `DP-`
// and `VR-` — this delivery's own PackageID/ReportID prefixes — are
// neither an X- exchange type nor in that closed set, so they will
// render as "unknown" there until a web-allowlisted change extends it
// deliberately. Recorded here, and in this wave's own deviations report,
// per the top-level brief's explicit instruction not to paper over it.
type Delivery struct {
	HandoffID string `json:"handoffId"`
	Name      string `json:"name"`
	Ref       string `json:"ref"`

	Resolution  DeliveryResolution `json:"resolution"`
	Unavailable string             `json:"unavailable,omitempty"`

	PackageID  string `json:"packageId,omitempty"`
	Attempt    int    `json:"attempt,omitempty"`
	Supersedes string `json:"supersedes,omitempty"`

	Verdict  DeliveryVerdict `json:"verdict,omitempty"`
	ReportID string          `json:"reportId,omitempty"`

	Failures []DeliveryFailure `json:"failures,omitempty"`

	// Chain is always non-nil (an empty array, never omitted or null),
	// mirroring cache.Delivery.Chain's own guarantee.
	Chain []DeliveryChainEntry `json:"chain"`
}

// ProjectDeliveries converts cache's read model into the dashboard's own
// render vocabulary. It never re-derives a verdict from checks — that
// derivation already happened once, in datapackage.NewReport, and
// cache.ResolveDelivery only read it; this function only ever translates
// an already-resolved cache.DeliveryVerdictStatus into its render-layer
// spelling.
func ProjectDeliveries(deliveries []cache.Delivery) []Delivery {
	out := make([]Delivery, 0, len(deliveries))
	for _, d := range deliveries {
		out = append(out, projectDelivery(d))
	}
	return out
}

func projectDelivery(d cache.Delivery) Delivery {
	pd := Delivery{
		HandoffID: d.HandoffID, Name: d.Name, Ref: d.Ref,
		PackageID: d.PackageID, Attempt: d.Attempt, Supersedes: d.Supersedes,
		ReportID: d.ReportID,
		Chain:    make([]DeliveryChainEntry, 0, len(d.Chain)),
	}

	if d.Status == cache.DeliveryUnavailable {
		pd.Resolution = DeliveryUnresolved
		pd.Unavailable = d.UnavailableReason
		return pd
	}
	pd.Resolution = DeliveryResolved
	pd.Verdict = projectVerdict(d.Verdict)

	for _, f := range d.Failures {
		pd.Failures = append(pd.Failures, DeliveryFailure{EntryPath: f.EntryPath, Rule: f.Rule, Record: f.Record})
	}
	for _, c := range d.Chain {
		pd.Chain = append(pd.Chain, projectChainEntry(c))
	}
	return pd
}

func projectChainEntry(c cache.DeliveryChainEntry) DeliveryChainEntry {
	entry := DeliveryChainEntry{PackageID: c.PackageID, Attempt: c.Attempt, ReportID: c.ReportID}
	if c.Status == cache.DeliveryUnavailable {
		entry.Resolution = DeliveryUnresolved
		return entry
	}
	entry.Resolution = DeliveryResolved
	entry.Verdict = projectVerdict(c.Verdict)
	return entry
}

// projectVerdict is the ONLY place a cache.DeliveryVerdictStatus becomes a
// DeliveryVerdict. Every known value has its own explicit case; an
// unrecognized value renders as "unknown:<value>" rather than silently
// falling back to the empty string (which a template would then be free
// to treat as "not failed", i.e. passing) — visibly wrong beats silently
// wrong. See this file's own seeded-red receipt on the
// DeliveryVerdictUnverified branch specifically, since that is the one
// this project has already gotten wrong once.
func projectVerdict(v cache.DeliveryVerdictStatus) DeliveryVerdict {
	switch v {
	case cache.DeliveryVerdictPass:
		return DeliveryVerdictPassed
	case cache.DeliveryVerdictFail:
		return DeliveryVerdictFailed
	case cache.DeliveryVerdictError:
		return DeliveryVerdictErrored
	case cache.DeliveryVerdictUnverified:
		return DeliveryVerdictUnverified
	default:
		return DeliveryVerdict("unknown:" + string(v))
	}
}

// AttachmentClaim is a datapackage.Attachment's own reader-facing claim —
// spec 04 (agent-exchange-2026-08) §6/§8 AC4 and AC5, the two statements a
// reader has to be able to read correctly about a DELIBERATE state, never
// as a failure. This is a sibling vocabulary to Delivery above, not a reuse
// of it: Delivery's Unavailable/DeliveryUnresolved describe a manifest this
// code could not resolve (cache.Delivery's own "could not be resolved"
// text, delivery.go's cache-side twin); an attachment's verification:none
// or lapsed retention are never that — they are the producer's own
// deliberate statement, correctly formed, and rendering either one through
// the unavailable/missing-package vocabulary is exactly the confusion AC4
// and AC5 exist to remove (datapackage/attach.go's own doc comment on
// VerificationNone says this first; this type is what makes it renderable).
//
// Verification and lapse are orthogonal (spec §7's axis table: "claim" and
// a retention's lifecycle are two different questions about the same
// bytes), so both are carried as independent fields rather than folded
// into one string — an attachment can be verification:none AND lapsed, and
// a reader needs both statements, not whichever one a collapsed string
// happened to pick.
type AttachmentClaim struct {
	// VerificationClaim is always non-empty: it is what an attachment's own
	// verification enum member asserts about these bytes, independent of
	// whether the attachment has lapsed.
	VerificationClaim string `json:"verificationClaim"`

	// Lapsed and LapsedOn/LapseClaim are AC5's own claim. Lapsed is false,
	// and LapsedOn/LapseClaim empty, whenever the attachment carries no
	// resolved expiry (retention: pinned resolves to no expiry at all,
	// datapackage.ResolveAttachmentExpiry's own documented behaviour) or
	// its resolved expiry has not yet passed as of now.
	Lapsed   bool   `json:"lapsed"`
	LapsedOn string `json:"lapsedOn,omitempty"`
	// LapseClaim is the full reader-facing sentence, non-empty exactly
	// when Lapsed is true: "references bytes whose retention lapsed on
	// <date>" — never a fetch error (datapackage.Fetch's own ErrExpired,
	// which this projection never surfaces to a reader).
	LapseClaim string `json:"lapseClaim,omitempty"`
}

// ProjectAttachmentClaim turns one attachment's own Verification plus its
// ALREADY-RESOLVED ExpiresAt (entry.ExpiresAt — datapackage's own
// AttachmentManifestEntry field; this function re-derives no expiry of its
// own, per the top-level brief's "consume what is there, not re-derive
// it") into the reader-facing claim both the dashboard and `a2a show`
// print (internal/cli/cmd_show.go calls this same function — one
// artifact, one answer, no second vocabulary). now is the caller's own
// reference clock.
//
// DEVIATION, recorded because it bounds what this function can prove for a
// caller reading a COMMITTED artifact rather than a freshly-minted
// AttachmentManifestEntry: an attachment's frontmatter entry, as written by
// `a2a attach` and read back off a committed artifact, never carries
// expires_at at all (cmd_attach.go's own doc comment: the schema's
// attachments[] additionalProperties:false declares no such property, so
// writing it would be a SCH-003 drift). So a caller that decodes an
// attachment straight from committed frontmatter always passes
// entry.ExpiresAt == "" here and always gets Lapsed == false — correct for
// retention:pinned, but merely UNPROVABLE (not false) for a duration
// retention whose attach-time anchor was never captured anywhere this
// function, or any caller on this wave's allowlist, can reach. Closing
// that gap needs either a new schema property or a side-car manifest —
// both outside this file's allowlist (schemas/**, internal/datapackage/**)
// — and is reported here, not silently worked around by anchoring on an
// unrelated timestamp (the artifact's own `created` field, which is
// artifact-creation-time, not attach-time, and would produce a false
// lapse date for the same reason the top-level brief warns against).
func ProjectAttachmentClaim(entry datapackage.AttachmentManifestEntry, now time.Time) AttachmentClaim {
	claim := AttachmentClaim{VerificationClaim: attachmentVerificationClaim(entry.Verification)}

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
// datapackage.Attachment.Verification value becomes reader-facing text.
// VerificationNone's text is copied verbatim from datapackage/attach.go's
// own doc comment on that constant ("makes the explicit claim 'no verdict
// is defined for these bytes'") — not reinvented here, per this file's own
// "no second vocabulary" rule. An unrecognized value renders visibly as
// such, mirroring projectVerdict's own "unknown:<value>" convention above,
// rather than silently collapsing to one of the three known claims.
func attachmentVerificationClaim(verification string) string {
	switch verification {
	case datapackage.VerificationNone:
		return "no verdict is defined for these bytes"
	case datapackage.VerificationRequired:
		return "a verdict is required for these bytes"
	case datapackage.VerificationOffered:
		return "a verdict is offered for these bytes"
	default:
		return "verification: unknown:" + verification
	}
}
