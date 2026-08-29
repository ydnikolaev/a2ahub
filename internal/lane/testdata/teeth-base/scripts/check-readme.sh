#!/usr/bin/env bash
# check-readme.sh — teeth-base fixture copy. A clean, fully-declared,
# fully-honest gate: it declares README.md and docs/guide.md, and READS
# BOTH. The second declaration still exists purely so receipt (a) can
# delete it without needing a second script — it is the deletion target,
# not an unread claim.
#
# It used to be declared-but-unread on purpose, which stopped being a
# CLEAN baseline the moment a declared glob that nothing reads became a
# refusal (computed-not-listed-2026-08 P1). A fixture that models the
# defect cannot also be the control the mutation tests measure against.
#
# lane-inputs:
#   README.md
#   docs/guide.md
set -euo pipefail
grep -q "quick start" README.md
grep -q "guide" docs/guide.md
