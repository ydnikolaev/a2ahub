#!/usr/bin/env bash
# ci-parity — run what CI runs, locally, and refuse when the two lists drift.
#
# lane-inputs: ALWAYS
# lane-reason: it reads every .github/workflows/*.yml and compares them against
#   its own execution list; any workflow edit can change its verdict, and it
#   claims no path of its own.
# lane-claims:
#   docs/runbooks/release.md
#   scripts/lib/gate-lib.sh
# lane-reads-opaque: the dogfood EXECUTES step and tooth T1 both `cat "$report"`,
#   a file this script CREATES with mktemp and deletes — it is the validator's
#   own JSON output, never a repository input, so no repo path can flip a
#   verdict through it. Declared rather than silenced: the classifier is right
#   that it cannot resolve the path, and an unresolved read that nobody explains
#   is how a gate ends up judging something it never named.
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

# A COMPOSITION IS NEVER AN ITERATING HUMAN (release-cost-2026-08 P2).
# verify.sh refuses to re-run a mode over a byte-identical tree that already
# passed — the right answer for somebody typing `make check` twice, and the
# wrong one for a ship gate, which runs `make check` inside a container whose
# work volume PERSISTS BETWEEN RUNS. Two ship gates over the same clean tree
# would otherwise meet the guard in there and report the ceiling as exit 1.
# Exported once, at the entry to every ci-parity path, rather than sprinkled
# at the call sites that happen to be remembered.
export VERIFY_AGAIN=1

# gate_unmeasured, and the exit code that separates "I measured it and it is
# wrong" (1) from "I could not measure it" (3). A composed SHIP gate is the
# worst possible place to blur those two: "Docker is down" and "the tree is
# broken" are opposite instructions to the operator, and only one of them is a
# statement about shippability. Sourced, never executed (the lib guards its own
# --teeth on BASH_SOURCE), and tracked + unstripped, so the projection's
# `make check` reaches it the same way every other gate's does.
# shellcheck source=lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

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
    # NARROWED 2026-08-23. This arm used to read
    #   *"gitleaks version"*|*"install -m 0755 gitleaks"*|*"curl"*|*"tar "*|*"sha256sum"*
    # and the three bare globs at the end excused ANY command containing
    # `curl`, `tar ` or `sha256sum` — under a reason naming gitleaks. The
    # golangci-lint install in ci.yml, which NO local step runs, was reported
    # as "installs the pinned gitleaks binary". This audit is the declared
    # SSOT for what CI runs, and it was answering with a claim it had not
    # checked — the epic's own defect class, inside the gate the epic built.
    #
    # Every glob below now names gitleaks or the variable its own step
    # defines, so a command from another step cannot wear this excuse.
    *"gitleaks version"* \
    | *"install -m 0755 gitleaks"* \
    | *'archive="gitleaks_'* \
    | *"releases/download/v\${GITLEAKS_VERSION}"* \
    | *'echo "${GITLEAKS_SHA256}'* \
    | *'tar -xzf "$archive" gitleaks'*)
      echo "installs the pinned gitleaks binary; the scans themselves are executed below" ;;

    # --- P1 close, 2026-08-21: the lead's triage of the remaining 73. ------

    # Group 3 — release.yml, tag-triggered release machinery (~20). Runs only
    # on a version tag, against a real GitHub release — goreleaser build,
    # cosign signing and asset upload need a tag and credentials that do not
    # exist locally. What proves this path is the release runbook's own
    # post-tag verification, not a local re-run.
    *": > dist/SHA256SUMS"* \
    | *'sha256sum "$asset" | sed'* \
    | *"cosign sign-blob"* \
    | *'cp "releasenotes/'* \
    | *"find dist -maxdepth 1 -type f"* \
    | *'gh release download "$GITHUB_REF_NAME"'* \
    | *"gh release upload "* \
    | *'gh release view "$GITHUB_REF_NAME"'* \
    | *'go run ./scripts/releasebody'* \
    | *'if [ ! -f "$notes" ]; then'* \
    | *'mkdir -p dist'* \
    | *'name="$(basename "$asset")"'* \
    | *"npm run package:vsix"* \
    | *'npm version "${GITHUB_REF_NAME#v}"'* \
    | *"scripts/build-release-cohort.sh dist"* \
    | *'SHA256SUMS|*.cosign.bundle) continue ;;'* \
    | *'while IFS= read -r asset; do'*)
      echo "runs only on a version tag, against a real GitHub release — goreleaser build, cosign signing and asset upload need a tag and credentials that do not exist locally. What proves this path is the release runbook's own post-tag verification, not a local re-run." ;;

    # Group 4 — feedback-intake.yml, hub intake (~22). Runs on
    # pull_request_target against a feedback file submitted by an incoming
    # PR; there is no such event locally. The validator it drives is
    # exercised by its own Go tiers.
    *'arm-feedback: invalid kind output'* \
    | *"verdict-diff /tmp/record-base.yaml"* \
    | *"chmod +x ./a2a"* \
    | *"grep -cE '^kind:"* \
    | *'elif [ "$ARM_RESULT" != "skipped" ]; then'* \
    | *'gh api "repos/${REPO}/contents/'* \
    | *'gh api "repos/${REPO}/pulls/${PR_NUMBER}/files"'* \
    | *"enablePullRequestAutoMerge"* \
    | *'gh label create "feedback'* \
    | *'gh pr edit "${PR_NUMBER}"'* \
    | *'gh release download "v${A2A_INTAKE_VERSION}"'* \
    | *'if [ "$ARM_RESULT" != "success" ]; then'* \
    | *'if [ "$count" -ne 1 ]; then'* \
    | *'if [ "$IS_FEEDBACK" = "true" ]; then'* \
    | *'if [ "$POLICY_RESULT" != "success" ]; then'* \
    | *'kind=$(grep -E'* \
    | *'mkdir -p "$(dirname "$FILE_PATH")"'* \
    | *'node_id=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}"'* \
    | *'test -s "$RUNNER_TEMP/policy.sh"'*)
      echo "runs on pull_request_target against a feedback file submitted by an incoming PR; there is no such event locally. The validator it drives is exercised by its own Go tiers." ;;

    # Group 5 — macos-notifier. THE GAP IS CLOSED, AND ONLY ONE LINE OF IT IS
    # STILL EXCUSED (release-loop-2026-08 P4, 2026-08-21).
    #
    # This entry used to excuse all three of the macos-15 commands with
    # "NOTHING local or containerised covers these two jobs". That was true of
    # the CONTAINER and false of the HOST: `ci.yml`'s check job is
    # `runs-on: ubuntu-latest`, so a darwin host run is parity with nothing in
    # CI *except* these two jobs — and this machine has the toolchain
    # (`/usr/bin/swift`, Apple Swift 6.2.4). The excuse was covering for the
    # fact that nobody had pointed the host at the one thing only the host can
    # judge. They are now EXECUTES members: see macos_delta() below, which runs
    # them and reports gate_unmeasured — never a silent skip — when the
    # toolchain or the platform is absent.
    #
    # What remains excused is release.yml's variant of the SAME script, which
    # differs from the ci.yml invocation only in where VERSION comes from.
    *'VERSION="${VERSION#v}" integrations/macos-notifier/scripts/package-release.sh'*)
      echo "the same script the ci.yml macos-15 step runs below, in the same SIGNING_MODE=adhoc mode; the only difference is VERSION, which release.yml derives from the tag being cut and which does not exist locally" ;;

    # `make lane-run-strict` — LEFT the composition, 2026-08-21, and this is
    # the coverage argument that lets it. It is a "covered by" excuse of the
    # same shape as the `npm run build` one above, and it is derived, not
    # asserted:
    #
    #   * `scripts/verify.sh`'s MODE=full ordered set is run_repo_gates plus
    #     gofmt, vet, golangci-lint, go-test, coverage-policy and logic-e2e.
    #     `run_derived_phase` can emit exactly those names plus three others:
    #     harness-teeth, web-quality and projection, and `go-test-scoped:<pkg>`
    #     — which is `go test <pkg>`, a strict subset of go-test's `./...`.
    #   * All three of the others are EXECUTES members in their own right:
    #     `make harness-check` (harness-teeth), `npm run check:quality`
    #     (web-quality's own first half) and `make projection`.
    #   * What the lane adds beyond the phases is the DERIVATION REFUSAL — a
    #     changed path no gate claims. `make lane` is exactly that, derive-only,
    #     and it is an EXECUTES member below.
    #
    # So every phase this command can run is run by the suite around it, and
    # running it again re-pays them. Measured on this repo's own telemetry
    # (.a2a/cache/verify/telemetry.jsonl, 2026-08-21): mode=full sums to 845 s
    # of medians, of which logic-e2e alone is 542 s; `make lane` on a single Go
    # file selects 26 phases including `projection` and `logic-e2e`. That
    # duplicate was being paid ONCE PER USERLAND.
    #
    # THE ONE THING THIS EXCUSE DOES NOT COVER, said out loud: CI runs this
    # target over a PULL REQUEST's changed set, and the local invocation could
    # only ever run it over `git diff HEAD~1`, a different set entirely. The
    # audit could never have proved the derivation right for CI's input; what
    # it can prove is that no phase goes unrun, which is what the list above
    # does.
    "make lane-run-strict")
      echo "every phase it can derive is run by the suite around it — MODE=full covers all of them except harness-teeth, web-quality and projection, each an EXECUTES member of its own ('make harness-check', 'npm run check:quality', 'make projection'), and the derivation refusal it adds is 'make lane', also below. Re-running it re-paid a whole ceiling per userland." ;;

    # Group 7 — shell scaffolding (set/exit lines with no check of their own).
    "set -e" | "set +e" | "set -euo pipefail" | "set -o pipefail" \
    | "exit 1" | "exit 2" | 'exit "$status"')
      echo "shell scaffolding, not a check" ;;

    # ci.yml's own ripgrep provisioning for `make check` (job `check`) and
    # `make lane-run-strict` (job `lane`) — same rationale as the `go
    # install`/`npm ci` case above: a dev machine already has (or lacks, same
    # as the runner would) ripgrep; `make check`/`make lane-run-strict` below
    # are the actual checks. NOT one of the lead's seven named groups —
    # ADDED here because the audit cannot reach 0 uncovered without it and
    # the rationale is identical to the existing provisioning case just
    # above (flagged as a deviation in this wave's report).
    *"apt-get install -y -qq ripgrep"* | *"rg --version"*)
      echo "provisioning, not a check" ;;

    # golangci-lint's own installer. Surfaced UNCOVERED on 2026-08-23 the
    # moment the gitleaks arm above stopped excusing every command containing
    # `curl` — until then this call, which NO local step runs, was reported as
    # "installs the pinned gitleaks binary". It is provisioning: `make
    # golangci-lint` runs the linter itself and IS executed below.
    #
    # Recorded rather than smoothed over: unlike the gitleaks install beside
    # it, this one is `curl | sh` with NO checksum and NO signature — it is
    # pinned by version in the URL and nothing verifies what arrives.
    # docs/backlog.md carries that as its own row; this excuse covers the
    # command not being locally re-run, and claims nothing about its integrity.
    # The download RETRY LOOPS' own control flow — gitleaks.yml's install and
    # the two reusable workflows' `Install a2a`. One arm because the three are
    # deliberately the same shape, and the reason is true of all three: a loop
    # that exists so a transient CDN read does not red a job has nothing for a
    # local step to reproduce. Locally the tools are provisioned by the
    # developer's own environment, and the SCANS and VALIDATIONS those loops
    # feed are what the composed gate executes.
    #
    # Narrow on purpose: these globs name the loop's own variables (`$rc`,
    # `$attempt`, `$archive`, `$RUNNER_TEMP/$asset`), not `curl`. The arm this
    # sits beside used to end in a bare *"curl"* and excused a golangci-lint
    # install under a gitleaks reason for it.
    *'curl -fsSL -o "$archive" "$url"'* \
    | *'curl -fsSL -o "$RUNNER_TEMP/$asset" "$base/$asset"'* \
    | *'if [ "$rc" -eq 0 ]; then'* \
    | *'if [ "$rc" -eq 22 ]; then'* \
    | *'[ "$attempt" -lt 3 ] || break'*)
      echo "a download retry loop's own control flow; the scan or validation it feeds is executed below" ;;

    *"golangci-lint/v2.12.2/install.sh"*)
      echo "provisioning, not a check (installs the linter; \`make golangci-lint\` runs it below)" ;;

    # The release tags live on the PUBLISHED repository; this private source
    # remote carries exactly one, v0.1.0, because releases are tagged at
    # FILTERED commits absent from this history. `check-release-record` needs
    # them and is offline-by-design, so CI fetches them into its EPHEMERAL
    # checkout. Provisioning of a fact, not a check — and it has no local
    # counterpart because a developer checkout already has every tag, imported
    # by the publisher on promotion. That asymmetry is exactly why this was
    # green on every laptop and red only on CI.
    *"git fetch --tags"* | *"tags now visible:"*)
      echo "provisioning (the published release tags a source clone never receives), not a check" ;;

    # Group 2 close (dogfood, the rest) — a2a-validate-reusable.yml commands
    # that are real on the SPACE-PATH (a2a-ref != "") or v3-pr-mode branches,
    # neither of which a2a-validate-dogfood.yml's call
    # (a2a-ref: "", mode: v3-full-repo) ever reaches. The dogfood EXECUTES
    # step above reproduces the branch that DOES run; these are dead code on
    # that path, not covered by faking their execution.
    "break" \
    | *"cosign verify-blob"* \
    | *'if [ -n "${A2A_AUTHOR:-}" ]; then'* \
    | *'if [ -z "$A2A_BASE" ]; then'* \
    | *'if [ "$downloaded" -ne 1 ]; then'* \
    | *'install -m 0755 "$RUNNER_TEMP/$asset" "$bin/a2a"'* \
    | *'sleep $((attempt * 5))'* \
    | *'if curl -fsSL -o "$RUNNER_TEMP/$asset"'*)
      echo "only reachable when a2a-ref is set (the space release-asset path) or mode=v3-pr; a2a-validate-dogfood.yml always calls with a2a-ref=\"\" and mode=v3-full-repo, so this branch is dead code on the path the dogfood EXECUTES step reproduces" ;;
    *'jq -r '*'while IFS=$'"'"'\t'"'"' read -r code path severity artifact_id message; do'*)
      echo "GitHub-annotation projection of the report for the PR UI; the report's own violations and the validator's exit status — what this check actually tests — are already checked by the dogfood EXECUTES step above, and there is no GitHub annotation API to write to locally" ;;

    # classify-guard.yml's PUBLIC-only private-path backstop. The lead's first
    # draft of this excuse said `make classify-guard` covers it; the implementing
    # agent refused to write that, and was right on two independent grounds —
    # recorded here because the wrong version is the plausible one.
    #
    # (1) The step is gated `if: github.repository == 'ydnikolaev/a2ahub'`. This
    #     checkout is the PRIVATE source; the step never runs here at all, and
    #     `workflow-lint` actively asserts that gating exists.
    # (2) scripts/classify-guard.sh reaches the same eight patterns but calls
    #     `note` — non-fatal — because in the private dev repo those paths are
    #     SUPPOSED to be tracked. The backstop calls `exit 1` because in the
    #     published projection they must be ABSENT. Opposite repositories,
    #     opposite required outcomes: the local gate checks the CONVERSE
    #     invariant, not this one.
    *"git ls-files | grep -E "*|*'if [ -n "$priv" ]'*)
      echo "runs only against the published projection (if: github.repository == 'ydnikolaev/a2ahub'), which this checkout is not; scripts/classify-guard.sh TOLERATES these same paths here by design, so it checks the converse invariant rather than this one" ;;

    *) return 1 ;;
  esac
}

# EXECUTED — the local equivalent of each CI command, in CI's own order.
#
# Every step is TIMED and the elapsed seconds are printed on its own result
# line. Not decoration: AC2 asks for a wall-clock comparison between the
# composed gate and a full parity run, and job-timeouts.tsv's measured-median
# discipline says report what a phase cost rather than what it was expected to.
# A step whose cost nobody records is a step nobody can argue about removing —
# which is how three ceiling runs accumulated in one runbook.
run_step() { # $1 = label, $2... = command
  local label="$1"; shift
  local start end rc=0
  start="$(date +%s)"
  printf '\n=== ci-parity: %s ===\n' "$label"
  "$@" || rc=$?
  end="$(date +%s)"
  if [ "$rc" -eq 0 ]; then
    printf 'ci-parity: %s OK (%ss)\n' "$label" "$((end - start))"
    return 0
  fi
  printf 'ci-parity: %s FAILED (%ss)\n' "$label" "$((end - start))" >&2
  return "$rc"
}

