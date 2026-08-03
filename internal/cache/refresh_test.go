package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ageMirror backdates dir's own sync-age markers (.git/FETCH_HEAD and/or
// .git/HEAD, whichever exist) to `past` before now — mirrorSyncAge's own
// two files (staleness.go) — so a test can force a real, already-cloned
// fixture mirror past a Store's TTL without waiting real wall-clock time
// or faking mirrorSyncAge's own file read.
func ageMirror(t *testing.T, dir string, past time.Duration) {
	t.Helper()
	old := time.Now().Add(-past)
	for _, rel := range []string{filepath.Join(".git", "FETCH_HEAD"), filepath.Join(".git", "HEAD")} {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("ageMirror: Chtimes %s: %v", p, err)
		}
	}
}

// TestSyncIfStale_RefreshRevealsNewlyPublishedArtifact is AC-1050.1: a
// stale mirror is refreshed and an artifact the counterparty published
// AFTER this side's last sync — pushed to the fixture ORIGIN from a
// second, wholly independent clone, never touching fx.dir directly —
// becomes visible on the next read once SyncIfStale runs.
func TestSyncIfStale_RefreshRevealsNewlyPublishedArtifact(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	cacheDir := t.TempDir()
	const freshID = "XW-seomatrix-20260701-fresh"

	store := NewStore("axon", cacheDir, []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, RepoURL: fx.dir, Manifest: mustManifest(t, fx)}}, time.Now, time.Hour)

	before, err := store.Inbox(context.Background(), false)
	if err != nil {
		t.Fatalf("Inbox (before): %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("Inbox (before) = %+v, want empty (nothing published yet)", before)
	}

	// Publish behind the store's own mirror's back: clone the same
	// fixture ORIGIN into a second, independent working dir and push
	// from there — fx.dir itself is never touched by this step.
	origin := filepath.Join(filepath.Dir(fx.dir), "origin.git")
	other := filepath.Join(t.TempDir(), "other-clone")
	fxRunGit(t, "", "clone", origin, other)
	second := &fixtureSpace{t: t, dir: other, year: fx.year}
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	second.commitArtifact("seomatrix/exchanges/"+freshID+".md",
		wr(freshID, "fresh work", "seomatrix", []string{"axon"}, "p2", false), "body")
	second.commitEvent("seomatrix", fxULID(900), evt(freshID, "submit", "seomatrix", base))
	if err := WriteMarker(cacheDir, "sp1", PendingMarker{ArtifactID: freshID, State: "pending-merge"}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}

	ageMirror(t, fx.dir, 2*time.Hour)

	if errs := store.SyncIfStale(context.Background()); len(errs) != 0 {
		t.Fatalf("SyncIfStale: %v", errs)
	}

	after, err := store.Inbox(context.Background(), false)
	if err != nil {
		t.Fatalf("Inbox (after): %v", err)
	}
	if len(after) != 1 || after[0].ID != freshID {
		t.Fatalf("Inbox (after) = %+v, want the newly-published artifact", after)
	}
	if _, err := ReadMarker(cacheDir, "sp1", freshID); !os.IsNotExist(err) {
		t.Fatalf("pending marker still exists after read refresh revealed canonical artifact: %v", err)
	}
}

