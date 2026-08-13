#!/usr/bin/env bash
# check-view-vocabulary.sh — the view-derivation gate (agent-exchange P0, AC5/AC6).
#
# A renderer must not decide what a state MEANS. `fold.OutcomeOf` and
# `fold.Terminal` answer that once, the read model carries the answer, and a
# component reads it. This gate is what keeps that true after the phase ships.
#
# It enforces two distinct things, and conflating them would break the second:
#
#   AC6 — no self-made classification. A component may not carry a LIST of
#         state names and derive meaning from membership in it. That is what
#         SUCCESS_STATES / CANCELLED_STATES / REFUSED_STATES were, and being
#         type-blind is what made them wrong: `retired` filed as cancelled,
#         handoff `accepted` conflated with question `accepted`.
#
#   AC5 — no name outside the domain's own vocabulary. A component that spells
#         a state name at all must spell one that EXISTS. This is deliberately
#         not "no state literals": a translation table has to name every state
#         to label it in Russian, and forbidding that would forbid
#         localisation. What it forbids is a name the domain does not have —
#         `canceled` with one `l`, which lived inside CANCELLED_STATES and
#         matched nothing the fold could ever produce.
#
# The vocabulary comes from the BINARY (`a2a __catalog --vocabulary --json`),
# never from a list in this file and never by reading Go source: a gate that
# names a fixed source of truth other than the shipped artifact stays green
# through exactly the drift it exists to catch.
#
# Scope note: this scans web/design-source/**, the AUTHORED components.
# internal/html/template.html is generated from them and is proven identical
# by the `dashboard-template-drift` gate, which runs in the same lane — so a
# component fixed here cannot ship unfixed there without that gate reding.
#
# Usage: bash scripts/check-view-vocabulary.sh            # check the components
#        bash scripts/check-view-vocabulary.sh --teeth    # self-test on fixtures

# lane-inputs:
#   web/design-source/**
#   **/*.go
#   go.mod
#   go.sum
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   below self-locates scripts/lib/gate-lib.sh from this script's own path, so
#   the classifier cannot resolve the $(dirname ...) substitution to a literal.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

SCRIPT_ABS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"

# vocabulary_json asks the binary. The outer verification runner supplies the
# exact binary shared with e2e; a direct invocation falls back to `go run`.
# A failure fails CLOSED — an empty vocabulary would let this gate pass while
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

# vocabulary_states prints every STATE name the domain has, one per line.
# Deliberately only the states: `contract` is a kind and `submit` a
# transition, and a component listing artifact kinds or verbs is doing
# something ordinary. What this gate is about is deciding what a STATE means.
#
# Parsed with a small awk pass rather than jq — jq is not a dependency of
# `make check`, and a gate that skips when a tool is missing is not a gate.
# The `grep -v` drops the map KEYS (each kind, spelled `"contract": [`) and
# keeps their values.
vocabulary_states() { # $1 = vocabulary json
  printf '%s\n' "$1" |
    awk '/"states"/{f=1;next} f&&/^  \}/{f=0} f' |
    grep -vE '":[[:space:]]*\[' |
    grep -oE '"[a-z_]+"' | tr -d '"' | sort -u
}

# vocabulary_outcomes prints the domain meanings a server may carry. Several
# outcome names intentionally overlap state names (`withdrawn`, `superseded`);
# comparing the carried outcome is presentation, not state classification, so
# the inline-chain check below excludes a chain made entirely of outcomes.
vocabulary_outcomes() { # $1 = vocabulary json
  printf '%s\n' "$1" |
    awk '/"outcomes"/{f=1} f{print} f&&/\]/{exit}' |
    grep -oE '"[a-z_]+"' | tail -n +2 | tr -d '"' | sort -u
}

