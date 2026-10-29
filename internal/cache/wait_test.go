package cache

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// ---- the selection rule (pure; no git) -------------------------------------

func waitArtifact(id, typ, from string, to any, thread string, events ...string) foldedArtifact {
	fa := foldedArtifact{Env: envelopeProbe{ID: id, Type: typ, From: from, To: to, Thread: thread}}
	for _, ulid := range events {
		fa.Events = append(fa.Events, fold.Event{ULID: ulid, Subject: id})
	}
	return fa
}

func waitCand(ulid, actor, transition, subject string) waitCandidate {
	return waitCandidate{Space: "sp1", ULID: ulid, ActorSystem: actor, Transition: transition, Subject: subject, At: "2026-10-29T10:00:00Z"}
}

// TestWait_Selection is spec 02 §6's table: every `why` row for --mine, the
// other two selectors, and the rows that must NOT wake.
func TestWait_Selection(t *testing.T) {
	t.Parallel()
	const me = "axon"
	mine := WaitSelector{Kind: WaitMine}
	consumes := map[string]bool{"XC-beta-widget": true}

	artifacts := map[string]foldedArtifact{
		// I authored it, addressed to seomatrix.
		"XW-axon-1": waitArtifact("XW-axon-1", "work_request", me, []any{"seomatrix"}, "T-axon-1", "01A"),
		// Addressed to me by seomatrix.
		"XW-sem-to-me": waitArtifact("XW-sem-to-me", "work_request", "seomatrix", []any{me}, "T-sem", "01A"),
		// A third-party artifact on a thread where I also have an artifact.
		"XQ-gamma-onthread": waitArtifact("XQ-gamma-onthread", "question", "gamma", []any{"seomatrix"}, "T-axon-1", "01A"),
		// Nothing to do with me.
		"XQ-gamma-other": waitArtifact("XQ-gamma-other", "question", "gamma", []any{"seomatrix"}, "T-other", "01A"),
		// An envelope with no `to:` at all, not mine.
		"XQ-gamma-noto": waitArtifact("XQ-gamma-noto", "question", "gamma", nil, "T-noto", "01A"),
		// A contract I consume, and an announcement deprecating it.
		"XC-beta-widget": waitArtifact("XC-beta-widget", "contract", "beta", []any{"gamma"}, "T-widget", "01A"),
		"XA-beta-dep": func() foldedArtifact {
			fa := waitArtifact("XA-beta-dep", "announcement", "beta", []any{"gamma"}, "T-dep", "01A")
			fa.Env.Deprecates = "XC-beta-widget@1.0.0"
			return fa
		}(),
		// An announcement whose deprecation my registry names AND whose
		// DeprecatesMyDependency fact is set (the declared half of addressedToMe).
		"XA-beta-dep2": func() foldedArtifact {
			fa := waitArtifact("XA-beta-dep2", "announcement", "beta", []any{"gamma"}, "T-dep2", "01A")
			fa.DeprecatesMyDependency = true
			return fa
		}(),
		// A broadcast by a third party, and one of mine.
		"XN-gamma-all": waitArtifact("XN-gamma-all", "announcement", "gamma", "all", "T-gamma-all", "01B1"),
		"XN-axon-all":  waitArtifact("XN-axon-all", "announcement", me, "all", "T-axon-all", "01B1"),
	}

	cases := []struct {
		name string
		sel  WaitSelector
		c    waitCandidate
		want []string // the `why` of every reported event
	}{
		{"acknowledge on an artifact I authored", mine, waitCand("01C", "seomatrix", "acknowledge", "XW-axon-1"), []string{WhyAuthored}},
		{"note on an artifact I authored", mine, waitCand("01C", "seomatrix", "note", "XW-axon-1"), []string{WhyAuthored}},
		{"response/close on an artifact I authored", mine, waitCand("01C", "seomatrix", "respond", "XW-axon-1"), []string{WhyAuthored}},
		{"event on an artifact addressed to me", mine, waitCand("01C", "seomatrix", "note", "XW-sem-to-me"), []string{WhyAddressed}},
		{"third party's note on an artifact addressed to me", mine, waitCand("01C", "gamma", "note", "XW-sem-to-me"), []string{WhyAddressed}},
		{"sibling artifact on a thread I take part in", mine, waitCand("01C", "gamma", "note", "XQ-gamma-onthread"), []string{WhyThread}},
		{"deprecation of a contract my registry names", mine, waitCand("01C", "beta", "publish", "XA-beta-dep"), []string{WhyConsumes}},
		{"publish of a contract my registry names", mine, waitCand("01C", "beta", "publish", "XC-beta-widget"), []string{WhyConsumes}},
		{"deprecate of a contract my registry names", mine, waitCand("01C", "beta", "deprecate", "XC-beta-widget"), []string{WhyConsumes}},
		{"a note on a contract I consume is not a publish", mine, waitCand("01C", "beta", "note", "XC-beta-widget"), nil},
		{"declared deprecation dependency is addressed", mine, waitCand("01C", "beta", "publish", "XA-beta-dep2"), []string{WhyAddressed}},

		{"my own event never wakes", mine, waitCand("01C", me, "note", "XW-axon-1"), nil},
		{"a third party's event that does not concern me", mine, waitCand("01C", "seomatrix", "note", "XQ-gamma-other"), nil},
		{"an envelope with no to: that is not mine", mine, waitCand("01C", "seomatrix", "note", "XQ-gamma-noto"), nil},

		{"broadcast: its creation event wakes once", mine, waitCand("01B1", "gamma", "submit", "XN-gamma-all"), []string{WhyAddressed}},
		{"broadcast: a later third-party note does not", mine, waitCand("01B2", "seomatrix", "note", "XN-gamma-all"), nil},
		{"broadcast of mine: a later note reaches me as authored", mine, waitCand("01B2", "seomatrix", "note", "XN-axon-all"), []string{WhyAuthored}},

		{"a subject absent from the mirror is reported, not dropped", mine, waitCand("01C", "seomatrix", "note", "XW-nowhere"), []string{WhyUnresolved}},

		{"--subject: the named artifact", WaitSelector{Kind: WaitSubject, ID: "XQ-gamma-other"}, waitCand("01C", "seomatrix", "note", "XQ-gamma-other"), []string{WhySubject}},
		{"--subject: any other artifact does not match", WaitSelector{Kind: WaitSubject, ID: "XQ-gamma-other"}, waitCand("01C", "seomatrix", "note", "XW-axon-1"), nil},
		{"--subject: my own event never wakes", WaitSelector{Kind: WaitSubject, ID: "XW-axon-1"}, waitCand("01C", me, "note", "XW-axon-1"), nil},
		{"--thread: an artifact on the thread", WaitSelector{Kind: WaitThread, ID: "T-other"}, waitCand("01C", "seomatrix", "note", "XQ-gamma-other"), []string{WhyThread}},
		{"--thread: an artifact on another thread does not match", WaitSelector{Kind: WaitThread, ID: "T-other"}, waitCand("01C", "seomatrix", "note", "XW-axon-1"), nil},
		{"--thread: an unresolvable subject is reported", WaitSelector{Kind: WaitThread, ID: "T-other"}, waitCand("01C", "seomatrix", "note", "XW-nowhere"), []string{WhyUnresolved}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := selectWaitEvents(me, consumes, tc.sel, []waitCandidate{tc.c}, artifacts)
			var why []string
			for _, e := range got {
				why = append(why, e.Why)
			}
			if !reflect.DeepEqual(why, tc.want) {
				t.Fatalf("selectWaitEvents(%s %s on %s) why = %v, want %v", tc.c.ActorSystem, tc.c.Transition, tc.c.Subject, why, tc.want)
			}
		})
	}
}

