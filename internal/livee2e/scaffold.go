package livee2e

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Participant is one row `a2a space init` needs patched into the scaffolded
// space.yaml, ported from the python heredoc inside
// docs/runbooks/live-e2e/reset.sh (lines ~57-80). Owner is a PARAMETER —
// reset.sh hardcodes `ydnikolaev` and `a2a-e2e-test-bot`; this package's
// untagged files never do (spec 36's identities-are-derived-never-literal
// rule).
type Participant struct {
	// System is the participant's system id inside the space (e.g. "alpha").
	System string
	// Org is the GitHub org the participant's own repo lives under.
	Org string
	// Section is the participant's write section (e.g. "alpha/").
	Section string
	// Owner is the GitHub login reset.sh writes as this participant's
	// `owners:` entry — a parameter, never a literal in this package.
	Owner string
	// Joined is the ISO date reset.sh stamps as `joined:`.
	Joined string
}

// ErrParticipantsPlaceholderNotFound is returned by PatchSpaceParticipants
// when the space YAML does not contain the `participants: []` placeholder
// `a2a space init` emits. Silently returning the input unchanged would
// scaffold a space with NO participants and a green reset step — a strictly
// worse failure than a loud error, because nothing downstream would notice
// until a scenario tried to act as a participant that was never added.
var ErrParticipantsPlaceholderNotFound = errors.New("livee2e: participants placeholder not found in space.yaml")

// spaceParticipantsPlaceholder matches the `participants: []` line
// `a2a space init` emits — including its trailing same-line comment and any
// immediately-following comment-only continuation lines — exactly the shape
// reset.sh's python `re.sub(r'participants: \[\][^\n]*\n(\s+#[^\n]*\n)*', ...)`
// matches against the real space-template/space.yaml.
var spaceParticipantsPlaceholder = regexp.MustCompile(`(?m)^participants: \[\][^\n]*\n(?:[ \t]+#[^\n]*\n)*`)

// PatchSpaceParticipants replaces the `participants: []` placeholder (and its
// trailing comment lines) `a2a space init` scaffolds, with the given rows
// rendered in the same inline-mapping shape reset.sh uses:
//
//	participants:
//	  - {system: alpha, org: ORG, section: alpha/, owners: [login], status: active, joined: 2026-07-24}
//
// status is always "active": a reset scaffold seeds active participants — a
// non-active row is a scenario's own job to create, not the scaffold's (P8
// wave 31: that job is SetParticipantStatus below, applied mid-path, on a
// space.yaml that has already been through one render — never a genesis-time
// literal here; see that function's own doc comment for why a
// genesis-departed row cannot answer a scenario that must first ADDRESS the
// counterparty before it leaves).
//
// Returns ErrParticipantsPlaceholderNotFound if the placeholder is absent —
// see that error's doc comment for why this must be a hard failure, not a
// silent no-op.
func PatchSpaceParticipants(spaceYAML string, ps []Participant) (string, error) {
	loc := spaceParticipantsPlaceholder.FindStringIndex(spaceYAML)
	if loc == nil {
		return "", ErrParticipantsPlaceholderNotFound
	}

	rendered := "participants:\n"
	for _, p := range ps {
		rendered += fmt.Sprintf(
			"  - {system: %s, org: %s, section: %s, owners: [%s], status: active, joined: %s}\n",
			p.System, p.Org, p.Section, p.Owner, p.Joined,
		)
	}

	return spaceYAML[:loc[0]] + rendered + spaceYAML[loc[1]:], nil
}

// ErrParticipantRowNotFound is returned by SetParticipantStatus when
// spaceYAML carries no participant row naming system.
var ErrParticipantRowNotFound = errors.New("livee2e: no participant row for that system")

// participantStatusPattern matches ONE participant row's own status field —
// "status: active" or "status: left", capturing the value — the exact token
// PatchSpaceParticipants renders. Applied line-by-line by SetParticipantStatus
// (never against the whole document at once), so only the row already
// matched by its own `system: <id>,` marker can ever be rewritten.
var participantStatusPattern = regexp.MustCompile(`status: (active|left)`)

