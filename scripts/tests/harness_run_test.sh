#!/usr/bin/env bash
# Teeth for scripts/lib/harness-run.sh (release-speed-2026-11 P5, spec 05 T2, T4, §6).
#
# Drives the runner over STUB scripts in a temp dir, never the real roster. Each
# case reads what the runner printed, its exit status and the rows it wrote.
#
# SEAM, stated: RUNNER names the runner to drive (default: this repo's). Pointing
# it at a broken copy is how each case is watched failing.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
RUNNER="${RUNNER:-$ROOT/scripts/lib/harness-run.sh}"

if [ -n "${CLAUDE_JOB_DIR:-}" ]; then
  mkdir -p "$CLAUDE_JOB_DIR/tmp"
  WORK="$(mktemp -d "$CLAUDE_JOB_DIR/tmp/harness-run-test.XXXXXX")"
else
  WORK="$(mktemp -d)"
fi
WORK="$(cd "$WORK" && pwd -P)"
trap 'rm -rf "$WORK"' EXIT

# The runner reads these; a case sets what it needs.
unset HARNESS_TEETH_JOBS HEAVY_RUN_CORES GOMAXPROCS A2A_STAGES_SINK RUN_TREE GITHUB_ACTIONS

fail() {
  echo "harness-run-test: FAIL - $*" >&2
  exit 1
}

# stub <name> <body>: a script under $WORK/s that runs <body>.
stub() {
  mkdir -p "$WORK/s"
  printf '%s\n' "$2" >"$WORK/s/$1.sh"
  printf '%s\n' "$WORK/s/$1.sh"
}

# run <args...>: the runner's stdout to $WORK/out, stderr to $WORK/err, status to $RC.
run() {
  RC=0
  bash "$RUNNER" "$@" >"$WORK/out" 2>"$WORK/err" || RC=$?
}

want_rc() { [ "$RC" = "$1" ] || fail "$2: exit $RC, want $1 (stdout: $(cat "$WORK/out"); stderr: $(cat "$WORK/err"))"; }
want_in() { grep -qF -- "$2" "$1" || fail "$3: $1 lacks '$2' (have: $(cat "$1"))"; }
want_not_in() { if grep -qF -- "$2" "$1"; then fail "$3: $1 holds '$2'"; fi; }

# --- order: sleeps 3, 1, 2 print whole, in roster order (and a stderr-only script) ---
a="$(stub a 'sleep 2; echo A1; echo A2; echo A-err >&2')"
b="$(stub b 'sleep 1; echo B1')"
c="$(stub c 'sleep 1; echo C-err-only >&2')"
HARNESS_TEETH_JOBS=3 run tests "$a" "$b" "$c"
want_rc 0 order
printf 'A1\nA2\nA-err\nB1\nC-err-only\n' >"$WORK/want"
cmp -s "$WORK/want" "$WORK/out" || fail "order: stdout is not the roster-ordered whole output: $(cat "$WORK/out")"

# --- teeth mode passes --teeth; tests mode passes nothing ---
t="$(stub argv 'echo "argc=$# arg=${1:-}"')"
HARNESS_TEETH_JOBS=2 run teeth "$t" "$t"
want_in "$WORK/out" 'argc=1 arg=--teeth' teeth-flag
HARNESS_TEETH_JOBS=2 run tests "$t" "$t"
want_in "$WORK/out" 'argc=0 arg=' tests-flag

