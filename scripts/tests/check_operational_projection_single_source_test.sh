#!/usr/bin/env bash
# Teeth for check_operational_projection_single_source.sh.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check_operational_projection_single_source.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() { echo "operational-projection-single-source-test: FAIL — $*" >&2; exit 1; }
copy_tree() {
  local destination="$1"
  mkdir -p "$destination/internal"
  cp -R "$ROOT/internal/html" "$destination/internal/html"
  cp -R "$ROOT/internal/localserver" "$destination/internal/localserver"
  cp -R "$ROOT/internal/operationalsource" "$destination/internal/operationalsource"
  mkdir -p "$destination/internal/cli"
  cp "$ROOT/internal/cli/cmd_html.go" "$destination/internal/cli/cmd_html.go"
  mkdir -p "$destination/web/design-source"
  cp "$ROOT/web/design-source/14-local-dashboard-v4.dc.html" "$destination/web/design-source/14-local-dashboard-v4.dc.html"
}
expect_red() {
  local tree="$1" needle="$2" label="$3" output
  if output="$(OPERATIONAL_PROJECTION_ROOT="$tree" bash "$GATE" 2>&1)"; then fail "$label stayed green"; fi
  grep -Fq "$needle" <<<"$output" || fail "$label red did not name '$needle': $output"
}

bash "$GATE" >/dev/null || fail "the real P3 source is not green"

second_model="$WORK/second-model"
copy_tree "$second_model"
printf '\ntype OperationalTimeline []struct{}\n' >>"$second_model/internal/html/model.go"
expect_red "$second_model" "second operational projection" "second presentation model"

second_derivation="$WORK/second-derivation"
copy_tree "$second_derivation"
printf '\nfunc rogueOperationalProjection() { operational.Build(operational.Input{}, time.Now(), operational.Limits{}) }\n' >>"$second_derivation/internal/html/assemble.go"
expect_red "$second_derivation" "derives operational semantics" "HTML-side projection"

missing_injection="$WORK/missing-injection"
copy_tree "$missing_injection"
perl -0pi -e 's/AssembleWithOperational/AssembleFromThreadView/g' "$missing_injection/internal/html/dashboard_renderer.go"
expect_red "$missing_injection" "server shell renderer" "server-side re-derivation seam"

browser_derivation="$WORK/browser-derivation"
copy_tree "$browser_derivation"
perl -0pi -e 's/w && w\.current === true/\["local-current", "committed-current"\].indexOf(w.freshness) > -1/' "$browser_derivation/web/design-source/14-local-dashboard-v4.dc.html"
expect_red "$browser_derivation" "core-owned work.current" "browser freshness derivation"

rogue_core="$WORK/rogue-core"
copy_tree "$rogue_core"
mkdir -p "$rogue_core/internal/cache"
printf 'package cache\nfunc rogue() { operational.Build(operational.Input{}, time.Now(), operational.Limits{}) }\n' >"$rogue_core/internal/cache/rogue.go"
expect_red "$rogue_core" "outside operationalsource" "core-side second projection"

echo "operational-projection-single-source-test: PASS"
