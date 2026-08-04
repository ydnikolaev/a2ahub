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
	"github.com/ydnikolaev/a2ahub/internal/validate"
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
	ResolveActor ActorResolver
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

		// §T1 "--thread is checked in two places, for two different
		// things": at draft time this is GRAMMAR ONLY (a pure
		// ParseThreadID call, no I/O) — whether the thread actually
		// EXISTS in the space is the validator's job (REF-010), not
		// this drafting verb's. Checked once for the whole batch, so a
		// malformed value never gets a chance to mint anything first.
		if in.Thread != "" {
			if _, err := artifact.ParseThreadID(in.Thread); err != nil {
				return nil, "", fmt.Errorf("new: malformed thread %q: %w", in.Thread, err)
			}
		}

		// Resolve the entire batch before creating the staging directory,
		// minting ids, or writing the first draft. Otherwise an anonymous
		// later item could leave earlier items behind despite the call
		// returning a refusal.
		resolvedActors := make([]template.Actor, len(in.Items))
		for i, item := range in.Items {
			resolved, actorErr := deps.ResolveActor(item.Actor)
			if actorErr != nil {
				return nil, "", fmt.Errorf("new: item %d: %w", i, actorErr)
			}
			resolvedActors[i] = resolved
		}

		if err := os.MkdirAll(deps.StagingDir, 0o755); err != nil {
			return nil, "", fmt.Errorf("new: cannot create staging directory: %w", err)
		}

		// batchThread is the ONE thread every item in this call shares
		// (§T1 "Batch"): the caller's explicit --thread if given,
		// otherwise minted ONCE on the first item and reused for every
		// other item — the agent makes no per-item choice.
		var batchThread string
		var out []newDraftResult
		for itemIndex, item := range in.Items {
			prefixInfo, ok := newTypePrefix[item.Type]
			if !ok {
				return nil, "", fmt.Errorf("new: unknown type %q", item.Type)
			}

			fields := map[string]string{}
			for k, v := range item.Fields {
				fields[k] = v
			}
			// itemThread captures a per-item `fields.thread` override
			// BEFORE the batch-level fill below can touch it — D0's own
			// class of bug is a silently discarded override, so an
			// item-level value that turns out to disagree with the
			// resolved batch thread is a conflict (§T1 "Explicit
			// conflict refuses"), never a silent overwrite.
			itemThread := fields["thread"]
			if _, has := fields["from"]; !has {
				fields["from"] = deps.OwnSystem
			}

			now := deps.Now()

			// §T1 "Mint always": no artifact is ever drafted off-thread.
			if in.Thread != "" {
				batchThread = in.Thread
			} else if batchThread == "" {
				mintedThread, terr := artifact.MintThreadIDAt(deps.OwnSystem, now, deps.Entropy)
				if terr != nil {
					return nil, "", fmt.Errorf("new: cannot mint thread: %w", terr)
				}
				batchThread = mintedThread
			}
			if itemThread != "" && itemThread != batchThread {
				return nil, "", fmt.Errorf("new: thread conflict: item field thread %q differs from batch thread %q", itemThread, batchThread)
			}
			fields["thread"] = batchThread

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

			draft, err := template.RenderNew(template.Input{
				Type: item.Type, ID: mintedID, Actor: resolvedActors[itemIndex], Created: now,
				Fields: fields, Body: bodyOverride,
			}, validate.IsJSONSchemaFormat)
			if err != nil {
				return nil, "", fmt.Errorf("new: render failed: %w", err)
			}

			path := filepath.Join(deps.StagingDir, mintedID+".md")
			if err := deps.WriteFile(path, draft, 0o644); err != nil {
				return nil, "", fmt.Errorf("new: cannot write %s: %w", path, err)
			}
			out = append(out, newDraftResult{ID: mintedID, Path: path})

			// D-D: a fresh JSON-Schema contract gets its starter schema +
			// valid fixture, so §5.4b's compatibility check has a baseline
			// from the moment the contract exists. R-018 says this surface
			// exposes no capability the CLI lacks and lacks none the CLI
			// has — so the scaffold happens here for the same reason and
			// through the SAME function, never a second implementation.
			// internal/mcp may not import internal/cli, which is exactly
			// why it lives in internal/template.
			if item.Type == "contract" {
				schemaFormat, sfErr := template.ContractDraftSchemaFormat(draft)
				if sfErr != nil {
					return nil, "", fmt.Errorf("new: cannot read drafted schema_format: %w", sfErr)
				}
				if validate.IsJSONSchemaFormat(schemaFormat) {
					slug := item.Slug
					if slug == "" {
						slug = item.Fields["slug"]
					}
					written, werr := template.ScaffoldContractCandidateInStaging(deps.StagingDir, deps.OwnSystem, slug, draft, deps.WriteFile)
					if werr != nil {
						return nil, "", fmt.Errorf("new: cannot scaffold contract schema: %w", werr)
					}
					for _, p := range written {
						out = append(out, newDraftResult{ID: mintedID, Path: p})
					}
				}
			}
		}

		return out, "", nil
	}
}