# state_lists finds ARRAY literals of two or more bare lowercase strings on
# one line. Prints "file:line:content". Whether one is a classification is
# decided by its CONTENT, below — this only narrows the search.
#
# Four evasions are knowingly NOT covered, measured rather than assumed, and
# recorded in the epic's own epic-backlog.md: an object literal
# (`{"closed": true, …}`), a list split one string per line, a Set built by
# chained .add() calls, and a list assembled from named constants. None
# exists in the components today.
#
# The object-literal form was TRIED and reverted, and the reason is worth
# keeping: widening the pattern to `{` made the gate red on
# A2A_STATUS_GUIDE — a 26-entry human glossary that names every state so the
# UI can label it, alongside `blocking`, `overdue` and `space stale`, which
# are not states at all. That list is a vocabulary for a reader, not a rule
# for a machine, and refusing it would be refusing the thing this gate
# explicitly set out not to refuse. Catching a hypothetical evasion at the
# cost of a false positive on real, correct code is the wrong trade; the
# honest boundary is a narrow pattern plus a written note of where it ends.
state_lists() { # $1 = scan root
  grep -rnE '\[[[:space:]]*"[a-z_]+"([[:space:]]*,[[:space:]]*"[a-z_]+")+[[:space:]]*\]' \
    --include='*.dc.html' "$1" 2>/dev/null
}

# state_equality_chains finds a one-line assignment or return whose `||`
# branches contain equality comparisons. This is the inlined form of the state
# arrays above. Requiring comparisons on at least two distinct branches avoids
# treating an unrelated fallback (`lookup[key] || "neutral"`) as a chain.
state_equality_chains() { # $1 = scan root
  find "$1" -type f -name '*.dc.html' -exec awk '
    function comparison(part) {
      return part ~ /={2,3}[[:space:]]*"[a-z_]+"/ || part ~ /"[a-z_]+"[[:space:]]*={2,3}/
    }
    {
      text=$0
	  # Strip block comments, including a block spanning input lines. A
	  # commented example documents a forbidden shape; it does not execute
	  # one and must not make the component fail the gate.
	  while (1) {
	    if (in_block) {
	      block_end=index(text, "*/")
	      if (!block_end) { text=""; break }
	      text=substr(text, block_end+2)
	      in_block=0
	    }
	    block_start=index(text, "/*")
	    if (!block_start) break
	    after_start=substr(text, block_start+2)
	    block_end=index(after_start, "*/")
	    if (!block_end) {
	      text=substr(text, 1, block_start-1)
	      in_block=1
	      break
	    }
	    text=substr(text, 1, block_start-1) substr(after_start, block_end+2)
	  }
      sub(/[[:space:]]*\/\/.*/, "", text)
      if (text !~ /\|\|/) next
      if (text !~ /(^|[;{}[:space:]])(const|let|var)[[:space:]][A-Za-z_$][A-Za-z0-9_$]*[[:space:]]*=/ &&
          text !~ /(^|[;{}[:space:]])return[[:space:]]/) next
      count=split(text, branches, /\|\|/)
      compared=0
      for (i=1; i<=count; i++) if (comparison(branches[i])) compared++
      if (compared >= 2) print FILENAME ":" FNR ":" $0
    }
  ' {} + 2>/dev/null
}

