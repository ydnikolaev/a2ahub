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
	d.Operational, err = demoOperationalSnapshot(d.GeneratedAt)
	if err != nil {
		return Data{}, fmt.Errorf("html: demo operational snapshot: %w", err)
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

func demoOperationalSnapshot(now time.Time) (operational.Snapshot, error) {
	now = now.UTC()
	syncedAt := now.Add(-2 * time.Minute)
	thread := func(id, title string, protocol operational.Protocol, milestone *operational.Milestone) operational.ThreadEvidence {
		return operational.ThreadEvidence{
			Space: "customer-ops", Thread: id, Title: title,
			Participants: []string{"atlas", "legacycrm"}, Protocol: protocol, LatestMilestone: milestone,
		}
	}
	actor := func(name, system, session string) workreport.Actor {
		return workreport.Actor{Kind: "agent", Name: name, System: system, Model: "gpt-5", Session: session}
	}
	input := operational.Input{
		Sources: []operational.SourceEvidence{
			{Kind: operational.SourceSpace, Space: "customer-ops", Revision: "demo-space-revision", SyncedAt: &syncedAt, ObservedAt: now, Freshness: operational.SourceCurrent},
			{Kind: operational.SourceLocalWork, Revision: "sha256:demo-local-work", ObservedAt: now, Freshness: operational.SourceCurrent},
		},
		Threads: []operational.ThreadEvidence{
			thread("thread:legacycrm-20260709-x3y4", "Replace the legacy CRM export safely", operational.Protocol{
				Settled: false, OpenCount: 1, WaitingOn: []string{"legacycrm"}, BlockingBy: []string{"legacycrm"},
			}, &operational.Milestone{
				Kind: "event", At: now.Add(-8 * time.Minute), Actor: operational.Actor{Kind: "agent", Name: "codex", System: "atlas", Model: "gpt-5", Session: "session:demo-atlas"},
				Transition: "respond", Subject: "XW-legacycrm-20260709-x3y4",
			}),
			thread("thread:ingest-20260803-a2b3", "Implement the agreed ingest contract", operational.Protocol{
				Settled: true, OpenCount: 0, WaitingOn: []string{}, BlockingBy: []string{},
			}, &operational.Milestone{
				Kind: "event", At: now.Add(-25 * time.Minute), Actor: operational.Actor{Kind: "agent", Name: "xpressmike", System: "legacycrm", Model: "gpt-5", Session: "session:demo-mike"},
				Transition: "verify", Subject: "XS-ingest-20260803-c4d5",
			}),
			thread("thread:support-20260729-q5n6", "Redact message bodies before case events leave Support", operational.Protocol{
				Settled: false, OpenCount: 1, WaitingOn: []string{"atlas"}, YourMove: true, BlockingBy: []string{},
			}, nil),
		},
		CommittedWork: []operational.CommittedWorkEvidence{
			{
				Space: "customer-ops", Thread: "thread:legacycrm-20260709-x3y4", WorkID: "work:01K20ABCDEFHJKMNPQRSTVWXYZ",
				SubjectRef: "XC-legacycrm-export@2.0.0", Mode: workreport.ModeImplementing,
				Summary: "Replacing the export adapter and preparing migration fixtures",
				Actor:   actor("codex", "atlas", "session:demo-atlas"), ReportedAt: now.Add(-4 * time.Minute), ValidUntil: now.Add(20 * time.Minute),
				ArtifactID: "XA-atlas-20260803-a2b3", CommitSequence: 14,
			},
			{
				Space: "customer-ops", Thread: "thread:legacycrm-20260709-x3y4", WorkID: "work:01K20ABCDEFHJKMNPQRSTVWXYA",
				SubjectRef: "XW-legacycrm-20260709-x3y4", Mode: workreport.ModeWaiting,
				Summary:    "Preparing the provider-side export after the contract update",
				Actor:      actor("xpressmike", "legacycrm", "session:demo-mike"),
				WaitingOn:  []workreport.WaitingOn{{Kind: workreport.WaitSystem, ID: "atlas", Summary: "Final contract bytes"}},
				ReportedAt: now.Add(-6 * time.Minute), ValidUntil: now.Add(18 * time.Minute),
				ArtifactID: "XA-legacycrm-20260803-b3c4", CommitSequence: 15,
			},
			{
				Space: "customer-ops", Thread: "thread:ingest-20260803-a2b3", WorkID: "work:01K20ABCDEFHJKMNPQRSTVWXYB",
				SubjectRef: "XC-atlas-ingest@1.4.0", Mode: workreport.ModeTesting,
				Summary: "Running the cross-system ingest fixture suite",
				Actor:   actor("codex", "atlas", "session:demo-ingest"), ReportedAt: now.Add(-12 * time.Minute), ValidUntil: now.Add(30 * time.Minute),
				ArtifactID: "XA-atlas-20260803-c4d5", CommitSequence: 16,
			},
		},
		LocalLeases: []operational.LocalLeaseEvidence{}, Unavailable: []operational.Unavailable{},
	}
	return operational.Build(input, demoOperationalClock{now: now}, operational.DefaultLimits())
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
