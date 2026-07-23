package mcp

// a2a_whatsnew (P31, MCP twin of internal/cli's cmd_whatsnew.go): an
// action-free, standalone tool (the a2a_new/a2a_submit precedent, tools.go)
// surfacing internal/notes' committed, embedded release-notes corpus as
// StructuredContent. Unlike the CLI verb there is no binary-version stamp
// here to bound the upper end of a `since` query, so `since` set queries
// unbounded-above (the newest corpus entry, whatever it is); `since` absent
// returns just the newest corpus entry.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/notes"
)

// WhatsnewInput is a2a_whatsnew's structured input.
type WhatsnewInput struct {
	Since string `json:"since,omitempty"`
}

// newWhatsnewHandler builds a2a_whatsnew's handler. load is injected
// (mirrors NewWhatsnewCommand's own load field) so tests drive a fixed
// corpus; the real registration (tools.go) calls notes.Load(releasenotes.FS)
// inline.
func newWhatsnewHandler(load func() ([]notes.ReleaseNotes, error)) HandlerFunc {
	return func(_ context.Context, args json.RawMessage) (any, string, error) {
		var in WhatsnewInput
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, "", fmt.Errorf("a2a_whatsnew: invalid input: %w", err)
			}
		}

		all, err := load()
		if err != nil {
			return nil, "", fmt.Errorf("a2a_whatsnew: %w", err)
		}

		if in.Since != "" {
			return notes.Since(all, in.Since, ""), "", nil
		}
		if len(all) == 0 {
			return []notes.ReleaseNotes{}, "", nil
		}
		return []notes.ReleaseNotes{all[len(all)-1]}, "", nil
	}
}
