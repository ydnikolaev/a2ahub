#!/usr/bin/env bash
# ci-changes.sh — resolve the changed-file set ONCE per run, and answer the
# questions CI gates on. agent-ops-2026-07 P13 K1 (spec 13 §3.2, rung 0, AC16).
#
# WHY THIS IS A SCRIPT AND NOT A `${{ }}` EXPRESSION.
#
# Spec 13 §5 rung 0 states the rule this file exists to satisfy: **no `${{ }}`
# expression may contain a decision, only a reference to one.** A workflow
# expression cannot be run at a terminal, cannot be unit-tested, and is not
# seen by actionlint beyond its syntax — measured on 2026-08-12, actionlint
# said NOTHING about the job that had no path condition. So the question
# "does this diff touch the macOS companion" is a shell function with its own
# teeth, and the YAML carries only `if: needs.changes.outputs.macos == 'true'`.
#
# WHY NOT A THIRD-PARTY `changed-files` ACTION. Spec 13 §3.2: every action in
# this tree is SHA-pinned, and a new dependency in the path that decides WHAT
# RUNS is not worth the seconds it saves. This asks the GitHub API through
# `gh`, which the workflows already use.
#
# WHY IT ALSO EMITS `files`. §3.2 again: this is `make lane`'s own
# computation. The set is resolved once and feeds BOTH the macOS gate and
# `LANE_FILES=`, so the terminal and the runner answer "given this diff, what
# runs" from one derivation rather than two. K4 is the consumer of the second
# half; K1 ships the producer.
#
# THE FALLBACK DIRECTION IS DELIBERATE. When the changed set cannot be
# resolved — a force-push whose `before` SHA is gone, a new branch whose
# `before` is all-zeros, an API read that fails — this reports
# `resolved=false`, and the caller must then buy EVERYTHING (the ceiling, and
# the macOS job). Unresolvable means "I do not know what changed", and the
# only safe answer to that is the expensive one. `scripts/verify.sh`'s
# `--require-nonempty` closed the mirror-image hole on the lane side: a lane
# that would run zero gates now refuses rather than reporting green.
#
# THIS FILE DELIBERATELY CARRIES NO `lane-inputs:` BLOCK, and the reason is
# worth stating so nobody adds one back. A declaration must name the corpus
# phase whose Makefile recipe invokes the script directly, and this script has
# no gate phase of its own — it is a reporter, not a verdict. Its only
# invocation is `--teeth` inside `_harness-check`, whose recipe maps only its
# FIRST script call (internal/lane/makefile.go:156-180, and the comment there
# explains what widening that map already cost). A declaration here therefore
# refuses to load the whole tree, which is exactly what happened when this
# file first carried one.
#
# It is covered anyway, by the right thing: `harness-check` declares
# `scripts/**/*.sh`, so editing this script selects the phase that runs its
# teeth. The workflow side is covered by `workflow-lint`'s
# `.github/workflows/**`.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

# The one path predicate this file owns. `integrations/macos-notifier/` is the
# whole of what `CI / macos-notifier` compiles and tests — spec 13 F2 measured
# ZERO commits touching it in a twelve-day window against 689 commits total,
# while the job billed 2650 equivalent minutes proving nothing that had
# changed.
MACOS_PREFIX="integrations/macos-notifier/"

# The workflow file itself is in the set on purpose: a change to how the
# macOS job is built or gated must be able to exercise it.
MACOS_WORKFLOW=".github/workflows/ci.yml"

macos_relevant() {
  local f
  for f in "$@"; do
    case "$f" in
      "$MACOS_PREFIX"*) return 0 ;;
      "$MACOS_WORKFLOW") return 0 ;;
    esac
  done
  return 1
}

