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
	"strconv"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/lane"
)

// ungatedListPath is W2's file (D-6: the LEAD adjudicates its entries, not
// an agent). readUngatedList below already treats its absence as "nothing
// ungated" rather than an error — the header-only file W2 ships keeps that
// path exercised without pre-populating the residue.
const ungatedListPath = "scripts/lib/lane-ungated.txt"

// opaqueCeilingPath is P1's US-5 ratchet file — the same shape
// scripts/lib/refusal-ratchet-budget.txt uses for a different debt: a
// stored ceiling, free to fall, red on growth. readOpaqueCeiling below
// treats its absence as "not enforced here" (the readUngatedList idiom),
// not an error — a fixture tree under LANE_ROOT carries none.
const opaqueCeilingPath = "scripts/lib/lane-opaque-ceiling.txt"

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
	case "--write-opaque-ceiling":
		// US-5's `--write`, scripts/check-refusal-ratchet.sh's own shape:
		// regenerate the ceiling from the CURRENT tree's opaqueCount. Never
		// invoked automatically by --verify — a maintainer runs this
		// explicitly after resolving a construct (or accepting a deliberate
		// growth), then reviews the diff before committing.
		os.Exit(runWriteOpaqueCeiling(root))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run internal/lane/lanecheck.go --verify")
	fmt.Fprintln(os.Stderr, "       go run internal/lane/lanecheck.go --derive <path>...")
	fmt.Fprintln(os.Stderr, "       go run internal/lane/lanecheck.go --phases <path>...   # names only, for scripts")
	fmt.Fprintln(os.Stderr, "       go run internal/lane/lanecheck.go --write-opaque-ceiling")
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

	// D-11: the honesty pass — does each declared script's actual reads
	// match what it declared? Run BEFORE Coverage: P1's three-valued
	// verdict needs the reads/opacity evidence (backing) Coverage now
	// consults, and HonestyCheck is the one pass that already extracts it —
	// reordering these two calls (they used to run Coverage first) is the
	// whole plumbing change, not a second reads-extraction pass.
	honestyRefusals, opaqueCount, backing, err := lane.HonestyCheck(root, decls)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not run the honesty pass:", err)
		return 1
	}
	refusals = append(refusals, honestyRefusals...)
	refusals = append(refusals, lane.Coverage(decls, universe, ungated, backing)...)

	// PHASES, not scripts. HonestyCheck counts one per phase whose backing
	// window carries the directive, and scripts/verify.sh backs several
	// phases — so this number has always been phase-instances while the label
	// said "script(s)". A debt metric that misnames its own unit invites the
	// wrong conclusion from whoever tries to shrink it. This exact line is
	// pinned verbatim by scripts/check-lane-declarations.sh --teeth's own
	// grep -Fq checks — printed on every run regardless of pass/fail.
	fmt.Println("lane:", opaqueCount, "phase(s) declare lane-reads-opaque.")

	// US-2's debt metric beside it: how many (phase, glob) claims the
	// extractor could back neither by a read nor by an opacity directive —
	// independent of whether a currently-tracked path exercises the claim.
	unbackedExcused, unbackedBare := lane.UnbackedClaimSplit(decls, backing)
	// Two numbers, not one. The aggregate would hide the half that matters:
	// a glob the extractor could not resolve AND whose phase declares no
	// directive is an unexcused claim, which is what P1 exists to surface.
	// Averaging it into the excused population is how a debt metric stops
	// being a signal.
	fmt.Printf("lane: %d declared glob(s) have no resolved read — %d excused by a lane-reads-opaque directive, %d unexcused.\n",
		unbackedExcused+unbackedBare, unbackedExcused, unbackedBare)

	// US-5: the opaque ceiling. A ceiling file absent under this root is not
	// itself an error — the same "nothing ungated" idiom readUngatedList
	// already uses — so a fixture tree under LANE_ROOT that carries none
	// (scripts/check-lane-declarations.sh --teeth's own fixtures) is simply
	// not enforced, rather than refusing on a file it was never given.
	ceiling, ceilingPresent, cerr := readOpaqueCeiling(root)
	if cerr != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not read", opaqueCeilingPath+":", cerr)
		return 1
	}
	if ceilingPresent {
		refusals = append(refusals, lane.CheckOpaqueCeiling(opaqueCount, ceiling)...)
	}

	if len(refusals) == 0 {
		fmt.Println("lane: OK —", len(corpus), "corpus phase(s),", len(decls), "declaration(s), universe covered.")
		return 0
	}
	printRefusals(refusals)
	return 1
}

