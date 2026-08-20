# syntax=docker/dockerfile:1
#
# ci-parity.Dockerfile — the GNU-userland half of the release gate.
#
# Why this image exists. `make ci-parity` proves that every command CI runs is
# also run locally. It cannot prove that running it produces the same verdict,
# because it runs on darwin/arm64 with BSD userland while every non-notifier CI
# job runs on ubuntu-latest with GNU userland. That gap has cost real work in
# both directions: a `sed '0,/re/s//to/'` fix authored here is GNU-only and BSD
# rejects address 0, and an `awk -F'\x1f'` gate authored under GNU silently
# mis-parsed on macOS. Either direction ships a gate that is green on the
# surface it was written on and red on the other.
#
# WHAT THIS PROVES, precisely — say it, so nobody reads more into it:
#   • GNU userland parity: bash, sed, awk (mawk, ubuntu's default), grep,
#     coreutils, and a Linux Go/Node toolchain.
#   • NOT amd64 parity. This builds for the host architecture, which on an
#     Apple-silicon machine is arm64 while ubuntu-latest is amd64. Emulating
#     amd64 turns a 15-minute ceiling into an hour, and the defect class being
#     bought here — userland, not instruction set — is identical on both.
#   • NOT the macOS half of CI. Two jobs run on `macos-15` (`macos-notifier` in
#     ci.yml, and the darwin build in release.yml). Those are covered by running
#     `make ci-parity` on the host, which is why the runbook keeps both.
#
# NO TOOLCHAIN VERSION IS WRITTEN IN THIS FILE. Every one arrives as a build
# arg that scripts/ci-parity-docker.sh derives from the repository's own SSOT —
# go.mod for Go, the workflows for Node/actionlint/gitleaks, package-lock.json
# for Playwright. A pin typed here would be a fourth copy of a version that
# already lives somewhere authoritative, and the next bump would silently leave
# this image testing a different compiler: the exact false-green class the whole
# parity effort exists to close. Each ARG therefore fails closed on empty.

ARG UBUNTU_TAG=24.04
FROM ubuntu:${UBUNTU_TAG}

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ENV DEBIAN_FRONTEND=noninteractive

# mawk is named explicitly and gawk is deliberately NOT installed: ubuntu's
# default `awk` is mawk, and installing gawk here would quietly grant the image
# extensions CI does not have.
# ripgrep is here because the ubuntu-latest runner image ships it and several
# gates require it by name (`operational-confidence-guard` refuses outright
# without it). Anything the runner preinstalls and a gate reads belongs in this
# list — a missing tool turns a parity run into a tooling failure that looks
# like a product failure.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git make rsync jq shellcheck ripgrep \
      xz-utils unzip gnupg mawk coreutils findutils diffutils file procps \
 && rm -rf /var/lib/apt/lists/*

ARG GO_VERSION
RUN test -n "${GO_VERSION}" || { echo "GO_VERSION is empty — it is derived from go.mod by scripts/ci-parity-docker.sh; there is no default on purpose." >&2; exit 1; } \
 && arch="$(dpkg --print-architecture)" \
 && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" -o /tmp/go.tgz \
 && tar -C /usr/local -xzf /tmp/go.tgz \
 && rm /tmp/go.tgz
ENV PATH="/usr/local/go/bin:/root/go/bin:${PATH}"

ARG NODE_MAJOR
RUN test -n "${NODE_MAJOR}" || { echo "NODE_MAJOR is empty — it is derived from .github/workflows/ by scripts/ci-parity-docker.sh." >&2; exit 1; } \
 && curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && rm -rf /var/lib/apt/lists/*

# gitleaks: CI downloads the linux_x64 archive because its runner is amd64.
# This resolves the architecture instead, which is the one place the image
# deliberately differs from the workflow — same version, same rules, native
# build.
ARG GITLEAKS_VERSION
RUN test -n "${GITLEAKS_VERSION}" || { echo "GITLEAKS_VERSION is empty — derived from .github/workflows/gitleaks.yml." >&2; exit 1; } \
 && case "$(dpkg --print-architecture)" in \
      amd64) a=x64 ;; \
      arm64) a=arm64 ;; \
      *) echo "unsupported architecture for a gitleaks release archive" >&2; exit 1 ;; \
    esac \
 && curl -fsSL "https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/gitleaks_${GITLEAKS_VERSION}_linux_${a}.tar.gz" \
      | tar -xz -C /usr/local/bin gitleaks \
 && gitleaks version

ARG ACTIONLINT_VERSION
RUN test -n "${ACTIONLINT_VERSION}" || { echo "ACTIONLINT_VERSION is empty — derived from .github/workflows/ci.yml." >&2; exit 1; } \
 && go install "github.com/rhysd/actionlint/cmd/actionlint@${ACTIONLINT_VERSION}"

# govulncheck is @latest in the workflow, so it is @latest here. Pinning it
# would make this image disagree with CI on the one gate whose whole job is to
# know today's vulnerability set.
RUN go install golang.org/x/vuln/cmd/govulncheck@latest

# Browsers are baked into a layer rather than fetched per run: ~500MB, and it is
# the difference between a two-minute and a twelve-minute suite.
ARG PLAYWRIGHT_VERSION
RUN test -n "${PLAYWRIGHT_VERSION}" || { echo "PLAYWRIGHT_VERSION is empty — derived from web/package-lock.json." >&2; exit 1; } \
 && npx --yes "playwright@${PLAYWRIGHT_VERSION}" install --with-deps chromium \
 && rm -rf /root/.npm/_cacache

COPY ci-parity-entrypoint.sh /usr/local/bin/parity-entrypoint
RUN chmod +x /usr/local/bin/parity-entrypoint

WORKDIR /work
ENTRYPOINT ["/usr/local/bin/parity-entrypoint"]
CMD ["make", "ci-parity"]
