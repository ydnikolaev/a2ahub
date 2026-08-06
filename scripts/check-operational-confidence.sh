#!/usr/bin/env bash
# P0/W0 static guard: normative IDs, mandatory dispatch edges, stable error
# history, published-v1 immutability and the approved package/I/O boundary.
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

check_hashes() {
  local root="$1" manifest="$2" expected current path listed actual
  while read -r expected path; do
    [ -n "$expected" ] && [ -n "$path" ] || continue
    [ -f "$root/$path" ] || { fail "published-v1 manifest path missing: $path"; continue; }
    actual="$(shasum -a 256 "$root/$path" | awk '{print $1}')"
    [ "$actual" = "$expected" ] || fail "published v1 bytes changed: $path"
  done < "$manifest"

  current="$(find "$root/schemas" -type f \( -path '*/v1/*.schema.json' -o -path "$root/schemas/templates/v1/*.md" \) -print | sed "s#^$root/##" | sort)"
  listed="$(awk '{print $2}' "$manifest" | sort)"
  [ "$current" = "$listed" ] || fail "published-v1 manifest does not exactly cover every v1 schema/template"
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

run_teeth() {
  local tmp path
  tmp="$(mktemp -d)" || exit 1
  trap "rm -rf '$tmp'" EXIT

  mkdir -p "$tmp/ids"; cp -R "$ROOT/docs/the-plan/plan/." "$tmp/ids/"
  printf '\n| R-023 | duplicate | x |\n' >> "$tmp/ids/01-vision.md"
  expect_red duplicate-id 'duplicate normative' check_ids "$tmp/ids"

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

  mkdir -p "$tmp/dispatch/docs/features/active"
  cp -R "$ROOT/docs/features/active/operational-confidence-2026-08" "$tmp/dispatch/docs/features/active/"
  sed -i.bak '/- id: P1/,/- id: P2/ s/blocked_by: \[P0\]/blocked_by: []/' "$tmp/dispatch/docs/features/active/operational-confidence-2026-08/tracker.yaml"
  expect_red mandatory-dag 'P1 must be blocked_by P0' check_dispatch "$tmp/dispatch"

  echo "✓ operational-confidence-guard --teeth: duplicate ID, unknown live code, v1 mutation and missing P0 edge all red"
}

case "${1:-check}" in
  check) run_all ;;
  --teeth) run_teeth ;;
  *) echo "usage: $0 [check|--teeth]" >&2; exit 2 ;;
esac
