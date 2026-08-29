#!/usr/bin/env bash
# verify.sh — outer boundary for the a2ahub validation graph.
#
# Owns one checkout-scoped accelerator cache, one built CLI artifact and the
# phase telemetry for every validation entrypoint, including harness and live.
# Nested Go commands inherit this environment; none allocates or purges a peer
# cache. GOMODCACHE is deliberately untouched: modules are shared inputs.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
VERIFY_ROOT="$ROOT/.a2a/cache/verify"
MARKER="$VERIFY_ROOT/.a2ahub-verify-cache-v1"
MAX_CACHE_KIB=$((2 * 1024 * 1024))
TELEMETRY_LIMIT=2000

# ONE VOCABULARY FOR "I COULD NOT MEASURE THIS", not a second spelling.
#
# This runner referenced `GATE_EXIT_UNMEASURED` with a `:-3` default and never
# had the function that produces it, so the one place it could itself fail to
# measure — a configured lint gate with no linter — said FAIL instead. Every
# other gate in this repository routes through gate-lib, the teeth assert on
# its literal token, and inventing a second wording here is what spec 03 §5
# forbids by name.
#
# Safe to source: the file's own `BASH_SOURCE == $0` guard keeps its teeth from
# running, it defines no name this script already uses, and its only top-level
# effects are zeroing three counters and choosing colours.
# shellcheck source=lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

MODE="${1:-full}"
SCOPED_PACKAGES=()
SCOPED_TEST_COUNT="${A2A_VERIFY_TEST_COUNT:-1}"
if [ "$MODE" = "test" ]; then
  shift
  SCOPED_PACKAGES=("$@")
fi

now_ms() {
  perl -MTime::HiRes=time -e 'printf "%.0f\n", time() * 1000'
}

fail() {
  echo "verify: FAIL — $*" >&2
  return 1
}

expected_marker() {
  printf 'a2ahub-verify-cache-v1\nrepo=%s\n' "$ROOT"
}

prepare_cache_root() {
  local path canonical existing
  for path in "$ROOT/.a2a" "$ROOT/.a2a/cache" "$VERIFY_ROOT"; do
    if [ -L "$path" ]; then
      fail "$path is a symlink; remove it manually before verification (cache ownership must not escape the checkout)"
      return 1
    fi
  done

  if [ -d "$VERIFY_ROOT" ] && [ ! -f "$MARKER" ]; then
    existing="$(find "$VERIFY_ROOT" -mindepth 1 -maxdepth 1 -print -quit)"
    if [ -n "$existing" ]; then
      fail "$VERIFY_ROOT is non-empty but has no ownership marker; retained untouched for inspection"
      return 1
    fi
  fi

  mkdir -p "$VERIFY_ROOT"
  canonical="$(cd "$VERIFY_ROOT" && pwd -P)"
  if [ "$canonical" != "$VERIFY_ROOT" ]; then
    fail "cache root resolved to $canonical, expected exact project-owned path $VERIFY_ROOT"
    return 1
  fi

  if [ -f "$MARKER" ]; then
    if [ "$(cat "$MARKER")" != "$(expected_marker)" ]; then
      fail "$MARKER does not identify this checkout; retained untouched for inspection"
      return 1
    fi
  else
    expected_marker >"$MARKER"
  fi

  mkdir -p "$VERIFY_ROOT/go-build" "$VERIFY_ROOT/golangci-lint" "$VERIFY_ROOT/bin"
  configure_go_cache
  export GOLANGCI_LINT_CACHE="$VERIFY_ROOT/golangci-lint"
  export GOWORK=off
  # Verification consumes the already-resolved module graph and never reaches
  # the network. CI downloads modules in an explicit setup step; a fresh local
  # checkout does the same once with `go mod download`.
  export GOPROXY=off
  configure_verify_binary
}

configure_verify_binary() {
  if [ "$MODE" = test ]; then
    # Scoped tests do not run build_cli first. Leaving this variable pointed
    # at a prior full gate lets internal/e2e/TestMain execute stale product
    # code while reporting the current source green. Unset it so TestMain's
    # existing fallback builds exactly the tree being tested.
    unset A2A_VERIFY_BINARY
    return 0
  fi
  export A2A_VERIFY_BINARY="$VERIFY_ROOT/bin/a2a"
}

