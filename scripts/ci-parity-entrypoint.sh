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

[ -d "$SRC/.git" ] || { echo "parity: $SRC is not a git checkout — the host repo must be mounted read-only there." >&2; exit 1; }

excludes=$(mktemp)
trap 'rm -f "$excludes"' EXIT
cat > "$excludes" <<'EXCL'
/.a2a/
/integrations/
/dist/
/a2a
/coverage.out
/.parity-npm-stamp
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

# `npm ci` only when the lockfile actually changed. CI runs it unconditionally
# because its runner starts empty; here the volume persists, and reinstalling
# 350MB per run buys nothing.
lock="$WORK/web/package-lock.json"
if [ -f "$lock" ]; then
  want=$(sha256sum "$lock" | cut -d' ' -f1)
  have=$(cat "$WORK/.parity-npm-stamp" 2>/dev/null || true)
  if [ "$want" != "$have" ] || [ ! -d "$WORK/web/node_modules" ]; then
    echo "parity: npm ci (lockfile changed or node_modules absent)"
    (cd "$WORK/web" && npm ci)
    printf '%s' "$want" > "$WORK/.parity-npm-stamp"
  else
    echo "parity: npm ci skipped — lockfile unchanged since the last run"
  fi
fi

echo "parity: $(uname -s)/$(uname -m) · go $(go version | awk '{print $3}') · node $(node --version) · awk $(awk --version 2>&1 | head -1)"
exec "$@"
