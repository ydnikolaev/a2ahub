package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// ---- fixtures -------------------------------------------------------------

var testRepo = Repo{Owner: "fixture", Name: "space"}

// gitTry runs git (hardened argv, cwd=dir) and returns its combined output; the
// caller decides what a failure means. Refusals are expected results in this
// file, so the helper never fails the test itself.
func gitTry(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// gitOut runs git and returns its trimmed output, failing the test on error.
func gitOut(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := gitTry(dir, nil, args...)
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(out)
}

// bare runs git against the bare space repository.
func bare(t testing.TB, origin string, args ...string) string {
	t.Helper()
	return gitOut(t, "", append([]string{"--git-dir=" + origin}, args...)...)
}

// bareTry is bare without the failure: it reports whether git exited zero.
func bareTry(origin string, args ...string) (string, bool) {
	out, err := gitTry("", nil, append([]string{"--git-dir=" + origin}, args...)...)
	return strings.TrimSpace(out), err == nil
}

// refExists reports whether the bare repository has the ref.
func refExists(origin, ref string) bool {
	_, ok := bareTry(origin, "rev-parse", "--verify", "--quiet", ref)
	return ok
}

// localRig is a throwaway local space: a bare origin made a local space by
// PrepareLocalSpace, and two participant clones.
type localRig struct {
	fx     *spacefixture.Fixture
	origin string
}

func newLocalRig(t *testing.T) *localRig {
	t.Helper()
	fx := spacefixture.New(t, "axon", "beta")
	if err := PrepareLocalSpace(context.Background(), fx.OriginDir, "main"); err != nil {
		t.Fatalf("PrepareLocalSpace: %v", err)
	}
	return &localRig{fx: fx, origin: fx.OriginDir}
}

func (r *localRig) clone(system string) string { return r.fx.Clones[system] }

// host builds a LocalHost the way Open does. A nil check is the always-valid one.
func (r *localRig) host(t *testing.T, check LandCheckFunc, author string) *LocalHost {
	t.Helper()
	if check == nil {
		check = validCheck
	}
	loc, err := ParseLocator(r.origin)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(loc, OpenConfig{LandCheck: check, LandAuthor: author})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	lh, ok := h.(*LocalHost)
	if !ok {
		t.Fatalf("Open = %T, want *LocalHost", h)
	}
	return lh
}

// pushHead commits file=content on a fresh branch of clone (cut from the clone's
// main) with the head commit message exactly as given, and pushes it to the
// space through the host's own PushBranch. It returns the head commit.
func (r *localRig) pushHead(t *testing.T, h Host, clone, branch, file, content, message string) string {
	t.Helper()
	gitOut(t, clone, "checkout", "-q", "-B", branch, "main")
	full := filepath.Join(clone, file)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, clone, "add", file)
	msgFile := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(msgFile, []byte(message), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := gitTry(clone, []string{
		"GIT_AUTHOR_NAME=a2a-test", "GIT_AUTHOR_EMAIL=test@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-test", "GIT_COMMITTER_EMAIL=test@a2ahub.invalid",
	}, "commit", "-q", "--cleanup=verbatim", "-F", msgFile)
	if err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	sha := gitOut(t, clone, "rev-parse", "HEAD")
	if _, err := h.PushBranch(context.Background(), PushBranchRequest{
		RepoDir: clone, LocalRef: "HEAD", Branch: branch, RemoteURL: r.origin,
	}); err != nil {
		t.Fatalf("PushBranch(%s): %v", branch, err)
	}
	return sha
}

func openReq(head, title, body string) OpenPRRequest {
	return OpenPRRequest{Repo: testRepo, Head: head, Base: "main", Title: title, Body: body}
}

func validCheck(context.Context, LandCandidate) (LandVerdict, error) {
	return LandVerdict{OK: true}, nil
}

// callLog is a land check that records every candidate it judges.
type callLog struct {
	mu    sync.Mutex
	calls []LandCandidate
	n     atomic.Int32
	// during runs inside the check, while the candidate's checkout still exists.
	during func(LandCandidate)
}

func (c *callLog) check(_ context.Context, cand LandCandidate) (LandVerdict, error) {
	c.n.Add(1)
	c.mu.Lock()
	c.calls = append(c.calls, cand)
	c.mu.Unlock()
	if c.during != nil {
		c.during(cand)
	}
	return LandVerdict{OK: true}, nil
}

func (c *callLog) snapshot() []LandCandidate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]LandCandidate(nil), c.calls...)
}

// rawMessage is a commit's message as git stores it, untouched.
func rawMessage(t testing.TB, origin, sha string) string {
	t.Helper()
	raw, err := gitTry("", nil, "--git-dir="+origin, "cat-file", "commit", sha)
	if err != nil {
		t.Fatalf("cat-file commit %s: %v\n%s", sha, err, raw)
	}
	_, msg, _ := strings.Cut(raw, "\n\n")
	return msg
}

func landURL(origin string, number int) string {
	u := url.URL{Scheme: "file", Path: origin}
	return u.String() + "#land-" + strconv.Itoa(number)
}

// ---- AC1 ------------------------------------------------------------------

