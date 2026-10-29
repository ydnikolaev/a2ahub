package cache

// wait.go is `a2a wait`'s whole core: the selection rule (which of another
// system's events concerns this project's system), the diff-based read of
// what landed, the persisted re-arm cursor, and the watch loop.
//
// The CLI is transport only (ADR-019): it parses flags, calls Waiter.Wait and
// renders the result. The rule is a pure function over parsed inputs so its
// table test needs no git (selectWaitEvents); the mirror read that feeds it is
// a separate method. Every git call goes through runGitOutput and every file
// read through readBounded, this package's two reviewed seams for both.
//
// No goroutines: one loop tracks a next-poll time per space and sleeps until
// the earliest, so there is nothing to own, recover or leak (AGENTS.md
// concurrency rail).
//
// D-014: WaitEvent has no free-text field. A counterparty's note body or title
// is never decoded into anything this file returns, so no renderer can leak it.

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

const (
	// waitLocalPollInterval is a local space's ref-poll period: space.Awaiter's
	// own 2 s, a `git rev-parse` of the bare repository's ref (about 21 ms wall
	// and 6.7 ms CPU measured on 2026-10-29, spec 02 §T3), so about a third of a
	// percent of one core while waiting.
	waitLocalPollInterval = 2 * time.Second
	// waitHostedMaxInterval caps the hosted backoff. The interval starts at
	// readRefreshTTL, doubles after every poll that saw no change or failed, and
	// resets to readRefreshTTL after a change.
	waitHostedMaxInterval = 5 * time.Minute
	// waitCursorMaxBytes bounds a cursor file read. A cursor is one git sha.
	waitCursorMaxBytes = 256
)

// WaitSelectorKind names what a wait is listening for.
type WaitSelectorKind string

const (
	// WaitMine selects every counterparty event that concerns this system.
	WaitMine WaitSelectorKind = "mine"
	// WaitSubject selects every counterparty event whose subject is the ID.
	WaitSubject WaitSelectorKind = "subject"
	// WaitThread selects every counterparty event on an artifact of the thread.
	WaitThread WaitSelectorKind = "thread"
)

// The `why` vocabulary: the first reason that holds, in this order, is the one
// reported (spec 02 §T2).
const (
	// WhyAddressed: the subject's `to:` names me, or it is a deprecation of a
	// contract I depend on, or it is a broadcast's creation event.
	WhyAddressed = "addressed"
	// WhyAuthored: the subject's `from:` is me.
	WhyAuthored = "authored"
	// WhyThread: the subject sits on a thread I take part in (or the thread
	// named by --thread).
	WhyThread = "thread"
	// WhyConsumes: the subject is a publish or a deprecation under a contract
	// my consumes.yaml names.
	WhyConsumes = "consumes"
	// WhySubject: the subject is the one named by --subject.
	WhySubject = "subject"
	// WhyUnresolved: the event's subject is not in the mirror, so whether it
	// concerns me could not be judged. It is reported, never dropped: an event
	// that could not be JUDGED is not an event that was EXCLUDED (AGENTS.md
	// anti-pattern 22).
	WhyUnresolved = "unresolved"
)

// WaitSelector is the closed set of things a wait can listen for.
type WaitSelector struct {
	Kind WaitSelectorKind
	// ID is the artifact id (WaitSubject) or thread id (WaitThread). Empty for
	// WaitMine.
	ID string
}

// cursorKey is the selector's part of the cursor file name: `mine`,
// `subject-<id>` or `thread-<id>` with path-unsafe bytes hex-escaped.
func (s WaitSelector) cursorKey() string {
	switch s.Kind {
	case WaitSubject:
		return "subject-" + waitPathSafe(s.ID)
	case WaitThread:
		return "thread-" + waitPathSafe(s.ID)
	default:
		return string(WaitMine)
	}
}

