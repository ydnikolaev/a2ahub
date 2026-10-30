#!/usr/bin/env bash
# Teeth for verify.sh's static block (release-speed-2026-11 P2, spec 02 T1 and §6).
#
# The static block is `validators` mode and the first thing `full` runs: build-cli,
# gofmt, vet, golangci-lint, then the repo gates in two cost tiers, COLLECT-ALL,
# with a boundary after each tier that names every red and stops. This file
# drives a COPY of verify.sh against a fixture checkout whose roster, gates and Go
# toolchain are stubs, so it judges the runner's ordering and boundaries and never
# calls a real gate.
#
# Every claim is read from the phases the runner itself recorded
# (.a2a/cache/verify/telemetry.jsonl, one row per run_phase, in run order) and
# from the stub `go`'s call log, never from a re-implementation of the order.
#
# SEAM, stated: VERIFY_SH names the verify.sh to copy (default: this repo's).
# Pointing it at the pre-P2 script is how each case is watched failing:
#   git show <base>:scripts/verify.sh > /tmp/base-verify.sh
#   VERIFY_SH=/tmp/base-verify.sh bash scripts/tests/verify_static_block_test.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
VERIFY_SH="${VERIFY_SH:-$ROOT/scripts/verify.sh}"

if [ -n "${CLAUDE_JOB_DIR:-}" ]; then
  mkdir -p "$CLAUDE_JOB_DIR/tmp"
  WORK="$(mktemp -d "$CLAUDE_JOB_DIR/tmp/static-block-test.XXXXXX")"
else
  WORK="$(mktemp -d)"
fi
WORK="$(cd "$WORK" && pwd -P)"
trap 'rm -rf "$WORK"' EXIT

# Fixture repos must not inherit the operator's git config (signing, hooks), and
# a test run under `make` must not hand its jobserver to the fixture's make.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
unset MAKEFLAGS MFLAGS MAKELEVEL GITHUB_ACTIONS GITHUB_WORKSPACE A2A_VERIFY_BINARY

fail() {
  echo "verify-static-block-test: FAIL - $*" >&2
  exit 1
}

# mkfx <name> <all-gates> <slow-gates>: a git checkout holding a copy of verify.sh
# and its libs, a Makefile whose roster is the given lists, and a stub toolchain.
# A gate is red when its name is in $FX_RED; vet is red the same way. Prints the path.
mkfx() {
  local fx="$WORK/$1" g
  mkdir -p "$fx/scripts/lib" "$fx/bin" "$fx/goroot/bin" "$fx/internal/version"
  cp "$VERIFY_SH" "$fx/scripts/verify.sh"
  cp "$ROOT"/scripts/lib/*.sh "$fx/scripts/lib/"
  printf 'module example.com/fx\n\ngo 1.26\n' >"$fx/go.mod"
  printf 'package version\n\nconst OperationalConfidenceFloor = "0.19.0"\n' >"$fx/internal/version/operationalconfidence.go"
  {
    printf '_print-repo-gates:\n\t@echo "%s"\n' "$2"
    printf '_print-repo-gates-slow:\n\t@echo "%s"\n' "$3"
    printf '_print-early-go-tests:\n\t@echo ""\n'
    for g in $2; do
      printf '%s:\n\t@case " $$FX_UNMEASURED " in *" %s "*) echo "unmeasured" >>"$$A2A_UNMEASURED_MARKER"; exit 3;; esac\n\t@case " $$FX_RED " in *" %s "*) echo "FAIL %s" >&2; exit 1;; esac\n' "$g" "$g" "$g" "$g"
    done
  } >"$fx/Makefile"
  cat >"$fx/bin/go" <<'EOF'
#!/usr/bin/env bash
# Stub toolchain: answers `go env`, logs every other call, and is red for `vet`
# when FX_RED names it.
here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
case "${1:-}" in
  env)
    case "${2:-}" in
      GOROOT) printf '%s\n' "$here/goroot" ;;
      GOVERSION) echo go1.fake ;;
      *) echo /nonexistent ;;
    esac
    exit 0
    ;;
  run)
    case "${2:-}" in
      internal/lane/lanecheck.go)
        case "${3:-}" in
          --phases) for p in ${FX_LANE_PHASES:-}; do echo "$p"; done ;;
          *) ;;
        esac
        exit 0
        ;;
    esac
    ;;
esac
echo "go $*" >>"$here/go.log"
if [ "${1:-}" = vet ]; then
  case " ${FX_RED:-} " in *" vet "*) echo "FAIL vet" >&2; exit 1 ;; esac
fi
exit 0
EOF
  printf '#!/bin/sh\nexit 0\n' >"$fx/goroot/bin/gofmt"
  chmod +x "$fx/bin/go" "$fx/goroot/bin/gofmt"
  : >"$fx/go.log"
  printf '.a2a/\ngo.log\n' >"$fx/.gitignore"
  git -C "$fx" init -q -b main
  git -C "$fx" config user.email teeth@example.com
  git -C "$fx" config user.name teeth
  git -C "$fx" config commit.gpgsign false
  git -C "$fx" add -A
  git -C "$fx" commit -qm c0
  printf '%s' "$fx"
}

OUT="" RC=0
# run_mode <fx> <red-names> <mode...>: sets OUT (stdout+stderr) and RC.
run_mode() {
  local fx="$1" red="$2"
  shift 2
  RC=0
  OUT="$(cd "$fx" && PATH="$fx/bin:$PATH" FX_RED="$red" VERIFY_AGAIN=1 bash "$fx/scripts/verify.sh" "$@" 2>&1)" || RC=$?
}

# phases <fx>: the phases the runner recorded, in run order, on one line.
phases() {
  sed -n 's/.*"gate":"\([^"]*\)".*/\1/p' "$1/.a2a/cache/verify/telemetry.jsonl" 2>/dev/null |
    grep -v '^run:' | tr '\n' ' ' | sed 's/ $//' || true
}

