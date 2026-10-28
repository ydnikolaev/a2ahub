package host

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/fakegithub"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// The host property suite (local-space-2026-10 spec 07 §6.1).
//
// One table holds the PRODUCTION hosts: the GitHub host against
// testkit/fakegithub, and the local host. Each property is a function over a
// host; a property that only some hosts can promise is gated on the host's own
// declared Capabilities(), never on its name, and a host that does not declare
// it is logged as "not asserted" rather than skipped or failed. A third host is
// a new row in confCases and nothing else.
//
// Properties already proven for one host elsewhere (the local host's landing
// algorithm in local_test.go, head-branch uniqueness in
// head_branch_uniqueness_test.go) are re-proven here over the table through the
// same helpers, not rewritten.

// fakeHostExclusion is printed by every run of the suite (go test -v).
//
// FakeHost is excluded on purpose (spec 07 §T1.1). It is a test double whose
// permissive default OpenPR mints a new PR on every call; internal/space tests
// rely on that, and TestOneOpenPRPerHeadBranch_PermissiveFakeReds documents
// that it fails the uniqueness property by design. Making it conform has an
// unsized blast radius and would prove nothing about a shipped host.
const fakeHostExclusion = "host conformance: FakeHost is excluded from the table. It is a test double whose permissive default OpenPR " +
	"is relied on by internal/space tests and fails the head-branch-uniqueness property on purpose " +
	"(TestOneOpenPRPerHeadBranch_PermissiveFakeReds); the suite measures shipped hosts only"

// confHost is one constructed host implementation with the repository it works
// against.
type confHost struct {
	// rig carries the bare space repository, the two participant clones (axon,
	// beta) and the push helper.
	rig *localRig
	// open builds a host value over the rig's repository. check is the land check
	// a synchronously landing host consults; a host that lands elsewhere ignores
	// it. author is the identity a host that asserts its own writer uses.
	open func(t *testing.T, check LandCheckFunc, author string) Host
	// requests returns the API requests the host has made, or nil for a host
	// that has no API.
	requests func() []string
}

// confCase is one row of the table.
type confCase struct {
	name  string
	build func(t *testing.T) confHost
}

// confCases is the table. FakeHost is deliberately absent (fakeHostExclusion).
var confCases = []confCase{
	{name: "github-fakegithub", build: buildGitHubConf},
	{name: "local", build: buildLocalConf},
}

func buildGitHubConf(t *testing.T) confHost {
	t.Helper()
	fx := spacefixture.New(t, "axon", "beta")
	srv := fakegithub.New(t, fx.OriginDir)
	// With auto-merge on, the fake merges the PR inside OpenPR and its create
	// handler only refuses a second PR for a head while one is OPEN, so the
	// second OpenPR of the uniqueness property would mint PR #2. Real GitHub is
	// in the other state when OpenPR returns: armed and open, the merge waiting
	// on the space's own check. This is the faithful stand-in for that window.
	srv.AutoMerge = false
	return confHost{
		// A plain rig literal, not newLocalRig: PrepareLocalSpace would install the
		// pre-receive hook that refuses the fake's merge push. It is used only for
		// its pushHead, which pushes through the host's own PushBranch.
		rig: &localRig{fx: fx, origin: fx.OriginDir},
		open: func(*testing.T, LandCheckFunc, string) Host {
			return NewGitHubHost(http.DefaultClient, srv.URL)
		},
		requests: srv.Requests,
	}
}

func buildLocalConf(t *testing.T) confHost {
	t.Helper()
	r := newLocalRig(t)
	return confHost{
		rig: r,
		open: func(t *testing.T, check LandCheckFunc, author string) Host {
			return r.host(t, check, author)
		},
	}
}

// confOpen is the OpenPR request every property sends. ExpectedHeadSHA is set
// because the GitHub host refuses a request without it; the local host accepts
// it when it equals the head.
func confOpen(head, headSHA string) OpenPRRequest {
	return OpenPRRequest{Repo: testRepo, Head: head, Base: "main", Title: "conformance write", Body: "body", ExpectedHeadSHA: headSHA}
}

func confFind(head string) FindPRRequest {
	return FindPRRequest{Repo: testRepo, Branch: head}
}

// confProperty is one row of §6.1.
type confProperty struct {
	name string
	run  func(t *testing.T, c confHost)
}