// waitPathSafe keeps [A-Za-z0-9_-] and hex-escapes every other byte as %XX, so
// an id can never climb out of the cursor directory or collide with another.
func waitPathSafe(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

// WaitEvent is one counterparty event the wait reports. It carries facts only:
// there is deliberately no note, title or other free-text field (D-014).
type WaitEvent struct {
	Space      string          `json:"space"`
	Event      string          `json:"event"`
	Actor      string          `json:"actor"`
	Transition fold.Transition `json:"transition"`
	Subject    string          `json:"subject"`
	Thread     string          `json:"thread"`
	Why        string          `json:"why"`
	At         string          `json:"at"`
}

// WaitRequest is one wait. The deadline is the caller's context's.
type WaitRequest struct {
	// Spaces restricts the wait to these configured space ids; empty means all.
	Spaces   []string
	Selector WaitSelector
	// Notify receives one diagnostic line at a time (a cursor that is not an
	// ancestor of main, an undecodable event file and its path, ...). It is
	// required: core packages return or hand off, they never log (AGENTS.md
	// error flow), and a diagnostic with nowhere to go would be silent.
	Notify func(string)
}

// WaitResult is a wait's outcome. Events is non-empty exactly when the wait
// ended on a landing; TimedOut is set when the context's deadline ended it.
// Unmeasured lists the spaces whose latest observation failed.
type WaitResult struct {
	Events     []WaitEvent
	TimedOut   bool
	Unmeasured []string
}

// ErrWaitUnknownSpace is returned when a requested space is not configured.
var ErrWaitUnknownSpace = errors.New("cache: wait: space is not configured")

// ErrWaitNoSpaces is returned when there is nothing to watch.
var ErrWaitNoSpaces = errors.New("cache: wait: no connected space to watch")

// waitCandidate is one decoded event of another system: exactly the fields the
// rule reads, none of the free text.
type waitCandidate struct {
	Space       string
	ULID        string
	ActorSystem string
	Transition  fold.Transition
	Subject     string
	At          string
	// Path is the event file's repo-relative path, for diagnostics only.
	Path string
}

// selectWaitEvents is the selection rule: a pure function over
// (me, consumes, selector, events, envelopes). candidates are events written
// by OTHER systems only (the caller filters its own); artifacts maps an
// artifact id to its folded read model. Events come back in input order.
func selectWaitEvents(me string, consumes map[string]bool, sel WaitSelector, candidates []waitCandidate, artifacts map[string]foldedArtifact) []WaitEvent {
	threads := threadsConcerning(artifacts, me)
	var out []WaitEvent
	for _, c := range candidates {
		if c.ActorSystem == me {
			continue // my own write is `await`'s business, never a wake
		}
		fa, found := artifacts[c.Subject]
		why, ok := waitWhy(me, consumes, sel, threads, c, fa, found)
		if !ok {
			continue
		}
		out = append(out, WaitEvent{
			Space: c.Space, Event: c.ULID, Actor: c.ActorSystem, Transition: c.Transition,
			Subject: c.Subject, Thread: fa.Env.Thread, Why: why, At: c.At,
		})
	}
	return out
}

// threadsConcerning is the set of thread ids on which some artifact has
// `from:` me or a `to:` that literally names me. `to: all` does NOT name me:
// counting a broadcast would make every thread a broadcast ever touched mine
// and wake this system on every later note a third party writes under it.
func threadsConcerning(artifacts map[string]foldedArtifact, me string) map[string]bool {
	out := map[string]bool{}
	for _, fa := range artifacts {
		if fa.Env.Thread == "" {
			continue
		}
		if fa.Env.From == me || containsString(normalizeTo(fa.Env.To), me) {
			out[fa.Env.Thread] = true
		}
	}
	return out
}

// waitWhy returns the first reason the event concerns the selector, if any.
func waitWhy(me string, consumes map[string]bool, sel WaitSelector, threads map[string]bool, c waitCandidate, fa foldedArtifact, found bool) (string, bool) {
	switch sel.Kind {
	case WaitSubject:
		return WhySubject, c.Subject == sel.ID
	case WaitThread:
		if !found {
			return WhyUnresolved, true // cannot tell which thread it sits on
		}
		return WhyThread, fa.Env.Thread == sel.ID
	}
	if !found {
		return WhyUnresolved, true
	}
	if waitAddressed(fa, me, c) {
		return WhyAddressed, true
	}
	if fa.Env.From == me {
		return WhyAuthored, true
	}
	if fa.Env.Thread != "" && threads[fa.Env.Thread] {
		return WhyThread, true
	}
	if waitConsumes(fa, c, consumes) {
		return WhyConsumes, true
	}
	return "", false
}

// waitAddressed is addressedToMe with the broadcast clause narrowed: a
// broadcast counts only through its CREATION event. Later events on it reach
// this system only through authored, thread or consumes. A wrapper, not a
// copy: every other clause (a `to:` naming me, the declared and observed
// deprecation halves) is addressedToMe's own.
func waitAddressed(fa foldedArtifact, me string, c waitCandidate) bool {
	if fa.Env.isBroadcast() {
		return isCreationEvent(fa, c)
	}
	return addressedToMe(fa, me)
}

// isCreationEvent reports whether c is the first event, by ULID, among the
// events whose subject is the artifact itself. The event under judgment is
// part of the set, so an artifact whose history is not in fa is still judged.
func isCreationEvent(fa foldedArtifact, c waitCandidate) bool {
	if c.Subject != fa.Env.ID {
		return false
	}
	first := c.ULID
	for _, e := range fa.Events {
		if e.Subject == fa.Env.ID && e.ULID < first {
			first = e.ULID
		}
	}
	return first == c.ULID
}

// waitConsumes holds when the subject is a deprecation announcement of a
// contract my consumes.yaml names (the declared half DeprecatesMyDependency
// reads), or a publish/deprecate event on such a contract itself.
func waitConsumes(fa foldedArtifact, c waitCandidate, consumes map[string]bool) bool {
	if id := fa.Env.deprecatedContractID(); id != "" && consumes[id] {
		return true
	}
	if fa.Env.Type == string(fold.KindContract) && consumes[fa.Env.ID] {
		return c.Transition == fold.TPublish || c.Transition == fold.TDeprecate
	}
	return false
}

// waitEnv is every outside-world read the loop makes, as a field, so the loop's
// tests run with a fake clock and scripted refs (space.Awaiter's SetWaitForTest
// shape). NewWaiter fills every field; none is ever nil in production.
type waitEnv struct {
	now func() time.Time
	// sleep blocks for d or until ctx is done, returning ctx.Err() then.
	sleep func(ctx context.Context, d time.Duration) error
	// localKind classifies a space's RepoURL: the bare repository's git dir and
	// whether it is a local space. An error is a classification that could not
	// be made, never "hosted".
	localKind func(ctx context.Context, repoURL string) (gitDir string, local bool, err error)
	// localRef reads refs/heads/<base> from the local space's bare repository.
	localRef func(ctx context.Context, gitDir string) (string, error)
	// mirrorHead is the mirror's current main sha.
	mirrorHead func(ctx context.Context, dir string) (string, error)
	// isAncestor reports whether ancestor is an ancestor of head.
	isAncestor func(ctx context.Context, dir, ancestor, head string) (bool, error)
	// judge reads the events that landed in from..to and applies the rule.
	judge func(ctx context.Context, w *spaceWatch, from, to string) (judgement, error)
}

// judgement is what judging one range yields.
type judgement struct {
	Events []WaitEvent
	// Degraded: something in the range could not be judged (an undecodable
	// event file, an unreadable registry). The observation counts as failed.
	Degraded bool
}

// Waiter runs `a2a wait` over a Store's connected spaces.
type Waiter struct {
	store *Store
	env   waitEnv
	req   WaitRequest // set for the duration of one Wait call
}

// NewWaiter constructs a Waiter. store must not be nil (rails anti-pattern 10).
func NewWaiter(store *Store) *Waiter {
	w := &Waiter{store: store}
	w.env = waitEnv{
		now:        time.Now,
		sleep:      sleepCtx,
		localKind:  localSpaceKind,
		localRef:   readLocalRef,
		mirrorHead: readMirrorHead,
		isAncestor: gitIsAncestor,
		judge:      w.judgeRange,
	}
	return w
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// spaceWatch is one space's loop state.
type spaceWatch struct {
	idx        int
	sm         SpaceMirror
	local      bool
	gitDir     string
	cursorPath string
	// judged is the mirror main sha whose events have been judged.
	judged string
	// failed: the latest observation of this space did not succeed.
	failed   bool
	interval time.Duration
	next     time.Time
}

// Wait blocks until at least one event concerning the selector lands, the
// context ends, or the first observation cannot be made. See spec 02 §T3-T4.
//
// A returned error means no observation was possible at all (unknown space, no
// mirror to baseline from, a local space that cannot be classified); the caller
// maps it to its "error before the first observation" code. A deadline is not
// an error: it is WaitResult.TimedOut.
func (w *Waiter) Wait(ctx context.Context, req WaitRequest) (WaitResult, error) {
	if req.Notify == nil {
		return WaitResult{}, errors.New("cache: wait: Notify is required")
	}
	switch req.Selector.Kind {
	case WaitMine:
	case WaitSubject, WaitThread:
		if req.Selector.ID == "" {
			return WaitResult{}, fmt.Errorf("cache: wait: selector %q needs an id", req.Selector.Kind)
		}
	default:
		return WaitResult{}, fmt.Errorf("cache: wait: unknown selector %q", req.Selector.Kind)
	}
	w.req = req
	watches, err := w.selectSpaces(req.Spaces)
	if err != nil {
		return WaitResult{}, err
	}
	for _, wt := range watches {
		if err := w.start(ctx, wt); err != nil {
			return WaitResult{}, err
		}
	}

	// Catch-up: a cursor behind main is judged before blocking.
	var events []WaitEvent
	for _, wt := range watches {
		events = append(events, w.step(ctx, wt)...)
	}
	now := w.env.now()
	for _, wt := range watches {
		wt.next = now // a local space's first poll is immediate
		if !wt.local {
			wt.next = now.Add(wt.interval) // the read verb just refreshed this mirror
		}
	}

	for len(events) == 0 {
		due := earliest(watches)
		if wait := due.Sub(w.env.now()); wait > 0 {
			if err := w.env.sleep(ctx, wait); err != nil {
				return w.timedOut(watches, err)
			}
		}
		now = w.env.now()
		for _, wt := range watches {
			if wt.next.After(now) {
				continue
			}
			events = append(events, w.poll(ctx, wt)...)
			if ctx.Err() != nil {
				return w.timedOut(watches, ctx.Err())
			}
		}
	}

	// One exit reports everything that has already landed locally: judge every
	// space whose mirror main moved once more, without fetching.
	events = w.collectLanded(ctx, watches, events)
	w.persist(watches, false)
	return WaitResult{Events: events, Unmeasured: unmeasured(watches)}, nil
}

// collectLanded appends, for every space, whatever its mirror has already
// landed past what was judged. Events are kept in space order, then landing
// order (ULID) within a space.
func (w *Waiter) collectLanded(ctx context.Context, watches []*spaceWatch, events []WaitEvent) []WaitEvent {
	for _, wt := range watches {
		events = append(events, w.step(ctx, wt)...)
	}
	order := map[string]int{}
	for i, wt := range watches {
		order[wt.sm.SpaceID] = i
	}
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if order[a.Space] != order[b.Space] {
			return order[a.Space] < order[b.Space]
		}
		return a.Event < b.Event
	})
	return events
}

