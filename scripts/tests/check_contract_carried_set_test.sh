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

schema_root_drift="$WORK/schema-root-drift"
copy_tree "$schema_root_drift"
target="$schema_root_drift/schemas/envelope/v2/contract.schema.json"
perl -0pi -e 's/"path": \{ "pattern": "\^schema\//"path": { "pattern": "^artifacts\//' "$target"
expect_red "$schema_root_drift" "contract schema role/root matrix" "schema role/root drift"

core_role_drift="$WORK/core-role-drift"
copy_tree "$core_role_drift"
target="$core_role_drift/internal/contract/types.go"
perl -0pi -e 's/RoleVocabulary\s+Role = "vocabulary"/RoleVocabulary     Role = "lexicon"/' "$target"
grep -Fq 'Role = "lexicon"' "$target" || fail "could not seed core role drift"
expect_red "$core_role_drift" "internal/contract Role constants" "core role drift"

core_root_drift="$WORK/core-root-drift"
copy_tree "$core_root_drift"
target="$core_root_drift/internal/contract/set.go"
perl -0pi -e 's/case RoleSchema:\n\t\treturn "schema\/", true/case RoleSchema:\n\t\treturn "artifacts\/", true/' "$target"
expect_red "$core_root_drift" "internal/contract role/root matrix" "core role/root drift"

template_role_drift="$WORK/template-role-drift"
copy_tree "$template_role_drift"
target="$template_role_drift/schemas/templates/v2/contract.md"
perl -0pi -e 's/errors, vocabulary, limits/errors, lexicon, limits/' "$target"
grep -Fq 'lexicon' "$target" || fail "could not seed template vocabulary drift"
expect_red "$template_role_drift" "non-canonical template role" "template role drift"

profile_drift="$WORK/profile-drift"
copy_tree "$profile_drift"
target="$profile_drift/schemas/event/v2/event.schema.json"
perl -0pi -e 's/"contract-tree-v1", "contract-set-v2"/"contract-tree-v1", "contract-set-v2", "contract-set-v3"/' "$target"
grep -Fq '"contract-set-v3"' "$target" || fail "could not seed event profile drift"
expect_red "$profile_drift" "event/v2 digest_profile enum" "publication profile drift"

event_required_drift="$WORK/event-required-drift"
copy_tree "$event_required_drift"
target="$event_required_drift/schemas/event/v2/event.schema.json"
perl -0pi -e 's/\["version", "digest", "digest_profile", "publication"\]/["digest_profile", "publication"]/' "$target"
expect_red "$event_required_drift" "event/v2 contract publish required fields" "publication required-field drift"

core_profile_drift="$WORK/core-profile-drift"
copy_tree "$core_profile_drift"
target="$core_profile_drift/internal/contract/types.go"
perl -0pi -e 's/ProfileExportSourceV1 DigestProfile = "export-source-v1"/ProfileExportSourceV1 DigestProfile = "export-source-v2"/' "$target"
grep -Fq 'export-source-v2' "$target" || fail "could not seed core profile drift"
expect_red "$core_profile_drift" "internal/contract DigestProfile constants" "core profile drift"

core_profile_route_drift="$WORK/core-profile-route-drift"
copy_tree "$core_profile_route_drift"
target="$core_profile_route_drift/internal/contract/set.go"
perl -0pi -e 's/case ProfileContractTreeV1:/case ProfileExportSourceV1:/' "$target"
expect_red "$core_profile_route_drift" "internal/contract BuildCarriedSet profiles" "core profile routing drift"

template_profile_drift="$WORK/template-profile-drift"
copy_tree "$template_profile_drift"
target="$template_profile_drift/schemas/templates/v2/contract.md"
perl -0pi -e 's/export-source-v1/export-source-v2/' "$target"
expect_red "$template_profile_drift" "omits export-source-v1" "template provenance profile drift"

second_builder="$WORK/second-builder"
copy_tree "$second_builder"
cat >"$second_builder/internal/validate/rogue_carried_set.go" <<'GO'
package validate