configure_go_cache() {
  local expected workspace_root cache_root
  if [ "${GITHUB_ACTIONS:-}" != "true" ]; then
    export GOCACHE="$VERIFY_ROOT/go-build"
    return 0
  fi
  if [ -z "${GITHUB_WORKSPACE:-}" ]; then
    fail "GITHUB_ACTIONS=true but GITHUB_WORKSPACE is empty"
    return 1
  fi
  expected="$GITHUB_WORKSPACE/.a2a/cache/ci-go-build"
  if [ "${GOCACHE:-}" != "$expected" ]; then
    fail "GitHub GOCACHE must equal setup-go's project path $expected (got ${GOCACHE:-<empty>})"
    return 1
  fi
  if [ -L "$GITHUB_WORKSPACE" ] || [ -L "$expected" ]; then
    fail "GitHub cache path may not be a symlink ($expected)"
    return 1
  fi
  mkdir -p "$GITHUB_WORKSPACE" "$expected"
  workspace_root="$(cd "$GITHUB_WORKSPACE" && pwd -P)"
  cache_root="$(cd "$expected" && pwd -P)"
  case "$cache_root" in
    "$workspace_root"/*) ;;
    *)
      fail "GitHub cache resolved outside GITHUB_WORKSPACE: $cache_root"
      return 1
      ;;
  esac
  export GOCACHE="$cache_root"
  echo "verify: GOCACHE=$GOCACHE (owned by actions/setup-go for this ephemeral runner)"
}

# THE RECORD GAINS A TREE IDENTITY (release-cost-2026-08 P2). Every reader
# tolerates its absence by construction — internal/lane/telemetry.go unmarshals
# JSON into a struct, so an older line written by an older verify.sh simply
# leaves the field empty, and the ring buffer below still holds thousands of
# them. `""` when the identity could not be computed, which is honest and is
# never equal to a real one, so the guard below cannot fire on it.
append_telemetry() {
  local gate="$1" verdict="$2" duration_ms="$3" at
  at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf '{"gate":"%s","verdict":"%s","duration_ms":%s,"mode":"%s","at":"%s","tree":"%s"}\n' \
    "$gate" "$verdict" "$duration_ms" "$MODE" "$at" "${RUN_TREE:-}" >>"$VERIFY_ROOT/telemetry.jsonl"
}

# working_tree_delta prints every path this working tree has changed against
# HEAD — staged, unstaged and untracked — one per line.
#
# ONE DEFINITION, TWO QUESTIONS. `lane_files` asks it to decide WHICH GATES a
# diff can reach; `run_tree_identity` asks it to decide WHETHER THIS TREE HAS
# ALREADY BEEN JUDGED. Both need exactly the same set and neither may drift
# from the other, so the porcelain parsing — including the rename case below,
# which cost a real bug to get right — lives here and nowhere else.
#
# `--porcelain=v1 -z` emits a RENAME/COPY as two NUL-terminated tokens:
# "XY <newpath>\0<origpath>\0" — and the ORIGINAL path carries NO "XY "
# prefix. A blanket `sed 's/^...//'` over every token therefore eats three
# characters off it (verified: `git mv original.txt renamed.txt` yielded
# "ginal.txt"), and the deriver then either refuses a path that does not
# exist or, worse, matches a mangled fragment against some unrelated glob.
# Both halves of a rename are real inputs — the old path stops existing and
# the new one starts — so both are reported.
working_tree_delta() {
  git -C "$ROOT" status --porcelain=v1 -z --untracked-files=all |
    while IFS= read -r -d '' record; do
      printf '%s\n' "${record:3}"
      case "${record:0:2}" in
        [RC]*|?[RC])
          IFS= read -r -d '' original || break
          printf '%s\n' "$original"
          ;;
      esac
    done
}

# run_tree_identity — a COMPLETE identity for the tree this run is judging.
#
# HEAD's sha, plus every changed path with the git hash of its CONTENT, plus
# the mode and any explicit LANE_FILES. That is complete rather than
# approximate, and the completeness is the whole point: a tracked file absent
# from `git status` is byte-identical to HEAD by definition, so HEAD plus the
# delta's contents determines the tree exactly.
#
# THIS IS NOT THE CACHE THE EPIC REFUSED, and the difference is not a matter
# of degree. That cache would have keyed on DECLARED inputs — where 63% of
# declarations self-admit reads they cannot name — and then SKIPPED a phase,
# reporting a stored verdict as the current one. A wrong key there is a false
# green. This asks git what the tree is, never a declaration; and it can only
# REFUSE TO DISPATCH, never report a verdict it did not measure. A wrong
# answer here costs one `VERIFY_AGAIN=1`.
run_tree_identity() {
  # `hashes` initialised: `set -u` is on, and an EMPTY delta — a clean tree,
  # which is exactly what a ship gate judges — otherwise leaves it unset and
  # the identity cannot be computed for the one tree that matters most.
  local head paths hashes=""
  head="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null)" || return 1
  paths="$(working_tree_delta | LC_ALL=C sort -u)" || return 1
  if [ -n "$paths" ]; then
    # One `git hash-object` for the whole set, not one per file. A path that is
    # not a readable regular file (a deletion, a directory) hashes to nothing
    # and is reported by its status line alone — deterministic either way,
    # which is all an identity needs.
    hashes="$(printf '%s\n' "$paths" |
      while IFS= read -r p; do
        if [ -f "$ROOT/$p" ]; then
          printf '%s %s\n' "$(git -C "$ROOT" hash-object -- "$p" 2>/dev/null || echo unreadable)" "$p"
        else
          printf 'absent %s\n' "$p"
        fi
      done)"
  fi
  printf 'head=%s\nmode=%s\nlane_files=%s\n%s\n' \
    "$head" "$MODE" "${LANE_FILES:-}" "$hashes" |
    git -C "$ROOT" hash-object --stdin 2>/dev/null
}

run_phase() {
  local gate="$1" start end rc verdict marker
  shift
  marker="$VERIFY_ROOT/.unmeasured.$$"
  rm -f "$marker"
  # EXPORTED, not a `VAR=v cmd` prefix: several phases are shell FUNCTIONS in
  # this file, and bash keeps a prefix assignment set after a function call
  # rather than restoring it. Explicit export/unset behaves the same way for a
  # function, a `make` child and an external script alike.
  export A2A_UNMEASURED_MARKER="$marker"
  start="$(now_ms)"
  if "$@"; then
    rc=0
  else
    rc=$?
  fi
  end="$(now_ms)"
  unset A2A_UNMEASURED_MARKER
  # THREE VERDICTS, NOT TWO. gate-lib spends exit 3 for "I could not measure
  # this" precisely so it is distinguishable from "I measured it and it is
  # wrong" — and this runner used to record BOTH as `fail`, which erased the
  # distinction at the only place anything reads it back. Every gate_unmeasured
  # inside `make check` was being filed as a measured failure.
  #
  # NB the caller still cannot see 3 through `make`: GNU Make turns a recipe's
  # exit 3 into its own exit 2. That is a separate, real defect and it is why
  # the TELEMETRY has to carry the distinction — the exit code cannot.
  verdict=pass
  # THE EXIT CODE IS NOT ENOUGH, AND THIS USED TO BE THE WHOLE TEST.
  #
  # Every REPO_GATES member is dispatched below as `make <gate>`, so make has
  # already turned the gate's exit 3 into its own exit 2 by the time it reaches
  # here — measured 2026-08-23: check-operational-confidence.sh alone exits 3,
  # `make operational-confidence-guard` exits 2. So this branch NEVER fired for
  # a repo gate and every one of them was filed as `fail`. The comment above
  # says the telemetry has to carry the distinction because the exit code
  # cannot; it was not carrying it, for the same reason, one layer up.
  #
  # The marker closes it: gate-lib appends to $A2A_UNMEASURED_MARKER whenever
  # gate_unmeasured fires, and an environment variable crosses the make
  # boundary that an exit code does not. A phase that is not a gate-lib gate
  # writes nothing and behaves exactly as before.
  if [ "$rc" -eq "${GATE_EXIT_UNMEASURED:-3}" ]; then
    verdict=unmeasured
  elif [ "$rc" -ne 0 ] && [ -s "$marker" ]; then
    verdict=unmeasured
  elif [ "$rc" -ne 0 ]; then
    verdict=fail
  fi
  rm -f "$marker"
  append_telemetry "$gate" "$verdict" "$((end - start))"
  return "$rc"
}

trim_telemetry() {
  local telemetry="$VERIFY_ROOT/telemetry.jsonl" tmp lines
  [ -f "$telemetry" ] || return 0
  lines="$(wc -l <"$telemetry" | tr -d ' ')"
  [ "$lines" -le "$TELEMETRY_LIMIT" ] && return 0
  tmp="$VERIFY_ROOT/.telemetry.trim.$$"
  tail -n "$TELEMETRY_LIMIT" "$telemetry" >"$tmp"
  mv "$tmp" "$telemetry"
}

bound_accelerators() {
  local size_after size_before
  size_before="$(du -sk "$VERIFY_ROOT" | awk '{print $1}')"
  [ "$size_before" -le "$MAX_CACHE_KIB" ] && return 0

  echo "verify: accelerator cache is ${size_before} KiB (cap ${MAX_CACHE_KIB} KiB); using tool-native purge."
  GOCACHE="$VERIFY_ROOT/go-build" go clean -cache
  if command -v golangci-lint >/dev/null 2>&1; then
    GOLANGCI_LINT_CACHE="$VERIFY_ROOT/golangci-lint" golangci-lint cache clean
  fi

  size_after="$(du -sk "$VERIFY_ROOT" | awk '{print $1}')"
  if [ "$size_after" -gt "$MAX_CACHE_KIB" ]; then
    fail "$VERIFY_ROOT remains ${size_after} KiB after tool-native purge; unknown residue retained, inspect it manually"
    return 1
  fi
  echo "verify: accelerator cache trimmed to ${size_after} KiB."
}

# ── THE REPETITION GUARD (release-cost-2026-08 P2) ───────────────────────────
#
# MEASURED MOTIVATION, not a hunch. On 2026-08-22 `logic-e2e` ran ELEVEN times
# for 2 h 08 min, median 10.8 min, inside a total lane wall time of 4 h 37 min
# — while `.claude/rules/check-convention.md` already said, in prose, to
# iterate with the scoped test and run the lane once at a coherent step. The
# rule existed and was ignored for a whole day, which is what a rule with no
# mechanism does.
#
# THE QUESTION IT ASKS IS DELIBERATELY THE ANSWERABLE ONE: *have I already run
# THIS command over THIS EXACT TREE, and did it pass?* Not "are the declared
# inputs unchanged" — that is the mechanism the epic REFUSED, with evidence,
# because declarations here are known-incomplete. This asks git.
#
# AND IT CANNOT TURN A RED VERDICT GREEN, by construction. It has exactly two
# outcomes: refuse to dispatch, or dispatch unchanged. It never skips a phase,
# never reports a stored verdict, and never touches a running gate's result.
# The worst it can do is refuse a run somebody wanted, which costs one
# VERIFY_AGAIN=1 — and the escape says out loud that it was used, so a
# transcript still shows what happened.
#
# CLEANUP STORY: THERE IS NO NEW STATE. The guard reads the telemetry stream
# that already exists, already has a bounded size (TELEMETRY_LIMIT above), and
# is already trimmed on every exit by trim_telemetry. A record ages out of the
# ring buffer and the guard goes quiet on its own. Every state file in this
# repository has a cleanup story; this one's is "it is not a new file".
REPEAT_GUARD_MODES="full validators coverage harness logic-e2e lane-run projection"

# cheaper_than names the command that answers the same question for less, per
# mode. A refusal that only scolds gets bypassed; one that hands over the
# right tool gets used.
cheaper_than() { # $1 = mode
  case "$1" in
    full)       echo "make lane-run — it judges what THIS tree's edits can actually reach, and refuses a path no gate claims rather than falling to the ceiling" ;;
    validators) echo "make lane-run — the derivation selects only the repo gates this diff can actually reach" ;;
    lane-run)   echo "the scoped test for the package you touched: make test PKG=./internal/<pkg>/..." ;;
    logic-e2e)  echo "ONE path instead of the catalogue: go test -tags=livee2e ./internal/livee2e/ -run 'TestLogicMatrix/<row>'" ;;
    projection) echo "bash scripts/check-projection.sh --template-only — the sub-second half, which rehearses the publisher's template projection alone" ;;
    harness)    echo "the one gate's own --teeth: bash scripts/check-<gate>.sh --teeth" ;;
    *)          echo "the scoped test or gate for the thing you actually changed" ;;
  esac
}

# make_target_for — the command an operator would actually retype. A refusal
# that names a target `make` does not have teaches the reader to distrust the
# rest of the message; `verify.sh validators` is reached as `make
# check-validators`, and the mode name is not the target name for four of the
# seven guarded modes.
make_target_for() { # $1 = mode
  case "$1" in
    full)       echo "check" ;;
    validators) echo "check-validators" ;;
    harness)    echo "harness-check" ;;
    *)          echo "$1" ;;
  esac
}

# _telemetry_field pulls one string field out of a telemetry line. Deliberately
# not jq: this runner has no jq dependency and must work on a bare machine.
_telemetry_field() { # $1 = line, $2 = field
  printf '%s' "$1" | sed -n "s/.*\"$2\":\"\([^\"]*\)\".*/\1/p"
}

# todays_bill — how many times this mode has already run today (UTC) and what
# it cost, in minutes. The sentence nobody had on 2026-08-22.
todays_bill() { # $1 = mode -> "N runs, M min" or empty
  local today runs ms
  today="$(date -u +%Y-%m-%d)"
  [ -f "$VERIFY_ROOT/telemetry.jsonl" ] || return 0
  runs="$(grep -F "\"gate\":\"run:$1\"" "$VERIFY_ROOT/telemetry.jsonl" 2>/dev/null | grep -F "\"at\":\"$today" || true)"
  [ -n "$runs" ] || return 0
  ms="$(printf '%s\n' "$runs" | sed -n 's/.*"duration_ms":\([0-9]*\).*/\1/p' | awk '{t+=$1} END {print t+0}')"
  printf '%s run(s) today costing %s min' "$(printf '%s\n' "$runs" | wc -l | tr -d ' ')" "$((ms / 60000))"
}

repeat_guard() {
  case " $REPEAT_GUARD_MODES " in *" $MODE "*) ;; *) return 0 ;; esac

  # A RUNNER IS NOT AN ITERATING HUMAN, and a gate that refuses to run is the
  # last thing CI should ever do. On a hosted runner the cache is empty anyway,
  # so this is belt and braces — but `ci-parity-docker` reuses a NAMED VOLUME
  # between runs, so a second ship gate over an identical clean tree WOULD have
  # met this guard inside the container and reported `make check` as exit 1.
  # That is a composed gate blocked by an ergonomics feature, which is not a
  # trade this guard is allowed to make. `ci-parity.sh` exports VERIFY_AGAIN=1
  # for the same reason, one layer up, so every composition is covered whether
  # or not it happens to run under Actions.
  if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
    return 0
  fi
  RUN_TREE="$(run_tree_identity 2>/dev/null || true)"
  [ -n "$RUN_TREE" ] || return 0

  local bill last verdict tree when
  bill="$(todays_bill "$MODE")"

  if [ "${VERIFY_AGAIN:-}" = "1" ]; then
    echo "verify: VERIFY_AGAIN=1 — the repetition guard was BYPASSED for \`$MODE\`${bill:+ ($bill)}. Running anyway."
    return 0
  fi

  last="$(grep -F "\"gate\":\"run:$MODE\"" "$VERIFY_ROOT/telemetry.jsonl" 2>/dev/null | tail -1 || true)"
  [ -n "$last" ] || { [ -n "$bill" ] && echo "verify: \`$MODE\` — $bill."; return 0; }
  verdict="$(_telemetry_field "$last" verdict)"
  tree="$(_telemetry_field "$last" tree)"
  when="$(_telemetry_field "$last" at)"

  # ONLY AFTER A PASS. A re-run following a FAILURE is somebody reading the
  # output again, which is a legitimate thing to want and costs the operator
  # nothing to be wrong about.
  if [ "$verdict" = "pass" ] && [ -n "$tree" ] && [ "$tree" = "$RUN_TREE" ]; then
    echo "verify: REFUSING to re-run \`$MODE\` — this exact tree already passed it at $when." >&2
    echo "        Not 'the same files': the SAME BYTES. HEAD is unchanged and every path git reports as" >&2
    echo "        modified hashes identically to that run, so there is nothing new for $MODE to judge." >&2
    [ -n "$bill" ] && echo "        Already spent: $bill." >&2
    echo "        Iterate with: $(cheaper_than "$MODE")" >&2
    echo "        Run it anyway with: VERIFY_AGAIN=1 make $(make_target_for "$MODE")   (it will say that you did)" >&2
    # NOTHING RAN, SO NOTHING MAY BE RECORDED. `trap finish EXIT` is installed
    # before this function is called, so this `exit` fires finish(), which sees
    # RUN_TREE and RUN_STARTED_MS both set and would append `run:<mode> fail`
    # for a run that never happened. The next invocation would then read that
    # `fail`, find the `verdict = pass` clause false, and DISPATCH — with no
    # VERIFY_AGAIN=1 and no BYPASSED line. A straight retry would slip through
    # every other time, silently, which is precisely what the escape exists to
    # make visible. It would also count each refusal into todays_bill as an
    # expensive run. Found by review, reproduced by tooth P2h, which is the
    # only one here that goes through the trap rather than around it.
    REPEAT_REFUSED=1
    exit 1
  fi
  [ -n "$bill" ] && echo "verify: \`$MODE\` — $bill. Iterate with: $(cheaper_than "$MODE")"
  return 0
}

