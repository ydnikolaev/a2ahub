#!/usr/bin/env bash
# Guard P3's frozen read-only loopback HTTP surface.
set -euo pipefail

ROOT="${LOCALSERVER_READONLY_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)}"
ROUTER="$ROOT/internal/localserver/router.go"
SERVER_ROOT="$ROOT/internal/localserver"

fail() {
  echo "localserver-readonly-routes: FAIL — $*" >&2
  exit 1
}

test -f "$ROUTER" || fail "missing internal/localserver/router.go"

routes="$(sed -nE 's/^[[:space:]]*case "(\/[^\"]*)":[[:space:]]*$/\1/p' "$ROUTER" | sort -u | tr '\n' ' ' | sed 's/ $//')"
expected="/ /api/v1/events /api/v1/snapshot"
if [[ "$routes" != "$expected" ]]; then
  fail "route inventory is [$routes], want [$expected]"
fi

if grep -R -Eq --include='*.go' --exclude='*_test.go' 'http\.Method(Post|Put|Patch|Delete|Connect|Options|Trace)' "$SERVER_ROOT"; then
  fail "router admits a write/control HTTP method"
fi
if grep -R -Eq --include='*.go' --exclude='*_test.go' 'request\.(FormValue|ParseForm|ParseMultipartForm)|json\.NewDecoder[[:space:]]*\([[:space:]]*request\.Body|io\.ReadAll[[:space:]]*\([[:space:]]*request\.Body' "$SERVER_ROOT"; then
  fail "router parses a request body"
fi
grep -Fq 'request bodies are not accepted' "$ROUTER" ||
  fail "router no longer rejects request bodies before dispatch"
grep -Fq 'validateRequestHost(request.Host' "$ROUTER" ||
  fail "router no longer validates Host before dispatch"

# This is not merely a source grep: on the real repository the gate constructs
# the production handler and probes the exact route/method matrix. Teeth trees
# omit the Go module and exercise the source guard independently.
if [[ "${LOCALSERVER_READONLY_SKIP_RUNTIME:-0}" != "1" && -f "$ROOT/go.mod" ]]; then
  if ! A2A_VERIFY_TEST_RUN='^TestRouterExactRouteAndMethodInventory$' make -s -C "$ROOT" test PKG=./internal/localserver/... >/dev/null; then
    fail "constructed production router does not satisfy the frozen route/method matrix"
  fi
fi

echo "localserver-readonly-routes: ok — constructed router has the exact GET/HEAD inventory and no mutation/body path"
