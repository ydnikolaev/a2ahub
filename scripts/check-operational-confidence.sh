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

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
ERRORS=0

fail() { echo "operational-confidence-guard: FAIL — $*" >&2; ERRORS=$((ERRORS + 1)); }

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
  echo "operational-confidence-guard: FAIL — ripgrep (rg) is required and not on PATH." >&2
  echo "  Install it: brew install ripgrep · apt-get install -y ripgrep" >&2
  echo "  This gate reads the normative plan with rg; without it every check below" >&2
  echo "  would report a missing definition for a document that is present." >&2
  exit 1
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
    fail "the id extractor produced NOTHING from $plan_root — the plan is not the suspect here, the scan is"
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
    rg -q "\\b${evidence}\\b" "$plan_root/13-testing.md" || fail "$evidence missing from §13"
    rg -q "\\b${evidence}\\b" "$plan_root/14-us-ac.md" || fail "$evidence has no §14 acceptance link"
  done
}

registry_codes() { sed -n 's/^[[:space:]]*- code:[[:space:]]*//p' "$1"; }

check_history() {
  local history="$1" registry="$2" source_root="$3" code status owner meaning introduced retired
  local duplicates registry_list emitted
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

  emitted="$(rg --no-filename --glob '*.go' --glob '!*_test.go' -o '"(REF|LFC|POL)-[0-9]{3}"' "$source_root/internal/validate" "$source_root/internal/cli" 2>/dev/null | tr -d '"' | sort -u)"
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
  local root="$1" manifest="$2" expected current path listed actual
  while read -r expected path; do
    [ -n "$expected" ] && [ -n "$path" ] || continue
    [ -f "$root/$path" ] || { fail "published-v1 manifest path missing: $path"; continue; }
    actual="$(shasum -a 256 "$root/$path" | awk '{print $1}')"
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
  grep -q '^[[:space:]]*- id: P0$' "$tracker" || fail "tracker is missing mandatory P0"
  for p in P1 P4; do
    phase_record "$tracker" "$p" | grep -Eq 'blocked_by:.*P0' || fail "$p must be blocked_by P0"
  done
  while IFS= read -r plan; do
    [ -f "$root/docs/features/active/operational-confidence-2026-08/$plan" ] || fail "tracker plan path missing: $plan"
  done < <(sed -n 's/^[[:space:]]*plan:[[:space:]]*//p' "$tracker")
  rg -q 'lead owns final wiring' "$root/docs/features/active/operational-confidence-2026-08/plan.md" || fail "shared wiring has no sequencing owner"
  rg -q 'reconciled before assigning P1D' "$root/docs/features/active/operational-confidence-2026-08/plans/01-producer-evaluation-receipts.plan.md" || fail "P1 cache/HTML overlap has no serialization guard"
}

check_boundaries() {
  local root="$1"
  for token in 'internal/workreport/' 'internal/operational/' 'internal/contract/' 'internal/localserver/'; do
    rg -q "$token" "$root/docs/decisions.md" || fail "ADR-001 missing $token"
  done
  for token in workreport operational contract localserver; do
    rg -q "$token" "$root/AGENTS.md" || fail "project architecture rail missing $token"
  done
  rg -q 'sole loopback' "$root/AGENTS.md" || fail "localserver loopback exception is not bounded"
  rg -q 'disposable local I/O remains authorized' "$root/AGENTS.md" || fail "existing disposable local-I/O exceptions are not preserved"
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
  if [ "$ERRORS" -ne 0 ]; then exit 1; fi
  echo "operational-confidence-guard: OK — IDs/traceability, dispatch DAG, error history, published-v1 bytes and boundaries are coherent."
}

expect_red() {
  local label="$1" needle="$2"; shift 2
  local out rc
  out="$( (ERRORS=0; "$@"; [ "$ERRORS" -eq 0 ]) 2>&1)"; rc=$?
  [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "$needle" || { echo "operational-confidence-guard --teeth: $label did not red on '$needle'"; echo "$out"; exit 1; }
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

  if corpus_present docs/features/active/operational-confidence-2026-08 mandatory-dag; then
    mkdir -p "$tmp/dispatch/docs/features/active"
    cp -R "$ROOT/docs/features/active/operational-confidence-2026-08" "$tmp/dispatch/docs/features/active/"
    sed -i.bak '/- id: P1/,/- id: P2/ s/blocked_by: \[P0\]/blocked_by: []/' "$tmp/dispatch/docs/features/active/operational-confidence-2026-08/tracker.yaml"
    expect_red mandatory-dag 'P1 must be blocked_by P0' check_dispatch "$tmp/dispatch"
  fi

  # The two fixtures that always build — their corpora are product, not
  # planning — are named separately from the two that may not, so a green line
  # here says what was actually proven rather than what the file contains.
  if [ -n "$teeth_skipped" ]; then
    echo "✓ operational-confidence-guard --teeth: unknown live code and v1 mutation red; SKIPPED (source corpus absent): $teeth_skipped"
  else
    echo "✓ operational-confidence-guard --teeth: duplicate ID, unknown live code, v1 mutation and missing P0 edge all red"
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
