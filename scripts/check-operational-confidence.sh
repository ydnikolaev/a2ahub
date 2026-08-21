#!/usr/bin/env bash
# P0/W0 static guard: normative IDs, mandatory dispatch edges, stable error
# history, published-v1 immutability and the approved package/I/O boundary.

# lane-inputs:
#   schemas/errors/v1/history.tsv
#   schemas/errors/v1/registry.yaml
#   schemas/published-v1.sha256
#   schemas/**/v1/*.schema.json
#   internal/validate/**/*.go
#   internal/cli/**/*.go
#   !**/*_test.go
#   docs/the-plan/plan/**
#   docs/features/active/operational-confidence-2026-08/**
#   docs/decisions.md
#   AGENTS.md
# lane-reads-opaque: every real read is behind a function-parameter variable,
#   not a literal path. `check_history()` (line ~83) takes $history/$registry
#   and greps/awks them — run_all (line ~159) calls it with
#   "$ROOT/schemas/errors/v1/history.tsv" and "$ROOT/schemas/errors/v1/
#   registry.yaml". `check_hashes()` (line ~111) takes $manifest, called with
#   "$ROOT/schemas/published-v1.sha256". `check_dispatch()` (line ~133)
#   builds $tracker as "$root/docs/features/active/operational-confidence-
#   2026-08/tracker.yaml" and greps/seds it. Each resolves, at call time, to
#   a path the lane-inputs above already declare.
set -uo pipefail

# SELF-LOCATION USES BASH BUILTINS, NOT coreutils, AND THAT IS LOAD-BEARING.
#
# This gate's own tooth runs it under a MINIMAL PATH to prove that an absent
# ripgrep reports UNMEASURED rather than a corpus verdict. That PATH has no
# `dirname` and no `basename` either — so a script that shells out to locate
# itself cannot even source gate-lib.sh, and `gate_unmeasured` is then an
# undefined command. The tooth failed exactly that way in the container on
# 2026-08-21: "gate_unmeasured: command not found", i.e. the refusal this gate
# was taught to make could not be made, in the one situation it exists for.
#
# Found by ci-parity-docker, not by the host: macOS resolved the two utilities
# from a path the minimal set still carried. Parameter expansion has no such
# dependency.
_self_dir="${BASH_SOURCE[0]%/*}"
[ "$_self_dir" = "${BASH_SOURCE[0]}" ] && _self_dir="."
ROOT="$(cd "$_self_dir/.." && pwd -P)"
SCRIPT_ABS="$(cd "$_self_dir" && pwd -P)/${BASH_SOURCE[0]##*/}"
ERRORS=0
UNMEASURED=0

# shellcheck source=scripts/lib/gate-lib.sh
source "$_self_dir/lib/gate-lib.sh"

fail() { echo "operational-confidence-guard: FAIL — $*" >&2; ERRORS=$((ERRORS + 1)); }

# unmeasured is fail's sibling for the other half of this gate's history: the
# times it could not READ the thing it judges. Every finding below is a claim
# about a document — an id defined twice, a code emitted but unregistered,
# frozen bytes moved. None of those claims is available when the reader
# refused to run, and announcing one anyway is what put private CI in the
# state described above the ripgrep preflight.
#
# The text comes from gate-lib so the word a reader greps for is the same in
# every gate; the counter is local because this script owns its own exit
# path (run_all), and gate_summary's tally is not what it prints.
unmeasured() { gate_unmeasured "operational-confidence-guard: $*"; UNMEASURED=$((UNMEASURED + 1)); }

# require_marker asks whether FILE contains PATTERN and keeps the three
# possible answers apart: it is there, it is not there (a finding, with the
# caller's own domain wording), or the file could not be read at all — which
# is not evidence about its contents in either direction.
require_marker() { # $1 = pattern, $2 = file, $3 = domain message when genuinely absent
  local rc
  rg -q "$1" "$2"
  rc=$?
  case "$rc" in
    0) return 0 ;;
    1) fail "$3"; return 1 ;;
    *) unmeasured "could not read $2 while looking for a required marker (rg exit $rc) — this run did not judge that file, so its verdict is missing rather than negative"; return 1 ;;
  esac
}

