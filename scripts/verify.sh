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

append_telemetry() {
  local gate="$1" verdict="$2" duration_ms="$3" at
  at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf '{"gate":"%s","verdict":"%s","duration_ms":%s,"mode":"%s","at":"%s"}\n' \
    "$gate" "$verdict" "$duration_ms" "$MODE" "$at" >>"$VERIFY_ROOT/telemetry.jsonl"
}

run_phase() {
  local gate="$1" start end rc verdict
  shift
  start="$(now_ms)"
  if "$@"; then
    rc=0
  else
    rc=$?
  fi
  end="$(now_ms)"
  verdict=pass
  if [ "$rc" -ne 0 ]; then
    verdict=fail
  fi
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

finish() {
  local command_status=$? maintenance_status=0
  set +e
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
    echo "check: FAIL — .golangci.yml exists but golangci-lint is not installed." >&2
    echo "       A configured lint gate that silently skips is a hole, not a gate." >&2
    return 1
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
  out="$(LANE_FILES=" " bash "$ROOT/scripts/verify.sh" lane 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ] || ! grep -q "nothing changed against HEAD" <<<"$out"; then
    echo "verify --teeth: FAIL — the interactive default (no strict flag) stopped exiting 0 on an empty set:" >&2
    echo "$out" >&2
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

  echo "verify --teeth: owned root accepted by construction; symlink and foreign residue refused; scoped tests reject stale binaries; target preserved; red status recorded and returned; lane strict mode refuses an empty or misspelled input and stays out of the way of a clean-tree default."
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
trap finish EXIT
cd "$ROOT"

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
  # `--porcelain=v1 -z` emits a RENAME/COPY as two NUL-terminated tokens:
  # "XY <newpath>\0<origpath>\0" — and the ORIGINAL path carries NO "XY "
  # prefix. A blanket `sed 's/^...//'` over every token therefore eats three
  # characters off it (verified: `git mv original.txt renamed.txt` yielded
  # "ginal.txt"), and the deriver then either refuses a path that does not
  # exist or, worse, matches a mangled fragment against some unrelated glob.
  # Both halves of a rename are real inputs — the old path stops existing and
  # the new one starts — so both are reported.
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
  case "${2:-}" in
    "") ;;
    --require-nonempty) require_nonempty=1 ;;
    *)
      echo "usage: $0 $MODE [--require-nonempty]" >&2
      exit 2
      ;;
  esac
  if [ "${A2A_VERIFY_REQUIRE_NONEMPTY:-}" = "true" ]; then
    require_nonempty=1
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
  # A canonical ORDER, filtered by membership: build-cli feeds the
  # binary-backed static gates, and coverage-policy reads the profile
  # go-test writes. Running the derived set in declaration order would break
  # both couplings for a reason nobody could see from the output.
  ordered="build-cli $(make --no-print-directory -s _print-repo-gates) web-quality gofmt vet golangci-lint go-test coverage-policy logic-e2e harness-teeth"
  ran=0
  for phase in $ordered; do
    if printf '%s\n' "$phases" | grep -qxF "$phase"; then
      run_derived_phase "$phase"
      ran=$((ran + 1))
    fi
  done
  # Scoped package tests are not in the canonical order (their names carry a
  # package path); run them last, after the whole-module phases they narrow.
  for phase in $phases; do
    case "$phase" in
      go-test-scoped:*) run_derived_phase "$phase"; ran=$((ran + 1)) ;;
    esac
  done
  echo "lane: $ran derived phase(s) green. This is NOT the ceiling — a release runs 'make check' (spec 12 J5)."
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