// timedOut ends the wait because ctx did. A deadline is the normal end of a
// wait; any other cancellation is the caller's and comes back as an error.
func (w *Waiter) timedOut(watches []*spaceWatch, cause error) (WaitResult, error) {
	if !errors.Is(cause, context.DeadlineExceeded) {
		return WaitResult{}, fmt.Errorf("cache: wait: %w", cause)
	}
	w.persist(watches, true)
	return WaitResult{TimedOut: true, Unmeasured: unmeasured(watches)}, nil
}

func unmeasured(watches []*spaceWatch) []string {
	out := []string{}
	for _, wt := range watches {
		if wt.failed {
			out = append(out, wt.sm.SpaceID)
		}
	}
	return out
}

func earliest(watches []*spaceWatch) time.Time {
	t := watches[0].next
	for _, wt := range watches[1:] {
		if wt.next.Before(t) {
			t = wt.next
		}
	}
	return t
}

// selectSpaces resolves the requested ids against the Store's spaces, in
// configuration order.
func (w *Waiter) selectSpaces(ids []string) ([]*spaceWatch, error) {
	all := w.store.spaceMirrorsSnapshot()
	if len(all) == 0 {
		return nil, ErrWaitNoSpaces
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []*spaceWatch
	for i, sm := range all {
		if len(want) > 0 && !want[sm.SpaceID] {
			continue
		}
		delete(want, sm.SpaceID)
		out = append(out, &spaceWatch{
			idx: i, sm: sm, interval: readRefreshTTL,
			cursorPath: filepath.Join(w.store.cacheDir, "wait", waitPathSafe(sm.SpaceID)+"."+w.req.Selector.cursorKey()+".cursor"),
		})
	}
	for id := range want {
		return nil, fmt.Errorf("%w: %q", ErrWaitUnknownSpace, id)
	}
	return out, nil
}

// start establishes one space's baseline: its kind, its mirror's main, and the
// cursor. It is the only place a failure is an error rather than an
// observation that failed: with no mirror there is nothing to baseline from.
func (w *Waiter) start(ctx context.Context, wt *spaceWatch) error {
	gitDir, local, err := w.env.localKind(ctx, wt.sm.RepoURL)
	if err != nil {
		return fmt.Errorf("cache: wait: space %s: %w", wt.sm.SpaceID, err)
	}
	wt.gitDir, wt.local = gitDir, local
	if local {
		wt.interval = waitLocalPollInterval
	}
	head, err := w.env.mirrorHead(ctx, wt.sm.Dir)
	if err != nil {
		return fmt.Errorf("cache: wait: space %s: read the mirror's main at %s: %w", wt.sm.SpaceID, wt.sm.Dir, err)
	}
	cursor, have := readWaitCursor(wt.cursorPath)
	switch {
	case !have:
		// No cursor: the baseline is the current main. Old history is the
		// session-start inbox's business, not a flood from `wait`. It is
		// written now, so a wait killed before it exits still leaves a baseline.
		wt.judged = head
		w.saveCursor(wt)
	case cursor == head:
		wt.judged = head
	default:
		ancestor, aerr := w.env.isAncestor(ctx, wt.sm.Dir, cursor, head)
		switch {
		case aerr != nil:
			// Could not tell: judge from the cursor on every poll until it can.
			wt.judged = cursor
			wt.failed = true
		case !ancestor:
			w.req.Notify(fmt.Sprintf("wait: space %s: cursor %s is not an ancestor of main %s (a re-created space or a moved mirror); taking main as the baseline",
				wt.sm.SpaceID, short(cursor), short(head)))
			wt.judged = head
			w.saveCursor(wt)
		default:
			wt.judged = cursor // behind main: step() judges cursor..main
		}
	}
	return nil
}

// poll makes one observation of one space and schedules the next.
func (w *Waiter) poll(ctx context.Context, wt *spaceWatch) []WaitEvent {
	var events []WaitEvent
	changed := false
	if wt.local {
		ref, err := w.env.localRef(ctx, wt.gitDir)
		switch {
		case err != nil:
			w.observeFailed(ctx, wt)
		case ref == wt.judged:
			wt.failed = false // a successful look that found nothing new
		default:
			events, changed = w.fetchAndStep(ctx, wt)
		}
	} else {
		events, changed = w.fetchAndStep(ctx, wt)
	}
	w.schedule(wt, changed)
	return events
}

// fetchAndStep refreshes the mirror, then judges whatever main moved past.
func (w *Waiter) fetchAndStep(ctx context.Context, wt *spaceWatch) (events []WaitEvent, changed bool) {
	if err := w.store.cloneOrFetch(ctx, wt.sm.Dir, wt.sm.RepoURL, wt.sm.Credential); err != nil {
		w.observeFailed(ctx, wt)
		return nil, false
	}
	w.store.refreshManifestAfterFetch(wt.idx)
	before := wt.judged
	events = w.step(ctx, wt)
	return events, wt.judged != before
}

// observeFailed marks the latest observation failed, unless the failure is the
// wait's own deadline cutting the call short: that is the timeout, not an
// unreadable space.
func (w *Waiter) observeFailed(ctx context.Context, wt *spaceWatch) {
	if ctx.Err() != nil {
		return
	}
	wt.failed = true
}

// schedule sets the next poll. A local space polls every 2 s. A hosted space
// starts at readRefreshTTL, doubles after every poll that saw no change or
// failed (capped), and resets after a change.
func (w *Waiter) schedule(wt *spaceWatch, changed bool) {
	if !wt.local {
		if changed {
			wt.interval = readRefreshTTL
		} else {
			wt.interval = min(wt.interval*2, waitHostedMaxInterval)
		}
	}
	wt.next = w.env.now().Add(wt.interval)
}

// step judges judged..main without fetching. A range that could not be judged
// leaves judged where it was, so the next step retries it.
func (w *Waiter) step(ctx context.Context, wt *spaceWatch) []WaitEvent {
	head, err := w.env.mirrorHead(ctx, wt.sm.Dir)
	if err != nil {
		w.observeFailed(ctx, wt)
		return nil
	}
	if head == wt.judged {
		wt.failed = false
		return nil
	}
	got, err := w.env.judge(ctx, wt, wt.judged, head)
	if err != nil {
		w.observeFailed(ctx, wt)
		return nil
	}
	wt.judged = head
	wt.failed = got.Degraded
	return got.Events
}

// persist writes every space's cursor at the end of a wait. After a timeout a
// space whose latest observation failed keeps its old cursor: "nothing landed"
// was not measured there.
func (w *Waiter) persist(watches []*spaceWatch, timedOut bool) {
	for _, wt := range watches {
		if timedOut && wt.failed {
			continue
		}
		w.saveCursor(wt)
	}
}

// saveCursor writes wt.judged by temp file and rename. A cursor is disposable
// (D-001): a failed write costs at most one duplicate wake, so it is reported
// and the wait carries on.
func (w *Waiter) saveCursor(wt *spaceWatch) {
	if err := writeWaitCursor(wt.cursorPath, wt.judged); err != nil {
		w.req.Notify(fmt.Sprintf("wait: space %s: cursor not saved: %v", wt.sm.SpaceID, err))
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// readWaitCursor returns the saved sha. A missing or malformed file is "no
// cursor": the cursor is disposable cache state, never an error.
func readWaitCursor(path string) (string, bool) {
	raw, err := readBounded(path, waitCursorMaxBytes)
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(string(raw))
	if !isHexSHA(sha) {
		return "", false
	}
	return sha, true
}

func isHexSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func writeWaitCursor(path, sha string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cursor-*")
	if err != nil {
		return err
	}
	_, werr := tmp.WriteString(sha + "\n")
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name()) // reason: best-effort cleanup of a temp file whose write already failed
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name()) // reason: best-effort cleanup of a temp file that never became the cursor
		return err
	}
	return nil
}

