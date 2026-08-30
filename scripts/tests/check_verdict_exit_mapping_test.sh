#!/usr/bin/env bash
# Teeth wiring for check-verdict-exit-mapping.sh (computed-not-listed-2026-08
# P4, spec 04-the-exit-code-declares.md §8 rows 6, 7, 12, 13, 14). The
# synthetic fixture trees themselves live in the gate's own `--teeth` mode
# (scripts/check-verdict-exit-mapping.sh, the check-human-gates.sh /
# check-cross-layer-test-import-ceiling.sh sibling shape) — this file is
# the ONE invocation path that makes them actually run as part of
# `make harness-check`, mirroring how the other scripts/tests/*_test.sh
# wrappers here wire their gate's self-test into the same target.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check-verdict-exit-mapping.sh"

fail() {
  echo "check_verdict_exit_mapping_test: FAIL — $*" >&2
  exit 1
}

bash "$GATE" >/dev/null || fail "the real tree (schemas/verdict-exit-codes.yaml against the computed cmd/a2a/wire.go + internal/cli/**/*.go universe) is not green"
bash "$GATE" --teeth || fail "the gate's own --teeth fixtures did not all pass"

echo "check_verdict_exit_mapping_test: ok — real tree green (every computed verb declared, every json-carrying file claimed); --teeth fixtures (universe growth from a fixture with no gate edit, an unclaimed json-carrying file, a {clean: 0}-only trivial declaration, a disagreeing declaration, and all three unmeasured arms) all behave as designed"
