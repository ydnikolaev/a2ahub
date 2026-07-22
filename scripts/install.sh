#!/bin/sh
# Installs the latest `a2a` release from GitHub. POSIX sh — no bashisms — so it
# runs under whatever /bin/sh the user's box ships (dash, ash, busybox, ...).
#
# a2a ships the RAW `a2a-<os>-<arch>` binary per .goreleaser.yaml's "raw"
# archives entry (§7 OPTION 1) — this script downloads that binary directly
# and does NOT unpack anything (there's no tar.gz/zip to open on this path;
# those are the separate human-facing archives — see the release page). The
# checksum file is `SHA256SUMS` (goreleaser's `checksum.name_template`, the
# same file internal/release.ChecksumVerifier parses for `a2a update`) — this
# script mirrors that verb's download+verify contract, first-install flavor:
# resolve the release, fetch the asset + SHA256SUMS, verify, install.
#
# The repo (ydnikolaev/a2ahub) may be private, so a GITHUB_TOKEN is honoured
# if present (it unlocks the release-asset API path); it is optional once the
# repo goes public, at which point the browser_download_url path serves
# unauthenticated requests too.

set -eu

OWNER="ydnikolaev"
REPO="a2ahub"
BINARY="a2a"

log() { printf 'a2a-install: %s\n' "$*" >&2; }
die() { log "$*"; exit 1; }

# --- 1. detect OS/arch, map to goreleaser's raw-binary naming ---------------

os_raw="$(uname -s)"
arch_raw="$(uname -m)"

case "$os_raw" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  MINGW*|MSYS*|CYGWIN*)
    die "windows is not supported by this shell installer — grab the a2a_<version>_windows_<arch>.zip archive from https://github.com/${OWNER}/${REPO}/releases/latest instead" ;;
  *) die "unsupported OS: $os_raw" ;;
esac

case "$arch_raw" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) die "unsupported architecture: $arch_raw (a2a ships amd64/arm64 only)" ;;
esac

asset_name="${BINARY}-${os}-${arch}"
sums_name="SHA256SUMS"

# --- 2. auth headers (optional; public repo needs none) ---------------------

auth_header=""
if [ -n "${GITHUB_TOKEN:-}" ]; then
  auth_header="Authorization: Bearer ${GITHUB_TOKEN}"
fi

api="https://api.github.com/repos/${OWNER}/${REPO}"

curl_json() {
  if [ -n "$auth_header" ]; then
    curl -fsSL -H "$auth_header" -H "Accept: application/vnd.github+json" "$1"
  else
    curl -fsSL -H "Accept: application/vnd.github+json" "$1"
  fi
}

# --- 3. resolve the release (latest, or a pinned A2A_VERSION) ---------------

if [ -n "${A2A_VERSION:-}" ]; then
  tag="${A2A_VERSION}"
  case "$tag" in
    v*) ;;
    *) tag="v${tag}" ;;
  esac
  release_json="$(curl_json "${api}/releases/tags/${tag}")" \
    || die "couldn't reach GitHub's releases API for ${OWNER}/${REPO} tag ${tag} (private repo? check GITHUB_TOKEN)"
else
  release_json="$(curl_json "${api}/releases/latest")" \
    || die "couldn't reach GitHub's releases API for ${OWNER}/${REPO} (private repo? check GITHUB_TOKEN)"
fi

tag="$(printf '%s' "$release_json" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
[ -n "$tag" ] || die "couldn't parse a tag_name out of the releases response"
version="${tag#v}"

