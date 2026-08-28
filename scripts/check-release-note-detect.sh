#!/usr/bin/env bash
# check-release-note-detect.sh — P13 US-5's forward-only release-note
# authoring gate (spec docs/features/active/answers-that-hold-2026-08/
# specs/13-adapt-from-a-baseline.md §"The authoring gate (US-5, ACs
# 13-14)"): a release-notes change whose action.scope is "local" or
# "space" MUST carry a detect: — how `a2a adapt --done` can ever verify a
# recorded obligation instead of trusting an agent's word — or a row in
# scripts/lib/release-note-detect-budget.txt naming it as exempt.
#
# It parses NO YAML itself (D-7, the internal/lane/lanecheck.go / check-
# lane-declarations.sh precedent): the corpus already has a typed parser
# (internal/notes.Load), so this `go run`s internal/notes/detectcheck.go
# and does plain set arithmetic on its line-per-id output. A bash
# reimplementation of "walk every change, read action.scope/detect" would
# be a second hand-written copy of the schema's own shape.
#
# THE FROZEN CEILING lives in the budget file's own `# frozen-ceiling: N`
# header line (AC-14's "printed number that may not increase"), not as a
# second number here — a ceiling duplicated in two files is how the two
# drift. See scripts/lib/release-note-detect-budget.txt's own header for
# why the population is the WHOLE corpus and not the spec's illustrative
# (0.19.0, 0.25.6] window.
#
# lane-inputs:
#
#	releasenotes/*.yaml
#	scripts/lib/release-note-detect-budget.txt
#	internal/notes/detectcheck.go
#
# lane-reads-opaque: `go run internal/notes/detectcheck.go` additionally
#   reads every embedded corpus file through releasenotes.FS (a Go
#   embed directive in another package), which the glob above already
#   names by its real repo path.
#
# Usage:
#   bash scripts/check-release-note-detect.sh          # the everyday check
#   bash scripts/check-release-note-detect.sh --teeth  # offline self-test
set -uo pipefail

ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
# shellcheck source=scripts/lib/gate-lib.sh
source "${BASH_SOURCE[0]%/*}/lib/gate-lib.sh"

# scan_violations prints one change id per line: every change under $1 (a
# releasenotes/-shaped directory) whose action.scope is local|space and
# carries no detect:. `go run`'s package path only resolves from the
# module root, so this always cd's there first — the fixture directory is
# passed as an ABSOLUTE argument instead, decoupling "where go run
# executes" from "what directory is scanned" (the same LANE_ROOT idiom
# check-lane-declarations.sh uses for the identical reason).
scan_violations() {
  local dir="$1"
  ( cd "$ROOT" && go run internal/notes/detectcheck.go "$dir" )
}

# budget_ceiling reads the `# frozen-ceiling: N` line from $1 — the ONE
# place AC-14's ratchet number lives.
budget_ceiling() {
  sed -n 's/^# frozen-ceiling:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$1" | head -1
}

# budget_ids prints $1's listed change ids, one per line (comments and
# blanks stripped) — scripts/lib/flaky-tests.txt's grammar.
budget_ids() {
  grep -vE '^[[:space:]]*(#|$)' "$1"
}

run_check() { # $1 = releasenotes-shaped dir, $2 = budget file, $3 = gate name
  local dir="$1" budget="$2" name="$3"

  if [ ! -f "$budget" ]; then
    gate_unmeasured "$budget is absent. It IS the exemption ratchet AC-14 reads; without it a real violation is indistinguishable from an exempted one, so this refuses rather than reporting a verdict it cannot support."
    gate_summary "$name"; return $?
  fi

  local ceiling
  ceiling="$(budget_ceiling "$budget")"
  if [ -z "$ceiling" ]; then
    gate_unmeasured "$budget carries no '# frozen-ceiling: N' header line."
    gate_summary "$name"; return $?
  fi

  local violations
  if ! violations="$(scan_violations "$dir")"; then
    gate_unmeasured "internal/notes/detectcheck.go failed to run against $dir"
    gate_summary "$name"; return $?
  fi

  local listed
  listed="$(budget_ids "$budget")"
  local listed_count
  listed_count="$(printf '%s\n' "$listed" | grep -c . || true)"

  if [ "$listed_count" -gt "$ceiling" ]; then
    gate_fail "$budget lists $listed_count exemptions, above its own frozen ceiling of $ceiling — AC-14: this count may only shrink, never grow"
  fi

  local unlisted=""
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    if ! printf '%s\n' "$listed" | grep -qxF "$id"; then
      unlisted="$unlisted $id"
    fi
  done <<<"$violations"
  if [ -n "$unlisted" ]; then
    gate_fail "these changes declare action.scope local|space, carry no detect:, and have no exemption row in $budget:$unlisted"
  fi

  local stale=""
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    if ! printf '%s\n' "$violations" | grep -qxF "$id"; then
      stale="$stale $id"
    fi
  done <<<"$listed"
  if [ -n "$stale" ]; then
    gate_fail "these exemptions in $budget no longer describe a real violation (a detect: was added, or the change no longer exists) — delete them so the ratchet drains:$stale"
  fi

  gate_ok "$listed_count/$ceiling frozen exemptions accounted for; every scope:local|space change with no detect: is exempted by name"
  gate_summary "$name"
}