var confProperties = []confProperty{
	{name: "head-branch-uniqueness", run: propHeadBranchUniqueness},
	{name: "open-and-find", run: propOpenAndFind},
	{name: "synchronous-landing", run: propSynchronousLanding},
	{name: "synchronous-landing-moved-tip", run: propSynchronousLandingMovedTip},
	{name: "refusal", run: propRefusal},
	{name: "credential", run: propCredential},
}

// TestHostConformance runs every property over every host in the table, one
// fresh repository per pair, in parallel.
func TestHostConformance(t *testing.T) {
	t.Log(fakeHostExclusion)
	for _, p := range confProperties {
		for _, c := range confCases {
			t.Run(p.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				p.run(t, c.build(t))
			})
		}
	}
}

// TestHostConformance_TableShape pins what the table is: FakeHost stays out, no
// two rows share a name, and every capability-gated property has at least one
// host that is asserted by it, so a gate cannot go vacuous unseen.
func TestHostConformance_TableShape(t *testing.T) {
	t.Log(fakeHostExclusion)
	seen := map[string]bool{}
	var landsSync, noCredential, needsCredential int
	for _, c := range confCases {
		if seen[c.name] {
			t.Errorf("two table rows are named %q", c.name)
		}
		seen[c.name] = true
		h := c.build(t).open(t, validCheck, "local:axon")
		if _, isFake := h.(*FakeHost); isFake {
			t.Errorf("row %q is a *FakeHost, which the table excludes: %s", c.name, fakeHostExclusion)
		}
		caps := h.Capabilities()
		if caps.LandsSynchronously {
			landsSync++
		}
		if caps.NeedsCredential {
			needsCredential++
		} else {
			noCredential++
		}
	}
	if landsSync == 0 {
		t.Errorf("no host in the table declares LandsSynchronously: the landing and refusal properties assert nothing")
	}
	if needsCredential == 0 || noCredential == 0 {
		t.Errorf("hosts declaring NeedsCredential = %d, not declaring it = %d; want both columns present", needsCredential, noCredential)
	}
}

// notAsserted records that a host does not declare the capability a property is
// gated on. It is a log and not a skip: the host is not wrong, the property just
// does not speak about it.
func notAsserted(t *testing.T, h Host, capability string) {
	t.Helper()
	t.Logf("host does not declare %s (Capabilities = %+v): property not asserted", capability, h.Capabilities())
}

// ---- head-branch uniqueness ------------------------------------------------

