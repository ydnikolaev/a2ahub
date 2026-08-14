package html

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
	"github.com/ydnikolaev/a2ahub/releasenotes"
)

// demoJSON is the committed synthetic breadth fixture (testdata/demo.json).
// TestDemoFixtureCoversBinaryVocabulary keeps every fixture-backed catalogue
// value present, while TestDemoFixtureExercisesHonestBounds proves the work,
// consistency, and preview limits with real overflow. Embedded so
// `a2a html --demo` renders a rich page with NO connected space, for design
// iteration and screenshots; it is not a substitute for an axon truth render.
//
//go:embed testdata/demo.json
var demoJSON []byte

// DemoData returns the embedded demo model (`a2a html --demo`).
func DemoData() (Data, error) {
	var d Data
	decoder := json.NewDecoder(bytes.NewReader(demoJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return Data{}, fmt.Errorf("html: demo data: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Data{}, fmt.Errorf("html: demo data: trailing content")
	}
	d.Vocabulary = DashboardVocabulary()
	releases, err := notes.Load(releasenotes.FS)
	if err != nil {
		return Data{}, fmt.Errorf("html: demo release notes: %w", err)
	}
	currentIssues, err := notes.LoadCurrentKnownIssues(releasenotes.FS)
	if err != nil {
		return Data{}, fmt.Errorf("html: demo current known issues: %w", err)
	}
	releases = notes.AttachCurrentKnownIssues(releases, releases, currentIssues)
	d.ReleaseNotes = toReleaseNotes(releases)
	deriveDemoOwnership(&d)
	deriveDemoRowFacts(&d)
	deriveDemoDerivedState(&d)
	if err := deriveDemoPreviewBound(&d); err != nil {
		return Data{}, fmt.Errorf("html: demo preview bound: %w", err)
	}
	authoredOperational := d.Operational
	d.Operational, err = demoOperationalSnapshot(d)
	if err != nil {
		return Data{}, fmt.Errorf("html: demo operational snapshot: %w", err)
	}
	if authoredOperational.SchemaVersion == 0 {
		return Data{}, fmt.Errorf("html: demo operational snapshot: fixture projection is missing")
	}
	authoredJSON, err := operational.CanonicalJSON(authoredOperational)
	if err != nil {
		return Data{}, fmt.Errorf("html: demo operational snapshot: encode fixture projection: %w", err)
	}
	projectedJSON, err := operational.CanonicalJSON(d.Operational)
	if err != nil {
		return Data{}, fmt.Errorf("html: demo operational snapshot: encode rebuilt projection: %w", err)
	}
	if !bytes.Equal(authoredJSON, projectedJSON) {
		return Data{}, fmt.Errorf("html: demo operational snapshot: fixture projection drifted from demo evidence")
	}
	for i := range d.ArtifactDetails {
		rendered, renderErr := renderArtifactMarkdown(d.ArtifactDetails[i].Body)
		if renderErr != nil {
			return Data{}, fmt.Errorf("html: demo artifact detail %s Markdown: %w", d.ArtifactDetails[i].ID, renderErr)
		}
		d.ArtifactDetails[i].BodyHTML = rendered
	}
	return d, nil
}

type demoOperationalClock struct{ now time.Time }

func (clock demoOperationalClock) Now() time.Time { return clock.now }

func demoOperationalSnapshot(d Data) (operational.Snapshot, error) {
	now := d.GeneratedAt.UTC()
	syncedAt := now.Add(-2 * time.Minute)
	actor := func(name, system, session string) workreport.Actor {
		return workreport.Actor{Kind: "agent", Name: name, System: system, Model: "gpt-5", Session: session}
	}
	threads := demoOperationalThreads(d)
	committedWork, err := demoCommittedWork(d.WorkReports)
	if err != nil {
		return operational.Snapshot{}, fmt.Errorf("committed work reports: %w", err)
	}
	localLease := demoLocalLease(now, workreport.Identity{
		LeaseKey:  "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ProjectID: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		Space:     "checkout-core", Thread: "thread:checkout-20260728-c3d4",
		WorkID: "work:01K20ABCDEFHJKMNPQRSTVWXYZ", Actor: actor("codex", "atlas", "session:demo-atlas"),
	}, "XW-checkout-20260728-c3d4", workreport.ModeImplementing,
		"Implementing the idempotent capture fix and replay fixture", nil)
	closingLease, closingPending, err := demoClosingLocalLease(now, actor("recovery-bot", "atlas", "session:demo-recovery"))
	if err != nil {
		return operational.Snapshot{}, fmt.Errorf("closing local lease: %w", err)
	}
	boundedLeases := demoBoundedLocalLeases(now)
	localLeases := make([]operational.LocalLeaseEvidence, 0, len(boundedLeases)+2)
	localLeases = append(localLeases,
		operational.LocalLeaseEvidence{Lease: localLease, ObservedAt: now.Add(-20 * time.Second)},
		operational.LocalLeaseEvidence{Lease: closingLease, ObservedAt: now.Add(-15 * time.Second), Pending: closingPending},
	)
	localLeases = append(localLeases, boundedLeases...)
	input := operational.Input{
		Sources: []operational.SourceEvidence{
			{Kind: operational.SourceSpace, Space: "archive-migration", Revision: "unavailable", SyncedAt: timePointer(now.Add(-24 * time.Hour)), ObservedAt: now, Freshness: operational.SourceUnavailable},
			{Kind: operational.SourceSpace, Space: "checkout-core", Revision: "demo-checkout-revision", SyncedAt: &syncedAt, ObservedAt: now, Freshness: operational.SourceCurrent},
			{Kind: operational.SourceSpace, Space: "customer-ops", Revision: "demo-customer-ops-revision", SyncedAt: timePointer(now.Add(-45 * time.Minute)), ObservedAt: now, Freshness: operational.SourceStale},
			{Kind: operational.SourceLocalWork, Revision: "sha256:demo-local-work", ObservedAt: now, Freshness: operational.SourceDegraded},
		},
		Threads:       threads,
		CommittedWork: committedWork,
		LocalLeases:   localLeases,
		Unavailable: []operational.Unavailable{{
			SourceKind: operational.SourceSpace, Space: "archive-migration", Code: "space-index-unavailable",
			Summary: "Committed operational evidence is unavailable for this space",
		}, {
			SourceKind: operational.SourceLocalWork, Code: "local-work-scan-degraded",
			Summary: "One malformed local lease was omitted while the remaining work evidence stayed readable",
		}},
	}
	return operational.Build(input, demoOperationalClock{now: now}, operational.DefaultLimits())
}

func demoBoundedLocalLeases(now time.Time) []operational.LocalLeaseEvidence {
	limits := operational.DefaultLimits()
	evidence := make([]operational.LocalLeaseEvidence, 0, limits.ConsistencyPerRow+1)
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	for index := 0; index <= limits.ConsistencyPerRow; index++ {
		workID := "work:01K20ABCDEFHJKMNPQRSTVWX" + string(alphabet[index/len(alphabet)]) + string(alphabet[index%len(alphabet)])
		lease := demoLocalLease(now, workreport.Identity{
			LeaseKey:  fmt.Sprintf("sha256:%064x", index+10),
			ProjectID: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
			Space:     "checkout-core", Thread: "thread:checkout-20260728-c3d4", WorkID: workID,
			Actor: workreport.Actor{
				Kind: "agent", Name: fmt.Sprintf("replay-shard-%02d", index+1), System: "checkout",
				Model: "gpt-5", Session: fmt.Sprintf("session:demo-replay-%02d", index+1),
			},
		}, "XW-checkout-20260728-c3d4", workreport.ModeTesting,
			fmt.Sprintf("Replaying capture shard %02d against the idempotency fixture", index+1), nil)
		lease.StartedAt = now.Add(-2 * time.Hour)
		lease.RenewedAt = now.Add(-90 * time.Minute)
		lease.ExpiresAt = now.Add(-75 * time.Minute)
		evidence = append(evidence, operational.LocalLeaseEvidence{Lease: lease, ObservedAt: now.Add(-10 * time.Second)})
	}
	return evidence
}

func demoClosingLocalLease(now time.Time, actor workreport.Actor) (workreport.Lease, *operational.SharedPending, error) {
	const (
		workID      = "work:01K20ABCDEFHJKMNPQRSTVWXYH"
		operationID = "op-v1-40366a4f27dd08fc05488fc8f4b2a613cfed0b8a0cd3134da7d005241a9ab6b6"
	)
	prepared, err := workreport.NewPreparedJournal([]byte(`{"demo":"closing-local-lease"}`))
	if err != nil {
		return workreport.Lease{}, nil, err
	}
	lease := demoLocalLease(now, workreport.Identity{
		LeaseKey:  "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		ProjectID: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		Space:     "checkout-core", Thread: "thread:checkout-20260728-c3d4", WorkID: workID, Actor: actor,
	}, "XW-checkout-20260728-c3d4", workreport.ModePaused,
		"Pausing replay publication until the interrupted stop operation is recovered", nil)
	lease.Closing = true
	lease.SemanticSequence = 2
	lease.Pending = &workreport.PendingOperation{
		OperationKey: operationID, Action: workreport.ActionStop,
		ArtifactID: "XA-atlas-20260803-recovery", EventID: "01K1A2B3C4D5E6F7G8H9J0K1M3",
		SemanticSequence: 2, Prepared: prepared, LocalTarget: workreport.TargetClosing,
	}
	pending := &operational.SharedPending{
		OperationID: operationID, Action: "stop", Stage: "committed", RemainingAction: "retry-push",
	}
	return lease, pending, nil
}

func deriveDemoPreviewBound(d *Data) error {
	full := []byte(strings.Repeat("{\"event\":\"capture-replayed\",\"idempotency_key\":\"provider-order-key\"}\n", 48))
	for contractIndex := range d.Contracts {
		for versionIndex := range d.Contracts[contractIndex].Versions {
			version := &d.Contracts[contractIndex].Versions[versionIndex]
			if version.Detail == nil || version.Detail.Status != ContractVersionDetailAvailable {
				continue
			}
			for documentIndex := range version.Detail.Documents {
				document := &version.Detail.Documents[documentIndex]
				if document.PreviewLanguage != "json" {
					continue
				}
				document.Digest = artifact.Digest(full)
				document.SizeBytes = int64(len(full))
				document.Preview = contractVersionPreview(full)
				document.ShownBytes = int64(len(document.Preview))
				document.TotalBytes = document.SizeBytes
				document.Truncated = document.ShownBytes < document.TotalBytes
				return nil
			}
		}
	}
	return fmt.Errorf("no available JSON contract document")
}

// demoCommittedWork converts the fixture's durable history into the shared
// operational projection input. WorkReports are the canonical authored demo
// evidence; the operational snapshot must not repeat their semantic fields.
func demoCommittedWork(reports []WorkReport) ([]operational.CommittedWorkEvidence, error) {
	evidence := make([]operational.CommittedWorkEvidence, 0, len(reports))
	for _, report := range reports {
		reportedAt, err := time.Parse(time.RFC3339Nano, report.ReportedAt)
		if err != nil {
			return nil, fmt.Errorf("report %s reported_at %q: %w", report.ArtifactID, report.ReportedAt, err)
		}
		var validUntil time.Time
		if report.ValidUntil != "" {
			validUntil, err = time.Parse(time.RFC3339Nano, report.ValidUntil)
			if err != nil {
				return nil, fmt.Errorf("report %s valid_until %q: %w", report.ArtifactID, report.ValidUntil, err)
			}
		}
		waitingOn := make([]workreport.WaitingOn, len(report.WaitingOn))
		for i, waiting := range report.WaitingOn {
			waitingOn[i] = workreport.WaitingOn{
				Kind: workreport.WaitKind(waiting.Kind), ID: waiting.ID, Summary: waiting.Summary,
			}
		}
		evidence = append(evidence, operational.CommittedWorkEvidence{
			Space: report.Space, Thread: report.Thread, WorkID: report.WorkID, SubjectRef: report.SubjectRef,
			Mode: workreport.Mode(report.Mode), Summary: report.Summary,
			Actor: workreport.Actor{
				Kind: report.Actor.Kind, Name: report.Actor.Name, System: report.Actor.System,
				Model: report.Actor.Model, Session: report.Actor.Session,
			},
			WaitingOn: waitingOn, ReportedAt: reportedAt, ValidUntil: validUntil,
			ArtifactID: report.ArtifactID, CommitSequence: report.CommitSequence,
		})
	}
	return evidence, nil
}

func demoOperationalThreads(d Data) []operational.ThreadEvidence {
	threads := make([]operational.ThreadEvidence, 0, len(d.ThreadViews))
	for _, view := range d.ThreadViews {
		waiting, blocking := []string{}, []string{}
		openCount, yourMove := 0, false
		for _, item := range view.OpenItems {
			if len(item.WaitingOn) == 0 {
				continue
			}
			openCount++
			waiting = append(waiting, item.WaitingOn...)
			yourMove = yourMove || item.YourMove
			if item.Blocking {
				blocking = append(blocking, item.WaitingOn...)
			}
		}
		threads = append(threads, operational.ThreadEvidence{
			Space: view.Space, Thread: view.Thread, Title: view.Opener.Title,
			Participants: append([]string(nil), view.Participants...),
			Protocol: operational.Protocol{
				Settled: openCount == 0, OpenCount: openCount,
				WaitingOn: sortedUnique(waiting), YourMove: yourMove, BlockingBy: sortedUnique(blocking),
			},
			LatestMilestone: demoLatestMilestone(view),
		})
	}
	return threads
}

func demoLatestMilestone(view ThreadView) *operational.Milestone {
	for index := len(view.Transcript) - 1; index >= 0; index-- {
		row := view.Transcript[index]
		if row.Kind != "event" || row.Event == nil || row.At == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, row.At)
		if err != nil {
			continue
		}
		actor := row.Event.Actor
		name, _ := actor["name"].(string)
		system, _ := actor["system"].(string)
		kind, _ := actor["kind"].(string)
		model, _ := actor["model"].(string)
		session, _ := actor["session"].(string)
		if kind == "" {
			kind = "agent"
		}
		if name == "" || system == "" {
			continue
		}
		if session == "" {
			session = "session:demo-" + system
		}
		return &operational.Milestone{
			Kind: "event", At: at.UTC(), Actor: operational.Actor{
				Kind: kind, Name: name, System: system, Model: model, Session: session,
			},
			Transition: row.Event.Transition, Subject: row.Event.Subject,
		}
	}
	return nil
}

func demoLocalLease(now time.Time, identity workreport.Identity, subject string, mode workreport.Mode, summary string, waiting []workreport.WaitingOn) workreport.Lease {
	return workreport.Lease{
		SchemaVersion: workreport.SchemaVersion, Identity: identity, SubjectRef: subject,
		Mode: mode, Summary: summary, WaitingOn: waiting,
		Recipients: []string{"all"}, Classification: workreport.DefaultClassification,
		StartedAt: now.Add(-25 * time.Minute), RenewedAt: now.Add(-40 * time.Second), ExpiresAt: now.Add(14*time.Minute + 20*time.Second),
		HeartbeatSequence: 4, SemanticSequence: 2,
	}
}

func sortedUnique(values []string) []string {
	sort.Strings(values)
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return append([]string(nil), out...)
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

// deriveDemoRowFacts fills the two row fields the live assembler computes but
// the fixture does not state: the sort key behind the humanised age, and the
// agent prompt's facts.
//
// Both are derived rather than authored for the same reason Pending is: the
// fixture is hand-maintained, a field it predates decodes to its zero value
// without failing any gate, and a zeroed sort key looks plausible on screen
// while ordering the list at random.
func deriveDemoRowFacts(d *Data) {
	type openKey struct{ space, id string }
	author := map[openKey]string{}
	for _, tv := range d.ThreadViews {
		for _, a := range tv.Artifacts {
			author[openKey{tv.Space, a.ID}] = a.From
		}
	}
	open := map[openKey]ThreadOpenItem{}
	for i := range d.ThreadViews {
		tv := &d.ThreadViews[i]
		for j := range tv.OpenItems {
			oi := &tv.OpenItems[j]
			oi.Prompt = agentPromptOf(oi.NextActions, oi.Type, d.Self, author[openKey{tv.Space, oi.ID}] == d.Self)
			open[openKey{tv.Space, oi.ID}] = *oi
		}
	}
	for _, list := range [][]Item{d.Inbox, d.Outbox, d.Archive} {
		for i := range list {
			it := &list[i]
			// The synthetic fixture predates the explicit creation/activity split.
			// Its authored age describes document birth, so derive both clocks from
			// that one admitted demo fact without inventing Git order.
			it.CreatedAt = shiftBack(d.GeneratedAt, it.Age)
			it.MovedAt = it.CreatedAt
			key := openKey{it.Space, it.ID}
			oi, ok := open[key]
			if !ok {
				it.YourMove, it.Prompt = false, nil
				continue
			}
			// Same rule the cache applies: the move is ours when the folded
			// item still waits on us. Escape hatches are already out of
			// WaitingOn, so owning the only way to cancel something is not the
			// same as owing a move on it.
			it.YourMove = containsString(oi.WaitingOn, d.Self)
			it.Prompt = oi.Prompt
		}
	}
}

// shiftBack turns one of humanizeAge's own outputs ("5h", "1d", "just now")
// back into the instant it was formatted from, relative to the snapshot. It is
// the inverse of the only formatting the fixture's `age` field ever went
// through, so the fixture keeps stating one fact about a row's recency rather
// than two that can disagree.
func shiftBack(now time.Time, age string) string {
	if age == "" {
		return ""
	}
	if age == "just now" {
		return now.UTC().Format(time.RFC3339)
	}
	unit := age[len(age)-1:]
	n, err := strconv.Atoi(age[:len(age)-1])
	if err != nil || n < 0 {
		return ""
	}
	var step time.Duration
	switch unit {
	case "m":
		step = time.Minute
	case "h":
		step = time.Hour
	case "d":
		step = 24 * time.Hour
	case "w":
		step = 7 * 24 * time.Hour
	default:
		return ""
	}
	return now.Add(-time.Duration(n) * step).UTC().Format(time.RFC3339)
}

// deriveDemoOwnership recomputes the "who owes a move" fields from the facts the
// fixture actually states, instead of trusting the fixture to have restated
// them. The fixture is hand-maintained and predates both fields, so decoding it
// left every open item Pending:false — which the dashboard reads as "nothing is
// pending" and used to render a whose-move panel with an empty list under it.
//
// Same rule as the live assembler: an item whose WaitingOn is empty has only
// the owner's escape hatches left and owes nobody anything; every other name in
// WaitingOn that is not Self is somebody the thread is waiting on.
func deriveDemoOwnership(d *Data) {
	waitingByThread := map[string][]string{}
	for i := range d.ThreadViews {
		tv := &d.ThreadViews[i]
		for j := range tv.OpenItems {
			oi := &tv.OpenItems[j]
			oi.Pending = len(oi.WaitingOn) > 0
			if !oi.Pending {
				continue
			}
			for _, who := range oi.WaitingOn {
				if who == d.Self || containsString(waitingByThread[tv.Thread], who) {
					continue
				}
				waitingByThread[tv.Thread] = append(waitingByThread[tv.Thread], who)
			}
		}
	}
	for i := range d.Threads {
		others := waitingByThread[d.Threads[i].ID]
		sort.Strings(others)
		if others == nil {
			others = []string{}
		}
		d.Threads[i].WaitingOthers = others
	}
}

// deriveDemoDerivedState fills the five fields 0.19.10 put on every read
// surface — Outcome, Terminal, StateSince, StateBy, StateEvent — from the
// facts the fixture already states, for the third time in this file and for
// the third time for the same reason deriveDemoRowFacts states above: the
// fixture is hand-maintained, a field it predates decodes to its zero value
// without failing any gate, and the zero value looks plausible on screen.
//
// Here the zero value is not merely plausible, it is FALSE. `Terminal` has no
// `omitempty`, so a fixture that never mentions it serialises `terminal:false`
// on all 31 carriers — including a `closed` question and a `cancelled`
// work_request, which are terminal by the domain's own answer. The demo is
// what `a2a html --demo` renders and what web/public/demo-data.json ships to
// the site, so the release whose headline is "the domain says what a state
// means" had its own showcase asserting the opposite. Absence would have been
// a gap; `false` is a wrong claim.
//
// The two halves are derived differently, and deliberately:
//
//   - Outcome and Terminal are ASKED OF THE DOMAIN — fold.OutcomeOf and
//     fold.Terminal over (type, state), the same call internal/html/assemble.go
//     makes for a real artifact detail. Nothing here re-implements the answer,
//     so a fixture row can never disagree with the product about what its own
//     state means.
//
//   - StateSince, StateBy and StateEvent are READ OFF THE FIXTURE'S OWN
//     EVENTS. They are not a function of (type, state) — they name a specific
//     event — so there is nothing to ask the domain for. The rule mirrors the
//     question internal/cache/mirror.go asks (`next.State != result.State` →
//     that event authored the state, By = the actor's SYSTEM): walk an
//     artifact's events in fixture order and keep the last one whose claimed
//     state DIFFERS from the claimed state before it. A contract's three
//     `publish` rows therefore credit the first, not the third — the later two
//     moved nothing. The difference from mirror.go is the input, not the rule:
//     there a real fold runs over envelopes and events, here the fixture
//     asserts claimed_state directly and is the only thing there is to read.
//
// A FLAGGED event is skipped, and it is the half that a claim-sequence rule
// gets wrong on its own. mirror.go says why, at the line this derivation
// mirrors: "a state-moving transition that was illegal or unauthorized is
// flagged and changes nothing, and it must not claim authorship of a state it
// never moved." The fixture contains exactly that case on purpose —
// XH-legacycrm-20260709-x3y4 is `submitted` with an `acknowledge` event after
// it, by `unrelated-agent` of a system that is not the recipient, carrying the
// thread flag `unauthorized-actor`. Reading claims alone credits the
// acknowledgement with a state it never produced; the first run of
// TestDemoStateOriginNamesAnEventThatProducedThatState caught precisely that.
//
// Where the fixture states no state-moving event for a row, all three stay
// zero. They are omitempty/omitzero, so that renders as absence — which is
// what internal/cache/mirror.go itself returns for an artifact with no events
// ("an empty origin is the honest answer, and the read model renders it as
// absence rather than inventing a timestamp").
func deriveDemoDerivedState(d *Data) {
	origins := demoStateOrigins(d)

	for _, list := range [][]Item{d.Inbox, d.Outbox, d.Archive} {
		for i := range list {
			it := &list[i]
			it.Outcome = fold.OutcomeOf(fold.Kind(it.Type), fold.State(it.State))
			it.Terminal = fold.Terminal(fold.Kind(it.Type), fold.State(it.State))
			if origin, ok := origins[demoArtifactKey{it.Space, it.ID}]; ok {
				it.StateSince, it.StateBy, it.StateEvent = origin.at, origin.by, origin.event
			}
		}
	}
	for i := range d.ThreadViews {
		tv := &d.ThreadViews[i]
		for j := range tv.OpenItems {
			oi := &tv.OpenItems[j]
			oi.Outcome = fold.OutcomeOf(fold.Kind(oi.Type), fold.State(oi.State))
			oi.Terminal = fold.Terminal(fold.Kind(oi.Type), fold.State(oi.State))
			if origin, ok := origins[demoArtifactKey{tv.Space, oi.ID}]; ok {
				oi.StateSince, oi.StateBy, oi.StateEvent = origin.at, origin.by, origin.event
			}
		}
	}
	for i := range d.ArtifactDetails {
		ad := &d.ArtifactDetails[i]
		ad.Outcome = fold.OutcomeOf(fold.Kind(ad.Type), fold.State(ad.State))
		ad.Terminal = fold.Terminal(fold.Kind(ad.Type), fold.State(ad.State))
	}
}

// demoArtifactKey qualifies an artifact id by its space, because the fixture
// is deliberately two full spaces plus one unavailable one
// (TestDemoIsTwoFullSpacesPlusOneHonestUnavailableSpace) and an id is only
// unique inside its own.
type demoArtifactKey struct{ space, id string }

// demoStateOrigin is what one artifact's event history says about the event
// that produced its current state.
type demoStateOrigin struct {
	at    time.Time
	by    string
	event string
}

// demoStateOrigins indexes both places the fixture keeps events — a thread's
// transcript rows and an artifact detail's own list — under one key. They
// overlap: an artifact can appear in both, and when it does the two must not
// disagree, which TestDemoStateOriginsAgreeAcrossBothEventSources asserts
// rather than this function silently preferring one.
//
// An event whose `at` does not parse, or which claims no state at all, is
// skipped rather than guessed at: it cannot name an instant, and naming a
// wrong one is the failure mode this whole function exists to remove.
func demoStateOrigins(d *Data) map[demoArtifactKey]demoStateOrigin {
	type dated struct {
		at           time.Time
		by           string
		event        string
		claimedState string
		seq          int64
	}
	// Every event the fixture flags changed nothing — see this function's
	// caller for the case that proves it matters. Collected first, because a
	// flag lives on the thread while the event it names also appears in an
	// artifact detail's own list.
	flagged := map[string]bool{}
	for i := range d.ThreadViews {
		for _, f := range d.ThreadViews[i].Flags {
			if f.EventULID != "" {
				flagged[f.EventULID] = true
			}
		}
	}

	byArtifact := map[demoArtifactKey][]dated{}
	add := func(key demoArtifactKey, e dated) {
		if e.claimedState == "" || e.at.IsZero() || flagged[e.event] {
			return
		}
		byArtifact[key] = append(byArtifact[key], e)
	}

	for i := range d.ThreadViews {
		tv := &d.ThreadViews[i]
		for _, row := range tv.Transcript {
			if row.Kind != "event" || row.Event == nil {
				continue
			}
			at, err := time.Parse(time.RFC3339, row.At)
			if err != nil {
				continue
			}
			add(demoArtifactKey{tv.Space, row.Event.Subject}, dated{
				at: at, by: demoActorSystem(row.Event.Actor), event: row.Event.ULID,
				claimedState: row.Event.ClaimedState, seq: row.Seq,
			})
		}
	}
	for i := range d.ArtifactDetails {
		ad := &d.ArtifactDetails[i]
		for _, e := range ad.Events {
			at, err := time.Parse(time.RFC3339, e.At)
			if err != nil {
				continue
			}
			add(demoArtifactKey{ad.Space, e.Subject}, dated{
				at: at, by: e.ActorSystem, event: e.ULID, claimedState: e.ClaimedState,
			})
		}
	}

	origins := map[demoArtifactKey]demoStateOrigin{}
	for key, events := range byArtifact {
		// Fixture order is the commit order the fixture asserts; seq is
		// authoritative where a transcript row carries one, and the instant
		// breaks the tie for detail events, which carry no seq. The ULID is
		// the final tiebreak, exactly as internal/cache/mirror.go sorts.
		sort.SliceStable(events, func(i, j int) bool {
			if events[i].seq != events[j].seq {
				return events[i].seq < events[j].seq
			}
			if !events[i].at.Equal(events[j].at) {
				return events[i].at.Before(events[j].at)
			}
			return events[i].event < events[j].event
		})
		previous := ""
		var origin demoStateOrigin
		found := false
		for _, e := range events {
			if e.claimedState == previous {
				continue // moved nothing; it does not author the state
			}
			previous = e.claimedState
			origin, found = demoStateOrigin{at: e.at, by: e.by, event: e.event}, true
		}
		if found {
			origins[key] = origin
		}
	}
	return origins
}

// demoActorSystem pulls the actor's system out of a transcript event's
// untyped actor map. StateBy is the SYSTEM, not the agent name — mirroring
// internal/cache/mirror.go's `By: event.Actor.System` — because the question
// the field answers is which participant moved the artifact, and one system
// can drive several named agents.
func demoActorSystem(actor map[string]any) string {
	system, _ := actor["system"].(string)
	return system
}
