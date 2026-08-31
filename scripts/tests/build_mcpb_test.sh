#!/usr/bin/env bash
# EXECUTES scripts/build-mcpb.sh, which is the whole point of this file.
#
# That script had never run once before v0.26.1. It is reached only by
# release.yml's `cohort` job, which fires only on a tag, and it broke two
# consecutive releases in two different ways before anything executed it:
#
#   v0.26.0 — tracked 100644, invoked by bare path: exit 126, Permission
#             denied. Now refused by `make workflow-lint`.
#   v0.26.1 — the zip runs from inside the staging directory and was handed a
#             DIST-RELATIVE output path, so it resolved against the staging
#             dir: "zip error: Could not create output file".
#
# Both releases therefore shipped with no release-cohort.json, no
# release-notes asset and no .mcpb bundle. The second defect was invisible to
# every gate precisely because the first one stopped the script before it.
#
# So this runs it FOR REAL, from a different working directory, with a
# RELATIVE dist path — the exact shape release.yml uses and the exact shape
# that broke.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SCRIPT="$ROOT/scripts/build-mcpb.sh"

fail() { echo "build_mcpb_test: FAIL — $*" >&2; exit 1; }

command -v jq >/dev/null || { echo "build_mcpb_test: skip — jq absent"; exit 0; }
command -v zip >/dev/null || { echo "build_mcpb_test: skip — zip absent"; exit 0; }
command -v unzip >/dev/null || { echo "build_mcpb_test: skip — unzip absent"; exit 0; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/dist"
printf '#!/bin/sh\necho fixture\n' >"$work/dist/a2a-darwin-arm64"
chmod +x "$work/dist/a2a-darwin-arm64"
printf 'MZ fixture\n' >"$work/dist/a2a-windows-amd64.exe"

# (1) THE REGRESSION ITSELF: relative DIST, invoked from elsewhere.
( cd "$work" && bash "$SCRIPT" dist 9.9.9 >/dev/null ) \
  || fail "a relative DIST path from another working directory did not build (this is the v0.26.1 defect)"

for want in a2a-darwin-arm64 a2a-windows-amd64; do
  [ -f "$work/dist/$want.mcpb" ] \
    || fail "$want.mcpb was not written INTO the dist directory the caller named"
  unzip -l "$work/dist/$want.mcpb" >/dev/null 2>&1 \
    || fail "$want.mcpb is not a readable zip"
done

# (2) The bundle carries what a host needs, and the windows one is .exe.
unzip -l "$work/dist/a2a-darwin-arm64.mcpb" | grep -q 'server/a2a$' \
  || fail "the darwin bundle does not carry server/a2a"
unzip -p "$work/dist/a2a-darwin-arm64.mcpb" manifest.json \
  | jq -e '.version == "9.9.9" and .server.entry_point == "server/a2a" and .server.mcp_config.command == "server/a2a"' >/dev/null \
  || fail "the darwin manifest does not carry the version and entry point it was given"
unzip -p "$work/dist/a2a-windows-amd64.mcpb" manifest.json \
  | jq -e '.server.entry_point == "server/a2a.exe" and .server.mcp_config.command == "server/a2a.exe"' >/dev/null \
  || fail "the windows manifest does not point at the .exe the raw archive actually names"

# (3) A2A_PROJECT_ROOT is what makes the bundle able to start at all — MCPB
#     has no cwd field, so this env var is the only way the server learns
#     which directory is the project root. Assert the env entry exists AND
#     that the user_config key it interpolates is one the manifest declares;
#     an undeclared key interpolates to nothing and the server dies on its
#     first config read, silently.
manifest="$(unzip -p "$work/dist/a2a-darwin-arm64.mcpb" manifest.json)"
printf '%s' "$manifest" \
  | jq -e '
      (.server.mcp_config.env.A2A_PROJECT_ROOT // "")
      | capture("^\\$\\{user_config\\.(?<k>[A-Za-z0-9_]+)\\}$").k' >/dev/null \
  || fail "the manifest does not set A2A_PROJECT_ROOT from a user_config value"
key="$(printf '%s' "$manifest" | jq -r '.server.mcp_config.env.A2A_PROJECT_ROOT | capture("^\\$\\{user_config\\.(?<k>[A-Za-z0-9_]+)\\}$").k')"
printf '%s' "$manifest" | jq -e --arg k "$key" '.user_config | has($k)' >/dev/null \
  || fail "A2A_PROJECT_ROOT interpolates user_config.$key, which the manifest does not declare"
printf '%s' "$manifest" | jq -e --arg k "$key" '.user_config[$k].type == "directory"' >/dev/null \
  || fail "user_config.$key is not typed \"directory\", so a host will not offer a folder picker for the project root"

# (4) An empty dist must refuse rather than report success over nothing.
mkdir -p "$work/empty"
if ( cd "$work" && bash "$SCRIPT" empty 9.9.9 >/dev/null 2>&1 ); then
  fail "a dist carrying no platform binary reported success"
fi

# (5) A DIST that is not a directory is a caller error, not a zero-bundle run.
if bash "$SCRIPT" "$work/dist/a2a-darwin-arm64" 9.9.9 >/dev/null 2>&1; then
  fail "a DIST that is not a directory was accepted"
fi

echo "build_mcpb_test: ok — the script RUNS (relative DIST from another cwd, the v0.26.1 defect), writes readable bundles into the caller's dist, carries server/a2a and the windows .exe variant, declares and substitutes A2A_PROJECT_ROOT, and refuses both an empty dist and a non-directory DIST"