finish() {
  local command_status=$? maintenance_status=0
  set +e
  # THE RUN RECORD, written before the trim so it ages out with everything
  # else. It is what repeat_guard reads next time, and it is the ONLY new
  # thing P2 writes anywhere.
  #
  # REPEAT_REFUSED is the guard saying "I refused; nothing ran". Without that
  # third condition the refusal's own `exit 1` lands here and records a failed
  # run, which the next invocation reads as "the last attempt was not a pass"
  # and lets through — the guard defeating itself on alternate attempts. Tooth
  # P2h holds it, by running the whole trap sequence three times.
  if [ -n "${RUN_TREE:-}" ] && [ -n "${RUN_STARTED_MS:-}" ] && [ -z "${REPEAT_REFUSED:-}" ]; then
    if [ "$command_status" -eq 0 ]; then
      append_telemetry "run:$MODE" pass "$(( $(now_ms) - RUN_STARTED_MS ))"
    else
      append_telemetry "run:$MODE" fail "$(( $(now_ms) - RUN_STARTED_MS ))"
    fi
  fi
  trim_telemetry
  maintenance_status=$?
  if [ "$maintenance_status" -eq 0 ]; then
    bound_accelerators
    maintenance_status=$?
  fi
  set -e
  trap - EXIT
  if [ "$command_status" -ne 0 ]; then
    exit "$command_status"
  fi
  exit "$maintenance_status"
}

check_gofmt() {
  local unformatted
  unformatted="$(gofmt -l .)"
  if [ -n "$unformatted" ]; then
    echo "check: gofmt -l found unformatted file(s):" >&2
    printf '%s\n' "$unformatted" >&2
    return 1
  fi
}

check_lint() {
  if [ ! -f .golangci.yml ]; then
    return 0
  fi
  if ! command -v golangci-lint >/dev/null 2>&1; then
    # A configured lint gate that silently skips is a hole, not a gate — and
    # one that says FAIL is a different hole: it claims the code was linted and
    # found wanting, when nothing was linted at all. UNMEASURED is both loud and
    # true, and gate_summary already makes it dominate a mixed run
    # (release-cost-2026-08 P3).
    gate_unmeasured "check: .golangci.yml exists but golangci-lint is not on PATH, so THE CODE WAS NOT LINTED. This is not a clean lint and not a finding. Install it, or remove .golangci.yml if the gate is meant to be gone."
    return "$GATE_EXIT_UNMEASURED"
  fi
  golangci-lint run ./...
}

run_repo_gates() {
  local gate gates
  gates="$(make --no-print-directory -s _print-repo-gates)"
  for gate in $gates; do
    run_phase "$gate" make --no-print-directory "$gate"
  done
  echo "check-validators: repo gates green ($gates). No tests ran."
}

harness_stamp() {
  # The version this shared binary claims to be, read from the product rather
  # than repeated here.
  #
  # It used to be the literal 0.1.0, and that number silently disabled a whole
  # generation of tests. internal/e2e execs THIS binary whenever
  # A2A_VERIFY_BINARY is exported — which is every verify.sh mode except
  # MODE=test — and every verb authoring event/v2 (`contract activate`,
  # `verify --verdict`, `close --verdict`) is refused when the binary is older
  # than the space's floor (CC-085). A binary stamped 0.1.0 against a floor of
  # 0.19.0 cannot run any of them, so the tier stayed green by never reaching
  # them: agent-exchange-2026-08 B33, and the reason three defects that tier
  # was built to catch survived a green ceiling.
  #
  # internal/e2e/main_test.go's own fallback build derives its stamp from
  # contract.ContractPublicationFloor, which is a plain alias of
  # version.OperationalConfidenceFloor (publication_plan.go:34). Reading the
  # aliased constant here is reading the SAME value, so the two builds cannot
  # drift into stamping different binaries for the same test run.
  # Read from the SOURCE FILE, not from `go doc`.
  #
  # `go doc` asks the toolchain, and the toolchain needs a resolvable module
  # graph — which is exactly what a fresh CI checkout does not reliably have
  # before anything has been built. It returned nothing on GitHub Actions for
  # both the private repo and the v0.19.10 candidate branch, on a tree where
  # the identical query succeeds locally, and the whole downstream failure
  # (`no match for a2a 0.19.0` in TestT3Scripts) was that empty answer wearing
  # a disguise. The constant is a plain literal in one file; reading the file
  # needs no module graph, no cache, and no network, and it cannot disagree
  # with what the compiler will see.
  local floor_file="$ROOT/internal/version/operationalconfidence.go"
  local stamp
  stamp="$(sed -n 's/^const OperationalConfidenceFloor = "\(.*\)"$/\1/p' "$floor_file" 2>/dev/null | head -1)"
  if [ -z "$stamp" ]; then
    echo "verify.sh: cannot read OperationalConfidenceFloor from $floor_file — refusing to guess the harness stamp." >&2
    echo "           A wrong stamp does not fail loudly; it makes event/v2 verbs unreachable and the tier green (B33)." >&2
    return 1
  fi
  printf '%s' "$stamp"
}

build_cli() {
  # This is a synthetic test artifact with an explicit version. VCS stamping
  # adds no information, and Go 1.26's stamp resolver writes a revision stat
  # entry to the shared GOMODCACHE. Disabling it at the owning build command
  # keeps that shared input read-only inside agent sandboxes.
  # The stamp is resolved into a VARIABLE first, and checked, because
  # harness_stamp's own refusal used to be unreachable: called inside
  # `$(...)`, its `exit 1` killed only the subshell and the build carried on
  # with an EMPTY version stamp. The guard printed its warning and the run
  # continued, failing ten minutes later in tests that never mention versions.
  # A guard that cannot stop the thing it guards is decoration.
  local stamp
  if ! stamp="$(harness_stamp)"; then
    echo "verify.sh: refusing to build the shared CLI without a harness stamp." >&2
    exit 1
  fi
  go build -buildvcs=false -ldflags "-X main.version=$stamp" -o "$A2A_VERIFY_BINARY" ./cmd/a2a
}

run_go_tests() {
  go test ./... -race -covermode=atomic -coverprofile=coverage.out -count=1
}

run_scoped_tests() {
  local args=(-race "-count=$SCOPED_TEST_COUNT")
  if [ -n "${A2A_VERIFY_TEST_RUN:-}" ]; then
    args+=(-run "$A2A_VERIFY_TEST_RUN")
  fi
  go test "${SCOPED_PACKAGES[@]}" "${args[@]}"
}