// readOpaqueCeiling reads opaqueCeilingPath's stored integer (the first
// non-blank, non-"#"-comment line) relative to root. Absence is legal (not
// present, no error) — the readUngatedList idiom; a malformed file (present
// but no parseable integer line) is a loud error, never a silent 0.
func readOpaqueCeiling(root string) (ceiling int, present bool, err error) {
	f, err := os.Open(filepath.Join(root, opaqueCeilingPath))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n, perr := strconv.Atoi(line)
		if perr != nil {
			return 0, false, fmt.Errorf("%q is not an integer", line)
		}
		return n, true, nil
	}
	if err := sc.Err(); err != nil {
		return 0, false, err
	}
	return 0, false, fmt.Errorf("carries no integer line")
}

// runWriteOpaqueCeiling regenerates opaqueCeilingPath from the CURRENT
// tree's opaqueCount — scripts/check-refusal-ratchet.sh's own write_budget
// shape, applied to a single global count rather than a per-file table.
func runWriteOpaqueCeiling(root string) int {
	decls, err := lane.Load(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not load declarations:", err)
		return 1
	}
	_, opaqueCount, _, err := lane.HonestyCheck(root, decls)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not run the honesty pass:", err)
		return 1
	}
	const header = `# scripts/lib/lane-opaque-ceiling.txt — P1 (computed-not-listed-2026-08)'s
# ceiling on "lane-reads-opaque" phase-instances across the whole corpus
# (internal/lane.HonestyCheck's opaqueCount, printed by ` + "`make lane-declarations`" + `).
# One integer, alone on its own line. It may only FALL (a construct resolved
# to a literal path, or narrowed enough that the extractor resolves it) or
# stay the same; a tree whose opaqueCount exceeds it reds. The directive
# this counts suppresses D-9's own per-line unresolved-construct refusal —
# it does NOT back a phase's declared globs for Coverage's own three-valued
# verdict (internal/lane/coverage.go's claimVerdict deliberately does not
# consult PhaseBacking.Opaque, so a directive cannot launder an unread glob
# into coverage), which is what keeps this ceiling from ever being the
# escape hatch for THAT defect. It still bounds ITS OWN debt: growth here
# means more constructs are being waved past the classifier, unresolved.
#
# Regenerate with:
#   go run internal/lane/lanecheck.go --write-opaque-ceiling
# then review the diff before committing.
`
	body := fmt.Sprintf("%s%d\n", header, opaqueCount)
	if err := os.WriteFile(filepath.Join(root, opaqueCeilingPath), []byte(body), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not write", opaqueCeilingPath+":", err)
		return 1
	}
	fmt.Println("lane: wrote", opaqueCeilingPath, "at", opaqueCount)
	return 0
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

	// --derive needs the SAME backing evidence --verify consults, or the two
	// modes can disagree on a path's claimed verdict — the contradiction
	// derive.go's own comments name twice. HonestyCheck's own refusals (the
	// reads-without-a-claim direction) are deliberately NOT surfaced here,
	// same as before this phase: --derive has never printed them, and P1
	// does not change that.
	_, _, backing, herr := lane.HonestyCheck(root, decls)
	if herr != nil {
		fmt.Fprintln(os.Stderr, "lane: FAIL — could not run the honesty pass:", herr)
		return 1
	}

	refusals := lane.Reconcile(decls, corpus)
	sel, deriveRefusals := lane.Derive(decls, changed, backing)
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
