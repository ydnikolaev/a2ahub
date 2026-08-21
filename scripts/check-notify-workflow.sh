#!/usr/bin/env bash
# check-notify-workflow.sh — the notify workflow pair's security posture
# (space-notify-2026-08 P7, spec 07 §T5; invariant #6 is
# judge-the-thing-2026-08 P8, spec 08 §T5).
#
# Owns exactly six invariants, all static and Node-free:
#   1. `secrets: inherit` appears nowhere in either file, as a real mapping
#      value — never as prose explaining that it is forbidden.
#   2. Triggers: the reusable workflow's `on:` is exactly {workflow_call};
#      the template caller's `on:` is exactly {push, workflow_dispatch};
#      `pull_request_target` appears nowhere in either file (again: as a
#      real trigger key, never as prose).
#   3. The reusable workflow's job-level `permissions:` block is exactly
#      {contents: read}. (Scoped to the reusable file only — the spec
#      names "Its `permissions:`", singular, for a2a-notify-reusable.yml;
#      the template caller's own top-level permissions: block is not
#      separately gated here, though it happens to already match.)
#   4. The template caller carries no `run:` step at all.
#   5. Unset-expressibility, across BOTH reusable workflows
#      (judge-the-thing-2026-08 spec 07 §T5): a `workflow_call` input that is
#      `required: false` over a NON-STRING type must carry a `default:` —
#      only a string has an empty member that can mean "the operator set
#      nothing", and an unset `number` resolves to `0`. Second clause: such
#      an input's `description:` may not claim emptiness over a type with no
#      empty member. This is the one invariant that reads
#      a2a-validate-reusable.yml as well, because the defect it names is a
#      property of the input DECLARATION, not of the notify pair.
#   6. Signed-binary install, across BOTH reusable workflows (spec 08 §T5):
#      6a. the literal `go install github.com/ydnikolaev/a2ahub/cmd/a2a@`
#          (the RETIRED remote-module-ref mechanism) appears nowhere in
#          either file — a2a-validate-reusable.yml's own local-path dogfood
#          install (`go install "$GITHUB_WORKSPACE/cmd/a2a"`) carries no `@`
#          and no module path, so it is deliberately NOT caught.
#      6b. within any step's `run:` body, `releases/download/` present ⇒ a
#          bare, unwrapped `cosign verify-blob` call is present, sits
#          outside any `for`/`while` retry loop in that same body, and no
#          `install `/`chmod `/`echo … >>"$GITHUB_PATH"` command precedes it
#          — F1 (fails the step), F2 (fails once, not retried) and F3 (never
#          executable before verified) from spec 08's "Failure posture",
#          asserted structurally so none depends on a reviewer noticing.
#
# THE TRAP. Both forbidden literals (`pull_request_target`, `secrets:
# inherit`) appear in BOTH real files as COMMENTS explaining why they are
# forbidden (see each file's own header) — a gate that counts occurrences
# reds a correct file. And the caller's workflow_dispatch input is named
# `dry-run:`, which a naive `grep -c "run:"` counts as a `run:` step.
#
# This gate strips '#'-to-end-of-line on every line before any text check
# (these two fixed files use '#' only for full-line prose and short
# trailing pin comments — never inside a quoted value, so a blanket strip
# is exact for this pair) and matches `run:` anchored to the START of a
# (whitespace-trimmed) line — "dry-run:" is preceded by "dry-" rather than
# by whitespace, so it never satisfies that anchor. Every one of these is
# proven by a --teeth case below, not merely asserted here.
#
# What this gate does NOT own: render/send behaviour, redaction, the 429
# budget and a real Telegram send are proven elsewhere (Go tiers, the live
# tier) — see this epic's own docs for the "what stays unproven" table.
#
# Usage: bash scripts/check-notify-workflow.sh
#        bash scripts/check-notify-workflow.sh --teeth

# lane-inputs lives on this gate's Makefile recipe, not here. This script is
# PRIVATE_ONLY and the publisher strips it, so a claim written here is absent
# from every public checkout — and the lane derivation, unable to settle the
# phase, refuses the WHOLE lane rather than that one gate. Public CI on `main`
# was red for exactly that reason. Same placement as check-feedback-corpus.sh.
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   self-locates the shared helper through command substitution; every other
#   unresolved construct below is this file's own --teeth harness reading and
#   writing files under a `mktemp -d` scratch root, never a repo path.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

REUSABLE_DEFAULT="$GATE_ROOT/.github/workflows/a2a-notify-reusable.yml"
CALLER_DEFAULT="$GATE_ROOT/space-template/.github/workflows/a2a-notify.yml"
# The sibling reusable workflow (a2a-validate-reusable.yml) is READ, never
# edited, by invariant #5 (spec 07 §T5 "the audit is closed, not sampled" —
# AC 10 requires the same input-shape rule to reach BOTH reusable workflows).
VALIDATE_REUSABLE_DEFAULT="$GATE_ROOT/.github/workflows/a2a-validate-reusable.yml"

# stripped_content prints $1 with every '#'-to-end-of-line comment removed.
stripped_content() { # $1 = file
  sed -E 's/#.*$//; s/[[:space:]]+$//' "$1"
}

# trigger_keys prints the `on:` block's immediate (2-space-indented) child
# keys, one per line — the trigger names a workflow actually declares. The
# `on:` block is anchored at column 0; the block ends at the next column-0
# key (jobs:, permissions:, secrets:, name:, …).
trigger_keys() { # $1 = stripped content
  printf '%s\n' "$1" | awk '
    /^on:[[:space:]]*$/ { inon=1; next }
    inon && /^[A-Za-z]/  { inon=0 }
    inon && /^  [A-Za-z_-]+:/ {
      key=$0
      sub(/^  /, "", key)
      sub(/:.*$/, "", key)
      print key
    }
  '
}

