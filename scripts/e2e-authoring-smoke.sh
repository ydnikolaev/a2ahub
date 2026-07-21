#!/usr/bin/env bash
# e2e-authoring-smoke.sh — live end-to-end smoke of the a2a authoring path
# (the wave-3 readiness gate: green `make check` proves the fakes pass, this
# proves the wired binary works). Runs init -> new -> validate -> template in
# a throwaway project. Live GitHub submit is NOT covered here — that is
# E2E-1/E2E-7 against the real getvisa space (P11).
#
# Usage: bash scripts/e2e-authoring-smoke.sh
set -euo pipefail

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
bin="$work/a2a"

echo "==> build"
go build -o "$bin" ./cmd/a2a

proj="$work/proj"
mkdir -p "$proj"
run() { ( cd "$proj" && "$bin" "$@" ); }

echo "==> version"; run version
echo "==> init"; run init --system axon --space https://github.com/getvisa/getvisa
echo "==> init (idempotent)"; run init --system axon --space https://github.com/getvisa/getvisa
echo "==> template list"; run template list
echo "==> new question"; run new question --field title="Which ISO codes?" --field space=getvisa
echo "==> validate --all"; run validate --all

# Read surface (P7) — pre-onboarding / no-mirror state must be graceful:
# statusline silent+exit0 (CC-092), inbox/outbox empty JSON array.
echo "==> statusline (silent, exit 0 when nothing actionable)"; run statusline; echo "   statusline exit=$?"
echo "==> inbox --json (empty array before any content)"; run inbox --json
echo "==> outbox --json"; run outbox --json

echo "==> OK: authoring + read path green"
