#!/usr/bin/env bash
# Mutation teeth for check_work_checkpoint_schema.sh. Every red case changes a
# real P2 consumer in a disposable copy of the production vocabulary corpus.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check_work_checkpoint_schema.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() {
  echo "work-checkpoint-schema-test: FAIL — $*" >&2
  exit 1
}

# The gate ITSELF handles the private spec's absence — `checkDocsIfPresent`
# stats it and reports a reduced comparison surface. The fixture builder did
# not, and under `set -e` an absent source made `cp` abort the whole teeth run.
# In a public checkout that is not a hypothetical: `docs/**` is stripped at
# publish, so this line reddened `_harness-check` on public `main` while the
# candidate ref carrying the identical tree was green.
DOCS_SPEC="docs/features/archive/operational-confidence-2026-08/specs/02-reported-work-checkpoints.md"
docs_present=false
[ -f "$ROOT/$DOCS_SPEC" ] && docs_present=true

copy_tree() {
  local destination="$1"
  mkdir -p "$destination/schemas/envelope/v2" "$destination/schemas/templates/v2" \
    "$destination/internal/workreport" "$destination/internal/validate" \
    "$destination/docs/features/archive/operational-confidence-2026-08/specs"
  cp "$ROOT/schemas/envelope/v2/announcement.schema.json" "$destination/schemas/envelope/v2/announcement.schema.json"
  cp "$ROOT/schemas/templates/v2/announcement.md" "$destination/schemas/templates/v2/announcement.md"
  cp "$ROOT/internal/workreport/types.go" "$destination/internal/workreport/types.go"
  cp "$ROOT/internal/validate/work_checkpoint.go" "$destination/internal/validate/work_checkpoint.go"
  if [ "$docs_present" = true ]; then
    cp "$ROOT/$DOCS_SPEC" "$destination/$DOCS_SPEC"
  fi
}

expect_red() {
  local tree="$1" needle="$2" label="$3" output
  if output="$(WORK_CHECKPOINT_SCHEMA_ROOT="$tree" bash "$GATE" 2>&1)"; then
    fail "$label stayed green"
  fi
  if ! grep -Fq "$needle" <<<"$output"; then
    fail "$label red did not name '$needle': $output"
  fi
}

bash "$GATE" >/dev/null || fail "the real P2 vocabulary is not green"

public_projection="$WORK/public-projection"
copy_tree "$public_projection"
rm -rf "$public_projection/docs"
public_output="$(WORK_CHECKPOINT_SCHEMA_ROOT="$public_projection" bash "$GATE")" || \
  fail "the public projection without private docs is not green"
grep -Fq "public projection has no private normative spec" <<<"$public_output" || \
  fail "the public projection did not report its deliberately reduced comparison surface"

schema_mode="$WORK/schema-mode"
copy_tree "$schema_mode"
target="$schema_mode/schemas/envelope/v2/announcement.schema.json"
perl -0pi -e 's/"planning", "implementing"/"designing", "implementing"/' "$target"
expect_red "$schema_mode" "announcement.schema.json mode enum" "schema mode drift"

schema_wait="$WORK/schema-wait"
copy_tree "$schema_wait"
target="$schema_wait/schemas/envelope/v2/announcement.schema.json"
perl -0pi -e 's/"system", "human"/"service", "human"/' "$target"
expect_red "$schema_wait" "announcement.schema.json wait-kind enum" "schema wait-kind drift"

template_mode="$WORK/template-mode"
copy_tree "$template_mode"
target="$template_mode/schemas/templates/v2/announcement.md"
perl -0pi -e 's/<planning\|implementing/<designing|implementing/' "$target"
expect_red "$template_mode" "announcement.md mode vocabulary" "template mode drift"

workreport_wait="$WORK/workreport-wait"
copy_tree "$workreport_wait"
target="$workreport_wait/internal/workreport/types.go"
perl -0pi -e 's/WaitSystem\s+WaitKind = "system"/WaitSystem   WaitKind = "service"/' "$target"
expect_red "$workreport_wait" "workreport WaitKind constants" "workreport wait-kind drift"

workreport_mode_remove="$WORK/workreport-mode-remove"
copy_tree "$workreport_mode_remove"
target="$workreport_mode_remove/internal/workreport/types.go"
perl -0pi -e 's/\n\t\tModePlanning,//' "$target"
expect_red "$workreport_mode_remove" "workreport Mode Modes vocabulary" "workreport mode enumerator removal"

workreport_wait_valid="$WORK/workreport-wait-valid"
copy_tree "$workreport_wait_valid"
target="$workreport_wait_valid/internal/workreport/types.go"
perl -0pi -e 's/case WaitSystem, WaitHuman/case WaitHuman/' "$target"
expect_red "$workreport_wait_valid" "workreport WaitKind Valid behavior" "workreport wait runtime drift"

workreport_mode_widen="$WORK/workreport-mode-widen"
copy_tree "$workreport_mode_widen"
target="$workreport_mode_widen/internal/workreport/types.go"
perl -0pi -e 's/(ModePlanning Mode = "planning"\n)/$1\tModeDesigning Mode = "designing"\n/' "$target"
perl -0pi -e 's/(\n\t\tModePlanning,)/\n\t\tModeDesigning,$1/' "$target"
expect_red "$workreport_mode_widen" "workreport Mode Modes vocabulary" "workreport mode enumerator widening"

workreport_mode_disconnect="$WORK/workreport-mode-disconnect"
copy_tree "$workreport_mode_disconnect"
target="$workreport_mode_disconnect/internal/workreport/types.go"
perl -0pi -e 's/range Modes\(\)/range []Mode{ModePlanning}/' "$target"
expect_red "$workreport_mode_disconnect" "workreport Mode Valid behavior" "workreport Mode.Valid enumerator disconnect"

workreport_mode_extra_true="$WORK/workreport-mode-extra-true"
copy_tree "$workreport_mode_extra_true"
target="$workreport_mode_extra_true/internal/workreport/types.go"
perl -0pi -e 's/for _, mode := range Modes\(\) \{\n\t\tif m == mode/for _, m := range Modes() {\n\t\tif m == m/' "$target"
expect_red "$workreport_mode_extra_true" "workreport Mode Valid behavior" "workreport Mode.Valid runtime widening"

validator_mode="$WORK/validator-mode"
copy_tree "$validator_mode"
target="$validator_mode/internal/validate/work_checkpoint.go"
perl -0pi -e 's/WorkModePlanning\s+WorkMode = "planning"/WorkModePlanning     WorkMode = "designing"/' "$target"
expect_red "$validator_mode" "validator WorkMode constants" "validator mode drift"

if [ "$docs_present" = true ]; then
  docs_wait="$WORK/docs-wait"
  copy_tree "$docs_wait"
  target="$docs_wait/$DOCS_SPEC"
  perl -0pi -e 's/enum `system`, `human`/enum `service`, `human`/' "$target"
  expect_red "$docs_wait" "02-reported-work-checkpoints.md wait-kind vocabulary" "documentation wait-kind drift"
fi

bash "$GATE" >/dev/null || fail "the real tree did not return to green after mutation probes"
if [ "$docs_present" = true ]; then
  echo "work-checkpoint-schema-test: ok — schema/template/workreport/validator/docs mutations all red; production tree greens"
else
  echo "work-checkpoint-schema-test: ok — schema/template/workreport/validator mutations all red; production tree greens. SKIPPED the docs mutation — $DOCS_SPEC absent (public checkout), which is the same surface the gate itself reports reduced"
fi