// TestWait_Selection_BroadcastThreadIsNotMine is the trap the table above can
// pass for the wrong reason: if `to: all` counted as naming me, a broadcast's
// whole thread would be mine and a later note on a SIBLING of the broadcast
// would wake me.
func TestWait_Selection_BroadcastThreadIsNotMine(t *testing.T) {
	t.Parallel()
	artifacts := map[string]foldedArtifact{
		"XN-gamma-all": waitArtifact("XN-gamma-all", "announcement", "gamma", "all", "T-all", "01A"),
		"XQ-gamma-sib": waitArtifact("XQ-gamma-sib", "question", "gamma", []any{"seomatrix"}, "T-all", "01A"),
	}
	got := selectWaitEvents("axon", nil, WaitSelector{Kind: WaitMine},
		[]waitCandidate{waitCand("01B", "seomatrix", "note", "XQ-gamma-sib")}, artifacts)
	if len(got) != 0 {
		t.Fatalf("a note on a sibling of a broadcast woke the wait: %+v", got)
	}
}

// TestWait_NoFreeText_EventShape pins D-014 at the type: the only strings a
// WaitEvent can hold are the ones listed here.
func TestWait_NoFreeText_EventShape(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(WaitEvent{})
	var got []string
	for i := 0; i < typ.NumField(); i++ {
		got = append(got, typ.Field(i).Name)
	}
	want := []string{"Space", "Event", "Actor", "Transition", "Subject", "Thread", "Why", "At"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WaitEvent fields = %v, want %v (a new field must be an identifier, never free text; D-014)", got, want)
	}
}

// ---- a real-git rig ---------------------------------------------------------

type waitRig struct {
	t       *testing.T
	fx      *fixtureSpace
	origin  string
	mirror  string
	store   *Store
	w       *Waiter
	clock   *fakeWaitClock
	notes   []string
	fetches atomic.Int32
	ulid    int
}

