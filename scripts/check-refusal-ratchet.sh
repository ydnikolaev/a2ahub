#!/usr/bin/env bash
# check-refusal-ratchet.sh — answers-that-hold-2026-08 P4 (spec
# 04-a-refusal-names-the-caller-state.md §T1/§6, AC-6/7/8): a per-file
# ceiling on raw `err`-to-stderr passthrough in internal/cli, outside the
# NewRefusal constructor (internal/cli/refusal_state.go). It does not, and
# cannot, judge whether a message "names the caller's state" — that is
# prose quality, and a gate asserting it either greps banned substrings
# (defeated in one commit) or gets declared around into inertness (spec 04's
# own §T1 note). What it CAN judge, mechanically, is growth: a new raw
# err-print appearing outside the constructor moves a file's count past its
# stored ceiling, and that reds. Migrating a site to NewRefusal lowers the
# count, the ceiling is lowered to match, and the tree stays green — "a
# debt with a size", the same shape check-lane-declarations.sh's own
# corpus/declaration reconciliation already uses for a different debt.
#
# THE ANCHOR IS THE SINK, DELIBERATELY, NOT THE ORIGIN. Anchoring on the
# origin (os.ReadFile and friends) finds five sites and MISSES the three
# worst live ones, whose error arrives through a dependency seam with no
# `os` call visible where the message is printed (spec 04 §T1). The sink is
# an `err`-shaped identifier — a bare `err`, or an identifier ending in
# `Err`/`err` (readErr, manifestErr, remoteErr — this file's own local
# convention, see cmd_notify_setup.go) — appearing among the arguments of a
# write to a destination named `Stderr` (stdio.Stderr, os.Stderr).
#
# THE ONE THING THAT MAKES THAT GRAMMAR WORK: "Stderr" (S-t-d-e-r-r) itself
# ends in the three letters "err", so a naive scan for `err` anywhere on a
# stderr-write line matches EVERY SUCH LINE via its own destination
# argument — including a bare usage/hint line carrying no error at all,
# which is exactly the false-green AC-8 exists to name ("a usage or hint
# line carrying no error must never enter the gate's universe"; the
# measured shape of that class is ~200 lines). This scan therefore strips
# every "Stderr"/"stderr" token from a candidate line BEFORE testing for an
# err-shaped identifier — see count_site_lines below and its --teeth
# fixture 3.
#
# Usage: bash scripts/check-refusal-ratchet.sh            # verify (CI/commit gate)
#        bash scripts/check-refusal-ratchet.sh --write    # regenerate the budget from the current tree
#        bash scripts/check-refusal-ratchet.sh --teeth    # this gate's own self-test

# lane-reads-opaque: the gate-lib source is written as
# "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh" — the repo-wide idiom every
# gate here uses, and a literal the extractor cannot resolve through the
# subshell.
#
# lane-reads-opaque: the sink scan greps "$f", which iterates the non-test .go
# files under internal/cli — already covered by the declaration below, but
# built from a loop variable.
#
# lane-reads-opaque: the AC-4 arity check runs `go run "$work/arity_check.go"`,
# a Go analyzer this gate WRITES into its own mktemp directory. That path is
# scratch, never a repo path: nothing under it can change this gate's verdict
# on a real tree, so it is deliberately not a lane-input. Same shape
# check_contract_carried_set.sh and check_event_writer_receipts.sh already
# declare for their own generated analyzers.
#
# lane-inputs:
#   internal/cli/**/*.go
#   !internal/cli/**/*_test.go
#   scripts/lib/refusal-ratchet-budget.txt
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

DEFAULT_SCAN_ROOT="$GATE_ROOT/internal/cli"
DEFAULT_BUDGET_FILE="$GATE_ROOT/scripts/lib/refusal-ratchet-budget.txt"

# count_site_lines <file>: number of lines in <file> that write to a
# Stderr-named destination through fmt.Fprint*/Fprintln and carry an
# err-shaped identifier among their arguments (see this file's own header
# for why "Stderr" is stripped first).
count_site_lines() {
  local f="$1"
  grep -E 'Fprint' "$f" 2>/dev/null \
    | grep -E '[Ss]tderr' \
    | sed 's/[Ss]tderr//g' \
    | grep -cE '\b([A-Za-z_][A-Za-z0-9_]*)?[Ee]rr\b'
}