func TestLocalHost_LandsMergeCommit(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	log := &callLog{}
	var sawFile, sawTip string
	log.during = func(c LandCandidate) {
		b, err := os.ReadFile(filepath.Join(c.CheckoutDir, "axon", "docs", "note.md"))
		if err == nil {
			sawFile = string(b)
		}
		tipManifest, _ := gitTry(c.CheckoutDir, nil, "show", c.BaseSHA+":space.yaml")
		sawTip = tipManifest
	}
	h := r.host(t, log.check, "local:axon")

	tip := bare(t, r.origin, "rev-parse", "refs/heads/main")
	head := "a2a/axon/submit/XQ-axon-1"
	headSHA := r.pushHead(t, h, r.clone("axon"), head, "axon/docs/note.md", "hello\n", "feat: add a note\n")

	title := "Добавить заметку 🚀 café"
	body := "line one\n\n  indented, trailing spaces   \n\n\n"
	info, err := h.OpenPR(context.Background(), openReq(head, title, body))
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	want := PRInfo{
		Number: 2, URL: landURL(r.origin, 2), State: "merged", Title: title, Body: body,
		BaseBranch: "main", HeadSHA: headSHA, MergeMethod: MergeMethodMerge,
	}
	if info != want {
		t.Fatalf("PRInfo =\n%+v\nwant\n%+v", info, want)
	}

	merge := bare(t, r.origin, "rev-parse", "refs/heads/main")
	if merge == tip {
		t.Fatal("main did not move")
	}
	if got := bare(t, r.origin, "rev-parse", merge+"^1"); got != tip {
		t.Errorf("first parent = %s, want the old tip %s", got, tip)
	}
	if got := bare(t, r.origin, "rev-parse", merge+"^2"); got != headSHA {
		t.Errorf("second parent = %s, want the head %s", got, headSHA)
	}
	if got := bare(t, r.origin, "rev-list", "--first-parent", "--count", "refs/heads/main"); got != "2" {
		t.Errorf("first-parent length = %s, want 2 (one merge on the seed)", got)
	}
	if got := bare(t, r.origin, "show", merge+":axon/docs/note.md"); got != "hello" {
		t.Errorf("merged file = %q", got)
	}
	if got := bare(t, r.origin, "log", "-1", "--format=%an <%ae>", merge); got != "local:axon <local-axon@local.a2a.invalid>" {
		t.Errorf("merge author = %q", got)
	}
	if got, want := rawMessage(t, r.origin, merge), "Merge pull request #2 from "+head+"\n\n"+title+"\n"; got != want {
		t.Errorf("merge message = %q, want %q", got, want)
	}

	// The landed record and its number ref exist; the head branch is gone.
	if !refExists(r.origin, "refs/a2a/landed/"+head) || !refExists(r.origin, "refs/a2a/pr/2") {
		t.Errorf("record refs missing: %s", bare(t, r.origin, "for-each-ref", "refs/a2a"))
	}
	if refExists(r.origin, "refs/heads/"+head) {
		t.Error("head branch still exists after the land")
	}

	// The check judged the exact tree: a live checkout at the merge, the tip's
	// objects present, nothing left behind.
	calls := log.snapshot()
	if len(calls) != 1 {
		t.Fatalf("LandCheck calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.BaseBranch != "main" || c.BaseSHA != tip || c.MergeSHA != merge || c.HeadSHA != headSHA ||
		c.HeadMessage != "feat: add a note\n" || c.Author != "local:axon" {
		t.Errorf("candidate = %+v", c)
	}
	if sawFile != "hello\n" {
		t.Errorf("the check saw file %q, want the candidate checked out at the merge", sawFile)
	}
	if !strings.Contains(sawTip, "schema: space/v1") {
		t.Errorf("the tip manifest was not readable as `git show <BaseSHA>:space.yaml`: %q", sawTip)
	}
	if _, err := os.Stat(c.CheckoutDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the check directory %s was not removed (stat err = %v)", c.CheckoutDir, err)
	}

	// The same record answers every reader.
	found, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{Repo: testRepo, Branch: head})
	if err != nil || found == nil || *found != want {
		t.Fatalf("FindPRByHeadBranch = %+v, %v; want %+v", found, err, want)
	}
	status, err := h.CheckStatus(context.Background(), StatusRequest{Repo: testRepo, PRNumber: 2})
	if err != nil || status.State != "completed" || status.Conclusion != "success" || status.HeadSHA != headSHA || len(status.Ambiguous) != 0 {
		t.Errorf("CheckStatus = %+v, %v", status, err)
	}
	if _, err := h.CheckStatus(context.Background(), StatusRequest{Repo: testRepo, PRNumber: 99}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("CheckStatus(unknown) = %v, want ErrInvalidRequest", err)
	}
	if _, err := h.CheckStatus(context.Background(), StatusRequest{Repo: testRepo}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("CheckStatus(no number) = %v, want ErrInvalidRequest", err)
	}
}

// The record survives garbage collection: a landed write is still answered
// after unreferenced objects are pruned.
func TestLocalHost_RecordSurvivesGC(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	head := "a2a/axon/submit/XQ-axon-2"
	r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	want, err := h.OpenPR(context.Background(), openReq(head, "t", "b"))
	if err != nil {
		t.Fatal(err)
	}
	bare(t, r.origin, "gc", "--prune=now", "--quiet")
	got, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{Repo: testRepo, Branch: head})
	if err != nil || got == nil || *got != want {
		t.Fatalf("after gc: %+v, %v; want %+v", got, err, want)
	}
}

// ---- AC2 ------------------------------------------------------------------

// barrier forces n writers to hold their first judgement until all of them have
// reached it, so every one validates against the same tip. Only the first call
// of each writer waits: a retry must not wait for peers that already finished.
type barrier struct{ wg sync.WaitGroup }

func newBarrier(n int) *barrier {
	b := &barrier{}
	b.wg.Add(n)
	return b
}

func (b *barrier) wait() error {
	b.wg.Done()
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(30 * time.Second):
		return errors.New("barrier: a peer never reached its first judgement")
	}
}

// writer returns a land check private to one LocalHost value.
func (b *barrier) writer(log *callLog) LandCheckFunc {
	var first atomic.Bool
	first.Store(true)
	return func(ctx context.Context, c LandCandidate) (LandVerdict, error) {
		v, _ := log.check(ctx, c)
		if first.Swap(false) {
			if err := b.wait(); err != nil {
				return LandVerdict{}, err
			}
		}
		return v, nil
	}
}

type landResult struct {
	info PRInfo
	err  error
}

func TestLocalHost_ConcurrentLandsCompareAndSwap(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	log := &callLog{}
	b := newBarrier(2)
	// Two values, no shared in-process state: each has its own check closure,
	// exec seam and author.
	hosts := []*LocalHost{r.host(t, b.writer(log), "local:axon"), r.host(t, b.writer(log), "local:beta")}
	if hosts[0] == hosts[1] {
		t.Fatal("the two writers must be distinct values")
	}
	heads := []string{"a2a/axon/submit/XQ-axon-10", "a2a/beta/submit/XQ-beta-10"}
	shas := []string{
		r.pushHead(t, hosts[0], r.clone("axon"), heads[0], "axon/docs/one.md", "1\n", "feat: one\n"),
		r.pushHead(t, hosts[1], r.clone("beta"), heads[1], "beta/docs/two.md", "2\n", "feat: two\n"),
	}
	seed := bare(t, r.origin, "rev-parse", "refs/heads/main")

	results := make([]landResult, 2)
	var wg sync.WaitGroup
	for i := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].info, results[i].err = hosts[i].OpenPR(context.Background(), openReq(heads[i], "write "+strconv.Itoa(i), ""))
		}()
	}
	wg.Wait()
	for i, res := range results {
		if res.err != nil {
			t.Fatalf("writer %d: %v", i, res.err)
		}
	}

	// Both landed, in a chain: two first-parent merges on the seed.
	chain := strings.Fields(bare(t, r.origin, "rev-list", "--first-parent", "refs/heads/main"))
	if len(chain) != 3 || chain[2] != seed {
		t.Fatalf("first-parent chain = %v, want [M2 M1 %s]", chain, seed)
	}
	m2, m1 := chain[0], chain[1]
	if got := bare(t, r.origin, "rev-parse", m2+"^1"); got != m1 {
		t.Errorf("the second land's first parent = %s, want the first land's merge %s", got, m1)
	}
	seconds := map[string]bool{
		bare(t, r.origin, "rev-parse", m1+"^2"): true,
		bare(t, r.origin, "rev-parse", m2+"^2"): true,
	}
	if !seconds[shas[0]] || !seconds[shas[1]] {
		t.Errorf("second parents %v, want both heads %v", seconds, shas)
	}
	if results[0].info.Number == results[1].info.Number {
		t.Errorf("both writes got number %d", results[0].info.Number)
	}
	for _, res := range results {
		depth := bare(t, r.origin, "rev-list", "--first-parent", "--count", mergeOf(t, r.origin, res.info))
		if depth != strconv.Itoa(res.info.Number) {
			t.Errorf("number %d != first-parent depth %s of its merge", res.info.Number, depth)
		}
	}

	// A retry happened and the retried candidate was rebuilt on the new tip.
	calls := log.snapshot()
	if len(calls) < 3 {
		t.Fatalf("LandCheck calls = %d, want at least 3 (two forced onto one tip plus a retry)", len(calls))
	}
	bases := map[string]int{}
	for _, c := range calls {
		bases[c.BaseSHA]++
	}
	if bases[seed] < 2 {
		t.Errorf("only %d judgements saw the seed tip, want both forced onto it", bases[seed])
	}
	if bases[m1] < 1 {
		t.Errorf("no judgement saw the first land's merge %s as its tip: %v", m1, bases)
	}
	t.Logf("judgements: %d (retries: %d)", len(calls), len(calls)-2)
}

