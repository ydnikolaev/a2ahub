#!/usr/bin/env bash
# check-deadcode-ceiling.sh — computed-not-listed-2026-08 P5 (spec
# 05-code-that-cannot-run.md §T5/§8 AC rows 3/5/6/7): a ceiling on the count
# of `deadcode ./cmd/a2a` unreachable-function entries, copying
# check-refusal-ratchet.sh's mechanics (a stored ceiling, `--write` to
# reseed, `--teeth` self-tests, gate-lib's fail/ok/unmeasured/summary) with
# one difference: a single stored NUMBER instead of a per-file budget.
#
# WHY A COUNT RATCHET AND NOT AN ALLOWLIST — measured, not preferred. 71 of
# the 93 entries measured at the source audit (94 re-measured at this
# phase's HEAD) need a human verdict, roughly 2-3 hours of adjudication, and
# internal/contract/types.go:47's DigestProfiles proved a name-and-comment
# read is not enough on its own — it carries a 12-line doc comment
# explaining why it must exist and is still unreachable. Worse, an
# allowlist of individually-named symbols INVERTS the tool's own value: an
# entry allowlisted today becomes real dead code tomorrow and stays green
# forever, because nothing about a growing allowlist ever shrinks on its
# own. A count has no such failure mode — it may only fall or hold, never
# quietly cover new rot — and it costs exactly one file.
#
# THE DARWIN BLIND SPOT, stated rather than discovered. `deadcode` on darwin
# is SILENT — not wrong — about 14 platform-suffixed files under
# internal/** (statusline_process_*, worklease_lock/permissions/sync_*,
# *_noreplace_darwin/linux/unsupported) and about 23 //go:build livee2e
# files: none of them is part of the darwin default build this gate
# measures, so dead code inside any of them is invisible here. This gate's
# own green line states that every time it passes, rather than leaving it
# to be rediscovered.
#
# OFFLINE, FROM THE MODULE CACHE, WITHOUT TOUCHING THIS REPO'S go.mod/
# go.sum. `go run golang.org/x/tools/cmd/deadcode@$DEADCODE_VERSION <root>`
# resolves the tool's OWN, separate module graph — it never reads or writes
# this repository's go.mod/go.sum (verified empirically while building this
# gate: `git status --short go.mod go.sum` stayed clean across every
# invocation below; `-mod=mod` against the LOCAL module graph, by contrast,
# silently rewrote go.sum and is deliberately not used here). Plain
# `GOPROXY=off` still fails even for an already-cached, exactly pinned
# version: `go run pkg@version` looks up the package's deprecation notice
# through the module proxy protocol regardless, and GOPROXY=off refuses
# that lookup outright instead of answering it from disk. Pointing GOPROXY
# at the on-disk module cache's own download directory (a `file://` URL)
# serves that same lookup from already-downloaded content with no network
# round trip — the offline form this gate actually uses. If the module
# cache does not carry the pinned deadcode module at all (a fresh checkout,
# or one that never ran `go run`/`go install` against golang.org/x/tools),
# this gate reports UNMEASURED rather than guessing at a verdict.
#
# Usage: bash scripts/check-deadcode-ceiling.sh            # verify (CI/commit gate)
#        bash scripts/check-deadcode-ceiling.sh --write    # regenerate the ceiling from the current tree
#        bash scripts/check-deadcode-ceiling.sh --teeth    # this gate's own self-test
#
# lane-reads-opaque: DEADCODE_VERSION below pins
# golang.org/x/tools/cmd/deadcode; bumping it changes which TOOL measures
# this gate, never which repository files decide its verdict, so it is
# deliberately not itself a lane-input.
#
# lane-inputs:
#   **/*.go
#   scripts/check-deadcode-ceiling.sh
#   scripts/lib/deadcode-ceiling.txt
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

DEADCODE_VERSION="v0.46.0"
DEFAULT_MODULE_DIR="$GATE_ROOT"
DEFAULT_PKG="./cmd/a2a"
DEFAULT_CEILING_FILE="$GATE_ROOT/scripts/lib/deadcode-ceiling.txt"

