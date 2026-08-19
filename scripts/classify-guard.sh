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
# lane-reads-opaque: check 5 greps each PUBLIC gate script in turn — `grep -E ... "$public_file"` — where $public_file is bound by iterating ALLOW_FILES, this file's own literal list. The set is declared here and nowhere else, so the read is bounded by this script's own contents rather than by any caller input; the classifier resolves literals, not a loop variable.
set -euo pipefail

# The tree under judgement. Defaults to this repository, which is the only
# thing it was ever pointed at — and that is precisely why this gate was the
# last one in the repo with no teeth: a check that can only ever run against a
# tree that is green by construction has nowhere to put a violating fixture.
# `scripts/tests/classify_guard_test.sh` builds one repo per check and points
# this at it. Same shape as CONTRACT_CARRIED_SET_ROOT next door.
#
# The asymmetry that makes the teeth mean something: the BOUNDARY (ALLOW/DENY/
# PENDING/PRIVATE_ONLY, below) is declared in THIS file and is never read from
# the tree, while the tree supplies only the facts being judged. Teaching these
# lists to read from $CLASSIFY_GUARD_ROOT would make every fixture agree with
# itself and silently delete every case below — the same trap
# check_contract_carried_set.sh records in its own header.
cd "${CLASSIFY_GUARD_ROOT:-$(git rev-parse --show-toplevel)}"

# ── SSOT of the public/private boundary. ──────────────────────────────────────────
# Every DENY entry MUST also be in .gitignore — check 3 asserts it, so the two can
# never drift into a leak. PENDING entries are deliberately NOT required to be
# gitignored (see docs/ above) until they graduate to DENY.
ALLOW_DIRS=( .github cmd integrations internal schemas skill space-template testkit seeds feedback web ui releasenotes )
PUBLIC_VALIDATOR_FILES=(
  scripts/check-dashboard-cards.sh
  scripts/check-dashboard-derivation.sh
  scripts/check-lane-declarations.sh
  # ci-changes.sh is PUBLIC because `.github/workflows/ci.yml` calls it, and a
  # workflow that is published without the script it invokes is exactly the
  # v0.19.9 defect this list exists to prevent (see publish-to-public.sh's own
  # header): the private tree stayed green while the candidate refused.
  scripts/ci-changes.sh
  scripts/ci-skill-drift.sh
  scripts/ci-usage-report.sh
  scripts/check-runner-economics.sh
  scripts/feedback-intake-policy.sh
  scripts/lib/lane-ungated.txt
  scripts/lib/job-timeouts.tsv
  scripts/lib/ci-usage-window.json
  scripts/lib/gate-lib.sh
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
  scripts/tests/check_human_gates_test.sh
  scripts/tests/check_loop_reachability_test.sh
  scripts/tests/check_loop_coverage_test.sh
  scripts/tests/classify_guard_test.sh
)
ALLOW_FILES=( .gitignore .golangci.yml .goreleaser.yaml .gitleaks.toml .govulncheck-allow.txt Makefile SECURITY.md README.md LICENSE NOTICE go.mod go.sum cc-coverage.yaml scripts/install.sh scripts/dev-install.sh scripts/e2e-authoring-smoke.sh scripts/classify-guard.sh scripts/release-preflight.sh scripts/check-release-notes-freshness.sh scripts/check-roadmap-release-decisions.sh scripts/check-provider-tier-deferral.sh scripts/bump-space-template.sh scripts/check-gosec-scope.sh scripts/check-readme.sh scripts/dashboard-template-drift.sh scripts/check-view-vocabulary.sh scripts/check-pendency-uniqueness.sh scripts/check-loop-coverage.sh scripts/check-human-gates.sh scripts/check-loop-reachability.sh scripts/check-prose-roster.sh scripts/check-prose-coverage.sh scripts/check-operational-confidence.sh scripts/check-error-codes.sh scripts/verify.sh scripts/build-release-cohort.sh "${PUBLIC_VALIDATOR_FILES[@]}" )
DENY_DIRS=( .agents .claude .codex .mate .sporo )   # scripts/ handled below (install.sh + e2e-authoring-smoke.sh are the public exceptions)
DENY_FILES=( AGENTS.md CLAUDE.md )
PENDING_DIRS=( docs )   # deferred to P6 — tracked today, tolerated by check 1, classified by check 2, exempt from check 3.
# PRIVATE_ONLY_FILES: tracked HERE, deliberately stripped at publish. Same
# shape as PENDING_DIRS (tracked, not published, so not required to be
# gitignored) but permanent rather than deferred — these live inside an
# ALLOW_DIRS tree, so without this list they would classify as "public" and the
# guard would be lying about the boundary. Must stay in sync with the STRIP set
# in docs/runbooks/publish-to-public.sh.
PRIVATE_ONLY_FILES=( .github/dependabot.yml scripts/check-feature-lint.sh scripts/check-feedback-corpus.sh scripts/check-notify-secrets.sh scripts/check-notify-workflow.sh scripts/check-skill-citations.sh scripts/check-spec-verify-refs.sh scripts/check-dashboard-props.sh scripts/check-card-content.sh )
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
  # `|| true` is load-bearing, and it was missing until 2026-08-18. `grep`
  # exits 1 when it matches nothing; under `set -euo pipefail` that failure
  # propagates out of the command substitution and kills the script BEFORE the
  # refusal below can print. So a publisher whose STRIP block this cannot read
  # produced a bare non-zero exit with no message at all — fail-closed, but
  # mute, which is the shape that gets misdiagnosed as an unrelated crash. This
  # gate's own header promises "the message prints the FIX, not the symptom".
  # Found by writing the teeth (scripts/tests/classify_guard_test.sh); the
  # branch had been unreachable dead code since check 4 was written.
  strip_paths="$(awk '/^STRIP=\(/{inside=1} inside{print} inside&&/\)/{exit}' "$PUBLISHER" |
    grep -oE -- '--path [^ )]+' | sed 's/^--path //' || true)"
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

