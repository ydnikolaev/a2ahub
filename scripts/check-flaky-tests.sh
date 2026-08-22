#!/usr/bin/env bash
# check-flaky-tests.sh — run the Go suite N times and refuse a test that does
# not always agree with itself.
#
# WHY THIS EXISTS. Until 2026-08-22 this repository ran its tests exactly once
# per gate and had no mechanism of any kind for detecting non-determinism: no
# repeat run, no known-flaky list, no counter. Two timing-sensitive tests
# therefore reached a tagged release — one failing in `t.TempDir()` cleanup
# against a detached child, one betting that every loopback write finishes
# inside 25ms — and both were found by accident, after the tag, on CI.
#
# The parity machinery could not have caught either, and that is the point
# worth keeping: `ci-parity` and `ci-parity-docker` answer "is the environment
# the same?". They answered it correctly. Nobody was asking "does this test
# always pass?", and no amount of environment fidelity answers that. A test
# that fails one run in three passes a single-run gate two times in three,
# forever.
#
# NOT IN `make check`: it costs N times the suite. It belongs to the ship lane
# and to whoever is chasing a red.
#
# The lane declaration lives on the `flaky-scan` Makefile recipe, not here: one
# phase, one declaration. A copy in this header would be a second one, and the
# resolver refuses that by name.
#
# lane-reads-opaque: gate-lib is sourced through "${BASH_SOURCE[0]%/*}", the
#   builtin self-location every gate here uses; and the known-flaky list is read
#   through "$LIST" so a tooth can point the same script at a fixture list. Both
#   paths are fixed in practice — scripts/lib/gate-lib.sh and
#   scripts/lib/flaky-tests.txt — but neither is a literal the classifier can
#   resolve. The recipe declares NEVER, so no diff selects this phase anyway.
set -uo pipefail

ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
# shellcheck source=scripts/lib/gate-lib.sh
source "${BASH_SOURCE[0]%/*}/lib/gate-lib.sh"


# --- teeth ---------------------------------------------------------------
#
# Both directions, because either alone is meaningless: a scan that only reds
# on an unlisted flake passes for one that never reds, and a scan that only
# reds on a stale entry passes for one that reds on everything.
run_teeth() {
  local d out rc fail=0
  d="$(mktemp -d)"
  mkdir -p "$d/flaky"
  cat >"$d/go.mod" <<'EOF'
module flakyprobe

go 1.26
EOF
  # Fails on the SECOND iteration only — exactly the shape a single run misses.
  cat >"$d/flaky/flaky_test.go" <<'EOF'
package flaky

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSometimes(t *testing.T) {
	marker := filepath.Join(os.TempDir(), "flakyprobe.marker")
	if _, err := os.Stat(marker); err == nil {
		_ = os.Remove(marker)
		t.Fatal("second run fails")
	}
	_ = os.WriteFile(marker, []byte("x"), 0o600)
}
EOF
  rm -f "${TMPDIR:-/tmp}/flakyprobe.marker"

  # (a) an UNLISTED flake must red BY NAME.
  out="$(cd "$d" && FLAKY_PKGS=./... FLAKY_COUNT=2 ROOT="$ROOT" bash "$ROOT/scripts/check-flaky-tests.sh" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q "TestSometimes" <<<"$out"; then
    echo "check-flaky-tests --teeth: FAIL — an unlisted flake must red naming the test; rc=$rc" >&2
    printf '%s
' "$out" >&2; fail=1
  fi

  # (b) a LISTED entry that no longer flakes must ALSO red, or the list never drains.
  local tmplist="$d/list.txt"
  printf '# probe
flakyprobe/NeverFlakes
' >"$tmplist"
  rm -f "${TMPDIR:-/tmp}/flakyprobe.marker"
  cat >"$d/flaky/flaky_test.go" <<'EOF'
package flaky

import "testing"

func TestAlwaysPasses(t *testing.T) {}
EOF
  out="$(cd "$d" && FLAKY_PKGS=./... FLAKY_COUNT=2 ROOT="$d" bash "$ROOT/scripts/check-flaky-tests.sh" 2>&1)"; rc=$?
  # ROOT=$d makes the script read $d/scripts/lib/flaky-tests.txt, which is absent
  # -> UNMEASURED, which is itself the third assertion: an absent list refuses.
  if [ "$rc" -ne 3 ] || ! grep -q "UNMEASURED\|absent" <<<"$out"; then
    echo "check-flaky-tests --teeth: FAIL — an absent known-flaky list must be UNMEASURED, not a verdict; rc=$rc" >&2
    printf '%s
' "$out" >&2; fail=1
  fi

  # (c) the stale-entry refusal, with a real list this time.
  mkdir -p "$d/scripts/lib"
  printf '# probe entry that no longer flakes
flakyprobe/NeverFlakes
' >"$d/scripts/lib/flaky-tests.txt"
  out="$(cd "$d" && FLAKY_PKGS=./... FLAKY_COUNT=2 ROOT="$d" bash "$ROOT/scripts/check-flaky-tests.sh" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q "NeverFlakes" <<<"$out"; then
    echo "check-flaky-tests --teeth: FAIL — a listed entry that stopped flaking must red so the list drains; rc=$rc" >&2
    printf '%s
' "$out" >&2; fail=1
  fi

  rm -rf "$d"; rm -f "${TMPDIR:-/tmp}/flakyprobe.marker"
  if [ "$fail" -ne 0 ]; then echo "check-flaky-tests --teeth: FAIL" >&2; exit 1; fi
  echo "check-flaky-tests --teeth: an unlisted flake reds by name; an absent list is UNMEASURED; a listed entry that stopped flaking reds so the list drains."
}

