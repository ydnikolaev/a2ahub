package datapackage

import "errors"

// The refusal vocabulary of spec 05a §T2.4 and §T2.5, in one place because the
// pack, fetch and verify halves must refuse the same conditions by the same
// name. Every one of these is a refusal, never a warning: the session that
// authored the spec had just fixed a defect of the opposite shape, where a
// violation was computed and then not acted on, so the command exited zero and
// the contradiction surfaced minutes later somewhere else.
//
// Each sentinel is wrapped by a typed error carrying the offending value and
// path — the sentinel is what callers match on, the wrapper is what the agent
// reads and acts from.
var (
	// ErrProductionProfile refuses data_profile: production at pack time,
	// before anything is staged. Whoever the data is about is not a party to
	// the trust between the two systems, which is why this is a refusal here
	// rather than a discussion later.
	ErrProductionProfile = errors.New("datapackage: data_profile production is refused")

	// ErrEntryNonConforming refuses an entry, or a record within one, that
	// does not validate against its conforms_to schema. Raised at pack before
	// staging and at verify against the delivered bytes.
	ErrEntryNonConforming = errors.New("datapackage: entry does not conform to its contract schema")

	// ErrDigestMismatch refuses bytes whose declared digest is not what they
	// hash to. Raised before parsing, at both fetch and verify — an aggregate
	// that matches while an entry does not is a contradiction the consumer
	// must never be able to reach (L-3).
	ErrDigestMismatch = errors.New("datapackage: declared digest does not match computed digest")

	// ErrExpired refuses a fetch of a package past its expires_at.
	ErrExpired = errors.New("datapackage: package has expired")

	// ErrBoundExceeded refuses a package, entry or record count over its
	// configured limit, at pack and at fetch. These bounds are what make the
	// permanence accepted in §9.3 survivable, so they are enforced rather
	// than advertised.
	ErrBoundExceeded = errors.New("datapackage: configured bound exceeded")

	// ErrVerdictNotPass refuses driving verify-pass while the report's own
	// result is not pass. The verdict is derived from checks[], never
	// authored, and this is the guard that makes that true at the boundary.
	ErrVerdictNotPass = errors.New("datapackage: cannot pass a report whose result is not pass")

	// ErrUnsafeLocator refuses a locator shaped like a credential, a token or
	// an absolute local path. A locator names where a package lives; anything
	// that looks like how to get in is a different kind of value.
	ErrUnsafeLocator = errors.New("datapackage: locator must not be a credential or an absolute path")

	// ErrAttemptCeiling refuses an attempt past the configured maximum and
	// names escalation as the remaining action (L-1). The shipped default is
	// unset, so this refuses nothing today: nobody runs the loop unattended
	// yet, and a ceiling that fires during hand-driven debugging costs more
	// than it saves. The check ships now so that enabling it later is
	// configuration rather than a code change.
	ErrAttemptCeiling = errors.New("datapackage: attempt ceiling reached")

	// ErrPathEscapes refuses an entry path that leaves the package root,
	// whether by traversal, by absolute path, or by a symlink pointing out.
	ErrPathEscapes = errors.New("datapackage: entry path escapes the package root")

	// ErrSymlink refuses a symlink anywhere in a package. Not followed, not
	// recorded, not copied — refused, including one swapped between the check
	// and the read.
	ErrSymlink = errors.New("datapackage: symlinks are refused")

	// ErrDuplicateEntryPath refuses a package in which two entries claim the
	// same path. Its own sentinel rather than a bound: a duplicate is not a
	// package that is too big, and a refusal that names the wrong reason
	// sends the next attempt in the wrong direction.
	ErrDuplicateEntryPath = errors.New("datapackage: two entries claim the same path")

	// ErrEntryMissing refuses a manifest that declares an entry for which no
	// bytes were delivered. Distinct from ErrDigestMismatch: nothing was
	// hashed and nothing disagreed — the file simply is not there, and the
	// producer's next move differs accordingly.
	ErrEntryMissing = errors.New("datapackage: declared entry has no delivered bytes")

	// ErrAtomicNoReplaceUnsupported refuses an install on a platform whose
	// kernel offers no no-replace rename. Only linux and darwin do
	// (Renameat2 / RenameatxNp), the same constraint contract materialize
	// already carries — a non-atomic fallback would trade the guarantee the
	// caller relies on for the appearance of portability.
	ErrAtomicNoReplaceUnsupported = errors.New("datapackage: atomic no-replace directory install is unsupported on this platform")

	// ErrContractRefMismatch refuses a verification whose caller resolved a
	// different contract version than the manifest pins. This package cannot
	// resolve a contract itself, so the schemas a checker was built from are
	// the caller's word; without this check a caller bug would judge a
	// delivery against a version it never agreed to and the report would
	// look entirely normal. L-2 at the boundary where it can actually be
	// enforced.
	ErrContractRefMismatch = errors.New("datapackage: resolved contract ref does not match the contract pinned in the manifest")

	// ErrDestinationDiverged refuses a fetch into a destination that already
	// holds different bytes, leaving it untouched. A destination holding
	// byte-identical content is NOT an error: a re-fetch that finds exactly
	// what it would have written is a success, and the opposite reading would
	// make the retry this loop depends on unsafe.
	ErrDestinationDiverged = errors.New("datapackage: destination holds divergent content")
)
