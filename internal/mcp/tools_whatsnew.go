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

	"github.com/ydnikolaev/go-foundation/units/adoption"
)

// WhatsnewInput is a2a_whatsnew's structured input.
type WhatsnewInput struct {
	Since string `json:"since,omitempty"`
}

// newWhatsnewHandler builds a2a_whatsnew's handler. Both loaders are injected
// (mirrors NewWhatsnewCommand) so tests drive fixed release history and current
// limitations independently.
func newWhatsnewHandler(
	load func() ([]adoption.Release, error),
	loadCurrentIssues func() ([]adoption.Change, error),
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
			selected := adoption.Since(all, in.Since, "")
			return adoption.AttachStanding(selected, all, currentIssues), "", nil
		}
		if len(all) == 0 {
			return []adoption.Release{}, "", nil
		}
		selected := []adoption.Release{all[len(all)-1]}
		return adoption.AttachStanding(selected, all, currentIssues), "", nil
	}
}