# extract_commands reads every `run:` value out of the given workflow YAML
# files — a plain single-line `run: cmd` AND a literal block's (`run: |`)
# BODY, line by line — and prints one command per output line, unsorted and
# unfiltered by excuse. It is the SECOND fix to this class of bug in this
# function. The first is the comment two paragraphs below this one: getting
# the EXECUTES markers' leading '#' wrong once made every command look
# uncovered. This one is the mirror image — `grep -h 'run: ' | sed 's/.*run: //'`
# left a bare `|` for a literal block, the next filter dropped the pipe, and
# the block's BODY never contained the substring `run: ` at all, so it was
# never read. Measured 2026-08-21: 27 literal blocks — `release.yml` alone had
# 8, and the audit had never read one line of the workflow that cuts the
# release. Same class of bug both times: the extractor's own shape decided
# what was "coverage", not the workflow's.
#
# A folded block (`run: >`) is REFUSED LOUDLY (distinct stderr line, distinct
# exit code) rather than silently producing zero commands — a shape this
# reader does not understand must never look like "nothing to report" (T4).
# None exist in this repo today; the refusal exists so the day one is added,
# the audit says so instead of going quietly blind a third time.
#
# A line ending in a single trailing backslash inside a block is joined to the
# next content line (shell-style continuation) so a wrapped invocation is one
# command, not an arg fragment nobody could ever excuse. Comment lines
# (leading '#') and blank lines inside a block are ignored, per its own body
# — they are not commands (T5).
#
# THE UNIT IS A COMMAND, NOT A PHYSICAL LINE (P1 refinement, 2026-08-21). The
# first cut of this widening emitted every physical block line as a candidate
# "command" — `;;`, `*)`, `}`, jq-filter continuation lines, pure variable
# assignments — none of which are commands, and the audit drowned the real
# findings (macos-notifier scripts, an excused `sha256sum --check --strict`)
# in 170 lines of shell syntax. Two mechanisms now sit between a raw block
# line and an emitted candidate:
#
#   1. CONTINUATION MERGING. A command can span several physical lines three
#      ways: a trailing backslash (already handled), a trailing pipeline
#      operator (`|`, `&&`) with the rest of the pipeline on the next line, or
#      an UNCLOSED QUOTE — a `run: |` block can carry a multi-line single- or
#      double-quoted argument (a jq filter, a GraphQL query body) whose
#      interior lines contain no shell syntax of their own at all. quote_scan
#      tracks quote state (', ") across lines the same way a shell reader
#      would — respecting backslash-escaping inside "..." and NOT inside
#      '...' — so all three continuation forms collapse into ONE buffered
#      command before it is ever classified. This is what turns
#      a2a-validate-reusable.yml's 9-line `jq -r '...' "$report" | while ...`
#      pipeline (and feedback-intake.yml's multi-line `gh api graphql -f
#      query='...'`) from 9 bogus "commands" into the one real one CI runs.
#   2. CLASSIFICATION. classify_and_emit() then decides whether the merged
#      line BEGINS a command at all, dropping: pure control-flow keywords and
#      delimiters (fi/done/esac/else/then/do/{/}/(/)), a case label alone
#      (`<pattern>)` with nothing after it), a line that opens with a stray
#      operator or delimiter (a defence-in-depth net for anything the merge
#      step did not already fold in), a bare `case EXPR in` / `for VAR in
#      LIST` header with no embedded command substitution, a bare function-
#      definition header (`name() {`), a `}` that closes a group and carries
#      only a redirect, and a pure variable-assignment sequence with no
#      invocation anywhere in its value. `if`/`elif`/`while`/`until` headers
#      are KEPT whole — the header's own condition is a command (a `[ ... ]`
#      test, a `git rev-parse`, a `read`) — and so is any assignment whose
#      value contains `$(...)` or a backtick (`VERSION="${VERSION#v}"
#      some/script.sh`, `count=$(grep ... )`, `rendered="$(mktemp)"`): the
#      assignment does not change the fact that a program runs.
#
# Bias: KEEP over DROP whenever a line does not cleanly match one of the
# enumerated drop shapes above (e.g. `SHA256SUMS|*.cosign.bundle) continue
# ;;` — a case label with a real `continue` after it — is kept whole rather
# than surgically split) — a filter that quietly drops a real command is
# strictly worse than the noise it replaces.
extract_commands() { # $@ = workflow yaml file paths
  local f rc
  for f in "$@"; do
    rc=0
    awk '
      function trim(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }

      # quote_scan advances quote state `q` ("" | SQ | DQ) across string s,
      # the same finite-state walk a shell reader does: inside "..." a
      # backslash escapes the next char; inside '"'"'...'"'"' nothing is
      # special, not even backslash; outside quotes a backslash escapes the
      # next char and either quote character opens its own state.
      function quote_scan(s, q,    i, n, c) {
        n = length(s)
        for (i = 1; i <= n; i++) {
          c = substr(s, i, 1)
          if (q == "") {
            if (c == BS) { i++; continue }
            if (c == SQ || c == DQ) q = c
          } else if (q == SQ) {
            if (c == SQ) q = ""
          } else {
            if (c == BS) { i++; continue }
            if (c == DQ) q = ""
          }
        }
        return q
      }

      # is_pure_assign_seg: one semicolon-delimited segment, already trimmed.
      # An assignment whose value contains a command substitution (`$(...)`
      # or a backtick) still runs a program — KEEP. Otherwise pure only when
      # the value is a single quoted/array/bare token with nothing after it.
      function is_pure_assign_seg(seg,    eqpos, val, first, last) {
        if (seg !~ /^[A-Za-z_][A-Za-z0-9_]*(\[[^]]*\])?\+?=/) return 0
        eqpos = index(seg, "=")
        val = substr(seg, eqpos + 1)
        if (index(val, "$(") > 0 || index(val, "`") > 0) return 0
        if (val == "") return 1
        first = substr(val, 1, 1)
        last = substr(val, length(val), 1)
        if (first == DQ && last == DQ) return 1
        if (first == SQ && last == SQ) return 1
        if (first == "(" && last == ")") return 1
        if (val !~ /[ \t]/) return 1
        return 0
      }

      function is_pure_assignment_line(s,    n, i, seg, parts) {
        n = split(s, parts, ";")
        for (i = 1; i <= n; i++) {
          seg = trim(parts[i])
          if (seg == "") continue
          if (!is_pure_assign_seg(seg)) return 0
        }
        return 1
      }

      # classify_and_emit: the UNIT is a command, not a physical line — see
      # the header above this function for the full reasoning.
      function classify_and_emit(s,    core, c1, c2) {
        core = s
        sub(/[ \t]*;;[ \t]*$/, "", core)
        core = trim(core)
        if (core == "") return
        if (core ~ /^(if|elif|while|until)[ \t]/) { emit(s); return }
        if (core in dropkw) return
        if (core ~ /^[^ \t]+\)$/) return
        # Defence-in-depth for a continuation fragment the merge step above
        # did not fold in — but ONLY the shapes no real command can ever
        # legitimately start with. A single or double quote is deliberately
        # NOT in this set: `"$bin/a2a" "${args[@]}" >"$report"`
        # (a2a-validate-reusable.yml) is a real, ordinary command that quotes
        # its own executable path — dropping it once because the quoted-path
        # idiom shares a first character with a multi-line quoted jq-filter
        # continuation is exactly "a filter that quietly drops a real
        # command", so this list stays to pipe, close-paren, semicolon, and
        # double-ampersand — none of which any real command starts with,
        # unlike a quoted path or a subshell group.
        c1 = substr(core, 1, 1)
        c2 = substr(core, 1, 2)
        if (c1 == "|" || c1 == ")" || c1 == ";" || c2 == "&&") return
        if (core ~ /^case[ \t].*[ \t]in$/) return
        if (core ~ /^for[ \t]+[A-Za-z_][A-Za-z0-9_]*[ \t]+in([ \t]|$)/ && index(core, "$(") == 0 && index(core, "`") == 0) return
        if (core ~ /^[A-Za-z_][A-Za-z0-9_]*\(\)[ \t]*\{([ \t]*#.*)?$/) return
        if (core ~ /^\}[ \t]*(>>|>|<)[^ \t]+$/) return
        if (is_pure_assignment_line(core)) return
        emit(s)
      }

      function emit(s,    t) { t = trim(s); if (t != "") print t }

      BEGIN {
        inblock = 0; runindent = -1; buf = ""; qchar = ""
        BS = sprintf("%c", 92); SQ = sprintf("%c", 39); DQ = sprintf("%c", 34)
        dropkw["fi"]=1; dropkw["done"]=1; dropkw["esac"]=1; dropkw["else"]=1
        dropkw["then"]=1; dropkw["do"]=1; dropkw["{"]=1; dropkw["}"]=1
        dropkw["("]=1; dropkw[")"]=1
      }
      {
        line = $0
        lead = line
        sub(/^[ \t]*/, "", lead)
        indent = length(line) - length(lead)
        content = lead
        sub(/[ \t]+$/, "", content)

        if (inblock) {
          if (content == "") next
          if (indent > runindent) {
            if (buf == "" && qchar == "" && content ~ /^#/) next

            # The quote a line ENDS in decides everything below — including
            # whether a trailing backslash on THIS line is a real
            # continuation marker or still just quoted data. A quote can
            # close and a fresh backslash-continuation start on the SAME
            # line (feedback-intake.yml: a closing brace-quote-backslash
            # closes the graphql query and continues onto the next -f flag),
            # so qnew must be computed on the untouched line before any
            # backslash is stripped.
            raw = content
            qnew = quote_scan(raw, qchar)

            hasBS = 0
            frag = raw
            if (qnew == "" && raw ~ /\\$/) {
              hasBS = 1
              sub(/\\$/, "", frag)
              frag = trim(frag)
            }
            buf = (buf == "" ? frag : buf " " frag)

            if (qnew != "") { qchar = qnew; next }
            if (hasBS) { qchar = ""; next }
            if (raw ~ /\|$/ || raw ~ /&&$/) { qchar = ""; next }

            qchar = ""
            classify_and_emit(buf)
            buf = ""
            next
          }
          inblock = 0
          qchar = ""
          if (buf != "") { classify_and_emit(buf); buf = "" }
          # fall through: this line is not part of the block — re-examine it
          # as an ordinary top-level line below.
        }

        if (match(content, /^(- )?run:[ \t]*/)) {
          val = substr(content, RLENGTH + 1)
          val = trim(val)
          if (val == "|") { inblock = 1; runindent = indent; buf = ""; qchar = ""; next }
          if (val ~ /^[|>]/) {
            print FILENAME ":" FNR ": " content > "/dev/stderr"
            exit 3
          }
          if (val != "") classify_and_emit(val)
          next
        }
      }
      END { if (buf != "") classify_and_emit(buf) }
    ' "$f" || rc=$?
    if [ "$rc" -ne 0 ]; then
      if [ "$rc" -eq 3 ]; then
        printf 'ci-parity: UNSUPPORTED — folded run block (run: >) above; extraction refuses rather than silently skipping it\n' >&2
      fi
      return "$rc"
    fi
  done
}

# is_workflow_call: true (exit 0) iff $1's TOP-LEVEL `on:` block declares
# `workflow_call` as a trigger key — the mark of a REUSABLE workflow (one
# invoked only via a caller's `uses:`, never by a repo event of its own).
# Scoped to the `on:` block specifically (stops at the next top-level key) so
# a file that merely MENTIONS "workflow_call" in a comment or a `description:`
# string does not false-positive.
is_workflow_call() { # $1 = workflow yaml file path
  awk '
    /^on:[ \t]*$/ { inon=1; next }
    inon && /^[^ \t#]/ { inon=0 }
    inon && /^[ \t]+workflow_call:?[ \t]*$/ { found=1 }
    END { exit !found }
  ' "$1"
}

# is_called_locally: true iff some OTHER workflow file in the same directory
# invokes $1 (matched by basename) from a `uses:` line — the shape a LOCAL
# `uses: ./.github/workflows/<file>` caller takes. This is deliberately NOT
# folded into is_workflow_call: a workflow can declare `workflow_call` and
# still be exercised by this repo's own CI when a sibling calls it locally
# (a2a-validate-dogfood.yml calls a2a-validate-reusable.yml on every PR) — a
# filter keyed on the trigger alone would drop 21 commands CI genuinely runs,
# which is exactly the "gate reports less than it knows" failure this epic
# exists to fix. Only a reusable workflow with NO local caller is a shipped
# product this repo never runs — see the OUT OF SCOPE announcement below.
is_called_locally() { # $1 = workflow yaml file path, $2... = candidate callers
  local target base other
  target="$1"; shift
  base="$(basename "$target")"
  for other in "$@"; do
    [ "$other" = "$target" ] && continue
    if grep -E '^[ \t]*uses:' "$other" 2>/dev/null | grep -qF "$base"; then
      return 0
    fi
  done
  return 1
}

# out_of_scope_workflows: prints, one per line, the workflows under $1 that
# are reusable (workflow_call) AND uncalled by any sibling in the same
# directory — i.e. shipped for OTHER repositories' CI to invoke, never run
# by this one. Derived from the corpus itself every run; see is_workflow_call
# and is_called_locally above for why both conditions are required.
out_of_scope_workflows() { # $1 = workflow dir, $2... = all workflow files
  local dir="$1"; shift
  local f
  for f in "$@"; do
    if is_workflow_call "$f" && ! is_called_locally "$f" "$@"; then
      basename "$f"
    fi
  done
}

# executes_haystack is the coverage claim: the EXECUTES block of this file,
# WITHOUT its comment lines.
#
# Stripping them is a correctness fix, not tidiness. The block is 220 lines of
# which 138 are prose explaining what each step covers and why, and the
# coverage test is a substring grep — so a command merely NAMED in a comment
# registered as "run by the composed gate". A claim that this gate is the SSOT
# for what CI runs, satisfied by a sentence ABOUT running something, is the
# same defect as an over-broad excuse glob in the opposite direction: there a
# command wore another step's reason, here a command wore a description of
# itself.
executes_haystack() {
  sed -n '/^# EXECUTES_BEGIN/,/^# EXECUTES_END/p' "$ROOT/scripts/ci-parity.sh" \
    | grep -v '^[[:space:]]*#'
}

audit() {
  local dir="${1:-.github/workflows}"
  local cmds executes missing=0
  local -a files=()
  files=("$dir"/*.yml)
  if [ ! -e "${files[0]}" ]; then
    printf 'ci-parity: EMPTY CORPUS — no workflow files matched %s/*.yml; refusing rather than reporting a clean run\n' "$dir" >&2
    return 1
  fi

  # OUT-OF-SCOPE ANNOUNCEMENT — printed on every run, including the zero
  # case, so a tooth can assert the count rather than merely a line's
  # presence (P1 final). A workflow excluded here is a decision made by the
  # corpus itself (workflow_call + no local caller), never a hand-kept list.
  local -a excluded=() inscope=()
  local x f base is_excl
  while IFS= read -r x; do
    [ -n "$x" ] && excluded+=("$x")
  done < <(out_of_scope_workflows "$dir" "${files[@]}")
  for f in "${files[@]}"; do
    base="$(basename "$f")"
    is_excl=0
    for x in "${excluded[@]+"${excluded[@]}"}"; do
      [ "$base" = "$x" ] && is_excl=1 && break
    done
    if [ "$is_excl" -eq 0 ]; then
      inscope+=("$f")
    fi
  done
  if [ "${#excluded[@]}" -gt 0 ]; then
    local excl_paths=() excl_cmd_count
    for x in "${excluded[@]}"; do excl_paths+=("$dir/$x"); done
    if ! excl_cmd_count="$(extract_commands "${excl_paths[@]}" | sort -u | wc -l | tr -d ' ')"; then
      return 1
    fi
    printf 'ci-parity: OUT OF SCOPE — %s command(s) in %d reusable workflow(s) (%s). This repository never runs them: they are shipped and executed by OTHER repositories'"'"' CI (space workflows call them by release-tag-pinned path). Their structural coverage: scripts/check-notify-workflow.sh, scripts/check-notify-secrets.sh — neither executes their run: commands'"'"' behaviour.\n' \
      "$excl_cmd_count" "${#excluded[@]}" "$(IFS=,; echo "${excluded[*]}")"
  else
    printf 'ci-parity: OUT OF SCOPE — 0 command(s) in 0 reusable workflow(s) (none). Every reusable workflow under %s is called locally by a sibling.\n' "$dir"
  fi

  # The coverage claim is the EXECUTES block itself, read from this file — not
  # a second list. The markers carry a leading '#', and getting that wrong once
  # made every command look uncovered, which is the same class of bug this
  # script exists to catch. (extract_commands' own header records the SECOND
  # instance of that class, in the extraction half instead of the markers.)
  # COMMENT LINES ARE STRIPPED, and that is a correctness fix rather than
  # tidiness. The block is 220 lines of which about half are prose explaining
  # what each step covers and why — so a command merely NAMED in a comment
  # registered as "run by the composed gate". A claim that this gate is the
  # SSOT for what CI runs, satisfied by a sentence about running it, is the
  # same defect as the excuse globs narrowed above, in the opposite direction:
  # there a command wore another step's reason, here a command wears a
  # description of itself.
  executes="$(executes_haystack)"
  if ! cmds="$(extract_commands "${inscope[@]}" | sort -u)"; then
    return 1
  fi
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

# audit_composed — the workflow half plus AC3's half.
#
# AC3's half is reached by `--audit`, so `ci-parity-audit` runs it: that target
# is a REPO_GATES member, which means `make check`, the derived lane AND the
# projection each execute it. A gate nothing runs is this epic's own subject —
# `make harness-check` was reached by no local flow for a week and a tooth
# stopped biting. `--audit-runbook` exists beside it for iterating on this half
# alone, never as its only executor.
#
# IT IS DELIBERATELY NOT PART OF `--suite`, i.e. it does NOT run inside the
# container. Two reasons, and the second is the load-bearing one. It reads a
# private markdown file, which is not a userland-sensitive read — the container
# buys nothing by re-parsing it, unlike the workflow extractor, whose awk
# genuinely behaves differently under mawk. And putting an awk program that has
# only ever been exercised under BSD awk at the FRONT of a twenty-minute
# container suite would mean a mawk difference reds the release gate at minute
# zero with "no step named a make target" — a message that cannot tell the
# operator whether the runbook or the parser is at fault. Three executors on the
# host are enough; a fourth in the one userland the author could not test is a
# liability, not coverage.
audit_composed() {
  audit "${1:-}" || return $?
  # A PRIVATE path: a public checkout has no docs/runbooks/release.md (docs/ is
  # stripped at publish), and audit_runbook reports gate_unmeasured on a missing
  # file rather than a clean pass — right for a gate, wrong for a public
  # checkout that legitimately does not carry the input. So the presence check
  # comes first, the same shape `make projection` and `_harness-check` already
  # use for stripped inputs.
  if [ -f "$RELEASE_RUNBOOK" ]; then
    audit_runbook "$RELEASE_RUNBOOK" || return $?
  else
    printf 'ci-parity: runbook audit skipped — %s absent (public checkout; the release runbook is private and stripped at publish).\n' "$RELEASE_RUNBOOK"
  fi
}

# EXECUTES_BEGIN
# Each run_step's FIRST argument is the CI command VERBATIM — that is what the
# audit greps for, so a label and its coverage claim cannot drift apart. The
# remaining arguments are how this repo invokes the same thing locally, which
# sometimes differs (npm needs --prefix web; the strict lane needs an explicit
# file set because nothing changed against HEAD locally).

# a2a_validate_dogfood_step reproduces a2a-validate-reusable.yml's `validate`
# job on the EXACT path a2a-validate-dogfood.yml exercises on every PR
# (uses: ./.github/workflows/a2a-validate-reusable.yml, with: mode:
# v3-full-repo, space-path: space-template, a2a-ref: "" — build ./cmd/a2a from
# this checkout rather than downloading a release). The body below is that
# job's own `a2a validate --ci` run: block, kept to the lines that actually
# execute on THIS path — the v3-pr-only arg wiring (base/author), the
# release-asset download+cosign-verify branch, and the GitHub-annotation
# projection are dead code when a2a-ref="" and mode=v3-full-repo, so they are
# NOT reproduced here (that would be inventing a step that does not really run
# what CI runs) — excused_reason() excuses each of them individually, naming
# why the dogfood path never reaches it.
#
# Body is `(...)`, a SUBSHELL, not `{...}` — run_step invokes this in ci-
# parity.sh's OWN shell (`if "$@"; then`), so a bare `{...}` function whose
# body ends in `exit "$status"` (the reusable workflow's own last line, kept
# verbatim for the audit's substring match) would exit ci-parity.sh itself
# instead of just this step — silently skipping every EXECUTES entry after
# it on `--run`, and leaking this step's `cd`/`set +e`/`set -e` into the
# caller's shell either way. The subshell contains all three.
a2a_validate_dogfood_step() (
  set -euo pipefail
  A2A_MODE="v3-full-repo"
  SPACE_PATH="$ROOT/space-template"
  cd "$SPACE_PATH"
  report="$(mktemp)"
  args=(validate --ci --mode="$A2A_MODE")
  if [ "$A2A_MODE" = "v3-pr" ]; then
    :
  fi
  bin="$(mktemp -d)"
  mkdir -p "$bin"
  A2A_REF=""
  if [ -n "$A2A_REF" ]; then
    :
  else
    GOBIN="$bin" go install "$ROOT/cmd/a2a"
  fi
  set +e
  "$bin/a2a" "${args[@]}" >"$report"
  status=$?
  set -e
  cat "$report"
  if jq -e . "$report" >/dev/null 2>&1; then
    :
  fi
  exit "$status"
)

# suite_members — THE USERLAND-PORTABLE SUITE. Everything CI runs that is not
# tied to a macOS toolchain, in CI's own order. This is what the CONTAINER
# executes, and it is the composed release gate's primary phase.
#
# THREE CHANGES LANDED HERE ON 2026-08-21 (release-loop-2026-08 P4). Each
# removes a duplicate or closes a hole; none of them narrows what is judged.
#
#  1. THE STRICT LANE TARGET LEFT. Its coverage argument is written out in full
#     in excused_reason() above — every phase it can derive is run by the
#     members around it, so it was re-paying an entire ceiling, once per
#     userland. Two members below are what keep that true rather than
#     convenient. (Its name is deliberately NOT spelled in full anywhere between
#     EXECUTES_BEGIN and EXECUTES_END: this block IS the coverage claim, greps
#     are substring greps, and a command named in a comment about why it is
#     absent would silently claim the very coverage the excuse is there to
#     argue. That is the same class as the marker bug this file's extractor
#     header records, one level up.)
#  2. `make lane` and `make projection` JOINED, because they are what the lane
#     was silently contributing. The spec's own amendment says dropping
#     lane-run-strict "loses only the derivation refusal — and a11y/lighthouse";
#     that enumeration is INCOMPLETE, and the missing member is the expensive
#     one. `make lane` on a single Go file prints `projection matched **`, so
#     every Go-touching release was running check-projection.sh's ~10-20 minute
#     public-projection ceiling through the lane — the gate that exists to catch
#     the `_harness-check` Error 127 class the runbook still lists as open. A
#     member set without it would have dropped that gate silently, which is the
#     precise failure mode this whole epic is about.
#  3. `npm run check` became `npm run check:quality`, its own superset
#     (`check && check:a11y && check:lighthouse`). Accessibility and Lighthouse
#     were in the ship gate BY ACCIDENT until today: no workflow runs
#     check:quality, and its only path was `make web-quality`, which the lane
#     selects only when the diff happens to touch web/**. Gate membership
#     depended on what was in HEAD~1. The coverage claim for CI's own
#     `npm run check` is this sentence plus the label below — the audit greps
#     this whole block, comments included, and `check:quality` runs
#     `npm run check` as its literal first step.
#
#     It is `npm --prefix web run check:quality` rather than `make web-quality`
#     on purpose: that target is presence-gated on web/node_modules and prints
#     "skip" when it is absent. A silent skip is the correct behaviour for a
#     COMMIT lane on a laptop that never installed Node, and the wrong one for
#     a ship gate — the one place a skipped suite must be a refusal.
suite_members() {
  run_step "bash scripts/ci-changes.sh"                bash scripts/ci-changes.sh
  run_step "bash scripts/ci-skill-drift.sh"            bash scripts/ci-skill-drift.sh
  run_step "bash scripts/dashboard-template-drift.sh"  bash scripts/dashboard-template-drift.sh
  run_step "make check"                                make check
  run_step "make harness-check"                        make harness-check
  # A root, shallow or merge HEAD makes `git diff HEAD~1` empty, the derivation
  # then judges NOTHING, and the step greens. verify.sh already implements the
  # refusal (A2A_VERIFY_REQUIRE_NONEMPTY); the ship gate simply never asked for
  # it. The 2>/dev/null went with it — a swallowed git error is how the empty
  # set arrives unannounced.
  run_step "make lane"                                 env A2A_VERIFY_REQUIRE_NONEMPTY=true LANE_FILES="${LANE_FILES:-$(git diff --name-only HEAD~1 | tr '\n' ' ')}" make lane
  run_step "make projection"                           make projection
  run_step "make vulncheck"                            make vulncheck
  # THE ONLY MEMBER HERE THAT CI DOES NOT RUN, and it is deliberate.
  #
  # Every other member mirrors a CI command; this one exists because CI itself
  # cannot answer the question. CI runs the suite ONCE, so a test failing one
  # run in three passes it two times in three — which is exactly how two
  # timing-sensitive tests reached the v0.25.0 tag on 2026-08-22 and were then
  # found by accident, on CI, after the tag. `ci-parity` and `ci-parity-docker`
  # could not have caught either: they prove the environment matches, correctly,
  # and no amount of environment fidelity answers "does this test always pass?".
  #
  # It costs roughly the Go tier twice over. A release is worth it; a commit is
  # not, which is why `flaky-scan` is NEVER in the derived lane.
  run_step "make flaky-scan"                           make flaky-scan
  run_step "npm run check:unit"                        npm --prefix web run check:unit
  run_step "npm run check"                             npm --prefix web run check
  run_step "npx playwright test tests/dashboard-visual-contract.spec.mjs" \
    sh -c 'cd web && npx playwright test tests/dashboard-visual-contract.spec.mjs'
  run_step "gitleaks dir"                              gitleaks dir --config .gitleaks.toml --redact --verbose .
  run_step "gitleaks git"                              gitleaks git --config .gitleaks.toml --redact --verbose .
  run_step "a2a-validate-reusable.yml validate --ci (a2ahub dogfood: a2a-ref=\"\", mode=v3-full-repo)" \
    a2a_validate_dogfood_step
}

# macos_delta — THE ONLY THING A DARWIN HOST CAN PROVE THAT THE CONTAINER
# CANNOT, and until 2026-08-21 the only part of CI that NOTHING ran.
#
# The job set is DERIVED, never listed: macos_jobs() below greps `runs-on:` and
# reports every job that asks for a macOS runner. Today that resolves to exactly
# two — `macos-notifier` in ci.yml and in release.yml — and if a third appears,
# the derivation says so instead of a hand-kept list going quietly stale. That
# is this epic's whole subject, applied to itself.
#
# The commands below are ci.yml's macos-15 job verbatim, with its own `env:`
# block reproduced: VERSION=0.0.0 and SIGNING_MODE=adhoc, so nothing here needs
# a signing identity, and OUTPUT_DIR pointed at a temp dir so the run leaves
# nothing in the tree it is judging.
#
# ABSENT TOOLCHAIN IS gate_unmeasured, NOT A SKIP AND NOT A FAIL. "There is no
# Swift here" is a fact about the measurement; "the notifier does not build" is
# a fact about the product. A composed ship gate that let the first wear the
# second's clothes — in either direction — would be worse than three separate
# targets, because it would look complete.
macos_delta() {
  if [ "$(uname -s)" != "Darwin" ]; then
    gate_unmeasured "the macos-15 delta needs a darwin host; this is $(uname -s). The container half of the release gate CANNOT stand in for it — ci.yml's and release.yml's macos-15 jobs build and package a Swift companion, and no Linux image runs them. Run \`bash scripts/ci-parity.sh --macos-delta\` on a macOS machine before shipping."
    return "$GATE_EXIT_UNMEASURED"
  fi
  if ! command -v swift >/dev/null 2>&1; then
    gate_unmeasured "the macos-15 delta needs a Swift toolchain and \`swift\` is not on PATH (install Xcode or the Command Line Tools). Nothing was measured about integrations/macos-notifier; this is NOT a claim that it builds."
    return "$GATE_EXIT_UNMEASURED"
  fi

  local outdir rc=0
  outdir="$(mktemp -d "${TMPDIR:-/tmp}/a2a-notifier.XXXXXX")"

  # A SUBSHELL, so the three exports below do not outlive this phase. It is the
  # last phase of `execute_all` today, which is exactly the argument that makes
  # a leak invisible until somebody adds a fourth — and release_gate ALREADY
  # runs `git rev-parse`, the telemetry read and the receipt write after it,
  # with VERSION=0.0.0 exported into all of them. Same reasoning, same fix, as
  # a2a_validate_dogfood_step's own subshell body above.
  (
    # ci.yml's macos-15 job sets these three in the step's own `env:` block. The
    # values are that job's, not invented here: a different VERSION or a real
    # SIGNING_MODE would be testing something CI does not run.
    export VERSION=0.0.0 SIGNING_MODE=adhoc OUTPUT_DIR="$outdir"
    run_step "swift --version"                                        swift --version
    run_step "integrations/macos-notifier/scripts/test.sh"            bash integrations/macos-notifier/scripts/test.sh
    run_step "integrations/macos-notifier/scripts/package-release.sh" bash integrations/macos-notifier/scripts/package-release.sh
  ) || rc=$?
  rm -rf "$outdir"
  return "$rc"
}

# host_only — WHAT ONLY A DEVELOPER HOST CAN MEASURE.
#
# The partition is NOT "userland sensitivity", which is undecidable and would
# have needed a hand-kept column. It is MEASURABILITY: run each thing where it
# can actually be measured, and say so where it cannot.
#
# `npm run check:quality` (a11y + Lighthouse) was a container member until
# 2026-08-21, when the composed gate ran it there for the first time. Every
# route came back `performance 0 [0/0/0] (min 80)` with every metric `n/a` —
# which is what an unloaded page looks like, not a bad one. The same command on
# this host: performance 96-99, accessibility 100, best-practices 100, SEO 100,
# on every route. Lighthouse needs a browser stack the image does not carry, and
# a threshold compared against an unmeasured zero is a verdict nobody earned.
#
# The container keeps `npm run check` — unit tests, the build, and the dist
# assertions — which it measures perfectly well.
host_only() {
  # CI=true, and the container already sets it (ci-parity.Dockerfile ENV CI=true).
  # The HOST did not, and playwright.config keys three things on it:
  #   reuseExistingServer: !CI  — a STALE preview server on :4324 is reused, so
  #                               the visual contract judges an old web/dist
  #   forbidOnly:          CI   — a stray `test.only` greens the whole suite
  #   retries:             CI?1:0
  # This is the half of the ship gate the container cannot cover, so nothing
  # else was ever going to catch it.
  run_step "npm run check:quality"                     env CI=true npm --prefix web run check:quality
  macos_delta
}

execute_all() {
  suite_members
  host_only
}
# EXECUTES_END

# --- the macOS partition, DERIVED ---------------------------------------
#
# macos_jobs prints one `<file>:<job>:<runs-on>` line per job whose `runs-on:`
# asks for a macOS runner. The partition between "what the container can judge"
# and "what only a darwin host can" is the ONE axis this repo can derive from
# the workflows themselves, and the spec's original proposal — partition by
# userland sensitivity — is not: that would be a hand-maintained column on the
# EXECUTES list, the exact anti-pattern §5's own checkbox forbids.
#
# Matched on the PREFIX `macos`, not the literal `macos-15`, so a bump to
# macos-16 does not silently empty this set. A `runs-on:` naming a matrix
# expression (`${{ matrix.os }}`) resolves to no runner here and is reported as
# UNRESOLVED rather than counted as ubuntu — a shape this reader does not
# understand must never look like "nothing to report", the same rule the folded
# `run: >` block follows.
macos_jobs() { # $1 = workflow dir
  local dir="${1:-.github/workflows}" f
  for f in "$dir"/*.yml; do
    [ -e "$f" ] || continue
    awk -v file="$f" '
      function trim(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }
      /^jobs:[ \t]*$/ { injobs = 1; next }
      injobs && /^[^ \t#]/ { injobs = 0 }
      injobs && /^  [A-Za-z_][A-Za-z0-9_-]*:[ \t]*$/ {
        job = trim($0); sub(/:$/, "", job); next
      }
      injobs && job != "" && /^[ \t]+runs-on:[ \t]*/ {
        line = $0
        sub(/^[ \t]+runs-on:[ \t]*/, "", line)
        line = trim(line)
        if (line ~ /\$\{\{/) {
          print file ":" job ":UNRESOLVED " line
        } else if (tolower(line) ~ /^\[?"?'"'"'?macos/) {
          print file ":" job ":" line
        }
      }
    ' "$f"
  done
}

# --- phase accounting: COUNT PHASES, NOT BANNERS ------------------------
#
# AC1 as drafted counts `make check` banners in a transcript. That measures the
# wrong quantity and would have reported the old composition clean: the strict
# lane re-ran the ceiling's phases WITHOUT printing a banner, which is exactly
# how a whole duplicate ceiling hid inside a suite for weeks.
#
# scripts/verify.sh already appends one JSONL line per phase — gate, verdict,
# duration, mode — to .a2a/cache/verify/telemetry.jsonl. Counting the lines a
# run appends therefore answers "how many phases did this actually execute",
# per mode, and gives AC2's wall clock for free. Two callers bracket a run with
# telemetry_offset / telemetry_report.
#
# lane-reads-opaque: telemetry_report reads "$VERIFY_TELEMETRY", a path built
#   from ROOT and pointing inside .a2a/cache/, which is this repo's gitignored
#   local runtime state and never a repository input. It is scripts/verify.sh's
#   own output file; nothing in the tracked corpus can flip a verdict through
#   it.
# Overridable ONLY so --teeth can point the reader at a constructed fixture: a
# duplicate-phase refusal that can never be exercised is a refusal nobody has
# seen fail, and this one guards the composition's whole reason to exist.
VERIFY_TELEMETRY="${A2A_VERIFY_TELEMETRY:-$ROOT/.a2a/cache/verify/telemetry.jsonl}"

telemetry_offset() {
  if [ -f "$VERIFY_TELEMETRY" ]; then
    wc -l <"$VERIFY_TELEMETRY" | tr -d ' '
  else
    echo 0
  fi
}

# telemetry_report — print what the phases between $1 and now actually were,
# and REFUSE a repeat.
#
# THE INVARIANT, and it is an invariant rather than a count: within ONE composed
# run, no (gate, mode) pair may appear twice. "The ceiling ran exactly once" is
# a state that a future member could legitimately change; "no gate was paid for
# twice in the same mode" is what the composition is FOR, and it stays true
# however the member list grows. A repeat names both occurrences.
# $3 (optional) — a SECOND telemetry file and its own offset, as
# "<path>:<offset>". The container writes its phases into a bound-out file the
# host cannot otherwise see; a report that read only the host's would say zero
# after a suite that executed forty. Both are folded into one set, so the
# no-phase-ran-twice invariant holds ACROSS the boundary rather than inside
# each side of it.
telemetry_report() { # $1 = offset recorded before the run; $2 = a label
  local offset="$1" label="$2" new dupes total
  if [ ! -f "$VERIFY_TELEMETRY" ]; then
    gate_unmeasured "$label: scripts/verify.sh wrote no telemetry at $VERIFY_TELEMETRY, so the phase count is unknown. This is NOT a claim that fewer phases ran — it is a claim that nobody counted."
    return "$GATE_EXIT_UNMEASURED"
  fi
  new="$(tail -n "+$((offset + 1))" "$VERIFY_TELEMETRY")"
  if [ -n "${3:-}" ]; then
    local extra_path="${3%:*}" extra_off="${3##*:}"
    if [ -f "$extra_path" ]; then
      local extra
      extra="$(tail -n "+$((extra_off + 1))" "$extra_path" 2>/dev/null || true)"
      [ -n "$extra" ] && new="$(printf '%s\n%s' "$new" "$extra")"
    fi
  fi
  total="$(printf '%s' "$new" | grep -c '"gate"' || true)"
  if [ "$total" -eq 0 ]; then
    gate_unmeasured "$label: zero phases were recorded between the start of this run and now. A suite that executed no measurable phase must not report a phase count of 0 as if that were a clean result."
    return "$GATE_EXIT_UNMEASURED"
  fi

  # THE ONE COUNT, and it is deliberately not recomputed anywhere else.
  # write_receipt used to count again from "$VERIFY_TELEMETRY" alone — the host
  # file — and so wrote `verify_phases: 0` into the receipt after a run that had
  # executed 71, because every one of those 71 was written by the container into
  # a file the host cannot see. A receipt is the artifact the publisher trusts;
  # one that announces a number nothing measured is the defect this whole gate
  # exists to remove, wearing the gate's own clothes. Measured on the ship
  # gate's sixth run, 2026-08-21 — the first green one.
  COMPOSED_PHASE_COUNT="$total"

  printf '\nci-parity: %s executed %s verify phase(s):\n' "$label" "$total"
  printf '%s\n' "$new" \
    | sed -n 's/.*"gate":"\([^"]*\)".*"mode":"\([^"]*\)".*/  \2 \1/p' \
    | sort | uniq -c | sort -rn | sed 's/^/  /'

  # mode=test is EXCLUDED, and this is a limit of the RECORD rather than an
  # exemption of convenience. `verify.sh test ./pkg/...` is per-target by
  # construction — one invocation per package — but append_telemetry writes the
  # gate name without the target, so 24 scoped runs over 24 different packages
  # are indistinguishable from one gate paid 24 times. Measured on the ship
  # gate's fifth run, 2026-08-21: exactly that, all passing, all different
  # durations.
  #
  # The exclusion is derived from the MODE, never from a list of gate names, and
  # it disappears the day telemetry records the target: at that point the pairs
  # differ on their own and no rule is needed. Every other mode stays in — the
  # composition's whole reason to exist is that `make check` is not paid twice.
  dupes="$(printf '%s\n' "$new" \
    | grep -v '"mode":"test"' \
    | sed -n 's/.*"gate":"\([^"]*\)".*"mode":"\([^"]*\)".*/\2 \1/p' \
    | sort | uniq -d)"
  if [ -n "$dupes" ]; then
    gate_fail "$label: a phase ran TWICE inside one composed run — the composition is paying for the same evidence more than once, which is the defect this gate exists to remove. Wanted: every (mode, gate) pair exactly once. Got these repeats:"
    printf '%s\n' "$dupes" | sed 's/^/    /' >&2
    return 1
  fi
  printf 'ci-parity: %s — no phase ran twice.\n' "$label"
}

# --- AC3's teeth: the runbook may not name a command nothing runs -------
#
# AC3 says release.md's three ceiling steps collapse to one and that the
# convention names it. That is a DOCS criterion guarding against accretion —
# and accretion is exactly how steps 5, 5a and 5b arrived, each added by
# somebody who had read the document first. Prose cannot refuse. This can.
#
# Every `make <target>` and `bash scripts/<name>.sh` NAMED AS A STEP in the
# runbook's Phases 1-3 must either be something the composed gate executes, or
# carry a written excuse below. The unit is the STEP OPENING LINE (`5.`, `5a.`,
# `13.`), not the prose: the surrounding paragraphs legitimately mention
# `make lane`, `make logic-e2e` and half a dozen others while explaining WHEN
# to reach for them, and a rule that refused those would be refusing the
# document for being helpful.
#
# The `### Deferring the provider tier` subsection is deliberately outside the
# scan: its numbered list is a set of CONDITIONS, not release steps, and its
# numbering restarts at 1.
RELEASE_RUNBOOK="docs/runbooks/release.md"

# release_step_commands prints one command per line, as named in a step opener.
release_step_commands() { # $1 = runbook path
  awk '
    /^### / {
      inphase = ($0 ~ /^### Phase [123] /)
    }
    !inphase { next }
    /^[ \t]*[0-9]+[a-z]?\.[ \t]/ {
      line = $0
      while (match(line, /make [a-z][a-z0-9-]*/)) {
        print substr(line, RSTART, RLENGTH)
        line = substr(line, RSTART + RLENGTH)
      }
      line = $0
      while (match(line, /bash scripts\/[A-Za-z0-9_.-]+\.sh/)) {
        print substr(line, RSTART, RLENGTH)
        line = substr(line, RSTART + RLENGTH)
      }
    }
  ' "$1" | sort -u
}

# release_step_member — exit 0 and print WHY when the named command is one the
# composed release gate accounts for.
#
# MEMBERSHIP IS DERIVED FROM suite_members(), NOT RESTATED HERE, and that is
# what gives spec §6's tooth T2 ("a suite removed from the composition → RED")
# its teeth for free: delete `make projection` from the suite and the runbook
# step that names it stops being a member on the same edit, so the audit reds
# naming it. A hand-kept membership table would instead have gone on asserting
# coverage for a member that no longer ran — the identical failure to the
# EXECUTES markers this file already carries two scars from.
#
# Only the ENTRY POINTS are enumerated below, because they are compositions of
# members rather than members themselves. Adding a row here is a decision
# visible in a diff — the property steps 5, 5a and 5b never had.
release_step_member() { # $1 = command
  # A here-string, not a pipe into `grep -q`: under `set -o pipefail` an early
  # -q exit can leave the producer with SIGPIPE and turn a MATCH into a
  # nonzero pipeline status. It happens to work at fourteen members because
  # the whole list is written before grep exits — which is a race, not a rule,
  # and the failure it would eventually produce ("this member is not a member")
  # is indistinguishable from the real refusal.
  local members
  members="$(list_members)"
  if grep -qxF -- "$1" <<<"$members"; then
    echo "an EXECUTES member of the composed suite, run in the container phase"
    return 0
  fi
  case "$1" in
    "make release-check")
      echo "the composed gate itself" ;;
    "make ci-parity-docker")
      echo "the composed gate's container phase — the full suite, in the userland every non-notifier CI job runs" ;;
    "make ci-parity")
      echo "the same suite on the host, plus the macos-15 delta; every gate it runs is a composed-gate member" ;;
    *) return 1 ;;
  esac
}

# release_step_excused — exit 0 and print WHY when a step legitimately names a
# command the composed gate does NOT run. Keyed to the command's NATURE, never
# to a step number: the runbook's steps are about to be renumbered, and an
# excuse that pinned "step 6" would expire on the rewrite that this gate exists
# to permit.
release_step_excused() { # $1 = command
  case "$1" in
    "make release-preflight")
      echo "judges the RELEASE COORDINATES, not the tree — needs the network to assert the version is free on the release remote and that the template's pins resolve to published tags. A tree-judging composition cannot contain it and must not imply it ran" ;;
    "make live-e2e"|"make live-e2e-evidence")
      echo "the provider tier: real GitHub, real credentials, hours. Declared NEVER to the lane deriver for the same reason, and gated on a published candidate that does not exist when the composed gate runs" ;;
    "make release-postflight"|"make space-template-baseline")
      echo "runs AFTER the tag exists; there is no tag when the composed gate judges the tree" ;;
    *) return 1 ;;
  esac
}

