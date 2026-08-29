#!/usr/bin/env bash
# check-discard-ceiling.sh — computed-not-listed-2026-08 P8 (spec
# 08-the-discard-ceiling.md §8): a seeded ceiling on UNDOCUMENTED
# error-discard sites, so the class AGENTS.md:120 names ("No swallowed
# errors (`_ =` on a fallible call needs a `// reason:`)") can only shrink.
#
# WHY A REGEX UNIVERSE IS SOUND HERE: .golangci.yml:18 enables `errcheck`
# (line 16 is `default: none`, line 17 is `enable:`), and separately Go's own
# assignment-arity rule requires every return position of a multi-value call
# to be given an explicit target — so a discarded return, error-typed or not,
# can ONLY ever be written as a literal `_`. There is no other spelling to
# miss.
#
# THE UNIVERSE IS THE DELIVERABLE — four candidate readings were measured
# uncapped at HEAD (`rg -n --pcre2 '<PATTERN>' --type go | grep -v
# '_test\.go' | wc -l`; `--type go` already honours .gitignore, so
# .a2a/cache/feedback-repo and web/node_modules are excluded without a flag):
#
#   U1  ^\s*_ = \S                                   statement-position single
#       discard, e.g. `_ = f()`                                    count: 108
#   U2  U1 + a NAMED multi-assign's trailing blank,   count: 208 (154 without
#       e.g. `raw, _ = f()` — NOT `_, _ = f()`        a `// reason:`)
#       (that is U3, below) and not a line whose
#       FIRST token is itself `_`
#   U3  U2 + fully-blank multi-assigns, e.g.                       count: 1091
#       `_, _ = f()` — dominated by the
#       `_, _ = fmt.Fprintf(stdio.Stderr, …)` idiom
#   U4  every standalone `_` token in non-test Go                  count: 4470
#       source — not a universe, a token count
#
# WHY U2: U1 alone is green on `cmd_validate_ci.go`'s own
# `raw, _ = os.ReadFile(...)` — a trailing-blank discard, not `^_ =` — which
# is the site that motivated this whole class; a gate scoped to the letter of
# `^_ =` is green on exactly the false-green vector it exists to catch. U3 is
# ~90% one idiom (`_, _ = fmt.Fprintf` to a Stderr-named destination
# reporting a failure already in flight) that would need a blanket exemption,
# not a per-site reason, if it is ever wanted — a distinct future universe
# (see this file's own footer), not this one. U4 is not a universe.
#
# THESE COUNTS ARE MEASURED AGAINST THIS TREE, NOT COPIED FROM A SPEC — spec
# 08 quotes 195/141 measured 2026-08-27; two sibling phases in this same wave
# are deleting code concurrently, so a re-`--write` after the wave lands is
# expected to move the number, and only ever down or up by real code change,
# never by spec citation.
#
# WHAT THIS GATE DOES NOT CATCH (US-3, stated so a green run is not
# over-read): a discard carrying a `// reason:` is excluded from the count
# UNCONDITIONALLY — this gate cannot and does not judge whether that stated
# reason is actually correct. `cmd_validate_ci.go`'s own motivating site once
# carried a five-line reason that was true for ENOENT and false for EACCES,
# EISDIR or a bad symlink; only a human reading the reason against the code
# catches that. This gate catches GROWTH of undocumented discards, nothing
# more. A `// reason:` must appear either as a trailing comment on the
# discard line itself, or anywhere in the contiguous `//` comment block
# directly above it — a reason attached only to a later line of a multi-line
# call is not detected (a stated, not a silent, limitation).
#
# Usage: bash scripts/check-discard-ceiling.sh            # verify (CI/commit gate)
#        bash scripts/check-discard-ceiling.sh --write    # regenerate the ceiling from the current tree
#        bash scripts/check-discard-ceiling.sh --teeth    # this gate's own self-test

# lane-reads-opaque: the gate-lib source is written as
# "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh" — the repo-wide idiom every
# gate here uses, and a literal the extractor cannot resolve through the
# subshell.
#
# lane-reads-opaque: discard_sites greps every file rg's own --type go walk
# yields under the given root(s) — already covered by the declaration below,
# but resolved by rg's own file-type machinery, not a literal path list.
#
# lane-inputs:
#   **/*.go
#   !**/*_test.go
#   scripts/lib/discard-ceiling.txt
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

