#!/usr/bin/env bash
# check-prose-coverage.sh — the prose-coverage ledger gate (agent-exchange-2026-08
# spec 13, §4 AC5).
#
# The universe is (cmd:<name> | tool:<name> | field:<surface>:<key>), DERIVED
# from the built binary, never hand-listed:
#
#   cmd:/tool:  every "## Commands" / "## MCP tools" row's own name from the
#               bare `a2a __catalog` markdown.
#   field:      every (surface, key) pair from
#               `a2a __catalog --surfaces --json` — see
#               cmd/a2a/catalog.go's catalogSurfaces() doc comment for the
#               five surfaces and the residual hole (a newly added SURFACE
#               is not derived; a newly added FIELD on an already-listed
#               surface is).
#
# Every derived member must appear in schemas/prose-coverage.yaml's
# `covered:` map (naming a skill page AND a heading anchor, both of which
# this gate confirms actually exist) or its `declared:` map (a non-blank,
# STRUCTURAL reason — a reason that reads as a DEFERRAL is refused, see
# is_deferral_reason below). A member in neither reds by name — that is the
# deliverable: a capability shipping with no prose row becomes visible
# instead of discovered at the next tagged release (spec 13 §1).
#
# `schemas/prose-coverage.yaml`'s own `surfaces:` list is cross-checked
# against the binary's OWN `--surfaces --json` top-level key set, in both
# directions — the other half of the residual hole cmd/a2a/catalog.go's own
# doc comment states: a surface catalogSurfaces() adds without updating this
# file's `surfaces:` list reds here, rather than silently deriving zero
# field: members for it.
#
# Parsed with grep/sed/awk, not jq — jq is not a dependency of `make check`
# (the `check-view-vocabulary.sh` precedent, restated by every sibling gate
# that reads docs-manifest.json/loop-phases.yaml this way).
#
# Usage: bash scripts/check-prose-coverage.sh            # check the real tree
#        bash scripts/check-prose-coverage.sh --teeth    # self-test on fixtures

# lane-inputs:
#   schemas/prose-coverage.yaml
#   skill/a2ahub/**
#   **/*.go
#   go.mod
#   go.sum
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   below self-locates scripts/lib/gate-lib.sh from this script's own path, so
#   the classifier cannot resolve the $(dirname ...) substitution to a
#   literal. `--teeth`'s own fixture writes under a `mktemp -d` directory are
#   the SYNTHETIC corpus this gate's teeth judge, never the real tree — see
#   check-loop-reachability.sh's own header for the same reasoning applied to
#   its sibling gate.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# catalog_markdown asks the binary — never a literal list — for the bare
# `a2a __catalog` markdown (the source of every cmd:/tool: universe member).
# The outer verification runner supplies the exact binary shared with e2e; a
# direct invocation falls back to `go run`. A failure fails CLOSED: an
# empty/unreadable catalog would let this gate pass while policing nothing.
catalog_markdown() {
  if [ -n "${A2A_VERIFY_BINARY:-}" ]; then
    if [ ! -x "$A2A_VERIFY_BINARY" ]; then
      echo "A2A_VERIFY_BINARY is not executable: $A2A_VERIFY_BINARY" >&2
      return 1
    fi
    "$A2A_VERIFY_BINARY" __catalog
    return
  fi
  ( cd "$GATE_ROOT" && GOWORK=off go run ./cmd/a2a __catalog )
}

# surfaces_json asks the binary for `a2a __catalog --surfaces --json` (the
# source of every field: universe member). Same binary-or-go-run seam as
# catalog_markdown.
surfaces_json() {
  if [ -n "${A2A_VERIFY_BINARY:-}" ]; then
    if [ ! -x "$A2A_VERIFY_BINARY" ]; then
      echo "A2A_VERIFY_BINARY is not executable: $A2A_VERIFY_BINARY" >&2
      return 1
    fi
    "$A2A_VERIFY_BINARY" __catalog --surfaces --json
    return
  fi
  ( cd "$GATE_ROOT" && GOWORK=off go run ./cmd/a2a __catalog --surfaces --json )
}

# catalog_commands prints every "## Commands" row's own name, one per line
# — a sub-verb's name is "parent sub" (e.g. "contract publish"), the SAME
# space-joined row catalogCommandRows() renders (cmd/a2a/catalog.go's own
# doc comment: a hyphen used to render seven undispatchable names).
catalog_commands() { # $1 = catalog markdown
  printf '%s\n' "$1" |
    awk '/^## Commands/{f=1;next} /^## MCP tools/{f=0} f' |
    grep '^- `' | sed -E 's/^- `([^`]+)`.*/\1/'
}

# catalog_tools prints every "## MCP tools" row's own name, one per line.
catalog_tools() { # $1 = catalog markdown
  printf '%s\n' "$1" |
    awk '/^## MCP tools/{f=1;next} f' |
    grep '^- `' | sed -E 's/^- `([^`]+)`.*/\1/'
}

# surface_names prints every top-level key of the `--surfaces --json`
# object, one per line — the surface-name universe `surfaces:` is
# cross-checked against. Matches Go's `json.Encoder` with `SetIndent("", "
# ")`: every top-level key is indented by exactly two spaces.
surface_names() { # $1 = surfaces json
  printf '%s\n' "$1" | grep -E '^  "[A-Za-z0-9_-]+": \[$' | sed -E 's/^  "//; s/": \[$//'
}

