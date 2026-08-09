#!/usr/bin/env bash
# check-loop-coverage.sh — the 32-cell loop-coverage gate (P7, plan "The
# gate", AC4/US-4).
#
# The gate's universe is (type x role x phase):
#
#   type  DERIVED  from `a2a __catalog --vocabulary --json`'s `.states`
#         object keys — the same `fold.Kind` strings `schema.EnvelopeTypes()`
#         returns. Asking the binary rather than reading Go source is the
#         `check-view-vocabulary.sh` precedent: a gate that names a fixed
#         source of truth other than the shipped artifact stays green
#         through the drift it exists to catch.
#   role  a closed pair, `originator` / `counterparty`.
#   phase DECLARED in schemas/loop-phases.yaml, one list per role — read
#         that file's header before this one; it explains why phase has no
#         enumerator and prices the 88-vs-84-cell gap against Matrix C.
#
# Every cell in that universe must appear in loop-phases.yaml's `covered:`
# map (naming a skill/a2ahub/loops.md section id this gate confirms
# actually exists) or its `empty:` map (a non-blank, structural reason). A
# cell in neither is a real, undeclared gap and the gate reds NAMING it —
# that is the deliverable this gate exists to produce: an empty cell VISIBLE
# rather than discovered by a stalled integration (US-4).
#
# This gate also refuses drift the other direction: a `covered`/`empty` key
# naming a type, role or phase outside the derived/declared universe (a
# rename, a typo) reds too, and a derived type with ZERO ledger rows at all
# reds by name — the requirement that "a type added later cannot leave the
# matrix silently short."
#
# Usage: bash scripts/check-loop-coverage.sh            # check the real tree
#        bash scripts/check-loop-coverage.sh --teeth    # self-test on fixtures

# lane-inputs:
#   schemas/loop-phases.yaml
#   skill/a2ahub/loops.md
#   **/*.go
#   go.mod
#   go.sum
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   below self-locates scripts/lib/gate-lib.sh from this script's own path, so
#   the classifier cannot resolve the $(dirname ...) substitution to a literal.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# vocabulary_json asks the binary — never a literal list — for the domain
# vocabulary. The outer verification runner supplies the exact binary shared
# with e2e; a direct invocation falls back to `go run`. A failure fails
# CLOSED: an empty/unreadable vocabulary would let this gate pass while
# policing nothing, which is the one outcome worse than a red.
vocabulary_json() {
  if [ -n "${A2A_VERIFY_BINARY:-}" ]; then
    if [ ! -x "$A2A_VERIFY_BINARY" ]; then
      echo "A2A_VERIFY_BINARY is not executable: $A2A_VERIFY_BINARY" >&2
      return 1
    fi
    "$A2A_VERIFY_BINARY" __catalog --vocabulary --json
    return
  fi
  ( cd "$GATE_ROOT" && GOWORK=off go run ./cmd/a2a __catalog --vocabulary --json )
}

# derived_types prints every envelope type name the binary's vocabulary
# knows, one per line, sorted — the `states` object's own keys, not the
# state names nested inside each one. These are `fold.Kind` values, the same
# 8 strings `schema.EnvelopeTypes()` returns.
derived_types() { # $1 = vocabulary json
  printf '%s\n' "$1" |
    awk '/"states"/{f=1;next} f&&/^  \}/{f=0} f' |
    grep -oE '^    "[a-z_]+": \[' |
    grep -oE '"[a-z_]+"' | tr -d '"' | sort -u
}

