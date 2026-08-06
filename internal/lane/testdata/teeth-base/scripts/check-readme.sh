#!/usr/bin/env bash
# check-readme.sh — teeth-base fixture copy. A clean, fully-declared,
# fully-honest gate: reads exactly README.md, declares exactly README.md
# and docs/guide.md (the second is declared-but-unread on purpose, so
# receipt (a) can delete it without needing a second script).
#
# lane-inputs:
#   README.md
#   docs/guide.md
set -euo pipefail
grep -q "quick start" README.md
