#!/usr/bin/env bash
# Wires check-mcp-publish-preconditions.sh's own --teeth into
# `make harness-check`, the same shape every other scripts/tests/*_test.sh
# wrapper uses. Without this file the script's assertions exist and nothing
# runs them — which is the defect the script itself exists to prevent one
# level up.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check-mcp-publish-preconditions.sh"

fail() { echo "check_mcp_publish_preconditions_test: FAIL — $*" >&2; exit 1; }

bash "$GATE" --teeth || fail "the script's own --teeth did not all pass"

# The workflow must reach it, or the teeth guard nothing that runs.
wf="$ROOT/.github/workflows/publish-mcp.yml"
if [ -f "$wf" ]; then
  grep -q 'check-mcp-publish-preconditions.sh' "$wf" \
    || fail "publish-mcp.yml does not invoke the precondition script it is for"
  pub="$(grep -n 'mcp-publisher publish' "$wf" | head -1 | cut -d: -f1)"
  pre="$(grep -n 'check-mcp-publish-preconditions.sh' "$wf" | head -1 | cut -d: -f1)"
  { [ -n "$pub" ] && [ -n "$pre" ] && [ "$pre" -lt "$pub" ]; } \
    || fail "the precondition step does not precede \`mcp-publisher publish\` (pre=$pre pub=$pub)"
fi

echo "check_mcp_publish_preconditions_test: ok — the script's teeth pass, and publish-mcp.yml invokes it BEFORE the publish call"
