package mcp

// a2a_inbox/outbox/show/thread/search/contracts (OP-207/208/209/210/221):
// thin structured-JSON wrappers over internal/cache's own Store queries —
// mirrors internal/cli's P7 cmd_inbox.go/cmd_outbox.go/cmd_show.go/
// cmd_thread.go/cmd_search.go exactly. Zero new read logic (spec 14 §5).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// itemsWithSkipped is a2a_inbox/a2a_outbox/a2a_search's StructuredContent
// shape once a connected space carries a mirror file the read model's own
// best-effort walk could not decode (internal/cache/skipped.go — the
// defect filed 2026-07-26: a malformed file used to drop out of the index
// without a word). internal/cli's equivalent verbs carry this same fact on
// STDERR ONLY, because their stdout item array must stay byte-identical for
// existing consumers (see cmd_inbox.go's inboxWriteUpdateAdvisory doc
// comment for the precedent this mirrors). An MCP tool has no such stdout —
// StructuredContent IS the whole structured result, and the response TEXT
// body block is already spoken for by withUpdateNotice's own advisory — so
// here the skip list is folded into the result itself, as its own field,
// rather than smuggled into body prose. Skipped is omitted entirely
// (omitempty) when nothing was skipped, matching the CLI's "write nothing
// on a clean space" rule.
type itemsWithSkipped struct {
	Items   []cache.Item        `json:"items"`
	Skipped []cache.SkippedFile `json:"skipped,omitempty"`
	// Warnings carries a validate.SeverityUnmeasured Violation when the
	// cross-space skip report itself could not be computed (Store.
	// AllSkippedFiles failed) — distinct from Skipped being empty, which
	// means the report WAS computed and found nothing to report. Without
	// this field an agent reading an empty Skipped list cannot tell "clean"
	// from "we could not check" — the exact ambiguity spec 04
	// (computed-not-listed-2026-08 P4 AC-11/§8 row 11) closes: two exit-code
	// values cannot carry three states, and on a structured surface with no
	// exit code the same three states still need a place to live. Populated
	// by skippedFilesOrUnmeasured, below. omitempty: a clean read (the
	// common case, unchanged by this phase) keeps this payload byte-identical
	// to before it, matching the P15 per-verb StructuredContent
	// byte-identity guarantee (updateNoticeDecorator's own doc comment,
	// below).
	Warnings []validate.Violation `json:"warnings,omitempty"`
}

// allSkippedFilesReader is the narrow seam skippedFilesOrUnmeasured needs —
// satisfied implicitly by *cache.Store in production, and by a test double
// that embeds a real *cache.Store and shadows only this one method
// (tools_read_test.go), the same "override one method, keep the rest real"
// judgement internal/cli/skipadvisory_unavailable_test.go's own header
// applies to avoid a test that can only be written by breaking the world.
type allSkippedFilesReader interface {
	AllSkippedFiles(ctx context.Context) (map[string][]cache.SkippedFile, error)
}

// inboxSkipStore is newInboxHandler's own seam — narrowed per this repo's
// ISP convention (AGENTS.md) to only the methods that handler calls, rather
// than the full *cache.Store surface. *cache.Store satisfies this
// implicitly, so newReadDispatch (tools_dispatch.go, off this phase's
// allowlist) needs no change to keep compiling.
type inboxSkipStore interface {
	allSkippedFilesReader
	Overdue(ctx context.Context) ([]cache.Item, error)
	Inbox(ctx context.Context, actionableOnly bool) ([]cache.Item, error)
}

// outboxSkipStore is newOutboxHandler's own seam — see inboxSkipStore's doc
// comment for why it is narrowed rather than the full *cache.Store.
type outboxSkipStore interface {
	allSkippedFilesReader
	Outbox(ctx context.Context, attentionOnly bool) ([]cache.Item, error)
}

// searchSkipStore is newSearchHandler's own seam — see inboxSkipStore's doc
// comment for why it is narrowed rather than the full *cache.Store.
type searchSkipStore interface {
	allSkippedFilesReader
	Search(ctx context.Context, query string, filters cache.SearchFilters) ([]cache.Item, error)
}