# surface_keys prints every JSON key one surface's array carries, one per
# line, stripped of quotes and any trailing comma.
surface_keys() { # $1 = surfaces json, $2 = surface name
  local json="$1" surface="$2" line
  line="  \"$surface\": ["
  printf '%s\n' "$json" | awk -v want="$line" '
    $0 == want { f=1; next }
    f && $0 ~ /^  \]/ { f=0; next }
    f { print }
  ' | sed -E 's/^[[:space:]]*"//; s/",?[[:space:]]*$//'
}

# ledger_surfaces prints schemas/prose-coverage.yaml's own `surfaces:`
# block-list entries, one per line, in file order.
ledger_surfaces() { # $1 = yaml path
  awk '
    /^surfaces:[ \t]*$/ { insec=1; next }
    /^[a-zA-Z_]+:[ \t]*$/ { insec=0 }
    insec && /^[ \t]*-[ \t]/ {
      line = $0
      sub(/^[ \t]*-[ \t]*/, "", line)
      gsub(/[ \t]+$/, "", line)
      print line
    }
  ' "$1"
}

# ledger_keys prints every key declared inside one top-level section
# (`covered:` or `declared:`) of prose-coverage.yaml, one per line, in file
# order. A `# comment` line at the same 2-space indent is skipped — this is
# the fix loop-coverage.sh's own ledger_keys does not need (its comments
# never contain a bare "name: " substring; this file's do, e.g. "# --
# commands: the verb families ...").
ledger_keys() { # $1 = yaml path, $2 = section ("covered:" or "declared:")
  awk -v section="$2" '
    BEGIN { insec = 0 }
    /^[a-zA-Z_]+:[ \t]*$/ {
      insec = ($0 == section) ? 1 : 0
      next
    }
    insec == 1 {
      if (substr($0, 1, 2) != "  ") next
      body = substr($0, 3)
      if (substr(body, 1, 1) == "#") next
      colon = index(body, ": ")
      if (colon == 0) next
      print substr(body, 1, colon - 1)
    }
  ' "$1"
}

# ledger_has_key reports (via exit status) whether key is DECLARED inside
# section, independent of its value — deliberately separate from
# ledger_value: a key declared with a BLANK value and a key never declared
# at all both produce an empty string from command substitution, and
# collapsing "absent" and "present-but-blank" would misreport a blank
# `declared:` reason as "no ledger entry" instead of "blank reason" (the
# check-loop-coverage.sh precedent this function copies).
ledger_has_key() { # $1 = yaml path, $2 = section, $3 = key
  ledger_keys "$1" "$2" | grep -qxF "$3"
}

