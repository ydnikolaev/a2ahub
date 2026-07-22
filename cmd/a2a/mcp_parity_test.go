package main

// mcp_parity_test.go is the CLI/MCP parity bijection gate (spec 14 §8 AC
// 1/3/6, plan 14 Brief item 6). It lives in package main (not
// internal/mcp) because ADR-001 forbids internal/mcp from importing
// internal/cli — the bijection check needs BOTH registries, so this is
// the one place that can see both.
//
// Designated CLI verb set = buildCommands() keys MINUS the CLI-only
// exclusion set, with `contract` EXPANDED to cli.ContractSubcommands()'s
// 6 sub-verbs (they are NOT buildCommands() keys — ContractCommand.Run's
// own bare switch dispatches them, cli.ContractSubcommands() is their only
// machine-enumerable home per its own doc comment).
//
// Deviation (see this phase's report): `statusline` is a buildCommands()
// key (registered via wire.go's readVerbs()) but is NOT part of the
// §7.7-designated set — the plan Brief's own exclusion list
// (version/init/connect/disconnect/doctor/template/sync/validate/mcp)
// omits it, but spec 14's "Generated OP↔tool mapping table" scope note
// AND its AC #6 row both explicitly name `statusline` as excluded
// ("OPs not listed here (init/connect/disconnect/validate/sync/html/
// statusline/update/doctor/template) have no MCP tool in v1"). Schema/
// spec fidelity wins over the Brief's own shorthand: `statusline` is
// added to the exclusion set here.

import (
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
)

// mcpExcludedVerbs is the CLI-only verb set with NO MCP tool by design
// (spec 14 §T1 scope note + AC #6).
var mcpExcludedVerbs = map[string]bool{
	"version":    true,
	"init":       true,
	"connect":    true,
	"disconnect": true,
	"doctor":     true,
	"template":   true,
	"sync":       true,
	"validate":   true,
	"mcp":        true, // the mcp verb itself: no self-referencing tool
	"statusline": true, // spec 14 scope note + AC #6 (see file doc comment)
}

// designatedCLIVerbs returns the §7.7-designated CLI verb names:
// buildCommands() keys minus mcpExcludedVerbs, with `contract` expanded
// to `contract-<sub>` for each of cli.ContractSubcommands() (hyphenated
// so verbToToolName's uniform hyphen->underscore rule produces
// `a2a_contract_<sub>`, matching plan 14 Brief item 6's naming
// convention).
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

// verbToToolName applies plan 14 Brief item 6's naming convention: a2a_ +
// verb-name with EVERY hyphen replaced by an underscore (verify-pass ->
// a2a_verify_pass; contract-verify-export -> a2a_contract_verify_export).
func verbToToolName(verb string) string {
	return "a2a_" + strings.ReplaceAll(verb, "-", "_")
}

// checkBijection asserts every designated verb maps to exactly one tool
// name in toolNames, AND every tool name maps to exactly one designated
// verb — both directions (AC #3). It returns every mismatch found (a
// missing tool AND a decoy/orphan tool are each independently reported),
// never short-circuiting on the first.
func checkBijection(designated, toolNames []string) []string {
	want := make(map[string]bool, len(designated))
	for _, v := range designated {
		want[verbToToolName(v)] = true
	}
	got := make(map[string]bool, len(toolNames))
	for _, n := range toolNames {
		got[n] = true
	}

	var problems []string
	for name := range want {
		if !got[name] {
			problems = append(problems, "missing tool for designated verb: "+name)
		}
	}
	for name := range got {
		if !want[name] {
			problems = append(problems, "tool with no designated verb (MCP-only capability, R-018 violation): "+name)
		}
	}
	sort.Strings(problems)
	return problems
}

// emptyRegistry builds a Registry purely for structural (name-set)
// inspection — no handler in it is ever invoked by this test, so the
// zero-value dependencies BuildRegistry closes over are never
// dereferenced.
func emptyRegistry() *mcp.Registry {
	return mcp.BuildRegistry(nil, mcp.WriteDeps{}, "", nil, mcp.NewDeps{})
}

// TestMCPParityBijection is spec 14 §8 AC #1: every §7.7-designated CLI
// verb has exactly one MCP tool, and every MCP tool maps to exactly one
// designated verb.
func TestMCPParityBijection(t *testing.T) {
	t.Parallel()
	designated := designatedCLIVerbs()
	toolNames := emptyRegistry().ToolNames()

	problems := checkBijection(designated, toolNames)
	if len(problems) != 0 {
		t.Fatalf("CLI/MCP parity bijection failed:\n%s\n\ndesignated verbs: %v\ntool names: %v",
			strings.Join(problems, "\n"), designated, toolNames)
	}
}

// TestMCPParityDecoyToolFailsIndependently is AC #3's first half: a tool
// with no corresponding designated verb (an MCP-only capability, R-018
// violation) fails the check on its own, even when every real verb still
// has its tool.
func TestMCPParityDecoyToolFailsIndependently(t *testing.T) {
	t.Parallel()
	designated := designatedCLIVerbs()
	toolNames := append(append([]string(nil), emptyRegistry().ToolNames()...), "a2a_decoy_capability")

	problems := checkBijection(designated, toolNames)
	if len(problems) == 0 {
		t.Fatal("expected the decoy tool to fail the bijection check")
	}
	found := false
	for _, p := range problems {
		if strings.Contains(p, "a2a_decoy_capability") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a problem naming the decoy tool, got: %v", problems)
	}
}

// TestMCPParityDecoyVerbFailsIndependently is AC #3's second half: a
// designated verb with no corresponding tool fails the check on its own.
func TestMCPParityDecoyVerbFailsIndependently(t *testing.T) {
	t.Parallel()
	designated := append(append([]string(nil), designatedCLIVerbs()...), "decoy-verb-with-no-tool")
	toolNames := emptyRegistry().ToolNames()

	problems := checkBijection(designated, toolNames)
	if len(problems) == 0 {
		t.Fatal("expected the decoy verb to fail the bijection check")
	}
	found := false
	for _, p := range problems {
		if strings.Contains(p, "a2a_decoy_verb_with_no_tool") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a problem naming the decoy verb's tool, got: %v", problems)
	}
}

// TestMCPParityExcludedVerbsAbsent is AC #6: none of the CLI-only
// (excluded) verb names appear as MCP tools.
func TestMCPParityExcludedVerbsAbsent(t *testing.T) {
	t.Parallel()
	toolNames := make(map[string]bool)
	for _, n := range emptyRegistry().ToolNames() {
		toolNames[n] = true
	}
	for verb := range mcpExcludedVerbs {
		toolName := verbToToolName(verb)
		if toolNames[toolName] {
			t.Errorf("excluded verb %q must have NO MCP tool, but %q is registered", verb, toolName)
		}
	}
}

// TestMCPParityContractSubverbsExpanded proves the contract family is
// enumerated via cli.ContractSubcommands(), not the bare `contract`
// buildCommands() key (which dispatches a bare switch, spec 14 Placement
// decision: "two-level CLI enumeration").
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
	for _, sub := range cli.ContractSubcommands() {
		if !designatedSet["contract-"+sub.Name] {
			t.Errorf("expected contract-%s in the designated set", sub.Name)
		}
	}
}
