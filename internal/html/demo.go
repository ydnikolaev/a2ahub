package html

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"

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
	return d, nil
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