# role_phases prints role's declared phase list from loop-phases.yaml, one
# per line, in declared order. Reads the flow-sequence line
# `  <role>: [a, b, c]` directly — no YAML library, matching this repo's
# other check-*.sh scripts (view-vocabulary, skill-citations).
role_phases() { # $1 = loop-phases.yaml path, $2 = role
  awk -v role="$2" '
    substr($0, 1, 2) == "  " {
      body = substr($0, 3)
      colon = index(body, ": ")
      if (colon == 0) next
      k = substr(body, 1, colon - 1)
      if (k != role) next
      v = substr(body, colon + 2)
      gsub(/^\[/, "", v)
      gsub(/\][ \t]*$/, "", v)
      n = split(v, arr, ",")
      for (i = 1; i <= n; i++) {
        item = arr[i]
        gsub(/^[ \t]+|[ \t]+$/, "", item)
        if (item != "") print item
      }
    }
  ' "$1"
}

# loop_section_ids prints every `## §X` heading id loops.md actually
# carries, one per line — what a `covered:` entry is checked against.
loop_section_ids() { # $1 = loops.md path
  grep -oE '^## §[^[:space:]]+' "$1" | sed -E 's/^## //'
}

# ledger_value prints the (quoted-stripped) value of one key inside one
# top-level section (`covered:` or `empty:`) of loop-phases.yaml, or nothing
# if that key has no row there. Matches keys by literal string equality —
# never builds a dynamic regex from `key`, so a key containing a
# regex-meaningful character (loop-phases.yaml's own `/`-joined cells) can
# never be misread.
ledger_value() { # $1 = yaml path, $2 = section ("covered:" or "empty:"), $3 = key
  awk -v section="$2" -v key="$3" '
    BEGIN { insec = 0 }
    /^[a-zA-Z_]+:[ \t]*$/ {
      insec = ($0 == section) ? 1 : 0
      next
    }
    insec == 1 {
      if (substr($0, 1, 2) != "  ") next
      body = substr($0, 3)
      colon = index(body, ": ")
      if (colon == 0) next
      k = substr(body, 1, colon - 1)
      if (k != key) next
      v = substr(body, colon + 2)
      gsub(/^"/, "", v)
      gsub(/"[ \t]*$/, "", v)
      print v
      exit
    }
  ' "$1"
}

# ledger_has_key reports (via exit status) whether key is DECLARED inside
# section, independent of its value. This is deliberately separate from
# ledger_value: a key declared with a BLANK value ("") and a key never
# declared at all both produce an empty string from command substitution,
# and collapsing "absent" and "present-but-blank" into the same signal is
# exactly how a blank-reason `empty:` row would misreport as "no ledger
# entry" instead of "blank reason" — right verdict, wrong finding.
ledger_has_key() { # $1 = yaml path, $2 = section, $3 = key
  ledger_keys "$1" "$2" | grep -qx "$3"
}

# ledger_keys prints every key declared inside one top-level section of
# loop-phases.yaml, one per line, in file order.
ledger_keys() { # $1 = yaml path, $2 = section ("covered:" or "empty:")
  awk -v section="$2" '
    BEGIN { insec = 0 }
    /^[a-zA-Z_]+:[ \t]*$/ {
      insec = ($0 == section) ? 1 : 0
      next
    }
    insec == 1 {
      if (substr($0, 1, 2) != "  ") next
      body = substr($0, 3)
      colon = index(body, ": ")
      if (colon == 0) next
      print substr(body, 1, colon - 1)
    }
  ' "$1"
}

# check_declared_keys walks every key declared in EITHER ledger section and
# refuses one that does not parse into type/role/phase against the derived
# universe — the rename-protection the plan's "declare it, gate-check it"
# trade is actually paying for: a phase or type renamed out from under a
# stale ledger row reds by name instead of just silently never matching any
# cell in the main sweep below.
check_declared_keys() { # $1=yaml $2=types(nl) $3=originator-phases(nl) $4=counterparty-phases(nl)
  local yaml="$1" types="$2" origp="$3" cpp="$4"
  local section key type rest role phase rolelist
  for section in "covered:" "empty:"; do
    while IFS= read -r key; do
      [ -z "$key" ] && continue
      case "$key" in
        */*/*) : ;;
        *)
          gate_fail "loop-coverage: declared key \"$key\" in $section is not shaped type/role/phase"
          continue
          ;;
      esac
      type="${key%%/*}"
      rest="${key#*/}"
      role="${rest%%/*}"
      phase="${rest#*/}"
      if [ -z "$type" ] || [ -z "$role" ] || [ -z "$phase" ] || [ "$phase" = "$rest" ]; then
        gate_fail "loop-coverage: declared key \"$key\" in $section is not shaped type/role/phase"
        continue
      fi
      if ! printf '%s\n' "$types" | grep -qx "$type"; then
        gate_fail "loop-coverage: declared key \"$key\" in $section names type \"$type\", which is not one of the derived envelope types"
        continue
      fi
      case "$role" in
        originator) rolelist="$origp" ;;
        counterparty) rolelist="$cpp" ;;
        *)
          gate_fail "loop-coverage: declared key \"$key\" in $section names role \"$role\", which is neither originator nor counterparty"
          continue
          ;;
      esac
      if ! printf '%s\n' "$rolelist" | grep -qx "$phase"; then
        gate_fail "loop-coverage: declared key \"$key\" in $section names phase \"$phase\", which is not in $role's declared phase list"
      fi
    done < <(ledger_keys "$yaml" "$section")
  done
}