# permissions_keys prints the FIRST `permissions:` block's immediate
# children (its indentation + 2), one per line — wherever in the file that
# block sits (job-level here), since only one exists per file in this pair.
permissions_keys() { # $1 = stripped content
  printf '%s\n' "$1" | awk '
    function indent_of(line,    tmp) {
      tmp = line
      sub(/[^ ].*$/, "", tmp)
      return length(tmp)
    }
    /^[[:space:]]*permissions:[[:space:]]*$/ && !found {
      found = 1
      anchor = indent_of($0)
      next
    }
    found {
      if ($0 ~ /^[[:space:]]*$/) next
      cur = indent_of($0)
      if (cur <= anchor) { found = 0; next }
      if (cur == anchor + 2) {
        line = $0
        sub(/^[[:space:]]*/, "", line)
        print line
      }
    }
  '
}

# keys_match_exactly reports (via return code) whether the newline-separated
# key set in $1 equals the newline-separated expected set in $2, ignoring
# order. Prints nothing; the caller composes the gate_fail message.
keys_match_exactly() { # $1 = actual (newline-separated), $2 = expected (newline-separated)
  local actual_sorted expected_sorted
  actual_sorted="$(printf '%s\n' "$1" | sed '/^$/d' | sort)"
  expected_sorted="$(printf '%s\n' "$2" | sed '/^$/d' | sort)"
  [ "$actual_sorted" = "$expected_sorted" ]
}

check_no_forbidden_literal() { # $1 = label, $2 = stripped content, $3 = literal, $4 = citation (optional)
  local citation="${4:-spec 07 T5}"
  if printf '%s\n' "$2" | grep -Fq -- "$3"; then
    gate_fail "notify-workflow: $1 contains the literal \"$3\" outside a comment — forbidden ($citation)"
  fi
}

# check_no_remote_install_literal enforces invariant #6a (spec 08 T5): the
# module path plus `@` is the predicate — deliberately NOT plain
# `go install`, which a2a-validate-reusable.yml's local-path dogfood install
# (`go install "$GITHUB_WORKSPACE/cmd/a2a"`, no `@`, no module path) must
# survive. An optional leading `"` is tolerated because both real files spell
# the invocation as `go install "github.com/...@$REF"` — a plain
# grep -F on the un-quoted literal (§8 criterion 1's own spelling) misses the
# actual defect entirely, which is exactly the "a rule true and inert"
# failure this epic is about.
check_no_remote_install_literal() { # $1 = label, $2 = stripped content
  if printf '%s\n' "$2" | grep -Eq 'go install ["]?github\.com/ydnikolaev/a2ahub/cmd/a2a@'; then
    gate_fail "notify-workflow: $1 contains \"go install github.com/ydnikolaev/a2ahub/cmd/a2a@\" — the RETIRED remote-module-ref mechanism (spec 08 T5 #6a)"
  fi
}

check_no_secrets_inherit_literal() { # $1 = label, $2 = stripped content
  if printf '%s\n' "$2" | grep -Eq '^[[:space:]]*secrets:[[:space:]]*inherit[[:space:]]*$'; then
    gate_fail "notify-workflow: $1 sets \`secrets: inherit\` — declare each secret explicitly (spec 07 T5 #1)"
  fi
}

check_no_run_step() { # $1 = label, $2 = stripped content
  local hits
  hits="$(printf '%s\n' "$2" | grep -nE '^[[:space:]]*run:')"
  if [ -n "$hits" ]; then
    gate_fail "notify-workflow: $1 carries a \`run:\` step — the template caller must contain no logic (spec 07 T5 #4): $(printf '%s' "$hits" | head -1)"
  fi
}

# extract_inputs_block prints the FULL sub-block under `on.workflow_call.inputs:`
# (indentation preserved, `inputs:` itself excluded), stopping at the next
# sibling key of `inputs:` (e.g. `secrets:`) or end of block. Anchored on the
# fixed indentation both reusable workflows in this pair actually use
# (on:0, workflow_call:2, inputs:4, input key:6, input property:8) — the same
# assumption trigger_keys/permissions_keys already make for this pair.
extract_inputs_block() { # $1 = stripped content
  printf '%s\n' "$1" | awk '
    function indent_of(line,    tmp) { tmp = line; sub(/[^ ].*$/, "", tmp); return length(tmp) }
    /^    inputs:[[:space:]]*$/ && !found { found = 1; next }
    found {
      if ($0 ~ /^[[:space:]]*$/) next
      cur = indent_of($0)
      if (cur <= 4) { found = 0; next }
      print $0
    }
  '
}

# check_unset_expressible enforces invariant #5 (spec 07 T5 "the gate"):
# in EITHER reusable workflow's on.workflow_call.inputs block, an input
# declared `required: false` whose type is not `string` and which declares
# no `default:` cannot express "the operator set nothing" (clause 1); and an
# input whose `description:` claims emptiness (the word "empty",
# case-insensitive) over a non-string type is refused on that clause alone
# (clause 2), because the mechanism cannot honour what the prose promises.
# `required: true` inputs are exempt from clause 1 by construction — GitHub
# itself refuses "unset" for those before this job starts (spec 07 T5 audit,
# `mode` row).
check_unset_expressible() { # $1 = label, $2 = stripped content
  local block violations reason input_name input_type
  block="$(extract_inputs_block "$2")"
  violations="$(printf '%s\n' "$block" | awk -F'\t' '
    function indent_of(line,    tmp) { tmp = line; sub(/[^ ].*$/, "", tmp); return length(tmp) }
    function flush() {
      if (cur == "") return
      if (required == "false" && type != "" && type != "string" && has_default == 0) {
        print "no-default\t" cur "\t" type
      }
      if (type != "" && type != "string" && desc_lower ~ /empty/) {
        print "desc-empty\t" cur "\t" type
      }
    }
    {
      if ($0 ~ /^[[:space:]]*$/) next
      ind = indent_of($0)
      if (ind == 6) {
        flush()
        line = $0
        sub(/^[[:space:]]*/, "", line)
        sub(/:.*$/, "", line)
        cur = line; required = ""; type = ""; has_default = 0; desc_lower = ""
        next
      }
      if (cur == "") next
      line = $0
      sub(/^[[:space:]]*/, "", line)
      if (line ~ /^required:/) {
        v = line; sub(/^required:[[:space:]]*/, "", v); required = v
      } else if (line ~ /^type:/) {
        v = line; sub(/^type:[[:space:]]*/, "", v); type = v
      } else if (line ~ /^default:/) {
        has_default = 1
      } else if (line ~ /^description:/) {
        desc_lower = tolower(line)
      }
    }
    END { flush() }
  ')"
  [ -z "$violations" ] && return
  while IFS=$'\t' read -r reason input_name input_type; do
    [ -z "$reason" ] && continue
    case "$reason" in
      no-default)
        gate_fail "notify-workflow: $1's workflow_call input \"$input_name\" is required: false, type $input_type, and declares no default: — cannot express \"unset\" (spec 07 T5 #5)"
        ;;
      desc-empty)
        gate_fail "notify-workflow: $1's workflow_call input \"$input_name\" (type $input_type) has a description: claiming emptiness (\"empty\") over a type with no empty member (spec 07 T5 #5)"
        ;;
    esac
  done <<<"$violations"
}

