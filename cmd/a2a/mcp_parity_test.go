package main

// mcp_parity_test.go is the CLI/MCP CAPABILITY-parity gate (spec 15 §T2,
// §8 AC #3). It lives in package main (not internal/mcp) because ADR-001
// forbids internal/mcp from importing internal/cli — the parity check needs
// BOTH surfaces, so this is the one place that can see both.
//
// P15 reparameterizes P14's tool-level bijection to a (tool, action)
// capability bijection: the MCP surface is now ~6 capability-grouped tools,
// each dispatching a closed action/view enum. Every §7.7-designated CLI
// verb must map to exactly one reachable (tool, action), and every reachable
// (tool, action) to exactly one designated verb — both directions.
//
// Designated CLI verb set = buildCommands() keys MINUS the CLI-only
// exclusion set, with `contract` EXPANDED to cli.ContractSubcommands()'s
// 6 sub-verbs (they are NOT buildCommands() keys — ContractCommand.Run's
// own bare switch dispatches them, cli.ContractSubcommands() is their only
// machine-enumerable home per its own doc comment).
//
// MCP (tool, action) set = the grouped Registry's tool names paired with
// each tool's dispatch enum, read from mcp's own EXPORTED slices
// (mcp.ReadViews / mcp.LifecycleActions / mcp.ExchangeActions /
// mcp.ContractActions — the single source the grouped schemas also read).
// Each pair projects to a designated-verb name via toolAction.verb().
//
// Deviation (see this phase's report): `statusline` is a buildCommands()
// key but is NOT part of the §7.7-designated set (spec 14 scope note + AC
// #6). Carried over from P14: it is in the exclusion set here.

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
)

// groupedToolNames is the P15 capability-grouped tool set (spec 15 §T1).
var groupedToolNames = []string{
	"a2a_contract", "a2a_exchange", "a2a_lifecycle",
	"a2a_new", "a2a_read", "a2a_submit",
}

// mcpExcludedVerbs is the CLI-only verb set with NO MCP tool by design
// (spec 14 §T1 scope note + AC #6, carried into P15 unchanged).
var mcpExcludedVerbs = map[string]bool{
	"version":    true,
	"init":       true,
	"connect":    true,
	"disconnect": true,
	"doctor":     true,
	"template":   true,
	"sync":       true,
	"update":     true, // P19 OP-217: self-update is a host-machine act, CLI-only (spec 19 §9, R-018)
	"validate":   true,
	"mcp":        true, // the mcp verb itself: no self-referencing tool
	"statusline": true, // spec 14 scope note + AC #6 (see file doc comment)
	"__catalog":  true, // P13's CLI-only meta verb (catalog.go): no MCP tool
}

// toolAction is one reachable MCP capability: a grouped tool plus one of its
// dispatch-enum action/view values (action "" for the action-free write
// tools a2a_new / a2a_submit).
type toolAction struct {
	tool   string
	action string
}

// verb projects a (tool, action) to its designated-CLI-verb name — the
// bijection's shared key space.
func (ta toolAction) verb() string {
	switch ta.tool {
	case "a2a_contract":
		return "contract-" + ta.action
	case "a2a_new":
		return "new"
	case "a2a_submit":
		return "submit"
	default:
		// a2a_read view == the read verb; a2a_lifecycle / a2a_exchange
		// action == the lifecycle/exchange verb.
		return ta.action
	}
}

