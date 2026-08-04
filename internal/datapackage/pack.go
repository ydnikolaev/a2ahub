package datapackage

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// Pack is `a2a data pack`'s core (spec 05a §T1, §T2.1-§T2.6): it walks a
// local directory, refuses everything §T2.4 says to refuse, and returns a
// complete data-package/v1 manifest. It is write-free, exactly like
// `contract preflight` — there is no call anywhere in this file to
// InstallLocalFiles or any other writer, so "stages nothing" (AC-1, AC-2)
// holds by construction rather than by a check that could be forgotten.
//
// The returned Document's own Entries[] IS the per-entry preview a caller
// renders (digest, size_bytes, record_count already sit on every entry) —
// there is deliberately no second, parallel preview type carrying the same
// three numbers a second way.
//
// Refusal order is the design, not an implementation detail (spec 05a
// "What to do"): production profile, then locator shape, then the attempt
// ceiling — all three ahead of ever touching the filesystem — then the
// walk, then bounds (record count closes plan D-11's second row), then
// per-entry conformance, then media_type/format agreement (D-11's third
// row), then the entry set's digests, then the attempt/supersedes pair,
// then the mint. Each step returns before the next one runs, so a refusal
// early in the list can never be masked by one later in it.
func Pack(ctx context.Context, req PackRequest) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	// 1. data_profile: production is refused first, before the filesystem
	// is touched at all — a policy refusal, not a finding about data.
	if req.DataProfile == DataProfileProduction {
		return Document{}, fmt.Errorf("datapackage: Pack: %w", ErrProductionProfile)
	}

	// 2. A locator shaped like a credential, a token or an absolute path is
	// refused next. artifact.CleanRelativePath's restricted charset already
	// excludes ':' and '@' (so a "https://token:secret@host/..." locator
	// fails on the charset alone) and a leading '/' (so an absolute local
	// path fails on the same rule the schema's own cleanRelativePath $def
	// uses) — one predicate, shared with the schema's own shape, not a
	// second one invented here.
	if !artifact.CleanRelativePath(req.Locator) {
		return Document{}, fmt.Errorf("datapackage: Pack: %w: %q", ErrUnsafeLocator, req.Locator)
	}

	// 3. The attempt ceiling (L-1). MaxAttempts <= 0 means unset, and unset
	// refuses nothing — the shipped default (spec 05a §T2.5 L-1).
	if req.MaxAttempts > 0 && req.Attempt > req.MaxAttempts {
		return Document{}, fmt.Errorf("datapackage: Pack: %w: attempt %d exceeds the configured maximum of %d attempts; escalate rather than retry", ErrAttemptCeiling, req.Attempt, req.MaxAttempts)
	}

	wantMediaType, ok := formatMediaType[req.Format]
	if !ok {
		return Document{}, fmt.Errorf("datapackage: Pack: unknown format %q", req.Format)
	}

	// 4. Walk. WalkLocalDirectory is already hardened (os.Root containment,
	// Lstat-before-and-after, symlink refusal) and already enforces
	// per-entry bytes, entry count and total package bytes as it proceeds —
	// this function does not re-check any of those three.
	files, err := WalkLocalDirectory(ctx, req.Source, req.Bounds)
	if err != nil {
		return Document{}, fmt.Errorf("datapackage: Pack: %w", err)
	}

	// Every walked file must have a caller-supplied classification
	// (Role/MediaType/ConformsTo) — BuildEntrySet's own doc says as much:
	// it has no way to know a file's role or the contract schema entry it
	// is checked against, so the caller (this function's own caller) must
	// supply it per path. Coverage is checked in both directions: a walked
	// file with no declaration, and a declaration naming a path nothing was
	// walked for.
	for _, f := range files {
		if _, declared := req.Entries[f.RelPath]; !declared {
			return Document{}, fmt.Errorf("datapackage: Pack: entry %q was found on disk but has no declared role/media_type/conforms_to", f.RelPath)
		}
	}
	for path := range req.Entries {
		if !containsRelPath(files, path) {
			return Document{}, fmt.Errorf("datapackage: Pack: %w: %q declared but not found under %s", ErrEntryMissing, path, req.Source)
		}
	}

	// 5. Bounds: record count. Entry count, per-entry bytes and total bytes
	// were already enforced by the walk above; record count is the one
	// Bounds.CheckRecordCount closes here (plan D-11's second row — it had
	// zero production callers before this file).
	recordCounts := make(map[string]int64, len(files))
	var totalRecords int64
	for _, f := range files {
		decl := req.Entries[f.RelPath]
		if decl.Role != RoleDataset {
			continue
		}
		count, err := CountRecords(req.Format, f.Bytes)
		if err != nil {
			return Document{}, fmt.Errorf("datapackage: Pack: %w: entry %q: %w", ErrEntryNonConforming, f.RelPath, err)
		}
		recordCounts[f.RelPath] = count
		totalRecords += count
	}
	if err := req.Bounds.CheckRecordCount(int(totalRecords)); err != nil {
		return Document{}, fmt.Errorf("datapackage: Pack: %w", err)
	}

	// 6. Every dataset entry is checked with the EntryChecker. One bad file
	// among many refuses the whole pack and names it (and the record, for
	// ndjson) — AC-1. Files are walked in sorted-path order already, so the
	// first offending path in that order is always the one named,
	// deterministically.
	for _, f := range files {
		decl := req.Entries[f.RelPath]
		if decl.Role != RoleDataset {
			continue
		}
		check, err := req.Checker.CheckEntry(ctx, EntryCheckRequest{
			ConformsTo:    decl.ConformsTo,
			Format:        req.Format,
			Entry:         f,
			MaxViolations: req.MaxViolations,
		})
		if err != nil {
			return Document{}, fmt.Errorf("datapackage: Pack: entry %q: %w", f.RelPath, err)
		}
		if check.Status != CheckPass {
			return Document{}, fmt.Errorf("datapackage: Pack: %w: %s", ErrEntryNonConforming, describeFailingCheck(check))
		}
	}

	// 7. media_type must match format on dataset entries (§T2.2). Not
	// expressed in the schema — canonical media-type strings per format
	// were never pinned there — so it is pinned in Go and enforced here
	// (plan D-11's third row).
	for _, f := range files {
		decl := req.Entries[f.RelPath]
		if decl.Role != RoleDataset {
			continue
		}
		if decl.MediaType != wantMediaType {
			return Document{}, fmt.Errorf("datapackage: Pack: %w: entry %q declares media_type %q, format %q requires %q", ErrEntryNonConforming, f.RelPath, decl.MediaType, req.Format, wantMediaType)
		}
	}

	// 8. Build the entry set. BuildEntrySet is the only place a digest is
	// computed (spec 05a §5/§Digest discipline) — this function never
	// hashes a byte itself.
	entrySet, err := BuildEntrySet(files)
	if err != nil {
		return Document{}, fmt.Errorf("datapackage: Pack: %w", err)
	}

	entries := make([]Entry, len(entrySet.Entries))
	var totalSize int64
	for i, e := range entrySet.Entries {
		decl := req.Entries[e.Path]
		e.Role = decl.Role
		e.MediaType = decl.MediaType
		if decl.Role == RoleDataset {
			e.ConformsTo = decl.ConformsTo
			count := recordCounts[e.Path]
			e.RecordCount = &count
		} else {
			// Defensive by construction, not by trusting the caller: the
			// schema forbids conforms_to on a non-dataset entry
			// (data-package.schema.json's entry conditional), so a
			// non-dataset role never carries one out of this function
			// regardless of what the declaration held.
			e.ConformsTo = ""
			e.RecordCount = nil
		}
		entries[i] = e
		totalSize += e.SizeBytes
	}

	// 9. attempt/supersedes consistency, then the mint. attempt is 1
	// without supersedes, and supersedes.Attempt+1 with it; an inconsistent
	// pair is refused (spec 05a §T2.1).
	var supersedesID string
	if req.Supersedes == nil {
		if req.Attempt != 1 {
			return Document{}, fmt.Errorf("datapackage: Pack: attempt %d has no supersedes; only attempt 1 may omit it", req.Attempt)
		}
	} else {
		if req.Supersedes.Attempt < 1 {
			return Document{}, fmt.Errorf("datapackage: Pack: supersedes %q carries invalid attempt %d", req.Supersedes.ID, req.Supersedes.Attempt)
		}
		if req.Attempt != req.Supersedes.Attempt+1 {
			return Document{}, fmt.Errorf("datapackage: Pack: attempt %d is inconsistent with supersedes %q at attempt %d (want %d)", req.Attempt, req.Supersedes.ID, req.Supersedes.Attempt, req.Supersedes.Attempt+1)
		}
		supersedesID = req.Supersedes.ID
	}

	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	entropy := req.Entropy
	if entropy == nil {
		entropy = rand.Reader
	}
	id, err := MintPackageIDAt(req.System, now, entropy)
	if err != nil {
		return Document{}, fmt.Errorf("datapackage: Pack: %w", err)
	}

	return Document{
		Schema:          "data-package/v1",
		ID:              id,
		Thread:          req.Thread,
		Contract:        req.Contract,
		Mode:            ModeSnapshot,
		Classification:  req.Classification,
		DataProfile:     req.DataProfile,
		Format:          req.Format,
		TransportDriver: TransportDriverSpaceGit,
		Locator:         req.Locator,
		Entries:         entries,
		AggregateDigest: entrySet.AggregateDigest,
		SizeBytes:       totalSize,
		RecordCount:     totalRecords,
		CreatedAt:       now.UTC().Format(time.RFC3339),
		ExpiresAt:       now.Add(req.Expires).UTC().Format(time.RFC3339),
		Supersedes:      supersedesID,
		Attempt:         req.Attempt,
		Provenance:      req.Provenance,
	}, nil
}

