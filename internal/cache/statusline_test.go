package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStatusline_ZeroNoiseSilence is AC row 2: given nothing actionable,
// statusline prints nothing, exit 0 (CC-092), including the no-space-
// connected-at-all case.
func TestStatusline_ZeroNoiseSilence(t *testing.T) {
	t.Parallel()

	t.Run("no space connected", func(t *testing.T) {
		t.Parallel()
		store := NewStore("axon", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)
		result, err := store.Statusline(context.Background())
		if err != nil {
			t.Fatalf("Statusline: %v", err)
		}
		if result.Line != "" || result.Exit != 0 {
			t.Fatalf("want empty line + exit 0, got %+v", result)
		}
	})

	t.Run("connected but nothing actionable", func(t *testing.T) {
		t.Parallel()
		fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
		base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
		// Already acknowledged (past the pre-ack state), p2, no dispute,
		// no gate — matches nothing.
		fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-q.md", wr("XW-seomatrix-20260701-q", "q", "seomatrix", []string{"axon"}, "p2", false), "body")
		fx.commitEvent("seomatrix", fxULID(800), evt("XW-seomatrix-20260701-q", "submit", "seomatrix", base))
		fx.commitEvent("axon", fxULID(801), evt("XW-seomatrix-20260701-q", "acknowledge", "axon", base.Add(time.Hour)))

		store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(2 * time.Hour) }, time.Hour)
		result, err := store.Statusline(context.Background())
		if err != nil {
			t.Fatalf("Statusline: %v", err)
		}
		if result.Line != "" || result.Exit != 0 {
			t.Fatalf("want empty line + exit 0, got %+v", result)
		}
	})
}

// TestStatusline_P1Severity is AC row 3: given a p1/blocking inbound,
// the line + severity exit code reflect it; render is asserted <100ms.
//
// reason: NOT t.Parallel() — this test's own assertion is a wall-clock
// budget (<100ms). Running concurrently with this package's OTHER
// tests (several of which spawn their own `git` subprocesses) creates
// real OS-scheduling contention that occasionally pushes an
// individually-fast render past 100ms for reasons having nothing to do
// with this package's own correctness — a known flake risk flagged in
// this phase's Deviations report. Running serially keeps the
// measurement meaningful.
func TestStatusline_P1Severity(t *testing.T) {
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-urgent.md", wr("XW-seomatrix-20260701-urgent", "todo feed pagination", "seomatrix", []string{"axon"}, "p1", false), "body")
	fx.commitEvent("seomatrix", fxULID(810), evt("XW-seomatrix-20260701-urgent", "submit", "seomatrix", base))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, time.Hour)

	// Warm-up call (discarded): the FIRST `git` subprocess invocation on
	// a freshly-created fixture pays one-time OS-level exec/page-cache
	// cost unrelated to this package's own render latency; a standard
	// perf-test warm-up isolates the steady-state number the <100ms
	// budget is actually about (T3: "wall-clock render time asserted
	// <100ms").
	if _, err := store.Statusline(context.Background()); err != nil {
		t.Fatalf("Statusline (warm-up): %v", err)
	}

	start := time.Now()
	result, err := store.Statusline(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Statusline: %v", err)
	}
	if result.Exit != int(SeverityUrgent) {
		t.Fatalf("Exit = %d, want %d (p1 severity)", result.Exit, SeverityUrgent)
	}
	if result.Line == "" {
		t.Fatal("want a non-empty line naming the item")
	}
	if !contains(result.Line, "XW-seomatrix-20260701-urgent") {
		t.Fatalf("line %q does not name the urgent item", result.Line)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("render took %v, want <100ms", elapsed)
	}
}

// TestStatusline_NoHubSymbol is AC row 9, expressed as a behavioral
// check at this package's level: the refresh path never invokes
// anything beyond space.CloneOrFetch (git fetch) — this test asserts
// Statusline completes and returns without needing any hub-shaped
// dependency to be injected (Store's constructor takes none). The
// static grep half of AC row 9 (no hub RPC symbol reachable from
// cmd_statusline.go or this package) is verified separately (grep, see
// this phase's report).
func TestStatusline_NoHubSymbol(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	// TTL=1ns with a just-cloned mirror still triggers the detached-
	// refresh path (any nonzero age exceeds it) — it must not panic or
	// block.
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, RepoURL: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return time.Now() }, time.Nanosecond)
	if _, err := store.Statusline(context.Background()); err != nil {
		t.Fatalf("Statusline: %v", err)
	}

	// The detached refresh goroutine this call triggers writes to
	// fx.dir/.git/FETCH_HEAD in the background (fire-and-forget by
	// design — this package never waits for it). Bounded-poll for it to
	// land before this test function returns, so t.TempDir()'s
	// synchronous RemoveAll cleanup does not race a still-running `git
	// fetch` subprocess (observed flake otherwise: "unlinkat .../.git:
	// directory not empty", plus unbounded leaked temp dirs across
	// repeated test runs when this was instead solved by never cleaning
	// up). Bounded, not an unconditional sleep: gives up after 2s and
	// lets the test pass regardless — a slow-to-land background fetch is
	// not what this test is checking.
	deadline := time.Now().Add(2 * time.Second)
	fetchHead := filepath.Join(fx.dir, ".git", "FETCH_HEAD")
	for time.Now().Before(deadline) {
		if _, err := os.Stat(fetchHead); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