// mergeOf resolves the merge commit that landed a PR, from its number ref's record.
func mergeOf(t testing.TB, origin string, p PRInfo) string {
	t.Helper()
	blob := bare(t, origin, "cat-file", "blob", "refs/a2a/pr/"+strconv.Itoa(p.Number))
	_, rest, ok := strings.Cut(blob, `"merge":"`)
	if !ok {
		t.Fatalf("record has no merge: %s", blob)
	}
	sha, _, _ := strings.Cut(rest, `"`)
	return sha
}

func TestLocalHost_ConcurrentLandsCompareAndSwap_ThreeWriters(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	log := &callLog{}
	b := newBarrier(3)
	names := []string{"axon", "beta", "gamma"}
	hosts := make([]*LocalHost, 3)
	heads := make([]string, 3)
	for i, n := range names {
		hosts[i] = r.host(t, b.writer(log), "local:"+n)
		heads[i] = "a2a/" + n + "/submit/XQ-" + n + "-11"
	}
	gamma := t.TempDir()
	gitOut(t, "", "clone", "-q", r.origin, gamma)
	clones := []string{r.clone("axon"), r.clone("beta"), gamma}
	for i, n := range names {
		r.pushHead(t, hosts[i], clones[i], heads[i], n+"/docs/w.md", n+"\n", "feat: "+n+"\n")
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = hosts[i].OpenPR(context.Background(), openReq(heads[i], "w", ""))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	if got := bare(t, r.origin, "rev-list", "--first-parent", "--count", "refs/heads/main"); got != "4" {
		t.Errorf("first-parent length = %s, want 4 (three merges on the seed)", got)
	}
	// 3 forced judgements, then the 2 losers at least once more, then the last loser.
	n := log.n.Load()
	if n < 5 {
		t.Errorf("LandCheck calls = %d, want at least 5", n)
	}
	t.Logf("judgements: %d (retries: %d)", n, n-3)
	var numbers []int
	for _, h := range heads {
		got, err := hosts[0].FindPRByHeadBranch(context.Background(), FindPRRequest{Repo: testRepo, Branch: h})
		if err != nil || got == nil {
			t.Fatalf("FindPRByHeadBranch(%s) = %v, %v", h, got, err)
		}
		numbers = append(numbers, got.Number)
	}
	seen := map[int]bool{}
	for _, n := range numbers {
		seen[n] = true
	}
	if len(seen) != 3 {
		t.Errorf("numbers %v are not distinct", numbers)
	}
}

// A writer that keeps losing the swap gives up after the bound, with the head
// branch kept and nothing recorded.
func TestLocalHost_ConcurrentLandsCompareAndSwap_BoundExhausted(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	var calls atomic.Int32
	rival := func(_ context.Context, c LandCandidate) (LandVerdict, error) {
		calls.Add(1)
		// Another process moves main on every judgement; update-ref bypasses the hook.
		tree := bare(t, r.origin, "rev-parse", c.BaseSHA+"^{tree}")
		commit := bare(t, r.origin, "-c", "user.name=rival", "-c", "user.email=rival@x.invalid",
			"commit-tree", tree, "-p", c.BaseSHA, "-m", "rival "+strconv.Itoa(int(calls.Load())))
		bare(t, r.origin, "update-ref", "refs/heads/main", commit, c.BaseSHA)
		return LandVerdict{OK: true}, nil
	}
	h := r.host(t, rival, "local:axon")
	head := "a2a/axon/submit/XQ-axon-12"
	headSHA := r.pushHead(t, h, r.clone("axon"), head, "axon/docs/x.md", "x\n", "feat: x\n")

	_, err := h.OpenPR(context.Background(), openReq(head, "t", "b"))
	if !errors.Is(err, ErrLandContended) {
		t.Fatalf("OpenPR = %v, want ErrLandContended", err)
	}
	if got := calls.Load(); got != maxLandAttempts {
		t.Errorf("judgements = %d, want the bound %d", got, maxLandAttempts)
	}
	if got := bare(t, r.origin, "rev-parse", "refs/heads/"+head); got != headSHA {
		t.Errorf("head branch = %s, want it kept at %s", got, headSHA)
	}
	if out := bare(t, r.origin, "for-each-ref", "refs/a2a"); out != "" {
		t.Errorf("a contended write left refs: %s", out)
	}
}

// ---- AC3 + conflict + check error -----------------------------------------

func TestLocalHost_RefusalWritesNothing(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	refuse := func(context.Context, LandCandidate) (LandVerdict, error) {
		return LandVerdict{Codes: []string{"AUTHZ-001", "ART-007"}, Detail: "axon wrote into beta/"}, nil
	}
	h := r.host(t, refuse, "local:axon")
	head := "a2a/axon/submit/XQ-axon-20"
	r.pushHead(t, h, r.clone("axon"), head, "beta/docs/intrusion.md", "x\n", "feat: intrude\n")
	before := bare(t, r.origin, "rev-parse", "refs/heads/main")

	_, err := h.OpenPR(context.Background(), openReq(head, "t", "b"))
	if !errors.Is(err, ErrLandRefused) {
		t.Fatalf("OpenPR = %v, want ErrLandRefused", err)
	}
	var refusal *LandRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("errors.As(*LandRefusal) failed for %v", err)
	}
	if strings.Join(refusal.Codes, ",") != "AUTHZ-001,ART-007" || refusal.Detail != "axon wrote into beta/" {
		t.Errorf("refusal = %+v", refusal)
	}
	if !strings.Contains(err.Error(), "AUTHZ-001") {
		t.Errorf("the error does not name the code: %v", err)
	}
	for _, sentinel := range []error{ErrTransient, ErrRetriedWriteWasDuplicate} {
		if errors.Is(err, sentinel) {
			t.Errorf("a refusal matches %v, which the funnel reads as an unknown outcome", sentinel)
		}
	}
	if got := bare(t, r.origin, "rev-parse", "refs/heads/main"); got != before {
		t.Errorf("main moved on a refusal: %s -> %s", before, got)
	}
	if out := bare(t, r.origin, "for-each-ref", "refs/a2a"); out != "" {
		t.Errorf("a refusal left refs: %s", out)
	}
	if refExists(r.origin, "refs/heads/"+head) {
		t.Error("the refused head branch was not deleted")
	}
	got, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{Repo: testRepo, Branch: head})
	if err != nil || got != nil {
		t.Errorf("FindPRByHeadBranch = %+v, %v; want nil, nil", got, err)
	}

	// A corrected retry pushes fresh and lands: the branch name is free again.
	ok := r.host(t, nil, "local:axon")
	r.pushHead(t, ok, r.clone("axon"), head, "axon/docs/fixed.md", "ok\n", "feat: fixed\n")
	if _, err := ok.OpenPR(context.Background(), openReq(head, "t", "b")); err != nil {
		t.Fatalf("retry after the refusal: %v", err)
	}
}

