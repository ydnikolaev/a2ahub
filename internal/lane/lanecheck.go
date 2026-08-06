//go:build ignore

// lanecheck is the P12 lane-derivation CLI. It shells no bash and
// re-implements no parsing of its own (D-7) — internal/lane owns the whole
// derivation; this file is a thin `go run` wrapper around it, exactly the
// internal/coveragepolicy/covercheck.go precedent.
//
// Two modes, run from the repo root:
//
//	go run internal/lane/lanecheck.go --verify
//	go run internal/lane/lanecheck.go --derive <path>...
//	git diff --name-only main... | go run internal/lane/lanecheck.go --derive
//
// It is NOT wired into the Makefile or verify.sh — that is a later wave's
// (W2/W3) job; this file only has to run correctly when invoked by hand.
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

// ungatedListPath is W2's file — it does not exist yet in this wave, and
// its absence is not an error: an empty ungated list is the correct
// starting point, not a missing dependency.
const ungatedListPath = "scripts/lib/lane-ungated.txt"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	root := "."
	switch os.Args[1] {
	case "--verify":
		os.Exit(runVerify(root))
	case "--derive":
		os.Exit(runDerive(root, os.Args[2:]))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run internal/lane/lanecheck.go --verify")
	fmt.Fprintln(os.Stderr, "       go run internal/lane/lanecheck.go --derive <path>...")
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

	if len(refusals) == 0 {
		fmt.Println("lane: OK —", len(corpus), "corpus phase(s),", len(decls), "declaration(s), universe covered.")
		return 0
	}
	printRefusals(refusals)
	return 1
}

func runDerive(root string, args []string) int {
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
	ungatedSet := map[string]bool{}
	for _, p := range ungated {
		ungatedSet[p] = true
	}

	refusals := lane.Reconcile(decls, corpus)
	sel, deriveRefusals := lane.Derive(decls, changed)
	for _, r := range deriveRefusals {
		if ungatedSet[r.Subject] {
			continue
		}
		refusals = append(refusals, r)
	}

	telemetry := lane.ReadTelemetry(root)
	sort.Slice(sel.Phases, func(i, j int) bool {
		return sel.Phases[i].Declaration.Phase < sel.Phases[j].Declaration.Phase
	})
	for _, p := range sel.Phases {
		est := lane.EstimateFor(telemetry[p.Declaration.Phase])
		switch p.Declaration.Kind {
		case lane.KindAlways:
			fmt.Printf("%s  ALWAYS (%s) — %s\n", p.Declaration.Phase, p.Declaration.Reason, est)
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

func printRefusals(refusals []lane.Refusal) {
	for _, r := range refusals {
		fmt.Fprintln(os.Stderr, r.String())
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