# ── 5. A PUBLIC gate must not `source` a file that is not itself public. ──────
#
# Check 4 above catches a public file the publisher strips BY NAME. It cannot
# see the shape that actually shipped: a public script whose DEPENDENCY is
# private. On 2026-08-12, seven tracked gates in REPO_GATES sourced
# `scripts/lib/gate-lib.sh`, which STRIP deleted as part of `scripts/lib/`. Each
# of them dies in the filtered candidate with "No such file or directory" and
# then "GATE_ROOT: unbound variable", and `make check` refuses on the first —
# while the private tree is green by construction, because the file is right
# there.
#
# It is the same defect check 4 was written for after the 2026-08-06
# lane-ungated.txt incident ("it cost a full release cycle to find"), one edge
# further out in the graph: that time the missing thing WAS the public file,
# this time it was what the public file reads. Seven gates instead of one, and
# four of them shipped during P13.
#
# The extractor is deliberately literal — a `source "$(dirname ...)/lib/x.sh"`
# line resolves to `scripts/lib/x.sh` and nothing cleverer. A sourced path this
# cannot resolve is NOT silently passed: it is flagged, the same refuse-loudly
# rule the lane declaration parser uses for a construct it cannot read.
for public_file in "${ALLOW_FILES[@]}"; do
  case "$public_file" in scripts/*.sh) ;; *) continue ;; esac
  [ -f "$public_file" ] || continue
  while IFS= read -r line; do
    # Only the self-locating form this repo actually writes:
    #   source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"
    dep="$(printf '%s' "$line" |
      sed -n 's|.*\$(dirname "\${BASH_SOURCE\[0\]}")/\([^"]*\)".*|\1|p')"
    if [ -z "$dep" ]; then
      # A sourced path this cannot resolve is flagged, never passed silently —
      # the same refuse-loudly rule the lane parser applies to a construct it
      # cannot read. If a new form appears, teach the extractor; do not widen
      # the skip.
      flag "$public_file has a \`source\` line this check cannot resolve to a literal path, so the boundary cannot be vouched for: $line"
      continue
    fi
    dep="scripts/$dep"
    in_list "$dep" "${ALLOW_FILES[@]}" ||
      flag "$public_file sources $dep, which is NOT classified PUBLIC — the gate ships without the file it reads and dies on the candidate's first \`make check\`"
  done < <(grep -E '^[[:space:]]*(source|\.)[[:space:]]' "$public_file")
done

if [ "$fail" -ne 0 ]; then
  printf '\nclassify-guard: \033[31mFAIL\033[0m — public/private boundary violated (fixes above).\n' >&2
  exit 1
fi
printf 'classify-guard: \033[32mOK\033[0m — every path classified, no private path tracked (docs/ pending-untrack deferred to P6).\n'