DEFAULT_CEILING_FILE="$GATE_ROOT/scripts/lib/discard-ceiling.txt"

# U1 — statement-position single discard: `_ = f()`. Anchored so it never
# matches `_, x := range` (a comma, not ` = `, follows) or `_, _ = f()` (this
# pattern requires nothing between `_` and `=` but a single space).
U1_PATTERN='^\s*_ = \S'

# U2's additional half — a trailing blank in a NAMED multi-assign, e.g.
# `raw, _ = f()` or `a, b, _ := f()`. The negative lookahead on every
# comma-separated element forbids that element from being `_` itself, which
# is what keeps `_, _ = f()` (U3) and `for _, x := range` (out of every
# universe — `range` never precedes `:?=`) out of U2: a line only matches if
# EVERY element before the final, single trailing blank is a real name.
U2_TRAILING_PATTERN='^\s*(?!_\s*(,|:?=))[A-Za-z_][A-Za-z0-9_.]*(,\s*(?!_\s*(,|:?=))[A-Za-z_][A-Za-z0-9_.]*)*,\s*_\s*:?=[^=]'

# The full U2 universe is the two patterns' union, matched by ONE rg
# invocation via alternation — deliberately not two separate rg calls piped
# together as `{ rg1; rg2; } | ...`: this script has no `set -e` of its own,
# but a caller that runs `set -e` (as this file's own --teeth harness did,
# transiently, until it was found and removed — see teeth_expect's history)
# would abort such a group after rg1's exit 1 ("no match"), silently
# dropping rg2's contribution with no error. A single rg process cannot
# exhibit that failure mode regardless of what a caller's shell options are.
DISCARD_PATTERN="(${U1_PATTERN})|(${U2_TRAILING_PATTERN})"

# discard_sites <root>...: "file:line" for every U2 site (U1 ∪ trailing-blank)
# under the given root(s), one per line, deduplicated and sorted, non-test
# files only. `--type go` filters to *.go and already honours .gitignore.
# The test-file exclusion is anchored to the PATH field alone (`^[^:]*_test\.go:`)
# — a bare `grep -v '_test\.go'` would match the string anywhere, including a
# discard line whose CODE happens to mention a "_test.go" path
# (e.g. `_ = os.Remove("fixture_test.go")`), wrongly dropping it from the
# universe; anchoring to the path field is also what keeps `foo_testing.go`
# correctly IN (spec 08 §6's own edge case — see --teeth's AC-6).
discard_sites() {
  local roots=("$@")
  rg -n --pcre2 "$DISCARD_PATTERN" --type go "${roots[@]}" 2>/dev/null \
    | grep -v '^[^:]*_test\.go:' | sort -t: -k1,1 -k2,2n -u
}

# is_documented <file> <line>: true if the discard line itself carries a
# trailing `// reason:`, or any line in the contiguous `//`-comment block
# immediately above it does (walking up while lines are comment-only).
is_documented() {
  local file="$1" line="$2" content trimmed l prev
  content="$(sed -n "${line}p" "$file" 2>/dev/null)"
  case "$content" in *reason:*) return 0 ;; esac
  l=$((line - 1))
  while [ "$l" -ge 1 ]; do
    prev="$(sed -n "${l}p" "$file" 2>/dev/null)"
    trimmed="${prev#"${prev%%[![:space:]]*}"}"
    case "$trimmed" in
      //*)
        case "$trimmed" in *reason:*) return 0 ;; esac
        l=$((l - 1))
        ;;
      *) break ;;
    esac
  done
  return 1
}

# measure <root>...: sets MEASURE_TOTAL (U2 site count) and MEASURE_UNDOC
# (the subset with no `// reason:`, by is_documented).
measure() {
  local total=0 undoc=0 file line rest
  MEASURE_TOTAL=0
  MEASURE_UNDOC=0
  while IFS=: read -r file line rest; do
    [ -n "${file:-}" ] || continue
    total=$((total + 1))
    if ! is_documented "$file" "$line"; then
      undoc=$((undoc + 1))
    fi
  done < <(discard_sites "$@")
  MEASURE_TOTAL=$total
  MEASURE_UNDOC=$undoc
}

