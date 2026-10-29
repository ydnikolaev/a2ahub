#!/usr/bin/env bash
# deliver.sh — put a GATED build of private main on this machine's PATH as the
# one `a2a`, stamped `a2a <last-release> (local, <sha>)` (D-040, fast-loop-2026-10
# P1; specs/01-local-delivery.md T1 + T2). `make deliver` runs it.
#
# It is an installer, not a gate: it reads verdicts that already exist
# (.a2a/cache/verify/telemetry.jsonl, .a2a/release-gate/<sha>.json) and judges
# nothing itself. It refuses bytes nobody judged, and every refusal names the
# reason and the command that fixes it.
#
# Exit codes: 0 delivered or already delivered; 1 refused; 2 usage error.
#
# The target: `$A2A_DELIVER_TARGET` if set (the teeth set it so they never touch
# the real binary), else the `a2a` that `command -v a2a` resolves, followed
# through symlinks, else install.sh's rule (`A2A_INSTALL_DIR`, else
# /usr/local/bin if writable, else ~/.local/bin). Never sudo.
#
# Other test seams (all default to the real thing):
#   A2A_DELIVER_ROOT      the checkout to deliver (default: this script's repo)
#   A2A_DELIVER_GOCACHE   GOCACHE for the build (default: $ROOT/.a2a/cache/go/build)
#
# Telemetry is read from the MAIN checkout only (spec 01 §11): a worktree is
# refused outright, so its records never matter and a rebase cannot fake them.
#
# The identity formula is NOT defined here: scripts/lib/tree-identity.sh is
# sourced, the same file verify.sh sources, so the two cannot drift.
#
# lane-reads-opaque: the telemetry and receipt paths are built from "$ROOT" (a variable): $ROOT/.a2a/cache/verify/telemetry.jsonl and $ROOT/.a2a/release-gate/<sha>.json
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
ROOT="$(cd "${A2A_DELIVER_ROOT:-$REPO}" && pwd -P)"
# shellcheck source=lib/tree-identity.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/tree-identity.sh"

TELEMETRY="$ROOT/.a2a/cache/verify/telemetry.jsonl"
RECEIPTS="$ROOT/.a2a/release-gate"
WORK="$(mktemp -d)"
STAGED=""
cleanup() {
  rm -rf "$WORK"
  [ -z "$STAGED" ] || rm -f "$STAGED"
}
trap cleanup EXIT

if [ "$#" -ne 0 ]; then
  echo "usage: bash scripts/deliver.sh   (no arguments; make deliver)" >&2
  exit 2
fi

sha12() { printf '%s' "${1:0:12}"; }

# refuse <reason> <fix>: one refusal shape, always with the command that fixes it.
refuse() {
  echo "deliver: refused — $1" >&2
  echo "deliver: fix: $2" >&2
  exit 1
}

g() { git -C "$ROOT" "$@"; }

# json_field <key>: first string value of "key" in a JSON document on stdin.
# No jq dependency; `a2a version --json` is indented, and the tr makes a compact
# document work too.
json_field() {
  local out
  out="$(tr ',{}' '\n' | sed -n "s/^ *\"$1\": *\"\\([^\"]*\\)\" *\$/\\1/p")"
  printf '%s' "${out%%$'\n'*}"
}

# --- T1.1 main, clean --------------------------------------------------------
head_ref="$(g symbolic-ref -q HEAD || true)"
if [ "$head_ref" != "refs/heads/main" ]; then
  refuse "HEAD is ${head_ref:-detached}, not refs/heads/main" "git switch main in the main checkout, then make deliver"
fi
gitdir="$(g rev-parse --absolute-git-dir)"
commondir="$(g rev-parse --path-format=absolute --git-common-dir)"
if [ "$(cd "$gitdir" && pwd -P)" != "$(cd "$commondir" && pwd -P)" ]; then
  refuse "$ROOT is a linked worktree, not the main checkout" "run make deliver in the main checkout after merging"
fi
dirty="$(g status --porcelain=v1 --untracked-files=all | sed -n '1,5p')"
if [ -n "$dirty" ]; then
  refuse "the working tree is dirty; first paths: $(printf '%s' "$dirty" | tr '\n' ';')" "commit or stash them, then make deliver"
