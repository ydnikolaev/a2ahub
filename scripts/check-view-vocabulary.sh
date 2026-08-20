#!/usr/bin/env bash
# check-view-vocabulary.sh — the view-derivation gate (agent-exchange P0, AC5/AC6)
# plus the P2 (dashboard-ui-restoration-2026-08) provenance-vocabulary ban
# (Rule B).
#
# A renderer must not decide what a state MEANS. `fold.OutcomeOf` and
# `fold.Terminal` answer that once, the read model carries the answer, and a
# component reads it. This gate is what keeps that true after the phase ships.
#
# It enforces three distinct things, and conflating them would break the rest:
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
#   Rule B — no provenance vocabulary in a user-facing slot. Words describing
#         HOW a snapshot was carried (`перенесённ*`, `carried`, `точный
#         набор`, `exact set`, `полон и пуст`) never belong in a slot that
#         tells the reader WHAT IS HAPPENING. Provenance is not deleted; it
#         moves to Технические подробности — a quoted string on a line naming
#         a `technical`-ish identifier is exempt, and so is a quoted string
#         inside a bounded `// dashboard-derivation: allow-provenance-begin --
#         <reason>` / `allow-provenance-end` marker, in the exact grammar
#         `check-dashboard-derivation.sh` already uses for its sort
#         exceptions — one convention for "this is deliberate", not two. This
#         is a genuinely blunt word list (card-spec/README.md §Rule B says so
#         too): it will catch provenance-flavored prose beyond the four
#         originally measured phrases, in files this gate does not own the
#         fix for.
#
# The vocabulary comes from the BINARY (`a2a __catalog --vocabulary --json`),
# never from a list in this file and never by reading Go source: a gate that
# names a fixed source of truth other than the shipped artifact stays green
# through exactly the drift it exists to catch. Rule B is the one exception:
# there is no binary-owned provenance-word roster, so its word list lives here,
# named as exactly that in `card-spec/README.md`'s Rule B.
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
# comparing the carried outcome is presentation, not state classification. The
# inline-chain check therefore exempts each comparison only when ITS operand is
# a carried/derived outcome — the words alone cannot grant an exemption.
vocabulary_outcomes() { # $1 = vocabulary json
  printf '%s\n' "$1" |
    awk '/"outcomes"/{f=1} f{print} f&&/\]/{exit}' |
    grep -oE '"[a-z_]+"' | tail -n +2 | tr -d '"' | sort -u
}

