#!/usr/bin/env bash
# classify-guard.sh — the publish-boundary gate (make-ABI: wired into `make check`
# and `make check-validators`).
#
# Invariant: the a2ahub repo tracks ONLY public (product) paths, and every
# top-level entry in the working tree is EXPLICITLY classified — public (ALLOW),
# private (DENY), pending-untrack (PENDING), or ephemeral (IGNORE). A new,
# unclassified entry is a RED: it forces a public/private decision instead of
# silently defaulting to whatever git happens to do. This is the "never forget
# to classify" guarantee.
#
# a2ahub-specific deviation from the sporo original this was ported from: docs/
# (planning) is tracked today and classified PENDING, not DENY — its untrack to
# a private planning home is deferred to publish-prep phase P6. Check 1
# TOLERATES a tracked PENDING path (note, no fail); DENY_DIRS/DENY_FILES still
# fail check 1 loudly; check 3 does NOT require PENDING dirs to be gitignored.
#
# Fail-closed: non-zero on any violation AND on its own internal error (set -e).
# The message prints the FIX, not the symptom.
#
# Honest limit: this guards new top-level entries and known globs. It does NOT
# catch a private note buried inside a public dir — that residual rests on the
# "planning goes in docs/" convention plus a secret scanner.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# ── SSOT of the public/private boundary. ──────────────────────────────────────────
# Every DENY entry MUST also be in .gitignore — check 3 asserts it, so the two can
# never drift into a leak. PENDING entries are deliberately NOT required to be
# gitignored (see docs/ above) until they graduate to DENY.
ALLOW_DIRS=( .github cmd internal schemas skill space-template testkit )
ALLOW_FILES=( .gitignore .golangci.yml .goreleaser.yaml .gitleaks.toml .govulncheck-allow.txt Makefile SECURITY.md README.md LICENSE NOTICE go.mod go.sum cc-coverage.yaml scripts/install.sh scripts/e2e-authoring-smoke.sh scripts/classify-guard.sh )
DENY_DIRS=( .agents .claude .codex .mate )   # scripts/ handled below (install.sh + e2e-authoring-smoke.sh are the public exceptions)
DENY_FILES=( AGENTS.md CLAUDE.md )
PENDING_DIRS=( docs )   # deferred to P6 — tracked today, tolerated by check 1, classified by check 2, exempt from check 3.
IGNORE=( .git a2a bin dist go.work go.work.sum coverage.out .DS_Store .env )

fail=0
flag() { printf '  \033[31m✗\033[0m %s\n' "$1" >&2; fail=1; }
note() { printf '  \033[33m•\033[0m %s\n' "$1" >&2; }
in_list() { local n=$1; shift; local x; for x in "$@"; do [ "$x" = "$n" ] && return 0; done; return 1; }
top() { printf '%s' "${1%%/*}"; }

# ── 1. No private path is tracked in this repo (the core teeth). ──────────────────
# PENDING_DIRS are tolerated here (noted once, no fail) — DENY_DIRS/DENY_FILES fall
# through to the generic flag below, same as any other unclassified path.
pending_noted=()
while IFS= read -r f; do
  [ -z "$f" ] && continue
  in_list "$f" "${ALLOW_FILES[@]}" && continue
  t=$(top "$f")
  in_list "$t" "${ALLOW_DIRS[@]}" && continue
  if [ "$t" = scripts ]; then
    [ "$f" = "scripts/install.sh" ] && continue
    [ "$f" = "scripts/e2e-authoring-smoke.sh" ] && continue
    flag "tracked but NOT public: $f  → 'git rm --cached $f' (private), or add it to ALLOW in scripts/classify-guard.sh (public)"
    continue
  fi
  if in_list "$t" "${PENDING_DIRS[@]:-}"; then
    if ! in_list "$t" "${pending_noted[@]:-}"; then
      note "pending-untrack (deferred to P6): $t/"
      pending_noted+=("$t")
    fi
    continue
  fi
  flag "tracked but NOT public: $f  → 'git rm --cached $f' (private), or add it to ALLOW in scripts/classify-guard.sh (public)"
done < <(git ls-files)

# ── 2. Every present top-level entry is classified. ──────────────────────────────
# `for e in *` with dotglob/nullglob — NOT `$(ls -A)`, whose UNQUOTED word-split
# lets a top-level entry whose name contains IFS whitespace evade classification
# (a gate that greens on an unclassified entry is a hole). dotglob makes `*`
# include dotfiles; nullglob makes an empty tree a clean no-op; neither yields
# `.`/`..`. Scoped to this loop; check 3 below uses no globs.
shopt -s dotglob nullglob
for e in *; do
  t=$(top "$e")
  in_list "$t" "${ALLOW_DIRS[@]}" && continue
  in_list "$e" "${ALLOW_FILES[@]}" && continue
  in_list "$t" "${DENY_DIRS[@]}" && continue
  in_list "$e" "${DENY_FILES[@]}" && continue
  in_list "$t" "${PENDING_DIRS[@]:-}" && continue
  [ "$t" = scripts ] && continue
  in_list "$t" "${IGNORE[@]}" && continue
  flag "UNCLASSIFIED top-level entry: $e  → decide public/private, add it to ALLOW or DENY in scripts/classify-guard.sh"
done
shopt -u dotglob nullglob

# ── 3. Manifest ↔ .gitignore coherence: every DENY is actually ignored. ──────────
# PENDING_DIRS (docs/) is deliberately excluded — it is not gitignored yet.
# --no-index: this is a pure pattern-match check ("would .gitignore hide this
# path"), independent of whether the path happens to be tracked right now. A
# tracked path is never reported "ignored" by plain `git check-ignore` (that's
# real git semantics, not a bug) — pre-untrack, the DENY paths ARE still
# tracked, and without --no-index this check would false-fail alongside check 1
# instead of proving the manifest/.gitignore text agree.
for d in "${DENY_DIRS[@]}"; do
  git check-ignore -q --no-index -- "$d/_probe" || flag "DENY dir not ignored: $d/  → add '$d/' to .gitignore (else it can be committed)"
done
for f in "${DENY_FILES[@]}"; do
  git check-ignore -q --no-index -- "$f" || flag "DENY file not ignored: $f  → add '$f' to .gitignore"
done
git check-ignore -q --no-index -- scripts/_probe || flag "scripts/ not ignored  → add 'scripts/*' to .gitignore"
if git check-ignore -q --no-index -- scripts/install.sh; then
  flag "scripts/install.sh must stay PUBLIC  → add '!scripts/install.sh' to .gitignore"
fi
if git check-ignore -q --no-index -- scripts/e2e-authoring-smoke.sh; then
  flag "scripts/e2e-authoring-smoke.sh must stay PUBLIC  → add '!scripts/e2e-authoring-smoke.sh' to .gitignore"
fi
if git check-ignore -q --no-index -- scripts/classify-guard.sh; then
  flag "scripts/classify-guard.sh must stay PUBLIC (it IS this gate)  → add '!scripts/classify-guard.sh' to .gitignore"
fi

if [ "$fail" -ne 0 ]; then
  printf '\nclassify-guard: \033[31mFAIL\033[0m — public/private boundary violated (fixes above).\n' >&2
  exit 1
fi
printf 'classify-guard: \033[32mOK\033[0m — every path classified, no private path tracked (docs/ pending-untrack deferred to P6).\n'
