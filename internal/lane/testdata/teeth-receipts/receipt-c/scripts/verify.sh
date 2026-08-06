#!/usr/bin/env bash
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

run_vet() {
  go vet ./...
}

run_phase gofmt run_gofmt
run_phase vet run_vet
