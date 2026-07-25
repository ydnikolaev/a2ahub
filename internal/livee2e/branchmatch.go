package livee2e

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNoBranchMatch is matchCompositeBranch's "not found" sentinel — the
// composite-branch counterpart of ErrNoPRForBranch (harness_live.go). Kept
// as its own error (rather than reusing ErrNoPRForBranch directly) because
// this file is deliberately UNTAGGED — the branch-matching RULE is pure
// logic with no network dependency, and keeping it out from behind the
// `livee2e` build tag is what lets `go test ./internal/livee2e/...` (the
// plain, untagged suite) cover it, rather than only `-tags=livee2e`.
// ErrNoPRForBranch lives in harness_live.go, which is tagged; an untagged
// file cannot reference a tagged file's symbols (the identifier simply does
// not exist in the untagged build), so this is a second, deliberately
// analogous sentinel, not a duplicate by oversight. harness_live.go's
// pullForBranchContaining maps both errors into the SAME VerdictTimedOut
// class via verdictForError, so a caller sees no difference.
var ErrNoBranchMatch = errors.New("livee2e: no branch matches this composite artifact id")

// ErrAmbiguousBranchMatch is returned when more than one DISTINCT branch
// under the (system, verb) prefix carries artifactID as one of its
// "+"-separated composite ids. This is never resolved by picking one — an
// ambiguous match is a fact the caller must see and fail on, not a coin
// flip the harness makes silently (P36 §10's amendment: "a live tier that
// guesses is worse than one that fails").
var ErrAmbiguousBranchMatch = errors.New("livee2e: more than one branch matches this artifact id")

// branchPull is the minimal PR shape matchCompositeBranch needs. Deliberately
// NOT client_live.go's PullState, which is livee2e-build-tag-gated and so
// unavailable to this untagged file; harness_live.go's
// pullForBranchContaining adapts []PullState -> []branchPull before calling
// in, and maps the match back to the original PullState after.
type branchPull struct {
	HeadRef string
	State   string
	Number  int
}

// matchCompositeBranch resolves the ONE PR whose branch is the write
// funnel's deterministic name for (system, verb) — space.BranchName(system,
// verb, ArtifactID), where ArtifactID is cmd_lifecycle.go's buildRequest own
// sorted, "+"-joined composite of every id one write touched
// (`ArtifactID: strings.Join(sorted, "+")`) — and whose composite carries
// artifactID as one of its EXACT "+"-separated components.
//
// This exists because pullForBranch's own exact-HeadRef-equality lookup is
// right for every verb that writes a SINGLE subject (submit, withdraw,
// contract publish/retire/adopt); `contract deprecate` is the one verb in
// this matrix that folds TWO ids (the deprecated contract + the linked
// deprecation announcement) into one branch, so a caller that only knows the
// bare contract id needs this second lookup to find it.
//
// Matching a "+"-separated COMPONENT, never a substring of the branch name,
// is deliberate: XC-alpha-foo must not match a branch whose composite
// carries XC-alpha-foo-bar (a textual substring of the branch name, but a
// DIFFERENT contract). Scoping by BOTH system and verb via the exact prefix
// "a2a/<system>/<verb>/" — not just artifactID — means this can never cross
// into another verb's branch, or another system's, even if both happened to
// touch an id containing the same text.
//
// More than one DISTINCT branch matching is refused (ErrAmbiguousBranchMatch)
// rather than resolved by picking one. Among the PRs on the ONE matching
// branch, selection mirrors pullForBranch's own: prefer OPEN, else the
// highest PR number (this space auto-merges, so a fast-green write is merged
// and closed before a caller looks — fa3ee82).
func matchCompositeBranch(pulls []branchPull, system, verb, artifactID string) (branchPull, error) {
	prefix := "a2a/" + system + "/" + verb + "/"

	branches := map[string][]branchPull{}
	for _, p := range pulls {
		if !strings.HasPrefix(p.HeadRef, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(p.HeadRef, prefix)
		for _, part := range strings.Split(suffix, "+") {
			if part == artifactID {
				branches[p.HeadRef] = append(branches[p.HeadRef], p)
				break
			}
		}
	}

	switch len(branches) {
	case 0:
		return branchPull{}, fmt.Errorf("%w: no branch under %q carries %s as one of its composite artifact ids",
			ErrNoBranchMatch, prefix, artifactID)
	case 1:
		// exactly one candidate branch — fall through to PR selection below.
	default:
		names := make([]string, 0, len(branches))
		for b := range branches {
			names = append(names, b)
		}
		sort.Strings(names)
		return branchPull{}, fmt.Errorf("%w: %s matches %d branches under %q: %s",
			ErrAmbiguousBranchMatch, artifactID, len(branches), prefix, strings.Join(names, ", "))
	}

	var candidates []branchPull
	for _, v := range branches {
		candidates = v
	}

	var best branchPull
	var found bool
	for _, p := range candidates {
		switch {
		case !found, p.State == "open" && best.State != "open", p.State == best.State && p.Number > best.Number:
			best, found = p, true
		}
	}
	return best, nil
}
