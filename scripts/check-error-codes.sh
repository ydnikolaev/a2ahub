#!/usr/bin/env bash
# check-error-codes.sh — defects-fix-2026-08 P1 (spec 01-a-code-has-a-gate.md
# T5): a registered validation error code cannot ship undocumented, firing on
# no production path, or unscoped against ADR-011 (docs/decisions.md).
#
# check-operational-confidence.sh's check_history already reds the ceiling on
# a code EMITTED by internal/validate/internal/cli but ABSENT from the
# registry (unregistered). This gate is its mirror, same corpus, opposite
# direction: a code REGISTERED but ungated. It reuses check_history's own
# corpus walk (internal/validate + internal/cli, non-test) rather than
# forking a second copy of it.
#
# THE DECLARATION HOME: schemas/errors/v1/registry.yaml, not a second file
# and not internal/validate. That file's own header already states the
# principle this gate depends on: "schemas/errors/v1/registry.yaml is the
# authored SSOT... the Go mapper... loads/mirrors it, never leads it." A
# severity or mode-scope declaration that could disagree between the
# registry and a Go literal is exactly the defect this gate exists to catch,
# so it needs exactly one owner — the file that already claims the role for
# {code, class, title, applies_to}. Three fields were added there for this
# phase (severity, mode_scope, mode_scope_reason); see that file's own
# header comment for their grammar. gopkg.in/yaml.v3's Unmarshal (used by
# internal/schema.LoadRegistry) is non-strict, so the addition is backward
# compatible — confirmed by reading internal/schema/registry.go before
# adding a single field.
#
# THREE OBLIGATIONS, one row per registered code (spec 01 T5):
#   1. prose      — a `severity: reject` code's own string appears in
#                    skill/** (ADR-011 D4: "a reject-severity code ships
#                    with prose or it does not ship"). `severity: warning`
#                    codes are exempt BY CLASS, not by a per-row exemption:
#                    D4's own sentence is about reject codes only, and nothing
#                    in T5 or ADR-011 extends it to warnings.
#   2. reachability — the code's own string appears in a test file OUTSIDE
#                    internal/validate — internal/cli/**_test.go,
#                    internal/mcp/**_test.go or internal/e2e/**_test.go —
#                    proving it fires through a production entry point
#                    (`a2a validate`/`submit`/`validate --ci`/an MCP tool),
#                    not only through a direct call to its own check
#                    function from inside internal/validate's own unit
#                    tests. This is finding 04's exact shape: REF-019
#                    shipped "reachable from its own tests and firing on NO
#                    production path" (internal/validate/verdicts.go:50-51's
#                    own comment, quoting T5) until a later wave wired
#                    `cmd_validate_ci.go` to call it and added
#                    TestValidateCI_REF019FiresOnAnOutOfRangeVerdictIndex —
#                    a CLI-tier test, not a validate-package one. `class:
#                    schema` codes are exempt BY CLASS: schema_class.go's
#                    mapSchemaViolations is the ONE JSON-Schema-keyword ->
#                    SCH-code mapping, called only from the corpus's shared
#                    ValidateDraft/ValidateForSubmit entry point every
#                    production caller already goes through — there is no
#                    separate per-code check function a test could bypass
#                    the way checkVerdictIndexRange was bypassed, so the
#                    REF-019 failure shape cannot recur for this class.
#   3. mode scope  — the code declares mode_scope (v3-pr | v3-full-repo |
#                    both); a `severity: reject` code scoped `both` MUST
#                    carry a `mode_scope_reason` (ADR-011 D3's own
#                    principle: "a rule that can only be satisfied by
#                    editing an immutable artifact must not judge immutable
#                    history" — a reject code running in the post-merge
#                    v3-full-repo audit needs a stated reason it does not
#                    retroactively judge content that was valid when
#                    written).
#
# Any obligation may be waived per code with a stated reason — this file's
# own `prose_exempt`/`reachability_exempt` fields, the same shape
# scripts/lib/lane-ungated.txt already uses for a path no gate claims. An
# unexplained gap FAILS, naming the code — never a silent pass.
#
# ALSO, separately (obligation 0, load-bearing for obligation 3): the
# registry's declared `severity` must AGREE with the `Severity: Severity*`
# literal nearest that code's `Code: "..."` in internal/validate/*.go (then
# internal/cli/*.go, non-test, in that scan order — the same two directories
# check_history already reads, in the same order its own rg call lists
# them). internal/cli/cmd_show.go reuses REF-004/REF-008 at a DIFFERENT,
# presentational severity for its own "V5 warnings" (a mirror-staleness
# advisory, not the validation verdict) — reading internal/validate first is
# what keeps that reuse from being mistaken for a registry/Go disagreement,
# and is why the scan order is part of this gate's contract, not an
# implementation accident.
#
# Usage: bash scripts/check-error-codes.sh
#        bash scripts/check-error-codes.sh --teeth

