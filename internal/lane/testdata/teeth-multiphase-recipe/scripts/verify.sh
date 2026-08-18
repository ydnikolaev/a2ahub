#!/usr/bin/env bash
set -euo pipefail

run_phase() {
  local gate="$1"
  shift
  "$@"
}

# lane-inputs:
#   README.md
# lane-reads-opaque: SHARED_VAR is a scratch debug path, not a repo input
run_phase_a() {
  grep -q x "$SHARED_VAR"
}

# lane-inputs:
#   README.md
run_phase_b() {
  cat "$OTHER_VAR"
}

run_phase phase-a run_phase_a
run_phase phase-b run_phase_b