# want_phases <label> <fx> <expected>
want_phases() {
  local got
  got="$(phases "$2")"
  [ "$got" = "$3" ] || fail "$1: phases ran as [$got], want [$3]. Output: $OUT"
}

last_line() { printf '%s\n' "$OUT" | grep -v '^$' | tail -n 1; }

# want_last <label> <line>: the block's LAST line names every red.
want_last() {
  [ "$(last_line)" = "$2" ] || fail "$1: last line is [$(last_line)], want [$2]. Output: $OUT"
}

PRE="build-cli gofmt vet golangci-lint"

# --- 1. green: lint before any gate, cheap tier before slow tier ---------------
fx="$(mkfx green "f1 f2 f3 s1 s2" "s1 s2")"
run_mode "$fx" "" validators
[ "$RC" -eq 0 ] || fail "green validators exited $RC: $OUT"
want_phases "green validators (lint before any repo gate, slow tier last)" "$fx" "$PRE f1 f2 f3 s1 s2"

# --- 2. two reds and a green in one tier: all three run, both reds named -------
fx="$(mkfx tier1 "f1 f2 f3 s1 s2" "s1 s2")"
run_mode "$fx" "f1 f3" validators
[ "$RC" -eq 1 ] || fail "two tier-1 reds: exit $RC, want 1: $OUT"
want_phases "two tier-1 reds (f2 still runs; the slow tier does not)" "$fx" "$PRE f1 f2 f3"
want_last "two tier-1 reds" "static block: 2 red: f1, f3"

# --- 3. a tier-2-only red runs tier 1 fully, and tier 2 fully ------------------
fx="$(mkfx tier2 "f1 f2 f3 s1 s2" "s1 s2")"
run_mode "$fx" "s1" validators
[ "$RC" -eq 1 ] || fail "tier-2-only red: exit $RC, want 1: $OUT"
want_phases "tier-2-only red (tier 1 runs fully, s2 runs after s1)" "$fx" "$PRE f1 f2 f3 s1 s2"
want_last "tier-2-only red" "static block: 1 red: s1"

# --- 4. a red static tool and a red gate are named together --------------------
fx="$(mkfx vet "f1 f2 f3 s1 s2" "s1 s2")"
run_mode "$fx" "vet f2" validators
[ "$RC" -eq 1 ] || fail "vet+gate red: exit $RC, want 1: $OUT"
want_phases "vet and f2 red (the block does not stop at vet)" "$fx" "$PRE f1 f2 f3"
want_last "vet and f2 red" "static block: 2 red: vet, f2"