// DataProfileProduction is deliberately not one of DataProfileSynthetic/
// DataProfileSanitized in manifest.go — it exists only so Pack has a
// literal to compare against without duplicating the string. Production is
// refused (ErrProductionProfile), never a legal value the manifest carries.
const DataProfileProduction = "production"

// PackRequest is Pack's complete input: everything needed to produce a
// data-package/v1 manifest without ever reading anything beyond Source and
// the request itself. Every field the spec's refusal ordering (§T2.4)
// touches is here; nothing about staging or delivery is, because Pack never
// stages or delivers.
type PackRequest struct {
	// Source is the local directory WalkLocalDirectory reads.
	Source string

	// System is the minting system identity passed to MintPackageIDAt —
	// distinct from Provenance.OriginSystem, which names the data's own
	// source system, not the a2ahub system doing the packing.
	System string

	Thread         string
	Contract       string
	Classification string
	DataProfile    string
	Format         string
	Locator        string

	// Entries declares, per walked path, what BuildEntrySet's own doc
	// comment says it cannot know from bytes alone: Role, MediaType and
	// (for dataset entries) ConformsTo. Keyed by the same RelPath
	// WalkLocalDirectory returns.
	Entries map[string]EntryDeclaration

	// Expires is added to the clock (Now, or time.Now() if unset) to
	// produce expires_at — the shape `a2a data pack --expires <duration>`
	// takes (spec 05a §T1).
	Expires time.Duration

	// Attempt is 1 for a first delivery, Supersedes.Attempt+1 otherwise;
	// checked against Supersedes, not merely copied.
	Attempt int

	// Supersedes is nil for a first attempt. See the Supersedes type's own
	// doc comment for why it carries an Attempt the manifest itself does
	// not.
	Supersedes *Supersedes

	// MaxAttempts is the configured ceiling (L-1). <= 0 means unset, and
	// unset refuses nothing — the shipped default.
	MaxAttempts int

	Bounds  Bounds
	Checker EntryChecker

	// MaxViolations is forwarded to every EntryCheckRequest. Zero means the
	// checker's own default (checker.go's EntryCheckRequest doc comment).
	MaxViolations int

	Provenance Provenance

	// Now is the producer clock. Zero means time.Now(). Feeds CreatedAt,
	// ExpiresAt and the id mint, so a test can hold all three deterministic
	// by supplying one value once.
	Now time.Time

	// Entropy is the id mint's randomness source. Nil means crypto/rand.
	// Reader. Exists for the same reason MintPackageIDAt's own Entropy
	// parameter does: a deterministic test.
	Entropy io.Reader
}