validate_scoped_packages() {
  local pkg
  case "$SCOPED_TEST_COUNT" in
    ''|*[!0-9]*|0)
      fail "A2A_VERIFY_TEST_COUNT must be a positive integer (got $SCOPED_TEST_COUNT)"
      return 1
      ;;
  esac
  if [ "${#SCOPED_PACKAGES[@]}" -eq 0 ]; then
    fail "scoped test mode requires at least one ./package pattern"
    return 1
  fi
  for pkg in "${SCOPED_PACKAGES[@]}"; do
    case "$pkg" in
      ./*) ;;
      *)
        fail "scoped test package must start with ./ (got $pkg)"
        return 1
        ;;
    esac
  done
}

run_live_tests() {
  if [ ! -f go.mod ]; then
    echo "live-e2e: no go.mod — nothing to run." >&2
    return 2
  fi
  # Must stay above liveRunCeiling (runner_live_test.go) — the run's own
  # deadline has to expire first so the report still renders. Guarded by
  # TestLiveTimeoutCoversTheRunCeiling.
  go test ./internal/livee2e/... -tags=livee2e -count=1 -v -timeout 210m
}

run_logic_tests() {
  if [ ! -f go.mod ]; then
    echo "logic-e2e: no go.mod — nothing to run." >&2
    return 2
  fi
  # spec 09 §5a / plan D-5: TestLogicMatrix and its three siblings never call
  # LoadConfig and never read an A2A_LIVE_E2E_* variable — they stand a space
  # up on a local bare git repo and an in-process host stand-in, so unlike
  # TestLiveMatrix they need no credentials, no network and no candidate.
  # That is why this lane runs INSIDE `full` rather than being fenced away
  # from it like `live-e2e`. -run is scoped to exactly these four names —
  # widening it is how `full` would end up running the live matrix instead.
  #
  # -race, unlike the live lane. run_live_tests omits it for two reasons this
  # lane has neither of: a multi-hour instrumented run is genuinely expensive,
  # and that tier is network-bound anyway. Here the harness drives an
  # in-process HTTP host whose handlers run concurrently with the test, and
  # the harness's own scenario code uses goroutines — so this is exactly the
  # surface -race exists for, at a cost of seconds. `check` promises a raced
  # Go suite; a lane inside it that quietly opted out would be the promise
  # narrowing without anyone deciding to narrow it.
  #
  # -v and the timeout are BOTH load-bearing, and neither is a style choice.
  # `go test` discards a passing package's stdout unless -v is set (verified
  # directly), and spec 09 D-7's LOGIC_TIER_ROWS_SHA256 marker reaches the
  # release-evidence contract only by being IN this transcript. Without -v the
  # marker is emitted and thrown away, and the release gate reds with no
  # explanation an operator could act on.
  #
  # The timeout is sized from the measured full matrix (~5m for 30 judged
  # rows, each spawning real `a2a` subprocesses over real git), with room for
  # a slower machine. It is a CEILING against a hang, not a budget: if this
  # lane ever legitimately approaches it, the answer is to make the tier
  # faster, not to raise the number again.
  go test ./internal/livee2e/... -tags=livee2e -race -v -run '^(TestLogicMatrix|TestLogicTierWritesNothingOutsideItsOwnTempDirs|TestNewLogicHarnessLeavesExecutionCandidateZero|TestProvisionLocalSpaceScaffoldsCleanSpace)$' -count=1 -timeout 20m
}

# phase_is_dispatchable — can run_derived_phase actually execute this name?
#
# The explicit cases first, then whether the Makefile DEFINES a target of that
# name. The question is "does a recipe exist", and the answer is the target
# definition — NOT `.PHONY` membership, which is orthogonal bookkeeping about
# file-vs-task and says nothing about whether `make <name>` runs.
#
# The first draft asked `.PHONY`, and this refusal — the one that exists to stop
# a silent drop — became a FALSE REFUSAL on its first real lane, against
# `runner-economics`. Three REPO_GATES targets are outside `.PHONY` today
# (dashboard-props, card-content, runner-economics), all of them perfectly
# runnable. A guard that reads a proxy for the fact instead of the fact is the
# same defect one layer up.
phase_is_dispatchable() {
  case "$1" in
    build-cli|gofmt|vet|golangci-lint|go-test|coverage-policy|logic-e2e|harness-teeth|live-e2e|go-test-scoped:*) return 0 ;;
  esac
  grep -qE "^$1:" "$ROOT/Makefile"
}

run_teeth() {
  local tmp="$1" fixture_root foreign out rc
  mkdir -p "$tmp/project/scripts"
  fixture_root="$(cd "$tmp/project" && pwd -P)"
  cp "$0" "$fixture_root/scripts/verify.sh"

  # The helper functions are tested in this process against a runner-created
  # fixture; production never accepts an environment-selected cleanup root.
  foreign="$tmp/foreign"
  mkdir -p "$foreign"
  mkdir -p "$fixture_root/.a2a/cache"
  ln -s "$foreign" "$fixture_root/.a2a/cache/verify"
  set +e
  out="$(ROOT="$fixture_root" VERIFY_ROOT="$fixture_root/.a2a/cache/verify" MARKER="$fixture_root/.a2a/cache/verify/.a2ahub-verify-cache-v1" prepare_cache_root 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -eq 0 ] || ! grep -q "is a symlink" <<<"$out"; then
    echo "verify --teeth: FAIL — symlinked cache root was not refused:" >&2
    echo "$out" >&2
    return 1
  fi
  if [ -n "$(find "$foreign" -mindepth 1 -print -quit)" ]; then
    echo "verify --teeth: FAIL — refusing the symlink modified its target." >&2
    return 1
  fi

  rm "$fixture_root/.a2a/cache/verify"
  mkdir -p "$fixture_root/.a2a/cache/verify"
  printf 'foreign\n' >"$fixture_root/.a2a/cache/verify/unknown"
  set +e
  out="$(ROOT="$fixture_root" VERIFY_ROOT="$fixture_root/.a2a/cache/verify" MARKER="$fixture_root/.a2a/cache/verify/.a2ahub-verify-cache-v1" prepare_cache_root 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -eq 0 ] || ! grep -q "non-empty but has no ownership marker" <<<"$out"; then
    echo "verify --teeth: FAIL — unowned non-empty root was not refused:" >&2
    echo "$out" >&2
    return 1
  fi
  if [ ! -f "$fixture_root/.a2a/cache/verify/unknown" ]; then
    echo "verify --teeth: FAIL — unowned residue was deleted." >&2
    return 1
  fi

  rm "$fixture_root/.a2a/cache/verify/unknown"
  if ! out="$(ROOT="$fixture_root" VERIFY_ROOT="$fixture_root/.a2a/cache/verify" MARKER="$fixture_root/.a2a/cache/verify/.a2ahub-verify-cache-v1" prepare_cache_root 2>&1)"; then
    echo "verify --teeth: FAIL — an empty exact project root was not accepted:" >&2
    echo "$out" >&2
    return 1
  fi
  if [ ! -f "$fixture_root/.a2a/cache/verify/.a2ahub-verify-cache-v1" ]; then
    echo "verify --teeth: FAIL — accepted cache root has no ownership marker." >&2
    return 1
  fi

  SCOPED_PACKAGES=()
  if validate_scoped_packages >/dev/null 2>&1; then
    echo "verify --teeth: FAIL — empty scoped package set was accepted." >&2
    return 1
  fi
  SCOPED_PACKAGES=(-run)
  if validate_scoped_packages >/dev/null 2>&1; then
    echo "verify --teeth: FAIL — flag-shaped scoped package was accepted." >&2
    return 1
  fi
  SCOPED_PACKAGES=(./internal/cache/...)
  if ! validate_scoped_packages; then
    echo "verify --teeth: FAIL — valid scoped package was refused." >&2
    return 1
  fi

  MODE=test
  A2A_VERIFY_BINARY="$tmp/stale-a2a"
  configure_verify_binary
  if [ -n "${A2A_VERIFY_BINARY+x}" ]; then
    echo "verify --teeth: FAIL — scoped test mode retained a potentially stale shared binary." >&2
    return 1
  fi
  MODE=teeth
  configure_verify_binary
  if [ "${A2A_VERIFY_BINARY:-}" != "$VERIFY_ROOT/bin/a2a" ]; then
    echo "verify --teeth: FAIL — full verification lost its owned shared binary path." >&2
    return 1
  fi

  mkdir -p "$tmp/workspace"
  if ! out="$(
    GITHUB_ACTIONS=true \
      GITHUB_WORKSPACE="$tmp/workspace" \
      GOCACHE="$tmp/workspace/.a2a/cache/ci-go-build" \
      VERIFY_ROOT="$fixture_root/.a2a/cache/verify" \
      configure_go_cache 2>&1
  )"; then
    echo "verify --teeth: FAIL — exact setup-go cache path was refused:" >&2
    echo "$out" >&2
    return 1
  fi
  set +e
  out="$(
    GITHUB_ACTIONS=true \
      GITHUB_WORKSPACE="$tmp/workspace" \
      GOCACHE="$tmp/outside" \
      VERIFY_ROOT="$fixture_root/.a2a/cache/verify" \
      configure_go_cache 2>&1
  )"
  rc=$?
  set -e
  if [ "$rc" -eq 0 ] || ! grep -q "must equal setup-go's project path" <<<"$out"; then
    echo "verify --teeth: FAIL — an unrelated GitHub GOCACHE was accepted:" >&2
    echo "$out" >&2
    return 1
  fi

  # Exit fidelity: a red command stays red and its telemetry records fail.
  VERIFY_ROOT="$tmp/phase"
  MODE=teeth
  mkdir -p "$VERIFY_ROOT"
  set +e
  run_phase deliberate-red false
  rc=$?
  set -e
  if [ "$rc" -eq 0 ] || ! grep -q '"gate":"deliberate-red","verdict":"fail"' "$VERIFY_ROOT/telemetry.jsonl"; then
    echo "verify --teeth: FAIL — phase exit status/telemetry fidelity is broken." >&2
    return 1
  fi

  # Lane strict mode: an empty input set under --require-nonempty is a
  # refusal (exit 1, named), never the silent exit 0 that let a CI job
  # report green having run zero gates. `lane` (print), not `lane-run`, for
  # both cases here — a non-empty `lane-run` would actually execute the
  # derived phases (repo gates, harness-teeth, the matrix), which this
  # self-test must not do. Runs the real, on-disk script against the real
  # project ROOT (not a fixture): the empty-set branch returns before
  # touching anything beyond the owned cache root it already prepares for
  # every mode, and the non-empty branch is exactly what `make lane` does.
  set +e
  out="$(LANE_FILES=" " bash "$ROOT/scripts/verify.sh" lane --require-nonempty 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -eq 0 ] || ! grep -q "REFUSED" <<<"$out"; then
    echo "verify --teeth: FAIL — empty lane input under --require-nonempty was not refused:" >&2
    echo "$out" >&2
    return 1
  fi

  set +e
  out="$(LANE_FILES="internal/lane/lanecheck.go" bash "$ROOT/scripts/verify.sh" lane --require-nonempty 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ]; then
    echo "verify --teeth: FAIL — a non-empty lane input under --require-nonempty was refused:" >&2
    echo "$out" >&2
    return 1
  fi

  set +e
  out="$(bash "$ROOT/scripts/verify.sh" lane --require-nonempy 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 2 ] || ! grep -q "^usage:" <<<"$out"; then
    echo "verify --teeth: FAIL — a misspelled lane flag was not refused by usage:" >&2
    echo "$out" >&2
    return 1
  fi

  set +e
  out="$(LANE_FILES="scripts/ci-parity.sh" bash "$ROOT/scripts/verify.sh" lane --plan 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 2 ] || ! grep -q -- "--plan is a lane-run flag" <<<"$out"; then
    echo "verify --teeth: FAIL — \`lane --plan\` must be refused by the mode that cannot honour it, not accepted and ignored:" >&2
    echo "$out" >&2
    return 1
  fi

  set +e
  out="$(LANE_FILES=" " bash "$ROOT/scripts/verify.sh" lane 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ] || ! grep -q "nothing changed against HEAD" <<<"$out"; then
    echo "verify --teeth: FAIL — the interactive default (no strict flag) stopped exiting 0 on an empty set:" >&2
    echo "$out" >&2
    return 1
  fi

  # ── THE DERIVED SET AND THE EXECUTED SET MUST BE THE SAME SET. ──
  #
  # Until 2026-08-21 `lane-run` filtered the derived phases through a
  # hand-written roster and printed its own truncated count as the lane's
  # size: `make lane` derived 12, the runner executed 10, and the green line
  # said "10 derived phase(s) green". `projection` was one of the two it threw
  # away, so a gate declared on nearly every commit had been executed by a lane
  # exactly zero times since it was written.
  #
  # Three cases, and the third is what keeps the first from being a blanket
  # refusal that would red every real lane. The fixture is a COPY of the
  # declaration corpus with one undispatchable phase appended, reached through
  # LANE_ROOT — the derivation reads the fixture, the runner stays real, and
  # the refusal fires during classification. `--plan` keeps it honest AND
  # safe: without it, a blunted refusal would let this self-test execute the
  # derived lane, which selects `harness-teeth` — the self-test running itself.
  local lanefix lane_out lane_rc=0
  lanefix="$(mktemp -d "${TMPDIR:-/tmp}/verify-lane-teeth.XXXXXX")"
  cp "$ROOT/Makefile" "$lanefix/"
  cp -R "$ROOT/scripts" "$lanefix/scripts"
  ( cd "$ROOT" && find internal -name doc.go -print0 ) | while IFS= read -r -d '' f; do
    mkdir -p "$lanefix/$(dirname "$f")" && cp "$ROOT/$f" "$lanefix/$f"
  done

  # (c) FIRST, and deliberately: the untouched copy must derive and classify
  # cleanly. A refusal tooth that never sees a green run cannot tell "refuses
  # the undispatchable" from "refuses everything".
  set +e
  lane_out="$(LANE_ROOT="$lanefix" LANE_FILES="scripts/ci-parity.sh" bash "$ROOT/scripts/verify.sh" lane --require-nonempty 2>&1)"
  lane_rc=$?
  set -e
  if [ "$lane_rc" -ne 0 ]; then
    echo "verify --teeth: FAIL — the unmodified lane fixture did not derive cleanly, so the refusal case below proves nothing:" >&2
    echo "$lane_out" >&2
    rm -rf "$lanefix"
    return 1
  fi

  # (a) an undispatchable derived phase is REFUSED BY NAME, not dropped.
  printf '%s\n' '' \
    'if [ "$MODE" = teeth-undispatchable ]; then' \
    '  # lane-inputs:' \
    '  #   scripts/ci-parity.sh' \
    '  run_phase teeth-no-such-target true' \
    '  exit 0' \
    'fi' >> "$lanefix/scripts/verify.sh"
  set +e
  lane_out="$(LANE_ROOT="$lanefix" LANE_FILES="scripts/ci-parity.sh" bash "$ROOT/scripts/verify.sh" lane-run --plan 2>&1)"
  lane_rc=$?
  set -e
  if [ "$lane_rc" -eq 0 ] || ! grep -q "teeth-no-such-target" <<<"$lane_out" || ! grep -q "REFUSED" <<<"$lane_out"; then
    echo "verify --teeth: FAIL — a derived phase this runner cannot execute must be refused BY NAME, never dropped:" >&2
    echo "$lane_out" >&2
    rm -rf "$lanefix"
    return 1
  fi
  rm -rf "$lanefix"

  # (a2) EVERY PHASE THE CORPUS CAN PRODUCE IS DISPATCHABLE. The refusal above
  # only helps if it fires on genuinely undispatchable names; a predicate that
  # reads a PROXY for "a recipe exists" turns it into a refusal of working
  # gates. The first draft asked `.PHONY` membership, and three REPO_GATES
  # targets are outside `.PHONY` — so the very first real lane after the
  # refusal shipped refused `runner-economics`, which runs perfectly well.
  #
  # A one-off enumeration missed it because the enumeration used the same
  # proxy. This asserts the invariant against the REAL predicate instead.
  local undispatchable="" g
  for g in $(make --no-print-directory -s -C "$ROOT" _print-repo-gates); do
    phase_is_dispatchable "$g" || undispatchable="$undispatchable $g"
  done
  if [ -n "$undispatchable" ]; then
    echo "verify --teeth: FAIL — these repo gates are derivable but phase_is_dispatchable says the runner cannot execute them, so a real lane would REFUSE them:$undispatchable" >&2
    return 1
  fi
  if phase_is_dispatchable "teeth-no-such-target-anywhere"; then
    echo "verify --teeth: FAIL — phase_is_dispatchable accepts a name no target defines; the refusal it guards can then never fire." >&2
    return 1
  fi

  # (b) the ship tier is real and non-vacuous: `projection` is DERIVED by an
  # ordinary Go edit (so it is not excluded) AND named as ship-tier (so the
  # commit lane will defer rather than run it). Both halves, or "deferred"
  # cannot be told apart from "never selected".
  local shiplist alllist
  shiplist="$(printf '%s\n' internal/cache/store.go | (cd "$ROOT" && go run internal/lane/lanecheck.go --ship-phases) 2>&1)"
  alllist="$(printf '%s\n' internal/cache/store.go | (cd "$ROOT" && go run internal/lane/lanecheck.go --phases) 2>&1)"
  if ! grep -qx "projection" <<<"$shiplist" || ! grep -qx "projection" <<<"$alllist"; then
    echo "verify --teeth: FAIL — projection must be BOTH derived and ship-tier; derived=[$(grep -cx projection <<<"$alllist")] ship=[$(grep -cx projection <<<"$shiplist")]" >&2
    return 1
  fi

  # ── gate-lib's ANNOTATION MODE, which every other tooth is now blind to. ──
  #
  # `_harness-check` pins GITHUB_ACTIONS empty for the whole teeth block (see
  # the Makefile comment), because a self-test asserts what a gate SAYS and the
  # annotation format is presentation owned by CI. That fix has a cost: with
  # the format pinned, nothing else exercises the annotation path at all. This
  # is the one place that does.
  #
  # Both halves matter and the second is the one that bit: under CI the
  # emitters write to STDOUT, in plain mode to STDERR. A tooth capturing only
  # one stream sees an empty string rather than a wrong string.
  #
  # The `.` that loads gate-lib is deliberately kept INSIDE the command string
  # rather than on a line of its own: classify-guard check 5 resolves top-level
  # `source`/`.` lines to literal paths and refuses what it cannot resolve —
  # correctly, and it refused an earlier draft of this tooth. gate-lib is
  # public (PUBLIC_VALIDATOR_FILES), so no boundary is being dodged, only an
  # unresolvable line avoided.
  local gatelib ann probe
  gatelib="$ROOT/scripts/lib/gate-lib.sh"
  probe='GATE_ROOT="$(mktemp -d)"; export GATE_ROOT; . "$1"; gate_fail "boom"; gate_warn "hmm"'
  ann="$(env GITHUB_ACTIONS=true bash -c "$probe" _ "$gatelib" 2>/dev/null)"
  if ! grep -q '^::error::boom$' <<<"$ann" || ! grep -q '^::warning::hmm$' <<<"$ann"; then
    echo "verify --teeth: FAIL — GITHUB_ACTIONS=true must put ::error::/::warning:: on STDOUT, got:" >&2
    printf '%s\n' "$ann" >&2
    return 1
  fi
  ann="$(env -u GITHUB_ACTIONS bash -c "$probe" _ "$gatelib" 2>&1 >/dev/null)"
  if ! grep -q 'FAIL.*boom' <<<"$ann" || ! grep -q 'WARN.*hmm' <<<"$ann"; then
    echo "verify --teeth: FAIL — plain mode must put FAIL/WARN on STDERR, got:" >&2
    printf '%s\n' "$ann" >&2
    return 1
  fi

  # ── UNMEASURED SURVIVES THE MAKE BOUNDARY (release-cost-2026-08 P3) ───────
  #
  # THE DEFECT THIS PINS WAS LIVE AND SILENT. `run_phase` files a phase as
  # `unmeasured` rather than `fail` by testing for exit 3 — and it dispatches
  # every REPO_GATES member as `make <gate>`, so GNU make had already collapsed
  # that 3 into its own 2. Measured 2026-08-23:
  # `bash scripts/check-operational-confidence.sh` with no ripgrep exits 3;
  # `make operational-confidence-guard` on the same PATH exits 2. So the branch
  # never fired for a repo gate, and the distinction this file's own comment
  # says "the TELEMETRY has to carry" was not in the telemetry.
  #
  # Both halves are asserted, because the first alone would pass against a
  # run_phase that filed EVERYTHING as unmeasured.
  local mkt mkgate mkout
  mkt="$(mktemp -d)"
  mkdir -p "$mkt/verify"

  # (a) a phase that could not measure, reached through `make`, files
  #     `unmeasured` even though make reports 2.
  # WRITTEN WITH printf, NOT A HEREDOC, and the reason is a gate two doors
  # down. classify-guard scans every PUBLIC script for a line whose FIRST
  # token is `source` or `.` and refuses one it cannot resolve to a literal
  # path — correctly, because a public gate that reads a stripped file dies on
  # the candidate's first `make check`. A heredoc body is invisible to that:
  # the sourcing line sat at column zero in THIS file and read as a dependency
  # of the runner, which it is not. The runner does not source that path; it
  # WRITES a fixture that does. printf makes the text stop claiming otherwise,
  # rather than teaching the guard to parse heredocs.
  printf '#!/usr/bin/env bash\n. "%s"\ngate_unmeasured "the fixture tool is absent, so nothing was scanned"\nexit "$GATE_EXIT_UNMEASURED"\n' "$ROOT/scripts/lib/gate-lib.sh" >"$mkt/gate.sh"
  printf 'fixture:\n\t@bash %s/gate.sh\n' "$mkt" >"$mkt/Makefile"
  ( VERIFY_ROOT="$mkt/verify" MODE=teeth
    run_phase fixture-unmeasured make --no-print-directory -C "$mkt" fixture >/dev/null 2>&1 ) || true
  mkout="$(grep -F '"gate":"fixture-unmeasured"' "$mkt/verify/telemetry.jsonl" 2>/dev/null | tail -1)"
  if ! grep -q '"verdict":"unmeasured"' <<<"${mkout:-}"; then
    echo "verify --teeth: FAIL — a gate that could not measure, reached through make, must be filed as unmeasured; got: ${mkout:-<no record>}" >&2
    rm -rf "$mkt"; return 1
  fi

  # (b) AND A REAL FAILURE THROUGH THE SAME BOUNDARY IS STILL `fail`. Without
  #     this, (a) passes against a runner that stopped telling them apart in
  #     the other direction — which is the same defect, mirrored.
  printf '#!/usr/bin/env bash\n. "%s"\ngate_fail "the fixture measured something and it is wrong"\nexit 1\n' "$ROOT/scripts/lib/gate-lib.sh" >"$mkt/gate.sh"
  ( VERIFY_ROOT="$mkt/verify" MODE=teeth
    run_phase fixture-failed make --no-print-directory -C "$mkt" fixture >/dev/null 2>&1 ) || true
  mkout="$(grep -F '"gate":"fixture-failed"' "$mkt/verify/telemetry.jsonl" 2>/dev/null | tail -1)"
  if ! grep -q '"verdict":"fail"' <<<"${mkout:-}"; then
    echo "verify --teeth: FAIL — a gate that measured and refused must stay \`fail\`, or the two verdicts have merged the other way; got: ${mkout:-<no record>}" >&2
    rm -rf "$mkt"; return 1
  fi
  rm -rf "$mkt"
  echo "verify --teeth: UNMEASURED survives the make boundary — a repo gate that could not measure files as unmeasured while make reports its own 2, and a gate that measured and refused still files as fail."

  # ── THE REPETITION GUARD (release-cost-2026-08 P2) ────────────────────────
  #
  # Exercised through a FIXTURE REPOSITORY with its own telemetry file, never
  # against this checkout: a guard whose only observable behaviour is refusing
  # would otherwise be watched by breaking the tree it guards.
  local rg rgt rgout rgrc
  rg="$(mktemp -d)"
  git -C "$rg" init -q -b main
  git -C "$rg" config user.email teeth@example.invalid
  git -C "$rg" config user.name "repeat guard teeth"
  printf 'one\n' >"$rg/a.txt"
  # `.a2a/` IS GITIGNORED IN THE REAL REPOSITORY, and the fixture has to say so
  # or it is not a fixture of anything. Without this line the telemetry file
  # this guard writes shows up as an untracked path in the fixture's own delta,
  # so the act of RECORDING a run changes the tree identity and no run can ever
  # match its own record — the guard silently never fires, which is precisely
  # the failure the fixture exists to detect.
  printf '.a2a/\n' >"$rg/.gitignore"
  git -C "$rg" add a.txt .gitignore
  git -C "$rg" commit -q -m "seed"
  rgt="$rg/.a2a/cache/verify"
  mkdir -p "$rgt"

  # THE GUARD REFUSES BY CALLING `exit`, so its status has to be read off the
  # command substitution itself — a trailing `echo rc=$?` INSIDE the subshell
  # never runs on the refusing path, which is how this harness silently ate the
  # first green run of these teeth.
  # AND IT CONTROLS ITS OWN INPUTS. Both of the guard's early exits are read
  # from the ENVIRONMENT, so a tooth that inherits the caller's would be
  # vacuously green in exactly the two places it matters most: a lane invoked
  # as `VERIFY_AGAIN=1 make lane-run` (which is how a developer runs it while
  # iterating, and how this tooth was first seen to fail), and CI, where
  # GITHUB_ACTIONS=true makes repeat_guard return before it decides anything.
  # `make harness-check` runs on the runner, so without these two assignments
  # the whole P2 set would have passed there without testing a single thing.
  _rg() { # sets rgout and rgrc, with the guard's two escapes explicitly OFF
    rgrc=0
    rgout="$( set +e; VERIFY_AGAIN="" GITHUB_ACTIONS="" repeat_guard 2>&1 )" || rgrc=$?
  }

  # The guard is a function in THIS file, so exercise it in-process with the
  # fixture's variables swapped in and restored — cheaper and more faithful
  # than re-entering verify.sh, which would run real phases.
  local save_root="$ROOT" save_vroot="$VERIFY_ROOT" save_mode="$MODE" save_tree="${RUN_TREE:-}"
  ROOT="$rg"; VERIFY_ROOT="$rgt"; MODE="full"

  # T-P2a — AN EMPTY STREAM CANNOT REFUSE. This is also AC-5's "inert once
  # cleared": the guard's whole state is the telemetry ring buffer, so a
  # trimmed-away record leaves exactly this situation.
  : >"$rgt/telemetry.jsonl"
  _rg
  if [ "$rgrc" -ne 0 ]; then
    echo "verify --teeth: FAIL — P2a: an empty telemetry stream must not refuse anything: $rgout" >&2
    ROOT="$save_root"; VERIFY_ROOT="$save_vroot"; MODE="$save_mode"; RUN_TREE="$save_tree"; rm -rf "$rg"; return 1
  fi

  # T-P2b — A PASS OVER THIS EXACT TREE REFUSES, names a cheaper command, and
  # names the escape.
  #
  # THE TREE IS MADE DIRTY FIRST, and that is not incidental. P2c below has to
  # edit a file that is ALREADY in the delta — otherwise it changes the delta's
  # MEMBERSHIP, which even a file-name key notices, and the tooth passes
  # against a guard keyed the wrong way. It did exactly that until a mutation
  # (hash-object replaced by the bare path) left P2c green.
  printf 'two\n' >>"$rg/a.txt"
  RUN_TREE=""
  RUN_TREE="$(run_tree_identity)"
  append_telemetry "run:full" pass 900000
  _rg
  if [ "$rgrc" -eq 0 ] \
    || ! grep -q 'REFUSING to re-run' <<<"$rgout" \
    || ! grep -q 'VERIFY_AGAIN=1' <<<"$rgout" \
    || ! grep -q 'make lane-run' <<<"$rgout"; then
    echo "verify --teeth: FAIL — P2b: a pass over the identical tree must refuse, naming a cheaper command and the escape: $rgout" >&2
    ROOT="$save_root"; VERIFY_ROOT="$save_vroot"; MODE="$save_mode"; RUN_TREE="$save_tree"; rm -rf "$rg"; return 1
  fi

  # T-P2c — AND A CHANGED TREE DOES NOT. Without this the guard is a rate
  # limiter wearing a repetition guard's name.
  #
  # The edit is to the CONTENT of a file ALREADY IN THE DELTA — the same path
  # set before and after — which is the single case a file-NAME key gets wrong,
  # and it is the commonest iteration there is: fix the bug in the file you
  # were already editing, run again. A guard that refused here would be
  # refusing the loop it exists to serve.
  printf 'three\n' >>"$rg/a.txt"
  RUN_TREE=""
  _rg
  if [ "$rgrc" -ne 0 ] || grep -q 'REFUSING' <<<"$rgout"; then
    echo "verify --teeth: FAIL — P2c: editing a file already in the delta must clear the guard; a name-keyed guard would still refuse: $rgout" >&2
    ROOT="$save_root"; VERIFY_ROOT="$save_vroot"; MODE="$save_mode"; RUN_TREE="$save_tree"; rm -rf "$rg"; return 1
  fi

  # T-P2d — THE ESCAPE WORKS AND SAYS THAT IT WAS USED. A bypass nobody can
  # see in a transcript is how "it passed" stops meaning anything.
  git -C "$rg" checkout -q -- a.txt
  RUN_TREE=""
  rgrc=0; rgout="$( set +e; VERIFY_AGAIN=1 GITHUB_ACTIONS="" repeat_guard 2>&1 )" || rgrc=$?
  if [ "$rgrc" -ne 0 ] || ! grep -q 'BYPASSED' <<<"$rgout"; then
    echo "verify --teeth: FAIL — P2d: VERIFY_AGAIN=1 must run anyway AND announce it: $rgout" >&2
    ROOT="$save_root"; VERIFY_ROOT="$save_vroot"; MODE="$save_mode"; RUN_TREE="$save_tree"; rm -rf "$rg"; return 1
  fi

  # T-P2e — A FAILING PREVIOUS RUN NEVER REFUSES. Re-running after a red is
  # somebody reading the output again, and being wrong about that costs the
  # operator a gate they wanted.
  : >"$rgt/telemetry.jsonl"
  RUN_TREE=""
  RUN_TREE="$(run_tree_identity)"
  append_telemetry "run:full" fail 900000
  _rg
  if [ "$rgrc" -ne 0 ] || grep -q 'REFUSING' <<<"$rgout"; then
    echo "verify --teeth: FAIL — P2e: a previous FAILING run must never block a re-run: $rgout" >&2
    ROOT="$save_root"; VERIFY_ROOT="$save_vroot"; MODE="$save_mode"; RUN_TREE="$save_tree"; rm -rf "$rg"; return 1
  fi

  # T-P2f — A RUNNER IS NEVER REFUSED. CI must not be blocked by an
  # ergonomics feature, and ci-parity-docker's named volume makes this
  # reachable rather than theoretical.
  : >"$rgt/telemetry.jsonl"
  RUN_TREE=""
  RUN_TREE="$(run_tree_identity)"
  append_telemetry "run:full" pass 900000
  rgrc=0; rgout="$( set +e; VERIFY_AGAIN="" GITHUB_ACTIONS=true repeat_guard 2>&1 )" || rgrc=$?
  if [ "$rgrc" -ne 0 ] || grep -q 'REFUSING' <<<"$rgout"; then
    echo "verify --teeth: FAIL — P2f: GITHUB_ACTIONS=true must never meet this guard: $rgout" >&2
    ROOT="$save_root"; VERIFY_ROOT="$save_vroot"; MODE="$save_mode"; RUN_TREE="$save_tree"; rm -rf "$rg"; return 1
  fi

  # T-P2g — THE GUARD NEVER TOUCHES A VERDICT. It has two outcomes and this
  # reads them out of its own source: every exit inside repeat_guard is either
  # `exit 1` (refuse) or `return 0` (dispatch unchanged). An arm that returned
  # anything else would be an arm that had started editing results.
  local body sawother
  body="$(awk '/^repeat_guard\(\)/{f=1} f{print} f && /^}/{exit}' "$save_root/scripts/verify.sh")"
  sawother="$(grep -oE '^[[:space:]]*(exit|return) [0-9]+' <<<"$body" | grep -vE '(exit 1|return 0)$' || true)"
  if [ -n "$sawother" ]; then
    echo "verify --teeth: FAIL — P2g: repeat_guard may only refuse (exit 1) or dispatch unchanged (return 0); found: $sawother" >&2
    ROOT="$save_root"; VERIFY_ROOT="$save_vroot"; MODE="$save_mode"; RUN_TREE="$save_tree"; rm -rf "$rg"; return 1
  fi

  # T-P2h — THE REFUSAL MUST NOT LOOK LIKE A RUN, and every previous tooth here
  # was blind to it because `_rg` calls the guard inside a command
  # substitution: `exit 1` leaves only that subshell, and `trap finish EXIT` is
  # not installed there. In a real invocation the trap IS installed (two lines
  # above the guard's call site), so the refusal's own `exit 1` fires finish(),
  # which sees RUN_TREE and RUN_STARTED_MS both set and appends
  # `run:<mode> fail` for a run that never happened.
  #
  # The consequence is the guard defeating itself on alternate attempts: the
  # next invocation reads that `fail`, the `verdict = pass` clause is false,
  # and it dispatches — with no VERIFY_AGAIN=1 and no BYPASSED line. A bypass
  # nobody can see in a transcript is the exact thing P2d exists to forbid.
  #
  # So this exercises the WHOLE sequence, trap included, three times over one
  # unchanged tree: run, refuse, refuse. The third is the assertion.
  local n3 refusals=0
  : >"$rgt/telemetry.jsonl"
  for n3 in 1 2 3; do
    ( trap finish EXIT
      RUN_STARTED_MS="$(now_ms)"
      VERIFY_AGAIN="" GITHUB_ACTIONS="" repeat_guard
      true ) >/dev/null 2>&1 || refusals=$((refusals + 1))
  done
  if [ "$refusals" -ne 2 ]; then
    echo "verify --teeth: FAIL — P2h: over ONE unchanged tree, run/refuse/refuse must give 2 refusals; got $refusals. A refusal recorded as a run lets every other retry through silently." >&2
    ROOT="$save_root"; VERIFY_ROOT="$save_vroot"; MODE="$save_mode"; RUN_TREE="$save_tree"; rm -rf "$rg"; return 1
  fi
  if grep -q '"gate":"run:full","verdict":"fail"' "$rgt/telemetry.jsonl"; then
    echo "verify --teeth: FAIL — P2h: a REFUSAL was recorded as a failed run. Nothing ran, so nothing may be recorded — and the next invocation reads that record and dispatches." >&2
    ROOT="$save_root"; VERIFY_ROOT="$save_vroot"; MODE="$save_mode"; RUN_TREE="$save_tree"; rm -rf "$rg"; return 1
  fi

  ROOT="$save_root"; VERIFY_ROOT="$save_vroot"; MODE="$save_mode"; RUN_TREE="$save_tree"
  rm -rf "$rg"
  echo "verify --teeth: the repetition guard refuses a byte-identical tree that already passed, clears when a file already in the delta is EDITED (not renamed), announces its own bypass, never blocks a re-run after a red, never meets a runner, can only refuse or dispatch, and does not record its own refusal as a run — so a straight retry does not slip through every other time."

  echo "verify --teeth: owned root accepted by construction; symlink and foreign residue refused; scoped tests reject stale binaries; target preserved; red status recorded and returned; lane strict mode refuses an empty or misspelled input and stays out of the way of a clean-tree default; a derived phase the runner cannot execute is refused by name rather than dropped, every repo gate is dispatchable so that refusal cannot fire on a working one, and the ship tier is both derived and deferred."
}

if [ "$MODE" = "--teeth" ]; then
  teeth_root="$(mktemp -d)"
  trap 'rm -rf "$teeth_root"' EXIT
  run_teeth "$teeth_root"
  exit 0
fi

case "$MODE" in
  full|validators|coverage|harness|live|logic-e2e|lane|lane-run|projection) ;;
  test) validate_scoped_packages ;;
  *)
    echo "usage: $0 [full|validators|coverage|harness|live|logic-e2e|lane|lane-run|projection|test ./pkg...|--teeth]" >&2
    exit 2
    ;;
esac

prepare_cache_root
# A KILLED RUN IS NOT A PASS, and until 2026-08-29 it recorded one.
#
# Only EXIT was trapped here. On SIGTERM bash runs the EXIT trap with `$?` set
# to the last COMPLETED command's status — 0 whenever the phase before the kill
# succeeded — so finish() appended `verdict: pass` for a run that never reached
# its last phase. That receipt then made repeat_guard REFUSE the next honest
# run ("this exact tree already passed it"), so a tree nothing had fully judged
# both looked proven AND could not be re-judged without VERIFY_AGAIN=1.
#
# Observed, not theorised: a `lane-run` terminated inside `_harness-check`,
# 309 log lines of ~1250 with logic-e2e never reached, recorded
# `run:lane-run pass` 2.8 min after it started, and the next invocation refused.
#
# `exit 143` (128+SIGTERM) makes finish() see a non-zero command_status and
# record `fail`. `fail` rather than a third verdict is deliberate: D9's rule is
# that UNMEASURED is a SEVERITY, never a fourth verdict, and this record has
# exactly one consumer — repeat_guard, which asks only whether the last run was
# a pass. Recording `fail` is not a claim that the tree is bad; it is the
# honest statement that this run proved nothing.
on_signal() { exit 143; }
trap on_signal TERM INT
trap finish EXIT
cd "$ROOT"

RUN_STARTED_MS="$(now_ms)"
repeat_guard

changed_paths() {
  # What THIS working tree has changed against HEAD: staged, unstaged and
  # untracked, which is exactly the set a session is about to ask a gate to
  # judge. `--porcelain` is used over `diff --name-only` on purpose — the
  # latter cannot see an untracked file, and a brand-new file is precisely
  # the case the hand-maintained map used to classify wrongly.
  #
  # Explicit paths win when given (`make lane FILES="a b"`), so the incident
  # in the spec can be replayed against a clean tree.
  if [ -n "${LANE_FILES:-}" ]; then
    # Whitespace-separated, and therefore SPACE-HOSTILE by construction: this
    # is the ergonomic escape hatch for replaying a diff by hand, not the
    # general input. A path containing a space goes through the derivation's
    # own stdin form instead
    # (`printf '%s\n' "a path" | go run internal/lane/lanecheck.go --derive`).
    # Stated rather than silently mangled.
    # shellcheck disable=SC2086
    printf '%s\n' $LANE_FILES
    return 0
  fi
  # The porcelain walk, including the rename case that cost a real bug, lives
  # in working_tree_delta — the same set run_tree_identity reads, on purpose.
  working_tree_delta
}

# run_derived_phase maps ONE derived phase name to the thing that runs it.
# The mapping lives here because verify.sh is already the one place that
# knows how each phase is invoked; a second table would be the copy problem
# this whole phase exists to remove.
run_derived_phase() {
  local pkg
  case "$1" in
    build-cli)       run_phase build-cli build_cli ;;
    gofmt)           run_phase gofmt check_gofmt ;;
    vet)             run_phase vet go vet -tags=livee2e ./... ;;
    golangci-lint)   run_phase golangci-lint check_lint ;;
    go-test)         run_phase go-test run_go_tests ;;
    coverage-policy) run_phase coverage-policy go run internal/coveragepolicy/covercheck.go coverage.out ;;
    logic-e2e)       run_phase logic-e2e run_logic_tests ;;
    harness-teeth)   run_phase harness-teeth make --no-print-directory _harness-check ;;
    live-e2e)
      fail "live-e2e is declared NEVER and must not be reachable from a derived lane"
      return 1
      ;;
    go-test-scoped:*)
      pkg="${1#go-test-scoped:}"
      run_phase "go-test-scoped $pkg" go test "$pkg" -race -count=1
      ;;
    *)
      # Everything else is a Makefile target: the REPO_GATES entries and the
      # opt-in ones (web-quality).
      run_phase "$1" make --no-print-directory "$1"
      ;;
  esac
}

if [ "$MODE" = lane ] || [ "$MODE" = lane-run ]; then
  # Strict mode: an empty (or unresolvable) input set is a REFUSAL, never a
  # silent green. The interactive default — a developer at a keyboard on a
  # clean tree — stays the friendly message and exit 0; CI opts into strict
  # by the flag or the env var. Both are supported because Makefile:75 passes
  # `make lane-run` through with no extra args and the Makefile is not this
  # brief's to touch — the env var is the only way a CI recipe can reach this
  # today, while the flag is the explicit form for a direct `bash
  # scripts/verify.sh lane[-run] --require-nonempty` call. Follows the file's
  # own boolean convention (`GITHUB_ACTIONS` above): exact string "true", not
  # bare `-n`, so an accidentally-set empty var does not silently arm it.
  require_nonempty=0
  plan_only=0
  for arg in "${@:2}"; do
    case "$arg" in
      "") ;;
      --require-nonempty) require_nonempty=1 ;;
      # --plan — CLASSIFY WITHOUT EXECUTING. Derive, apply the ship tier,
      # refuse an undispatchable phase, and print what would run; run nothing.
      #
      # It exists because the alternative is unsafe: a self-test that exercises
      # the refusal has to reach `lane-run`, and a `lane-run` whose refusal is
      # blunted EXECUTES the derived lane — which selects `harness-teeth`,
      # which is the self-test. The tooth would run itself. Found on
      # 2026-08-22 by a red-check that hung for ten minutes doing exactly that.
      # It is also the honest way for a person to ask "what will this cost?"
      # without paying it.
      --plan) plan_only=1 ;;
      *)
        echo "usage: $0 $MODE [--require-nonempty] [--plan]" >&2
        exit 2
        ;;
    esac
  done
  if [ "${A2A_VERIFY_REQUIRE_NONEMPTY:-}" = "true" ]; then
    require_nonempty=1
  fi
  # `lane` DERIVES AND PRINTS; it never executes, so it has no plan to make.
  # Accepting the flag and ignoring it would be the shape this whole change
  # removes — a caller told the tool something and the tool silently did not
  # do it. Refuse, and name the mode that implements it.
  if [ "$MODE" = lane ] && [ "$plan_only" = 1 ]; then
    echo "verify: --plan is a lane-run flag; \`$0 lane\` already executes nothing and prints the derived set." >&2
    echo "        You probably want: $0 lane-run --plan  (classify, refuse, name deferrals, run nothing)." >&2
    exit 2
  fi

  paths="$(changed_paths)"
  if [ -z "$paths" ]; then
    if [ "$require_nonempty" = 1 ]; then
      echo "lane: REFUSED — nothing changed against HEAD and --require-nonempty (or A2A_VERIFY_REQUIRE_NONEMPTY=true) demands a non-empty input; a lane that ran zero gates must not report green." >&2
      echo '      Pass LANE_FILES="a b c" to derive for an explicit set.' >&2
      exit 1
    fi
    echo "lane: nothing changed against HEAD — no lane to derive." >&2
    echo '      Pass LANE_FILES="a b c" to derive for an explicit set.' >&2
    exit 0
  fi
  if [ "$MODE" = lane ]; then
    printf '%s\n' "$paths" | go run internal/lane/lanecheck.go --derive
    exit $?
  fi
  # lane-run: derive first, and let a REFUSAL stop the run. Deriving a lane
  # the tool does not fully understand and running the part it does is the
  # silent-hole shape this phase removes (spec 12 J2).
  if ! phases="$(printf '%s\n' "$paths" | go run internal/lane/lanecheck.go --phases)"; then
    echo "lane: refusing to run a lane the derivation could not settle (see above)." >&2
    exit 1
  fi
  # ORDER, NOT MEMBERSHIP — and until 2026-08-21 this was one hand-written
  # string that silently decided both.
  #
  # The roster named all 22 phases in order to express exactly TWO couplings
  # (its own comment said so): `build-cli` feeds the binary-backed static
  # gates, and `coverage-policy` reads the profile `go-test` writes. Membership
  # was its side effect — a derived phase absent from the string was DROPPED,
  # and `ran` was then printed as though it were the lane's size. Measured that
  # day: `make lane` derived 12, `lane-run` executed 10, and the green line
  # said 10. A phase could be declared, matched against the diff, and never run,
  # with nothing saying so.
  #
  # So the couplings are expressed as couplings and nothing else is ordered:
  # build-cli first, coverage-policy after the whole-module tests, scoped
  # package tests last (their names carry a package path, and they narrow what
  # the whole-module phases already ran). Everything else runs in derivation
  # order. A phase this runner cannot dispatch is now a NAMED REFUSAL — the
  # same discipline as a path no gate claims.
  ship_phases="$(printf '%s\n' "$paths" | go run internal/lane/lanecheck.go --ship-phases)" || {
    echo "lane: refusing to run a lane whose ship-tier set could not be resolved (see above)." >&2
    exit 1
  }

  derived=0 ran=0 deferred=0
  run_now="" run_last="" run_coverage=""
  for phase in $phases; do
    derived=$((derived + 1))
    if [ -n "$ship_phases" ] && printf '%s\n' "$ship_phases" | grep -qxF "$phase"; then
      deferred=$((deferred + 1))
      echo "lane: $phase — DERIVED and DEFERRED to the ship lane (\`lane-tier: ship\`). Not run here; \`make release-check\` runs it, and \`make $phase\` pays it now."
      continue
    fi
    case "$phase" in
      build-cli)         run_now="build-cli $run_now" ;;
      coverage-policy)   run_coverage="coverage-policy" ;;
      go-test-scoped:*)  run_last="$run_last $phase" ;;
      *)
        # DISPATCHABILITY IS CHECKED, NOT ASSUMED. run_derived_phase's
        # fallback is `make <phase>`, so a derived phase that is neither an
        # explicit case nor a real target would fail as a make error — a
        # different message, at a different layer, for what is really "this
        # runner does not know this phase".
        if ! phase_is_dispatchable "$phase"; then
          echo "lane: REFUSED — the derivation selected phase '$phase', and this runner cannot execute it." >&2
          echo "      It is neither a case in run_derived_phase nor a .PHONY target in the Makefile." >&2
          echo "      Either give it a target/case, or declare it \`# lane-tier: ship\` if only a ship lane should pay for it." >&2
          echo "      A derived phase is never silently dropped: that is how 'projection' went unrun from every lane it was ever selected for." >&2
          exit 1
        fi
        run_now="$run_now $phase"
        ;;
    esac
  done

  if [ "$plan_only" = 1 ]; then
    for phase in $run_now $run_coverage $run_last; do
      echo "lane: would run $phase"
      ran=$((ran + 1))
    done
    echo "lane: PLAN ONLY — $derived derived, $ran would run, $deferred deferred to the ship lane. Nothing executed."
    exit 0
  fi

  for phase in $run_now; do run_derived_phase "$phase"; ran=$((ran + 1)); done
  for phase in $run_coverage; do run_derived_phase "$phase"; ran=$((ran + 1)); done
  for phase in $run_last; do run_derived_phase "$phase"; ran=$((ran + 1)); done

  # THREE NUMBERS, because one cannot carry the meaning. "$ran green" over a
  # derived set of a different size is the shape that hid the drop.
  if [ "$deferred" -gt 0 ]; then
    echo "lane: $derived derived, $ran run and green, $deferred deferred to the ship lane (named above). This is NOT the ceiling — a release runs 'make check' (spec 12 J5)."
  else
    echo "lane: $derived derived, all $ran run and green. This is NOT the ceiling — a release runs 'make check' (spec 12 J5)."
  fi
  exit 0
fi

if [ "$MODE" = test ]; then
  # lane-inputs: NEVER
  # lane-reason: parameterised by the caller (`make test PKG=…`). The derivation
  #   emits `go-test-scoped:<pkg>` from a package's OWN doc.go lane-inputs block;
  #   this bare phase is that mechanism's implementation, never a selectable one.
  run_phase go-test-scoped run_scoped_tests
  exit 0
fi

if [ "$MODE" != live ] && [ "$MODE" != logic-e2e ]; then
  # lane-inputs: ALWAYS
  # lane-reason: a prerequisite, not a verdict — the binary-backed static gates
  #   (skill-citations runs `a2a __catalog`, localserver-readonly-routes probes the
  #   built router) need it whatever the diff touched. ~405 ms median, n=53.
  run_phase build-cli build_cli
fi

if [ "$MODE" = live ]; then
  # lane-inputs: NEVER
  # lane-reason: two credentials, network, and a real throwaway GitHub space with
  #   Actions latency (Makefile:109 — "NEVER in `check`, never a merge gate"). No
  #   diff may select it; `make live-e2e` is its only entry.
  # lane-reads-opaque: trim_telemetry (line ~156) rotates
  #   "$VERIFY_ROOT"/telemetry.jsonl through "$tmp". Both are inside
  #   .a2a/cache/, this runner's own accelerator cache — not repo content, and
  #   nothing a diff can contain. It is flagged because the classifier resolves
  #   literals, not because the read is a lane input.
  run_phase live-e2e run_live_tests
  exit 0
fi

if [ "$MODE" = logic-e2e ]; then
  # The inner loop: someone iterating on a scenario runs just this lane
  # instead of the whole ceiling. Same entry points `full` runs, same
  # -run scope — never a second, wider invocation.
  #
  # This is `logic-e2e`'s canonical declaration; the phase is invoked from a
  # second call site inside `full` below, and one declaration covers both
  # (P12 plan D-2 — dedup by phase name).
  #
  # 338 s median (n=29) — 78% of the ceiling, so this is the single phase the
  # derivation exists to keep out of a lane that cannot reach it (spec 12 §3.4).
  # The Go tree is declared whole rather than as `go list -deps ./internal/livee2e`:
  # an honest over-approximation, because narrowing it is a tiering decision and
  # J4 forbids a tier without its own before/after numbers. Filed in
  # docs/backlog.md; the win this phase banks is on every NON-Go change.
  # lane-inputs:
  #   **/*.go
  #   go.mod
  #   go.sum
  #   schemas/**
  #   space-template/**
  # lane-reads-opaque: trim_telemetry (line ~156) rotates
  #   "$VERIFY_ROOT"/telemetry.jsonl through "$tmp". Both are inside
  #   .a2a/cache/, this runner's own accelerator cache — not repo content, and
  #   nothing a diff can contain. It is flagged because the classifier resolves
  #   literals, not because the read is a lane input.
  run_phase logic-e2e run_logic_tests
  exit 0
