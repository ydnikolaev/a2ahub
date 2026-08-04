package html

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
	"github.com/ydnikolaev/a2ahub/releasenotes"
)

// demoJSON is the committed demo fixture (testdata/demo.json) — a
// deterministic model covering every type, state, drift, severity, and corner
// case the dashboard renders. Embedded so `a2a html --demo` renders a rich page
// with NO connected space, for design iteration + screenshots.
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
	input := operational.Input{
		Sources: []operational.SourceEvidence{
			{Kind: operational.SourceSpace, Space: "archive-migration", Revision: "unavailable", SyncedAt: timePointer(now.Add(-24 * time.Hour)), ObservedAt: now, Freshness: operational.SourceUnavailable},
			{Kind: operational.SourceSpace, Space: "checkout-core", Revision: "demo-checkout-revision", SyncedAt: &syncedAt, ObservedAt: now, Freshness: operational.SourceCurrent},
			{Kind: operational.SourceSpace, Space: "customer-ops", Revision: "demo-customer-ops-revision", SyncedAt: timePointer(now.Add(-45 * time.Minute)), ObservedAt: now, Freshness: operational.SourceStale},
			{Kind: operational.SourceLocalWork, Revision: "sha256:demo-local-work", ObservedAt: now, Freshness: operational.SourceCurrent},
		},
		Threads:       threads,
		CommittedWork: committedWork,
		LocalLeases:   []operational.LocalLeaseEvidence{{Lease: localLease, ObservedAt: now.Add(-20 * time.Second)}},
		Unavailable: []operational.Unavailable{{
			SourceKind: operational.SourceSpace, Space: "archive-migration", Code: "space-index-unavailable",
			Summary: "Committed operational evidence is unavailable for this space",
		}},
	}
	return operational.Build(input, demoOperationalClock{now: now}, operational.DefaultLimits())
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
