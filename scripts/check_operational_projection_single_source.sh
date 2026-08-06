#!/usr/bin/env bash
# Guard P3's single-source invariant: production HTML and the loopback server
# consume the frozen operational snapshot; only operationalsource derives it.
#
# lane-inputs:
#   internal/**/*.go
#   !internal/**/*_test.go
#   web/design-source/14-local-dashboard-v4.dc.html
set -euo pipefail

ROOT="${OPERATIONAL_PROJECTION_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)}"

fail() {
  echo "operational-projection-single-source: FAIL — $*" >&2
  exit 1
}

model="$ROOT/internal/html/model.go"
static="$ROOT/internal/cli/cmd_html.go"
renderer="$ROOT/internal/html/dashboard_renderer.go"
source="$ROOT/internal/operationalsource/source.go"
browser="$ROOT/web/design-source/14-local-dashboard-v4.dc.html"

for required in "$model" "$static" "$renderer" "$source" "$browser"; do
  test -f "$required" || fail "missing ${required#"$ROOT/"}"
done

grep -Eq 'Operational[[:space:]]+operational\.Snapshot' "$model" ||
  fail "internal/html.Data must carry operational.Snapshot"
grep -Fq 'AssembleWithOperational' "$static" ||
  fail "static HTML does not inject the shared operational snapshot"
grep -Fq 'AssembleWithOperational' "$renderer" ||
  fail "server shell renderer does not inject the supplied operational snapshot"
grep -Fq 'operational.Build(' "$source" ||
  fail "internal/operationalsource is not the operational projection owner"

# A demo fixture may call operational.Build explicitly. No other production
# package may derive operational semantics outside operationalsource.
while IFS= read -r file; do
  case "$file" in
    */internal/operational/*|*/internal/operationalsource/*) continue ;;
    */internal/html/demo.go) continue ;;
  esac
  if grep -Eq 'operational\.Build[[:space:]]*\(' "$file"; then
    fail "${file#"$ROOT/"} derives operational semantics outside operationalsource"
  fi
done < <(find "$ROOT/internal" -type f -name '*.go' ! -name '*_test.go' -print)

if grep -R -nE --include='*.go' --exclude='*_test.go' \
  'type[[:space:]]+Operational(Thread|Timeline)|OperationalTimeline[[:space:]]+\[\]|toOperationalThread[[:space:]]*\(' \
  "$ROOT/internal/html" "$ROOT/internal/localserver" >/dev/null; then
  fail "HTML/localserver declares a second operational projection model or derivation"
fi

# The browser may localise and navigate, but current-work membership is a
# core-owned boolean. Reintroducing a browser-side freshness set recreates the
# exact second truth this gate exists to prevent.
grep -Eq 'w[[:space:]]*&&[[:space:]]*w\.current[[:space:]]*===[[:space:]]*true' "$browser" ||
  fail "dashboard does not consume the core-owned work.current decision"
if grep -Eq '\[[^]]*"local-current"[^]]*"committed-current"[^]]*\].*(current|fresh)|indexOf\(w\.freshness\)' "$browser"; then
  fail "dashboard derives current work from a browser-owned freshness vocabulary"
fi

echo "operational-projection-single-source: ok — static HTML and server consume one operational snapshot"
