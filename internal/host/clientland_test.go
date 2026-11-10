package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

const (
	clientLandLogin = "ydnikolaev-test"
	clientLandToken = "test-token-value"
)

var clientLandRepoID = Repo{Owner: "o", Name: "r"}

// fakePR is one pull request the fake REST surface holds.
type fakePR struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	NodeID  string `json:"node_id"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Base    struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

// fakePRAPI is the part of GitHub's REST surface a client land reads. Any other
// route, including GraphQL and the merge endpoint, is recorded as unexpected.
type fakePRAPI struct {
	mu         sync.Mutex
	origin     string
	logins     map[string]string
	userStatus int
	userCalls  int
	creates    int
	unexpected []string
	prs        []*fakePR
}

func (a *fakePRAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/user":
		a.userCalls++
		if a.userStatus != 0 {
			w.WriteHeader(a.userStatus)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_ = json.NewEncoder(w).Encode(map[string]string{"login": a.logins[token]}) // reason: a failed test write surfaces as the client's decode error
	case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
		branch := strings.TrimPrefix(r.URL.Query().Get("head"), "o:")
		out := []*fakePR{}
		for i := len(a.prs) - 1; i >= 0; i-- {
			if a.prs[i].Head.Ref == branch {
				out = append(out, a.prs[i])
			}
		}
		_ = json.NewEncoder(w).Encode(out) // reason: as above
	case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/pulls":
		var body struct{ Title, Head, Base, Body string }
		_ = json.NewDecoder(r.Body).Decode(&body) // reason: a malformed body fails the test's own PR assertions
		a.creates++
		sha, _ := bareTry(a.origin, "rev-parse", "refs/heads/"+body.Head) // reason: an unresolved head reads as a mismatch in the assertions
		pr := a.add(body.Head, body.Base, sha, "open", false)
		pr.Title, pr.Body = body.Title, body.Body
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(pr) // reason: as above
	default:
		a.unexpected = append(a.unexpected, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (a *fakePRAPI) add(head, base, sha, state string, merged bool) *fakePR {
	pr := &fakePR{Number: len(a.prs) + 1, State: state, Merged: merged}
	pr.HTMLURL = fmt.Sprintf("https://github.test/o/r/pull/%d", pr.Number)
	pr.NodeID = fmt.Sprintf("node-%d", pr.Number)
	pr.Base.Ref, pr.Head.Ref, pr.Head.SHA = base, head, sha
	a.prs = append(a.prs, pr)
	return pr
}

func (a *fakePRAPI) seed(head, base, sha, state string, merged bool) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.add(head, base, sha, state, merged).Number
}

func (a *fakePRAPI) counts() (creates, users, total int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.creates, a.userCalls, len(a.prs)
}

func (a *fakePRAPI) setUserStatus(status int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.userStatus = status
}

func (a *fakePRAPI) assertOnlyExpectedRoutes(t *testing.T) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.unexpected) > 0 {
		t.Errorf("unexpected API calls (no auto-merge, no merge API): %v", a.unexpected)
	}
}

// clientLandRig is a bare repository standing in for the GitHub remote, plus the
// fake REST surface and a host wired to both.
type clientLandRig struct {
	*localRig
	api  *fakePRAPI
	host *ClientLandHost
	log  *callLog
}

func newClientLandRig(t *testing.T, check LandCheckFunc, guard LandGuardFunc) *clientLandRig {
	t.Helper()
	fx := spacefixture.New(t, "axon", "beta")
	api := &fakePRAPI{origin: fx.OriginDir, logins: map[string]string{clientLandToken: clientLandLogin, "second-token": "second-login"}}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	gh := NewGitHubHost(srv.Client(), srv.URL)
	gh.RetryAttempts = 1
	log := &callLog{}
	if check == nil {
		check = log.check
	}
	return &clientLandRig{
		localRig: &localRig{fx: fx, origin: fx.OriginDir},
		api:      api,
		host:     newClientLandHost(gh, fx.OriginDir, check, guard),
		log:      log,
	}
}

func (r *clientLandRig) mainTip(t *testing.T) string {
	t.Helper()
	return bare(t, r.origin, "rev-parse", "refs/heads/main")
}

func (r *clientLandRig) pushWrite(t *testing.T, branch, file string) string {
	t.Helper()
	return r.pushHead(t, r.host, r.clone("axon"), branch, file, "content of "+file+"\n", "write "+file+"\n")
}

// advanceMain lands a commit on the remote's main from the other participant,
// the concurrent writer a client land has to lose to.
func (r *clientLandRig) advanceMain(t *testing.T, file string) string {
	t.Helper()
	clone := r.clone("beta")
	gitOut(t, clone, "fetch", "-q", "origin")
	gitOut(t, clone, "checkout", "-q", "-B", "main", "origin/main")
	commitInDir(t, clone, file, "moved\n")
	gitOut(t, clone, "push", "-q", "origin", "main")
	return gitOut(t, clone, "rev-parse", "HEAD")
}

func clientLandRequest(head, sha string) OpenPRRequest {
	return OpenPRRequest{
		Repo: clientLandRepoID, Head: head, Base: "main", Title: "the title", Body: "the body",
		ExpectedHeadSHA: sha, Credential: Credential{Token: clientLandToken},
	}
}

func TestClientLand_LandsAndReturnsMerged(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	base := r.mainTip(t)
	message := "write\n\nA2A-Built-By: a2a 1.2.3 (abc)\n\nA2A-Approved-Gates: G1,G2\n"
	head := r.pushHead(t, r.host, r.clone("axon"), "a2a/axon/one", "axon/one.md", "one\n", message)

	info, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	tip := r.mainTip(t)
	if info.State != "merged" || info.MergeSHA != tip || info.HeadSHA != head || info.BaseBranch != "main" ||
		info.Number != 1 || info.URL == "" || info.MergeMethod != MergeMethodMerge || info.AutoMergeArmed {
		t.Fatalf("PRInfo = %+v, main = %s", info, tip)
	}
	if parents := bare(t, r.origin, "rev-list", "--parents", "-n", "1", tip); parents != tip+" "+base+" "+head {
		t.Fatalf("merge parents = %q, want tip then head", parents)
	}
	want := "Merge branch 'a2a/axon/one' into main\n\nthe title\n\nA2A-Built-By: a2a 1.2.3 (abc)\n\nA2A-Approved-Gates: G1,G2\n"
	if got := rawMessage(t, r.origin, tip); got != want {
		t.Errorf("merge message = %q, want %q", got, want)
	}
	if got := bare(t, r.origin, "log", "-1", "--format=%an", tip); got != clientLandLogin {
		t.Errorf("merge author = %q, want the token's login", got)
	}
	calls := r.log.snapshot()
	if len(calls) != 1 {
		t.Fatalf("land check ran %d times, want 1", len(calls))
	}
	c := calls[0]
	if c.BaseSHA != base || c.MergeSHA != tip || c.HeadSHA != head || c.BaseBranch != "main" || c.HeadMessage != message {
		t.Errorf("candidate = %+v", c)
	}
	if creates, _, _ := r.api.counts(); creates != 1 {
		t.Errorf("creates = %d, want 1", creates)
	}
	r.api.assertOnlyExpectedRoutes(t)
}

func TestClientLand_AuthorIsTheUserLoginCachedPerCredential(t *testing.T) {
	t.Parallel()
	var (
		mu      sync.Mutex
		authors []string
	)
	check := func(_ context.Context, c LandCandidate) (LandVerdict, error) {
		mu.Lock()
		defer mu.Unlock()
		authors = append(authors, c.Author)
		return LandVerdict{OK: true}, nil
	}
	r := newClientLandRig(t, check, nil)
	ctx := context.Background()
	for _, name := range []string{"one", "two"} {
		branch := "a2a/axon/" + name
		if _, err := r.host.OpenPR(ctx, clientLandRequest(branch, r.pushWrite(t, branch, "axon/"+name+".md"))); err != nil {
			t.Fatalf("OpenPR(%s): %v", name, err)
		}
	}
	req := clientLandRequest("a2a/axon/three", r.pushWrite(t, "a2a/axon/three", "axon/three.md"))
	req.Credential = Credential{Token: "second-token"}
	if _, err := r.host.OpenPR(ctx, req); err != nil {
		t.Fatalf("OpenPR(three): %v", err)
	}
	if want := []string{clientLandLogin, clientLandLogin, "second-login"}; !slices.Equal(authors, want) {
		t.Errorf("authors = %v, want %v", authors, want)
	}
	if _, users, _ := r.api.counts(); users != 2 {
		t.Errorf("GET /user ran %d times, want once per credential", users)
	}
}

func TestClientLand_LoginFailureIsAnErrorAndNothingMoves(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	before := r.mainTip(t)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")
	r.api.setUserStatus(http.StatusUnauthorized)

	_, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("err = %v, want ErrRequestFailed", err)
	}
	if len(r.log.snapshot()) != 0 || r.mainTip(t) != before {
		t.Errorf("a write went ahead without an identity")
	}
	if creates, _, total := r.api.counts(); creates != 0 || total != 0 {
		t.Errorf("PRs created = %d, total = %d", creates, total)
	}
	req := clientLandRequest("a2a/axon/one", head)
	req.Credential = Credential{}
	if _, err := r.host.OpenPR(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("empty credential: err = %v, want ErrInvalidRequest", err)
	}
}

func TestClientLand_RefusalLeavesMainAndPRsUntouched(t *testing.T) {
	t.Parallel()
	check := func(context.Context, LandCandidate) (LandVerdict, error) {
		return LandVerdict{Codes: []string{"AUTHZ-001"}, Detail: "not yours"}, nil
	}
	r := newClientLandRig(t, check, nil)
	before := r.mainTip(t)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")

	info, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if !errors.Is(err, ErrLandRefused) {
		t.Fatalf("err = %v, want ErrLandRefused", err)
	}
	var refusal *LandRefusal
	if !errors.As(err, &refusal) || !slices.Equal(refusal.Codes, []string{"AUTHZ-001"}) || refusal.Detail != "not yours" {
		t.Errorf("refusal = %+v", refusal)
	}
	if info != (PRInfo{}) {
		t.Errorf("info = %+v, want the zero value", info)
	}
	if got := r.mainTip(t); got != before {
		t.Errorf("main moved to %s", got)
	}
	if creates, _, total := r.api.counts(); creates != 0 || total != 0 {
		t.Errorf("PRs created = %d, total = %d", creates, total)
	}
	r.api.assertOnlyExpectedRoutes(t)
}

func TestClientLand_CheckErrorIsNotAPass(t *testing.T) {
	t.Parallel()
	boom := errors.New("manifest unreadable")
	r := newClientLandRig(t, func(context.Context, LandCandidate) (LandVerdict, error) { return LandVerdict{}, boom }, nil)
	before := r.mainTip(t)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")

	if _, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head)); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the check's error", err)
	}
	if creates, _, _ := r.api.counts(); creates != 0 || r.mainTip(t) != before {
		t.Errorf("creates = %d, main moved = %v", creates, r.mainTip(t) != before)
	}
}

func TestClientLand_GuardRefusalLeavesEverythingUntouched(t *testing.T) {
	t.Parallel()
	guardErr := errors.New("the space is ahead of this binary")
	var gotTip, gotHeadFile string
	var history int
	var r *clientLandRig
	guard := func(_ context.Context, dir, tip string) error {
		gotTip = tip
		if _, err := os.Stat(filepath.Join(dir, "axon", "one.md")); err == nil {
			gotHeadFile = "present"
		}
		count := gitOut(t, dir, "rev-list", "--first-parent", "--count", "HEAD")
		if _, err := fmt.Sscan(count, &history); err != nil {
			t.Errorf("first-parent count %q: %v", count, err)
		}
		if at := gitOut(t, dir, "rev-parse", "HEAD"); at != tip {
			t.Errorf("guard checkout is at %s, want the tip %s", at, tip)
		}
		return guardErr
	}
	r = newClientLandRig(t, nil, guard)
	r.advanceMain(t, "beta/earlier.md")
	before := r.mainTip(t)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")

	_, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if !errors.Is(err, guardErr) || err.Error() != guardErr.Error() {
		t.Fatalf("err = %#v, want the guard's error unchanged", err)
	}
	if gotTip != before || gotHeadFile != "" {
		t.Errorf("guard saw tip %s (want %s), head file %q", gotTip, before, gotHeadFile)
	}
	if history < 2 {
		t.Errorf("guard saw %d first-parent commits, want the whole history", history)
	}
	if len(r.log.snapshot()) != 0 {
		t.Errorf("the land check ran after the guard refused")
	}
	if creates, _, total := r.api.counts(); creates != 0 || total != 0 || r.mainTip(t) != before {
		t.Errorf("creates = %d, total = %d, main moved = %v", creates, total, r.mainTip(t) != before)
	}
}

func TestClientLand_ConcurrentMoveIsJudgedAgainOnTheNewTip(t *testing.T) {
	t.Parallel()
	var r *clientLandRig
	var bases []string
	var movedTo string
	check := func(_ context.Context, c LandCandidate) (LandVerdict, error) {
		bases = append(bases, c.BaseSHA)
		if len(bases) == 1 {
			movedTo = r.advanceMain(t, "beta/concurrent.md")
		}
		return LandVerdict{OK: true}, nil
	}
	var guardTips []string
	guard := func(_ context.Context, _, tip string) error {
		guardTips = append(guardTips, tip)
		return nil
	}
	r = newClientLandRig(t, check, guard)
	first := r.mainTip(t)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")

	info, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	if want := []string{first, movedTo}; !slices.Equal(bases, want) || !slices.Equal(guardTips, want) {
		t.Fatalf("judged on %v, guarded on %v, want %v", bases, guardTips, want)
	}
	tip := r.mainTip(t)
	if parents := bare(t, r.origin, "rev-list", "--parents", "-n", "1", tip); parents != tip+" "+movedTo+" "+head {
		t.Fatalf("merge parents = %q, want the new tip then head", parents)
	}
	if info.MergeSHA != tip || info.State != "merged" {
		t.Errorf("info = %+v, main = %s", info, tip)
	}
	if creates, _, _ := r.api.counts(); creates != 1 {
		t.Errorf("creates = %d, want one PR across the retry", creates)
	}
}

func TestClientLand_RetriesAreBounded(t *testing.T) {
	t.Parallel()
	var r *clientLandRig
	var judged int
	check := func(context.Context, LandCandidate) (LandVerdict, error) {
		judged++
		r.advanceMain(t, fmt.Sprintf("beta/move-%d.md", judged))
		return LandVerdict{OK: true}, nil
	}
	r = newClientLandRig(t, check, nil)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")

	info, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if !errors.Is(err, ErrLandContended) {
		t.Fatalf("err = %v, want ErrLandContended", err)
	}
	if judged != maxLandAttempts {
		t.Errorf("judged %d times, want %d", judged, maxLandAttempts)
	}
	if info.Number != 1 || info.State != "open" {
		t.Errorf("info = %+v, want the open PR's handle", info)
	}
}

func TestClientLand_AuthFailureDoesNotRetry(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	var pushes int
	realExec := r.host.exec
	r.host.exec = func(ctx context.Context, stdin []byte, limit int, args ...string) recoveryGitResult {
		if at := slices.Index(args, "push"); at >= 0 {
			pushes++
			auth := slices.IndexFunc(args, func(a string) bool { return strings.HasPrefix(a, "http.extraheader=") })
			if auth < 0 || auth > at {
				t.Errorf("credential args must precede the subcommand: auth at %d, push at %d", auth, at)
			}
			return recoveryGitResult{exitCode: 128, stderr: []byte(
				"remote: Permission to o/r.git denied to someone.\nfatal: unable to access 'x': The requested URL returned error: 403\n")}
		}
		return realExec(ctx, stdin, limit, args...)
	}
	before := r.mainTip(t)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")

	info, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if !errors.Is(err, ErrPushRejected) || !errors.Is(err, ErrPushForbidden) {
		t.Fatalf("err = %v, want ErrPushForbidden inside ErrPushRejected", err)
	}
	if errors.Is(err, ErrLandContended) {
		t.Errorf("an access failure reads as contention: %v", err)
	}
	if strings.Contains(err.Error(), "denied to someone") {
		t.Errorf("git's stderr reached the error: %v", err)
	}
	if pushes != 1 || len(r.log.snapshot()) != 1 {
		t.Errorf("pushes = %d, judged = %d, want one of each", pushes, len(r.log.snapshot()))
	}
	if info.Number != 1 || info.State != "open" {
		t.Errorf("info = %+v, want the open PR's handle", info)
	}
	if r.mainTip(t) != before {
		t.Errorf("main moved")
	}
}

func TestClientLand_ServerRefusalWithoutAMoveDoesNotRetry(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")
	hook := "#!/bin/sh\nwhile read old new ref; do\n  if [ \"$ref\" = refs/heads/main ]; then exit 1; fi\ndone\nexit 0\n"
	hooks := filepath.Join(r.origin, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	before := r.mainTip(t)

	_, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if !errors.Is(err, ErrPushRejected) {
		t.Fatalf("err = %v, want ErrPushRejected", err)
	}
	if !strings.Contains(err.Error(), "remote rejected") {
		t.Errorf("the server's refusal is not named: %v", err)
	}
	if n := len(r.log.snapshot()); n != 1 {
		t.Errorf("judged %d times, want 1: the base did not move", n)
	}
	if r.mainTip(t) != before {
		t.Errorf("main moved")
	}
}

func TestClientLand_LostPushAnswerIsRecognisedAsLanded(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	realExec := r.host.exec
	r.host.exec = func(ctx context.Context, stdin []byte, limit int, args ...string) recoveryGitResult {
		res := realExec(ctx, stdin, limit, args...)
		if slices.Contains(args, "push") && res.exitCode == 0 {
			return recoveryGitResult{exitCode: 128, stderr: []byte("fatal: the remote end hung up unexpectedly\n")}
		}
		return res
	}
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")

	info, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	if tip := r.mainTip(t); info.State != "merged" || info.MergeSHA != tip {
		t.Errorf("info = %+v, main = %s", info, tip)
	}
	if n := len(r.log.snapshot()); n != 1 {
		t.Errorf("judged %d times, want 1", n)
	}
}

func TestClientLand_JudgedTreeCarriesNothingAGuardLeft(t *testing.T) {
	t.Parallel()
	guard := func(_ context.Context, dir, _ string) error {
		return os.WriteFile(filepath.Join(dir, "stray.md"), []byte("left behind\n"), 0o644)
	}
	var stray error
	check := func(_ context.Context, c LandCandidate) (LandVerdict, error) {
		_, stray = os.Stat(filepath.Join(c.CheckoutDir, "stray.md"))
		return LandVerdict{OK: true}, nil
	}
	r := newClientLandRig(t, check, guard)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")

	if _, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head)); err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	if !errors.Is(stray, os.ErrNotExist) {
		t.Errorf("the check saw the guard's file: stat = %v", stray)
	}
}

func TestClientLand_OpenPRIsReused(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")
	number := r.api.seed("a2a/axon/one", "main", head, "open", false)

	info, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	if info.Number != number || info.State != "merged" {
		t.Errorf("info = %+v, want PR %d merged", info, number)
	}
	if creates, _, total := r.api.counts(); creates != 0 || total != 1 {
		t.Errorf("creates = %d, total = %d, want the open PR reused", creates, total)
	}
}

func TestClientLand_MergedPROnAReusedBranchIsNotReused(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")
	r.api.seed("a2a/axon/one", "main", strings.Repeat("a", 40), "closed", true)

	info, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", head))
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	if creates, _, total := r.api.counts(); creates != 1 || total != 2 || info.Number != 2 {
		t.Errorf("creates = %d, total = %d, number = %d, want a new PR", creates, total, info.Number)
	}
}

func TestClientLand_ReRunAfterALandMovesNothing(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")
	ctx := context.Background()
	first, err := r.host.OpenPR(ctx, clientLandRequest("a2a/axon/one", head))
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	tip := r.mainTip(t)

	// GitHub still says open here: the seconds before it closes the PR.
	again, err := r.host.OpenPR(ctx, clientLandRequest("a2a/axon/one", head))
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if again.State != "merged" || again.Number != first.Number || again.HeadSHA != head {
		t.Errorf("re-run = %+v, first = %+v", again, first)
	}
	if got := r.mainTip(t); got != tip {
		t.Errorf("the re-run pushed a second merge: main %s -> %s", tip, got)
	}
	if n := len(r.log.snapshot()); n != 1 {
		t.Errorf("judged %d times, want 1", n)
	}
	if creates, _, _ := r.api.counts(); creates != 1 {
		t.Errorf("creates = %d, want 1", creates)
	}
}

func TestClientLand_ConflictMovesNothing(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	head := r.pushWrite(t, "a2a/axon/same", "shared.md")
	clone := r.clone("beta")
	gitOut(t, clone, "fetch", "-q", "origin")
	gitOut(t, clone, "checkout", "-q", "-B", "main", "origin/main")
	commitInDir(t, clone, "shared.md", "the other side\n")
	gitOut(t, clone, "push", "-q", "origin", "main")
	before := r.mainTip(t)

	_, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/same", head))
	if !errors.Is(err, ErrLandConflict) {
		t.Fatalf("err = %v, want ErrLandConflict", err)
	}
	if creates, _, _ := r.api.counts(); creates != 0 || r.mainTip(t) != before || len(r.log.snapshot()) != 0 {
		t.Errorf("creates = %d, main moved = %v, judged = %d", creates, r.mainTip(t) != before, len(r.log.snapshot()))
	}
}

func TestClientLand_RefusesAHeadThatIsNotTheExpectedOne(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	r.pushWrite(t, "a2a/axon/one", "axon/one.md")

	_, err := r.host.OpenPR(context.Background(), clientLandRequest("a2a/axon/one", strings.Repeat("b", 40)))
	if !errors.Is(err, ErrPRHeadMoved) {
		t.Fatalf("err = %v, want ErrPRHeadMoved", err)
	}
	if len(r.log.snapshot()) != 0 {
		t.Errorf("a moved head was judged")
	}
}

func TestClientLand_RefusesAForkQualifiedHead(t *testing.T) {
	t.Parallel()
	r := newClientLandRig(t, nil, nil)
	head := r.pushWrite(t, "a2a/axon/one", "axon/one.md")

	_, err := r.host.OpenPR(context.Background(), clientLandRequest("someone:a2a/axon/one", head))
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}

// The funnel finds these by type assertion, so "implements nothing that merges
// or arms" has to be true of the method set and not only of the behaviour.
func TestClientLand_OffersNoMergeOrForkCapability(t *testing.T) {
	t.Parallel()
	var h Host = newClientLandHost(NewGitHubHost(nil, ""), "https://github.com/o/r", validCheck, nil)
	if _, ok := h.(AutoMerger); ok {
		t.Errorf("exposes AutoMerger: the funnel would try to arm auto-merge")
	}
	if _, ok := h.(Merger); ok {
		t.Errorf("exposes Merger: the funnel would land through the merge API")
	}
	if _, ok := h.(Forker); ok {
		t.Errorf("exposes Forker: a fork's head cannot be landed by pushing to the base")
	}
	want := Capabilities{NeedsCredential: true, LandsSynchronously: true}
	if got := h.Capabilities(); got != want {
		t.Errorf("Capabilities = %+v, want %+v", got, want)
	}
}

func TestOpen_ClientLandSelection(t *testing.T) {
	t.Parallel()
	loc, err := ParseLocator("https://github.com/o/r")
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(loc, OpenConfig{ClientLand: true, LandCheck: validCheck})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	landHost, ok := h.(*ClientLandHost)
	if !ok {
		t.Fatalf("Open = %T, want *ClientLandHost", h)
	}
	if landHost.remoteURL != loc.Raw {
		t.Errorf("remote = %q, want %q", landHost.remoteURL, loc.Raw)
	}
	if _, err := Open(loc, OpenConfig{ClientLand: true}); !errors.Is(err, errNoLandCheck) {
		t.Errorf("no land check: err = %v, want errNoLandCheck", err)
	}
	h, err = Open(loc, OpenConfig{LandCheck: validCheck})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := h.(*GitHubHost); !ok {
		t.Errorf("absent ClientLand: Open = %T, want *GitHubHost", h)
	}
	fx := spacefixture.New(t, "axon")
	path, err := ParseLocator(fx.OriginDir)
	if err != nil {
		t.Fatal(err)
	}
	h, err = Open(path, OpenConfig{ClientLand: true, LandCheck: validCheck})
	if err != nil {
		t.Fatalf("Open(path): %v", err)
	}
	if _, ok := h.(*GitHubHost); !ok {
		t.Errorf("path locator: Open = %T, want the unchanged *GitHubHost", h)
	}
}

func TestBuildNoteLine(t *testing.T) {
	t.Parallel()
	const note = "A2A-Built-By: a2a 1.2.3 (abc)"
	cases := []struct {
		name, message, want string
	}{
		{"final paragraph", "write\n\n" + note + "\n", note},
		{"above the trailer paragraph", "write\n\n" + note + "\n\nA2A-Approved-Gates: G1\nA2A-Operation-Key: k\n", note},
		{"absent", "write\n\nA2A-Approved-Gates: G1\n", ""},
		{"body line is not the note", note + "\n\nwrite\n\nmore\n\nA2A-Approved-Gates: G1\n", ""},
		{"not alone in its paragraph", "write\n\n" + note + "\nother: x\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := buildNoteLine(c.message); got != c.want {
				t.Errorf("buildNoteLine = %q, want %q", got, c.want)
			}
		})
	}
}