# --- 4a. a gate that could not measure is named as that, not as a measured red -
fx="$(mkfx unmeasured "f1 f2 f3 s1 s2" "s1 s2")"
export FX_UNMEASURED=f2
run_mode "$fx" "f1" validators
unset FX_UNMEASURED
[ "$RC" -eq 1 ] || fail "unmeasured gate: exit $RC, want 1: $OUT"
want_last "unmeasured gate" "static block: 2 red: f1, f2 (unmeasured)"

# --- 5. in `full`, the Go block does not start behind a static red -------------
fx="$(mkfx full-red "f1 f2 f3 s1 s2" "s1 s2")"
run_mode "$fx" "f1 f2" full
[ "$RC" -eq 1 ] || fail "full with two reds: exit $RC, want 1: $OUT"
want_last "full with two reds" "static block: 2 red: f1, f2"
if grep -Eq '^go (test|run internal/coveragepolicy)' "$fx/go.log"; then
  fail "full with two static reds still started the Go block: $(cat "$fx/go.log")"
fi
case "$(phases "$fx")" in *go-test* | *coverage-policy* | *logic-e2e*) fail "full with two static reds recorded a Go phase: $(phases "$fx")" ;; esac

# --- 6. a green `full` runs the same phases: static block, then the Go block ---
fx="$(mkfx full-green "f1 f2 f3 s1 s2" "s1 s2")"
run_mode "$fx" "" full
[ "$RC" -eq 0 ] || fail "green full exited $RC: $OUT"
want_phases "green full" "$fx" "$PRE f1 f2 f3 s1 s2 go-test coverage-policy logic-e2e"

# --- 7. an empty slow list is allowed ------------------------------------------
fx="$(mkfx noslow "f1 f2" "")"
run_mode "$fx" "" validators
[ "$RC" -eq 0 ] || fail "an empty REPO_GATES_SLOW was refused ($RC): $OUT"
want_phases "empty slow list" "$fx" "$PRE f1 f2"

# --- 8. a slow name REPO_GATES lacks is refused, naming it, and nothing runs ---
fx="$(mkfx stray "f1 f2 s1" "s1 zz-stray")"
run_mode "$fx" "" validators
[ "$RC" -ne 0 ] || fail "a REPO_GATES_SLOW name absent from REPO_GATES was accepted: $OUT"
grep -Fq "zz-stray" <<<"$OUT" || fail "the stray-slow refusal does not name zz-stray: $OUT"
[ -z "$(phases "$fx")" ] || fail "phases ran behind a refused roster: $(phases "$fx")"

# --- 9. the derived lane applies the same rule to the phases it selected -------
# A deliberately shuffled derivation: the lane must still run tier 1, tier 2,
# then the rest, and run exactly the selected set, no more and no fewer.
lane_set="go-test s1 f2 gofmt build-cli f1 vet"
fx="$(mkfx lane-green "f1 f2 f3 s1 s2" "s1 s2")"
(cd "$fx" && LANE_FILES="a.go" FX_LANE_PHASES="$lane_set" PATH="$fx/bin:$PATH" FX_RED="" VERIFY_AGAIN=1 bash "$fx/scripts/verify.sh" lane-run >"$WORK/lane-green.out" 2>&1) || fail "green lane-run failed: $(cat "$WORK/lane-green.out")"
OUT="$(cat "$WORK/lane-green.out")"
want_phases "green lane-run (selected set, static tiers first)" "$fx" "build-cli gofmt vet f1 f2 s1 go-test"

fx="$(mkfx lane-red "f1 f2 f3 s1 s2" "s1 s2")"
RC=0
OUT="$(cd "$fx" && LANE_FILES="a.go" FX_LANE_PHASES="$lane_set" PATH="$fx/bin:$PATH" FX_RED="f1 f2" VERIFY_AGAIN=1 bash "$fx/scripts/verify.sh" lane-run 2>&1)" || RC=$?
[ "$RC" -eq 1 ] || fail "lane-run with two reds: exit $RC, want 1: $OUT"
want_phases "lane-run with two reds (s1 and go-test never start)" "$fx" "build-cli gofmt vet f1 f2"
want_last "lane-run with two reds" "static block: 2 red: f1, f2"

echo "verify-static-block-test: all cases passed"