// TestSyncIfStale_RevealsAMergeInsideTheStatuslineWindow is the BEHAVIOURAL
// regression for RN-0910-1, and it exists because the two tests either side of
// it could not fail on that defect.
//
// The one above ages its mirror by TWO HOURS. That is outside every window
// either version of this code ever used, so it passed before the fix and after
// it — vacuous for this defect specifically. And
// TestSyncIfStaleDoesNotInheritTheStatuslineTTL pins only the RELATIONSHIP
// between two constants: it would still pass if the read path stopped calling
// SyncIfStale altogether, or if the staleness check were dropped. Neither
// asserts the thing a user experiences.
//
// So this row aims at the ONE window where the defect lived: a mirror synced
// 60 seconds ago is INSIDE the statusline's five-minute window (which the read
// path used to share) and OUTSIDE readRefreshTTL (which it has of its own).
// The Store is therefore built with the real production DefaultStatuslineTTL
// rather than the hour these tests usually pass, because the whole point is
// what happens when the two windows disagree.
//
//	pre-fix:  60s < 5m  -> skipped as fresh -> the artifact stays invisible
//	post-fix: 60s > 30s -> refreshed        -> the artifact is there
//
// Which is exactly the shape found by hand against a live space on
// 2026-07-26: an artifact whose pull request had just merged was invisible to
// `outbox` and answered "artifact not found" from `show`, and one explicit
// `a2a sync` revealed it at once. Sixty seconds is not an edge case — it is
// how long a counterparty's merge takes, and the session-start loop every
// agent follows reads an empty inbox as permission to proceed.
//
// No sleeping and no wall-clock dependency: ageMirror backdates the sync
// markers, so the window is exact.
//
// It runs as a PAIR — the same fixture, the same merge, two ages either side of
// readRefreshTTL and opposite outcomes. The pair is what proves the visibility
// assertion is actually sensitive to the refresh: a test that only ever asserts
// "the artifact is visible" would also pass if the artifact were visible for
// some reason having nothing to do with fetching, and this whole file exists
// because of an assertion that agreed with the code about a situation neither
// would ever meet.
func TestSyncIfStale_RevealsAMergeInsideTheStatuslineWindow(t *testing.T) {
	t.Parallel()

	// Guard the premise rather than assume it. If the constants ever move so
	// that a minute no longer sits between them, these cases would keep
	// passing while asserting nothing about the defect — the same vacuity they
	// were written to replace.
	const insideTheOldWindow = time.Minute
	if insideTheOldWindow <= readRefreshTTL || insideTheOldWindow >= DefaultStatuslineTTL {
		t.Fatalf("this test's whole point is an age INSIDE the statusline window and OUTSIDE the read "+
			"window; %s no longer sits between readRefreshTTL (%s) and DefaultStatuslineTTL (%s), so it "+
			"would pass without exercising the defect", insideTheOldWindow, readRefreshTTL, DefaultStatuslineTTL)
	}
	const withinTheReadWindow = readRefreshTTL / 3

	const merged = "XW-seomatrix-20260701-justmerged"

	cases := []struct {
		name        string
		age         time.Duration
		wantVisible bool
	}{
		{
			// THE DEFECT. A minute old means recently fetched — by this side's
			// own `a2a submit`, in the live reproduction — so a window sized
			// for a shell prompt calls it fresh and the merge that happened
			// since stays invisible.
			name: "a minute old: fresh to the statusline, stale to a read verb — the merge must surface",
			age:  insideTheOldWindow, wantVisible: true,
		},
		{
			// The other direction, and it is not just symmetry: several read
			// verbs in one agent turn (inbox, then show, then thread) must not
			// each pay a fetch for an answer that cannot have changed between
			// them. It also proves the case above is decided by the refresh
			// and not by something incidental in the fixture.
			name: "inside the read window: not refetched, so the merge is NOT yet visible",
			age:  withinTheReadWindow, wantVisible: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
			store := NewStore("axon", t.TempDir(),
				[]SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, RepoURL: fx.dir, Manifest: mustManifest(t, fx)}},
				time.Now, DefaultStatuslineTTL)

			// The counterparty publishes and merges behind this mirror's back,
			// the same way the row above does it: a second independent clone of
			// the same origin, never touching fx.dir.
			origin := filepath.Join(filepath.Dir(fx.dir), "origin.git")
			other := filepath.Join(t.TempDir(), "other-clone")
			fxRunGit(t, "", "clone", origin, other)
			second := &fixtureSpace{t: t, dir: other, year: fx.year}
			base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
			second.commitArtifact("seomatrix/exchanges/"+merged+".md",
				wr(merged, "just merged", "seomatrix", []string{"axon"}, "p2", false), "body")
			second.commitEvent("seomatrix", fxULID(901), evt(merged, "submit", "seomatrix", base))

			ageMirror(t, fx.dir, tc.age)

			if errs := store.SyncIfStale(context.Background()); len(errs) != 0 {
				t.Fatalf("SyncIfStale: %v", errs)
			}

			items, err := store.Inbox(context.Background(), false)
			if err != nil {
				t.Fatalf("Inbox: %v", err)
			}
			visible := len(items) == 1 && items[0].ID == merged
			switch {
			case tc.wantVisible && !visible:
				t.Fatalf("Inbox = %+v, want the just-merged artifact.\n"+
					"A mirror synced %s ago was treated as fresh, so a merge that happened since is "+
					"invisible — this is RN-0910-1 exactly, and the agent reading this inbox is told by "+
					"the documented session-start loop to proceed as if nothing arrived", items, tc.age)
			case !tc.wantVisible && visible:
				t.Fatalf("Inbox = %+v — a mirror synced %s ago was refetched, inside the %s read window. "+
					"Either the dedupe window is gone (a burst of read verbs now pays a fetch each) or "+
					"this artifact is visible for a reason unrelated to fetching, which would make the "+
					"case above vacuous", items, tc.age, readRefreshTTL)
			}

			if !tc.wantVisible {
				return
			}
			// `show` is named separately because it is the verb that answered
			// "artifact not found" in the live reproduction, and it resolves a
			// single ref rather than walking the index — a different code path
			// to the same mirror, and the one whose failure reads as "that
			// artifact does not exist" rather than as "nothing new".
			shown, err := store.Show(context.Background(), merged)
			if err != nil {
				t.Fatalf("Show after the refresh: %v — this is the \"artifact not found\" the live "+
					"space answered on v0.9.0", err)
			}
			if shown.ID != merged {
				t.Fatalf("Show = %+v, want the just-merged artifact", shown)
			}
		})
	}
}