func TestLocalHost_CheckErrorWritesNothing(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	cause := errors.New("engine exploded")
	h := r.host(t, func(context.Context, LandCandidate) (LandVerdict, error) { return LandVerdict{}, cause }, "local:axon")
	head := "a2a/axon/submit/XQ-axon-21"
	headSHA := r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	before := bare(t, r.origin, "rev-parse", "refs/heads/main")

	_, err := h.OpenPR(context.Background(), openReq(head, "t", "b"))
	if err == nil || !errors.Is(err, cause) || errors.Is(err, ErrLandRefused) {
		t.Fatalf("OpenPR = %v, want the check's own error, wrapped and not a refusal", err)
	}
	if !strings.Contains(err.Error(), "could not judge") {
		t.Errorf("the error does not say the write was not judged: %v", err)
	}
	if got := bare(t, r.origin, "rev-parse", "refs/heads/main"); got != before {
		t.Error("main moved when the check could not judge")
	}
	if out := bare(t, r.origin, "for-each-ref", "refs/a2a"); out != "" {
		t.Errorf("an unjudged write left refs: %s", out)
	}
	if got := bare(t, r.origin, "rev-parse", "refs/heads/"+head); got != headSHA {
		t.Errorf("head branch = %s, want it kept at %s so the retry resumes", got, headSHA)
	}
}

func TestLocalHost_ConflictDeletesHeadAndWritesNothing(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	first, second := "a2a/axon/submit/XQ-axon-30", "a2a/beta/submit/XQ-beta-30"
	r.pushHead(t, h, r.clone("axon"), first, "axon/docs/shared.md", "from axon\n", "feat: axon\n")
	r.pushHead(t, h, r.clone("beta"), second, "axon/docs/shared.md", "from beta\n", "feat: beta\n")
	if _, err := h.OpenPR(context.Background(), openReq(first, "t", "")); err != nil {
		t.Fatalf("first land: %v", err)
	}
	afterFirst := bare(t, r.origin, "rev-parse", "refs/heads/main")
	records := bare(t, r.origin, "for-each-ref", "refs/a2a")

	_, err := h.OpenPR(context.Background(), openReq(second, "t", ""))
	if !errors.Is(err, ErrLandConflict) {
		t.Fatalf("second OpenPR = %v, want ErrLandConflict", err)
	}
	if errors.Is(err, ErrTransient) || errors.Is(err, ErrRetriedWriteWasDuplicate) {
		t.Errorf("a conflict matches a sentinel the funnel reads as an unknown outcome: %v", err)
	}
	if got := bare(t, r.origin, "rev-parse", "refs/heads/main"); got != afterFirst {
		t.Error("main moved on a conflict")
	}
	if got := bare(t, r.origin, "for-each-ref", "refs/a2a"); got != records {
		t.Errorf("a conflict changed the records:\n%s\nwas\n%s", got, records)
	}
	if refExists(r.origin, "refs/heads/"+second) {
		t.Error("the conflicting head branch was not deleted")
	}
}

// ---- idempotency (AC6 shares the reuse case) ------------------------------

func TestLocalHost_OpenPRIsIdempotent(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	log := &callLog{}
	h := r.host(t, log.check, "local:axon")
	head := "a2a/axon/submit/XQ-axon-40"
	headSHA := r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")

	// A wrong expected head is refused before anything is read into a land.
	req := openReq(head, "title", "body")
	req.ExpectedHeadSHA = strings.Repeat("0", 40)
	if _, err := h.OpenPR(context.Background(), req); !errors.Is(err, ErrPRHeadMoved) {
		t.Fatalf("OpenPR(wrong expected head) = %v, want ErrPRHeadMoved", err)
	}
	if log.n.Load() != 0 || refExists(r.origin, "refs/a2a/pr/2") {
		t.Fatal("a moved-head refusal judged or wrote something")
	}

	req.ExpectedHeadSHA = headSHA
	first, err := h.OpenPR(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	tipAfter := bare(t, r.origin, "rev-parse", "refs/heads/main")
	// Retry after the land deleted the branch: the record answers.
	again, err := h.OpenPR(context.Background(), req)
	if err != nil || again != first {
		t.Fatalf("retry = %+v, %v; want the identical %+v", again, err, first)
	}
	req.ExpectedHeadSHA = ""
	if again, err = h.OpenPR(context.Background(), req); err != nil || again != first {
		t.Fatalf("retry without an expected head = %+v, %v; want %+v", again, err, first)
	}
	// A record for a different head than the caller expects is not an answer.
	req.ExpectedHeadSHA = strings.Repeat("1", 40)
	if _, err := h.OpenPR(context.Background(), req); !errors.Is(err, ErrPRHeadMoved) {
		t.Errorf("retry expecting another head = %v, want ErrPRHeadMoved", err)
	}
	if got := bare(t, r.origin, "rev-parse", "refs/heads/main"); got != tipAfter {
		t.Error("an idempotent retry moved main")
	}
	if log.n.Load() != 1 {
		t.Errorf("LandCheck calls = %d, want 1: a retry must not re-judge a landed write", log.n.Load())
	}

	// A head branch that never existed is an invalid request.
	if _, err := h.OpenPR(context.Background(), openReq("a2a/axon/submit/never", "t", "")); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("OpenPR(absent head, no record) = %v, want ErrInvalidRequest", err)
	}
}