audit_runbook() { # $1 = runbook path (default: the release runbook)
  local book="${1:-$RELEASE_RUNBOOK}" cmd why missing=0 seen=0
  if [ ! -f "$book" ]; then
    gate_unmeasured "the release runbook is not at $book, so no step could be read. This is NOT a clean runbook; it is an unread one."
    return "$GATE_EXIT_UNMEASURED"
  fi
  while IFS= read -r cmd; do
    [ -n "$cmd" ] || continue
    seen=$((seen + 1))
    if why="$(release_step_member "$cmd")"; then
      printf 'ci-parity: runbook step runs %s — %s\n' "$cmd" "$why"
      continue
    fi
    if why="$(release_step_excused "$cmd")"; then
      printf 'ci-parity: runbook step excused — %s (%s)\n' "$cmd" "$why"
      continue
    fi
    printf 'ci-parity: UNCOMPOSED — %s is named as a step in %s Phases 1-3, and the composed release gate runs nothing that covers it\n' "$cmd" "$book" >&2
    missing=$((missing + 1))
  done < <(release_step_commands "$book")

  if [ "$seen" -eq 0 ]; then
    gate_unmeasured "no step in $book Phases 1-3 named a \`make\` target or a \`bash scripts/*.sh\` command. Either the phase headings changed shape or the extraction is broken; a runbook with zero commands is not a runbook this gate has judged."
    return "$GATE_EXIT_UNMEASURED"
  fi
  if [ "$missing" -gt 0 ]; then
    printf '\nci-parity: %d release step(s) name a command the composed gate does not run.\n' "$missing" >&2
    printf 'Either make it a member of the composition, or add it to release_step_excused() with the reason it cannot be one.\n' >&2
    printf 'A release runbook that grows a fourth suite beside the composed one is the accretion this gate exists to refuse (spec 04 AC3).\n' >&2
    return 1
  fi
  printf '\nci-parity: every command named as a release step is run by the composed gate or excused with a reason (%d step command(s)).\n' "$seen"
}