# ceiling_value <ceiling-file>: the last whitespace-delimited token on the
# last non-comment, non-blank line — a bare integer alone on its own line,
# same shape as scripts/lib/lane-opaque-ceiling.txt. Missing/unreadable file
# reads as ceiling 0 (an absent ceiling blocks everything, never nothing).
ceiling_value() {
  local f="$1"
  awk '$0 !~ /^#/ && NF > 0 { v = $1 } END { print v + 0 }' "$f" 2>/dev/null
}

# verify_against_ceiling <ceiling-file> <root>...: gate_fail if the measured
# undocumented count exceeds the stored ceiling; always gate_ok's one summary
# line naming both counts and this gate's own boundary (US-3).
verify_against_ceiling() {
  local ceiling_file="$1"
  shift
  local ceiling
  # An absent rg or an unreadable ceiling file must never read as "0
  # discards, all under budget" — that is the false-green class gate-lib.sh's
  # own header names as the more dangerous half of an unmeasured run
  # (check-provider-tier-deferral once reported "0 outstanding" under a
  # depth-1 clone when the real count was 14).
  command -v rg >/dev/null 2>&1 || {
    gate_unmeasured "discard-ceiling: ripgrep (rg) not found on PATH — the U2 universe could not be measured"
    return 1
  }
  [ -r "$ceiling_file" ] || {
    gate_unmeasured "discard-ceiling: ceiling file $ceiling_file is missing or unreadable"
    return 1
  }
  measure "$@"
  ceiling="$(ceiling_value "$ceiling_file")"
  if [ "$MEASURE_UNDOC" -gt "$ceiling" ]; then
    gate_fail "discard-ceiling: $MEASURE_UNDOC undocumented discard site(s) in universe U2 (statement-position '_ = f()' plus a named multi-assign's trailing blank, e.g. 'raw, _ = f()' — outside _test.go), ceiling is $ceiling in scripts/lib/discard-ceiling.txt — the class may only shrink (add a '// reason: ...' or remove the discard), never grow. Regenerate the ceiling after a real reduction with: bash scripts/check-discard-ceiling.sh --write"
    return 1
  fi
  gate_ok "discard-ceiling: $MEASURE_UNDOC/$MEASURE_TOTAL undocumented/total U2 discard site(s), at or under the $ceiling ceiling — this gate catches GROWTH of undocumented discards; it does not and cannot judge whether an existing '// reason:' is actually correct (see this script's own header)"
}

# write_ceiling: regenerate scripts/lib/discard-ceiling.txt from the CURRENT
# tree's measured undocumented U2 count. Never invoked by the verify path —
# a maintainer runs this explicitly after adding reasons or deleting
# discards, then reviews the diff (a decrement is the one honest way to bank
# a reduction, mirroring check-refusal-ratchet.sh's --write).
write_ceiling() {
  measure "$GATE_ROOT"
  {
    cat <<EOF
# scripts/lib/discard-ceiling.txt — the CEILING on undocumented U2
# error-discard sites (computed-not-listed-2026-08 P8, spec 08). U2 =
# statement-position \`_ = f()\` plus a named multi-assign's trailing blank
# (\`raw, _ = f()\`) — see scripts/check-discard-ceiling.sh's own header for
# the full universe definition and why U2 was chosen over U1/U3/U4. A site
# carrying a \`// reason:\` (same line or the contiguous comment block above
# it) is excluded unconditionally — this ceiling counts UNDOCUMENTED sites
# only, and does not and cannot judge whether a stated reason is correct.
#
# One integer, alone on its own line. It may only FALL (a site gains a
# reason, or is removed) or stay the same; a tree whose undocumented U2
# count exceeds it reds.
#
# Regenerate with:
#   bash scripts/check-discard-ceiling.sh --write
# then review the diff before committing.
EOF
    echo "$MEASURE_UNDOC"
  } >"$DEFAULT_CEILING_FILE"
}

