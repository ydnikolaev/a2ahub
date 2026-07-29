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
// non-active row is a scenario's own job to create, not the scaffold's.
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
