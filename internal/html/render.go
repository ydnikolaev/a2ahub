package html

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed template.html
var placeholderTemplate []byte

// The designed template (from Claude Design) carries two injection regions, each
// delimited by a marker pair — inline samples live between them so the file
// previews standalone. Render replaces each whole region (markers + sample) with
// the real JSON: `const DATA = <json>;` and `const DOCS = <json>;`.
const (
	dataStart = "/*A2A_DATA_START*/"
	dataEnd   = "/*A2A_DATA_END*/"
	docsStart = "/*A2A_DOCS_START*/"
	docsEnd   = "/*A2A_DOCS_END*/"
)

// Render injects the DATA and DOCS globals into tmpl and returns the
// self-contained HTML. json.MarshalIndent escapes <,>,& (SetEscapeHTML default),
// so the JSON is safe inside a <script> block — no </script> breakout even if an
// artifact title contains "</script>" (DATA) and no breakout from our own doc
// HTML either (DOCS).
//
// Both marker regions are located in the ORIGINAL (trusted, embedded) template
// and spliced in a single left-to-right pass. Critically we NEVER re-scan a
// buffer that already holds injected content: an untrusted Item.Title could
// contain the literal string "/*A2A_DOCS_START*/", and a second Index over the
// DATA-spliced buffer would find that as a false marker and silently corrupt
// the page. Locating both regions up front on the marker-only template closes
// that. A template whose markers are missing, duplicated, or out of order is an
// error (fail loud, never emit a half-injected page).
func Render(tmpl []byte, data Data, docs []DocSection) ([]byte, error) {
	dataJSON, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("html: marshal data: %w", err)
	}
	if docs == nil {
		docs = []DocSection{} // marshal to [] not null, so the page never sees a non-array DOCS
	}
	docsJSON, err := json.MarshalIndent(docs, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("html: marshal docs: %w", err)
	}

	dStart, dStop, err := regionBounds(tmpl, dataStart, dataEnd)
	if err != nil {
		return nil, err
	}
	kStart, kStop, err := regionBounds(tmpl, docsStart, docsEnd)
	if err != nil {
		return nil, err
	}
	// Expect DATA fully before DOCS (the template's layout). regionBounds already
	// guarantees each region's own start<end; the only cross-region check left is
	// that DATA ends before DOCS begins — otherwise the template is malformed and
	// we fail loud rather than mis-splice.
	if dStop > kStart {
		return nil, fmt.Errorf("html: DATA region must precede DOCS region (markers overlap or are out of order)")
	}

	var out bytes.Buffer
	out.Grow(len(tmpl) + len(dataJSON) + len(docsJSON))
	out.Write(tmpl[:dStart])
	out.Write(dataJSON)
	out.Write(tmpl[dStop:kStart])
	out.Write(docsJSON)
	out.Write(tmpl[kStop:])
	return out.Bytes(), nil
}

// regionBounds returns [start-of-startMarker, end-of-endMarker) for the region
// delimited by start…end in tmpl. Each marker MUST appear exactly once — a
// missing or duplicated marker is an error (an ambiguous match must never be
// resolved silently), and the end must follow the start.
func regionBounds(tmpl []byte, start, end string) (int, int, error) {
	if n := bytes.Count(tmpl, []byte(start)); n != 1 {
		return 0, 0, fmt.Errorf("html: template must contain exactly one %s marker (found %d)", start, n)
	}
	if n := bytes.Count(tmpl, []byte(end)); n != 1 {
		return 0, 0, fmt.Errorf("html: template must contain exactly one %s marker (found %d)", end, n)
	}
	si := bytes.Index(tmpl, []byte(start))
	ei := bytes.Index(tmpl, []byte(end))
	if ei < si {
		return 0, 0, fmt.Errorf("html: %s marker precedes %s", end, start)
	}
	return si, ei + len(end), nil
}

// DefaultTemplate returns the embedded designed template.
func DefaultTemplate() []byte { return placeholderTemplate }

// MarshalData returns the model as indented JSON (the `a2a html --json` output,
// also reused by the Telegram summary). DOCS is a separate, static global (from
// the embedded skill tree) and is not part of the DATA --json contract.
func MarshalData(data Data) ([]byte, error) {
	return json.MarshalIndent(data, "", "  ")
}