# measure_dir <dir>: prints "<count> <basename>" for every non-test *.go
# file directly under <dir> whose count_site_lines is greater than 0.
measure_dir() {
  local dir="$1" f base count
  for f in "$dir"/*.go; do
    [ -e "$f" ] || continue
    case "$f" in *_test.go) continue ;; esac
    base="$(basename "$f")"
    count="$(count_site_lines "$f")"
    if [ "${count:-0}" -gt 0 ]; then
      printf '%s %s\n' "$count" "$base"
    fi
  done
}

# budget_for <budget-file> <basename>: the stored ceiling for basename, or 0
# when the file names no such line — an unlisted file's budget is 0, so any
# occurrence in it is growth from nothing.
budget_for() {
  local budget_file="$1" name="$2"
  awk -v n="$name" '
    $0 ~ /^#/ { next }
    NF >= 2 && $2 == n { print $1; found=1 }
    END { if (!found) print 0 }
  ' "$budget_file" 2>/dev/null || echo 0
}

# verify_against_budget <dir> <budget-file>: gate_fail's every file whose
# CURRENT count exceeds its stored ceiling; always prints one gate_ok
# summary line naming the total (the lane-declarations idiom: a debt with a
# size, printed with its total).
verify_against_budget() {
  local dir="$1" budget_file="$2" total=0 nfiles=0 base count ceiling
  while IFS=' ' read -r count base; do
    [ -n "${base:-}" ] || continue
    total=$((total + count))
    nfiles=$((nfiles + 1))
    ceiling="$(budget_for "$budget_file" "$base")"
    if [ "$count" -gt "$ceiling" ]; then
      gate_fail "refusal-ratchet: $base carries $count raw err-to-stderr sink site(s) outside NewRefusal, budget is $ceiling — migrate the new site(s) through internal/cli/refusal_state.go's NewRefusal (the per-file budget may only shrink, never grow)"
    fi
  done < <(measure_dir "$dir")
  gate_ok "refusal-ratchet: $total raw err-to-stderr sink site(s) across $nfiles file(s) measured under $dir, at or under scripts/lib/refusal-ratchet-budget.txt's ceiling"
}

# write_budget <dir> <out>: regenerates the budget file from dir's CURRENT
# tree. Never invoked automatically by the verify path — a maintainer runs
# this explicitly after migrating a site, then reviews the diff (a decrement
# is the one honest way to bank a migration, AC-7).
write_budget() {
  local dir="$1" out="$2"
  {
    cat <<'EOF'
# scripts/lib/refusal-ratchet-budget.txt — the per-file CEILING on raw
# `err`-to-stderr passthrough sites in internal/cli, outside
# internal/cli/refusal_state.go's NewRefusal constructor (answers-that-hold-
# 2026-08 P4, spec 04). One line per file that carries at least one site:
# "<count> <basename>". A file's line may only be LOWERED (a site migrated
# to NewRefusal) or removed (its count reaches 0) — check-refusal-ratchet.sh
# reds any file whose CURRENT count exceeds the line here, and reds a
# nonzero-count file that carries no line at all (its budget is implicitly
# 0). Regenerate with:
#   bash scripts/check-refusal-ratchet.sh --write
# then review the diff before committing.
#
# THESE NUMBERS ARE MEASURED AGAINST THIS TREE, NOT COPIED FROM A SPEC. Spec
# 04 quotes ~438/~238/~171/~200 stderr-site counts measured 2026-08-27 under
# a wider (any-stderr-write) definition; this file's own sink-anchored scan
# over the tree at generation time produced the numbers below, and a ratchet
# seeded from someone else's measurement either blocks nothing or blocks
# everything.
EOF
    measure_dir "$dir" | sort -k2,2
  } > "$out"
}

# ── AC-4: the constructor's arity, proven statically ────────────────────
#
# "A refusal constructed without a next step fails to COMPILE" is a fact
# about NewRefusal's own signature: a fixed, non-variadic Go function with
# exactly 3 required parameters cannot be called with 2 — Go's compiler
# refuses that unconditionally, for any such function, in every build. That
# is decidable from the FUNCTION DECLARATION alone, so this proves it by
# parsing internal/cli/refusal_state.go's AST and asserting NewRefusal has
# exactly 3 parameters — deliberately NOT by compiling a scratch package
# that imports internal/cli: this repo's own concurrency convention (three
# sibling agents editing internal/cli/*.go in the same wave) makes a gate
# that builds ANY package under internal/cli's own import path a source of
# spurious contention with unrelated, in-flight edits; a pure AST read of
# one already-tracked file has no such cost and proves the identical fact.
check_constructor_arity() {
  local target="$1" fn="$2" want="$3" work
  work="$(mktemp -d)" || { gate_unmeasured "refusal-ratchet: could not create a scratch dir for the arity check"; return 1; }
  trap 'rm -rf -- "$work"' RETURN
  cat > "$work/arity_check.go" <<'EOF'
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
)

func main() {
	target := os.Getenv("ARITY_TARGET_FILE")
	fn := os.Getenv("ARITY_TARGET_FUNC")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, target, nil, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fn {
			continue
		}
		n := 0
		for _, p := range fd.Type.Params.List {
			if len(p.Names) == 0 {
				n++
			} else {
				n += len(p.Names)
			}
		}
		fmt.Println(n)
		return
	}
	fmt.Fprintln(os.Stderr, "func not found: "+fn)
	os.Exit(2)
}
EOF
  local got
  got="$(ARITY_TARGET_FILE="$target" ARITY_TARGET_FUNC="$fn" go run "$work/arity_check.go" 2>&1)"
  if [ "$got" != "$want" ]; then
    gate_fail "refusal-ratchet: $fn's arity is $got, want $want — a refusal missing its next-step argument would now COMPILE, which is the one thing this constructor exists to forbid (AC-4)"
    return 1
  fi
  gate_ok "refusal-ratchet: $fn requires exactly $want positional argument(s) — a next-step-less refusal does not compile"
}

# ── --teeth ───────────────────────────────────────────────────────────────

teeth_run() { # $1 = dir, $2 = budget file; prints gate_summary's stdout/stderr, returns its exit code
  (
    _GATE_ERRORS=0
    _GATE_WARNINGS=0
    _GATE_UNMEASURED=0
    verify_against_budget "$1" "$2"
    gate_summary "check-refusal-ratchet-teeth"
  ) 2>&1
}

teeth_expect() { # $1 = label, $2 = red|green, $3 = dir, $4 = budget file
  local label="$1" verdict="$2" dir="$3" budget="$4" out rc
  set +e
  out="$(teeth_run "$dir" "$budget")"
  rc=$?
  set -e
  if [ "$verdict" = "red" ]; then
    if [ "$rc" -eq 0 ]; then
      echo "check-refusal-ratchet --teeth: FALSE GREEN — $label did not red:" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-refusal-ratchet --teeth: $label reds"
  else
    if [ "$rc" -ne 0 ]; then
      echo "check-refusal-ratchet --teeth: FALSE RED — $label should green:" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-refusal-ratchet --teeth: $label greens"
  fi
}

run_teeth() {
  local work
  work="$(mktemp -d)" || return 1
  trap 'rm -rf -- "$work"' RETURN

  # AC-6: a file AT budget that GAINS an err-printing site REDS.
  local d1="$work/ac6"
  mkdir -p "$d1"
  cat > "$d1/foo.go" <<'EOF'
package cli

import "fmt"

func f(stdio IO) {
	_, _ = fmt.Fprintf(stdio.Stderr, "one: %v\n", readErr)
	_, _ = fmt.Fprintf(stdio.Stderr, "two: %v\n", parseErr)
	_, _ = fmt.Fprintf(stdio.Stderr, "three: %v\n", writeErr)
}
EOF
  printf '3 foo.go\n' > "$d1/budget.txt"
  teeth_expect "AC6a: exactly at budget greens" green "$d1" "$d1/budget.txt" || exit 1
  cat >> "$d1/foo.go" <<'EOF'

func g(stdio IO) {
	_, _ = fmt.Fprintf(stdio.Stderr, "four: %v\n", closeErr)
}
EOF
  teeth_expect "AC6b: gaining a site past budget reds" red "$d1" "$d1/budget.txt" || exit 1

  # AC-7: a site migrated to the constructor lets its budget line be
  # decremented, and the decremented budget PASSES.
  local d2="$work/ac7"
  mkdir -p "$d2"
  cat > "$d2/bar.go" <<'EOF'
package cli

import "fmt"

func h(stdio IO) {
	_, _ = fmt.Fprintf(stdio.Stderr, "kept: %v\n", keptErr)
	_, _ = fmt.Fprintf(stdio.Stderr, "migrated: %v\n", migratedErr)
}
EOF
  printf '2 bar.go\n' > "$d2/budget.txt"
  teeth_expect "AC7a: two sites at budget 2 greens" green "$d2" "$d2/budget.txt" || exit 1
  # Simulate the migration: the second site now renders through NewRefusal
  # instead of a raw %v — no `Err`-shaped identifier in its stderr write's
  # own arguments any more.
  cat > "$d2/bar.go" <<'EOF'
package cli

import "fmt"

func h(stdio IO) {
	_, _ = fmt.Fprintf(stdio.Stderr, "kept: %v\n", keptErr)
	refusal, _ := NewRefusal("h", "migrated site", "run the fix")
	_, _ = fmt.Fprintln(stdio.Stderr, refusal)
}
EOF
  printf '1 bar.go\n' > "$d2/budget.txt"
  teeth_expect "AC7b: migrated site + decremented budget greens" green "$d2" "$d2/budget.txt" || exit 1

  # AC-8: a usage or hint line carrying no error must NEVER enter the
  # gate's universe — ~200-line shape, budget file names nothing for it.
  local d3="$work/ac8"
  mkdir -p "$d3"
  {
    echo "package cli"
    echo
    echo 'import "fmt"'
    echo
    echo "func usage(stdio IO) {"
    i=0
    while [ "$i" -lt 200 ]; do
      echo '	_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a validate <path> | a2a validate --all")'
      i=$((i + 1))
    done
    echo "}"
  } > "$d3/usage.go"
  : > "$d3/budget.txt"
  teeth_expect "AC8: ~200 usage/hint lines with no error never enter the universe" green "$d3" "$d3/budget.txt" || exit 1
  # Confirm it is not merely permissive — the measured count must be
  # EXACTLY 0, not "0 but only because the budget happens to cover it".
  local measured
  measured="$(measure_dir "$d3")"
  if [ -n "$measured" ]; then
    echo "check-refusal-ratchet --teeth: FALSE — AC8 fixture measured nonzero sites: $measured" >&2
    exit 1
  fi
  echo "check-refusal-ratchet --teeth: AC8 fixture measures exactly 0 sites, confirmed"

  # AC-4: the constructor's own arity.
  check_constructor_arity "$GATE_ROOT/internal/cli/refusal_state.go" NewRefusal 3 || exit 1
  gate_summary "check-refusal-ratchet-teeth-ac4" || exit 1

  echo "check-refusal-ratchet --teeth: PASS — AC-4 (arity), AC-6 (grows past budget reds), AC-7 (migrated + decremented budget greens), AC-8 (usage/hint lines never counted)"
}

# ── entry point ──────────────────────────────────────────────────────────

case "${1:-}" in
--teeth)
  run_teeth
  exit $?
  ;;
--write)
  write_budget "$DEFAULT_SCAN_ROOT" "$DEFAULT_BUDGET_FILE"
  echo "wrote $DEFAULT_BUDGET_FILE"
  ;;
"")
  verify_against_budget "$DEFAULT_SCAN_ROOT" "$DEFAULT_BUDGET_FILE"
  gate_summary "refusal-ratchet"
  exit $?
  ;;
*)
  echo "usage: $0 [--teeth|--write]" >&2
  exit 2
  ;;
esac
