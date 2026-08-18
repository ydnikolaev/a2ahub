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
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
)

// groupedToolNames is the P15 capability-grouped tool set (spec 15 §T1).
var groupedToolNames = []string{
	"a2a_contract", "a2a_data", "a2a_exchange", "a2a_lifecycle",
	"a2a_new", "a2a_read", "a2a_submit", "a2a_whatsnew", "a2a_work",
}

// mcpExcludedVerbs is the CLI-only verb set with NO MCP tool by design
// (spec 14 §T1 scope note + AC #6, carried into P15 unchanged).
var mcpExcludedVerbs = map[string]bool{
	"version":       true,
	"init":          true,
	"connect":       true,
	"disconnect":    true,
	"doctor":        true,
	"template":      true,
	"sync":          true,
	"update":        true, // P19 OP-217: self-update is a host-machine act, CLI-only (spec 19 §9, R-018)
	"validate":      true,
	"mcp":           true, // the mcp verb itself: no self-referencing tool
	"statusline":    true, // spec 14 scope note + AC #6 (see file doc comment)
	"await":         true, // P47: waits on a machine-local pending marker and refreshes that machine's mirror
	"__catalog":     true, // P13's CLI-only meta verb (catalog.go): no MCP tool
	"skill":         true, // P20: installs the skill tree to the local repo — a host-machine act, CLI-only (like update)
	"html":          true, // OP-214: renders a local HTML file — a host-machine act, CLI-only
	"dashboard":     true, // alias of html — CLI-only
	"serve":         true, // loopback HTTP process lifecycle is a host-machine act, CLI-only
	"completion":    true, // P23/OP-222: prints a shell completion script — a host-machine act, CLI-only
	"notifications": true, // P49/OP-223: installs and coordinates host-native UI components — CLI-only
	"feedback":      true, // P25: files feedback on a2a itself (consumer submit + hub-operator triage) — a host act, CLI-only (spec 25 §T1: triage "Not exposed via MCP")
	"space":         true, // P33 §12: scaffolds a NEW space repo onto the local filesystem — an operator/host act outside any connected space, CLI-only (like init/skill/html)
	// TEMPORARY (space-notify-2026-08 P3): the MCP twins for notify's sub-verbs
	// land in P6 (W4). This is the ONLY time-limited entry in this list — every
	// other row states a permanent design reason. P6 deletes this line and P7's
	// freeze gate fails if it is still here.
	"notify": true,
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
	case "a2a_data":
		// `attach` is the one a2a_data action whose CLI half is NOT a `data`
		// sub-verb. P4 decided attachment is a first-class act and gave it a
		// designated top-level verb, while MCP keeps it grouped under the
		// data tool beside `deliver`, which it generalises. So this one
		// action projects to the bare verb name; every other keeps the
		// "data-" prefix the comment below explains.
		if ta.action == "attach" {
			return "attach"
		}
		// "fetch-blob" is a2a_data's OWN second top-level exception, mirroring
		// "attach" immediately above for the identical reason — P10
		// (agent-exchange-2026-08) spec 10 §3 seam 6: `a2a fetch <BL-id>
		// --to <dir>` is a designated top-level CLI verb (it resolves ANY
		// blob-shaped attachment, not only a `DP-` package — `data fetch`'s
		// own territory, untouched), while MCP keeps it grouped under
		// a2a_data beside `fetch`, which it generalises. It cannot be named
		// the bare "fetch" action (a2a_data already has one, for `DP-`
		// packages), so it is "fetch-blob" here and projects to the bare
		// "fetch" verb through this case. Without it, this action would fall
		// to the default below and project to "data-fetch-blob", which no
		// designated verb answers (seeded-red receipt: remove this case and
		// TestMCPParityBijection fails naming BOTH "fetch" as an unreachable
		// designated verb AND (a2a_data, action="fetch-blob") as a decoy
		// capability — proven empirically before this case was added).
		if ta.action == "fetch-blob" {
			return "fetch"
		}
		// Without this case a2a_data's "verify" would fall to default and
		// project to the same bare "verify" key a2a_exchange's own verify
		// action already claims, and checkBijection's map-keyed-by-verb()
		// would let one silently overwrite the other in `got` — the exact
		// drift class this gate exists to catch (seeded-red receipt: remove
		// this case and TestMCPParityBijection fails naming data-pack,
		// data-deliver, data-fetch, data-verify as unreachable AND a2a_data
		// pack/deliver/fetch as decoy MCP-only capabilities — "verify"
		// itself does not appear in either list because it silently
		// collided with a2a_exchange's own verify action instead of
		// failing loud).
		return "data-" + ta.action
	case "a2a_new":
		return "new"
	case "a2a_submit":
		return "submit"
	case "a2a_whatsnew":
		return "whatsnew"
	case "a2a_work":
		return "work-" + ta.action
	default:
		// a2a_read view == the read verb; a2a_lifecycle / a2a_exchange
		// action == the lifecycle/exchange verb.
		return ta.action
	}
}

