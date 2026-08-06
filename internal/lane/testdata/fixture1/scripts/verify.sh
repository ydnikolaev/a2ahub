#!/usr/bin/env bash
set -euo pipefail

run_phase() {
  local gate="$1"
  shift
  "$@"
}

run_repo_gates() {
  local gate gates
  gates="$(make --no-print-directory -s _print-repo-gates)"
  for gate in $gates; do
    run_phase "$gate" make --no-print-directory "$gate"
  done
}

# lane-inputs:
#   cmd/a2a/**/*.go
#   go.mod
#   go.sum
build_cli() {
  go build -o "$A2A_VERIFY_BINARY" ./cmd/a2a
}

check_gofmt() {
  gofmt -l .
}

run_teeth() {
  local tmp="$1"
  run_phase deliberate-red false
}

if [ "$MODE" = "full" ]; then
  # lane-inputs:
  #   **/*.go
  #   go.mod
  #   go.sum
  run_phase vet go vet ./...
fi

run_phase build-cli build_cli
run_phase gofmt check_gofmt
