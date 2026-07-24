#!/usr/bin/env bash
# release-preflight.sh — the check that must pass BEFORE cutting a release tag.
#
# Why this exists (P33, 2026-07-24): the space-CI redesign shipped with the
# space-template caller pinned to `@v0.4.0` — a version that was ALREADY
# released and that PREDATES the reusable workflow the caller references. Every
# space created from that template would have failed with "workflow not found".
# The root cause was reading release state from LOCAL `git tag` (stale) instead
# of the release remote. This gate makes that class impossible to ship blind.
#
# Two assertions, both keyed off the RELEASE REMOTE (never local-only state):
#   (a) the version you are about to cut is FREE on the remote;
#   (b) every reusable-workflow ref the space-template pins resolves to a tag
#       whose TREE ACTUALLY CONTAINS that workflow file.
#
# Determinism note (validation doctrine §5): the only non-deterministic step is
# the `git fetch` of the release remote. Everything after it is a pure local git
# read of the fetched refs — which is exactly what `--teeth` exercises offline
# against a fixture repo. Because it needs the network, this is NOT part of
# `make check`; it is its own target, like `make vulncheck`.
#
# Usage:
#   bash scripts/release-preflight.sh v0.6.0        # before cutting v0.6.0
#   bash scripts/release-preflight.sh --teeth       # offline self-test (the gate bites)
set -euo pipefail

REMOTE="${A2A_RELEASE_REMOTE:-public}"
REUSABLE_PATH=".github/workflows/a2a-validate-reusable.yml"
TEMPLATE_WORKFLOWS="space-template/.github/workflows"

fail() { printf '\033[31m✗\033[0m %s\n' "$1" >&2; return 1; }
ok()   { printf '\033[32m✓\033[0m %s\n' "$1"; }

# ── core assertions — pure LOCAL git reads over already-fetched refs ──────────

# (a) the version being cut must not already exist as a tag.
assert_version_free() { # $1 = repo, $2 = version
  local repo="$1" want="$2"
  if git -C "$repo" tag -l "$want" | grep -qx "$want"; then
    fail "release-preflight: $want ALREADY EXISTS as a tag — pick the next free version.
    (This is the P33 miss: v0.4.0 was already released and predated the workflow.)"
    return 1
  fi
  ok "release-preflight: $want is free"
}

# (b) every pinned reusable-workflow ref must resolve to a tag whose tree
#     carries the reusable workflow. A pin to a tag that predates the workflow
#     fails every space's CI with "workflow not found".
assert_pins_resolve() { # $1 = repo
  local repo="$1" rc=0 found=0 line ref
  while IFS= read -r line; do
    # strip comments, take the token after the last '@'
    line="${line%%#*}"
    case "$line" in *a2a-validate-reusable.yml@*) ;; *) continue ;; esac
    ref="${line##*@}"
    ref="$(printf '%s' "$ref" | tr -d '[:space:]')"
    [ -n "$ref" ] || continue
    found=$((found + 1))
    if ! git -C "$repo" rev-parse -q --verify "refs/tags/$ref" >/dev/null 2>&1; then
      fail "release-preflight: space-template pins @$ref, which is NOT a known tag on '$REMOTE'.
    Fix: pin a released version, or cut $ref first."
      rc=1
      continue
    fi
    if ! git -C "$repo" cat-file -e "$ref:$REUSABLE_PATH" 2>/dev/null; then
      fail "release-preflight: space-template pins @$ref, but that tag's tree does NOT contain
    $REUSABLE_PATH — every space created from this template would fail
    'workflow not found'. Fix: pin the release that ADDED the reusable workflow."
      rc=1
      continue
    fi
    ok "release-preflight: pin @$ref resolves and carries $REUSABLE_PATH"
  done < <(grep -rh "a2a-validate-reusable.yml@" "$repo/$TEMPLATE_WORKFLOWS" 2>/dev/null || true)

  if [ "$found" -eq 0 ]; then
    fail "release-preflight: no reusable-workflow pin found under $TEMPLATE_WORKFLOWS —
    the template must reference the reusable workflow (did a refactor drop it?)."
    return 1
  fi
  return "$rc"
}

# ── teeth: the gate must go RED on a violating fixture (offline) ──────────────

