package fakegithub

// Pinning tests for the pulls/branches/refs HTTP surface driven over REAL
// HTTP against a running Server (no direct field pokes for the request
// path — only for setup/verification, mirroring how internal/host's own
// ghClient actually talks to this fake).
//
// Fixture style follows internal/host/remote_recovery_test.go's own
// convention (gitfixture.Args + exec.Command, no test-only wrapper struct):
// a bare origin built with raw git, branches pushed by a throwaway
// non-bare clone.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

const (
	testOwner = "acme"
	testRepo  = "space"
)

// runGit executes one git command (gitfixture.Args-wrapped, so it dies with
// the test) and fails the test on error.
func runGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", gitfixture.Args(full...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", full, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newSeededOrigin builds a bare repo with one commit on main — the base
// every PR in this file merges into.
func newSeededOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	runGit(t, "", "init", "-q", "--bare", "-b", "main", origin)

	work := filepath.Join(root, "seed")
	runGit(t, "", "init", "-q", "-b", "main", work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "-c", "user.name=fixture", "-c", "user.email=fixture@a2ahub.invalid", "commit", "-q", "-m", "seed")
	runGit(t, work, "push", "-q", origin, "HEAD:refs/heads/main")
	return origin
}

// pushBranch commits one file to a new branch off repo's current default
// branch and pushes it, returning the branch's head SHA. repo may be the
// origin or a fork's bare repo — both accept a push the same way.
func pushBranch(t *testing.T, repo, branch, relative, content string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, "", "clone", "-q", repo, dir)
	runGit(t, dir, "checkout", "-q", "-b", branch)
	full := filepath.Join(dir, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "-c", "user.name=fixture", "-c", "user.email=fixture@a2ahub.invalid", "commit", "-q", "-m", branch)
	sha := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "push", "-q", repo, "HEAD:refs/heads/"+branch)
	return sha
}

// httpJSON issues one HTTP call against the fake and decodes a JSON
// response body into out (nil out skips decoding). Returns the raw body too,
// for error messages and non-JSON responses (204 with an empty body).
func httpJSON(t *testing.T, method, url string, body, out any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s %s response %q: %v", method, url, raw, err)
		}
	}
	return resp.StatusCode, raw
}

// wirePull mirrors internal/livee2e/client_live.go's own pullPayload
// EXACTLY — the point of this file is proving the fake answers what that
// struct decodes, so drift between the two must show up as a test failure,
// not as a silently-passing looser shape.
type wirePull struct {
	Number int `json:"number"`
	Head   struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Merged         bool   `json:"merged"`
	MergeableState string `json:"mergeable_state"`
	State          string `json:"state"`
	Body           string `json:"body"`
	// AutoMerge mirrors host.ListOpenPRs' own decode field exactly (`any`,
	// json:"auto_merge") rather than a bool: the caller's whole distinction
	// is null-vs-non-null (AutoMergeArmed = `result.AutoMerge != nil`), and
	// a bool field here would silently paper over a regression that made
	// this fake emit `false` instead of omitting/nulling the field.
	AutoMerge any `json:"auto_merge"`
}

func openPR(t *testing.T, gh *Server, title, head, base, body string) int {
	t.Helper()
	var created struct {
		Number int `json:"number"`
	}
	url := fmt.Sprintf("%s/repos/%s/%s/pulls", gh.URL, testOwner, testRepo)
	status, raw := httpJSON(t, http.MethodPost, url, map[string]any{
		"title": title, "head": head, "base": base, "body": body,
	}, &created)
	if status != http.StatusOK {
		t.Fatalf("open PR: status %d: %s", status, raw)
	}
	return created.Number
}

func mergePR(t *testing.T, gh *Server, number int) {
	t.Helper()
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/merge", gh.URL, testOwner, testRepo, number)
	status, raw := httpJSON(t, http.MethodPut, url, map[string]any{"merge_method": "merge"}, nil)
	if status != http.StatusOK {
		t.Fatalf("merge PR %d: status %d: %s", number, status, raw)
	}
}