# lane-inputs:
#   schemas/errors/v1/registry.yaml
#   internal/validate/**/*.go
#   internal/cli/**/*.go
#   internal/mcp/**/*.go
#   internal/e2e/**/*.go
#   skill/**
# lane-reads-opaque: every real read is behind a function-parameter variable
#   ($1/$2, built at call time from $GATE_ROOT — no literal path in the
#   function bodies), the same shape check-operational-confidence.sh's own
#   lane-reads-opaque note documents for check_history/check_hashes. Also:
#   `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"` self-locates
#   via command substitution, the same construct check-notify-secrets.sh
#   declares opaque at its own line 67.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# registry_all_codes prints one code per line, in file order.
registry_all_codes() { sed -n 's/^  - code: //p' "$1"; }

# registry_field prints $3's single-line value for $2's entry in $1 (empty
# if absent). Block-scalar fields (mode_scope_reason's `>-`) print only the
# marker line — callers that need PRESENCE, not content, is exactly what
# every caller here needs.
registry_field() { # $1 = registry file, $2 = code, $3 = field name
  awk -v code="$2" -v field="$3:" '
    $0 ~ "^  - code: " code "$" { active=1; next }
    active && /^  - code: / { active=0 }
    active && index($0, "    " field) == 1 {
      sub("^    " field "[[:space:]]*", "")
      print
      exit
    }
  ' "$1"
}

# severity_label turns a captured "SeverityReject"/"SeverityWarning" Go
# literal into the registry's own lowercase vocabulary.
severity_label() { # $1 = SeverityXxx
  local s="${1#Severity}"
  printf '%s' "$s" | tr '[:upper:]' '[:lower:]'
}

# go_severity_for_code returns the FIRST "SeverityReject"/"SeverityWarning"
# literal found within 8 lines after a `Code: "<code>"` literal, scanning
# internal/validate/*.go then internal/cli/*.go (non-test, sorted, in that
# order) — the same two directories and order check_history's own rg call
# uses. Empty output (rc 1) means no Go emission was found at all.
go_severity_for_code() { # $1 = code, $2 = source root
  local code="$1" root="$2" f sev
  while IFS= read -r f; do
    [ -f "$f" ] || continue
    # internal/validate's own files write the bare `SeverityX` literal;
    # internal/cli imports the package and writes `validate.SeverityX` —
    # both are the same value under a different qualifier, so the
    # `(validate\.)?` prefix is optional, and only the unqualified suffix
    # is kept for comparison (cmd_validate_ci.go:1251's REF-011 emission is
    # `Severity: validate.SeverityReject,` — this was watched failing
    # without the prefix, reporting REF-011 as having no Go emission at
    # all, before this fix).
    sev="$(grep -A8 "Code:[[:space:]]*\"${code}\"" "$f" 2>/dev/null \
      | grep -m1 -o 'Severity:[[:space:]]*\(validate\.\)\?Severity[A-Za-z]*' \
      | awk '{print $NF}' \
      | sed 's/^validate\.//')"
    if [ -n "$sev" ]; then
      printf '%s\n' "$sev"
      return 0
    fi
  done < <(
    find "$root/internal/validate" -maxdepth 1 -name '*.go' ! -name '*_test.go' 2>/dev/null | sort
    find "$root/internal/cli" -maxdepth 1 -name '*.go' ! -name '*_test.go' 2>/dev/null | sort
  )
  return 1
}