// newWaitRig builds a bare origin (marked a2a.host=local when local), a
// fixture working clone that plays the counterparties, and a separate mirror
// clone read through a Store as system "axon". axon has already authored
// XW-axon-wait (to seomatrix) and submitted it.
func newWaitRig(t *testing.T, local bool) *waitRig {
	t.Helper()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	root := filepath.Dir(fx.dir)
	origin := filepath.Join(root, "origin.git")
	if local {
		fxRunGit(t, root, "--git-dir="+origin, "config", "a2a.host", "local")
	}
	r := &waitRig{t: t, fx: fx, origin: origin, mirror: filepath.Join(root, "mirror"), ulid: 500}
	base := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XW-axon-wait.md", wr("XW-axon-wait", "mine", "axon", []string{"seomatrix"}, "p2", false), "body")
	fx.commitEvent("axon", r.nextULID(), evt("XW-axon-wait", "submit", "axon", base))
	fxRunGit(t, root, "clone", origin, r.mirror)

	manifest := mustManifest(t, fx)
	r.store = NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: r.mirror, RepoURL: origin, Manifest: manifest}},
		func() time.Time { return base.Add(24 * time.Hour) }, 0)
	r.store.SetCloneOrFetchForTest(func(ctx context.Context, dir, repoURL string, cred host.Credential) error {
		r.fetches.Add(1)
		return space.CloneOrFetch(ctx, dir, repoURL, cred)
	})
	r.clock = &fakeWaitClock{t: base}
	r.w = NewWaiter(r.store)
	r.w.env.now = r.clock.now
	r.w.env.sleep = r.clock.sleep
	return r
}

func (r *waitRig) nextULID() string {
	r.ulid++
	return fxULID(r.ulid)
}

// counterparty lands an event from seomatrix on XW-axon-wait.
func (r *waitRig) counterparty(transition string) string {
	r.t.Helper()
	ulid := r.nextULID()
	r.fx.commitEvent("seomatrix", ulid, evt("XW-axon-wait", transition, "seomatrix", time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)))
	return ulid
}

// refreshMirror plays the read verb's staleness refresh.
func (r *waitRig) refreshMirror() {
	r.t.Helper()
	if err := space.CloneOrFetch(context.Background(), r.mirror, r.origin, host.Credential{}); err != nil {
		r.t.Fatalf("refresh mirror: %v", err)
	}
}

func (r *waitRig) head() string {
	r.t.Helper()
	h, err := readMirrorHead(context.Background(), r.mirror)
	if err != nil {
		r.t.Fatalf("mirror head: %v", err)
	}
	return h
}

func (r *waitRig) cursorFile() string {
	return filepath.Join(r.store.cacheDir, "wait", "sp1.mine.cursor")
}

func (r *waitRig) seedCursor(sha string) {
	r.t.Helper()
	if err := writeWaitCursor(r.cursorFile(), sha); err != nil {
		r.t.Fatalf("seed cursor: %v", err)
	}
}

func (r *waitRig) readCursor() string {
	r.t.Helper()
	sha, ok := readWaitCursor(r.cursorFile())
	if !ok {
		r.t.Fatalf("no cursor at %s", r.cursorFile())
	}
	return sha
}

func (r *waitRig) wait(ctx context.Context) (WaitResult, error) {
	return r.w.Wait(ctx, WaitRequest{Selector: WaitSelector{Kind: WaitMine}, Notify: func(s string) { r.notes = append(r.notes, s) }})
}

// fakeWaitMaxSleeps bounds a wait that no scripted event ever ends.
const fakeWaitMaxSleeps = 400

// fakeWaitClock is a clock whose sleep advances it and may script a result.
type fakeWaitClock struct {
	t       time.Time
	sleeps  []time.Duration
	onSleep func(n int, d time.Duration) error
}

func (c *fakeWaitClock) now() time.Time { return c.t }

func (c *fakeWaitClock) sleep(_ context.Context, d time.Duration) error {
	c.sleeps = append(c.sleeps, d)
	c.t = c.t.Add(d)
	if len(c.sleeps) > fakeWaitMaxSleeps {
		// A wait that never ends is a failing test, not a hung one.
		return context.DeadlineExceeded
	}
	if c.onSleep != nil {
		return c.onSleep(len(c.sleeps), d)
	}
	return nil
}

// ---- AC-1, AC-2: the cases `inbox --actionable` cannot see ----------------

