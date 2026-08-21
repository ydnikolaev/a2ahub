#!/usr/bin/env bash
# gate-lib.sh — shared helpers for repo gate scripts (scripts/check-*.sh).
#
# Source it from a script in scripts/:
#     source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"
#
# Deliberately a NEW lib under scripts/lib/ rather than reusing
# .agents/scripts/lib/common.sh — live `check` gates must NOT depend on the
# `.agents/` tree the pipeline epic is deprecating (removal hazard / dependency
# direction). NOT auto-exec — source only.
#
# Adopted by ALL 15 scripts/check-*.sh (migration completed 2026-06-20, done in
# verified batches — never a big-bang rewrite, since a bug here would fail every gate
# in check at once). Collect-all gates use gate_fail/gate_warn/gate_ok + gate_summary;
# fail-fast gates use GATE_ROOT only (their exit-on-first-error model is preserved).
#
# Provides: GATE_ROOT (repo root), gate_fail / gate_warn / gate_ok / gate_unmeasured
# (CI-aware), gate_summary (prints tally, returns 1 if any error and
# GATE_EXIT_UNMEASURED if anything could not be measured).
#
# Its own self-test: `bash scripts/lib/gate-lib.sh --teeth` (see the bottom of
# this file — guarded on BASH_SOURCE, so sourcing it never runs it).

# Repo root, resolved canonically from this lib's own location (scripts/lib → repo).
# shellcheck disable=SC2034  # consumed by sourcing scripts, not within this lib
GATE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# Colors — off when stdout is not a TTY or NO_COLOR is set.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  GATE_RED=$'\033[0;31m'; GATE_YEL=$'\033[0;33m'; GATE_GRN=$'\033[0;32m'; GATE_NC=$'\033[0m'
else
  GATE_RED=''; GATE_YEL=''; GATE_GRN=''; GATE_NC=''
fi

_GATE_ERRORS=0
_GATE_WARNINGS=0
_GATE_UNMEASURED=0

# The exit status gate_summary returns when this run COULD NOT MEASURE part of
# what it judges. 1 stays what it has always been — "I measured it and it is
# wrong" — so a caller can branch on the difference instead of guessing from
# prose. 3, not 2: scripts/verify.sh already spends 2 on a usage error, and two
# meanings on one number is the substitution this whole helper exists to stop.
# shellcheck disable=SC2034  # consumed by sourcing scripts, not within this lib
GATE_EXIT_UNMEASURED=3

# In GitHub Actions, emit workflow annotations; otherwise plain text.
_gate_ci() { [ "${GITHUB_ACTIONS:-}" = "true" ]; }

gate_fail() {
  _GATE_ERRORS=$((_GATE_ERRORS + 1))
  if _gate_ci; then echo "::error::$*"; else echo "${GATE_RED}FAIL${GATE_NC} $*" >&2; fi
}

# gate_unmeasured — "I COULD NOT MEASURE THIS", the sibling of gate_fail.
#
# THE RULE, and it is older than this function: distinguish "I measured X and
# it is wrong" from "I could not measure X", and never let the second borrow
# the first's message (docs/backlog.md; check-notify-secrets.sh's own header
# states it too, and that gate still emitted the wrong message — a header is
# not evidence the rule is applied, only a call site is).
#
# Use it wherever a SHELL-OUT FAILS TO RUN rather than reporting a finding: an
# absent binary, an unreadable or missing input, an empty corpus, `grep` exit 2
# (pattern rejected / file unreadable — NOT "no match"), a shallow clone with
# no history to walk, `go test` refusing because there is no compiler for a
# race build. Every one of those is a fact about the MEASUREMENT. Saying
# "template.html differs from a fresh build" when the build threw, or "grep
# rejected all fourteen shapes" when a tracked file was absent from the tree,
# sends the reader to fix something that is not broken; nine such incidents are
# on the record, and one of them happened to the author of the spec about them
# while it was being written.
#
# Both outcomes stay RED — a gate that cannot run must never look green, and
# the more dangerous half of this class is the FALSE GREEN, because nobody
# investigates a green (check-provider-tier-deferral reported "0 outstanding"
# under a depth-1 clone when the real count was 14). What changes is which
# thing the reader is sent to fix, and the exit status a caller branches on.
#
# Textually distinct in BOTH output modes: the token UNMEASURED, which
# gate_fail never emits. Under GITHUB_ACTIONS this is still `::error::` on
# stdout (Actions has no third severity, and a warning would be wrong — this
# run did not measure what it claims to judge), so the distinction lives in the
# message CONTENT, which is what teeth may safely assert.
gate_unmeasured() {
  _GATE_UNMEASURED=$((_GATE_UNMEASURED + 1))
  if _gate_ci; then echo "::error::UNMEASURED: $*"; else echo "${GATE_RED}UNMEASURED${GATE_NC} $*" >&2; fi
}

