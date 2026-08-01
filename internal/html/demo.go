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
	for i := range d.ArtifactDetails {
		rendered, renderErr := renderArtifactMarkdown(d.ArtifactDetails[i].Body)
		if renderErr != nil {
			return Data{}, fmt.Errorf("html: demo artifact detail %s Markdown: %w", d.ArtifactDetails[i].ID, renderErr)
		}
		d.ArtifactDetails[i].BodyHTML = rendered
	}
	return d, nil
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
	for _, list := range [][]Item{d.Inbox, d.Outbox} {
		for i := range list {
			it := &list[i]
			it.MovedAt = shiftBack(d.GeneratedAt, it.Age)
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
