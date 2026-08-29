#!/usr/bin/env bash
# Teeth wiring for check-usage-workflow.sh (answers-that-hold-2026-08 P8,
# spec 08). The mutation fixtures live in the gate's own `--teeth` mode;
# this file is the invocation path that makes `make harness-check` actually
# run them, mirroring check_render_ledger_test.sh's own shape and its own
# stated reason: a gate's teeth belong to a target, not to whoever
# remembers to run them by hand.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check-usage-workflow.sh"

fail() {
  echo "check_usage_workflow_test: FAIL — $*" >&2
  exit 1
}

bash "$GATE" >/dev/null || fail "the real tree (every catalogue verb whose docs-manifest.json topic set the 2026-08-29 widening derives is non-empty has a workflowLine pointer, and every named topic resolves and is accepted by \`a2a docs\`) is not green"
bash "$GATE" --teeth || fail "the gate's own --teeth fixtures did not all pass"

echo "check_usage_workflow_test: ok — real tree green (the 2026-08-29 widened universe, not just feedback/notify/notifications), --teeth fixtures (a universe verb missing its workflow line, a workflow line naming a nonexistent manifest topic, an unresolvable dynamic argument not mistaken for a real one, a decoy string outside any call left untouched, the verify/verify-pass longest-match trap, a valid-but-wrong-verb topic, a mechanism-2 map-literal pair checked the same way, a missing docs-manifest.json) all red/behaved as designed"