gate_warn() {
  _GATE_WARNINGS=$((_GATE_WARNINGS + 1))
  if _gate_ci; then echo "::warning::$*"; else echo "${GATE_YEL}WARN${GATE_NC} $*" >&2; fi
}

gate_ok() { echo "${GATE_GRN}✓${GATE_NC} $*"; }

# Gate-firing telemetry (Δ15) — one JSONL line per gate run; the data source for
# the retirement + suite-wall-clock scans (a "never fired in N months" verdict
# without firing data is a guess). Duration = bash $SECONDS (whole seconds since
# the gate script started — v1 granularity). Appends are best-effort: telemetry
# must NEVER fail a gate. Home: .claude/.telemetry/gates.jsonl (gitignored,
# survives `make clean-artifacts`).
gate_telemetry() { # <name> <verdict:green|red|warn|unmeasured>
  {
    mkdir -p "$GATE_ROOT/.claude/.telemetry" &&
      printf '{"ts":"%s","gate":"%s","verdict":"%s","duration_s":%d,"errors":%d,"warnings":%d}\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "$2" "$SECONDS" "$_GATE_ERRORS" "$_GATE_WARNINGS" \
        >> "$GATE_ROOT/.claude/.telemetry/gates.jsonl"
  } 2>/dev/null || true
}

# Print the tally; return 1 if any error, GATE_EXIT_UNMEASURED if anything
# could not be measured (use as the script's exit status).
#
# UNMEASURED DOMINATES A MIXED RUN, deliberately. Both are red either way, so
# no CI outcome changes; what the exit code answers is "can I trust this
# verdict as complete?", and the answer is no the moment one measurement did
# not happen. Letting a real error outrank it would hide the incomplete half
# behind the measured one — one verdict masquerading as another, which is the
# defect class this helper closes. Nothing is lost: every gate_fail line is
# still printed, and the summary names BOTH counts so the exit code is never
# the only carrier of the distinction.
gate_summary() {
  local name="${1:-gate}"
  if [ "$_GATE_UNMEASURED" -gt 0 ]; then
    gate_telemetry "$name" unmeasured
    echo "${GATE_RED}✗ ${name}: ${_GATE_UNMEASURED} unmeasured, ${_GATE_ERRORS} error(s), ${_GATE_WARNINGS} warning(s) — this run could not measure everything it judges, so its verdict is incomplete${GATE_NC}" >&2
    return "$GATE_EXIT_UNMEASURED"
  fi
  if [ "$_GATE_ERRORS" -gt 0 ]; then
    gate_telemetry "$name" red
    echo "${GATE_RED}✗ ${name}: ${_GATE_ERRORS} error(s), ${_GATE_WARNINGS} warning(s)${GATE_NC}" >&2
    return 1
  fi
  if [ "$_GATE_WARNINGS" -gt 0 ]; then
    gate_telemetry "$name" warn
    echo "${GATE_YEL}${name}: ${_GATE_WARNINGS} warning(s), no errors${GATE_NC}"
    return 0
  fi
  gate_telemetry "$name" green
  return 0
}

