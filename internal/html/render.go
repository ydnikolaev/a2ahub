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
// HTML either (DOCS). A template missing either marker pair is an error (fail
// loud, never emit a half-injected page).
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
	// Replace DATA first, then recompute the DOCS marker offsets on the RESULT
	// buffer — DATA precedes DOCS, so splicing DATA shifts every later index;
	// re-scanning avoids writing DOCS at a stale position.
	out, err := replaceRegion(tmpl, dataStart, dataEnd, dataJSON)
	if err != nil {
		return nil, err
	}
	out, err = replaceRegion(out, docsStart, docsEnd, docsJSON)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// replaceRegion replaces the first start…end region (inclusive of both markers)
// in src with replacement. Missing/inverted markers are an error.
func replaceRegion(src []byte, start, end string, replacement []byte) ([]byte, error) {
	si := bytes.Index(src, []byte(start))
	ei := bytes.Index(src, []byte(end))
	if si < 0 || ei < 0 || ei < si {
		return nil, fmt.Errorf("html: template missing %s…%s markers", start, end)
	}
	var out bytes.Buffer
	out.Grow(len(src) - (ei + len(end) - si) + len(replacement))
	out.Write(src[:si])
	out.Write(replacement)
	out.Write(src[ei+len(end):])
	return out.Bytes(), nil
}

// DefaultTemplate returns the embedded designed template.
func DefaultTemplate() []byte { return placeholderTemplate }

// MarshalData returns the model as indented JSON (the `a2a html --json` output,
// also reused by the Telegram summary). DOCS is a separate, static global (from
// the embedded skill tree) and is not part of the DATA --json contract.
func MarshalData(data Data) ([]byte, error) {
	return json.MarshalIndent(data, "", "  ")
}
