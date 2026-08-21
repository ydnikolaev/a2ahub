#!/usr/bin/env bash
# ci-parity-docker.sh — run the CI-parity suite under ubuntu + GNU userland.
#
# `make ci-parity` answers "does every command CI runs also run here?".
# This answers the question underneath it: "and does running it there give the
# same verdict?" See scripts/ci-parity.Dockerfile for exactly what that proves
# and — just as important — what it does not (it is not amd64 parity, and it is
# not the macos-15 half of CI, which the host run covers).
#
# NOT auto-selectable by `make lane`: it needs a Docker daemon, it pulls a base
# image on first build, and it runs the full ceiling inside the container.
# Reached only through `make ci-parity-docker`, and named in the release runbook
# as a ship gate, never a commit gate.
#
# Usage:
#   bash scripts/ci-parity-docker.sh                 # the full parity suite
#   bash scripts/ci-parity-docker.sh make check      # one gate, same image
#   bash scripts/ci-parity-docker.sh bash            # a shell, to reproduce a red
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

IMAGE=${PARITY_IMAGE:-a2ahub-ci-parity:local}
# A fixed, repo-named container rather than docker's random two-word name: this
# thing runs for twenty minutes and shows up in Docker Desktop next to every
# other project's containers, where `flamboyant_hoover` tells nobody what it is
# or whether it is safe to stop.
CONTAINER=${PARITY_CONTAINER:-a2ahub-ci-parity}

# ── Derive every toolchain version from the repository's own SSOT. ────────────
# Nothing below is a constant. Each read fails closed with the file it was
# supposed to find the answer in, because an empty build arg would otherwise
# become a stale pin baked into an image that then tests the wrong compiler
# while reporting green — the precise defect class this harness exists to close.
die() { printf 'ci-parity-docker: %s\n' "$1" >&2; exit 1; }

GO_VERSION="$(awk '$1 == "go" && $2 ~ /^[0-9]+\.[0-9]+\.[0-9]+$/ { print $2; exit }' go.mod)"
[ -n "$GO_VERSION" ] || die "go.mod names no patch-level go directive — \`make workflow-lint\` refuses the same thing and explains why."

NODE_MAJOR="$(grep -hoE '^[[:space:]]*node-version:[[:space:]]*[0-9]+' .github/workflows/*.yml \
  | grep -oE '[0-9]+$' | sort -u)"
[ -n "$NODE_MAJOR" ] || die "no workflow declares a node-version — nothing to derive Node from."
[ "$(printf '%s\n' "$NODE_MAJOR" | wc -l | tr -d ' ')" = 1 ] \
  || die "workflows disagree on Node major ($(printf '%s' "$NODE_MAJOR" | tr '\n' ' ')) — the container cannot be parity with two of them."

GITLEAKS_VERSION="$(grep -oE 'GITLEAKS_VERSION:[[:space:]]*[0-9]+(\.[0-9]+)*' .github/workflows/gitleaks.yml \
  | head -1 | grep -oE '[0-9]+(\.[0-9]+)*')"
[ -n "$GITLEAKS_VERSION" ] || die ".github/workflows/gitleaks.yml declares no GITLEAKS_VERSION."

GOLANGCI_VERSION="$(grep -hoE 'golangci-lint/v[0-9]+(\.[0-9]+)*/install\.sh' .github/workflows/*.yml \
  | sed -E 's#golangci-lint/(v[0-9.]+)/install\.sh#\1#' | sort -u)"
[ -n "$GOLANGCI_VERSION" ] || die "no workflow installs golangci-lint — \`make check\` refuses without it, so the image needs the version CI uses."
[ "$(printf '%s\n' "$GOLANGCI_VERSION" | wc -l | tr -d ' ')" = 1 ] \
  || die "workflows disagree on the golangci-lint version ($(printf '%s' "$GOLANGCI_VERSION" | tr '\n' ' '))."

ACTIONLINT_VERSION="$(grep -hoE 'actionlint@v[0-9]+(\.[0-9]+)*' .github/workflows/*.yml \
  | sed 's/.*@//' | sort -u)"
