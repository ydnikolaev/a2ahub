#!/usr/bin/env bash
# check-verdict-exit-mapping.sh — computed-not-listed-2026-08 P4 (spec
# 04-the-exit-code-declares.md §7/§8 rows 6, 7, 12, 13, 14): verifies
# schemas/verdict-exit-codes.yaml against the SAME computed verb universe
# the registry itself is seeded from — never a second, hand-typed roster
# inside this script (spec §7's own mandate).
#
# TWO REFUSALS, kept structurally separate because they answer different
# questions and a conflated message would hide which one fired:
#
#   REFUSAL A (verb side) — every dispatch verb the computed universe names
#   must have a registry row. The universe is computed in two steps, per
#   spec §7:
#     1. VERB NAMES from cmd/a2a/wire.go's buildCommands() — its three map
#        literals (the direct `m["x"] = ` assignments, readVerbs()'s
#        returned map, lifecycleVerbs()'s returned map), the SAME dispatch
#        registry `a2a __catalog` walks (cmd/a2a/catalog.go:82) and
#        TestEveryCatalogNameIsDispatchable already guards. `contract` is
#        replaced by its twelve cli.ContractSubcommands() rows
#        (internal/cli/cmd_contract.go), named "contract <sub>" — the same
#        two-level shape cmd/a2a/catalog.go's own catalogCommandRows()
#        already uses for the identical family.
#     2. This script deliberately does NOT filter the universe down to
#        "only the JSON-carrying verbs" before requiring a row: every
#        dispatch verb needs a declaration, including a verb this repo's
#        static idiom scan (below) cannot attribute to any one file by
#        naming convention — internal/cli/cmd_validate_ci.go carries the
#        idiom yet defines no `<Verb>Command` receiver at all (it is a
#        companion file to cmd_submit.go's ValidateCommand), and
#        ValidateCommand itself lives in cmd_submit.go, not cmd_validate.go
#        — filename-to-verb attribution is NOT reliably derivable (verified
#        empirically while building this gate; see the registry's own
#        header for the full account). Requiring a row for every verb
#        sidesteps that unreliable attribution problem entirely: a verb
#        with no distinguishable JSON-carrying behaviour still gets the
#        TRIVIAL {clean: 0} row spec §7's own text describes for `version`/
#        `html --json` — never silently exempted (§8 row 6).
#
#   REFUSAL B (file side) — every source file this script's own idiom scan
#   finds (`json.NewEncoder(stdio.Stdout)` over internal/cli/**/*.go,
#   non-test — spec §7 step 2: a flag-presence check alone would silently
#   miss cmd_validate_ci.go, which registers no --json flag at all yet
#   emits JSON unconditionally) must be named by at least one registry
#   row's `site:` list. An unclaimed file reds BY NAME — this is what
#   actually catches a new JSON-emitting file landing with no owning
#   declaration, independent of whether its owning verb happens to already
#   have a (possibly trivial) row for unrelated reasons.
#
# ROW 7 (a declaration disagreeing with the verb's behaviour): for every
# registry row with a non-empty `site:`, its mapping's non-zero values
# must each appear as a literal `return N` somewhere in its own site
# file(s) — a best-effort LEXICAL check (a code returned through a named
# variable is invisible to it; declared in the registry's own header).
# `clean: 0` is exempt from this proof — 0 is the universal Unix success
# baseline, not a fact worth demanding a literal for (statusline's own row
# returns `result.Exit`, a computed value, and needs this exemption to be
# a true statement rather than a workaround).
#
# Usage: bash scripts/check-verdict-exit-mapping.sh            # verify
#        bash scripts/check-verdict-exit-mapping.sh --teeth    # self-test
#
# lane-inputs:
#   schemas/verdict-exit-codes.yaml
#   internal/cli/**/*.go
#   cmd/a2a/wire.go
#
# lane-reads-opaque: every read in this gate is parameterised — the registry
# path, the wire.go path and the cli directory arrive as function arguments
# ("$1", "$f", "$cli_dir", "$work/registry.yaml"), never as literals. That is
# not incidental: §8 row 12 requires the computed universe to GROW from a
# synthetic fixture tree with the gate script itself untouched, which is only
# possible if the same code can be pointed at a fixture. A teeth-capable gate
# and a literal-path gate are mutually exclusive here, and the teeth are the
# thing that proves the derivation works.

