#!/usr/bin/env bash
# Teeth for check_event_writer_receipts.sh. Each red case mutates a copy of a
# real production writer; no synthetic grep-count fixture can satisfy it.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check_event_writer_receipts.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() {
  echo "event-writer-receipts-test: FAIL — $*" >&2
  exit 1
}

copy_tree() {
  local destination="$1"
  mkdir -p "$destination/internal"
  cp -R "$ROOT/internal/cli" "$destination/internal/cli"
  cp -R "$ROOT/internal/mcp" "$destination/internal/mcp"
}

expect_red() {
  local tree="$1" needle="$2" label="$3" output
  if output="$(EVENT_WRITER_ROOT="$tree" bash "$GATE" 2>&1)"; then
    fail "$label stayed green"
  fi
  if ! grep -Fq "$needle" <<<"$output"; then
    fail "$label red did not name '$needle': $output"
  fi
}

bash "$GATE" >/dev/null || fail "the real writer tree is not green"

bypass="$WORK/bypass"
copy_tree "$bypass"
target="$bypass/internal/cli/cmd_submit.go"
perl -0pi -e 's/State:\s+string\(evaluation\.Outcome\),/State:      "caller-chosen",/' "$target"
grep -Fq 'State:      "caller-chosen",' "$target" || fail "could not seed the real submit writer bypass"
expect_red "$bypass" "cmd_submit.go" "canonical-evaluation bypass"
expect_red "$bypass" "State bypasses canonical fold evaluation" "canonical-evaluation diagnostic"

public_input="$WORK/public-input"
copy_tree "$public_input"
target="$public_input/internal/mcp/tools_submit.go"
perl -0pi -e 's/type SubmitInput struct \{/type SubmitInput struct {\n\tState string `json:"state,omitempty"`/' "$target"
grep -Fq 'json:"state,omitempty"' "$target" || fail "could not seed the real MCP public input"
expect_red "$public_input" "SubmitInput exposes user-authored State" "user-authored MCP state"

echo "event-writer-receipts-test: ok — real CLI bypass and MCP state-input mutations both red; production tree greens"
