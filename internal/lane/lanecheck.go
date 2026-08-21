//go:build ignore

// lanecheck is the P12 lane-derivation CLI. It shells no bash and
// re-implements no parsing of its own (D-7) — internal/lane owns the whole
// derivation; this file is a thin `go run` wrapper around it, exactly the
// internal/coveragepolicy/covercheck.go precedent.
//
// Two modes, run from the repo root (a repo-relative `go run` path only
// resolves from there):
//
//	go run internal/lane/lanecheck.go --verify
//	go run internal/lane/lanecheck.go --derive <path>...
//	git diff --name-only main... | go run internal/lane/lanecheck.go --derive
//
// LANE_ROOT overrides the tree acted on (default "."), the same seam
// check-readme.sh's README_PATH and epic_docs_drift.sh's FEATURES_DIR
// already use — scripts/check-lane-declarations.sh --teeth points it at a
// fixture under internal/lane/testdata/ so its receipts never touch the
// live repo.
//
// It is NOT wired into REPO_GATES/verify.sh yet — the lead adjudicates the
// coverage residue (scripts/lib/lane-ungated.txt) before that wiring
// lands (plan D-6, the W2 sequencing constraint); scripts/check-lane-
// declarations.sh already shells to --verify and is ready for that wiring.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/lane"
)

// ungatedListPath is W2's file (D-6: the LEAD adjudicates its entries, not
// an agent). readUngatedList below already treats its absence as "nothing
// ungated" rather than an error — the header-only file W2 ships keeps that
// path exercised without pre-populating the residue.
const ungatedListPath = "scripts/lib/lane-ungated.txt"

// laneRoot resolves the tree --verify/--derive act on: LANE_ROOT when set
// (scripts/check-lane-declarations.sh --teeth points this at a fixture
// under internal/lane/testdata/, the same seam check-readme.sh's own
// README_PATH and epic_docs_drift.sh's FEATURES_DIR already use), "."
// otherwise — the real repo root when run from there directly.
func laneRoot() string {
	if r := os.Getenv("LANE_ROOT"); r != "" {
		return r
	}
	return "."
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	root := laneRoot()
	switch os.Args[1] {
	case "--verify":
		os.Exit(runVerify(root))
	case "--derive":
		os.Exit(runDerive(root, os.Args[2:], false))
	case "--phases":
		// The machine-readable half of --derive: phase names only, one per
		// line, so `verify.sh lane` can execute the derived set. Same
		// derivation, same refusals, same exit code — a second code path
		// that could disagree with the human-readable one is the copy
		// problem this package exists to remove.
		os.Exit(runDerive(root, os.Args[2:], true))
	case "--ship-phases":
		// The ship-tier subset of --phases, one name per line. Kept as its
		// own flag rather than an annotation on --phases so the existing
		// format has no second meaning: every consumer that greps a phase
		// name out of --phases keeps working unchanged, and a runner that
		// has not learned about tiers cannot silently mis-read one.
		os.Exit(runDerive(root, os.Args[2:], true, lane.TierShip))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run internal/lane/lanecheck.go --verify")
	fmt.Fprintln(os.Stderr, "       go run internal/lane/lanecheck.go --derive <path>...")
	fmt.Fprintln(os.Stderr, "       go run internal/lane/lanecheck.go --phases <path>...   # names only, for scripts")
	fmt.Fprintln(os.Stderr, "       <changed paths on stdin> | go run internal/lane/lanecheck.go --derive")
}

func runVerify(root string) int {
	decls, err := lane.Load(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not load declarations:", err)
		return 1
	}
	corpus, err := lane.Corpus(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not discover the corpus:", err)
		return 1
	}
	universe, err := lane.Universe(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not build the coverage universe:", err)
		return 1
	}
	ungated, err := readUngatedList(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not read", ungatedListPath+":", err)
		return 1
	}

	refusals := lane.Reconcile(decls, corpus)
	refusals = append(refusals, lane.Coverage(decls, universe, ungated)...)

	// D-11: the honesty pass — does each declared script's actual reads
	// match what it declared? opaqueCount is printed on every run
	// regardless of pass/fail: "N scripts declare opaque reads" is a debt
	// with a size, not a nicety to mention only when something is wrong.
	honestyRefusals, opaqueCount, err := lane.HonestyCheck(root, decls)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not run the honesty pass:", err)
		return 1
	}
	refusals = append(refusals, honestyRefusals...)
	// PHASES, not scripts. HonestyCheck counts one per phase whose backing
	// window carries the directive, and scripts/verify.sh backs several
	// phases — so this number has always been phase-instances while the label
	// said "script(s)". A debt metric that misnames its own unit invites the
	// wrong conclusion from whoever tries to shrink it.
	fmt.Println("lane:", opaqueCount, "phase(s) declare lane-reads-opaque.")

	if len(refusals) == 0 {
		fmt.Println("lane: OK —", len(corpus), "corpus phase(s),", len(decls), "declaration(s), universe covered.")
		return 0
	}
	printRefusals(refusals)
	return 1
}