# ── --teeth: this lib's own self-test ────────────────────────────────────
#
# GUARDED ON BASH_SOURCE, NEVER ON "$1". Inside a SOURCED file the positional
# parameters are the SOURCING SCRIPT'S — and `make _harness-check` invokes
# every gate as `bash <gate> --teeth`. A `[ "${1:-}" = "--teeth" ]` guard here
# would therefore fire inside all ~30 of them at once, which is the single way
# an edit to this file can break every gate in the repo simultaneously. There
# is a tooth below that holds this exact line down.
#
# Everything from here is inside the guard: no function is defined and no shell
# option is set for a sourcing gate. (`set -e` at this file's top level would
# silently change the error semantics of every script that reads it;
# check-notify-secrets.sh, for one, chooses `set -uo pipefail` for itself.)
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  set -uo pipefail

  if [ "${1:-}" != "--teeth" ]; then
    echo "gate-lib.sh is a library — source it, do not run it." >&2
    echo "The only valid direct invocation is: bash ${BASH_SOURCE[0]} --teeth" >&2
    exit 2
  fi

  # RE-ENTRANCY REFUSAL, and it earned its place empirically. T4 below builds a
  # gate that sources this lib and runs it as `bash <gate> --teeth`. With the
  # entry guard flipped to "$1" (the regression T4 exists to catch), that child
  # enters this block, builds another such gate, and the self-test becomes a
  # FORK BOMB — measured: the run had to be killed at 120 s having printed not
  # one assertion. A tooth that hangs proves nothing a reader can act on, so
  # the nested entry is refused HERE, by name, and T4 then reds with a message
  # instead of with a load average. Placed AFTER the argument check on purpose:
  # T5's child is a bare direct invocation and must still get exit 2.
  if [ -n "${_GATE_TEETH_ACTIVE:-}" ]; then
    echo "gate-lib --teeth: REFUSING a nested run — this block was re-entered from inside its own self-test, which means the entry guard is matching something other than direct execution. Guard on BASH_SOURCE, never on \"\$1\": inside a sourced file the positional parameters belong to the SOURCING gate, and make _harness-check runs every gate as \`bash <gate> --teeth\`." >&2
    exit 1
  fi
  export _GATE_TEETH_ACTIVE=1

  _teeth_lib="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
  _teeth_home="$(mktemp -d)" || exit 1
  trap 'rm -rf -- "${_teeth_home:-}"' EXIT
  _teeth_failures=0

  # Each probe is a FRESH subshell loading this lib from disk, so counters
  # never bleed between cases and GITHUB_ACTIONS is set explicitly rather than
  # inherited (`_harness-check` pins it empty for the whole teeth block).
  # GATE_ROOT is repointed AFTER the load — the lib sets it unconditionally
  # from its own location — so gate_telemetry writes into the scratch dir
  # instead of the real .claude/.telemetry.
  #
  # Sets _T_OUT (stdout), _T_ERR (stderr) and _T_RC separately: the streams are
  # half the contract. Under Actions the emitters write to STDOUT, in plain
  # mode to STDERR, so a tooth that merges them cannot tell a wrong string from
  # an absent one.
  _teeth_probe() { # $1 = GITHUB_ACTIONS value ("" or "true"), $2 = shell body
    local errfile
    errfile="$(mktemp)" || return 1
    _T_OUT="$(env GITHUB_ACTIONS="$1" bash -c 'GATE_TEETH_HOME="$2"; . "$1"; GATE_ROOT="$GATE_TEETH_HOME"; eval "$3"' _ "$_teeth_lib" "$_teeth_home" "$2" 2>"$errfile")"
    _T_RC=$?
    _T_ERR="$(cat "$errfile")"
    rm -f "$errfile"
  }

  _teeth_bad() { # $1 = message
    echo "gate-lib --teeth: FAIL — $1" >&2
    _teeth_failures=$((_teeth_failures + 1))
  }

  _teeth_want_rc() { # $1 = label, $2 = wanted rc, $3 = actual rc
    [ "$3" -eq "$2" ] && return 0
    _teeth_bad "$1: wanted exit $2, got $3"
  }

  _teeth_want_in() { # $1 = label, $2 = needle, $3 = haystack
    printf '%s\n' "$3" | grep -Fq -- "$2" && return 0
    _teeth_bad "$1: wanted the text '$2' — got: $(printf '%s' "$3" | tr '\n' '|')"
  }

  _teeth_want_not_in() { # $1 = label, $2 = needle, $3 = haystack
    printf '%s\n' "$3" | grep -Fq -- "$2" || return 0
    _teeth_bad "$1: must NOT contain '$2' — got: $(printf '%s' "$3" | tr '\n' '|')"
  }

  # ── T1. The two MESSAGES differ, plain mode (FAIL/WARN on stderr). ──────
  # Asserted in BOTH directions: "gate_unmeasured says UNMEASURED" alone still
  # passes if gate_fail grew the same marker, and then the two verdicts are
  # indistinguishable again while the tooth stays green.
  _teeth_probe "" 'gate_fail "the corpus disagrees with the schema"'
  _fail_plain="$_T_ERR"
  _teeth_want_in   "plain gate_fail" "FAIL" "$_fail_plain"
  _teeth_want_not_in "plain gate_fail" "UNMEASURED" "$_fail_plain"
  [ -z "$_T_OUT" ] || _teeth_bad "plain gate_fail wrote to stdout, not stderr: $_T_OUT"

  _teeth_probe "" 'gate_unmeasured "the corpus disagrees with the schema"'
  _unmeasured_plain="$_T_ERR"
  _teeth_want_in     "plain gate_unmeasured" "UNMEASURED" "$_unmeasured_plain"
  _teeth_want_not_in "plain gate_unmeasured" "FAIL" "$_unmeasured_plain"
  [ -z "$_T_OUT" ] || _teeth_bad "plain gate_unmeasured wrote to stdout: $_T_OUT"

  if [ "$_fail_plain" = "$_unmeasured_plain" ]; then
    _teeth_bad "plain mode: gate_fail and gate_unmeasured produced the SAME line for the same input ('$_fail_plain') — one verdict is masquerading as the other"
  fi

  # ── T2. The two MESSAGES differ under GITHUB_ACTIONS (stdout annotations).
  # Content, not presentation: both are ::error:: because Actions has no third
  # severity, so the distinction has to survive in the message itself.
  _teeth_probe "true" 'gate_fail "the corpus disagrees with the schema"'
  _fail_ci="$_T_OUT"
  _teeth_want_in     "actions gate_fail" "::error::" "$_fail_ci"
  _teeth_want_not_in "actions gate_fail" "UNMEASURED" "$_fail_ci"
  [ -z "$_T_ERR" ] || _teeth_bad "actions gate_fail wrote to stderr: $_T_ERR"

  _teeth_probe "true" 'gate_unmeasured "the corpus disagrees with the schema"'
  _unmeasured_ci="$_T_OUT"
  _teeth_want_in "actions gate_unmeasured" "::error::" "$_unmeasured_ci"
  _teeth_want_in "actions gate_unmeasured" "UNMEASURED" "$_unmeasured_ci"
  [ -z "$_T_ERR" ] || _teeth_bad "actions gate_unmeasured wrote to stderr: $_T_ERR"

  if [ "$_fail_ci" = "$_unmeasured_ci" ]; then
    _teeth_bad "actions mode: gate_fail and gate_unmeasured produced the SAME annotation for the same input ('$_fail_ci') — one verdict is masquerading as the other"
  fi

  # ── T3. The two EXIT CODES differ. ──────────────────────────────────────
  # The constant first: if GATE_EXIT_UNMEASURED were ever set to 1 the cases
  # below would still agree with each other and prove nothing.
  _teeth_probe "" 'printf "%s\n" "$GATE_EXIT_UNMEASURED"'
  _teeth_want_rc "reading GATE_EXIT_UNMEASURED" 0 "$_T_RC"
  if [ "$_T_OUT" = "1" ] || [ "$_T_OUT" = "0" ]; then
    _teeth_bad "GATE_EXIT_UNMEASURED is $_T_OUT, which collides with $([ "$_T_OUT" = "1" ] && echo "a measured failure" || echo "green") — a caller cannot branch on it"
  fi
  _unmeasured_rc="$_T_OUT"

  _teeth_probe "" 'gate_summary probe'
  _teeth_want_rc "a clean run" 0 "$_T_RC"

  _teeth_probe "" 'gate_warn "a soft note"; gate_summary probe'
  _teeth_want_rc "warnings only" 0 "$_T_RC"

  _teeth_probe "" 'gate_fail "measured, and wrong"; gate_summary probe'
  _teeth_want_rc "a measured failure" 1 "$_T_RC"

  _teeth_probe "" 'gate_unmeasured "could not read the corpus"; gate_summary probe'
  _teeth_want_rc "an unmeasured run" "$_unmeasured_rc" "$_T_RC"
  if [ "$_T_RC" -eq 1 ] || [ "$_T_RC" -eq 0 ]; then
    _teeth_bad "an unmeasured run exited $_T_RC — indistinguishable from $([ "$_T_RC" -eq 1 ] && echo "a measured failure" || echo "green")"
  fi
  _teeth_want_in "an unmeasured summary names the count" "1 unmeasured" "$_T_ERR"

  # A MIXED run: one measured failure AND one failed measurement. The
  # incomplete half must not disappear behind the measured one — the exit code
  # says "incomplete", and both messages are still printed.
  _teeth_probe "" 'gate_fail "measured, and wrong"; gate_unmeasured "could not read the corpus"; gate_summary probe'
  _teeth_want_rc "a mixed run" "$_unmeasured_rc" "$_T_RC"
  _teeth_want_in "a mixed run still prints the measured failure" "FAIL" "$_T_ERR"
  _teeth_want_in "a mixed run still prints the refusal" "UNMEASURED" "$_T_ERR"
  _teeth_want_in "a mixed summary names both counts" "1 unmeasured, 1 error(s)" "$_T_ERR"

  # ── T4. Sourcing this lib NEVER runs this block, whatever "$1" holds. ────
  # The regression is concrete: `make _harness-check` runs `bash <gate>
  # --teeth` for every gate, and each gate sources this file at the top. A
  # guard on "$1" instead of BASH_SOURCE fires inside all of them at once.
  _sourcer="$_teeth_home/sourcing-gate.sh"
  {
    echo '#!/usr/bin/env bash'
    echo 'set -uo pipefail'
    echo "source \"$_teeth_lib\""
    echo 'echo "sourcing gate ran, and its own \$1 is: ${1:-<none>}"'
  } >"$_sourcer"
  _src_out="$(bash "$_sourcer" --teeth 2>&1)"
  _src_rc=$?
  _teeth_want_rc "a gate sourcing this lib while its own \$1 is --teeth" 0 "$_src_rc"
  _teeth_want_in "the sourcing gate reached its own code" "sourcing gate ran, and its own \$1 is: --teeth" "$_src_out"
  _teeth_want_not_in "sourcing must not run this lib's teeth" "gate-lib --teeth" "$_src_out"

  # ── T5. Running the lib directly with no --teeth is refused, not run. ───
  _direct_out="$(bash "$_teeth_lib" 2>&1)"
  _direct_rc=$?
  _teeth_want_rc "a bare direct invocation" 2 "$_direct_rc"
  _teeth_want_in "a bare direct invocation says what to do instead" "source it, do not run it" "$_direct_out"

  if [ "$_teeth_failures" -gt 0 ]; then
    echo "gate-lib --teeth: $_teeth_failures assertion(s) failed" >&2
    exit 1
  fi
  echo "gate-lib --teeth: PASS — gate_fail and gate_unmeasured differ in TEXT (both streams, plain and ::error:: modes) and in EXIT CODE (1 vs $_unmeasured_rc); a mixed run reports incomplete and still prints both lines; warnings and a clean run stay 0; sourcing this lib with --teeth in \$1 runs nothing."
  exit 0
fi
