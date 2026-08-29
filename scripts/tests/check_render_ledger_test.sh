#!/usr/bin/env bash
# Teeth wiring for check-render-ledger.sh (answers-that-hold-2026-08 P3,
# spec 03 §T1). The mutation fixtures live in the gate's own `--teeth`
# mode; this file is the invocation path that makes `make harness-check`
# actually run them, mirroring check_loop_coverage_test.sh's own shape and
# its own stated reason: a gate's teeth belong to a target, not to whoever
# remembers to run them by hand.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check-render-ledger.sh"

fail() {
  echo "check_render_ledger_test: FAIL — $*" >&2
  exit 1
}

bash "$GATE" >/dev/null || fail "the real tree (every contract render field human-rendered or declared json-only with a structural reason) is not green"
bash "$GATE" --teeth || fail "the gate's own --teeth fixtures did not all pass"

echo "check_render_ledger_test: ok — real tree green, --teeth fixtures (missing field, deferral-shaped json-only reason, blank json-only reason, blank human value, surface absent from surfaces:, both-human-and-json-only, ledger key outside the derived universe) all red as designed"