# --- the composition ----------------------------------------------------
#
# CONTAINER-PRIMARY, and the inversion is the point. The spec's §T body proposes
# host-full / container-delta; §11's pre-implementation audit found that
# backwards on three independent grounds, all verified on the code:
#
#   * ci.yml's `check` job is `runs-on: ubuntu-latest`. A darwin host ceiling is
#     parity with NOTHING in CI except the two macOS jobs.
#   * The container SYNCS AND CLEANS, so it judges the TRACKED tree — what
#     actually publishes — where the host judges a working tree with generated,
#     gitignored files present. That difference has already shipped a live
#     defect (ci.yml's web job, first clean-runner run).
#   * release.md's own amendment ALREADY tells the operator to run the container
#     and treat the host runs as satisfied by it. The draft's savings table was
#     computed against a three-run baseline nobody is told to run any more.
#
# So: the container runs the full suite; the host runs only what the container
# cannot reach. Docker becomes a hard requirement for cutting a release. That is
# the correct trade, and it is stated rather than discovered — see the refusal
# text below, which names the container's own value instead of degrading to a
# host-only pass. A `--no-docker` flag is deliberately absent: a bare flag
# becomes a permanent silent downgrade, and the escape hatch this repo already
# uses for exactly this shape is a dated, written, COUNTED record that refuses
# at the third unremediated use (scripts/check-provider-tier-deferral.sh).
RECEIPT_DIR="$ROOT/.a2a/release-gate"
# Set by telemetry_report, read by write_receipt. Empty means nobody counted.
COMPOSED_PHASE_COUNT=""

# list_members — the composition's member list, READ OUT OF suite_members()
# rather than restated. A dry run that printed a second, hand-kept copy of the
# member list would be rehearsing a fiction, and this file already carries the
# scar of a coverage claim drifting from the thing it claimed (the EXECUTES
# markers, twice). Same mechanism, same reason: the source of truth is the code
# that runs.
list_members() {
  sed -n '/^suite_members() {/,/^}/p' "$ROOT/scripts/ci-parity.sh" | awk '
    # The label is the FIRST double-quoted argument, and it may contain
    # backslash-escaped quotes of its own (the dogfood step carries
    # a2a-ref=\"\"). A [^"]* match would truncate there and print a member
    # name that is not the one that runs — so the quote is walked, escapes
    # respected, exactly the way the workflow extractor above walks its own.
    /^  run_step "/ {
      s = substr($0, index($0, "\"") + 1)
      out = ""
      for (i = 1; i <= length(s); i++) {
        c = substr(s, i, 1)
        if (c == "\\") { out = out substr(s, i + 1, 1); i++; continue }
        if (c == "\"") break
        out = out c
      }
      print out
    }
  '
}


# ── PHASE 0 — THE FAST STATIC PASS ───────────────────────────────────────────
#
# WHY, with the bill attached. Two of 2026-08-22's ship-gate runs were spent
# discovering facts that cost nothing to check. One tree carried
# `released: unreleased` past runbook step 1 and was refused by
# release-preflight at step 6, AFTER the composed gate had run for 24 minutes;
# another regenerated the demo-JSON golden before stamping the release date
# rather than after, and the ten-byte drift was found by the container. Neither
# fact needs a container, a network or a minute. Both cost one.
#
# THE MEMBERSHIP RULE, and it is not "every cheap gate". This pass judges THE
# CUT COMMIT — the edits runbook Phase 1 steps 1-4 make immediately before the
# ship gate, which no commit lane has ever judged against the release's own
# semantics. Everything else in the tree was judged when it landed.
#
# Members come in two tiers, and the tiers carry DIFFERENT risk. They run
# CHEAPEST FIRST — Tier B, then Tier A — for a reason stated at the call site:
# Tier B is also the "is this a cut at all?" question, so it refuses alone in
# under a second, while Tier A's four independent findings are collected
# together rather than one twenty-second round trip at a time.
#
#   TIER A — already inside the container's own `make check`, moved earlier.
#     Zero new refusals. Skipping this pass cannot make such a member green; it
#     only means paying ~24 minutes for the identical finding. That is what
#     makes a Tier A member safe by construction, and it is the answer to
#     US-3's worry: a pass that only reorders refusals cannot teach an operator
#     to bypass it, because there is nothing on the other side of the bypass.
#
#   TIER B — moved forward from `release-preflight` (step 6), which is
#     network-gated as a whole while these three assertions are pure local
#     reads. These ARE new refusals at step 5, so each must be argued
#     satisfiable by step 5, and each is:
#       * the notes carry a real, quoted date  <- runbook Phase 1 step 1
#       * every a2a-ref default names the cut  <- runbook Phase 1 step 2
#       * the template floor is not ahead      <- runbook Phase 1 step 4
#     They are REUSED, not reimplemented: `release-preflight.sh` carries a
#     BASH_SOURCE guard precisely so its assertions can be borrowed, and
#     `check-release-record.sh` already borrows `_ref_default_of` the same way.
#
# WHAT THIS PASS CANNOT DO, stated because the spec asks for it in teeth
# rather than in prose. It has exactly two exits: refuse (non-zero, no
# receipt), or fall through to phase 1 unchanged. There is no path by which it
# returns success on its own, and none by which it reaches `write_receipt`.
# T23 asserts that structurally, against this function's own source, so an
# edit that moves the container above it or lets a refusal fall through reds.
#
# WHERE IT SITS, and why not first. The Docker probe stays ahead of it: an
# unreachable daemon is UNMEASURED, which gate-lib says dominates a mixed run,
# and T18 exercises `--release` over a PATH carrying neither `docker` nor `go`.
# The dirty-tree refusal stays ahead of it too, and that ordering is load
# bearing rather than incidental: after it, the working tree IS the tree the
# receipt will name, so every file this pass reads is a file the container
# will read. It is also why `release-notes-freshness` is a member at all —
# the "uncommitted notes file" state that gate correctly refuses cannot reach
# here, because a tree carrying one is refused two checks earlier.
#
# NOT run by `--dry-run`. A rehearsal executes nothing (T17); the plan names
# this phase and its members, and that is all a rehearsal may do.