// EntryDeclaration is the caller's classification of one walked file —
// the Role, MediaType and (for a dataset entry) ConformsTo that
// BuildEntrySet's own doc comment says only the caller can supply, because
// LocalFile carries no such metadata.
type EntryDeclaration struct {
	Role       string
	MediaType  string
	ConformsTo string
}

// Supersedes names the prior attempt this pack replaces AND that prior
// attempt's own Attempt number. The manifest's own supersedes field (see
// Document.Supersedes) is a bare package id with no attempt number in it —
// deliberately, per §T2.1's field table — so "attempt = supersedes.attempt
// + 1" is not computable from the manifest alone. Attempt exists on this
// type solely so Pack can check that arithmetic; only ID reaches the
// produced Document.
type Supersedes struct {
	ID      string
	Attempt int
}

// formatMediaType pins the canonical media_type string a dataset entry
// must declare for each format (§T2.2, plan D-11's third row: never
// expressed as a schema conditional because these canonical strings were
// never pinned before this wave).
//
//   - json:   "application/json" — IANA-registered, and the shape the
//     shipped data-package/v1 fixture (orders.json) already uses.
//   - ndjson: "application/x-ndjson" — there is no IANA registration for
//     line-delimited JSON; "application/x-ndjson" is the de facto standard
//     (used by, among others, Docker's and GitHub's own streaming APIs),
//     chosen here over the unregistered "application/jsonl" alternative.
//     Nothing in this repository pinned either string before this file;
//     this choice is recorded as a deviation in the implementer's report.
var formatMediaType = map[string]string{
	FormatJSON:   "application/json",
	FormatNDJSON: "application/x-ndjson",
}

// MediaTypeForFormat exposes formatMediaType's mapping so a later wave
// (verify) can check the same pinned strings this file does, rather than
// re-deriving them and risking the two sides drifting apart.
func MediaTypeForFormat(format string) (string, bool) {
	mediaType, ok := formatMediaType[format]
	return mediaType, ok
}

// containsRelPath reports whether files holds an entry at path.
func containsRelPath(files []LocalFile, path string) bool {
	for _, f := range files {
		if f.RelPath == path {
			return true
		}
	}
	return false
}

// describeFailingCheck renders a failing Check as a message naming the
// entry path, the check id and — when present — the ndjson record number
// the violation was found at, so "the file is wrong" never has to stand in
// for "record 4108 of 90000 is wrong" (spec 05a §T2.6).
func describeFailingCheck(check Check) string {
	var detail string
	if len(check.Violations) > 0 {
		v := check.Violations[0]
		if v.Record != nil {
			detail = fmt.Sprintf(" record %d: %s", *v.Record, v.Message)
		} else {
			detail = fmt.Sprintf(": %s", v.Message)
		}
	}
	return fmt.Sprintf("entry %q (check %q, status %s)%s", check.Path, check.ID, check.Status, detail)
}
