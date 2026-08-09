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

# lane-inputs: ALWAYS
# lane-reason: check 1 reads `git ls-files` over the whole tracked set (line 114) and check 2 globs every top-level working-tree entry with `for e in *` (line 123) — any newly tracked or newly appearing top-level path can flip the verdict
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# ── SSOT of the public/private boundary. ──────────────────────────────────────────
# Every DENY entry MUST also be in .gitignore — check 3 asserts it, so the two can
# never drift into a leak. PENDING entries are deliberately NOT required to be
# gitignored (see docs/ above) until they graduate to DENY.
ALLOW_DIRS=( .github cmd integrations internal schemas skill space-template testkit seeds feedback web ui releasenotes )
PUBLIC_VALIDATOR_FILES=(
  scripts/check-lane-declarations.sh
  scripts/lib/lane-ungated.txt
  scripts/check_contract_carried_set.sh
  scripts/check_event_writer_receipts.sh
  scripts/check_live_e2e_evidence.sh
  scripts/check_localserver_readonly_routes.sh
  scripts/check_operational_projection_single_source.sh
  scripts/check_work_checkpoint_schema.sh
  scripts/tests/check_contract_carried_set_test.sh
  scripts/tests/check_event_writer_receipts_test.sh
  scripts/tests/check_live_e2e_evidence_test.sh
  scripts/tests/check_localserver_readonly_routes_test.sh
  scripts/tests/check_operational_projection_single_source_test.sh
  scripts/tests/check_work_checkpoint_schema_test.sh
)
ALLOW_FILES=( .gitignore .golangci.yml .goreleaser.yaml .gitleaks.toml .govulncheck-allow.txt Makefile SECURITY.md README.md LICENSE NOTICE go.mod go.sum cc-coverage.yaml scripts/install.sh scripts/dev-install.sh scripts/e2e-authoring-smoke.sh scripts/classify-guard.sh scripts/release-preflight.sh scripts/check-release-notes-freshness.sh scripts/check-roadmap-release-decisions.sh scripts/check-provider-tier-deferral.sh scripts/bump-space-template.sh scripts/check-gosec-scope.sh scripts/check-readme.sh scripts/dashboard-template-drift.sh scripts/check-view-vocabulary.sh scripts/check-pendency-uniqueness.sh scripts/check-loop-coverage.sh scripts/check-operational-confidence.sh scripts/verify.sh scripts/build-release-cohort.sh "${PUBLIC_VALIDATOR_FILES[@]}" )
DENY_DIRS=( .agents .claude .codex .mate .sporo )   # scripts/ handled below (install.sh + e2e-authoring-smoke.sh are the public exceptions)
DENY_FILES=( AGENTS.md CLAUDE.md )
PENDING_DIRS=( docs )   # deferred to P6 — tracked today, tolerated by check 1, classified by check 2, exempt from check 3.
# PRIVATE_ONLY_FILES: tracked HERE, deliberately stripped at publish. Same
# shape as PENDING_DIRS (tracked, not published, so not required to be
# gitignored) but permanent rather than deferred — these live inside an
# ALLOW_DIRS tree, so without this list they would classify as "public" and the
# guard would be lying about the boundary. Must stay in sync with the STRIP set
# in docs/runbooks/publish-to-public.sh.
PRIVATE_ONLY_FILES=( .github/dependabot.yml )
# .history is the editor's own local-history tree (VS Code writes timestamped
# snapshots of edited files there). Ephemeral and machine-local like .DS_Store:
# never tracked, never published, and it reappears the moment anyone edits a
# file, so classifying it once beats re-deciding it on every red gate.
IGNORE=( .git a2a bin dist go.work go.work.sum coverage.out .DS_Store .env .a2a .history )

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
  if in_list "$f" "${PRIVATE_ONLY_FILES[@]}"; then
    note "private-only (tracked here, stripped at publish): $f"
    continue
  fi
  t=$(top "$f")
  in_list "$t" "${ALLOW_DIRS[@]}" && continue
  if [ "$t" = scripts ]; then
    [ "$f" = "scripts/install.sh" ] && continue
    [ "$f" = "scripts/dev-install.sh" ] && continue
    [ "$f" = "scripts/e2e-authoring-smoke.sh" ] && continue
    [ "$f" = "scripts/release-preflight.sh" ] && continue
    [ "$f" = "scripts/check-release-notes-freshness.sh" ] && continue
    [ "$f" = "scripts/check-roadmap-release-decisions.sh" ] && continue
    [ "$f" = "scripts/check-gosec-scope.sh" ] && continue
    [ "$f" = "scripts/check-readme.sh" ] && continue
    [ "$f" = "scripts/check-operational-confidence.sh" ] && continue
    [ "$f" = "scripts/build-release-cohort.sh" ] && continue
    # install.sh's own regression net (P40 AC-1002.*) — public for the same
    # reason install.sh is. Matched as a prefix, not a filename: it is a Go
    # test package, so a second file in it is normal growth, not a new
    # boundary decision. Mirrored by `!scripts/installsh/` in .gitignore.
    case "$f" in scripts/installsh/*) continue ;; esac
    case "$f" in scripts/releasebody/*) continue ;; esac
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
if git check-ignore -q --no-index -- scripts/release-preflight.sh; then
  flag "scripts/release-preflight.sh must stay PUBLIC (the pre-tag release gate)  → add '!scripts/release-preflight.sh' to .gitignore"
fi
if git check-ignore -q --no-index -- scripts/check-release-notes-freshness.sh; then
  flag "scripts/check-release-notes-freshness.sh must stay PUBLIC (the offline notes gate)  → add '!scripts/check-release-notes-freshness.sh' to .gitignore"
fi
if git check-ignore -q --no-index -- scripts/check-gosec-scope.sh; then
  flag "scripts/check-gosec-scope.sh must stay PUBLIC (the gosec scope gate)  → add '!scripts/check-gosec-scope.sh' to .gitignore"
fi
if git check-ignore -q --no-index -- scripts/check-readme.sh; then
  flag "scripts/check-readme.sh must stay PUBLIC (the README release gate)  → add '!scripts/check-readme.sh' to .gitignore"
fi
if git check-ignore -q --no-index -- scripts/build-release-cohort.sh; then
  flag "scripts/build-release-cohort.sh must stay PUBLIC (the signed cohort manifest builder)  → add '!scripts/build-release-cohort.sh' to .gitignore"
fi
for f in "${PUBLIC_VALIDATOR_FILES[@]}"; do
  if git check-ignore -q --no-index -- "$f"; then
    flag "$f must stay PUBLIC (operational-confidence validator/gate)  → unignore it in .gitignore"
  fi
done
if git check-ignore -q --no-index -- scripts/classify-guard.sh; then
  flag "scripts/classify-guard.sh must stay PUBLIC (it IS this gate)  → add '!scripts/classify-guard.sh' to .gitignore"
fi
if git check-ignore -q --no-index -- scripts/releasebody/main.go; then
  flag "scripts/releasebody/ must stay PUBLIC (it renders GitHub Release notes from the shipped SSOT)  → add '!scripts/releasebody/' to .gitignore"
fi

# ── check 4: the publisher's STRIP set may not delete a file this gate calls PUBLIC. ──
#
# The PRIVATE_ONLY_FILES comment above has always said the two must "stay in
# sync with the STRIP set in docs/runbooks/publish-to-public.sh". Nothing
# enforced it, and on 2026-08-06 they drifted: P12 added
# `scripts/lib/lane-ungated.txt` — classified PUBLIC right here, in
# PUBLIC_VALIDATOR_FILES — while the publisher went on stripping the whole of
# `scripts/lib/`. So the public tree shipped `check-lane-declarations.sh`
# without the list it reads, and `make check` refused on its first phase.
#
# It cost a full release cycle to find, because it is invisible from here: the
# private tree is green by construction, and the ONLY thing that executes the
# filtered tree is a candidate. None had been cut between P12 landing and
# v0.19.9.
#
# The publisher lives under `docs/`, which is itself stripped, so this check is
# guarded on its presence exactly like the Makefile guards the private gates.
# On a public checkout there is nothing to compare against and skipping is the
# honest answer, not a failure.
PUBLISHER=docs/runbooks/publish-to-public.sh
if [ -f "$PUBLISHER" ]; then
  # The STRIP array as the publisher actually declares it: every `--path X`
  # inside the STRIP=( ... ) block, read from the file rather than re-typed
  # here — a copy would be the same drift one layer down.
  strip_paths="$(awk '/^STRIP=\(/{inside=1} inside{print} inside&&/\)/{exit}' "$PUBLISHER" |
    grep -oE -- '--path [^ )]+' | sed 's/^--path //')"
  if [ -z "$strip_paths" ]; then
    flag "$PUBLISHER — could not read its STRIP=( ) block; this check cannot vouch for the boundary"
  fi
  for public_file in "${ALLOW_FILES[@]}"; do
    while IFS= read -r stripped; do
      [ -n "$stripped" ] || continue
      case "$stripped" in
        */)
          # a directory strip swallows everything beneath it
          case "$public_file" in
            "$stripped"*)
              flag "$public_file is classified PUBLIC here but $PUBLISHER strips '$stripped' — it will be absent from every candidate" ;;
          esac ;;
        *)
          [ "$public_file" = "$stripped" ] &&
            flag "$public_file is classified PUBLIC here but $PUBLISHER strips it by name" ;;
      esac
    done <<< "$strip_paths"
  done
fi

if [ "$fail" -ne 0 ]; then
  printf '\nclassify-guard: \033[31mFAIL\033[0m — public/private boundary violated (fixes above).\n' >&2
  exit 1
fi
printf 'classify-guard: \033[32mOK\033[0m — every path classified, no private path tracked (docs/ pending-untrack deferred to P6).\n'
