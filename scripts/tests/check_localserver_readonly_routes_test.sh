#!/usr/bin/env bash
# Teeth for check_localserver_readonly_routes.sh.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check_localserver_readonly_routes.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() { echo "localserver-readonly-routes-test: FAIL — $*" >&2; exit 1; }
copy_tree() {
  local destination="$1"
  mkdir -p "$destination/internal"
  cp -R "$ROOT/internal/localserver" "$destination/internal/localserver"
}
expect_red() {
  local tree="$1" needle="$2" label="$3" output
  if output="$(LOCALSERVER_READONLY_ROOT="$tree" LOCALSERVER_READONLY_SKIP_RUNTIME=1 bash "$GATE" 2>&1)"; then fail "$label stayed green"; fi
  grep -Fq "$needle" <<<"$output" || fail "$label red did not name '$needle': $output"
}

bash "$GATE" >/dev/null || fail "the real P3 router is not green"

mutation_route="$WORK/mutation-route"
copy_tree "$mutation_route"
perl -0pi -e 's/case "\/api\/v1\/events":/case "\/api\/v1\/report":\n\t\t\ts.handleEvents(writer, request)\n\t\tcase "\/api\/v1\/events":/' "$mutation_route/internal/localserver/router.go"
expect_red "$mutation_route" "route inventory" "mutation route"

write_method="$WORK/write-method"
copy_tree "$write_method"
perl -0pi -e 's/request\.Method != http\.MethodGet/request.Method != http.MethodGet \&\& request.Method != http.MethodPost/' "$write_method/internal/localserver/router.go"
expect_red "$write_method" "write/control HTTP method" "write method"

body_parser="$WORK/body-parser"
copy_tree "$body_parser"
printf '\nfunc rogueBodyParser(request *http.Request) { request.ParseForm() }\n' >>"$body_parser/internal/localserver/server.go"
expect_red "$body_parser" "parses a request body" "request-body parser"

echo "localserver-readonly-routes-test: PASS"
