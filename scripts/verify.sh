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

build_cli() {
  # This is a synthetic test artifact with an explicit version. VCS stamping
  # adds no information, and Go 1.26's stamp resolver writes a revision stat
  # entry to the shared GOMODCACHE. Disabling it at the owning build command
  # keeps that shared input read-only inside agent sandboxes.
  go build -buildvcs=false -ldflags "-X main.version=0.1.0" -o "$A2A_VERIFY_BINARY" ./cmd/a2a
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
  go test ./internal/livee2e/... -tags=livee2e -count=1 -v -timeout 125m
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

  echo "verify --teeth: owned root accepted by construction; symlink and foreign residue refused; target preserved; red status recorded and returned."
}

if [ "$MODE" = "--teeth" ]; then
  teeth_root="$(mktemp -d)"
  trap 'rm -rf "$teeth_root"' EXIT
  run_teeth "$teeth_root"
  exit 0
fi

case "$MODE" in
  full|validators|coverage|harness|live) ;;
  test) validate_scoped_packages ;;
  *)
    echo "usage: $0 [full|validators|coverage|harness|live|test ./pkg...|--teeth]" >&2
    exit 2
    ;;
esac

prepare_cache_root
trap finish EXIT
cd "$ROOT"

if [ "$MODE" = test ]; then
  run_phase go-test-scoped run_scoped_tests
  exit 0
fi

if [ "$MODE" != live ]; then
  run_phase build-cli build_cli
fi

if [ "$MODE" = live ]; then
  run_phase live-e2e run_live_tests
  exit 0
fi

if [ "$MODE" = harness ]; then
  run_phase harness-teeth make --no-print-directory _harness-check
  exit 0
fi

if [ "$MODE" = validators ]; then
  run_repo_gates
  exit 0
fi

if [ "$MODE" = full ]; then
  run_repo_gates
  run_phase gofmt check_gofmt
  # The tagged tree is a strict superset: this repository has no !livee2e
  # production file, so one vet invocation covers both ordinary and live code.
  run_phase vet go vet -tags=livee2e ./...
  run_phase golangci-lint check_lint
fi

run_phase go-test run_go_tests
run_phase coverage-policy go run internal/coveragepolicy/covercheck.go coverage.out

if [ "$MODE" = full ]; then
  echo "check: repo gates + Go gates green (coverage floor met)."
else
  echo "coverage: Go race suite green (coverage floor met)."
fi