// armAutoMerge issues the ONE GraphQL mutation host.EnableAutoMerge sends
// (enablePullRequestAutoMerge). node_id is deterministic ("PR_<number>",
// pullPayload's own construction), so this needs no round trip to read it
// back first.
func armAutoMerge(t *testing.T, gh *Server, number int) {
	t.Helper()
	body := map[string]any{
		"query":     "mutation { enablePullRequestAutoMerge(input: {pullRequestId: $id}) { clientMutationId } }",
		"variables": map[string]any{"id": fmt.Sprintf("PR_%d", number)},
	}
	// GraphQL answers 200 even for a refused mutation (the refusal rides in
	// the body's `errors` array) — matching real GitHub, and exactly what
	// internal/host/github.go's own graphQLCall doc explains at length.
	status, raw := httpJSON(t, http.MethodPost, gh.URL+"/graphql", body, nil)
	if status != http.StatusOK {
		t.Fatalf("arm auto-merge for PR %d: status %d: %s", number, status, raw)
	}
}

func listPulls(t *testing.T, gh *Server, query string) []wirePull {
	t.Helper()
	var out []wirePull
	url := fmt.Sprintf("%s/repos/%s/%s/pulls%s", gh.URL, testOwner, testRepo, query)
	status, raw := httpJSON(t, http.MethodGet, url, nil, &out)
	if status != http.StatusOK {
		t.Fatalf("list pulls %q: status %d: %s", query, status, raw)
	}
	return out
}

func getPull(t *testing.T, gh *Server, number int) wirePull {
	t.Helper()
	var out wirePull
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", gh.URL, testOwner, testRepo, number)
	status, raw := httpJSON(t, http.MethodGet, url, nil, &out)
	if status != http.StatusOK {
		t.Fatalf("get PR %d: status %d: %s", number, status, raw)
	}
	return out
}

func listBranchNames(t *testing.T, gh *Server, query string) []string {
	t.Helper()
	var payload []struct {
		Name string `json:"name"`
	}
	url := fmt.Sprintf("%s/repos/%s/%s/branches%s", gh.URL, testOwner, testRepo, query)
	status, raw := httpJSON(t, http.MethodGet, url, nil, &payload)
	if status != http.StatusOK {
		t.Fatalf("list branches %q: status %d: %s", query, status, raw)
	}
	names := make([]string, 0, len(payload))
	for _, p := range payload {
		names = append(names, p.Name)
	}
	return names
}

