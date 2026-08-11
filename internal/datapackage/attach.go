package datapackage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// This file is the package-local, pure core of "attach" — spec 04
// (agent-exchange-2026-08) §7's widening of this subsystem: `deliver`
// becomes `attach` + `conforms_to` + `verification: required` + the
// handoff (plan D4), so this is the general case the rest of the package's
// pack/deliver/fetch/verify flows are built on top of, not a replacement
// for any of them. Nothing in this file changes the behaviour of any
// exported symbol elsewhere in the package.
//
// Spec: docs/features/active/agent-exchange-2026-08/specs/04-possession.md §7
// Plan: docs/features/active/agent-exchange-2026-08/plans/04-possession.plan.md wave B
//
// # A bound this file does NOT add
//
// Bounds.MaxEntries (bounds.go) caps entries within one data-package
// attempt; it says nothing about how many attachments[] one artifact (a
// work_request) may declare — the schema itself has no maxItems on
// attachments either. There is no existing Check* helper for "attachment
// count on one artifact" the way there is for "entry count in one
// package", so per the brief's own instruction this file adds no parallel
// constant for it and reports the gap instead: an artifact declaring an
// unbounded number of attachments passes both the schema and every
// function in this file today.

// Verification values for attachments[].verification
// (schemas/envelope/v2/work_request.schema.json:97-100's enum — copied
// verbatim, not invented).
//
// VerificationNone is deliberately its own member rather than a stand-in
// for "unverified": an attachment carrying it makes the explicit claim "no
// verdict is defined for these bytes", which is a different statement from
// "nobody has checked yet". A reader that collapsed the two would render a
// verification: none attachment the same way it renders a missing package
// (spec 04 §6, AC4) — exactly the confusion this field exists to remove.
// This file only carries the constant and the enum-closure check
// (validateVerification); the rendering rule itself is internal/html's and
// internal/cli's, a later wave (out of this file's allowlist).
const (
	VerificationRequired = "required"
	VerificationOffered  = "offered"
	VerificationNone     = "none"
)

// RetentionPinned is attachments[].retention's "pinned" literal — the
// schema's oneOf const branch
// (schemas/envelope/v2/work_request.schema.json:101-109). Every other legal
// Retention value is a Go duration string: the schema's own pattern
// (`^-?([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`) is CLOSE to but not an
// exact subset of what time.ParseDuration accepts — ParseDuration also
// takes a bare "0" and a leading "+", both refused by the schema pattern's
// digit-then-unit shape, and this file inherits that gap rather than
// hand-rolling a second, narrower parser. Recorded as a deviation: a
// retention of exactly "0" is schema-illegal but would parse here and
// resolve to expires_at == now — an attachment lapsed the instant it is
// written. This file parses with the
// stdlib's own parser rather than a second, hand-rolled one.
const RetentionPinned = "pinned"

// Attachment mirrors schemas/envelope/v2/work_request.schema.json's
// attachments[] entry field for field: names, the required/optional split
// and the json/yaml tags are copied from the schema's own property list
// (work_request.schema.json:78-114), not invented. Ref, Digest,
// Verification and Retention are schema-required (schema's own
// `"required": ["ref", "digest", "verification", "retention"]`); Role and
// ConformsTo are schema-optional, hence the omitempty tag on those two
// fields only.
type Attachment struct {
	// Ref is the content-addressed blob id (schema: string, minLength 1).
	// This package mints it as the SAME value as Digest: "content-addressed"
	// means the identifier IS the hash, and this package has no second,
	// independent id-minting scheme for a blob the way MintPackageID mints a
	// package id — a package id is random-plus-timestamp by design (id.go),
	// the opposite of content addressing. Recorded as a design decision in
	// this wave's report: the schema does not pin Ref's exact format beyond
	// "content-addressed blob id", string, minLength 1, no pattern.
	Ref string `json:"ref" yaml:"ref"`

	// Digest is the `sha256:<hex>` identity — never a path or URL alone
	// (spec 04 §7, out-of-scope row: "the reference is a digest ... which
	// is what keeps external storage swappable later").
	Digest string `json:"digest" yaml:"digest"`

	Role         string `json:"role,omitempty" yaml:"role,omitempty"`
	ConformsTo   string `json:"conforms_to,omitempty" yaml:"conforms_to,omitempty"`
	Verification string `json:"verification" yaml:"verification"`
	Retention    string `json:"retention" yaml:"retention"`
}