# WHY cmd/a2a/catalog.go IS *NOT* DECLARED, though spec §7's mandated
# lane-inputs line named it. This script never opens that file. It cites
# catalog.go as PRECEDENT for the two-level contract expansion it performs
# — catalog.go walks the same buildCommands()/ContractSubcommands() sources
# at runtime, and this gate re-derives them STATICALLY so a synthetic
# `--teeth` fixture can grow the universe with no compiled binary (§8 row
# 12 requires exactly that; a compiled catalog walk cannot be pointed at a
# fixture tree).
#
# The declaration must name what the gate READS, not what it was inspired
# by. `.claude/rules/check-convention.md` puts it as "as narrow as the gate
# really reads, no narrower": over-declaring is cheap in seconds but it is
# still a false claim, and it would make an edit to catalog.go select a
# gate that cannot judge it. A `# lane-reads-opaque:` directive was the
# wrong instrument for this and briefly stood here — that directive means
# "this gate reads a path the extractor cannot resolve", never "I declared
# an input I do not read".

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"
set -uo pipefail

DEFAULT_REGISTRY="$GATE_ROOT/schemas/verdict-exit-codes.yaml"
DEFAULT_WIRE="$GATE_ROOT/cmd/a2a/wire.go"
DEFAULT_CLI_DIR="$GATE_ROOT/internal/cli"
DEFAULT_ROOT="$GATE_ROOT"

# ── derivation: the computed verb universe ──────────────────────────────

# extract_func_body <file> <start-regex>: prints from the first line
# matching <start-regex> through the first subsequent top-level `}` line
# (column 0) — gofmt's own convention makes that boundary reliable without
# a real Go parser.
extract_func_body() {
  local file="$1" pat="$2"
  [ -r "$file" ] || return 1
  awk -v pat="$pat" '
    $0 ~ pat { active=1 }
    active { print }
    active && /^}/ { exit }
  ' "$file"
}

# direct_dispatch_verbs <wire-path>: buildCommands()'s own direct
# `m["x"] = ` assignments (never the verbs added via the two `for name,
# construct := range ...()` loops — those are read separately below, since
# they use a variable key, not a literal, and would not match this pattern
# — which is exactly why they cannot double-count here).
direct_dispatch_verbs() {
  extract_func_body "$1" '^func buildCommands' \
    | grep -oE 'm\["[A-Za-z0-9_-]+"\][[:space:]]*=' \
    | sed -E 's/^m\["//; s/"\][[:space:]]*=$//'
}

# map_literal_verbs <wire-path> <func-start-regex>: the string keys of a
# top-level `func <name>() map[string]...{ return map[string]...{ "k": ...
# } }`-shaped function — used for both readVerbs() and lifecycleVerbs(),
# whose dispatch keys buildCommands() installs via a `for name, construct
# := range` loop rather than a literal `m["x"] =`.
map_literal_verbs() {
  extract_func_body "$1" "$2" \
    | grep -oE '^[[:space:]]*"[A-Za-z0-9_-]+":' \
    | sed -E 's/^[[:space:]]*"//; s/":$//'
}

# exit_codes_returned <go-file>: every integer this file can `return`, written
# EITHER as a literal or as a named integer constant declared in the same file.
#
# THE NAMED FORM IS NOT A NICETY, and leaving it out was found by this epic's
# own drift probe on 2026-08-30. `internal/cli/cmd_work.go` returns
# `workStatusExitUnmeasured`, a const = 3 — the third verdict state this whole
# phase exists to make declarable. A literal-only scan reports that file as
# never returning 3, so declaring the truth in the registry would RED this very
# check, and the registry's own header went on to assert that no verb in the
# binary implements a third code at all. A gate that forces a true declaration
# to be omitted is worse than no gate.
#
# It resolves only same-file `<ident> = <int>` constants. A code returned
# through a helper in another file, or computed at runtime
# (`cmd_statusline.go`'s `return result.Exit`), stays invisible — the same
# documented limit `check-error-codes.sh`'s registry_field obligation carries,
# and the reason `clean: 0` is exempt from needing proof at all.
exit_codes_returned() {
  local f="$1" consts name val
  grep -oE 'return[[:space:]]+-?[0-9]+' "$f" | awk '{print $2}'
  consts="$(grep -oE '^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*[[:space:]]*=[[:space:]]*-?[0-9]+[[:space:]]*$' "$f" \
    | sed -E 's/^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*(-?[0-9]+)[[:space:]]*$/\1 \2/')"
  [ -n "$consts" ] || return 0
  while read -r name val; do
    [ -n "$name" ] || continue
    if grep -qE "return[[:space:]]+${name}([^A-Za-z0-9_]|\$)" "$f"; then
      printf '%s\n' "$val"
    fi
  done <<<"$consts"
}


# contract_subcommands <cli-dir>: the string Name values of
# cli.ContractSubcommands() (internal/cli/cmd_contract.go) — the SSOT
# cmd/a2a/catalog.go's own two-level expansion already reads for the
# identical family (spec §7).
contract_subcommands() {
  local f="$1/cmd_contract.go"
  [ -r "$f" ] || return 1
  extract_func_body "$f" '^func ContractSubcommands' \
    | grep -oE '\{Name: "[A-Za-z0-9_-]+"' \
    | sed -E 's/^\{Name: "//; s/"$//'
}