func runDerive(root string, args []string, namesOnly bool, only ...lane.Tier) int {
	changed := args
	if len(changed) == 0 {
		changed = readStdinPaths()
	}

	decls, err := lane.Load(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not load declarations:", err)
		return 1
	}
	corpus, err := lane.Corpus(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not discover the corpus:", err)
		return 1
	}
	ungated, err := readUngatedList(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not read", ungatedListPath+":", err)
		return 1
	}

	refusals := lane.Reconcile(decls, corpus)
	sel, deriveRefusals := lane.Derive(decls, changed)
	for _, r := range deriveRefusals {
		// Glob match, not exact string membership — lane.MatchesAnyGlob's
		// own doc says why (lane-ungated.txt is root-shaped globs, D-6).
		if lane.MatchesAnyGlob(ungated, r.Subject) {
			continue
		}
		refusals = append(refusals, r)
	}

	telemetry := lane.ReadTelemetry(root)
	sort.Slice(sel.Phases, func(i, j int) bool {
		return sel.Phases[i].Declaration.Phase < sel.Phases[j].Declaration.Phase
	})
	for _, p := range sel.Phases {
		if len(only) > 0 && p.Declaration.Tier != only[0] {
			continue
		}
		if namesOnly {
			fmt.Println(p.Declaration.Phase)
			continue
		}
		est := lane.EstimateFor(telemetry[p.Declaration.Phase])
		switch p.Declaration.Kind {
		case lane.KindAlways:
			fmt.Printf("%s  ALWAYS (%s) — %s\n", p.Declaration.Phase, p.Declaration.Reason, est)
		case lane.KindScoped:
			if p.Declaration.Tier == lane.TierShip {
				// Named, never implied. A phase the derivation selects and
				// the commit lane does not execute must SAY so here, or the
				// reader takes the list for the set that will run.
				fmt.Printf("%s  %s — %s [SHIP TIER: derived, executed by the ship lane, not by `make lane-run`]\n",
					p.Declaration.Phase, matchSummary(p.Matched), est)
				continue
			}
			fmt.Printf("%s  %s — %s\n", p.Declaration.Phase, matchSummary(p.Matched), est)
		default:
			fmt.Printf("%s  %s — %s\n", p.Declaration.Phase, matchSummary(p.Matched), est)
		}
	}

	if len(refusals) > 0 {
		printRefusals(refusals)
		return 1
	}
	return 0
}

// printRefusals prints every refusal's String() plus its Subject on its
// own line. Refusal.String() (Problem + Fix) never carries Subject — it is
// shared by every refusal kind (coverage_test.go, reconcile_test.go assert
// its exact text) and most of them already repeat Subject inside Problem
// (Coverage's "no gate claims docs/guide.md" IS docs/guide.md). But an
// orphan-declaration refusal's Subject is a file:line no Problem text
// carries, and D-11's honesty refusals are the same shape (Subject =
// "script.sh:N", the exact site the operator needs) — so printing it
// unconditionally is the one rule that names "the thing" for every kind,
// not a per-kind special case that silently misses the next one.
func printRefusals(refusals []lane.Refusal) {
	for _, r := range refusals {
		fmt.Fprintln(os.Stderr, r.String())
		if r.Subject != "" {
			fmt.Fprintln(os.Stderr, "  at", r.Subject)
		}
	}
}

// matchSummary reports WHICH declared input matched, not just which
// changed path did — "why did this phase get selected" is answered by the
// pattern (e.g. "**/*.go"), not by the (potentially very long) list of
// paths it happened to claim this run.
func matchSummary(matched []lane.MatchedPath) string {
	byPattern := map[string][]string{}
	var order []string
	for _, m := range matched {
		if _, ok := byPattern[m.Pattern]; !ok {
			order = append(order, m.Pattern)
		}
		byPattern[m.Pattern] = append(byPattern[m.Pattern], m.Path)
	}
	var parts []string
	for _, pat := range order {
		parts = append(parts, fmt.Sprintf("%s -> %v", pat, byPattern[pat]))
	}
	return "matched " + strings.Join(parts, "; ")
}

func readUngatedList(root string) ([]string, error) {
	f, err := os.Open(filepath.Join(root, ungatedListPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var paths []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		paths = append(paths, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func readStdinPaths() []string {
	var paths []string
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		paths = append(paths, line)
	}
	return paths
}
