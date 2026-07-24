//go:build livee2e

package livee2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
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

// ErrCheckWaitTimedOut is WaitForRequiredCheck's sentinel. Not part of the
// pinned cross-agent API; a coordination point named in the wave report —
// wave 3-2's scenario families map it to VerdictTimedOut, never to a fail
// (spec 36 §T4: a bounded wait expiring is "we did not wait long enough",
// not "the product is wrong").
var ErrCheckWaitTimedOut = errors.New("livee2e: WaitForRequiredCheck: bounded wait expired before the check reached a terminal state")

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