# check_download_verify_order enforces invariant #6b (spec 08 T5 "the
# gate"): within EVERY `run:` step body in the file, if `releases/download/`
# is present then a bare, unretried `cosign verify-blob` call must also be
# present and must precede any `install `/`chmod `/`echo …>>"$GITHUB_PATH"`
# command in that same body.
#
# The "every line STRICTLY DEEPER than the run: anchor, up to the first line
# at <= anchor" reader spec 08 §T5 asks for is folded into this one awk pass
# (rather than a separate reader function feeding a second pass) so the
# per-block state — has the download line been seen, has cosign, is a
# retry loop currently open, is `set +e` currently in force — stays
# together with the block it describes. Same `indent_of` shape as
# trigger_keys/permissions_keys; a NEW variant because those two print
# children at exactly `anchor + 2` and a `run:` body's shell text nests far
# deeper (an `if`, a `for`, a backslash continuation).
check_download_verify_order() { # $1 = label, $2 = stripped content
  local violations reason
  violations="$(printf '%s\n' "$2" | awk '
    function indent_of(line,    tmp) { tmp = line; sub(/[^ ].*$/, "", tmp); return length(tmp) }
    function reset_block() {
      has_download = 0; has_cosign = 0; bad_order = 0
      f1_broken = 0; f2_broken = 0
      loop_depth = 0; pipefail_off = 0; cosign_stmt_active = 0
    }
    function eval_block() {
      if (!has_download) return
      if (!has_cosign) { print "no-verify"; return }
      if (bad_order) print "bad-order"
      if (f1_broken) print "f1-suppressed"
      if (f2_broken) print "f2-inside-loop"
    }
    BEGIN { in_block = 0; reset_block() }
    {
      line = $0
      if (in_block) {
        if (line ~ /^[[:space:]]*$/) next
        cur = indent_of(line)
        if (cur <= anchor) {
          eval_block()
          in_block = 0
          reset_block()
          # fall through: this line may itself open a NEW run: block
        } else {
          t = line
          sub(/^[[:space:]]*/, "", t)

          # Retry-loop tracking: a for/while ... ; do opens depth, a lone
          # "done" line closes it. Only the ONE retry loop this pair ever
          # writes (wrapping the download curls) needs to be seen.
          if (t ~ /^(for|while)[[:space:]].*;[[:space:]]*do[[:space:]]*$/) loop_depth++
          else if (t ~ /^done[[:space:]]*$/ && loop_depth > 0) loop_depth--

          # set +e / set -e (set -euo pipefail included) toggle whether a
          # later bare command still fails the step.
          if (t ~ /^set[[:space:]]+\+e([[:space:]]|$)/) pipefail_off = 1
          else if (t ~ /^set[[:space:]]+-e/) pipefail_off = 0

          if (t ~ /releases\/download\//) has_download = 1

          if (cosign_stmt_active) {
            if (t ~ /\|\|/) f1_broken = 1
            if (t !~ /\\$/) cosign_stmt_active = 0
          } else if (t ~ /cosign verify-blob/) {
            has_cosign = 1
            if (t ~ /\|\|/) f1_broken = 1
            if (t ~ /^if[[:space:]]/) f1_broken = 1
            if (pipefail_off) f1_broken = 1
            if (loop_depth > 0) f2_broken = 1
            if (t ~ /\\$/) cosign_stmt_active = 1
          } else if (has_download && !has_cosign) {
            if (t ~ /^install[[:space:]]/ || t ~ /^chmod[[:space:]]/ || t ~ /^echo[[:space:]].*GITHUB_PATH/) {
              bad_order = 1
            }
          }
          next
        }
      }
      trimmed_open = line
      sub(/^[[:space:]]*/, "", trimmed_open)
      if (trimmed_open ~ /^run:/) {
        anchor = indent_of(line)
        in_block = 1
        reset_block()
      }
    }
    END { if (in_block) eval_block() }
  ')"
  [ -z "$violations" ] && return
  while IFS= read -r reason; do
    [ -z "$reason" ] && continue
    case "$reason" in
      no-verify)
        gate_fail "notify-workflow: $1 has a run: body downloading from releases/download/ with no cosign verify-blob call (spec 08 T5 #6b)"
        ;;
      bad-order)
        gate_fail "notify-workflow: $1 has a run: body where install/chmod/\$GITHUB_PATH precedes cosign verify-blob after a releases/download/ download (spec 08 T5 #6b ordering, F3)"
        ;;
      f1-suppressed)
        gate_fail "notify-workflow: $1's cosign verify-blob call is wrapped (||, set +e, or a captured status) rather than a bare command — a verification that warns is not a verification (spec 08 T5 #6b, F1)"
        ;;
      f2-inside-loop)
        gate_fail "notify-workflow: $1's cosign verify-blob call sits INSIDE the retry loop — a genuinely bad artifact must red once, not be retried (spec 08 T5 #6b, F2)"
        ;;
    esac
  done <<<"$violations"
}