func deleteRef(t *testing.T, gh *Server, ref string) int {
	t.Helper()
	url := fmt.Sprintf("%s/repos/%s/%s/git/refs/%s", gh.URL, testOwner, testRepo, ref)
	status, _ := httpJSON(t, http.MethodDelete, url, nil, nil)
	return status
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func findWirePull(t *testing.T, list []wirePull, number int) wirePull {
	t.Helper()
	for _, p := range list {
		if p.Number == number {
			return p
		}
	}
	t.Fatalf("PR %d not found in %+v", number, list)
	return wirePull{}
}

// TestListPulls_UnfilteredReturnsEveryPRWithFullShape pins Defect 1 + Defect
// 2 together: `state=all` with NO `head` param — the exact query
// ghClient.ListPulls (internal/livee2e) issues — must return every PR, and
// every field pullPayload decodes must be populated and correct.
func TestListPulls_UnfilteredReturnsEveryPRWithFullShape(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)

	shaAlpha := pushBranch(t, origin, "feature/alpha", "alpha.txt", "alpha\n")
	shaBeta := pushBranch(t, origin, "feature/beta", "beta.txt", "beta\n")
	numAlpha := openPR(t, gh, "alpha", "feature/alpha", "main", "opkey:alpha")
	numBeta := openPR(t, gh, "beta", "feature/beta", "main", "opkey:beta")
	mergePR(t, gh, numAlpha)
	mergedMain := runGit(t, origin, "rev-parse", "refs/heads/main")

	list := listPulls(t, gh, "?state=all&per_page=100")
	if len(list) != 2 {
		t.Fatalf("state=all unfiltered list = %d PRs, want 2 — the exact defect: absent `head` must not answer empty. Got: %+v", len(list), list)
	}

	alpha := findWirePull(t, list, numAlpha)
	// alpha's head branch is a same-repo head, pruned by DeleteBranchOnMerge's
	// default (true) as part of this merge — but head.sha must still equal
	// what it was merged FROM: real GitHub keeps reporting a merged PR's
	// head commit forever, branch or no branch, and pullPayload's own
	// captured-at-merge-time fallback (PR.HeadSHA) is what makes that true
	// here too (see TestHeadSHASurvivesPruneWhileBranchGenuinelyDisappears
	// for the property named directly, alongside the branch listing).
	if alpha.Head.Ref != "feature/alpha" || alpha.Head.SHA != shaAlpha {
		t.Errorf("alpha.head = %+v, want ref=feature/alpha sha=%s", alpha.Head, shaAlpha)
	}
	if alpha.State != "closed" || !alpha.Merged {
		t.Errorf("alpha state/merged = %q/%v, want closed/true", alpha.State, alpha.Merged)
	}
	if alpha.MergeCommitSHA != mergedMain {
		t.Errorf("alpha.merge_commit_sha = %q, want %q (main's tip right after the merge)", alpha.MergeCommitSHA, mergedMain)
	}

	beta := findWirePull(t, list, numBeta)
	if beta.Head.Ref != "feature/beta" || beta.Head.SHA != shaBeta {
		t.Errorf("beta.head = %+v, want ref=feature/beta sha=%s", beta.Head, shaBeta)
	}
	if beta.State != "open" || beta.Merged {
		t.Errorf("beta state/merged = %q/%v, want open/false", beta.State, beta.Merged)
	}
	if beta.MergeCommitSHA != "" {
		t.Errorf("beta.merge_commit_sha = %q, want empty (never merged)", beta.MergeCommitSHA)
	}
	if alpha.MergeableState != "clean" || beta.MergeableState != "clean" {
		t.Errorf("mergeable_state = %q/%q, want the default %q for both", alpha.MergeableState, beta.MergeableState, "clean")
	}

	// Pagination must terminate a caller paginating past the (short) first
	// page: page 2 answers empty, never re-serves page 1's rows.
	if page2 := listPulls(t, gh, "?state=all&per_page=100&page=2"); len(page2) != 0 {
		t.Errorf("page=2 = %+v, want empty (a short page-1 must be the last page)", page2)
	}
}

// TestMergeableStateKnobDrivesThePayload pins the settable knob itself: set
// BEFORE the fake serves any request (same ordering every other Server knob
// in this package uses — setting one mid-flight, after New has started the
// httptest.Server's goroutine, is a data race, not merely a style choice).
func TestMergeableStateKnobDrivesThePayload(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)
	gh.MergeableState = "dirty"

	pushBranch(t, origin, "feature/alpha", "alpha.txt", "alpha\n")
	number := openPR(t, gh, "alpha", "feature/alpha", "main", "")

	got := getPull(t, gh, number)
	if got.MergeableState != "dirty" {
		t.Fatalf("mergeable_state = %q, want the driven value %q", got.MergeableState, "dirty")
	}
}

// TestAutoMergeField_ArmedButNotYetMergedReportsNonNull pins the field
// host.ListOpenPRs' whole "stuck green PRs" doctor probe depends on: a PR
// whose auto-merge mutation succeeded but has not yet actually merged
// (AutoMerge=false — this fake's stand-in for "CI has not gone green yet")
// must report a non-null `auto_merge`, not the never-armed default.
func TestAutoMergeField_ArmedButNotYetMergedReportsNonNull(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)
	gh.AutoMerge = false // arming must not fast-forward, so "armed but open" is observable

	pushBranch(t, origin, "feature/alpha", "alpha.txt", "alpha\n")
	number := openPR(t, gh, "alpha", "feature/alpha", "main", "")

	if before := getPull(t, gh, number); before.AutoMerge != nil {
		t.Fatalf("auto_merge before arming = %#v, want nil (never armed)", before.AutoMerge)
	}

	armAutoMerge(t, gh, number)

	after := getPull(t, gh, number)
	if after.AutoMerge == nil {
		t.Fatal("auto_merge after arming = nil, want a non-null object")
	}
	if after.Merged {
		t.Fatal("PR reports merged, want still open — AutoMerge=false must not fast-forward on its own")
	}
}

