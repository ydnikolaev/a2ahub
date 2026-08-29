#!/usr/bin/env bash
# check-usage-workflow.sh — the usage-names-its-workflow gate
# (answers-that-hold-2026-08 P8, spec 08).
#
# THE UNIVERSE spec 08 §7 names, and this script derives it, never lists it
# (2026-08-29 WIDENING — see the spec's own amendment for why the original
# "verb name == manifest section id" rule was wrong: it yielded exactly
# {feedback, notify, notifications}, and the phase's own motivating incident,
# `a2a contract publish`, is not a section id, so it got no line). For a verb
# V drawn from the binary's own `a2a __catalog` roster:
#
#   { section id of every loopCorpus page whose text names `a2a V` }
#     UNION
#   { the section id equal to V, when one exists }
#
# V has a workflow obligation iff that set is non-empty. A verb whose set is
# empty carries NO line and this gate never checks it (spec AC-3). The set
# itself, and every (verb, topic) attribution the AST walk can prove, are
# both computed by internal/cli's own TestUsageWorkflowDump (Go, not bash —
# see below) and read off its dump; this script never lists a verb, a
# topic, or a page (AC-5).
#
# __catalog is excluded from the catalogue this gate reads (catalogue_verbs,
# below) even though it dispatches like any other verb: its own output is
# skill/a2ahub/reference/commands.md, a byte-for-byte committed artifact the
# skill-drift CI job regenerates and diffs (cmd/a2a/catalog.go's own doc
# comment), so a line added to it is not spec T1's "no behaviour changes" —
# it would red a DIFFERENT gate, outside this phase's own Footprint
# ("the usage and synopsis strings across internal/cli/*.go" — __catalog is
# implemented in cmd/a2a/catalog.go, not internal/cli). Filtering the `__`
# prefix is a naming-convention predicate, not a verb list.
#
# THREE SEPARATE CHECKS, all read off ONE dump (spec 08 §11's 2026-08-28
# amendment: "one traversal instead of a handoff across two phases"):
#
#   1. VERB-DRIVEN (US-2 / AC-2, AC-3): every verb with a non-empty topic
#      set (a SET line in the dump) must have at least one PAIR line naming
#      it. A verb outside the universe (no SET line) is never iterated here
#      — AC-3's own green case, not a gap.
#
#   2. PAIR-CORRECTNESS (new 2026-08-29 tooth): every PAIR line's topic must
#      be a MEMBER of that same verb's own SET — a topic that is a real,
#      valid manifest section for some OTHER verb still reds here, naming
#      both the verb and the wrong topic. This is what catches a
#      workflowLine call attributed to the wrong verb (a longest-match or
#      mechanism-2 bug) even when the topic itself is perfectly valid.
#
#   3. TOPIC-DRIVEN (US-2 / AC-4, and P9's absorbed AC-2 — spec 08 §11's
#      2026-08-29 amendment): every TOPIC line the walk finds ANYWHERE must
#      (a) be a real docs-manifest.json section id, and (b) be ACCEPTED by
#      the shipped `a2a docs <topic>` verb — asked of the real binary, never
#      a second manifest reader.
#
# The extraction itself cannot live in bash (spec 08 T1: "do not extract the
# usage strings by grepping for their prefix ... the extraction is an AST
# walk") — internal/cli/usageworkflow_dump_test.go's own TestUsageWorkflowDump
# is the real Go source analysis this gate asks (the check-render-ledger.sh
# precedent: "ask the program" via `go test -run`, not a `go run` of a bare
# main package). Its own doc comment carries the two attribution mechanisms
# (an anchored "usage: a2a V" scan, and a map-literal key for a table-driven
# command shared by several verbs) this gate's dump format exposes as PAIR
# lines.
#
# The dump is plain TSV-ish text, not JSON: this repo's gate scripts
# deliberately avoid a jq dependency (check-loop-reachability.sh's own
# convention), so a JSON-shaped contract bash would have to parse line-by-
# line anyway gains nothing over a format that already is line-oriented.
# Three record kinds, one per line:
#
#   TOPIC\t<topic>                    — one per usageWorkflowExtract entry
#   PAIR\t<verb>\t<topic>             — one per usageWorkflowExtractPairs entry
#   SET\t<verb>\t<topic1>,<topic2>,…  — one per non-empty derived topic set
#
# The verb catalogue and the `a2a docs` refusal are both asked of the REAL
# binary in EVERY mode, including --teeth (the check-human-gates.sh
# precedent: "a gate naming a fixed source of truth other than the shipped
# artifact stays green through exactly the drift it exists to catch"), and
# the docs-manifest.json + loop-corpus tree this gate's own topic-set
# derivation reads is likewise ALWAYS the real one, never synthetic under
# --teeth — only the SOURCE DIRECTORY the AST walk reads over is synthetic
# there.
#
# Usage: bash scripts/check-usage-workflow.sh            # check the real tree
#        bash scripts/check-usage-workflow.sh --teeth    # self-test on fixtures