# state_lists finds ARRAY literals of two or more bare lowercase strings on
# one line. Prints "file:line:content". Whether one is a classification is
# decided by its CONTENT, below — this only narrows the search.
#
# Four evasions are knowingly NOT covered GENERICALLY, measured rather than
# assumed, and recorded in the epic's own epic-backlog.md: an object literal
# (`{"closed": true, …}`), a list split one string per line, a Set built by
# chained .add() calls, and a list assembled from named constants. None
# exists in the components today.
#
# P16 adds one deliberately narrow exception below: a named browser-local
# transition label/classification object. It is not generic object-literal
# recognition and derives its permitted transition keys from the binary.
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
  # The scanner is handed to awk with `-f`, not inline, and that is not style.
  # Its regex spells the set `[;{}[:space:]]`, whose two middle members form the
  # literal string `{}`. GNU find scans EVERY argument of `-exec ... +` for that
  # string and refuses with "Only one instance of {} is supported" — while BSD
  # find, which is what macOS has, only cares about the trailing placeholder.
  # So this function returned nothing at all on Linux, the `2>/dev/null` below
  # ate the refusal, and the whole AC6 inline-equality-chain rule was inert in
  # CI while its teeth passed locally. Found 2026-08-20 by the container lane
  # (`make ci-parity-docker`), which exists for exactly this.
  #
  # `-f` keeps the program out of find's argv, so the brace pair can never again
  # be read as a placeholder. Do NOT inline it back.
  local prog
  prog="$(mktemp)" || { echo "view-vocabulary: mktemp failed" >&2; return 1; }
  cat > "$prog" <<'CHAIN_AWK'
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
CHAIN_AWK
  # stderr is NOT silenced here. A scanner that cannot run must say so: an empty
  # result and a clean exit are indistinguishable to the caller, and that is the
  # shape that hid the defect above for as long as it existed.
  find "$1" -type f -name '*.dc.html' -exec awk -f "$prog" {} +
  rm -f "$prog"

  # Preserve the shipped one-line grammar above exactly, then add only the
  # missing multiline assignment/return form. A candidate begins at its
  # declaration or return and ends at the first semicolon; its branches are
  # joined with spaces so equality_pairs and outcome_operand consume the same
  # normalized statement they already understand. Diagnostics retain the
  # statement's source filename and starting line.
  find "$1" -type f -name '*.dc.html' -exec awk '
    function comparison(part) {
      return part ~ /={2,3}[[:space:]]*"[a-z_]+"/ || part ~ /"[a-z_]+"[[:space:]]*={2,3}/
    }
    function inspect(statement, start_line, span, count, branches, compared, i) {
      if (span < 2 || statement !~ /\|\|/) return
      count=split(statement, branches, /\|\|/)
      compared=0
      for (i=1; i<=count; i++) if (comparison(branches[i])) compared++
      if (compared >= 2) print FILENAME ":" start_line ":" statement
    }
    function strip_comments(value, block_start, block_end, after_start) {
      while (1) {
        if (in_block) {
          block_end=index(value, "*/")
          if (!block_end) return ""
          value=substr(value, block_end+2)
          in_block=0
        }
        block_start=index(value, "/*")
        if (!block_start) break
        after_start=substr(value, block_start+2)
        block_end=index(after_start, "*/")
        if (!block_end) {
          value=substr(value, 1, block_start-1)
          in_block=1
          break
        }
        value=substr(value, 1, block_start-1) substr(after_start, block_end+2)
      }
      sub(/[[:space:]]*\/\/.*/, "", value)
      return value
    }
    {
      text=strip_comments($0)
      if (!collecting) {
        if (text !~ /(^|[}{;[:space:]])(const|let|var)[[:space:]][A-Za-z_$][A-Za-z0-9_$]*[[:space:]]*=/ &&
            text !~ /(^|[}{;[:space:]])return[[:space:]]/) next
        collecting=1
        start_line=FNR
        span=0
        statement=""
      }
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", text)
      if (text != "") {
        statement=statement (statement == "" ? "" : " ") text
        span++
      }
      if (text ~ /;/) {
        inspect(statement, start_line, span)
        collecting=0
        statement=""
        span=0
      }
    }
  ' {} \; 2>/dev/null
}

# vocabulary_transitions reads the same binary payload as the state/outcome
# checks. It intentionally carries no transition roster of its own.
vocabulary_transitions() { # $1 = vocabulary json
  printf '%s\n' "$1" |
    awk '/"transitions"/{f=1} f{print} f&&/\]/{exit}' |
    grep -oE '"[a-z_-]+"' | tail -n +2 | tr -d '"' | sort -u
}

# transition_policy_maps is the one named exception to the generic DC-B2
# object-literal backlog. Only an assigned object whose identifier names both
# TRANSITION and LABEL/TONE/CLASSIFICATION is admitted for further checking.
# Ordinary translation/object tables remain outside this candidate grammar.
transition_policy_maps() { # $1 = scan root
  find "$1" -type f -name '*.dc.html' -exec awk '
    function strip_comments(value, block_start, block_end, after_start) {
      while (1) {
        if (in_block) {
          block_end=index(value, "*/")
          if (!block_end) return ""
          value=substr(value, block_end+2)
          in_block=0
        }
        block_start=index(value, "/*")
        if (!block_start) break
        after_start=substr(value, block_start+2)
        block_end=index(after_start, "*/")
        if (!block_end) {
          value=substr(value, 1, block_start-1)
          in_block=1
          break
        }
        value=substr(value, 1, block_start-1) substr(after_start, block_end+2)
      }
      sub(/[[:space:]]*\/\/.*/, "", value)
      return value
    }
    function emit() {
      if (span > 0) print FILENAME ":" start_line ":" statement
      collecting=0
      statement=""
      span=0
    }
    {
      text=strip_comments($0)
      upper=toupper(text)
      if (!collecting) {
        if (upper !~ /(CONST|LET|VAR)[[:space:]]+[A-Z0-9_$]*TRANSITION[A-Z0-9_$]*(LABEL|TONE|CLASSIFICATION)[A-Z0-9_$]*[[:space:]]*=/ &&
            upper !~ /(CONST|LET|VAR)[[:space:]]+[A-Z0-9_$]*(LABEL|TONE|CLASSIFICATION)[A-Z0-9_$]*TRANSITION[A-Z0-9_$]*[[:space:]]*=/) next
        collecting=1
        start_line=FNR
        statement=""
        span=0
      }
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", text)
      statement=statement (statement == "" ? "" : " ") text
      span++
      if (text ~ /}[[:space:]]*\)?[[:space:]]*;[[:space:]]*$/) emit()
    }
    END { if (collecting) emit() }
  ' {} \; 2>/dev/null
}