// TestAutoMergeField_CleanStatusRefusalLeavesItNull pins the other half: the
// AutoMergeAlreadyClean refusal must NOT report armed. Getting this backwards
// would make the refusal itself untestable through host.ListOpenPRs, since
// every PR would silently look armed regardless of what was actually armed.
func TestAutoMergeField_CleanStatusRefusalLeavesItNull(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)
	gh.AutoMergeAlreadyClean = true

	pushBranch(t, origin, "feature/alpha", "alpha.txt", "alpha\n")
	number := openPR(t, gh, "alpha", "feature/alpha", "main", "")

	armAutoMerge(t, gh, number) // refused; GraphQL still answers 200

	got := getPull(t, gh, number)
	if got.AutoMerge != nil {
		t.Fatalf("auto_merge after a clean-status refusal = %#v, want nil", got.AutoMerge)
	}
}

// TestListPulls_HeadFilterMatchesBareAndOwnerForms pins that the `head=`
// filter still narrows the list, and matches BOTH shapes a stored PR.Head
// can take: bare for a same-repo PR queried with an owner:branch head (the
// form FindPRByHeadBranch always sends, owner defaulted to the repo's own),
// and owner:branch for a cross-fork PR queried with its exact form.
func TestListPulls_HeadFilterMatchesBareAndOwnerForms(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)

	pushBranch(t, origin, "feature/alpha", "alpha.txt", "alpha\n")
	numAlpha := openPR(t, gh, "alpha", "feature/alpha", "main", "")

	forkURL := fmt.Sprintf("%s/repos/%s/%s/forks", gh.URL, testOwner, testRepo)
	if status, raw := httpJSON(t, http.MethodPost, forkURL, nil, nil); status != http.StatusOK {
		t.Fatalf("create fork: status %d: %s", status, raw)
	}
	forkDir := gh.ForkDir(ForkLogin)
	if forkDir == "" {
		t.Fatal("ForkDir empty after forking")
	}
	pushBranch(t, forkDir, "feature/gamma", "gamma.txt", "gamma\n")
	numGamma := openPR(t, gh, "gamma", ForkLogin+":feature/gamma", "main", "")

	// owner:branch query narrows to the SAME-REPO PR via its bare stored Head.
	same := listPulls(t, gh, "?state=all&head="+testOwner+":feature/alpha")
	if len(same) != 1 || same[0].Number != numAlpha {
		t.Fatalf("head=%s:feature/alpha = %+v, want exactly PR %d", testOwner, same, numAlpha)
	}

	// owner:branch query narrows to the CROSS-FORK PR via its exact stored Head.
	forked := listPulls(t, gh, "?state=all&head="+ForkLogin+":feature/gamma")
	if len(forked) != 1 || forked[0].Number != numGamma {
		t.Fatalf("head=%s:feature/gamma = %+v, want exactly PR %d", ForkLogin, forked, numGamma)
	}
}

// TestListPulls_StateFiltering pins GitHub's own default (`state` absent
// means "open") alongside the explicit open/closed/all values.
func TestListPulls_StateFiltering(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)

	pushBranch(t, origin, "feature/alpha", "alpha.txt", "alpha\n")
	pushBranch(t, origin, "feature/beta", "beta.txt", "beta\n")
	numAlpha := openPR(t, gh, "alpha", "feature/alpha", "main", "")
	numBeta := openPR(t, gh, "beta", "feature/beta", "main", "")
	mergePR(t, gh, numAlpha)

	if got := listPulls(t, gh, ""); len(got) != 1 || got[0].Number != numBeta {
		t.Fatalf("absent state = %+v, want exactly the OPEN PR %d (GitHub's own default)", got, numBeta)
	}
	if got := listPulls(t, gh, "?state=open"); len(got) != 1 || got[0].Number != numBeta {
		t.Fatalf("state=open = %+v, want exactly PR %d", got, numBeta)
	}
	if got := listPulls(t, gh, "?state=closed"); len(got) != 1 || got[0].Number != numAlpha {
		t.Fatalf("state=closed = %+v, want exactly the MERGED PR %d", got, numAlpha)
	}
	if got := listPulls(t, gh, "?state=all"); len(got) != 2 {
		t.Fatalf("state=all = %+v, want both PRs", got)
	}
}