# This gate is written against ripgrep, and ripgrep is NOT part of a bare
# GitHub-hosted runner. Without this preflight the absence was silent: every
# `rg -q ... || fail` fired, and `definition_ids` swallowed the "command not
# found" through its own `2>/dev/null`, so the gate reported that each of
# twenty-seven normative ids "must have exactly one normative definition (found
# 0)". Twenty-seven confident, specific, wrong findings — about a plan file that
# was present and correct.
#
# That is worse than a crash: it accuses the documents. The private repository's
# CI was red on it for forty consecutive runs across two days, and releases were
# cut straight through, because the failure looked like a documentation problem
# nobody had time for rather than a missing binary.
#
# A gate states its own preconditions or it cannot be trusted when it speaks.
if ! command -v rg > /dev/null 2>&1; then
  gate_unmeasured "operational-confidence-guard: ripgrep (rg) is required and not on PATH, so NOTHING below was measured — this is a fact about the runner, not about the documents. Install it: brew install ripgrep · apt-get install -y ripgrep"
  exit "$GATE_EXIT_UNMEASURED"
fi

definition_ids() {
  rg --no-filename -o '^\| (R|D|OP|CC)-[0-9]+ |^\*\*US-[0-9]+\*\*|^- AC-[0-9]+\.[0-9]+' "$1" \
    | sed -E 's/^\| //; s/ \|$//; s/^\*\*//; s/\*\*$//; s/^- //; s/[[:space:]]+$//'
}

check_ids() {
  local plan_root="$1" id count duplicates total
  # A gate that scans nothing must FAIL, not pass — and must say so as one
  # finding about the scan rather than N findings about the documents. The
  # per-id loop below is only meaningful once the extractor has produced
  # something; when it produces nothing, every id "found 0" and the twenty-seven
  # resulting messages all point at the plan instead of at the extractor.
  total="$(definition_ids "$plan_root" | grep -c . || true)"
  if [ "$total" -eq 0 ]; then
    unmeasured "the id extractor produced NOTHING from $plan_root — the plan is not the suspect here, the scan is, and every per-id finding this function would otherwise print would be an accusation against a document nobody read"
    return
  fi
  duplicates="$(definition_ids "$plan_root" | sort | uniq -d)"
  [ -z "$duplicates" ] || fail "duplicate normative definition id(s): $(printf '%s' "$duplicates" | tr '\n' ' ')"

  for id in R-023 R-024 R-025 R-026 D-032 D-033 D-034 D-035 OP-224 OP-225 OP-226 \
            US-1201 US-1202 US-1203 US-1204 US-1205 \
            AC-1201.1 AC-1201.2 AC-1202.1 AC-1202.2 AC-1203.1 AC-1203.2 AC-1203.3 \
            AC-1204.1 AC-1204.2 AC-1205.1 AC-1205.2; do
    count="$(definition_ids "$plan_root" | grep -Fxc "$id" || true)"
    [ "$count" -eq 1 ] || fail "$id must have exactly one normative definition (found $count)"
  done

  for evidence in E2E-OC-1 E2E-OC-2 E2E-OC-3 E2E-OC-4 E2E-OC-5 E2E-OC-6 E2E-OC-7 E2E-OC-8 E2E-OC-9; do
    require_marker "\\b${evidence}\\b" "$plan_root/13-testing.md" "$evidence missing from §13"
    require_marker "\\b${evidence}\\b" "$plan_root/14-us-ac.md" "$evidence has no §14 acceptance link"
  done
}

registry_codes() { sed -n 's/^[[:space:]]*- code:[[:space:]]*//p' "$1"; }

