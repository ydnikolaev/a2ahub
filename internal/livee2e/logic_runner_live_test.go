//go:build livee2e

package livee2e

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestLogicMatrix is the logic tier's ONLY entry point (plan D-5: the tier
// is chosen by WHICH TEST RUNS, never by an env var a mis-set shell could
// flip). It never calls LoadConfig and never reads an A2A_LIVE_E2E_*
// variable — newLogicHarness stands the space up entirely on a filesystem
// path served by testkit/fakegithub, so this function literally cannot
// construct a live Config.
//
// Scope, deliberately narrow (this wave's own stop-condition — "a
// two-checkout space stands up locally and one artifact round-trips"):
// drive ONE artifact all the way round — draft+submit an announcement from
// checkout A, confirm the fake opened and merged exactly one PR on the
// funnel's own deterministic branch, `a2a sync` from checkout B, and read
// the artifact back through a real read verb (`a2a inbox`). Scenario
// FAMILIES, the catalogue, the report renderer and the evidence bundle are
// the next wave's scope and are deliberately NOT wired in here.
func TestLogicMatrix(t *testing.T) {
	start := time.Now()
	ctx := t.Context()

	h, cleanup, err := newLogicHarness(ctx, t)
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Errorf("logic harness cleanup failed: %v", cleanupErr)
		}
	}()
	if err != nil {
		t.Fatalf("newLogicHarness: %v", err)
	}

	// Asserted before anything else: a logic run must never be able to
	// reach a real API root (plan D-5's structural property
	// TestLiveTierIsNotAMergeGate depends on downstream).
	if err := h.Seam.Validate(); err != nil {
		t.Fatalf("logic harness seam did not validate: %v", err)
	}
	if h.Seam.IsRealGitHub() {
		t.Fatalf("logic harness seam reports real GitHub — refusing to drive a write against it: %+v", h.Seam)
	}

	sub, err := h.DraftAndSubmit(ctx, h.A, "announcement")
	if err != nil {
		t.Fatalf("draft+submit an announcement from checkout A: %v", err)
	}

	pr, err := h.pullForBranch(ctx, sub.Branch)
	if err != nil {
		t.Fatalf("resolve the PR the funnel opened on %s: %v", sub.Branch, err)
	}
	if !pr.Merged {
		t.Fatalf("PR #%d on %s did not merge: %+v", pr.Number, sub.Branch, pr)
	}
	if n, err := h.countPRsForBranch(ctx, sub.Branch); err != nil {
		t.Fatalf("count PRs on %s: %v", sub.Branch, err)
	} else if n != 1 {
		t.Fatalf("PRs opened on %s = %d, want exactly 1", sub.Branch, n)
	}

	if _, stderr, err := h.B.Run(ctx, "sync"); err != nil {
		t.Fatalf("a2a sync (checkout B): %v: %s", err, stderr)
	}
	stdout, stderr, err := h.B.Run(ctx, "inbox")
	if err != nil {
		t.Fatalf("a2a inbox (checkout B): %v: %s", err, stderr)
	}
	if !strings.Contains(stdout, sub.ID) {
		t.Fatalf("inbox does not carry %s after sync:\n%s", sub.ID, stdout)
	}

	t.Logf("TestLogicMatrix: one artifact round trip (draft -> submit -> merge -> sync -> read) in %s", time.Since(start))
}

// snapshotDirNames lists the top-level entry names under dir, or an empty
// set when dir does not exist yet — the normal state on a machine that has
// never run a2a. It reads via os.UserHomeDir()-rooted paths ONLY, never a
// checkout's own (isolated) Home: this is the check on the REAL machine
// state a hermetic tier must never touch.
func snapshotDirNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}
		}
		t.Fatalf("read %s: %v", dir, err)
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out
}

// newDirNames returns the names present in after but not before, sorted.
func newDirNames(before, after map[string]bool) []string {
	var out []string
	for name := range after {
		if !before[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// TestLogicTierWritesNothingOutsideItsOwnTempDirs is the gate that makes the
// logic tier's hermeticity a property that is CHECKED, not one this wave's
// author fixed once and trusted to stay fixed.
//
// It exists because it very nearly wasn't written: five consecutive runs of
// this package's early tagged tests each left a directory in this
// operator's REAL `~/.cache/a2a/mirrors` — the same cache a real live-e2e
// run's mirror lives in — and the gap was noticed only by ACCIDENT, when two
// of those five runs happened to collide on the same cache key and one of
// them failed loudly. A run that did NOT collide would have polluted the
// operator's machine and reported a clean pass; that is the actual failure
// mode a hermeticity property defends against, and it is silent by
// construction unless something asserts it. The root cause (an ambient
// mirror_root machine config a logic checkout has no business reading) is
// fixed in newLogicHarness (each checkout gets its own isolated HOME,
// workspace_live.go's checkout.Home) — this test is what keeps that fix
// from being one refactor away from quietly regressing.
//
// Deliberately drives a REAL write (draft+submit+sync), not just
// construction: a hermeticity gap in the write path (a submit, a sync) is
// exactly as real as one in newLogicHarness itself, and a construction-only
// check would miss it.
func TestLogicTierWritesNothingOutsideItsOwnTempDirs(t *testing.T) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}
	mirrorsDir := filepath.Join(realHome, ".cache", "a2a", "mirrors")
	configDir := filepath.Join(realHome, ".config", "a2a")

	beforeMirrors := snapshotDirNames(t, mirrorsDir)
	beforeConfig := snapshotDirNames(t, configDir)

	ctx := t.Context()
	h, cleanup, err := newLogicHarness(ctx, t)
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Errorf("logic harness cleanup failed: %v", cleanupErr)
		}
	}()
	if err != nil {
		t.Fatalf("newLogicHarness: %v", err)
	}

	if _, err := h.DraftAndSubmit(ctx, h.A, "announcement"); err != nil {
		t.Fatalf("draft+submit an announcement from checkout A: %v", err)
	}
	if _, stderr, err := h.B.Run(ctx, "sync"); err != nil {
		t.Fatalf("a2a sync (checkout B): %v: %s", err, stderr)
	}

	if newNames := newDirNames(beforeMirrors, snapshotDirNames(t, mirrorsDir)); len(newNames) > 0 {
		t.Errorf("logic run wrote into the operator's real mirror cache %s: new entries %v", mirrorsDir, newNames)
	}
	if newNames := newDirNames(beforeConfig, snapshotDirNames(t, configDir)); len(newNames) > 0 {
		t.Errorf("logic run wrote into the operator's real machine config dir %s: new entries %v", configDir, newNames)
	}
}
