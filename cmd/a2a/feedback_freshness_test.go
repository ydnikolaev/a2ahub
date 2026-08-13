package main

// feedback_freshness_test.go tests resolveFeedbackFreshness directly —
// offline, against LOCAL DIRECTORY git repos standing in for the hub of
// record, mirroring docs/runbooks/feedback-sync.sh's own --teeth idiom (own-
// loop 09 §8 AC3, §11 wave D2). runGitFixture (wire_tier_test.go, same
// package) is reused rather than redefined — one hardened-git-invocation
// helper per package (testkit/gitfixture's own convention).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/feedback"
)

// seedFeedbackFixtureRepo git-inits dir on the HUB branch with
// feedback/inbox/.gitkeep plus one committed feedback/inbox/<id>.yaml per
// id in ids (content is irrelevant to presence/absence — deliberately
// minimal), and returns dir.
func seedFeedbackFixtureRepo(t *testing.T, dir string, ids ...string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "feedback", "inbox"), 0o755); err != nil {
		t.Fatalf("mkdir feedback/inbox: %v", err)
	}
	// The fixture is seeded on feedbackBaseBranch, not "main", because that is
	// the branch production fetches. Seeding "main" while the code fetches the
	// hub branch is a fixture that agrees with the test's author rather than
	// with the system: these three tests went red on exactly that when P15 M2
	// repointed the fetch, which is the fixture doing its job.
	runGitFixture(t, dir, "init", "-q", "-b", feedbackBaseBranch)
	if err := os.WriteFile(filepath.Join(dir, "feedback", "inbox", ".gitkeep"), nil, 0o644); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}
	for i, id := range ids {
		content := "feedback: v1\nid: " + id + "\nstatus: new\nseed: " + string(rune('a'+i)) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "feedback", "inbox", id+".yaml"), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}
	runGitFixture(t, dir, "add", "-A")
	runGitFixture(t, dir, "commit", "-q", "-m", "seed")
	return dir
}

// TestResolveFeedbackFreshness_NotApplicableWhenNotAGitWorkTree is AC3's own
// escape hatch: a hubRoot that is not a git work tree at all yields
// not-applicable, never a refusal.
func TestResolveFeedbackFreshness_NotApplicableWhenNotAGitWorkTree(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir() // deliberately no `git init`

	got := resolveFeedbackFreshness(context.Background(), hubRoot, "https://example.invalid/hub")
	if got.Status != feedback.FreshnessNotApplicable {
		t.Fatalf("Status = %v, want FreshnessNotApplicable; got %+v", got.Status, got)
	}
}

