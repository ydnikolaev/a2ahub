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
# map (naming a section id this gate confirms actually exists, in one of
# the pages skill/a2ahub/docs-manifest.json's `loop_corpus` array lists) or
# its `empty:` map (a non-blank, structural reason). A cell in neither is a
# real, undeclared gap and the gate reds NAMING it — that is the
# deliverable this gate exists to produce: an empty cell VISIBLE rather
# than discovered by a stalled integration (US-4).
#
# `loop_corpus` — not a hardcoded loops.md path — is the reachable set of
# pages that carry `## §X` loop sections. loops.md is a single 778-line
# page today; a later split moves sections across several pages, and this
# gate must keep asking the manifest which pages those are rather than one
# fixed filename that a split would silently orphan.
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
#   skill/a2ahub/**
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

# loop_corpus_files prints the skill-relative paths in the manifest's
# "loop_corpus" array, one per line, in declared order — the pages that
# carry `## §X` loop sections. docs-manifest.json is one JSON object per
# physical line (the check-loop-reachability.sh convention: no jq
# dependency), so "loop_corpus" is written on its own single line and
# parsed the same grep/sed way declared_gated_verbs parses a roster line.
# Returns failure (empty stdout, non-zero status) if the key is missing or
# its array is empty — the distinct "not declared" signal a caller must
# fail CLOSED on, never silently falling back to a hardcoded loops.md path.
loop_corpus_files() { # $1 = manifest path
  local line tokens
  line="$(grep -E '^[[:space:]]*"loop_corpus"[[:space:]]*:' "$1" | head -1)"
  if [ -z "$line" ]; then
    return 1
  fi
  # The first quoted token on the line is the key itself; drop it and keep
  # only the array's string elements.
  tokens="$(printf '%s\n' "$line" | grep -oE '"[^"]+"' | tr -d '"' | tail -n +2)"
  if [ -z "$tokens" ]; then
    return 1
  fi
  printf '%s\n' "$tokens"
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

# loop_section_ids_for_file prints every `## §X` heading id one loop_corpus
# file actually carries, one per line.
loop_section_ids_for_file() { # $1 = md file path
  grep -oE '^## §[^[:space:]]+' "$1" | sed -E 's/^## //'
}

# corpus_section_pairs prints "<id>\t<file>" for every `## §X` heading across
# the corpus, de-duplicated — the same file listing the same heading twice is
# not an error, only a heading shared by DISTINCT files is.
#
# THIS FUNCTION DOES NOT CALL gate_fail, AND THAT IS THE POINT. It used to,
# and the failure was swallowed twice over. A caller reads it through
# `$( )`, so anything it writes to stdout lands in the CALLER'S VARIABLE
# instead of the log — and under GitHub Actions `gate_fail` writes to stdout
# (`::error::`, which the runner only reads there). So in CI the duplicate-id
# failure was captured into the section-id list, corrupting the value AND
# hiding the message; the gate still reddened, by exit status, naming nothing.
# Its own `--teeth` case 11 could not see the message either, which is how it
# was found. The adjudication belongs to the caller, outside the substitution.
corpus_section_pairs() { # $1 = newline-separated list of existing corpus file abs paths
  local files="$1" f id
  local pairs=""
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    while IFS= read -r id; do
      [ -z "$id" ] && continue
      pairs="$pairs$id"$'\t'"$f"$'\n'
    done < <(loop_section_ids_for_file "$f")
  done <<<"$files"
  pairs="$(printf '%s' "$pairs" | sed '/^$/d')"
  [ -z "$pairs" ] && return 0
  printf '%s\n' "$pairs" | sort -u
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
# The lookup is deliberately not a pipeline, and the reason is a FLAKY GATE
# rather than a style preference — the same fix check-prose-coverage.sh's own
# ledger_has_key carries, for the same defect. `grep -q` exits at the FIRST
# match; under this file's `set -o pipefail` the pipeline then reports the
# PRODUCER's status, and a producer still writing when grep closes the pipe
# dies of SIGPIPE (141), so a key that IS present reports absent.
#
# `-x` WITHOUT `-F` IS PRESERVED EXACTLY. Adding -F here would change what
# this gate matches (these keys are compared as regexes today), and a silent
# semantic change smuggled inside a reliability fix is its own defect.
ledger_has_key() { # $1 = yaml path, $2 = section, $3 = key
  local keys
  keys="$(ledger_keys "$1" "$2")" || return 1
  grep -qx -- "$3" <<<"$keys"
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
      if ! grep -qx -- "$type" <<<"$types"; then
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
      if ! grep -qx -- "$phase" <<<"$rolelist"; then
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
  local manifest="$root/skill/a2ahub/docs-manifest.json"

  if [ ! -f "$yaml" ]; then
    gate_fail "loop-coverage: $yaml does not exist"
    gate_summary "loop-coverage"
    return $?
  fi
  if [ ! -f "$manifest" ]; then
    gate_fail "loop-coverage: $manifest does not exist"
    gate_summary "loop-coverage"
    return $?
  fi
  if ! grep -qx 'schema: loop-phases/v1' "$yaml"; then
    gate_fail "loop-coverage: $yaml is missing the \"schema: loop-phases/v1\" line"
  fi

  local corpus_rel
  if ! corpus_rel="$(loop_corpus_files "$manifest")"; then
    gate_fail "loop-coverage: $manifest has no non-empty \"loop_corpus\" array — declare the skill-relative pages that carry loop sections, e.g. \"loop_corpus\": [\"a2ahub/loops.md\"]"
    gate_summary "loop-coverage"
    return $?
  fi

  local corpus_abs="" rel abs
  while IFS= read -r rel; do
    [ -z "$rel" ] && continue
    abs="$root/skill/$rel"
    if [ ! -f "$abs" ]; then
      gate_fail "loop-coverage: $manifest's loop_corpus names \"$rel\", which does not exist at $abs"
      continue
    fi
    corpus_abs="$corpus_abs$abs"$'\n'
  done <<<"$corpus_rel"
  corpus_abs="$(printf '%s' "$corpus_abs" | sed '/^$/d')"
  if [ -z "$corpus_abs" ]; then
    gate_fail "loop-coverage: none of $manifest's loop_corpus files exist — failing closed rather than policing nothing"
    gate_summary "loop-coverage"
    return $?
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

  # A section id defined in more than one corpus file is ambiguity, not
  # coverage, and reds naming every file that carries it: a split that
  # duplicates a heading instead of moving it must not pass silently. The
  # adjudication is HERE rather than inside corpus_section_pairs — see the
  # comment there for what calling gate_fail inside a `$( )` costs.
  local section_pairs section_ids dupid dupfiles
  section_pairs="$(corpus_section_pairs "$corpus_abs")"
  while IFS= read -r dupid; do
    [ -z "$dupid" ] && continue
    dupfiles="$(printf '%s\n' "$section_pairs" | awk -F'\t' -v i="$dupid" '$1==i{print $2}' | paste -sd, -)"
    gate_fail "loop-coverage: section id \"$dupid\" is defined in more than one loop_corpus file ($dupfiles) — a duplicated section id across the corpus is ambiguous"
  done < <(printf '%s\n' "$section_pairs" | cut -f1 | sed '/^$/d' | sort | uniq -d)
  section_ids="$(printf '%s\n' "$section_pairs" | cut -f1 | sed '/^$/d' | sort -u)"

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
          if ! grep -qx -- "$cov" <<<"$section_ids"; then
            gate_fail "loop-coverage: $cell declares covered: \"$cov\", which is not a section heading any of $manifest's loop_corpus files has"
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
  # per type, a manifest declaring a single-file loop_corpus, and a loops.md
  # carrying the one section id it cites. Every mutation below starts from
  # this baseline and breaks exactly one thing.
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
    {
      echo '{'
      echo '  "schema": "a2a-docs-manifest/v1",'
      echo '  "loop_corpus": ["a2ahub/loops.md"],'
      echo '  "sections": []'
      echo '}'
    } >"$tmp/skill/a2ahub/docs-manifest.json"
    printf '# fixture\n\n## §8.1 Test Heading\n' >"$tmp/skill/a2ahub/loops.md"
    rm -f "$tmp/skill/a2ahub/loops-extra.md"
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
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -q -- "has no ledger entry" <<<"$out"; then
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
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -q -- "blank reason" <<<"$out"; then
    echo "loop-coverage --teeth: FAILED — an empty: entry with a blank reason did not red with the 'blank reason' message" >&2
    return 1
  fi

  # 3. a ledger key naming a phase outside that role's declared list must red
  # with THAT reason, not merely "no ledger entry" for some other cell.
  write_good_fixture
  echo "  $victim/originator/nonexistent-phase: \"§8.1\"" >>"$tmp/schemas/loop-phases.yaml"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -q -- "names phase \"nonexistent-phase\", which is not in originator's declared phase list" <<<"$out"; then
    echo "loop-coverage --teeth: FAILED — a ledger key naming an undeclared phase did not red with that message" >&2
    return 1
  fi

  # 4. a ledger key naming a type outside the derived universe must red
  # naming that type.
  write_good_fixture
  echo "  not-a-real-type/originator/author: \"§8.1\"" >>"$tmp/schemas/loop-phases.yaml"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -q -- 'names type "not-a-real-type", which is not one of the derived envelope types' <<<"$out"; then
    echo "loop-coverage --teeth: FAILED — a ledger key naming an undeclared type did not red with that message" >&2
    return 1
  fi

  # 5. a derived type with ZERO ledger rows must red by name — the "a type
  # added later cannot leave the matrix silently short" requirement.
  write_good_fixture
  grep -v "^  $victim/" "$tmp/schemas/loop-phases.yaml" >"$tmp/schemas/loop-phases.yaml.new"
  mv "$tmp/schemas/loop-phases.yaml.new" "$tmp/schemas/loop-phases.yaml"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -q -- "\"$victim\" has ZERO ledger rows" <<<"$out"; then
    echo "loop-coverage --teeth: FAILED — a type with zero ledger rows did not red with the ZERO-rows message naming it" >&2
    return 1
  fi

  # 6. `covered:` naming a section id loops.md does not have must red
  # naming that section id.
  write_good_fixture
  sed -i.bak "s#^  $victim/originator/author: \"§8.1\"#  $victim/originator/author: \"§9.9\"#" "$tmp/schemas/loop-phases.yaml"
  rm -f "$tmp/schemas/loop-phases.yaml.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -q -- 'declares covered: "§9.9", which is not a section heading' <<<"$out"; then
    echo "loop-coverage --teeth: FAILED — a covered: section id absent from loops.md did not red with that message" >&2
    return 1
  fi

  # 7. an empty declared phase list for a role must fail CLOSED, naming the
  # phase-list failure rather than cascading into per-cell noise.
  write_good_fixture
  sed -i.bak "s#^  originator: \[author\]#  originator: []#" "$tmp/schemas/loop-phases.yaml"
  rm -f "$tmp/schemas/loop-phases.yaml.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -q -- "declares an empty phase list for one or both roles" <<<"$out"; then
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
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -q -- "is declared BOTH covered and empty" <<<"$out"; then
    echo "loop-coverage --teeth: FAILED — a cell declared both covered and empty did not red with that message" >&2
    return 1
  fi

  # 9. a missing "loop_corpus" key in the manifest must red naming the
  # manifest and telling the reader what to add — never silently fall back
  # to a hardcoded loops.md path.
  write_good_fixture
  {
    echo '{'
    echo '  "schema": "a2a-docs-manifest/v1",'
    echo '  "sections": []'
    echo '}'
  } >"$tmp/skill/a2ahub/docs-manifest.json"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -qF -- 'has no non-empty "loop_corpus" array' <<<"$out"; then
    echo "loop-coverage --teeth: FAILED — a manifest with no loop_corpus array did not red naming the missing key" >&2
    return 1
  fi

  # 10. a loop_corpus entry naming a file that does not exist must red
  # naming that file.
  write_good_fixture
  sed -i.bak 's#"loop_corpus": \["a2ahub/loops.md"\]#"loop_corpus": ["a2ahub/nonexistent.md"]#' "$tmp/skill/a2ahub/docs-manifest.json"
  rm -f "$tmp/skill/a2ahub/docs-manifest.json.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -qF -- 'names "a2ahub/nonexistent.md", which does not exist' <<<"$out"; then
    echo "loop-coverage --teeth: FAILED — a loop_corpus entry naming a nonexistent file did not red naming it" >&2
    return 1
  fi

  # 11. the same section id defined in two loop_corpus files is ambiguous
  # and must red — a split that duplicates a heading instead of moving it
  # must not pass silently.
  write_good_fixture
  sed -i.bak 's#"loop_corpus": \["a2ahub/loops.md"\]#"loop_corpus": ["a2ahub/loops.md", "a2ahub/loops-extra.md"]#' "$tmp/skill/a2ahub/docs-manifest.json"
  rm -f "$tmp/skill/a2ahub/docs-manifest.json.bak"
  printf '# fixture extra\n\n## §8.1 Duplicate Heading\n' >"$tmp/skill/a2ahub/loops-extra.md"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! grep -qF -- 'section id "§8.1" is defined in more than one loop_corpus file' <<<"$out"; then
    echo "loop-coverage --teeth: FAILED — a section id defined in two corpus files did not red naming the ambiguity" >&2
    return 1
  fi

  # (m) ledger_has_key must find a key that a `grep -q` would match on the
  # FIRST line of a ledger far larger than a pipe buffer. Written as a
  # pipeline it cannot: grep exits at that first match, awk takes SIGPIPE
  # with tens of thousands of lines still unwritten, and `pipefail` reports
  # the dead producer — so a present key reads as absent.
  #
  # THIS GATE ALREADY LOST A RELEASE-GATE RUN TO THAT CLASS, on 2026-08-31:
  # the container suite reported `handoff/counterparty/work` naming a phase
  # "not in counterparty's declared phase list" while `work` sat second in
  # that very list, and the same tree was green standalone three times on the
  # host, three times in the container, and once through a full faithful
  # harness-check. It was never reproduced on demand — the window is narrow —
  # so this tooth widens the payload until the window is certain, which is
  # the only honest way to test a race.
  local big="$tmp/sigpipe.yaml"
  {
    printf 'schema: loop-phases/v1\n'
    printf 'covered:\n'
    seq 1 50000 | sed 's#^#  fixture/originator/author-#; s#$#: "§8.1"#'
  } >"$big"
  if ! ledger_has_key "$big" "covered:" "fixture/originator/author-1"; then
    echo "loop-coverage --teeth: FAILED (m) — ledger_has_key reported the FIRST key of a 50000-row ledger absent. That is the SIGPIPE-under-pipefail defect: the lookup must not be a pipeline into \`grep -q\`." >&2
    return 1
  fi
  if ledger_has_key "$big" "covered:" "fixture/originator/author-never"; then
    echo "loop-coverage --teeth: FAILED (m) — ledger_has_key reported an absent key present; the fix must not green everything." >&2
    return 1
  fi

  echo "loop-coverage --teeth: ok (including (m): the first key of a 50000-row ledger is found, an absent one is not)"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

run_check "$GATE_ROOT"
exit $?