# --- teeth ---------------------------------------------------------------
run_teeth() {
  local d fail=0
  d="$(mktemp -d)"
  mkdir -p "$d/notes"

  cat >"$d/notes/9.9.9.yaml" <<'EOF'
schema: release-notes/v1
version: "9.9.9"
released: unreleased
headline: fixture
changes:
  - id: RN-FIXTURE-UNLISTED
    kind: feat
    impact: normal
    subject: an unlisted violation
    detail: d
    action:
      scope: local
      why: w
  - id: RN-FIXTURE-EXEMPT
    kind: feat
    impact: normal
    subject: an exempted violation
    detail: d
    action:
      scope: space
      why: w
  - id: RN-FIXTURE-COMPLIANT
    kind: feat
    impact: normal
    subject: carries its own detect
    detail: d
    action:
      scope: local
      why: w
      detect: ["true"]
EOF

  # (a) a clean budget: both real violations exempted, ceiling matches
  # exactly -> green.
  cat >"$d/budget-clean.txt" <<'EOF'
# frozen-ceiling: 2
RN-FIXTURE-UNLISTED
RN-FIXTURE-EXEMPT
EOF
  out="$(run_check "$d/notes" "$d/budget-clean.txt" "release-note-detect-teeth" 2>&1)"; rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "check-release-note-detect --teeth (a): FAIL — a clean fixture should be green, rc=$rc" >&2
    printf '%s\n' "$out" >&2; fail=1
  fi

  # (b) an unlisted violation must red BY NAME (AC-13).
  cat >"$d/budget-missing.txt" <<'EOF'
# frozen-ceiling: 2
RN-FIXTURE-EXEMPT
EOF
  out="$(run_check "$d/notes" "$d/budget-missing.txt" "release-note-detect-teeth" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q "RN-FIXTURE-UNLISTED" <<<"$out"; then
    echo "check-release-note-detect --teeth (b): FAIL — an unlisted violation must red naming it; rc=$rc" >&2
    printf '%s\n' "$out" >&2; fail=1
  fi

  # (c) a stale exemption (no longer a real violation) must red BY NAME, so
  # the ratchet drains rather than accumulating dead weight.
  cat >"$d/budget-stale.txt" <<'EOF'
# frozen-ceiling: 3
RN-FIXTURE-UNLISTED
RN-FIXTURE-EXEMPT
RN-FIXTURE-GHOST
EOF
  out="$(run_check "$d/notes" "$d/budget-stale.txt" "release-note-detect-teeth" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q "RN-FIXTURE-GHOST" <<<"$out"; then
    echo "check-release-note-detect --teeth (c): FAIL — a stale exemption must red naming it; rc=$rc" >&2
    printf '%s\n' "$out" >&2; fail=1
  fi

  # (d) the ceiling itself must red when the budget grows past it (AC-14).
  cat >"$d/budget-over-ceiling.txt" <<'EOF'
# frozen-ceiling: 1
RN-FIXTURE-UNLISTED
RN-FIXTURE-EXEMPT
EOF
  out="$(run_check "$d/notes" "$d/budget-over-ceiling.txt" "release-note-detect-teeth" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q "frozen ceiling" <<<"$out"; then
    echo "check-release-note-detect --teeth (d): FAIL — exceeding the frozen ceiling must red; rc=$rc" >&2
    printf '%s\n' "$out" >&2; fail=1
  fi

  # (e) an absent budget file is UNMEASURED, never a silent pass.
  out="$(run_check "$d/notes" "$d/does-not-exist.txt" "release-note-detect-teeth" 2>&1)"; rc=$?
  if [ "$rc" -ne "$GATE_EXIT_UNMEASURED" ] || ! grep -q "UNMEASURED" <<<"$out"; then
    echo "check-release-note-detect --teeth (e): FAIL — an absent budget file must be UNMEASURED, not a verdict; rc=$rc" >&2
    printf '%s\n' "$out" >&2; fail=1
  fi

  rm -rf "$d"
  if [ "$fail" -ne 0 ]; then echo "check-release-note-detect --teeth: FAIL" >&2; exit 1; fi
  echo "check-release-note-detect --teeth: an unlisted violation reds by name; a stale exemption reds so the ratchet drains; exceeding the ceiling reds; an absent budget is UNMEASURED."
}

if [ "${1:-}" = "--teeth" ]; then run_teeth; exit 0; fi

BUDGET="${BUDGET_FILE:-$ROOT/scripts/lib/release-note-detect-budget.txt}"
NOTES_DIR="${RELEASENOTES_DIR:-$ROOT/releasenotes}"
run_check "$NOTES_DIR" "$BUDGET" release-note-detect
exit $?
