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

# AC11's lookup, and the three literals it turns on. A promotion pushes a SHA
# that a candidate ref has ALREADY proven at the ceiling, on a byte-identical
# tree, minutes earlier. That second execution can change no verdict.
CANDIDATE_PREFIX='a2a-candidate/'
CI_WORKFLOW_PATH='.github/workflows/ci.yml'
PROOF_BRANCH='refs/heads/main'

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

# resolve_candidate_proof prints `true` when the SHA being pushed to public
# `main` already carries a SUCCESSFUL CI run on an `a2a-candidate/**` ref, and
# `false` in every other circumstance including every failure to find out.
# Spec 13 AC11.
#
# THE "ONLY WHEN" IS THE WHOLE CRITERION, and it is a correction of an earlier
# draft rather than caution. §2.2 measured that eight of 23 head SHAs pushed to
# public `main` in the window have NO candidate run at all — seven are
# squash-merges of feedback pull requests, one is a direct admin push (that
# branch carries `enforce_admins: false`). A blanket "skip on main" would
# delete the only proof those commits ever get. So the answer is per-SHA, asked
# of the API, and the failure direction is to run everything.
#
# EVENT AND BRANCH ARE BOTH PART OF THE QUESTION. `event=push` excludes a
# `workflow_dispatch` on a candidate ref, which `resolve_tier` below is free to
# answer `lane` for — a dispatched lane run is not a ceiling and must never
# count as one. `refs/heads/main` excludes the candidate ref itself: re-pushing
# a SHA to a candidate branch must still buy the ceiling, or the one execution
# the filtered tree ever gets could skip itself.
#
# THE --teeth SEAM IS THE API ANSWER, NOT THE VERDICT. `CI_CANDIDATE_RUNS`
# substitutes the run COUNT the API would have returned, so the event and
# branch guards above it stay under test. Stubbing the verdict instead would
# have left the two conditions most likely to be got wrong — "only a push" and
# "only main" — proved by nothing.
resolve_candidate_proof() {
  local event="${CI_EVENT_NAME:-${GITHUB_EVENT_NAME:-}}"
  local ref="${CI_REF:-${GITHUB_REF:-}}"
  local repo="${CI_REPOSITORY:-${GITHUB_REPOSITORY:-}}"
  local sha="${CI_AFTER_SHA:-${GITHUB_SHA:-}}"

  if [ "$event" != push ] || [ "$ref" != "$PROOF_BRANCH" ]; then echo false; return; fi
  if [ -z "$repo" ] || [ -z "$sha" ]; then echo false; return; fi

  local n
  if [ -n "${CI_CANDIDATE_RUNS:-}" ]; then
    n="${CI_CANDIDATE_RUNS}"
  else
    n="$(gh api "repos/$repo/actions/runs?head_sha=$sha&status=success&event=push" \
      --jq "[.workflow_runs[] | select(.path == \"$CI_WORKFLOW_PATH\" and (.head_branch | startswith(\"$CANDIDATE_PREFIX\")))] | length" \
      2>/dev/null)" || { echo false; return; }
  fi
  case "$n" in
    '' | 0) echo false ;;
    *) echo true ;;
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
  local files resolved macos reason proven

  proven="$(resolve_candidate_proof)"

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

  if [ "$proven" = true ]; then
    # AC11, and note WHERE the answer is applied: here, so that every `if:` in
    # ci.yml stays the single comparison it already was (AC16). Adding
    # `&& outputs.proven != 'true'` to three conditions would have put the
    # decision back into `${{ }}`, which is the one thing rung 0 forbids.
    macos=false
    reason="the SHA already has a successful CI run on an ${CANDIDATE_PREFIX}** ref — the promotion cannot change its verdict (AC11)"
  elif [ "$resolved" != true ]; then
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
  emit proven "$proven"
  emit tier "$(resolve_tier "$resolved" "$proven" "${set[@]+"${set[@]}"}")"
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
# WHY A CANDIDATE REF STILL BUYS IT: spec 13 §2.1 measured that bucket as the
# one that must stay. It is the only execution the FILTERED tree ever gets,
# and it is what caught the `scripts/lib/` strip defect at v0.19.9 while every
# local gate was green.
resolve_tier() {
  local resolved="$1" proven="${2:-false}"
  shift 2 2>/dev/null || shift $#
  local -a set=("$@")
  local event="${CI_EVENT_NAME:-${GITHUB_EVENT_NAME:-}}"
  local ref="${CI_REF:-${GITHUB_REF:-}}"
  local cron="${CI_SCHEDULE:-}"

  # AC11. `resolve_candidate_proof` has already established that this is a push
  # to public `main` of a SHA a candidate ref proved at the ceiling; nothing
  # below can be a better answer than "that has been done".
  #
  # WHAT THIS DELIBERATELY DOES NOT COVER, because the API said so rather than
  # because it was overlooked: a candidate ref runs `CI` ONLY. Checked on
  # 2026-08-13 for a2a-candidate/f2a0b54e57ca — one run, `CI`, nothing else,
  # while the promoted SHA on `main` additionally ran CodeQL, gitleaks,
  # classify-guard and Pages. So there is no candidate proof for those four to
  # trust, and "skip them because the candidate passed" would DELETE four gates
  # rather than deduplicate them. They already run exactly once per promoted
  # SHA; moving them onto candidate refs would be net zero, and strictly worse
  # for a candidate that never promotes. Pages is a third thing again — it is
  # the DEPLOY, and a deploy the candidate never performed cannot be skipped
  # because the candidate succeeded.
  if [ "$proven" = true ]; then echo none; return; fi

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
    refs/heads/a2a-candidate/*) echo ceiling; return ;;
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

expect_proven() {
  local label="$1" want="$2"
  shift 2
  local out got
  out="$(env "$@" bash "$ROOT/scripts/ci-changes.sh" 2>/dev/null)"
  got="$(sed -n 's/^proven=//p' <<< "$out")"
  if [ "$got" != "$want" ]; then
    echo "ci-changes --teeth: FAIL — $label: got proven=$got, want proven=$want" >&2
    teeth_fail=1
    return
  fi
  echo "ci-changes --teeth: $label — proven=$got"
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

  # --- the cadence (AC8, AC9) ------------------------------------------------

  # (j) An ordinary push runs the LANE, not the ceiling. This is the whole of
  # AC8: `check` ran on every push, on every branch, in two repositories,
  # while the derivation that prices it existed and was invoked by hand.
  expect_tier "an ordinary push runs the derived lane" lane \
    CI_CHANGES_FILES="docs/backlog.md" CI_EVENT_NAME=push CI_REF=refs/heads/main GITHUB_OUTPUT=

  # (k) A CANDIDATE ref keeps the ceiling. Spec 13 §2.1: the only execution the
  # filtered tree ever gets, and what caught the scripts/lib/ strip defect at
  # v0.19.9 while every local gate was green. Losing this is the one loss in
  # this phase that would not be noticed until a public release was broken.
  expect_tier "a candidate ref still buys the ceiling" ceiling \
    CI_CHANGES_FILES="docs/backlog.md" CI_EVENT_NAME=push CI_REF=refs/heads/a2a-candidate/deadbeef GITHUB_OUTPUT=

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

  # --- the candidate proof (AC11) --------------------------------------------
  #
  # Every case below names a REAL SHA class from §2.2's window, because the
  # criterion's whole difficulty is that a push to `main` is not one thing.

  # (s) THE PROMOTE. f2a0b54e57ca: the candidate ref ran the ceiling at
  # 08:12, the promotion pushed the identical tree to `main` at 08:36, and the
  # second run could change no verdict. This is the case AC11 exists for.
  local promote_env=(CI_EVENT_NAME=push CI_REF=refs/heads/main
    CI_REPOSITORY=ydnikolaev/a2ahub CI_AFTER_SHA=f2a0b54e57cad83528fc675c7cd92dace28911fa
    CI_CANDIDATE_RUNS=1 CI_CHANGES_FILES="internal/fold/fold.go" GITHUB_OUTPUT=)
  expect_proven "a promote to main whose SHA a candidate already proved" true "${promote_env[@]}"
  expect_tier "…buys no tier at all" none "${promote_env[@]}"

  # (s2) …and the ×10 runner goes with it. Without this the promotion still
  # pays 10 equivalent minutes to rebuild a companion the candidate built.
  # `integrations/` is in the diff on purpose: the path predicate would say
  # `true`, and the proof must outrank it.
  expect "…and the macOS runner is not bought either" false true \
    CI_EVENT_NAME=push CI_REF=refs/heads/main CI_REPOSITORY=ydnikolaev/a2ahub \
    CI_AFTER_SHA=f2a0b54e57cad83528fc675c7cd92dace28911fa CI_CANDIDATE_RUNS=1 \
    CI_CHANGES_FILES="integrations/macos-notifier/Package.swift" GITHUB_OUTPUT=

  # (t) A FEEDBACK SQUASH-MERGE. Eight of 23 head SHAs in the window have NO
  # candidate run — seven auto-merged feedback pull requests and one direct
  # admin push. These are the commits for which the push-side run is the ONLY
  # proof, and the blanket "skip on main" form of AC11 would have deleted it.
  expect_tier "a main push with no candidate run keeps its lane" lane \
    CI_EVENT_NAME=push CI_REF=refs/heads/main CI_REPOSITORY=ydnikolaev/a2ahub \
    CI_AFTER_SHA=69b91e603baf CI_CANDIDATE_RUNS=0 \
    CI_CHANGES_FILES="feedback/inbox/fb-20260812-755a23.yaml" GITHUB_OUTPUT=

  # (u) A CANDIDATE REF MUST NOT PROOF-GATE ITSELF. Re-pushing a SHA to a
  # candidate branch while a successful candidate run for it exists would,
  # without the branch guard, skip the one execution the FILTERED tree ever
  # gets — the run that caught the scripts/lib/ strip defect at v0.19.9.
  expect_tier "a candidate ref is never silenced by its own proof" ceiling \
    CI_EVENT_NAME=push CI_REF=refs/heads/a2a-candidate/deadbeef CI_REPOSITORY=ydnikolaev/a2ahub \
    CI_AFTER_SHA=deadbeef CI_CANDIDATE_RUNS=1 CI_CHANGES_FILES="docs/backlog.md" GITHUB_OUTPUT=

  # (v) A DISPATCH IS NOT A PUSH. An operator asking for a manual run has asked
  # for an execution, not for a report that one already happened — and the
  # event guard is also what stops a dispatched LANE run on a candidate ref
  # from ever being counted as a ceiling.
  expect_tier "a workflow_dispatch is not silenced by a candidate run" ceiling \
    CI_EVENT_NAME=workflow_dispatch CI_REF=refs/heads/main CI_REPOSITORY=ydnikolaev/a2ahub \
    CI_AFTER_SHA=f2a0b54e57ca CI_CANDIDATE_RUNS=1 GITHUB_OUTPUT=

  # (w) THE TAG KEEPS ITS CEILING, deliberately and at a stated price. AC11
  # names "a push to public main" and this is the reason not to widen it: once
  # the promotion trusts the lookup, the tag is the one ship-boundary proof
  # that does NOT depend on the lookup being right. ~17 equivalent minutes per
  # release, bought as insurance against this file.
  expect_tier "a tag keeps the ceiling even when the candidate proved the SHA" ceiling \
    CI_EVENT_NAME=push CI_REF=refs/tags/v9.9.9 CI_REPOSITORY=ydnikolaev/a2ahub \
    CI_AFTER_SHA=f2a0b54e57ca CI_CANDIDATE_RUNS=1 CI_CHANGES_FILES="docs/backlog.md" GITHUB_OUTPUT=

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
