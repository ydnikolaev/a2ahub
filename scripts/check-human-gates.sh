#!/usr/bin/env bash
# check-human-gates.sh — the human-gate roster gate (agent-exchange P7, AC4).
#
# AC4 is a conjunction and this gate enforces both halves:
#
#   1. The set of transitions skill/a2ahub/loops.md PRESENTS as human-gated
#      is EXACTLY the set the binary reports. `fold.Vocabulary`'s
#      `human_gates` object (surfaced via `a2a __catalog --vocabulary
#      --json`) is the single source of truth — never a copy hand-listed
#      here. loops.md declares its own roster on ONE machine-readable line
#      (the "**Human-gated verbs (machine roster):**" prefix, backtick-
#      wrapped verbs) so this gate can compare a real declaration rather
#      than parsing free prose, which the epic's own rails forbid.
#
#   2. No loop step routes an agent to self-serve one of them: the bare
#      command form (`a2a approve`, `a2a reject`, …) must not appear
#      anywhere in loops.md. A gated verb is legitimately NAMED in prose
#      (backtick-quoted, discussed) many times over — only the command
#      form, `a2a <verb>`, is a routing instruction, and that is what is
#      forbidden.
#
# The vocabulary comes from the BINARY, never from Go source: a gate naming
# a fixed source of truth other than the shipped artifact stays green
# through exactly the drift it exists to catch (the `check-view-vocabulary.sh`
# / `check-loop-coverage.sh` precedent — read `humangate.go` from bash is
# forbidden).
#
# Usage: bash scripts/check-human-gates.sh            # check the real tree
#        bash scripts/check-human-gates.sh --teeth    # self-test on fixtures

# lane-inputs:
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

# The exact prefix loops.md's machine-roster line starts with. A `grep -F`
# match on this literal string, never a regex over free prose.
MARKER_PREFIX='**Human-gated verbs (machine roster):**'

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

# derived_gated_verbs prints every key of the vocabulary's `human_gates`
# object, one per line, sorted — the same awk shape check-loop-coverage.sh
# uses for `states`, scoped to the `human_gates` object instead.
derived_gated_verbs() { # $1 = vocabulary json
  printf '%s\n' "$1" |
    awk '/"human_gates"/{f=1;next} f&&/^  \}/{f=0} f' |
    grep -oE '^    "[a-z_]+":' |
    grep -oE '"[a-z_]+"' | tr -d '"' | sort -u
}

# declared_gated_verbs prints the backtick-wrapped verbs on loops.md's ONE
# machine-roster line, one per line, sorted. Returns failure (empty stdout,
# non-zero status) if the marker line itself is absent — the distinct
# "no declaration at all" signal `run_check` fails closed on, the same trap
# `ledger_has_key` in check-loop-coverage.sh exists to avoid.
declared_gated_verbs() { # $1 = loops.md path
  local line
  line="$(grep -F "$MARKER_PREFIX" "$1" | head -1)"
  if [ -z "$line" ]; then
    return 1
  fi
  printf '%s\n' "$line" | grep -oE '`[a-z_]+`' | tr -d '`' | sort -u
}

# self_serve_hits prints every "file:line:content" in loops.md where a gated
# verb appears in COMMAND form — `a2a <verb>` — one hit per line. A verb
# named in prose (backtick-quoted, discussed) is not a hit; only the literal
# invocation form is, because that is the one shape that routes an agent to
# do the gated move itself instead of asking a human.
self_serve_hits() { # $1 = loops.md path, $2 = verb
  grep -nE "a2a[[:space:]]+$2\\b" "$1" || true
}