# ── --teeth ───────────────────────────────────────────────────────────────

teeth_run() { # $1 = ceiling file, $2.. = roots; prints gate_summary's stdout/stderr, returns its exit code
  local ceiling_file="$1"
  shift
  (
    _GATE_ERRORS=0
    _GATE_WARNINGS=0
    _GATE_UNMEASURED=0
    verify_against_ceiling "$ceiling_file" "$@"
    gate_summary "check-discard-ceiling-teeth"
  ) 2>&1
}

teeth_expect() { # $1 = label, $2 = red|green, $3 = ceiling file, $4.. = roots
  # Deliberately NO `set +e`/`set -e` pair here: this script never sets `-e`
  # in the first place (only `set -uo pipefail`, at the top), so toggling
  # errexit on and off around a command-substitution capture is a no-op at
  # best — and was a live footgun at worst. A `set -e` accidentally left ON
  # by a future edit would abort the `{ rg1; rg2; } | ...`-shaped pipelines
  # this script's own comments warn about the instant the first pattern in
  # a group finds no match, and would do so SILENTLY (no error, no non-zero
  # exit visible to the caller) — this was found and fixed 2026-08-30 in
  # exactly this function.
  local label="$1" verdict="$2" ceiling_file="$3" out rc
  shift 3
  out="$(teeth_run "$ceiling_file" "$@")"
  rc=$?
  if [ "$verdict" = "red" ]; then
    if [ "$rc" -eq 0 ]; then
      echo "check-discard-ceiling --teeth: FALSE GREEN — $label did not red:" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-discard-ceiling --teeth: $label reds"
  else
    if [ "$rc" -ne 0 ]; then
      echo "check-discard-ceiling --teeth: FALSE RED — $label should green:" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-discard-ceiling --teeth: $label greens"
  fi
}

