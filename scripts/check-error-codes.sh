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
# FOUR OBLIGATIONS, one row per registered code (1-3: spec 01 T5;
# 4: judge-the-thing-2026-08 spec 11 T5.2):
#   1. prose      — a `severity: reject` code's own string appears in
#                    skill/** (ADR-011 D4: "a reject-severity code ships
#                    with prose or it does not ship"). `severity: warning`
#                    codes are exempt BY CLASS, not by a per-row exemption:
#                    D4's own sentence is about reject codes only, and nothing
#                    in T5 or ADR-011 extends it to warnings. `severity:
#                    unmeasured` codes (D9, no-silent-yes-2026-08/P3, minted
#                    2026-08-27) are exempt BY CLASS for the identical
#                    reason: D9 forecloses UNMEASURED ever blocking a write
#                    ("UNMEASURED can never itself block a write"), so it is
#                    not a refusal either, and D4's sentence still names
#                    reject only. Decided deliberately and written down at
#                    registry.yaml's own `severity:` field comment — this is
#                    the gate's mirror of that decision, not a second one.
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
#   4. driven      — a `severity: reject` code is named by a
#                    `Refusal{Code: "..."}` literal inside a conformance
#                    path the catalogue ACTUALLY DRIVES (internal/livee2e,
#                    non-test, whole tree). `severity: warning` codes are
#                    out of scope BY CLASS, the same class-level reasoning
#                    obligation 1 uses for ADR-011 D4: a warning is not a
#                    refusal. `severity: unmeasured` codes (D9,
#                    no-silent-yes-2026-08/P3) are out of scope BY CLASS for
#                    the same reason: D9's own foreclosure ("UNMEASURED can
#                    never itself block a write") means it is not a refusal
#                    either, so a code raised at that severity never carries
#                    a `path_exempt` and is never counted toward — or
#                    against — the burn-down this obligation prints.
#
#                    This is NOT obligation 2 again, and the difference is
#                    the whole point. Obligation 2 is satisfied by the
#                    code's own string appearing in any test file outside
#                    internal/validate — a real obligation, and one a
#                    COMMENT satisfies. Obligation 4 is satisfied only by a
#                    declared narrative that attempts an act the product
#                    must refuse, and only if the driver executes that
#                    narrative. Measured when it landed: 50 reject codes
#                    fail this one, 16 of which satisfy obligation 2 with a
#                    waiver and 34 with no waiver at all.
#
#                    What it answers: internal/livee2e/operationpull.go:52
#                    read `nil, // refs: no declared path drives 'respond
#                    --ref' yet`. The refusal that comment stranded was
#                    written to close a real reported defect, unit-proven,
#                    and executed by no declared path. A rule nothing
#                    drives is indistinguishable from a rule that is wrong,
#                    and the only way that stops recurring is a gate.
#
#                    Its own waiver is `path_exempt` (spec 11 §7), read
#                    exactly like the two below it. It is a written waiver,
#                    never a claim about state: the DRIVEN set is derived
#                    from the catalogue's own literals, so nothing here
#                    asks to be trusted.
#
# Any obligation may be waived per code with a stated reason — this file's
# own `prose_exempt`/`reachability_exempt`/`path_exempt` fields, the same shape
# scripts/lib/lane-ungated.txt already uses for a path no gate claims. An
# unexplained gap FAILS, naming the code — never a silent pass.
#
# ALSO, separately (obligation 0, load-bearing for obligation 3): FOR A CODE
# RAISED FROM internal/validate — which is every code with no `emitted_by`
# field, and the default this gate resolves — the
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
#   internal/livee2e/**/*.go
#   !internal/livee2e/**/*_test.go
#   skill/**
# lane-reads-opaque: every real read is behind a function-parameter variable
#   ($1/$2/$3, built at call time from $GATE_ROOT — no literal path in the
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

# ── obligation 4: is a refusal DRIVEN? ────────────────────────────────────
#
# The three readers below are text over Go source, not a compiler — spec 11
# §9 names that fragility and states its mitigation, which is the rule this
# parser is built on: it REFUSES what it cannot read rather than skipping
# it. A skipped declaration is a silent under-count, and an under-count in
# this obligation is a green gate over the exact state it exists to forbid.
#
# LC_ALL=C on every pass: the walk is byte-oriented substring arithmetic
# over source that carries UTF-8 prose, and a multibyte-aware awk aborts on
# it (watched failing on internal/livee2e/client_live.go before this was
# set).

# catalogue_go_files lists the catalogue tree's non-test Go files,
# RECURSIVELY and never by name (spec 11 §T5.3). The day a family is split
# into a second file or a subpackage, a name-pinned read stops seeing a
# genuinely driven refusal and reds a code that IS driven — a false red
# nobody would connect back to this gate. Note the absence of -maxdepth:
# the two helpers above are deliberately shallow because their corpus is a
# flat package; this one declares a TREE.
catalogue_go_files() { # $1 = catalogue root
  find "$1" -name '*.go' ! -name '*_test.go' 2>/dev/null | sort
}