// TestMergeCommitSHA_DoesNotDriftWhenALaterPRMergesOntoTheSameBase pins the
// PR struct's own MergeCommitSHA field: it is captured ONCE at this PR's own
// merge time, not re-resolved from the base's CURRENT tip on every read — a
// later merge onto the same base must not change an earlier PR's answer.
func TestMergeCommitSHA_DoesNotDriftWhenALaterPRMergesOntoTheSameBase(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)

	pushBranch(t, origin, "feature/one", "one.txt", "one\n")
	pushBranch(t, origin, "feature/two", "two.txt", "two\n")
	numOne := openPR(t, gh, "one", "feature/one", "main", "")
	numTwo := openPR(t, gh, "two", "feature/two", "main", "")

	mergePR(t, gh, numOne)
	tipAfterOne := runGit(t, origin, "rev-parse", "refs/heads/main")

	mergePR(t, gh, numTwo)
	tipAfterTwo := runGit(t, origin, "rev-parse", "refs/heads/main")

	if tipAfterOne == tipAfterTwo {
		t.Fatal("test fixture bug: base did not move on the second merge")
	}

	one := getPull(t, gh, numOne)
	if one.MergeCommitSHA != tipAfterOne {
		t.Errorf("PR %d merge_commit_sha = %q, want %q (its OWN merge, unaffected by PR %d's later merge)",
			numOne, one.MergeCommitSHA, tipAfterOne, numTwo)
	}
	two := getPull(t, gh, numTwo)
	if two.MergeCommitSHA != tipAfterTwo {
		t.Errorf("PR %d merge_commit_sha = %q, want %q", numTwo, two.MergeCommitSHA, tipAfterTwo)
	}
}

// TestCrossForkHeadRefIsBareBranchWhilePRHeadKeepsOwnerPrefix pins the one
// distinction real GitHub draws that a naive builder would collapse: the
// PR's OWN Head (what the idempotency filter matches on) keeps the
// `owner:branch` form, but the wire payload's `head.ref` is the bare branch.
func TestCrossForkHeadRefIsBareBranchWhilePRHeadKeepsOwnerPrefix(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)

	forkURL := fmt.Sprintf("%s/repos/%s/%s/forks", gh.URL, testOwner, testRepo)
	if status, raw := httpJSON(t, http.MethodPost, forkURL, nil, nil); status != http.StatusOK {
		t.Fatalf("create fork: status %d: %s", status, raw)
	}
	forkDir := gh.ForkDir(ForkLogin)
	pushBranch(t, forkDir, "feature/gamma", "gamma.txt", "gamma\n")
	number := openPR(t, gh, "gamma", ForkLogin+":feature/gamma", "main", "")

	prs := gh.PRs()
	if len(prs) != 1 || prs[0].Head != ForkLogin+":feature/gamma" {
		t.Fatalf("PRs() = %+v, want recorded Head %q", prs, ForkLogin+":feature/gamma")
	}

	got := getPull(t, gh, number)
	if got.Head.Ref != "feature/gamma" {
		t.Errorf("head.ref = %q, want the bare branch %q (real GitHub never reports the fork owner prefix there)", got.Head.Ref, "feature/gamma")
	}
}

// TestDeleteBranchOnMerge_PrunesSameRepoHeadByDefault pins the default this
// wave introduced: a real space runs `delete_branch_on_merge: true`
// (internal/livee2e's RepoSettingsBody), so the fake must too, or every
// scenario after a first merge fails for a reason production does not have
// (ProbeContractPublicationHeads' plan-digest recheck over a stale branch
// being the exact case that surfaced this).
func TestDeleteBranchOnMerge_PrunesSameRepoHeadByDefault(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)
	pushBranch(t, origin, "feature/alpha", "alpha.txt", "alpha\n")
	number := openPR(t, gh, "alpha", "feature/alpha", "main", "")

	if names := listBranchNames(t, gh, ""); !containsName(names, "feature/alpha") {
		t.Fatalf("branches before merge = %v, want feature/alpha present", names)
	}

	mergePR(t, gh, number)

	if names := listBranchNames(t, gh, ""); containsName(names, "feature/alpha") {
		t.Fatalf("branches after merge = %v, still names the merged head (DeleteBranchOnMerge defaults true)", names)
	}
}