// TestWait_AckOnAuthored: a counterparty's acknowledge on an artifact I
// authored ends the wait at once, why=authored.
func TestWait_AckOnAuthored(t *testing.T) {
	t.Parallel()
	r := newWaitRig(t, false)
	r.seedCursor(r.head())
	ulid := r.counterparty("acknowledge")
	r.refreshMirror()
	r.clock.onSleep = func(int, time.Duration) error {
		t.Error("the wait slept although an event had already landed")
		return context.DeadlineExceeded
	}

	res, err := r.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("events = %+v, want exactly the acknowledge", res.Events)
	}
	got := res.Events[0]
	want := WaitEvent{Space: "sp1", Event: ulid, Actor: "seomatrix", Transition: "acknowledge", Subject: "XW-axon-wait", Thread: got.Thread, Why: WhyAuthored, At: got.At}
	if got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
	if res.TimedOut {
		t.Fatal("TimedOut = true on a landing")
	}
}

// TestWait_NoteOnAuthored: a counterparty's note on my artifact wakes the
// wait, and on the SAME fixture `inbox --actionable` lists nothing for it.
func TestWait_NoteOnAuthored(t *testing.T) {
	t.Parallel()
	r := newWaitRig(t, false)
	r.seedCursor(r.head())
	r.counterparty("acknowledge")
	noteULID := r.counterparty("note")
	r.refreshMirror()

	res, err := r.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	var notes []WaitEvent
	for _, e := range res.Events {
		if e.Event == noteULID {
			notes = append(notes, e)
		}
	}
	if len(notes) != 1 || notes[0].Why != WhyAuthored || notes[0].Transition != "note" {
		t.Fatalf("the note did not wake the wait as authored: events = %+v", res.Events)
	}

	items, err := r.store.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("`inbox --actionable` lists %v for the same fixture; the case wait exists for is one it cannot see", itemIDs(items))
	}
}

// ---- AC-6: re-arm without loss ----------------------------------------------

func TestWait_CursorRearm(t *testing.T) {
	t.Parallel()
	r := newWaitRig(t, false)
	r.clock.onSleep = func(int, time.Duration) error { return context.DeadlineExceeded }

	// Wait 1: no cursor, baseline = current main. Nothing lands. 124-shaped.
	res, err := r.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait 1: %v", err)
	}
	if !res.TimedOut || len(res.Events) != 0 || len(res.Unmeasured) != 0 {
		t.Fatalf("Wait 1 = %+v, want a clean timeout", res)
	}
	baseline := r.head()
	if got := r.readCursor(); got != baseline {
		t.Fatalf("cursor after a timeout = %s, want main %s", got, baseline)
	}

	// An event lands between the exit and the re-arm.
	ulid := r.counterparty("acknowledge")
	r.refreshMirror()

	// Wait 2 reports it at once, without blocking.
	r.clock.onSleep = func(int, time.Duration) error {
		t.Error("the re-arm blocked although an event was waiting")
		return context.DeadlineExceeded
	}
	res, err = r.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait 2: %v", err)
	}
	if len(res.Events) != 1 || res.Events[0].Event != ulid {
		t.Fatalf("Wait 2 events = %+v, want the one that landed between the waits (%s)", res.Events, ulid)
	}
	if got := r.readCursor(); got != r.head() {
		t.Fatalf("cursor after a landing = %s, want main %s", got, r.head())
	}

	// Wait 3 starts where wait 2 stopped: the same event is not reported twice.
	r.clock.onSleep = func(int, time.Duration) error { return context.DeadlineExceeded }
	res, err = r.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait 3: %v", err)
	}
	if !res.TimedOut || len(res.Events) != 0 {
		t.Fatalf("Wait 3 = %+v, want a clean timeout (the event was already reported)", res)
	}
}

// TestWait_CursorRearm_NoCursorNoFlood: old history is the session-start
// inbox's business, never a flood from wait.
func TestWait_CursorRearm_NoCursorNoFlood(t *testing.T) {
	t.Parallel()
	r := newWaitRig(t, false)
	r.counterparty("acknowledge") // already in the history the mirror will see
	r.refreshMirror()
	r.clock.onSleep = func(int, time.Duration) error { return context.DeadlineExceeded }

	res, err := r.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !res.TimedOut || len(res.Events) != 0 {
		t.Fatalf("a missing cursor flooded the session with old history: %+v", res)
	}
}

// TestWait_CursorRearm_NotAnAncestor: a cursor that main no longer descends
// from takes the baseline with one stderr line and is not an error.
func TestWait_CursorRearm_NotAnAncestor(t *testing.T) {
	t.Parallel()
	for name, sha := range map[string]string{
		"a commit the mirror does not have": strings.Repeat("b", 40),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newWaitRig(t, false)
			r.seedCursor(sha)
			r.clock.onSleep = func(int, time.Duration) error { return context.DeadlineExceeded }

			res, err := r.wait(context.Background())
			if err != nil {
				t.Fatalf("Wait: %v", err)
			}
			if !res.TimedOut || len(res.Events) != 0 {
				t.Fatalf("Wait = %+v, want a clean timeout on the new baseline", res)
			}
			if len(r.notes) != 1 || !strings.Contains(r.notes[0], "sp1") || !strings.Contains(r.notes[0], "not an ancestor") {
				t.Fatalf("notes = %q, want one line naming the space and the ancestor problem", r.notes)
			}
			if got := r.readCursor(); got != r.head() {
				t.Fatalf("cursor = %s, want the new baseline %s", got, r.head())
			}
		})
	}
}

