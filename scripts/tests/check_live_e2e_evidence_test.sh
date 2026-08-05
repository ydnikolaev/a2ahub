#!/usr/bin/env bash
# Teeth for VG-OC-24. Each probe mutates the same complete release manifest.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check_live_e2e_evidence.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() {
  echo "live-e2e-evidence-test: FAIL — $*" >&2
  exit 1
}

expect_red() {
  local file="$1" needle="$2" label="$3" output
  if output="$(bash "$GATE" "$file" 2>&1)"; then
    fail "$label stayed green"
  fi
  if ! grep -Fq "$needle" <<<"$output"; then
    fail "$label red did not name '$needle': $output"
  fi
}

copy_bundle() {
  local destination="$1"
  mkdir -p "$destination"
  cp "$VALID" "$destination/manifest.json"
  cp -R "$WORK/logs" "$destination/logs"
  cp -R "$WORK/evidence" "$destination/evidence"
}

VALID="$WORK/valid.json"
mkdir -p "$WORK/logs"
VERIFY_ROOT="$WORK/verification-checkout"
VERIFY_ID="checkout-$(printf '%s' "$VERIFY_ROOT" | shasum -a 256 | awk '{print substr($1,1,24)}')"

# spec 09 D-7: the retained transcript's own LOGIC_TIER_ROWS_SHA256 marker —
# the digest of every catalogue row the logic tier judged. Obtained from Go
# (TestLogicTierRowsDigestBridge, evidence_test.go) rather than recomputed in
# bash: it is the SAME function verifyCandidateCheckLog (evidence.go) uses to
# recompute the expected value at validation time, so this fixture can only
# ever be as correct as the real gate's own arithmetic — never a second,
# independently-typed copy that could silently drift from it.
LOGIC_TIER_DIGEST="$(
  cd "$ROOT" && A2A_LIVE_E2E_PRINT_LOGIC_TIER_DIGEST=1 \
    go test ./internal/livee2e/ -run '^TestLogicTierRowsDigestBridge$' -count=1 -v |
    grep -E '^sha256:[0-9a-f]{64}$'
)"
[ -n "$LOGIC_TIER_DIGEST" ] || fail "could not obtain LOGIC_TIER_ROWS_SHA256 from TestLogicTierRowsDigestBridge"

