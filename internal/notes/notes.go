package notes

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"

	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

// Action is one change's actionability directive: whether the agent
// running a2a needs to do anything about this change (scope), why
// (why), and — when there is something to do — how to detect whether it
// applies (detect) and what to run (run). scope: "space" directives are
// commands for the reading agent to run through the normal write funnel;
// a2a never runs them itself.
type Action struct {
	Scope  string   `yaml:"scope" json:"scope"`
	Why    string   `yaml:"why" json:"why"`
	Detect []string `yaml:"detect,omitempty" json:"detect,omitempty"`
	Run    []string `yaml:"run,omitempty" json:"run,omitempty"`
}

// KindKnownIssue is the one Change.Kind value a renderer must treat
// differently, so it is named here rather than spelled as a literal at each
// surface. The other kinds (feat/fix/break/schema/policy — the schema's enum is
// the full list) all describe something that CHANGED in that version and render
// identically; a known-issue describes something that is BROKEN OR INCOMPLETE
// right now, which is a different statement to a reader and especially to an
// agent: it means "do not rely on this surface, use the named alternative". It
// remains valid for historical release documents. Current limitations live
// once in releasenotes/current/known-issues.yaml and are attached after range
// selection until the fix ships and removes them there.
const KindKnownIssue = "known-issue"

// UnreleasedSentinel is the ReleaseNotes.Released value an authored-ahead-of
// -its-tag version carries instead of a real date (one-answer-2026-08 P6,
// B12). Notes are written while changes are fresh (docs/runbooks/release.md
// Phase 1 step 1), which is BEFORE the tag exists; the sentinel is what lets
// that window stay honest — the notes may NAME the version without claiming
// it was RELEASED on a date. scripts/check-release-record.sh refuses a real
// date on a version with no matching `git tag`, offline, every commit; the
// schema (schemas/release-notes/v1/release-notes.schema.json) accepts this
// exact string via `released`'s pattern. CLI (`a2a whatsnew`), the MCP
// StructuredContent and internal/html's ReleaseNote all print Released
// verbatim, so the sentinel reaches them unmodified; RenderMarkdown (this
// package) is the one renderer that special-cases it — see markdown.go.
const UnreleasedSentinel = "unreleased"

// Change is one entry in a release-notes file's changes list.
type Change struct {
	ID      string   `yaml:"id" json:"id"`
	Kind    string   `yaml:"kind" json:"kind"`
	Impact  string   `yaml:"impact" json:"impact"`
	Subject string   `yaml:"subject" json:"subject"`
	Detail  string   `yaml:"detail" json:"detail"`
	Affects []string `yaml:"affects,omitempty" json:"affects,omitempty"`
	Action  Action   `yaml:"action" json:"action"`
}

// ReleaseNotes is the parsed structural shape of one releasenotes/<version>
// .yaml file (P31 schema, schemas/release-notes/v1/release-notes.schema.
// json) — a structural YAML decode only, mirroring internal/space's
// ParseManifest/Manifest split (D-011): schema/policy validity is a
// separate concern (internal/schema's ValidateReleaseNotes), never
// re-implemented here.
type ReleaseNotes struct {
	Schema   string   `yaml:"schema" json:"schema"`
	Version  string   `yaml:"version" json:"version"`
	Released string   `yaml:"released" json:"released"`
	Headline string   `yaml:"headline" json:"headline"`
	Changes  []Change `yaml:"changes" json:"changes"`

	// Raw holds the exact bytes ParseReleaseNotes was given, so a caller
	// can hand them, unmodified, to a schema-validation seam that needs
	// the full document rather than just this struct's typed subset (the
	// same rationale space.Manifest.Raw documents). json:"-" keeps it out
	// of the agent-facing `whatsnew --json` / MCP StructuredContent shape —
	// that surface is the machine contract, not a debug dump.
	Raw []byte `yaml:"-" json:"-"`
}

// ParseReleaseNotes structurally parses raw release-notes YAML bytes.
// Malformed YAML is a typed error wrapping ErrReleaseNotesInvalid; this is
// a syntax check only — schema validity is internal/schema's job (mirrors
// space.ParseManifest exactly).
func ParseReleaseNotes(raw []byte) (ReleaseNotes, error) {
	const op = "ParseReleaseNotes"
	var rn ReleaseNotes
	if err := yaml.Unmarshal(raw, &rn); err != nil {
		return ReleaseNotes{}, &Error{Op: op, Err: ErrReleaseNotesInvalid}
	}
	if !releasedFieldWellFormed(rn.Released) {
		return ReleaseNotes{}, &Error{Op: op, Input: rn.Version, Err: fmt.Errorf("%w: released: %q must be an ISO date (YYYY-MM-DD) or the %q sentinel", ErrReleaseNotesInvalid, rn.Released, UnreleasedSentinel)}
	}
	rn.Raw = raw
	return rn, nil
}