# fast_pass_version — the version being cut, DERIVED rather than passed in.
#
# The newest authored notes file is the same source `internal/notes`'
# TestReusableRefDefaultMatchesTheNewestAuthoredVersion uses, so the two
# cannot disagree about which release is in flight. An operator-supplied
# version could; `make release-check` deliberately takes no argument.
fast_pass_version() { # $1 = repo root -> "X.Y.Z", or empty
  local f base
  for f in "$1"/releasenotes/*.yaml; do
    [ -f "$f" ] || continue
    base="${f##*/}"; base="${base%.yaml}"
    case "$base" in
      [0-9]*.[0-9]*.[0-9]*) ;;
      *) continue ;;
    esac
    case "$base" in
      *[!0-9.]*) continue ;;
    esac
    printf '%s\n' "$base"
  done | sort -V | tail -1
}

# _fp_run runs a member with the JUDGED TREE as its working directory. Several
# of the Tier A gates read relative paths (`find releasenotes`), so a fixture
# root is only judgeable if the cwd moves with it — which is exactly what the
# teeth need.
#
# THE SCRIPTS THEMSELVES ARE ALWAYS THIS CHECKOUT'S ($ROOT), never the judged
# tree's. Logic from here, data from there: a tooth pointing this at a fixture
# is then exercising the REAL gate against fabricated content, rather than a
# copy of the gate the tooth itself wrote — which would prove nothing about
# what a release run does.
_fp_run() { # $1 = root, $2... = command
  local root="$1"; shift
  ( cd "$root" || exit 1; "$@" )
}

# fast_pass_step — one member, timed, and its refusal NAMES THE RUNBOOK STEP
# that satisfies it (AC-2). "Which of eighteen steps am I mid-way through" is
# the question an operator actually has when a release gate reds.
fast_pass_step() { # $1 = label, $2 = the runbook step that satisfies it, $3... = command
  local label="$1" where="$2"; shift 2
  local start end rc=0
  start="$(date +%s)"
  "$@" || rc=$?
  end="$(date +%s)"
  if [ "$rc" -eq 0 ]; then
    printf 'ci-parity: fast pass — %s OK (%ss)\n' "$label" "$((end - start))"
    return 0
  fi
  gate_fail "release fast pass: $label — see the lines above for the finding. Satisfied by: $where. Refused in $((end - start))s, before the container started; the container would have reported the same thing about twenty-four minutes from now."
  return 1
}

FAST_PASS_PLAN='phase 0 (fast static pass, ~20s) — the cut commit itself, cheapest member first:
  the notes carry a real, quoted date, not the sentinel      (release-preflight, borrowed)
  every a2a-ref default names the version being cut          (release-preflight, borrowed)
  the space-template write floor is not ahead of it          (release-preflight, borrowed)
  the release record and the world agree                     (check-release-record)
  the notes corpus covers every later product commit         (check-release-notes-freshness)
  the demo-JSON golden matches the notes corpus it embeds    (internal/cli golden)
  a provider-tier deferral record covers the version cut     (check-provider-tier-deferral)'

fast_pass() { # $1 = repo root (default $ROOT) -> 0 pass, 1 refuse
  local root="${1:-$ROOT}" version rc=0
  version="$(fast_pass_version "$root")"
  if [ -z "$version" ]; then
    gate_fail "release fast pass: $root/releasenotes carries no semver <version>.yaml, so the version being cut cannot be named and no member below can be asked. Satisfied by: docs/runbooks/release.md Phase 1 step 1."
    return 1
  fi
  printf '\n=== ci-parity: fast pass — the cut commit for v%s ===\n' "$version"

  # ── TIER B FIRST, and the order is the acceptance criterion ────────────────
  #
  # Borrowed from release-preflight (step 6), which is network-gated as a whole
  # while these three assertions are pure local reads costing well under a
  # second. They are also the "is this a cut at all?" question: a tree still
  # wearing the pre-tag sentinel has not finished step 1, so asking the four
  # members below what they think of it would be judging a release candidate
  # that does not exist yet. This trio therefore REFUSES IMMEDIATELY rather
  # than collecting company — which is what makes the sentinel case, the one
  # that cost twenty-four minutes on 2026-08-22, a sub-second refusal.
  #
  # In a SUBSHELL: release-preflight sets `set -euo pipefail` and four globals
  # of its own at source time, and this script is not the place to find out
  # which of them collide. The guard at the bottom of that file is what makes
  # sourcing it safe; the subshell is what makes it local.
  if ! (
    set +e
    # PARAMETER EXPANSION, not `$(dirname ...)`: classify-guard resolves exactly
    # two literal forms and refuses anything else, because a PUBLIC file that
    # sources a path the boundary check cannot read is how a private dependency
    # ships invisibly. All four Tier A gates below and this one are ALLOW_FILES
    # — checked, not remembered; `check-release-record.sh`,
    # `check-release-notes-freshness.sh`, `check-provider-tier-deferral.sh` and
    # `release-preflight.sh` each carry their own "must stay PUBLIC" refusal in
    # classify-guard, so `make projection` sees the same phase 0 this tree does.
    # shellcheck source=release-preflight.sh
    . "${BASH_SOURCE[0]%/*}/release-preflight.sh"
    prc=0
    assert_notes_carry_a_date "$root" "v$version" || prc=1
    assert_ref_default_matches "$root" "v$version" || prc=1
    assert_floor_not_ahead     "$root" "v$version" || prc=1
    exit "$prc"
  ); then
    gate_fail "release fast pass: a static release-preflight assertion refused — see the lines above. Satisfied by: docs/runbooks/release.md Phase 1 step 1 (the released: date), step 2 (the a2a-ref defaults) and step 4 (leave the template floor and pins alone). All three are pure local reads borrowed from step 6's gate, so none of this needed the network — and meeting them at step 6 instead costs the whole composed run again."
    printf 'ci-parity: FAST PASS REFUSED — the four remaining members were NOT asked: this tree has not finished Phase 1, so there is no release candidate for them to judge. The container was never started and no receipt was written for %s.\n' \
      "$(git -C "$root" rev-parse HEAD 2>/dev/null || echo 'this tree')" >&2
    return 1
  fi
  printf 'ci-parity: fast pass — the three static release-preflight assertions OK\n'

  # ── TIER A — already inside the container; asked here first, at no new risk ─
  #
  # ALL FOUR RUN even after one refuses. They are independent findings about a
  # tree that IS a candidate, and three twenty-second round trips to collect
  # them one at a time is the same impatience this epic was cut against, one
  # order of magnitude down.
  fast_pass_step "the release record and the world agree" \
    "docs/runbooks/release.md Phase 1 steps 1-4 (the released: date, the a2a-ref default, the untouched template floor and pins)" \
    _fp_run "$root" bash "$ROOT/scripts/check-release-record.sh" || rc=1

  fast_pass_step "the notes corpus covers every product commit after it" \
    "docs/runbooks/release.md Phase 1 step 1 (author releasenotes/$version.yaml)" \
    _fp_run "$root" env ROOT="$root" bash "$ROOT/scripts/check-release-notes-freshness.sh" || rc=1

  # The 2026-08-22 loss, by name. The golden embeds the release-notes corpus,
  # so it moves TWICE per release — once when the notes file is added and
  # again when `released:` stops being provisional. Regenerating after only
  # the first leaves a drift nothing local catches, and the container found it.
  fast_pass_step "the demo-JSON golden matches the notes corpus it embeds" \
    "docs/runbooks/release.md Phase 1 step 1, its LAST edit (regenerate the golden AFTER stamping the date)" \
    _fp_run "$root" go test ./internal/cli/ -count=1 \
      -run 'TestHtmlCommandDemoJSONMatchesIndentedCompatibilityGolden' || rc=1

  # LAST because it is the only expensive member: ~15s, almost all of it one
  # repo-wide `find` for live-e2e evidence directories. Worth its price here —
  # a release cut without a signed deferral record is refused by `make check`
  # inside the container anyway, twenty-four minutes later.
  fast_pass_step "a provider-tier deferral record covers v$version, or live evidence does" \
    "docs/runbooks/release.md §\"Deferring the provider tier\", condition 1" \
    _fp_run "$root" bash "$ROOT/scripts/check-provider-tier-deferral.sh" || rc=1

  if [ "$rc" -ne 0 ]; then
    printf 'ci-parity: FAST PASS REFUSED — the container was never started and no receipt was written for %s.\n' \
      "$(git -C "$root" rev-parse HEAD 2>/dev/null || echo 'this tree')" >&2
    return 1
  fi
  printf 'ci-parity: fast pass green — nothing static stands between this tree and a release.\n'
  return 0
}

release_gate() { # $1 = --dry-run | (empty)
  local dry=0
  case "${1:-}" in
    "") ;;
    --dry-run) dry=1 ;;
    *) printf 'usage: %s --release [--dry-run]\n' "$0" >&2; return 2 ;;
  esac

  local head_before head_after dirty started ended
  started="$(date +%s)"
  head_before="$(git -C "$ROOT" rev-parse HEAD)"

  # A RECEIPT NAMES A SHA. If the working tree is not that SHA, the receipt is a
  # claim about a tree that was never judged — which is precisely the 2026-08-21
  # failure this deliverable exists to close, one step earlier in the pipeline.
  # docs/runbooks/publish-to-public.sh's assert_private_source_ready refuses the
  # same thing at the same strength; this is the producer half of that rule.
  dirty="$(git -C "$ROOT" status --porcelain=v1 --untracked-files=all)"

  printf '\n=== release gate — the tree at %s ===\n' "$head_before"
  printf '%s\n' "$FAST_PASS_PLAN"
  printf 'phase 1 (container, GNU userland) — the full suite:\n'
  list_members | sed 's/^/  /'
  printf 'phase 2 (host, darwin) — npm run check:quality (a11y + Lighthouse: measurable here, not in the image) and the macos-15 delta, derived from runs-on:\n'
  macos_jobs .github/workflows | sed 's/^/  /'
  printf 'receipt: %s/%s.json\n' "$RECEIPT_DIR" "$head_before"

  # THE REHEARSAL RUNS ON A DIRTY TREE; THE REAL ONE DOES NOT. A ship gate
  # nobody can rehearse is its own problem — and a rehearsal that refused
  # exactly when a developer has edits open would be a rehearsal nobody could
  # ever perform. So the dry run REPORTS the refusal the real run would make,
  # by name, and exits 0; only the real run enforces it.
  if [ "$dry" -eq 1 ]; then
    if [ -n "$dirty" ]; then
      printf '\nci-parity: this tree is DIRTY (%s path(s)). A real run would REFUSE here — see below — because a receipt naming %s would describe a tree that does not exist.\n' \
        "$(printf '%s\n' "$dirty" | wc -l | tr -d ' ')" "$head_before"
    fi
    printf '\nci-parity: DRY RUN — nothing was executed and NO RECEIPT WAS WRITTEN. A rehearsal must never leave evidence a publisher would accept.\n'
    return 0
  fi

  # THE UNMEASURABLE PRECONDITION IS REPORTED BEFORE THE MEANINGLESS ONE, which
  # is gate-lib's own rule one level up ("UNMEASURED DOMINATES A MIXED RUN...
  # what the exit code answers is 'can I trust this verdict as complete?'").
  # Both checks below are instantaneous, so the order costs nothing and decides
  # which single thing the operator is sent to fix first: a gate that cannot run
  # at all outranks a tree that is not yet publishable.
  #
  # "DOCKER IS DOWN" AND "THE TREE IS BROKEN" ARE OPPOSITE INSTRUCTIONS.
  # ci-parity-docker.sh already dies hard on a missing daemon, and its message is
  # the right one — but its exit status is 1, indistinguishable from a gate that
  # ran and refused. This probe is deliberately the same pair of checks that
  # script makes, run BEFORE the phase, so an unreachable daemon reports
  # gate_unmeasured (exit 3) while a container that started and then judged the
  # tree reports gate_fail.
  #
  # There is deliberately NO --no-docker escape hatch. A bare flag becomes a
  # permanent silent downgrade; the shape this repo already uses for a justified
  # skip is a dated, written, COUNTED record that refuses at the third
  # unremediated use (scripts/check-provider-tier-deferral.sh).
  if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
    gate_unmeasured "the container phase could not START: Docker is not on PATH or its daemon is unreachable. NOTHING was measured — this is not a host-only pass, and the host cannot stand in. The container is the PRIMARY judge on two axes at once: GNU userland, where every non-notifier CI job actually runs (it found a rule that had been entirely inert in CI while its teeth passed on every laptop), and the TRACKED tree, because it syncs and cleans while the host judges a working tree carrying generated, gitignored files. Start Docker and re-run; there is no flag that skips this."
    return "$GATE_EXIT_UNMEASURED"
  fi

  # A RECEIPT NAMES A SHA. If the working tree is not that SHA, the receipt is a
  # claim about a tree that was never judged — which is precisely the 2026-08-21
  # failure this deliverable exists to close, one step earlier in the pipeline.
  if [ -n "$dirty" ]; then
    gate_fail "the working tree has staged, worktree or untracked changes, so a receipt for $head_before would name a tree that does not exist. Commit or clean first; a ship verdict must be about something a publisher can push."
    printf '%s\n' "$dirty" | head -20 | sed 's/^/    /' >&2
    return 1
  fi

  # PHASE 0 — the fast static pass. Deliberately the LAST thing before the
  # container and the FIRST thing that costs more than a second, so a static
  # mistake in the cut commit is refused now rather than at minute twenty-four.
  # It cannot pass this function on its own: the only two outcomes are the
  # refusal below and falling through to phase 1 unchanged (T23 holds that).
  if ! fast_pass "$ROOT"; then
    return 1
  fi

  local offset rc=0 container_rc=0 macos_rc=0
  # The container writes its phase telemetry into a directory bound out by
  # ci-parity-docker.sh (see its comment): verify.sh derives VERIFY_ROOT from
  # ROOT, which is /work in there. Read THAT file for phase 1 — the host's own
  # telemetry records nothing the container did.
  local container_telemetry="$ROOT/.a2a/cache/parity-container/telemetry.jsonl"
  local container_offset=0
  [ -f "$container_telemetry" ] && container_offset="$(wc -l <"$container_telemetry" | tr -d ' ')"
  offset="$(telemetry_offset)"

  # PHASE 1 — the container. Its daemon was proved reachable above, so a
  # non-zero status from here is the tree's, not the harness's.
  if ! run_step "release gate phase 1 — container suite" \
      bash "$ROOT/scripts/ci-parity-docker.sh" bash scripts/ci-parity.sh --suite; then
    container_rc=1
    gate_fail "the container phase RAN and did not pass. It is the primary judge, not a second opinion, and there is no host-only verdict to fall back to."
  fi

  # PHASE 2 — the host. The only part of CI nothing else can reach.
  if ! run_step "release gate phase 2 — host-only: check:quality + the macos-15 delta" host_only; then
    macos_rc=$?
    if [ "$macos_rc" -eq "$GATE_EXIT_UNMEASURED" ]; then
      printf 'ci-parity: phase 2 was UNMEASURED (see above) — the composed gate makes no claim about the macOS companion.\n' >&2
    else
      gate_fail "the macos-15 delta failed: the Swift companion CI ships does not build or test here."
    fi
  fi

  # AC4/T3 — a verdict may not outlive the SHA it judged.
  head_after="$(git -C "$ROOT" rev-parse HEAD)"
  if [ "$head_after" != "$head_before" ]; then
    gate_fail "the tree moved under this run. Judged: $head_before. HEAD is now: $head_after. Nothing was proven about $head_after and no receipt was written; re-run the composed gate on the tree you intend to publish."
    return 1
  fi

  telemetry_report "$offset" "the composed run" "$container_telemetry:$container_offset" || rc=$?

  ended="$(date +%s)"
  if [ "$container_rc" -ne 0 ] || [ "$macos_rc" -ne 0 ] || [ "$rc" -ne 0 ]; then
    printf '\nci-parity: RELEASE GATE RED — no receipt written for %s.\n' "$head_before" >&2
    if [ "$macos_rc" -eq "$GATE_EXIT_UNMEASURED" ] && [ "$container_rc" -eq 0 ] && [ "$rc" -eq 0 ]; then
      return "$GATE_EXIT_UNMEASURED"
    fi
    return 1
  fi

  write_receipt "$head_before" "$((ended - started))" "$COMPOSED_PHASE_COUNT"
}

