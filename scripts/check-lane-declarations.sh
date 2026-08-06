#!/usr/bin/env bash
# check-lane-declarations.sh — P12 W2 (D-7): the lane-declaration honesty
# gate. It SHELLS INTO internal/lane and parses NOTHING itself — a bash
# reimplementation of "find the phases, parse the headers, intersect the
# globs" would be a second hand-written copy of the very rule it exists to
# police (P11's I9: a check that restated the predicate stayed green after
# the production filter was deleted). This script owns only the root seam,
# the exit-code contract, the human-readable summary, and --teeth's
# fixtures; the derivation, the corpus/declaration reconciliation, the
# coverage check and the D-11 honesty pass all live in internal/lane, once.
#
# Wired into REPO_GATES in the same commit that adjudicated the coverage
# residue into scripts/lib/lane-ungated.txt, and not one commit earlier:
# joining the ceiling with 318 unadjudicated refusals outstanding would have
# reddened `make check` for every checkout, including a concurrent session's.
# Its own declaration below had to land in that same commit for the mirror
# reason — D-2 resolves a script to a phase only once a REPO_GATES target
# invokes it, so a declaration ahead of the wiring makes Load() refuse the
# whole tree.

# lane-inputs: ALWAYS
# lane-reason: its coverage half walks `git ls-files` plus every present
#   private harness root to build the path universe, so ANY newly tracked or
#   newly appearing path anywhere can flip it — the same read that makes
#   classify-guard universal. Its corpus half additionally scans the Makefile,
#   scripts/verify.sh, every gate script and every package doc.go.
# lane-claims:
#   scripts/lib/lane-ungated.txt
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

# run_check runs `go run internal/lane/lanecheck.go --verify` against
# LANE_ROOT (default: this repo). LANE_ROOT is the root seam --teeth uses
# to point the SAME command at a fixture tree — README_PATH
# (check-readme.sh) and FEATURES_DIR (epic_docs_drift.sh) are the same
# idiom. `go run`'s module path only resolves from the repo root, so this
# always `cd`s there first, independent of where LANE_ROOT points.
run_check() {
  local target="${LANE_ROOT:-$ROOT}"
  ( cd "$ROOT" && LANE_ROOT="$target" go run internal/lane/lanecheck.go --verify )
}

# --- --teeth -------------------------------------------------------------
#
# Every receipt below runs against a FRESH, throwaway copy of
# internal/lane/testdata/teeth-base, overlaid with ONE receipt's mutated
# file(s) from internal/lane/testdata/teeth-receipts/<name>/, made into its
# OWN git repository — Universe() reads `git ls-files`, and this session's
# fixture files are untracked in the real checkout, so copying into a real
# (if throwaway) repo is required, not decorative (the same idiom
# internal/lane/universe_test.go already uses for the same reason). The
# copy lives under a mktemp -d directory OUTSIDE this checkout, so nothing
# here ever touches the live repo — not even read-only — and
# scripts/verify.sh itself is never mutated (that file is a later wave's
# sole-writer grant).
#
# The mutated content lives in its OWN files under internal/lane/testdata/
# rather than inline heredocs in THIS script on purpose: a heredoc
# containing the literal text "# lane-inputs:" would make
# scripts/check-lane-declarations.sh itself look like it carries six
# lane-inputs blocks to the very parser this gate exists to run — a
# self-inflicted version of the exact defect class D-11 polices.

BASE="$ROOT/internal/lane/testdata/teeth-base"
RECEIPTS="$ROOT/internal/lane/testdata/teeth-receipts"

prepare_fixture() {
  local dest="$1" overlay="${2:-}"
  mkdir -p "$dest"
  cp -R "$BASE"/. "$dest"/
  if [ -n "$overlay" ]; then
    cp -R "$RECEIPTS/$overlay"/. "$dest"/
  fi
  ( cd "$dest" && git init -q && git add -A ) >/dev/null
}

verify_fixture() {
  local dest="$1"
  ( cd "$ROOT" && LANE_ROOT="$dest" go run internal/lane/lanecheck.go --verify )
}

expect_red() {
  local dest="$1" needle="$2" label="$3" out rc
  set +e
  out="$(verify_fixture "$dest" 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -eq 0 ]; then
    echo "check-lane-declarations --teeth: FAIL — $label stayed green" >&2
    echo "$out" >&2
    exit 1
  fi
  if ! grep -Fq "$needle" <<<"$out"; then
    echo "check-lane-declarations --teeth: FAIL — $label red did not name '$needle':" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "check-lane-declarations --teeth: $label — reds, naming '$needle'"
}