// skippedFilesOrUnmeasured resolves an a2a_read verb's cross-space
// skipped-file report, replacing the wholesale `bySpace, _ :=` discard the
// closeout audit found at this file's four AllSkippedFiles call sites
// (computed-not-listed-2026-08 P4 AC-11/§8 row 11, carried into this
// phase's footprint from P6's deliberately, so the two phases stay
// file-disjoint). A silently-discarded error here used to drop two facts at
// once: that the returned item list might be missing rows, and that
// nothing could find out which — the exact defect P6 already closed for
// the other stage-2 surface. The sentence both surfaces say is now ONE
// function below both of them (cache.SkippedFilesUnavailableMessage), per
// ADR-019 half 1, so this comment has a definition to point at instead of
// a sibling package's private symbol. This surface has no stderr channel
// to carry an out-of-band advisory
// on: StructuredContent IS the whole structured result (itemsWithSkipped's
// own doc comment, this file), so the fact rides the returned payload
// itself, as a validate.SeverityUnmeasured Violation in Warnings.
// SeverityUnmeasured already exists one layer down
// (internal/validate/result.go, D9) — this phase does not mint a second
// name for it. The message carries all three parts a bare "%v" discard was
// missing: the attempted action (verb), the reason (err), and a next step —
// a symptom with no action is spec 04's own defect.
func skippedFilesOrUnmeasured(ctx context.Context, verb string, store allSkippedFilesReader) ([]cache.SkippedFile, []validate.Violation) {
	bySpace, err := store.AllSkippedFiles(ctx)
	if err != nil {
		return nil, []validate.Violation{{
			Class:    validate.ClassReferential,
			Message:  cache.SkippedFilesUnavailableMessage(verb, err),
			Severity: validate.SeverityUnmeasured,
		}}
	}
	return cache.FlattenSkippedFiles(bySpace), nil
}

// InboxInput is a2a_inbox's structured input.
type InboxInput struct {
	// View is a2a_read's own discriminator — never read here; it exists
	// purely so this struct stays decodable under decodeStrict's
	// DisallowUnknownFields when newDispatch forwards the full raw args,
	// discriminator included (tools_dispatch.go's own doc comment).
	View       string `json:"view,omitempty"`
	Actionable bool   `json:"actionable,omitempty"`
	// Overdue selects the open items addressed to this system whose
	// needed_by has passed — see internal/cache/overdue.go. Mirrored from
	// the CLI's own `--overdue` in the same change that added it: a read
	// filter that exists on one surface only is the asymmetry P43 exists to
	// close, and the cheapest moment to not create one is now.
	Overdue bool `json:"overdue,omitempty"`
}

func newInboxHandler(store inboxSkipStore) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in InboxInput
		if err := decodeStrict(args, &in, "a2a_inbox", 0); err != nil {
			return nil, "", err
		}
		if in.Actionable && in.Overdue {
			return nil, "", fmt.Errorf(
				"a2a_inbox: actionable and overdue are different questions — pass one: " +
					"actionable = what OP-207 says needs your move now; " +
					"overdue = what you owe whose needed_by has passed (acked or not)")
		}
		if in.Overdue {
			items, err := store.Overdue(ctx)
			if err != nil {
				return nil, "", fmt.Errorf("a2a_inbox: %w", err)
			}
			if items == nil {
				items = []cache.Item{}
			}
			skipped, warnings := skippedFilesOrUnmeasured(ctx, "a2a_inbox", store)
			return itemsWithSkipped{Items: items, Skipped: skipped, Warnings: warnings}, "", nil
		}
		items, err := store.Inbox(ctx, in.Actionable)
		if err != nil {
			return nil, "", fmt.Errorf("a2a_inbox: %w", err)
		}
		if items == nil {
			items = []cache.Item{}
		}
		// Defect fix (filed 2026-07-26): see itemsWithSkipped's own doc
		// comment. inbox is cross-space, so this folds in the union across
		// every connected mirror.
		skipped, warnings := skippedFilesOrUnmeasured(ctx, "a2a_inbox", store)
		return itemsWithSkipped{Items: items, Skipped: skipped, Warnings: warnings}, "", nil
	}
}

