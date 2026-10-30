#!/usr/bin/env bash
# harness-run.sh — the teeth runner (release-speed-2026-11 P5, spec 05 T2 and T4).
#
#   harness-run.sh teeth <script>...          run `bash <script> --teeth` for each
#   harness-run.sh tests <script>...          run `bash <script>` for each
#   harness-run.sh jobs                       print "<pool> <child GOMAXPROCS>" and exit
#   harness-run.sh check-serial <serial> <roster>
#                                             refuse a serial-list name that is not
#                                             in the roster (both are word lists)
#
# POOL SIZE: HARNESS_TEETH_JOBS, else HEAVY_RUN_CORES, else GOMAXPROCS, else 1.
# Each child gets GOMAXPROCS = max(1, grant / pool), where grant is HEAVY_RUN_CORES,
# else GOMAXPROCS, else the pool size, so a tooth that runs `go build` cannot
# multiply the grant. With a pool of 1 nothing is changed for the child.
#
# A pool of 1 is today's serial loop: output streams live, the first red stops
# the run. A larger pool is `xargs -P` over a per-script wrapper (bash 3.2 has no
# `wait -n`): each child writes <n>.out (stdout and stderr), then <n>.rc; the
# parent prints every <n>.out whole, in roster order. COLLECT-ALL: the first red
# writes a sentinel; a wrapper that finds it before it starts records "not run";
# scripts already running finish. The run exits 1 and prints
# `harness-check: <n> red: a, b` on stderr, the shape of verify.sh's static block.
#
# Each pooled child runs with its own TMPDIR under one parent this script makes
# and removes on exit. Nothing else in its environment changes.
#
# A script that is absent prints today's `skip — absent (public checkout)` line.
#
# TELEMETRY (T4): with A2A_STAGES_SINK set, one row per script that ran is
# appended to that file, in the telemetry row shape:
#   {"gate":"tooth:<script>","verdict","duration_ms","mode","at","tree"}
# `mode` is this runner's mode (teeth or tests); `tree` is $RUN_TREE when the
# caller exports it, else empty. Unset sink: no row. Never telemetry.jsonl.
#
# bash 3.2 compatible: no `wait -n`, no associative arrays, no `mapfile`.
set -uo pipefail

now_ms() { perl -MTime::HiRes=time -e 'printf "%.0f\n", time() * 1000'; }

is_pos_int() { case "${1:-}" in '' | *[!0-9]* | 0 | 0[0-9]*) return 1 ;; esac; return 0; }

# resolve_pool prints "<pool> <grant>"; refuses a value that is not a positive integer.
resolve_pool() {
  local pool grant name
  pool=""
  for name in HARNESS_TEETH_JOBS HEAVY_RUN_CORES GOMAXPROCS; do
    if [ -n "${!name:-}" ]; then
      is_pos_int "${!name}" || { echo "harness-check: $name=${!name} is not a positive integer" >&2; return 2; }
      [ -n "$pool" ] || pool="${!name}"
    fi
  done
  [ -n "$pool" ] || pool=1
  grant="${HEAVY_RUN_CORES:-${GOMAXPROCS:-$pool}}"
  printf '%s %s\n' "$pool" "$grant"
}

child_procs() { # <pool> <grant>
  local c=$(($2 / $1))
  [ "$c" -ge 1 ] || c=1
  printf '%s\n' "$c"
}

mode_flag() { [ "$1" = teeth ] && printf '%s' '--teeth' || true; }

# row <mode> <script> <rc> <ms>
row() {
  [ -n "${A2A_STAGES_SINK:-}" ] || return 0
  local verdict=pass
  [ "$3" = 0 ] || verdict=fail
  printf '{"gate":"tooth:%s","verdict":"%s","duration_ms":%s,"mode":"%s","at":"%s","tree":"%s"}\n' \
    "$2" "$verdict" "$4" "$1" "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "${RUN_TREE:-}" >>"$A2A_STAGES_SINK"
}

absent_line() { printf 'harness-check: skip — %s absent (public checkout).\n' "$1"; }

summary() { # <red...>
  local n=$# list="" r
  for r in "$@"; do list="${list:+$list, }$r"; done
  echo "harness-check: $n red: $list" >&2
}

run_serial() { # <mode> <script>...
  local mode="$1" s start rc flag
  shift
  flag="$(mode_flag "$mode")"
  for s in "$@"; do
    if [ ! -f "$s" ]; then
      absent_line "$s"
      continue
    fi
    start="$(now_ms)"
    if bash "$s" ${flag:+"$flag"}; then rc=0; else rc=$?; fi
    row "$mode" "$s" "$rc" "$(($(now_ms) - start))"
    if [ "$rc" != 0 ]; then
      echo "harness-check: red: $s"
      summary "$s"
      return 1
    fi
  done
  return 0
}

WORK=""
XPID=""
cleanup() {
  if [ -n "$XPID" ]; then kill "$XPID" 2>/dev/null || true; fi
  if [ -n "$WORK" ]; then rm -rf "$WORK"; fi
}

