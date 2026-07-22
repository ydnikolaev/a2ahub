package mcp

// tools_dispatch.go is P15's thin capability-grouping layer. Each grouped
// tool (a2a_read/a2a_lifecycle/a2a_exchange/a2a_contract) reads a CLOSED
// action/view discriminator and delegates to the SAME per-verb handler P14
// already ships — the sub-handler unmarshals its OWN typed input struct and
// ignores the extra discriminator field (Go json default), so the funnel
// path stays byte-for-byte identical per verb (spec 15 §T1, plan 15
// Placement decisions). No per-verb handler body is rewritten here.
//
// The exported enum slices below are the SINGLE source both the grouped
// schema (tools.go) and the capability-parity test (cmd/a2a) read.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// ReadViews is the a2a_read view enum (the 6 folded read tools).
var ReadViews = []string{"inbox", "outbox", "show", "thread", "search", "contracts"}

// LifecycleActions is the a2a_lifecycle action enum, DERIVED from
// LifecycleVerbTable (the SSOT of the 15 generic transitions) — never
// re-typed, so a new §3.4 transition row automatically becomes a new
// a2a_lifecycle action with no second list to update (spec 15 §5).
var LifecycleActions = func() []string {
	out := make([]string, len(LifecycleVerbTable))
	for i, spec := range LifecycleVerbTable {
		out[i] = spec.Verb
	}
	return out
}()

// ExchangeActions is the a2a_exchange action enum (respond/verify/dispute/
// note folded into one tool).
var ExchangeActions = []string{"respond", "verify", "dispute", "note"}

// ContractActions is the a2a_contract action enum. It mirrors
// cli.ContractSubcommands() (spec 15 §5's named SSOT) but is re-typed here
// because ADR-001 forbids internal/mcp importing internal/cli — the
// cmd/a2a capability-parity test is the reconciliation gate that reds on
// any drift between this slice and cli.ContractSubcommands().
var ContractActions = []string{"new", "publish", "deprecate", "retire", "diff", "verify-export"}

// newDispatch builds a grouped tool's handler: it reads the discKey
// discriminator ("action"/"view"), looks up the matching per-verb handler,
// and calls it with the SAME raw args (the sub-handler ignores the
// discriminator field). An absent or unknown discriminator is a
// well-formed error — surfaced by the server as an isError tool result,
// never a panic and never a JSON-RPC protocol error (spec 15 §6).
func newDispatch(tool, discKey string, handlers map[string]HandlerFunc, enum []string) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var probe map[string]json.RawMessage
		if len(args) > 0 {
			if err := json.Unmarshal(args, &probe); err != nil {
				return nil, "", fmt.Errorf("%s: invalid input: %w", tool, err)
			}
		}
		rawVal, present := probe[discKey]
		if !present {
			return nil, "", fmt.Errorf("%s: %s is required (one of: %s)", tool, discKey, strings.Join(enum, "|"))
		}
		var val string
		if err := json.Unmarshal(rawVal, &val); err != nil {
			return nil, "", fmt.Errorf("%s: %s must be a string", tool, discKey)
		}
		h, ok := handlers[val]
		if !ok {
			return nil, "", fmt.Errorf("%s: unknown %s %q (one of: %s)", tool, discKey, val, strings.Join(enum, "|"))
		}
		return h(ctx, args)
	}
}

// newReadDispatch builds a2a_read: view -> the 6 P14 read handlers.
func newReadDispatch(store *cache.Store) HandlerFunc {
	return newDispatch("a2a_read", "view", map[string]HandlerFunc{
		"inbox":     newInboxHandler(store),
		"outbox":    newOutboxHandler(store),
		"show":      newShowHandler(store),
		"thread":    newThreadHandler(store),
		"search":    newSearchHandler(store),
		"contracts": newContractsHandler(store),
	}, ReadViews)
}

// newLifecycleDispatch builds a2a_lifecycle: action -> newLifecycleHandler
// for the matching LifecycleVerbTable row (handlers keyed off the SAME
// table the action enum derives from).
func newLifecycleDispatch(write WriteDeps) HandlerFunc {
	handlers := make(map[string]HandlerFunc, len(LifecycleVerbTable))
	for _, spec := range LifecycleVerbTable {
		handlers[spec.Verb] = newLifecycleHandler(spec, write)
	}
	return newDispatch("a2a_lifecycle", "action", handlers, LifecycleActions)
}

// newExchangeDispatch builds a2a_exchange: action -> respond/verify/
// dispute/note.
func newExchangeDispatch(write WriteDeps) HandlerFunc {
	return newDispatch("a2a_exchange", "action", map[string]HandlerFunc{
		"respond": newRespondHandler(write),
		"verify":  newVerifyHandler(write),
		"dispute": newDisputeHandler(write),
		"note":    newNoteHandler(write),
	}, ExchangeActions)
}

// newContractDispatch builds a2a_contract: action -> the 6 contract
// sub-verb handlers (new delegates to a2a_new's draft path via newDeps;
// the rest run over contractDeps).
func newContractDispatch(newDeps NewDeps, contractDeps ContractDeps) HandlerFunc {
	return newDispatch("a2a_contract", "action", map[string]HandlerFunc{
		"new":           newContractNewHandler(newDeps),
		"publish":       newContractPublishHandler(contractDeps),
		"deprecate":     newContractDeprecateHandler(contractDeps),
		"retire":        newContractRetireHandler(contractDeps),
		"diff":          newContractDiffHandler(contractDeps),
		"verify-export": newContractVerifyExportHandler(contractDeps),
	}, ContractActions)
}