# write_receipt — THE PRODUCER HALF. The consumer half lives in
# docs/runbooks/publish-to-public.sh, whose assert_private_source_ready already
# refuses a dirty tree and a HEAD that is not exactly the pushed branch, and is
# therefore the one place that already knows about SHAs. Producer witnesses,
# consumer refuses, and no fourth place learns what a SHA is.
#
# Under .a2a/, which is gitignored: a receipt written into the tracked tree
# would dirty the very tree the publisher asserts clean, so the artifact would
# refuse the publish it exists to authorise.
write_receipt() { # $1 = sha, $2 = wall seconds, $3 = the composed phase count
  local sha="$1" wall="$2" phases="$3"
  # This function does not count, and that is the point: the number it records
  # is the one telemetry_report printed, folded across host AND container. A
  # second counter here is how the receipt came to claim 0 over a run of 71.
  # A NUMERIC test, not an arithmetic one. `[ "$phases" -eq 0 ]` exits 2 on a
  # non-integer, so the `||` chain went false, the refusal was skipped, and the
  # receipt was written as `"verify_phases": abc,` — invalid JSON that the
  # publisher's own `grep '"verdict": "pass"'` accepts without noticing.
  case "$phases" in
    ''|*[!0-9]*|0) _receipt_countless=1 ;;
    *) _receipt_countless=0 ;;
  esac
  if [ "$_receipt_countless" -eq 1 ]; then
    gate_unmeasured "the composed run passed but no phase count reached the receipt, so nothing can be written about what the pass covered. No receipt written for $sha."
    return "$GATE_EXIT_UNMEASURED"
  fi
  mkdir -p "$RECEIPT_DIR"
  cat > "$RECEIPT_DIR/$sha.json" <<JSON
{
  "schema": "a2ahub.release-gate.receipt/v1",
  "sha": "$sha",
  "verdict": "pass",
  "at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "wall_seconds": $wall,
  "verify_phases": $phases,
  "phases": [
    { "name": "container-suite", "userland": "gnu/linux", "verdict": "pass" },
    { "name": "macos-15-delta", "userland": "darwin", "verdict": "pass" }
  ]
}
JSON
  printf '\nci-parity: RELEASE GATE GREEN for %s (%ss, %s verify phase(s)).\n' "$sha" "$wall" "$phases"
  printf 'receipt: %s\n' "$RECEIPT_DIR/$sha.json"
}

# --- --teeth -----------------------------------------------------------
#
# Spec 01-audit-sees-what-ci-runs.md §6, T1..T6. T1 runs against the REAL
# workflow corpus and is EXPECTED to red, naming a command the old extractor
# could never see — that is the phase's own finding, not a bug in the tooth.
# T2..T6 use synthetic single-file fixtures under a fresh temp dir so a
# folded block, a comment-laced body, and an empty corpus can each be
# constructed on demand, and every case re-invokes the REAL CLI entry point
# (`bash scripts/ci-parity.sh --audit <dir>`) as a fresh subprocess rather
# than calling the shell functions in-process, so what is proved is what a
# caller actually runs.

teeth_fail=0

run_audit_dir() { # $1 = workflow directory; sets AUDIT_OUT / AUDIT_RC
  local rc=0
  AUDIT_OUT="$(bash "$ROOT/scripts/ci-parity.sh" --audit "$1" 2>&1)" || rc=$?
  AUDIT_RC="$rc"
}

teeth_fixture_dir() { # $1 = workflow.yml content; prints the fresh temp dir
  local dir
  dir="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-teeth.XXXXXX")"
  printf '%s\n' "$1" > "$dir/workflow.yml"
  printf '%s' "$dir"
}

run_teeth() {
  # T1 — the real corpus. Expected RED, naming a command that lives ONLY
  # inside a literal block (release.yml's own release-notes guard) — the old
  # The ORIGINAL extraction never read this line: it stripped through
  # "run: ", which leaves a bare pipe for a literal block, and discarded that.
  # (The pipeline is described rather than quoted here on purpose — a shell
  # snippet inside a comment is parsed by the lane classifier as a real read,
  # and this one made it resolve six English words as file arguments.)
  # The marker is the guard's own `[ ! -f ... ]` test, not the `notes=`
  # assignment two lines above it: P1's refinement (2026-08-21) correctly
  # drops a pure assignment as non-command noise, so the assignment itself no
  # longer proves anything about literal-block reading — the `if` header
  # right below it, in the SAME block, still does.
  #
  # AMENDED 2026-08-21, when P1's triage landed. T1 used to assert the real
  # corpus RED, which was true while the finding was outstanding and became
  # false the moment it was triaged. "The corpus is red" is a STATE, not an
  # invariant; a tooth that pins a state expires. What must hold forever is
  # that the extraction READS a command living only inside a literal block —
  # so that is what T1 asserts now, whether the corpus is green or red.
  run_audit_dir "$ROOT/.github/workflows"
  if ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'if [ ! -f "$notes" ]; then'; then
    printf 'ci-parity --teeth: FAIL — T1: a command that lives ONLY inside a literal run: block (release.yml release-notes guard) is invisible to the audit; extraction is not reading block bodies\n%s\n' "$AUDIT_OUT" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T1 — the real corpus is read to the bottom of its literal blocks (a release.yml block-only command is extracted and accounted for)\n'
  fi

  # T2 — a synthetic workflow whose literal block carries an unexcused
  # command. Must RED, naming that exact command.
  local marker="teeth-marker-block-91fbe2 --flag"
  local d2 d3 d4 d5 d6
  d2="$(teeth_fixture_dir "$(cat <<EOF
name: teeth-fixture
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: run something
        run: |
          echo hello
          $marker
EOF
  )")"
  run_audit_dir "$d2"
  if [ "$AUDIT_RC" -eq 0 ] || ! printf '%s\n' "$AUDIT_OUT" | grep -qF "$marker"; then
    printf 'ci-parity --teeth: FAIL — T2: a literal-block command must RED naming itself; got rc=%s\n%s\n' "$AUDIT_RC" "$AUDIT_OUT" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T2 — a literal-block command REDS naming itself\n'
  fi
  rm -rf "$d2"

  # T3 — the SAME command, moved to single-line `run:` form. Shape must not
  # change the verdict.
  d3="$(teeth_fixture_dir "$(cat <<EOF
name: teeth-fixture
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: run something
        run: $marker
EOF
  )")"
  run_audit_dir "$d3"
  if [ "$AUDIT_RC" -eq 0 ] || ! printf '%s\n' "$AUDIT_OUT" | grep -qF "$marker"; then
    printf 'ci-parity --teeth: FAIL — T3: the same command in single-line form must RED identically; got rc=%s\n%s\n' "$AUDIT_RC" "$AUDIT_OUT" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T3 — single-line form REDS identically to the block form\n'
  fi
  rm -rf "$d3"

  # T4 — a folded block (`run: >`). Must be REFUSED LOUDLY, never silently
  # skipped (which would look exactly like "nothing to report").
  d4="$(teeth_fixture_dir "$(cat <<'EOF'
name: teeth-fixture
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: folded
        run: >
          echo "folded block, unsupported"
EOF
  )")"
  run_audit_dir "$d4"
  if [ "$AUDIT_RC" -eq 0 ] || ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'UNSUPPORTED'; then
    printf 'ci-parity --teeth: FAIL — T4: a folded run block must be refused loudly, not silently skipped; got rc=%s\n%s\n' "$AUDIT_RC" "$AUDIT_OUT" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T4 — a folded run block is refused loudly, not silently skipped\n'
  fi
  rm -rf "$d4"

  # T5 — a literal block whose body mixes comments and blank lines among real
  # commands. Every executable line counts; comments and blanks do not.
  d5="$(teeth_fixture_dir "$(cat <<'EOF'
name: teeth-fixture
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: multi
        run: |
          # a leading comment, not a command
          set -euo pipefail

          # another comment after a blank line
          teeth-marker-t5-alpha --one

          teeth-marker-t5-beta --two
EOF
  )")"
  run_audit_dir "$d5"
  if [ "$AUDIT_RC" -eq 0 ] \
    || ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'teeth-marker-t5-alpha --one' \
    || ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'teeth-marker-t5-beta --two'; then
    printf 'ci-parity --teeth: FAIL — T5: not every executable line in the block was counted; got rc=%s\n%s\n' "$AUDIT_RC" "$AUDIT_OUT" >&2
    teeth_fail=1
  elif printf '%s\n' "$AUDIT_OUT" | grep -qF 'a leading comment' \
    || printf '%s\n' "$AUDIT_OUT" | grep -qF 'another comment'; then
    printf 'ci-parity --teeth: FAIL — T5: a comment line inside the block was treated as a command\n%s\n' "$AUDIT_OUT" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T5 — every executable line counted, comments and blanks ignored\n'
  fi
  rm -rf "$d5"

  # T6 — zero workflow files. An empty corpus is a broken read, not a clean
  # repo, and must RED distinctly rather than reporting "every CI command is
  # executed locally or excused" over nothing.
  d6="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-teeth.XXXXXX")"
  run_audit_dir "$d6"
  if [ "$AUDIT_RC" -eq 0 ] || ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'EMPTY CORPUS'; then
    printf 'ci-parity --teeth: FAIL — T6: zero workflow files must RED with an EMPTY CORPUS refusal, not a clean pass; got rc=%s\n%s\n' "$AUDIT_RC" "$AUDIT_OUT" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T6 — an empty workflow corpus REDS with an explicit refusal\n'
  fi
  rm -rf "$d6"

  # T7 — THE TOOTH THAT MATTERS MOST (P1 refinement, 2026-08-21). The
  # classifier added to tell noise (`;;`, `*)`, `fi`, `}`, a case label,
  # jq-filter continuation lines) apart from real commands must not, in
  # earning that, drop a real one — that would be a gate green while blind,
  # which is this epic's own subject. Four shapes must all survive
  # extraction from the SAME block: a bare script invocation, an
  # assignment-PREFIXED invocation (the value does not make it pure — the
  # command after it still runs), a command inside an `if` header (the
  # condition IS the command), and a command on the line AFTER a case label
  # (the label itself is dropped; what follows it is not).
  d7="$(teeth_fixture_dir "$(cat <<'EOF'
name: teeth-fixture
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: real commands survive the noise filter
        run: |
          scripts/teeth-marker-t7-bare.sh
          VERSION="x" scripts/teeth-marker-t7-prefixed.sh
          if teeth-marker-t7-ifcond; then
            echo body
          fi
          case "$kind" in
            teeth-marker-t7-label)
              teeth-marker-t7-afterlabel --run
              ;;
          esac
EOF
  )")"
  run_audit_dir "$d7"
  if [ "$AUDIT_RC" -eq 0 ] \
    || ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'teeth-marker-t7-bare.sh' \
    || ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'VERSION="x" scripts/teeth-marker-t7-prefixed.sh' \
    || ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'if teeth-marker-t7-ifcond; then' \
    || ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'teeth-marker-t7-afterlabel --run'; then
    printf 'ci-parity --teeth: FAIL — T7: the noise filter dropped a real command; got rc=%s\n%s\n' "$AUDIT_RC" "$AUDIT_OUT" >&2
    teeth_fail=1
  elif printf '%s\n' "$AUDIT_OUT" | grep -qF 'no local step runs ;;' \
    || printf '%s\n' "$AUDIT_OUT" | grep -qF 'no local step runs fi' \
    || printf '%s\n' "$AUDIT_OUT" | grep -qF 'no local step runs teeth-marker-t7-label)'; then
    printf 'ci-parity --teeth: FAIL — T7: a bare delimiter or a case label alone surfaced as a command\n%s\n' "$AUDIT_OUT" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T7 — a bare invocation, an assignment-prefixed one, an if-header condition, and a command after a case label all survive; the surrounding ;;/label/fi do not\n'
  fi
  rm -rf "$d7"

  # T8 — a reusable workflow (workflow_call) with NO local caller must be
  # excluded from scope: its own marker command must NOT appear as UNCOVERED,
  # and the OUT OF SCOPE announcement must name it and count its commands. A
  # sibling ORDINARY workflow's own marker still reds — proves the exclusion
  # is scoped to the reusable file, not a blanket "audit found nothing" pass
  # (P1 final, Task 1).
  local d8 marker8_reusable marker8_ordinary
  marker8_reusable="teeth-marker-t8-reusable-91a2 --flag"
  marker8_ordinary="teeth-marker-t8-ordinary-77c3 --flag"
  d8="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-teeth.XXXXXX")"
  cat > "$d8/reusable-uncalled.yml" <<EOF
name: teeth-fixture-reusable-uncalled
on:
  workflow_call:
    inputs:
      example:
        required: false
        type: string
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: run something
        run: $marker8_reusable
EOF
  cat > "$d8/ordinary.yml" <<EOF
name: teeth-fixture-ordinary
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: run something
        run: $marker8_ordinary
EOF
  run_audit_dir "$d8"
  if [ "$AUDIT_RC" -eq 0 ] \
    || printf '%s\n' "$AUDIT_OUT" | grep -qF "no local step runs $marker8_reusable" \
    || ! printf '%s\n' "$AUDIT_OUT" | grep -qF "no local step runs $marker8_ordinary" \
    || ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'OUT OF SCOPE — 1 command(s) in 1 reusable workflow(s) (reusable-uncalled.yml)'; then
    printf 'ci-parity --teeth: FAIL — T8: an uncalled reusable workflow must be excluded from scope (named in the OUT OF SCOPE line, its command never UNCOVERED) while a sibling ordinary workflow still reds; got rc=%s\n%s\n' "$AUDIT_RC" "$AUDIT_OUT" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T8 — an uncalled reusable workflow is excluded from scope and named with its command count; a sibling ordinary workflow still reds\n'
  fi
  rm -rf "$d8"

  # T9 — the mirror of T8, and the one this epic exists to prove: a reusable
  # workflow CALLED LOCALLY by a sibling (`uses: ./reusable-called.yml`, the
  # exact shape a2a-validate-dogfood.yml uses on a2a-validate-reusable.yml)
  # is NOT excluded — this repo's own CI runs it, so its marker command must
  # still RED as UNCOVERED. `workflow_call` alone is not the whole rule;
  # dropping this half would silently un-audit 21 real commands (P1 final).
  local d9 marker9
  marker9="teeth-marker-t9-called-55e1 --flag"
  d9="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-teeth.XXXXXX")"
  cat > "$d9/reusable-called.yml" <<EOF
name: teeth-fixture-reusable-called
on:
  workflow_call:
    inputs:
      example:
        required: false
        type: string
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: run something
        run: $marker9
EOF
  cat > "$d9/caller.yml" <<EOF
name: teeth-fixture-caller
on: push
jobs:
  call-it:
    uses: ./reusable-called.yml
EOF
  run_audit_dir "$d9"
  if [ "$AUDIT_RC" -eq 0 ] \
    || ! printf '%s\n' "$AUDIT_OUT" | grep -qF "no local step runs $marker9" \
    || ! printf '%s\n' "$AUDIT_OUT" | grep -qF 'OUT OF SCOPE — 0 command(s) in 0 reusable workflow(s)'; then
    printf 'ci-parity --teeth: FAIL — T9: a reusable workflow CALLED LOCALLY by a sibling must stay IN SCOPE (its command still UNCOVERED, zero workflows excluded); got rc=%s\n%s\n' "$AUDIT_RC" "$AUDIT_OUT" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T9 — a reusable workflow called locally by a sibling stays in scope; its command still reds\n'
  fi
  rm -rf "$d9"

  # T10 — THE RUBBER-STAMP GUARD (P1 close, 2026-08-21). excused_reason()
  # grew a dozen new glob patterns this wave (release.yml, feedback-intake.yml,
  # macos-notifier, shell scaffolding, the dogfood path) — exactly the kind of
  # widening that could, one over-broad `*` too many, start excusing commands
  # it was never meant to see, and the audit would go from "proves parity" to
  # "prints a lot of green text". A command with NO excuse and NO local step
  # must still RED after this wave's additions, same as before it.
  local d10 marker10
  marker10="teeth-marker-t10-rubberstamp-c4e91 --flag"
  d10="$(teeth_fixture_dir "$(cat <<EOF
name: teeth-fixture
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: run something
        run: $marker10
EOF
  )")"
  run_audit_dir "$d10"
  if [ "$AUDIT_RC" -eq 0 ] || ! printf '%s\n' "$AUDIT_OUT" | grep -qF "no local step runs $marker10"; then
    printf 'ci-parity --teeth: FAIL — T10: an unexcused, unexecuted command must still RED after this wave'"'"'s excused_reason() additions; got rc=%s\n%s\n' "$AUDIT_RC" "$AUDIT_OUT" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T10 — an unexcused command still reds; this wave'"'"'s new excuses did not turn the audit into a rubber stamp\n'
  fi
  rm -rf "$d10"

  # ── release-loop-2026-08 P4, the composed ship gate. ──────────────────
  #
  # Every case below pins an INVARIANT, never a state. Wave 1 of this epic
  # shipped a tooth asserting "the real corpus is currently red" and it expired
  # the moment the finding was triaged; T1 above carries the amendment. So:
  # nothing here counts members, names a step number, or asserts today's
  # workflow inventory.

  # T11 — THE PARTITION IS DERIVED. The host/container split is the one axis
  # this repo can read out of the workflows themselves, and the epic exists to
  # delete hand-kept lists. A macOS job must be reported, an ubuntu sibling must
  # not, and a `runs-on:` this reader cannot resolve must be announced as
  # UNRESOLVED rather than silently counted as neither — a shape not understood
  # must never look like "nothing to report" (the same rule T4 pins for folded
  # run blocks).
  local d11 out11
  d11="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-teeth.XXXXXX")"
  cat > "$d11/mac.yml" <<'EOF'
