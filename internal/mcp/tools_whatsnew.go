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

// newWhatsnewHandler builds a2a_whatsnew's handler. Both loaders are injected
// (mirrors NewWhatsnewCommand) so tests drive fixed release history and current
// limitations independently.
func newWhatsnewHandler(
	load func() ([]notes.ReleaseNotes, error),
	loadCurrentIssues func() ([]notes.Change, error),
) HandlerFunc {
	return func(_ context.Context, args json.RawMessage) (any, string, error) {
		var in WhatsnewInput
		if err := decodeStrict(args, &in, "a2a_whatsnew", 0); err != nil {
			return nil, "", err
		}

		all, err := load()
		if err != nil {
			return nil, "", fmt.Errorf("a2a_whatsnew: %w", err)
		}
		currentIssues, err := loadCurrentIssues()
		if err != nil {
			return nil, "", fmt.Errorf("a2a_whatsnew: %w", err)
		}

		if in.Since != "" {
			selected := notes.Since(all, in.Since, "")
			return notes.AttachCurrentKnownIssues(selected, all, currentIssues), "", nil
		}
		if len(all) == 0 {
			return []notes.ReleaseNotes{}, "", nil
		}
		selected := []notes.ReleaseNotes{all[len(all)-1]}
		return notes.AttachCurrentKnownIssues(selected, all, currentIssues), "", nil
	}
}