# verb_universe <wire-path> <cli-dir>: one verb identity per line, sorted,
# de-duplicated, `contract` replaced by its "contract <sub>" expansion.
verb_universe() {
  local wire="$1" cli_dir="$2" direct read_v lifecycle_v all subs
  direct="$(direct_dispatch_verbs "$wire")" || return 1
  read_v="$(map_literal_verbs "$wire" '^func readVerbs')" || return 1
  lifecycle_v="$(map_literal_verbs "$wire" '^func lifecycleVerbs')" || return 1
  all="$(printf '%s\n%s\n%s\n' "$direct" "$read_v" "$lifecycle_v" | sed '/^$/d' | sort -u)"
  if printf '%s\n' "$all" | grep -qx "contract"; then
    all="$(printf '%s\n' "$all" | grep -vx "contract")"
    subs="$(contract_subcommands "$cli_dir")" || return 1
    while IFS= read -r s; do
      [ -n "$s" ] || continue
      all="$(printf '%s\ncontract %s\n' "$all" "$s")"
    done <<<"$subs"
  fi
  printf '%s\n' "$all" | sed '/^$/d' | sort -u
}

# json_carrying_files <root> <cli-dir>: repo(root)-relative paths of every
# non-test internal/cli/*.go file carrying the json.NewEncoder(stdio.Stdout)
# idiom (spec §7 step 2) — the shared call every JSON-emitting verb uses,
# scanned instead of a --json flag so a verb like `validate --ci` (no such
# flag, unconditional JSON) cannot silently fall out of the universe.
json_carrying_files() {
  local root="$1" cli_dir="$2"
  [ -d "$cli_dir" ] || return 1
  find "$cli_dir" -maxdepth 1 -name '*.go' ! -name '*_test.go' -print0 2>/dev/null \
    | xargs -0 grep -l 'json\.NewEncoder(stdio\.Stdout)' 2>/dev/null \
    | sed "s#^${root%/}/##" \
    | sort
}

# ── the registry's own reader ────────────────────────────────────────────

registry_all_verbs() { sed -n 's/^  - verb: //p' "$1"; }

# registry_unsited_rows <registry>: how many rows declare an EMPTY site.
#
# WHY THIS IS COUNTED AT ALL, and it is this epic's own thesis pointed at this
# epic's own artifact. verify_all's backing check begins `[ -n "$sites" ] ||
# continue` — a row with no site is SKIPPED, so its declared mapping is never
# compared against anything. That is a real exemption, and it was invisible:
# `contract materialize` declares `{clean: 0}` and its handler
# (internal/cli/cmd_contract_p6.go, runMaterialize) plainly `return 1`s on a
# MaterializeContract failure, and nothing reddened.
#
# The exemption cannot simply be abolished. Verb-to-file attribution is not
# reliably derivable — `cmd_validate_ci.go` defines no `<Verb>Command` receiver
# and `ValidateCommand` lives in `cmd_submit.go` — which is exactly why the
# universe is every dispatch verb rather than a file-scan subset. So the
# exemption survives in the only shape this epic permits a survivor: a seeded
# ceiling that reds on growth, read from `unsited_rows_ceiling:` in the registry
# itself rather than from a second file nobody would remember to ship.
#
# WHAT THIS DOES NOT DO, said plainly so a green run is not over-read: it does
# not check behaviour AGAINST declaration for a sited row either. The gate
# verifies every DECLARED code has backing; it does not verify every OBSERVED
# code is declared, because a site file holds many verbs' handlers and
# per-handler attribution is a capability this gate does not have. Filed in
# docs/validator-backlog.md with the measurement.
registry_unsited_rows() {
  awk '/^  - verb: /{v=1} /^    site: \[\]/{if (v) {n++; v=0}} END{print n+0}' "$1"
}

# registry_unsited_ceiling <registry>: the seeded budget, or EMPTY when the key
# is absent. Empty is UNMEASURED, never 0 — "the file stores nothing" and "the
# file stores zero" are different facts and only one is a measurement, the same
# split ceiling_stored/ceiling_count draws in check-cross-layer-test-import-
# ceiling.sh.
registry_unsited_ceiling() {
  sed -n 's/^unsited_rows_ceiling:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$1" | head -1
}


# registry_all_sites <registry>: every declared `site:` path across every
# row, flattened — Refusal B only needs SOME row to claim a file, never a
# specific one, so a flat union is the correct shape to check membership
# against (not a per-verb lookup).
registry_all_sites() {
  awk '/^      - / { sub(/^      - /, ""); print }' "$1"
}