// releasedReISODate is the shape `released` may take besides the sentinel.
var releasedReISODate = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// releasedFieldWellFormed is a POLICY check, and it lives here rather than in
// schemas/release-notes/v1/release-notes.schema.json for a reason the schema
// itself cannot state: that file is byte-frozen by schemas/published-v1.sha256,
// because its bytes decide whether documents ALREADY COMMITTED to shared spaces
// are valid, so an in-place edit is a compatibility event however additive it
// looks (docs/the-plan/plan §5.7.1, ADR-018, root AGENTS.md §Anti-patterns #21).
//
// one-answer-2026-08 P6 first added a `pattern` to that schema and the
// operational-confidence guard refused it by name, teaching this exact move at
// the moment it was needed. The sanctioned shape is: leave the published bytes
// alone, constrain in a layer that is not frozen, and accept that the refusal
// is policy-class — which means the schema-only fixture corpus cannot express
// it and it is proven in package tests instead. The missing policy-fixture
// corpus is a row in docs/backlog.md.
//
// An EMPTY value stays legal here: `released` is not in the schema's own
// required list, and a notes file that has not chosen yet is a different state
// from one that chose something malformed. `scripts/check-release-record.sh`
// is what decides whether a well-formed value is TRUE — a real date must have
// a real tag — and that is deliberately a separate question from this one.
func releasedFieldWellFormed(v string) bool {
	return v == "" || v == UnreleasedSentinel || releasedReISODate.MatchString(v)
}

// Load reads every *.yaml file at the root of fsys (typically
// releasenotes.FS, the embedded P31 corpus), parses each with
// ParseReleaseNotes, and returns them sorted by version ASCENDING
// (internal/version.OlderThan — the same comparator internal/space and
// internal/release use, spec 19 §7 anti-dup). A parse failure for any
// file is a returned error; there is no partial/best-effort result.
func Load(fsys fs.FS) ([]ReleaseNotes, error) {
	const op = "Load"

	files, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, &Error{Op: op, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
	}

	out := make([]ReleaseNotes, 0, len(files))
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, &Error{Op: op, Input: f, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
		}
		rn, err := ParseReleaseNotes(raw)
		if err != nil {
			return nil, &Error{Op: op, Input: f, Err: err}
		}
		out = append(out, rn)
	}

	var sortErr error
	sort.SliceStable(out, func(i, j int) bool {
		if sortErr != nil {
			return false
		}
		older, err := version.OlderThan(out[i].Version, out[j].Version)
		if err != nil {
			sortErr = err
			return false
		}
		return older
	})
	if sortErr != nil {
		return nil, &Error{Op: op, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, sortErr)}
	}

	return out, nil
}

// Since returns the entries of all (assumed already version-ascending,
// the shape Load returns) whose version is strictly GREATER than from and
// LESS-THAN-OR-EQUAL-TO upto, preserving ascending order. from == ""
// means "everything up to upto"; upto == "" means "everything strictly
// after from". A malformed from/upto/entry version (internal/version.
// OlderThan's ErrInvalidVersion) fails CLOSED for that comparison — the
// entry is excluded rather than guessed into the result.
func Since(all []ReleaseNotes, from, upto string) []ReleaseNotes {
	out := make([]ReleaseNotes, 0, len(all))
	for _, rn := range all {
		if from != "" {
			newer, err := version.OlderThan(from, rn.Version)
			if err != nil || !newer {
				continue
			}
		}
		if upto != "" {
			tooNew, err := version.OlderThan(upto, rn.Version)
			if err != nil || tooNew {
				continue
			}
		}
		out = append(out, rn)
	}
	return out
}

// Exactly returns the single entry of all whose Version equals version,
// and whether one was found.
func Exactly(all []ReleaseNotes, version string) (ReleaseNotes, bool) {
	for _, rn := range all {
		if rn.Version == version {
			return rn, true
		}
	}
	return ReleaseNotes{}, false
}
