#!/bin/bash
set -euo pipefail

DIST="${1:?usage: build-release-cohort.sh DIST VERSION APPLE_TEAM_ID}"
VERSION="${2:?usage: build-release-cohort.sh DIST VERSION APPLE_TEAM_ID}"
APPLE_TEAM_ID="${3:?usage: build-release-cohort.sh DIST VERSION APPLE_TEAM_ID}"
PROTOCOL_VERSION=1
MAC_ASSET="a2a-notifier-${VERSION}.zip"
VSCODE_ASSET="a2ahub-notifications.vsix"
NOTES_ASSET="release-notes-${VERSION}.yaml"
OUT="${DIST}/release-cohort.json"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
test -f "${DIST}/${MAC_ASSET}"
test -f "${DIST}/${VSCODE_ASSET}"
test -f "${DIST}/${NOTES_ASSET}"

digest() {
  if command -v sha256sum >/dev/null; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

components="$(mktemp)"
trap 'rm -f "$components"' EXIT
printf '[]' > "$components"

for asset in "${DIST}"/a2a-darwin-* "${DIST}"/a2a-linux-* "${DIST}"/a2a-windows-*; do
  test -f "$asset" || continue
  jq \
    --arg name "$(basename "$asset")" \
    --arg sha "$(digest "$asset")" \
    --arg version "$VERSION" \
    --argjson protocol "$PROTOCOL_VERSION" \
    '. + [{kind:"cli",name:$name,sha256:$sha,product_version:$version,protocol_version:$protocol}]' \
    "$components" > "${components}.next"
  mv "${components}.next" "$components"
done

test "$(jq 'length' "$components")" -gt 0
jq \
  --arg name "$MAC_ASSET" --arg sha "$(digest "${DIST}/${MAC_ASSET}")" \
  --arg version "$VERSION" --arg team "$APPLE_TEAM_ID" \
  --argjson protocol "$PROTOCOL_VERSION" \
  '. + [{kind:"macos",name:$name,sha256:$sha,product_version:$version,protocol_version:$protocol,bundle_id:"io.a2ahub.notifier",team_id:$team}]' \
  "$components" > "${components}.next"
mv "${components}.next" "$components"

jq \
  --arg name "$VSCODE_ASSET" --arg sha "$(digest "${DIST}/${VSCODE_ASSET}")" \
  --arg version "$VERSION" --argjson protocol "$PROTOCOL_VERSION" \
  '. + [{kind:"vscode",name:$name,sha256:$sha,product_version:$version,protocol_version:$protocol,publisher:"a2ahub",extension_id:"a2ahub.a2ahub-notifications"}]' \
  "$components" > "${components}.next"
mv "${components}.next" "$components"

jq \
  --arg name "$NOTES_ASSET" --arg sha "$(digest "${DIST}/${NOTES_ASSET}")" \
  --arg version "$VERSION" --argjson protocol "$PROTOCOL_VERSION" \
  '. + [{kind:"release-notes",name:$name,sha256:$sha,product_version:$version,protocol_version:$protocol,schema:"release-notes/v1"}]' \
  "$components" > "${components}.next"
mv "${components}.next" "$components"

jq -n \
  --arg version "$VERSION" \
  --argjson protocol "$PROTOCOL_VERSION" \
  --slurpfile components "$components" \
  '{schema:"release-cohort/v1",product_version:$version,protocol:{min:$protocol,max:$protocol},components:$components[0]}' \
  > "$OUT"

echo "$OUT"