// TestSyncIfStale_RefreshesManifestSoNewParticipantIsAuthorized covers a
// narrower instance of the exact same defect class SyncIfStale exists to
// kill: walkArtifacts/walkEvents (mirror.go) are generic tree walks, so
// a just-fetched artifact from a brand-new participant is found on disk
// regardless of the manifest — but internal/fold's own entry-transition
// authorization check is membership-gated (fold.go's authorized/
// legalRole), and membership is resolved from the Store's CACHED
// Manifest. Without also refreshing the cached manifest after a
// successful fetch, a participant who joins the space and publishes
// their first artifact in the SAME push would fold to an
// unauthorized-actor flag stuck at `draft` — visible in Inbox, but
// silently wrong, not the honest "submitted" state the counterparty
// actually wrote.
func TestSyncIfStale_RefreshesManifestSoNewParticipantIsAuthorized(t *testing.T) {
	t.Parallel()
	// Only axon is a participant when the Store's manifest is captured.
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, RepoURL: fx.dir, Manifest: mustManifest(t, fx)}}, time.Now, time.Hour)

	// Behind the store's own mirror's back: seomatrix joins AND publishes
	// its first artifact, via a second, independent clone of the same
	// origin — the manifest update and the artifact land in the same
	// commit (git add -A picks up both), the accompanying submit event in
	// the very next commit of the same push.
	origin := filepath.Join(filepath.Dir(fx.dir), "origin.git")
	other := filepath.Join(t.TempDir(), "other-clone")
	fxRunGit(t, "", "clone", origin, other)
	second := &fixtureSpace{t: t, dir: other, year: fx.year}
	second.writeManifest(fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	second.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-newmember.md",
		wr("XW-seomatrix-20260701-newmember", "new member's first contract", "seomatrix", []string{"axon"}, "p2", false), "body")
	second.commitEvent("seomatrix", fxULID(920), evt("XW-seomatrix-20260701-newmember", "submit", "seomatrix", base))

	ageMirror(t, fx.dir, 2*time.Hour)

	if errs := store.SyncIfStale(context.Background()); len(errs) != 0 {
		t.Fatalf("SyncIfStale: %v", errs)
	}

	// The Store's own cached manifest must now know about seomatrix.
	mirrors := store.SpaceMirrors()
	if len(mirrors) != 1 {
		t.Fatalf("SpaceMirrors() = %d entries, want 1", len(mirrors))
	}
	found := false
	for _, p := range mirrors[0].Manifest.Participants {
		if p.System == "seomatrix" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Manifest.Participants = %+v, want seomatrix present after SyncIfStale", mirrors[0].Manifest.Participants)
	}

	show, err := store.Show(context.Background(), "XW-seomatrix-20260701-newmember")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if show.State != "submitted" {
		t.Fatalf("State = %q, want %q (stale manifest would fold this to draft with an unauthorized-actor flag)", show.State, "submitted")
	}
	if len(show.Flags) != 0 {
		t.Fatalf("Flags = %v, want none", show.Flags)
	}
}

