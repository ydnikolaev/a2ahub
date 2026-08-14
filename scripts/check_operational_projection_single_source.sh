#!/usr/bin/env bash
# Guard P3's single-source invariant: production HTML and the loopback server
# consume the frozen operational snapshot; only operationalsource derives it.
#
# lane-inputs:
#   internal/**/*.go
#   !internal/**/*_test.go
#   web/design-source/*.dc.html
# lane-reads-opaque: every grep target is a $ROOT-prefixed variable, not a
#   literal. The bindings below point at the four declared Go inputs and the
#   dashboard root. The browser-source array is derived from that root's literal
#   dc-import graph and resolves only sibling web/design-source/*.dc.html files,
#   which is the declared source corpus rather than a second component list.
#   The internal find below walks the same internal/**/*.go tree.
#   Concretely, $model/$static/$renderer/$source/$browser bind to
#   "$ROOT/internal/html/model.go", "$ROOT/internal/cli/cmd_html.go",
#   "$ROOT/internal/html/dashboard_renderer.go",
#   "$ROOT/internal/operationalsource/source.go" and
#   "$ROOT/web/design-source/14-local-dashboard-v4.dc.html" respectively —
#   each covered by the declarations above.
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

# P4 extracted presentation into direct root imports. Derive the validator's
# source corpus from that import graph so extraction cannot make a live
# consumer invisible to the guard and adding a component cannot require a
# second registry update here.
browser_sources=("$browser")
browser_imports="$(grep -oE '<dc-import[[:space:]]+name="[^"]+"' "$browser" | sed -E 's/.*name="([^"]+)"/\1/' | sort -u)"
test -n "$browser_imports" || fail "dashboard root declares no component imports"
while IFS= read -r component; do
  component_source="$ROOT/web/design-source/$component.dc.html"
  test -f "$component_source" || fail "dashboard import $component has no web/design-source/$component.dc.html"
  browser_sources+=("$component_source")
done <<<"$browser_imports"

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
# The presentation variable name is intentionally irrelevant: extraction moved
# the consumer from `w` to `item`, while the load-bearing contract stayed the
# explicit comparison against the core-owned boolean.
grep -Eq '[[:alpha:]_][[:alnum:]_]*\.current[[:space:]]*===[[:space:]]*true' "${browser_sources[@]}" ||
  fail "dashboard does not consume the core-owned work.current decision"
if grep -Eq '\[[^]]*"local-current"[^]]*"committed-current"[^]]*\].*(current|fresh)|indexOf\(w\.freshness\)' "${browser_sources[@]}"; then
  fail "dashboard derives current work from a browser-owned freshness vocabulary"
fi

echo "operational-projection-single-source: ok — static HTML and server consume one operational snapshot"
