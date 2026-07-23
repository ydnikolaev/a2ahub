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

# A2A_INSTALL_DIR pins the destination (useful for a sandboxed/CI install, or
# a machine where neither default is the right place); otherwise prefer
# /usr/local/bin and fall back to ~/.local/bin when it is not writable.
if [ -n "${A2A_INSTALL_DIR:-}" ]; then
  install_dir="${A2A_INSTALL_DIR}"
  mkdir -p "$install_dir"
else
  install_dir="/usr/local/bin"
  if [ ! -w "$install_dir" ] 2>/dev/null; then
    install_dir="${HOME}/.local/bin"
    mkdir -p "$install_dir"
  fi
fi

cp "$bin_path" "${install_dir}/${BINARY}"
log "installed ${BINARY} ${version} -> ${install_dir}/${BINARY}"

# --- 7. macOS Gatekeeper note ------------------------------------------------

if [ "$os" = "darwin" ]; then
  xattr -d com.apple.quarantine "${install_dir}/${BINARY}" 2>/dev/null || true
  log "macOS: cleared the com.apple.quarantine flag on the binary."
  log "  (if Gatekeeper still balks, run: xattr -d com.apple.quarantine ${install_dir}/${BINARY})"
fi

# --- 8. shell integration: PATH + completion (best-effort) ------------------
# An installed binary the shell cannot find is a failed install as far as the
# user is concerned, so this step WIRES the shell rather than printing advice
# and hoping: it appends one guarded, idempotent block to the login shell's rc
# file (PATH when needed, plus the two lines zsh needs to load completions).
# Opt out with A2A_NO_MODIFY_PATH=1 — then the same lines are printed instead.
# Every failure here is swallowed: a shell that could not be wired must never
# abort an otherwise successful binary install.

a2a_bin="${install_dir}/${BINARY}"
shell_name="$(basename "${SHELL:-sh}")"
block_start="# >>> a2a install >>>"
block_end="# <<< a2a install <<<"

# rc_needs_line FILE LINE -> true when FILE does not already contain LINE.
rc_needs_line() {
  [ -f "$1" ] || return 0
  ! grep -qF "$2" "$1" 2>/dev/null
}

# append_block FILE LINES... — writes the guarded block, once. A file that
# already carries the marker is left alone (re-running the installer, or
# `a2a update`, never appends a second copy).
append_block() {
  rc_file="$1"
  shift
  [ -n "$rc_file" ] || return 0
  if [ -f "$rc_file" ] && grep -qF "$block_start" "$rc_file" 2>/dev/null; then
    log "shell: ${rc_file} already carries the a2a block — left untouched"
    return 0
  fi
  {
    printf '\n%s\n' "$block_start"
    for line in "$@"; do printf '%s\n' "$line"; done
    printf '%s\n' "$block_end"
  } >> "$rc_file" 2>/dev/null || return 1
  log "shell: wired ${rc_file} (PATH/completions) — run 'source ${rc_file}' or open a new shell"
  return 0
}

# print_block FILE LINES... — the A2A_NO_MODIFY_PATH=1 path: say exactly what
# to paste, never touch the file.
print_block() {
  log "shell: A2A_NO_MODIFY_PATH is set — add these lines to $1 yourself:"
  shift
  for line in "$@"; do log "    $line"; done
}

setup_shell() {
  path_line="export PATH=\"${install_dir}:\$PATH\""
  need_path=1
  case ":$PATH:" in
    *":${install_dir}:"*) need_path=0 ;;
  esac

  case "$shell_name" in
    zsh)
      rc_file="${ZDOTDIR:-$HOME}/.zshrc"
      comp_dir="${HOME}/.zsh/completions"
      mkdir -p "$comp_dir" || return 0
      "$a2a_bin" completion zsh > "${comp_dir}/_${BINARY}" 2>/dev/null || return 0
      log "zsh completion installed -> ${comp_dir}/_${BINARY}"

      set --
      if [ "$need_path" -eq 1 ]; then set -- "$path_line"; fi
      set -- "$@" "fpath=(${comp_dir} \$fpath)"
      # Only add compinit when the rc does not already run it (a framework
      # like oh-my-zsh runs its own; two compinit calls just cost startup
      # time).
      if rc_needs_line "$rc_file" "compinit"; then
        set -- "$@" "autoload -Uz compinit && compinit"
      fi
      [ "$#" -gt 0 ] || return 0
      if [ -n "${A2A_NO_MODIFY_PATH:-}" ]; then print_block "$rc_file" "$@"; else append_block "$rc_file" "$@"; fi
      ;;
    bash)
      rc_file="${HOME}/.bashrc"
      comp_dir="${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions"
      mkdir -p "$comp_dir" || return 0
      "$a2a_bin" completion bash > "${comp_dir}/${BINARY}" 2>/dev/null || return 0
      log "bash completion installed -> ${comp_dir}/${BINARY} (needs the bash-completion package)"

      [ "$need_path" -eq 1 ] || return 0
      if [ -n "${A2A_NO_MODIFY_PATH:-}" ]; then print_block "$rc_file" "$path_line"; else append_block "$rc_file" "$path_line"; fi
      # macOS bash reads ~/.bash_profile for login shells and may never
      # source ~/.bashrc — say so rather than leaving a silent no-op.
      if [ "$os" = "darwin" ] && [ -f "${HOME}/.bash_profile" ] && rc_needs_line "${HOME}/.bash_profile" ".bashrc"; then
        log "  note: ~/.bash_profile does not source ~/.bashrc — add: [ -f ~/.bashrc ] && . ~/.bashrc"
      fi
      ;;
    fish)
      comp_dir="${HOME}/.config/fish/completions"
      mkdir -p "$comp_dir" || return 0
      "$a2a_bin" completion fish > "${comp_dir}/${BINARY}.fish" 2>/dev/null || return 0
      log "fish completion installed -> ${comp_dir}/${BINARY}.fish"

      [ "$need_path" -eq 1 ] || return 0
      rc_file="${HOME}/.config/fish/config.fish"
      mkdir -p "$(dirname "$rc_file")" || return 0
      if [ -n "${A2A_NO_MODIFY_PATH:-}" ]; then
        print_block "$rc_file" "fish_add_path ${install_dir}"
      else
        append_block "$rc_file" "fish_add_path ${install_dir}"
      fi
      ;;
    *)
      log "shell completion available: run '${BINARY} completion <bash|zsh|fish>' to generate it"
      if [ "$need_path" -eq 1 ]; then
        log "note: ${install_dir} isn't on your PATH — add: ${path_line}"
      fi
      ;;
  esac
}
setup_shell || true

log "done — run '${BINARY}' with no arguments to see the command list."
log "next: 'a2a init --system <your-system> --space <space-repo-url>' then 'a2a doctor'."
log "  writes (sync/submit/doctor) need a GitHub token: a2a uses 'gh auth token' when the"
log "  GitHub CLI is authenticated, or export A2A_TOKEN_<SPACE_ID> to override it."

# --- other channels -----------------------------------------------------------
# Already installed a2a? `a2a update` runs this exact download+verify contract
# in-place (same asset + SHA256SUMS names), so re-running this script is only
# needed for a first install or if the binary was removed.