// TestWait_CursorRearm_DivergedHistory: a commit the mirror HAS but that main
// does not descend from is the exit-1 branch of merge-base, not the
// unknown-commit branch.
func TestWait_CursorRearm_DivergedHistory(t *testing.T) {
	t.Parallel()
	r := newWaitRig(t, false)
	// A commit on a side branch of the mirror: present, not an ancestor of main.
	fxRunGit(t, r.mirror, "checkout", "-q", "-b", "side")
	fxWriteFile(t, filepath.Join(r.mirror, "side.txt"), "x")
	fxCommit(t, r.mirror, "side")
	side := strings.TrimSpace(runGitForTest(t, r.mirror, "rev-parse", "HEAD"))
	fxRunGit(t, r.mirror, "checkout", "-q", "main")
	r.seedCursor(side)
	r.clock.onSleep = func(int, time.Duration) error { return context.DeadlineExceeded }

	res, err := r.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !res.TimedOut || len(res.Events) != 0 || len(r.notes) != 1 || !strings.Contains(r.notes[0], "not an ancestor") {
		t.Fatalf("res = %+v notes = %q, want a clean timeout and one ancestor line", res, r.notes)
	}
}

func runGitForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGitOutput(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// ---- AC-7: a local bare space, no network -----------------------------------

func TestWait_LocalBareRepo(t *testing.T) {
	t.Parallel()
	r := newWaitRig(t, true)
	r.seedCursor(r.head())

	var fetchesBeforeLanding int32 = -1
	var landed string
	r.clock.onSleep = func(n int, d time.Duration) error {
		if d != waitLocalPollInterval {
			t.Errorf("sleep %d = %s, want the local poll interval %s", n, d, waitLocalPollInterval)
		}
		if n == 1 {
			// The first poll saw main unchanged. Now the counterparty lands.
			fetchesBeforeLanding = r.fetches.Load()
			landed = r.counterparty("acknowledge")
		}
		return nil
	}

	res, err := r.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if fetchesBeforeLanding != 0 {
		t.Fatalf("fetches while main was unchanged = %d, want 0 (a local space reads the bare ref; fetching on every poll is what the ref read exists to avoid)", fetchesBeforeLanding)
	}
	if len(res.Events) != 1 || res.Events[0].Event != landed || res.Events[0].Why != WhyAuthored {
		t.Fatalf("events = %+v, want the acknowledge %s as authored", res.Events, landed)
	}
	if got := r.fetches.Load(); got != 1 {
		t.Fatalf("fetches after the landing = %d, want exactly 1", got)
	}
	if got := r.readCursor(); got != r.head() {
		t.Fatalf("cursor = %s, want main %s", got, r.head())
	}
}

// ---- the watch loop, with a fake clock and scripted refs -------------------

var (
	waitShaA = strings.Repeat("a", 40)
	waitShaB = strings.Repeat("b", 40)
	waitShaC = strings.Repeat("c", 40)
)

// fakeSpaces is a loop-test rig with no git: heads and refs are variables.
type fakeSpaces struct {
	t       *testing.T
	store   *Store
	w       *Waiter
	clock   *fakeWaitClock
	heads   map[string]string
	refs    map[string]string
	refErr  map[string]error
	judges  []string
	fetches map[string]int
	notes   []string
	onFetch func(space string, n int)
	judge   func(space, from, to string) judgement
}

func newFakeSpaces(t *testing.T, local bool, ids ...string) *fakeSpaces {
	t.Helper()
	f := &fakeSpaces{t: t, heads: map[string]string{}, refs: map[string]string{}, refErr: map[string]error{}, fetches: map[string]int{}}
	var mirrors []SpaceMirror
	for _, id := range ids {
		url := "https://example.invalid/" + id
		if local {
			url = "/local/" + id + ".git"
		}
		mirrors = append(mirrors, SpaceMirror{SpaceID: id, Dir: filepath.Join(t.TempDir(), id), RepoURL: url})
		f.heads[id] = waitShaA
		f.refs[id] = waitShaA
	}
	base := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	f.store = NewStore("axon", t.TempDir(), mirrors, func() time.Time { return base }, 0)
	f.store.SetCloneOrFetchForTest(func(_ context.Context, dir, _ string, _ host.Credential) error {
		id := filepath.Base(dir)
		f.fetches[id]++
		if f.onFetch != nil {
			f.onFetch(id, f.fetches[id])
		}
		return nil
	})
	f.clock = &fakeWaitClock{t: base}
	f.w = NewWaiter(f.store)
	f.w.env.now = f.clock.now
	f.w.env.sleep = f.clock.sleep
	f.w.env.localKind = func(_ context.Context, url string) (string, bool, error) {
		return url, strings.HasPrefix(url, "/local/"), nil
	}
	f.w.env.localRef = func(_ context.Context, gitDir string) (string, error) {
		id := strings.TrimSuffix(filepath.Base(gitDir), ".git")
		return f.refs[id], f.refErr[id]
	}
	f.w.env.mirrorHead = func(_ context.Context, dir string) (string, error) { return f.heads[filepath.Base(dir)], nil }
	f.w.env.isAncestor = func(context.Context, string, string, string) (bool, error) { return true, nil }
	f.w.env.judge = func(_ context.Context, wt *spaceWatch, from, to string) (judgement, error) {
		f.judges = append(f.judges, wt.sm.SpaceID+":"+from[:1]+".."+to[:1])
		if f.judge != nil {
			return f.judge(wt.sm.SpaceID, from, to), nil
		}
		return judgement{}, nil
	}
	return f
}

func (f *fakeSpaces) wait(ctx context.Context, spaces ...string) (WaitResult, error) {
	return f.w.Wait(ctx, WaitRequest{Spaces: spaces, Selector: WaitSelector{Kind: WaitMine}, Notify: func(s string) { f.notes = append(f.notes, s) }})
}

func (f *fakeSpaces) cursor(id string) (string, bool) {
	return readWaitCursor(filepath.Join(f.store.cacheDir, "wait", id+".mine.cursor"))
}

func fakeEvent(space, ulid string) WaitEvent {
	return WaitEvent{Space: space, Event: ulid, Actor: "seomatrix", Transition: "note", Subject: "XW-1", Why: WhyAuthored}
}

// TestWait_HostedBackoff: the hosted interval starts at readRefreshTTL,
// doubles to the 5 min cap and resets on a change.
func TestWait_HostedBackoff(t *testing.T) {
	t.Parallel()
	f := newFakeSpaces(t, false, "gh")
	f.onFetch = func(id string, n int) {
		switch n {
		case 6:
			f.heads[id] = waitShaB // main moves, nothing in it concerns me
		case 8:
			f.heads[id] = waitShaC // main moves and the event matches
		}
	}
	f.judge = func(id, _, to string) judgement {
		if to == waitShaC {
			return judgement{Events: []WaitEvent{fakeEvent(id, "01X")}}
		}
		return judgement{}
	}

	res, err := f.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("events = %+v, want the matching event", res.Events)
	}
	want := []time.Duration{
		readRefreshTTL, 2 * readRefreshTTL, 4 * readRefreshTTL, 8 * readRefreshTTL, // 30s 1m 2m 4m
		waitHostedMaxInterval, waitHostedMaxInterval, // the cap, twice
		readRefreshTTL, 2 * readRefreshTTL, // reset by the change, then doubling again
	}
	if !reflect.DeepEqual(f.clock.sleeps, want) {
		t.Fatalf("sleeps = %v, want %v", f.clock.sleeps, want)
	}
	if readRefreshTTL != 30*time.Second || waitHostedMaxInterval != 5*time.Minute {
		t.Fatalf("the spec's numbers moved: ttl=%s cap=%s", readRefreshTTL, waitHostedMaxInterval)
	}
	if f.fetches["gh"] != 8 {
		t.Fatalf("fetches = %d, want 8 (one per hosted poll)", f.fetches["gh"])
	}
}

