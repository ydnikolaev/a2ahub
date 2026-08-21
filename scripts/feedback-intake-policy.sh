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

  # THE GRAMMAR MATCH IS WHOLE-STRING, AND IT USED TO BE PER-LINE.
  #
  # `grep -qE '^…$' <<<"$filename"` succeeds when ANY LINE matches, and git
  # permits a newline inside a path. `jq length` counts such a path as one
  # element, so `n -eq 1` above does not exclude it. A filename of
  # "scripts/x.sh\nfeedback/inbox/fb-20260101-abcdef.yaml" therefore passed the
  # grammar, and `path=$filename` then went to `$GITHUB_OUTPUT` through
  # `tee -a` — a multi-line value with no delimiter, which is an Actions
  # output-injection primitive, feeding the very variable the blob fetch writes
  # to. Bash's `[[ =~ ]]` anchors against the WHOLE string, so it cannot.
  #
  # The explicit newline refusal is kept in front of it anyway: it names what
  # was wrong instead of reporting a path that renders as two lines.
  case "$filename" in
    *$'\n'* | *$'\r'*) die "a record path may not contain a line break; got a multi-line path, which is an Actions output-injection primitive" ;;
  esac
  [[ "$filename" =~ $FEEDBACK_NAME_RE ]] || die "unexpected path $filename"

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
#
# AND IT READS THE VALUE A LINE CARRIES, NOT THE LINE. This gate runs on the
# PUBLIC repository and decides whether a triage verdict may touch the hub of
# record. It compared key BLOCKS byte-wise, so a verdict against a record
# filed by an in-field binary — one that still carries the authoring
# skeleton's `# auto-filled:` comments — was read as a body rewrite and
# refused with "a reporter's words are theirs". The reporter had no way to
# fix that and no way to see why.
#
# THIS COPY OF THE RULE IS DELIBERATE, and it is the only duplicated one.
# `feedback-intake.yml` fetches THIS FILE ALONE through `gh api
# contents/...` and checks nothing out, anywhere — the security property the
# whole workflow is built on — and the hub is an orphan branch with no
# `scripts/` directory at all. So this reader cannot `source` a sibling
# without widening a deliberately narrow trusted-input surface. What keeps
# it in step with its three private siblings is the shared corpus,
# schemas/feedback/v1/fixtures/comment-hostile/, which all four `--teeth`
# harnesses drive (judge-the-thing-2026-08 P10 §T1.3).
top_level_keys() {
  awk '
    # ── the VALUE a line carries, not the line (P10) ──────────────────────
    # _p10_indent(s) — leading space/tab count.
    function _p10_indent(s,   i, c, n) {
      n = 0
      for (i = 1; i <= length(s); i++) {
        c = substr(s, i, 1)
        if (c == " " || c == "\t") { n++ } else { break }
      }
      return n
    }
    # _p10_strip(s) — s with its YAML comment removed. A `#` opens a comment
    # only OUTSIDE a quoted scalar and only at the start of the line or after
    # a space/tab, so `bug#42` and `"a bug in the # channel"` stay data. A
    # quote opens a scalar only where a scalar may START (line start, or
    # after a space/tab) — otherwise `don'"'"'t # note` would read its
    # apostrophe as an opening quote and keep the comment.
    function _p10_strip(s,   i, c, q, prev, n, out) {
      n = length(s); q = ""
      for (i = 1; i <= n; i++) {
        c = substr(s, i, 1)
        prev = (i == 1) ? "" : substr(s, i - 1, 1)
        if (q == "") {
          if ((c == "\"" || c == "'"'"'") && (i == 1 || prev == " " || prev == "\t")) { q = c; continue }
          if (c == "#" && (i == 1 || prev == " " || prev == "\t")) {
            out = substr(s, 1, i - 1)
            sub(/[ \t]+$/, "", out)
            return out
          }
        } else if (q == "\"") {
          if (c == "\\") { i++ } else if (c == "\"") { q = "" }
        } else {
          if (c == "'"'"'") { if (substr(s, i + 1, 1) == "'"'"'") { i++ } else { q = "" } }
        }
      }
      return s
    }
    # _p10_line() — sets LINE and classifies the current record:
    #   2  INSIDE a block scalar: DATA, verbatim, never scanned for comments
    #   1  a comment and nothing else: the caller DROPS it, line and all
    #   0  ordinary: LINE is $0 with any trailing comment removed
    # The strip runs BEFORE the block-scalar indicator is looked for, because
    # `resolution: >- # hub-mutated` is legal and the indicator is only at
    # end-of-line once its comment is gone.
    function _p10_line(   ind) {
      LINE = $0
      if (P10_BLOCK) {
        if ($0 ~ /^[ \t]*$/) { return 2 }
        if (_p10_indent($0) > P10_BLOCK_INDENT) { return 2 }
        P10_BLOCK = 0
      }
      if ($0 ~ /^[ \t]*#/) { LINE = ""; return 1 }
      LINE = _p10_strip($0)
      if (LINE ~ /(:|^[ \t]*-)[ \t]*[|>][0-9]*[+-]?[ \t]*$/) {
        P10_BLOCK = 1
        P10_BLOCK_INDENT = _p10_indent($0)
      }
      return 0
    }
    {
      if (_p10_line() != 0) { next }
      if (LINE ~ /^[A-Za-z_][A-Za-z0-9_-]*:([ \t]|$)/) { sub(/:.*/, "", LINE); print LINE }
    }
  ' "$1"
}

# key_block FILE KEY — the key and everything indented under it, COMMENT-FREE,
# so a change anywhere inside a multi-line value counts as a change to that
# key and a change to no value counts as nothing. A `#` inside a quoted or
# block scalar is the reporter's own text and survives; a comment-only line
# is dropped, line and all, because the skeleton's mid-document blocks
# otherwise attach to the preceding key.
key_block() {
  awk -v want="$2" '
    # ── the VALUE a line carries, not the line (P10) ──────────────────────
    # _p10_indent(s) — leading space/tab count.
    function _p10_indent(s,   i, c, n) {
      n = 0
      for (i = 1; i <= length(s); i++) {
        c = substr(s, i, 1)
        if (c == " " || c == "\t") { n++ } else { break }
      }
      return n
    }
    # _p10_strip(s) — s with its YAML comment removed. A `#` opens a comment
    # only OUTSIDE a quoted scalar and only at the start of the line or after
    # a space/tab, so `bug#42` and `"a bug in the # channel"` stay data. A
    # quote opens a scalar only where a scalar may START (line start, or
    # after a space/tab) — otherwise `don'"'"'t # note` would read its
    # apostrophe as an opening quote and keep the comment.
    function _p10_strip(s,   i, c, q, prev, n, out) {
      n = length(s); q = ""
      for (i = 1; i <= n; i++) {
        c = substr(s, i, 1)
        prev = (i == 1) ? "" : substr(s, i - 1, 1)
        if (q == "") {
          if ((c == "\"" || c == "'"'"'") && (i == 1 || prev == " " || prev == "\t")) { q = c; continue }
          if (c == "#" && (i == 1 || prev == " " || prev == "\t")) {
            out = substr(s, 1, i - 1)
            sub(/[ \t]+$/, "", out)
            return out
          }
        } else if (q == "\"") {
          if (c == "\\") { i++ } else if (c == "\"") { q = "" }
        } else {
          if (c == "'"'"'") { if (substr(s, i + 1, 1) == "'"'"'") { i++ } else { q = "" } }
        }
      }
      return s
    }
    # _p10_line() — sets LINE and classifies the current record:
    #   2  INSIDE a block scalar: DATA, verbatim, never scanned for comments
    #   1  a comment and nothing else: the caller DROPS it, line and all
    #   0  ordinary: LINE is $0 with any trailing comment removed
    # The strip runs BEFORE the block-scalar indicator is looked for, because
    # `resolution: >- # hub-mutated` is legal and the indicator is only at
    # end-of-line once its comment is gone.
    function _p10_line(   ind) {
      LINE = $0
      if (P10_BLOCK) {
        if ($0 ~ /^[ \t]*$/) { return 2 }
        if (_p10_indent($0) > P10_BLOCK_INDENT) { return 2 }
        P10_BLOCK = 0
      }
      if ($0 ~ /^[ \t]*#/) { LINE = ""; return 1 }
      LINE = _p10_strip($0)
      if (LINE ~ /(:|^[ \t]*-)[ \t]*[|>][0-9]*[+-]?[ \t]*$/) {
        P10_BLOCK = 1
        P10_BLOCK_INDENT = _p10_indent($0)
      }
      return 0
    }
    {
      cls = _p10_line()
      if (cls == 1) { next }
      if (cls == 0 && LINE ~ /^[A-Za-z_][A-Za-z0-9_-]*:([ \t]|$)/) {
        k = LINE; sub(/:.*/, "", k); inblock = (k == want)
      }
      if (inblock) { print LINE }
    }
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
    # "carries no differing top-level key", not "is byte-identical": since
    # P10 this reader compares VALUES, so a head that only dropped the
    # skeleton's comments reaches here while differing in bytes. Saying
    # byte-identical would be a refusal describing something it did not check.
    die "the record carries no differing top-level key: a verdict that changes nothing is not a verdict"
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

  # (g2) THE MULTI-LINE PATH. Git permits a newline in a path, `jq length`
  # counts it as one file, and the grammar check used to be `grep -qE` — which
  # succeeds when ANY LINE matches. So this exact payload passed, and
  # `path=$filename` reached `$GITHUB_OUTPUT` as an undelimited multi-line
  # value: an Actions output-injection primitive feeding the variable the blob
  # fetch writes to. Both halves of the repair are pinned, because a later
  # reader may see the newline refusal as redundant beside the anchor.
  # The payload has to START inside the prefix, or the `fb -eq 0` branch above
  # refuses it for a different reason and the grammar is never reached. The
  # first line is a VALID record name, which is exactly why `grep -qE` passed
  # it: the forged second line then becomes an extra `$GITHUB_OUTPUT` key —
  # here `is-feedback=true`, the output the write-capable job gates on.
  expect_classify "a path smuggling a newline is refused by name" 1 "may not contain a line break" \
    '[{"filename":"feedback/inbox/fb-20260101-abcdef.yaml\nis-feedback=true","status":"added"}]' \
    $pub CI_BASE_REF=feedback-hub

  # ── the verdict comparison ───────────────────────────────────────────────
  local d d_p10; d="$(mktemp -d)"; d_p10="$(mktemp -d)"
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

  # ── judge-the-thing-2026-08 P10: a record carries no scaffolding ─────────
  #
  # This gate runs on the PUBLIC repository and decides whether a triage
  # verdict may touch the hub of record. It compared key BLOCKS byte-wise,
  # so a verdict PR against a scaffolded base whose head is comment-free was
  # read as a body rewrite and refused with "a reporter's words are theirs."
  # Every v0.23.0 binary in the field keeps filing scaffolded records for the
  # length of the rollover window, so this is not a transitional shape.
  #
  # The corpus below is SHARED with R1 (docs/runbooks/feedback-sync.sh),
  # R2/R3 (scripts/check-feedback-corpus.sh) and R9
  # (docs/runbooks/publish-to-public.sh). This reader keeps its OWN copy of
  # the rule — the workflow fetches this file ALONE through `gh api
  # contents/...` and never checks anything out, so it cannot source a
  # sibling — and the corpus is what stops the four copies from drifting.
  local corpus
  corpus="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/schemas/feedback/v1/fixtures/comment-hostile"
  if [ ! -d "$corpus" ]; then
    t_bad "the shared comment-hostile corpus is missing: $corpus"
  else
    # THE LIVE SHAPE: a scaffolded base, a head that is that same record
    # normalized (an in-field binary's record, re-filed by a current one)
    # carrying one new `status`. Only `status` changed in VALUE terms.
    sed 's/^status: new$/status: shipped/' "$corpus/scaffolded.clean.yaml" >"$d_p10/head.yaml"
    if out="$(bash "$0" verdict-diff "$corpus/scaffolded.dirty.yaml" "$d_p10/head.yaml" 2>&1)"; then
      grep -q "verdict-fields=status" <<<"$out" \
        && t_ok "a verdict against a scaffolded base reports only the verdict field" \
        || t_bad "scaffolded base: unexpected output $out"
    else
      t_bad "a verdict against a scaffolded base was refused as a body rewrite: $out"
    fi

    # THE RECONCILING TOOTH. Every dirty/clean pair must carry NO differing
    # top-level key — which this reader reports by refusing the pair as a
    # verdict that changes nothing. The MESSAGE is asserted, not merely the
    # exit code: the negative case below also exits non-zero.
    p10_pairs=0
    p10_bad=""
    for dirty in "$corpus"/*.dirty.yaml; do
      clean="${dirty%.dirty.yaml}.clean.yaml"
      if [ ! -f "$clean" ]; then
        p10_bad="${p10_bad}$(basename "$dirty") has no clean twin; "
        continue
      fi
      out="$(bash "$0" verdict-diff "$dirty" "$clean" 2>&1)" && rc=0 || rc=$?
      if [ "$rc" -eq 0 ] || ! grep -q "no differing top-level key" <<<"$out"; then
        p10_bad="${p10_bad}$(basename "$dirty"): $out; "
      fi
      p10_pairs=$((p10_pairs + 1))
    done
    if [ -n "$p10_bad" ]; then
      t_bad "comment-hostile pairs disagree across this reader: $p10_bad"
    elif [ "$p10_pairs" -lt 6 ]; then
      t_bad "expected at least 6 comment-hostile pairs, found $p10_pairs"
    else
      t_ok "every comment-hostile pair ($p10_pairs) reads identically through key_block"
    fi

    # US-7: a `#` inside a folded block scalar is the reporter's own text.
    if bash "$0" _internal-key-block "$corpus/folded-summary.dirty.yaml" summary \
       | grep -q '# This heading is DATA'; then
      t_ok "a '#' inside a folded summary is data, not a comment"
    else
      t_bad "a '#' inside a folded summary was eaten as a comment"
    fi

    # US-4: the guard is not bought back. A head that is comment-free AND
    # rewrites the reporter's summary is still refused, in the same words.
    if out="$(bash "$0" verdict-diff "$corpus/body-diverged.base.yaml" "$corpus/body-diverged.head.yaml" 2>&1)"; then
      t_bad "a comment-only normalization that also rewrote the summary was ALLOWED: $out"
    elif grep -q "A reporter's words are theirs" <<<"$out"; then
      t_ok "a normalization that also rewrites the report body is still refused"
    else
      t_bad "the diverged pair was refused for the wrong reason: $out"
    fi
  fi

  rm -rf "$d_p10"

  rm -rf "$d"

  if [ "$teeth_fail" -ne 0 ]; then
    echo "feedback-intake-policy --teeth: FAIL" >&2
    exit 1
  fi
  echo "feedback-intake-policy --teeth: 18 case(s) green."
}

case "${1:-}" in
  classify)     classify ;;
  verdict-diff) shift; verdict_diff "$1" "$2" ;;
  # --teeth's own seam onto key_block, so the US-7 assertion drives the real
  # function rather than a copy of it. Not part of the workflow's contract.
  _internal-key-block) shift; key_block "$1" "$2" ;;
  --teeth)      run_teeth ;;
  *) echo "usage: feedback-intake-policy.sh {classify|verdict-diff BASE HEAD|--teeth}" >&2; exit 2 ;;
esac
