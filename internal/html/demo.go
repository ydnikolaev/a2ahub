package html

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"

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
	return d, nil
}
