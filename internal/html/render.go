package html

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed template.html
var placeholderTemplate []byte

// The designed template (from Claude Design) MUST carry exactly one DATA region
// delimited by these markers — its inline sample lives between them so the file
// previews standalone; Render replaces the whole region (markers + sample) with
// the real JSON, yielding `const DATA = <json>;`.
const (
	dataStart = "/*A2A_DATA_START*/"
	dataEnd   = "/*A2A_DATA_END*/"
)

// Render injects data as JSON into tmpl's DATA region and returns the
// self-contained HTML. json.MarshalIndent escapes <,>,& (SetEscapeHTML default),
// so the JSON is safe to embed in a <script> block — no </script> breakout even
// if an artifact title contains "</script>". A template missing the markers is
// an error (fail loud, never emit an un-injected page).
func Render(tmpl []byte, data Data) ([]byte, error) {
	js, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("html: marshal data: %w", err)
	}
	si := bytes.Index(tmpl, []byte(dataStart))
	ei := bytes.Index(tmpl, []byte(dataEnd))
	if si < 0 || ei < 0 || ei < si {
		return nil, fmt.Errorf("html: template missing %s…%s data markers", dataStart, dataEnd)
	}
	var out bytes.Buffer
	out.Write(tmpl[:si])
	out.Write(js)
	out.Write(tmpl[ei+len(dataEnd):])
	return out.Bytes(), nil
}

// DefaultTemplate returns the embedded placeholder template. A designed template
// from Claude Design replaces template.html later, keeping the DATA markers.
func DefaultTemplate() []byte { return placeholderTemplate }

// MarshalData returns the model as indented JSON (the `a2a html --json` output,
// also reused by the Telegram summary).
func MarshalData(data Data) ([]byte, error) {
	return json.MarshalIndent(data, "", "  ")
}