# reachability_ok: class schema is exempt by construction (see header). For
# every other class, the code's own string must appear in a test file
# outside internal/validate.
#
# Deliberately a find+loop, never a piped multi-glob search. Beneath this
# script's own `set -o pipefail`, a glob that matches NOTHING in one of the
# three directories is passed along as a literal, unopenable filename, the
# search exits two, and pipefail reports the PIPELINE's exit as that even
# when a LATER glob's own search found the code and exited zero — a real
# false "unreachable" this shape produced during --teeth's own control case,
#
# The prose above deliberately avoids naming the search tool inline: the
# lane extractor scans this file for read constructs and cannot tell a
# COMMENT from a command, so an inline `<tool> ... <word>` in prose minted
# two phantom paths ("under", "2") and reddened lane-declarations. Filed as
# its own row in docs/validator-backlog.md — the extractor's weakness, not
# this gate's.
# watched failing before this fix.
reachability_ok() { # $1 = code, $2 = root, $3 = class
  local code="$1" root="$2" class="$3" f
  [ "$class" = "schema" ] && return 0
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    grep -q -- "$code" "$f" 2>/dev/null && return 0
  done < <(find "$root/internal/cli" "$root/internal/mcp" "$root/internal/e2e" -maxdepth 1 -name '*_test.go' 2>/dev/null)
  return 1
}

# prose_ok: the code's own string appears anywhere under skill/.
prose_ok() { # $1 = code, $2 = root
  [ -d "$2/skill" ] || return 1
  grep -rq -- "$1" "$2/skill" 2>/dev/null
}

# check_obligations walks every registered code in $1 (a registry.yaml) and
# applies obligation 0 (severity correlation) and the three T5 obligations,
# reading Go/skill/test evidence rooted at $2.
check_obligations() { # $1 = registry file, $2 = source root
  local registry="$1" root="$2"
  local code class severity mode_scope reason prose_exempt reach_exempt
  local sev_actual want

  [ -f "$registry" ] || { gate_fail "check-error-codes: registry not found: $registry"; return; }

  while IFS= read -r code; do
    [ -z "$code" ] && continue
    class="$(registry_field "$registry" "$code" "class")"
    severity="$(registry_field "$registry" "$code" "severity")"
    mode_scope="$(registry_field "$registry" "$code" "mode_scope")"
    reason="$(registry_field "$registry" "$code" "mode_scope_reason")"
    prose_exempt="$(registry_field "$registry" "$code" "prose_exempt")"
    reach_exempt="$(registry_field "$registry" "$code" "reachability_exempt")"

    if [ -z "$severity" ]; then
      gate_fail "check-error-codes: $code declares no severity — obligation 3 (mode scope) cannot be judged"
      continue
    fi
    case "$severity" in
      reject | warning) ;;
      *) gate_fail "check-error-codes: $code declares an unrecognized severity '$severity' — want reject|warning" ;;
    esac
    case "$mode_scope" in
      v3-pr | v3-full-repo | both) ;;
      *) gate_fail "check-error-codes: $code declares an unrecognized mode_scope '$mode_scope' — want v3-pr|v3-full-repo|both" ;;
    esac

    # Obligation 0: severity correlation (SSOT is the registry; Go must agree).
    if [ "$class" = "schema" ]; then
      : # schema_class.go hardcodes SeverityReject uniformly for the whole class.
    elif sev_actual="$(go_severity_for_code "$code" "$root")"; then
      want="$(severity_label "$sev_actual")"
      if [ "$want" != "$severity" ]; then
        gate_fail "check-error-codes: $code declares severity:$severity but the nearest Go literal is $sev_actual (severity:$want) — registry and Go disagree"
      fi
    else
      gate_fail "check-error-codes: $code has no Severity: literal within 8 lines of its Code: literal in internal/validate/*.go or internal/cli/*.go (non-test) — cannot correlate the registry's declared severity against Go"
    fi

    # Obligation 3: mode scope. A reject code scoped `both` needs a reason.
    if [ "$severity" = "reject" ] && [ "$mode_scope" = "both" ] && [ -z "$reason" ]; then
      gate_fail "check-error-codes: $code is severity:reject scoped mode_scope:both with no mode_scope_reason — ADR-011 D3 requires a stated reason a post-merge (v3-full-repo) reject does not judge immutable history"
    fi

    # Obligation 1: prose. Reject codes only (ADR-011 D4's own wording).
    if [ "$severity" = "reject" ] && [ -z "$prose_exempt" ]; then
      if ! prose_ok "$code" "$root"; then
        gate_fail "check-error-codes: $code (severity:reject) appears nowhere under skill/ — ADR-011 D4 (\"a reject-severity code ships with prose or it does not ship\")"
      fi
    fi

    # Obligation 2: reachability.
    if [ -z "$reach_exempt" ]; then
      if ! reachability_ok "$code" "$root" "$class"; then
        gate_fail "check-error-codes: $code names no test in internal/cli, internal/mcp or internal/e2e — reachable only from its own check function inside internal/validate, the exact \"true and inert\" shape finding 04 found for REF-019 before it was wired to a production entry point"
      fi
    fi
  done < <(registry_all_codes "$registry")
}