name: teeth-fixture-mac
on: push
jobs:
  needs-a-mac:
    runs-on: macos-99
    steps:
      - run: swift --version
  needs-no-mac:
    runs-on: ubuntu-latest
    steps:
      - run: echo hello
  cannot-tell:
    runs-on: ${{ matrix.os }}
    steps:
      - run: echo hello
EOF
  out11="$(bash "$ROOT/scripts/ci-parity.sh" --macos-jobs "$d11" 2>&1)"
  if ! printf '%s\n' "$out11" | grep -qF 'needs-a-mac:macos-99' \
    || printf '%s\n' "$out11" | grep -qF 'needs-no-mac' \
    || ! printf '%s\n' "$out11" | grep -qF 'cannot-tell:UNRESOLVED'; then
    printf 'ci-parity --teeth: FAIL — T11: the macOS partition must be DERIVED from runs-on: — a macos-prefixed runner reported (including a future major), an ubuntu sibling not, and an unresolvable runs-on announced rather than silently dropped\n%s\n' "$out11" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T11 — the host/container partition is derived from runs-on:, tolerates a macOS major bump, and announces a runs-on it cannot resolve\n'
  fi
  rm -rf "$d11"

  # T12 — AN ABSENT TOOLCHAIN IS UNMEASURED, NOT A FAILURE AND NOT A SKIP.
  # "There is no Swift here" is a fact about the measurement; "the notifier does
  # not build" is a fact about the product. A composed ship gate that let either
  # wear the other's clothes would be worse than three separate targets, because
  # it would look complete. Asserted in BOTH directions and on the exit code,
  # which is what a caller branches on: 3, never 1.
  local d12 out12 rc12=0 t
  d12="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-teeth.XXXXXX")"
  # A PATH with everything the script needs EXCEPT swift. `swift` is absent by
  # omission rather than by a stub that fails, so what is exercised is the
  # `command -v` guard a machine without Xcode would hit, not a fake failure.
  for t in uname mktemp rm date sed git awk grep wc sort tail head cat ls tr cp mkdir dirname basename; do
    if [ -x "/usr/bin/$t" ]; then ln -sf "/usr/bin/$t" "$d12/$t"
    elif [ -x "/bin/$t" ]; then ln -sf "/bin/$t" "$d12/$t"; fi
  done
  out12="$(env PATH="$d12" /bin/bash "$ROOT/scripts/ci-parity.sh" --macos-delta 2>&1)" || rc12=$?
  if [ "$rc12" -ne "$GATE_EXIT_UNMEASURED" ] \
    || ! printf '%s\n' "$out12" | grep -qF 'UNMEASURED' \
    || printf '%s\n' "$out12" | grep -qF 'FAIL'; then
    printf 'ci-parity --teeth: FAIL — T12: a missing Swift toolchain must report UNMEASURED and exit %s, never FAIL and never a silent skip; got rc=%s\n%s\n' "$GATE_EXIT_UNMEASURED" "$rc12" "$out12" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T12 — an unreachable macOS toolchain is UNMEASURED (exit %s), textually distinct from a product failure\n' "$GATE_EXIT_UNMEASURED"
  fi
  rm -rf "$d12"

  # T13 — AC3 HAS TEETH. A release step naming a command the composition does
  # not run is REFUSED, by name. This is the whole point of the docs criterion:
  # steps 5, 5a and 5b each arrived from somebody who had read the document
  # first, so prose could not have stopped any of them. The fixture uses the
  # runbook's own heading shapes; the assertion is about the RULE, not about
  # which targets happen to be members today.
  local d13 out13 rc13=0
  d13="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-teeth.XXXXXX")"
  cat > "$d13/runbook.md" <<'EOF'
### Phase 1 — prepare

1. **`make check`** — a real member, must be accepted.
2. **`make teeth-marker-t13-accreted`** — a fourth suite nobody composed.

### Phase 4 — after the tag

3. **`make teeth-marker-t13-out-of-phase`** — outside Phases 1-3, never judged.
EOF
  out13="$(bash "$ROOT/scripts/ci-parity.sh" --audit-runbook "$d13/runbook.md" 2>&1)" || rc13=$?
  if [ "$rc13" -eq 0 ] \
    || ! printf '%s\n' "$out13" | grep -qF 'UNCOMPOSED — make teeth-marker-t13-accreted' \
    || printf '%s\n' "$out13" | grep -qF 'teeth-marker-t13-out-of-phase'; then
    printf 'ci-parity --teeth: FAIL — T13: a release step naming an uncomposed command must RED naming it, and a step outside Phases 1-3 must not be judged; got rc=%s\n%s\n' "$rc13" "$out13" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T13 — a runbook step naming a command the composition does not run is refused by name; Phase 4 is out of scope\n'
  fi

  # T14 — THE MEMBERSHIP CLAIM CANNOT DRIFT FROM THE THING THAT RUNS, which is
  # spec §6's tooth T2 ("a suite removed from the composition → RED") mechanized:
  # membership is read out of suite_members(), so a member deleted from the
  # composition stops satisfying the runbook step that names it, on the same
  # edit. Proved here by asking about a command that is NOT in the suite and
  # NOT an entry point — the same answer a deleted member would produce.
  local out14 rc14=0
  cat > "$d13/removed.md" <<'EOF'
### Phase 1 — prepare