fi

if [ "$MODE" = harness ]; then
  # lane-inputs:
  #   scripts/**
  #   .agents/scripts/**
  #   Makefile
  #   docs/runbooks/publish-to-public.sh
  run_phase harness-teeth make --no-print-directory _harness-check
  exit 0
fi

if [ "$MODE" = projection ]; then
  #
  # PRESENCE-GATED, and the reason is this phase's own subject. verify.sh SHIPS;
  # scripts/check-projection.sh is on the private path set and does not. Inside
  # a projection the phase would therefore reference a file that is not there —
  # a shipped artifact depending on a stripped path, which is precisely what
  # this gate exists to refuse. Found by the gate itself, on its own first
  # acceptance run, against HEAD.
  #
  # Shipping the gate instead would be worse: in the published repository every
  # private path is ALREADY absent, so its own empty/unmatched-set refusal
  # (teeth T6/T6b) would red every public run for the right reason at the wrong
  # time. An announced skip is the shape every other private gate here uses.
  #
  # NOTE: this guard stops the RUN. The one internal/lane reads is the
  # Makefile RECIPE's — `recipeGuardsPresence` tolerates an absent script only
  # when the recipe guards it, which is why `projection:` carries the same
  # `if [ -f ... ]` shape feature-lint and its five siblings do. Both exist on
  # purpose: this one for anyone invoking verify.sh directly, that one for the
  # corpus scanner.
  if [ ! -f "$ROOT/scripts/check-projection.sh" ]; then
    echo "projection: skip — scripts/check-projection.sh absent (public checkout); there is nothing to project inside a projection."
    exit 0
  fi
  # THE PROJECTION GATE (release-loop-2026-08 P3). It materialises a public
  # projection of the commit under test and runs CI's `check` job inside it, so
  # "a shipped artifact reads a path the publisher removes" reds here instead
  # of at a published candidate 45 minutes later.
  #
  # DELIBERATELY NOT INSIDE `full`. This phase runs `make check` and
  # `make harness-check` in the projection, so ceiling membership would mean
  # the ceiling runs the ceiling: roughly double the ~15-minute `check` job
  # plus the ~136-second teeth, on every Go edit, as a COMMIT gate. It is the
  # derived lane and the ship lane that reach it. The re-entry that membership
  # would create is also refused by name inside the gate itself.
  #
  # SHIP TIER (added 2026-08-21). Kind and Tier answer different questions:
  # the globs below decide WHETHER a diff selects this phase, `lane-tier:`
  # decides WHO EXECUTES it once selected. `NEVER` would be a false statement
  # here — a docs-only commit genuinely must not select this gate and an
  # internal/** one must, which is spec 03's AC5 — while `commit` would be
  # wrong for a different reason: the commit lane judges the tree you are
  # about to commit, and this gate judges a PUBLIC PROJECTION of a commit.
  # Different question, different lane.
  #
  # It was already reachable: `make projection` is a phase-1 member of
  # `make release-check` and ran there for 358 s on 2026-08-21. What it was
  # NOT is honestly reported — `lane-run` filtered the derived set through a
  # hand-written roster and printed its own truncated count as the lane's
  # size, so this phase was derived on nearly every commit and executed by a
  # lane exactly zero times. `lane-run` now names it instead.
  #
  # The declaration below is as wide as the projection really is and no wider:
  # the projection CONTAINS every shipped path, so any of them can change its
  # verdict, while the private trees it removes cannot. The exclusions are the
  # private path set (scripts/lib/strip-set.txt) minus its own home, which
  # stays IN — a commit that only adds a path to the strip set can introduce
  # this exact defect by making a file a shipped artifact already reads
  # disappear, and a declaration that missed it would be a false green.
  # docs/** is excluded and that is what AC5 asks for; the strip set is NOT in
  # docs/, which is precisely why it can be.
  # lane-inputs:
  #   **
  #   !docs/**
  #   !.agents/**
  #   !.claude/**
  #   !.codex/**
  #   !.mate/**
  #   !AGENTS.md
  #   !CLAUDE.md
  # lane-tier: ship
  run_phase projection bash "$ROOT/scripts/check-projection.sh" --all
  exit 0