# deadcode_module_proxy: a file:// URL serving already-downloaded module
# content from this machine's own module cache — see the header for why
# plain GOPROXY=off is not enough on its own.
deadcode_module_proxy() {
  printf 'file://%s/cache/download' "$(go env GOMODCACHE)"
}

# run_deadcode <module-dir> <pkg>: prints the oracle's raw stdout+stderr;
# the caller reads $? — 0 means the run is trustworthy (a nonzero function
# count is still a successful run), nonzero means the oracle itself could
# not run (module cache miss, a broken build) and the caller must report
# UNMEASURED, never a verdict. `-C <module-dir>` (not a plain trailing path
# argument) is load-bearing: `go run pkg@version` resolves the TARGET
# package against the process's own working directory's module, so a
# --teeth run against a synthetic module elsewhere on disk must move the Go
# command there rather than just naming its path.
run_deadcode() {
  local module_dir="$1" pkg="$2"
  GOPROXY="$(deadcode_module_proxy)" go run -C "$module_dir" "golang.org/x/tools/cmd/deadcode@$DEADCODE_VERSION" "$pkg" 2>&1
}

# count_entries: counts "unreachable func:" lines on stdin.
count_entries() {
  grep -c 'unreachable func:' || true
}

# read_ceiling <file>: the first non-comment, non-blank token in <file>, or
# empty if the file is absent or names no number.
read_ceiling() {
  local file="$1"
  awk '$0 !~ /^#/ && NF > 0 { print $1; exit }' "$file" 2>/dev/null
}

# verify_against_ceiling <module-dir> <pkg> <ceiling-file>: gate_fail if the
# CURRENT measured count exceeds the stored ceiling (naming every entry the
# oracle currently reports, so the offending symbol is never just a
# number); gate_unmeasured if the oracle could not run or the ceiling file
# is empty; gate_ok with the darwin-blind-spot note otherwise.
verify_against_ceiling() {
  local module_dir="$1" pkg="$2" ceiling_file="$3" raw rc count ceiling
  raw="$(run_deadcode "$module_dir" "$pkg")"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    gate_unmeasured "deadcode-ceiling: could not run the deadcode oracle against $pkg in $module_dir: $raw"
    return
  fi
  count="$(printf '%s\n' "$raw" | count_entries)"
  ceiling="$(read_ceiling "$ceiling_file")"
  if [ -z "$ceiling" ]; then
    gate_unmeasured "deadcode-ceiling: $ceiling_file names no stored ceiling — run --write to seed it"
    return
  fi
  if [ "$count" -gt "$ceiling" ]; then
    gate_fail "deadcode-ceiling: $count unreachable entries exceeds the stored ceiling of $ceiling (delta $((count - ceiling))) — every entry the oracle currently names: $(printf '%s\n' "$raw" | grep 'unreachable func:' | tr '\n' ';')"
    return
  fi
  gate_ok "deadcode-ceiling: $count unreachable entries at or under ceiling $ceiling, measured on the darwin default build — blind to 14 platform-suffixed files under internal/** (statusline_process_*, worklease_lock/permissions/sync_*, *_noreplace_darwin/linux/unsupported) and 23 //go:build livee2e files, neither of which this build compiles"
}

