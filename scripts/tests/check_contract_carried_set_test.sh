#!/usr/bin/env bash
# Teeth for check_contract_carried_set.sh. Every red case mutates a copy of
# the real P5 corpus/source rather than a synthetic fixture.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check_contract_carried_set.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() {
  echo "contract-carried-set-test: FAIL — $*" >&2
  exit 1
}

copy_tree() {
  local destination="$1"
  mkdir -p "$destination"
  cp -R "$ROOT/schemas" "$destination/schemas"
  cp -R "$ROOT/internal" "$destination/internal"
}

expect_red() {
  local tree="$1" needle="$2" label="$3" output
  if output="$(CONTRACT_CARRIED_SET_ROOT="$tree" bash "$GATE" 2>&1)"; then
    fail "$label stayed green"
  fi
  if ! grep -Fq "$needle" <<<"$output"; then
    fail "$label red did not name '$needle': $output"
  fi
}

bash "$GATE" >/dev/null || fail "the real P5 corpus/source is not green"

enum_drift="$WORK/enum-drift"
copy_tree "$enum_drift"
target="$enum_drift/schemas/envelope/v2/contract.schema.json"
perl -0pi -e 's/"example",\n            "other"/"example",\n            "extension"/' "$target"
grep -Fq '"extension"' "$target" || fail "could not seed contract role enum drift"
expect_red "$enum_drift" "contract schema role enum" "role enum drift"

profile_drift="$WORK/profile-drift"
copy_tree "$profile_drift"
target="$profile_drift/schemas/event/v2/event.schema.json"
perl -0pi -e 's/"contract-tree-v1", "contract-set-v2"/"contract-tree-v1", "contract-set-v2", "contract-set-v3"/' "$target"
grep -Fq '"contract-set-v3"' "$target" || fail "could not seed event profile drift"
expect_red "$profile_drift" "event/v2 digest_profile enum" "publication profile drift"

second_builder="$WORK/second-builder"
copy_tree "$second_builder"
cat >"$second_builder/internal/validate/rogue_carried_set.go" <<'GO'
package validate

func BuildCarriedSet() {}
GO
expect_red "$second_builder" "declares second carried-set/profile builder BuildCarriedSet" "second carried-set builder"

v1_mutation="$WORK/v1-mutation"
copy_tree "$v1_mutation"
target="$v1_mutation/schemas/envelope/v1/contract.schema.json"
printf '\n' >>"$target"
new_digest="$(shasum -a 256 "$target" | awk '{print $1}')"
perl -0pi -e 's/^[0-9a-f]{64}(  schemas\/envelope\/v1\/contract\.schema\.json)$/'"$new_digest"'$1/m' "$v1_mutation/schemas/published-v1.sha256"
grep -Fq "$new_digest  schemas/envelope/v1/contract.schema.json" "$v1_mutation/schemas/published-v1.sha256" || fail "could not seed matching v1 schema+manifest mutation"
expect_red "$v1_mutation" "published v1 checksum manifest mutated" "published v1 schema plus manifest mutation"

bash "$GATE" >/dev/null || fail "the real tree did not return to green after mutation probes"
echo "contract-carried-set-test: ok — role/profile drift, second builder and v1 mutation all red; production tree greens"