func BuildCarriedSet() {}
GO
expect_red "$second_builder" "declares second carried-set/profile builder BuildCarriedSet" "second carried-set builder"

missing_builder="$WORK/missing-builder"
copy_tree "$missing_builder"
target="$missing_builder/internal/contract/set.go"
perl -0pi -e 's/func digestBytes\(/func renamedDigestBytes(/' "$target"
expect_red "$missing_builder" "canonical function digestBytes has 0 definitions" "missing canonical digest builder"

second_role_enum="$WORK/second-role-enum"
copy_tree "$second_role_enum"
cat >"$second_role_enum/internal/validate/rogue_roles.go" <<'GO'
package validate

const (
	rogueSchema = "schema"
	rogueOther  = "other"
)
GO
expect_red "$second_role_enum" "redeclares carried-set role enum" "second role enum"

second_profile_enum="$WORK/second-profile-enum"
copy_tree "$second_profile_enum"
cat >"$second_profile_enum/internal/validate/rogue_profiles.go" <<'GO'
package validate

const rogueContractProfile = "contract-set-v2"
GO
expect_red "$second_profile_enum" "redeclares carried-set digest profile" "second profile enum"

second_digest="$WORK/second-digest"
copy_tree "$second_digest"
cat >"$second_digest/internal/validate/rogue_digest.go" <<'GO'
package validate

import "github.com/ydnikolaev/a2ahub/internal/artifact"

func rogueContractDigest() string { return artifact.CombineDigestPairs(nil) }
GO
expect_red "$second_digest" "calls CombineDigestPairs outside the canonical builder/legacy adapter allowlist" "second contract digest"

second_legacy_inventory="$WORK/second-legacy-inventory"
copy_tree "$second_legacy_inventory"
cat >"$second_legacy_inventory/internal/validate/rogue_legacy_inventory.go" <<'GO'
package validate

var contractDigestSubtrees = []string{"schema", "fixtures"}
GO
expect_red "$second_legacy_inventory" "declares a second legacy contract digest subtree inventory" "second legacy subtree inventory"

legacy_scope_drift="$WORK/legacy-scope-drift"
copy_tree "$legacy_scope_drift"
target="$legacy_scope_drift/internal/contract/set.go"
perl -0pi -e 's#(func insideLegacyRoot\(path string\) bool \{\n\treturn [^\n]+)#$1 || strings.HasPrefix(path, "artifacts/")#' "$target"
grep -A1 -F 'func insideLegacyRoot' "$target" | grep -Fq 'artifacts/' || fail "could not seed legacy digest scope drift"
expect_red "$legacy_scope_drift" "internal/contract legacy roots" "legacy digest scope drift"

v1_mutation="$WORK/v1-mutation"
copy_tree "$v1_mutation"
target="$v1_mutation/schemas/envelope/v1/contract.schema.json"
printf '\n' >>"$target"
new_digest="$(shasum -a 256 "$target" | awk '{print $1}')"
perl -0pi -e 's/^[0-9a-f]{64}(  schemas\/envelope\/v1\/contract\.schema\.json)$/'"$new_digest"'$1/m' "$v1_mutation/schemas/published-v1.sha256"
grep -Fq "$new_digest  schemas/envelope/v1/contract.schema.json" "$v1_mutation/schemas/published-v1.sha256" || fail "could not seed matching v1 schema+manifest mutation"
expect_red "$v1_mutation" "published v1 checksum manifest mutated" "published v1 schema plus manifest mutation"

v1_file_mutation="$WORK/v1-file-mutation"
copy_tree "$v1_file_mutation"
target="$v1_file_mutation/schemas/envelope/v1/contract.schema.json"
printf '\n' >>"$target"
expect_red "$v1_file_mutation" "published v1 mutation" "published v1 file mutation"

bash "$GATE" >/dev/null || fail "the real tree did not return to green after mutation probes"
echo "contract-carried-set-test: ok — schema/core/template vocabulary, role roots, profiles, publication shape, duplicate builders/digests/inventories and both v1 immutability paths all red; production tree greens"