// TestWait_LocalPoll_FetchesOnlyOnChange: a local space polls the ref every
// 2 s and fetches only when it differs from what was judged.
func TestWait_LocalPoll_FetchesOnlyOnChange(t *testing.T) {
	t.Parallel()
	f := newFakeSpaces(t, true, "loc")
	f.clock.onSleep = func(n int, _ time.Duration) error {
		if n == 3 {
			f.refs["loc"] = waitShaB
			f.heads["loc"] = waitShaB
		}
		return nil
	}
	f.judge = func(id, _, _ string) judgement { return judgement{Events: []WaitEvent{fakeEvent(id, "01X")}} }

	res, err := f.wait(context.Background())
	if err != nil || len(res.Events) != 1 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	for i, d := range f.clock.sleeps {
		if d != waitLocalPollInterval {
			t.Fatalf("sleep %d = %s, want %s", i, d, waitLocalPollInterval)
		}
	}
	if len(f.clock.sleeps) != 3 || f.fetches["loc"] != 1 {
		t.Fatalf("sleeps = %v fetches = %d, want 3 sleeps and exactly 1 fetch", f.clock.sleeps, f.fetches["loc"])
	}
}

// TestWait_Unmeasured_FailedRefRead: a failing ref read marks the space
// failed, the next success clears it, and a space that is still failed at the
// timeout keeps its old cursor.
func TestWait_Unmeasured_FailedRefRead(t *testing.T) {
	t.Parallel()
	t.Run("still failed at the timeout", func(t *testing.T) {
		t.Parallel()
		f := newFakeSpaces(t, true, "loc")
		f.heads["loc"] = waitShaB // main is past the saved cursor, judged cleanly
		if err := writeWaitCursor(filepath.Join(f.store.cacheDir, "wait", "loc.mine.cursor"), waitShaA); err != nil {
			t.Fatal(err)
		}
		f.refErr["loc"] = errors.New("bare repository unreadable")
		f.clock.onSleep = func(n int, _ time.Duration) error {
			if n == 2 {
				return context.DeadlineExceeded
			}
			return nil
		}

		res, err := f.wait(context.Background())
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
		if !res.TimedOut || !reflect.DeepEqual(res.Unmeasured, []string{"loc"}) || len(res.Events) != 0 {
			t.Fatalf("res = %+v, want a timeout naming loc as unmeasured", res)
		}
		if got, _ := f.cursor("loc"); got != waitShaA {
			t.Fatalf("cursor of a failed space moved to %s, want it left at %s", got, waitShaA)
		}
	})
	t.Run("the next success clears it", func(t *testing.T) {
		t.Parallel()
		f := newFakeSpaces(t, true, "loc")
		f.heads["loc"] = waitShaB
		if err := writeWaitCursor(filepath.Join(f.store.cacheDir, "wait", "loc.mine.cursor"), waitShaA); err != nil {
			t.Fatal(err)
		}
		f.refErr["loc"] = errors.New("bare repository unreadable")
		f.refs["loc"] = waitShaB
		f.clock.onSleep = func(n int, _ time.Duration) error {
			switch n {
			case 1:
				f.refErr["loc"] = nil // the second poll reads fine
			case 2:
				return context.DeadlineExceeded
			}
			return nil
		}

		res, err := f.wait(context.Background())
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
		if !res.TimedOut || len(res.Unmeasured) != 0 {
			t.Fatalf("res = %+v, want a clean timeout once a poll succeeded", res)
		}
		if got, _ := f.cursor("loc"); got != waitShaB {
			t.Fatalf("cursor = %s, want it advanced to %s on a measured timeout", got, waitShaB)
		}
	})
}