func propHeadBranchUniqueness(t *testing.T, c confHost) {
	h := c.open(t, nil, "local:axon")
	head := "a2a/axon/submit/XQ-axon-1"
	sha := c.rig.pushHead(t, h, c.rig.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	if err := assertOneOpenPRPerHeadBranch(context.Background(), h, confOpen(head, sha), confFind(head)); err != nil {
		t.Fatalf("head-branch uniqueness does not hold: %v", err)
	}
}

// ---- open and find ----------------------------------------------------------

// The property is "found", not "open": on the local host the PR is already
// merged by the time OpenPR returns.
func propOpenAndFind(t *testing.T, c confHost) {
	ctx := context.Background()
	h := c.open(t, nil, "local:axon")
	head := "a2a/axon/submit/XQ-axon-2"
	sha := c.rig.pushHead(t, h, c.rig.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")

	before, err := h.FindPRByHeadBranch(ctx, confFind(head))
	if err != nil || before != nil {
		t.Fatalf("FindPRByHeadBranch before OpenPR = %+v, %v; want nil, nil", before, err)
	}
	opened, err := h.OpenPR(ctx, confOpen(head, sha))
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	found, err := h.FindPRByHeadBranch(ctx, confFind(head))
	if err != nil {
		t.Fatalf("FindPRByHeadBranch: %v", err)
	}
	if found == nil {
		t.Fatalf("FindPRByHeadBranch(%q) = nil after OpenPR returned PR #%d", head, opened.Number)
	}
	if found.Number != opened.Number {
		t.Errorf("FindPRByHeadBranch resolved PR #%d, want the opened #%d", found.Number, opened.Number)
	}
	if found.HeadSHA != sha {
		t.Errorf("FindPRByHeadBranch head = %q, want the pushed head %q", found.HeadSHA, sha)
	}
}

// ---- synchronous landing (gated on LandsSynchronously) ---------------------

// headParents returns the first and second parent of a commit in the bare repo.
func headParents(t *testing.T, origin, commit string) (first, second string) {
	t.Helper()
	return bare(t, origin, "rev-parse", commit+"^1"), bare(t, origin, "rev-parse", commit+"^2")
}

func propSynchronousLanding(t *testing.T, c confHost) {
	probe := c.open(t, nil, "local:axon")
	if !probe.Capabilities().LandsSynchronously {
		notAsserted(t, probe, "LandsSynchronously")
		return
	}
	ctx := context.Background()
	log := &callLog{}
	h := c.open(t, log.check, "local:axon")
	head := "a2a/axon/submit/XQ-axon-3"
	sha := c.rig.pushHead(t, h, c.rig.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	tip := bare(t, c.rig.origin, "rev-parse", "refs/heads/main")

	info, err := h.OpenPR(ctx, confOpen(head, sha))
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	if info.State != "merged" {
		t.Errorf("OpenPR state = %q, want %q (a synchronous host returns the write merged)", info.State, "merged")
	}
	calls := log.snapshot()
	if len(calls) != 1 {
		t.Fatalf("judgements = %d, want 1", len(calls))
	}
	if calls[0].BaseSHA != tip {
		t.Errorf("judged against tip %q, want main's tip before the write %q", calls[0].BaseSHA, tip)
	}
	merge := bare(t, c.rig.origin, "rev-parse", "refs/heads/main")
	if merge != calls[0].MergeSHA {
		t.Errorf("main = %q, want the judged merge commit %q", merge, calls[0].MergeSHA)
	}
	first, second := headParents(t, c.rig.origin, merge)
	if first != tip {
		t.Errorf("merge first parent = %q, want the tip it was validated against %q", first, tip)
	}
	if second != sha {
		t.Errorf("merge second parent = %q, want the pushed head %q", second, sha)
	}
}

// main moves WHILE the write is being judged: the host must rebuild the merge on
// the new tip and judge that one. The first parent is the new tip, and the head
// commit is the one that was pushed (nothing is rebased).
func propSynchronousLandingMovedTip(t *testing.T, c confHost) {
	probe := c.open(t, nil, "local:axon")
	if !probe.Capabilities().LandsSynchronously {
		notAsserted(t, probe, "LandsSynchronously")
		return
	}
	ctx := context.Background()
	log := &callLog{}
	peer := c.open(t, nil, "local:beta")

	headA, headB := "a2a/axon/submit/XQ-axon-4", "a2a/beta/submit/XQ-beta-4"
	var rivalOnce sync.Once
	var rivalErr error
	var rivalTip string
	check := func(ctx context.Context, cand LandCandidate) (LandVerdict, error) {
		v, err := log.check(ctx, cand)
		rivalOnce.Do(func() {
			// The check runs on this goroutine, so t may fail the test from here.
			shaB := c.rig.pushHead(t, peer, c.rig.clone("beta"), headB, "beta/docs/b.md", "b\n", "feat: b\n")
			if _, rivalErr = peer.OpenPR(ctx, confOpen(headB, shaB)); rivalErr == nil {
				rivalTip, _ = bareTry(c.rig.origin, "rev-parse", "refs/heads/main")
			}
		})
		return v, err
	}
	h := c.open(t, check, "local:axon")
	shaA := c.rig.pushHead(t, h, c.rig.clone("axon"), headA, "axon/docs/a.md", "a\n", "feat: a\n")
	t0 := bare(t, c.rig.origin, "rev-parse", "refs/heads/main")

	info, err := h.OpenPR(ctx, confOpen(headA, shaA))
	if rivalErr != nil {
		t.Fatalf("the rival write that moves main failed: %v", rivalErr)
	}
	if err != nil {
		t.Fatalf("OpenPR after main moved: %v", err)
	}
	if info.State != "merged" {
		t.Errorf("OpenPR state = %q, want %q", info.State, "merged")
	}
	t1 := rivalTip
	if t1 == "" || t1 == t0 {
		t.Fatalf("main did not move under the write: tip before = %q, after the rival = %q", t0, t1)
	}

	calls := log.snapshot()
	if len(calls) != 2 {
		t.Fatalf("judgements = %d, want 2 (the original candidate, then the rebuilt one)", len(calls))
	}
	if calls[0].BaseSHA != t0 {
		t.Errorf("first judgement tip = %q, want the tip at push time %q", calls[0].BaseSHA, t0)
	}
	if calls[1].BaseSHA != t1 {
		t.Errorf("second judgement tip = %q, want the moved tip %q", calls[1].BaseSHA, t1)
	}
	merge := bare(t, c.rig.origin, "rev-parse", "refs/heads/main")
	if merge != calls[1].MergeSHA {
		t.Errorf("main = %q, want the re-judged merge commit %q (what landed must be what was judged)", merge, calls[1].MergeSHA)
	}
	first, second := headParents(t, c.rig.origin, merge)
	if first != t1 {
		t.Errorf("merge first parent = %q, want the NEW tip %q", first, t1)
	}
	if second != shaA {
		t.Errorf("merge second parent = %q, want the pushed head %q unchanged (nothing is rebased)", second, shaA)
	}
}

// ---- refusal (gated on LandsSynchronously) ---------------------------------

func propRefusal(t *testing.T, c confHost) {
	probe := c.open(t, nil, "local:axon")
	if !probe.Capabilities().LandsSynchronously {
		notAsserted(t, probe, "LandsSynchronously")
		return
	}
	ctx := context.Background()
	wantCodes := []string{"AUTHZ-001", "ART-007"}
	refuse := func(context.Context, LandCandidate) (LandVerdict, error) {
		return LandVerdict{Codes: wantCodes, Detail: "axon wrote into beta/"}, nil
	}
	h := c.open(t, refuse, "local:axon")
	head := "a2a/axon/submit/XQ-axon-5"
	sha := c.rig.pushHead(t, h, c.rig.clone("axon"), head, "beta/docs/intrusion.md", "x\n", "feat: intrude\n")
	mainBefore := bare(t, c.rig.origin, "rev-parse", "refs/heads/main")
	refsBefore := refSnapshot(t, c.rig.origin)

	_, err := h.OpenPR(ctx, confOpen(head, sha))
	if !errors.Is(err, ErrLandRefused) {
		t.Fatalf("OpenPR error = %v, want one satisfying errors.Is(ErrLandRefused)", err)
	}
	var wrapped *Error
	if !errors.As(err, &wrapped) {
		t.Errorf("OpenPR error is %T, want it wrapped in *host.Error", err)
	}
	var refusal *LandRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("errors.As(*LandRefusal) failed for %v", err)
	}
	if got, want := strings.Join(refusal.Codes, ","), strings.Join(wantCodes, ","); got != want {
		t.Errorf("refusal codes = %q, want the validator's %q", got, want)
	}

	if got := bare(t, c.rig.origin, "rev-parse", "refs/heads/main"); got != mainBefore {
		t.Errorf("main = %q after a refusal, want it unmoved at %q", got, mainBefore)
	}
	// Refs, never objects: the merge commit object is created before the check
	// runs, so an unreferenced commit may remain.
	var wantRefs []string
	for _, line := range refsBefore {
		if !strings.HasPrefix(line, "refs/heads/"+head+" ") {
			wantRefs = append(wantRefs, line)
		}
	}
	if got, want := strings.Join(refSnapshot(t, c.rig.origin), "\n"), strings.Join(wantRefs, "\n"); got != want {
		t.Errorf("refs after a refusal =\n%s\nwant the refs before minus the head branch =\n%s", got, want)
	}
	found, err := h.FindPRByHeadBranch(ctx, confFind(head))
	if err != nil || found != nil {
		t.Errorf("FindPRByHeadBranch after a refusal = %+v, %v; want nil, nil (a refused write keeps no record)", found, err)
	}
}

// refSnapshot lists every ref of the bare repository with the object it points
// at, sorted by name (for-each-ref's own order).
func refSnapshot(t *testing.T, origin string) []string {
	t.Helper()
	out := bare(t, origin, "for-each-ref", "--format=%(refname) %(objectname)")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// ---- credential (gated on NeedsCredential) ---------------------------------

func propCredential(t *testing.T, c confHost) {
	h := c.open(t, nil, "local:axon")
	if h.Capabilities().NeedsCredential {
		// The GitHub host refuses an empty token, and refuses it before any request.
		before := 0
		if c.requests != nil {
			before = len(c.requests())
		}
		_, _, err := h.TokenScopes(context.Background(), Credential{})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("TokenScopes with an empty credential = %v, want errors.Is(ErrInvalidRequest)", err)
		}
		if c.requests != nil {
			if after := len(c.requests()); after != before {
				t.Errorf("an empty credential made %d API request(s), want 0", after-before)
			}
		}
		return
	}
	// A host that needs no credential answers without one, and without the
	// network: the context is already cancelled, so a host that tried to reach a
	// remote would fail with it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := h.TokenScopes(ctx, Credential{}); err != nil {
		t.Errorf("TokenScopes without a credential on a host that declares NeedsCredential=false = %v, want nil", err)
	}
}