// A branch present with an unchanged head and an existing record answers with
// the record too (the land was interrupted after the transaction by a crash of
// the caller, then the head was pushed again at the same commit).
func TestLocalHost_RecordWithSameHeadAnswersWhileBranchExists(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	log := &callLog{}
	h := r.host(t, log.check, "local:axon")
	head := "a2a/axon/submit/XQ-axon-41"
	headSHA := r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	first, err := h.OpenPR(context.Background(), openReq(head, "t", "b"))
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the head branch at the landed commit.
	bare(t, r.origin, "update-ref", "refs/heads/"+head, headSHA)
	again, err := h.OpenPR(context.Background(), openReq(head, "t", "b"))
	if err != nil || again != first {
		t.Fatalf("OpenPR = %+v, %v; want %+v", again, err, first)
	}
	if log.n.Load() != 1 {
		t.Errorf("judgements = %d, want 1", log.n.Load())
	}
}

func TestLocalHost_ReusedBranchNameLandsAsNewRecord(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	head := "a2a/axon/submit/XQ-axon-42"
	r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	first, err := h.OpenPR(context.Background(), openReq(head, "first", "one"))
	if err != nil {
		t.Fatal(err)
	}
	second := r.pushHead(t, h, r.clone("axon"), head, "axon/docs/b.md", "b\n", "feat: b\n")
	got, err := h.OpenPR(context.Background(), openReq(head, "second", "two"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Number == first.Number || got.HeadSHA != second || got.Title != "second" || got.Body != "two" {
		t.Fatalf("a new head on a reused name answered %+v, want a new record for %s (old: %+v)", got, second, first)
	}
	found, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{Repo: testRepo, Branch: head})
	if err != nil || found == nil || *found != got {
		t.Fatalf("FindPRByHeadBranch = %+v, %v; want the newest %+v", found, err, got)
	}
	// The earlier write is still answered by its number.
	st, err := h.CheckStatus(context.Background(), StatusRequest{Repo: testRepo, PRNumber: first.Number})
	if err != nil || st.HeadSHA != first.HeadSHA {
		t.Errorf("CheckStatus(old number) = %+v, %v", st, err)
	}
	// A retry on the second write while the branch is gone returns the second.
	if again, err := h.OpenPR(context.Background(), openReq(head, "second", "two")); err != nil || again != got {
		t.Errorf("retry = %+v, %v; want %+v", again, err, got)
	}
}

// ---- AC9 ------------------------------------------------------------------

func TestLocalHost_FirstParentOrder(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	seed := bare(t, r.origin, "rev-parse", "refs/heads/main")
	var infos []PRInfo
	for i := range 3 {
		head := fmt.Sprintf("a2a/axon/submit/XQ-axon-5%d", i)
		r.pushHead(t, h, r.clone("axon"), head, fmt.Sprintf("axon/docs/n%d.md", i), "x\n", "feat: n\n")
		info, err := h.OpenPR(context.Background(), openReq(head, "t", ""))
		if err != nil {
			t.Fatalf("land %d: %v", i, err)
		}
		infos = append(infos, info)
	}
	chain := strings.Fields(bare(t, r.origin, "rev-list", "--first-parent", "refs/heads/main"))
	if len(chain) != 4 || chain[3] != seed {
		t.Fatalf("first-parent chain = %v, want three merges on the seed %s", chain, seed)
	}
	for i, info := range infos {
		merge := chain[len(infos)-1-i]
		if got := mergeOf(t, r.origin, info); got != merge {
			t.Errorf("land %d: record merge %s, want %s in landing order", i, got, merge)
		}
		depth := bare(t, r.origin, "rev-list", "--first-parent", "--count", merge)
		if depth != strconv.Itoa(info.Number) || info.Number != i+2 {
			t.Errorf("land %d: number %d, first-parent depth %s, want %d", i, info.Number, depth, i+2)
		}
	}
}

// ---- AC10 -----------------------------------------------------------------

func TestLocalHost_MergeCarriesApprovedGatesTrailer(t *testing.T) {
	t.Parallel()
	const trailer = TrailerApprovedGates
	cases := []struct {
		name    string
		message string
		want    string // the lines the merge message must end with ("" = no trailer)
	}{
		{"one trailer", "feat: x\n\nbody\n\n" + trailer + ": G1\n", trailer + ": G1"},
		{"odd spacing is copied verbatim", "feat: x\n\n" + trailer + ":    G1,\tG2 \t\n", trailer + ":    G1,\tG2 \t"},
		{"two trailers", "feat: x\n\nSigned-off-by: a\n" + trailer + ": G1\n" + trailer + ": G2\n", trailer + ": G1\n" + trailer + ": G2"},
		{"no trailer", "feat: x\n\nbody only\n", ""},
		{"a similar token is not the trailer", "feat: x\n\n" + trailer + "-Extra: G1\nX-" + trailer + ": G1\n", ""},
		{"a trailer-looking line in the body is not a trailer", trailer + ": G1\n\nlater paragraph\n", ""},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLocalRig(t)
			log := &callLog{}
			h := r.host(t, log.check, "local:axon")
			head := fmt.Sprintf("a2a/axon/submit/XQ-axon-6%d", i)
			r.pushHead(t, h, r.clone("axon"), head, "axon/docs/t.md", "t\n", tc.message)
			info, err := h.OpenPR(context.Background(), openReq(head, "the title", ""))
			if err != nil {
				t.Fatal(err)
			}
			calls := log.snapshot()
			if len(calls) != 1 || calls[0].HeadMessage != tc.message {
				t.Fatalf("HeadMessage = %q, want the head's message unchanged %q", calls[0].HeadMessage, tc.message)
			}
			got := rawMessage(t, r.origin, mergeOf(t, r.origin, info))
			subject := "Merge pull request #" + strconv.Itoa(info.Number) + " from " + head + "\n\nthe title\n"
			want := subject
			if tc.want != "" {
				want += "\n" + tc.want + "\n"
			}
			if got != want {
				t.Errorf("merge message = %q, want %q", got, want)
			}
		})
	}
}

// ---- AC13 -----------------------------------------------------------------

