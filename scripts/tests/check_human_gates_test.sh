#!/usr/bin/env bash
# Teeth wiring for check-human-gates.sh (agent-exchange P7, AC4). The
# mutation fixtures themselves live in the gate's own `--teeth` mode
# (scripts/check-human-gates.sh, the check-loop-coverage.sh sibling shape) —
# this file is the ONE invocation path that makes them actually run as part
# of `make harness-check`, mirroring how the other scripts/tests/*_test.sh
# wrappers here wire their gate's self-test into the same target.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check-human-gates.sh"

fail() {
  echo "check_human_gates_test: FAIL — $*" >&2
  exit 1
}

bash "$GATE" >/dev/null || fail "the real tree (skill/a2ahub/loops.md against the binary's human_gates) is not green"
bash "$GATE" --teeth || fail "the gate's own --teeth fixtures did not all pass"

echo "check_human_gates_test: ok — real tree green, --teeth fixtures (missing marker, over-declared verb, omitted verb, self-serve command form) all red as designed"