// TestHeadSHASurvivesPruneWhileBranchGenuinelyDisappears names the exact
// property this wave's fix depends on, directly: a merged, pruned PR's
// head.sha does NOT go empty (host.CheckStatus/prHeadSHA and
// internal/livee2e's WaitForRequiredCheck both read it for an
// already-merged PR — AutoMerge defaults true, so every one of them would
// resolve nothing without this), while the branch it names is, at the same
// time, genuinely gone from the branch listing (the whole point of
// DeleteBranchOnMerge existing at all). Both facts must hold together, not
// just one or the other.
func TestHeadSHASurvivesPruneWhileBranchGenuinelyDisappears(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin) // AutoMerge and DeleteBranchOnMerge both default true

	sha := pushBranch(t, origin, "feature/alpha", "alpha.txt", "alpha\n")
	number := openPR(t, gh, "alpha", "feature/alpha", "main", "")
	mergePR(t, gh, number)

	got := getPull(t, gh, number)
	if got.Head.SHA != sha {
		t.Errorf("head.sha after merge+prune = %q, want the pre-merge SHA %q (real GitHub keeps reporting it)", got.Head.SHA, sha)
	}
	if names := listBranchNames(t, gh, ""); containsName(names, "feature/alpha") {
		t.Errorf("branches after merge = %v, still names feature/alpha — the branch must be genuinely gone", names)
	}
}

// TestDeleteBranchOnMerge_FalseLeavesHeadPresent pins that the knob works in
// BOTH directions: a test that wants the un-pruned shape can still have it.
func TestDeleteBranchOnMerge_FalseLeavesHeadPresent(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)
	gh.DeleteBranchOnMerge = false // set before any request reaches the server

	pushBranch(t, origin, "feature/alpha", "alpha.txt", "alpha\n")
	number := openPR(t, gh, "alpha", "feature/alpha", "main", "")
	mergePR(t, gh, number)

	if names := listBranchNames(t, gh, ""); !containsName(names, "feature/alpha") {
		t.Fatalf("branches after merge = %v, want feature/alpha still present (DeleteBranchOnMerge=false)", names)
	}
}

// TestDeleteBranchOnMerge_LeavesForkBranchAloneAndKeepsHeadSHAResolvable
// pins the fork carve-out: a cross-fork PR's head lives in the FORK, not the
// origin, and real GitHub never deletes a fork's own branch on merge. It
// also pins that the refs/fork/<branch> copy mergeByNumber fetches into the
// origin for SHA resolution survives pruning — it is not the head branch.
func TestDeleteBranchOnMerge_LeavesForkBranchAloneAndKeepsHeadSHAResolvable(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin) // DeleteBranchOnMerge defaults true

	forkURL := fmt.Sprintf("%s/repos/%s/%s/forks", gh.URL, testOwner, testRepo)
	if status, raw := httpJSON(t, http.MethodPost, forkURL, nil, nil); status != http.StatusOK {
		t.Fatalf("create fork: status %d: %s", status, raw)
	}
	forkDir := gh.ForkDir(ForkLogin)
	forkSHA := pushBranch(t, forkDir, "feature/gamma", "gamma.txt", "gamma\n")
	number := openPR(t, gh, "gamma", ForkLogin+":feature/gamma", "main", "")

	mergePR(t, gh, number)

	// The fork's own branch must survive — pruning is same-repo only.
	if got := runGit(t, forkDir, "rev-parse", "refs/heads/feature/gamma"); got != forkSHA {
		t.Fatalf("fork branch after merge = %q, want untouched at %q", got, forkSHA)
	}

	// The SHA resolution path (refs/fork/<branch> inside the origin) must
	// still answer — pruning must never have touched that ref.
	got := getPull(t, gh, number)
	if got.Head.SHA != forkSHA {
		t.Fatalf("head.sha after merge = %q, want %q (refs/fork/<branch> must survive pruning)", got.Head.SHA, forkSHA)
	}
}