# write_ceiling <module-dir> <pkg> <out>: regenerates <out> from pkg's
# CURRENT tree. Never invoked automatically by the verify path — a
# maintainer runs this explicitly after deleting dead code, then reviews
# the diff (a lowered number is the one honest way to bank a deletion).
write_ceiling() {
  local module_dir="$1" pkg="$2" out="$3" raw rc count
  raw="$(run_deadcode "$module_dir" "$pkg")"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "check-deadcode-ceiling: could not run the deadcode oracle to seed $out: $raw" >&2
    return 1
  fi
  count="$(printf '%s\n' "$raw" | count_entries)"
  {
    cat <<HEADER
# scripts/lib/deadcode-ceiling.txt — the CEILING on \`deadcode ./cmd/a2a\`
# unreachable-function entries (computed-not-listed-2026-08 P5, spec
# 05-code-that-cannot-run.md §T5, AC rows 3/6). ONE NUMBER: the count may
# fall (delete dead code, lower this) but must never rise —
# check-deadcode-ceiling.sh reds the moment the measured count exceeds it.
# Regenerate with:
#   bash scripts/check-deadcode-ceiling.sh --write
# then review the diff before committing; a rise from --write alone means
# new dead code landed, not that the ratchet moved on its own.
#
# 22 of the entries at seed time are legitimately-unreachable exported API
# (14 in internal/host/fake.go's fake host, 5 in
# internal/datatransport/conformance.go's driver conformance suite, 3
# Set*ForTest seams) and are deliberately NOT individually allowlisted here
# — the ceiling holds a NUMBER, not 22 symbol names, because a per-symbol
# allowlist goes stale in exactly the direction that matters (an entry
# allowlisted today becomes real dead code tomorrow and stays green
# forever; a count cannot).
#
# THE DARWIN BLIND SPOT: this count is measured on the darwin default
# build. 14 platform-suffixed files under internal/** (statusline_process_*,
# worklease_lock/permissions/sync_*, *_noreplace_darwin/linux/unsupported)
# and 23 //go:build livee2e files are invisible to this oracle here — any
# dead code inside them is not counted by this ceiling.
HEADER
    printf '%s\n' "$count"
  } >"$out"
}

# ── --teeth ──────────────────────────────────────────────────────────────

teeth_run() { # $1 = module dir, $2 = pkg, $3 = ceiling file; isolated counters, like check-refusal-ratchet.sh's own teeth_run
  (
    _GATE_ERRORS=0
    _GATE_WARNINGS=0
    _GATE_UNMEASURED=0
    verify_against_ceiling "$1" "$2" "$3"
    gate_summary "check-deadcode-ceiling-teeth"
  ) 2>&1
}

teeth_expect() { # $1 = label, $2 = red|green, $3 = module dir, $4 = pkg, $5 = ceiling file
  local label="$1" verdict="$2" module_dir="$3" pkg="$4" ceiling="$5" out rc
  # No set -e/+e toggling here (unlike check-refusal-ratchet.sh's
  # teeth_expect): this script never enables errexit in the first place
  # (top-of-file is `set -uo pipefail` only), and a stray `set -e` left ON
  # after this function returns previously aborted the later raw
  # teeth_run call below (the empty-ceiling-file case) the instant it
  # returned a nonzero UNMEASURED exit — found while building this gate,
  # not copied blind.
  out="$(teeth_run "$module_dir" "$pkg" "$ceiling")"
  rc=$?
  if [ "$verdict" = "red" ]; then
    if [ "$rc" -eq 0 ]; then
      echo "check-deadcode-ceiling --teeth: FALSE GREEN — $label did not red:" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-deadcode-ceiling --teeth: $label reds"
  else
    if [ "$rc" -ne 0 ]; then
      echo "check-deadcode-ceiling --teeth: FALSE RED — $label should green:" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-deadcode-ceiling --teeth: $label greens"
  fi
  printf '%s\n' "$out"
}

run_teeth() {
  local work
  work="$(mktemp -d)" || return 1
  trap 'rm -rf -- "$work"' RETURN

  cat >"$work/go.mod" <<'EOF'
module deadcodeceilingteeth

go 1.26
EOF
  cat >"$work/main.go" <<'EOF'
package main

func main() {
	reachable()
}

func reachable() {}
EOF

  # Baseline: nothing unreachable, ceiling 0, greens.
  printf '0\n' >"$work/ceiling.txt"
  teeth_expect "baseline: 0 unreachable at ceiling 0" green "$work" "." "$work/ceiling.txt" >/dev/null || exit 1

  # AC row 3 / spec §6 "adding an unreachable exported function reds": one
  # new unreachable func at ceiling 0 REDS, and the failure message NAMES
  # the entry (never just a bare count).
  cat >>"$work/main.go" <<'EOF'

func Unreachable() {}
EOF
  local out
  out="$(teeth_expect "AC-growth: a new unreachable func past ceiling 0 reds" red "$work" "." "$work/ceiling.txt")" || exit 1
  case "$out" in
  *"Unreachable"*) echo "check-deadcode-ceiling --teeth: growth failure names the entry (Unreachable)" ;;
  *)
    echo "check-deadcode-ceiling --teeth: FALSE — growth failure did not name the new entry:" >&2
    echo "$out" >&2
    exit 1
    ;;
  esac

  # Raising the ceiling to match greens again (never required for a FALL,
  # only shown here so the next case's baseline is unambiguous).
  printf '1\n' >"$work/ceiling.txt"
  teeth_expect "at ceiling 1 with 1 unreachable greens" green "$work" "." "$work/ceiling.txt" >/dev/null || exit 1

  # A symbol reachable only from a _test.go file (spec §6 edge case): the
  # plain (non -test) oracle this gate runs does not compile _test.go files
  # into the analyzed program, so a func called only from one is exactly as
  # unreachable as one called from nowhere at all.
  cat >>"$work/main.go" <<'EOF'