run_check() { # $1 = reusable path, $2 = caller path, $3 = a2a-validate-reusable.yml path
  local reusable="${1:-$REUSABLE_DEFAULT}" caller="${2:-$CALLER_DEFAULT}"
  local validate_reusable="${3:-$VALIDATE_REUSABLE_DEFAULT}"
  local reusable_stripped caller_stripped validate_reusable_stripped

  if [ ! -f "$reusable" ]; then
    gate_fail "notify-workflow: missing reusable workflow $reusable"
    return
  fi
  if [ ! -f "$caller" ]; then
    gate_fail "notify-workflow: missing template caller $caller"
    return
  fi
  if [ ! -f "$validate_reusable" ]; then
    gate_fail "notify-workflow: missing a2a-validate-reusable.yml $validate_reusable"
    return
  fi

  reusable_stripped="$(stripped_content "$reusable")"
  caller_stripped="$(stripped_content "$caller")"
  validate_reusable_stripped="$(stripped_content "$validate_reusable")"

  # Invariant 1: secrets: inherit nowhere in either file.
  check_no_secrets_inherit_literal "a2a-notify-reusable.yml" "$reusable_stripped"
  check_no_secrets_inherit_literal "the template caller" "$caller_stripped"

  # Invariant 2: exact trigger sets, and pull_request_target in neither.
  if ! keys_match_exactly "$(trigger_keys "$reusable_stripped")" "workflow_call"; then
    gate_fail "notify-workflow: a2a-notify-reusable.yml's on: block is not exactly {workflow_call} (got: $(trigger_keys "$reusable_stripped" | tr '\n' ' '))"
  fi
  if ! keys_match_exactly "$(trigger_keys "$caller_stripped")" "$(printf 'push\nworkflow_dispatch')"; then
    gate_fail "notify-workflow: the template caller's on: block is not exactly {push, workflow_dispatch} (got: $(trigger_keys "$caller_stripped" | tr '\n' ' '))"
  fi
  check_no_forbidden_literal "a2a-notify-reusable.yml" "$reusable_stripped" "pull_request_target"
  check_no_forbidden_literal "the template caller" "$caller_stripped" "pull_request_target"

  # Invariant 3: reusable's permissions: block is exactly {contents: read}.
  if ! keys_match_exactly "$(permissions_keys "$reusable_stripped")" "contents: read"; then
    gate_fail "notify-workflow: a2a-notify-reusable.yml's permissions: block is not exactly {contents: read} (got: $(permissions_keys "$reusable_stripped" | tr '\n' '|'))"
  fi

  # Invariant 4: the template caller carries no run: step at all.
  check_no_run_step "the template caller" "$caller_stripped"

  # Invariant 5: every optional workflow_call input in BOTH reusable
  # workflows can express "unset" — the audit in spec 07 T5 is closed, not
  # sampled, so the rule reads both files, not just the one that shipped
  # the defect.
  check_unset_expressible "a2a-notify-reusable.yml" "$reusable_stripped"
  check_unset_expressible "a2a-validate-reusable.yml" "$validate_reusable_stripped"

  # Invariant 6a: the RETIRED remote-module-ref install literal appears
  # nowhere in either reusable workflow (spec 08 T5). Deliberately targets
  # the remote ref, not `go install` in general — a2a-validate-reusable.yml's
  # local-path dogfood install carries no `@` and no module path, and must
  # not red here.
  check_no_remote_install_literal "a2a-notify-reusable.yml" "$reusable_stripped"
  check_no_remote_install_literal "a2a-validate-reusable.yml" "$validate_reusable_stripped"

  # Invariant 6b: any run: body downloading from releases/download/ must
  # verify with a bare cosign verify-blob call before anything makes the
  # asset executable or reachable (spec 08 T5).
  check_download_verify_order "a2a-notify-reusable.yml" "$reusable_stripped"
  check_download_verify_order "a2a-validate-reusable.yml" "$validate_reusable_stripped"
}

# ── teeth fixtures ──────────────────────────────────────────────────────
# Synthetic, minimal, and independent of the real files' own future edits —
# they exercise the STRUCTURAL SHAPE the four invariants police, including
# the exact trap (comment-only mentions of both forbidden literals, and a
# `dry-run:` key that must never be mistaken for a `run:` step).

write_good_reusable() { # $1 = path
  cat >"$1" <<'YAML'
# `pull_request_target` is FORBIDDEN as a trigger here, and always will be:
# a fork-authored commit could read the token out from under the caller.
# TG_BOT_TOKEN is an EXPLICIT secret input, never `secrets: inherit` — a
# space repo may not be public, so explicit is what review can see.
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      dry-run:
        type: boolean
        default: false
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: render and send
        run: echo notify
YAML
}

write_good_caller() { # $1 = path
  cat >"$1" <<'YAML'
# `pull_request_target` is FORBIDDEN as a trigger here — see the reusable
# workflow's own header. Triggers are exactly `push: main` and
# `workflow_dispatch`.
name: a2a-notify

on:
  push:
    branches: [main]
  workflow_dispatch:
    inputs:
      dry-run:
        type: boolean
        default: false

permissions:
  contents: read

jobs:
  a2a-notify:
    uses: ydnikolaev/a2ahub/.github/workflows/a2a-notify-reusable.yml@v0.22.0
    with:
      dry-run: ${{ inputs.dry-run }}
    secrets:
      TG_BOT_TOKEN: ${{ secrets.TG_BOT_TOKEN }}
YAML
}

# write_bad_input_no_default_reusable — clause 1's exact bad shape:
# required: false, a non-string type, no default: at all.
write_bad_input_no_default_reusable() { # $1 = path
  cat >"$1" <<'YAML'
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      limit:
        description: "The digest-coalescing threshold."
        required: false
        type: number
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: render and send
        run: echo notify
YAML
}

