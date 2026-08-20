#!/usr/bin/env bash
# ci-parity — run what CI runs, locally, and refuse when the two lists drift.
#
# lane-inputs: ALWAYS
# lane-reason: it reads every .github/workflows/*.yml and compares them against
#   its own execution list; any workflow edit can change its verdict, and it
#   claims no path of its own.
#
# WHY THIS EXISTS, in one paragraph, because the cost is documented.
#
# Before 2026-08-20 no local command ran what CI ran, and the gap was not
# theoretical:
#
#   - `make harness-check` — the gates' own --teeth — is reached by NO local
#     flow. `make check`'s own comment says it does not reach the teeth;
#     `make lane` selects harness-check only when a gate script changes; the
#     release runbook's step 5 is `make check`. So the card-content tooth went
#     FALSE GREEN on 2026-08-13 and nobody saw it for a week.
#   - `gitleaks` has no local runner at all — its only executor is a workflow.
#     A fabricated credential fixture added on 2026-08-18 was invisible locally
#     until the release push became the first CI run to see it, and was then
#     chased as a possible leak.
#   - CI runs `make lane-run-strict`; the convention tells humans to run
#     `make lane-run`. Different target, different refusal.
#
# The point is NOT that CI is authoritative. It is that neither surface knew
# what the other skipped, so "green" meant different things in each and nobody
# could say which.
#
# TWO LIMITS THIS CANNOT SEE, both worth knowing before trusting a green:
#
#   - It compares COMMANDS, not conditions. A job gated on `if:` that evaluates
#     false on every run still contributes its `run:` lines, so the audit reports
#     them covered while CI executes them never. `ci.yml`'s `web` job is exactly
#     that today: added 2026-08-14, `skipped` in 100% of runs since, so
#     `dashboard-template-drift` and `npm run check:unit` have never once
#     executed in CI. The audit said "covered" throughout.
#   - It runs on darwin with BSD userland; every non-notifier CI job runs on
#     Linux with GNU userland. Same command, different verdict — three live
#     defects on 2026-08-20 alone. `make ci-parity-docker` is the half that
#     closes it, and the release runbook names both.
#
# THE LIST IS DERIVED, NOT DUPLICATED. `--audit` extracts every `run:` command
# from every workflow and refuses when one is neither executed here nor
# explicitly excused below. A hand-copied list would drift exactly the way the
# two secret scanners drifted.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# EXCUSED — each with the reason it cannot or should not run locally. An entry
# here is a decision, not a hiding place: it prints on every audit.
excused_reason() { # $1 = command
  case "$1" in
    *"go install"*|*"go mod download"*|*"npm ci"*|*"playwright install"*)
      echo "provisioning, not a check" ;;
    echo\ *)
      echo "a workflow's own explanatory echo, not a check" ;;
    *"a2a feedback validate --ci"*)
      echo "runs inside a space's own CI against a submitted file, not this repo" ;;
    *"npm run build"*)
      echo "covered by 'npm run check', which invokes it as prebuild+build" ;;
    *"gitleaks version"*|*"install -m 0755 gitleaks"*|*"curl"*|*"tar "*|*"sha256sum"*)
      echo "installs the pinned gitleaks binary; the scans themselves are executed below" ;;
    *) return 1 ;;
  esac
}

# EXECUTED — the local equivalent of each CI command, in CI's own order.
run_step() { # $1 = label, $2... = command
  local label="$1"; shift
  printf '\n=== ci-parity: %s ===\n' "$label"
  if "$@"; then
    printf 'ci-parity: %s OK\n' "$label"
    return 0
  fi
  printf 'ci-parity: %s FAILED\n' "$label" >&2
  return 1
}

audit() {
  local cmds executes missing=0
  # The coverage claim is the EXECUTES block itself, read from this file — not
  # a second list. The markers carry a leading '#', and getting that wrong once
  # made every command look uncovered, which is the same class of bug this
  # script exists to catch.
  executes="$(sed -n '/^# EXECUTES_BEGIN/,/^# EXECUTES_END/p' "$ROOT/scripts/ci-parity.sh")"
  cmds="$(grep -h 'run: ' .github/workflows/*.yml \
    | grep -vE '^\s*#' \
    | sed 's/.*run: //' \
    | sed 's/^[[:space:]]*//; s/[[:space:]]*$//' \
    | grep -v '^|$' \
    | sort -u)"
  while IFS= read -r c; do
    [ -n "$c" ] || continue
    if printf '%s\n' "$executes" | grep -qF -- "$c"; then
      continue
    fi
    local why
    if why="$(excused_reason "$c")"; then
      printf 'ci-parity: excused — %s (%s)\n' "$c" "$why"
      continue
    fi
    printf 'ci-parity: UNCOVERED — no local step runs %s\n' "$c" >&2
    missing=$((missing + 1))
  done <<< "$cmds"
  if [ "$missing" -gt 0 ]; then
    printf '\nci-parity: %d CI command(s) run nowhere locally.\n' "$missing" >&2
    printf 'Add a step to EXECUTES below, or an entry to excused_reason() with its reason.\n' >&2
    return 1
  fi
  printf '\nci-parity: every CI command is executed locally or excused with a reason.\n'
}

# EXECUTES_BEGIN
# Each run_step's FIRST argument is the CI command VERBATIM — that is what the
# audit greps for, so a label and its coverage claim cannot drift apart. The
# remaining arguments are how this repo invokes the same thing locally, which
# sometimes differs (npm needs --prefix web; the strict lane needs an explicit
# file set because nothing changed against HEAD locally).
execute_all() {
  run_step "bash scripts/ci-changes.sh"                bash scripts/ci-changes.sh
  run_step "bash scripts/ci-skill-drift.sh"            bash scripts/ci-skill-drift.sh
  run_step "bash scripts/dashboard-template-drift.sh"  bash scripts/dashboard-template-drift.sh
  run_step "make check"                                make check
  run_step "make harness-check"                        make harness-check
  run_step "make lane-run-strict"                      env LANE_FILES="${LANE_FILES:-$(git diff --name-only HEAD~1 2>/dev/null | tr '\n' ' ')}" make lane-run-strict
  run_step "make vulncheck"                            make vulncheck
  run_step "npm run check:unit"                        npm --prefix web run check:unit
  run_step "npm run check"                             npm --prefix web run check
  run_step "npx playwright test tests/dashboard-visual-contract.spec.mjs" \
    sh -c 'cd web && npx playwright test tests/dashboard-visual-contract.spec.mjs'
  run_step "gitleaks dir"                              gitleaks dir --config .gitleaks.toml --redact .
  run_step "gitleaks git"                              gitleaks git --config .gitleaks.toml --redact .
}
# EXECUTES_END

case "${1:---run}" in
  --audit) audit ;;
  --run)   audit && execute_all ;;
  *) printf 'usage: %s [--run|--audit]\n' "$0" >&2; exit 2 ;;
esac
