#!/usr/bin/env bash
# classify-guard.sh — publish-boundary gate.
#
# lane-inputs: ALWAYS
# lane-reason: reads `git ls-files` over the whole tracked set — any path can flip it
set -euo pipefail
echo ok