// designatedCLIVerbs returns the §7.7-designated CLI verb names:
// buildCommands() keys minus mcpExcludedVerbs, with `contract` expanded to
// `contract-<sub>` for each of cli.ContractSubcommands(), and `data`
// expanded to `data-<sub>` for each of cli.DataSubcommands() (spec 05a) —
// buildCommands()["data"] (wire.go's runData), the a2a_data registration
// (internal/mcp/tools.go), and this file's own groupedToolNames slice +
// mcp.DataActions pairs loop in mcpCapabilityPairs all landed together in
// this wave, so this branch is live from here on.
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
		if name == "data" {
			for _, sub := range cli.DataSubcommands() {
				out = append(out, "data-"+sub.Name)
			}
			continue
		}
		if name == "work" {
			for _, sub := range cli.WorkSubcommands() {
				out = append(out, "work-"+sub)
			}
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestDataSubcommandsMatchMCPDataActions is this wave's own "byte-identical
// to DataSubcommands()" acceptance line for the a2a_data grouped tool's
// closed action enum. cli.DataSubcommands() returns []DataSubcommand (the
// same {Name, Synopsis} shape ContractSubcommands() already uses, so help/
// completion/catalog can read it identically); the projection to plain
// names is what "byte-identical" is checked against, since mcp.DataActions
// is a plain []string enum (mirroring mcp.WorkActions).
//
// This test is fully self-contained: unlike TestMCPParityBijection it does
// NOT depend on buildCommands()["data"] or the registry ever registering
// a2a_data, so it is green today, ahead of that wiring landing.
//
// ONE action is deliberately not a `data` sub-verb: `attach`. P4 gave
// attachment a designated top-level verb because it is the general case
// `data deliver` specialises, while MCP keeps it grouped under a2a_data
// beside the action it generalises. That asymmetry is a decision, so it is
// named here rather than absorbed into a looser comparison — every OTHER
// action must still match byte-for-byte and in order.
func TestDataSubcommandsMatchMCPDataActions(t *testing.T) {
	t.Parallel()
	names := make([]string, len(cli.DataSubcommands()))
	for i, sub := range cli.DataSubcommands() {
		names[i] = sub.Name
	}
	// topLevelDataActions are the a2a_data actions whose CLI half is a
	// designated top-level verb instead of a `data` sub-verb. Adding an
	// entry here is a decision about the CLI's shape; a typo just moves the
	// failure to TestMCPParityBijection, which resolves the bare verb name.
	topLevelDataActions := map[string]bool{"attach": true, "fetch-blob": true}
	var wantSubs []string
	for _, action := range mcp.DataActions {
		if topLevelDataActions[action] {
			continue
		}
		wantSubs = append(wantSubs, action)
	}
	if !reflect.DeepEqual(names, wantSubs) {
		t.Fatalf("cli.DataSubcommands() names must be byte-identical, in order, to mcp.DataActions minus the top-level ones %v: got %v, want %v", topLevelDataActions, names, wantSubs)
	}
	// And the exclusion must not be a way to hide a missing verb: every
	// top-level exception has to actually BE a designated verb. Resolved
	// through toolAction.verb() — NOT the bare action name — because that
	// projection is not always the identity: "attach" happens to name both
	// its own action and its own verb, but "fetch-blob" (P10
	// agent-exchange-2026-08 spec 10 §3 seam 6) does not — it projects to
	// the bare "fetch" verb (verb()'s own case), so buildCommands()["fetch-
	// blob"] would never exist even though the capability IS reachable.
	for action := range topLevelDataActions {
		verb := (toolAction{tool: "a2a_data", action: action}).verb()
		if _, ok := buildCommands()[verb]; !ok {
			t.Errorf("a2a_data action %q is excused from the sub-verb list as top-level, but its own designated verb %q is not registered — the exclusion is hiding an unreachable capability", action, verb)
		}
	}
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
	pairs = append(pairs, toolAction{"a2a_new", ""}, toolAction{"a2a_submit", ""}, toolAction{"a2a_whatsnew", ""})
	for _, a := range mcp.LifecycleActions {
		pairs = append(pairs, toolAction{"a2a_lifecycle", a})
	}
	for _, a := range mcp.ExchangeActions {
		pairs = append(pairs, toolAction{"a2a_exchange", a})
	}
	for _, a := range mcp.ContractActions {
		pairs = append(pairs, toolAction{"a2a_contract", a})
	}
	for _, a := range mcp.DataActions {
		pairs = append(pairs, toolAction{"a2a_data", a})
	}
	for _, a := range mcp.WorkActions {
		pairs = append(pairs, toolAction{"a2a_work", a})
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