transition_map_pairs() { # $1 = normalized object statement
  printf '%s\n' "$1" |
    grep -oE '("[a-z_-]+"|[a-z_][a-z0-9_]*)([[:space:]]*):[[:space:]]*"[^"]+"' 2>/dev/null |
    while IFS= read -r pair; do
      local key value
      key="${pair%%:*}"
      value="${pair#*:}"
      key="$(printf '%s' "$key" | tr -d '[:space:]\"')"
      value="$(printf '%s' "$value" | sed -E 's/^[[:space:]]*"//; s/"$//')"
      printf '%s|%s\n' "$key" "$value"
    done
}

# equality_pairs emits `operand|literal` for the simple equality comparisons
# state_equality_chains admits. Literal-left and literal-right forms normalize
# to the same record. Keeping the operand is essential: `withdrawn` and
# `superseded` are both state and outcome vocabulary, so the quoted word cannot
# tell whether a component is presenting `item.outcome` or classifying `state`.
equality_pairs() { # $1 = one source line
  printf '%s\n' "$1" |
    grep -oE '([A-Za-z_$][A-Za-z0-9_$.]*|"[a-z_]+")([[:space:]]*)={2,3}([[:space:]])*([A-Za-z_$][A-Za-z0-9_$.]*|"[a-z_]+")' 2>/dev/null |
    while IFS= read -r pair; do
      local left right literal operand
      left="${pair%%=*}"
      right="${pair##*=}"
      left="$(printf '%s' "$left" | tr -d '[:space:]')"
      right="$(printf '%s' "$right" | tr -d '[:space:]')"
      if [[ "$left" == \"*\" ]]; then
        literal="${left#\"}"
        literal="${literal%\"}"
        operand="$right"
      else
        literal="${right#\"}"
        literal="${literal%\"}"
        operand="$left"
      fi
      printf '%s|%s\n' "$operand" "$literal"
    done
}

outcome_operand() { # $1 = operand, $2 = source line
  local operand="$1" content="$2" outcome_var
  case "$operand" in
    outcome|*.outcome) return 0 ;;
  esac
  # The authored dashboard's canonical helper form is
  # `const o = outcomeOf(x); return o === ...`. Recognize only the identifier
  # actually bound to outcomeOf on this line; another operand on the same line
  # receives no exemption.
  while IFS= read -r outcome_var; do
    [ "$operand" = "$outcome_var" ] && return 0
  done < <(printf '%s\n' "$content" |
    grep -oE '(const|let|var)[[:space:]]+[A-Za-z_$][A-Za-z0-9_$]*[[:space:]]*=[[:space:]]*outcomeOf[[:space:]]*\(' 2>/dev/null |
    sed -E 's/^(const|let|var)[[:space:]]+([A-Za-z_$][A-Za-z0-9_$]*).*/\2/')
  return 1
}

# resolver_tone_literals finds the chroma class namespace. The resolver is the
# sole owner: a view may consume its returned class but may not spell a class
# and thereby recreate presentation policy. No tone roster lives here; the
# namespace itself is the invariant.
resolver_tone_literals() { # $1 = scan root
  grep -rnE 'tone-[a-z][a-z0-9-]*' --include='*.dc.html' "$1" 2>/dev/null
}

check_resolver_monopoly() { # $1 = scan root
  local root="$1" owner line file content lineno
  owner="$root/VocabularyResolver.dc.html"
  if [ ! -f "$owner" ]; then
    gate_fail "view-vocabulary: missing exact resolver owner VocabularyResolver.dc.html — failing closed rather than allowing tone classes without an owner"
  fi
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file="${line%%:*}"
    content="${line#*:}"
    lineno="${content%%:*}"
    if [ "$file" != "$owner" ]; then
      gate_fail "$(basename "$file"):$lineno spells a tone- class outside VocabularyResolver.dc.html — every chroma treatment must come from the one resolver"
    fi
  done <<< "$(resolver_tone_literals "$root")"
}

# provenance_records tags each source line BEGIN/END/BAD/CODE for the
# allow-provenance marker, reusing the exact bounded-marker shape
# check-dashboard-derivation.sh uses for allow-sort. Comments are stripped
# from CODE lines so a phrase inside a comment cannot fail the gate.
provenance_records() { # $1 = file
  awk '
    function strip_comments(text, out, start, rest, finish, slash) {
      out=""
      while (1) {
        if (in_block) {
          finish=index(text, "*/")
          if (!finish) return out
          text=substr(text, finish+2)
          in_block=0
        }
        start=index(text, "/*")
        slash=index(text, "//")
        if (slash && (!start || slash < start)) return out substr(text, 1, slash-1)
        if (!start) return out text
        out=out substr(text, 1, start-1)
        rest=substr(text, start+2)
        finish=index(rest, "*/")
        if (!finish) { in_block=1; return out }
        text=substr(rest, finish+2)
      }
    }
    /dashboard-derivation:[[:space:]]*allow-provenance-begin/ {
      if ($0 ~ /^[[:space:]]*\/[\/][[:space:]]*dashboard-derivation:[[:space:]]*allow-provenance-begin[[:space:]]+--[[:space:]]+[^[:space:]].*$/)
        printf "%d\tBEGIN\t%s\n", FNR, $0
      else printf "%d\tBAD\t%s\n", FNR, $0
      next
    }
    /dashboard-derivation:[[:space:]]*allow-provenance-end/ {
      if ($0 ~ /^[[:space:]]*\/[\/][[:space:]]*dashboard-derivation:[[:space:]]*allow-provenance-end[[:space:]]*$/)
        printf "%d\tEND\t%s\n", FNR, $0
      else printf "%d\tBAD\t%s\n", FNR, $0
      next
    }
    { printf "%d\tCODE\t%s\n", FNR, strip_comments($0) }
  ' "$1"
}

# provenance_phrase tests one already-extracted quoted-string segment (without
# its surrounding quotes) against the Rule B word list from card-spec/README.md
# §Rule B: перенесённ*, carried, точный набор, exact set, полон и пуст, and
# the English equivalents. Word-boundary via [^A-Za-z] padding rather than
# `\b`, which BSD grep -E (the default on the macOS gates run under) does not
# accept.
provenance_phrase() { # $1 = segment content
  printf '%s' " $1 " | grep -qE '[Пп]еренесённ|[^A-Za-z]([Cc]arried)[^A-Za-z]|[Тт]очный[[:space:]]+набор|[Ee]xact[[:space:]]+set|[Пп]олон[[:space:]]+и[[:space:]]+пуст'
}

check_provenance_ban() { # $1 = scan root
  local root="$1" file lineno record content marker=0 segments seg
  while IFS= read -r file; do
    [ -z "$file" ] && continue
    marker=0
    while IFS=$'\t' read -r lineno record content; do
      case "$record" in
        BEGIN)
          if [ "$marker" -eq 1 ]; then
            gate_fail "view-vocabulary: $(basename "$file"):$lineno nests an allow-provenance marker — keep each exception bounded and independent"
          else
            marker=1
          fi
          ;;
        END)
          if [ "$marker" -eq 0 ]; then
            gate_fail "view-vocabulary: $(basename "$file"):$lineno has an allow-provenance end without a matching begin"
          else
            marker=0
          fi
          ;;
        BAD)
          gate_fail "view-vocabulary: $(basename "$file"):$lineno has a malformed allow-provenance marker — use the exact documented begin/end syntax with a non-empty reason"
          ;;
        CODE)
          [ "$marker" -eq 1 ] && continue
          # Extract each quoted segment together with its OWN immediately
          # preceding `key:` (if any), not the whole line: several kind
          # components pack a whole `{ key:"value", key2:"value2" }` locale
          # dictionary onto one physical line, so a line-wide "does this line
          # mention technical anywhere" test would exempt an unrelated
          # sibling key's provenance leak by accident.
          segments="$(printf '%s\n' "$content" | grep -oE '([A-Za-z_$][A-Za-z0-9_$]*[[:space:]]*:[[:space:]]*)?"[^"]*"' 2>/dev/null || true)"
          while IFS= read -r seg; do
            [ -z "$seg" ] && continue
            local key value
            key="${seg%%\"*}"
            # Технические подробности exemption: a quoted string assigned to
            # a `technical`-ish key is where Rule B says provenance moves TO,
            # not a second escape hatch that needs its own marker.
            case "$key" in
              *[Tt]echnical*) continue ;;
            esac
            value="${seg#*\"}"; value="${value%\"}"
            if provenance_phrase "$value"; then
              gate_fail "view-vocabulary: $(basename "$file"):$lineno carries provenance vocabulary in a user-facing string — \"$value\" describes how the snapshot was carried, not what is happening; move it to Технические подробности or bound it with a reasoned // dashboard-derivation: allow-provenance-begin -- <reason> marker"
            fi
          done <<< "$segments"
          ;;
      esac
    done <<< "$(provenance_records "$file")"
    if [ "$marker" -eq 1 ]; then
      gate_fail "view-vocabulary: $(basename "$file") has an unclosed allow-provenance block"
    fi
  done <<< "$(find "$root" -type f -name '*.dc.html' 2>/dev/null | sort)"
}