// AttachmentManifestEntry is Attachment plus the absolute expires_at this
// package resolves from Retention against a reference clock — the
// datapackage-layer manifest record for one attachment. It mirrors
// Document.ExpiresAt's own RFC3339-string shape (manifest.go) so a caller
// that already knows how to read one Document field knows how to read this
// one too.
//
// UNLIKE Document.ExpiresAt — manifest.go's own doc comment records that
// timestamps there are plain strings so a produced value round-trips
// byte-for-byte, and every Document Pack produces today carries a
// populated ExpiresAt; there is no "no expiry" state a Document can
// express — ExpiresAt here is genuinely optional: "pinned" retention
// leaves it "" and the `omitempty` tag drops the field from the marshaled
// manifest entirely, the same way Entry.ConformsTo's own conditional field
// already round-trips absence (manifest.go's Entry doc comment). That is
// this file's answer to the brief's "read how expiry is represented today
// before choosing a representation": today's representation has no
// "absent" state to reuse, so this type adds one rather than overloading
// Document.ExpiresAt's existing non-optional string with a new meaning.
type AttachmentManifestEntry struct {
	Attachment
	ExpiresAt string `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
}

var (
	// ErrAttachmentVerificationInvalid refuses a Verification value outside
	// the schema's closed three-member enum (required|offered|none).
	ErrAttachmentVerificationInvalid = errors.New("datapackage: attachment verification is not one of required|offered|none")

	// ErrAttachmentRetentionInvalid refuses a Retention value that is
	// neither RetentionPinned nor a valid, non-negative duration string.
	ErrAttachmentRetentionInvalid = errors.New(`datapackage: attachment retention is neither "pinned" nor a valid duration`)
)

// NewAttachmentFromBytes builds an Attachment for one blob of bytes: the
// content-addressed pair (Ref, Digest) is minted from data itself via
// artifact.Digest — the repo's one digest implementation (this package's
// own doc.go "Digest discipline" section; this file does not hand-roll a
// second hasher).
//
// size is checked against bounds.CheckEntryBytes — the same per-entry
// ceiling DefaultBounds() already enforces for one dataset entry
// (bounds.go) — before the digest is even computed, and Verification and
// Retention are validated before that: an oversized or malformed attachment
// is refused before any byte is hashed.
func NewAttachmentFromBytes(data []byte, role, conformsTo, verification, retention string, bounds Bounds) (Attachment, error) {
	if err := validateVerification(verification); err != nil {
		return Attachment{}, fmt.Errorf("datapackage: NewAttachmentFromBytes: %w", err)
	}
	if err := validateRetention(retention); err != nil {
		return Attachment{}, fmt.Errorf("datapackage: NewAttachmentFromBytes: %w", err)
	}
	if err := bounds.CheckEntryBytes(int64(len(data))); err != nil {
		return Attachment{}, fmt.Errorf("datapackage: NewAttachmentFromBytes: %w", err)
	}

	digest := artifact.Digest(data)
	return Attachment{
		Ref:          digest,
		Digest:       digest,
		Role:         role,
		ConformsTo:   conformsTo,
		Verification: verification,
		Retention:    retention,
	}, nil
}

// NewAttachmentFromDirectory builds an Attachment for a whole local
// directory: it reuses WalkLocalDirectory (safefs.go) — already hardened
// against traversal, symlinks and TOCTOU, and already enforcing
// bounds.CheckEntryBytes/CheckEntryCount/CheckPackageBytes as it walks — so
// this function makes no second, independent size decision, only reuses the
// package's own existing bound checks. The digest is BuildEntrySet's own
// AggregateDigest (entryset.go), computed via artifact.CombineDigestPairs
// over the same per-file artifact.Digest this package already uses
// elsewhere — again, no second hasher.
func NewAttachmentFromDirectory(ctx context.Context, source, role, conformsTo, verification, retention string, bounds Bounds) (Attachment, error) {
	if err := validateVerification(verification); err != nil {
		return Attachment{}, fmt.Errorf("datapackage: NewAttachmentFromDirectory: %w", err)
	}
	if err := validateRetention(retention); err != nil {
		return Attachment{}, fmt.Errorf("datapackage: NewAttachmentFromDirectory: %w", err)
	}

	files, err := WalkLocalDirectory(ctx, source, bounds)
	if err != nil {
		return Attachment{}, fmt.Errorf("datapackage: NewAttachmentFromDirectory: %w", err)
	}
	entrySet, err := BuildEntrySet(files)
	if err != nil {
		return Attachment{}, fmt.Errorf("datapackage: NewAttachmentFromDirectory: %w", err)
	}

	return Attachment{
		Ref:          entrySet.AggregateDigest,
		Digest:       entrySet.AggregateDigest,
		Role:         role,
		ConformsTo:   conformsTo,
		Verification: verification,
		Retention:    retention,
	}, nil
}

// NewAttachmentFromDirectoryWithPayload builds an Attachment for a whole
// local directory AND returns the walked payload (blob-root-relative paths
// to bytes) alongside it, so a caller that must ALSO write those same bytes
// somewhere else — P10 (agent-exchange-2026-08) spec 10 wave B's `a2a
// attach`, landing them via space.DeliverBlob before the draft references
// them (spec 10 §1's decided ordering) — does not re-walk source a second
// time: WalkLocalDirectory's own safe-walk (safefs.go) already closes the
// TOCTOU window once, and a second, independently-timed walk would reopen
// it.
//
// UNLIKE NewAttachmentFromDirectory, this function's returned Digest is
// digestForPayload(payload), not entrySet.AggregateDigest — see
// digestForPayload's own doc comment for exactly why (the one-file-
// directory divergence) and TestNewAttachmentFromDirectoryWithPayload_
// OneFileDirectoryDigestMatchesSinglePayloadBranch (attach_test.go) for the
// regression pin. This is a NEW function: NewAttachmentFromDirectory keeps
// its existing documented behaviour unchanged for its existing callers.
func NewAttachmentFromDirectoryWithPayload(ctx context.Context, source, role, conformsTo, verification, retention string, bounds Bounds) (Attachment, map[string][]byte, error) {
	if err := validateVerification(verification); err != nil {
		return Attachment{}, nil, fmt.Errorf("datapackage: NewAttachmentFromDirectoryWithPayload: %w", err)
	}
	if err := validateRetention(retention); err != nil {
		return Attachment{}, nil, fmt.Errorf("datapackage: NewAttachmentFromDirectoryWithPayload: %w", err)
	}

	files, err := WalkLocalDirectory(ctx, source, bounds)
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("datapackage: NewAttachmentFromDirectoryWithPayload: %w", err)
	}

	payload := make(map[string][]byte, len(files))
	for _, f := range files {
		payload[f.RelPath] = f.Bytes
	}
	digest := digestForPayload(payload)

	return Attachment{
		Ref:          digest,
		Digest:       digest,
		Role:         role,
		ConformsTo:   conformsTo,
		Verification: verification,
		Retention:    retention,
	}, payload, nil
}

