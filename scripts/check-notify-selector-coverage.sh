#!/usr/bin/env bash
# check-notify-selector-coverage.sh — P11's coverage gate
# (answers-that-hold-2026-08 spec 11, ACs 11-13).
#
# AC-11: every fold.Kind and every fold.State must be reachable by SOME
# selector, or carry a WRITTEN reason. AC-13: no kind or state list may
# exist anywhere in internal/spacenotify.
#
# The two are one gate because they are one guarantee: internal/spacenotify
# matches kind/state by RAW STRING EQUALITY against the fact already on the
# artifact (selector.go's selectorMatches) — never against an enumerated
# list. That is what makes AC-11 true BY CONSTRUCTION: a kind or state
# fold.BuildVocabulary() adds tomorrow needs zero code change here to
# become selectable. The only way to BREAK that guarantee is to reintroduce
# a hardcoded kind/state list — so this gate polices AC-13 directly (a
# structural scan of internal/spacenotify's own non-test source) and treats
# a clean scan as AC-11's own proof.
#
# The universe (kind names, state names) is DERIVED from the real binary's
# `a2a __catalog --vocabulary --json` — never hand-listed here — so a kind
# or state fold gains is scanned for immediately, with no edit to this
# file. Parsed with awk/sed/grep, not jq (jq is not a `make check`
# dependency — the check-view-vocabulary.sh precedent).
#
# AC-11's own written reason (the P5 funnel-guard note — a verb's own
# reachability is not a fold vocabulary and never becomes a selector
# dimension) lives in internal/spacenotify/coverage.go, a Go doc-comment
# file this gate greps rather than re-deriving: NOT under docs/ — line 43
# of scripts/lib/strip-set.txt removes that tree from every public
# checkout, and a gate reading a removed path is red locally and silently
# skips in public. `answers-that-hold-2026-08` (2026-08) records a worked
# example of exactly this mistake, made by a neighbouring phase of the same
# epic — which is why the rule is restated here instead of pointed at.
#
# Usage: bash scripts/check-notify-selector-coverage.sh            # check the real tree
#        bash scripts/check-notify-selector-coverage.sh --teeth    # self-test on fixtures

# lane-reads-opaque: scan_second_vocabulary reads `done <"$file"` (line ~113),
# where $file iterates "$root/internal/spacenotify"/*.go — already covered by the
# internal/spacenotify/** declaration below, but built from a variable the
# extractor cannot resolve.
#
# lane-reads-opaque: --teeth writes and reads its own captured output under a
# per-run mktemp directory ($TEETH_OUT). Those paths are scratch, never repo
# paths: nothing under them can change this gate's verdict on a real tree, so
# they are deliberately NOT lane-inputs. They were literal /tmp/ names until
# 2026-08-28, which made the classifier ask for six declarations of files that
# do not exist in the repository — and made two concurrent --teeth runs on one
# machine clobber each other's output.
#
# lane-inputs:
#   internal/spacenotify/**
#   internal/fold/**
#   scripts/check-notify-selector-coverage.sh
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# vocabulary_json asks the binary — never a literal list — for
# `a2a __catalog --vocabulary --json`. A2A_VERIFY_BINARY (the outer
# verification runner's own seam, prose-coverage.sh's own precedent) is
# preferred; a direct invocation falls back to `go run`.
vocabulary_json() {
  if [ -n "${A2A_VERIFY_BINARY:-}" ]; then
    if [ ! -x "$A2A_VERIFY_BINARY" ]; then
      echo "notify-selector-coverage: A2A_VERIFY_BINARY is not executable: $A2A_VERIFY_BINARY" >&2
      return 1
    fi
    "$A2A_VERIFY_BINARY" __catalog --vocabulary --json
    return
  fi
  ( cd "$ROOT" && GOWORK=off go run ./cmd/a2a __catalog --vocabulary --json )
}

