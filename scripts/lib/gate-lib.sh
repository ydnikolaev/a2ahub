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
# Parameter expansion, not `dirname`: this library is sourced by every gate,
# including ones whose own teeth run them under a MINIMAL PATH carrying no
# coreutils. `dirname` missing there made this line print an error and resolve
# GATE_ROOT to garbage — one level below the gate whose refusal then could not
# be made. Found by ci-parity-docker on 2026-08-21; the host resolved it and
# said nothing.
_gate_lib_dir="${BASH_SOURCE[0]%/*}"
[ "$_gate_lib_dir" = "${BASH_SOURCE[0]}" ] && _gate_lib_dir="."
GATE_ROOT="$(cd "$_gate_lib_dir/../.." && pwd)"

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
  # A MARKER FOR THE LAYER THAT CANNOT SEE THE EXIT CODE (release-cost-2026-08
  # P3). verify.sh's run_phase files a phase as `unmeasured` rather than `fail`
  # by testing for exit 3 — and it invokes every REPO_GATES member as
  # `make <gate>`, so GNU make has already collapsed that 3 into its own 2
  # before run_phase can read it. Measured 2026-08-23: the gate script alone
  # exits 3, the same gate through make exits 2. So the three-verdict logic was
  # DEAD for every repo gate, and the distinction verify.sh's own comment says
  # "the TELEMETRY has to carry" was not in the telemetry.
  #
  # An environment variable crosses that boundary where an exit code cannot:
  # run_phase points it at a scratch path, this appends, run_phase reads it
  # afterwards. Absent variable = no marker = every non-verify caller behaves
  # exactly as before. Best-effort by construction — a marker that could not be
  # written must never turn a gate red, which would be this function's own
  # subject in reverse.
  if [ -n "${A2A_UNMEASURED_MARKER:-}" ]; then
    printf '%s\n' "$*" >>"$A2A_UNMEASURED_MARKER" 2>/dev/null || true
  fi
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
# TWO FIELDS ADDED 2026-08-22, both because this store could not answer the one
# question that mattered: "is this gate reliable?"
#
#   "teeth"  — a --teeth fixture run is SUPPOSED to go red, and until now it was
#              written under the same gate name as a real run. check-projection
#              read 575 red / 292 green in this file while having exactly TWO
#              real runs on record, both green. The corpus was unusable for its
#              only purpose.
#   "run_id" — pairs with the `start` line below.
#
# And the deeper hole: this function is reachable ONLY from gate_summary, so a
# gate that is killed, times out or OOMs writes NOTHING. Absence of a line then
# reads as absence of a failure — indistinguishable from "never ran". A dying
# process cannot write its own epitaph, so the START line is emitted up front;
# a start with no outcome IS "began and did not survive to report".
gate_telemetry() { # <name> <verdict:green|red|warn|unmeasured|start>
  {
    mkdir -p "$GATE_ROOT/.claude/.telemetry" &&
      printf '{"ts":"%s","gate":"%s","verdict":"%s","duration_s":%d,"errors":%d,"warnings":%d,"teeth":%s,"run_id":"%s"}\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "$2" "$SECONDS" "$_GATE_ERRORS" "$_GATE_WARNINGS" \
        "$([ -n "${_GATE_TEETH_ACTIVE:-}" ] && echo true || echo false)" "${_GATE_RUN_ID:-unknown}" \
        >> "$GATE_ROOT/.claude/.telemetry/gates.jsonl"
  } 2>/dev/null || true
}

# gate_started — emit the paired START. Called by a gate that wants a killed run
# to leave a trace; the outcome line from gate_summary closes it by run_id.
gate_started() { # <name>
  _GATE_RUN_ID="${_GATE_RUN_ID:-$$-$SECONDS-$RANDOM}"
  gate_telemetry "$1" start
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

  # --unmeasured — THE ONE THING A MAKE RECIPE CAN USE.
  #
  # A recipe cannot `source` this file. GNU make runs every recipe under
  # `/bin/sh`, and this Makefile's own header states the rule it lives by:
  # "Recipes are POSIX sh — no bashisms — even though the gate scripts they
  # call are bash (invoked explicitly via `bash`, never relying on $(SHELL))."
  # Line 32 above is `${BASH_SOURCE[0]%/*}`, which dash refuses outright —
  # `/bin/sh: Bad substitution`. On macOS `/bin/sh` IS bash 3.2, so a recipe
  # that sourced this worked here and died on every Linux host: CI's runners,
  # the parity container, and therefore `make check` and `make release-check`.
  # Three recipes were written that way on 2026-08-23 and reproduced under
  # `/bin/dash` before this entry point existed.
  #
  # So the library keeps its one job and gains one PUBLIC verb instead: print
  # the UNMEASURED annotation, in whichever format the environment calls for,
  # and exit 3. A recipe reaches it the way the header prescribes — through
  # `bash`, explicitly — with no quoting gymnastics and no second spelling of
  # the token.
  if [ "${1:-}" = "--unmeasured" ]; then
    shift
    if [ "$#" -eq 0 ]; then
      echo "gate-lib.sh --unmeasured needs a message: what did NOT happen, and whose problem it is." >&2
      exit 2
    fi
    gate_unmeasured "$*"
    exit "$GATE_EXIT_UNMEASURED"
  fi

  if [ "${1:-}" != "--teeth" ]; then
    echo "gate-lib.sh is a library — source it, do not run it." >&2
    echo "The only valid direct invocations are:" >&2
    echo "  bash ${BASH_SOURCE[0]} --teeth" >&2
    echo "  bash ${BASH_SOURCE[0]} --unmeasured \"<what did not happen>\"" >&2
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