run_check() { # $1 = scan root
  local root="$1" json states outcomes
  if ! json="$(vocabulary_json)"; then
    gate_fail "view-vocabulary: could not read the vocabulary from the binary — failing closed rather than policing nothing"
    return 1
  fi
  states="$(vocabulary_states "$json")"
  outcomes="$(vocabulary_outcomes "$json")"
  if [ -z "$states" ]; then
    gate_fail "view-vocabulary: the binary returned no states — failing closed rather than policing nothing"
    return 1
  fi

  local line file lineno content words word known unknown
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file="${line%%:*}"
    content="${line#*:}"
    lineno="${content%%:*}"
    content="${content#*:}"

    words="$(printf '%s\n' "$content" | grep -oE '"[a-z_]+"' | tr -d '"')"
    known=0
    unknown=""
    for word in $words; do
      if printf '%s\n' "$states" | grep -qx "$word"; then
        known=$((known + 1))
      else
        unknown="$unknown $word"
      fi
    done

    # ONE state name in a list is not a classification — an artifact-kind
    # list, a css class list, an icon key list all legitimately contain a
    # word that happens to also be a state. TWO or more is a set built to
    # test membership against, which is the thing being removed.
    if [ "$known" -lt 2 ]; then
      continue
    fi

    # AC6 — the classification itself.
    gate_fail "$(basename "$file"):$lineno classifies by its own list of $known state names — a component must read \`outcome\` and \`terminal\` off the payload. Being type-blind is what made the retired sets wrong: \`retired\` filed as cancelled, handoff \`accepted\` conflated with question \`accepted\`"

    # AC5 — a name inside that list which the domain does not have. This is
    # where `canceled` (one l) surfaced: it sat in CANCELLED_STATES beside
    # the real `cancelled` and could never match anything the fold produces.
    for word in $unknown; do
      gate_fail "$(basename "$file"):$lineno lists \"$word\", which is not a state the domain has — the binary's \`__catalog --vocabulary --json\` is the list, and a name outside it matches nothing the fold can ever produce"
    done
  done <<< "$(state_lists "$root")"

  local outcome_known
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file="${line%%:*}"
    content="${line#*:}"
    lineno="${content%%:*}"
    content="${content#*:}"

    words="$(printf '%s\n' "$content" | grep -oE '"[a-z_]+"' | tr -d '"')"
    known=0
    outcome_known=0
    unknown=""
    for word in $words; do
      if printf '%s\n' "$states" | grep -qx "$word"; then
        known=$((known + 1))
        if printf '%s\n' "$outcomes" | grep -qx "$word"; then
          outcome_known=$((outcome_known + 1))
        fi
      else
        unknown="$unknown $word"
      fi
    done

    if [ "$known" -lt 2 ] || [ "$known" -eq "$outcome_known" ]; then
      continue
    fi
    gate_fail "$(basename "$file"):$lineno classifies by an inline equality chain over $known state names — a component must read the server-carried outcome/terminal verdict instead of spelling the state set branch by branch"
    for word in $unknown; do
      gate_fail "$(basename "$file"):$lineno compares against \"$word\" inside a state classification, but the binary vocabulary has no such state"
    done
  done <<< "$(state_equality_chains "$root")"

  gate_summary "view-vocabulary"
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || { echo "view-vocabulary --teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN

  # A component that classifies by its own list must red.
  cat > "$tmp/Bad.dc.html" <<'FIXTURE'
<script>
const SUCCESS_STATES = ["closed", "verified"];
</script>
FIXTURE
  # Each fixture runs in a SUBSHELL: gate-lib's error counters are
  # process-global, so a second run_check in the same shell inherits the
  # first one's tally and every later fixture reds for the wrong reason.
  if ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — a self-made classification list stayed green" >&2
    return 1
  fi
  rm -f "$tmp/Bad.dc.html"

  # A name the domain does not have, inside a classification, must red for
  # BOTH reasons — and this is the exact shape the real defect had:
  # CANCELLED_STATES carried `canceled` with one `l` beside the real
  # `cancelled`, matching nothing the fold can ever produce.
  cat > "$tmp/Typo.dc.html" <<'FIXTURE'
<script>
const CANCELLED_STATES = ["cancelled", "withdrawn", "canceled"];
</script>
FIXTURE
  if ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — a name outside the vocabulary stayed green" >&2
    return 1
  fi
  rm -f "$tmp/Typo.dc.html"

  # An assigned inline equality chain is the same classification with the
  # array inlined, and must red on its own.
  cat > "$tmp/AssignedInlineChain.dc.html" <<'FIXTURE'
<script>
function settled(state) {
  const result = state === "closed" || state === "verified";
  return result;
}
</script>
FIXTURE
  if ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — an assigned inline state classification stayed green" >&2
    return 1
  fi
  rm -f "$tmp/AssignedInlineChain.dc.html"

  # A returned chain is a separate grammar branch; proving assignment red does
  # not prove return red, so it gets an isolated run too.
  cat > "$tmp/ReturnedInlineChain.dc.html" <<'FIXTURE'
<script>
function refused(state) {
  return state === "declined" || state === "closed";
}
</script>
FIXTURE
  if ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — a returned inline state classification stayed green" >&2
    return 1
  fi
  rm -f "$tmp/ReturnedInlineChain.dc.html"

  # A component that only reads the payload must stay green. So must a single
  # equality, an unrelated non-vocabulary chain, and a forbidden chain shown
  # only inside a comment.
  cat > "$tmp/Good.dc.html" <<'FIXTURE'
<script>
const tone = it.outcome === "settled" ? "healthy" : "neutral";
const selected = state === "closed";
const theme = mode === "dark" || mode === "light";
/* const oldGuess = state === "closed" || state === "verified"; */
</script>
FIXTURE
  if ! ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — a component reading the payload was refused" >&2
    return 1
  fi

  echo "view-vocabulary --teeth: ok"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

run_check "$GATE_ROOT/web/design-source"
exit $?