# lane-inputs:
#   internal/cli/**
#   cmd/a2a/**
#   skill/a2ahub/docs-manifest.json
#   skill/a2ahub/loops.md
#   skill/a2ahub/loops/**
#   go.mod
#   go.sum
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   below self-locates scripts/lib/gate-lib.sh from this script's own path,
#   so the classifier cannot resolve the $(dirname ...) substitution to a
#   literal.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# a2a_bin runs the real a2a binary (A2A_VERIFY_BINARY, matching every other
# "ask the binary" gate's own convention) or falls back to `go run`. Both
# stdout and stderr are the caller's to interpret; only the exit code and
# stdout are used by this gate's own two call sites below.
a2a_bin() {
  if [ -n "${A2A_VERIFY_BINARY:-}" ]; then
    "$A2A_VERIFY_BINARY" "$@"
    return
  fi
  ( cd "$GATE_ROOT" && GOWORK=off go run ./cmd/a2a "$@" )
}

# catalogue_verbs prints every top-level dispatch verb name `a2a __catalog`
# reports, one per line, sorted and de-duplicated — never a list hand-typed
# in this gate (AC-5). A sub-verb row ("contract publish") is kept WHOLE:
# collapsing it to its first token ("contract") was the original defect
# (this gate's own git history) — "contract" is not a real dispatch verb,
# and collapsing produced a universe that missed every multi-word row
# entirely. `__catalog` itself is excluded (see this file's own header) by
# a naming-convention predicate, not by naming it.
catalogue_verbs() {
  local out
  if ! out="$(a2a_bin __catalog 2>&1)"; then
    return 1
  fi
  printf '%s\n' "$out" |
    awk '/^## Commands$/{f=1;next} /^## /{f=0} f' |
    grep -oE '^- `[^`]+`' |
    sed -E 's/^- `//; s/`$//' |
    grep -v '^__' |
    sort -u
}

# manifest_topics prints every section id in $1 (docs-manifest.json), one
# per line, sorted and de-duplicated. Read directly from the file, not the
# binary: it is a static repo artifact, not domain state the binary could
# drift from underneath this gate (the check-loop-reachability.sh
# precedent for this exact file). One JSON object per physical line — the
# shape docs-manifest.json actually uses.
manifest_topics() { # $1 = manifest path
  grep -oE '"id":"[^"]+"' "$1" | sed -E 's/^"id":"//; s/"$//' | sort -u
}

# usage_workflow_dump runs internal/cli's own TestUsageWorkflowDump over $2
# (an ABSOLUTE source directory — a fixture path under --teeth, or
# internal/cli itself for a real run), feeding it $1 (the real catalogue,
# newline-separated) so its topic-set derivation and pair attribution both
# run — and prints the dump verbatim (see this file's own header for its
# three record kinds). Fails closed: an unreadable/empty dump would let
# this gate pass while policing nothing.
usage_workflow_dump() { # $1 = catalogue verbs (newline-separated), $2 = absolute source dir
  local catalogue="$1" src_dir="$2"
  local dump
  dump="$(mktemp -d)/usage-workflow-dump.txt" || return 1
  if ! ( cd "$GATE_ROOT" && GOWORK=off USAGE_WORKFLOW_SRC_DIR="$src_dir" USAGE_WORKFLOW_CATALOGUE="$catalogue" USAGE_WORKFLOW_DUMP="$dump" \
      go test ./internal/cli/... -run '^TestUsageWorkflowDump$' -count=1 ) >&2; then
    return 1
  fi
  if [ ! -s "$dump" ]; then
    return 1
  fi
  cat "$dump"
  rm -f "$dump"
}

