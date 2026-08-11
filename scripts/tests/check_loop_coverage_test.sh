#!/usr/bin/env bash
# Teeth wiring for check-loop-coverage.sh (agent-exchange P7, AC4/US-4; wired
# by P13 H2). The eight mutation fixtures live in the gate's own `--teeth`
# mode — this file is the ONE invocation path that makes them actually run as
# part of `make harness-check`, mirroring its two siblings here.
#
# Why this file did not exist until 2026-08-12, which is the part worth
# recording: check-loop-coverage.sh is the ELDEST of the three and the one both
# siblings cite as the shape they copied — check_loop_reachability_test.sh's own
# header calls itself "the check-loop-coverage.sh / check-human-gates.sh sibling
# shape". Both siblings got a wrapper. The model did not. `make check` ran the
# gate in plain mode only (Makefile `loop-coverage`), so for eight mutations'
# worth of self-test, nothing had ever proved the gate bites.
#
# It does: the teeth passed on the first run after being wired. The defect was
# never in the fixtures, only in the fact that no documented target reached
# them — which is exactly the class this epic keeps finding, and the reason a
# gate's teeth belong to a target and not to whoever remembers.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check-loop-coverage.sh"

fail() {
  echo "check_loop_coverage_test: FAIL — $*" >&2
  exit 1
}

bash "$GATE" >/dev/null || fail "the real tree (every type x role x phase cell covered by a loops.md section or declared empty with a reason) is not green"
bash "$GATE" --teeth || fail "the gate's own --teeth fixtures did not all pass"

echo "check_loop_coverage_test: ok — real tree green, --teeth fixtures (no ledger entry, blank empty-reason, undeclared phase, undeclared type, zero-row type, dangling section id, empty phase list, both-covered-and-empty) all red as designed"