# --- collect-all: two reds named, exit 1, nothing starts after the first red ---
r1="$(stub r1 'echo R1-out; exit 1')"
slow="$(stub slow 'sleep 1; echo SLOW-ran')"
r2="$(stub r2 'sleep 1; echo R2-out; exit 1')"
late="$(stub late "echo LATE-ran >$WORK/late.marker")"
HARNESS_TEETH_JOBS=3 run teeth "$r1" "$slow" "$r2" "$late"
want_rc 1 collect-all
want_in "$WORK/out" 'R1-out' collect-all
want_in "$WORK/out" 'R2-out' collect-all
want_in "$WORK/out" "harness-check: red: $r1" collect-all
want_in "$WORK/out" "harness-check: red: $r2" collect-all
want_in "$WORK/out" 'SLOW-ran' 'collect-all: a running script finishes'
want_in "$WORK/err" "harness-check: 2 red: $r1, $r2" collect-all
[ ! -e "$WORK/late.marker" ] || fail 'collect-all: a script started after the first red'
want_in "$WORK/out" "harness-check: not run — $late" collect-all

# --- a red in the last (serial-tail) call: one script, pool of 1 ---
HARNESS_TEETH_JOBS=1 run teeth "$r1"
want_rc 1 serial-tail
want_in "$WORK/err" "harness-check: 1 red: $r1" serial-tail

# --- pool-size chain: each step in turn, then all unset ---
run jobs
[ "$(cat "$WORK/out")" = "1 1" ] || fail "chain: all unset gave '$(cat "$WORK/out")', want '1 1'"
GOMAXPROCS=4 run jobs
[ "$(cat "$WORK/out")" = "4 1" ] || fail "chain: GOMAXPROCS gave '$(cat "$WORK/out")', want '4 1'"
GOMAXPROCS=4 HEAVY_RUN_CORES=6 run jobs
[ "$(cat "$WORK/out")" = "6 1" ] || fail "chain: HEAVY_RUN_CORES gave '$(cat "$WORK/out")', want '6 1'"
GOMAXPROCS=4 HEAVY_RUN_CORES=6 HARNESS_TEETH_JOBS=2 run jobs
[ "$(cat "$WORK/out")" = "2 3" ] || fail "chain: HARNESS_TEETH_JOBS gave '$(cat "$WORK/out")', want '2 3' (grant 6 / 2)"
HEAVY_RUN_CORES=6 HARNESS_TEETH_JOBS=4 run jobs
[ "$(cat "$WORK/out")" = "4 1" ] || fail "chain: grant 6 / 4 gave '$(cat "$WORK/out")', want '4 1' (floor of 1)"
HARNESS_TEETH_JOBS=abc run jobs
want_rc 2 'chain: junk value'

# --- the child really gets that GOMAXPROCS and its own TMPDIR ---
g1="$(stub g1 'echo "g1 procs=$GOMAXPROCS tmp=$TMPDIR"; touch "$TMPDIR/x"')"
g2="$(stub g2 'echo "g2 procs=$GOMAXPROCS tmp=$TMPDIR"')"
HEAVY_RUN_CORES=6 HARNESS_TEETH_JOBS=2 run tests "$g1" "$g2"
want_rc 0 child-env
want_in "$WORK/out" 'g1 procs=3' child-env
want_in "$WORK/out" 'g2 procs=3' child-env
t1="$(sed -n 's/.*g1 .*tmp=//p' "$WORK/out")"
t2="$(sed -n 's/.*g2 .*tmp=//p' "$WORK/out")"
[ -n "$t1" ] && [ "$t1" != "$t2" ] || fail "child-env: TMPDIR is not per child ($t1 vs $t2)"
[ ! -e "$t1" ] || fail "child-env: the temp parent outlived the run ($t1)"

# --- jobs=1: streams live, in order, child environment untouched ---
s1="$(stub s1 'echo "S1 procs=${GOMAXPROCS:-unset} tmp=${TMPDIR:-unset}"; sleep 1; echo S1-done')"
s2="$(stub s2 "echo S2 >$WORK/s2.marker")"
rm -f "$WORK/s2.marker"
: >"$WORK/out" # a stale file from the last case would satisfy the wait below
HARNESS_TEETH_JOBS=1 HEAVY_RUN_CORES=6 run tests "$s1" "$s2" &
pid=$!
n=0
until [ -s "$WORK/out" ] || [ "$n" -ge 100 ]; do
  n=$((n + 1))
  sleep 0.05 # real timing: waiting for the first live line of a streaming child