run_check() { # $1 = scan root
  local root="$1" json states outcomes transitions
  if ! json="$(vocabulary_json)"; then
    gate_fail "view-vocabulary: could not read the vocabulary from the binary — failing closed rather than policing nothing"
    return 1
  fi
  states="$(vocabulary_states "$json")"
  outcomes="$(vocabulary_outcomes "$json")"
  transitions="$(vocabulary_transitions "$json")"
  if [ -z "$states" ]; then
    gate_fail "view-vocabulary: the binary returned no states — failing closed rather than policing nothing"
    return 1
  fi
  if [ -z "$transitions" ]; then
    gate_fail "view-vocabulary: the binary returned no transitions — failing closed rather than allowing a local transition policy unchecked"
    return 1
  fi

  check_resolver_monopoly "$root"
  check_provenance_ban "$root"

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

  local pairs pair operand word records operands operand_records
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file="${line%%:*}"
    content="${line#*:}"
    lineno="${content%%:*}"
    content="${content#*:}"

    pairs="$(equality_pairs "$content")"
    records=""
    while IFS= read -r pair; do
      [ -z "$pair" ] && continue
      operand="${pair%%|*}"
      word="${pair#*|}"
      if outcome_operand "$operand" "$content"; then
        continue
      fi
      records="${records}${operand}|${word}"$'\n'
    done <<< "$pairs"

    operands="$(printf '%s' "$records" | cut -d'|' -f1 | sed '/^$/d' | sort -u)"
    while IFS= read -r operand; do
      [ -z "$operand" ] && continue
      operand_records="$(printf '%s' "$records" | awk -F'|' -v wanted="$operand" '$1 == wanted { print $2 }')"
      known=0
      unknown=""
      for word in $operand_records; do
        if printf '%s\n' "$states" | grep -qx "$word"; then
          known=$((known + 1))
        else
          unknown="$unknown $word"
        fi
      done

      if [ "$known" -ge 2 ]; then
        gate_fail "$(basename "$file"):$lineno classifies operand \`$operand\` by an inline equality chain over $known state names — a component must read the server-carried outcome/terminal verdict instead of spelling the state set branch by branch"
        for word in $unknown; do
          gate_fail "$(basename "$file"):$lineno compares \`$operand\` against \"$word\" inside a state classification, but the binary vocabulary has no such state"
        done
        continue
      fi

      # One known value normally cannot prove classification (a filter may
      # compare `value === "all" || value === "closed"`). An operand explicitly
      # named `state`, however, supplies that missing semantic fact: any sibling
      # literal outside the vocabulary is an unknown state comparison and must
      # fail even though the known-value count is below two.
      case "$operand" in
        state|*.state)
          if [ "$known" -ge 1 ]; then
            for word in $unknown; do
              gate_fail "$(basename "$file"):$lineno compares state operand \`$operand\` against \"$word\", but the binary vocabulary has no such state"
            done
          fi
          ;;
      esac
    done <<< "$operands"
  done <<< "$(state_equality_chains "$root")"

  local map_pairs transition_count classification_count value upper_content
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file="${line%%:*}"
    content="${line#*:}"
    lineno="${content%%:*}"
    content="${content#*:}"
    upper_content="$(printf '%s' "$content" | tr '[:lower:]' '[:upper:]')"
    map_pairs="$(transition_map_pairs "$content")"
    transition_count=0
    classification_count=0
    while IFS= read -r pair; do
      [ -z "$pair" ] && continue
      word="${pair%%|*}"
      value="${pair#*|}"
      if printf '%s\n' "$transitions" | grep -qx "$word"; then
        transition_count=$((transition_count + 1))
        case "$value" in
          positive|negative|caution) classification_count=$((classification_count + 1)) ;;
        esac
      fi
    done <<< "$map_pairs"

    if [ "$transition_count" -ge 1 ]; then
      if [[ "$upper_content" == *TRANSITION*LABEL* || "$upper_content" == *LABEL*TRANSITION* ]]; then
        gate_fail "$(basename "$file"):$lineno defines a browser-local label map for $transition_count binary-known transitions — use VocabularyResolver family \`transition\` instead"
      fi
      if [ "$classification_count" -ge 1 ]; then
        gate_fail "$(basename "$file"):$lineno classifies binary-known transitions as positive/negative/caution in the browser — use VocabularyResolver family \`transition\` instead"
      fi
    fi
  done <<< "$(transition_policy_maps "$root")"

  gate_summary "view-vocabulary"
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || { echo "view-vocabulary --teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN

  # The exact owner must exist and may spell the whole tone-class namespace.
  # Copying the production owner proves the exemption follows its exact name,
  # not a reduced fixture that could drift from the real resolver.
  cp "$GATE_ROOT/web/design-source/VocabularyResolver.dc.html" "$tmp/VocabularyResolver.dc.html"
  if ! ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — the exact resolver owner was refused" >&2
    return 1
  fi

  # A non-owner literal must red and name its exact source location.
  cat > "$tmp/Offender.dc.html" <<'FIXTURE'
<span class="tone-invented">bad</span>
FIXTURE
  local offender_output
  if offender_output="$( ( run_check "$tmp" ) 2>&1 )"; then
    echo "view-vocabulary --teeth: FAILED — a non-owner tone class stayed green" >&2
    return 1
  fi
  if [[ "$offender_output" != *"Offender.dc.html:1"* ]]; then
    echo "view-vocabulary --teeth: FAILED — the tone-class diagnostic omitted filename/line" >&2
    return 1
  fi
  rm -f "$tmp/Offender.dc.html"

  # The filename is part of the monopoly. Renaming the owner must fail closed
  # even though all of its literal classes still exist in the scan tree.
  mv "$tmp/VocabularyResolver.dc.html" "$tmp/RenamedResolver.dc.html"
  local renamed_output
  if renamed_output="$( ( run_check "$tmp" ) 2>&1 )"; then
    echo "view-vocabulary --teeth: FAILED — a renamed resolver owner stayed green" >&2
    return 1
  fi
  if [[ "$renamed_output" != *"missing exact resolver owner VocabularyResolver.dc.html"* ]]; then
    echo "view-vocabulary --teeth: FAILED — a missing resolver owner did not fail closed by name" >&2
    return 1
  fi
  mv "$tmp/RenamedResolver.dc.html" "$tmp/VocabularyResolver.dc.html"

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

  # Assignment and return each have their own multiline grammar. They must be
  # normalized into one statement before the existing equality-pair analysis.
  local new_shape_failed=0
  cat > "$tmp/AssignedMultilineChain.dc.html" <<'FIXTURE'
<script>
function settled(state) {
  const result =
    state === "closed" ||
    state === "verified";
  return result;
}
</script>
FIXTURE
  local multiline_output
  if multiline_output="$( ( run_check "$tmp" ) 2>&1 )"; then
    echo "view-vocabulary --teeth: FAILED — a multiline assigned state classification stayed green" >&2
    new_shape_failed=1
  elif [[ "$multiline_output" != *"AssignedMultilineChain.dc.html:3"* ]]; then
    echo "view-vocabulary --teeth: FAILED — multiline assignment diagnostic omitted its filename/starting line" >&2
    new_shape_failed=1
  fi
  rm -f "$tmp/AssignedMultilineChain.dc.html"

  cat > "$tmp/ReturnedMultilineChain.dc.html" <<'FIXTURE'
<script>
function refused(state) {
  return (
    state === "declined" ||
    state === "closed"
  );
}
</script>
FIXTURE
  if multiline_output="$( ( run_check "$tmp" ) 2>&1 )"; then
    echo "view-vocabulary --teeth: FAILED — a multiline returned state classification stayed green" >&2
    new_shape_failed=1
  elif [[ "$multiline_output" != *"ReturnedMultilineChain.dc.html:3"* ]]; then
    echo "view-vocabulary --teeth: FAILED — multiline return diagnostic omitted its filename/starting line" >&2
    new_shape_failed=1
  fi
  rm -f "$tmp/ReturnedMultilineChain.dc.html"

  # Generic object-literal detection remains outside this gate. These two
  # named transition policy surfaces are the deliberately narrow exception.
  cat > "$tmp/TransitionLabels.dc.html" <<'FIXTURE'
<script>
const TRANSITION_LABELS = Object.freeze({
  "accept": "Accept",
  "reject": "Reject"
});
</script>
FIXTURE
  local transition_output
  if transition_output="$( ( run_check "$tmp" ) 2>&1 )"; then
    echo "view-vocabulary --teeth: FAILED — a browser-local transition label map stayed green" >&2
    new_shape_failed=1
  elif [[ "$transition_output" != *"TransitionLabels.dc.html:2"* ]]; then
    echo "view-vocabulary --teeth: FAILED — transition-label diagnostic omitted its filename/starting line" >&2
    new_shape_failed=1
  fi
  rm -f "$tmp/TransitionLabels.dc.html"

  cat > "$tmp/TransitionClassification.dc.html" <<'FIXTURE'
<script>
const TRANSITION_TONES = Object.freeze({
  "accept": "positive",
  "reject": "negative",
  "block": "caution"
});
</script>
FIXTURE
  if transition_output="$( ( run_check "$tmp" ) 2>&1 )"; then
    echo "view-vocabulary --teeth: FAILED — a browser-local transition classification stayed green" >&2
    new_shape_failed=1
  elif [[ "$transition_output" != *"TransitionClassification.dc.html:2"* ]]; then
    echo "view-vocabulary --teeth: FAILED — transition-classification diagnostic omitted its filename/starting line" >&2
    new_shape_failed=1
  fi
  rm -f "$tmp/TransitionClassification.dc.html"
  if [ "$new_shape_failed" -ne 0 ]; then
    return 1
  fi

  # These two fixtures pin the audit escapes independently. Accumulate their
  # results so one false green cannot hide the other during the gate's own red
  # proof.
  local escape_failed=0
  cat > "$tmp/OutcomeOperandEscape.dc.html" <<'FIXTURE'
<script>
function abandoned(state) {
  return state === "withdrawn" || state === "superseded";
}
</script>
FIXTURE
  if ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — outcome names compared on a state operand stayed green" >&2
    escape_failed=1
  fi
  rm -f "$tmp/OutcomeOperandEscape.dc.html"

  cat > "$tmp/UnknownStateEscape.dc.html" <<'FIXTURE'
<script>
function closed(state) {
  return state === "closed" || state === "clsoed";
}
</script>
FIXTURE
  if ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — an unknown state comparison beside only one known state stayed green" >&2
    escape_failed=1
  fi
  rm -f "$tmp/UnknownStateEscape.dc.html"
  if [ "$escape_failed" -ne 0 ]; then
    return 1
  fi

  # A component that only reads the payload must stay green. So must a single
  # equality, an unrelated non-vocabulary chain, and a forbidden chain shown
  # only inside a comment.
  cat > "$tmp/Good.dc.html" <<'FIXTURE'
<script>
const tone = it.outcome === "settled" ? "healthy" : "neutral";
const selected = state === "closed";
const theme = mode === "dark" || mode === "light";
/* const oldGuess = state === "closed" || state === "verified"; */
/*
const oldMultilineGuess =
  state === "closed" ||
  state === "verified";
*/

const abandoned =
  it.outcome === "withdrawn" ||
  it.outcome === "superseded";

const translatedActions = {
  save_form: { en: "Save", ru: "Сохранить" },
  dismiss_dialog: { en: "Dismiss", ru: "Закрыть" }
};
/*
const TRANSITION_LABELS = Object.freeze({
  "accept": "Accept",
  "reject": "Reject"
});
*/
const transitionPresentation = A2A_VOCABULARY_RESOLVER.lookup(
  data,
  "transition",
  event.transition,
  locale
);
</script>
FIXTURE
  if ! ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — a component reading the payload was refused" >&2
    return 1
  fi

  # Rule B — the provenance-vocabulary ban (P2, dashboard-ui-restoration-2026-08).
  # Green control FIRST: ordinary copy plus a provenance phrase correctly
  # parked behind a `technical`-ish key must stay green — that key IS where
  # Rule B says provenance moves to.
  cat > "$tmp/ProvenanceGood.dc.html" <<'FIXTURE'
<script>
const copy = {
  lead: "3 documents need your move.",
  technicalHint: "точный перенесённый набор",
};
</script>
FIXTURE
  if ! ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — ordinary copy plus a technical-keyed provenance phrase was refused" >&2
    return 1
  fi
  rm -f "$tmp/ProvenanceGood.dc.html"

  # A named-offender RU phrase reds, naming file:line.
  cat > "$tmp/ProvenanceRU.dc.html" <<'FIXTURE'
<script>
const hint = "точный перенесённый набор";
</script>
FIXTURE
  local ru_output
  if ru_output="$( ( run_check "$tmp" ) 2>&1 )"; then
    echo "view-vocabulary --teeth: FAILED — a RU provenance phrase stayed green" >&2
    return 1
  elif [[ "$ru_output" != *"ProvenanceRU.dc.html:2"* ]]; then
    echo "view-vocabulary --teeth: FAILED — RU provenance diagnostic omitted filename/line" >&2
    return 1
  fi
  rm -f "$tmp/ProvenanceRU.dc.html"

  # Its EN equivalent reds too — AC-4 requires both locales.
  cat > "$tmp/ProvenanceEN.dc.html" <<'FIXTURE'
<script>
const hint = "exact carried set";
</script>
FIXTURE
  local en_output
  if en_output="$( ( run_check "$tmp" ) 2>&1 )"; then
    echo "view-vocabulary --teeth: FAILED — an EN provenance phrase stayed green" >&2
    return 1
  elif [[ "$en_output" != *"ProvenanceEN.dc.html:2"* ]]; then
    echo "view-vocabulary --teeth: FAILED — EN provenance diagnostic omitted filename/line" >&2
    return 1
  fi
  rm -f "$tmp/ProvenanceEN.dc.html"

  # The phrase inside a bounded, reasoned marker greens (AC-5, non-empty half).
  cat > "$tmp/ProvenanceMarked.dc.html" <<'FIXTURE'
<script>
// dashboard-derivation: allow-provenance-begin -- shown once in a roadmap prototype, not the live dashboard
const hint = "exact carried set";
// dashboard-derivation: allow-provenance-end
</script>
FIXTURE
  if ! ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "view-vocabulary --teeth: FAILED — a bounded, reasoned allow-provenance marker was refused" >&2
    return 1
  fi
  rm -f "$tmp/ProvenanceMarked.dc.html"

  # The same marker with an EMPTY reason fails closed (AC-5, empty half).
  cat > "$tmp/ProvenanceEmptyReason.dc.html" <<'FIXTURE'
<script>
// dashboard-derivation: allow-provenance-begin --
const hint = "exact carried set";
// dashboard-derivation: allow-provenance-end
</script>
FIXTURE
  local empty_reason_output
  if empty_reason_output="$( ( run_check "$tmp" ) 2>&1 )"; then
    echo "view-vocabulary --teeth: FAILED — an allow-provenance marker with an empty reason stayed green" >&2
    return 1
  elif [[ "$empty_reason_output" != *"malformed allow-provenance marker"* ]]; then
    echo "view-vocabulary --teeth: FAILED — empty-reason marker diagnostic did not name the malformed marker" >&2
    return 1
  fi
  rm -f "$tmp/ProvenanceEmptyReason.dc.html"

  # An unclosed marker fails closed too, even with no banned phrase inside it.
  cat > "$tmp/ProvenanceUnclosed.dc.html" <<'FIXTURE'
<script>
// dashboard-derivation: allow-provenance-begin -- proving the unclosed-block guard
const hint = "ordinary copy, no banned words here";
</script>
FIXTURE
  local unclosed_output
  if unclosed_output="$( ( run_check "$tmp" ) 2>&1 )"; then
    echo "view-vocabulary --teeth: FAILED — an unclosed allow-provenance block stayed green" >&2
    return 1
  elif [[ "$unclosed_output" != *"unclosed allow-provenance block"* ]]; then
    echo "view-vocabulary --teeth: FAILED — unclosed-marker diagnostic did not name the block" >&2
    return 1
  fi
  rm -f "$tmp/ProvenanceUnclosed.dc.html"

  echo "view-vocabulary --teeth: ok"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

run_check "$GATE_ROOT/web/design-source"
exit $?