# write_good_input_string_reusable — the same input, fixed: type: string,
# default: '' — must green.
write_good_input_string_reusable() { # $1 = path
  cat >"$1" <<'YAML'
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      limit:
        description: "The digest-coalescing threshold. Empty means the flag is not passed."
        required: false
        type: string
        default: ""
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: render and send
        run: echo notify
YAML
}

# write_bad_desc_empty_reusable — clause 1 is satisfied (a default: exists,
# so "unset" IS expressible by GitHub's own resolution), but the
# description: still claims emptiness over a non-string type — clause 2
# must fire on the description alone.
write_bad_desc_empty_reusable() { # $1 = path
  cat >"$1" <<'YAML'
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      limit:
        description: "Empty means: use the CLI's own default."
        required: false
        type: number
        default: 5
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: render and send
        run: echo notify
YAML
}

# ── invariant #6 fixtures (spec 08 §T5) ─────────────────────────────────
# All eight share the real "Install a2a"/"a2a validate --ci" step's shape at
# 6/8-space indentation — the #6b reader needs a real run: anchor to nest
# under, not the one-liner `run: echo notify` the invariant 1–5 fixtures
# use. Four are spec 08 §T5's own named cases (remote-install, the correct
# download+verify block, no-verify, verify-after-install) plus the local
# dogfood green and the (b) substring trap; two more (verify-suppressed,
# verify-inside-loop) are additions proving criterion 3's F1/F2 claims that
# spec 08 §T5's four-fixture list does not itself name — see this phase's
# §11.

# write_reusable_remote_install — today's actual defect: go install of the
# remote module ref. Must RED on #6a.
write_reusable_remote_install() { # $1 = path
  cat >"$1" <<'YAML'
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      a2a-ref:
        type: string
        default: "v0.24.0"
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: Install a2a
        env:
          A2A_REF: ${{ inputs.a2a-ref }}
        run: |
          set -euo pipefail
          bin="$RUNNER_TEMP/a2a-notify-bin"
          if GOBIN="$bin" go install "github.com/ydnikolaev/a2ahub/cmd/a2a@$A2A_REF"; then
            echo ok
          fi
          echo "$bin" >>"$GITHUB_PATH"
YAML
}

# write_reusable_download_verify — the correct download+verify+install
# shape (C1–C7). Must GREEN on both #6a and #6b.
write_reusable_download_verify() { # $1 = path
  cat >"$1" <<'YAML'
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      a2a-ref:
        type: string
        default: "v0.24.0"
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: Install a2a
        env:
          A2A_REF: ${{ inputs.a2a-ref }}
        run: |
          set -euo pipefail
          bin="$RUNNER_TEMP/a2a-notify-bin"
          mkdir -p "$bin"
          asset="a2a-linux-amd64"
          base="https://github.com/ydnikolaev/a2ahub/releases/download/$A2A_REF"
          downloaded=0
          for attempt in 1 2 3; do
            if curl -fsSL -o "$RUNNER_TEMP/$asset" "$base/$asset"; then
              downloaded=1
              break
            fi
            sleep $((attempt * 5))
          done
          if [ "$downloaded" -ne 1 ]; then
            exit 1
          fi
          cosign verify-blob \
            --bundle "$RUNNER_TEMP/$asset.cosign.bundle" \
            --certificate-identity-regexp '^https://github\.com/ydnikolaev/a2ahub/\.github/workflows/release\.yml@refs/tags/' \
            --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
            "$RUNNER_TEMP/$asset"
          install -m 0755 "$RUNNER_TEMP/$asset" "$bin/a2a"
          echo "$bin" >>"$GITHUB_PATH"
YAML
}

# write_reusable_download_no_verify — downloads from releases/download/ but
# never calls cosign verify-blob at all. Must RED on #6b (no-verify).
write_reusable_download_no_verify() { # $1 = path
  cat >"$1" <<'YAML'
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      a2a-ref:
        type: string
        default: "v0.24.0"
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: Install a2a
        env:
          A2A_REF: ${{ inputs.a2a-ref }}
        run: |
          set -euo pipefail
          bin="$RUNNER_TEMP/a2a-notify-bin"
          mkdir -p "$bin"
          asset="a2a-linux-amd64"
          base="https://github.com/ydnikolaev/a2ahub/releases/download/$A2A_REF"
          curl -fsSL -o "$RUNNER_TEMP/$asset" "$base/$asset"
          install -m 0755 "$RUNNER_TEMP/$asset" "$bin/a2a"
          echo "$bin" >>"$GITHUB_PATH"
YAML
}

# write_reusable_verify_after_install — verifies, but AFTER install -m 0755
# has already made the (unverified at that point) asset executable. Must RED
# on #6b's ordering clause (F3).
write_reusable_verify_after_install() { # $1 = path
  cat >"$1" <<'YAML'
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      a2a-ref:
        type: string
        default: "v0.24.0"
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: Install a2a
        env:
          A2A_REF: ${{ inputs.a2a-ref }}
        run: |
          set -euo pipefail
          bin="$RUNNER_TEMP/a2a-notify-bin"
          mkdir -p "$bin"
          asset="a2a-linux-amd64"
          base="https://github.com/ydnikolaev/a2ahub/releases/download/$A2A_REF"
          curl -fsSL -o "$RUNNER_TEMP/$asset" "$base/$asset"
          install -m 0755 "$RUNNER_TEMP/$asset" "$bin/a2a"
          cosign verify-blob \
            --bundle "$RUNNER_TEMP/$asset.cosign.bundle" \
            --certificate-identity-regexp '^https://github\.com/ydnikolaev/a2ahub/\.github/workflows/release\.yml@refs/tags/' \
            --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
            "$RUNNER_TEMP/$asset"
          echo "$bin" >>"$GITHUB_PATH"
YAML
}

