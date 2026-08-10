#!/usr/bin/env bash
# Teeth wiring for check-loop-reachability.sh (agent-exchange P7, AC5). The
# mutation fixtures themselves live in the gate's own `--teeth` mode
# (scripts/check-loop-reachability.sh, the check-loop-coverage.sh /
# check-human-gates.sh sibling shape) — this file is the ONE invocation
# path that makes them actually run as part of `make harness-check`,
# mirroring how the other scripts/tests/*_test.sh wrappers here wire their
# gate's self-test into the same target.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check-loop-reachability.sh"

fail() {
  echo "check_loop_reachability_test: FAIL — $*" >&2
  exit 1
}

bash "$GATE" >/dev/null || fail "the real tree (every Concepts/Reference/Authoring manifest page reachable from skill/a2ahub/loops.md) is not green"
bash "$GATE" --teeth || fail "the gate's own --teeth fixtures did not all pass"

echo "check_loop_reachability_test: ok — real tree green, --teeth fixtures (dropped direct link, dropped second-hop link, dropped directory link, empty universe, missing loops.md) all red as designed"