// OutboxInput is a2a_outbox's structured input.
type OutboxInput struct {
	// View is a2a_read's own discriminator — never read here; see
	// InboxInput's identical field for why it exists.
	View      string `json:"view,omitempty"`
	Attention bool   `json:"attention,omitempty"`
}

func newOutboxHandler(store outboxSkipStore) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in OutboxInput
		if err := decodeStrict(args, &in, "a2a_outbox", 0); err != nil {
			return nil, "", err
		}
		items, err := store.Outbox(ctx, in.Attention)
		if err != nil {
			return nil, "", fmt.Errorf("a2a_outbox: %w", err)
		}
		if items == nil {
			items = []cache.Item{}
		}
		// Defect fix (filed 2026-07-26): see itemsWithSkipped's own doc
		// comment. outbox is cross-space, so this folds in the union across
		// every connected mirror.
		skipped, warnings := skippedFilesOrUnmeasured(ctx, "a2a_outbox", store)
		return itemsWithSkipped{Items: items, Skipped: skipped, Warnings: warnings}, "", nil
	}
}

// ShowInput is a2a_show's structured input.
type ShowInput struct {
	// View is a2a_read's own discriminator — never read here; see
	// InboxInput's identical field for why it exists.
	View string `json:"view,omitempty"`
	Ref  string `json:"ref"`
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
		if err := decodeStrict(args, &in, "a2a_show", 0); err != nil {
			return nil, "", err
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
//
// `thread_id` accepts EITHER a `thread:...` value or any member artifact id —
// a weak caller pastes whichever id it is holding, and forcing it to find the
// thread id first is a determinism tax with no upside.
//
// `space` is OPTIONAL and is the recovery path for §T4's space-locality
// refusal: when a thread resolves in more than one connected space the reader
// refuses rather than merging two conversations into one, and `space` is how
// the caller then names which it meant. Without it this surface would hit an
// unrecoverable error where the CLI prints the fix.
type ThreadInput struct {
	// View is a2a_read's own discriminator — never read here; see
	// InboxInput's identical field for why it exists.
	View     string `json:"view,omitempty"`
	ThreadID string `json:"thread_id"`
	Space    string `json:"space,omitempty"`
}

func newThreadHandler(store *cache.Store) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ThreadInput
		if err := decodeStrict(args, &in, "a2a_thread", 0); err != nil {
			return nil, "", err
		}
		if in.ThreadID == "" {
			return nil, "", fmt.Errorf("a2a_thread: thread_id is required")
		}
		result, err := store.ThreadView(ctx, in.ThreadID, in.Space)
		if err != nil {
			// The ambiguity refusal must arrive with its RECOVERY, not as a
			// bare failure: ThreadAmbiguityError's own Error() names each
			// space, its member count and its opener, and the caller's next
			// move is to re-ask with `space`. Merging the two conversations
			// instead would be the silent cross-space merge the plan's
			// CC-073 forbids, and it is exactly the shape a weak caller
			// would misread as one exchange.
			var ambiguous *cache.ThreadAmbiguityError
			if errors.As(err, &ambiguous) {
				return nil, "", fmt.Errorf("a2a_thread: %w — re-ask with the `space` input naming which one you mean", err)
			}
			return nil, "", fmt.Errorf("a2a_thread: %w", err)
		}
		// Defect fix (filed 2026-07-26): see itemsWithSkipped's own doc
		// comment (a2a_thread's own shape differs — embedded, like
		// showOutput, since ThreadResult is already a struct, not a bare
		// list). thread is scoped to the ONE resolved space (result.Space);
		// advising about a different connected space's skips would be noise
		// about a space this render never touches. Never reached on the
		// error paths above (ambiguity/not-found) — there is no resolved
		// space to advise about.
		skipped, _ := store.SkippedFiles(ctx, result.Space)
		return threadOutput{ThreadResult: result, Skipped: skipped}, "", nil
	}
}

// threadOutput is a2a_thread's StructuredContent shape once its resolved
// space carries a skipped mirror file — mirrors showOutput's own
// embed-and-extend pattern (this file, above) rather than itemsWithSkipped's
// items/skipped wrapper, because cache.ThreadResult is already a struct: an
// embed keeps every existing top-level field (thread, space, transcript,
// ...) exactly where a caller already expects it, with skipped added
// alongside rather than nested under a new "result" key.
type threadOutput struct {
	cache.ThreadResult
	Skipped []cache.SkippedFile `json:"skipped,omitempty"`
}

