#!/usr/bin/env bash
# VG-OC-24: validate one explicitly selected P7 release/live evidence manifest.
# Ordinary PR gates exercise this validator's code and teeth; they do not
# require a release artifact that does not exist yet.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

if [[ $# -ne 1 || -z "$1" ]]; then
  echo "live-e2e-evidence: usage: $0 <evidence.json>" >&2
  echo "live-e2e-evidence: fix: pass the exact candidate's persisted P7 evidence manifest" >&2
  exit 2
fi

EVIDENCE_FILE="$1"
if [[ ! -f "$EVIDENCE_FILE" ]]; then
  echo "live-e2e-evidence: FAIL — evidence file is not a regular file: $EVIDENCE_FILE" >&2
  exit 1
fi

if ! (
  cd "$ROOT"
  A2A_LIVE_E2E_EVIDENCE_FILE="$EVIDENCE_FILE" \
    A2A_VERIFY_TEST_RUN='^TestEvidenceFileGate$' \
    bash scripts/verify.sh test ./internal/livee2e/...
); then
  echo "live-e2e-evidence: fix: regenerate the manifest from two clean detached checkouts and rerun every red/missing live row" >&2
  exit 1
fi

echo "live-e2e-evidence: ok — exact candidate attestations and candidate-bound scenario logs are release-ready"
