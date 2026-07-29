//go:build livee2e

package livee2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// The two local system ids the rig scaffolds (the axon/seomatrix shape,
// spec 36 §T1). These are the a2a `--system` ids the checkouts register
// as — distinct from the matrix's own SystemA/SystemB row labels
// (catalogue.go), which name which IDENTITY (provisioner/participant)
// backs a scenario, not which local directory it runs from. systemAlpha is
// provisioner-backed (matrix SystemA); systemBravo is participant-backed
// (matrix SystemB).
const (
	systemAlpha = "alpha"
	systemBravo = "bravo"
)

// harnessSpaceSlug is the space id inside the scaffolded space.yaml (spec
// 36 §9.5: a single lowercase word — see the hyphen backlog item). A
// constant, like catalogue.go's DefaultRepo: one less variable that could
// point the rig somewhere real.
const harnessSpaceSlug = "livee2e"

// harness is what every wave-3-2 scenario family receives: both GitHub
// clients (one per identity — never shared, so a scenario cannot
// accidentally author a boundary assertion with the wrong credential), both
// local checkouts, and the one seam (WaitForRequiredCheck) every family
// needs and that four independent agents must not each reinvent.
type harness struct {
	Cfg       Config
	Pre       Preflight
	Org, Repo string
	SpaceSlug string
	Bin       string
	Prov      *ghClient
	Part      *ghClient
	A, B      *checkout

	// PRFloor is the highest PR number the space carried at the moment
	// ResetSpace finished — every write THIS run makes is numbered above it.
	// Every branch->PR lookup filters on it (runPulls), because a reset can
	// delete branches but not the pull requests that pointed at them, and
	// this tier's branch names are deterministic per (system, verb, id): two
	// runs produce two PRs on the same head ref. Zero when ResetSpace has not
	// run, which degrades to the pre-fix behaviour rather than hiding every
	// PR.
	PRFloor int

	// reds carries the workflow-run ids scenarios declare they reddened ON
	// PURPOSE, for runSpaceCIHealth to account for at the end of the run.
	//
	// A row that makes a gate go red says so BY ID. It is the only honest
	// way to tell that red apart from one nobody meant: a branch-name or
	// title pattern absolves the next probe somebody adds without their
	// having asserted anything, and this classifier has already produced 96
	// false findings once by inferring intent from a field rather than
	// reading a declaration (see spacehealth.go).
	reds   []int64
	redsMu sync.Mutex
}

// claimRed declares that this matrix reddened these workflow runs on purpose.
// Called by the scenario that caused them, with the ids it actually observed
// — never with a guess, and never with a pattern.
//
// Guarded by a mutex even though driveFamilies runs the families serially:
// the failure-recovery family drives two writes from two goroutines, and an
// invariant that holds only because of who happens to call it today is one
// refactor away from a data race that `-race` would report from somewhere
// unrelated.
func (h *harness) claimRed(ids ...int64) {
	h.redsMu.Lock()
	defer h.redsMu.Unlock()
	h.reds = append(h.reds, ids...)
}

// claimedReds returns a copy of what has been claimed so far.
func (h *harness) claimedReds() []int64 {
	h.redsMu.Lock()
	defer h.redsMu.Unlock()
	return append([]int64(nil), h.reds...)
}

// requiredCheckWaitCeiling bounds WaitForRequiredCheck's poll.
// docs/runbooks/live-e2e/run.sh's own ceiling was 30 attempts * 10s = 5
// minutes; matched here as the ceiling MAGNITUDE spec 36 §T4 calls for
// ("bounded waits and an honest 'timed out' verdict, never an optimistic
// pass") — Actions latency on a public repo can legitimately run a few
// minutes.
const requiredCheckWaitCeiling = 5 * time.Minute

// requiredCheckPollInterval is the step between polls.
const requiredCheckPollInterval = 10 * time.Second

// A successful POST /pulls and LIST /pulls are not one atomic visibility
// boundary on GitHub. The 2026-07-29 targeted happy run opened deprecation PR
// #1549, then the immediately-following list omitted it; the PR appeared and
// auto-merged normally while the scenario had already reported a false
// failure. Poll narrowly for that one provider-consistency gap instead of
// rerunning a full matrix.
const (
	prVisibilityAttempts     = 18
	prVisibilityPollInterval = 5 * time.Second
)

// ErrProvisionFailed wraps a ResetSpace failure surfaced through newHarness,
// so a caller can errors.Is-discriminate "provisioning failed" from
// "checkout setup failed" without substring-matching the wrapped message —
// the distinction the wave-3-3 runner needs to render AC-960.1's space-init
// row from the real cause.
var ErrProvisionFailed = errors.New("livee2e: provisioning the test space failed")

