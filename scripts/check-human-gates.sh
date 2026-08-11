#!/usr/bin/env bash
# check-human-gates.sh — the human-gate roster gate (agent-exchange P7, AC4).
#
# AC4 is a conjunction and this gate enforces both halves:
#
#   1. The set of transitions the loop corpus PRESENTS as human-gated is
#      EXACTLY the set the binary reports. `fold.Vocabulary`'s
#      `human_gates` object (surfaced via `a2a __catalog --vocabulary
#      --json`) is the single source of truth — never a copy hand-listed
#      here. The corpus declares its roster on ONE machine-readable line,
#      in exactly ONE of its files (the "**Human-gated verbs (machine
#      roster):**" prefix, backtick-wrapped verbs) so this gate can compare
#      a real declaration rather than parsing free prose, which the epic's
#      own rails forbid. The loop corpus itself — the pages that carry
#      loop content — is not a hardcoded loops.md path; it is the
#      skill-relative file list in skill/a2ahub/docs-manifest.json's
#      `loop_corpus` array, so a future split of the 778-line loops.md into
#      several pages does not silently blind this gate to steps that moved.
#
#   2. No loop step routes an agent to self-serve one of them: the bare
#      command form (`a2a approve`, `a2a reject`, …) must not appear
#      anywhere in ANY corpus file. A gated verb is legitimately NAMED in
#      prose (backtick-quoted, discussed) many times over — only the
#      command form, `a2a <verb>`, is a routing instruction, and that is
#      what is forbidden.
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

# loop_corpus_files prints the skill-relative paths in the manifest's
# "loop_corpus" array, one per line, in declared order — the pages that
# carry loop content. docs-manifest.json is one JSON object per physical
# line (the check-loop-reachability.sh convention: no jq dependency), so
# "loop_corpus" is written on its own single line and parsed the same
# grep/sed way declared_gated_verbs below parses a roster line. Returns
# failure (empty stdout, non-zero status) if the key is missing or its
# array is empty — the distinct "not declared" signal a caller must fail
# CLOSED on, never silently falling back to a hardcoded loops.md path.
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

# self_serve_hits prints every "file:line:content" in one corpus file where
# a gated verb appears in COMMAND form — `a2a <verb>` — one hit per line.
# `-H` forces the filename prefix even for this single-file invocation, so
# a hit is self-describing without the caller re-stitching the path back
# on. A verb named in prose (backtick-quoted, discussed) is not a hit; only
# the literal invocation form is, because that is the one shape that
# routes an agent to do the gated move itself instead of asking a human.
self_serve_hits() { # $1 = corpus file path, $2 = verb
  grep -nHE "a2a[[:space:]]+$2\\b" "$1" || true
}