# registry_site_files <registry> <verb>: the exact `site:` list for one
# verb (empty for a verb declared `site: []`).
registry_site_files() {
  awk -v verb="$2" '
    $0 == "  - verb: " verb { active=1; next }
    active && /^  - verb: / { active=0 }
    active && /^    site:$/ { in_list=1; next }
    active && in_list && /^      - / { sub(/^      - /, ""); print; next }
    active && in_list && /^    [a-z]/ { in_list=0 }
  ' "$1"
}

# registry_mapping_entries <registry> <verb>: "<class>: <value>" lines for
# one verb's declared mapping.
registry_mapping_entries() {
  awk -v verb="$2" '
    $0 == "  - verb: " verb { active=1; next }
    active && /^  - verb: / { active=0; in_map=0 }
    active && /^    mapping:$/ { in_map=1; next }
    active && in_map && /^    [a-z]/ { in_map=0 }
    active && in_map && /^      [A-Za-z_]+:/ { line=$0; sub(/^      /, "", line); print line }
  ' "$1"
}

# ── verification ─────────────────────────────────────────────────────────

# verify_all <registry> <wire-path> <cli-dir> <root>
#
# THREE UNMEASURED ARMS — the inputs this script actually opens (never a
# fourth for cmd/a2a/catalog.go, which it does not read; see the header's
# own `lane-reads-opaque` note). Each fails in a way that would otherwise
# either falsely GREEN (an absent wire.go/cli-dir yields an EMPTY computed
# universe, satisfied trivially by any registry, however wrong) or falsely
# RED (an absent registry has no rows at all, so a real universe reds
# every verb as "missing" when the true state is "could not be checked").
# Neither is a verdict this gate is entitled to state.
verify_all() {
  local registry="$1" wire="$2" cli_dir="$3" root="$4"
  local universe files verb sites site_files observed rel abs kv class value

  if [ ! -r "$registry" ]; then
    gate_unmeasured "verdict-exit-mapping: cannot read $registry — the declaration registry IS what this gate verifies, so with no file every row looks undeclared and every check would either vacuously pass or wrongly fail"
    return
  fi
  if [ ! -r "$wire" ]; then
    gate_unmeasured "verdict-exit-mapping: cannot read $wire — the verb universe is derived from buildCommands() there; with it absent this run cannot compute what verbs exist at all"
    return
  fi
  if [ ! -d "$cli_dir" ]; then
    gate_unmeasured "verdict-exit-mapping: $cli_dir is not a directory — there is no internal/cli source to scan for the json.NewEncoder(stdio.Stdout) idiom, which measures zero JSON-carrying files rather than proving none exist"
    return
  fi

  universe="$(verb_universe "$wire" "$cli_dir")" || {
    gate_unmeasured "verdict-exit-mapping: could not derive the verb universe from $wire (buildCommands()/readVerbs()/lifecycleVerbs() not found in the expected shape, or contract's own ContractSubcommands() could not be read from $cli_dir/cmd_contract.go)"
    return
  }
  local universe_count
  universe_count="$(printf '%s\n' "$universe" | sed '/^$/d' | wc -l | tr -d ' ')"

  # ── Refusal A: every computed verb has a row ───────────────────────────
  while IFS= read -r verb; do
    [ -n "$verb" ] || continue
    if ! registry_all_verbs "$registry" | grep -qFx "$verb"; then
      gate_fail "verdict-exit-mapping: no declaration for verb '$verb' — the computed universe (cmd/a2a/wire.go's buildCommands(), expanded for contract via cli.ContractSubcommands()) names it, but $registry has no matching row; add one, even a trivial {clean: 0} mapping if it has no distinguishable objections verdict"
    fi
  done <<<"$universe"

  # ── Refusal B: every JSON-carrying file is claimed by some row ─────────
  files="$(json_carrying_files "$root" "$cli_dir")"
  local sites_flat
  sites_flat="$(registry_all_sites "$registry")"
  while IFS= read -r rel; do
    [ -n "$rel" ] || continue
    if ! printf '%s\n' "$sites_flat" | grep -qFx "$rel"; then
      gate_fail "verdict-exit-mapping: $rel carries json.NewEncoder(stdio.Stdout) but no row in $registry names it in a site: list — a JSON-emitting file must be claimed by at least one verb's declaration"
    fi
  done <<<"$files"

  # ── Row 7: a declaration must not disagree with observable behaviour ───
  while IFS= read -r verb; do
    [ -n "$verb" ] || continue
    sites="$(registry_site_files "$registry" "$verb")"
    [ -n "$sites" ] || continue
    observed=""
    local unreadable=0
    while IFS= read -r site_files; do
      [ -n "$site_files" ] || continue
      abs="$root/$site_files"
      if [ ! -r "$abs" ]; then
        gate_fail "verdict-exit-mapping: verb '$verb' declares site $site_files, which does not exist under $root — a stale or invented site, naming both the registry's own claim and the file that no longer backs it"
        unreadable=1
        continue
      fi
        observed="$(printf '%s\n%s\n' "$observed" "$(exit_codes_returned "$abs")")"
    done <<<"$sites"
    [ "$unreadable" -eq 1 ] && continue
    observed="$(printf '%s\n' "$observed" | sed '/^$/d' | sort -u)"
    while IFS= read -r kv; do
      [ -n "$kv" ] || continue
      class="${kv%%:*}"
      value="${kv#*:}"
      value="${value# }"
      [ "$value" = "0" ] && continue # the universal success baseline needs no literal proof
      if ! printf '%s\n' "$observed" | grep -qFx "$value"; then
        gate_fail "verdict-exit-mapping: verb '$verb' declares $class: $value, but no 'return $value' (literal, or a same-file named constant) appears in its own site file(s) ($sites) — the declaration disagrees with the verb's behaviour"
      fi
    done < <(registry_mapping_entries "$registry" "$verb")
  done <<<"$universe"

  local file_count
  file_count="$(printf '%s\n' "$files" | sed '/^$/d' | wc -l | tr -d ' ')"
  local unsited unsited_ceiling
  unsited="$(registry_unsited_rows "$registry")"
  unsited_ceiling="$(registry_unsited_ceiling "$registry")"
  if [ -z "$unsited_ceiling" ]; then
    gate_unmeasured "verdict-exit-mapping: the registry declares no \`unsited_rows_ceiling:\`, so the number of rows exempt from the backing check could not be judged — an absent ceiling reads as zero and would green over any growth"
  elif [ "$unsited" -gt "$unsited_ceiling" ]; then
    gate_fail "verdict-exit-mapping: $unsited row(s) declare an empty site:, ceiling is $unsited_ceiling — a row with no site is SKIPPED by the backing check, so its mapping is never compared against the verb. Give the new row its real site: (and the mapping that site actually supports), or re-seed unsited_rows_ceiling: in the registry and say in its header why the exemption grew"
  fi
  gate_ok "verdict-exit-mapping: $universe_count verb(s) in the computed universe, $file_count json-carrying file(s) measured, $unsited row(s) exempt from the backing check (ceiling $unsited_ceiling)"
}

