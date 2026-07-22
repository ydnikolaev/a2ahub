package mcp

// a2a_new (OP-203): mirrors internal/cli's cmd_new.go NewCommand, widened
// per §7.7 to accept `items[]` for batch drafting on one thread (multiple
// artifacts, one tool call) — drafts never enter the space (§3.4), written
// straight to `.a2a/staging/`, exactly like the CLI's own draft path. No
// funnel/event is involved (draft-writer only, not a write-funnel verb).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/template"
)

// newTypePrefix maps an envelope type to its §3.3 ID prefix + mint class —
// mirrors internal/cli's newTypePrefix table exactly.
var newTypePrefix = map[string]struct {
	Prefix string
	Class  artifact.Class
}{
	"contract":     {"XC", artifact.ClassStanding},
	"requirement":  {"XR", artifact.ClassStanding},
	"question":     {"XQ", artifact.ClassExchangeBroadcast},
	"work_request": {"XW", artifact.ClassExchangeBroadcast},
	"decision":     {"XD", artifact.ClassExchangeBroadcast},
	"response":     {"XS", artifact.ClassExchangeBroadcast},
	"handoff":      {"XH", artifact.ClassExchangeBroadcast},
	"announcement": {"XA", artifact.ClassExchangeBroadcast},
}

// NewDeps is a2a_new's own dependency set (mirrors internal/cli's
// NewCommand fields, minus the flag-parsing outer shape).
type NewDeps struct {
	StagingDir   string
	OwnSystem    string
	Now          func() time.Time
	Entropy      io.Reader
	ResolveActor func(ActorInput) template.Actor
	WriteFile    func(path string, data []byte, perm os.FileMode) error
}

// NewItem is one drafted artifact within an a2a_new call.
type NewItem struct {
	Type   string            `json:"type"`
	Fields map[string]string `json:"fields,omitempty"`
	Body   string            `json:"body,omitempty"`
	Slug   string            `json:"slug,omitempty"`
	Actor  ActorInput        `json:"actor,omitempty"`
}

// NewInput is a2a_new's structured input: `items[]` for batch drafting on
// one thread (§7.7).
type NewInput struct {
	Items  []NewItem `json:"items"`
	Thread string    `json:"thread,omitempty"`
}

// newDraftResult is one drafted item's result.
type newDraftResult struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func newNewHandler(deps NewDeps) HandlerFunc {
	return func(_ context.Context, args json.RawMessage) (any, string, error) {
		var in NewInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("new: invalid input: %w", err)
		}
		if len(in.Items) == 0 {
			return nil, "", fmt.Errorf("new: items is required")
		}

		if err := os.MkdirAll(deps.StagingDir, 0o755); err != nil {
			return nil, "", fmt.Errorf("new: cannot create staging directory: %w", err)
		}

		var out []newDraftResult
		for _, item := range in.Items {
			prefixInfo, ok := newTypePrefix[item.Type]
			if !ok {
				return nil, "", fmt.Errorf("new: unknown type %q", item.Type)
			}

			fields := map[string]string{}
			for k, v := range item.Fields {
				fields[k] = v
			}
			if in.Thread != "" {
				fields["thread"] = in.Thread
			}
			if _, has := fields["from"]; !has {
				fields["from"] = deps.OwnSystem
			}

			now := deps.Now()
			var mintedID string
			switch prefixInfo.Class {
			case artifact.ClassStanding:
				slug := item.Slug
				if slug == "" {
					slug = fields["slug"]
				}
				delete(fields, "slug")
				if slug == "" {
					return nil, "", fmt.Errorf("new: slug is required for standing types (contract, requirement)")
				}
				id, err := artifact.MintStandingID(prefixInfo.Prefix, deps.OwnSystem, slug)
				if err != nil {
					return nil, "", fmt.Errorf("new: cannot mint id: %w", err)
				}
				mintedID = id
			case artifact.ClassExchangeBroadcast:
				id, err := artifact.MintExchangeIDAt(prefixInfo.Prefix, deps.OwnSystem, now, deps.Entropy)
				if err != nil {
					return nil, "", fmt.Errorf("new: cannot mint id: %w", err)
				}
				mintedID = id
			}

			var bodyOverride []byte
			if item.Body != "" {
				bodyOverride = []byte(item.Body)
			}

			resolvedActor := deps.ResolveActor(item.Actor)
			draft, err := template.Render(template.Input{
				Type: item.Type, ID: mintedID, Actor: resolvedActor, Created: now,
				Fields: fields, Body: bodyOverride,
			})
			if err != nil {
				return nil, "", fmt.Errorf("new: render failed: %w", err)
			}

			path := filepath.Join(deps.StagingDir, mintedID+".md")
			if err := deps.WriteFile(path, draft, 0o644); err != nil {
				return nil, "", fmt.Errorf("new: cannot write %s: %w", path, err)
			}
			out = append(out, newDraftResult{ID: mintedID, Path: path})
		}

		return out, "", nil
	}
}