cat >"$WORK/logs/make-check.log" <<EOF
CHECKOUT_ROOT=$VERIFY_ROOT
CANDIDATE_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
CANDIDATE_TREE=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
CANDIDATE_TAG=v0.19.0
CHECKOUT_DETACHED=true
INDEX_CLEAN=true
WORKTREE_CLEAN=true
UNTRACKED_CLEAN=true
WEB_DEPS_READY=true
LOGIC_TIER_ROWS_SHA256=$LOGIC_TIER_DIGEST
EXIT=0
EOF
CHECK_DIGEST="sha256:$(shasum -a 256 "$WORK/logs/make-check.log" | awk '{print $1}')"
cat >"$VALID" <<'JSON'
{
  "manifest_version": "live-e2e-evidence/v1",
  "candidate": {
    "sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "tree": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    "version": "v0.19.0",
    "verification_checkout": {"id":"VERIFICATION_CHECKOUT_ID","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tree":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","detached":true,"index_clean":true,"worktree_clean":true,"untracked_clean":true},
    "execution_checkout": {"id":"execution-checkout","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tree":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","detached":true,"index_clean":true,"worktree_clean":true,"untracked_clean":true},
    "built_source_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "binary": {"sha256":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","version":"0.19.0","build_info":{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","modified":false}},
    "embedded_template_floor": "0.19.0"
  },
  "candidate_check": {"log_path":"logs/make-check.log","log_sha256":"CANDIDATE_CHECK_SHA","passed":true},
  "scenarios": [
    {"scenario_id":"LE-OC-01","branch":"baseline","started_at":"2026-08-03T10:00:00Z","ended_at":"2026-08-03T10:01:00Z","surface_ids":["cli","github"],"repository_id":"public/space","command_family":"a2a submit","outcome":"pass","artifact_refs":[],"pr_refs":["https://github.com/public/space/pull/1"],"cleanup_status":"complete","log_path":"evidence/logs/le-oc-01.log","log_sha256":"SCENARIO_LE_OC_01_SHA","limitations":[],"live_manifest_floor":"0.19.0","live_manifest_commit":"cccccccccccccccccccccccccccccccccccccccc","assertions":[{"id":"receipt","outcome":"pass"},{"id":"provenance","outcome":"pass"},{"id":"fresh-fold","outcome":"pass"},{"id":"no-mismatch-flag","outcome":"pass"}]},
    {"scenario_id":"LE-OC-02","branch":"baseline","started_at":"2026-08-03T10:00:00Z","ended_at":"2026-08-03T10:01:00Z","surface_ids":["cli","github"],"repository_id":"public/space","command_family":"a2a work","outcome":"pass","artifact_refs":[],"pr_refs":["https://github.com/public/space/pull/2"],"cleanup_status":"complete","log_path":"evidence/logs/le-oc-02.log","log_sha256":"SCENARIO_LE_OC_02_SHA","limitations":[],"live_manifest_floor":"0.19.0","live_manifest_commit":"cccccccccccccccccccccccccccccccccccccccc","assertions":[{"id":"announcement-v2","outcome":"pass"},{"id":"durable-checkpoints","outcome":"pass"},{"id":"cross-clone-projection","outcome":"pass"},{"id":"local-heartbeat-local-only","outcome":"pass"}]},
    {"scenario_id":"LE-OC-03","branch":"baseline","started_at":"2026-08-03T10:00:00Z","ended_at":"2026-08-03T10:01:00Z","surface_ids":["static-html","localserver"],"repository_id":"public/space","command_family":"a2a serve","outcome":"pass","artifact_refs":[],"pr_refs":[],"cleanup_status":"complete","log_path":"evidence/logs/le-oc-03.log","log_sha256":"SCENARIO_LE_OC_03_SHA","limitations":[],"live_manifest_floor":"0.19.0","live_manifest_commit":"cccccccccccccccccccccccccccccccccccccccc","assertions":[{"id":"snapshot-revision-equal","outcome":"pass"},{"id":"operational-rows-equal","outcome":"pass"},{"id":"sse-revision-only","outcome":"pass"},{"id":"conditional-snapshot","outcome":"pass"}]},
    {"scenario_id":"LE-OC-04","branch":"provider-setting","started_at":"2026-08-03T10:00:00Z","ended_at":"2026-08-03T10:01:00Z","surface_ids":["cli","github"],"repository_id":"public/space","command_family":"a2a submit","outcome":"pass","artifact_refs":[],"pr_refs":["https://github.com/public/space/pull/4"],"cleanup_status":"complete","log_path":"evidence/logs/le-oc-04.log","log_sha256":"SCENARIO_LE_OC_04_SHA","limitations":[],"live_manifest_floor":"0.19.0","live_manifest_commit":"cccccccccccccccccccccccccccccccccccccccc","assertions":[{"id":"same-pr-retry","outcome":"pass"}]},
    {"scenario_id":"LE-OC-05","branch":"baseline","started_at":"2026-08-03T10:00:00Z","ended_at":"2026-08-03T10:01:00Z","surface_ids":["cli","github"],"repository_id":"public/space","command_family":"a2a doctor","outcome":"pass","artifact_refs":[],"pr_refs":[],"cleanup_status":"not-required","log_path":"evidence/logs/le-oc-05.log","log_sha256":"SCENARIO_LE_OC_05_SHA","limitations":[],"live_manifest_floor":"0.19.0","live_manifest_commit":"cccccccccccccccccccccccccccccccccccccccc","assertions":[{"id":"visibility-public","outcome":"pass"},{"id":"visibility-higher-classification","outcome":"pass"}]},
    {"scenario_id":"LE-OC-06","branch":"baseline","started_at":"2026-08-03T10:00:00Z","ended_at":"2026-08-03T10:01:00Z","surface_ids":["cli","github"],"repository_id":"public/space","command_family":"a2a contract","outcome":"pass","artifact_refs":["XC-public-demo"],"pr_refs":["https://github.com/public/space/pull/6"],"cleanup_status":"complete","log_path":"evidence/logs/le-oc-06.log","log_sha256":"SCENARIO_LE_OC_06_SHA","limitations":[],"live_manifest_floor":"0.19.0","live_manifest_commit":"cccccccccccccccccccccccccccccccccccccccc","assertions":[{"id":"preflight-write-free","outcome":"pass"},{"id":"contract-v2","outcome":"pass"},{"id":"event-v2","outcome":"pass"},{"id":"publication-intent","outcome":"pass"},{"id":"contract-set-v2","outcome":"pass"},{"id":"plan-publish-equal","outcome":"pass"}]},
    {"scenario_id":"LE-OC-07","branch":"baseline","started_at":"2026-08-03T10:00:00Z","ended_at":"2026-08-03T10:01:00Z","surface_ids":["cli","github"],"repository_id":"public/space","command_family":"a2a contract","outcome":"pass","artifact_refs":["XC-public-demo"],"pr_refs":[],"cleanup_status":"complete","log_path":"evidence/logs/le-oc-07.log","log_sha256":"SCENARIO_LE_OC_07_SHA","limitations":[],"live_manifest_floor":"0.19.0","live_manifest_commit":"cccccccccccccccccccccccccccccccccccccccc","assertions":[{"id":"historical-bytes-equal","outcome":"pass"},{"id":"historical-digest-equal","outcome":"pass"},{"id":"idempotent-rerun","outcome":"pass"},{"id":"divergent-refusal","outcome":"pass"}]},
    {"scenario_id":"LE-OC-08","branch":"baseline","started_at":"2026-08-03T10:00:00Z","ended_at":"2026-08-03T10:01:00Z","surface_ids":["cli","github"],"repository_id":"public/space","command_family":"a2a contract","outcome":"pass","artifact_refs":["XC-public-schema"],"pr_refs":[],"cleanup_status":"complete","log_path":"evidence/logs/le-oc-08.log","log_sha256":"SCENARIO_LE_OC_08_SHA","limitations":[],"live_manifest_floor":"0.19.0","live_manifest_commit":"cccccccccccccccccccccccccccccccccccccccc","assertions":[{"id":"self-suite","outcome":"pass"},{"id":"known-valid-pass","outcome":"pass"},{"id":"known-invalid-stable-locations","outcome":"pass"}]},
    {"scenario_id":"LE-OC-08","branch":"unsupported-provider","started_at":"2026-08-03T10:00:00Z","ended_at":"2026-08-03T10:01:00Z","surface_ids":["cli","github"],"repository_id":"public/space","command_family":"a2a contract","outcome":"unverified","artifact_refs":["XC-public-schema"],"pr_refs":[],"cleanup_status":"complete","log_path":"evidence/logs/le-oc-08-unsupported.log","log_sha256":"SCENARIO_LE_OC_08_UNSUPPORTED_SHA","limitations":["provider exposes no safe unsupported-format probe"],"live_manifest_floor":"0.19.0","live_manifest_commit":"cccccccccccccccccccccccccccccccccccccccc","assertions":[{"id":"unsupported-reported","outcome":"unverified"}]}
  ]
}
JSON

perl -0pi -e "s/VERIFICATION_CHECKOUT_ID/$VERIFY_ID/; s/CANDIDATE_CHECK_SHA/$CHECK_DIGEST/" "$VALID"

write_scenario_log() {
  local path="$1" scenario_id="$2" branch="$3" outcome="$4" assertions="$5"
  mkdir -p "$(dirname "$path")"
  printf '%s\n' "{\"evidence_version\":\"live-e2e-scenario-log/v1\",\"candidate_sha\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"candidate_tree\":\"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"binary_sha256\":\"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\",\"scenario_id\":\"$scenario_id\",\"branch\":\"$branch\",\"started_at\":\"2026-08-03T10:00:00Z\",\"ended_at\":\"2026-08-03T10:01:00Z\",\"outcome\":\"$outcome\",\"assertions\":$assertions}" >"$path"
}

bind_scenario_log() {
  local marker="$1" path="$2" digest
  digest="sha256:$(shasum -a 256 "$path" | awk '{print $1}')"
  perl -0pi -e "s/$marker/$digest/" "$VALID"
}

write_scenario_log "$WORK/evidence/logs/le-oc-01.log" "LE-OC-01" "baseline" "pass" '[{"id":"receipt","outcome":"pass"},{"id":"provenance","outcome":"pass"},{"id":"fresh-fold","outcome":"pass"},{"id":"no-mismatch-flag","outcome":"pass"}]'
write_scenario_log "$WORK/evidence/logs/le-oc-02.log" "LE-OC-02" "baseline" "pass" '[{"id":"announcement-v2","outcome":"pass"},{"id":"durable-checkpoints","outcome":"pass"},{"id":"cross-clone-projection","outcome":"pass"},{"id":"local-heartbeat-local-only","outcome":"pass"}]'
write_scenario_log "$WORK/evidence/logs/le-oc-03.log" "LE-OC-03" "baseline" "pass" '[{"id":"snapshot-revision-equal","outcome":"pass"},{"id":"operational-rows-equal","outcome":"pass"},{"id":"sse-revision-only","outcome":"pass"},{"id":"conditional-snapshot","outcome":"pass"}]'
write_scenario_log "$WORK/evidence/logs/le-oc-04.log" "LE-OC-04" "provider-setting" "pass" '[{"id":"same-pr-retry","outcome":"pass"}]'
write_scenario_log "$WORK/evidence/logs/le-oc-05.log" "LE-OC-05" "baseline" "pass" '[{"id":"visibility-public","outcome":"pass"},{"id":"visibility-higher-classification","outcome":"pass"}]'
write_scenario_log "$WORK/evidence/logs/le-oc-06.log" "LE-OC-06" "baseline" "pass" '[{"id":"preflight-write-free","outcome":"pass"},{"id":"contract-v2","outcome":"pass"},{"id":"event-v2","outcome":"pass"},{"id":"publication-intent","outcome":"pass"},{"id":"contract-set-v2","outcome":"pass"},{"id":"plan-publish-equal","outcome":"pass"}]'
write_scenario_log "$WORK/evidence/logs/le-oc-07.log" "LE-OC-07" "baseline" "pass" '[{"id":"historical-bytes-equal","outcome":"pass"},{"id":"historical-digest-equal","outcome":"pass"},{"id":"idempotent-rerun","outcome":"pass"},{"id":"divergent-refusal","outcome":"pass"}]'
write_scenario_log "$WORK/evidence/logs/le-oc-08.log" "LE-OC-08" "baseline" "pass" '[{"id":"self-suite","outcome":"pass"},{"id":"known-valid-pass","outcome":"pass"},{"id":"known-invalid-stable-locations","outcome":"pass"}]'
write_scenario_log "$WORK/evidence/logs/le-oc-08-unsupported.log" "LE-OC-08" "unsupported-provider" "unverified" '[{"id":"unsupported-reported","outcome":"unverified"}]'

bind_scenario_log SCENARIO_LE_OC_01_SHA "$WORK/evidence/logs/le-oc-01.log"
bind_scenario_log SCENARIO_LE_OC_02_SHA "$WORK/evidence/logs/le-oc-02.log"
bind_scenario_log SCENARIO_LE_OC_03_SHA "$WORK/evidence/logs/le-oc-03.log"
bind_scenario_log SCENARIO_LE_OC_04_SHA "$WORK/evidence/logs/le-oc-04.log"
bind_scenario_log SCENARIO_LE_OC_05_SHA "$WORK/evidence/logs/le-oc-05.log"
bind_scenario_log SCENARIO_LE_OC_06_SHA "$WORK/evidence/logs/le-oc-06.log"
bind_scenario_log SCENARIO_LE_OC_07_SHA "$WORK/evidence/logs/le-oc-07.log"
bind_scenario_log SCENARIO_LE_OC_08_SHA "$WORK/evidence/logs/le-oc-08.log"
bind_scenario_log SCENARIO_LE_OC_08_UNSUPPORTED_SHA "$WORK/evidence/logs/le-oc-08-unsupported.log"

make --no-print-directory -C "$ROOT" live-e2e-evidence EVIDENCE="$VALID" >/dev/null || fail "complete control manifest is not green through the release target"

missing_row="$WORK/missing-row.json"
cp "$VALID" "$missing_row"
# LE-OC-03/baseline is live-required under D-7 (TierLogic with the
# "sse-revision-only" ProviderAssertions carve-out — plan D-2's "progression
# is not judgement"), unlike LE-OC-02/baseline, whose proof is now the
# logic-tier marker below, not a bundle row.
perl -0pi -e 's/    \{"scenario_id":"LE-OC-03".*?\},\n//' "$missing_row"
expect_red "$missing_row" "mandatory PASS row LE-OC-03/baseline" "missing mandatory row"

missing_provider_row="$WORK/missing-provider-row.json"
cp "$VALID" "$missing_provider_row"
perl -0pi -e 's/    \{"scenario_id":"LE-OC-04".*?\},\n//' "$missing_provider_row"
expect_red "$missing_provider_row" "scenarios must include LE-OC-04/provider-setting as pass or explicitly unverified" "missing provider row"

missing_carveout_assertion="$WORK/missing-carveout-assertion.json"
cp "$VALID" "$missing_carveout_assertion"
perl -0pi -e 's/\{"id":"sse-revision-only","outcome":"pass"\},//' "$missing_carveout_assertion"
expect_red "$missing_carveout_assertion" "assertions must include sse-revision-only" "missing carve-out assertion"

no_logic_marker="$WORK/no-logic-marker"
copy_bundle "$no_logic_marker"
no_logic_marker_log="$no_logic_marker/logs/make-check.log"
no_logic_marker_old="sha256:$(shasum -a 256 "$no_logic_marker_log" | awk '{print $1}')"
perl -0pi -e 's/^LOGIC_TIER_ROWS_SHA256=.*\n//m' "$no_logic_marker_log"
no_logic_marker_new="sha256:$(shasum -a 256 "$no_logic_marker_log" | awk '{print $1}')"
perl -0pi -e "s/$no_logic_marker_old/$no_logic_marker_new/" "$no_logic_marker/manifest.json"
expect_red "$no_logic_marker/manifest.json" "exactly one LOGIC_TIER_ROWS_SHA256 marker" "missing logic tier marker"

shrunk_logic_marker="$WORK/shrunk-logic-marker"
copy_bundle "$shrunk_logic_marker"
shrunk_logic_marker_log="$shrunk_logic_marker/logs/make-check.log"
shrunk_logic_marker_old="sha256:$(shasum -a 256 "$shrunk_logic_marker_log" | awk '{print $1}')"
# A well-formed but WRONG digest — the shape a logic lane whose dispatch
# table silently drops a catalogue row (spec 46 postmortem) would produce.
bogus_digest="sha256:$(printf '%064d' 0)"
perl -0pi -e "s/^LOGIC_TIER_ROWS_SHA256=.*\$/LOGIC_TIER_ROWS_SHA256=$bogus_digest/m" "$shrunk_logic_marker_log"
shrunk_logic_marker_new="sha256:$(shasum -a 256 "$shrunk_logic_marker_log" | awk '{print $1}')"
perl -0pi -e "s/$shrunk_logic_marker_old/$shrunk_logic_marker_new/" "$shrunk_logic_marker/manifest.json"
expect_red "$shrunk_logic_marker/manifest.json" "does not match the catalogue's own logic-tier row set" "logic tier marker mismatch"

missing_cleanup="$WORK/missing-cleanup.json"
cp "$VALID" "$missing_cleanup"
perl -0pi -e 's/,"cleanup_status":"complete"//' "$missing_cleanup"
expect_red "$missing_cleanup" "cleanup_status is required" "missing cleanup"

missing_attestation="$WORK/missing-attestation.json"
cp "$VALID" "$missing_attestation"
perl -0pi -e 's/,"untracked_clean":true//' "$missing_attestation"
expect_red "$missing_attestation" "untracked_clean attestation is required" "missing checkout attestation"

floor_mismatch="$WORK/floor-mismatch.json"
cp "$VALID" "$floor_mismatch"
perl -0pi -e 's/"embedded_template_floor": "0\.19\.0"/"embedded_template_floor": "0.18.0"/' "$floor_mismatch"
expect_red "$floor_mismatch" "embedded_template_floor must be" "candidate floor mismatch"

secret="$WORK/secret.json"
cp "$VALID" "$secret"
perl -0pi -e 's#"repository_id":"public/space"#"repository_id":"https://user:ghp_abcdefghijklmnopqrstuvwxyz@example.com/space"#' "$secret"
expect_red "$secret" "secret-shaped content" "secret-bearing identifier"

private_payload="$WORK/private-payload.json"
cp "$VALID" "$private_payload"
perl -0pi -e 's/\{\n  "manifest_version"/\{\n  "private_payload":{"body":"not publishable"},\n  "manifest_version"/' "$private_payload"
expect_red "$private_payload" "private_payload is forbidden" "private payload"

malformed_unverified="$WORK/malformed-unverified.json"
cp "$VALID" "$malformed_unverified"
perl -0pi -e 's/("scenario_id":"LE-OC-04"[^\n]*?"outcome":)"pass"/$1"unverified"/' "$malformed_unverified"
expect_red "$malformed_unverified" "limitations must explain an unverified optional branch" "malformed optional unverified row"

missing_scenario_dir="$WORK/missing-scenario"
copy_bundle "$missing_scenario_dir"
rm "$missing_scenario_dir/evidence/logs/le-oc-01.log"
expect_red "$missing_scenario_dir/manifest.json" "log_path stat" "missing scenario log"

tampered_scenario_dir="$WORK/tampered-scenario"
copy_bundle "$tampered_scenario_dir"
printf 'tampered\n' >"$tampered_scenario_dir/evidence/logs/le-oc-01.log"
expect_red "$tampered_scenario_dir/manifest.json" "log_sha256 does not match" "tampered scenario log"

oversized_scenario_dir="$WORK/oversized-scenario"
copy_bundle "$oversized_scenario_dir"
truncate -s 1048577 "$oversized_scenario_dir/evidence/logs/le-oc-01.log"
expect_red "$oversized_scenario_dir/manifest.json" "no larger than 1048576 bytes" "oversized scenario log"

traversal_scenario="$WORK/traversal-scenario.json"
cp "$VALID" "$traversal_scenario"
perl -0pi -e 's#"log_path":"evidence/logs/le-oc-01.log"#"log_path":"../outside.log"#' "$traversal_scenario"
expect_red "$traversal_scenario" "clean repository-relative path" "scenario log path traversal"

duplicate_scenario_log="$WORK/duplicate-scenario-log.json"
cp "$VALID" "$duplicate_scenario_log"
perl -0pi -e 's#"log_path":"evidence/logs/le-oc-02.log"#"log_path":"evidence/logs/le-oc-01.log"#' "$duplicate_scenario_log"
expect_red "$duplicate_scenario_log" "duplicates another scenario log path" "duplicate scenario log path"

unproven_pass="$WORK/unproven-pass.json"
cp "$VALID" "$unproven_pass"
perl -0pi -e 's/("assertions":\[\{"id":"receipt","outcome":)"pass"/$1"unverified"/' "$unproven_pass"
expect_red "$unproven_pass" "must pass to prove a passing scenario" "unproven PASS assertion"

mismatched_scenario_dir="$WORK/mismatched-scenario"
copy_bundle "$mismatched_scenario_dir"
mismatched_log="$mismatched_scenario_dir/evidence/logs/le-oc-01.log"
old_digest="$(shasum -a 256 "$mismatched_log" | awk '{print $1}')"
perl -0pi -e 's/"candidate_tree":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"/"candidate_tree":"ffffffffffffffffffffffffffffffffffffffff"/' "$mismatched_log"
new_digest="$(shasum -a 256 "$mismatched_log" | awk '{print $1}')"
perl -0pi -e "s/sha256:$old_digest/sha256:$new_digest/" "$mismatched_scenario_dir/manifest.json"
expect_red "$mismatched_scenario_dir/manifest.json" "not the canonical candidate-bound scenario record" "mismatched candidate-bound scenario log"

missing_log_dir="$WORK/missing-log"
mkdir -p "$missing_log_dir"
cp "$VALID" "$missing_log_dir/manifest.json"
expect_red "$missing_log_dir/manifest.json" "candidate_check.log_path stat" "missing candidate check log"

tampered_log_dir="$WORK/tampered-log"
mkdir -p "$tampered_log_dir/logs"
cp "$VALID" "$tampered_log_dir/manifest.json"
printf 'tampered\n' >"$tampered_log_dir/logs/make-check.log"
expect_red "$tampered_log_dir/manifest.json" "log_sha256 does not match" "tampered candidate check log"

bash "$GATE" "$VALID" >/dev/null || fail "control manifest did not return to green"
echo "live-e2e-evidence-test: ok — manifest drift plus missing, tampered, oversized, traversing, duplicate, mismatched, and unproven scenario evidence all red, including a missing/incomplete provider row, a missing carve-out assertion, and a missing/shrunk logic-tier marker (spec 09 D-7); complete bound bundle greens"