# resolve_changed_files prints one changed path per line, or fails (non-zero)
# when the set cannot be determined. It never guesses: an empty set from a
# genuinely empty diff is a SUCCESS printing nothing, which is a different
# answer from "I could not tell", and the caller distinguishes them.
resolve_changed_files() {
  # An explicit set always wins. This is the seam --teeth drives, and it is
  # also how a caller at a terminal asks the same question the runner asks.
  if [ -n "${CI_CHANGES_FILES:-}" ]; then
    printf '%s\n' $CI_CHANGES_FILES
    return 0
  fi

  local repo="${CI_REPOSITORY:-${GITHUB_REPOSITORY:-}}"
  local event="${CI_EVENT_NAME:-${GITHUB_EVENT_NAME:-}}"
  [ -n "$repo" ] || return 1

  case "$event" in
    pull_request | pull_request_target)
      local pr="${CI_PR_NUMBER:-}"
      [ -n "$pr" ] || return 1
      gh api "repos/$repo/pulls/$pr/files" --paginate --jq '.[].filename' 2>/dev/null || return 1
      ;;
    push)
      local before="${CI_BEFORE_SHA:-}" after="${CI_AFTER_SHA:-}"
      [ -n "$before" ] && [ -n "$after" ] || return 1
      # A new branch reports an all-zero `before`, and a force-push can name a
      # SHA the remote no longer has. Both are "I do not know what changed".
      case "$before" in *[!0]*) ;; *) return 1 ;; esac
      gh api "repos/$repo/compare/$before...$after" --jq '.files[].filename' 2>/dev/null || return 1
      ;;
    *)
      # schedule, workflow_dispatch, and anything else: there is no diff to
      # speak of. Not an error, but not a resolved set either.
      return 1
      ;;
  esac
}

emit() {
  local key="$1" value="$2"
  if [ -n "${GITHUB_OUTPUT:-}" ]; then
    printf '%s=%s\n' "$key" "$value" >> "$GITHUB_OUTPUT"
  fi
  printf '%s=%s\n' "$key" "$value"
}

run_report() {
  local files resolved macos reason

  if files="$(resolve_changed_files)"; then
    resolved=true
  else
    resolved=false
    files=""
  fi

  # shellcheck disable=SC2086 # word-splitting the newline-separated set is the intent
  local -a set=()
  if [ -n "$files" ]; then
    while IFS= read -r line; do
      [ -n "$line" ] && set+=("$line")
    done <<< "$files"
  fi

  if [ "$resolved" != true ]; then
    # The unresolvable case, and the WEEKLY SAFETY NET, land in the same
    # branch by construction. Spec 13 AC2 asks for one weekly schedule in the
    # PUBLIC repository only, as the guard against a path filter that
    # silently never fires — two repositories would re-add ~104 macOS runs a
    # year for one repository's worth of information.
    local repo="${CI_REPOSITORY:-${GITHUB_REPOSITORY:-}}"
    local event="${CI_EVENT_NAME:-${GITHUB_EVENT_NAME:-}}"
    if [ "$event" = schedule ] && [ "$repo" != "ydnikolaev/a2ahub" ]; then
      # A scheduled run in any repository that is not the public one buys
      # nothing: the safety net exists once.
      macos=false
      reason="scheduled run outside the public repository — the weekly net runs in one repo only (AC2)"
    else
      macos=true
      reason="changed set unresolved for event '${event:-<none>}' — buying everything is the only safe answer"
    fi
  elif macos_relevant "${set[@]+"${set[@]}"}"; then
    macos=true
    reason="the diff touches $MACOS_PREFIX or $MACOS_WORKFLOW"
  else
    macos=false
    reason="${#set[@]} changed path(s), none under $MACOS_PREFIX"
  fi

  emit resolved "$resolved"
  emit macos "$macos"
  emit files "${set[*]+${set[*]}}"
  printf 'ci-changes: macos=%s (%s)\n' "$macos" "$reason" >&2
}

# --- --teeth ---------------------------------------------------------------
#
# Every case below was a live possibility on 2026-08-12, and the two that
# matter most are the ones no linter can see: a docs-only diff must NOT buy
# the ×10 runner, and an unresolvable diff MUST.