func TestLocalHost_EmptyLandAuthorRefusesBeforeWrite(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	log := &callLog{}
	h := r.host(t, log.check, "")
	if h.author != "" {
		t.Fatal("the host must open with an empty author")
	}
	head := "a2a/axon/submit/XQ-axon-70"
	headSHA := r.pushHead(t, r.host(t, nil, "local:axon"), r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	tip := bare(t, r.origin, "rev-parse", "refs/heads/main")
	refsBefore := bare(t, r.origin, "for-each-ref")

	_, err := h.OpenPR(context.Background(), openReq(head, "t", "b"))
	if err == nil || !strings.Contains(err.Error(), "no system is configured") {
		t.Fatalf("OpenPR = %v, want an error naming the missing system", err)
	}
	if log.n.Load() != 0 {
		t.Errorf("the land check ran %d times, want 0", log.n.Load())
	}
	if got := bare(t, r.origin, "rev-parse", "refs/heads/main"); got != tip {
		t.Error("main moved")
	}
	if got := bare(t, r.origin, "for-each-ref"); got != refsBefore {
		t.Errorf("refs changed:\n%s\nwas\n%s", got, refsBefore)
	}
	if got := bare(t, r.origin, "rev-parse", "refs/heads/"+head); got != headSHA {
		t.Error("the head branch changed")
	}

	// The read side still works without a system.
	if _, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{Repo: testRepo, Branch: head}); err != nil {
		t.Errorf("FindPRByHeadBranch on an author-less host: %v", err)
	}
	if _, err := h.InspectLocalSpace(context.Background()); err != nil {
		t.Errorf("InspectLocalSpace on an author-less host: %v", err)
	}
}

func TestLocalHost_OpenPRRejectsMalformedRequests(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	for name, req := range map[string]OpenPRRequest{
		"no head":       {Head: "", Base: "main"},
		"no base":       {Head: "a2a/x", Base: ""},
		"bad head name": {Head: "a2a/x y", Base: "main"},
		"bad base name": {Head: "a2a/x", Base: "ma..in"},
		"NUL in title":  {Head: "a2a/x", Base: "main", Title: "a\x00b"},
		"bad utf8 body": {Head: "a2a/x", Base: "main", Body: "\xff\xfe"},
	} {
		if _, err := h.OpenPR(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: OpenPR = %v, want ErrInvalidRequest", name, err)
		}
	}
	if _, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("FindPRByHeadBranch(no branch) = %v", err)
	}
	if _, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{Branch: "a b"}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("FindPRByHeadBranch(bad name) = %v", err)
	}
	// A base branch that does not exist.
	head := "a2a/axon/submit/XQ-axon-80"
	r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	req := openReq(head, "t", "")
	req.Base = "nonexistent"
	if _, err := h.OpenPR(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("OpenPR(missing base) = %v, want ErrInvalidRequest", err)
	}
	if !refExists(r.origin, "refs/heads/"+head) {
		t.Error("a request for a missing base deleted the head branch")
	}
}

// ---- git floor ------------------------------------------------------------

func TestLocalHost_OldGitGivesErrGitTooOld(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	head := "a2a/axon/submit/XQ-axon-90"
	headSHA := r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	real := h.exec
	h.exec = func(ctx context.Context, stdin []byte, limit int, args ...string) recoveryGitResult {
		for _, a := range args {
			if a == "merge-tree" {
				// What a git older than 2.38 answers to `merge-tree --write-tree`.
				return recoveryGitResult{exitCode: 129, stderr: []byte("usage: git merge-tree <base-tree> <branch1> <branch2>")}
			}
		}
		return real(ctx, stdin, limit, args...)
	}
	_, err := h.OpenPR(context.Background(), openReq(head, "t", ""))
	if !errors.Is(err, ErrGitTooOld) {
		t.Fatalf("OpenPR = %v, want ErrGitTooOld", err)
	}
	if !strings.Contains(err.Error(), "2.38") {
		t.Errorf("the error does not name the floor: %v", err)
	}
	if got := bare(t, r.origin, "rev-parse", "refs/heads/"+head); got != headSHA {
		t.Error("the head branch was touched on an unsupported git")
	}
}

// A real git failure surfaces as an error that is neither a refusal nor a
// transient outcome.
func TestLocalHost_GitFailureIsReported(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	head := "a2a/axon/submit/XQ-axon-91"
	r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	h.exec = func(context.Context, []byte, int, ...string) recoveryGitResult {
		return recoveryGitResult{exitCode: -1, err: errors.New("fork/exec: resource temporarily unavailable")}
	}
	_, err := h.OpenPR(context.Background(), openReq(head, "t", ""))
	if !errors.Is(err, errLocalGit) || errors.Is(err, ErrLandRefused) {
		t.Fatalf("OpenPR = %v, want a local git error", err)
	}
	h.exec = func(context.Context, []byte, int, ...string) recoveryGitResult {
		return recoveryGitResult{exitCode: 3, stderr: []byte(strings.Repeat("e", 500))}
	}
	_, err = h.FindPRByHeadBranch(context.Background(), FindPRRequest{Repo: testRepo, Branch: head})
	if !errors.Is(err, errLocalGit) {
		t.Fatalf("FindPRByHeadBranch = %v, want a local git error", err)
	}
	if len(err.Error()) > 500 {
		t.Errorf("git's stderr was not bounded: %d bytes", len(err.Error()))
	}
}

// ---- small surfaces -------------------------------------------------------

func TestLocalHost_NeutralReads(t *testing.T) {
	t.Parallel()
	h := newLocalHost(t.TempDir(), validCheck, "local:axon")
	ctx := context.Background()
	if rs, err := h.ReviewStatus(ctx, StatusRequest{PRNumber: 1}); err != nil || rs.Approved {
		t.Errorf("ReviewStatus = %+v, %v; want not approved, no error", rs, err)
	}
	scopes, known, err := h.TokenScopes(ctx, Credential{})
	if scopes != nil || known || err != nil {
		t.Errorf("TokenScopes = %v, %v, %v; want nil, false, nil", scopes, known, err)
	}
	open, err := h.ListOpenPRs(ctx, ListOpenPRsRequest{})
	if err != nil || len(open) != 0 {
		t.Errorf("ListOpenPRs = %v, %v; want none", open, err)
	}
}

func TestLocalHost_ReadsRemoteBranchesThroughSharedMechanics(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	head := "a2a/axon/submit/XQ-axon-95"
	sha := r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	var reader RemoteBranchReader = h
	got, err := reader.ReadRemoteBranch(context.Background(), RemoteBranchRequest{RepoDir: r.clone("axon"), RemoteURL: r.origin, Branch: head})
	if err != nil || !got.Exists || got.SHA != sha {
		t.Fatalf("ReadRemoteBranch = %+v, %v; want %s", got, err, sha)
	}
	absent, err := reader.ReadRemoteBranch(context.Background(), RemoteBranchRequest{RepoDir: r.clone("axon"), RemoteURL: r.origin, Branch: "a2a/none"})
	if err != nil || absent.Exists {
		t.Fatalf("ReadRemoteBranch(absent) = %+v, %v", absent, err)
	}
}