# write_reusable_local_dogfood — a2a-validate-reusable.yml's OWN local-path
# dogfood install, carrying no `@` and no module path. Must GREEN on #6a —
# a rule that reddens the dogfood is the wrong rule (spec 08 §T5).
write_reusable_local_dogfood() { # $1 = path
  cat >"$1" <<'YAML'
name: a2a-validate-reusable

on:
  workflow_call:
    inputs:
      a2a-ref:
        type: string
        default: ""
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  validate:
    permissions:
      contents: read
    steps:
      - name: a2a validate --ci
        run: |
          set -euo pipefail
          bin="$RUNNER_TEMP/a2a-validate-bin"
          GOBIN="$bin" go install "$GITHUB_WORKSPACE/cmd/a2a"
YAML
}

# write_reusable_download_verify_trap — the correct download+verify block,
# but the step is named "Install a2a", it `uses:
# sigstore/cosign-installer`, and the header prose says "chmod +x" — the
# (b) trap spec 08 §T5 names: a substring predicate would red this correct
# file three ways over. Must GREEN.
write_reusable_download_verify_trap() { # $1 = path
  cat >"$1" <<'YAML'
# Install cosign, then Install a2a. The download is followed by
# `install -m 0755` (not `chmod +x`), but this header still SAYS
# "chmod +x" in prose, on purpose — a substring predicate must not red it.
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      a2a-ref:
        type: string
        default: "v0.24.0"
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: Install cosign
        uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.0.0
      - name: Install a2a
        env:
          A2A_REF: ${{ inputs.a2a-ref }}
        run: |
          set -euo pipefail
          bin="$RUNNER_TEMP/a2a-notify-bin"
          mkdir -p "$bin"
          asset="a2a-linux-amd64"
          base="https://github.com/ydnikolaev/a2ahub/releases/download/$A2A_REF"
          downloaded=0
          for attempt in 1 2 3; do
            if curl -fsSL -o "$RUNNER_TEMP/$asset" "$base/$asset"; then
              downloaded=1
              break
            fi
            sleep $((attempt * 5))
          done
          if [ "$downloaded" -ne 1 ]; then
            exit 1
          fi
          cosign verify-blob \
            --bundle "$RUNNER_TEMP/$asset.cosign.bundle" \
            --certificate-identity-regexp '^https://github\.com/ydnikolaev/a2ahub/\.github/workflows/release\.yml@refs/tags/' \
            --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
            "$RUNNER_TEMP/$asset"
          install -m 0755 "$RUNNER_TEMP/$asset" "$bin/a2a"
          echo "$bin" >>"$GITHUB_PATH"
YAML
}

# write_reusable_verify_suppressed — cosign verify-blob runs, but its exit
# is swallowed by a trailing `||` — a "verification" that cannot fail the
# step. Must RED on #6b's F1 clause. Not one of spec 08 §T5's four named
# fixtures; added because criterion 3 claims F1 is asserted by #6b and an
# untested clause is exactly the "rule true and inert" shape this epic is
# about (see this phase's own §11 deviation note).
write_reusable_verify_suppressed() { # $1 = path
  cat >"$1" <<'YAML'
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      a2a-ref:
        type: string
        default: "v0.24.0"
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: Install a2a
        env:
          A2A_REF: ${{ inputs.a2a-ref }}
        run: |
          set -euo pipefail
          bin="$RUNNER_TEMP/a2a-notify-bin"
          mkdir -p "$bin"
          asset="a2a-linux-amd64"
          base="https://github.com/ydnikolaev/a2ahub/releases/download/$A2A_REF"
          curl -fsSL -o "$RUNNER_TEMP/$asset" "$base/$asset"
          cosign verify-blob --bundle "$RUNNER_TEMP/$asset.cosign.bundle" "$RUNNER_TEMP/$asset" || echo "::warning::unverified"
          install -m 0755 "$RUNNER_TEMP/$asset" "$bin/a2a"
          echo "$bin" >>"$GITHUB_PATH"
YAML
}

# write_reusable_verify_inside_loop — cosign verify-blob runs INSIDE the
# retry loop that wraps the download — a bad artifact would be retried
# three times instead of reddening once. Must RED on #6b's F2 clause. Same
# rationale as write_reusable_verify_suppressed above: not one of the four
# named fixtures, added to prove criterion 3's F2 claim rather than leave it
# asserted-but-untested.
write_reusable_verify_inside_loop() { # $1 = path
  cat >"$1" <<'YAML'
name: a2a-notify-reusable

on:
  workflow_call:
    inputs:
      a2a-ref:
        type: string
        default: "v0.24.0"
    secrets:
      TG_BOT_TOKEN:
        required: false

jobs:
  notify:
    permissions:
      contents: read
    steps:
      - name: Install a2a
        env:
          A2A_REF: ${{ inputs.a2a-ref }}
        run: |
          set -euo pipefail
          bin="$RUNNER_TEMP/a2a-notify-bin"
          mkdir -p "$bin"
          asset="a2a-linux-amd64"
          base="https://github.com/ydnikolaev/a2ahub/releases/download/$A2A_REF"
          for attempt in 1 2 3; do
            curl -fsSL -o "$RUNNER_TEMP/$asset" "$base/$asset"
            cosign verify-blob --bundle "$RUNNER_TEMP/$asset.cosign.bundle" "$RUNNER_TEMP/$asset"
            break
          done
          install -m 0755 "$RUNNER_TEMP/$asset" "$bin/a2a"
          echo "$bin" >>"$GITHUB_PATH"
YAML
}

