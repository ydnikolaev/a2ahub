package html

import (
	_ "embed"
	"encoding/json"
	"fmt"
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
	if err := json.Unmarshal(demoJSON, &d); err != nil {
		return Data{}, fmt.Errorf("html: demo data: %w", err)
	}
	return d, nil
}