check_history() {
  local history="$1" registry="$2" source_root="$3" code status owner meaning introduced retired
  local duplicates registry_list emitted emit_rc emit_err
  # Neither file readable means none of the row-level findings below is
  # available: each of them is an assertion ABOUT a line in these files.
  if [ ! -r "$history" ] || [ ! -r "$registry" ]; then
    unmeasured "cannot read the error history ($history) or the live registry ($registry) — every row-level finding below would be an assertion about a file this run never opened"
    return
  fi
  duplicates="$(awk -F '\t' '!/^#/ && NF {print $1}' "$history" | sort | uniq -d)"
  [ -z "$duplicates" ] || fail "duplicate error-history code(s): $(printf '%s' "$duplicates" | tr '\n' ' ')"
  registry_list="$(registry_codes "$registry")"

  while IFS=$'\t' read -r code status owner meaning introduced retired; do
    case "$code" in ''|'#'*) continue ;; esac
    case "$status" in
      live) printf '%s\n' "$registry_list" | grep -Fqx "$code" || fail "$code is live in history but absent from registry" ;;
      reserved|tombstone) printf '%s\n' "$registry_list" | grep -Fqx "$code" && fail "$code is $status but appears in live registry" ;;
      *) fail "$code has unknown history status '$status'" ;;
    esac
    [ -n "$owner" ] && [ -n "$meaning" ] && [ -n "$introduced" ] || fail "$code history row is incomplete"
  done < "$history"

  for code in POL-011 POL-012 POL-013 POL-014 POL-015 POL-016 LFC-003 REF-014 REF-015 REF-016; do
    grep -q "^${code}"$'\t' "$history" || fail "$code missing from stable error history"
  done

  # The emission scan is the half of this function that can only ever produce
  # findings by FINDING something, so a scan that does not run is a silent
  # green: no output, no complaint, an obligation unpoliced. The stderr is
  # kept and the status is read (1 is "no matches", which is an answer; above
  # that is "I could not look", which is not).
  emit_err="$(mktemp)" || { unmeasured "no scratch file for the live-source emission scan"; return; }
  emitted="$(rg --no-filename --glob '*.go' --glob '!*_test.go' -o '"(REF|LFC|POL)-[0-9]{3}"' "$source_root/internal/validate" "$source_root/internal/cli" 2>"$emit_err" | tr -d '"' | sort -u)"
  emit_rc=$?
  if [ "$emit_rc" -gt 1 ]; then
    unmeasured "the live-source emission scan did not run under $source_root (rg exit $emit_rc: $(tr '\n' ' ' <"$emit_err")) — no Go source was read, so 'every emitted code is registered' is unproven rather than true"
    rm -f "$emit_err"
    return
  fi
  rm -f "$emit_err"
  while IFS= read -r code; do
    [ -z "$code" ] && continue
    printf '%s\n' "$registry_list" | grep -Fqx "$code" || fail "live Go source emits unregistered $code"
  done <<< "$emitted"
}

