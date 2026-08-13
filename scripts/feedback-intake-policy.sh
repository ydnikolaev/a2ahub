#!/usr/bin/env bash
# feedback-intake-policy.sh — what may merge into a branch that accepts
# external writes, and what a triage verdict is allowed to change.
#
# agent-ops-2026-07: P13 §4.5 / P14 §5 (the merge-gate precondition) and
# P15 AC11 (a verdict must be able to reach a reporter without a release).
#
# ── WHY THIS FILE EXISTS, AND WHY IT IS PUBLIC ──────────────────────────────
#
# `feedback-intake.yml` is `pull_request_target`, so it runs with a base-repo
# token. Its `merge-policy` job is the SOLE required status check on public
# `main`, and it returned green for ANY non-feedback pull request without
# validating it: the guard computed `is-feedback=false` and exited 0.
#
# That was survivable only because nothing else depended on it. P13's AC11 and
# AC11b remove push-side and PR-side CI proof from public `main`, and P14 makes
# a public branch the permanent, externally-written system of record — and the
# COMPOSITION of those two with a check that inspects nothing is a merged
# external pull request nobody validated. Both specs therefore name a real
# merge gate as a PRECONDITION of either landing, in the same words:
#
#   "a required CI context that actually runs on the PR head, and/or a
#    non-zero review requirement, on every branch that accepts external writes"
#
# The review-requirement half is not free here: `a2a feedback submit` arms
# auto-merge, so a required review would leave every reporter's record waiting
# for a human who is not watching. This file is the other half.
#
# It is PUBLIC because the workflow that calls it runs in the PUBLIC
# repository. `docs/runbooks/feedback-sync.sh` carries a per-path comparison of
# the same shape, and it is stripped at publish — so it cannot be the SSOT for
# a decision the public repository has to make. When P14's N4 collapses that
# script, this becomes the only copy rather than the second one.
#
# ── THE SECURITY BOUNDARY, WHICH THIS DOES NOT MOVE ─────────────────────────
#
# The calling job still never checks out or executes pull-request-head content.
# It checks out the BASE repository (which is what `pull_request_target`
# resolves by default) to obtain THIS script, and it passes the PR's file
# metadata and blobs in as DATA on stdin/env. A blob is read, never sourced,
# never executed, and never used to build a command.
set -uo pipefail

# The branches on the PUBLIC repository that accept writes from outside.
#
# `main` is still one of them for the length of P15's rollover window: binaries
# released before the hub moved submit there and cannot be updated
# retroactively. `feedback-hub` is the new home. Both need the gate; neither
# needs it in the private repository, where Dependabot opens ordinary pull
# requests that must keep merging.
PUBLIC_REPO="ydnikolaev/a2ahub"
EXTERNAL_WRITE_BRANCHES="main feedback-hub"

FEEDBACK_PREFIX="feedback/inbox/"
FEEDBACK_NAME_RE='^feedback/inbox/fb-[0-9]{8}-[0-9a-f]{6}\.yaml$'

# The two fields a triage verdict owns. This is not a new rule: it is the
# per-path merge rule docs/runbooks/feedback-hub.md has carried since the hub
# existed — a record's REPORT BODY is authored by its reporter, its
# `status`/`resolution` by triage. Stated here as the machine-checkable half.
VERDICT_FIELDS="status resolution"

die() { echo "::error::merge-policy: $1" >&2; exit 1; }

# ── the changed-file classification ─────────────────────────────────────────

classify() { # stdin: the files API response (JSON array)
  local files_json repo base n fb status filename
  files_json="$(cat)"
  repo="${CI_REPOSITORY:-}"
  base="${CI_BASE_REF:-}"

  n="$(jq 'length' <<<"$files_json")"
  fb="$(jq --arg p "$FEEDBACK_PREFIX" \
        '[.[] | select(.filename | startswith($p))] | length' <<<"$files_json")"

  if [ "$fb" -eq 0 ]; then
    # THE GATE. On a branch that accepts external writes, a pull request that
    # is not a feedback record has no contract to be validated against — and
    # waving it through is what made this check green for anything at all.
    #
    # Scoped to the public repository on purpose: the private repository takes
    # Dependabot pull requests, and refusing those would stop dependency
    # updates to fix a hole that does not exist there.
    if [ "$repo" = "$PUBLIC_REPO" ] && printf '%s\n' $EXTERNAL_WRITE_BRANCHES | grep -qx -- "$base"; then
      die "'$base' accepts external writes, so the only pull request it takes is a feedback record: exactly one file under $FEEDBACK_PREFIX. This one changes $n file(s), none of them there. Nothing validates an ordinary change to this branch, so nothing may merge one."
    fi
    echo "is-feedback=false"
    echo "change=none"
    return 0
  fi

  [ "$n" -eq 1 ] && [ "$fb" -eq 1 ] || \
    die "a PR touching $FEEDBACK_PREFIX must contain exactly one changed file"

  status="$(jq -r '.[0].status' <<<"$files_json")"
  filename="$(jq -r '.[0].filename' <<<"$files_json")"

  grep -qE "$FEEDBACK_NAME_RE" <<<"$filename" || die "unexpected path $filename"

  case "$status" in
    added)
      echo "is-feedback=true"
      echo "change=added"
      echo "path=$filename"
      ;;
    modified)
      # P15 AC11. A triage verdict MODIFIES an existing record, and until this
      # existed the intake contract admitted new reports and nothing else — so
      # the hub could receive a report and never an answer to one, while its
      # own US-3 promised "answer a reporter the moment I have a verdict, not
      # at the next release". Both doors were shut: a direct push is refused by
      # the branch protection, and a verdict PR was refused here.
      #
      # The caller must additionally run `verdict-diff` on the two blobs. This
      # branch only says the SHAPE is admissible.
      echo "is-feedback=true"
      echo "change=modified"
      echo "path=$filename"
      ;;
    *)
      die "a feedback record may be added (a new report) or modified (a triage verdict); got status=$status for $filename"
      ;;
  esac
}

