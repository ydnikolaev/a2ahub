#!/usr/bin/env bash
# check-readme.sh — keep the repository's front door compact, current, and
# useful. Tone still needs a human release review; this gate owns only the
# deterministic floor that review should never have to remember.
set -euo pipefail

ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
README_PATH="${README_PATH:-$ROOT/README.md}"

run_check() {
  if [ ! -f "$README_PATH" ]; then
    echo "readme-lint: FAIL — README.md is missing; restore the public project front door." >&2
    return 1
  fi

  local lines words fences fail=0
  lines="$(wc -l <"$README_PATH" | tr -d ' ')"
  words="$(wc -w <"$README_PATH" | tr -d ' ')"
  fences="$(grep -c '^```' "$README_PATH" || true)"

  if [ "$lines" -gt 100 ]; then
    echo "readme-lint: FAIL — README has $lines lines (max 100); move detail to linked documentation." >&2
    fail=1
  fi
  if [ "$words" -gt 700 ]; then
    echo "readme-lint: FAIL — README has $words words (max 700); keep only why, core capabilities, start, trust, and links." >&2
    fail=1
  fi
  if [ "$fences" -gt 4 ]; then
    echo "readme-lint: FAIL — README has $fences code fences (max 4); link the command reference instead of copying it." >&2
    fail=1
  fi

  local required
  for required in \
    "## What it gives you" \
    "## How it works" \
    "## Install" \
    "## Start a project" \
    "## Release confidence" \
    "## Documentation" \
    "https://a2ahub.dev/" \
    "skill/a2ahub/reference/commands.md" \
    "SECURITY.md"
  do
    if ! grep -qF "$required" "$README_PATH"; then
      echo "readme-lint: FAIL — README is missing '$required'; keep the compact public narrative and its documentation exits." >&2
      fail=1
    fi
  done

  local stale
  for stale in \
    "MCP read tools do not refresh a stale mirror" \
    "a2a_contract publish skips the client-side compatibility check" \
    "xattr -d com.apple.quarantine" \
    "the CLI is the surface to work through today"
  do
    if grep -qiF "$stale" "$README_PATH"; then
      echo "readme-lint: FAIL — README contains retired guidance '$stale'; describe the current product and keep old caveats in historical release notes." >&2
      fail=1
    fi
  done

  if [ "$fail" -ne 0 ]; then
    return 1
  fi
  echo "readme-lint: OK — $lines lines, $words words, compact sections and current links present."
}

run_teeth() {
  local tmp out rc
  tmp="$(mktemp -d)"
  trap "rm -rf -- '$tmp'" EXIT

  cp "$ROOT/README.md" "$tmp/README.md"
  README_PATH="$tmp/README.md" "$0" _internal-check >/dev/null

  printf '\nMCP read tools do not refresh a stale mirror.\n' >>"$tmp/README.md"
  set +e
  out="$(README_PATH="$tmp/README.md" "$0" _internal-check 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -eq 0 ] || ! grep -q "retired guidance" <<<"$out"; then
    echo "readme-lint --teeth: FAIL — adding retired guidance did not make the gate red:" >&2
    echo "$out" >&2
    exit 1
  fi

  echo "readme-lint --teeth: current README greens; adding a retired claim reds with an actionable message."
}

case "${1:-check}" in
  check|_internal-check) run_check ;;
  --teeth) run_teeth ;;
  *)
    echo "usage: $0 [check|--teeth]" >&2
    exit 2
    ;;
esac