fi

if [ "$MODE" = validators ]; then
  run_repo_gates
  exit 0
fi

if [ "$MODE" = full ]; then
  run_repo_gates
  # lane-inputs:
  #   **/*.go
  run_phase gofmt check_gofmt
  # The tagged tree is a strict superset: this repository has no !livee2e
  # production file, so one vet invocation covers both ordinary and live code.
  # lane-inputs:
  #   **/*.go
  #   go.mod
  #   go.sum
  run_phase vet go vet -tags=livee2e ./...
  # lane-inputs:
  #   **/*.go
  #   go.mod
  #   go.sum
  #   .golangci.yml
  run_phase golangci-lint check_lint
fi

# lane-inputs:
#   **/*.go
#   go.mod
#   go.sum
#   schemas/dashboard-view-objects.schema.json
#   schemas/fixtures/dashboard-view-objects/**
#   web/design-source/cards.manifest.json
run_phase go-test run_go_tests
# Reads the coverage.out this run just produced plus its own policy source; the
# aggregate floor is a function of the whole Go tree, so it is declared as such
# rather than as the one package that holds the thresholds.
# lane-inputs:
#   **/*.go
#   go.mod
#   go.sum
run_phase coverage-policy go run internal/coveragepolicy/covercheck.go coverage.out

if [ "$MODE" = full ]; then
  # After the Go tests, not before: a broken package fails on the cheaper
  # untagged signal first. This is the logic tier's own lane inside the
  # merge gate (spec 09 §5a) — it needs no credentials and no network, so
  # unlike `live-e2e` it belongs here rather than fenced away from `check`.
  run_phase logic-e2e run_logic_tests
  echo "check: repo gates + Go gates green (coverage floor met)."
else
  echo "coverage: Go race suite green (coverage floor met)."
fi