// TestWait_Unmeasured_FailedFetch: a failed hosted fetch is a failed
// observation, not a quiet one.
func TestWait_Unmeasured_FailedFetch(t *testing.T) {
	t.Parallel()
	f := newFakeSpaces(t, false, "gh")
	f.store.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return errors.New("network unreachable") })
	f.clock.onSleep = func(n int, _ time.Duration) error {
		if n == 2 {
			return context.DeadlineExceeded
		}
		return nil
	}
	res, err := f.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !res.TimedOut || !reflect.DeepEqual(res.Unmeasured, []string{"gh"}) {
		t.Fatalf("res = %+v, want a timeout naming gh as unmeasured", res)
	}
	if want := []time.Duration{readRefreshTTL, 2 * readRefreshTTL}; !reflect.DeepEqual(f.clock.sleeps, want) {
		t.Fatalf("sleeps = %v, want a failed poll to back off too: %v", f.clock.sleeps, want)
	}
}

// TestWait_CallerCancelIsAnErrorNotATimeout: a cancellation that is not the
// wait's own deadline is an error, never a quiet 124.
func TestWait_CallerCancelIsAnErrorNotATimeout(t *testing.T) {
	t.Parallel()
	f := newFakeSpaces(t, false, "gh")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer cancel()
	f.store.SetCloneOrFetchForTest(func(c context.Context, _, _ string, _ host.Credential) error {
		cancel()
		return c.Err()
	})
	f.clock.onSleep = func(int, time.Duration) error { return nil }
	_, err := f.wait(ctx)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("a caller's cancellation must come back as an error, got %v", err)
	}
}

// TestWait_MultiSpace_OneExitReportsEverythingLanded: the first space with a
// match ends the wait, and another space whose mirror already moved is
// reported in the same exit, in configuration order.
func TestWait_MultiSpace_OneExitReportsEverythingLanded(t *testing.T) {
	t.Parallel()
	f := newFakeSpaces(t, true, "one", "two")
	f.clock.onSleep = func(n int, _ time.Duration) error {
		if n == 1 {
			f.refs["two"], f.heads["two"] = waitShaB, waitShaB // `two` fetches and matches...
			f.heads["one"] = waitShaC                          // ...while `one` already moved locally
		}
		return nil
	}
	f.judge = func(id, _, _ string) judgement {
		return judgement{Events: []WaitEvent{fakeEvent(id, "01"+id)}}
	}
	res, err := f.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	var got []string
	for _, e := range res.Events {
		got = append(got, e.Space)
	}
	if !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("event spaces = %v, want both, in configuration order", got)
	}
	if f.fetches["one"] != 0 {
		t.Fatalf("`one` was fetched %d times; the final sweep must not fetch", f.fetches["one"])
	}
	for _, id := range []string{"one", "two"} {
		want := map[string]string{"one": waitShaC, "two": waitShaB}[id]
		if got, _ := f.cursor(id); got != want {
			t.Errorf("cursor(%s) = %s, want %s", id, got, want)
		}
	}
}

