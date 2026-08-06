#!/usr/bin/env bash
# classify-guard.sh — publish-boundary gate (fixture copy for internal/lane tests).
DENY_DIRS=( .agents .claude )   # scripts/ handled below
DENY_FILES=( AGENTS.md CLAUDE.md )