# ── the verdict comparison ──────────────────────────────────────────────────

# top_level_keys FILE — one line per top-level YAML key. A line starts a new
# key only if it matches the top-level grammar; anything indented, blank, or
# part of a literal block continues the current key. Same rule
# docs/runbooks/feedback-sync.sh applies for the same reason: `summary: |`
# blocks contain blank lines and colons, and a naive split reports them as
# keys.
top_level_keys() {
  awk '
    /^[A-Za-z_][A-Za-z0-9_-]*:([ \t]|$)/ { sub(/:.*/, ""); print; }
  ' "$1"
}

# key_block FILE KEY — the key and everything indented under it, so a change
# anywhere inside a multi-line value counts as a change to that key.
key_block() {
  awk -v want="$2" '
    /^[A-Za-z_][A-Za-z0-9_-]*:([ \t]|$)/ {
      k = $0; sub(/:.*/, "", k); inblock = (k == want)
    }
    inblock { print }
  ' "$1"
}

verdict_diff() { # $1 = base blob, $2 = head blob
  local base="$1" head="$2" key differing="" k
  for k in $(cat <(top_level_keys "$base") <(top_level_keys "$head") | sort -u); do
    if ! diff -q <(key_block "$base" "$k") <(key_block "$head" "$k") >/dev/null 2>&1; then
      differing="${differing:+$differing }$k"
    fi
  done

  if [ -z "$differing" ]; then
    die "the record is byte-identical: a verdict that changes nothing is not a verdict"
  fi

  for key in $differing; do
    printf '%s\n' $VERDICT_FIELDS | grep -qx -- "$key" || \
      die "a triage verdict may change only: $VERDICT_FIELDS. This one also changes: $differing. A reporter's words are theirs; if the BODY needs correcting, that is a conversation on the pull request, not a rewrite."
  done

  echo "verdict-fields=$differing"
}

# ── --teeth ─────────────────────────────────────────────────────────────────

teeth_fail=0
t_ok() { echo "feedback-intake-policy --teeth: $1"; }
t_bad() { echo "feedback-intake-policy --teeth: FAIL — $1" >&2; teeth_fail=1; }

expect_classify() { # $1 label, $2 want-exit, $3 want-substring, $4 files json, rest: env
  local label="$1" want="$2" needle="$3" json="$4"; shift 4
  local out rc
  out="$(printf '%s' "$json" | env "$@" bash "$0" classify 2>&1)"; rc=$?
  if [ "$rc" -ne "$want" ]; then t_bad "$label: exit $rc, want $want ($out)"; return; fi
  if [ -n "$needle" ] && ! grep -q -- "$needle" <<<"$out"; then
    t_bad "$label: output did not contain '$needle': $out"; return
  fi
  t_ok "$label"
}

