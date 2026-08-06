#!/usr/bin/env bash
# Guards the pre-P5 trust-boundary decision: G204/G304 may be suppressed only
# by the exact path rules in .golangci.yml, never globally.
#
# lane-inputs:
#   .golangci.yml
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="$ROOT/.golangci.yml"

check_config() {
  if sed -n '/^[[:space:]]*gosec:/,/^[[:space:]][^[:space:]]/p' "$CONFIG" |
    grep -Eq '^[[:space:]]*-[[:space:]]*G(204|304)([[:space:]#]|$)'; then
    echo "gosec-scope: FAIL — G204/G304 are globally excluded under linters.settings.gosec.excludes." >&2
    return 1
  fi
  echo "gosec-scope: G204/G304 have no repository-global exclusion."
}

run_teeth() {
  command -v golangci-lint >/dev/null 2>&1 || {
    echo "gosec-scope --teeth: golangci-lint is required." >&2
    return 1
  }

  local tmp output rc
  tmp="$(mktemp -d)"
  printf '%s\n' 'module example.invalid/gosec-teeth' 'go 1.26' >"$tmp/go.mod"
  cat >"$tmp/unsafe.go" <<'EOF'
package main

import (
	"context"
	"os"
	"os/exec"
)

func main() {
	input := os.Args[1]
	_, _ = os.ReadFile(input)
	_ = exec.CommandContext(context.Background(), input, os.Args[2:]...).Run()
}
EOF

  set +e
  output="$(cd "$tmp" && golangci-lint run --config "$CONFIG" ./... 2>&1)"
  rc=$?
  set -e
  rm -rf "$tmp"
  if [ "$rc" -eq 0 ]; then
    echo "gosec-scope --teeth: FAIL — an unsafe file outside the allowlist stayed green." >&2
    return 1
  fi
  for code in G204 G304; do
    if ! grep -q "$code:" <<<"$output"; then
      echo "gosec-scope --teeth: FAIL — fixture did not report $code:" >&2
      echo "$output" >&2
      return 1
    fi
  done
  echo "gosec-scope --teeth: outside-allowlist exec and file read both red."
}

case "${1:-check}" in
  check) check_config ;;
  --teeth)
    check_config
    run_teeth
    ;;
  *)
    echo "usage: $0 [check|--teeth]" >&2
    exit 2
    ;;
esac