// TestResolveFeedbackFreshness_CurrentWhenLocalHasEveryHubRecord is the
// clean-agreement path: every feedback/inbox/fb-*.yaml the hub carries is
// also present at this tree's HEAD (even with different bytes — §11 wave
// D2's correction: presence/absence only, content drift is
// feedback-sync.sh's business).
func TestResolveFeedbackFreshness_CurrentWhenLocalHasEveryHubRecord(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hubDir := seedFeedbackFixtureRepo(t, filepath.Join(root, "hub"), "fb-20260801-aaaaaa")
	localDir := seedFeedbackFixtureRepo(t, filepath.Join(root, "local"), "fb-20260801-aaaaaa")
	// Prove content differences do not matter: overwrite and commit a
	// differing body for the SAME id locally after the shared seed.
	if err := os.WriteFile(filepath.Join(localDir, "feedback", "inbox", "fb-20260801-aaaaaa.yaml"), []byte("feedback: v1\nid: fb-20260801-aaaaaa\nstatus: accepted\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	runGitFixture(t, localDir, "add", "-A")
	runGitFixture(t, localDir, "commit", "-q", "-m", "triage verdict")

	got := resolveFeedbackFreshness(context.Background(), localDir, hubDir)
	if got.Status != feedback.FreshnessCurrent {
		t.Fatalf("Status = %v, want FreshnessCurrent; got %+v", got.Status, got)
	}
}

// TestResolveFeedbackFreshness_IgnoresOffGrammarHubPaths proves the
// resolver filters to the fb-*.yaml grammar: a hub-only path outside it
// (the .gitkeep sentinel, or a stray file) must not itself trigger a
// refusal — that classification is feedback-sync.sh's job, not triage's.
func TestResolveFeedbackFreshness_IgnoresOffGrammarHubPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hubDir := seedFeedbackFixtureRepo(t, filepath.Join(root, "hub"))
	if err := os.WriteFile(filepath.Join(hubDir, "feedback", "inbox", "README.md"), []byte("not a record\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitFixture(t, hubDir, "add", "-A")
	runGitFixture(t, hubDir, "commit", "-q", "-m", "off-grammar path")
	localDir := seedFeedbackFixtureRepo(t, filepath.Join(root, "local"))

	got := resolveFeedbackFreshness(context.Background(), localDir, hubDir)
	if got.Status != feedback.FreshnessCurrent {
		t.Fatalf("Status = %v, want FreshnessCurrent (off-grammar hub path ignored); got %+v", got.Status, got)
	}
}

// TestResolveFeedbackFreshness_RefusesOnHubOnlyRecord is AC3 case (a),
// content-level per §11 wave D2's correction: the hub's default branch
// carries a feedback/inbox/fb-*.yaml record this tree lacks at HEAD.
func TestResolveFeedbackFreshness_RefusesOnHubOnlyRecord(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hubDir := seedFeedbackFixtureRepo(t, filepath.Join(root, "hub"), "fb-20260801-aaaaaa", "fb-20260808-cccccc")
	localDir := seedFeedbackFixtureRepo(t, filepath.Join(root, "local"), "fb-20260801-aaaaaa")

	got := resolveFeedbackFreshness(context.Background(), localDir, hubDir)
	if got.Status != feedback.FreshnessRefused {
		t.Fatalf("Status = %v, want FreshnessRefused; got %+v", got.Status, got)
	}
	if !strings.Contains(got.Reason, "fb-20260808-cccccc") {
		t.Errorf("Reason = %q, want it to name the hub-only record", got.Reason)
	}
	if got.HubOfRecord != hubDir {
		t.Errorf("HubOfRecord = %q, want %q", got.HubOfRecord, hubDir)
	}
}

// TestResolveFeedbackFreshness_RefusesWhenHubUnreachable is AC3 case (b):
// the comparison cannot be completed because the hub cannot be fetched at
// all (an unreachable remote — folds in "fetch error" and "missing branch",
// which git reports identically to an unreachable remote via a non-zero
// `git fetch` exit).
func TestResolveFeedbackFreshness_RefusesWhenHubUnreachable(t *testing.T) {
	t.Parallel()
	localDir := seedFeedbackFixtureRepo(t, filepath.Join(t.TempDir(), "local"))
	unreachableHub := filepath.Join(t.TempDir(), "does-not-exist")

	got := resolveFeedbackFreshness(context.Background(), localDir, unreachableHub)
	if got.Status != feedback.FreshnessRefused {
		t.Fatalf("Status = %v, want FreshnessRefused; got %+v", got.Status, got)
	}
	// The exact git stderr fragment for a fetch against a path that is not a
	// git repository at all — pins this to the FETCH failure specifically,
	// not to some other refusal reachable further down the same function
	// (e.g. the ls-tree-against-FETCH_HEAD failure that would follow a
	// SILENTLY IGNORED fetch error).
	if !strings.Contains(got.Reason, "does not appear to be a git repository") {
		t.Errorf("Reason = %q, want it to name the git fetch failure", got.Reason)
	}
	if got.HubOfRecord != unreachableHub {
		t.Errorf("HubOfRecord = %q, want %q", got.HubOfRecord, unreachableHub)
	}
}

// TestResolveFeedbackFreshness_RefusesWhenNoHubConfigured is AC3 case (c):
// no hub-of-record remote is configured — the resolver's hubURL argument is
// empty. Structurally unreachable through runFeedback today (its caller
// always falls back to canonicalFeedbackRepo), so this exercises the
// resolver directly rather than through the wiring (§11 wave D2: recorded
// as untested-from-production rather than counted as exercised).
func TestResolveFeedbackFreshness_RefusesWhenNoHubConfigured(t *testing.T) {
	t.Parallel()
	localDir := seedFeedbackFixtureRepo(t, filepath.Join(t.TempDir(), "local"))

	got := resolveFeedbackFreshness(context.Background(), localDir, "")
	if got.Status != feedback.FreshnessRefused {
		t.Fatalf("Status = %v, want FreshnessRefused; got %+v", got.Status, got)
	}
	if !strings.Contains(got.Reason, "no hub-of-record remote is configured") {
		t.Errorf("Reason = %q, want it to say no hub-of-record remote is configured", got.Reason)
	}
}

// TestResolveFeedbackFreshness_RefusesATransportHelperHub is a SECURITY
// assertion, not a validation nicety. `git fetch` treats `<helper>::<arg>` as
// a transport helper and executes it — `ext::sh -c whoami` is the canonical
// demonstration — and hubURL reaches this resolver straight from
// A2A_FEEDBACK_HUB_URL. Without the guard, an environment variable is an
// arbitrary-command channel into a verb agents run unattended.
//
// It is also what makes this file's G702 entry in .golangci.yml honest:
// gosec's taint pass cannot see the guard, so the suppression rests on this
// test existing and failing when the guard does not.
func TestResolveFeedbackFreshness_RefusesATransportHelperHub(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	seedFeedbackFixtureRepo(t, root)

	for _, hub := range []string{
		"ext::sh -c whoami",
		"ext::git-upload-pack %S /tmp/evil",
	} {
		got := resolveFeedbackFreshness(context.Background(), root, hub)
		if got.Status != feedback.FreshnessRefused {
			t.Fatalf("resolveFeedbackFreshness(%q).Status = %v, want FreshnessRefused — a transport-helper hub must never reach `git fetch`", hub, got.Status)
		}
		if !strings.Contains(got.Reason, "transport-helper") {
			t.Fatalf("resolveFeedbackFreshness(%q).Reason = %q, want it to name the transport-helper syntax as the reason", hub, got.Reason)
		}
	}

	// A shape that is neither a URL nor an absolute path is refused too, and
	// for a stated reason rather than by falling through to git.
	got := resolveFeedbackFreshness(context.Background(), root, "not-a-url")
	if got.Status != feedback.FreshnessRefused || !strings.Contains(got.Reason, "not a fetchable location") {
		t.Fatalf("resolveFeedbackFreshness(\"not-a-url\") = %+v, want a refusal naming an unfetchable location", got)
	}
}