// VerifyAttachment checks data against attachment's declared Digest — the
// same hard-refusal-on-mismatch rule the package already enforces at pack
// and fetch (entryset.go's VerifyEntrySet; ErrDigestMismatch's own doc
// comment in errors.go), reused here rather than a second, parallel error:
// an attachment is bytes moving through the same subsystem (plan D4), so a
// mismatch is refused by the same name regardless of which side of the
// exchange calls this. Both directions of spec 04 §6's "a hard refusal on
// both ends" are the SAME function: the sender proving what it is about to
// publish, and the reader proving what it just received, both call
// VerifyAttachment — there is no separate send-side/receive-side check to
// keep in agreement.
func VerifyAttachment(attachment Attachment, data []byte) error {
	actual := artifact.Digest(data)
	if actual != attachment.Digest {
		return fmt.Errorf("datapackage: VerifyAttachment: %w: attachment %q declared %s, computed %s", ErrDigestMismatch, attachment.Ref, attachment.Digest, actual)
	}
	return nil
}

// ResolveAttachmentExpiry computes the manifest's absolute expires_at for
// retention against now (the caller's own clock — the same injectable-time
// discipline Pack's own Now parameter already uses, pack.go).
//
// RetentionPinned returns "", nil: the manifest carries NO expiry at all.
// Any other legal retention is a duration, added to now and formatted
// exactly as Document.ExpiresAt already is (now.Add(d).UTC().Format(time.RFC3339),
// mirroring pack.go's own ExpiresAt line) so the two fields share one
// timestamp convention.
func ResolveAttachmentExpiry(retention string, now time.Time) (string, error) {
	d, pinned, err := parseRetention(retention)
	if err != nil {
		return "", fmt.Errorf("datapackage: ResolveAttachmentExpiry: %w", err)
	}
	if pinned {
		return "", nil
	}
	return now.Add(d).UTC().Format(time.RFC3339), nil
}