// designatedCLIVerbs returns the §7.7-designated CLI verb names:
// buildCommands() keys minus mcpExcludedVerbs, with `contract` expanded to
// `contract-<sub>` for each of cli.ContractSubcommands().
func designatedCLIVerbs() []string {
	var out []string
	for name := range buildCommands() {
		if mcpExcludedVerbs[name] {
			continue
		}
		if name == "contract" {
			for _, sub := range cli.ContractSubcommands() {
				out = append(out, "contract-"+sub.Name)
			}
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// mcpCapabilityPairs enumerates every reachable (tool, action) from the
// grouped registry + mcp's own exported enum slices. It first asserts the
// registry exposes exactly the 6 grouped tools (a fail here means the
// enumeration below is stale), then pairs each with its dispatch enum.
func mcpCapabilityPairs(t *testing.T) []toolAction {
	t.Helper()
	names := emptyRegistry().ToolNames()
	if len(names) != len(groupedToolNames) {
		t.Fatalf("registry tool set changed: want %v, got %v", groupedToolNames, names)
	}
	for i, n := range groupedToolNames {
		if names[i] != n {
			t.Fatalf("registry tool set changed: want %v, got %v", groupedToolNames, names)
		}
	}

	var pairs []toolAction
	for _, v := range mcp.ReadViews {
		pairs = append(pairs, toolAction{"a2a_read", v})
	}
	pairs = append(pairs, toolAction{"a2a_new", ""}, toolAction{"a2a_submit", ""})
	for _, a := range mcp.LifecycleActions {
		pairs = append(pairs, toolAction{"a2a_lifecycle", a})
	}
	for _, a := range mcp.ExchangeActions {
		pairs = append(pairs, toolAction{"a2a_exchange", a})
	}
	for _, a := range mcp.ContractActions {
		pairs = append(pairs, toolAction{"a2a_contract", a})
	}
	return pairs
}

// checkBijection asserts every designated verb maps to exactly one reachable
// (tool, action), AND every (tool, action) maps to exactly one designated
// verb — both directions (AC #3). It returns every mismatch found (a missing
// capability AND a decoy/orphan capability are each independently reported),
// never short-circuiting on the first.
func checkBijection(designated []string, pairs []toolAction) []string {
	want := make(map[string]bool, len(designated))
	for _, v := range designated {
		want[v] = true
	}
	got := make(map[string]toolAction, len(pairs))
	for _, ta := range pairs {
		got[ta.verb()] = ta
	}

	var problems []string
	for v := range want {
		if _, ok := got[v]; !ok {
			problems = append(problems, "designated CLI verb has no reachable (tool, action): "+v)
		}
	}
	for v, ta := range got {
		if !want[v] {
			problems = append(problems, fmt.Sprintf("(tool, action) with no designated CLI verb (MCP-only capability, R-018 violation): (%s, action=%q) -> verb %q", ta.tool, ta.action, v))
		}
	}
	sort.Strings(problems)
	return problems
}

// emptyRegistry builds a Registry purely for structural (name-set)
// inspection — no handler in it is ever invoked by this test, so the
// zero-value dependencies BuildRegistry closes over are never dereferenced.
func emptyRegistry() *mcp.Registry {
	return mcp.BuildRegistry(nil, mcp.WriteDeps{}, "", nil, mcp.NewDeps{})
}

// TestMCPParityBijection is spec 15 §8 AC #3: every §7.7-designated CLI verb
// has exactly one reachable MCP (tool, action), and every (tool, action)
// maps to exactly one designated verb.
func TestMCPParityBijection(t *testing.T) {
	t.Parallel()
	designated := designatedCLIVerbs()
	pairs := mcpCapabilityPairs(t)

	problems := checkBijection(designated, pairs)
	if len(problems) != 0 {
		t.Fatalf("CLI/MCP capability-parity bijection failed:\n%s\n\ndesignated verbs: %v",
			strings.Join(problems, "\n"), designated)
	}
}

// TestMCPParityDecoyCapabilityFailsIndependently is AC #3's first half: a
// (tool, action) with no corresponding designated verb (an MCP-only
// capability, R-018 violation) fails the check on its own, even when every
// real verb still has its capability.
func TestMCPParityDecoyCapabilityFailsIndependently(t *testing.T) {
	t.Parallel()
	designated := designatedCLIVerbs()
	pairs := append(mcpCapabilityPairs(t), toolAction{"a2a_lifecycle", "decoy-capability"})

	problems := checkBijection(designated, pairs)
	if len(problems) == 0 {
		t.Fatal("expected the decoy (tool, action) to fail the bijection check")
	}
	found := false
	for _, p := range problems {
		if strings.Contains(p, "decoy-capability") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a problem naming the decoy capability, got: %v", problems)
	}
}

// TestMCPParityDecoyVerbFailsIndependently is AC #3's second half: a
// designated verb with no corresponding (tool, action) fails the check on
// its own.
func TestMCPParityDecoyVerbFailsIndependently(t *testing.T) {
	t.Parallel()
	designated := append(designatedCLIVerbs(), "decoy-verb-with-no-tool")
	pairs := mcpCapabilityPairs(t)

	problems := checkBijection(designated, pairs)
	if len(problems) == 0 {
		t.Fatal("expected the decoy verb to fail the bijection check")
	}
	found := false
	for _, p := range problems {
		if strings.Contains(p, "decoy-verb-with-no-tool") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a problem naming the decoy verb, got: %v", problems)
	}
}

// TestMCPParityExcludedVerbsAbsent is AC #6: none of the CLI-only (excluded)
// verb names is reachable as an MCP (tool, action).
func TestMCPParityExcludedVerbsAbsent(t *testing.T) {
	t.Parallel()
	reachable := make(map[string]bool)
	for _, ta := range mcpCapabilityPairs(t) {
		reachable[ta.verb()] = true
	}
	for verb := range mcpExcludedVerbs {
		if reachable[verb] {
			t.Errorf("excluded verb %q must have NO MCP capability, but it is reachable", verb)
		}
	}
}

// TestMCPParityContractSubverbsExpanded proves the contract family is
// enumerated via cli.ContractSubcommands(), not the bare `contract`
// buildCommands() key (which dispatches a bare switch, spec 14 Placement
// decision: "two-level CLI enumeration") — and that each expands to an
// a2a_contract action.
func TestMCPParityContractSubverbsExpanded(t *testing.T) {
	t.Parallel()
	designated := designatedCLIVerbs()
	designatedSet := make(map[string]bool, len(designated))
	for _, v := range designated {
		designatedSet[v] = true
	}
	if designatedSet["contract"] {
		t.Fatal("the bare `contract` key must NOT appear in the designated set (it is expanded to its 6 sub-verbs)")
	}
	contractActions := make(map[string]bool, len(mcp.ContractActions))
	for _, a := range mcp.ContractActions {
		contractActions[a] = true
	}
	for _, sub := range cli.ContractSubcommands() {
		if !designatedSet["contract-"+sub.Name] {
			t.Errorf("expected contract-%s in the designated set", sub.Name)
		}
		if !contractActions[sub.Name] {
			t.Errorf("CLI contract sub-verb %q has no a2a_contract action (mcp.ContractActions drift)", sub.Name)
		}
	}
}