// TestSyncIfStale_CallerCtxCancellation_MidFetchLeavesMirrorReadable
// proves clause 5's claim about a timed-out fetch behaviorally, not just
// by code-path argument: SyncIfStale derives each mirror's own context
// from the CALLER's ctx (context.WithTimeout(ctx, perMirrorTimeout)), so
// a caller context that is already about to expire kills the real `git
// fetch` almost immediately — and the mirror is left exactly as usable
// as it was (its .git dir survives, and the pre-existing artifact is
// still readable).
func TestSyncIfStale_CallerCtxCancellation_MidFetchLeavesMirrorReadable(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-precancel.md",
		wr("XW-seomatrix-20260701-precancel", "pre-existing", "seomatrix", []string{"axon"}, "p2", false), "body")
	fx.commitEvent("seomatrix", fxULID(930), evt("XW-seomatrix-20260701-precancel", "submit", "seomatrix", base))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, RepoURL: fx.dir, Manifest: mustManifest(t, fx)}}, time.Now, time.Hour)
	ageMirror(t, fx.dir, 2*time.Hour)

	// A caller ctx that is already about to expire — the real, un-mocked
	// cloneOrFetch (space.CloneOrFetch) inherits this deadline via
	// SyncIfStale's own context.WithTimeout(ctx, perMirrorTimeout), so
	// the real `git fetch` subprocess is killed almost immediately.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	errs := store.SyncIfStale(ctx)
	if len(errs) != 1 {
		t.Fatalf("SyncIfStale errs = %v, want exactly 1 (the killed fetch)", errs)
	}
	if !strings.Contains(errs[0].Error(), "sp1") {
		t.Fatalf("error %v does not name the space", errs[0])
	}

	if _, err := os.Stat(filepath.Join(fx.dir, ".git")); err != nil {
		t.Fatalf(".git no longer present after the killed fetch: %v", err)
	}

	after, err := store.Inbox(context.Background(), false)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(after) != 1 || after[0].ID != "XW-seomatrix-20260701-precancel" {
		t.Fatalf("Inbox = %+v, want the pre-existing local artifact still readable", after)
	}
}

// TestSyncIfStale_FreshMirrorTriggersNoRefresh is AC-1050.2: a mirror
// whose sync-age is within the Store's TTL is left alone — zero calls
// into the injected refresh primitive.
func TestSyncIfStale_FreshMirrorTriggersNoRefresh(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, RepoURL: fx.dir, Manifest: mustManifest(t, fx)}}, time.Now, time.Hour)

	var calls int
	store.SetCloneOrFetchForTest(func(context.Context, string, string) error {
		calls++
		return nil
	})

	if errs := store.SyncIfStale(context.Background()); len(errs) != 0 {
		t.Fatalf("SyncIfStale: %v", errs)
	}
	if calls != 0 {
		t.Fatalf("cloneOrFetch called %d times, want 0 (mirror is fresh)", calls)
	}
}

// TestSyncIfStale_UnreachableOriginErrorsButMirrorStaysReadable is
// AC-1050.3: a mirror whose remote is unreachable returns a named error
// from SyncIfStale, and the read path still returns whatever was already
// local — a refresh failure is never fatal to a read.
func TestSyncIfStale_UnreachableOriginErrorsButMirrorStaysReadable(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-local.md",
		wr("XW-seomatrix-20260701-local", "local work", "seomatrix", []string{"axon"}, "p2", false), "body")
	fx.commitEvent("seomatrix", fxULID(910), evt("XW-seomatrix-20260701-local", "submit", "seomatrix", base))

	// Break the mirror's own git remote so a real `git fetch origin`
	// fails (space.CloneOrFetch's fetch step reads the configured
	// remote, not the SpaceMirror.RepoURL field, once dir is already a
	// clone — RepoURL is set to the same bogus value for consistency).
	bogus := filepath.Join(t.TempDir(), "does-not-exist.git")
	fxRunGit(t, fx.dir, "remote", "set-url", "origin", bogus)

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, RepoURL: bogus, Manifest: mustManifest(t, fx)}}, time.Now, time.Hour)

	ageMirror(t, fx.dir, 2*time.Hour)

	errs := store.SyncIfStale(context.Background())
	if len(errs) != 1 {
		t.Fatalf("SyncIfStale errs = %v, want exactly 1", errs)
	}
	if !strings.Contains(errs[0].Error(), "sp1") {
		t.Fatalf("error %v does not name the failing space", errs[0])
	}

	after, err := store.Inbox(context.Background(), false)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(after) != 1 || after[0].ID != "XW-seomatrix-20260701-local" {
		t.Fatalf("Inbox = %+v, want the pre-existing local artifact still readable", after)
	}
}