run_teeth() {
  # NOT `local` on purpose: `trap ... EXIT` fires at PROCESS exit, after
  # run_teeth has already returned — a `local work` would be out of scope
  # by then and `set -u` turns the trap's own `$work` reference into a
  # fatal "unbound variable" on the way out, even after every receipt
  # already passed.
  work="$(mktemp -d)"
  trap 'rm -rf -- "$work"' EXIT

  # Sanity floor: the UNMUTATED baseline must be green, or a receipt's red
  # could just as well be a pre-existing defect in teeth-base rather than
  # the mutation it claims to prove.
  local clean="$work/clean" out rc
  prepare_fixture "$clean"
  set +e
  out="$(verify_fixture "$clean" 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ]; then
    echo "check-lane-declarations --teeth: FAIL — the unmutated teeth-base fixture is not green:" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "check-lane-declarations --teeth: clean teeth-base fixture greens"

  # (a) delete a declared input from a gate header -> the gate reds,
  # naming the now-unclaimed path (Coverage).
  local a="$work/receipt-a"
  prepare_fixture "$a" receipt-a
  expect_red "$a" "no gate claims docs/guide.md" "receipt (a) deleted-input"

  # (b) add an undeclared LITERAL path read to a gate script -> the gate
  # reds, naming the undeclared read (D-11's honesty pass).
  local b="$work/receipt-b"
  prepare_fixture "$b" receipt-b
  expect_red "$b" "reads docs/other.md" "receipt (b) undeclared-literal-read"

  # (c) add a run_phase to a fixture verify.sh with no declaration -> the
  # gate reds, naming the undeclared phase (Reconcile).
  local c="$work/receipt-c"
  prepare_fixture "$c" receipt-c
  expect_red "$c" 'phase "vet" has no lane-inputs declaration' "receipt (c) undeclared-phase"

  # (d) a real path read through a construct the classifier cannot resolve
  # (a variable-built grep argument), with NO lane-reads-opaque line ->
  # the gate reds naming the script and line; the SAME read WITH a
  # lane-reads-opaque line -> the gate passes and the opaque count printed
  # on every run moves 0 -> 1 (D-11 — "the number is the point", not just
  # the phrase "refuses loudly").
  local d1="$work/receipt-d-no-opaque"
  prepare_fixture "$d1" receipt-d-no-opaque
  set +e
  out="$(verify_fixture "$d1" 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -eq 0 ]; then
    echo "check-lane-declarations --teeth: FAIL — receipt (d) no-opaque stayed green" >&2
    echo "$out" >&2
    exit 1
  fi
  # "scripts/check-readme.sh:7" is coupled to
  # internal/lane/testdata/teeth-receipts/receipt-d-no-opaque/scripts/
  # check-readme.sh's own line count — editing that fixture file (even
  # adding a comment line) moves the unresolved grep call and this
  # assertion needs the same edit.
  if ! grep -Fq "scripts/check-readme.sh:7" <<<"$out" || ! grep -Fq "cannot resolve" <<<"$out"; then
    echo "check-lane-declarations --teeth: FAIL — receipt (d) no-opaque red did not name scripts/check-readme.sh:7:" >&2
    echo "$out" >&2
    exit 1
  fi
  if ! grep -Fq "lane: 0 phase(s) declare lane-reads-opaque." <<<"$out"; then
    echo "check-lane-declarations --teeth: FAIL — receipt (d) no-opaque should report an opaque count of 0:" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "check-lane-declarations --teeth: receipt (d) no-opaque — reds, naming scripts/check-readme.sh:7, opaque count 0"

  local d2="$work/receipt-d-opaque"
  prepare_fixture "$d2" receipt-d-opaque
  set +e
  out="$(verify_fixture "$d2" 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ]; then
    echo "check-lane-declarations --teeth: FAIL — receipt (d) WITH lane-reads-opaque should green:" >&2
    echo "$out" >&2
    exit 1
  fi
  if ! grep -Fq "lane: 1 phase(s) declare lane-reads-opaque." <<<"$out"; then
    echo "check-lane-declarations --teeth: FAIL — receipt (d) opaque count did not move 0 -> 1:" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "check-lane-declarations --teeth: receipt (d) with-opaque — greens, opaque count moved 0 -> 1"

  echo "check-lane-declarations --teeth: PASS — all four D-11/J3 receipts proved: (a) deleted-input, (b) undeclared-literal-read, (c) undeclared-phase, (d) unresolved-construct (no-opaque red, with-opaque green + count 0->1)"
}

case "${1:-check}" in
  check) run_check ;;
  --teeth) run_teeth ;;
  *)
    echo "usage: $0 [check|--teeth]" >&2
    exit 2
    ;;
esac