# kind_names prints every fold.Kind name, one per line — the `.states`
# object's own top-level keys, matching Go's `json.Encoder` with
# `SetIndent("", "  ")`: a kind key is indented by exactly 4 spaces and
# opens its own array.
kind_names() { # $1 = vocabulary json
  printf '%s\n' "$1" | awk '
    /^  "states": \{$/ { insec = 1; next }
    insec && /^  \},?$/ { insec = 0 }
    insec && /^    "[A-Za-z0-9_]+": \[$/ {
      line = $0
      sub(/^    "/, "", line)
      sub(/": \[$/, "", line)
      print line
    }
  '
}

# state_names prints every DISTINCT fold.State name across every kind, one
# per line — the union of every `.states.<kind>[]` array entry (6-space
# indent, inside the 4-space kind block).
state_names() { # $1 = vocabulary json
  printf '%s\n' "$1" | awk '
    /^  "states": \{$/ { insec = 1; next }
    insec && /^  \},?$/ { insec = 0 }
    insec { print }
  ' | grep -E '^      "' | sed -E 's/^      "//; s/",?$//' | sort -u
}

# scan_second_vocabulary is AC-13's own teeth: any NON-TEST .go file under
# $1/internal/spacenotify carrying TWO OR MORE of $2 (a `|`-joined kind/
# state name alternation) as quoted string literals on the SAME line is a
# hand-maintained vocabulary list — the exact defect this phase exists to
# remove. Scoped to non-test files: a table test naming several kinds on
# one line (this package's own selector_test.go) is normal test fixture
# data, not a second vocabulary the render path reads.
scan_second_vocabulary() { # $1 = root, $2 = pattern
  local root="$1" pattern="$2" dir file lineno line count found=0
  dir="$root/internal/spacenotify"
  [ -d "$dir" ] || { echo "notify-selector-coverage: $dir does not exist" >&2; return 2; }
  for file in "$dir"/*.go; do
    [ -f "$file" ] || continue
    case "$file" in *_test.go) continue ;; esac
    lineno=0
    while IFS= read -r line; do
      lineno=$((lineno + 1))
      count=$(printf '%s\n' "$line" | grep -oE "\"($pattern)\"" | wc -l | tr -d ' ')
      if [ "$count" -ge 2 ]; then
        echo "notify-selector-coverage: FAIL — $file:$lineno carries $count fold.Kind/fold.State literals on one line, a second vocabulary AC-13 forbids:" >&2
        echo "  $line" >&2
        found=1
      fi
    done <"$file"
  done
  return $found
}

# check_written_reason is AC-11's own half: internal/spacenotify/coverage.go
# must exist and carry the `coverage-reason:` marker naming the ONE
# documented exception (the P5 funnel-guard verb-vs-state distinction).
check_written_reason() { # $1 = root
  local f="$1/internal/spacenotify/coverage.go"
  if [ ! -f "$f" ]; then
    echo "notify-selector-coverage: FAIL — $f is missing; AC-11's written reason has nowhere to live" >&2
    return 1
  fi
  if ! grep -q 'coverage-reason:' "$f"; then
    echo "notify-selector-coverage: FAIL — $f carries no 'coverage-reason:' marker" >&2
    return 1
  fi
  if ! grep -q 'TSubmit' "$f"; then
    echo "notify-selector-coverage: FAIL — $f's coverage-reason does not name the P5 funnel-guard row (TSubmit)" >&2
    return 1
  fi
  return 0
}

# run_check is the whole gate over root ($1) — the derived universe always
# comes from the REAL binary (GATE_ROOT), matching prose-coverage.sh's own
# "the universe is real even while the tree under test is not" precedent
# for --teeth.
run_check() { # $1 = root
  local root="$1" json kinds states pattern rc=0

  if ! json="$(vocabulary_json)"; then
    echo "notify-selector-coverage: could not read the vocabulary from the binary — failing closed rather than policing nothing" >&2
    return 1
  fi
  kinds="$(kind_names "$json")"
  states="$(state_names "$json")"
  if [ -z "$kinds" ] || [ -z "$states" ]; then
    echo "notify-selector-coverage: derived kind/state universe is empty — failing closed" >&2
    return 1
  fi
  local kind_count state_count
  kind_count=$(printf '%s\n' "$kinds" | grep -c .)
  state_count=$(printf '%s\n' "$states" | grep -c .)
  echo "notify-selector-coverage: derived universe — $kind_count kinds, $state_count states (from a2a __catalog --vocabulary --json)"

  pattern="$(printf '%s\n%s\n' "$kinds" "$states" | grep -v '^$' | paste -sd'|' -)"

  if ! scan_second_vocabulary "$root" "$pattern"; then
    rc=1
  fi
  if ! check_written_reason "$root"; then
    rc=1
  fi

  if [ "$rc" -eq 0 ]; then
    echo "notify-selector-coverage: PASS — no second kind/state vocabulary in internal/spacenotify; AC-11's written reason is present"
  fi
  return $rc
}

run_teeth() {
  local tmp rc=0 TEETH_OUT
  TEETH_OUT="$(mktemp -d)"

  # (a) a fixture spacenotify dir carrying a hand-maintained kind list, and
  # a coverage.go copied verbatim from the real tree (so ONLY the second-
  # vocabulary half is under test here) — must RED, naming the file.
  tmp="$(mktemp -d)"
  mkdir -p "$tmp/internal/spacenotify"
  cp "$ROOT/internal/spacenotify/coverage.go" "$tmp/internal/spacenotify/coverage.go"
  cat >"$tmp/internal/spacenotify/badlist.go" <<'EOF'
package spacenotify

var legacyKinds = []string{"contract", "requirement", "question"}
EOF
  if run_check "$tmp" >"$TEETH_OUT/a.out" 2>&1; then
    echo "notify-selector-coverage --teeth: FAILED (a) — a hardcoded kind list stayed green" >&2
    cat "$TEETH_OUT/a.out" >&2
    rc=1
  elif ! grep -q "badlist.go" "$TEETH_OUT/a.out"; then
    echo "notify-selector-coverage --teeth: FAILED (a) — reds, but does not name badlist.go" >&2
    cat "$TEETH_OUT/a.out" >&2
    rc=1
  fi
  rm -rf "$tmp"

  # (b) a fixture spacenotify dir with NO coverage.go at all — AC-11's own
  # written-reason half must RED.
  tmp="$(mktemp -d)"
  mkdir -p "$tmp/internal/spacenotify"
  cat >"$tmp/internal/spacenotify/clean.go" <<'EOF'
package spacenotify

func kindMatches(kind string, want []string) bool {
	for _, w := range want {
		if w == kind {
			return true
		}
	}
	return false
}
EOF
  if run_check "$tmp" >"$TEETH_OUT/b.out" 2>&1; then
    echo "notify-selector-coverage --teeth: FAILED (b) — a missing written-reason file stayed green" >&2
    cat "$TEETH_OUT/b.out" >&2
    rc=1
  elif ! grep -q "coverage.go is missing" "$TEETH_OUT/b.out"; then
    echo "notify-selector-coverage --teeth: FAILED (b) — reds, but does not name the missing file" >&2
    cat "$TEETH_OUT/b.out" >&2
    rc=1
  fi
  rm -rf "$tmp"

  # (c) positive control: the REAL tree passes.
  if ! run_check "$ROOT" >"$TEETH_OUT/c.out" 2>&1; then
    echo "notify-selector-coverage --teeth: FAILED (c) — the real tree itself reds" >&2
    cat "$TEETH_OUT/c.out" >&2
    rc=1
  fi

  rm -rf "$TEETH_OUT"
  if [ "$rc" -eq 0 ]; then
    echo "notify-selector-coverage --teeth: ok"
  fi
  return $rc
}

case "${1:-check}" in
  check) run_check "$ROOT" ;;
  --teeth) run_teeth ;;
  *)
    echo "usage: $0 [check|--teeth]" >&2
    exit 2
    ;;
esac
