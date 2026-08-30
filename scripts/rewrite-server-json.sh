#!/bin/bash
set -euo pipefail

# Rewrites server.json's inert placeholders into the values ONE tag publishes,
# and refuses rather than emitting a half-substituted document.
#
# The committed file carries `version: 0.0.0`, `PLACEHOLDER` inside every
# identifier URL and an all-zero `fileSha256` (spec 05 §T2). Those are inert BY
# DESIGN: the MCP registry requires a unique version per publication and freezes
# metadata once published, so a value that is right for one tag must not sit in
# the tree pretending to be right for the next.
#
# WHY THE DIGESTS COME FROM SHA256SUMS. The registry does NOT validate
# `fileSha256`, but MCP CLIENTS DO, before installing. A wrong digest therefore
# publishes silently and cannot be corrected except by burning a version. The
# only source that cannot be wrong is the release's own checksum set over the
# exact published bytes — which release.yml builds after the bundles land in
# dist/, then cosign-signs and attests. Hashing the bundles a second time here
# would introduce a second answer to a question that already has a signed one.
#
# The release cohort is deliberately NOT that source: build-release-cohort.sh
# describes four kinds, a bundle is none of them, and an unknown kind fails
# ParseCohort for every ALREADY-INSTALLED binary (internal/release/cohort.go:29
# — the document an older binary fetches before it trusts a release).

TAG="${1:?usage: rewrite-server-json.sh TAG SHA256SUMS [SERVER_JSON]}"
SUMS="${2:?usage: rewrite-server-json.sh TAG SHA256SUMS [SERVER_JSON]}"
SERVER_JSON="${3:-server.json}"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
test -f "$SUMS" || { echo "checksum set not found: $SUMS" >&2; exit 2; }
test -f "$SERVER_JSON" || { echo "not found: $SERVER_JSON" >&2; exit 2; }

version="${TAG#v}"

# Every identifier names its own asset; the digest is looked up by that name.
# A bundle the checksum set does not mention is a REFUSAL, not a zero.
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
cp "$SERVER_JSON" "$tmp"

count="$(jq '.packages | length' "$tmp")"
test "$count" -gt 0 || { echo "$SERVER_JSON declares no packages" >&2; exit 2; }

for i in $(seq 0 $((count - 1))); do
  identifier="$(jq -r ".packages[$i].identifier" "$tmp")"
  asset="${identifier##*/}"
  digest="$(awk -v want="$asset" '$2 == want { print $1 }' "$SUMS" | head -1)"
  if [ -z "$digest" ]; then
    echo "$SUMS names no digest for $asset (declared by packages[$i])" >&2
    exit 1
  fi
  jq \
    --arg tag "$TAG" \
    --arg digest "$digest" \
    --argjson i "$i" \
    '.packages[$i].identifier |= sub("/PLACEHOLDER/"; "/" + $tag + "/")
     | .packages[$i].fileSha256 = $digest' \
    "$tmp" > "${tmp}.next"
  mv "${tmp}.next" "$tmp"
done

jq --arg version "$version" '.version = $version' "$tmp" > "${tmp}.next"
mv "${tmp}.next" "$tmp"

# FAIL RATHER THAN PUBLISH. A surviving placeholder or an all-zero digest means
# the substitution did not reach a field, and publishing it is unfixable.
if grep -q 'PLACEHOLDER' "$tmp"; then
  echo "refusing: PLACEHOLDER survives the substitution in $SERVER_JSON" >&2
  exit 1
fi
if jq -e '.packages[] | select(.fileSha256 == "0000000000000000000000000000000000000000000000000000000000000000")' "$tmp" >/dev/null; then
  echo "refusing: an all-zero fileSha256 survives the substitution" >&2
  exit 1
fi
if [ "$(jq -r '.version' "$tmp")" = "0.0.0" ]; then
  echo "refusing: version is still the inert 0.0.0" >&2
  exit 1
fi

mv "$tmp" "$SERVER_JSON"
trap - EXIT
echo "server.json: version $version, $count package identifier(s) and digest(s) substituted for $TAG"
