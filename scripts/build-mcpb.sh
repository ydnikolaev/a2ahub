#!/bin/bash
set -euo pipefail

# Assembles one .mcpb bundle (a zip holding manifest.json plus the server
# binary, per anthropics/mcpb MANIFEST.md) for every a2a-<os>-<arch>
# platform binary DIST already carries — post-`dist/` work in the same
# class as build-release-cohort.sh, modeled on its shape: positional
# DIST/VERSION args, a `test -f ... || continue` per-platform skip, and a
# `-gt 0` refusal if nothing was built. See
# docs/features/active/built-not-listed-2026-08/specs/05-the-registry-row-over-shipped-bytes.md.
#
# The .goreleaser.yaml `raw` archive writes bare `a2a-<os>-<arch>` for every
# platform including windows (no `.exe` in that name) -- confirmed by
# internal/release/download.go's identical platformName construction, which
# `a2a update` fetches. MCPB's own MANIFEST.md says a host "will
# automatically append `.exe` on Windows" for a binary server type, so this
# script also accepts an `a2a-<os>-<arch>.exe` binary in DIST and, when
# found, packages it as `server/a2a.exe` with the entry_point/command
# updated to match -- whichever the platform's raw binary is actually named.

DIST="${1:?usage: build-mcpb.sh DIST VERSION}"
VERSION="${2:?usage: build-mcpb.sh DIST VERSION}"

# THE ZIP RUNS FROM INSIDE THE STAGING DIRECTORY, so the output path it is
# handed must be absolute. It was not: `dist/a2a-darwin-amd64.mcpb` resolved
# against the staging dir, which has no `dist/`, and zip answered "Could not
# create output file". That is what v0.26.1's cohort job died on — the second
# consecutive release this one job broke, and the first time this script had
# ever executed at all, because v0.26.0's run never got past its missing
# executable bit. `scripts/tests/build_mcpb_test.sh` now runs it for real.
test -d "$DIST" || { echo "DIST is not a directory: $DIST" >&2; exit 2; }
DIST_ABS="$(cd "$DIST" && pwd)"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="${SCRIPT_DIR}/lib/mcpb-manifest.template.json"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
command -v zip >/dev/null || { echo "zip is required" >&2; exit 2; }
test -f "$TEMPLATE" || { echo "bundle manifest template not found: $TEMPLATE" >&2; exit 2; }

staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

built=0
for os in darwin linux windows; do
  for arch in amd64 arm64; do
    bin="${DIST}/a2a-${os}-${arch}"
    entry="server/a2a"
    if [ ! -f "$bin" ] && [ -f "${bin}.exe" ]; then
      bin="${bin}.exe"
      entry="server/a2a.exe"
    fi
    test -f "$bin" || continue

    bundle_dir="${staging}/a2a-${os}-${arch}"
    rm -rf "$bundle_dir"
    mkdir -p "${bundle_dir}/server"
    cp "$bin" "${bundle_dir}/${entry}"
    chmod +x "${bundle_dir}/${entry}"

    jq \
      --arg version "$VERSION" \
      --arg entry "$entry" \
      '.version = $version | .server.entry_point = $entry | .server.mcp_config.command = $entry' \
      "$TEMPLATE" > "${bundle_dir}/manifest.json"

    # Every ${user_config.KEY} the manifest substitutes must be a key the
    # manifest DECLARES, or the host silently substitutes nothing and the
    # server starts without the value. A2A_PROJECT_ROOT is the one that makes
    # the difference between a working bundle and one that dies on its first
    # config read, so this is checked where the artifact is made rather than
    # trusted to review.
    undeclared="$(jq -r '
      [ .. | strings | capture("\\$\\{user_config\\.(?<k>[A-Za-z0-9_]+)\\}"; "g").k ] as $used
      | ($used - (.user_config // {} | keys)) | unique | .[]' "${bundle_dir}/manifest.json")"
    if [ -n "$undeclared" ]; then
      echo "manifest for ${os}-${arch} substitutes undeclared user_config key(s): $(echo "$undeclared" | tr '\n' ' ')" >&2
      exit 1
    fi

    out="${DIST}/a2a-${os}-${arch}.mcpb"
    out_abs="${DIST_ABS}/a2a-${os}-${arch}.mcpb"
    rm -f "$out_abs"
    (cd "$bundle_dir" && zip -q -X -r "$out_abs" manifest.json server)

    echo "$out"
    built=$((built + 1))
  done
done

test "$built" -gt 0