done
want_in "$WORK/out" 'S1 procs=unset' 'jobs=1 streams live'
want_not_in "$WORK/out" 'S1-done' 'jobs=1 streams live (script 1 not finished yet)'
wait "$pid" || true
want_in "$WORK/out" 'S1-done' 'jobs=1 order'
[ -e "$WORK/s2.marker" ] || fail 'jobs=1: the second script did not run'
# jobs=1 stops at the first red, like the loop it replaces.
rm -f "$WORK/late.marker"
HARNESS_TEETH_JOBS=1 run teeth "$r1" "$late"
want_rc 1 'jobs=1 stops at first red'
[ ! -e "$WORK/late.marker" ] || fail 'jobs=1: ran a script after a red'

# --- absent: today's skip line, verdict unchanged (both paths) ---
miss="$WORK/s/missing.sh"
ok="$(stub ok 'echo OK-ran')"
for j in 1 3; do
  HARNESS_TEETH_JOBS=$j run teeth "$miss" "$ok"
  want_rc 0 "absent jobs=$j"
  want_in "$WORK/out" "harness-check: skip — $miss absent (public checkout)." "absent jobs=$j"
  want_in "$WORK/out" 'OK-ran' "absent jobs=$j"
done

# --- rows: one tooth: row per script that ran, only into the sink ---
sink="$WORK/stages.jsonl"
for j in 1 3; do
  rm -f "$sink"
  A2A_STAGES_SINK="$sink" RUN_TREE=abc123 HARNESS_TEETH_JOBS=$j run teeth "$ok" "$miss" "$r1"
  want_rc 1 "rows jobs=$j"
  [ "$(wc -l <"$sink" | tr -d ' ')" = 2 ] || fail "rows jobs=$j: want 2 rows (ok, r1; not the absent one), have: $(cat "$sink")"
  want_in "$sink" "\"gate\":\"tooth:$ok\",\"verdict\":\"pass\",\"duration_ms\":" "rows jobs=$j"
  want_in "$sink" "\"gate\":\"tooth:$r1\",\"verdict\":\"fail\",\"duration_ms\":" "rows jobs=$j"
  want_in "$sink" '"mode":"teeth","at":"' "rows jobs=$j"
  want_in "$sink" '"tree":"abc123"}' "rows jobs=$j"
  grep -Eq '^\{"gate":"tooth:[^"]+","verdict":"(pass|fail)","duration_ms":[0-9]+,"mode":"teeth","at":"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}Z","tree":"[^"]*"\}$' "$sink" ||
    fail "rows jobs=$j: a row is not in the telemetry shape: $(cat "$sink")"
done
# unset sink: no file appears, and telemetry.jsonl is never named.
mkdir -p "$WORK/cwd"
(cd "$WORK/cwd" && HARNESS_TEETH_JOBS=2 bash "$RUNNER" tests "$ok" "$ok" >/dev/null 2>&1)
[ -z "$(ls -A "$WORK/cwd")" ] || fail "rows: a run with no sink wrote $(ls "$WORK/cwd")"
if grep -n 'telemetry.jsonl' "$RUNNER" | grep -v '^[0-9]*:#' >/dev/null; then
  fail 'rows: the runner code names telemetry.jsonl'
fi

# --- serial list: a name not in the roster is refused ---
run check-serial "a.sh b.sh" "a.sh b.sh c.sh"
want_rc 0 'serial list ok'
run check-serial "" "a.sh"
want_rc 0 'serial list empty'
run check-serial "a.sh zz.sh" "a.sh b.sh"
want_rc 1 'serial list refusal'
want_in "$WORK/err" 'zz.sh' 'serial list refusal names it'

echo "harness-run-test: ok"