// TestRepoSettings_ReportsDeleteBranchOnMerge pins that GET /repos/{o}/{n}
// answers the flag alongside the other allow_* settings, in both directions.
func TestRepoSettings_ReportsDeleteBranchOnMerge(t *testing.T) {
	t.Parallel()

	t.Run("default true", func(t *testing.T) {
		t.Parallel()
		gh := New(t, t.TempDir())
		var settings struct {
			DeleteBranchOnMerge bool `json:"delete_branch_on_merge"`
		}
		url := fmt.Sprintf("%s/repos/%s/%s", gh.URL, testOwner, testRepo)
		status, raw := httpJSON(t, http.MethodGet, url, nil, &settings)
		if status != http.StatusOK {
			t.Fatalf("GET /repos/%s/%s: status %d: %s", testOwner, testRepo, status, raw)
		}
		if !settings.DeleteBranchOnMerge {
			t.Fatalf("delete_branch_on_merge = %v, want true (the default)", settings.DeleteBranchOnMerge)
		}
	})

	t.Run("driven false", func(t *testing.T) {
		t.Parallel()
		gh := New(t, t.TempDir())
		gh.DeleteBranchOnMerge = false
		var settings struct {
			DeleteBranchOnMerge bool `json:"delete_branch_on_merge"`
		}
		url := fmt.Sprintf("%s/repos/%s/%s", gh.URL, testOwner, testRepo)
		status, raw := httpJSON(t, http.MethodGet, url, nil, &settings)
		if status != http.StatusOK {
			t.Fatalf("GET /repos/%s/%s: status %d: %s", testOwner, testRepo, status, raw)
		}
		if settings.DeleteBranchOnMerge {
			t.Fatalf("delete_branch_on_merge = %v, want the driven false", settings.DeleteBranchOnMerge)
		}
	})
}

// TestBranchListingReflectsRealRefsAndDeleteRef pins Gap 3.1 + 3.2 together:
// GET .../branches reads REAL git, and a branch removed through DELETE
// .../git/refs/heads/<name> stops appearing.
func TestBranchListingReflectsRealRefsAndDeleteRef(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)
	pushBranch(t, origin, "feature/x", "x.txt", "x\n")

	names := listBranchNames(t, gh, "")
	if !containsName(names, "main") || !containsName(names, "feature/x") {
		t.Fatalf("branches = %v, want main and feature/x", names)
	}

	if status := deleteRef(t, gh, "heads/feature/x"); status != http.StatusNoContent {
		t.Fatalf("delete heads/feature/x: status %d, want 204", status)
	}

	names = listBranchNames(t, gh, "")
	if containsName(names, "feature/x") {
		t.Fatalf("branches after delete = %v, still names feature/x", names)
	}

	// Same pagination tolerance as the pulls listing.
	if page2 := listBranchNames(t, gh, "?per_page=100&page=2"); len(page2) != 0 {
		t.Errorf("branches page=2 = %v, want empty", page2)
	}
}

// TestDeleteRef_MultiSegmentBranchNameAndAbsentRef pins that a ref whose
// branch name itself contains slashes (this project's own branch shape,
// e.g. a2a/<system>/submit/<id>) deletes correctly, and that deleting an
// absent ref answers 404 — the status ghClient.DeleteRef (internal/livee2e)
// depends on to treat "already gone" as a no-op rather than an error.
func TestDeleteRef_MultiSegmentBranchNameAndAbsentRef(t *testing.T) {
	t.Parallel()
	origin := newSeededOrigin(t)
	gh := New(t, origin)
	pushBranch(t, origin, "a2a/alpha/submit/XQ-1", "xq1.txt", "xq1\n")

	if status := deleteRef(t, gh, "heads/a2a/alpha/submit/XQ-1"); status != http.StatusNoContent {
		t.Fatalf("delete multi-segment ref: status %d, want 204", status)
	}
	names := listBranchNames(t, gh, "")
	if containsName(names, "a2a/alpha/submit/XQ-1") {
		t.Fatalf("branches after delete = %v, still names the deleted branch", names)
	}

	if status := deleteRef(t, gh, "heads/does-not-exist"); status != http.StatusNotFound {
		t.Fatalf("delete absent ref: status %d, want 404", status)
	}
}