run_check() { # $1 = registry file, $2 = source root
  check_obligations "$1" "$2"
}

# ── teeth ────────────────────────────────────────────────────────────────

teeth_expect() { # $1 = label, $2 = red|green, $3 = needle, $4 = registry, $5 = root
  local label="$1" verdict="$2" needle="$3" registry="$4" root="$5" out rc
  set +e
  out="$(
    (
      _GATE_ERRORS=0
      run_check "$registry" "$root"
      gate_summary "check-error-codes-teeth"
    ) 2>&1
  )"
  rc=$?
  set -e
  if [ "$verdict" = "red" ]; then
    if [ "$rc" -eq 0 ] || ! printf '%s\n' "$out" | grep -Fq "$needle"; then
      echo "check-error-codes --teeth: FALSE GREEN — $label did not red with '$needle':" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-error-codes --teeth: $label reds"
  elif [ "$rc" -ne 0 ]; then
    echo "check-error-codes --teeth: FALSE RED — $label should green:" >&2
    echo "$out" >&2
    return 1
  else
    echo "check-error-codes --teeth: $label greens"
  fi
}

# write_fixture_tree seeds a minimal, isolated source root: one Go file per
# scanned directory, a skill/ dir, and whatever the caller adds on top.
write_fixture_tree() { # $1 = root
  local root="$1"
  mkdir -p "$root/internal/validate" "$root/internal/cli" "$root/internal/mcp" "$root/internal/e2e" "$root/skill"
  : > "$root/skill/.gitkeep"
}

