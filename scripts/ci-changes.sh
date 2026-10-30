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

# Dashboard-coherence P16. One shared path predicate feeds both the ordinary
# Node unit job and the rare browser-truth workflow. Occasion policy is folded
# separately below; no workflow YAML owns a competing `paths` decision.
WEB_PREFIX='web/'
DASHBOARD_TEMPLATE='internal/html/template.html'

# The nightly ceiling's cron, stated ONCE. `ci.yml` declares two schedules and
# `github.event.schedule` is the only thing that tells a scheduled run which
# one fired it, so this literal and the workflow's must agree. They are pinned
# together by --teeth case (k) below, which reads the workflow.
NIGHTLY_CRON='23 2 * * *'

# AC11b. The inbound corpus every shipped binary writes to. A pull request
# whose diff is entirely inside it cannot change a Go verdict, a Swift build,
# or a schema — the lane derivation says so out loud: `LANE_FILES=` one inbox
# record selects `feedback-corpus` plus the four universal phases, and NOT
# `logic-e2e`, `go-test` or the conformance matrix.
FEEDBACK_PREFIX='feedback/'

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

web_relevant() {
  local f
  for f in "$@"; do
    case "$f" in
      "$WEB_PREFIX"* | "$DASHBOARD_TEMPLATE") return 0 ;;
    esac
  done
  return 1
}

resolve_web() {
  local resolved="$1"
  shift
  local event="${CI_EVENT_NAME:-${GITHUB_EVENT_NAME:-}}"
  if [ "$resolved" = true ]; then
    if web_relevant "$@"; then echo true; else echo false; fi
    return
  fi
  case "$event" in
    push | pull_request | pull_request_target) echo true ;;
    *) echo false ;;
  esac
}

resolve_rare_web() {
  local resolved="$1"
  shift
  local event="${CI_EVENT_NAME:-${GITHUB_EVENT_NAME:-}}"
  local ref="${CI_REF:-${GITHUB_REF:-}}"
  if [ "$event" = workflow_dispatch ]; then echo true; return; fi
  if [ "$event" != push ] || [ "$ref" != refs/heads/main ]; then
    echo false
    return
  fi
  if [ "$resolved" != true ]; then echo true; return; fi
  if web_relevant "$@"; then echo true; else echo false; fi
}

# feedback_only is true when EVERY changed path is inside the inbound corpus,
# and false for an empty set. The "every" is the direction that matters: one
# path outside `feedback/` is enough to make this an ordinary pull request
# again, so a record smuggled alongside a source edit buys the full ceiling.
# The empty case is false because an empty resolved set is indistinguishable
# from a question nobody asked.
feedback_only() {
  [ "$#" -gt 0 ] || return 1
  local f
  for f in "$@"; do
    case "$f" in
      "$FEEDBACK_PREFIX"*) ;;
      *) return 1 ;;
    esac
  done
  return 0
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
  emit web "$(resolve_web "$resolved" "${set[@]+"${set[@]}"}")"
  emit rare_web "$(resolve_rare_web "$resolved" "${set[@]+"${set[@]}"}")"
  emit files "${set[*]+${set[*]}}"
  emit tier "$(resolve_tier "$resolved" "${set[@]+"${set[@]}"}")"
  printf 'ci-changes: macos=%s (%s)\n' "$macos" "$reason" >&2
}