run_teeth() {
  local pub="CI_REPOSITORY=ydnikolaev/a2ahub"
  local priv="CI_REPOSITORY=ydnikolaev/a2ahub-private"

  # (a) THE HOLE THIS CLOSES. An ordinary pull request against a branch that
  # accepts external writes used to pass, unvalidated, as the sole required
  # check.
  expect_classify "an ordinary PR to public main is REFUSED" 1 "accepts external writes" \
    '[{"filename":"README.md","status":"modified"}]' $pub CI_BASE_REF=main
  expect_classify "an ordinary PR to the hub branch is REFUSED" 1 "accepts external writes" \
    '[{"filename":"README.md","status":"modified"}]' $pub CI_BASE_REF=feedback-hub

  # (b) …and NOT in the private repository, where Dependabot must keep merging.
  expect_classify "an ordinary PR in the private repo still passes" 0 "is-feedback=false" \
    '[{"filename":"go.mod","status":"modified"}]' $priv CI_BASE_REF=main

  # (c) …nor on some other public branch nobody writes to from outside.
  expect_classify "an ordinary PR to another public branch still passes" 0 "is-feedback=false" \
    '[{"filename":"README.md","status":"modified"}]' $pub CI_BASE_REF=some-topic-branch

  # (d) A new report: the original contract, unchanged.
  expect_classify "a new report is admitted" 0 "change=added" \
    '[{"filename":"feedback/inbox/fb-20260812-755a23.yaml","status":"added"}]' $pub CI_BASE_REF=feedback-hub

  # (e) P15 AC11: a triage verdict is admitted as a SHAPE. Before this it was
  # refused outright — "feedback file must be added (got status=modified)" —
  # which is why the hub could take a report and never an answer.
  expect_classify "a triage verdict is admitted" 0 "change=modified" \
    '[{"filename":"feedback/inbox/fb-20260812-755a23.yaml","status":"modified"}]' $pub CI_BASE_REF=feedback-hub

  # (f) A DELETED record is still refused. Removing a reporter's record is not
  # a verdict, and the two must not share a branch.
  expect_classify "a deleted record is refused" 1 "added (a new report) or modified" \
    '[{"filename":"feedback/inbox/fb-20260812-755a23.yaml","status":"removed"}]' $pub CI_BASE_REF=feedback-hub

  # (g) The one-file rule and the name grammar survive.
  expect_classify "two files including a record are refused" 1 "exactly one changed file" \
    '[{"filename":"feedback/inbox/fb-20260812-755a23.yaml","status":"added"},{"filename":"README.md","status":"modified"}]' $pub CI_BASE_REF=feedback-hub
  expect_classify "an off-grammar path under feedback/inbox is refused" 1 "unexpected path" \
    '[{"filename":"feedback/inbox/notes.yaml","status":"added"}]' $pub CI_BASE_REF=feedback-hub

  # ── the verdict comparison ───────────────────────────────────────────────
  local d; d="$(mktemp -d)"
  cat >"$d/base.yaml" <<'YAML'
feedback: v1
id: fb-20260812-755a23
kind: friction
summary: >-
  A one-file record costs eleven CI jobs.

  It also has a blank line inside this block, which a naive key split reports
  as a new top-level key.
status: new
YAML

  # only status changes -> allowed
  sed 's/^status: new$/status: shipped/' "$d/base.yaml" >"$d/verdict.yaml"
  if out="$(bash "$0" verdict-diff "$d/base.yaml" "$d/verdict.yaml" 2>&1)"; then
    grep -q "verdict-fields=status" <<<"$out" && t_ok "a status-only verdict is allowed" \
      || t_bad "status-only verdict: unexpected output $out"
  else
    t_bad "a status-only verdict was refused: $out"
  fi

  # status + a NEW resolution key -> allowed
  { sed 's/^status: new$/status: shipped/' "$d/base.yaml"; echo 'resolution: fixed in v0.21.0'; } >"$d/verdict2.yaml"
  bash "$0" verdict-diff "$d/base.yaml" "$d/verdict2.yaml" >/dev/null 2>&1 \
    && t_ok "adding a resolution alongside status is allowed" \
    || t_bad "status+resolution verdict was refused"

  # THE ONE THAT MATTERS: the reporter's words rewritten under cover of a verdict
  { sed 's/^status: new$/status: shipped/' "$d/base.yaml" | sed 's/eleven CI jobs/three CI jobs/'; } >"$d/tamper.yaml"
  if bash "$0" verdict-diff "$d/base.yaml" "$d/tamper.yaml" >/dev/null 2>&1; then
    t_bad "a verdict that also rewrote the reporter's summary was ALLOWED"
  else
    t_ok "a verdict that rewrites the report body is refused"
  fi

  # a no-op "verdict"
  cp "$d/base.yaml" "$d/same.yaml"
  bash "$0" verdict-diff "$d/base.yaml" "$d/same.yaml" >/dev/null 2>&1 \
    && t_bad "a byte-identical 'verdict' was allowed" \
    || t_ok "a verdict that changes nothing is refused"

  rm -rf "$d"

  if [ "$teeth_fail" -ne 0 ]; then
    echo "feedback-intake-policy --teeth: FAIL" >&2
    exit 1
  fi
  echo "feedback-intake-policy --teeth: 13 case(s) green."
}

case "${1:-}" in
  classify)     classify ;;
  verdict-diff) shift; verdict_diff "$1" "$2" ;;
  --teeth)      run_teeth ;;
  *) echo "usage: feedback-intake-policy.sh {classify|verdict-diff BASE HEAD|--teeth}" >&2; exit 2 ;;
esac