// SearchInput is a2a_search's structured input.
type SearchInput struct {
	// View is a2a_read's own discriminator — never read here; see
	// InboxInput's identical field for why it exists.
	View  string `json:"view,omitempty"`
	Query string `json:"query"`
	Type  string `json:"type,omitempty"`
	Space string `json:"space,omitempty"`
	State string `json:"state,omitempty"`
	// Thread narrows to one conversation, matching the CLI's `--thread`.
	// Without it this surface could find an artifact and offer no way to ask
	// "what else is on its thread" in the same vocabulary the CLI uses.
	Thread string `json:"thread,omitempty"`
}

func newSearchHandler(store searchSkipStore) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in SearchInput
		if err := decodeStrict(args, &in, "a2a_search", 0); err != nil {
			return nil, "", err
		}
		items, err := store.Search(ctx, in.Query, cache.SearchFilters{Type: in.Type, Space: in.Space, State: in.State, Thread: in.Thread})
		if err != nil {
			return nil, "", fmt.Errorf("a2a_search: %w", err)
		}
		if items == nil {
			items = []cache.Item{}
		}
		// Defect fix (filed 2026-07-26): see itemsWithSkipped's own doc
		// comment. search is cross-space (in.Space is only a filter on the
		// fold, not the walk), so this folds in the union across every
		// connected mirror, same as inbox/outbox above.
		skipped, warnings := skippedFilesOrUnmeasured(ctx, "a2a_search", store)
		return itemsWithSkipped{Items: items, Skipped: skipped, Warnings: warnings}, "", nil
	}
}

// ContractsInput is a2a_contracts's structured input.
type ContractsInput struct {
	// View is a2a_read's own discriminator — never read here; see
	// InboxInput's identical field for why it exists.
	View     string `json:"view,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// withUpdateNotice wraps inner so its response body carries the shared T4
// update advisory (spec 19 T4 AMENDED / §11 wave-12c) OUT-OF-BAND: the
// notice rides the response's TEXT body block ONLY — result
// (StructuredContent) passes through byte-unchanged, so P15's per-verb
// StructuredContent byte-identity holds regardless of update state. A
// GradeNone notice (EnableUpdateNotice never called on store, or nothing to
// advise) leaves body byte-unchanged too; a handler error short-circuits
// before any notice lookup and is returned exactly as inner produced it.
// updateNoticeDecorator adapts withUpdateNotice to Registry.Decorate, so the
// advisory is attached ONCE at registry construction rather than at a chosen
// handler. Until 2026-08-22 it wrapped a2a_read alone: an agent asked to send
// a notification reached a2a_notify, was told nothing about the release it was
// missing, and wrote from a stale binary. The concern is cross-cutting, so it
// belongs at the seam every tool passes through.
func updateNoticeDecorator(store *cache.Store) func(HandlerFunc) HandlerFunc {
	// A NIL STORE IS A REAL, LEGITIMATE STATE — some registries are built
	// without one — and it means the advisory has nothing to read, not that
	// something is wrong. Returning the handler untouched is the honest
	// answer; wrapping it would panic inside Store.UpdateNotice on the first
	// call, which is exactly what widening this decorator from one tool to
	// all of them surfaced (TestEquivContractNew, 2026-08-22).
	if store == nil {
		return func(inner HandlerFunc) HandlerFunc { return inner }
	}
	return func(inner HandlerFunc) HandlerFunc { return withUpdateNotice(inner, store) }
}

func withUpdateNotice(inner HandlerFunc, store *cache.Store) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		result, body, err := inner(ctx, args)
		if err != nil {
			return result, body, err
		}
		n := store.UpdateNotice()
		if n.Grade == release.GradeNone {
			return result, body, nil
		}
		if body != "" {
			body += "\n"
		}
		body += "note: " + n.Sentence
		return result, body, nil
	}
}

func newContractsHandler(store *cache.Store) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractsInput
		if err := decodeStrict(args, &in, "a2a_contracts", 0); err != nil {
			return nil, "", err
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