# THE CADENCE, decided here so every workflow `if:` stays a single comparison
# against one output (AC16). `.claude/rules/check-convention.md` has carried
# this table since P12 and CI has never obeyed it: **the derived lane is the
# COMMIT gate, the ceiling is the SHIP gate.** P13 §3.3 is the decision to
# make the runner obey it too.
#
#   ceiling  — `make check`, the ~15-minute ship gate
#   lane     — `make lane-run-strict`, priced off the diff
#   none     — nothing to verify (the weekly macOS net; see AC2)
#
# WHAT A PULL REQUEST BUYS, and why it stopped being one answer (AC11b).
#
# It used to be "the ceiling, always", and that was a FENCE rather than a
# measurement: spec 13 §4.5 (= spec 14 §5) refused to remove validating proof
# from a branch that accepts external writes while public `main`'s sole
# required context returned green for any non-feedback pull request without
# validating it. `scripts/feedback-intake-policy.sh` closed that on 2026-08-13
# — an ordinary pull request to `main` or `feedback-hub` in the public
# repository is now REFUSED — so the fence has been replaced rather than
# removed: a `feedback/**`-only diff buys the LANE, and a diff touching
# anything else still buys the ceiling. Both directions are pinned below.
#
# The saving is the largest single bucket left on the public side: 426
# equivalent minutes over 100 runs in the measured window, against a lane that
# selects `feedback-corpus` and four universal phases — seconds, not minutes.
#
# WHAT A RELEASE BUYS (R-1 / D-041, release-speed P6): the local
# `make release-check` is the one judge and runs BEFORE the push, so public CI
# is information after it. A candidate ref triggers no CI at all; the push of
# the release SHA to `main` resolves the lane and the tag push resolves the
# ceiling, once per release. There is no proof lookup: no API call here can
# turn a run into `none`.
resolve_tier() {
  local resolved="$1"
  shift 2>/dev/null || shift $#
  local -a set=("$@")
  local event="${CI_EVENT_NAME:-${GITHUB_EVENT_NAME:-}}"
  local ref="${CI_REF:-${GITHUB_REF:-}}"
  local cron="${CI_SCHEDULE:-}"

  case "$event" in
    schedule)
      # Two crons share this workflow and they buy different things. The
      # nightly one is the ceiling's new home now that push no longer runs it
      # (§3.3 accepts up to 24h of latency on that verdict, and the acceptance
      # is the operator's). The weekly one is the macOS path-gate's safety net
      # and must buy NOTHING else, or the net costs a ceiling a week.
      if [ "$cron" = "$NIGHTLY_CRON" ]; then echo ceiling; else echo none; fi
      return
      ;;
    workflow_dispatch)
      # A dispatch names its own occasion (ci.yml's `inputs.tier`). The
      # DEFAULT is the ceiling — an operator reaching for a manual run
      # usually wants a tree proven — but §5 rung 5's per-wave verification
      # explicitly wants the derived lane instead, at single-digit minutes,
      # so that it can be run nine times inside the 200-minute verification
      # allowance without buying nine ceilings. An unrecognised value falls
      # to the expensive answer rather than the cheap one.
      case "${CI_DISPATCH_TIER:-}" in
        lane) echo lane ;;
        *) echo ceiling ;;
      esac
      return
      ;;
    pull_request | pull_request_target)
      # An UNRESOLVED pull request falls to the ceiling with everything else:
      # "I could not read the diff" is not "the diff is only feedback".
      #
      # The `resolved` clause is belt-and-braces TODAY and is kept knowingly:
      # `run_report` blanks `files` whenever the set is unresolved, so
      # `feedback_only`'s empty-set rule already refuses that case, and
      # removing this clause reds no --teeth case. It is stated because the
      # two facts that make it redundant live in a different function, and a
      # later partial-read path would make it load-bearing again.
      if [ "$resolved" = true ] && feedback_only "${set[@]+"${set[@]}"}"; then
        echo lane
      else
        echo ceiling
      fi
      return
      ;;
  esac

  case "$ref" in
    refs/tags/*) echo ceiling; return ;;
  esac

  # An ordinary push. The lane is the gate — unless we could not work out what
  # changed, in which case the lane has nothing to price and the honest answer
  # is the expensive one.
  if [ "$resolved" = true ]; then echo lane; else echo ceiling; fi
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

expect_tier() {
  local label="$1" want="$2"
  shift 2
  local out got
  out="$(env "$@" bash "$ROOT/scripts/ci-changes.sh" 2>/dev/null)"
  got="$(sed -n 's/^tier=//p' <<< "$out")"
  if [ "$got" != "$want" ]; then
    echo "ci-changes --teeth: FAIL — $label: got tier=$got, want tier=$want" >&2
    teeth_fail=1
    return
  fi
  echo "ci-changes --teeth: $label — tier=$got"
}

expect_web_outputs() {
  local label="$1" want_web="$2" want_rare="$3"
  shift 3
  local out got_web got_rare
  out="$(env "$@" bash "$ROOT/scripts/ci-changes.sh" 2>/dev/null)"
  got_web="$(sed -n 's/^web=//p' <<< "$out")"
  got_rare="$(sed -n 's/^rare_web=//p' <<< "$out")"
  if [ "$got_web" != "$want_web" ] || [ "$got_rare" != "$want_rare" ]; then
    echo "ci-changes --teeth: FAIL — $label: got web=$got_web rare_web=$got_rare, want web=$want_web rare_web=$want_rare" >&2
    teeth_fail=1
    return
  fi
  echo "ci-changes --teeth: $label — web=$got_web rare_web=$got_rare"
}

run_teeth() {
  local ci_workflow="$ROOT/.github/workflows/ci.yml"
  local rare_workflow="$ROOT/.github/workflows/dashboard-rare-truth.yml"
  if [ ! -f "$rare_workflow" ] ||
     ! grep -q '^  push:$' "$rare_workflow" ||
     ! grep -q '^    branches: \[main\]$' "$rare_workflow" ||
     ! grep -q '^  workflow_dispatch:$' "$rare_workflow" ||
     grep -Eq '^  schedule:|^[[:space:]]+paths:' "$rare_workflow" ||
     ! grep -Fq 'rare_web: ${{ steps.resolve.outputs.rare_web }}' "$rare_workflow" ||
     ! grep -Fq 'needs: changes' "$rare_workflow" ||
     [ "$(grep -Fc "if: needs.changes.outputs.rare_web == 'true'" "$rare_workflow")" -ne 1 ] ||
     grep -Fq 'web/**' "$rare_workflow" ||
     grep -Fq 'internal/html/template.html' "$rare_workflow"; then
    echo "ci-changes --teeth: FAIL — rare workflow must use main+dispatch, no paths/schedule, exported rare_web and one output-only browser condition" >&2
    teeth_fail=1
  else
    echo "ci-changes --teeth: rare workflow has one resolver-owned path authority and one rare_web browser condition"
  fi
  # WHAT THIS GUARDS is the invariant stated at the top of this file — "no
  # workflow YAML owns a competing `paths` decision" — not a headcount.
  #
  # It counted the comparisons and demanded EXACTLY ONE, which was accurate
  # while exactly one job consumed the signal and became wrong the moment a
  # second legitimately did: on 2026-08-26 the `lane` job gained a Node setup
  # gated on the same output, because the lane can derive `web-quality` and had
  # no Node to run it with. Two consumers of one exported output do not create a
  # competing authority; a `paths:` filter in this workflow would.
  #
  # So the proxy is loosened to "at least one" and the property it stood for is
  # asserted directly and for the first time: this workflow must own no web path
  # decision of its own. Comments are stripped before that read — the lane job's
  # own explanation names the globs it does not own.
  ci_workflow_code="$(sed 's/#.*$//' "$ci_workflow")"
  if ! grep -Fq 'web: ${{ steps.resolve.outputs.web }}' "$ci_workflow" ||
     ! grep -Fq 'needs: changes' "$ci_workflow" ||
     [ "$(grep -Fc "if: needs.changes.outputs.web == 'true'" "$ci_workflow")" -lt 1 ] ||
     grep -Fq "${WEB_PREFIX}**" <<<"$ci_workflow_code" ||
     grep -Fq "$DASHBOARD_TEMPLATE" <<<"$ci_workflow_code"; then
    echo "ci-changes --teeth: FAIL — the ordinary CI workflow must consume the resolver-owned web output and own no web path decision of its own" >&2
    teeth_fail=1
  else
    echo "ci-changes --teeth: ordinary CI web job consumes only the resolver-owned web output"
  fi

  # P16: the shared web predicate and its two distinct occasion folds. These
  # answers depend on the diff and the occasion alone.
  expect_web_outputs "a main web change buys unit and rare truth" true true \
    CI_CHANGES_FILES="web/design-source/Overview.dc.html" CI_EVENT_NAME=push CI_REF=refs/heads/main GITHUB_OUTPUT=
  expect_web_outputs "a generated template change buys unit and rare truth" true true \
    CI_CHANGES_FILES="internal/html/template.html" CI_EVENT_NAME=push CI_REF=refs/heads/main GITHUB_OUTPUT=
  expect_web_outputs "a resolved main Go-only change buys neither web job" false false \
    CI_CHANGES_FILES="internal/fold/fold.go" CI_EVENT_NAME=push CI_REF=refs/heads/main GITHUB_OUTPUT=
  expect_web_outputs "an unresolved main push fails expensive" true true \
    CI_CHANGES_FILES= CI_EVENT_NAME=push CI_REF=refs/heads/main CI_REPOSITORY=ydnikolaev/a2ahub \
    CI_BEFORE_SHA=0000000000000000000000000000000000000000 CI_AFTER_SHA=deadbeef GITHUB_OUTPUT=
  expect_web_outputs "manual dispatch buys rare truth but no diff-scoped unit job" false true \
    CI_CHANGES_FILES= CI_EVENT_NAME=workflow_dispatch CI_REF=refs/heads/main GITHUB_OUTPUT=
  expect_web_outputs "a schedule buys neither dashboard web job" false false \
    CI_CHANGES_FILES= CI_EVENT_NAME=schedule CI_REF=refs/heads/main GITHUB_OUTPUT=
  expect_web_outputs "a resolved main push touching web/design-source buys both web jobs" true true \
    CI_CHANGES_FILES="web/design-source/Overview.dc.html" CI_EVENT_NAME=push CI_REF=refs/heads/main \
    CI_REPOSITORY=ydnikolaev/a2ahub CI_AFTER_SHA=deadbeef GITHUB_OUTPUT=
  expect_web_outputs "a web pull request buys unit but not main-only rare truth" true false \
    CI_CHANGES_FILES="web/tests/ui-contract.test.mjs" CI_EVENT_NAME=pull_request CI_REF=refs/pull/27/merge GITHUB_OUTPUT=
  expect_web_outputs "a web push to a tag ref cannot buy rare truth" true false \
    CI_CHANGES_FILES="web/tests/ui-contract.test.mjs" CI_EVENT_NAME=push CI_REF=refs/tags/v9.9.9 GITHUB_OUTPUT=

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

  # --- the cadence (AC8, AC9) ------------------------------------------------

  # (j) An ordinary push runs the LANE, not the ceiling. This is the whole of
  # AC8: `check` ran on every push, on every branch, in two repositories,
  # while the derivation that prices it existed and was invoked by hand.
  expect_tier "an ordinary push runs the derived lane" lane \
    CI_CHANGES_FILES="docs/backlog.md" CI_EVENT_NAME=push CI_REF=refs/heads/main GITHUB_OUTPUT=

  # (k) A release cut costs one lane and one ceiling (R-1 / D-041). The release
  # SHA pushed to `main` resolves the lane; the tag push of the same SHA
  # resolves the ceiling. No candidate-ref run exists, and no proof lookup can
  # silence either.
  local release_env=(CI_REPOSITORY=ydnikolaev/a2ahub
    CI_AFTER_SHA=f2a0b54e57cad83528fc675c7cd92dace28911fa
    CI_CHANGES_FILES="internal/fold/fold.go" GITHUB_OUTPUT=)
  expect_tier "the main push of a release SHA resolves the lane" lane \
    CI_EVENT_NAME=push CI_REF=refs/heads/main "${release_env[@]}"
  expect_tier "the tag push of the same SHA resolves the ceiling" ceiling \
    CI_EVENT_NAME=push CI_REF=refs/tags/v9.9.9 "${release_env[@]}"

  # (l) A TAG is the ship boundary and buys the ceiling.
  expect_tier "a tag buys the ceiling" ceiling \
    CI_CHANGES_FILES="docs/backlog.md" CI_EVENT_NAME=push CI_REF=refs/tags/v9.9.9 GITHUB_OUTPUT=

  # (m) AC11b, THE SAVING. This case used to assert the opposite — a fence, so
  # that nobody could take the saving before public `main` carried a merge gate
  # that inspected anything. The precondition was satisfied on 2026-08-13 by
  # scripts/feedback-intake-policy.sh, so the fence is INVERTED rather than
  # deleted: what was guarded is now the thing with teeth.
  expect_tier "a feedback-only pull request buys the lane, not the ceiling" lane \
    CI_CHANGES_FILES="feedback/inbox/fb-20260812-755a23.yaml" CI_EVENT_NAME=pull_request CI_REF=refs/pull/27/merge GITHUB_OUTPUT=

  # (m2) …and the fence is what remains of it. One path outside `feedback/` is
  # enough: a record smuggled alongside a source edit is an ordinary pull
  # request and buys the full ceiling. Without this case, "feedback-only"
  # degrades to "mentions feedback" and the saving becomes a hole.
  expect_tier "a pull request touching anything else still buys the ceiling" ceiling \
    CI_CHANGES_FILES="feedback/inbox/fb-20260812-755a23.yaml internal/fold/fold.go" \
    CI_EVENT_NAME=pull_request CI_REF=refs/pull/27/merge GITHUB_OUTPUT=

  # (m3) An UNRESOLVED pull request is not a feedback pull request. The
  # fail-safe direction has to hold on this path too, or a diff nobody could
  # read would buy the cheapest tier.
  expect_tier "an unresolvable pull request falls to the ceiling" ceiling \
    CI_CHANGES_FILES= CI_EVENT_NAME=pull_request CI_REPOSITORY=ydnikolaev/a2ahub GITHUB_OUTPUT=

  # (n) workflow_dispatch defaults to the ceiling…
  expect_tier "workflow_dispatch defaults to the ceiling" ceiling \
    CI_EVENT_NAME=workflow_dispatch CI_REF=refs/heads/main GITHUB_OUTPUT=

  # (n2) …and can be asked for the LANE instead, which is what makes §5 rung
  # 5's "one dispatch per wave, of the derived lane, never the ceiling"
  # executable at all rather than a step nobody can perform.
  expect_tier "workflow_dispatch can buy the lane on request" lane \
    CI_EVENT_NAME=workflow_dispatch CI_REF=refs/heads/main CI_DISPATCH_TIER=lane CI_CHANGES_FILES="docs/backlog.md" GITHUB_OUTPUT=

  # (n3) An unrecognised input falls to the EXPENSIVE answer. A typo must not
  # silently buy less verification than the operator asked for.
  expect_tier "an unrecognised dispatch tier falls to the ceiling" ceiling \
    CI_EVENT_NAME=workflow_dispatch CI_REF=refs/heads/main CI_DISPATCH_TIER=lanee GITHUB_OUTPUT=

  # (o) The NIGHTLY cron is the ceiling's new home.
  expect_tier "the nightly cron buys the ceiling" ceiling \
    CI_EVENT_NAME=schedule CI_SCHEDULE="$NIGHTLY_CRON" CI_REPOSITORY=ydnikolaev/a2ahub GITHUB_OUTPUT=

  # (p) The WEEKLY cron buys the macOS net and NOTHING else. Getting this
  # wrong costs a full ceiling every week for a job that takes 52 seconds.
  expect_tier "the weekly macOS net buys no tier at all" none \
    CI_EVENT_NAME=schedule CI_SCHEDULE='17 6 * * 1' CI_REPOSITORY=ydnikolaev/a2ahub GITHUB_OUTPUT=

  # (q) AC9's own case: an unresolvable push falls to the CEILING, never to
  # the lane. `make lane-run-strict` would refuse an empty input set — which
  # is correct and loud — but the honest tier for "I do not know what changed"
  # is the one that checks everything.
  expect_tier "an unresolvable push falls to the ceiling, not the lane" ceiling \
    CI_CHANGES_FILES= CI_EVENT_NAME=push CI_REF=refs/heads/main CI_REPOSITORY=ydnikolaev/a2ahub \
    CI_BEFORE_SHA=0000000000000000000000000000000000000000 CI_AFTER_SHA=deadbeef GITHUB_OUTPUT=

  # (r) The two crons live in TWO files — this script's literal and the
  # workflow's `on.schedule`. A rename in one is silent in the other, and the
  # failure it produces is a nightly ceiling that never runs while the run
  # list looks healthy. Pin them.
  local wf="$ROOT/.github/workflows/ci.yml"
  if [ -f "$wf" ]; then
    if ! grep -qF "cron: '$NIGHTLY_CRON'" "$wf"; then
      echo "ci-changes --teeth: FAIL — the nightly cron '$NIGHTLY_CRON' is not declared in ci.yml; a scheduled run would resolve to tier=none and the ceiling would silently never run" >&2
      teeth_fail=1
    else
      echo "ci-changes --teeth: the nightly cron matches ci.yml's own schedule"
    fi
  fi

  if [ "$teeth_fail" -ne 0 ]; then
    echo "ci-changes --teeth: FAIL" >&2
    exit 1
  fi
  echo "ci-changes --teeth: 29 case(s) green."
}

case "${1:-}" in
  --teeth) run_teeth ;;
  "") run_report ;;
  *) echo "usage: ci-changes.sh [--teeth]" >&2; exit 2 ;;
esac