if [ "${1:-}" = "--teeth" ]; then run_teeth; exit 0; fi

COUNT="${FLAKY_COUNT:-3}"
LIST="$ROOT/scripts/lib/flaky-tests.txt"
PKGS="${FLAKY_PKGS:-./...}"

# The list is REQUIRED to exist, and an unreadable one is UNMEASURED rather
# than an empty list. An exception file that silently reads as empty would turn
# every listed flake into a hard red and teach people to delete the scan.
if [ ! -f "$LIST" ]; then
  gate_unmeasured "scripts/lib/flaky-tests.txt is absent. It IS the known-flaky set this scan judges against; without it a listed flake is indistinguishable from an unlisted one, so this refuses rather than reporting a verdict it cannot support."
  gate_summary flaky-tests; exit $?
fi

listed() { # $1 = Package/TestName
  grep -vE '^[[:space:]]*(#|$)' "$LIST" | grep -qxF "$1"
}

out="$(mktemp)"
trap 'rm -f "$out"' EXIT

# -count=N re-runs every test N times INSIDE one invocation, so a test that
# disagrees with itself fails the package. Deliberately NOT N separate `go test`
# invocations: those share a warm cache and would mostly re-report the same
# cached PASS.
echo "running $PKGS ${COUNT}x — a test that does not agree with itself across $COUNT runs is the subject"
go test $PKGS -count="$COUNT" >"$out" 2>&1
rc=$?

# A build failure is not a flake and must not be reported as one.
if grep -qE '^(# |.*\[build failed\])' "$out"; then
  gate_unmeasured "the suite did not build, so nothing was sampled: $(grep -m3 -E '^# |\[build failed\]' "$out" | tr '\n' ' ')"
  gate_summary flaky-tests; exit $?
fi

failed="$(grep -E '^--- FAIL: ' "$out" | sed 's/^--- FAIL: //; s/ (.*//' | sort -u)"

unlisted=""
while IFS= read -r name; do
  [ -n "$name" ] || continue
  if listed "$name"; then
    gate_warn "$name failed within $COUNT runs and is a KNOWN flake (scripts/lib/flaky-tests.txt). It is not a new defect; it is an unpaid one."
  else
    unlisted="$unlisted $name"
  fi
done <<<"$failed"

if [ -n "$unlisted" ]; then
  gate_fail "these tests did not agree with themselves across $COUNT runs and are NOT in scripts/lib/flaky-tests.txt:$unlisted"
  echo "fix the test, or add it to the list WITH a dated reason. A single run would have shown a green here — that is the whole reason this scan exists."
  gate_summary flaky-tests; exit $?
fi

# THE LIST MUST DRAIN. An entry that stopped flaking is an exception nobody
# retired, and a stale exception is how a list becomes a place to hide things.
stale=""
while IFS= read -r entry; do
  [ -n "$entry" ] || continue
  printf '%s\n' "$failed" | grep -qxF "$entry" || stale="$stale $entry"
done < <(grep -vE '^[[:space:]]*(#|$)' "$LIST")
if [ -n "$stale" ]; then
  gate_fail "these entries in scripts/lib/flaky-tests.txt passed all $COUNT runs, so their reason has expired — delete them:$stale"
  gate_summary flaky-tests; exit $?
fi

if [ "$rc" -ne 0 ] && [ -z "$failed" ]; then
  gate_unmeasured "go test exited $rc but named no failing test, so this scan cannot say whether anything flaked."
  gate_summary flaky-tests; exit $?
fi

gate_ok "no test disagreed with itself across $COUNT runs, and every known-flaky entry still earns its place."
gate_summary flaky-tests; exit $?