run_check() { # $1 = root
  local root="$1"
  local loops="$root/skill/a2ahub/loops.md"

  if [ ! -f "$loops" ]; then
    gate_fail "human-gates: $loops does not exist"
    gate_summary "human-gates"
    return $?
  fi

  local vocab
  if ! vocab="$(vocabulary_json)"; then
    gate_fail "human-gates: could not read the vocabulary from the binary — failing closed rather than policing nothing"
    gate_summary "human-gates"
    return $?
  fi

  local derived
  derived="$(derived_gated_verbs "$vocab")"
  if [ -z "$derived" ]; then
    gate_fail "human-gates: the binary reported no human-gated transitions in its human_gates object — failing closed rather than policing nothing"
    gate_summary "human-gates"
    return $?
  fi

  local declared
  if ! declared="$(declared_gated_verbs "$loops")"; then
    gate_fail "human-gates: $loops has no \"$MARKER_PREFIX\" line — the human-gated roster must be declared on one machine-readable line, not only in prose"
    gate_summary "human-gates"
    return $?
  fi

  local verb
  for verb in $derived; do
    if ! printf '%s\n' "$declared" | grep -qx "$verb"; then
      gate_fail "human-gates: $loops's machine roster omits \"$verb\", which the binary's human_gates object reports as human-gated"
    fi
  done
  for verb in $declared; do
    if ! printf '%s\n' "$derived" | grep -qx "$verb"; then
      gate_fail "human-gates: $loops's machine roster names \"$verb\", which the binary's human_gates object does not gate"
    fi
  done

  # AC4's second conjunct: no loop step may spell the command form of a
  # gated verb — that would route an agent to self-serve it.
  local hit
  for verb in $derived; do
    while IFS= read -r hit; do
      [ -z "$hit" ] && continue
      gate_fail "human-gates: $hit — spells the command form of gated verb \"$verb\"; a loop step must route to a human, never self-serve it"
    done < <(self_serve_hits "$loops" "$verb")
  done

  gate_summary "human-gates"
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || { echo "human-gates --teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN
  mkdir -p "$tmp/skill/a2ahub"

  local vocab derived
  if ! vocab="$(vocabulary_json)"; then
    echo "human-gates --teeth: could not read the binary's vocabulary — cannot build a fixture universe" >&2
    return 1
  fi
  derived="$(derived_gated_verbs "$vocab")"
  if [ -z "$derived" ]; then
    echo "human-gates --teeth: the binary reported no human-gated transitions" >&2
    return 1
  fi

  local backticked
  backticked="$(printf '%s\n' "$derived" | sed 's/^/`/; s/$/`/' | paste -sd, - | sed 's/,/, /g')"

  # write_good_fixture writes a loops.md carrying a correct machine roster
  # (exactly the derived verbs, backtick-wrapped) and no command-form
  # self-serve mention. Every mutation below starts from this baseline and
  # breaks exactly one thing.
  write_good_fixture() {
    {
      echo "# fixture"
      echo ""
      echo "**Human-gated verbs (machine roster):** $backticked."
      echo "Prose may still name a gated verb, e.g. \`$( printf '%s\n' "$derived" | head -1 )\` is discussed here, just never as a command."
    } >"$tmp/skill/a2ahub/loops.md"
  }

  write_good_fixture
  if ! (run_check "$tmp") >/dev/null 2>&1; then
    echo "human-gates --teeth: FAILED — a fully declared, self-consistent fixture stayed red" >&2
    return 1
  fi

  # 1. A missing machine-roster line must red naming the missing marker.
  printf '# fixture\n\nNo roster line here.\n' >"$tmp/skill/a2ahub/loops.md"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "has no \"$MARKER_PREFIX\" line"; then
    echo "human-gates --teeth: FAILED — a loops.md with no machine-roster line did not red naming the missing marker" >&2
    return 1
  fi

  # 2. The roster naming a THIRD verb the binary does not gate must red
  # naming that verb — this is the AC's own "naming a third gated verb"
  # fixture.
  write_good_fixture
  sed -i.bak "s/(machine roster):\*\* $backticked\./(machine roster):** $backticked, \`withdraw\`./" "$tmp/skill/a2ahub/loops.md"
  rm -f "$tmp/skill/a2ahub/loops.md.bak"
  grep -Fq '`withdraw`' "$tmp/skill/a2ahub/loops.md" || { echo "human-gates --teeth: could not seed a third-verb fixture" >&2; return 1; }
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF 'names "withdraw", which the binary'"'"'s human_gates object does not gate'; then
    echo "human-gates --teeth: FAILED — a roster naming an ungated third verb did not red naming it" >&2
    return 1
  fi

  # 3. The roster omitting a real gated verb must red naming it.
  local victim rest
  victim="$(printf '%s\n' "$derived" | head -1)"
  rest="$(printf '%s\n' "$derived" | tail -n +2 | sed 's/^/`/; s/$/`/' | paste -sd, - | sed 's/,/, /g')"
  write_good_fixture
  if [ -n "$rest" ]; then
    sed -i.bak "s/(machine roster):\*\* $backticked\./(machine roster):** $rest./" "$tmp/skill/a2ahub/loops.md"
  else
    sed -i.bak "s/(machine roster):\*\* $backticked\./(machine roster):** ./" "$tmp/skill/a2ahub/loops.md"
  fi
  rm -f "$tmp/skill/a2ahub/loops.md.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "omits \"$victim\""; then
    echo "human-gates --teeth: FAILED — a roster omitting a real gated verb ($victim) did not red naming it" >&2
    return 1
  fi

  # 4. A loop step spelling the command form of a gated verb — "calling
  # `approve` autonomous" (the AC's own other named fixture) — must red,
  # even though the roster line itself stays perfectly correct.
  write_good_fixture
  printf '\n1. Run `a2a %s` yourself, no human needed.\n' "$victim" >>"$tmp/skill/a2ahub/loops.md"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "spells the command form of gated verb \"$victim\""; then
    echo "human-gates --teeth: FAILED — a loop step self-serving \"a2a $victim\" did not red naming it" >&2
    return 1
  fi

  echo "human-gates --teeth: ok"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

run_check "$GATE_ROOT"
exit $?