func TestLandErrors_AreNotUnknownOutcomes(t *testing.T) {
	t.Parallel()
	for _, e := range []error{ErrLandRefused, ErrLandConflict, ErrLandContended, ErrGitTooOld} {
		wrapped := &Error{Op: "OpenPR", Err: fmt.Errorf("%w: detail", e)}
		if !errors.Is(wrapped, e) {
			t.Errorf("%v does not unwrap", e)
		}
		if errors.Is(wrapped, ErrTransient) || errors.Is(wrapped, ErrRetriedWriteWasDuplicate) {
			t.Errorf("%v matches a sentinel the funnel reads as an unknown outcome", e)
		}
	}
	if !strings.Contains(ErrGitTooOld.Error(), "2.38") {
		t.Errorf("ErrGitTooOld does not name the floor: %v", ErrGitTooOld)
	}
	for _, tc := range []struct {
		r    LandRefusal
		want string
	}{
		{LandRefusal{Codes: []string{"A", "B"}, Detail: "why"}, "A, B: why"},
		{LandRefusal{Codes: []string{"A"}}, "A"},
		{LandRefusal{Detail: "why"}, "why"},
	} {
		if got := tc.r.Error(); got != tc.want {
			t.Errorf("LandRefusal%+v.Error() = %q, want %q", tc.r, got, tc.want)
		}
	}
}

func TestLocalHost_AuthorEmailIsAnAddress(t *testing.T) {
	t.Parallel()
	if got := authorEmail("local:axon"); got != "local-axon@local.a2a.invalid" {
		t.Errorf("authorEmail = %q", got)
	}
	if got := authorEmail("local:a b/ü"); strings.ContainsAny(got, ": /ü") {
		t.Errorf("authorEmail kept an illegal character: %q", got)
	}
}

// ---- AC12 -----------------------------------------------------------------

