#!/usr/bin/env bash
# ci-parity — run what CI runs, locally, and refuse when the two lists drift.
#
# lane-inputs: ALWAYS
# lane-reason: it reads every .github/workflows/*.yml and compares them against
#   its own execution list; any workflow edit can change its verdict, and it
#   claims no path of its own.
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

    # --- P1 close, 2026-08-21: the lead's triage of the remaining 73. ------

    # Group 3 — release.yml, tag-triggered release machinery (~20). Runs only
    # on a version tag, against a real GitHub release — goreleaser build,
    # cosign signing and asset upload need a tag and credentials that do not
    # exist locally. What proves this path is the release runbook's own
    # post-tag verification, not a local re-run.
    *": > dist/SHA256SUMS"* \
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

    # Group 5 — macos-notifier and its toolchain (ci.yml's macos-15 job AND
    # release.yml's macos-15 job, ~5). NAMED GAP: the macos-15 job builds and
    # packages a Swift notifier; it needs the macOS toolchain and signing
    # context. NOTHING local or containerised covers these two jobs — this is
    # a known, named gap (release-loop-2026-08 P4), not a claim of coverage.
    *"swift --version"* \
    | *"integrations/macos-notifier/scripts/test.sh"* \
    | *"integrations/macos-notifier/scripts/package-release.sh"*)
      echo "the macos-15 job builds and packages a Swift notifier; it needs the macOS toolchain and signing context. NOTHING local or containerised covers these two jobs — this is a known, named gap (release-loop-2026-08 P4), not a claim of coverage." ;;

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
    | *'sleep $((attempt * 5))'*)
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
  executes="$(sed -n '/^# EXECUTES_BEGIN/,/^# EXECUTES_END/p' "$ROOT/scripts/ci-parity.sh")"
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
  run_step "gitleaks dir"                              gitleaks dir --config .gitleaks.toml --redact --verbose .
  run_step "gitleaks git"                              gitleaks git --config .gitleaks.toml --redact --verbose .
  run_step "a2a-validate-reusable.yml validate --ci (a2ahub dogfood: a2a-ref=\"\", mode=v3-full-repo)" \
    a2a_validate_dogfood_step
}
# EXECUTES_END

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

  if [ "$teeth_fail" -ne 0 ]; then
    printf 'ci-parity --teeth: FAIL\n' >&2
    exit 1
  fi
  printf 'ci-parity --teeth: 10 case(s) green.\n'
}

case "${1:---run}" in
  --audit) if [ -n "${2:-}" ]; then audit "$2"; else audit; fi ;;
  --run)   audit && execute_all ;;
  --teeth) run_teeth ;;
  *) printf 'usage: %s [--run|--audit [dir]|--teeth]\n' "$0" >&2; exit 2 ;;
esac