# run_check walks the whole (type x role x phase) universe against
# loop-phases.yaml and reports. $1 is the root to read
# schemas/loop-phases.yaml and skill/a2ahub/loops.md from (GATE_ROOT for a
# real run; a fixture directory for --teeth).
run_check() { # $1 = root
  local root="$1"
  local yaml="$root/schemas/loop-phases.yaml"
  local loops="$root/skill/a2ahub/loops.md"

  if [ ! -f "$yaml" ]; then
    gate_fail "loop-coverage: $yaml does not exist"
    gate_summary "loop-coverage"
    return $?
  fi
  if [ ! -f "$loops" ]; then
    gate_fail "loop-coverage: $loops does not exist"
    gate_summary "loop-coverage"
    return $?
  fi
  if ! grep -qx 'schema: loop-phases/v1' "$yaml"; then
    gate_fail "loop-coverage: $yaml is missing the \"schema: loop-phases/v1\" line"
  fi

  local vocab types
  if ! vocab="$(vocabulary_json)"; then
    gate_fail "loop-coverage: could not read the vocabulary from the binary — failing closed rather than policing nothing"
    gate_summary "loop-coverage"
    return $?
  fi
  types="$(derived_types "$vocab")"
  if [ -z "$types" ]; then
    gate_fail "loop-coverage: the binary returned no envelope types — failing closed rather than policing nothing"
    gate_summary "loop-coverage"
    return $?
  fi

  local originator_phases counterparty_phases
  originator_phases="$(role_phases "$yaml" originator)"
  counterparty_phases="$(role_phases "$yaml" counterparty)"
  if [ -z "$originator_phases" ] || [ -z "$counterparty_phases" ]; then
    gate_fail "loop-coverage: $yaml declares an empty phase list for one or both roles — failing closed rather than policing nothing"
    gate_summary "loop-coverage"
    return $?
  fi

  local section_ids
  section_ids="$(loop_section_ids "$loops")"

  local type role phase phaselist cell cov emp has_cov has_emp type_has_row
  for type in $types; do
    type_has_row=0
    for role in originator counterparty; do
      if [ "$role" = originator ]; then phaselist="$originator_phases"; else phaselist="$counterparty_phases"; fi
      for phase in $phaselist; do
        cell="$type/$role/$phase"
        if ledger_has_key "$yaml" "covered:" "$cell"; then has_cov=1; else has_cov=0; fi
        if ledger_has_key "$yaml" "empty:" "$cell"; then has_emp=1; else has_emp=0; fi
        if [ "$has_cov" -eq 1 ] && [ "$has_emp" -eq 1 ]; then
          gate_fail "loop-coverage: $cell is declared BOTH covered and empty in $yaml — pick one"
          type_has_row=1
        elif [ "$has_cov" -eq 1 ]; then
          type_has_row=1
          cov="$(ledger_value "$yaml" "covered:" "$cell")"
          if ! printf '%s\n' "$section_ids" | grep -qx "$cov"; then
            gate_fail "loop-coverage: $cell declares covered: \"$cov\", which is not a section heading $loops actually has"
          fi
        elif [ "$has_emp" -eq 1 ]; then
          type_has_row=1
          emp="$(ledger_value "$yaml" "empty:" "$cell")"
          if [ -z "$(printf '%s' "$emp" | tr -d '[:space:]')" ]; then
            gate_fail "loop-coverage: $cell is declared empty in $yaml with a blank reason — an exemption without a reason reads as a decision"
          fi
        else
          gate_fail "loop-coverage: $cell has no ledger entry in $yaml — neither covered nor declared empty"
        fi
      done
    done
    if [ "$type_has_row" -eq 0 ]; then
      gate_fail "loop-coverage: envelope type \"$type\" has ZERO ledger rows across every phase in $yaml — a type added later must not leave the matrix silently short"
    fi
  done

  check_declared_keys "$yaml" "$types" "$originator_phases" "$counterparty_phases"

  gate_summary "loop-coverage"
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || { echo "loop-coverage --teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN
  mkdir -p "$tmp/schemas" "$tmp/skill/a2ahub"

  local vocab types
  if ! vocab="$(vocabulary_json)"; then
    echo "loop-coverage --teeth: could not read the binary's vocabulary — cannot build a fixture universe" >&2
    return 1
  fi
  types="$(derived_types "$vocab")"
  if [ -z "$types" ]; then
    echo "loop-coverage --teeth: the binary returned no envelope types" >&2
    return 1
  fi
  local victim
  victim="$(printf '%s\n' "$types" | head -1)"

  # write_good_fixture writes a small but FULLY declared, self-consistent
  # ledger against the REAL derived type universe: one originator phase
  # (author, covered) and one counterparty phase (receive, declared empty)
  # per type, plus a loops.md carrying the one section id it cites. Every
  # mutation below starts from this baseline and breaks exactly one thing.
  write_good_fixture() {
    {
      echo "schema: loop-phases/v1"
      echo "phases:"
      echo "  originator: [author]"
      echo "  counterparty: [receive]"
      echo "covered:"
      local t
      for t in $types; do
        echo "  $t/originator/author: \"§8.1\""
      done
      echo "empty:"
      for t in $types; do
        echo "  $t/counterparty/receive: \"deliberately empty in this fixture\""
      done
    } >"$tmp/schemas/loop-phases.yaml"
    printf '# fixture\n\n## §8.1 Test Heading\n' >"$tmp/skill/a2ahub/loops.md"
  }

  write_good_fixture
  if ! (run_check "$tmp") >/dev/null 2>&1; then
    echo "loop-coverage --teeth: FAILED — a fully declared, self-consistent fixture ledger stayed red" >&2
    return 1
  fi

  # 1. a cell in the derived universe with no ledger entry must red — and
  # must red with "no ledger entry", never "blank reason" (that would be
  # the wrong reason: see mutation 2 below for why they must stay distinct).
  write_good_fixture
  grep -v "^  $victim/originator/author:" "$tmp/schemas/loop-phases.yaml" >"$tmp/schemas/loop-phases.yaml.new"
  mv "$tmp/schemas/loop-phases.yaml.new" "$tmp/schemas/loop-phases.yaml"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -q "has no ledger entry"; then
    echo "loop-coverage --teeth: FAILED — a cell with no ledger entry did not red with the 'no ledger entry' message" >&2
    return 1
  fi

  # 2. an `empty:` entry with a BLANK reason must red with "blank reason",
  # never "no ledger entry". A blank value and an absent key both collapse
  # to an empty bash string from command substitution — this is the exact
  # trap ledger_has_key exists to avoid, so the message text is the
  # assertion, not just "did it turn red".
  write_good_fixture
  sed -i.bak "s#^  $victim/counterparty/receive: \".*\"#  $victim/counterparty/receive: \"\"#" "$tmp/schemas/loop-phases.yaml"
  rm -f "$tmp/schemas/loop-phases.yaml.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -q "blank reason"; then
    echo "loop-coverage --teeth: FAILED — an empty: entry with a blank reason did not red with the 'blank reason' message" >&2
    return 1
  fi

  # 3. a ledger key naming a phase outside that role's declared list must red
  # with THAT reason, not merely "no ledger entry" for some other cell.
  write_good_fixture
  echo "  $victim/originator/nonexistent-phase: \"§8.1\"" >>"$tmp/schemas/loop-phases.yaml"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -q "names phase \"nonexistent-phase\", which is not in originator's declared phase list"; then
    echo "loop-coverage --teeth: FAILED — a ledger key naming an undeclared phase did not red with that message" >&2
    return 1
  fi

  # 4. a ledger key naming a type outside the derived universe must red
  # naming that type.
  write_good_fixture
  echo "  not-a-real-type/originator/author: \"§8.1\"" >>"$tmp/schemas/loop-phases.yaml"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -q 'names type "not-a-real-type", which is not one of the derived envelope types'; then
    echo "loop-coverage --teeth: FAILED — a ledger key naming an undeclared type did not red with that message" >&2
    return 1
  fi

  # 5. a derived type with ZERO ledger rows must red by name — the "a type
  # added later cannot leave the matrix silently short" requirement.
  write_good_fixture
  grep -v "^  $victim/" "$tmp/schemas/loop-phases.yaml" >"$tmp/schemas/loop-phases.yaml.new"
  mv "$tmp/schemas/loop-phases.yaml.new" "$tmp/schemas/loop-phases.yaml"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -q "\"$victim\" has ZERO ledger rows"; then
    echo "loop-coverage --teeth: FAILED — a type with zero ledger rows did not red with the ZERO-rows message naming it" >&2
    return 1
  fi

  # 6. `covered:` naming a section id loops.md does not have must red
  # naming that section id.
  write_good_fixture
  sed -i.bak "s#^  $victim/originator/author: \"§8.1\"#  $victim/originator/author: \"§9.9\"#" "$tmp/schemas/loop-phases.yaml"
  rm -f "$tmp/schemas/loop-phases.yaml.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -q 'declares covered: "§9.9", which is not a section heading'; then
    echo "loop-coverage --teeth: FAILED — a covered: section id absent from loops.md did not red with that message" >&2
    return 1
  fi

  # 7. an empty declared phase list for a role must fail CLOSED, naming the
  # phase-list failure rather than cascading into per-cell noise.
  write_good_fixture
  sed -i.bak "s#^  originator: \[author\]#  originator: []#" "$tmp/schemas/loop-phases.yaml"
  rm -f "$tmp/schemas/loop-phases.yaml.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -q "declares an empty phase list for one or both roles"; then
    echo "loop-coverage --teeth: FAILED — an empty declared phase list did not fail closed with that message" >&2
    return 1
  fi

  # 8. a cell declared BOTH covered and empty must red. The duplicate line
  # is inserted right after the literal "empty:" heading (not appended at
  # EOF) so the assertion does not depend on `empty:` staying the file's
  # last section.
  write_good_fixture
  sed -i.bak "/^empty:\$/a\\
  $victim/originator/author: \"deliberately duplicated\"
" "$tmp/schemas/loop-phases.yaml"
  rm -f "$tmp/schemas/loop-phases.yaml.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -q "is declared BOTH covered and empty"; then
    echo "loop-coverage --teeth: FAILED — a cell declared both covered and empty did not red with that message" >&2
    return 1
  fi

  echo "loop-coverage --teeth: ok"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

run_check "$GATE_ROOT"
exit $?