// SetParticipantStatus rewrites the status field of the participant row
// naming system — matched by its own "system: <id>," token, the exact shape
// PatchSpaceParticipants renders — to status ("active" or "left"), leaving
// every other row, field and byte untouched.
//
// This is PatchSpaceParticipants' MID-PATH counterpart: that function only
// ever matches the pre-render `participants: []` placeholder `a2a space
// init` emits, so it can render a status ONCE, at genesis, never rewrite a
// row it (or a prior scenario) already committed. SetParticipantStatus is
// what a scenario reaches for AFTER genesis, on a space.yaml some earlier
// path step has already extended — the primitive P8 wave 31's own Family 15
// drivers need (pathcatalogue_paths.go), after wave 30B's genesis-time
// departure (2026-08-11, since reverted) was proven wrong by the real
// conformance matrix: REF-006 refuses SUBMITTING an envelope addressed to an
// already-departed system, so a scenario that must first ADDRESS the
// counterparty (name it in `to`/`required_approvers`) and only THEN watch it
// leave can never be answered by seeding "left" before that submission ever
// exists. See pathcatalogue_paths.go's own Family 15 doc comment for the
// full finding.
//
// alreadyAtStatus reports true (with spaceYAML returned UNCHANGED) when
// system's row already carries status — provision_live.go's
// SetParticipantStatusMidPath uses this to skip an empty commit rather than
// authoring a no-op one.
//
// Returns ErrParticipantRowNotFound if no row names system.
func SetParticipantStatus(spaceYAML, system, status string) (patched string, alreadyAtStatus bool, err error) {
	lines := strings.Split(spaceYAML, "\n")
	marker := "system: " + system + ","
	found := false
	for i, line := range lines {
		if !strings.Contains(line, marker) {
			continue
		}
		found = true
		loc := participantStatusPattern.FindStringSubmatchIndex(line)
		if loc == nil {
			return "", false, fmt.Errorf("livee2e: SetParticipantStatus: participant row for %q has no recognizable status field: %q", system, line)
		}
		if line[loc[2]:loc[3]] == status {
			return spaceYAML, true, nil
		}
		lines[i] = line[:loc[2]] + status + line[loc[3]:]
		break
	}
	if !found {
		return "", false, fmt.Errorf("%w: %q", ErrParticipantRowNotFound, system)
	}
	return strings.Join(lines, "\n"), false, nil
}

// RenderCODEOWNERS renders the trust-root-only CODEOWNERS file reset.sh
// writes: /space.yaml, /CODEOWNERS and /.github/** are gated to a single
// code owner because they define WHO may write, so they cannot be governed
// by "is the author a member" — everything else is V3 diff-authz + document
// status. owner is the code-owner login, a PARAMETER (never a literal in
// this package).
func RenderCODEOWNERS(owner string) string {
	return "# Trust-root paths only: these define WHO may write, so they cannot be\n" +
		"# governed by \"is the author a member\". Everything else is V3 + status.\n" +
		"/space.yaml   @" + owner + "\n" +
		"/CODEOWNERS   @" + owner + "\n" +
		"/.github/**   @" + owner + "\n"
}

// PinCandidateWorkflow rewrites the two reusable-workflow calls in a
// scaffolded space to one immutable public candidate commit. It pins both
// independent resolution axes: GitHub's `uses:` target and the Go module
// revision passed as `a2a-ref`. Exact cardinality is enforced so a new caller
// job cannot silently remain on a release tag.
func PinCandidateWorkflow(src, sha string) (string, error) {
	if !candidateSHAPattern.MatchString(sha) {
		return "", fmt.Errorf("livee2e: candidate SHA must be full lowercase 40-hex")
	}
	lines := strings.Split(src, "\n")
	uses, refs := 0, 0
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		const reusable = "uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@"
		if strings.HasPrefix(trimmed, reusable) {
			indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
			lines[i] = indent + reusable + sha
			uses++
			continue
		}
		if strings.HasPrefix(trimmed, "mode: ") {
			indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
			lines = append(lines[:i+1], append([]string{indent + `a2a-ref: "` + sha + `"`}, lines[i+1:]...)...)
			i++
			refs++
		}
	}
	if uses != 2 || refs != 2 {
		return "", fmt.Errorf("livee2e: candidate workflow pin cardinality uses=%d refs=%d, want 2/2", uses, refs)
	}
	return strings.Join(lines, "\n"), nil
}

// ErrNoTemplateFloor is returned by TemplateMinBinaryVersion when the space
// template's own `min_binary_version:` pin cannot be read. Fail-closed on
// purpose: guessing a floor here would build a harness binary that either
// cannot write to the space it just scaffolded, or claims a version the
// template never sanctioned.
var ErrNoTemplateFloor = errors.New("livee2e: space template declares no min_binary_version")

// templateFloorPattern matches the template's write-floor pin. Anchored at
// the line start so a `min_binary_version` mentioned inside a comment or a
// nested mapping cannot answer for the real one.
var templateFloorPattern = regexp.MustCompile(`(?m)^min_binary_version:\s*([0-9]+\.[0-9]+\.[0-9]+)\b`)

// TemplateMinBinaryVersion reads the space template's own CC-085 write floor
// out of its space.yaml.
//
// It is what the live tier stamps into the binary it builds, INSTEAD of a
// hand-maintained constant. A constant is not merely inconvenient here: the
// harness scaffolds the test space from this same template, so the moment the
// template's floor moves past the constant, every write scenario in the matrix
// fails a CC-085 refusal that has nothing to do with the product — a whole
// matrix reporting red for a reason no row names. Sourcing both from one place
// makes that state unreachable.
//
// Stamping EXACTLY the floor is also deliberate: it is the minimal version
// that may write, so the happy-path rows run at the boundary rather than
// comfortably above it, and row 17 (stale-write-floor-refused) still proves
// the refusal by raising the space's floor above the binary.
func TemplateMinBinaryVersion(spaceYAML string) (string, error) {
	m := templateFloorPattern.FindStringSubmatch(spaceYAML)
	if m == nil {
		return "", fmt.Errorf("%w: no `min_binary_version: <x.y.z>` line found", ErrNoTemplateFloor)
	}
	return m[1], nil
}