// WaitForRequiredCheck polls THROUGH internal/host.CheckStatus — the
// product's own resolver, not a private copy of its check-run selection
// logic — until prNumber's required check completes or the bounded wait
// expires. This is deliberate and is AC-961.1's whole evidence (plan
// §"Constraints a faithful port would get wrong": "row 7 ... must go
// THROUGH the real host.CheckStatus, not a curl").
func (h *harness) WaitForRequiredCheck(ctx context.Context, prNumber int, token string) (host.CheckStatusResult, error) {
	hc := host.NewGitHubHost(nil, defaultAPIRoot)
	deadline := time.Now().Add(requiredCheckWaitCeiling)
	req := host.StatusRequest{
		Repo:       host.Repo{Owner: h.Org, Name: h.Repo},
		PRNumber:   prNumber,
		Credential: host.Credential{Token: token},
	}
	for {
		res, err := hc.CheckStatus(ctx, req)
		if err != nil {
			return host.CheckStatusResult{}, fmt.Errorf("livee2e: WaitForRequiredCheck: %w", err)
		}
		if res.State == "completed" {
			return res, nil
		}
		if time.Now().After(deadline) {
			return host.CheckStatusResult{}, fmt.Errorf("%w after %s (pr #%d)", ErrCheckWaitTimedOut, requiredCheckWaitCeiling, prNumber)
		}
		select {
		case <-ctx.Done():
			return host.CheckStatusResult{}, ctx.Err()
		case <-time.After(requiredCheckPollInterval):
		}
	}
}

// submitted is what one driven write produced: the artifact and the PR the
// funnel opened for it.
type submitted struct {
	ID       string
	Branch   string
	PRNumber int
	HeadSHA  string
}

// DraftAndSubmit runs the whole public author path for one artifact — draft
// with typed fields, submit — and resolves the resulting PR.
//
// It exists on the harness rather than in each scenario family because all
// four families need it and four copies would drift; that is also why it
// resolves the PR through space.BranchName + ListPulls instead of scraping
// `a2a submit`'s stdout. The branch name is the funnel's own deterministic
// identity (the same one its idempotent-retry path looks a PR up by), so this
// keeps working when the CLI's output text changes, and it is the only
// resolution that stays correct after a §T6-d retry — where the first attempt
// may have opened the PR and merely failed to say so.
func (h *harness) DraftAndSubmit(ctx context.Context, c *checkout, artifactType string, extra ...string) (submitted, error) {
	id, unfilled, err := c.Draft(ctx, artifactType, extra...)
	if err != nil {
		return submitted{}, err
	}
	if len(unfilled) > 0 {
		// Not fatal: the draft may still validate. Carried up so a scenario
		// can put it in its Detail — an unfilled field is how we learn a
		// template asks for something the public authoring map cannot describe.
		_ = unfilled
	}
	return h.submitDrafted(ctx, c, id)
}

// submitDrafted runs `a2a submit <id>` for an already-authored
// artifact and resolves the PR the funnel opened for it (space.BranchName +
// pullForBranch, never scraped stdout — see DraftAndSubmit's own doc for
// why). It is DraftAndSubmit's own tail, extracted so a caller that supplies
// specialized public `--field` inputs can reuse the exact same
// submit+resolve path rather than a second, silently-drifting copy of it.
func (h *harness) submitDrafted(ctx context.Context, c *checkout, id string) (submitted, error) {
	branch := space.BranchName(c.System, "submit", id)
	if _, stderr, subErr := c.Run(ctx, "submit", id); subErr != nil {
		return submitted{ID: id, Branch: branch}, fmt.Errorf("livee2e: a2a submit %s (%s): %w: %s", id, c.System, subErr, strings.TrimSpace(stderr))
	}

	pr, err := h.pullForBranch(ctx, branch)
	if err != nil {
		return submitted{ID: id, Branch: branch}, err
	}
	return submitted{ID: id, Branch: branch, PRNumber: pr.Number, HeadSHA: pr.HeadSHA}, nil
}

// pullForBranch finds the PR whose head is branch, open or already closed.
//
// Listing only OPEN pull requests was wrong, and the second live run is what
// showed it: this space runs auto-merge, so a write whose check goes green
// quickly is merged — and therefore CLOSED — before the caller looks. Both
// contract rows reported "submit left no pull request on its branch" for a PR
// that had in fact been created, gated and merged exactly as intended. The
// harness was asking for the wrong state, not the product failing to open a PR.
//
// Open is still PREFERRED: a branch can carry more than one PR over a run's
// lifetime, and the one still open is the one a caller waiting on a gate means.
// Falling back to the highest number picks the most recent of the closed ones.
// runPulls lists every pull request THIS run opened: `state=all` (this
// space auto-merges, so a fast-green write is closed before the caller
// looks) narrowed to numbers above h.PRFloor, the watermark ResetSpace
// records once provisioning is done.
//
// Every branch->PR lookup goes through here rather than calling ListPulls
// directly, so no future lookup can reintroduce the live-run-3 defect by
// forgetting the filter: a reset deletes branches but cannot delete the
// pull requests that pointed at them, and this tier's branch names are
// deterministic per (system, verb, artifact id) — so a prior run's ghost
// PR sits on exactly the head ref this run is about to reuse. See
// ResetSpace's own note for why this is fixed here and not at the matcher.
func (h *harness) runPulls(ctx context.Context) ([]PullState, error) {
	pulls, err := h.Prov.ListPulls(ctx, h.Org, h.Repo, "all")
	if err != nil {
		return nil, err
	}
	out := pulls[:0:0]
	for _, p := range pulls {
		if p.Number > h.PRFloor {
			out = append(out, p)
		}
	}
	return out, nil
}