# run_check walks the derived universe, the attributed pairs, and the flat
# extracted topic set against $2 (the AST walk's own source directory,
# absolute) and reports.
# $1 = root to read cmd/a2a + skill/a2ahub/docs-manifest.json from
# (GATE_ROOT for a real run; still GATE_ROOT under --teeth — see this
# file's own header on why the manifest is never synthetic there).
run_check() { # $1 = root, $2 = absolute source dir for the AST walk
  local root="$1" src_dir="$2"
  local manifest="$root/skill/a2ahub/docs-manifest.json"

  if [ ! -f "$manifest" ]; then
    gate_fail "usage-workflow: $manifest does not exist"
    gate_summary "usage-workflow"
    return $?
  fi

  local verbs
  if ! verbs="$(catalogue_verbs)"; then
    gate_unmeasured "usage-workflow: could not read the verb catalogue from the binary (\`a2a __catalog\`) — failing closed rather than policing nothing"
    gate_summary "usage-workflow"
    return $?
  fi
  if [ -z "$verbs" ]; then
    gate_unmeasured "usage-workflow: the binary's catalogue reported no verbs — failing closed rather than policing nothing"
    gate_summary "usage-workflow"
    return $?
  fi

  local topics_manifest
  topics_manifest="$(manifest_topics "$manifest")"
  if [ -z "$topics_manifest" ]; then
    gate_fail "usage-workflow: $manifest declares no section id — failing closed rather than policing nothing"
    gate_summary "usage-workflow"
    return $?
  fi

  local dump
  if ! dump="$(usage_workflow_dump "$verbs" "$src_dir")"; then
    gate_unmeasured "usage-workflow: could not read the usage-workflow dump from internal/cli's own TestUsageWorkflowDump over $src_dir — failing closed rather than policing nothing"
    gate_summary "usage-workflow"
    return $?
  fi

  local extracted sets pairs
  extracted="$(printf '%s\n' "$dump" | awk -F'\t' '$1=="TOPIC"{print $2}' | sort -u)"
  sets="$(printf '%s\n' "$dump" | awk -F'\t' '$1=="SET"')"
  pairs="$(printf '%s\n' "$dump" | awk -F'\t' '$1=="PAIR"')"

  if [ -z "$sets" ]; then
    gate_unmeasured "usage-workflow: the derived universe (catalogue verbs whose docs-manifest.json topic set is non-empty) is empty — failing closed rather than policing nothing"
    gate_summary "usage-workflow"
    return $?
  fi

  # 1. VERB-DRIVEN (AC-2/AC-3): every verb with a non-empty topic set must
  # have at least one PAIR line naming it, wherever it was attributed from.
  local v topiclist
  while IFS=$'\t' read -r _ v topiclist; do
    [ -z "$v" ] && continue
    if ! printf '%s\n' "$pairs" | cut -f2 | grep -qxF "$v"; then
      gate_fail "usage-workflow: verb \"$v\" has a non-empty docs-manifest.json topic set {$topiclist}, but no usage output in $src_dir names its workflow via workflowLine(...) — the pointer has rotted or was never written"
    fi
  done <<<"$sets"

  # 2. PAIR-CORRECTNESS: every attributed pair's topic must be a member of
  # THAT SAME VERB's own topic set — a topic that is a real, valid manifest
  # section for some OTHER verb still reds here, naming both.
  local t own_set
  while IFS=$'\t' read -r _ v t; do
    [ -z "$v" ] && continue
    own_set="$(printf '%s\n' "$sets" | awk -F'\t' -v verb="$v" '$2==verb{print $3; exit}')"
    if [ -z "$own_set" ]; then
      gate_fail "usage-workflow: workflowLine(\"$t\") in $src_dir is attributed to verb \"$v\", which has no topic set at all (docs-manifest.json/the loop corpus name it no workflow)"
      continue
    fi
    if ! printf '%s\n' "$own_set" | tr ',' '\n' | grep -qxF "$t"; then
      gate_fail "usage-workflow: workflowLine(\"$t\") in $src_dir is attributed to verb \"$v\", but \"$t\" is not among $v's own topic set {$own_set} — a valid manifest topic named for the WRONG verb"
    fi
  done <<<"$pairs"

  # 3. TOPIC-DRIVEN: every topic the walk actually found must resolve to a
  # real manifest section id (AC-4) AND be accepted by the shipped
  # `a2a docs <topic>` verb (P9's absorbed AC-2).
  while IFS= read -r t; do
    [ -z "$t" ] && continue
    if ! printf '%s\n' "$topics_manifest" | grep -qxF "$t"; then
      gate_fail "usage-workflow: a workflow: line in $src_dir names topic \"$t\", which $manifest declares no section for"
      continue
    fi
    if ! a2a_bin docs "$t" >/dev/null 2>&1; then
      gate_fail "usage-workflow: a workflow: line in $src_dir names topic \"$t\" — docs-manifest.json declares that section, but \`a2a docs $t\` itself refuses it (P9's absorbed AC-2)"
    fi
  done <<<"$extracted"

  gate_summary "usage-workflow"
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || { echo "usage-workflow --teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN

  # copy_real_baseline replaces $tmp's contents with a COPY of the real
  # internal/cli/*.go non-test files (the same shape the AST walk itself
  # reads: no recursion, _test.go excluded). Using the real tree as the
  # "good" baseline — rather than a small synthetic universe — is the ONLY
  # way to exercise the real derived universe (51 verbs as of this writing,
  # not the old rule's 3) without hand-listing it a second time here, which
  # AC-5 forbids. Every mutation below starts from this baseline and either
  # DELETES a file (removing that file's own pairs) or ADDS one synthetic
  # file (introducing a new, isolated pair) — never a sed edit of a copied
  # real file, which would be fragile against the very source it is
  # rewriting.
  copy_real_baseline() {
    rm -f "$tmp"/*.go
    local f base
    for f in "$GATE_ROOT"/internal/cli/*.go; do
      base="$(basename "$f")"
      case "$base" in
        *_test.go) continue ;;
      esac
      cp "$f" "$tmp/$base"
    done
  }

  # --- Positive control -----------------------------------------------
  copy_real_baseline
  if ! (run_check "$GATE_ROOT" "$tmp") >/dev/null 2>&1; then
    echo "usage-workflow --teeth: FAILED — a copy of the real, fully-declared internal/cli tree stayed red" >&2
    return 1
  fi

  # --- AC-2/AC-3: a universe verb missing its own pair reds, naming it --
  copy_real_baseline
  rm -f "$tmp/cmd_notify.go"
  out="$(run_check "$GATE_ROOT" "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF 'verb "notify" has a non-empty docs-manifest.json topic set'; then
    echo "usage-workflow --teeth: FAILED (AC-2) — removing notify's own file did not red naming \"notify\" as missing its pair" >&2
    return 1
  fi

  # --- AC-4: a workflowLine call naming a topic docs-manifest.json ------
  # declares no section for must red naming that topic, without disturbing
  # any other verb's own satisfied state.
  copy_real_baseline
  cat >"$tmp/zzz_bogus_topic.go" <<'EOF'
package cli

func usageBogusTopic() string {
	return "some other message\n" + workflowLine("not-a-real-manifest-topic")
}
EOF
  out="$(run_check "$GATE_ROOT" "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF 'names topic "not-a-real-manifest-topic", which'; then
    echo "usage-workflow --teeth: FAILED (AC-4) — a workflowLine call naming a nonexistent manifest topic did not red naming it" >&2
    return 1
  fi
  if printf '%s\n' "$out" | grep -qF 'verb "feedback" has'; then
    echo "usage-workflow --teeth: FAILED (AC-4) — adding an extra bogus topic wrongly disturbed the real \"feedback\" verb's own satisfied state" >&2
    return 1
  fi

  # --- AC-6 negative control: an unresolvable dynamic argument must NOT --
  # be collected — proving this walk is real AST resolution, not a textual
  # match against a topic name that still appears in surrounding source.
  copy_real_baseline
  cat >"$tmp/zzz_dynamic_arg.go" <<'EOF'
package cli

func usageComputeTopic() string { return "feedback" }

func usageDynamicArg() string {
	return "some other message\n" + workflowLine(usageComputeTopic())
}
EOF
  out="$(run_check "$GATE_ROOT" "$tmp" 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ] || printf '%s\n' "$out" | grep -qF "FAIL "; then
    echo "usage-workflow --teeth: FAILED (AC-6 negative) — an unresolvable dynamic workflowLine argument wrongly disturbed the gate (exit $rc): $out" >&2
    return 1
  fi

  # --- Decoy-text anti-regression: a comment/string mentioning a topic ---
  # name OUTSIDE any workflowLine(...) call must not be picked up.
  copy_real_baseline
  cat >"$tmp/zzz_decoy.go" <<'EOF'
package cli

// A decoy: the literal text "workflow: notify" appears here but is NOT an
// argument to workflowLine(...), and must not be collected as a topic.
const usageDecoyComment = "workflow: notify — not a real call"
EOF
  out="$(run_check "$GATE_ROOT" "$tmp" 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ] || printf '%s\n' "$out" | grep -qF "FAIL "; then
    echo "usage-workflow --teeth: FAILED (decoy text) — a decoy string mentioning a topic name outside any workflowLine(...) call wrongly disturbed the gate (exit $rc): $out" >&2
    return 1
  fi

  # --- Longest-match trap (spec 08's own named hazard, exercised against --
  # a REAL catalogue collision: a2a verify is a byte-for-byte prefix of
  # a2a verify-pass/verify-fail). Deleting cmd_lifecycle.go removes every
  # verb ONLY implemented there (verify included — VerifyCommand lives in
  # that same file); the synthetic replacement below re-declares ONLY
  # verify-pass's own usage text. A boundary-blind matcher would credit
  # "verify" from it and go green for a verb whose real pair no longer
  # exists.
  copy_real_baseline
  rm -f "$tmp/cmd_lifecycle.go"
  cat >"$tmp/zzz_longest_match.go" <<'EOF'
package cli

func usageVerifyPassOnly() string {
	return "usage: a2a verify-pass <handoff-id>\n" + workflowLine("loop-send")
}
EOF
  out="$(run_check "$GATE_ROOT" "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF 'verb "verify" has a non-empty docs-manifest.json topic set'; then
    echo "usage-workflow --teeth: FAILED (longest-match) — \"verify\" was not reported missing once cmd_lifecycle.go's own pair was removed" >&2
    return 1
  fi
  if printf '%s\n' "$out" | grep -qF 'verb "verify-pass" has'; then
    echo "usage-workflow --teeth: FAILED (longest-match) — \"verify-pass\"'s own usage text was wrongly credited to \"verify\" instead (a boundary-blind longest match)" >&2
    return 1
  fi

  # --- Pair-correctness tooth: a topic that is a real, valid manifest ---
  # section — but NOT a member of the attributed verb's OWN topic set —
  # must red, naming both, without tripping the AC-4 "no such section"
  # branch (that branch is for a topic docs-manifest.json does not declare
  # AT ALL; this one exists and is simply the wrong verb's).
  copy_real_baseline
  rm -f "$tmp/cmd_serve.go"
  cat >"$tmp/zzz_out_of_set.go" <<'EOF'
package cli

func usageServeWrongTopic() string {
	return "usage: a2a serve [--listen 127.0.0.1:8765]\n" + workflowLine("loop-send")
}
EOF
  out="$(run_check "$GATE_ROOT" "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF 'attributed to verb "serve", but "loop-send" is not among'; then
    echo "usage-workflow --teeth: FAILED (pair-correctness) — a real, valid manifest topic outside serve's own topic set did not red naming both" >&2
    return 1
  fi
  if printf '%s\n' "$out" | grep -qF 'which .* declares no section for'; then
    echo "usage-workflow --teeth: FAILED (pair-correctness) — the out-of-set topic wrongly tripped the AC-4 \"no such section\" branch instead of pair-correctness" >&2
    return 1
  fi

  # --- Mechanism 2 tooth: a map-literal-keyed pair is pair-correctness- --
  # checked exactly like a mechanism-1 pair — proving mechanism 2 is not
  # inert. ack's own real topic set is {loop-contract-change, loop-receive};
  # loop-send is valid for OTHER verbs but not for ack.
  copy_real_baseline
  rm -f "$tmp/cmd_lifecycle.go"
  cat >"$tmp/zzz_mechanism2.go" <<'EOF'
package cli

var zzzLifecycleWorkflowLines = map[string]string{
	"ack": workflowLine("loop-send"),
}
EOF
  out="$(run_check "$GATE_ROOT" "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF 'attributed to verb "ack", but "loop-send" is not among'; then
    echo "usage-workflow --teeth: FAILED (mechanism 2) — a map-literal-keyed pair naming a topic outside its own verb's set did not red" >&2
    return 1
  fi

  # --- Missing manifest must red naming it ------------------------------
  local missing_root
  missing_root="$(mktemp -d)" || { echo "usage-workflow --teeth: mktemp failed" >&2; return 1; }
  mkdir -p "$missing_root/skill/a2ahub"
  copy_real_baseline
  out="$(run_check "$missing_root" "$tmp" 2>&1 || true)"
  rm -rf "$missing_root"
  if ! printf '%s\n' "$out" | grep -qF "does not exist"; then
    echo "usage-workflow --teeth: FAILED — a missing docs-manifest.json did not red naming it" >&2
    return 1
  fi

  echo "usage-workflow --teeth: ok"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

run_check "$GATE_ROOT" "$GATE_ROOT/internal/cli"
exit $?