[ -n "$ACTIONLINT_VERSION" ] || die "no workflow installs a pinned actionlint — \`make workflow-lint\` needs one in the image."
[ "$(printf '%s\n' "$ACTIONLINT_VERSION" | wc -l | tr -d ' ')" = 1 ] \
  || die "workflows disagree on the actionlint version ($(printf '%s' "$ACTIONLINT_VERSION" | tr '\n' ' '))."

PLAYWRIGHT_VERSION="$(jq -r '.packages["node_modules/@playwright/test"].version // empty' web/package-lock.json 2>/dev/null || true)"
[ -n "$PLAYWRIGHT_VERSION" ] || die "web/package-lock.json resolves no @playwright/test version — the browser layer would not match the suite that runs against it."

printf 'ci-parity-docker: go %s · node %s · gitleaks %s · golangci-lint %s · actionlint %s · playwright %s\n' \
  "$GO_VERSION" "$NODE_MAJOR" "$GITLEAKS_VERSION" "$GOLANGCI_VERSION" "$ACTIONLINT_VERSION" "$PLAYWRIGHT_VERSION"

command -v docker >/dev/null 2>&1 || die "docker is not on PATH."
docker info >/dev/null 2>&1 || die "the Docker daemon is not reachable — start Docker Desktop."

# The build context is scripts/, not the repo: the image needs its entrypoint
# and nothing else, and a 3GB context would be sent on every invocation.
docker build \
  --file scripts/ci-parity.Dockerfile \
  --build-arg "GO_VERSION=$GO_VERSION" \
  --build-arg "NODE_MAJOR=$NODE_MAJOR" \
  --build-arg "GITLEAKS_VERSION=$GITLEAKS_VERSION" \
  --build-arg "GOLANGCI_VERSION=$GOLANGCI_VERSION" \
  --build-arg "ACTIONLINT_VERSION=$ACTIONLINT_VERSION" \
  --build-arg "PLAYWRIGHT_VERSION=$PLAYWRIGHT_VERSION" \
  --tag "$IMAGE" \
  scripts

# Named volumes, not bind mounts: see the entrypoint's header for why the host
# tree is read-only and what would otherwise be clobbered.
# A crashed daemon can leave the name held by a container `--rm` never got to
# remove. Reclaim a dead one; refuse a live one rather than killing a run that
# may be somebody else's.
if state="$(docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null)"; then
  if [ "$state" = "true" ]; then
    die "a container named $CONTAINER is already running — wait for it, or stop it deliberately."
  fi
  docker rm "$CONTAINER" >/dev/null
fi

# WHAT THIS CONTAINER RUNS IS NAMED HERE, NOT INHERITED FROM THE IMAGE.
#
# The Dockerfile's `CMD ["make", "ci-parity"]` was the default until
# 2026-08-21, and it stopped being the right one when the macos-15 delta became
# an executed step rather than an excuse: `make ci-parity` is now audit + the
# portable suite + the macOS delta, and the macOS delta cannot run on Linux —
# it would refuse here, correctly and uselessly, every run.
#
# `--suite` is the userland-portable half: everything CI runs that is not tied
# to a macOS toolchain. It is the composed release gate's PRIMARY phase, and it
# is the container that runs it, because the container is the better judge on
# two axes at once — GNU userland (where every non-notifier CI job actually
# runs) and the TRACKED tree (the entrypoint syncs and `git clean`s, so this
# judges what publishes, while the host judges a working tree carrying
# generated, gitignored files).
#
# An explicit argument still wins, so `bash scripts/ci-parity-docker.sh make check`
# and `... bash` keep working exactly as documented above.
if [ "$#" -eq 0 ]; then
  set -- bash scripts/ci-parity.sh --suite
fi

exec docker run --rm -i \
  --name "$CONTAINER" \
  ${PARITY_TTY:+-t} \
  -v "$ROOT:/src:ro" \
  -v a2ahub-parity-work:/work \
  -v a2ahub-parity-state:/parity-state \
  -v a2ahub-parity-gocache:/root/.cache/go-build \
  -v a2ahub-parity-gomod:/root/go/pkg/mod \
  "$IMAGE" "$@"