func TestLocalHost_InspectAndCapabilities(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	ctx := context.Background()

	if got, want := h.Capabilities(), (Capabilities{
		NeedsCredential: false, ReviewsWrites: false, OwnerIdentitySelfAsserted: true,
		LandsSynchronously: true, OnThisMachine: true,
	}); got != want {
		t.Errorf("Capabilities = %+v, want %+v", got, want)
	}
	var _ LocalSpaceInspector = h

	inspect := func() LocalSpaceInspection {
		t.Helper()
		got, err := h.InspectLocalSpace(ctx)
		if err != nil {
			t.Fatalf("InspectLocalSpace: %v", err)
		}
		return got
	}
	hook := filepath.Join(r.origin, "hooks", preReceiveHookName)

	fresh := inspect()
	if !fresh.HookPresent || !fresh.HookExecutable || !fresh.HookCurrent || !fresh.Writable ||
		fresh.WritableDetail != "" || len(fresh.MainNonLandCommits) != 0 {
		t.Fatalf("a prepared space inspects as %+v", fresh)
	}

	// Edited bytes: present and executable, not current.
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := inspect(); !got.HookPresent || !got.HookExecutable || got.HookCurrent {
		t.Errorf("edited hook inspects as %+v", got)
	}
	// Right bytes, mode 0644: present, not executable, not current.
	if err := os.WriteFile(hook, []byte(preReceiveHook("main")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hook, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := inspect(); !got.HookPresent || got.HookExecutable || got.HookCurrent {
		t.Errorf("0644 hook inspects as %+v", got)
	}
	// Missing.
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if got := inspect(); got.HookPresent || got.HookExecutable || got.HookCurrent {
		t.Errorf("missing hook inspects as %+v", got)
	}
	// PrepareLocalSpace restores it.
	if err := PrepareLocalSpace(ctx, r.origin, "main"); err != nil {
		t.Fatal(err)
	}
	if got := inspect(); !got.HookCurrent {
		t.Errorf("after PrepareLocalSpace: %+v", got)
	}

	// A land is not a non-land commit; a one-parent commit placed with update-ref is.
	head := "a2a/axon/submit/XQ-axon-100"
	r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	if _, err := h.OpenPR(ctx, openReq(head, "t", "")); err != nil {
		t.Fatal(err)
	}
	if got := inspect(); len(got.MainNonLandCommits) != 0 {
		t.Errorf("a land reported as non-land: %v", got.MainNonLandCommits)
	}
	tip := bare(t, r.origin, "rev-parse", "refs/heads/main")
	tree := bare(t, r.origin, "rev-parse", tip+"^{tree}")
	rogue := bare(t, r.origin, "-c", "user.name=rogue", "-c", "user.email=r@x.invalid", "commit-tree", tree, "-p", tip, "-m", "rogue")
	bare(t, r.origin, "update-ref", "refs/heads/main", rogue, tip)
	if got := inspect(); len(got.MainNonLandCommits) != 1 || got.MainNonLandCommits[0] != rogue {
		t.Errorf("MainNonLandCommits = %v, want [%s]", got.MainNonLandCommits, rogue)
	}
}

func TestLocalHost_InspectUnwritable(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	refs := filepath.Join(r.origin, "refs")
	// The directory is gone: deterministic whatever user runs the test.
	moved := refs + ".gone"
	if err := os.Rename(refs, moved); err != nil {
		t.Fatal(err)
	}
	if ok, detail := h.probeWritable(); ok || !strings.Contains(detail, "refs/") {
		t.Errorf("missing refs/ probes as writable=%v %q", ok, detail)
	}
	if err := os.Rename(moved, refs); err != nil {
		t.Fatal(err)
	}

	if os.Geteuid() == 0 {
		return // root writes through a read-only directory; the case above covers it
	}
	if err := os.Chmod(refs, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(refs, 0o755) })
	if ok, detail := h.probeWritable(); ok || !strings.Contains(detail, "refs/") {
		t.Errorf("read-only refs/ probes as writable=%v %q", ok, detail)
	}
}

func TestLocalHost_InspectRefusesAHeadThatIsNoBranch(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	tip := bare(t, r.origin, "rev-parse", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(r.origin, "HEAD"), []byte(tip+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.InspectLocalSpace(context.Background()); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("InspectLocalSpace(detached HEAD) = %v, want ErrInvalidRequest", err)
	}
}

// ---- AC5 ------------------------------------------------------------------

func TestPrepareLocalSpace_HookRefusesMain(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	clone := r.clone("axon")

	gitOut(t, clone, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(clone, "axon", "docs", "direct.md"), []byte("direct\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, clone, "add", "axon/docs/direct.md")
	if out, err := gitTry(clone, []string{
		"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@x.invalid", "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@x.invalid",
	}, "commit", "-q", "-m", "direct"); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	mainBefore := bare(t, r.origin, "rev-parse", "refs/heads/main")

	for _, tc := range []struct {
		name, refspec, ref string
	}{
		{"push to main", "HEAD:refs/heads/main", "refs/heads/main"},
		{"delete main", ":refs/heads/main", "refs/heads/main"},
		{"create a landed record", "HEAD:refs/a2a/x", "refs/a2a/x"},
		{"create a number ref", "HEAD:refs/a2a/pr/9", "refs/a2a/pr/9"},
	} {
		out, err := gitTry(clone, nil, "push", r.origin, tc.refspec)
		if err == nil {
			t.Errorf("%s: push succeeded:\n%s", tc.name, out)
			continue
		}
		if want := fmt.Sprintf(PreReceiveRefusal, tc.ref); !strings.Contains(out, want) {
			t.Errorf("%s: stderr does not carry the refusal for %s:\n%s", tc.name, tc.ref, out)
		}
	}
	if got := bare(t, r.origin, "rev-parse", "refs/heads/main"); got != mainBefore {
		t.Error("a refused push moved main")
	}
	if out := bare(t, r.origin, "for-each-ref", "refs/a2a"); out != "" {
		t.Errorf("a refused push created refs: %s", out)
	}

	// A head-branch push passes, through the host's own PushBranch.
	head := "a2a/axon/submit/XQ-axon-110"
	r.pushHead(t, h, clone, head, "axon/docs/a.md", "a\n", "feat: a\n")
	if !refExists(r.origin, "refs/heads/"+head) {
		t.Error("the head-branch push did not land")
	}
	// ...and a rejected push through PushBranch is an ErrPushRejected carrying the text.
	gitOut(t, clone, "checkout", "-q", "main")
	_, err := h.PushBranch(context.Background(), PushBranchRequest{RepoDir: clone, LocalRef: "HEAD", Branch: "main", RemoteURL: r.origin})
	if !errors.Is(err, ErrPushRejected) || !strings.Contains(err.Error(), fmt.Sprintf(PreReceiveRefusal, "refs/heads/main")) {
		t.Errorf("PushBranch(main) = %v, want ErrPushRejected carrying the refusal", err)
	}
	// update-ref is not subject to the hook: the land itself moves main.
	if _, err := h.OpenPR(context.Background(), openReq(head, "t", "")); err != nil {
		t.Fatalf("the land was blocked by its own hook: %v", err)
	}
}

func TestPrepareLocalSpace_IdempotentAndRestoring(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newLocalRig(t)
	hook := filepath.Join(r.origin, "hooks", preReceiveHookName)
	if got := bare(t, r.origin, "config", "--local", "--get", "a2a.host"); got != "local" {
		t.Fatalf("marker = %q", got)
	}
	want := preReceiveHook("main")

	// A current hook is not rewritten.
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(hook, old, old); err != nil {
		t.Fatal(err)
	}
	if err := PrepareLocalSpace(ctx, r.origin, "main"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(hook)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("a re-run rewrote a current hook (mtime %v)", info.ModTime())
	}
	// Hand-edited bytes and mode are restored.
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hook, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PrepareLocalSpace(ctx, r.origin, "main"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(hook)
	if err != nil || string(got) != want {
		t.Fatalf("hook not restored: %q, %v", got, err)
	}
	if info, _ := os.Stat(hook); info.Mode().Perm()&0o111 == 0 {
		t.Errorf("hook mode = %v, want executable", info.Mode())
	}
	// No stray staging files.
	entries, _ := os.ReadDir(filepath.Dir(hook))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".new-") {
			t.Errorf("leftover staging file %s", e.Name())
		}
	}
	if n := strings.Count(bare(t, r.origin, "config", "--local", "--get-all", "a2a.host"), "local"); n != 1 {
		t.Errorf("marker written %d times", n)
	}
}

func TestPrepareLocalSpace_RefusesWhatItCannotHook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newLocalRig(t)
	for name, tc := range map[string]struct{ dir, base string }{
		"empty dir":      {"", "main"},
		"empty base":     {r.origin, ""},
		"quote in base":  {r.origin, "ma'in"},
		"dollar in base": {r.origin, "$(x)"},
		"dotdot base":    {r.origin, "a..b"},
		"lock base":      {r.origin, "x.lock"},
		"slash base":     {r.origin, "/main"},
		"space in base":  {r.origin, "a b"},
		"not a repo":     {t.TempDir(), "main"},
		"work tree":      {r.clone("axon"), "main"},
	} {
		if err := PrepareLocalSpace(ctx, tc.dir, tc.base); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: PrepareLocalSpace = %v, want ErrInvalidRequest", name, err)
		}
	}
	if localMarked(ctx, filepath.Join(r.clone("axon"), ".git")) {
		t.Error("a refused call marked a participant clone")
	}
}

func TestPrepareLocalSpace_NestedBaseIsGuarded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := spacefixture.New(t, "axon")
	gitOut(t, "", "--git-dir="+fx.OriginDir, "branch", "release/1", "main")
	if err := PrepareLocalSpace(ctx, fx.OriginDir, "release/1"); err != nil {
		t.Fatal(err)
	}
	clone := fx.Clones["axon"]
	commitInDir(t, clone, "axon/docs/direct.md", "direct\n") // a push of an unchanged commit is a no-op that never reaches the hook
	out, err := gitTry(clone, nil, "push", fx.OriginDir, "HEAD:refs/heads/release/1")
	if err == nil || !strings.Contains(out, fmt.Sprintf(PreReceiveRefusal, "refs/heads/release/1")) {
		t.Errorf("push to the nested base: %v\n%s", err, out)
	}
	// The unguarded main of this space is not the base, so it passes.
	if out, err := gitTry(clone, nil, "push", fx.OriginDir, "HEAD:refs/heads/a2a/x"); err != nil {
		t.Errorf("head push refused: %v\n%s", err, out)
	}
}

func TestPreReceiveRefusal_IsSafeToEmbedInTheHook(t *testing.T) {
	t.Parallel()
	if strings.Count(PreReceiveRefusal, "%") != 1 || strings.Count(PreReceiveRefusal, "%s") != 1 {
		t.Errorf("PreReceiveRefusal must hold exactly one %%s verb: %q", PreReceiveRefusal)
	}
	if strings.ContainsAny(PreReceiveRefusal, "'\\\n") {
		t.Errorf("PreReceiveRefusal holds a character the hook's single-quoted printf cannot carry: %q", PreReceiveRefusal)
	}
	if !strings.Contains(PreReceiveRefusal, "a2a submit") {
		t.Errorf("PreReceiveRefusal does not point at the command that lands a write: %q", PreReceiveRefusal)
	}
	hook := preReceiveHook("main")
	if !strings.Contains(hook, PreReceiveRefusal) || !strings.HasPrefix(hook, "#!/bin/sh\n") {
		t.Errorf("hook does not embed the refusal verbatim:\n%s", hook)
	}
}