# ── --teeth ───────────────────────────────────────────────────────────────

teeth_run() { # $1 registry $2 wire $3 cli-dir $4 root
  (
    _GATE_ERRORS=0
    _GATE_WARNINGS=0
    _GATE_UNMEASURED=0
    verify_all "$1" "$2" "$3" "$4"
    gate_summary "verdict-exit-mapping-teeth"
  ) 2>&1
}

teeth_expect() { # $1 label $2 red|green $3 registry $4 wire $5 cli-dir $6 root
  local label="$1" verdict="$2" out rc
  set +e
  out="$(teeth_run "$3" "$4" "$5" "$6")"
  rc=$?
  set -e
  if [ "$verdict" = "red" ]; then
    if [ "$rc" -eq 0 ]; then
      echo "check-verdict-exit-mapping --teeth: FALSE GREEN — $label did not red:" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-verdict-exit-mapping --teeth: $label reds"
  else
    if [ "$rc" -ne 0 ]; then
      echo "check-verdict-exit-mapping --teeth: FALSE RED — $label should green:" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-verdict-exit-mapping --teeth: $label greens"
  fi
  printf '%s' "$out"
}

# teeth_expect_unmeasured <label> <registry> <wire> <cli-dir> <root>: BOTH
# halves — exit 3 AND the text — because `make` collapses 3 into its own 2
# before a caller upstream of this script could otherwise tell "could not
# measure" apart from "measured, and wrong" (gate-lib.sh's own
# gate_unmeasured doc comment).
teeth_expect_unmeasured() {
  local label="$1" out rc
  set +e
  out="$(teeth_run "$2" "$3" "$4" "$5")"
  rc=$?
  set -e
  if [ "$rc" -ne 3 ]; then
    echo "check-verdict-exit-mapping --teeth: WRONG VERDICT — $label should be UNMEASURED (exit 3), got exit $rc:" >&2
    echo "$out" >&2
    return 1
  fi
  if ! printf '%s' "$out" | grep -q "unmeasured\|UNMEASURED"; then
    echo "check-verdict-exit-mapping --teeth: SILENT UNMEASURED — $label exited 3 but said nothing a reader can see:" >&2
    echo "$out" >&2
    return 1
  fi
  echo "check-verdict-exit-mapping --teeth: $label is unmeasured, and says so"
}