func (h *harness) pullForBranch(ctx context.Context, branch string) (PullState, error) {
	var best PullState
	err := awaitLookupVisibility(
		ctx,
		prVisibilityAttempts,
		func() error {
			pulls, err := h.runPulls(ctx)
			if err != nil {
				return err
			}
			var found bool
			for _, p := range pulls {
				if p.HeadRef != branch {
					continue
				}
				switch {
				case !found, p.State == "open" && best.State != "open", p.State == best.State && p.Number > best.Number:
					best, found = p, true
				}
			}
			if !found {
				return fmt.Errorf("%w: %s", ErrNoPRForBranch, branch)
			}
			return nil
		},
		func(err error) bool { return errors.Is(err, ErrNoPRForBranch) },
		pauseForPRVisibility,
	)
	if err != nil {
		return PullState{}, err
	}
	return best, nil
}

// pullForBranchContaining is pullForBranch's composite-branch counterpart
// (matchCompositeBranch, branchmatch.go): the ONE additional lookup
// `contract deprecate`'s branch needs, because its branch folds TWO ids
// (BranchName(system, verb, ArtifactID) where ArtifactID is the funnel's own
// sorted, "+"-joined composite of every id one write touched —
// cmd_lifecycle.go's buildRequest) into one branch a caller that only knows
// the bare contract id cannot look up by exact HeadRef equality.
//
// The matching RULE itself (matchCompositeBranch) is pure and lives in an
// untagged file so it is covered by the plain `go test ./internal/livee2e/...`
// suite; this method is the thin, network-touching adapter: list every pull
// (open+closed — same fa3ee82 reasoning pullForBranch's own doc records:
// this space auto-merges, so a fast-green write is merged and closed before
// a caller looks), narrow to the shape matchCompositeBranch needs, then map
// its match back to the original PullState (which carries fields
// matchCompositeBranch's own branchPull deliberately does not, e.g. HeadSHA).
func (h *harness) pullForBranchContaining(ctx context.Context, system, verb, artifactID string) (PullState, error) {
	var best PullState
	err := awaitLookupVisibility(
		ctx,
		prVisibilityAttempts,
		func() error {
			pulls, err := h.runPulls(ctx)
			if err != nil {
				return err
			}
			bps := make([]branchPull, 0, len(pulls))
			for _, p := range pulls {
				bps = append(bps, branchPull{HeadRef: p.HeadRef, State: p.State, Number: p.Number})
			}
			match, err := matchCompositeBranch(bps, system, verb, artifactID)
			if err != nil {
				return err
			}
			for _, p := range pulls {
				if p.HeadRef == match.HeadRef && p.Number == match.Number {
					best = p
					return nil
				}
			}
			// Unreachable in practice: match was derived FROM pulls, so it
			// must be present. A defensive error beats a zero PullState.
			return fmt.Errorf("livee2e: pullForBranchContaining: matched PR #%d (%s) not found in the pulls list it was derived from", match.Number, match.HeadRef)
		},
		func(err error) bool { return errors.Is(err, ErrNoBranchMatch) },
		pauseForPRVisibility,
	)
	if err != nil {
		return PullState{}, err
	}
	return best, nil
}

func pauseForPRVisibility(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(prVisibilityPollInterval):
		return nil
	}
}

