package mcp

// a2a_inbox/outbox/show/thread/search/contracts (OP-207/208/209/210/221):
// thin structured-JSON wrappers over internal/cache's own Store queries —
// mirrors internal/cli's P7 cmd_inbox.go/cmd_outbox.go/cmd_show.go/
// cmd_thread.go/cmd_search.go exactly. Zero new read logic (spec 14 §5).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// InboxInput is a2a_inbox's structured input.
type InboxInput struct {
	Actionable bool `json:"actionable,omitempty"`
}

func newInboxHandler(store *cache.Store) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in InboxInput
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, "", fmt.Errorf("a2a_inbox: invalid input: %w", err)
			}
		}
		items, err := store.Inbox(ctx, in.Actionable)
		if err != nil {
			return nil, "", fmt.Errorf("a2a_inbox: %w", err)
		}
		if items == nil {
			items = []cache.Item{}
		}
		return items, "", nil
	}
}

// OutboxInput is a2a_outbox's structured input.
type OutboxInput struct {
	Attention bool `json:"attention,omitempty"`
}

func newOutboxHandler(store *cache.Store) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in OutboxInput
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, "", fmt.Errorf("a2a_outbox: invalid input: %w", err)
			}
		}
		items, err := store.Outbox(ctx, in.Attention)
		if err != nil {
			return nil, "", fmt.Errorf("a2a_outbox: %w", err)
		}
		if items == nil {
			items = []cache.Item{}
		}
		return items, "", nil
	}
}

// ShowInput is a2a_show's structured input.
type ShowInput struct {
	Ref string `json:"ref"`
}

// showOutput is a2a_show's JSON shape — mirrors internal/cli's showOutput
// (cache.ShowResult + the derived V5 warnings).
type showOutput struct {
	cache.ShowResult
	Warnings []validate.Violation `json:"warnings,omitempty"`
}

func newShowHandler(store *cache.Store) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ShowInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("a2a_show: invalid input: %w", err)
		}
		if in.Ref == "" {
			return nil, "", fmt.Errorf("a2a_show: ref is required")
		}
		result, err := store.Show(ctx, in.Ref)
		if err != nil {
			return nil, "", fmt.Errorf("a2a_show: %w", err)
		}
		out := showOutput{ShowResult: result, Warnings: showV5Warnings(result)}
		return out, result.Body, nil
	}
}

// showV5Warnings maps cache's own digest/staleness FACTS to the V5
// registry code — mirrors internal/cli's showV5Warnings exactly.
func showV5Warnings(result cache.ShowResult) []validate.Violation {
	var out []validate.Violation
	for _, rf := range result.Refs {
		switch {
		case rf.DigestMismatch:
			out = append(out, validate.Violation{
				Code: "REF-004", Class: validate.ClassReferential, Path: "refs",
				Message:  fmt.Sprintf("V5: pinned ref %s digest does not match the resolved target", rf.Ref),
				Severity: validate.SeverityWarning,
			})
		case rf.PinnedDigest != "" && !rf.Resolved:
			out = append(out, validate.Violation{
				Code: "REF-008", Class: validate.ClassReferential, Path: "refs",
				Message:  fmt.Sprintf("V5: pinned ref %s could not be resolved to verify", rf.Ref),
				Severity: validate.SeverityWarning,
			})
		}
	}
	if result.SyncStale {
		out = append(out, validate.Violation{
			Class:    validate.ClassReferential,
			Message:  fmt.Sprintf("V5: this space's mirror sync-age (%s) exceeds the refresh TTL; data may be stale", result.SyncAge),
			Severity: validate.SeverityWarning,
		})
	}
	return out
}

// ThreadInput is a2a_thread's structured input.
type ThreadInput struct {
	ThreadID string `json:"thread_id"`
}

func newThreadHandler(store *cache.Store) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ThreadInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("a2a_thread: invalid input: %w", err)
		}
		if in.ThreadID == "" {
			return nil, "", fmt.Errorf("a2a_thread: thread_id is required")
		}
		items, err := store.Thread(ctx, in.ThreadID)
		if err != nil {
			return nil, "", fmt.Errorf("a2a_thread: %w", err)
		}
		if items == nil {
			items = []cache.Item{}
		}
		return items, "", nil
	}
}

// SearchInput is a2a_search's structured input.
type SearchInput struct {
	Query string `json:"query"`
	Type  string `json:"type,omitempty"`
	Space string `json:"space,omitempty"`
	State string `json:"state,omitempty"`
}

func newSearchHandler(store *cache.Store) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in SearchInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("a2a_search: invalid input: %w", err)
		}
		items, err := store.Search(ctx, in.Query, cache.SearchFilters{Type: in.Type, Space: in.Space, State: in.State})
		if err != nil {
			return nil, "", fmt.Errorf("a2a_search: %w", err)
		}
		if items == nil {
			items = []cache.Item{}
		}
		return items, "", nil
	}
}

// ContractsInput is a2a_contracts's structured input.
type ContractsInput struct {
	Provider string `json:"provider,omitempty"`
}

func newContractsHandler(store *cache.Store) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractsInput
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, "", fmt.Errorf("a2a_contracts: invalid input: %w", err)
			}
		}
		contracts, err := store.Contracts(ctx, in.Provider)
		if err != nil {
			return nil, "", fmt.Errorf("a2a_contracts: %w", err)
		}
		if contracts == nil {
			contracts = []cache.ContractInfo{}
		}
		return contracts, "", nil
	}
}