run_teeth() {
  local work
  work="$(mktemp -d)" || return 1
  trap 'rm -rf -- "$work"' RETURN

  # AC-2 (growth): a tree AT its ceiling greens; one more undocumented
  # discard, past the ceiling, reds.
  local d1="$work/growth"
  mkdir -p "$d1"
  cat >"$d1/foo.go" <<'EOF'
package fixture

import "os"

func f() {
	_ = os.Setenv("A", "1")
}
EOF
  printf '1\n' >"$d1/ceiling.txt"
  teeth_expect "AC2a: exactly at ceiling (1 undocumented site) greens" green "$d1/ceiling.txt" "$d1" || exit 1
  cat >>"$d1/foo.go" <<'EOF'

func g() {
	_ = os.Setenv("B", "2")
}
EOF
  teeth_expect "AC2b: a second undocumented discard past the ceiling reds" red "$d1/ceiling.txt" "$d1" || exit 1

  # AC-3 (reason-added): adding a `// reason:` to an EXISTING site lowers the
  # measured count WITHOUT deleting any code.
  local d2="$work/reason-added"
  mkdir -p "$d2"
  cat >"$d2/bar.go" <<'EOF'
package fixture

import "os"

func h() {
	_ = os.Setenv("C", "3")
}
EOF
  printf '1\n' >"$d2/ceiling.txt"
  measure "$d2"
  local before after
  before=$MEASURE_UNDOC
  if [ "$before" -ne 1 ]; then
    echo "check-discard-ceiling --teeth: FALSE — AC3 fixture measured $before undocumented sites before adding a reason, want 1" >&2
    exit 1
  fi
  cat >"$d2/bar.go" <<'EOF'
package fixture

import "os"

func h() {
	_ = os.Setenv("C", "3") // reason: intentionally best-effort, caller has no recourse on failure here
}
EOF
  measure "$d2"
  after=$MEASURE_UNDOC
  if [ "$after" -ne 0 ]; then
    echo "check-discard-ceiling --teeth: FALSE — AC3 fixture still measured $after undocumented sites after adding a reason, want 0 (code was not deleted, only commented)" >&2
    exit 1
  fi
  echo "check-discard-ceiling --teeth: AC3: adding a // reason: lowered the undocumented count from $before to $after with no deletion"
  teeth_expect "AC3: reason-documented site + unchanged ceiling greens" green "$d2/ceiling.txt" "$d2" || exit 1

  # AC-4 (trailing-blank): U2 matches `raw, _ = f()`, not only `_ = f()`.
  local d3="$work/trailing-blank"
  mkdir -p "$d3"
  cat >"$d3/baz.go" <<'EOF'
package fixture

import "os"

func k() {
	raw, _ = os.ReadFile("x")
}
EOF
  measure "$d3"
  if [ "$MEASURE_TOTAL" -ne 1 ] || [ "$MEASURE_UNDOC" -ne 1 ]; then
    echo "check-discard-ceiling --teeth: FALSE — AC4 fixture 'raw, _ = f()' measured total=$MEASURE_TOTAL undoc=$MEASURE_UNDOC, want total=1 undoc=1" >&2
    exit 1
  fi
  echo "check-discard-ceiling --teeth: AC4: 'raw, _ = f()' (trailing blank in a named multi-assign) is in universe U2"

  # AC-5 (out-of-universe): `for _, x := range` and `_, _ = fmt.Fprintf` must
  # never enter the universe.
  local d4="$work/out-of-universe"
  mkdir -p "$d4"
  cat >"$d4/qux.go" <<'EOF'
package fixture

import "fmt"

func m(xs []int, stdio struct{ Stderr *int }) {
	total := 0
	for _, x := range xs {
		total += x
	}
	_, _ = fmt.Fprintf(nil, "usage: a2a validate <path>\n")
	fmt.Println(total)
}
EOF
  measure "$d4"
  if [ "$MEASURE_TOTAL" -ne 0 ]; then
    echo "check-discard-ceiling --teeth: FALSE — AC5 fixture ('for _, x := range' + '_, _ = fmt.Fprintf') measured $MEASURE_TOTAL site(s) in universe U2, want 0" >&2
    exit 1
  fi
  echo "check-discard-ceiling --teeth: AC5: 'for _, x := range' and '_, _ = fmt.Fprintf' never enter universe U2, confirmed exactly 0"

  # AC-6 (scope, spec 08 §6): the test-file exclusion is anchored to the
  # PATH, not the whole "path:line:content" string. `real_test.go` (a
  # genuine test file) must be OUT; `foo_testing.go` (a file whose NAME
  # merely contains "test" but does not end `_test.go`) must be IN, even
  # though a naive `grep -v '_test\.go'` would also exclude a discard line
  # whose CODE happens to mention a "_test.go" path.
  local d5="$work/scope"
  mkdir -p "$d5"
  cat >"$d5/real_test.go" <<'EOF'
package fixture

import "os"

func t() {
	_ = os.Setenv("D", "4")
}
EOF
  cat >"$d5/foo_testing.go" <<'EOF'
package fixture

import "os"

func u() {
	_ = os.Setenv("E", "5")
}
EOF
  measure "$d5"
  if [ "$MEASURE_TOTAL" -ne 1 ]; then
    echo "check-discard-ceiling --teeth: FALSE — AC6 fixture measured $MEASURE_TOTAL site(s) across real_test.go (must be OUT) + foo_testing.go (must be IN), want exactly 1" >&2
    exit 1
  fi
  echo "check-discard-ceiling --teeth: AC6: real_test.go is out of scope, foo_testing.go is in scope, confirmed exactly 1 site"

  echo "check-discard-ceiling --teeth: PASS — AC-2 (growth reds past ceiling), AC-3 (reason-added lowers the count without deletion), AC-4 (trailing-blank is in U2), AC-5 (range/double-blank are out of U2), AC-6 (_test.go excluded by path, _testing.go included)"
}

# ── entry point ──────────────────────────────────────────────────────────

case "${1:-}" in
--teeth)
  run_teeth
  exit $?
  ;;
--write)
  write_ceiling
  echo "wrote $DEFAULT_CEILING_FILE"
  ;;
"")
  verify_against_ceiling "$DEFAULT_CEILING_FILE" "$GATE_ROOT"
  gate_summary "discard-ceiling"
  exit $?
  ;;
*)
  echo "usage: $0 [--teeth|--write]" >&2
  exit 2
  ;;
esac