# teach_frozen_schema prints the sanctioned alternative beside the refusal.
#
# The refusal alone ("bytes changed") tells an implementer they are wrong and
# not what to do instead, and the answer has cost this repo twice: agent-exchange
# P5 worked it out for `capabilities`, left it in its own phase plan, and
# space-notify P1 hit the identical wall for `notification_routes` months later
# because a private plan file of another epic is not somewhere anyone looks.
# The convention this repo already applies to lane declarations — the refusal
# teaches at the moment you need it — applies here for the same reason.
teach_frozen_schema() {
  local path="$1"
  cat >&2 <<EOF

  ── how to add a field to a frozen v1 schema ───────────────────────────────
  You do not. \`$path\` is one of the paths schemas/published-v1.sha256
  freezes, because its bytes decide whether documents ALREADY COMMITTED to
  shared spaces are valid — so an in-place edit is a compatibility event
  however additive it looks (docs/the-plan/plan/05-schemas.md §5.7.1).

  The sanctioned move (ADR-018, root AGENTS.md §Anti-patterns #21):

    1. Leave the schema at its published bytes. It is already
       \`additionalProperties: true\`, so every deployed binary accepts your
       new key today — no release, no min_binary_version bump, no pinned
       workflow bump, no \`x_\` prefix.
    2. Constrain it with a POLICY check in internal/validate — required
       fields, enums, patterns, bounds, unknown keys, all of it. That layer
       is not frozen, and V3 runs it on every pull request, so a malformed
       value is still refused by the space's own gate.
    3. Expect the refusals to be policy-class, which means the schema-only
       fixture corpus cannot express them. Prove them in package tests; the
       missing policy-fixture corpus is a row in docs/backlog.md.

  Minting a new schema generation to type a few keys is ruled out — twice,
  independently, on the migration it would force. If you believe your case is
  the exception, that is an ADR, not a hand-edit of the hash manifest.
  ───────────────────────────────────────────────────────────────────────────
EOF
}

check_hashes() {
  local root="$1" manifest="$2" expected current path listed actual find_rc
  # "published v1 bytes changed" is the most expensive sentence this gate can
  # print — it stops a release and sends the reader to ADR-018. It must never
  # be what a missing hashing tool or an unreadable manifest looks like.
  if ! command -v shasum > /dev/null 2>&1; then
    unmeasured "shasum is not on PATH, so the published-v1 byte freeze was NOT verified this run — whether those bytes changed is unknown, not answered"
    return
  fi
  if [ ! -r "$manifest" ]; then
    unmeasured "cannot read the freeze manifest $manifest — the ratchet was not checked, and an unread manifest freezes nothing"
    return
  fi
  while read -r expected path; do
    [ -n "$expected" ] && [ -n "$path" ] || continue
    [ -f "$root/$path" ] || { fail "published-v1 manifest path missing: $path"; continue; }
    actual="$(shasum -a 256 "$root/$path" | awk '{print $1}')"
    if [ -z "$actual" ]; then
      unmeasured "could not hash $path — this run cannot say whether its bytes changed"
      continue
    fi
    [ "$actual" = "$expected" ] || { fail "published v1 bytes changed: $path"; teach_frozen_schema "$path"; }
  done < "$manifest"

  # The ratchet covers SCHEMAS, and deliberately not the authoring templates
  # that live beside them.
  #
  # A schema is a wire contract: its bytes decide whether documents already
  # committed to a shared space are valid, so changing them retroactively is a
  # compatibility event and the freeze is exactly right.
  #
  # A template is prose with placeholders. Nothing validates against it, no
  # contract digest covers it, and the one runtime dependence — submitIsPlaceholder
  # in internal/cli/cmd_submit.go — matches the `<...>` SHAPE, never the text
  # inside. Freezing it protected documentation with a wire-contract mechanism,
  # and the bill came due: eight templates shipped the line
  # `actor: {kind: agent, name: <agent-name>, ...}`, an agent filled in a name
  # that was false, a live space recorded it permanently — and the fix was
  # unreachable without minting an envelope/v2 for types that need no v2.
  #
  # What templates get instead is a STRONGER guard for what they actually risk.
  # The byte freeze catches CHANGE, which for prose is the work, not the danger.
  # TestAuthoringPagesMatchTheTemplatesTheyDocument (internal/e2e) catches
  # DRIFT — the agent-facing page disagreeing with what the binary renders —
  # which is the failure that actually costs something.
  current="$(find "$root/schemas" -type f -path '*/v1/*.schema.json' -print | sed "s#^$root/##" | sort)"
  find_rc=$?
  if [ "$find_rc" -ne 0 ] || [ -z "$current" ]; then
    unmeasured "the v1 schema walk under $root/schemas found nothing (exit $find_rc) — comparing that empty result against the manifest would accuse the manifest of not covering schemas this run never enumerated"
    return
  fi
  listed="$(awk '{print $2}' "$manifest" | sort)"
  [ "$current" = "$listed" ] || fail "published-v1 manifest does not exactly cover every v1 schema"
}

phase_record() {
  local tracker="$1" target="$2"
  awk -v target="$target" '
    /^[[:space:]]*-[[:space:]]+id:/ { active=($3==target); next }
    active && /^[[:space:]]*blocked_by:/ { print; exit }
  ' "$tracker"
}

check_dispatch() {
  local root="$1" tracker p plan
  tracker="$root/docs/features/active/operational-confidence-2026-08/tracker.yaml"
  if [ ! -r "$tracker" ]; then
    unmeasured "cannot read $tracker — the dispatch DAG was not judged, and 'the tracker is missing mandatory P0' is not what an unreadable tracker means"
    return
  fi
  grep -q '^[[:space:]]*- id: P0$' "$tracker" || fail "tracker is missing mandatory P0"
  for p in P1 P4; do
    phase_record "$tracker" "$p" | grep -Eq 'blocked_by:.*P0' || fail "$p must be blocked_by P0"
  done
  while IFS= read -r plan; do
    [ -f "$root/docs/features/active/operational-confidence-2026-08/$plan" ] || fail "tracker plan path missing: $plan"
  done < <(sed -n 's/^[[:space:]]*plan:[[:space:]]*//p' "$tracker")
  require_marker 'lead owns final wiring' "$root/docs/features/active/operational-confidence-2026-08/plan.md" "shared wiring has no sequencing owner"
  require_marker 'reconciled before assigning P1D' "$root/docs/features/active/operational-confidence-2026-08/plans/01-producer-evaluation-receipts.plan.md" "P1 cache/HTML overlap has no serialization guard"
}

check_boundaries() {
  local root="$1"
  for token in 'internal/workreport/' 'internal/operational/' 'internal/contract/' 'internal/localserver/'; do
    require_marker "$token" "$root/docs/decisions.md" "ADR-001 missing $token"
  done
  for token in workreport operational contract localserver; do
    require_marker "$token" "$root/AGENTS.md" "project architecture rail missing $token"
  done
  require_marker 'sole loopback' "$root/AGENTS.md" "localserver loopback exception is not bounded"
  require_marker 'disposable local I/O remains authorized' "$root/AGENTS.md" "existing disposable local-I/O exceptions are not preserved"
}

run_all() {
  check_history "$ROOT/schemas/errors/v1/history.tsv" "$ROOT/schemas/errors/v1/registry.yaml" "$ROOT"
  check_hashes "$ROOT" "$ROOT/schemas/published-v1.sha256"
  if [ -d "$ROOT/docs/the-plan/plan" ]; then
    check_ids "$ROOT/docs/the-plan/plan"
  else
    echo "operational-confidence-guard: note — private normative plan absent; product history/hash checks remain active."
  fi
  if [ -f "$ROOT/docs/features/active/operational-confidence-2026-08/tracker.yaml" ]; then
    check_dispatch "$ROOT"
  fi
  if [ -f "$ROOT/docs/decisions.md" ] && [ -f "$ROOT/AGENTS.md" ]; then
    check_boundaries "$ROOT"
  fi
  # UNMEASURED DOMINATES, exactly as gate_summary does it for the gates that
  # exit through the shared helper. Both outcomes are red, so no CI verdict
  # moves; what the status answers is "is this verdict complete?", and one
  # measurement that did not happen makes the answer no. Letting the measured
  # errors outrank it would hide the incomplete half behind the judged one,
  # which is this defect class in one line.
  if [ "$UNMEASURED" -ne 0 ]; then
    echo "operational-confidence-guard: ✗ $UNMEASURED unmeasured, $ERRORS error(s) — this run could not measure everything it judges, so its verdict is incomplete." >&2
    exit "$GATE_EXIT_UNMEASURED"
  fi
  if [ "$ERRORS" -ne 0 ]; then exit 1; fi
  echo "operational-confidence-guard: OK — IDs/traceability, dispatch DAG, error history, published-v1 bytes and boundaries are coherent."
}

# expect_red — the thing is genuinely wrong, and the gate says so IN ITS OWN
# DOMAIN WORDS. The extra assertion is that the refusal carries no UNMEASURED
# marker: a finding reported as a failed measurement sends the reader to check
# the runner instead of the document, which is the same substitution as the
# reverse and just as wrong.
TEETH_LAST_OUT=""
expect_red() {
  local label="$1" needle="$2"; shift 2
  local out rc
  out="$( (ERRORS=0; UNMEASURED=0; "$@"; [ "$ERRORS" -eq 0 ] && [ "$UNMEASURED" -eq 0 ]) 2>&1)"; rc=$?
  TEETH_LAST_OUT="$out"
  [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "$needle" || { echo "operational-confidence-guard --teeth: $label did not red on '$needle'"; echo "$out"; exit 1; }
  if printf '%s' "$out" | grep -q "UNMEASURED"; then
    echo "operational-confidence-guard --teeth: $label is a real finding about a document and must not be reported as an unmeasured run"; echo "$out"; exit 1
  fi
}

# expect_unmeasured — the documents are FINE and the reader could not run.
#
# This is the half the gate did not have. Every one of the checks below can
# only produce findings by successfully reading something, so a reader that
# refuses produces either silence (an unearned green) or, worse, the
# confident and specific accusations that kept private CI red for forty runs
# across two days while releases were cut straight through them.
expect_unmeasured() {
  local label="$1" needle="$2"; shift 2
  local out rc
  out="$( (ERRORS=0; UNMEASURED=0; "$@"; [ "$UNMEASURED" -eq 0 ]) 2>&1)"; rc=$?
  TEETH_LAST_OUT="$out"
  if [ "$rc" -eq 0 ]; then
    echo "operational-confidence-guard --teeth: $label — wanted a recorded failed measurement, got UNMEASURED=0 and the output below; a check that could not run must never look like a check that passed"; echo "$out"; exit 1
  fi
  if ! printf '%s' "$out" | grep -q "UNMEASURED"; then
    echo "operational-confidence-guard --teeth: $label did not use the measurement refusal — a reader that cannot run must say so in the word every gate uses for it"; echo "$out"; exit 1
  fi
  if ! printf '%s' "$out" | grep -q "$needle"; then
    echo "operational-confidence-guard --teeth: $label refused without naming '$needle'"; echo "$out"; exit 1
  fi
}

# path_without_rg returns this process's PATH with every directory that holds
# an executable ripgrep removed — the bare-runner fixture, built from the
# machine it runs on rather than from a guessed list of directories.
path_without_rg() {
  local dir out="" IFS=:
  for dir in $PATH; do
    [ -n "$dir" ] || continue
    [ -x "$dir/rg" ] && continue
    out="${out:+$out:}$dir"
  done
  printf '%s' "$out"
}

# A fixture built by COPYING a corpus is only buildable where that corpus
# exists, and two of the four below copy PRIVATE trees — `docs/the-plan/plan`
# and `docs/features/**` are both stripped at publish. `run_all` above already
# presence-gates each of them at its call site; `run_teeth` did not, so in a
# public checkout the `cp -R` copied nothing, `check_ids` reported all 27
# normative ids "found 0", and `_harness-check` exited 1.
#
# That is not hypothetical. Public `main` was RED on it at f2a0b54e (run
# 31682736134) while the candidate ref carrying the identical tree was GREEN —
# because the ceiling is `verify.sh full` and the teeth are `verify.sh
# harness`, so only the derived lane reaches them, and only the lane runs on a
# push to main.
#
# It is also the SIXTH instance of one class: a check whose claim silently
# changes meaning when the private half of the tree is absent. The fix each
# time is the same — the presence gate belongs at the fixture, named, so a
# reader sees which teeth did not run instead of assuming all of them did.
teeth_skipped=""

# corpus_present <path> <label> — true when the fixture's source tree is here.
# Records the label when it is not, so the summary line cannot claim a
# fixture that never ran.
corpus_present() {
  local path="$1" label="$2"
  if [ -e "$ROOT/$path" ]; then return 0; fi
  teeth_skipped="${teeth_skipped:+$teeth_skipped, }$label"
  echo "operational-confidence-guard --teeth: skip $label — $path absent (public checkout); the fixture is a copy of it"
  return 1
}

run_teeth() {
  local tmp path
  tmp="$(mktemp -d)" || exit 1
  trap "rm -rf '$tmp'" EXIT

  if corpus_present docs/the-plan/plan duplicate-id; then
    mkdir -p "$tmp/ids"; cp -R "$ROOT/docs/the-plan/plan/." "$tmp/ids/"
    printf '\n| R-023 | duplicate | x |\n' >> "$tmp/ids/01-vision.md"
    expect_red duplicate-id 'duplicate normative' check_ids "$tmp/ids"
  fi

  mkdir -p "$tmp/history/internal/validate" "$tmp/history/internal/cli"
  cp "$ROOT/schemas/errors/v1/history.tsv" "$tmp/history/history.tsv"
  cp "$ROOT/schemas/errors/v1/registry.yaml" "$tmp/history/registry.yaml"
  printf 'package x\nvar _ = "POL-099"\n' > "$tmp/history/internal/validate/x.go"
  expect_red error-history 'unregistered POL-099' check_history "$tmp/history/history.tsv" "$tmp/history/registry.yaml" "$tmp/history"

  mkdir -p "$tmp/hash"
  while read -r _ path; do mkdir -p "$tmp/hash/$(dirname "$path")"; cp "$ROOT/$path" "$tmp/hash/$path"; done < "$ROOT/schemas/published-v1.sha256"
  cp "$ROOT/schemas/published-v1.sha256" "$tmp/hash/manifest"
  printf '\nseeded mutation\n' >> "$tmp/hash/schemas/envelope/v1/base.schema.json"
  expect_red v1-immutability 'published v1 bytes changed' check_hashes "$tmp/hash" "$tmp/hash/manifest"
  local v1_mutation_out="$TEETH_LAST_OUT"

  if corpus_present docs/features/active/operational-confidence-2026-08 mandatory-dag; then
    mkdir -p "$tmp/dispatch/docs/features/active"
    cp -R "$ROOT/docs/features/active/operational-confidence-2026-08" "$tmp/dispatch/docs/features/active/"
    sed -i.bak '/- id: P1/,/- id: P2/ s/blocked_by: \[P0\]/blocked_by: []/' "$tmp/dispatch/docs/features/active/operational-confidence-2026-08/tracker.yaml"
    expect_red mandatory-dag 'P1 must be blocked_by P0' check_dispatch "$tmp/dispatch"
  fi

  # ── the other half: the documents are fine, the reader is not ─────────
  #
  # Ta above and Tb here are asserted to DIFFER, not merely to be red. Either
  # assertion alone is satisfied by the bug: a failed measurement wearing a
  # finding's words is red and specific and names the needle, and that is
  # precisely what forty consecutive red CI runs looked like.
  local ta_out
  ta_out="$v1_mutation_out"

  mkdir -p "$tmp/empty-plan"
  expect_unmeasured empty-corpus 'the id extractor produced NOTHING' check_ids "$tmp/empty-plan"

  mkdir -p "$tmp/no-go-source"
  cp "$ROOT/schemas/errors/v1/history.tsv" "$tmp/no-go-source/history.tsv"
  cp "$ROOT/schemas/errors/v1/registry.yaml" "$tmp/no-go-source/registry.yaml"
  expect_unmeasured absent-source-tree 'the live-source emission scan did not run' \
    check_history "$tmp/no-go-source/history.tsv" "$tmp/no-go-source/registry.yaml" "$tmp/no-go-source"
  local tb_out="$TEETH_LAST_OUT"

  if [ "$ta_out" = "$tb_out" ]; then
    echo "operational-confidence-guard --teeth: a measured finding and a failed measurement produced the SAME output — one verdict is masquerading as the other"; echo "$ta_out"; exit 1
  fi
  if printf '%s' "$ta_out" | grep -q "UNMEASURED"; then
    echo "operational-confidence-guard --teeth: the v1-immutability finding was reported as an unmeasured run"; exit 1
  fi

  # The incident itself: ripgrep absent from a bare runner. Run as a whole
  # process, because the preflight is what the process does before any check
  # exists, and its exit status is part of the contract — a caller must be
  # able to tell "the corpus is wrong" from "this runner cannot judge it"
  # without reading prose.
  local nopath_out nopath_rc
  nopath_out="$(PATH="$(path_without_rg)" "$BASH" "$SCRIPT_ABS" check 2>&1)"
  nopath_rc=$?
  if [ "$nopath_rc" -eq 0 ]; then
    echo "operational-confidence-guard --teeth: a runner with no ripgrep exited GREEN"; echo "$nopath_out"; exit 1
  fi
  if [ "$nopath_rc" -eq 1 ]; then
    echo "operational-confidence-guard --teeth: a runner with no ripgrep exited 1, which is indistinguishable from a measured failure of the corpus"; echo "$nopath_out"; exit 1
  fi
  if [ "$nopath_rc" -ne "$GATE_EXIT_UNMEASURED" ]; then
    echo "operational-confidence-guard --teeth: a runner with no ripgrep exited $nopath_rc, wanted $GATE_EXIT_UNMEASURED"; echo "$nopath_out"; exit 1
  fi
  if ! printf '%s' "$nopath_out" | grep -q "UNMEASURED"; then
    echo "operational-confidence-guard --teeth: the missing-ripgrep refusal did not carry the measurement marker"; echo "$nopath_out"; exit 1
  fi
  if printf '%s' "$nopath_out" | grep -q "must have exactly one normative definition"; then
    echo "operational-confidence-guard --teeth: a missing search tool still accused the documents"; echo "$nopath_out"; exit 1
  fi

  # The two fixtures that always build — their corpora are product, not
  # planning — are named separately from the two that may not, so a green line
  # here says what was actually proven rather than what the file contains.
  if [ -n "$teeth_skipped" ]; then
    echo "✓ operational-confidence-guard --teeth: unknown live code and v1 mutation red in domain words; an empty id corpus, an absent Go source tree and a runner without ripgrep all refuse AS UNMEASURED (exit $GATE_EXIT_UNMEASURED) and are asserted to read differently from a finding; SKIPPED (source corpus absent): $teeth_skipped"
  else
    echo "✓ operational-confidence-guard --teeth: duplicate ID, unknown live code, v1 mutation and missing P0 edge all red in domain words; an empty id corpus, an absent Go source tree and a runner without ripgrep all refuse AS UNMEASURED (exit $GATE_EXIT_UNMEASURED) and are asserted to read differently from a finding"
  fi
}

# Guarded by the BASH_SOURCE/$0 check below so this file can be `source`d —
# scripts/check-frozen-allowlist.sh does exactly that, to reuse the manifest
# this file already reads (schemas/published-v1.sha256) and its ROOT
# resolution, rather than writing a second parser of the ratchet
# (rules-that-reach-2026-08 P2). Sourcing must not run this gate as a side
# effect; only invoking this file directly does.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then

case "${1:-check}" in
  check) run_all ;;
  --teeth) run_teeth ;;
  *) echo "usage: $0 [check|--teeth]" >&2; exit 2 ;;
esac

fi # BASH_SOURCE guard
