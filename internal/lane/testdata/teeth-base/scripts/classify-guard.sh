#!/usr/bin/env bash
# classify-guard.sh — teeth-base fixture copy. NOT a corpus phase (not in
# REPO_GATES); it exists here only so internal/lane.Universe's denyDirs()
# read has a DENY_DIRS array to find, the same role
# testdata/universe-fixture/scripts/classify-guard.sh already plays.
DENY_DIRS=( .agents .claude )
