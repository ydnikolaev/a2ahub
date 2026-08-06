#!/usr/bin/env bash
# lane-inputs:
#   README.md
#   docs/guide.md
set -euo pipefail
grep -q "quick start" README.md
cat docs/other.md