# catalogue_refusal_declarations prints the catalogue's own machine-readable
# refusal declarations, TAB-separated, one per line:
#
#   P <path-id> <code> <file>:<line>   a Refusal literal resolved to the
#                                      Path literal that ENCLOSES it
#   E <file>:<line> <why>              a Refusal literal that cannot be
#                                      attached without guessing
#
# An E line is a HARD FAIL upstream, never a skip. Mis-attribution is a
# DIFFERENT failure from unresolvable and the more dangerous one (spec 11
# §T5.2): a "nearest preceding ID:" rule credits the PREVIOUS path, and if
# that path is driven an undriven code passes — green gate, real bug. So
# association is STRUCTURAL: braces are counted over source with comments
# dropped and string bodies neutralised, a Path literal opens a frame, and
# a Refusal attaches to the innermost frame open at its own position. A
# path whose ID: field sits after its Steps: block therefore resolves to
# ITSELF, because its frame is still open when the ID: is read.
#
# Two shapes open a frame, because the catalogue uses both and field order
# is not a property this parser may assume: `x := Path{` (and any other
# `Path{` composite) and an element `{` directly inside a `[]Path{`.
#
# `Refused: nil` declares no refusal and yields no declaration — it carries
# no Refusal{Code:} literal, which is what 4b extracts. Any OTHER spelling
# this parser cannot read is an E line.
catalogue_refusal_declarations() { # $1 = catalogue root
  local f
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    LC_ALL=C awk '
      function scanline(s,   i, n, c) {
        n = length(s); g_code = ""; i = 1
        while (i <= n) {
          c = substr(s, i, 1)
          if (inblock) {
            if (c == "*" && substr(s, i + 1, 1) == "/") { inblock = 0; i += 2 } else { i++ }
            continue
          }
          if (instr) {
            if (c == "\\") { g_code = g_code "__"; i += 2; continue }
            if (c == "{" || c == "}") g_code = g_code "_"; else g_code = g_code c
            if (c == "\"") instr = 0
            i++; continue
          }
          if (inbt) {
            if (c == "{" || c == "}") g_code = g_code "_"; else g_code = g_code c
            if (c == "`") inbt = 0
            i++; continue
          }
          if (inch) {
            if (c == "\\") { g_code = g_code "__"; i += 2; continue }
            g_code = g_code c
            if (c == "'"'"'") inch = 0
            i++; continue
          }
          if (c == "/" && substr(s, i + 1, 1) == "/") break
          if (c == "/" && substr(s, i + 1, 1) == "*") { inblock = 1; i += 2; continue }
          if (c == "\"") { instr = 1; g_code = g_code c; i++; continue }
          if (c == "`")  { inbt = 1;  g_code = g_code c; i++; continue }
          if (c == "'"'"'")  { inch = 1;  g_code = g_code c; i++; continue }
          g_code = g_code c
          i++
        }
      }
      function qval(s,   a, b, r) {
        a = index(s, "\"")
        if (a == 0) return ""
        r = substr(s, a + 1)
        b = index(r, "\"")
        if (b == 0) return ""
        return substr(r, 1, b - 1)
      }
      function openframe() {
        sp++; nextf++
        fidx[sp] = nextf
        fdepth[sp] = depth
        fopen[nextf] = FILENAME ":" FNR
        frid[nextf] = ""
        nref[nextf] = 0
      }
      function emit_err(loc, msg) { errloc[++nerr] = loc; errmsg[nerr] = msg }
      FNR == 1 {
        inblock = 0; instr = 0; inbt = 0; inch = 0
        depth = 0; sp = 0
        split("", pathelem)
      }
      {
        scanline($0)
        m = length(g_code)
        for (i = 1; i <= m; i++) {
          c = substr(g_code, i, 1)
          if (c == "{") {
            pre = substr(g_code, 1, i - 1)
            if (pre ~ /\[\]Path[ \t]*$/) { depth++; pathelem[depth] = 1 }
            else if (pre ~ /(^|[^A-Za-z0-9_.])Path[ \t]*$/) { depth++; openframe() }
            else if (depth in pathelem) { depth++; openframe() }
            else depth++
            continue
          }
          if (c == "}") {
            if (sp > 0 && fdepth[sp] == depth) sp--
            if (depth in pathelem) delete pathelem[depth]
            depth--
            continue
          }
          if (i > 1 && substr(g_code, i - 1, 1) ~ /[A-Za-z0-9_]/) continue
          rest = substr(g_code, i)
          if (rest ~ /^ID:/) {
            if (sp > 0 && depth == fdepth[sp]) {
              v = qval(rest)
              if (v != "") frid[fidx[sp]] = v
            }
            continue
          }
          if (rest ~ /^Refused:/) {
            loc = FILENAME ":" FNR
            if (rest ~ /^Refused:[ \t]*nil/) continue
            if (rest !~ /^Refused:[ \t]*&?Refusal\{[ \t]*Code:[ \t]*"/) {
              emit_err(loc, "a Refused: field whose Refusal{Code: \"...\"} literal this parser cannot read")
              continue
            }
            v = qval(rest)
            if (sp == 0) {
              emit_err(loc, "a Refused: literal (" v ") that sits inside no Path{...} literal")
              continue
            }
            k = fidx[sp]
            nref[k]++
            refcode[k, nref[k]] = v
            refloc[k, nref[k]] = loc
            continue
          }
        }
      }
      END {
        if (depth != 0 || sp != 0)
          emit_err(FILENAME, "brace structure does not balance to zero at end of file — no association made in it can be trusted")
        for (k = 1; k <= nextf; k++) {
          for (j = 1; j <= nref[k]; j++) {
            if (frid[k] == "")
              emit_err(refloc[k, j], "a Refused: literal (" refcode[k, j] ") inside a Path{...} literal opened at " fopen[k] " that declares no ID: field")
            else
              printf "P\t%s\t%s\t%s\n", frid[k], refcode[k, j], refloc[k, j]
          }
        }
        for (e = 1; e <= nerr; e++) printf "E\t%s\t%s\n", errloc[e], errmsg[e]
      }
    ' "$f"
  done < <(catalogue_go_files "$1")
}

# catalogue_driven_path_ids prints every path id the catalogue declares as
# actually driven: drivenPathIDs() plus departedCounterpartyPathIDs()
# (Family 15's own three, driven through their own runner), MINUS every id
# undrivablePaths() names.
#
# The subtraction is inert on today's real tree — neither undrivable entry
# declares a Refused step — and it is read and tested anyway: a declared
# path the driver does not execute is precisely the shape
# pathdrivability.go exists to record, and a refusal is not driven by a
# narrative nobody runs.
catalogue_driven_path_ids() { # $1 = catalogue root
  local f
  {
    while IFS= read -r f; do
      [ -n "$f" ] || continue
      LC_ALL=C awk '
        function qval(s,   a, b, r) {
          a = index(s, "\"")
          if (a == 0) return ""
          r = substr(s, a + 1)
          b = index(r, "\"")
          if (b == 0) return ""
          return substr(r, 1, b - 1)
        }
        FNR == 1 { indriven = 0; inundrivable = 0 }
        /^func drivenPathIDs\(\)/ { indriven = 1; next }
        /^func departedCounterpartyPathIDs\(\)/ { indriven = 1; next }
        /^func undrivablePaths\(\)/ { inundrivable = 1; next }
        (indriven || inundrivable) && /^}/ { indriven = 0; inundrivable = 0; next }
        {
          line = $0
          sub(/\/\/.*$/, "", line)
          if (indriven && line ~ /^[ \t]*"[^"]*",?[ \t]*$/) { print "D\t" qval(line); next }
          if (inundrivable && line ~ /(^|[^A-Za-z0-9_])ID:[ \t]*"[^"]*"/) { print "U\t" qval(substr(line, index(line, "ID:"))); next }
        }
      ' "$f"
    done < <(catalogue_go_files "$1")
  } | LC_ALL=C awk -F'\t' '
      $1 == "D" { driven[$2] = 1; next }
      $1 == "U" { undrivable[$2] = 1; next }
      END { for (id in driven) if (!(id in undrivable)) print id }
    '
}

# check_obligations walks every registered code in $1 (a registry.yaml) and
# applies obligation 0 (severity correlation), the three T5 obligations and
# obligation 4, reading Go/skill/test evidence rooted at $2 and the
# conformance catalogue rooted at $3.
#
# ONE registry walk, four obligations. A second copy of this walk in a
# sibling script is the exact drift this gate exists to catch, one layer up
# (spec 11 §T5.1).
check_obligations() { # $1 = registry file, $2 = source root, $3 = catalogue root
  local registry="$1" root="$2" catroot="$3"
  local code class severity mode_scope reason prose_exempt reach_exempt path_exempt
  local sev_actual want
  local driven_codes="" undriven_exempt=0
  local tag decl_a decl_b

  [ -f "$registry" ] || { gate_fail "check-error-codes: registry not found: $registry"; return; }

  # Obligation 4, first half: read the catalogue's own declarations before
  # the registry walk needs them. A refusal the parser cannot attach is a
  # hard fail here — the whole obligation is worthless if an unreadable
  # declaration can be silently dropped from the driven set.
  if [ -z "$(catalogue_go_files "$catroot")" ]; then
    gate_fail "check-error-codes: obligation 4 found no non-test Go file under $catroot — the conformance catalogue is where a declared refusal lives, and a gate that walks to nothing greens over every undriven code"
  else
    local declared_driven_ids
    declared_driven_ids="$(catalogue_driven_path_ids "$catroot")"
    # E lines carry <file>:<line> then the reason; P lines carry the path
    # id then the code (the trailing <file>:<line> is read and unused —
    # the pair itself is the fact, its location is only needed when the
    # association FAILS).
    while IFS=$'\t' read -r tag decl_a decl_b _; do
      case "$tag" in
        E) gate_fail "check-error-codes: obligation 4 cannot resolve a declared refusal at $decl_a — $decl_b. A refusal this parser can only attach by GUESSING is refused, never skipped: crediting the nearest preceding path would green an undriven code" ;;
        P) if grep -qxF -- "$decl_a" <<< "$declared_driven_ids"; then
             driven_codes="$driven_codes$decl_b"$'\n'
           fi ;;
      esac
    done < <(catalogue_refusal_declarations "$catroot")
  fi

  while IFS= read -r code; do
    [ -z "$code" ] && continue
    class="$(registry_field "$registry" "$code" "class")"
    severity="$(registry_field "$registry" "$code" "severity")"
    mode_scope="$(registry_field "$registry" "$code" "mode_scope")"
    reason="$(registry_field "$registry" "$code" "mode_scope_reason")"
    prose_exempt="$(registry_field "$registry" "$code" "prose_exempt")"
    reach_exempt="$(registry_field "$registry" "$code" "reachability_exempt")"
    path_exempt="$(registry_field "$registry" "$code" "path_exempt")"
    emitted_by="$(registry_field "$registry" "$code" "emitted_by")"
    [ -n "$emitted_by" ] || emitted_by="internal/validate"

    if [ -z "$severity" ]; then
      gate_fail "check-error-codes: $code declares no severity — obligation 3 (mode scope) cannot be judged"
      continue
    fi
    case "$severity" in
      reject | warning | unmeasured) ;;
      *) gate_fail "check-error-codes: $code declares an unrecognized severity '$severity' — want reject|warning|unmeasured" ;;
    esac
    case "$mode_scope" in
      v3-pr | v3-full-repo | both) ;;
      *) gate_fail "check-error-codes: $code declares an unrecognized mode_scope '$mode_scope' — want v3-pr|v3-full-repo|both" ;;
    esac

    # Obligation 0: severity correlation (SSOT is the registry; Go must agree).
    #
    # It correlates a DECLARED severity with the one the emitting Go code
    # carries, so the two cannot drift. That presumes the emitter is a
    # validate.Violation with a `Severity:` literal beside its `Code:` literal,
    # which was true of every code minted before 2026-08-21 and was therefore
    # never written down. REF-024 ended it: a write-funnel refusal in
    # internal/space, placed there so BOTH write surfaces inherit it through
    # funnel.Submit instead of each mapping it (ADR-004). It carries no `Code:`
    # literal because a funnel refusal is not a Violation, and there is nothing
    # to correlate because it refuses BY CONSTRUCTION — a funnel seat has no
    # warning variant. Correlating a severity that cannot vary would be this
    # gate asserting a claim rather than a fact.
    #
    # So the registry is asked who raises the code. A foreign emitter is
    # ANNOUNCED, not silently skipped: an obligation that quietly stops
    # applying is the defect this gate exists to catch, one level up.
    if [ "$class" = "schema" ]; then
      : # schema_class.go hardcodes SeverityReject uniformly for the whole class.
    elif [ "$emitted_by" != "internal/validate" ]; then
      echo "check-error-codes: obligation 0 exempt — $code (emitted_by:$emitted_by; a refusal raised outside internal/validate carries no Code:/Severity: pair to correlate and no severity variant to drift from — it refuses by construction. Its severity is proven by its emitter's own tests and by the declared path that drives it.)"
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

    # Obligation 4: a declared path drives the refusal. Reject codes only —
    # a warning is not a refusal, so a warning code is never judged here in
    # any configuration, and never carries a waiver for a judgment that
    # cannot apply to it.
    if [ "$severity" = "reject" ]; then
      if grep -qxF -- "$code" <<< "$driven_codes"; then
        if [ -n "$path_exempt" ]; then
          gate_fail "check-error-codes: $code IS driven by a declared conformance path and still carries a path_exempt — a waiver for a debt that has been paid. Delete it: the count this obligation prints has to stay equal to the registry's own waiver-row count, or the burn-down stops being a number anyone can read off the file"
        fi
      elif [ -n "$path_exempt" ]; then
        undriven_exempt=$((undriven_exempt + 1))
      else
        gate_fail "check-error-codes: $code (severity:reject) is named by no Refusal{Code:} literal in a driven conformance path under $catroot — a rule no declared path drives is indistinguishable from a rule that is wrong (internal/livee2e/operationpull.go:52's own stranded comment). Declare a path that provokes it, or state a path_exempt reason in the registry — silence is not spellable"
      fi
    fi
  done < <(registry_all_codes "$registry")

  # Obligation 4e: the burn-down, printed on EVERY run whatever the verdict.
  # It is a number anyone can re-derive against the registry's own waiver
  # rows, not a claim this gate makes about itself.
  echo "check-error-codes: obligation 4 — ${undriven_exempt} severity:reject code(s) are driven by no declared conformance path and carry a stated waiver. Retiring one is a one-line commit: drive the code from a declared path, delete its waiver."
}