// TestSyncIfStale_SkipsMissingMirror_NeverFirstClones is the poisoning
// guard (clause 1): a mirror whose Dir does not exist at all is skipped
// silently — no error, and critically no clone attempt (a read verb must
// never be able to create/populate a mirror; that stays doctor/sync/
// connect's job).
func TestSyncIfStale_SkipsMissingMirror_NeverFirstClones(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "missing-mirror")
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: dir, RepoURL: "https://example.invalid/repo.git"}}, time.Now, time.Hour)

	var calls int
	store.SetCloneOrFetchForTest(func(context.Context, string, string) error {
		calls++
		return nil
	})

	if errs := store.SyncIfStale(context.Background()); len(errs) != 0 {
		t.Fatalf("SyncIfStale = %v, want no error (a missing mirror is not a sync failure)", errs)
	}
	if calls != 0 {
		t.Fatalf("cloneOrFetch called %d times, want 0 (never first-clone from a read verb)", calls)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir %s exists after SyncIfStale, want still absent", dir)
	}
}

// TestSyncIfStale_SkipsNonGitMirror_LeavesDirUntouched is the poisoning
// guard's second half: a dir that exists but is NOT a git repository
// (e.g. a clone that was killed mid-write by something else, or simply
// unrelated content) is skipped, and its contents are left byte-for-byte
// as they were.
func TestSyncIfStale_SkipsNonGitMirror_LeavesDirUntouched(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "unrelated.txt")
	if err := os.WriteFile(marker, []byte("pre-existing content"), 0o644); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: dir, RepoURL: "https://example.invalid/repo.git"}}, time.Now, time.Hour)

	var calls int
	store.SetCloneOrFetchForTest(func(context.Context, string, string) error {
		calls++
		return nil
	})

	if errs := store.SyncIfStale(context.Background()); len(errs) != 0 {
		t.Fatalf("SyncIfStale = %v, want no error", errs)
	}
	if calls != 0 {
		t.Fatalf("cloneOrFetch called %d times, want 0", calls)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "unrelated.txt" {
		t.Fatalf("dir contents changed: entries=%v err=%v, want only unrelated.txt untouched", entries, err)
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "pre-existing content" {
		t.Fatalf("marker file modified: got=%q err=%v", string(got), err)
	}
}

// TestSyncIfStale_BudgetExhaustion_NamesUnattemptedMirrors is the last
// AC bullet: with three stale mirrors whose refresh each blocks until
// its own per-mirror context expires (perMirrorTimeout=5s), the
// totalBudget (10s) is spent by the second attempt, and the third mirror
// is never attempted — SyncIfStale names it in a distinct,
// errors.Is(ErrSyncBudgetExhausted)-matching error.
func TestSyncIfStale_BudgetExhaustion_NamesUnattemptedMirrors(t *testing.T) {
	t.Parallel()

	mirrors := make([]SpaceMirror, 0, 3)
	for i := 0; i < 3; i++ {
		fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
		ageMirror(t, fx.dir, 2*time.Hour)
		mirrors = append(mirrors, SpaceMirror{SpaceID: fmt.Sprintf("sp%d", i+1), Dir: fx.dir, RepoURL: fx.dir})
	}

	store := NewStore("axon", t.TempDir(), mirrors, time.Now, time.Hour)
	store.SetCloneOrFetchForTest(func(ctx context.Context, dir, repoURL string) error {
		<-ctx.Done() // models a hung/unreachable fetch: only gives up when its own per-mirror timeout fires
		return ctx.Err()
	})

	start := time.Now()
	errs := store.SyncIfStale(context.Background())
	elapsed := time.Since(start)

	if elapsed < totalBudget {
		t.Fatalf("SyncIfStale returned after %v, want >= totalBudget (%v)", elapsed, totalBudget)
	}
	// Loose sanity ceiling only, not a precision assertion: SyncIfStale
	// checks the remaining budget BEFORE starting a mirror (not mid-flight),
	// so a mirror that starts just under the wire can still run a full
	// perMirrorTimeout — worst case totalBudget+perMirrorTimeout,
	// independent of mirror count. This package already flags real
	// scheduling contention as a flake risk for wall-clock assertions
	// (see TestStatusline_P1Severity's own comment); widen generously
	// rather than chase a tight bound.
	if elapsed > totalBudget+perMirrorTimeout+5*time.Second {
		t.Fatalf("SyncIfStale returned after %v, want roughly totalBudget+perMirrorTimeout (%v)", elapsed, totalBudget+perMirrorTimeout)
	}
	if len(errs) != 3 {
		t.Fatalf("errs = %v, want 3 (2 attempted timeouts + 1 budget-exhausted skip)", errs)
	}

	var budgetErrs int
	for _, e := range errs {
		if errors.Is(e, ErrSyncBudgetExhausted) {
			budgetErrs++
			if !strings.Contains(e.Error(), "sp3") {
				t.Fatalf("budget-exhausted error %v does not name the skipped mirror (sp3)", e)
			}
		}
	}
	if budgetErrs != 1 {
		t.Fatalf("budgetErrs = %d, want exactly 1 (the third, unattempted mirror)", budgetErrs)
	}
}

// TestSyncIfStaleDoesNotInheritTheStatuslineTTL is the regression for a
// blocker that survived its own fix by five minutes.
//
// SyncIfStale used the Store's TTL, which defaults to
// DefaultStatuslineTTL (5 minutes). Found end-to-end against a live space on
// 2026-07-26: an artifact whose pull request had just merged was INVISIBLE to
// `a2a outbox` and `a2a show` ("artifact not found"), and one explicit `a2a
// sync` revealed it at once. The submit had fetched the mirror moments
// earlier, so its age sat inside the statusline window and the read path
// skipped it as fresh.
//
// That is the original read-path blocker, narrower: agent A publishes, it
// merges, agent B reads within five minutes and is told there is nothing —
// while the documented session-start loop says an empty inbox means proceed.
//
// The test asserts the THRESHOLD relationship rather than a literal, because
// what matters is that a read verb never inherits a window sized for a shell
// prompt.
func TestSyncIfStaleDoesNotInheritTheStatuslineTTL(t *testing.T) {
	t.Parallel()

	if readRefreshTTL >= DefaultStatuslineTTL {
		t.Fatalf("readRefreshTTL (%s) is not shorter than DefaultStatuslineTTL (%s) — a read verb would "+
			"again accept a mirror stale enough to hide a counterparty's merge, which is the blocker "+
			"the read refresh exists to close", readRefreshTTL, DefaultStatuslineTTL)
	}
	// And not zero: several read verbs in one agent turn must not each pay a
	// fetch for an answer that cannot have changed between them.
	if readRefreshTTL <= 0 {
		t.Fatalf("readRefreshTTL is %s — every read verb in a burst would fetch again for nothing", readRefreshTTL)
	}
	// Short enough to be useful inside one exchange: a counterparty's merge
	// must surface on the next question, not after the conversation moved on.
	if readRefreshTTL > time.Minute {
		t.Fatalf("readRefreshTTL is %s — long enough for a just-merged artifact to stay invisible "+
			"across a normal back-and-forth", readRefreshTTL)
	}
}

func TestSyncIfStaleResultsAttributesFailureWithoutStringParsing(t *testing.T) {
	t.Parallel()

	fx := newFixtureSpace(t, fixtureParticipant{System: "atlas"})
	store := NewStore("atlas", t.TempDir(), []SpaceMirror{{
		SpaceID: "northstar", Dir: fx.dir, RepoURL: fx.dir, Manifest: mustManifest(t, fx),
	}}, time.Now, time.Hour)
	ageMirror(t, fx.dir, 2*time.Hour)
	store.SetCloneOrFetchForTest(func(context.Context, string, string) error {
		return errors.New("provider detail that callers must not parse")
	})

	results := store.SyncIfStaleResults(context.Background())
	if len(results) != 1 || results[0].Space != "northstar" || results[0].Err == nil {
		t.Fatalf("SyncIfStaleResults = %+v, want attributed northstar failure", results)
	}
}