run_teeth() {
  local work
  work="$(mktemp -d)" || return 1
  trap 'rm -rf -- "${work:-}"' EXIT

  # AC1: a code with no production-path test reds, naming the code.
  local d="$work/ac1" reg root
  write_fixture_tree "$d/root"
  root="$d/root"
  reg="$d/registry.yaml"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-901
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
EOF
  cat > "$root/internal/validate/fixture.go" <<'EOF'
package validate

func check() []Violation {
	return []Violation{{
		Code:     "REF-901",
		Severity: SeverityReject,
	}}
}
EOF
  printf 'REF-901 documented here.\n' > "$root/skill/loop.md"
  # No REF-901 mention anywhere under internal/cli, internal/mcp or
  # internal/e2e test files: reachability obligation must red.
  teeth_expect "AC1: no production-path test" red \
    "REF-901 names no test in internal/cli, internal/mcp or internal/e2e" "$reg" "$root" || return 1

  # AC2: a reject code absent from skill/** reds, naming the code.
  d="$work/ac2"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-902
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
EOF
  cat > "$root/internal/validate/fixture.go" <<'EOF'
package validate

func check() []Violation {
	return []Violation{{
		Code:     "REF-902",
		Severity: SeverityReject,
	}}
}
EOF
  cat > "$root/internal/cli/fixture_test.go" <<'EOF'
package cli

func TestREF902FiresThroughRealBinary(t *testing.T) {
	// REF-902
}
EOF
  # skill/ carries no mention of REF-902: prose obligation must red.
  teeth_expect "AC2: reject code absent from skill/**" red \
    "REF-902 (severity:reject) appears nowhere under skill/" "$reg" "$root" || return 1

  # AC3: a reject code scoped `both` with no mode_scope_reason reds, naming
  # the code.
  d="$work/ac3"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-903
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: both
EOF
  cat > "$root/internal/validate/fixture.go" <<'EOF'
package validate

func check() []Violation {
	return []Violation{{
		Code:     "REF-903",
		Severity: SeverityReject,
	}}
}
EOF
  cat > "$root/internal/cli/fixture_test.go" <<'EOF'
package cli

func TestREF903FiresThroughRealBinary(t *testing.T) {
	// REF-903
}
EOF
  printf 'REF-903 documented here.\n' > "$root/skill/loop.md"
  teeth_expect "AC3: reject code scoped both with no reason" red \
    "REF-903 is severity:reject scoped mode_scope:both with no mode_scope_reason" "$reg" "$root" || return 1

  # AC4: a legitimately exempt code, reason stated, passes green — same
  # fixture as AC1 (no production-path test) but with reachability_exempt
  # carrying a reason.
  d="$work/ac4"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-904
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    reachability_exempt: only reachable from a network-gated path this offline corpus cannot drive
EOF
  cat > "$root/internal/validate/fixture.go" <<'EOF'
package validate

func check() []Violation {
	return []Violation{{
		Code:     "REF-904",
		Severity: SeverityReject,
	}}
}
EOF
  printf 'REF-904 documented here.\n' > "$root/skill/loop.md"
  teeth_expect "AC4: legitimately exempt code with a reason passes" green \
    "" "$reg" "$root" || return 1

  # Control: a fully clean row (prose, reachable test, correct severity,
  # v3-pr scope needing no reason) greens outright — proves the obligations
  # are not vacuously red-only.
  d="$work/control"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-905
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
EOF
  cat > "$root/internal/validate/fixture.go" <<'EOF'
package validate

func check() []Violation {
	return []Violation{{
		Code:     "REF-905",
		Severity: SeverityReject,
	}}
}
EOF
  cat > "$root/internal/cli/fixture_test.go" <<'EOF'
package cli

func TestREF905FiresThroughRealBinary(t *testing.T) {
	// REF-905
}
EOF
  printf 'REF-905 documented here.\n' > "$root/skill/loop.md"
  teeth_expect "control: fully-closed row greens" green "" "$reg" "$root" || return 1

  # Severity correlation: registry says warning, Go says reject — reds,
  # naming the disagreement, distinct from the three T5 obligations above.
  d="$work/sevmismatch"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-906
    class: referential
    title: fixture
    applies_to: fixture
    severity: warning
    mode_scope: v3-pr
EOF
  cat > "$root/internal/validate/fixture.go" <<'EOF'
package validate

func check() []Violation {
	return []Violation{{
		Code:     "REF-906",
		Severity: SeverityReject,
	}}
}
EOF
  cat > "$root/internal/cli/fixture_test.go" <<'EOF'
package cli

func TestREF906FiresThroughRealBinary(t *testing.T) {
	// REF-906
}
EOF
  teeth_expect "severity correlation: registry disagrees with Go" red \
    "REF-906 declares severity:warning but the nearest Go literal is SeverityReject" "$reg" "$root" || return 1

  echo "check-error-codes --teeth: PASS — AC1 (no production-path test), AC2 (reject code absent from skill/**) and AC3 (reject+both with no reason) all red naming the code; AC4's stated-reason exemption and a fully-closed row both green; a registry/Go severity disagreement reds distinctly from the three T5 obligations"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
else
  run_check "$GATE_ROOT/schemas/errors/v1/registry.yaml" "$GATE_ROOT"
  gate_summary "check-error-codes"
fi
