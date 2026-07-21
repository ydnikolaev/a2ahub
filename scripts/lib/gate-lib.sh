#!/usr/bin/env bash
# gate-lib.sh — shared helpers for repo gate scripts (scripts/check-*.sh).
#
# Source it from a script in scripts/:
#     source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"
#
# Deliberately a NEW lib under scripts/lib/ rather than reusing
# .agents/scripts/lib/common.sh — live `check` gates must NOT depend on the
# `.agents/` tree the pipeline epic is deprecating (removal hazard / dependency
# direction). NOT auto-exec — source only.
#
# Adopted by ALL 15 scripts/check-*.sh (migration completed 2026-06-20, done in
# verified batches — never a big-bang rewrite, since a bug here would fail every gate
# in check at once). Collect-all gates use gate_fail/gate_warn/gate_ok + gate_summary;
# fail-fast gates use GATE_ROOT only (their exit-on-first-error model is preserved).
#
# Provides: GATE_ROOT (repo root), gate_fail / gate_warn / gate_ok (CI-aware),
# gate_summary (prints tally, returns 1 if any error).

# Repo root, resolved canonically from this lib's own location (scripts/lib → repo).
# shellcheck disable=SC2034  # consumed by sourcing scripts, not within this lib
GATE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# Colors — off when stdout is not a TTY or NO_COLOR is set.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  GATE_RED=$'\033[0;31m'; GATE_YEL=$'\033[0;33m'; GATE_GRN=$'\033[0;32m'; GATE_NC=$'\033[0m'
else
  GATE_RED=''; GATE_YEL=''; GATE_GRN=''; GATE_NC=''
fi

_GATE_ERRORS=0
_GATE_WARNINGS=0

# In GitHub Actions, emit workflow annotations; otherwise plain text.
_gate_ci() { [ "${GITHUB_ACTIONS:-}" = "true" ]; }

gate_fail() {
  _GATE_ERRORS=$((_GATE_ERRORS + 1))
  if _gate_ci; then echo "::error::$*"; else echo "${GATE_RED}FAIL${GATE_NC} $*" >&2; fi
}

gate_warn() {
  _GATE_WARNINGS=$((_GATE_WARNINGS + 1))
  if _gate_ci; then echo "::warning::$*"; else echo "${GATE_YEL}WARN${GATE_NC} $*" >&2; fi
}

gate_ok() { echo "${GATE_GRN}✓${GATE_NC} $*"; }

# Gate-firing telemetry (Δ15) — one JSONL line per gate run; the data source for
# the retirement + suite-wall-clock scans (a "never fired in N months" verdict
# without firing data is a guess). Duration = bash $SECONDS (whole seconds since
# the gate script started — v1 granularity). Appends are best-effort: telemetry
# must NEVER fail a gate. Home: .claude/.telemetry/gates.jsonl (gitignored,
# survives `make clean-artifacts`).
gate_telemetry() { # <name> <verdict:green|red|warn>
  {
    mkdir -p "$GATE_ROOT/.claude/.telemetry" &&
      printf '{"ts":"%s","gate":"%s","verdict":"%s","duration_s":%d,"errors":%d,"warnings":%d}\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "$2" "$SECONDS" "$_GATE_ERRORS" "$_GATE_WARNINGS" \
        >> "$GATE_ROOT/.claude/.telemetry/gates.jsonl"
  } 2>/dev/null || true
}

# Print the tally; return 1 if any error (use as the script's exit status).
gate_summary() {
  local name="${1:-gate}"
  if [ "$_GATE_ERRORS" -gt 0 ]; then
    gate_telemetry "$name" red
    echo "${GATE_RED}✗ ${name}: ${_GATE_ERRORS} error(s), ${_GATE_WARNINGS} warning(s)${GATE_NC}" >&2
    return 1
  fi
  if [ "$_GATE_WARNINGS" -gt 0 ]; then
    gate_telemetry "$name" warn
    echo "${GATE_YEL}${name}: ${_GATE_WARNINGS} warning(s), no errors${GATE_NC}"
    return 0
  fi
  gate_telemetry "$name" green
  return 0
}
