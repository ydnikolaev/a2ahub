package validate

import (
	"sort"
	"strings"
)

// SupersedeLink is one supersede EVENT's own linkage, projected down to the
// two ids it connects. §3.8's `supersedes` ENVELOPE field is dead
// (thread.go's own doc comment: "the dead `supersedes` envelope field") —
// the real link lives on the `supersede` EVENT §3.4/§5.2.2's committed
// event/v1 document authors when `a2a supersede <predecessor-id> --refs
// <successor-id>` runs (internal/cli/cmd_lifecycle.go): `subject` is the
// predecessor being superseded, and `refs[].ref` names the successor.
//
// A caller collects one SupersedeLink per supersede event it reads off
// disk. Both ids must be CANONICAL (bare) — the same grammar
// internal/artifact.ParseID accepts, with no trailing `@version` or
// `#digest` pin — because this package compares them by exact string
// equality to build the graph; a caller that reads a pinned ref off an
// event's `refs[]` must strip the pin before constructing a SupersedeLink,
// the same normalization internal/cli's own ref-parsing call sites already
// perform for other pinned refs.
type SupersedeLink struct {
	// Successor is the newer artifact's id — the one whose supersede event
	// carries this link.
	Successor string
	// Predecessor is the id being superseded — the supersede event's own
	// `subject`.
	Predecessor string
}

// CheckSupersessionGraph is CC-024 (fork: two successors claim one
// predecessor) and CC-025 (cycle) — plan 03 §3.8's "supersession chains
// MUST be linear (validator rejects forks; see CC catalog)" — evaluated
// over the FULL repository's supersede-event graph. Both questions are
// unanswerable from a single artifact or a single PR diff: a fork needs
// every supersede event that names a given predecessor, wherever in the
// repo it was committed; a cycle needs the whole graph's shape. That is
// why this is a V3 (full-repo merge-gate) check, not a per-draft one — see
// cmd_validate_ci.go's wiring comment for why v3-pr deliberately never
// calls this function.
//
// Pure and deterministic: the same links slice, in any order, produces the
// same set of violations. A fork is a fork whichever artifact the repo
// walk happened to read first; a cycle is a cycle regardless of which of
// its own members the caller's slice lists first. (The violation SET is
// order-independent; each individual violation's Subjects list is sorted
// for a stable, diffable report.)
//
// A link with an empty Successor or empty Predecessor is skipped — an
// incomplete link cannot be judged either a fork or a cycle participant,
// and the caller's own event-schema validation already refuses a
// supersede event missing subject/refs before this function ever sees it.
func CheckSupersessionGraph(links []SupersedeLink) []Violation {
	var violations []Violation
	violations = append(violations, checkSupersessionForks(links)...)
	violations = append(violations, checkSupersessionCycles(links)...)
	return violations
}

// checkSupersessionForks is CC-024: two (or more) DISTINCT successors each
// claim the same predecessor. Distinct successors is deliberate — the same
// successor linked to the same predecessor by two separately-committed
// events (a duplicate write, or the same PR merged twice into the walk by
// caller error) is a repeated CLAIM, not a competing one, and is not what
// §3.8's linearity guard exists to catch.
func checkSupersessionForks(links []SupersedeLink) []Violation {
	successorsOf := map[string]map[string]bool{}
	for _, l := range links {
		if l.Predecessor == "" || l.Successor == "" {
			continue
		}
		if successorsOf[l.Predecessor] == nil {
			successorsOf[l.Predecessor] = map[string]bool{}
		}
		successorsOf[l.Predecessor][l.Successor] = true
	}

	predecessors := make([]string, 0, len(successorsOf))
	for p := range successorsOf {
		predecessors = append(predecessors, p)
	}
	sort.Strings(predecessors)

	var violations []Violation
	for _, predecessor := range predecessors {
		successorSet := successorsOf[predecessor]
		if len(successorSet) <= 1 {
			continue
		}
		claimants := make([]string, 0, len(successorSet))
		for s := range successorSet {
			claimants = append(claimants, s)
		}
		sort.Strings(claimants)
		subjects := append([]string{predecessor}, claimants...)
		violations = append(violations, Violation{
			Code:     "REF-020",
			Class:    ClassReferential,
			Path:     "",
			Message:  "supersession fork: predecessor " + predecessor + " is claimed by more than one successor (" + strings.Join(claimants, ", ") + ")",
			Severity: SeverityReject,
			Subjects: subjects,
		})
	}
	return violations
}

// checkSupersessionCycles is CC-025, including the self-supersede
// degenerate case (an artifact naming itself as its own predecessor is a
// 1-node cycle). Standard directed-graph cycle detection: colour DFS over
// the Successor->Predecessor edges §3.8's "successor points backward"
// describes, walked from every node in deterministic (sorted) order so the
// result never depends on map iteration order.
//
// Each node is coloured white (unvisited) -> gray (on the current DFS
// stack) -> black (fully explored). Reaching a gray node closes a cycle;
// reaching a black node means that subgraph was already fully explored
// (with no cycle found through it) by an earlier walk, so it is never
// re-examined — which is also what keeps a cycle reachable from several
// entry points from being reported more than once: the first DFS to reach
// it consumes the whole cycle and blackens every node in it before any
// other entry point's walk can reach the same nodes.
func checkSupersessionCycles(links []SupersedeLink) []Violation {
	adjacency := map[string][]string{}
	nodeSet := map[string]bool{}
	for _, l := range links {
		if l.Predecessor == "" || l.Successor == "" {
			continue
		}
		adjacency[l.Successor] = append(adjacency[l.Successor], l.Predecessor)
		nodeSet[l.Successor] = true
		nodeSet[l.Predecessor] = true
	}
	for node := range adjacency {
		sort.Strings(adjacency[node])
	}

	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(nodes))
	var stack []string
	var violations []Violation

	var visit func(node string)
	visit = func(node string) {
		color[node] = gray
		stack = append(stack, node)
		for _, next := range adjacency[node] {
			switch color[next] {
			case white:
				visit(next)
			case gray:
				idx := len(stack) - 1
				for stack[idx] != next {
					idx--
				}
				cycle := append([]string(nil), stack[idx:]...)
				sortedCycle := append([]string(nil), cycle...)
				sort.Strings(sortedCycle)
				violations = append(violations, Violation{
					Code:     "REF-021",
					Class:    ClassReferential,
					Path:     "",
					Message:  "supersession cycle: " + strings.Join(cycle, " -> ") + " -> " + next,
					Severity: SeverityReject,
					Subjects: sortedCycle,
				})
			case black:
				// Already fully explored with no cycle through it — not
				// revisited (see this function's own doc comment).
			}
		}
		stack = stack[:len(stack)-1]
		color[node] = black
	}

	for _, node := range nodes {
		if color[node] == white {
			visit(node)
		}
	}
	return violations
}