run_teeth() {
  local work
  work="$(mktemp -d)" || return 1
  trap 'rm -rf -- "$work"' RETURN

  mkdir -p "$work/cmd/a2a" "$work/internal/cli"

  # A minimal buildCommands()-shaped wire.go: one direct verb (version,
  # JSON-carrying via its own fixture file), one alias-style direct verb
  # (submit, non-JSON), a readVerbs()-sourced verb (inbox), a
  # lifecycleVerbs()-sourced verb (ack), and contract (expanded via
  # ContractSubcommands() below).
  cat >"$work/cmd/a2a/wire.go" <<'EOF'
package main

func buildCommands() map[string]command {
	m := map[string]command{}
	m["version"] = func(args []string, stdout, stderr io.Writer) int {
		return 0
	}
	m["submit"] = runSubmit
	for name, construct := range readVerbs() {
		m[name] = func(args []string, stdout, stderr io.Writer) int {
			return 0
		}
	}
	for name, construct := range lifecycleVerbs() {
		m[name] = func(args []string, stdout, stderr io.Writer) int {
			return 0
		}
	}
	m["contract"] = runContract
	return m
}

func readVerbs() map[string]int {
	return map[string]int{
		"inbox":  1,
		"outbox": 2,
	}
}

func lifecycleVerbs() map[string]int {
	return map[string]int{
		"ack": 1,
	}
}
EOF

  cat >"$work/internal/cli/cmd_contract.go" <<'EOF'
package cli

func ContractSubcommands() []ContractSubcommand {
	return []ContractSubcommand{
		{Name: "preflight", Synopsis: "x"},
		{Name: "publish", Synopsis: "x"},
	}
}
EOF

  cat >"$work/internal/cli/cmd_version.go" <<'EOF'
package cli

func (c *VersionCommand) Run() int {
	if err := json.NewEncoder(stdio.Stdout).Encode(nil); err != nil {
		return 1
	}
	return 0
}
EOF

  write_baseline_registry() {
    cat >"$work/registry.yaml" <<'EOF'
unsited_rows_ceiling: 6
entries:
  - verb: version
    site:
      - internal/cli/cmd_version.go
    mapping:
      clean: 0
      objections: 1
  - verb: submit
    site: []
    mapping:
      clean: 0
  - verb: inbox
    site: []
    mapping:
      clean: 0
  - verb: outbox
    site: []
    mapping:
      clean: 0
  - verb: ack
    site: []
    mapping:
      clean: 0
  - verb: contract preflight
    site: []
    mapping:
      clean: 0
  - verb: contract publish
    site: []
    mapping:
      clean: 0
EOF
  }
  write_baseline_registry

  # ── baseline: computed universe == registry exactly → green ────────────
  teeth_expect "the baseline fixture (universe matches the registry exactly)" green \
    "$work/registry.yaml" "$work/cmd/a2a/wire.go" "$work/internal/cli" "$work" >/dev/null || exit 1

  # ── §8 row 12: universe growth from a FIXTURE, no gate edit ─────────────
  # Add a new JSON-emitting verb to the fixture buildCommands()-shaped
  # input. The computed universe must include it and red for the missing
  # declaration — this file (the gate itself) is never touched to make
  # that happen; only the fixture grows.
  cp "$work/cmd/a2a/wire.go" "$work/cmd/a2a/wire.go.bak"
  sed -i.bak 's/m\["submit"\] = runSubmit/m["submit"] = runSubmit\n\tm["newverb"] = func(args []string, stdout, stderr io.Writer) int { return 0 }/' "$work/cmd/a2a/wire.go"
  cat >"$work/internal/cli/cmd_newverb.go" <<'EOF'
package cli

func (c *NewverbCommand) Run() int {
	if err := json.NewEncoder(stdio.Stdout).Encode(nil); err != nil {
		return 1
	}
	return 0
}
EOF
  out="$(teeth_run "$work/registry.yaml" "$work/cmd/a2a/wire.go" "$work/internal/cli" "$work" || true)"
  if ! printf '%s' "$out" | grep -q "no declaration for verb 'newverb'"; then
    echo "check-verdict-exit-mapping --teeth: FALSE — row 12 (universe growth) did not name the new verb:" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "check-verdict-exit-mapping --teeth: row 12 — a verb added only to the fixture buildCommands() input is picked up by the computed universe and reds for its missing declaration, with the gate script itself untouched"

  # ── §8 row 13: a JSON-carrying FILE with no claiming row, distinct from
  # row 7's disagreement message ───────────────────────────────────────────
  # newverb's own row is still absent (Refusal A already covers it above);
  # this asserts the DISTINCT Refusal B message names the FILE, not just
  # the verb, and uses different wording than row 7's "disagrees with the
  # verb's behaviour".
  if ! printf '%s' "$out" | grep -q "cmd_newverb.go carries json.NewEncoder(stdio.Stdout) but no row"; then
    echo "check-verdict-exit-mapping --teeth: FALSE — row 13 (unclaimed json-carrying file) did not name the file distinctly from row 12's missing-verb message:" >&2
    echo "$out" >&2
    exit 1
  fi
  if printf '%s' "$out" | grep -q "disagrees with the verb's behaviour"; then
    echo "check-verdict-exit-mapping --teeth: FALSE — row 13's fixture incorrectly also tripped row 7's disagreement message; the two refusals are not staying distinct" >&2
    exit 1
  fi
  echo "check-verdict-exit-mapping --teeth: row 13 — an unclaimed JSON-carrying file reds naming the file, in a message textually distinct from row 7's declaration-disagreement message"

  # Restore, and add the row 12/13 verb's declaration cleanly for later
  # cases so the remaining probes start from a clean, green baseline.
  mv "$work/cmd/a2a/wire.go.bak" "$work/cmd/a2a/wire.go"
  rm -f "$work/internal/cli/cmd_newverb.go"

  # ── §8 row 8's shape: a verb whose file only ever returns 0, declared
  # with the trivial {clean: 0} mapping alone, passes BECAUSE it declared
  # (never because nobody looked) ─────────────────────────────────────────
  cat >"$work/internal/cli/cmd_trivialjson.go" <<'EOF'
package cli

func (c *TrivialJSONCommand) Run() int {
	_ = json.NewEncoder(stdio.Stdout).Encode(nil)
	return 0
}
EOF
  # trivialjson has no owning verb in the universe at all in this fixture,
  # so this probe only exercises Refusal B in isolation: claim the file
  # under an EXISTING trivial row (submit) to prove a {clean: 0}-only
  # declaration is sufficient — no objections/unmeasured key required —
  # as long as it is present.
  cat >"$work/registry_trivial.yaml" <<'EOF'
unsited_rows_ceiling: 6
entries:
  - verb: version
    site:
      - internal/cli/cmd_version.go
    mapping:
      clean: 0
      objections: 1
  - verb: submit
    site:
      - internal/cli/cmd_trivialjson.go
    mapping:
      clean: 0
  - verb: inbox
    site: []
    mapping:
      clean: 0
  - verb: outbox
    site: []
    mapping:
      clean: 0
  - verb: ack
    site: []
    mapping:
      clean: 0
  - verb: contract preflight
    site: []
    mapping:
      clean: 0
  - verb: contract publish
    site: []
    mapping:
      clean: 0
EOF
  teeth_expect "a {clean: 0}-only declaration over a file that only ever returns 0 passes because it declared" green \
    "$work/registry_trivial.yaml" "$work/cmd/a2a/wire.go" "$work/internal/cli" "$work" >/dev/null || exit 1
  rm -f "$work/internal/cli/cmd_trivialjson.go"

  # ── §8 row 7: a declaration disagreeing with observable behaviour reds,
  # naming the verb ────────────────────────────────────────────────────────
  write_baseline_registry
  sed -i.bak 's/objections: 1/objections: 9/' "$work/registry.yaml"
  out="$(teeth_run "$work/registry.yaml" "$work/cmd/a2a/wire.go" "$work/internal/cli" "$work" || true)"
  if ! printf '%s' "$out" | grep -q "verb 'version' declares objections: 9.*disagrees with the verb's behaviour"; then
    echo "check-verdict-exit-mapping --teeth: FALSE — row 7 (disagreeing declaration) did not name the verb and the disagreement:" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "check-verdict-exit-mapping --teeth: row 7 — a declared exit code with no literal 'return' backing it reds, naming the verb"

  # ── §8 row 7, the NAMED-CONSTANT half. Added after this epic's own wave-5
  # drift probe found the literal-only scan hiding the single real third
  # exit code in the binary: `a2a work status` returns 3 as
  # `return workStatusExitUnmeasured`. A gate that cannot see a named
  # constant forces a TRUE declaration to be omitted, which is worse than
  # no gate — so both directions are pinned here. ───────────────────────────
  write_baseline_registry
  cat >"$work/internal/cli/cmd_version.go" <<'EOF'
package cli

const (
	versionExitObjections = 1
	versionExitUnmeasured = 3
	versionExitNeverUsed  = 9
)

func (c *VersionCommand) Run() int {
	if err := json.NewEncoder(stdio.Stdout).Encode(nil); err != nil {
		return versionExitObjections
	}
	if unreadable {
		return versionExitUnmeasured
	}
	return 0
}
EOF
  sed -i.bak 's/      objections: 1/      objections: 1\n      unmeasured: 3/' "$work/registry.yaml"
  teeth_expect "a code returned through a same-file named constant BACKS its declaration — no literal 'return 3' exists anywhere in the fixture" green \
    "$work/registry.yaml" "$work/cmd/a2a/wire.go" "$work/internal/cli" "$work" >/dev/null || exit 1

  # The other direction: a constant that is DECLARED but never returned must
  # not back anything. Without this, `exit_codes_returned` collecting every
  # integer const in the file would green any declaration that happened to
  # match an unused value — a false green built out of the fix for a false red.
  sed -i.bak 's/      unmeasured: 3/      unmeasured: 9/' "$work/registry.yaml"
  out="$(teeth_run "$work/registry.yaml" "$work/cmd/a2a/wire.go" "$work/internal/cli" "$work" || true)"
  if ! printf '%s' "$out" | grep -q "verb 'version' declares unmeasured: 9"; then
    echo "check-verdict-exit-mapping --teeth: FALSE — a declared-but-never-returned constant must NOT back a declaration:" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "check-verdict-exit-mapping --teeth: row 7 — a same-file named constant backs its declaration, and a declared-but-never-returned constant does not"

  # ── The exemption must have a SIZE. A row with an empty site: is skipped by
  # the backing check, so growth in that set is coverage quietly leaving. Both
  # directions are pinned: the ceiling holding greens, growth past it reds, and
  # an ABSENT ceiling is unmeasured rather than read as zero (which would green
  # over any growth at all — the false green this epic keeps finding). ────────
  write_baseline_registry
  printf '  - verb: newlyexempt\n    site: []\n    mapping:\n      clean: 0\n' >>"$work/registry.yaml"
  sed -i.bak 's/^      m\["submit"\].*/&\n\tm["newlyexempt"] = \&NewlyexemptCommand{}/' "$work/cmd/a2a/wire.go" 2>/dev/null || true
  out="$(teeth_run "$work/registry.yaml" "$work/cmd/a2a/wire.go" "$work/internal/cli" "$work" || true)"
  if ! printf '%s' "$out" | grep -q "7 row(s) declare an empty site:, ceiling is 6"; then
    echo "check-verdict-exit-mapping --teeth: FALSE — a new empty-site row must red against the seeded ceiling and name both numbers:" >&2
    echo "$out" >&2
    exit 1
  fi

  write_baseline_registry
  sed -i.bak '/^unsited_rows_ceiling:/d' "$work/registry.yaml"
  teeth_expect_unmeasured "a registry with no unsited_rows_ceiling: cannot judge its own exemption, and says so instead of reading the absence as zero" \
    "declares no" "$work/registry.yaml" "$work/cmd/a2a/wire.go" "$work/internal/cli" "$work" || exit 1
  echo "check-verdict-exit-mapping --teeth: the empty-site exemption has a size — growth past the seeded ceiling reds naming both numbers, and an absent ceiling is unmeasured"
  write_baseline_registry
  write_baseline_registry
  write_baseline_registry

  # ── §8 row 5 shape: an UNMEASURED case, exit 3 AND the refusal text ─────
  teeth_expect_unmeasured "an unreadable registry file" \
    "$work/does-not-exist.yaml" "$work/cmd/a2a/wire.go" "$work/internal/cli" "$work" || exit 1
  teeth_expect_unmeasured "an unreadable wire.go" \
    "$work/registry.yaml" "$work/cmd/a2a/does-not-exist.go" "$work/internal/cli" "$work" || exit 1
  teeth_expect_unmeasured "a cli dir that is not a directory" \
    "$work/registry.yaml" "$work/cmd/a2a/wire.go" "$work/no-such-dir" "$work" || exit 1

  echo "check-verdict-exit-mapping --teeth: PASS — the baseline fixture greens; a verb added only to a fixture buildCommands() input grows the computed universe and reds for its missing declaration (row 12) without any edit to this gate; an unclaimed JSON-carrying file reds naming the file, distinctly from a disagreeing declaration (row 13); a {clean: 0}-only declaration over an always-zero file passes because it declared (row 8's shape); a declared exit code with no backing reds naming the verb, where backing means a literal OR a same-file named constant actually returned (row 7); the empty-site exemption has a size, so a new row cannot join it silently and an absent ceiling is unmeasured rather than zero; and all three unmeasured arms (registry, wire.go, cli dir) say so instead of guessing (row 5's shape)"
}

# ── entry point ──────────────────────────────────────────────────────────

case "${1:-}" in
--teeth)
  run_teeth
  exit $?
  ;;
"")
  verify_all "$DEFAULT_REGISTRY" "$DEFAULT_WIRE" "$DEFAULT_CLI_DIR" "$DEFAULT_ROOT"
  gate_summary "verdict-exit-mapping"
  exit $?
  ;;
*)
  echo "usage: $0 [--teeth]" >&2
  exit 2
  ;;
esac
