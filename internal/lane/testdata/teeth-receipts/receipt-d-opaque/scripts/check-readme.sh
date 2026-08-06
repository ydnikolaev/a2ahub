#!/usr/bin/env bash
# lane-inputs:
#   README.md
#   docs/guide.md
# lane-reads-opaque: EXTRA_PATH is a manual debug override, not a repo input
set -euo pipefail
grep -q "quick start" README.md
grep -q "x" "$EXTRA_PATH"