teeth_fail=0

expect() {
  local label="$1" want_macos="$2" want_resolved="$3"
  shift 3
  local out
  out="$(env "$@" bash "$ROOT/scripts/ci-changes.sh" 2>/dev/null)"
  local got_macos got_resolved
  got_macos="$(sed -n 's/^macos=//p' <<< "$out")"
  got_resolved="$(sed -n 's/^resolved=//p' <<< "$out")"
  if [ "$got_macos" != "$want_macos" ] || [ "$got_resolved" != "$want_resolved" ]; then
    echo "ci-changes --teeth: FAIL — $label: got macos=$got_macos resolved=$got_resolved, want macos=$want_macos resolved=$want_resolved" >&2
    teeth_fail=1
    return
  fi
  echo "ci-changes --teeth: $label — macos=$got_macos resolved=$got_resolved"
}

run_teeth() {
  # (a) The founding measurement: 494 of 689 commits touched only docs.
  expect "docs-only diff does not buy the macOS runner" false true \
    CI_CHANGES_FILES="docs/backlog.md README.md" GITHUB_OUTPUT=

  # (b) The single-YAML feedback record that cost eleven jobs (fb-20260812-755a23).
  expect "a one-file feedback record does not buy it either" false true \
    CI_CHANGES_FILES="feedback/inbox/fb-20260812-755a23.yaml" GITHUB_OUTPUT=

  # (c) A Go change is NOT macOS-relevant — the companion is Swift. This is
  # the case a coarse "any source change" filter would get wrong.
  expect "a Go change does not buy the macOS runner" false true \
    CI_CHANGES_FILES="internal/fold/fold.go" GITHUB_OUTPUT=

  # (d) The true positive. Without this the gate is a way to never run.
  expect "a Swift companion change DOES buy it" true true \
    CI_CHANGES_FILES="integrations/macos-notifier/scripts/test.sh" GITHUB_OUTPUT=

  # (e) Changing the workflow that builds it must be able to exercise it.
  expect "editing ci.yml itself buys it" true true \
    CI_CHANGES_FILES=".github/workflows/ci.yml" GITHUB_OUTPUT=

  # (f) Mixed diff: one relevant path among many is enough.
  expect "one relevant path among many is enough" true true \
    CI_CHANGES_FILES="docs/backlog.md integrations/macos-notifier/Package.swift README.md" GITHUB_OUTPUT=

  # (g) THE FAIL-SAFE. An unresolvable set must buy everything, never nothing.
  # A filter that silently answers "false" when it cannot tell is the exact
  # shape of a gate that stops proving things while looking green.
  expect "an unresolvable set buys everything" true false \
    CI_CHANGES_FILES= CI_EVENT_NAME=push CI_REPOSITORY=ydnikolaev/a2ahub CI_BEFORE_SHA=0000000000000000000000000000000000000000 CI_AFTER_SHA=deadbeef GITHUB_OUTPUT=

  # (h) The weekly net, in the public repository.
  expect "the weekly schedule fires the net in the public repo" true false \
    CI_CHANGES_FILES= CI_EVENT_NAME=schedule CI_REPOSITORY=ydnikolaev/a2ahub GITHUB_OUTPUT=

  # (i) …and only there. AC2 asks for ONE weekly run, not one per repository.
  expect "the weekly schedule stays out of every other repository" false false \
    CI_CHANGES_FILES= CI_EVENT_NAME=schedule CI_REPOSITORY=ydnikolaev/a2ahub-private GITHUB_OUTPUT=

  if [ "$teeth_fail" -ne 0 ]; then
    echo "ci-changes --teeth: FAIL" >&2
    exit 1
  fi
  echo "ci-changes --teeth: 9 case(s) green."
}

case "${1:-}" in
  --teeth) run_teeth ;;
  "") run_report ;;
  *) echo "usage: ci-changes.sh [--teeth]" >&2; exit 2 ;;
esac