func onlyFromTest() {}
EOF
  cat >"$work/main_test.go" <<'EOF'
package main

import "testing"

func TestOnlyFromTest(t *testing.T) {
	onlyFromTest()
}
EOF
  out="$(teeth_expect "a func reached only from _test.go reds like any other unreachable func" red "$work" "." "$work/ceiling.txt")" || exit 1
  case "$out" in
  *"onlyFromTest"*) echo "check-deadcode-ceiling --teeth: test-only-reachable symbol is counted (onlyFromTest)" ;;
  *)
    echo "check-deadcode-ceiling --teeth: FALSE — test-only-reachable symbol was not named:" >&2
    echo "$out" >&2
    exit 1
    ;;
  esac

  # AC row 3's other half / spec §6 "deleting one lowers the ceiling": drop
  # both extra funcs, lower the ceiling to match the fallen count, greens.
  cat >"$work/main.go" <<'EOF'
package main

func main() {
	reachable()
}

func reachable() {}
EOF
  rm -f "$work/main_test.go"
  printf '0\n' >"$work/ceiling.txt"
  teeth_expect "AC-fall: deleting the unreachable funcs and lowering the ceiling greens" green "$work" "." "$work/ceiling.txt" >/dev/null || exit 1

  # A stale-high ceiling never blocks a fall: the count may sit strictly
  # under a ceiling that was never lowered, and that still greens.
  printf '5\n' >"$work/ceiling.txt"
  teeth_expect "a ceiling above the measured count greens (falling is always allowed)" green "$work" "." "$work/ceiling.txt" >/dev/null || exit 1

  # UNMEASURED, not a false FAIL, when the ceiling file names no number.
  : >"$work/empty-ceiling.txt"
  out="$(teeth_run "$work" "." "$work/empty-ceiling.txt")"
  rc=$?
  if [ "$rc" -ne "$GATE_EXIT_UNMEASURED" ]; then
    echo "check-deadcode-ceiling --teeth: FALSE — an empty ceiling file should report UNMEASURED (exit $GATE_EXIT_UNMEASURED), got $rc:" >&2
    echo "$out" >&2
    exit 1
  fi
  case "$out" in
  *"UNMEASURED"*) echo "check-deadcode-ceiling --teeth: an empty ceiling file reports UNMEASURED, not a false FAIL" ;;
  *)
    echo "check-deadcode-ceiling --teeth: FALSE — empty-ceiling run did not say UNMEASURED:" >&2
    echo "$out" >&2
    exit 1
    ;;
  esac

  echo "check-deadcode-ceiling --teeth: PASS — growth reds and names the entry, a _test.go-only symbol counts too, falling plus a lowered ceiling greens, a stale-high ceiling never blocks a fall, an unreadable ceiling reports UNMEASURED"
}

# ── entry point ─────────────────────────────────────────────────────────

case "${1:-}" in
--teeth)
  run_teeth
  exit $?
  ;;
--write)
  write_ceiling "$DEFAULT_MODULE_DIR" "$DEFAULT_PKG" "$DEFAULT_CEILING_FILE"
  echo "wrote $DEFAULT_CEILING_FILE"
  ;;
"")
  verify_against_ceiling "$DEFAULT_MODULE_DIR" "$DEFAULT_PKG" "$DEFAULT_CEILING_FILE"
  gate_summary "deadcode-ceiling"
  exit $?
  ;;
*)
  echo "usage: $0 [--teeth|--write]" >&2
  exit 2
  ;;
esac
