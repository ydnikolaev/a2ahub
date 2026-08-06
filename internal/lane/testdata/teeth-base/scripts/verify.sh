#!/usr/bin/env bash
# verify.sh — teeth-base fixture copy: one bare run_phase statement wrapped
# by a function, the minimum corpusFromVerify needs (it hard-requires at
# least one top-level run_phase call site).
set -euo pipefail

run_phase() {
  local gate="$1"
  shift
  "$@"
}

# lane-inputs:
#   **/*.go
#   go.mod
run_gofmt() {
  gofmt -l .
}

run_phase gofmt run_gofmt