fi
HEAD_SHA="$(g rev-parse HEAD)"

# --- the target --------------------------------------------------------------
resolve_link() { # follow symlinks without readlink -f (BSD readlink has none)
  local p="$1" l n=0
  while [ -L "$p" ] && [ "$n" -lt 20 ]; do
    l="$(readlink "$p")"
    case "$l" in
      /*) p="$l" ;;
      *) p="$(dirname "$p")/$l" ;;
    esac
    n=$((n + 1))
  done
  printf '%s' "$p"
}
if [ -n "${A2A_DELIVER_TARGET:-}" ]; then
  TARGET="$A2A_DELIVER_TARGET"
elif found="$(command -v a2a 2>/dev/null)" && [ -n "$found" ]; then
  TARGET="$(resolve_link "$found")"
else
  # Restates scripts/install.sh resolve_install_dir (A2A_INSTALL_DIR, else
  # /usr/local/bin if writable, else ~/.local/bin). Keep the two in step.
  if [ -n "${A2A_INSTALL_DIR:-}" ]; then
    idir="$A2A_INSTALL_DIR"
  elif [ -w /usr/local/bin ]; then
    idir=/usr/local/bin
  else
    idir="$HOME/.local/bin"
  fi
  TARGET="$idir/a2a"
fi
TARGET_DIR="$(dirname "$TARGET")"
if [ ! -d "$TARGET_DIR" ] || [ ! -w "$TARGET_DIR" ]; then
  refuse "the target directory $TARGET_DIR is not a writable directory" "make it writable, or set A2A_DELIVER_TARGET to a writable path (deliver never uses sudo)"
fi

# The installed build, if it answers.
INST_CHANNEL="" INST_COMMIT="" INST_VERSION=""
if [ -x "$TARGET" ]; then
  if inst_json="$("$TARGET" version --json 2>/dev/null)"; then
    INST_CHANNEL="$(printf '%s' "$inst_json" | json_field channel)"
    INST_COMMIT="$(printf '%s' "$inst_json" | json_field commit)"
    INST_VERSION="$(printf '%s' "$inst_json" | json_field binary)"
  fi
fi

# --- T1.2 already there? -----------------------------------------------------
if [ "$INST_CHANNEL" = local ] && [ "$INST_COMMIT" = "$HEAD_SHA" ]; then
  echo "deliver: already delivered a2a $INST_VERSION (local, $(sha12 "$HEAD_SHA"))"
  exit 0
fi

# --- T1.3 not backwards ------------------------------------------------------
if [ "$INST_CHANNEL" = local ] && [ -n "$INST_COMMIT" ] &&
  g cat-file -e "$INST_COMMIT^{commit}" 2>/dev/null &&
  g merge-base --is-ancestor "$HEAD_SHA" "$INST_COMMIT" 2>/dev/null; then
  refuse "installed build $(sha12 "$INST_COMMIT") is newer than HEAD $(sha12 "$HEAD_SHA")" "deliver from the checkout that holds $(sha12 "$INST_COMMIT"), or sh scripts/install.sh to return to the release"
fi

pin_toolchain() {
  local v
  v="$(sed -n 's/^go \([0-9][0-9.]*\)$/\1/p' "$ROOT/go.mod" 2>/dev/null)"
  [ -z "$v" ] || export GOTOOLCHAIN="go$v"
  export GOCACHE="${A2A_DELIVER_GOCACHE:-$ROOT/.a2a/cache/go/build}"
  mkdir -p "$GOCACHE"
  export GOWORK=off
}
pin_toolchain

# --- T2 the evidence chain ---------------------------------------------------

# diff_paths <A> <C>: both sides of a rename, sorted LC_ALL=C (spec T2).
diff_paths() {
  g diff --name-only --no-renames -z "$1" "$2" | tr '\0' '\n' | LC_ALL=C sort -u
}

# norm_path <path>: collapse `.` and `..`; fails when it climbs out of the repo.
norm_path() {
  local part
  local -a parts=() out=()
  IFS=/ read -ra parts <<<"$1"
  for part in ${parts[@]+"${parts[@]}"}; do
    case "$part" in
      '' | .) ;;
      ..)
        [ "${#out[@]}" -gt 0 ] || return 1
        out=("${out[@]:0:${#out[@]}-1}")
        ;;
      *) out+=("$part") ;;
    esac
  done
  (IFS=/ && printf '%s' "${out[*]}")
}

# symlink_blob <C> <path>: what verify.sh's `git hash-object -- <path>` sees for
# a symlink. hash-object FOLLOWS a link, so the recorded identity holds the
# TARGET's content hash, not the committed link blob (measured; spec T2's "a
# working-file hash equals the committed blob" is false for symlinks). The
# target is resolved inside C's tree. Prints the blob; returns 1 when verify.sh
# would say `absent` (dangling, or a directory), 2 when it cannot be emulated
# (a link leaving the repository): that path then never matches a record, which
# fails closed.
symlink_blob() {
  local c="$1" p="$2" n=0 entry mode t
  while [ "$n" -lt 10 ]; do
    entry="$(g ls-tree "$c" -- "$p")"
    [ -n "$entry" ] || return 1
    mode="${entry%% *}"
    case "$mode" in
      120000)
        t="$(g cat-file blob "$c:$p")"
        case "$t" in /*) return 2 ;; esac
        if [ "$(dirname "$p")" = . ]; then
          p="$(norm_path "$t")" || return 2
        else
          p="$(norm_path "$(dirname "$p")/$t")" || return 2
        fi
        n=$((n + 1))
        ;;
      100644 | 100755)
        g rev-parse "$c:$p"
        return 0
        ;;
      *) return 1 ;;
    esac
  done
  return 2
}

# blob_lines <C> <paths on stdin>: `<blob> <path>` / `absent <path>` against C's
# git objects, the counterpart of verify.sh's working-file hashing.
blob_lines() {
  local c="$1" p kinds line blob rc i=0
  local -a paths=()
  while IFS= read -r p; do
    [ -n "$p" ] && paths+=("$p")
  done
  [ "${#paths[@]}" -gt 0 ] || return 0
  kinds="$(for p in "${paths[@]}"; do printf '%s:%s\n' "$c" "$p"; done | g cat-file --batch-check)"
  local links
  links="$(g ls-tree -z "$c" -- "${paths[@]}" | tr '\0' '\n' | sed -n 's/^120000 [a-z]* [0-9a-f]*\t//p')"
  while IFS= read -r line; do
    p="${paths[$i]}"
    if [[ "$line" =~ ^([0-9a-f]{40,64})\ blob\ [0-9]+$ ]]; then
      blob="${BASH_REMATCH[1]}"
      if [ -n "$links" ] && printf '%s\n' "$links" | grep -Fxq -- "$p"; then
        rc=0
        blob="$(symlink_blob "$c" "$p")" || rc=$?
        case "$rc" in
          0) printf '%s %s\n' "$blob" "$p" ;;
          1) printf 'absent %s\n' "$p" ;;
          *) printf 'unemulatable-symlink %s\n' "$p" ;;
        esac
      else
        printf '%s %s\n' "$blob" "$p"
      fi
    else
      printf 'absent %s\n' "$p"
    fi
    i=$((i + 1))
  done <<<"$kinds"
}

# id_of <A> <mode> <C>: the identity verify.sh <mode> recorded when it judged C's
# content as uncommitted changes on top of A.
id_of() {
  diff_paths "$1" "$3" | blob_lines "$3" | tree_identity "$1" "$2" ""
}

# Pass records, one file per mode. A pass is `"gate":"run:<mode>","verdict":"pass"`.
for m in full lane-run; do
  : >"$WORK/pass.$m"
  if [ -f "$TELEMETRY" ]; then
    grep -F "\"gate\":\"run:$m\",\"verdict\":\"pass\"" "$TELEMETRY" >"$WORK/pass.$m" || true
  fi
done
# <mode> <identity>. An empty identity (git could not compute one) proves nothing,
# and must not match a record that also lacks one.
has_pass() { [ -n "$2" ] && grep -qF "\"tree\":\"$2\"" "$WORK/pass.$1"; }

# build_inputs <C>: the files `go build ./cmd/a2a` reads for C's tree, from the
# toolchain's own answer (`go list -deps`), plus go.mod and go.sum. Cached per
# commit in $WORK. Cgo/C/C++/H/S/Syso files count as well as Go and embedded
# files (conservative, lead decision).
build_inputs() {
  local c="$1" dir real
  [ -f "$WORK/bi.$c" ] && return 0
  dir="$(mktemp -d "$WORK/tree.XXXXXX")"
  real="$(cd "$dir" && pwd -P)"
  g archive "$c" | tar -x -C "$real"
  {
    printf 'go.mod\ngo.sum\n'
    (
      cd "$real"
      # shellcheck disable=SC2016  # a go list template: {{$d}} is Go's, not the shell's
      go list -deps -f '{{if not .Standard}}{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{range .CgoFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{range .CFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{range .CXXFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{range .HFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{range .SFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{range .SysoFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{range .EmbedFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{end}}' ./cmd/a2a
    ) | sed -n "s|^$real/||p"
  } | LC_ALL=C sort -u >"$WORK/bi.$c"
  rm -rf "$dir"
}

# binary_neutral <A> <C>: A is C's first parent and `go build` reads none of the
# changed files (L3). The input set is the UNION of A's and C's, so a deleted
# source file (absent from C's set) still counts as an input change.
binary_neutral() {
  local a="$1" c="$2" changed
  changed="$(diff_paths "$a" "$c")"
  [ -n "$changed" ] || return 0
  build_inputs "$a"
  build_inputs "$c"
  printf '%s\n' "$changed" >"$WORK/changed"
  [ -z "$(LC_ALL=C sort -u "$WORK/bi.$a" "$WORK/bi.$c" | LC_ALL=C comm -12 - "$WORK/changed")" ]
}

# changed_inputs <A> <C>: the build inputs diff(A,C) touches, for the refusal.
changed_inputs() {
  local a="$1" c="$2"
  diff_paths "$a" "$c" >"$WORK/changed"
  build_inputs "$a"
  build_inputs "$c"
  LC_ALL=C sort -u "$WORK/bi.$a" "$WORK/bi.$c" | LC_ALL=C comm -12 - "$WORK/changed"
}

# anchor_kind <C>: prints A1/A2/A3 when C's whole tree is proven.
anchor_kind() {
  local c="$1" id
  if [ -f "$RECEIPTS/$c.json" ] && grep -Eq '"verdict"[[:space:]]*:[[:space:]]*"pass"' "$RECEIPTS/$c.json"; then
    echo "release receipt"
    return 0
  fi
  id="$(tree_identity "$c" full "" </dev/null)"
  if [ -n "$id" ] && has_pass full "$id"; then
    echo "ceiling pass"
    return 0
  fi
  if [ "$INST_CHANNEL" = local ] && [ "$INST_COMMIT" = "$c" ]; then
    echo "delivered build"
    return 0
  fi
  return 1
}

# chain: HEAD's first-parent line, newest first, at most 200 commits.
CHAIN=()
while IFS= read -r c; do CHAIN+=("$c"); done < <(g rev-list --first-parent --max-count=200 HEAD)
ANCHOR_IDX=-1 ANCHOR_KIND=""
for i in "${!CHAIN[@]}"; do
  if kind="$(anchor_kind "${CHAIN[$i]}")"; then
    ANCHOR_IDX=$i
    ANCHOR_KIND="$kind"
    break
  fi
done
if [ "$ANCHOR_IDX" -lt 0 ]; then
  refuse "no commit within the last ${#CHAIN[@]} on main is fully proven (no release receipt, no clean-tree make check pass, no delivered build)" "run make check on this clean HEAD (it then anchors the chain), then make deliver"
fi

# Walk forward from the anchor (oldest first). proven[] is index-aligned to CHAIN.
declare -a PROVEN=()
PROVEN[ANCHOR_IDX]=1
FIRST_UNPROVEN=-1
for ((i = ANCHOR_IDX - 1; i >= 0; i--)); do
  c="${CHAIN[$i]}"
  parent_idx=$((i + 1))
  ok=0
  # L3: the parent is proven and the build reads none of the changed files.
  if [ "${PROVEN[$parent_idx]:-0}" = 1 ] && binary_neutral "${CHAIN[$parent_idx]}" "$c"; then
    ok=1
  fi
  # L1/L2: a proven earlier commit A has a lane-run or full pass for id(A, mode, C).
  if [ "$ok" = 0 ]; then
    for ((j = ANCHOR_IDX; j > i; j--)); do
      [ "${PROVEN[$j]:-0}" = 1 ] || continue
      a="${CHAIN[$j]}"
      if [ -n "$(diff_paths "$a" "$c")" ] && has_pass lane-run "$(id_of "$a" lane-run "$c")"; then
        ok=1
        break
      fi
      if has_pass full "$(id_of "$a" full "$c")"; then
        ok=1
        break
      fi
    done
  fi
  if [ "$ok" = 1 ]; then
    PROVEN[i]=1
  elif [ "$FIRST_UNPROVEN" -lt 0 ]; then
    FIRST_UNPROVEN=$i
  fi
done
if [ "${PROVEN[0]:-0}" != 1 ]; then
  # Name the first unproven commit (oldest), its subject and the build inputs it changes.
  bad="${CHAIN[$FIRST_UNPROVEN]}"
  inputs="$(changed_inputs "${CHAIN[$((FIRST_UNPROVEN + 1))]}" "$bad" | sed -n '1,5p' | tr '\n' ' ')"
  refuse "commit $(sha12 "$bad") ($(g log -1 --format=%s "$bad")) has no passing record; build inputs it changes: ${inputs:-none (but the chain above it is unproven)}" "run make check on this clean HEAD (it then anchors the chain), then make deliver"
fi
LINKS=$((ANCHOR_IDX))

# --- T1.6 version ------------------------------------------------------------
if ! VERSION="$(cd "$REPO" && go run internal/notes/lastreleased.go "$ROOT/releasenotes")" || [ -z "$VERSION" ]; then
  refuse "no dated release note under $ROOT/releasenotes names a last release" "author a dated releasenotes/<version>.yaml, or check the go toolchain"
fi

# --- T1.7 build into the target's directory ----------------------------------
STAGED="$(mktemp "$TARGET_DIR/.a2a-deliver.XXXXXX")"
if ! (cd "$ROOT" && go build -buildvcs=false -trimpath \
  -ldflags "-s -w -X main.version=$VERSION -X main.commit=$HEAD_SHA -X main.channel=local" \
  -o "$STAGED" ./cmd/a2a); then
  refuse "go build ./cmd/a2a failed" "fix the build, commit, and re-run make deliver"
fi

# --- T1.8 smoke ----------------------------------------------------------------
chmod 0755 "$STAGED"
smoke="$("$STAGED" version --json 2>/dev/null || true)"
if [ "$(printf '%s' "$smoke" | json_field binary)" != "$VERSION" ] ||
  [ "$(printf '%s' "$smoke" | json_field channel)" != local ] ||
  [ "$(printf '%s' "$smoke" | json_field commit)" != "$HEAD_SHA" ]; then
  refuse "the built binary does not report binary $VERSION / channel local / commit $(sha12 "$HEAD_SHA") in version --json; the target is untouched" "inspect: $STAGED version --json (cmd/a2a/main.go channel + commit stamping)"
fi

# --- T1.9 swap: rename(2) on one filesystem is atomic -------------------------
mv -f "$STAGED" "$TARGET"
STAGED=""

# --- T1.10 report --------------------------------------------------------------
echo "deliver: a2a $VERSION (local, $(sha12 "$HEAD_SHA")) -> $TARGET (evidence: $ANCHOR_KIND $(sha12 "${CHAIN[$ANCHOR_IDX]}") + $LINKS links)"
mcp_pids="$(ps -axo pid=,command= 2>/dev/null | awk -v t="$TARGET mcp" '{pid=$1; $1=""; sub(/^ /,""); if ($0==t || $0=="a2a mcp") printf "%s ", pid}' || true)"
if [ -n "$mcp_pids" ]; then
  echo "deliver: a2a mcp is running (pid ${mcp_pids% }): restart your MCP client to load the new binary"
fi