# Pull "id" + "name" pairs out of the assets array and grep for the ones we
# want. (Not using jq: it isn't guaranteed present, and this script has
# exactly three fields to extract.)
#
# GitHub returns the release JSON pretty-printed — one field per line, each
# ending in a comma. `tr ',' '\n'` therefore turns every `,\n` into a BLANK
# line, so an asset object splits to `"id":…`, «», `"node_id":…`, «», and
# `"name":…`, with the id ending up FOUR lines above the name, out of a `-B2`
# window's reach. Dropping the blank lines first restores id-is-two-lines-
# above-name, which holds for both the pretty and the compact JSON shape.
find_asset_id() {
  name="$1"
  printf '%s' "$release_json" \
    | tr ',' '\n' \
    | sed '/^[[:space:]]*$/d' \
    | grep -B2 "\"name\": *\"${name}\"" \
    | grep '"id"' \
    | head -1 \
    | sed -E 's/[^0-9]*([0-9]+).*/\1/'
}

asset_id="$(find_asset_id "$asset_name")"
sums_id="$(find_asset_id "$sums_name")"
[ -n "$asset_id" ] || die "release ${tag} has no asset named ${asset_name} — check .goreleaser.yaml's raw name_template still matches"
[ -n "$sums_id" ] || die "release ${tag} has no ${sums_name} asset — refusing to install an unverified binary"

# --- 4. download via the asset API (works for private repos with a token) --

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

download_asset() {
  id="$1"
  out="$2"
  if [ -n "$auth_header" ]; then
    curl -fsSL -H "$auth_header" -H "Accept: application/octet-stream" \
      "${api}/releases/assets/${id}" -o "$out"
  else
    # Public phase: same endpoint still works unauthenticated once the repo is public.
    curl -fsSL -H "Accept: application/octet-stream" \
      "${api}/releases/assets/${id}" -o "$out"
  fi
}

log "downloading ${asset_name} (release ${tag})"
download_asset "$asset_id" "${workdir}/${asset_name}"
download_asset "$sums_id" "${workdir}/${sums_name}"

# --- 5. verify checksum (fail-closed — no skip path) -------------------------

expected="$(grep "  ${asset_name}\$" "${workdir}/${sums_name}" | awk '{print $1}')"
[ -n "$expected" ] || die "asset ${asset_name} not listed in ${sums_name}"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${workdir}/${asset_name}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "${workdir}/${asset_name}" | awk '{print $1}')"
else
  die "no sha256sum or shasum on PATH — can't verify the download, refusing to install unverified binary"
fi

[ "$expected" = "$actual" ] || die "checksum mismatch for ${asset_name}: expected ${expected}, got ${actual}"
log "checksum verified"

# --- 6. install: prefer /usr/local/bin, fall back to ~/.local/bin ----------
# NOTE: no unpack step — the downloaded asset IS the a2a binary, already
# executable-shaped (goreleaser's `formats: [binary]` raw archives entry).

bin_path="${workdir}/${asset_name}"
chmod +x "$bin_path"

install_dir="/usr/local/bin"
if [ ! -w "$install_dir" ] 2>/dev/null; then
  install_dir="${HOME}/.local/bin"
  mkdir -p "$install_dir"
fi

cp "$bin_path" "${install_dir}/${BINARY}"
log "installed ${BINARY} ${version} -> ${install_dir}/${BINARY}"

# --- 7. macOS Gatekeeper note ------------------------------------------------

if [ "$os" = "darwin" ]; then
  xattr -d com.apple.quarantine "${install_dir}/${BINARY}" 2>/dev/null || true
  log "macOS: cleared the com.apple.quarantine flag on the binary."
  log "  (if Gatekeeper still balks, run: xattr -d com.apple.quarantine ${install_dir}/${BINARY})"
fi

# --- 8. PATH hint -------------------------------------------------------------

case ":$PATH:" in
  *":${install_dir}:"*) ;;
  *) log "note: ${install_dir} isn't on your PATH — add: export PATH=\"${install_dir}:\$PATH\"" ;;
esac

log "done — run '${BINARY}' with no arguments to see the command list."

# --- other channels -----------------------------------------------------------
# Already installed a2a? `a2a update` runs this exact download+verify contract
# in-place (same asset + SHA256SUMS names), so re-running this script is only
# needed for a first install or if the binary was removed.