# The per-script wrapper, run by xargs as `bash wrap.sh <n> <script>`. It never
# exits non-zero: the verdict lives in <n>.rc.
write_wrapper() {
  cat >"$WORK/wrap.sh" <<'WRAP'
n="$1"
s="$2"
d="$HR_WORK/$n"
done_rc() { printf '%s\n' "$1" >"$d.rc.tmp" && mv "$d.rc.tmp" "$d.rc"; }
if [ -e "$HR_WORK/sentinel" ]; then
  printf 'harness-check: not run — %s (an earlier tooth is red)\n' "$s" >"$d.out"
  done_rc notrun
  exit 0
fi
if [ ! -f "$s" ]; then
  printf 'harness-check: skip — %s absent (public checkout).\n' "$s" >"$d.out"
  done_rc absent
  exit 0
fi
mkdir -p "$HR_WORK/tmp.$n"
start="$(perl -MTime::HiRes=time -e 'printf "%.0f\n", time() * 1000')"
if TMPDIR="$HR_WORK/tmp.$n" GOMAXPROCS="$HR_CHILD_PROCS" bash "$s" ${HR_FLAG:+"$HR_FLAG"} >"$d.out" 2>&1; then rc=0; else rc=$?; fi
end="$(perl -MTime::HiRes=time -e 'printf "%.0f\n", time() * 1000')"
printf '%s\n' "$((end - start))" >"$d.ms"
[ "$rc" = 0 ] || : >"$HR_WORK/sentinel"
done_rc "$rc"
exit 0
WRAP
}

run_pool() { # <mode> <pool> <child_procs> <script>...
  local mode="$1" pool="$2" cprocs="$3" n=0 i s rc ms reds="" nreds=0 flag
  shift 3
  local scripts=("$@")
  flag="$(mode_flag "$mode")"
  WORK="$(mktemp -d)" || return 2
  trap cleanup EXIT
  trap 'cleanup; exit 130' INT TERM
  write_wrapper
  export HR_WORK="$WORK" HR_CHILD_PROCS="$cprocs" HR_FLAG="$flag"
  {
    for s in "${scripts[@]}"; do
      n=$((n + 1))
      printf '%s\0%s\0' "$n" "$s"
    done
  } | xargs -0 -n 2 -P "$pool" bash "$WORK/wrap.sh" &
  XPID=$!
  i=0
  for s in "${scripts[@]}"; do
    i=$((i + 1))
    while [ ! -e "$WORK/$i.rc" ]; do
      if ! kill -0 "$XPID" 2>/dev/null && [ ! -e "$WORK/$i.rc" ]; then
        echo "harness-check: no result for $s (the pool ended early)"
        rc=pool
        break
      fi
      sleep 0.05 # real timing: polling for a child's result file, no event to wait on
    done
    if [ "${rc:-}" = pool ]; then
      rc=""
      reds="${reds:+$reds }$s"
      nreds=$((nreds + 1))
      continue
    fi
    cat "$WORK/$i.out"
    rc="$(cat "$WORK/$i.rc")"
    case "$rc" in
      notrun | absent) ;;
      0 | [0-9]*)
        ms="$(cat "$WORK/$i.ms" 2>/dev/null || echo 0)"
        row "$mode" "$s" "$rc" "$ms"
        if [ "$rc" != 0 ]; then
          echo "harness-check: red: $s"
          reds="${reds:+$reds }$s"
          nreds=$((nreds + 1))
        fi
        ;;
    esac
  done
  wait "$XPID" 2>/dev/null || true
  XPID=""
  if [ "$nreds" -gt 0 ]; then
    # shellcheck disable=SC2086 # reds is a space-joined list of paths
    summary $reds
    return 1
  fi
  return 0
}

main() {
  local cmd="${1:-}" pg pool grant cprocs s
  [ $# -gt 0 ] && shift
  case "$cmd" in
    jobs)
      pg="$(resolve_pool)" || return 2
      pool="${pg% *}"
      grant="${pg#* }"
      printf '%s %s\n' "$pool" "$(child_procs "$pool" "$grant")"
      ;;
    check-serial)
      local bad="" w r found
      for w in ${1:-}; do
        found=""
        for r in ${2:-}; do [ "$r" = "$w" ] && found=1; done
        [ -n "$found" ] || bad="${bad:+$bad, }$w"
      done
      if [ -n "$bad" ]; then
        echo "harness-check: HARNESS_TEETH_SERIAL names a script that is not in HARNESS_TEETH: $bad" >&2
        return 1
      fi
      ;;
    teeth | tests)
      [ $# -gt 0 ] || return 0
      pg="$(resolve_pool)" || return 2
      pool="${pg% *}"
      grant="${pg#* }"
      if [ "$pool" -le 1 ]; then
        run_serial "$cmd" "$@"
        return $?
      fi
      cprocs="$(child_procs "$pool" "$grant")"
      run_pool "$cmd" "$pool" "$cprocs" "$@"
      ;;
    *)
      echo "usage: harness-run.sh teeth|tests <script>... | jobs | check-serial <serial> <roster>" >&2
      return 2
      ;;
  esac
}

main "$@"