teeth() {
  local tmp rc out
  tmp="$(mktemp -d)" || { echo "release-preflight --teeth: mktemp failed" >&2; exit 1; }
  trap 'rm -rf "$tmp"' RETURN

  git -C "$tmp" init -q -b main
  git -C "$tmp" config user.email teeth@example.com
  git -C "$tmp" config user.name teeth

  # v0.1.0 — BEFORE the reusable workflow exists.
  mkdir -p "$tmp/$TEMPLATE_WORKFLOWS"
  echo "placeholder" > "$tmp/README.md"
  git -C "$tmp" add -A && git -C "$tmp" commit -qm "pre-workflow"
  git -C "$tmp" tag v0.1.0

  # v0.2.0 — ADDS the reusable workflow.
  mkdir -p "$tmp/$(dirname "$REUSABLE_PATH")"
  echo "on: workflow_call" > "$tmp/$REUSABLE_PATH"
  git -C "$tmp" add -A && git -C "$tmp" commit -qm "add reusable workflow"
  git -C "$tmp" tag v0.2.0

  # --- teeth 1: a pin to the PRE-workflow tag must go RED (the P33 miss) ---
  echo "    uses: o/r/$REUSABLE_PATH@v0.1.0" > "$tmp/$TEMPLATE_WORKFLOWS/a2a-validate.yml"
  if out="$(assert_pins_resolve "$tmp" 2>&1)"; then
    echo "release-preflight --teeth: FAILED — gate stayed GREEN on a pin to a tag that predates the workflow" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "does NOT contain" || {
    echo "release-preflight --teeth: FAILED — red, but not with the predates-the-workflow message" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 1: pin to a pre-workflow tag → RED"

  # --- teeth 2: a pin to a NONEXISTENT tag must go RED ---
  echo "    uses: o/r/$REUSABLE_PATH@v9.9.9" > "$tmp/$TEMPLATE_WORKFLOWS/a2a-validate.yml"
  if out="$(assert_pins_resolve "$tmp" 2>&1)"; then
    echo "release-preflight --teeth: FAILED — gate stayed GREEN on a pin to a nonexistent tag" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 2: pin to a nonexistent tag → RED"

  # --- teeth 3: a correct pin must go GREEN (no false positive) ---
  echo "    uses: o/r/$REUSABLE_PATH@v0.2.0" > "$tmp/$TEMPLATE_WORKFLOWS/a2a-validate.yml"
  if ! out="$(assert_pins_resolve "$tmp" 2>&1)"; then
    echo "release-preflight --teeth: FAILED — gate went RED on a correct pin" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 3: correct pin → GREEN"

  # --- teeth 4 (ADD direction): a SECOND caller with a bad pin must be caught ---
  echo "    uses: o/r/$REUSABLE_PATH@v0.1.0" > "$tmp/$TEMPLATE_WORKFLOWS/extra.yml"
  if out="$(assert_pins_resolve "$tmp" 2>&1)"; then
    echo "release-preflight --teeth: FAILED — a newly ADDED caller with a bad pin slipped through" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 4: newly added caller with a bad pin → RED (ADD direction)"

  # --- teeth 5: version-free assertion bites on an existing tag ---
  if out="$(assert_version_free "$tmp" v0.2.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — version-free stayed GREEN on an existing tag" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 5: cutting an already-released version → RED"

  if ! assert_version_free "$tmp" v0.3.0 >/dev/null 2>&1; then
    echo "release-preflight --teeth: FAILED — version-free went RED on a free version" >&2; exit 1
  fi
  ok "teeth 6: a free version → GREEN"

  echo "release-preflight --teeth: all teeth bite."
}

# ── entrypoint ───────────────────────────────────────────────────────────────

if [ "${1:-}" = "--teeth" ]; then
  teeth
  exit 0
fi

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "usage: bash scripts/release-preflight.sh <version-to-cut>   # e.g. v0.6.0" >&2
  echo "       bash scripts/release-preflight.sh --teeth" >&2
  exit 2
fi

ROOT="$(git rev-parse --show-toplevel)"

if ! git -C "$ROOT" remote get-url "$REMOTE" >/dev/null 2>&1; then
  echo "release-preflight: no '$REMOTE' remote — set A2A_RELEASE_REMOTE to the release remote." >&2
  exit 1
fi

echo "release-preflight: fetching tags from '$REMOTE' (the authoritative release state)…"
git -C "$ROOT" fetch --quiet "$REMOTE" --tags

rc=0
assert_version_free "$ROOT" "$VERSION" || rc=1
assert_pins_resolve "$ROOT" || rc=1

if [ "$rc" -ne 0 ]; then
  echo "release-preflight: FAIL — do not cut $VERSION until the above is fixed." >&2
  exit 1
fi
echo "release-preflight: OK — safe to cut $VERSION."