run_check() { # $1 = root
  local root="$1"
  local manifest="$root/skill/a2ahub/docs-manifest.json"

  if [ ! -f "$manifest" ]; then
    gate_fail "human-gates: $manifest does not exist"
    gate_summary "human-gates"
    return $?
  fi

  local corpus_rel
  if ! corpus_rel="$(loop_corpus_files "$manifest")"; then
    gate_fail "human-gates: $manifest has no non-empty \"loop_corpus\" array — declare the skill-relative pages that carry loop content, e.g. \"loop_corpus\": [\"a2ahub/loops.md\"]"
    gate_summary "human-gates"
    return $?
  fi

  local corpus_abs="" rel abs
  while IFS= read -r rel; do
    [ -z "$rel" ] && continue
    abs="$root/skill/$rel"
    if [ ! -f "$abs" ]; then
      gate_fail "human-gates: $manifest's loop_corpus names \"$rel\", which does not exist at $abs"
      continue
    fi
    corpus_abs="$corpus_abs$abs"$'\n'
  done <<<"$corpus_rel"
  corpus_abs="$(printf '%s' "$corpus_abs" | sed '/^$/d')"
  if [ -z "$corpus_abs" ]; then
    gate_fail "human-gates: none of $manifest's loop_corpus files exist — failing closed rather than policing nothing"
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

  # The machine roster must live in exactly ONE corpus file. Zero files
  # carrying the marker, or more than one, both red — a duplicated roster
  # is how two answers drift apart.
  local roster_files="" f
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    if grep -qF "$MARKER_PREFIX" "$f"; then
      roster_files="$roster_files$f"$'\n'
    fi
  done <<<"$corpus_abs"
  roster_files="$(printf '%s' "$roster_files" | sed '/^$/d')"

  local roster_count=0
  if [ -n "$roster_files" ]; then
    roster_count="$(printf '%s\n' "$roster_files" | wc -l | tr -d ' ')"
  fi

  local declared="" have_roster=0
  if [ "$roster_count" -eq 0 ]; then
    gate_fail "human-gates: no file in $manifest's loop_corpus has a \"$MARKER_PREFIX\" line — the human-gated roster must be declared on one machine-readable line, not only in prose"
  elif [ "$roster_count" -gt 1 ]; then
    gate_fail "human-gates: the \"$MARKER_PREFIX\" line appears in more than one loop_corpus file ($(printf '%s\n' "$roster_files" | paste -sd, -)) — a duplicated roster is how two answers drift apart"
  else
    local roster_file
    roster_file="$(printf '%s\n' "$roster_files" | head -1)"
    declared="$(declared_gated_verbs "$roster_file")"
    have_roster=1
  fi

  if [ "$have_roster" -eq 1 ]; then
    local verb
    for verb in $derived; do
      if ! printf '%s\n' "$declared" | grep -qx "$verb"; then
        gate_fail "human-gates: the loop corpus's machine roster omits \"$verb\", which the binary's human_gates object reports as human-gated"
      fi
    done
    for verb in $declared; do
      if ! printf '%s\n' "$derived" | grep -qx "$verb"; then
        gate_fail "human-gates: the loop corpus's machine roster names \"$verb\", which the binary's human_gates object does not gate"
      fi
    done
  fi

  # AC4's second conjunct: no loop step in ANY corpus file may spell the
  # command form of a gated verb — that would route an agent to self-serve
  # it.
  local verb hit
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    for verb in $derived; do
      while IFS= read -r hit; do
        [ -z "$hit" ] && continue
        gate_fail "human-gates: $hit — spells the command form of gated verb \"$verb\"; a loop step must route to a human, never self-serve it"
      done < <(self_serve_hits "$f" "$verb")
    done
  done <<<"$corpus_abs"

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

  # write_good_fixture writes a manifest declaring a single-file
  # loop_corpus and a loops.md carrying a correct machine roster (exactly
  # the derived verbs, backtick-wrapped) and no command-form self-serve
  # mention. Every mutation below starts from this baseline and breaks
  # exactly one thing.
  write_good_fixture() {
    {
      echo '{'
      echo '  "schema": "a2a-docs-manifest/v1",'
      echo '  "loop_corpus": ["a2ahub/loops.md"],'
      echo '  "sections": []'
      echo '}'
    } >"$tmp/skill/a2ahub/docs-manifest.json"
    {
      echo "# fixture"
      echo ""
      echo "**Human-gated verbs (machine roster):** $backticked."
      echo "Prose may still name a gated verb, e.g. \`$( printf '%s\n' "$derived" | head -1 )\` is discussed here, just never as a command."
    } >"$tmp/skill/a2ahub/loops.md"
    rm -f "$tmp/skill/a2ahub/loops-extra.md"
  }

  write_good_fixture
  if ! (run_check "$tmp") >/dev/null 2>&1; then
    echo "human-gates --teeth: FAILED — a fully declared, self-consistent fixture stayed red" >&2
    return 1
  fi

  # 1. A missing machine-roster line must red naming the missing marker.
  write_good_fixture
  printf '# fixture\n\nNo roster line here.\n' >"$tmp/skill/a2ahub/loops.md"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "has a \"$MARKER_PREFIX\" line"; then
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

  # 5. A missing "loop_corpus" key in the manifest must red naming the
  # manifest and telling the reader what to add — never silently fall back
  # to a hardcoded loops.md path.
  write_good_fixture
  {
    echo '{'
    echo '  "schema": "a2a-docs-manifest/v1",'
    echo '  "sections": []'
    echo '}'
  } >"$tmp/skill/a2ahub/docs-manifest.json"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF 'has no non-empty "loop_corpus" array'; then
    echo "human-gates --teeth: FAILED — a manifest with no loop_corpus array did not red naming the missing key" >&2
    return 1
  fi

  # 6. A loop_corpus entry naming a file that does not exist must red
  # naming that file.
  write_good_fixture
  sed -i.bak 's#"loop_corpus": \["a2ahub/loops.md"\]#"loop_corpus": ["a2ahub/nonexistent.md"]#' "$tmp/skill/a2ahub/docs-manifest.json"
  rm -f "$tmp/skill/a2ahub/docs-manifest.json.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF 'names "a2ahub/nonexistent.md", which does not exist'; then
    echo "human-gates --teeth: FAILED — a loop_corpus entry naming a nonexistent file did not red naming it" >&2
    return 1
  fi

  # 7. The machine roster marker appearing in TWO corpus files must red —
  # a duplicated roster is how two answers drift apart, even if both
  # copies happen to agree today.
  write_good_fixture
  sed -i.bak 's#"loop_corpus": \["a2ahub/loops.md"\]#"loop_corpus": ["a2ahub/loops.md", "a2ahub/loops-extra.md"]#' "$tmp/skill/a2ahub/docs-manifest.json"
  rm -f "$tmp/skill/a2ahub/docs-manifest.json.bak"
  {
    echo "# fixture extra"
    echo ""
    echo "**Human-gated verbs (machine roster):** $backticked."
  } >"$tmp/skill/a2ahub/loops-extra.md"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "the \"$MARKER_PREFIX\" line appears in more than one loop_corpus file"; then
    echo "human-gates --teeth: FAILED — a roster marker duplicated across two corpus files did not red naming the duplication" >&2
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