// refuseLand is the land check of a host opened only to be classified: a wait
// reads, it never lands a write.
func refuseLand(context.Context, host.LandCandidate) (host.LandVerdict, error) {
	return host.LandVerdict{}, errors.New("wait never lands a write")
}

// localSpaceKind classifies repoURL through host.Open, the one place a space's
// kind is decided: a space whose host runs on this machine is read from its
// bare repository's ref with no network. A repository whose kind could not be
// told is an error, never "hosted" (anti-pattern 22).
func localSpaceKind(_ context.Context, repoURL string) (string, bool, error) {
	loc, err := host.ParseLocator(repoURL)
	if err != nil {
		return "", false, err
	}
	h, err := host.Open(loc, host.OpenConfig{LandCheck: refuseLand})
	if err != nil {
		return "", false, err
	}
	if !h.Capabilities().OnThisMachine {
		return "", false, nil
	}
	return strings.TrimPrefix(repoURL, "file://"), true, nil
}

// readLocalRef reads refs/heads/<base> from the bare repository: one local
// git call, no fetch.
func readLocalRef(ctx context.Context, gitDir string) (string, error) {
	out, err := runGitOutput(ctx, "", "--git-dir="+gitDir, "rev-parse", "--verify", "-q", "refs/heads/"+space.LocalSpaceBaseBranch)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// readMirrorHead is the mirror's remote-tracking main: the remote's default
// branch as the last fetch saw it, falling back to origin/main.
func readMirrorHead(ctx context.Context, dir string) (string, error) {
	var firstErr error
	for _, ref := range []string{"refs/remotes/origin/HEAD", "refs/remotes/origin/" + space.LocalSpaceBaseBranch} {
		out, err := runGitOutput(ctx, dir, "rev-parse", "--verify", "-q", ref)
		if err == nil {
			return strings.TrimSpace(out), nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", firstErr
}

// gitIsAncestor reports whether ancestor is an ancestor of head. A commit the
// mirror does not have is "not an ancestor" (a re-created space); any other
// failure is an error, and the two are never merged.
func gitIsAncestor(ctx context.Context, dir, ancestor, head string) (bool, error) {
	if _, err := runGitOutput(ctx, dir, "rev-parse", "--verify", "-q", ancestor+"^{commit}"); err != nil {
		if gitExitCode(err) == 1 {
			return false, nil
		}
		return false, err
	}
	_, err := runGitOutput(ctx, dir, "merge-base", "--is-ancestor", ancestor, head)
	if err == nil {
		return true, nil
	}
	if gitExitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// gitExitCode returns git's exit status carried by err, or -1.
func gitExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// isEventPath reports whether a repo-relative path has the event layout
// `<system>/events/<year>/<ulid>.yaml`.
func isEventPath(p string) bool {
	parts := strings.Split(p, "/")
	return len(parts) == 4 && parts[1] == "events" && strings.HasSuffix(parts[3], ".yaml") &&
		parts[0] != "" && parts[2] != "" && parts[3] != ".yaml"
}

// waitTransitionShape is the form every transition name takes, and
// waitEventIDShape the form of an event id; a value outside them is prose, not
// an identifier, and is never printed.
var (
	waitTransitionShape = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	waitEventIDShape    = regexp.MustCompile(`^[0-9A-Za-z]+$`)
)

// waitEventMatchesPath reports whether a decoded event at p (an isEventPath
// path) is the event its path says it is: written by the path's system, named
// by its file, and carrying only identifier-shaped fields. The same parity the
// space funnel checks before it trusts an event.
func waitEventMatchesPath(ev eventProbe, p string) bool {
	owner, _, _ := strings.Cut(p, "/")
	if ev.Actor.System != owner || ev.Event != strings.TrimSuffix(path.Base(p), ".yaml") {
		return false
	}
	if !waitEventIDShape.MatchString(ev.Event) {
		return false
	}
	if _, err := artifact.ParseID(ev.Subject); err != nil {
		return false
	}
	if !waitTransitionShape.MatchString(ev.Transition) {
		return false
	}
	_, err := time.Parse(time.RFC3339, ev.At)
	return err == nil
}

// addedEventPaths lists the event files added between from and to. A path the
// diff reports as modified or deleted is not an event landing (history is
// append-only) and is not listed. Renames are switched off so a re-filed event
// still counts as added.
func addedEventPaths(ctx context.Context, dir, from, to string) ([]string, error) {
	out, err := runGitOutput(ctx, dir, "diff", "--name-only", "-z", "--diff-filter=A", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if isEventPath(p) {
			paths = append(paths, p)
		}
	}
	sort.Slice(paths, func(i, j int) bool { return filepath.Base(paths[i]) < filepath.Base(paths[j]) })
	return paths, nil
}

// judgeRange reads the events added in from..to from the mirror's working tree
// (which a fetch just moved onto `to`) and applies the rule to those written by
// other systems. An undecodable event file is reported with its path and makes
// the observation failed; it is never skipped silently and never judged "not
// mine".
func (w *Waiter) judgeRange(ctx context.Context, wt *spaceWatch, from, to string) (judgement, error) {
	paths, err := addedEventPaths(ctx, wt.sm.Dir, from, to)
	if err != nil {
		return judgement{}, err
	}
	me := w.store.ownSystem
	var got judgement
	var candidates []waitCandidate
	for _, p := range paths {
		raw, rerr := readBounded(filepath.Join(wt.sm.Dir, filepath.FromSlash(p)), maxCacheReadBytes)
		if rerr != nil {
			w.req.Notify(fmt.Sprintf("wait: space %s: event file %s unreadable: %v", wt.sm.SpaceID, p, rerr))
			got.Degraded = true
			continue
		}
		ev, derr := decodeEvent(raw)
		if derr != nil || ev.Event == "" {
			w.req.Notify(fmt.Sprintf("wait: space %s: event file %s is undecodable; it could not be judged", wt.sm.SpaceID, p))
			got.Degraded = true
			continue
		}
		// The path is the authenticated identity (a space gates who writes
		// under <system>/); the body's claims are only data. A body that
		// disagrees with its path, or a field that is not identifier-shaped,
		// is judged like an undecodable file: the path is named, the value is
		// never echoed (D-014).
		if !waitEventMatchesPath(ev, p) {
			w.req.Notify(fmt.Sprintf("wait: space %s: event file %s does not match its path or carries a malformed field; it could not be judged", wt.sm.SpaceID, p))
			got.Degraded = true
			continue
		}
		if ev.Actor.System == me {
			continue
		}
		candidates = append(candidates, waitCandidate{
			Space: wt.sm.SpaceID, ULID: ev.Event, ActorSystem: ev.Actor.System, Transition: fold.Transition(ev.Transition),
			Subject: ev.Subject, At: ev.At, Path: p,
		})
	}
	if len(candidates) == 0 {
		return got, nil // nothing of another system's to judge, nothing to read
	}
	return w.selectWithIndex(ctx, wt, candidates, got)
}

// selectWithIndex builds the space's read model once and runs the rule.
func (w *Waiter) selectWithIndex(ctx context.Context, wt *spaceWatch, candidates []waitCandidate, got judgement) (judgement, error) {
	manifest := w.store.spaceMirrorsSnapshot()[wt.idx].Manifest
	folded, _, err := buildIndex(ctx, wt.sm.SpaceID, wt.sm.Dir, w.store.ownSystem, manifest)
	if err != nil {
		return judgement{}, err
	}
	artifacts := make(map[string]foldedArtifact, len(folded))
	for _, fa := range folded {
		artifacts[fa.Env.ID] = fa
	}
	consumes, skip := myDependencyContracts(wt.sm.Dir, w.store.ownSystem)
	if skip != nil {
		w.req.Notify(fmt.Sprintf("wait: space %s: %s could not be read (%s); the consumes reason cannot be judged", wt.sm.SpaceID, skip.Path, skip.Reason))
		got.Degraded = true
	}
	got.Events = selectWaitEvents(w.store.ownSystem, consumes, w.req.Selector, candidates, artifacts)
	for _, e := range got.Events {
		if e.Why == WhyUnresolved {
			w.req.Notify(fmt.Sprintf("wait: space %s: event %s names subject %s, which is not in the mirror; it is reported as unresolved",
				wt.sm.SpaceID, e.Event, e.Subject))
		}
	}
	return got, nil
}