# ledger_value prints the (quote-stripped, `\"`-unescaped) value of one key
# inside one section, or nothing if that key has no row there. Matches by
# literal string equality — never builds a dynamic regex from key, so a key
# containing a regex-meaningful character (this file's own `:`-joined,
# space-carrying keys) can never be misread. Unescaping `\"` matters here in
# a way it does not for loop-phases.yaml's own values: several of this
# corpus's real headings carry a literal quoted phrase (`§8.2 Send loop —
# "I need something from another system"`), so a covered: value naming one
# must itself escape that inner `"`, and this function must undo it before
# comparing against the heading text anchor_exists reads off disk verbatim.
ledger_value() { # $1 = yaml path, $2 = section, $3 = key
  awk -v section="$2" -v key="$3" '
    BEGIN { insec = 0 }
    /^[a-zA-Z_]+:[ \t]*$/ {
      insec = ($0 == section) ? 1 : 0
      next
    }
    insec == 1 {
      if (substr($0, 1, 2) != "  ") next
      body = substr($0, 3)
      if (substr(body, 1, 1) == "#") next
      colon = index(body, ": ")
      if (colon == 0) next
      k = substr(body, 1, colon - 1)
      if (k != key) next
      v = substr(body, colon + 2)
      gsub(/^"/, "", v)
      gsub(/"[ \t]*$/, "", v)
      gsub(/\\"/, "\"", v)
      print v
      exit
    }
  ' "$1"
}

# -- declared_classes: parsing --------------------------------------------
#
# A `declared:` reason written 100+ times, one per member, is the unbounded
# human cost this phase exists to remove — but a bulk exemption with one
# vague reason is worse. `declared_classes:` is the middle path: a NAMED
# class carries exactly ONE structural reason plus an EXPLICIT member list
# — the reason is written once, and a member not listed anywhere (covered:,
# declared:, or a class) still reds by name, so a class can never become a
# silent catch-all for whatever ships next.
#
# Shape (three indent levels, 2 spaces each, matching this file's own
# convention):
#   declared_classes:
#     <class-name>:
#       reason: "<one structural sentence>"
#       members:
#         - <universe member>
#         - <universe member>
#
# i.e. class name at 2 spaces, `reason:`/`members:` at 4 spaces, each
# member's `- ` line at 6 spaces.
#
# class_names prints every class name declared under `declared_classes:`,
# one per line, in file order.
class_names() { # $1 = yaml path
  awk '
    /^declared_classes:[ \t]*$/ { insec = 1; next }
    insec && /^[a-zA-Z_]+:[ \t]*$/ { insec = 0 }
    insec && /^  [A-Za-z0-9_-]+:[ \t]*$/ {
      name = $0
      sub(/^  /, "", name)
      sub(/:[ \t]*$/, "", name)
      print name
    }
  ' "$1"
}

# class_reason prints one class's (quote-stripped, `\"`-unescaped) reason,
# or nothing if that class has no `reason:` line.
class_reason() { # $1 = yaml path, $2 = class name
  awk -v want="$2" '
    /^declared_classes:[ \t]*$/ { insec = 1; next }
    insec && /^[a-zA-Z_]+:[ \t]*$/ { insec = 0 }
    insec && /^  [A-Za-z0-9_-]+:[ \t]*$/ {
      name = $0
      sub(/^  /, "", name)
      sub(/:[ \t]*$/, "", name)
      cur = (name == want)
      next
    }
    insec && cur && /^    reason:/ {
      v = $0
      sub(/^    reason:[ \t]*/, "", v)
      gsub(/^"/, "", v)
      gsub(/"[ \t]*$/, "", v)
      gsub(/\\"/, "\"", v)
      print v
      exit
    }
  ' "$1"
}

# class_members prints one class's `members:` list, one universe member per
# line, in file order.
class_members() { # $1 = yaml path, $2 = class name
  awk -v want="$2" '
    /^declared_classes:[ \t]*$/ { insec = 1; next }
    insec && /^[a-zA-Z_]+:[ \t]*$/ { insec = 0 }
    insec && /^  [A-Za-z0-9_-]+:[ \t]*$/ {
      name = $0
      sub(/^  /, "", name)
      sub(/:[ \t]*$/, "", name)
      cur = (name == want)
      next
    }
    insec && cur && /^      - / {
      m = $0
      sub(/^      - /, "", m)
      print m
    }
  ' "$1"
}

# is_deferral_reason reports (via exit status) whether a `declared:` reason
# READS AS A DEFERRAL rather than a structural fact — spec 13 §4 AC5's own
# words: "not documented yet", "later", "TODO", a bare phase id all reopen
# the exact class this ledger exists to close, one list over.
#
# THE RULE, stated once here because AC5 asks the gate script to choose one
# and say so: case-insensitive substring/word match against a fixed list of
# deferral vocabulary (todo, fixme, "not [yet] documented", later, soon,
# pending, backlog, deferred, planned, coming, "will be"), PLUS a
# word-bounded phase/wave-id shape (`P<digits>`, `H<digits>`, `phase
# <digits>`, `wave[- ]<alnum>`) — this repo's own epic-wave vocabulary
# (P13, H3, H5, "wave-7"). A reason legitimately citing a wave or phase id
# IN PASSING (rare for a structural reason, which cites a code or schema
# fact, not a roadmap position) would false-positive; the fix is to word
# the reason around the token, not to weaken this check.
is_deferral_reason() { # $1 = reason text
  printf '%s' "$1" | grep -qiE \
    '\btodo\b|\bfixme\b|not (yet )?documented|\blater\b|\bsoon\b|\bpending\b|\bbacklog\b|\bdeferred\b|\bplanned\b|\bcoming\b|\bwill be\b|\bphase [0-9]+\b|\bwave[ -][0-9a-zA-Z]+\b|\bP[0-9]+\b|\bH[0-9]+\b'
}

# anchor_exists reports (via exit status) whether md carries a markdown
# heading (any level, `#`-`######`) whose text — after the leading `#`s and
# ONE space are stripped — equals anchor byte-for-byte. Matched as a fixed
# string (grep -F), never a regex: this corpus's headings carry `§`,
# backticks and parens, exactly the characters a regex would misread.
anchor_exists() { # $1 = md file, $2 = anchor text
  [ -f "$1" ] || return 1
  grep -E '^#{1,6}[[:space:]]' "$1" | sed -E 's/^#{1,6}[[:space:]]+//' | grep -qxF "$2"
}

# schema_declared_fields prints, one per line sorted, the UNION of every
# top-level "properties" key across schemas/envelope/v*/*.schema.json —
# ASKED OF THE SCHEMAS, never a literal list, the same "ask the source of
# truth" discipline this whole gate applies to the binary.
#
# THE PRINCIPLE this exists to implement: a key an envelope schema DECLARES
# is documented by the GENERATED authoring page for its type
# (skill/a2ahub/reference/authoring/<type>.md, produced from that same
# schema, byte-gated by `skill-drift`) — requiring a hand-written prose
# anchor for `id` or `title` on top of that is how a gate becomes the one
# everybody switches off. A key NO schema declares is a DERIVED field —
# nothing generates documentation for it, so prose is the only place it can
# be taught, which is exactly the class this gate exists to catch. This is
# why `state` stays in the universe even though every surface carries it:
# no envelope field stores it (it is a fold, per loops.md's own §3.4
# condensation), so no schema's `properties` names it.
#
# Real JSON parsing (a "properties" object can itself nest a "properties"
# object one level down, inside an object-typed field like
# `expected_response` — a byte-level grep for `"key":` would wrongly
# collect that NESTED key as though it were a top-level envelope field) —
# grep/sed cannot do this safely, so this is a small embedded Go analyzer,
# the `scripts/check_work_checkpoint_schema.sh` precedent for "this needs a
# real JSON decode, not a text scan over nested braces". `go run` on a
# single file needs no module context (no `cd`/`GOWORK` juggling), matching
# that script's own invocation shape exactly.
schema_declared_fields() { # $1 = repo root
  local dir
  dir="$(mktemp -d)" || return 1
  cat >"$dir/main.go" <<'GO'
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: schema-fields-analyzer <repository-root>")
		os.Exit(2)
	}
	matches, err := filepath.Glob(filepath.Join(os.Args[1], "schemas", "envelope", "v*", "*.schema.json"))
	if err != nil || len(matches) == 0 {
		fmt.Fprintln(os.Stderr, "schema-fields-analyzer: no schemas/envelope/v*/*.schema.json matched")
		os.Exit(1)
	}
	keys := map[string]bool{}
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil {
			fmt.Fprintf(os.Stderr, "schema-fields-analyzer: read %s: %v\n", m, err)
			os.Exit(1)
		}
		// ONLY the top-level "properties" object's own keys — RawMessage
		// values are never decoded further, so a nested object-typed
		// field's OWN "properties" (e.g. expected_response.{shape,by})
		// never contributes a key here.
		var doc struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			fmt.Fprintf(os.Stderr, "schema-fields-analyzer: decode %s: %v\n", m, err)
			os.Exit(1)
		}
		for k := range doc.Properties {
			keys[k] = true
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	for _, k := range out {
		fmt.Println(k)
	}
}
GO
  go run "$dir/main.go" "$1"
  local status=$?
  rm -rf "$dir"
  return $status
}

# _SCHEMA_DECLARED_FIELDS_CACHE memoizes schema_declared_fields' output for
# the lifetime of one script process — real-run and --teeth both call the
# derived-universe builder more than once (--teeth re-derives it once per
# mutation's run_check), and the schema fact does not change between calls
# within one invocation.
_SCHEMA_DECLARED_FIELDS_CACHE=""
schema_declared_fields_cached() {
  if [ -z "$_SCHEMA_DECLARED_FIELDS_CACHE" ]; then
    _SCHEMA_DECLARED_FIELDS_CACHE="$(schema_declared_fields "$GATE_ROOT")" || return 1
  fi
  printf '%s\n' "$_SCHEMA_DECLARED_FIELDS_CACHE"
}

# build_universe prints every derived universe member, one per line:
# cmd:<name>, tool:<name>, field:<surface>:<key> — for every surface
# surface_names lists. $2/$3/$4 are the already-derived commands/tools/
# surface-names newline lists (computed once by the caller so a multi-call
# teeth run does not re-invoke the binary once per mutation).
build_universe() { # $1 = surfaces json, $2 = commands(nl), $3 = tools(nl), $4 = surface_names(nl)
  local json="$1" commands="$2" tools="$3" snames="$4"
  local c t s k
  while IFS= read -r c; do
    [ -z "$c" ] && continue
    printf 'cmd:%s\n' "$c"
  done <<<"$commands"
  while IFS= read -r t; do
    [ -z "$t" ] && continue
    printf 'tool:%s\n' "$t"
  done <<<"$tools"
  while IFS= read -r s; do
    [ -z "$s" ] && continue
    while IFS= read -r k; do
      [ -z "$k" ] && continue
      printf 'field:%s:%s\n' "$s" "$k"
    done < <(surface_keys "$json" "$s")
  done <<<"$snames"
}

# derive_universe wraps build_universe with the schema-declared-field
# exclusion (see schema_declared_fields' own doc comment for the
# principle): every field: member whose key IS a schema-declared envelope
# property is dropped before any caller — run_check or run_teeth's fixture
# builder — ever sees it, so both apply the identical, narrowed universe.
# cmd:/tool: members are never touched; the exclusion is field-key-only,
# and by KEY NAME across every surface, not per-surface (the same field
# name means the same envelope property regardless of which read surface
# carries it).
#
# Prints a one-line summary to STDERR of how many (surface, key) pairs were
# excluded and why — spec 13's own ask: "print the count you excluded and
# the reason, so the exclusion is visible rather than silent."
derive_universe() { # $1 = surfaces json, $2 = commands(nl), $3 = tools(nl), $4 = surface_names(nl)
  local raw excluded_keys
  raw="$(build_universe "$1" "$2" "$3" "$4")"
  if ! excluded_keys="$(schema_declared_fields_cached)"; then
    echo "prose-coverage: could not derive schema-declared fields from schemas/envelope/v*/*.schema.json — failing closed rather than policing nothing" >&2
    return 1
  fi
  local line key total_fields=0 excluded_count=0 kept=""
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    case "$line" in
      field:*)
        total_fields=$((total_fields + 1))
        key="${line##*:}"
        if printf '%s\n' "$excluded_keys" | grep -qxF "$key"; then
          excluded_count=$((excluded_count + 1))
          continue
        fi
        ;;
    esac
    kept="$kept$line"$'\n'
  done <<<"$raw"
  echo "prose-coverage: excluded $excluded_count of $total_fields field: members — key is a schemas/envelope/v*/*.schema.json-declared envelope property, documented by the generated (skill-drift-gated) authoring page for that type, not this ledger. Excluded keys: $(printf '%s' "$excluded_keys" | tr '\n' ' ')" >&2
  printf '%s' "$kept" | sed '/^$/d'
}

# check_declared_keys walks every key declared in EITHER ledger section and
# refuses one that is not a member of universe (the same "declared key
# outside the derived set reds" the loop-coverage.sh precedent applies to
# type/role/phase — here applied to the flat cmd:/tool:/field: namespace).
check_declared_keys() { # $1 = yaml, $2 = universe(nl, sorted)
  local yaml="$1" universe="$2" section key
  for section in "covered:" "declared:"; do
    while IFS= read -r key; do
      [ -z "$key" ] && continue
      if ! printf '%s\n' "$universe" | grep -qxF "$key"; then
        gate_fail "prose-coverage: declared key \"$key\" in $section is not part of the derived universe (a stale/renamed cmd:, tool: or field: entry)"
      fi
    done < <(ledger_keys "$yaml" "$section")
  done
}

# run_check walks the whole derived universe against
# schemas/prose-coverage.yaml and reports. $1 is the root to read the yaml
# and skill/a2ahub pages from (GATE_ROOT for a real run; a fixture directory
# for --teeth) — catalog_markdown/surfaces_json always ask the REAL binary
# regardless of $1, matching check-loop-coverage.sh's own vocabulary_json()
# precedent: the universe is real even while the ledger under test is not.
run_check() { # $1 = root
  local root="$1"
  local yaml="$root/schemas/prose-coverage.yaml"

  if [ ! -f "$yaml" ]; then
    gate_fail "prose-coverage: $yaml does not exist"
    gate_summary "prose-coverage"
    return $?
  fi
  if ! grep -qx 'schema: prose-coverage/v1' "$yaml"; then
    gate_fail "prose-coverage: $yaml is missing the \"schema: prose-coverage/v1\" line"
  fi

  local md json
  if ! md="$(catalog_markdown)"; then
    gate_fail "prose-coverage: could not read the catalog from the binary — failing closed rather than policing nothing"
    gate_summary "prose-coverage"
    return $?
  fi
  if ! json="$(surfaces_json)"; then
    gate_fail "prose-coverage: could not read --surfaces --json from the binary — failing closed rather than policing nothing"
    gate_summary "prose-coverage"
    return $?
  fi

  local commands tools snames
  commands="$(catalog_commands "$md")"
  tools="$(catalog_tools "$md")"
  snames="$(surface_names "$json")"
  if [ -z "$commands" ] || [ -z "$tools" ] || [ -z "$snames" ]; then
    gate_fail "prose-coverage: the binary returned an empty commands, tools, or surfaces list — failing closed rather than policing nothing"
    gate_summary "prose-coverage"
    return $?
  fi

  # The surfaces: cross-check — the other half of the residual hole (see
  # this script's own header): the ledger's declared surface list and the
  # binary's own surface-name set must agree in BOTH directions.
  local ledger_snames
  ledger_snames="$(ledger_surfaces "$yaml" | sort -u)"
  local sorted_snames
  sorted_snames="$(printf '%s\n' "$snames" | sort -u)"
  local s
  while IFS= read -r s; do
    [ -z "$s" ] && continue
    if ! printf '%s\n' "$ledger_snames" | grep -qxF "$s"; then
      gate_fail "prose-coverage: the binary's --surfaces --json names surface \"$s\", which $yaml's own surfaces: list does not declare"
    fi
  done <<<"$sorted_snames"
  while IFS= read -r s; do
    [ -z "$s" ] && continue
    if ! printf '%s\n' "$sorted_snames" | grep -qxF "$s"; then
      gate_fail "prose-coverage: $yaml's surfaces: list declares \"$s\", which the binary's --surfaces --json no longer emits"
    fi
  done <<<"$ledger_snames"

  local universe
  universe="$(derive_universe "$json" "$commands" "$tools" "$snames" | sort -u)"
  if [ -z "$universe" ]; then
    gate_fail "prose-coverage: the derived universe is empty — failing closed rather than policing nothing"
    gate_summary "prose-coverage"
    return $?
  fi

  # declared_classes: validation — each class's reason is checked ONCE
  # (not once per member), and every class member must itself be part of
  # the derived universe (a stale/renamed member in a class is exactly as
  # much a defect as one in covered:/declared:). class_table is a
  # member<TAB>class lookup, built here so the per-member loop below never
  # rescans the whole yaml.
  local classes class_table="" cls creason cm
  classes="$(class_names "$yaml")"
  while IFS= read -r cls; do
    [ -z "$cls" ] && continue
    creason="$(class_reason "$yaml" "$cls")"
    if [ -z "$(printf '%s' "$creason" | tr -d '[:space:]')" ]; then
      gate_fail "prose-coverage: declared class \"$cls\" has a blank reason — an exemption without a reason reads as a decision"
    elif is_deferral_reason "$creason"; then
      gate_fail "prose-coverage: declared class \"$cls\"'s reason reads as a DEFERRAL, not a structural reason: \"$creason\" — a capability with no prose is a gap, not something to schedule"
    fi
    while IFS= read -r cm; do
      [ -z "$cm" ] && continue
      if ! printf '%s\n' "$universe" | grep -qxF "$cm"; then
        gate_fail "prose-coverage: declared class \"$cls\" lists \"$cm\", which is not part of the derived universe (a stale/renamed cmd:, tool: or field: entry)"
        continue
      fi
      class_table="$class_table$cm"$'\t'"$cls"$'\n'
    done < <(class_members "$yaml" "$cls")
  done <<<"$classes"

  local skill_dir="$root/skill/a2ahub"
  local member has_cov has_dec value page anchor reason member_classes member_class_count claims
  while IFS= read -r member; do
    [ -z "$member" ] && continue
    if ledger_has_key "$yaml" "covered:" "$member"; then has_cov=1; else has_cov=0; fi
    if ledger_has_key "$yaml" "declared:" "$member"; then has_dec=1; else has_dec=0; fi
    member_classes="$(printf '%s' "$class_table" | awk -F'\t' -v m="$member" '$1==m{print $2}')"
    member_class_count=0
    if [ -n "$member_classes" ]; then
      member_class_count="$(printf '%s\n' "$member_classes" | grep -c .)"
    fi

    if [ "$member_class_count" -gt 1 ]; then
      gate_fail "prose-coverage: $member is listed in more than one declared class ($(printf '%s' "$member_classes" | tr '\n' ' ')) in $yaml — pick one"
      continue
    fi

    claims=$((has_cov + has_dec + (member_class_count > 0 ? 1 : 0)))
    if [ "$claims" -gt 1 ]; then
      gate_fail "prose-coverage: $member is declared in more than one place (covered:, declared:, or a declared class) in $yaml — pick one"
      continue
    fi
    if [ "$claims" -eq 0 ]; then
      gate_fail "prose-coverage: $member has no ledger entry in $yaml — neither covered, declared, nor in a declared class"
      continue
    fi

    if [ "$has_cov" -eq 1 ]; then
      value="$(ledger_value "$yaml" "covered:" "$member")"
      case "$value" in
        *'#'*)
          page="${value%%#*}"
          anchor="${value#*#}"
          ;;
        *)
          gate_fail "prose-coverage: $member declares covered: \"$value\", which is not shaped \"<page>#<anchor>\""
          continue
          ;;
      esac
      if [ -z "$page" ] || [ -z "$anchor" ]; then
        gate_fail "prose-coverage: $member declares covered: \"$value\", which is not shaped \"<page>#<anchor>\""
        continue
      fi
      if [ ! -f "$skill_dir/$page" ]; then
        gate_fail "prose-coverage: $member declares covered: \"$value\", but $skill_dir/$page does not exist"
        continue
      fi
      if ! anchor_exists "$skill_dir/$page" "$anchor"; then
        gate_fail "prose-coverage: $member declares covered: \"$value\", but \"$anchor\" is not a heading on $page"
      fi
      continue
    fi

    if [ "$has_dec" -eq 1 ]; then
      reason="$(ledger_value "$yaml" "declared:" "$member")"
      if [ -z "$(printf '%s' "$reason" | tr -d '[:space:]')" ]; then
        gate_fail "prose-coverage: $member is declared in $yaml with a blank reason — an exemption without a reason reads as a decision"
        continue
      fi
      if is_deferral_reason "$reason"; then
        gate_fail "prose-coverage: $member's declared: reason reads as a DEFERRAL, not a structural reason: \"$reason\" — a capability with no prose is a gap, not something to schedule"
      fi
      continue
    fi

    # member_class_count == 1 here: satisfied via its declared class — that
    # class's own reason was already validated once, above.
  done <<<"$universe"

  check_declared_keys "$yaml" "$universe"

  gate_summary "prose-coverage"
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || { echo "prose-coverage --teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN

  if ! ( cd "$GATE_ROOT" && GOWORK=off go build -o "$tmp/a2a" ./cmd/a2a ) >&2; then
    echo "prose-coverage --teeth: could not build ./cmd/a2a" >&2
    return 1
  fi
  export A2A_VERIFY_BINARY="$tmp/a2a"

  local md json
  if ! md="$(catalog_markdown)"; then
    echo "prose-coverage --teeth: could not read the catalog from the binary" >&2
    return 1
  fi
  if ! json="$(surfaces_json)"; then
    echo "prose-coverage --teeth: could not read --surfaces --json from the binary" >&2
    return 1
  fi

  local commands tools snames
  commands="$(catalog_commands "$md")"
  tools="$(catalog_tools "$md")"
  snames="$(surface_names "$json")"
  if [ -z "$commands" ] || [ -z "$tools" ] || [ -z "$snames" ]; then
    echo "prose-coverage --teeth: the binary returned an empty commands, tools, or surfaces list" >&2
    return 1
  fi

  local universe
  universe="$(derive_universe "$json" "$commands" "$tools" "$snames" | sort -u)"
  if [ -z "$universe" ]; then
    echo "prose-coverage --teeth: the derived universe is empty" >&2
    return 1
  fi

  local victim_cmd victim_field victim_field2 victim_tool
  victim_cmd="cmd:$(printf '%s\n' "$commands" | head -1)"
  victim_field="$(printf '%s\n' "$universe" | grep '^field:' | head -1)"
  victim_field2="$(printf '%s\n' "$universe" | grep '^field:' | sed -n '2p')"
  victim_tool="tool:$(printf '%s\n' "$tools" | head -1)"

  # write_good_fixture writes a small but FULLY declared, self-consistent
  # ledger against the REAL derived universe: victim_cmd is `covered:`
  # against a synthetic fixture.md heading; every OTHER member is
  # `declared:` with a genuine-sounding structural reason. Every mutation
  # below starts from this baseline and breaks exactly one thing.
  write_good_fixture() {
    mkdir -p "$tmp/schemas" "$tmp/skill/a2ahub"
    {
      echo "schema: prose-coverage/v1"
      echo "surfaces:"
      local s
      while IFS= read -r s; do
        [ -z "$s" ] && continue
        echo "  - $s"
      done <<<"$snames"
      echo "covered:"
      echo "  $victim_cmd: \"fixture.md#Fixture Heading\""
      echo "declared:"
      local m
      while IFS= read -r m; do
        [ -z "$m" ] && continue
        [ "$m" = "$victim_cmd" ] && continue
        echo "  $m: \"structural fixture reason — not a real classification\""
      done <<<"$universe"
    } >"$tmp/schemas/prose-coverage.yaml"
    printf '# fixture\n\n## Fixture Heading\n' >"$tmp/skill/a2ahub/fixture.md"
  }

  write_good_fixture
  if ! (run_check "$tmp") >/dev/null 2>&1; then
    echo "prose-coverage --teeth: FAILED — a fully declared, self-consistent fixture ledger stayed red" >&2
    return 1
  fi

  # (a) a verb in the universe with no ledger row must red naming it.
  write_good_fixture
  grep -v "^  $victim_cmd: " "$tmp/schemas/prose-coverage.yaml" >"$tmp/schemas/prose-coverage.yaml.new"
  mv "$tmp/schemas/prose-coverage.yaml.new" "$tmp/schemas/prose-coverage.yaml"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_cmd has no ledger entry"; then
    echo "prose-coverage --teeth: FAILED (a) — a verb with no ledger row did not red naming it" >&2
    return 1
  fi

  # (b) a surface field with no row must red naming it.
  write_good_fixture
  grep -v "^  $victim_field: " "$tmp/schemas/prose-coverage.yaml" >"$tmp/schemas/prose-coverage.yaml.new"
  mv "$tmp/schemas/prose-coverage.yaml.new" "$tmp/schemas/prose-coverage.yaml"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_field has no ledger entry"; then
    echo "prose-coverage --teeth: FAILED (b) — a field with no ledger row did not red naming it" >&2
    return 1
  fi

  # (c) a blank declared: reason must red naming the member, "blank reason".
  write_good_fixture
  sed -i.bak "s#^  $victim_tool: \".*\"#  $victim_tool: \"\"#" "$tmp/schemas/prose-coverage.yaml"
  rm -f "$tmp/schemas/prose-coverage.yaml.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_tool is declared in" || ! printf '%s\n' "$out" | grep -q "blank reason"; then
    echo "prose-coverage --teeth: FAILED (c) — a blank declared: reason did not red naming the member and 'blank reason'" >&2
    return 1
  fi

  # (d) a deferral-shaped declared: reason must red naming the member and
  # the deferral finding.
  write_good_fixture
  sed -i.bak "s#^  $victim_field2: \".*\"#  $victim_field2: \"not documented yet\"#" "$tmp/schemas/prose-coverage.yaml"
  rm -f "$tmp/schemas/prose-coverage.yaml.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_field2" || ! printf '%s\n' "$out" | grep -q "reads as a DEFERRAL"; then
    echo "prose-coverage --teeth: FAILED (d) — a deferral-shaped declared: reason did not red naming the member and 'reads as a DEFERRAL'" >&2
    return 1
  fi

  # (e) covered: pointing at an anchor that does not exist must red naming
  # the missing anchor.
  write_good_fixture
  sed -i.bak "s|^  $victim_cmd: \"fixture.md#Fixture Heading\"|  $victim_cmd: \"fixture.md#Nonexistent Heading\"|" "$tmp/schemas/prose-coverage.yaml"
  rm -f "$tmp/schemas/prose-coverage.yaml.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF '"Nonexistent Heading" is not a heading on fixture.md'; then
    echo "prose-coverage --teeth: FAILED (e) — a covered: anchor absent from the page did not red naming it" >&2
    return 1
  fi

  # (f) a ledger key outside the derived universe must red naming it.
  write_good_fixture
  printf '  cmd:not-a-real-command-xyz: "bogus fixture row"\n' >>"$tmp/schemas/prose-coverage.yaml"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF 'declared key "cmd:not-a-real-command-xyz" in declared: is not part of the derived universe'; then
    echo "prose-coverage --teeth: FAILED (f) — a ledger key outside the derived universe did not red naming it" >&2
    return 1
  fi

  # Bonus, beyond the six named cases: a member declared BOTH covered and
  # declared must red (the loop-coverage.sh precedent for the same shape).
  write_good_fixture
  printf '  %s: "duplicated on purpose"\n' "$victim_cmd" >>"$tmp/schemas/prose-coverage.yaml"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_cmd is declared in more than one place"; then
    echo "prose-coverage --teeth: FAILED (bonus) — a member declared both covered and declared did not red naming it" >&2
    return 1
  fi

  # Bonus: the surfaces: cross-check — a ledger surfaces: list missing a
  # real surface must red naming it.
  write_good_fixture
  local first_surface
  first_surface="$(printf '%s\n' "$snames" | head -1)"
  grep -v "^  - $first_surface\$" "$tmp/schemas/prose-coverage.yaml" >"$tmp/schemas/prose-coverage.yaml.new"
  mv "$tmp/schemas/prose-coverage.yaml.new" "$tmp/schemas/prose-coverage.yaml"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "names surface \"$first_surface\", which"; then
    echo "prose-coverage --teeth: FAILED (bonus) — a surfaces: list missing a real surface did not red naming it" >&2
    return 1
  fi

  # write_class_fixture is write_good_fixture's shape, except victim_field
  # is satisfied via a `declared_classes:` class ("fixture-class") instead
  # of its own bare `declared:` row — proves a class actually SATISFIES
  # membership (the baseline check below), then every mutation after it
  # breaks exactly one thing about the class mechanism.
  write_class_fixture() {
    mkdir -p "$tmp/schemas" "$tmp/skill/a2ahub"
    {
      echo "schema: prose-coverage/v1"
      echo "surfaces:"
      local s
      while IFS= read -r s; do
        [ -z "$s" ] && continue
        echo "  - $s"
      done <<<"$snames"
      echo "covered:"
      echo "  $victim_cmd: \"fixture.md#Fixture Heading\""
      echo "declared:"
      local m
      while IFS= read -r m; do
        [ -z "$m" ] && continue
        [ "$m" = "$victim_cmd" ] && continue
        [ "$m" = "$victim_field" ] && continue
        echo "  $m: \"structural fixture reason — not a real classification\""
      done <<<"$universe"
      echo "declared_classes:"
      echo "  fixture-class:"
      echo "    reason: \"structural fixture-class reason — not a real classification\""
      echo "    members:"
      echo "      - $victim_field"
    } >"$tmp/schemas/prose-coverage.yaml"
    printf '# fixture\n\n## Fixture Heading\n' >"$tmp/skill/a2ahub/fixture.md"
  }

  write_class_fixture
  if ! (run_check "$tmp") >/dev/null 2>&1; then
    echo "prose-coverage --teeth: FAILED — a member satisfied via a declared class stayed red (a class does not satisfy membership)" >&2
    return 1
  fi

  # (g) a class with a blank reason must red naming the class.
  write_class_fixture
  sed -i.bak 's#^    reason: ".*"$#    reason: ""#' "$tmp/schemas/prose-coverage.yaml"
  rm -f "$tmp/schemas/prose-coverage.yaml.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF 'declared class "fixture-class" has a blank reason'; then
    echo "prose-coverage --teeth: FAILED (g) — a declared class with a blank reason did not red naming the class" >&2
    return 1
  fi

  # (h) a deferral-shaped class reason must red naming the class and the
  # deferral finding.
  write_class_fixture
  sed -i.bak 's#^    reason: ".*"$#    reason: "not documented yet"#' "$tmp/schemas/prose-coverage.yaml"
  rm -f "$tmp/schemas/prose-coverage.yaml.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF 'declared class "fixture-class"' || ! printf '%s\n' "$out" | grep -q "reads as a DEFERRAL"; then
    echo "prose-coverage --teeth: FAILED (h) — a deferral-shaped class reason did not red naming the class and 'reads as a DEFERRAL'" >&2
    return 1
  fi

  # (i) a class member outside the derived universe must red naming it.
  write_class_fixture
  printf '      - field:not-a-real-surface:not-a-real-key\n' >>"$tmp/schemas/prose-coverage.yaml"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF 'declared class "fixture-class" lists "field:not-a-real-surface:not-a-real-key", which is not part of the derived universe'; then
    echo "prose-coverage --teeth: FAILED (i) — a class member outside the derived universe did not red naming it" >&2
    return 1
  fi

  # (j) a member in a declared CLASS that also carries its own bare
  # declared: row must red — "more than one place" applies to a class
  # exactly like it applies to covered:/declared:. Inserted right after the
  # "declared:" heading (not appended at EOF, which would land inside the
  # LATER declared_classes: section, outside the declared: section this
  # parser's state machine tracks — see ledger_keys' own doc comment for
  # the section-boundary rule this mirrors).
  write_class_fixture
  sed -i.bak "/^declared:\$/a\\
  $victim_field: \"also given a bare declared row on purpose\"
" "$tmp/schemas/prose-coverage.yaml"
  rm -f "$tmp/schemas/prose-coverage.yaml.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_field is declared in more than one place"; then
    echo "prose-coverage --teeth: FAILED (j) — a member in both a declared class and a bare declared: row did not red naming it" >&2
    return 1
  fi

  # (k) a member listed in TWO declared classes must red naming both
  # classes.
  write_class_fixture
  sed -i.bak "s#^declared_classes:#declared_classes:\n  fixture-class-2:\n    reason: \"a second structural fixture-class reason\"\n    members:\n      - $victim_field#" "$tmp/schemas/prose-coverage.yaml"
  rm -f "$tmp/schemas/prose-coverage.yaml.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_field is listed in more than one declared class"; then
    echo "prose-coverage --teeth: FAILED (k) — a member listed in two declared classes did not red naming it" >&2
    return 1
  fi

  echo "prose-coverage --teeth: ok"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

run_check "$GATE_ROOT"
exit $?
