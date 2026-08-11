package livee2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// incidents_test.go is scenarios_incidents.go's own gate — P8's AC4
// (spec 08-paths.md §8: "the three live incidents replay offline and fail
// against the binary that shipped before their phase"). Untagged
// deliberately, same reasoning pathdrivability_test.go's own doc comment
// states for its sibling registry: this runs on the plain
// `go test ./internal/livee2e/...` suite `make check` already executes,
// never only under a `-tags=livee2e` run nobody triggers by hand — and
// none of these checks touch the real or logic binary at all, so there is
// no reason to gate them behind that tag.
//
// incidentReplayRepoRoot mirrors conformanceEpicSpecsDir's own convention
// (scenariocoverage_test.go): a relative path from this package's own
// directory, which `go test` always runs from, exactly as that file's own
// "../../docs/..." does — no runtime.Caller machinery needed for a plain
// untagged test.
const incidentReplayRepoRoot = "../.."

// shaPattern is a loose sanity check on FixCommit/FixCommitParent — a full
// git object id is 40 lowercase hex characters. Catches a truncated or
// mistyped sha at parse time rather than leaving a citation nobody can
// resolve.
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// proofRefPattern matches incidentReplay.ProofRefs' own declared shape:
// "<path>:<TestFuncName>".
var proofRefPattern = regexp.MustCompile(`^(.+\.go):([A-Za-z0-9_]+)$`)

// TestIncidentReplaysAreWellFormed refuses a stale or bare registry entry —
// the same discipline TestUndrivenScenariosNameOnlyDeclaredIdsWithReasons
// (scenariocoverage_test.go) already applies one file over: an exemption
// (or, here, a replay entry) without its required facts reads as a
// decision nobody actually made.
//
// MUTATION PROOF (arm 1 — blank field): blanking any entry's
// ArchaeologyFinding and re-running this test reds it, naming the entry's
// own ID.
// MUTATION PROOF (arm 3 — duplicate id): duplicating an ID across two
// entries reds it, naming both.
func TestIncidentReplaysAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range incidentReplays() {
		if r.ID == "" {
			t.Fatalf("incidentReplays(): an entry has an empty ID (Name=%q)", r.Name)
		}
		if seen[r.ID] {
			t.Errorf("incidentReplays() lists %q twice", r.ID)
		}
		seen[r.ID] = true

		if strings.TrimSpace(r.Name) == "" {
			t.Errorf("%s: empty Name", r.ID)
		}
		if strings.TrimSpace(r.FixedByPhase) == "" {
			t.Errorf("%s: empty FixedByPhase", r.ID)
		}
		if strings.TrimSpace(r.CorpusEvidence) == "" {
			t.Errorf("%s: empty CorpusEvidence", r.ID)
		}
		if !shaPattern.MatchString(r.FixCommit) {
			t.Errorf("%s: FixCommit %q is not a 40-char lowercase hex sha", r.ID, r.FixCommit)
		}
		if !shaPattern.MatchString(r.FixCommitParent) {
			t.Errorf("%s: FixCommitParent %q is not a 40-char lowercase hex sha", r.ID, r.FixCommitParent)
		}
		if r.FixCommit == r.FixCommitParent {
			t.Errorf("%s: FixCommit and FixCommitParent are identical (%s) — a fix commit is never its own parent", r.ID, r.FixCommit)
		}
		if strings.TrimSpace(r.ArchaeologyCommand) == "" {
			t.Errorf("%s: empty ArchaeologyCommand", r.ID)
		}
		if strings.TrimSpace(r.ArchaeologyFinding) == "" {
			t.Errorf("%s: empty ArchaeologyFinding", r.ID)
		}
		if len(r.ProofRefs) == 0 {
			t.Errorf("%s: no ProofRefs — a replay entry must point at an existing, shipped test proving today's binary is correct", r.ID)
		}
		if strings.TrimSpace(r.BinaryReplayStatus) == "" {
			t.Errorf("%s: empty BinaryReplayStatus — whether the pre-phase BINARY itself ran must be stated, never left implicit", r.ID)
		}
	}
}

// TestIncidentReplayCount pins the universe to exactly three — spec 08 §6's
// own count ("the three incidents") and §8 AC4's own wording ("The three
// live incidents replay offline..."). Unlike TransitionRows()/
// RestingStates(), this is not a derived-from-code universe (there is no
// enumerator for "an epic's founding incidents") — it is a closed,
// spec-named set, so pinning the literal count here is the direct analogue
// of parseScenarioIDs' own non-empty guard, not the "asserted total inside
// a derived gate" D2 forbids (scenariocoverage_test.go's own doc comment):
// nothing here is re-deriving what TransitionRows()/RestingStates() already
// derive.
func TestIncidentReplayCount(t *testing.T) {
	got := len(incidentReplays())
	if got != 3 {
		t.Fatalf("incidentReplays() returned %d entries, want exactly 3 (spec 08 §6: \"the three incidents\")", got)
	}
}

// TestIncidentReplayCorpusEvidenceExists proves every CorpusEvidence path
// resolves to a real file — a moved or renamed review doc must be caught
// here, not leave a dangling citation nobody notices.
//
// MUTATION PROOF (arm 2 — file does not exist): pointing an entry's
// CorpusEvidence at a nonexistent path and re-running this test reds it,
// naming the entry and the path.
func TestIncidentReplayCorpusEvidenceExists(t *testing.T) {
	for _, r := range incidentReplays() {
		full := filepath.Join(incidentReplayRepoRoot, r.CorpusEvidence)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("%s: CorpusEvidence %q: %v", r.ID, r.CorpusEvidence, err)
		}
	}
}

// TestIncidentReplayProofRefsExistAndDefineTheNamedFunc is this registry's
// own rot check: every ProofRefs entry must name a file that exists AND
// still defines the exact test function named — not merely "a file exists
// somewhere near this name". A renamed or deleted test must red this gate,
// the same discipline missingScenarioPaths()'s own per-file report follows
// one file over.
//
// MUTATION PROOF (arm 2 — file does not exist, second instance): pointing a
// ProofRefs entry at a nonexistent file reds it, naming the entry, the
// path and the ref.
func TestIncidentReplayProofRefsExistAndDefineTheNamedFunc(t *testing.T) {
	for _, r := range incidentReplays() {
		for _, ref := range r.ProofRefs {
			m := proofRefPattern.FindStringSubmatch(ref)
			if m == nil {
				t.Errorf("%s: ProofRefs entry %q is not \"<path>:<TestFuncName>\"", r.ID, ref)
				continue
			}
			path, funcName := m[1], m[2]

			full := filepath.Join(incidentReplayRepoRoot, path)
			raw, err := os.ReadFile(full)
			if err != nil {
				t.Errorf("%s: ProofRefs %q: %v", r.ID, ref, err)
				continue
			}
			needle := "func " + funcName + "("
			if !strings.Contains(string(raw), needle) {
				t.Errorf("%s: ProofRefs %q: %s does not define %q — a stale citation (renamed or deleted test)", r.ID, ref, path, needle)
			}
		}
	}
}