func TestWait_UnknownSpaceAndNoSpaces(t *testing.T) {
	t.Parallel()
	f := newFakeSpaces(t, false, "gh")
	if _, err := f.wait(context.Background(), "nope"); !errors.Is(err, ErrWaitUnknownSpace) {
		t.Fatalf("unknown space: err = %v, want ErrWaitUnknownSpace", err)
	}
	empty := NewStore("axon", t.TempDir(), nil, time.Now, 0)
	_, err := NewWaiter(empty).Wait(context.Background(), WaitRequest{Selector: WaitSelector{Kind: WaitMine}, Notify: func(string) {}})
	if !errors.Is(err, ErrWaitNoSpaces) {
		t.Fatalf("no spaces: err = %v, want ErrWaitNoSpaces", err)
	}
	if _, err := NewWaiter(empty).Wait(context.Background(), WaitRequest{Selector: WaitSelector{Kind: WaitMine}}); err == nil {
		t.Fatal("a nil Notify was accepted")
	}
}

// TestWait_UndecodableEventIsReportedNotSkipped: an event file that cannot be
// decoded is named on the diagnostic channel with its path and makes the
// observation failed; it is never judged "not mine".
func TestWait_UndecodableEventIsReportedNotSkipped(t *testing.T) {
	t.Parallel()
	r := newWaitRig(t, false)
	r.seedCursor(r.head())
	bad := filepath.Join(r.fx.dir, "seomatrix", "events", r.fx.year, fxULID(900)+".yaml")
	fxWriteFile(t, bad, "event: [unterminated\n  - :\n")
	fxCommitAndPush(t, r.fx.dir, "fixture: undecodable event")
	r.refreshMirror()
	r.clock.onSleep = func(int, time.Duration) error { return context.DeadlineExceeded }

	res, err := r.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	wantPath := "seomatrix/events/" + r.fx.year + "/" + fxULID(900) + ".yaml"
	if len(r.notes) != 1 || !strings.Contains(r.notes[0], wantPath) {
		t.Fatalf("notes = %q, want one line naming %s", r.notes, wantPath)
	}
	if !reflect.DeepEqual(res.Unmeasured, []string{"sp1"}) || !res.TimedOut {
		t.Fatalf("res = %+v, want a timeout that names sp1 unmeasured", res)
	}
}

func TestWait_PathHelpers(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"XW-axon-1": "XW-axon-1", "../x/y": "%2E%2E%2Fx%2Fy", "a b": "a%20b"} {
		if got := waitPathSafe(in); got != want {
			t.Errorf("waitPathSafe(%q) = %q, want %q", in, got, want)
		}
	}
	for p, want := range map[string]bool{
		"axon/events/2026/01H.yaml":  true,
		"axon/events/2026/.yaml":     false,
		"axon/exchanges/x.md":        false,
		"axon/events/2026/x/y.yaml":  false,
		"events/2026/01H.yaml":       false,
		"axon/events/2026/01H.yaml~": false,
	} {
		if got := isEventPath(p); got != want {
			t.Errorf("isEventPath(%q) = %v, want %v", p, got, want)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c"), []byte("not a sha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readWaitCursor(filepath.Join(dir, "c")); ok {
		t.Error("a malformed cursor was accepted")
	}
}

// TestWait_NoFreeText: D-014 end to end. An artifact whose title and body
// carry a sentinel, addressed to me, wakes the wait; the serialised result
// holds identifiers only.
func TestWait_NoFreeText(t *testing.T) {
	t.Parallel()
	const sentinel = "SENTINEL-FREE-TEXT-9f3a"
	r := newWaitRig(t, false)
	r.seedCursor(r.head())
	r.fx.commitArtifact("seomatrix/exchanges/XW-sem-sentinel.md",
		wr("XW-sem-sentinel", sentinel+" title", "seomatrix", []string{"axon"}, "p2", false), sentinel+" body")
	r.fx.commitEvent("seomatrix", r.nextULID(), evt("XW-sem-sentinel", "submit", "seomatrix", time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)))
	r.refreshMirror()

	res, err := r.wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(res.Events) != 1 || res.Events[0].Why != WhyAddressed {
		t.Fatalf("events = %+v, want the addressed submit", res.Events)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw)+strings.Join(r.notes, "\n"), sentinel) {
		t.Fatalf("free text reached the wait's output: %s %q", raw, r.notes)
	}
}
