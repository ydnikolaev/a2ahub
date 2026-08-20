#!/usr/bin/env bash
# ci-parity-entrypoint.sh — runs INSIDE the parity container, never on a host.
#
# The host repository is mounted READ-ONLY at /src and synced into /work, a
# named volume. That indirection is not caution for its own sake; three
# collisions make a writable bind mount wrong:
#
#   • `make check` builds ./a2a. In the container that is an ELF binary, and it
#     would overwrite the host's Mach-O one — every subsequent host-side gate
#     then dies with "exec format error" and blames the wrong thing.
#   • web/node_modules carries darwin/arm64 native builds. Linux npm would
#     either refuse them or replace them, breaking the host afterwards.
#   • web/dist, coverage.out and test-results would land in the host tree as
#     Linux artefacts of a run nobody asked to keep.
#
# /work being a named volume also means node_modules, the Go build cache and the
# module cache survive between runs, so a second parity run is minutes rather
# than a rebuild.
#
# What is NOT excluded, and why it matters: .git. `check-feedback-corpus` reads
# `refs/remotes/public/feedback-hub`, `gitleaks git` walks history, and
# `lane-run-strict` derives its file set from `git diff HEAD~1`. A fresh clone
# would carry none of those, so the copy is a copy, refs included.
set -euo pipefail

SRC=${PARITY_SRC:-/src}
WORK=${PARITY_WORK:-/work}
# Container state lives OUTSIDE the tree under judgement. It used to be a
# `.parity-npm-stamp` at the work root, and `classify-guard` refused it on
# sight — correctly: an unclassified top-level entry is exactly what that gate
# exists to catch, and a harness that adds files to the tree it is judging is
# measuring itself. Its own volume, so the copy stays byte-faithful to /src.
STATE=${PARITY_STATE:-/parity-state}

[ -d "$SRC/.git" ] || { echo "parity: $SRC is not a git checkout — the host repo must be mounted read-only there." >&2; exit 1; }

excludes=$(mktemp)
trap 'rm -f "$excludes"' EXIT
# EVERY LINE HERE MUST BE UNTRACKED. `/integrations/` was on this list until
# 2026-08-20 for its size (564MB) — and it is a tracked directory: only 352KB
# across 40 files is tracked, the rest is four ignored build trees. So
# `git ls-files` in the copy named files that were not there, and
# `check-notify-secrets` reported that grep rejected all fourteen of its secret
# shapes (exit 2 is grep's "a file could not be read", not "bad pattern").
# Exclude the ignored subtrees by name, never their tracked parent.
cat > "$excludes" <<'EXCL'
/.a2a/
/dist/
/a2a
/coverage.out
integrations/macos-notifier/.build/
integrations/macos-notifier/dist/
integrations/vscode/dist/
integrations/vscode/node_modules/
web/node_modules/
web/dist/
web/test-results/
web/playwright-report/
EXCL

echo "parity: syncing $SRC → $WORK"
# --delete keeps /work honest about deletions on the host. Excluded paths are
# protected from it by default (that is rsync's rule, and it is the reason
# node_modules and the npm stamp survive the sync).
rsync -a --delete --exclude-from="$excludes" "$SRC/" "$WORK/"

cd "$WORK"

# git refuses to operate on a tree it thinks belongs to someone else. The copy
# is root-owned inside a container nobody else touches, so the declaration is
# accurate rather than a workaround.
git config --global --add safe.directory "$WORK"

# THE COPY MUST BE THE REPOSITORY. An exclude that names a tracked path leaves
# `git ls-files` describing files that are not on disk, and gates that walk the
# tracked set then fail for reasons that have nothing to do with the repo — the
# defect above, found the expensive way. Deletions relative to the host tree are
# the exact signature, so they are refused here rather than diagnosed later.
deleted="$(git -C "$WORK" ls-files --deleted | head -20)"
if [ -n "$deleted" ]; then
  echo "parity: the synced copy is missing tracked files — an exclude names a tracked path:" >&2
  printf '%s\n' "$deleted" | sed 's/^/  /' >&2
  echo "  Fix the exclude list in scripts/ci-parity-entrypoint.sh; this run would judge a tree that is not the repository." >&2
  exit 1
fi

# `npm ci` only when the lockfile actually changed. CI runs it unconditionally
# because its runner starts empty; here the volume persists, and reinstalling
# 350MB per run buys nothing.
lock="$WORK/web/package-lock.json"
if [ -f "$lock" ]; then
  mkdir -p "$STATE"
  want=$(sha256sum "$lock" | cut -d' ' -f1)
  have=$(cat "$STATE/npm-stamp" 2>/dev/null || true)
  if [ "$want" != "$have" ] || [ ! -d "$WORK/web/node_modules" ]; then
    echo "parity: npm ci (lockfile changed or node_modules absent)"
    (cd "$WORK/web" && npm ci)
    printf '%s' "$want" > "$STATE/npm-stamp"
  else
    echo "parity: npm ci skipped — lockfile unchanged since the last run"
  fi
fi

echo "parity: $(uname -s)/$(uname -m) · go $(go version | awk '{print $3}') · node $(node --version) · awk $(awk --version 2>&1 | head -1)"
exec "$@"
