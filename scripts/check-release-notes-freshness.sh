#!/usr/bin/env bash
# check-release-notes-freshness.sh — keep user-visible product commits from
# outrunning the authored release-notes corpus.
#
# The anchor is the last commit touching the highest semver-shaped
# releasenotes/*.yaml file, never a local tag: public-release tags are cut in a
# filtered repository and may not exist in this checkout. Every later
# feat/fix/perf or breaking-marked conventional commit that touches a product
# surface must be covered by a newer notes commit.
set -uo pipefail

SCRIPT_ABS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

latest_notes_file() {
  git -C "$ROOT" ls-files 'releasenotes/*.yaml' |
    awk -F'[/\.]' '
      $2 ~ /^[0-9]+$/ && $3 ~ /^[0-9]+$/ && $4 ~ /^[0-9]+$/ && $5 == "yaml" {
        printf "%012d.%012d.%012d\t%s\n", $2, $3, $4, $0
      }
    ' |
    sort |
    tail -n 1 |
    cut -f2-
}

needs_notes() {
  case "$1" in
    feat:\ *|feat\(*\):\ *|feat!:\ *|feat\(*\)!:\ *|\
    fix:\ *|fix\(*\):\ *|fix!:\ *|fix\(*\)!:\ *|\
    perf:\ *|perf\(*\):\ *|perf!:\ *|perf\(*\)!:\ *|\
    [a-z]*!:\ *|[a-z]*\(*\)!:\ *)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

run_check() {
  local newest anchor
  newest="$(latest_notes_file)"
  if [ -z "$newest" ]; then
    echo "release-notes-freshness: FAIL — no tracked semver releasenotes/*.yaml file found." >&2
    return 1
  fi
  anchor="$(git -C "$ROOT" log -1 --format=%H -- "$newest")"
  if [ -z "$anchor" ]; then
    echo "release-notes-freshness: FAIL — $newest has no commit anchor." >&2
    return 1
  fi

  local uncovered=""
  while IFS=$'\t' read -r commit subject; do
    [ -n "$commit" ] || continue
    if needs_notes "$subject"; then
      uncovered+="${commit}"$'\t'"${subject}"$'\n'
    fi
  done < <(
    git -C "$ROOT" log "$anchor..HEAD" --format='%H%x09%s' -- \
      internal/ cmd/ schemas/ space-template/ skill/
  )

  if [ -n "$uncovered" ]; then
    echo "release-notes-freshness: FAIL — product commits landed after $newest was last authored:" >&2
    while IFS=$'\t' read -r commit subject; do
      [ -n "$commit" ] || continue
      echo "  ${commit:0:12} $subject" >&2
    done <<< "$uncovered"
    echo "Add the next releasenotes/<version>.yaml entry (or amend the newest notes if it is still unreleased)." >&2
    return 1
  fi

  echo "release-notes-freshness: $newest covers every later feat/fix/perf/breaking product commit."
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || {
    echo "release-notes-freshness --teeth: mktemp failed" >&2
    exit 1
  }
  trap "rm -rf -- '$tmp'" EXIT

  git -C "$tmp" init -q -b main
  git -C "$tmp" config user.email teeth@example.invalid
  git -C "$tmp" config user.name "release notes teeth"
  mkdir -p "$tmp/releasenotes" "$tmp/internal/widget" "$tmp/docs"
  printf '%s\n' 'version: "0.1.0"' >"$tmp/releasenotes/0.1.0.yaml"
  printf '%s\n' 'package widget' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add releasenotes/0.1.0.yaml internal/widget/widget.go
  git -C "$tmp" commit -q -m 'chore: seed release'

  printf '%s\n' 'package widget' 'const Fixed = true' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add internal/widget/widget.go
  git -C "$tmp" commit -q -m 'fix(widget): close visible defect'
  local bad_commit out rc
  bad_commit="$(git -C "$tmp" rev-parse --short=12 HEAD)"
  out="$(ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check 2>&1)"
  rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q "$bad_commit" <<<"$out"; then
    echo "release-notes-freshness --teeth: FAILED — uncovered fix did not red with its commit id:" >&2
    echo "$out" >&2
    exit 1
  fi

  printf '%s\n' 'version: "0.2.0"' >"$tmp/releasenotes/0.2.0.yaml"
  git -C "$tmp" add releasenotes/0.2.0.yaml
  git -C "$tmp" commit -q -m 'docs: author 0.2.0 release notes'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — a newer notes commit did not green the gate." >&2
    exit 1
  fi

  printf '%s\n' 'documentation only' >"$tmp/docs/readme.md"
  git -C "$tmp" add docs/readme.md
  git -C "$tmp" commit -q -m 'docs: explain widget'
  printf '%s\n' 'package widget' 'const Fixed = true' 'const Metadata = true' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add internal/widget/widget.go
  git -C "$tmp" commit -q -m 'chore(widget): refresh metadata'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — docs/chore commits demanded release notes." >&2
    exit 1
  fi

  echo "release-notes-freshness --teeth: uncovered fix reds with id; notes touch greens; docs/chore stay green."
}

case "${1:-check}" in
  check) run_check ;;
  --teeth) run_teeth ;;
  _internal-check) run_check ;;
  *)
    echo "usage: $0 [check|--teeth]" >&2
    exit 2
    ;;
esac