run_check() { # $1 = registry file, $2 = source root, $3 = catalogue root
  check_obligations "$1" "$2" "$3"
}

# ── teeth ────────────────────────────────────────────────────────────────

# $6 (catalogue root) defaults to the fixture tree's own internal/livee2e,
# which write_fixture_tree seeds — a case that needs a DIFFERENT catalogue
# (an undrivable path, an unresolvable association, a second catalogue
# file) passes its own. A teeth case that read the REAL catalogue would not
# be a teeth case at all: it cannot construct those shapes.
teeth_expect() { # $1 = label, $2 = red|green, $3 = needle, $4 = registry, $5 = root, $6 = catalogue root (optional)
  local label="$1" verdict="$2" needle="$3" registry="$4" root="$5" out rc
  local catroot="${6:-$5/internal/livee2e}"
  set +e
  out="$(
    (
      _GATE_ERRORS=0
      run_check "$registry" "$root" "$catroot"
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
# scanned directory, a skill/ dir, a synthetic conformance catalogue that
# declares one driven path and refuses nothing, and whatever the caller
# adds on top.
write_fixture_tree() { # $1 = root
  local root="$1"
  mkdir -p "$root/internal/validate" "$root/internal/cli" "$root/internal/mcp" "$root/internal/e2e" "$root/skill" "$root/internal/livee2e"
  : > "$root/skill/.gitkeep"
  write_fixture_catalogue "$root/internal/livee2e"
}

# write_fixture_catalogue writes the DEFAULT synthetic catalogue: one path,
# driven, declaring no refusal at all. Every obligation-1/2/3 case rides on
# it, so those cases keep testing what they were written for and none of
# them accidentally becomes an obligation-4 case.
write_fixture_catalogue() { # $1 = catalogue root
  mkdir -p "$1"
  cat > "$1/pathcatalogue_paths.go" <<'EOF'
package livee2e

func ConformancePaths() []Path {
	return []Path{
		{
			ID:    "fixture-driven-path",
			Steps: []Step{{Actor: SystemA, Transition: "submit"}},
		},
	}
}
EOF
  cat > "$1/pathdrivability.go" <<'EOF'
package livee2e

func drivenPathIDs() []string {
	return []string{
		"fixture-driven-path",
	}
}

func undrivablePaths() []undrivablePath {
	return []undrivablePath{}
}
EOF
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
    path_exempt: fixture — this case is about the reachability obligation, not about being driven
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
    path_exempt: fixture — this case is about the prose obligation, not about being driven
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
    path_exempt: fixture — this case is about the mode-scope obligation, not about being driven
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

  # ── severity vocabulary (no-silent-yes-2026-08/P3, D9) ──────────────────
  #
  # D9 mints `unmeasured` as the registry's third severity value. Two cases:
  # an unrecognized FOURTH value still reds naming both the code and the
  # now-three-member wanted set, and `unmeasured` itself is accepted with NO
  # side effect on obligations 1/4 — a code at that severity carries no
  # prose and drives no path and still greens, the same class-level
  # exemption `severity: warning` already has (o4-j below proves the
  # warning half; this proves the unmeasured half).

  # sev-a: a FOURTH, unrecognized severity value reds, naming the code and
  # the updated wanted set — proves the vocabulary teaches exactly
  # reject|warning|unmeasured, not "anything now goes".
  d="$work/sev-unrecognized"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-930
    class: referential
    title: fixture
    applies_to: fixture
    severity: bogus-fourth-value
    mode_scope: v3-pr
    emitted_by: internal/space
    reachability_exempt: fixture — this case is about the severity vocabulary, not reachability
EOF
  teeth_expect "sev: a fourth, unrecognized severity value reds naming the code" red \
    "REF-930 declares an unrecognized severity 'bogus-fourth-value' — want reject|warning|unmeasured" \
    "$reg" "$root" || return 1

  # sev-b: `severity: unmeasured` itself is accepted by the vocabulary check
  # AND stands obligations 1/4 down (D9's own foreclosure: it can never
  # block, so it is not "a refusal" either) — no prose anywhere under
  # skill/, no driven path, no path_exempt, and the row still greens.
  d="$work/sev-unmeasured"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-931
    class: referential
    title: fixture
    applies_to: fixture
    severity: unmeasured
    mode_scope: v3-pr
    emitted_by: internal/space
    reachability_exempt: fixture — this case is about the severity vocabulary, not reachability
EOF
  teeth_expect "sev: severity:unmeasured is accepted and stands obligations 1/4 down, with no prose and no driven path" green \
    "" "$reg" "$root" || return 1

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
    path_exempt: fixture — this case is about the reachability waiver, not about being driven
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
  # v3-pr scope needing no reason, AND a driven declared path that names it)
  # greens outright — proves the obligations are not vacuously red-only.
  # This is the ONE case whose catalogue actually refuses something, so it
  # is also obligation 4's happy predicate: the real-tree shape of LFC-001,
  # LFC-002, POL-006 and REF-024, with no waiver anywhere.
  d="$work/control"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$root/internal/livee2e/pathcatalogue_paths.go" <<'EOF'
package livee2e

func ConformancePaths() []Path {
	return []Path{
		{
			ID: "fixture-refusal-path",
			Steps: []Step{
				{
					Actor:   SystemA,
					Refused: &Refusal{Code: "REF-905"},
				},
			},
		},
	}
}
EOF
  cat > "$root/internal/livee2e/pathdrivability.go" <<'EOF'
package livee2e

func drivenPathIDs() []string {
	return []string{
		"fixture-refusal-path",
	}
}

func undrivablePaths() []undrivablePath {
	return []undrivablePath{}
}
EOF
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

  # emitted_by: a refusal raised OUTSIDE internal/validate has no Code:/
  # Severity: pair to correlate. Two halves, and the first is the one that
  # matters: WITHOUT the declaration the same tree reds, so the exemption is
  # load-bearing rather than a branch that would have passed anyway. This
  # mirrors the hand-check run when REF-024 introduced the shape on
  # 2026-08-21; a hand-check nobody repeats is a gate not yet written.
  d="$work/emitted-by"; write_fixture_tree "$d/root"; root="$d/root"
  cat > "$d/undeclared.yaml" <<'EOF'
entries:
  - code: REF-907
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    prose_exempt: fixture
    reachability_exempt: fixture
    path_exempt: fixture — this case is about obligation 0's emitter question, not about being driven
EOF
  teeth_expect "emitted_by absent: a funnel-shaped refusal still reds" red \
    "REF-907 has no Severity: literal within 8 lines of its Code: literal" "$d/undeclared.yaml" "$root" || return 1

  cat > "$d/declared.yaml" <<'EOF'
entries:
  - code: REF-907
    class: referential
    emitted_by: internal/space
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    prose_exempt: fixture
    reachability_exempt: fixture
    path_exempt: fixture — this case is about obligation 0's emitter question, not about being driven
EOF
  teeth_expect "emitted_by declared: obligation 0 stands down and says so" green \
    "" "$d/declared.yaml" "$root" || return 1

  # ── obligation 4 (judge-the-thing-2026-08 spec 11 §6) ──────────────────
  #
  # Every case below rides on a SYNTHETIC catalogue root. A case that read
  # the real internal/livee2e tree would not be a teeth case at all: the
  # undrivable-path, unresolvable-association and ID-after-Steps shapes
  # cannot be constructed there without editing the product's own
  # catalogue, and a gate proved against the very data it judges proves
  # nothing.
  #
  # Each fixture row carries prose_exempt/reachability_exempt so the ONLY
  # obligation on trial is the fourth — the mirror of what the five cases
  # above just did for obligations 0-3.

  # o4-a (AC1): a reject code no declared path drives reds, naming the code
  # AND the catalogue tree, so the reader knows where to add the path.
  d="$work/o4-undriven"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-910
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
EOF
  teeth_expect "o4: a reject code no declared path drives reds" red \
    "REF-910 (severity:reject) is named by no Refusal{Code:} literal in a driven conformance path" \
    "$reg" "$root" || return 1

  # o4-b (AC2): PROSE IS NOT A DECLARATION, and this is the live false
  # green a string search produces. On the real catalogue REF-006 (4 hits)
  # and POL-007 (1 hit) appear ONLY inside Intent strings, and both are in
  # the undriven set — an implementation that greps the code string reports
  # 48 undriven and is wrong about two codes in the direction that matters.
  d="$work/o4-prose"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$root/internal/livee2e/pathcatalogue_paths.go" <<'EOF'
package livee2e

func ConformancePaths() []Path {
	return []Path{
		{
			ID:     "fixture-driven-path",
			Intent: "the paired NEGATIVE control this path does not actually declare: REF-911 is named here in prose only.",
			Steps:  []Step{{Actor: SystemA, Transition: "submit"}},
		},
	}
}
EOF
  cat > "$reg" <<'EOF'
entries:
  - code: REF-911
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
EOF
  teeth_expect "o4: a code named only in a path's Intent prose is NOT driven" red \
    "REF-911 (severity:reject) is named by no Refusal{Code:} literal in a driven conformance path" \
    "$reg" "$root" || return 1

  # o4-c (AC3): declared but UNDRIVABLE. The id sits in drivenPathIDs() AND
  # in undrivablePaths() on purpose — that is the only shape in which the
  # subtraction is load-bearing, so dropping it turns this case green.
  # Inert on today's real tree (neither undrivable entry declares a Refused
  # step) and tested anyway: a refusal is not driven by a narrative nobody
  # runs.
  d="$work/o4-undrivable"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$root/internal/livee2e/pathcatalogue_paths.go" <<'EOF'
package livee2e

func ConformancePaths() []Path {
	return []Path{
		{
			ID: "fixture-undrivable-path",
			Steps: []Step{
				{Actor: SystemA, Refused: &Refusal{Code: "REF-912"}},
			},
		},
	}
}
EOF
  cat > "$root/internal/livee2e/pathdrivability.go" <<'EOF'
package livee2e

func drivenPathIDs() []string {
	return []string{
		"fixture-undrivable-path",
	}
}

func undrivablePaths() []undrivablePath {
	return []undrivablePath{
		{
			ID:     "fixture-undrivable-path",
			Reason: "the driver does not execute this narrative for real",
		},
	}
}
EOF
  cat > "$reg" <<'EOF'
entries:
  - code: REF-912
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
EOF
  teeth_expect "o4: a code declared inside an undrivable path is NOT driven" red \
    "REF-912 (severity:reject) is named by no Refusal{Code:} literal in a driven conformance path" \
    "$reg" "$root" || return 1

  # o4-d (AC4, both halves): a stated waiver greens; a BLANK one reds
  # exactly like an absent one — silence must not be spellable.
  d="$work/o4-waiver"; write_fixture_tree "$d/root"; root="$d/root"
  cat > "$d/stated.yaml" <<'EOF'
entries:
  - code: REF-913
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
    path_exempt: undriven, counted, and named in the backlog row this reason cites
EOF
  teeth_expect "o4: a waiver with a stated reason greens" green \
    "" "$d/stated.yaml" "$root" || return 1

  cat > "$d/blank.yaml" <<'EOF'
entries:
  - code: REF-914
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
    path_exempt:
EOF
  teeth_expect "o4: an empty waiver reds exactly like an absent one" red \
    "REF-914 (severity:reject) is named by no Refusal{Code:} literal in a driven conformance path" \
    "$d/blank.yaml" "$root" || return 1

  # The trailing run of spaces is written with printf rather than inside the
  # heredoc so no formatter can silently strip the very thing on trial.
  {
    cat <<'EOF'
entries:
  - code: REF-915
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
EOF
    printf '    path_exempt:    \n'
  } > "$d/whitespace.yaml"
  teeth_expect "o4: a whitespace-only waiver reds exactly like an absent one" red \
    "REF-915 (severity:reject) is named by no Refusal{Code:} literal in a driven conformance path" \
    "$d/whitespace.yaml" "$root" || return 1

  # o4-e (AC5): an association the parser cannot make is a HARD FAIL naming
  # file and line — never a silent skip, never a nearest-ID guess. Two
  # shapes: a Refused: enclosed by no Path literal at all, and a Refused:
  # inside a Path literal that declares no ID.
  d="$work/o4-stray"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$root/internal/livee2e/stray.go" <<'EOF'
package livee2e

func strayStep() Step {
	return Step{
		Actor:   SystemA,
		Refused: &Refusal{Code: "REF-916"},
	}
}
EOF
  cat > "$reg" <<'EOF'
entries:
  - code: REF-916
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
    path_exempt: waived — the case on trial is the parse, not the waiver
EOF
  teeth_expect "o4: a Refused: enclosed by no Path literal hard-fails, naming file and line" red \
    "stray.go:6 — a Refused: literal (REF-916) that sits inside no Path{...} literal" \
    "$reg" "$root" || return 1

  d="$work/o4-noid"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$root/internal/livee2e/pathcatalogue_paths.go" <<'EOF'
package livee2e

func ConformancePaths() []Path {
	return []Path{
		{
			Steps: []Step{
				{Actor: SystemA, Refused: &Refusal{Code: "REF-916"}},
			},
		},
	}
}
EOF
  cat > "$reg" <<'EOF'
entries:
  - code: REF-916
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
    path_exempt: waived — the case on trial is the parse, not the waiver
EOF
  teeth_expect "o4: a Refused: inside a Path literal with no ID hard-fails" red \
    "that declares no ID: field" "$reg" "$root" || return 1

  # o4-f (AC15): MIS-ATTRIBUTION, the more dangerous failure. The stranded
  # path declares its ID AFTER its Steps block, and the path DECLARED
  # BEFORE IT is in drivenPathIDs() — so a nearest-preceding-ID parser
  # credits the driven path and greens an undriven code. It must red.
  d="$work/o4-misattribution"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$root/internal/livee2e/pathcatalogue_paths.go" <<'EOF'
package livee2e

func ConformancePaths() []Path {
	driven := Path{
		ID: "fixture-driven-path",
		Steps: []Step{
			{Actor: SystemA, Transition: "submit"},
		},
	}

	stranded := Path{
		Steps: []Step{
			{
				Actor:   SystemA,
				Refused: &Refusal{Code: "REF-917"},
			},
		},
		ID: "fixture-stranded-path",
	}

	return []Path{driven, stranded}
}
EOF
  cat > "$reg" <<'EOF'
entries:
  - code: REF-917
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
EOF
  teeth_expect "o4: an ID declared AFTER Steps resolves to its own path, never the preceding one" red \
    "REF-917 (severity:reject) is named by no Refusal{Code:} literal in a driven conformance path" \
    "$reg" "$root" || return 1

  # o4-g (AC16): a Refusal declared in a non-test catalogue file OTHER than
  # pathcatalogue_paths.go still counts. This is the false RED a name-pinned
  # read produces the day a family is split into its own file — nobody
  # would connect that failure back to this gate.
  d="$work/o4-secondfile"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$root/internal/livee2e/pathcatalogue_family15.go" <<'EOF'
package livee2e

func departedCounterpartyPaths() []Path {
	return []Path{
		{
			ID: "fixture-second-file-path",
			Steps: []Step{
				{Actor: SystemA, Refused: &Refusal{Code: "REF-918"}},
			},
		},
	}
}
EOF
  cat > "$root/internal/livee2e/pathdrivability.go" <<'EOF'
package livee2e

func drivenPathIDs() []string {
	return []string{
		"fixture-driven-path",
	}
}

func departedCounterpartyPathIDs() []string {
	return []string{
		"fixture-second-file-path",
	}
}

func undrivablePaths() []undrivablePath {
	return []undrivablePath{}
}
EOF
  cat > "$reg" <<'EOF'
entries:
  - code: REF-918
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
EOF
  teeth_expect "o4: a Refusal in a second catalogue file counts as driven" green \
    "" "$reg" "$root" || return 1

  # o4-h (AC9): the SAME catalogue, green then red, with one id deleted from
  # drivenPathIDs() — a refusal must not be able to go inert by someone
  # else's edit, and the pair is what proves the driven half is not
  # vacuous.
  d="$work/o4-stranded"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$root/internal/livee2e/pathcatalogue_paths.go" <<'EOF'
package livee2e

func ConformancePaths() []Path {
	return []Path{
		{
			ID: "fixture-refusal-path",
			Steps: []Step{
				{Actor: SystemA, Refused: &Refusal{Code: "REF-919"}},
			},
		},
	}
}
EOF
  cat > "$reg" <<'EOF'
entries:
  - code: REF-919
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
EOF
  cat > "$root/internal/livee2e/pathdrivability.go" <<'EOF'
package livee2e

func drivenPathIDs() []string {
	return []string{
		"fixture-refusal-path",
	}
}

func undrivablePaths() []undrivablePath {
	return []undrivablePath{}
}
EOF
  teeth_expect "o4: the declared path is driven — the code greens with no waiver" green \
    "" "$reg" "$root" || return 1

  cat > "$root/internal/livee2e/pathdrivability.go" <<'EOF'
package livee2e

func drivenPathIDs() []string {
	return []string{}
}

func undrivablePaths() []undrivablePath {
	return []undrivablePath{}
}
EOF
  teeth_expect "o4: deleting the path's id from drivenPathIDs strands its refusal" red \
    "REF-919 (severity:reject) is named by no Refusal{Code:} literal in a driven conformance path" \
    "$reg" "$root" || return 1

  # o4-i (AC7's identity): a waiver on a code that IS driven is a waiver for
  # a debt already paid, and it breaks the one property that makes the
  # printed count re-derivable off the file.
  cat > "$root/internal/livee2e/pathdrivability.go" <<'EOF'
package livee2e

func drivenPathIDs() []string {
	return []string{
		"fixture-refusal-path",
	}
}

func undrivablePaths() []undrivablePath {
	return []undrivablePath{}
}
EOF
  cat > "$d/paid.yaml" <<'EOF'
entries:
  - code: REF-919
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
    path_exempt: stale — the path that drives this code already exists
EOF
  teeth_expect "o4: a waiver on a code that IS driven reds as a paid debt" red \
    "REF-919 IS driven by a declared conformance path and still carries a path_exempt" \
    "$d/paid.yaml" "$root" || return 1

  # o4-j (AC12): a `severity: warning` code is never judged by obligation 4,
  # in ANY configuration — no path, no waiver, green. A warning is not a
  # refusal, the same class-level reasoning obligation 1 uses for ADR-011 D4.
  d="$work/o4-warning"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-920
    class: referential
    title: fixture
    applies_to: fixture
    severity: warning
    mode_scope: v3-pr
    reachability_exempt: fixture
EOF
  cat > "$root/internal/validate/fixture.go" <<'EOF'
package validate

func check() []Violation {
	return []Violation{{
		Code:     "REF-920",
		Severity: SeverityWarning,
	}}
}
EOF
  teeth_expect "o4: a severity:warning code is never judged, with no path and no waiver" green \
    "" "$reg" "$root" || return 1

  # o4-k: a catalogue root that walks to NOTHING reds. The obligation is
  # satisfied by evidence the catalogue supplies; a reader that finds no
  # catalogue at all has not found "no refusals declared", it has found
  # nothing, and this whole epic is about the difference.
  d="$work/o4-nocatalogue"; write_fixture_tree "$d/root"; root="$d/root"; reg="$d/registry.yaml"
  mkdir -p "$d/empty"
  cat > "$reg" <<'EOF'
entries:
  - code: REF-921
    class: referential
    title: fixture
    applies_to: fixture
    severity: reject
    mode_scope: v3-pr
    emitted_by: internal/space
    prose_exempt: fixture
    reachability_exempt: fixture
    path_exempt: waived — the case on trial is the empty catalogue, not the waiver
EOF
  teeth_expect "o4: a catalogue root that walks to nothing reds" red \
    "obligation 4 found no non-test Go file under" "$reg" "$root" "$d/empty" || return 1

  echo "check-error-codes --teeth: PASS — AC1 (no production-path test), AC2 (reject code absent from skill/**) and AC3 (reject+both with no reason) all red naming the code; AC4's stated-reason exemption and a fully-closed row both green; a registry/Go severity disagreement reds distinctly from the three T5 obligations; and a code declaring emitted_by outside internal/validate stands obligation 0 down, while the SAME row without that declaration still reds. Obligation 4: an undriven reject code, a code named only in Intent PROSE, a code inside an UNDRIVABLE path, a blank waiver and a whitespace-only waiver all red naming the code; a Refused: with no enclosing Path and one inside a Path with no ID hard-fail naming file and line; an ID declared AFTER Steps resolves to its OWN path rather than the driven one before it; a Refusal in a SECOND catalogue file counts; deleting a path's id from drivenPathIDs strands its refusal, while the same catalogue with the id present greens with no waiver; a waiver on an already-driven code reds as a paid debt; a severity:warning code is never judged at all; and a catalogue root that walks to nothing reds instead of greening over every undriven code"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
else
  run_check "$GATE_ROOT/schemas/errors/v1/registry.yaml" "$GATE_ROOT" "$GATE_ROOT/internal/livee2e"
  gate_summary "check-error-codes"
fi