1. **`make teeth-marker-t14-was-a-member`** — a step whose member is gone.
EOF
  out14="$(bash "$ROOT/scripts/ci-parity.sh" --audit-runbook "$d13/removed.md" 2>&1)" || rc14=$?
  if [ "$rc14" -eq 0 ] || ! printf '%s\n' "$out14" | grep -qF 'the composed release gate runs nothing that covers it'; then
    printf 'ci-parity --teeth: FAIL — T14: a command absent from suite_members() must not satisfy a release step; membership is derived, so removing a member must red the step naming it. Got rc=%s\n%s\n' "$rc14" "$out14" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T14 — membership is derived from the suite that runs, so a removed member reds the step that names it\n'
  fi

  # T15 — AN EMPTY READ IS UNMEASURED, NOT A CLEAN RUNBOOK. Zero extracted
  # commands means the headings changed shape or the extractor broke; reporting
  # "every step is composed" over nothing is the false green this whole file
  # exists to refuse (T6 pins the same rule for an empty workflow corpus).
  local out15 rc15=0
  printf '### Phase 1 — prepare\n\n1. Author the notes.\n' > "$d13/empty.md"
  out15="$(bash "$ROOT/scripts/ci-parity.sh" --audit-runbook "$d13/empty.md" 2>&1)" || rc15=$?
  if [ "$rc15" -eq 0 ] || ! printf '%s\n' "$out15" | grep -qF 'UNMEASURED'; then
    printf 'ci-parity --teeth: FAIL — T15: a runbook from which zero step commands were extracted must report UNMEASURED, not a clean pass; got rc=%s\n%s\n' "$rc15" "$out15" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T15 — a runbook read that extracted nothing is UNMEASURED, never a clean pass\n'
  fi
  rm -rf "$d13"

  # T16 — PHASES ARE COUNTED, AND A PHASE PAID FOR TWICE IS REFUSED BY NAME.
  # AC1 as drafted counted `make check` BANNERS; the strict lane re-ran the
  # ceiling's phases without printing one, which is how a whole duplicate
  # ceiling hid inside a suite. The invariant is not "the ceiling ran exactly
  # once" — a future member could legitimately change that count — it is that
  # no (mode, gate) pair is paid for twice in one composed run.
  local d16 out16 rc16=0
  d16="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-teeth.XXXXXX")"
  cat > "$d16/clean.jsonl" <<'EOF'
{"gate":"go-test","verdict":"pass","duration_ms":1,"mode":"full","at":"2026-08-21T00:00:00Z"}
{"gate":"logic-e2e","verdict":"pass","duration_ms":1,"mode":"full","at":"2026-08-21T00:00:01Z"}
{"gate":"harness-teeth","verdict":"pass","duration_ms":1,"mode":"harness","at":"2026-08-21T00:00:02Z"}
EOF
  cp "$d16/clean.jsonl" "$d16/dupe.jsonl"
  printf '{"gate":"logic-e2e","verdict":"pass","duration_ms":1,"mode":"full","at":"2026-08-21T00:00:03Z"}\n' >> "$d16/dupe.jsonl"

  out16="$(env A2A_VERIFY_TELEMETRY="$d16/clean.jsonl" bash "$ROOT/scripts/ci-parity.sh" --phases 0 "the fixture" 2>&1)" || rc16=$?
  if [ "$rc16" -ne 0 ] || ! printf '%s\n' "$out16" | grep -qF 'no phase ran twice'; then
    printf 'ci-parity --teeth: FAIL — T16a: a run in which every phase appears once must pass and say so; got rc=%s\n%s\n' "$rc16" "$out16" >&2
    teeth_fail=1
  fi
  rc16=0
  out16="$(env A2A_VERIFY_TELEMETRY="$d16/dupe.jsonl" bash "$ROOT/scripts/ci-parity.sh" --phases 0 "the fixture" 2>&1)" || rc16=$?
  if [ "$rc16" -eq 0 ] || ! printf '%s\n' "$out16" | grep -qF 'full logic-e2e' \
    || ! printf '%s\n' "$out16" | grep -qF 'ran TWICE inside one composed run'; then
    printf 'ci-parity --teeth: FAIL — T16b: a phase executed twice in one composed run must RED naming the repeated (mode, gate) pair; got rc=%s\n%s\n' "$rc16" "$out16" >&2
    teeth_fail=1
  fi
  rc16=0
  out16="$(env A2A_VERIFY_TELEMETRY="$d16/absent.jsonl" bash "$ROOT/scripts/ci-parity.sh" --phases 0 "the fixture" 2>&1)" || rc16=$?
  if [ "$rc16" -ne "$GATE_EXIT_UNMEASURED" ] || ! printf '%s\n' "$out16" | grep -qF 'UNMEASURED'; then
    printf 'ci-parity --teeth: FAIL — T16c: absent telemetry must be UNMEASURED (exit %s), never a phase count of zero reported as clean; got rc=%s\n%s\n' "$GATE_EXIT_UNMEASURED" "$rc16" "$out16" >&2
    teeth_fail=1
  fi
  if [ "$teeth_fail" -eq 0 ]; then
    printf 'ci-parity --teeth: T16 — phases are counted from verify.sh telemetry; a repeat reds naming it, and absent telemetry is UNMEASURED rather than zero\n'
  fi
  rm -rf "$d16"

  # T17 — A REHEARSAL LEAVES NO EVIDENCE. `--release --dry-run` exists so a ship
  # gate can be rehearsed at all; a rehearsal that wrote a receipt would hand
  # the publisher an authorisation for a run that never happened.
  local out17 rc17=0 receipts_before receipts_after
  receipts_before="$( (ls -1 "$RECEIPT_DIR" 2>/dev/null || true) | wc -l | tr -d ' ')"
  out17="$(bash "$ROOT/scripts/ci-parity.sh" --release --dry-run 2>&1)" || rc17=$?
  receipts_after="$( (ls -1 "$RECEIPT_DIR" 2>/dev/null || true) | wc -l | tr -d ' ')"
  if [ "$rc17" -ne 0 ] \
    || [ "$receipts_before" != "$receipts_after" ] \
    || ! printf '%s\n' "$out17" | grep -qF 'NO RECEIPT WAS WRITTEN'; then
    printf 'ci-parity --teeth: FAIL — T17: a dry run must execute nothing, write no receipt, and say so; got rc=%s receipts %s->%s\n%s\n' "$rc17" "$receipts_before" "$receipts_after" "$out17" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T17 — a rehearsal writes no receipt; the ship gate can be practised without producing evidence a publisher would accept\n'
  fi

  # T18 — THE TOOTH SPEC §6 CALLS THE IMPORTANT ONE. A composed gate that
  # silently degraded to the host suite when Docker is down would be a WORSE
  # instrument than three separate targets, because it would look complete. It
  # must red — and it must red as UNMEASURED, because "the daemon is unreachable"
  # is a fact about the measurement, not about the tree. Exercised through a
  # PATH with no `docker` on it, which is what a machine without Docker Desktop
  # actually looks like; the receipt count is asserted too, since the whole
  # danger is an authorisation written for a run that never happened.
  local d18 out18 rc18=0 before18 after18 t18
  d18="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-teeth.XXXXXX")"
  for t18 in bash uname mktemp rm date sed git awk grep wc sort tail head cat ls tr cp mkdir dirname basename swift; do
    if [ -x "/usr/bin/$t18" ]; then ln -sf "/usr/bin/$t18" "$d18/$t18"
    elif [ -x "/bin/$t18" ]; then ln -sf "/bin/$t18" "$d18/$t18"; fi
  done
  before18="$( (ls -1 "$RECEIPT_DIR" 2>/dev/null || true) | wc -l | tr -d ' ')"
  out18="$(env PATH="$d18" /bin/bash "$ROOT/scripts/ci-parity.sh" --release 2>&1)" || rc18=$?
  after18="$( (ls -1 "$RECEIPT_DIR" 2>/dev/null || true) | wc -l | tr -d ' ')"
  if [ "$rc18" -ne "$GATE_EXIT_UNMEASURED" ] \
    || ! printf '%s\n' "$out18" | grep -qF 'UNMEASURED' \
    || ! printf '%s\n' "$out18" | grep -qF 'could not START' \
    || [ "$before18" != "$after18" ]; then
    printf 'ci-parity --teeth: FAIL — T18: an unreachable Docker daemon must red as UNMEASURED (exit %s) naming the container'"'"'s own value, and must write no receipt; got rc=%s receipts %s->%s\n%s\n' \
      "$GATE_EXIT_UNMEASURED" "$rc18" "$before18" "$after18" "$out18" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T18 — Docker unreachable is UNMEASURED, never a silent host-only pass, and leaves no receipt\n'
  fi
  rm -rf "$d18"

  # T19 — THE RECEIPT CARRIES THE COMPOSED COUNT, NOT THE HALF THE HOST CAN SEE.
  # The ship gate's sixth run, 2026-08-21, was the first green one, and its
  # receipt read `"verdict": "pass", "verify_phases": 0` over a run that had
  # executed 71. write_receipt counted a second time, from the host telemetry
  # alone, and every one of those 71 lines had been written by the container
  # into a file bound out of the image. Nothing was wrong with the RUN; the
  # ARTIFACT the publisher trusts announced a number nobody had measured — this
  # gate's own subject, wearing this gate's clothes.
  #
  # Paired on purpose. (a) proves the merged reading is what gets reported;
  # (b) proves (a) is not vacuous — the very same host file, read without the
  # container's, is UNMEASURED rather than a tidy zero, so 3 can only have come
  # from across the boundary. (c) is structural and says so: it guards the
  # regression itself, a second counter reappearing inside the writer.
  local d19 out19 rc19=0 body19
  d19="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-teeth.XXXXXX")"
  : > "$d19/host.jsonl"
  cat > "$d19/container.jsonl" <<'EOF'
{"gate":"go-test","verdict":"pass","duration_ms":1,"mode":"full","at":"2026-08-21T00:00:00Z"}
{"gate":"logic-e2e","verdict":"pass","duration_ms":1,"mode":"full","at":"2026-08-21T00:00:01Z"}
{"gate":"gitleaks","verdict":"pass","duration_ms":1,"mode":"full","at":"2026-08-21T00:00:02Z"}
EOF
  out19="$(env A2A_VERIFY_TELEMETRY="$d19/host.jsonl" bash "$ROOT/scripts/ci-parity.sh" \
    --phases 0 "the fixture" "$d19/container.jsonl:0" 2>&1)" || rc19=$?
  if [ "$rc19" -ne 0 ] || ! printf '%s\n' "$out19" | grep -qF 'executed 3 verify phase(s)'; then
    printf 'ci-parity --teeth: FAIL — T19a: phases written only by the container must still be counted; wanted 3, got rc=%s\n%s\n' "$rc19" "$out19" >&2
    teeth_fail=1
  fi
  rc19=0
  out19="$(env A2A_VERIFY_TELEMETRY="$d19/host.jsonl" bash "$ROOT/scripts/ci-parity.sh" \
    --phases 0 "the fixture" 2>&1)" || rc19=$?
  if [ "$rc19" -ne "$GATE_EXIT_UNMEASURED" ] || ! printf '%s\n' "$out19" | grep -qF 'UNMEASURED'; then
    printf 'ci-parity --teeth: FAIL — T19b: the same host file read alone must be UNMEASURED, which is what proves T19a counted across the boundary; got rc=%s\n%s\n' "$rc19" "$out19" >&2
    teeth_fail=1
  fi
  # CODE only. A structural assertion that reads the prose above the code will
  # trip on the comment explaining the very shape it forbids — which is exactly
  # what T19d did on its first run, against the comment naming `-eq 0` as the
  # thing not to do.
  body19="$(awk '/^write_receipt\(\) \{/,/^\}/' "$ROOT/scripts/ci-parity.sh" | grep -v '^[[:space:]]*#')"
  if printf '%s\n' "$body19" | grep -q 'VERIFY_TELEMETRY'; then
    printf 'ci-parity --teeth: FAIL — T19c: write_receipt reads the telemetry again. The count has ONE producer (telemetry_report) and is passed in; a second reading here is exactly how the receipt came to claim 0 over 71.\n' >&2
    teeth_fail=1
  fi
  # T19d, also structural and said so: `[ "$n" -eq 0 ]` exits 2 on a
  # non-integer, so an arithmetic guard lets a non-numeric count through and
  # writes `"verify_phases": abc,` — invalid JSON the publisher's own
  # `grep '"verdict": "pass"'` still accepts. The guard must be a NUMERIC test.
  if printf '%s\n' "$body19" | grep -qE '\$phases" -eq 0|\$3" -eq 0'; then
    printf 'ci-parity --teeth: FAIL — T19d: the receipt count is guarded arithmetically. `[ "$n" -eq 0 ]` exits 2 on a non-integer and the refusal is skipped; use a numeric case pattern so a non-numeric count refuses instead of producing invalid JSON.\n' >&2
    teeth_fail=1
  fi
  if [ "$teeth_fail" -eq 0 ]; then
    printf 'ci-parity --teeth: T19 — the receipt records the composed count; a container-only run counts 3, the host file alone is UNMEASURED, the writer does not count again, and the count is guarded numerically rather than arithmetically\n'
  fi
  rm -rf "$d19"

  # --- T20: AN EXCUSE MUST NAME WHAT IT EXCUSES. -------------------------
  #
  # The gitleaks arm used to end in three bare globs — *"curl"*, *"tar "*,
  # *"sha256sum"* — so ANY command containing those substrings was excused
  # under a reason naming gitleaks. The golangci-lint installer, which no
  # local step runs, was reported as "installs the pinned gitleaks binary".
  # This audit is the declared SSOT for what CI runs and it was answering with
  # a claim it had not checked.
  #
  # Both directions, because a one-sided tooth cannot tell a narrow arm from a
  # deleted one: a real gitleaks command must still BE excused.
  local gl_reason foreign_reason
  gl_reason="$(excused_reason 'gitleaks version' || true)"
  case "$gl_reason" in
    *"pinned gitleaks binary"*) ;;
    *)
      printf 'ci-parity --teeth: FAIL — T20: narrowing removed the gitleaks excuse itself; a real gitleaks install command is now UNCOVERED (got %q)\n' "$gl_reason" >&2
      teeth_fail=1 ;;
  esac
  for foreign in \
    'curl -fsSL https://example.invalid/some-other-tool.tgz -o t.tgz' \
    'tar -xzf some-other-tool.tgz' \
    'sha256sum --check --strict other.txt'
  do
    foreign_reason="$(excused_reason "$foreign" || true)"
    case "$foreign_reason" in
      *"pinned gitleaks binary"*)
        printf 'ci-parity --teeth: FAIL — T20: %q is excused as a gitleaks install. An excuse that matches a command it does not name is how a step nothing runs reports as covered.\n' "$foreign" >&2
        teeth_fail=1 ;;
    esac
  done

  # --- T21: A COMMENT IS NOT COVERAGE. -----------------------------------
  #
  # Coverage is a substring grep against the EXECUTES block, and that block is
  # 220 lines of which 138 are prose. A command merely NAMED in a comment
  # registered as run. The haystack must therefore carry no comment lines —
  # and must still carry the real ones, or the tooth would pass against an
  # empty string.
  local haystack
  haystack="$(executes_haystack)"
  if [ -z "$haystack" ]; then
    printf 'ci-parity --teeth: FAIL — T21: the coverage haystack is EMPTY; every command would read as uncovered\n' >&2
    teeth_fail=1
  fi
  if printf '%s\n' "$haystack" | grep -qE '^[[:space:]]*#'; then
    printf 'ci-parity --teeth: FAIL — T21: the coverage haystack still contains comment lines; a command named in prose counts as executed\n' >&2
    teeth_fail=1
  fi
  if ! printf '%s\n' "$haystack" | grep -q 'run_step'; then
    printf 'ci-parity --teeth: FAIL — T21: the coverage haystack carries no run_step line; stripping comments removed the claim itself\n' >&2
    teeth_fail=1
  fi


  # ── T22-T25 — PHASE 0, THE FAST STATIC PASS (release-cost-2026-08 P1) ──────
  #
  # A fixture root exists so these can be watched failing without breaking the
  # real repository. Logic comes from THIS checkout; only the content is
  # fabricated — a tooth that ran a copy of the gate it wrote itself would
  # prove nothing about what a release does.
  local fpd
  fpd="$(mktemp -d "${TMPDIR:-/tmp}/ci-parity-fastpass.XXXXXX")"
  mkdir -p "$fpd/releasenotes" "$fpd/.github/workflows" "$fpd/space-template"
  printf 'on:\n  workflow_call:\n    inputs:\n      a2a-ref:\n        type: string\n        default: "v0.99.0"\n' \
    >"$fpd/.github/workflows/a2a-validate-reusable.yml"
  printf 'min_binary_version: "0.98.0"\n' >"$fpd/space-template/space.yaml"

  # T22 — THE SENTINEL IS REFUSED BEFORE ANYTHING EXPENSIVE STARTS, and this is
  # the exact tree that cost a whole composed run on 2026-08-22: notes authored
  # ahead of the tag, the provisional `released: unreleased` never corrected at
  # step 1, discovered by release-preflight at step 6 after the container had
  # already judged 24 minutes' worth of a tree that could not ship.
  # The refusal must NAME THE RUNBOOK STEP (AC-2) and must say the container
  # was never started — an operator reading "FAIL" alone cannot tell whether
  # the expensive half ran.
  local out22 rc22=0
  printf 'schema: release-notes/v1\nversion: "0.99.0"\nreleased: unreleased\nheadline: fixture\n' \
    >"$fpd/releasenotes/0.99.0.yaml"
  out22="$(bash "$ROOT/scripts/ci-parity.sh" --fast-pass "$fpd" 2>&1)" || rc22=$?
  if [ "$rc22" -eq 0 ] \
    || ! printf '%s\n' "$out22" | grep -qF "released: unreleased" \
    || ! printf '%s\n' "$out22" | grep -qF "docs/runbooks/release.md Phase 1 step 1" \
    || ! printf '%s\n' "$out22" | grep -qiF "the container was never started"; then
    printf 'ci-parity --teeth: FAIL — T22: a tree carrying the pre-tag sentinel must be refused by the fast pass, naming the runbook step and saying the container never ran; got rc=%s\n%s\n' "$rc22" "$out22" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T22 — the pre-tag sentinel is refused in phase 0, naming runbook step 1, with the container never started\n'
  fi

  # T23 — AND T22 IS NOT VACUOUS. The same fixture with a real, quoted date
  # gets PAST the three static assertions. Without this, "refused" could mean
  # "any fixture is refused" and the sentinel would have nothing to do with it.
  local out23 rc23=0
  printf 'schema: release-notes/v1\nversion: "0.99.0"\nreleased: "2026-08-23"\nheadline: fixture\n' \
    >"$fpd/releasenotes/0.99.0.yaml"
  out23="$(bash "$ROOT/scripts/ci-parity.sh" --fast-pass "$fpd" 2>&1)" || rc23=$?
  if printf '%s\n' "$out23" | grep -qF "released: unreleased" \
    || ! printf '%s\n' "$out23" | grep -qF "the three static release-preflight assertions OK"; then
    printf 'ci-parity --teeth: FAIL — T23: a dated fixture must pass the three static assertions, or T22 proved nothing about the sentinel; got rc=%s\n%s\n' "$rc23" "$out23" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T23 — the same fixture with a real date passes the static trio, so T22 is about the sentinel and not about being a fixture\n'
  fi

  # T24 — THE VERSION BEING CUT IS DERIVED BY SEMVER, NOT BY STRING ORDER.
  # This repository has already paid for that distinction once, at v0.19.10:
  # a lookup that sorted lexically read `0.19.9` as newer than `0.19.10`. The
  # fast pass names the version in every message it prints and asks every
  # member about it, so getting this wrong would judge the wrong release while
  # looking entirely healthy.
  local out24 got24
  printf 'schema: release-notes/v1\nversion: "0.99.9"\nreleased: unreleased\nheadline: fixture\n' \
    >"$fpd/releasenotes/0.99.9.yaml"
  printf 'schema: release-notes/v1\nversion: "0.99.10"\nreleased: unreleased\nheadline: fixture\n' \
    >"$fpd/releasenotes/0.99.10.yaml"
  out24="$(bash "$ROOT/scripts/ci-parity.sh" --fast-pass "$fpd" 2>&1)" || true
  got24="$(printf '%s\n' "$out24" | sed -n 's/.*the cut commit for v\([0-9.]*\).*/\1/p' | head -1)"
  if [ "$got24" != "0.99.10" ]; then
    printf 'ci-parity --teeth: FAIL — T24: the version being cut must be the highest by semver; wanted 0.99.10, got %s\n%s\n' "${got24:-<none>}" "$out24" >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T24 — the version being cut is derived by semver order, so 0.99.10 outranks 0.99.9\n'
  fi
  rm -rf "$fpd"

  # T25 — THE PASS CANNOT TURN A RED VERDICT GREEN, AND THIS IS THE TOOTH THAT
  # SAYS SO (spec 01 AC-4, epic AC-6).
  #
  # Spec §6 asked for a tooth proving the fast pass is "a PREFIX, never a
  # substitute". That framing does not survive contact with the ground and the
  # honest tooth is a different one — see the spec's §11 amendment. The pass is
  # a SUPERSET of the container on three axes (the release-preflight trio,
  # which the container never runs) and a subset on the rest, so containment
  # cannot be asserted in either direction.
  #
  # What IS assertable, and is the property that actually matters, is
  # structural: inside `release_gate`, `fast_pass` is called BEFORE the
  # container step, and its ONLY failure arm returns. It therefore has exactly
  # two outcomes — refuse, or fall through to phase 1 unchanged — and no path
  # by which it reaches `write_receipt` or makes this function succeed on its
  # own. Read out of the function's own source, so an edit that reorders the
  # phases or lets the refusal fall through reds here.
  #
  # THE DIRTY-TREE CHECK IS READ TOO, and it is not decoration. Two members —
  # `release-notes-freshness` and `check-release-record` — can be correctly red
  # on a tree whose notes file is authored but not yet committed, and the spec
  # rejected both as members for exactly that reason. They are members here
  # because that state cannot REACH this pass: `release_gate` refuses a dirty
  # tree two checks earlier, so by the time phase 0 runs, the working tree is
  # the tree the receipt will name. Move the dirty check below the fast pass
  # and the spec's original objection becomes true again — so this tooth reds.
  local body25 line_dirty line_fast line_container line_receipt
  body25="$(awk '/^release_gate\(\)/{f=1} f{print} f && /^}/{exit}' "$ROOT/scripts/ci-parity.sh")"
  line_dirty="$(printf '%s\n' "$body25" | grep -n 'the working tree has staged, worktree or untracked changes' | head -1 | cut -d: -f1)"
  line_fast="$(printf '%s\n' "$body25" | grep -n 'if ! fast_pass "\$ROOT"; then' | head -1 | cut -d: -f1)"
  line_container="$(printf '%s\n' "$body25" | grep -n 'release gate phase 1 — container suite' | head -1 | cut -d: -f1)"
  line_receipt="$(printf '%s\n' "$body25" | grep -n 'write_receipt' | head -1 | cut -d: -f1)"
  if [ -z "$line_dirty" ] || [ -z "$line_fast" ] || [ -z "$line_container" ] || [ -z "$line_receipt" ]; then
    printf 'ci-parity --teeth: FAIL — T25: release_gate no longer contains one of the four anchors this tooth reads (dirty=%s fast_pass=%s container=%s write_receipt=%s); the ordering guarantee is unproven\n' \
      "${line_dirty:-<none>}" "${line_fast:-<none>}" "${line_container:-<none>}" "${line_receipt:-<none>}" >&2
    teeth_fail=1
  elif [ "$line_dirty" -ge "$line_fast" ]; then
    printf 'ci-parity --teeth: FAIL — T25: the dirty-tree refusal must come BEFORE the fast pass, or two of its members judge a working tree the receipt will never name; got dirty at %s, fast_pass at %s\n' \
      "$line_dirty" "$line_fast" >&2
    teeth_fail=1
  elif [ "$line_fast" -ge "$line_container" ] || [ "$line_fast" -ge "$line_receipt" ]; then
    printf 'ci-parity --teeth: FAIL — T25: the fast pass must be dispatched BEFORE the container and before any receipt is written; got fast_pass at %s, container at %s, write_receipt at %s\n' \
      "$line_fast" "$line_container" "$line_receipt" >&2
    teeth_fail=1
  elif ! printf '%s\n' "$body25" | awk -v n="$line_fast" 'NR==n+1' | grep -qE '^[[:space:]]*return 1[[:space:]]*$'; then
    printf 'ci-parity --teeth: FAIL — T25: the fast pass'"'"'s failure arm must RETURN; anything else lets a refused tree fall through into the container and on to a receipt\n' >&2
    teeth_fail=1
  else
    printf 'ci-parity --teeth: T25 — dirty-tree refusal, then the fast pass, then the container: the pass judges the tree the receipt will name, and its refusal returns rather than falling through\n'
  fi
  if [ "$teeth_fail" -ne 0 ]; then
    printf 'ci-parity --teeth: FAIL\n' >&2
    exit 1
  fi

  printf 'ci-parity --teeth: 25 case(s) green.\n'
}

case "${1:---run}" in
  --audit) if [ -n "${2:-}" ]; then audit_composed "$2"; else audit_composed; fi ;;
  --audit-runbook) audit_runbook "${2:-$RELEASE_RUNBOOK}" ;;
  # --run — the whole of what this machine can run: the audit, the portable
  # suite, and the macOS delta. Correct and complete on darwin. NOT what the
  # container runs; scripts/ci-parity-docker.sh passes `--suite` explicitly for
  # exactly that reason, because a Linux image reaching the macOS phase would
  # refuse — rightly, and pointlessly.
  --run)   audit_composed && execute_all ;;
  # --suite — the userland-portable half, and the container's entry point.
  --suite) audit && suite_members ;;
  # --macos-delta — the host's only honest content, and the one part of CI that
  # nothing ran until 2026-08-21.
  --macos-delta) macos_delta ;;
  # --release — the composed ship gate: container-primary, host-delta, receipt.
  --release) release_gate "${2:-}" ;;
  # --fast-pass [root] — phase 0 alone, over any tree. The root argument is not
  # a convenience: it is the only way a tooth can watch this refuse without
  # breaking the real repository, and a gate nobody can watch fail is the shape
  # this whole file exists to remove.
  --fast-pass) fast_pass "${2:-$ROOT}" ;;
  --macos-jobs) macos_jobs "${2:-.github/workflows}" ;;
  --members) list_members ;;
  # --phases — read back what a run's phases actually were. The composition
  # calls telemetry_report itself; this is the same reading, on demand, for an
  # operator holding a transcript and a question about it.
  --phases) telemetry_report "${2:-0}" "${3:-this run}" "${4:-}" ;;
  --teeth) run_teeth ;;
  *) printf 'usage: %s [--run|--suite|--macos-delta|--release [--dry-run]|--fast-pass [root]|--audit [dir]|--audit-runbook [file]|--macos-jobs [dir]|--members|--teeth]\n' "$0" >&2; exit 2 ;;
esac