// NewAttachmentManifestEntry resolves attachment's Retention into an
// absolute ExpiresAt at now and returns the combined manifest entry.
func NewAttachmentManifestEntry(attachment Attachment, now time.Time) (AttachmentManifestEntry, error) {
	expiresAt, err := ResolveAttachmentExpiry(attachment.Retention, now)
	if err != nil {
		return AttachmentManifestEntry{}, fmt.Errorf("datapackage: NewAttachmentManifestEntry: %w", err)
	}
	return AttachmentManifestEntry{Attachment: attachment, ExpiresAt: expiresAt}, nil
}

func validateVerification(v string) error {
	switch v {
	case VerificationRequired, VerificationOffered, VerificationNone:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrAttachmentVerificationInvalid, v)
	}
}

// validateRetention is parseRetention for callers (NewAttachmentFromBytes,
// NewAttachmentFromDirectory) that only need the pass/refuse verdict before
// doing anything else, not the parsed duration itself.
func validateRetention(retention string) error {
	_, _, err := parseRetention(retention)
	return err
}

// parseRetention is the ONE place this file parses a Retention string, so
// ResolveAttachmentExpiry never discards a *time.ParseDuration* error the
// way an inline `d, _ := time.ParseDuration(...)` would have — every caller
// that needs the parsed duration gets it back explicitly instead of
// re-deriving it from a value already proven valid elsewhere.
//
// DEVIATION (recorded here because the fix is outside this file's
// allowlist): this refuses a negative duration ("d < 0"), but wave A's
// shipped schema pattern for attachments[].retention
// (work_request.schema.json:106, `^-?([0-9]+...)+$`) explicitly permits a
// leading '-' and therefore admits a negative value. A document with
// retention: "-1h" is schema-legal today and refused by this Go layer — the
// opposite of a silently dropped guard, but still a real schema/Go
// divergence. Refusing it here is the right behaviour (a negative retention
// is nonsense), but tightening the schema pattern to match is a later
// wave's edit, not this one's.
func parseRetention(retention string) (d time.Duration, pinned bool, err error) {
	if retention == RetentionPinned {
		return 0, true, nil
	}
	d, err = time.ParseDuration(retention)
	if err != nil || d < 0 {
		return 0, false, fmt.Errorf("%w: %q", ErrAttachmentRetentionInvalid, retention)
	}
	return d, false, nil
}