// countPRsForBranch counts every PR (open+closed) whose head is branch — the
// AC-973.1 row's own "no PR opened" assertion (a refused `contract publish`
// must never reach the funnel) compares this count before and after the
// refused call, rather than asserting on pullForBranch's absence: the
// refused call's branch is `contract-publish`'s own deterministic name,
// which a PRIOR, successful publish on this same contract may already have
// used (BranchName is keyed on system+verb+artifactID, not on version) —
// so "a PR already exists on this branch" is expected, and the row's actual
// claim is narrower: this SPECIFIC call did not add another one.
func (h *harness) countPRsForBranch(ctx context.Context, branch string) (int, error) {
	pulls, err := h.runPulls(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range pulls {
		if p.HeadRef == branch {
			n++
		}
	}
	return n, nil
}

// AwaitCheck waits for a PR's required check using the credential of the
// checkout that authored it — never a fixed token. Reading a boundary
// scenario's check through the provisioner would be the §T5 mistake one layer
// down: the observation would be made by an identity the scenario is not about.
func (h *harness) AwaitCheck(ctx context.Context, c *checkout, prNumber int) (host.CheckStatusResult, error) {
	return h.WaitForRequiredCheck(ctx, prNumber, c.Token)
}

// resolvedCheckRun adapts what the PRODUCT resolved into the untagged
// CheckRunRef that ExecutedRefIsCurrent (refassert.go) consumes.
//
// It is the seam that lets row 15 (executed-ref-not-stale, AC-960.5) be
// driven without a second copy of the check-run selection rule: the name and
// the executed ref both come out of host.CheckStatus, which is the one place
// spec 34 §2's anchored, compound-preferred selection lives. It exists as a
// named function rather than four inline struct literals so the four wave-3-2
// families cannot each decide differently which field means what.
//
// CheckStatusResult.HeadSHA was added for exactly this: before it, the
// product could say WHICH SHAPE answered but not WHICH REF, and a caller
// could not tell a fresh green from a green computed against a stale merge
// commit (spec 36 §T6-b).
func resolvedCheckRun(res host.CheckStatusResult) CheckRunRef {
	return CheckRunRef{
		Name:       res.Name,
		HeadSHA:    res.HeadSHA,
		Status:     res.State,
		Conclusion: res.Conclusion,
	}
}

// newHarness builds the binary, resets the test space, constructs both
// GitHub clients, and wires both checkouts — the harness every scenario
// family receives. The returned func cleans up every temp dir this
// construction created; callers must defer it unconditionally (it is never
// nil, even on an error return, so `h, cleanup, err := newHarness(...);
// defer cleanup()` is always safe).
//
// ResetSpace is called HERE rather than left for the caller to sequence,
// which departs from a literal reading of "build the binary, construct both
// clients, set up both checkouts" (recorded as a deviation in the wave
// report): `a2a connect` needs a repo that already carries a valid
// space.yaml on main, which — on a first-ever run, or after a previous run
// left the space in an unknown state — only exists once ResetSpace has run.
// Folding it in here is also what spec 36 §T4 calls for: "every run resets
// the test space to a known state" is a property of USING the harness, not
// an opt-in step a scenario family might forget.
func newHarness(ctx context.Context, cfg Config, pre Preflight) (*harness, func(), error) {
	noop := func() {}

	if err := assertNoAmbientAPIRoot(os.Getenv); err != nil {
		return nil, noop, err
	}

	work, err := os.MkdirTemp("", "livee2e-harness-*")
	if err != nil {
		return nil, noop, fmt.Errorf("livee2e: newHarness: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(work) }

	version, err := harnessBinaryVersion()
	if err != nil {
		cleanup()
		return nil, noop, fmt.Errorf("livee2e: newHarness: %w", err)
	}

	bin, err := buildBinary(ctx, work, version)
	if err != nil {
		cleanup()
		return nil, noop, fmt.Errorf("livee2e: newHarness: %w", err)
	}

	h := &harness{
		Cfg:       cfg,
		Pre:       pre,
		Org:       cfg.Org,
		Repo:      DefaultRepo,
		SpaceSlug: harnessSpaceSlug,
		Bin:       bin,
		Prov:      &ghClient{Token: cfg.ProvisionerToken},
		Part:      &ghClient{Token: cfg.ParticipantToken},
	}

	if err := h.ResetSpace(ctx); err != nil {
		cleanup()
		return nil, noop, fmt.Errorf("livee2e: newHarness: %w: %w", ErrProvisionFailed, err)
	}

	h.A = &checkout{
		Dir: filepath.Join(work, "A"), System: systemAlpha,
		Token: cfg.ProvisionerToken, Login: pre.ProvisionerLogin,
		Bin: bin, SpaceSlug: harnessSpaceSlug, Peer: systemBravo,
	}
	h.B = &checkout{
		Dir: filepath.Join(work, "B"), System: systemBravo,
		Token: cfg.ParticipantToken, Login: pre.ParticipantLogin,
		Bin: bin, SpaceSlug: harnessSpaceSlug, Peer: systemAlpha,
	}

	spaceURL := "https://github.com/" + h.Org + "/" + h.Repo
	if err := setupCheckout(ctx, h.A, spaceURL); err != nil {
		cleanup()
		return nil, noop, fmt.Errorf("livee2e: newHarness: checkout A: %w", err)
	}
	if err := setupCheckout(ctx, h.B, spaceURL); err != nil {
		cleanup()
		return nil, noop, fmt.Errorf("livee2e: newHarness: checkout B: %w", err)
	}

	return h, cleanup, nil
}