teeth_expect() { # $1 = label, $2 = red|green, $3 = needle, $4 = reusable, $5 = caller, $6 = validate_reusable (optional)
  local label="$1" verdict="$2" needle="$3" reusable="$4" caller="$5" validate_reusable="${6:-}" out rc
  set +e
  out="$( (_GATE_ERRORS=0; run_check "$reusable" "$caller" "$validate_reusable"; gate_summary "notify-workflow-teeth") 2>&1)"
  rc=$?
  set -e
  if [ "$verdict" = "red" ]; then
    if [ "$rc" -eq 0 ] || ! printf '%s\n' "$out" | grep -Fq "$needle"; then
      echo "check-notify-workflow --teeth: FALSE GREEN — $label did not red with '$needle':" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-notify-workflow --teeth: $label reds"
  elif [ "$rc" -ne 0 ]; then
    echo "check-notify-workflow --teeth: FALSE RED — $label should green:" >&2
    echo "$out" >&2
    return 1
  else
    echo "check-notify-workflow --teeth: $label greens"
  fi
}

run_teeth() {
  local work good_reusable good_caller
  work="$(mktemp -d)" || return 1
  trap 'rm -rf -- "${work:-}"' EXIT

  good_reusable="$work/good-reusable.yml"; write_good_reusable "$good_reusable"
  good_caller="$work/good-caller.yml"; write_good_caller "$good_caller"
  teeth_expect "good pair (forbidden literals only in comments, dry-run: never counted as run:)" \
    green "" "$good_reusable" "$good_caller" || return 1

  # A TRAILING comment mentioning both forbidden literals (not just a
  # full-line one) must also green — the comment-strip is not anchored to
  # "the whole line is a comment", it strips '#'-to-EOL wherever it starts.
  local trailing_comment_reusable="$work/trailing-comment-reusable.yml"
  # Identical to the good fixture except one line grows a TRAILING comment
  # repeating both forbidden literals.
  sed 's|^      contents: read$|      contents: read  # never secrets: inherit, never pull_request_target|' "$good_reusable" >"$trailing_comment_reusable"
  teeth_expect "trailing (not full-line) comment mentioning both forbidden literals" \
    green "" "$trailing_comment_reusable" "$good_caller" || return 1

  # secrets: inherit — the realistic violation site is the CALLER's job
  # (GitHub Actions' own `secrets: inherit` directive is caller-side).
  local bad_secrets="$work/bad-secrets-caller.yml"
  sed 's/^    secrets:$/    secrets: inherit/; /^      TG_BOT_TOKEN:.*$/d' "$good_caller" >"$bad_secrets"
  teeth_expect "secrets: inherit in the caller" red \
    "sets \`secrets: inherit\`" "$good_reusable" "$bad_secrets" || return 1

  # pull_request_target as a real trigger (not a comment).
  local bad_trigger="$work/bad-trigger-caller.yml"
  sed 's/^  push:$/  pull_request_target:/' "$good_caller" >"$bad_trigger"
  teeth_expect "pull_request_target as a real trigger" red \
    "contains the literal \"pull_request_target\"" "$good_reusable" "$bad_trigger" || return 1

  # a run: step in the caller.
  local bad_run="$work/bad-run-caller.yml"
  { cat "$good_caller"; printf '%s\n' '      - name: leak' '        run: echo leaking a secret'; } >"$bad_run"
  teeth_expect "a run: step in the caller" red \
    "carries a \`run:\` step" "$good_reusable" "$bad_run" || return 1

  # permissions: grown beyond {contents: read}.
  local bad_perms="$work/bad-perms-reusable.yml"
  sed 's/^      contents: read$/      contents: read\n      id-token: write/' "$good_reusable" >"$bad_perms"
  teeth_expect "reusable permissions grown beyond contents: read" red \
    "permissions: block is not exactly {contents: read}" "$bad_perms" "$good_caller" || return 1

  # reusable on: grown beyond {workflow_call}.
  local bad_on="$work/bad-on-reusable.yml"
  sed 's/^on:$/on:\n  push:\n    branches: [main]/' "$good_reusable" >"$bad_on"
  teeth_expect "reusable on: grown beyond workflow_call" red \
    "on: block is not exactly {workflow_call}" "$bad_on" "$good_caller" || return 1

  # caller under-declared: workflow_dispatch silently dropped.
  local under_declared="$work/under-declared-caller.yml"
  awk '/^  workflow_dispatch:$/ { skip=1 } skip && /^permissions:$/ { skip=0 } !skip' "$good_caller" >"$under_declared"
  # A DELETED workflow must refuse, not skip. This is the gate's own scar
  # class: a check whose input has vanished and which reports ok is worse than
  # no check, because it reads as evidence. Spec 07 §6 asks for it by name and
  # the closing sweep found it was the one refusal path with no teeth case.
  teeth_expect "the reusable workflow is missing entirely" red \
    "missing reusable workflow" "$work/does-not-exist.yml" "$good_caller" || return 1

  teeth_expect "the template caller is missing entirely" red \
    "missing template caller" "$good_reusable" "$work/also-absent.yml" || return 1

  teeth_expect "caller missing workflow_dispatch" red \
    "on: block is not exactly {push, workflow_dispatch}" "$good_reusable" "$under_declared" || return 1

  # Invariant 5, clause 1: required: false + non-string type + no default:
  # cannot express "unset" — the exact shape that shipped the blocker.
  local bad_input_no_default good_input_string bad_desc_empty
  bad_input_no_default="$work/bad-input-no-default-reusable.yml"
  write_bad_input_no_default_reusable "$bad_input_no_default"
  good_input_string="$work/good-input-string-reusable.yml"
  write_good_input_string_reusable "$good_input_string"
  bad_desc_empty="$work/bad-desc-empty-reusable.yml"
  write_bad_desc_empty_reusable "$bad_desc_empty"

  teeth_expect "required: false + non-string type + no default: reds" red \
    "cannot express \"unset\"" "$bad_input_no_default" "$good_caller" || return 1

  teeth_expect "the same input fixed as type: string + default: '' greens" green "" \
    "$good_input_string" "$good_caller" || return 1

  # Invariant 5, clause 2: a description: claiming emptiness over a
  # non-string type reds on the description clause ALONE — clause 1's
  # own condition (no default:) is deliberately satisfied here (default: 5
  # is present), so a false green on clause 1 could not paper over this.
  teeth_expect "description: claims emptiness over type: number — reds on the description clause alone" red \
    "claiming emptiness" "$bad_desc_empty" "$good_caller" || return 1

  # Invariant 5's reach: the SAME bad input, planted in the OTHER reusable
  # workflow's slot (a2a-validate-reusable.yml), must also red — the audit
  # is closed over both files, not just the one that shipped the defect
  # (spec 07 T5 AC 10). The needle names the second file specifically, so
  # this case cannot pass off slot 1's (a2a-notify-reusable.yml's) output.
  teeth_expect "the bad input reaches a2a-validate-reusable.yml's slot too" red \
    "a2a-validate-reusable.yml's workflow_call input" \
    "$good_reusable" "$good_caller" "$bad_input_no_default" || return 1

  # ── invariant #6 (spec 08 §T5) ──────────────────────────────────────

  local remote_install download_verify download_no_verify verify_after_install
  local local_dogfood download_verify_trap verify_suppressed verify_inside_loop
  remote_install="$work/remote-install-reusable.yml"; write_reusable_remote_install "$remote_install"
  download_verify="$work/download-verify-reusable.yml"; write_reusable_download_verify "$download_verify"
  download_no_verify="$work/download-no-verify-reusable.yml"; write_reusable_download_no_verify "$download_no_verify"
  verify_after_install="$work/verify-after-install-reusable.yml"; write_reusable_verify_after_install "$verify_after_install"
  local_dogfood="$work/local-dogfood-reusable.yml"; write_reusable_local_dogfood "$local_dogfood"
  download_verify_trap="$work/download-verify-trap-reusable.yml"; write_reusable_download_verify_trap "$download_verify_trap"
  verify_suppressed="$work/verify-suppressed-reusable.yml"; write_reusable_verify_suppressed "$verify_suppressed"
  verify_inside_loop="$work/verify-inside-loop-reusable.yml"; write_reusable_verify_inside_loop "$verify_inside_loop"

  # #6a: the remote-module-ref install literal — RED, naming the file and
  # the literal (spec 08 §8 AC9).
  teeth_expect "go install of the remote module ref (today's actual defect)" red \
    'a2a-notify-reusable.yml contains "go install github.com/ydnikolaev/a2ahub/cmd/a2a@"' \
    "$remote_install" "$good_caller" || return 1

  # #6a/#6b: the correct download+verify+install block — GREEN.
  teeth_expect "download + cosign verify-blob + install (the correct shape)" green "" \
    "$download_verify" "$good_caller" || return 1

  # #6a: a2a-validate-reusable.yml's own LOCAL-path dogfood install — GREEN.
  # A rule that reddens the dogfood is the wrong rule (spec 08 §8 AC9).
  teeth_expect "validate's local-path dogfood go install" green "" \
    "$local_dogfood" "$good_caller" || return 1

  # #6b clause (a): downloads from releases/download/ with NO cosign
  # verify-blob call at all — RED.
  teeth_expect "download with no cosign verify-blob at all" red \
    "downloading from releases/download/ with no cosign verify-blob call" \
    "$download_no_verify" "$good_caller" || return 1

  # #6b ordering (F3): verifies, but AFTER install -m 0755 already made the
  # (then-unverified) asset executable — RED.
  teeth_expect "verifies AFTER install -m 0755 (ordering violated)" red \
    "precedes cosign verify-blob" \
    "$verify_after_install" "$good_caller" || return 1

  # #6b (b) trap: correct block, but the step is named "Install a2a", uses
  # sigstore/cosign-installer, and the header prose says "chmod +x" — three
  # forbidden-token substrings in a file that is otherwise correct. GREEN,
  # proving the predicate is line-anchored to a run: body command, not a
  # substring scan of the whole file (spec 08 §T5 "(b) needs a THIRD reader
  # shape").
  teeth_expect "correct block named Install a2a / cosign-installer / chmod prose (the substring trap)" green "" \
    "$download_verify_trap" "$good_caller" || return 1

  # #6b F1: cosign verify-blob's exit is swallowed by a trailing || — RED.
  # Not one of spec 08 §T5's four named fixtures; added because criterion 3
  # claims F1 is asserted by #6b (§11 records this as an addition, not a
  # deviation).
  teeth_expect "cosign verify-blob wrapped in || (F1: a verification that warns is not one)" red \
    "wrapped (||, set +e, or a captured status)" \
    "$verify_suppressed" "$good_caller" || return 1

  # #6b F2: cosign verify-blob runs INSIDE the retry loop — RED. Same
  # rationale as the F1 case above.
  teeth_expect "cosign verify-blob inside the retry loop (F2: must red once, not retry)" red \
    "sits INSIDE the retry loop" \
    "$verify_inside_loop" "$good_caller" || return 1

  # #6a's reach: the SAME remote-install fixture, planted in the OTHER
  # reusable workflow's slot (a2a-validate-reusable.yml) — RED, needle
  # naming the second file specifically (spec 08 §8 AC11).
  teeth_expect "the remote-install fixture reaches a2a-validate-reusable.yml's slot too" red \
    'a2a-validate-reusable.yml contains "go install github.com/ydnikolaev/a2ahub/cmd/a2a@"' \
    "$good_reusable" "$good_caller" "$remote_install" || return 1

  echo "check-notify-workflow --teeth: PASS — secrets: inherit, pull_request_target, a run: step, an over-wide trigger/permissions block, a MISSING workflow file, an input whose type/default/description cannot express \"unset\" (in either reusable workflow), and the remote-module-ref install (in either reusable workflow) all red; a download that never verifies and one that verifies too late also red; the comment-only + dry-run: good pair, the correct download+verify block, validate's local-path dogfood, and the Install-a2a/cosign-installer/chmod-prose trap all green"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
else
  run_check "$REUSABLE_DEFAULT" "$CALLER_DEFAULT"
  gate_summary "notify-workflow"
fi
