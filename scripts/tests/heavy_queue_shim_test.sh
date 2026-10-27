#!/usr/bin/env bash
# Proves the host-queue shim (GNUmakefile) classes goals the way the heavy-run
# doctrine says (rules 2 and 11, ~/Developer/_ops/doctrines/heavy-run.md):
#
#   - every heavy goal typed bare is queued once, through heavy_run, with
#     --project a2ahub, its own name as the label and its declared cores;
#   - inside a hold (HEAVY_RUN_SLOT set) nothing queues again;
#   - a light or unclassed goal is never queued and runs at 2 cores;
#   - command-line variables reach the make that runs inside the hold;
#   - without the host queue (CI, the parity container) the shim changes
#     nothing: no queue, no exported core count;
#   - every name on the shim's heavy list is a real target of the real Makefile.
#
# It never runs a real target. The REAL GNUmakefile is copied next to a stub
# Makefile whose catch-all recipe prints what it saw, and heavy_run is replaced
# through the shim's own seam, DEVKIT_HOST, by a fake that prints "queued" and
# its argv, then execs the command with a grant the way heavy_run does.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SHIM="$ROOT/GNUmakefile"

fail() { echo "heavy_queue_shim_test: FAIL — $*" >&2; exit 1; }

command -v python3 >/dev/null || { echo "heavy_queue_shim_test: skip — python3 absent"; exit 0; }
[ -f "$SHIM" ] || fail "GNUmakefile is missing at the repo root"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/repo" "$work/host/scripts"
cp "$SHIM" "$work/repo/GNUmakefile"
cat >"$work/repo/Makefile" <<'EOF'
.DEFAULT_GOAL := default-goal
Makefile GNUmakefile: ;
default-goal:
	@echo "ran default-goal"
%:
	@echo "ran $@ cores=$${HEAVY_RUN_CORES:-unset} gomaxprocs=$${GOMAXPROCS:-unset} slot=$${HEAVY_RUN_SLOT:-none} probe=$(PROBE)"
EOF
cat >"$work/host/scripts/heavy_run.py" <<'EOF'
import os, sys
argv = sys.argv[1:]
print("queued " + " ".join(argv), flush=True)
cores = argv[argv.index("--cores") + 1]
cmd = argv[argv.index("--") + 1:]
os.environ.update(HEAVY_RUN_SLOT="fake", HEAVY_RUN_CORES=cores, GOMAXPROCS=cores)
os.execvp(cmd[0], cmd)
EOF

# A clean make environment: under `make harness-check` this test inherits
# MAKELEVEL=1 and, inside the queue, a live HEAVY_RUN_SLOT, and the shim would
# then rightly do nothing.
mk() {
  env -u MAKELEVEL -u MAKEFLAGS -u MFLAGS -u GNUMAKEFLAGS -u MAKEOVERRIDES \
    -u HEAVY_RUN_SLOT -u HEAVY_RUN_CORES -u GOMAXPROCS \
    -u KIT_QUEUE_CORES -u KIT_QUEUE_CORES_DOCKER -u KIT_QUEUE_CORES_WEB -u KIT_HEAVY_TARGETS \
    DEVKIT_HOST="${HOST:-$work/host}" ${MK_ENV:-} make --no-print-directory -C "$work/repo" "$@"
}
has() { case "$1" in *"$2"*) return 0 ;; esac; return 1; }

heavy="$(sed -n 's/^a2a_heavy_targets := //p' "$SHIM")"
docker_goals=" $(sed -n 's/^a2a_docker_targets := //p' "$SHIM") "
web_goals=" $(sed -n 's/^a2a_web_targets := //p' "$SHIM") "
[ -n "$heavy" ] || fail "no a2a_heavy_targets line in GNUmakefile"
# `check` is the ~15 min ceiling here, not the template's light lint, and the
# container goals carry the docker grant: the two places a2ahub departs from it.
has " $heavy " " check " || fail "check must be on the heavy list: it is the ceiling here"
for goal in ci-parity-docker release-check; do
  has "$docker_goals" " $goal " || fail "$goal must carry the docker grant"
done

# (1) Every name on the list is a target the real Makefile defines.
for goal in $heavy; do
  grep -qE "^$goal:" "$ROOT/Makefile" || fail "heavy goal '$goal' is not a target in Makefile"
done

# (2) Every heavy goal, plus the pattern-classed ones, queues once with its cost.
for goal in $heavy check-validators space-template-baseline-check; do
  cores=6
  has "$docker_goals" " $goal " && cores=8
  has "$web_goals" " $goal " && cores=3
  out="$(mk "$goal" 2>&1)" || fail "make $goal exited non-zero: $out"
  has "$out" "queued run --project a2ahub --label $goal --cores $cores -- " \
    || fail "make $goal was not queued as label $goal at $cores cores: $out"
  has "$out" "ran $goal cores=$cores gomaxprocs=$cores slot=fake" \
    || fail "make $goal did not run inside the hold with its grant: $out"
done

# (3) Inside a hold nothing queues again.
out="$(MK_ENV=HEAVY_RUN_SLOT=1 mk check 2>&1)" || fail "nested make check exited non-zero: $out"
has "$out" "queued" && fail "make check re-queued inside a hold: $out"
has "$out" "ran check" || fail "make check did not run at once inside a hold: $out"

# (4) A light or unclassed goal is never queued and runs at 2 cores.
for goal in lane feature-lint; do
  out="$(mk "$goal" 2>&1)" || fail "make $goal exited non-zero: $out"
  has "$out" "queued" && fail "light goal $goal was queued: $out"
  has "$out" "ran $goal cores=2 gomaxprocs=2 slot=none" || fail "light goal $goal did not run at 2 cores: $out"
done

# (5) A command-line variable reaches the make inside the hold.
out="$(mk lane-run PROBE=reached 2>&1)" || fail "make lane-run PROBE=reached exited non-zero: $out"
has "$out" "probe=reached" || fail "a command-line variable was lost across the queue: $out"

# (6) Two goals queue once, under the first heavy goal's label.
out="$(mk check web-quality 2>&1)" || fail "make check web-quality exited non-zero: $out"
[ "$(grep -c '^queued ' <<<"$out")" -eq 1 ] || fail "two goals were queued more than once: $out"

# (7) A dry run is not queued.
out="$(mk -n check 2>&1)" || fail "make -n check exited non-zero: $out"
has "$out" "queued" && fail "make -n check was queued: $out"

# (8) No host queue: the shim is a plain include and exports nothing.
out="$(HOST="$work/no-such-host" mk check 2>&1)" || fail "make check without a queue exited non-zero: $out"
has "$out" "queued" && fail "make check queued with no host queue present: $out"
has "$out" "ran check cores=unset gomaxprocs=unset" || fail "the shim exported a core count with no host queue: $out"

# (9) The default goal is still Makefile's.
out="$(mk 2>&1)" || fail "a bare make exited non-zero: $out"
has "$out" "ran default-goal" || fail "a bare make no longer runs Makefile's default goal: $out"

echo "heavy_queue_shim_test: every heavy goal queues once with its cost, nothing re-queues inside a hold, light goals run at 2 cores, variables cross the queue, and without a queue the shim is inert."