// TestCodeownersErrorsUsersAndRateLimitEndpoints pins Gap 3.3: the three
// doctor-facing read endpoints answer the shape their internal/host callers
// decode (internal/host/github.go's CodeownersErrors/TokenScopes,
// internal/host/avatar.go's FetchAvatar).
//
// The decode shapes below (the anonymous `payload`/`user` structs, the raw
// map for /rate_limit) are DELIBERATELY MIRRORED BY HAND from those three
// internal/host functions rather than imported from internal/host itself:
// testkit packages are depended on by internal/* today, never the other way
// around, and this file is the wrong place to be the first crack in that
// direction. The cost of that choice is real and belongs on the record: if
// CodeownersErrors, TokenScopes, or FetchAvatar's own decode struct changes
// shape in internal/host/github.go or internal/host/avatar.go, these three
// subtests will NOT catch the drift — they only prove this fake's response
// matches what was true here on the day this file was written, not what
// internal/host currently expects. A change to any of those three functions'
// wire shape should re-verify this file by hand.
func TestCodeownersErrorsUsersAndRateLimitEndpoints(t *testing.T) {
	t.Parallel()
	gh := New(t, t.TempDir())

	t.Run("codeowners errors", func(t *testing.T) {
		t.Parallel()
		var payload struct {
			Errors []struct {
				Line       int    `json:"line"`
				Kind       string `json:"kind"`
				Message    string `json:"message"`
				Suggestion string `json:"suggestion"`
				Path       string `json:"path"`
			} `json:"errors"`
		}
		url := fmt.Sprintf("%s/repos/%s/%s/codeowners/errors", gh.URL, testOwner, testRepo)
		status, raw := httpJSON(t, http.MethodGet, url, nil, &payload)
		if status != http.StatusOK {
			t.Fatalf("codeowners/errors: status %d: %s", status, raw)
		}
		if len(payload.Errors) != 0 {
			t.Fatalf("codeowners/errors = %+v, want empty (no CODEOWNERS problem modelled)", payload.Errors)
		}
	})

	t.Run("rate_limit reports no scopes header for TokenScopes' reported=false path", func(t *testing.T) {
		t.Parallel()
		req, err := http.NewRequest(http.MethodGet, gh.URL+"/rate_limit", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /rate_limit: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /rate_limit: status %d", resp.StatusCode)
		}
		if _, ok := resp.Header["X-Oauth-Scopes"]; ok {
			t.Fatalf("X-OAuth-Scopes present, want absent — TokenScopes reads its absence as reported=false (fine-grained PAT), the faithful default here")
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode /rate_limit body: %v", err)
		}
		if _, ok := body["resources"]; !ok {
			t.Fatalf("/rate_limit body = %+v, want a resources object (well-formed rate-limit shape)", body)
		}
	})

	t.Run("users login", func(t *testing.T) {
		t.Parallel()
		var user struct {
			Login     string `json:"login"`
			AvatarURL string `json:"avatar_url"`
		}
		status, raw := httpJSON(t, http.MethodGet, gh.URL+"/users/octocat", nil, &user)
		if status != http.StatusOK {
			t.Fatalf("GET /users/octocat: status %d: %s", status, raw)
		}
		if user.Login != "octocat" {
			t.Errorf("login = %q, want octocat", user.Login)
		}
		if user.AvatarURL != "" {
			t.Errorf("avatar_url = %q, want empty (no avatar source modelled)", user.AvatarURL)
		}
	})
}

// TestForbiddenEndpointStill404s is a REPRESENTATIVE check, not exhaustive
// coverage of route's own forbidden-endpoint comment: PATCH /repos/{o}/{n}
// (repo settings) must keep 404ing. That 404 is the point — a fake that
// answered would let a provider-only scenario (repo settings changes only a
// real GitHub can validate) pass against a fabricated verdict, which reads
// as MORE complete than not running the scenario at all. See route's own
// forbidden-endpoint comment for the full list this generalizes from.
func TestForbiddenEndpointStill404s(t *testing.T) {
	t.Parallel()
	gh := New(t, t.TempDir())

	url := fmt.Sprintf("%s/repos/%s/%s", gh.URL, testOwner, testRepo)
	status, _ := httpJSON(t, http.MethodPatch, url, map[string]any{"allow_auto_merge": true}, nil)
	if status != http.StatusNotFound {
		t.Fatalf("PATCH /repos/%s/%s = %d, want 404 (repo settings are a provider-only decision this fake must refuse, not fabricate)", testOwner, testRepo, status)
	}
}
