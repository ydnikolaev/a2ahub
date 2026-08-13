#!/usr/bin/env bash
# check-runner-economics.sh — the next expensive CI job is refused by a gate,
# not caught by a chart. agent-ops-2026-07 P13 K3 (spec 13 §3.10, AC3, AC4).
#
# WHY THIS EXISTS INSTEAD OF LEANING ON actionlint (already wired at
# `make workflow-lint`). Measured against a seeded fixture on 2026-08-12 (spec
# 13 §5 rung 1): actionlint said NOTHING about a job with no path condition
# reachable on every push, and NOTHING about a job with no `timeout-minutes` —
# zero matches for "timeout". Those are exactly the two invariants this phase
# turns into rules. A linter that does not hold an invariant is not a reason
# to skip writing the gate that does.
#
# FOUR RULES, refused independently, over EVERY job in EVERY workflow file:
#
#   1. a job requests a NON-ubuntu runner (macos-*, windows-*, self-hosted,
#      …) and is reachable with no job-level path-condition `if:` and no
#      tag/release-only workflow trigger. §3.1's own decision names WHY this
#      is a job-level check and not `on.push.paths:`: a top-level path
#      filter skips the WHOLE workflow, `check` included, so a change to
#      `internal/` would merge ungated. This gate follows that shape rather
#      than re-litigating it — but the `if:` must actually BE a path
#      condition, not merely present: it must reference a `needs.` job
#      output, the exact mechanism §3.1/§3.2 mandate ("the macOS job is
#      gated by a cheap ubuntu `changes` job whose output it consumes via
#      `needs:` + `if:`"). `if: always()` on a macos-15 job reds — accepting
#      ANY `if:` would repeat, one layer down, the exact defect AC3 was
#      written to close (A5: "not left on the default passes if someone
#      writes 360 explicitly"). A workflow whose only trigger is a tag push
#      and/or `release:` separately counts as the tag/release condition,
#      independent of any job-level `if:` (release.yml's `macos-notifier`
#      carries none and must not red).
#
#      A DOCUMENTED, ACCEPTED GAP: `has_paths_filter` below is WORKFLOW-wide
#      — any `paths:` anywhere in a file's `on:` block exempts every non-
#      ubuntu job in that file from rule 1, even a job whose own reachable
#      trigger does not actually pass through that path-filtered event.
#      govulncheck.yml is a REAL instance of the shape, today, on an
#      otherwise-harmless ubuntu job: its `pull_request:` trigger carries a
#      `paths:` filter, but `schedule:` and `workflow_dispatch:` sit beside
#      it with none — so `hp=1` for that whole file, and a hypothetical
#      macos job added there under `schedule:` alone would be reachable on
#      every nightly run with no path condition at all, and this gate would
#      pass it. Modelling trigger-to-job reachability precisely would need
#      to parse which `on:` EVENT actually fires each job — no job in this
#      tree is non-ubuntu today outside `ci.yml`'s and `release.yml`'s
#      already-correctly-gated `macos-notifier` jobs, so nothing on disk is
#      wrongly passed by this gap yet. Left as a named limitation rather
#      than built out: doing so is real scope the brief for this gate did
#      not ask for.
#   2. any job omits `timeout-minutes` — EXCEPT a job that calls a reusable
#      workflow via `uses:` at the job level, because GitHub itself refuses
#      the key there. Verified empirically against this repo's own
#      a2a-validate-dogfood.yml, whose `a2a-validate` job carries the same
#      finding in its own comment: "when a reusable workflow is called with
#      \"uses\", \"timeout-minutes\" is not available." The bound for that
#      work lives on the CALLEE's own job instead
#      (a2a-validate-reusable.yml's `validate`), which this gate DOES check.
#   3. a job's `timeout-minutes` is below 1.5x its identity's measured p99 in
#      scripts/lib/job-timeouts.tsv (AC3's own bound — not the tsv header's
#      fuller `min(60, max(5, ceil(1.5p99), ceil(max)+2))` sizing formula,
#      which is how the numbers in that file's own `timeout_min` column were
#      chosen; AC3/AC4 name 1.5x p99 as the assertable floor and that is what
#      this gate checks against).
#   4. a job's `timeout-minutes` is above the 60-minute cap (spec 13 §3.6:
#      the bound on a FAILURE, not a performance target — at the macOS x10
#      multiplier a capped job bills 600 equivalent minutes, survivable
#      inside the 1500-minute monthly budget; the 360-minute GitHub default
#      bills 3600, more than the whole month).
#
# AN IDENTITY WITH NO ROW IN job-timeouts.tsv — DECIDED, not left implicit.
# ci.yml's `changes` job is exactly this today: a brand-new identity,
# `timeout-minutes: 5`, no measured p99 yet. Rule 3 has nothing to compare
# against for such a job, and asserting a violation with no basis would be
# the SAME "print a number and call it a bound" defect A5 already caught one
# layer up (spec 13 §3.6) — a made-up floor is not a measured one. So an
# unmeasured identity PASSES rule 3 with a NAMED WARNING in the summary
# rather than a silent skip, matching `make lane`'s own "no estimate (n=k)"
# discipline (job-timeouts.tsv's own header, line ~20) rather than inventing
# a third behaviour. Rule 4 (the 60-minute cap) still applies to an
# unmeasured identity — it needs no measurement, only the declared value.
#
# THE .a2a PRUNE LESSON (docs/features/active/agent-ops-2026-07/specs/13-ci-
# economics.md, "items found outside this phase that belong to it"):
# `a2a`'s feedback reader clones the public repo into
# `.a2a/cache/feedback-repo/<slug>/`, and a gate that walks the repo ROOT
# with no prune finds a SECOND copy of every workflow file there and judges
# it too (check-pendency-uniqueness.sh reproduced exactly this). This gate
# does NOT walk the repo root at all, recursively or otherwise: GitHub Actions
# itself only ever reads workflows from the single, fixed, top-level
# `.github/workflows/` directory, so listing that directory directly
# (non-recursive, one level) both answers the right question AND can never
# descend into `.a2a/cache/**/.github/workflows/`. If a future wave needs a
# second workflow root (a submodule, a nested space checkout), that walk must
# prune `.git`, `.a2a` and `node_modules` explicitly, the same way
# check-provider-tier-deferral.sh's find_live_e2e_artifacts() and
# check-pendency-uniqueness.sh already do — it is not needed today because
# there is exactly one workflow directory this gate has any business judging.
#
# The declaration below and the `runner-economics` REPO_GATES entry that backs
# it landed in ONE commit, which is not tidiness: check-lane-declarations.sh's
# D-2 refuses the whole tree for a declaration whose phase nothing runs, and
# the mirror case — a phase with no declaration — refuses too. The wave that
# wrote this gate deliberately left the block out and said so, because its
# allowlist held the script and not the Makefile.

# lane-inputs:
#   .github/workflows/**
#   scripts/lib/job-timeouts.tsv
# lane-reason: it reads every job in every workflow and the measured p99
#   artefact those jobs are sized against, so a change to either side can flip
#   a verdict — and a timeout that silently stops matching its basis is the
#   exact "stale paragraph" failure the artefact exists to turn into a red
#   line.
# lane-reads-opaque: the workflow walk is `find "$wf_dir" -maxdepth 1 -type f
#   -name '*.yml'`, where $wf_dir is the root seam --teeth rebinds to a fixture
#   tree (the same LANE_ROOT / FEATURES_DIR idiom check-lane-declarations.sh
#   and epic_docs_drift.sh use). The classifier resolves literals, not a
#   variable, so the read is declared above as `.github/workflows/**` — which
#   is what it resolves to in every non-fixture invocation.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

# The bound on the FAILURE (spec 13 §3.6), not a performance target.
CAP_MINUTES=60

# extract_jobs <workflow-file>
#
# Emits one line per job in the file, fields separated by octal 037 (ASCII
# Unit Separator — see emit()'s own comment for why not a tab):
#   workflow_name  job_id  runs_on  has_if  has_timeout  timeout_minutes
#   has_uses  tag_release_only  has_paths_filter
# `has_if` is 1 only when the job's `if:` references a `needs.` job output —
# the path-condition mechanism spec 13 §3.1/§3.2 actually mandate — not
# merely present (an `if: always()` on a non-ubuntu runner must still red).
#
# A hand-rolled state machine over 2-space-indented GitHub Actions YAML —
# every workflow in this tree is written that way, verified by reading all
# ten before writing this — NOT a general YAML parser. Its one documented
# blind spot: a `run: |` block scalar whose OWN content happened to start at
# EXACTLY column 2 with a `word:` pattern would be misread as a new job
# boundary. Every `run:` block in this tree is indented at column 8 or
# deeper (steps are already at column 6), so the blind spot is real but does
# not fire against anything on disk today; a future job whose script content
# reaches column 2 would need this comment updated, not silently trusted.
extract_jobs() {
  local file="$1"
  awk '
    function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
    function strip_comment(s) { sub(/[ \t]+#.*$/, "", s); return s }

    # A workflow whose ONLY reachable trigger is a tag push and/or `release:`
    # satisfies the "tag/release trigger" half of rule 1 for every job in it,
    # with no per-job `if:` required — the `macos-notifier` job in
    # release.yml is exactly this shape (`on: push: tags: …`, no branches,
    # no pull_request, no schedule, no job-level `if:` at all) and must NOT
    # red.
    function tag_release_only(block,    n, i, lines, key, in_push, other) {
      n = split(block, lines, "\n")
      in_push = 0; other = 0; push_seen = 0; push_branches = 0; push_tags = 0
      for (i = 1; i <= n; i++) {
        if (lines[i] ~ /^  [A-Za-z_]+:/) {
          in_push = 0
          key = lines[i]; sub(/^  /, "", key); sub(/:.*/, "", key)
          if (key == "push") { push_seen = 1; in_push = 1 }
          else if (key == "release") { }
          else { other = 1 }
        } else if (in_push && lines[i] ~ /^    branches:/) { push_branches = 1 }
        else if (in_push && lines[i] ~ /^    tags:/) { push_tags = 1 }
      }
      if (other) return 0
      if (!push_seen) return 1
      if (push_branches) return 0
      if (push_tags) return 1
      return 0
    }

    function has_paths_filter(block,    n, i, lines) {
      n = split(block, lines, "\n")
      for (i = 1; i <= n; i++) if (lines[i] ~ /^ +paths:/) return 1
      return 0
    }

    # Field separator is OCTAL 037 (ASCII Unit Separator), NOT a tab. `read`
    # treats every character in $IFS that is space/tab/newline as "IFS
    # whitespace" and COLLAPSES runs of it, silently eating the empty fields
    # this row legitimately carries (an unset `runs-on:` or `timeout-
    # minutes:` on a `uses:` job) and shifting every field after it — caught
    # by running this gate against the real repo and watching
    # a2a-validate-dogfood.yml, whose `uses:` job has exactly that empty
    # runs_on/timeout_val pair, read has_uses as "0" instead of "1". Octal
    # 037 is not IFS whitespace, so the loop that reads this output with
    # `read -r ... < <(...)`, IFS set to that one character, preserves empty
    # fields positionally.
    function emit() {
      printf "%s\037%s\037%s\037%d\037%d\037%s\037%d\037%d\037%d\n", \
        wfname, job, runs_on, has_if, has_timeout, timeout_val, has_uses, \
        tag_release_only(on_block), has_paths_filter(on_block)
    }

    BEGIN { wfname = ""; in_on = 0; in_jobs = 0; job = ""; on_block = "" }

    !wfname && /^name:[ \t]*/ {
      wfname = $0; sub(/^name:[ \t]*/, "", wfname)
      wfname = trim(strip_comment(wfname))
      next
    }

    /^on:/ { in_on = 1; next }
    in_on && /^[^ \t#]/ { in_on = 0 }
    in_on { on_block = on_block "\n" $0; next }

    /^jobs:/ { in_jobs = 1; next }

    in_jobs && /^  [A-Za-z0-9_.-]+:[ \t]*$/ {
      if (job != "") emit()
      job = $0; sub(/^  /, "", job); sub(/:.*$/, "", job)
      runs_on = ""; has_if = 0; has_timeout = 0; timeout_val = ""; has_uses = 0
      next
    }

    in_jobs && job != "" && /^    runs-on:/ {
      runs_on = $0; sub(/^    runs-on:[ \t]*/, "", runs_on)
      runs_on = trim(strip_comment(runs_on)); gsub(/"/, "", runs_on)
      next
    }
    in_jobs && job != "" && /^    if:/ {
      # A path condition, for RULE 1, is specifically an `if:` that
      # CONSUMES a `needs.` job output — the mechanism spec 13 §3.1/§3.2
      # actually mandate ("the macOS job is gated by a cheap ubuntu
      # `changes` job whose output it consumes via `needs:` + `if:`"). An
      # `if:` that names something else (`always()`, a bare
      # `github.event_name` comparison with no `needs.` reference) is NOT a
      # path condition and must NOT satisfy rule 1 — the exact "a print
      # cannot red, and not-the-default passes if someone writes something
      # else" shape AC3 was written to close one layer up (A5), reproduced
      # here if any `if:` were accepted.
      ifval = $0; sub(/^    if:[ \t]*/, "", ifval)
      if (ifval ~ /needs\./) has_if = 1
      next
    }
    in_jobs && job != "" && /^    timeout-minutes:/ {
      has_timeout = 1
      timeout_val = $0; sub(/^    timeout-minutes:[ \t]*/, "", timeout_val)
      timeout_val = trim(strip_comment(timeout_val))
      next
    }
    in_jobs && job != "" && /^    uses:/ { has_uses = 1; next }

    END { if (job != "") emit() }
  ' "$file"
}

# p99_for_identity <tsv-file> <identity>
#
# Echoes the measured p99 in SECONDS for "<workflow name>/<job id>", or
# nothing if the artefact has no row for it. Column shape from the tsv's own
# header (line ~47): identity, p50_s, p99_s, max_s, n, timeout_min — a plain
# linear scan, not an associative array, since `#!/usr/bin/env bash` on this
# machine can resolve to either the system bash (3.2, no `declare -A`) or a
# newer one on PATH, and the file is small enough that the scan cost is noise.
p99_for_identity() {
  local tsv="$1" identity="$2"
  [ -f "$tsv" ] || return 0
  awk -F'\t' -v id="$identity" '$0 !~ /^#/ && NF >= 3 && $1 == id { print $3; exit }' "$tsv"
}

# check_runner_economics [target-root]
#
# Walks TARGET/.github/workflows (non-recursive — see the .a2a prune-lesson
# header comment) and asserts all four rules against every job it finds.
check_runner_economics() {
  local target="${1:-$ROOT}"
  local wf_dir="$target/.github/workflows"
  local tsv="$target/scripts/lib/job-timeouts.tsv"
  local -a violations=()
  local -a warnings=()
  local file wfname job runs_on has_if has_timeout timeout_val has_uses tro hp
  local identity p99 required_min

  if [ ! -d "$wf_dir" ]; then
    echo "runner-economics: ok — no .github/workflows directory under $target"
    return 0
  fi

  # A `while read` loop fed by a sorted walk, rather than a `for` loop over an
  # unquoted command substitution — the word-splitting idiom this repo's other
  # gates already use (check-provider-tier-deferral.sh's
  # find_deferral_records() / find_live_e2e_artifacts() consumers), so a
  # workflow filename is never subject to `$IFS` word-splitting or glob
  # expansion on the way in.
  #
  # The `for` alternative is DESCRIBED rather than written out here on
  # purpose: an earlier revision spelled it as a literal example, and the lane
  # classifier parsed the example inside the comment as a real read of an
  # unresolvable path, refusing the whole tree. A gate's prose is parsed by
  # the same tools as its code.
  while IFS= read -r file; do
    [ -n "$file" ] || continue
    # IFS is octal 037 (Unit Separator), matching extract_jobs()'s emit() —
    # NOT a tab. Tab is "IFS whitespace" to `read`, which collapses runs of
    # it and silently eats an empty field (see emit()'s own comment for the
    # exact real-repo row that caught this).
    while IFS=$'\037' read -r wfname job runs_on has_if has_timeout timeout_val has_uses tro hp; do
      [ -n "$job" ] || continue
      if [ -z "$wfname" ]; then
        wfname="$(basename "$file" .yml)"
      fi
      identity="${wfname}/${job}"

      # RULE 1 — a non-ubuntu runner needs a path condition or a tag/release
      # trigger. Jobs that call a reusable workflow (`uses:`) have no runner
      # of their own and are skipped; so is any job actually on `ubuntu*`.
      if [ "$has_uses" -eq 0 ] && [ -n "$runs_on" ]; then
        case "$runs_on" in
          ubuntu*|Ubuntu*|UBUNTU*) : ;;
          *)
            if [ "$has_if" -eq 0 ] && [ "$tro" -eq 0 ] && [ "$hp" -eq 0 ]; then
              violations+=("$identity: runs on '$runs_on' with no job-level 'if:' and the workflow is not restricted to a tag/release trigger (RULE 1, spec 13 §3.10/AC4)")
            fi
            ;;
        esac
      fi

      if [ "$has_uses" -eq 1 ]; then
        # A reusable-workflow CALLER job: GitHub refuses timeout-minutes here
        # (verified against this repo's own a2a-validate-dogfood.yml). The
        # bound lives on the callee's own job, checked separately when this
        # loop reaches that file.
        continue
      fi

      # RULE 2 — every remaining job must declare timeout-minutes.
      if [ "$has_timeout" -eq 0 ]; then
        violations+=("$identity: no 'timeout-minutes' declared (RULE 2, spec 13 AC3/AC4)")
        continue
      fi

      if ! [[ "$timeout_val" =~ ^[0-9]+$ ]]; then
        violations+=("$identity: timeout-minutes '$timeout_val' is not a plain integer this gate can check — write a literal number, not an expression (RULE 2/3, spec 13 AC3)")
        continue
      fi

      # RULE 4 — the cap, independent of any measurement.
      if [ "$timeout_val" -gt "$CAP_MINUTES" ]; then
        violations+=("$identity: timeout-minutes=$timeout_val exceeds the ${CAP_MINUTES}-minute cap (RULE 4, spec 13 §3.6/AC4)")
      fi

      # RULE 3 — below 1.5x measured p99, ONLY when a measurement exists.
      p99="$(p99_for_identity "$tsv" "$identity")"
      if [ -n "$p99" ]; then
        # Integer form of "timeout_min >= 1.5 * p99_s / 60", exact:
        #   timeout_min * 60 * 2 >= p99_s * 3
        if [ $(( timeout_val * 60 * 2 )) -lt $(( p99 * 3 )) ]; then
          required_min=$(( (p99 * 3 + 119) / 120 ))
          violations+=("$identity: timeout-minutes=$timeout_val is below 1.5x its measured p99 of ${p99}s in $tsv — needs >= ${required_min}m (RULE 3, spec 13 §3.6/AC3/AC4)")
        fi
      else
        warnings+=("$identity: no row in $tsv for this identity — RULE 3 (1.5x p99) cannot be asserted with no measurement and is PASSED, not guessed (spec 13 §3.6's own 'no estimate' discipline)")
      fi
    done < <(extract_jobs "$file")
  done < <(find "$wf_dir" -maxdepth 1 -type f -name '*.yml' 2>/dev/null | sort)

  if [ "${#violations[@]}" -gt 0 ]; then
    echo "runner-economics: FAIL — ${#violations[@]} job(s) violate the CI cost gate (spec 13 §3.10/AC4):" >&2
    local v
    for v in "${violations[@]}"; do
      echo "  - $v" >&2
    done
    if [ "${#warnings[@]}" -gt 0 ]; then
      echo "" >&2
      local w
      for w in "${warnings[@]}"; do
        echo "  (unrelated, not a failure) $w" >&2
      done
    fi
    return 1
  fi

  if [ "${#warnings[@]}" -gt 0 ]; then
    echo "runner-economics: ok — 0 violation(s) over $wf_dir; ${#warnings[@]} identity(ies) have no measured p99 yet, RULE 3 not asserted for them:"
    local w
    for w in "${warnings[@]}"; do
      echo "  - $w"
    done
    return 0
  fi

  echo "runner-economics: ok — every job under $wf_dir passes all four rules (non-ubuntu gating, timeout-minutes present, >= 1.5x measured p99, under the ${CAP_MINUTES}-minute cap)."
}

# --- --teeth ---------------------------------------------------------------
#
# AC4's own words: "four fixture workflows: an ungated macos-* job, a
# timeout-less job, an under-p99 job and an over-cap job — must each RED, and
# the repaired fixtures must GREEN." Fixture content is generated INLINE with
# `cat`/`printf` into a `mktemp -d` tree, the same idiom
# check-provider-tier-deferral.sh uses for its own --teeth, and for the same
# reason: this gate's allowlist is this one file, so a fixture cannot live in
# a separate testdata/ tree the way check-lane-declarations.sh's does.
#
# A fifth, EXTRA case proves the no-row-in-job-timeouts.tsv decision this
# gate's header states explicitly (the `changes` job's real shape): it is not
# one of AC4's required four, but the brief that produced this gate demanded
# the decision be "handled explicitly and stated in the output", and a
# decision that is only ever asserted in a comment is exactly the "a print
# cannot red" defect this whole phase exists to end one level up.
teeth() {
  # NOT `local`: the cleanup trap fires at PROCESS exit, after this function
  # has already returned — the same reasoning check-provider-tier-deferral.sh
  # states for its own tmp1..tmp7 (its "Case 5, 5b and 6" comment).
  tmpA="" tmpB="" tmpC="" tmpD="" tmpE=""
  trap 'rm -rf "${tmpA:-}" "${tmpB:-}" "${tmpC:-}" "${tmpD:-}" "${tmpE:-}"' EXIT

  expect_red() {
    local dir="$1" needle="$2" label="$3" out rc
    set +e
    out="$(check_runner_economics "$dir" 2>&1)"
    rc=$?
    set -e
    if [ "$rc" -eq 0 ]; then
      echo "runner-economics --teeth: FAIL — $label stayed green" >&2
      echo "$out" >&2
      exit 1
    fi
    if ! grep -Fq "$needle" <<<"$out"; then
      echo "runner-economics --teeth: FAIL — $label red did not name '$needle':" >&2
      echo "$out" >&2
      exit 1
    fi
    echo "runner-economics --teeth: $label — reds, naming '$needle'"
  }

  expect_green() {
    local dir="$1" label="$2" out rc
    set +e
    out="$(check_runner_economics "$dir" 2>&1)"
    rc=$?
    set -e
    if [ "$rc" -ne 0 ]; then
      echo "runner-economics --teeth: FAIL — $label (repaired) stayed red:" >&2
      echo "$out" >&2
      exit 1
    fi
    echo "runner-economics --teeth: $label (repaired) — greens"
  }

  # --- Case A: an UNGATED macos-* job -> RED (RULE 1) -----------------------
  tmpA="$(mktemp -d)"
  mkdir -p "$tmpA/.github/workflows"
  cat >"$tmpA/.github/workflows/fixture.yml" <<'EOF'
name: Fixture
on:
  push:
    branches: [main]
  pull_request:
jobs:
  build-mac:
    timeout-minutes: 5
    runs-on: macos-latest
    steps:
      - run: echo hi
EOF
  expect_red "$tmpA" "Fixture/build-mac: runs on 'macos-latest' with no job-level 'if:'" "case A ungated macos-* job"

  # An `if:` that does NOT reference a `needs.` job output is not a path
  # condition and must still RED — accepting any `if:` at all would be the
  # A5 defect ("not left on the default passes if someone writes 360
  # explicitly") one layer down, applied to rule 1 instead of AC3's bound.
  cat >"$tmpA/.github/workflows/fixture.yml" <<'EOF'
name: Fixture
on:
  push:
    branches: [main]
  pull_request:
jobs:
  build-mac:
    timeout-minutes: 5
    if: always()
    runs-on: macos-latest
    steps:
      - run: echo hi
EOF
  expect_red "$tmpA" "Fixture/build-mac: runs on 'macos-latest' with no job-level 'if:'" "case A2 macos job with a non-path 'if: always()'"

  cat >"$tmpA/.github/workflows/fixture.yml" <<'EOF'
name: Fixture
on:
  push:
    branches: [main]
  pull_request:
jobs:
  build-mac:
    timeout-minutes: 5
    if: needs.changes.outputs.macos == 'true'
    runs-on: macos-latest
    steps:
      - run: echo hi
EOF
  expect_green "$tmpA" "case A ungated macos-* job"

  # --- Case B: a job with NO timeout-minutes -> RED (RULE 2) ---------------
  tmpB="$(mktemp -d)"
  mkdir -p "$tmpB/.github/workflows"
  cat >"$tmpB/.github/workflows/fixture.yml" <<'EOF'
name: Fixture
on:
  push:
    branches: [main]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
EOF
  expect_red "$tmpB" "Fixture/build: no 'timeout-minutes' declared" "case B timeout-less job"

  cat >"$tmpB/.github/workflows/fixture.yml" <<'EOF'
name: Fixture
on:
  push:
    branches: [main]
jobs:
  build:
    timeout-minutes: 10
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
EOF
  expect_green "$tmpB" "case B timeout-less job"

  # --- Case C: a job UNDER 1.5x its measured p99 -> RED (RULE 3) -----------
  # p99=600s -> the floor is 1.5*600/60 = 15m; 5m is under it.
  tmpC="$(mktemp -d)"
  mkdir -p "$tmpC/.github/workflows" "$tmpC/scripts/lib"
  printf '# identity\tp50_s\tp99_s\tmax_s\tn\ttimeout_min\nFixture/build\t10\t600\t650\t20\t15\n' \
    >"$tmpC/scripts/lib/job-timeouts.tsv"
  cat >"$tmpC/.github/workflows/fixture.yml" <<'EOF'
name: Fixture
on:
  push:
    branches: [main]
jobs:
  build:
    timeout-minutes: 5
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
EOF
  expect_red "$tmpC" "Fixture/build: timeout-minutes=5 is below 1.5x its measured p99 of 600s" "case C under-p99 job"

  cat >"$tmpC/.github/workflows/fixture.yml" <<'EOF'
name: Fixture
on:
  push:
    branches: [main]
jobs:
  build:
    timeout-minutes: 15
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
EOF
  expect_green "$tmpC" "case C under-p99 job"

  # --- Case D: a job ABOVE the 60-minute cap -> RED (RULE 4) ---------------
  tmpD="$(mktemp -d)"
  mkdir -p "$tmpD/.github/workflows"
  cat >"$tmpD/.github/workflows/fixture.yml" <<'EOF'
name: Fixture
on:
  push:
    branches: [main]
jobs:
  build:
    timeout-minutes: 90
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
EOF
  expect_red "$tmpD" "Fixture/build: timeout-minutes=90 exceeds the 60-minute cap" "case D over-cap job"

  cat >"$tmpD/.github/workflows/fixture.yml" <<'EOF'
name: Fixture
on:
  push:
    branches: [main]
jobs:
  build:
    timeout-minutes: 60
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
EOF
  expect_green "$tmpD" "case D over-cap job"

  # --- Case E (extra, not one of AC4's four): an identity with NO row in
  # job-timeouts.tsv -> GREEN with a named warning, never a silent skip and
  # never a guessed RED. This is ci.yml's real `changes` job shape.
  tmpE="$(mktemp -d)"
  mkdir -p "$tmpE/.github/workflows" "$tmpE/scripts/lib"
  printf '# identity\tp50_s\tp99_s\tmax_s\tn\ttimeout_min\n' >"$tmpE/scripts/lib/job-timeouts.tsv"
  cat >"$tmpE/.github/workflows/fixture.yml" <<'EOF'
name: Fixture
on:
  push:
    branches: [main]
jobs:
  changes:
    timeout-minutes: 5
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
EOF
  local out rc
  set +e
  out="$(check_runner_economics "$tmpE" 2>&1)"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ]; then
    echo "runner-economics --teeth: FAIL — case E (no measurement) should PASS with a warning, not red:" >&2
    echo "$out" >&2
    exit 1
  fi
  if ! grep -Fq "Fixture/changes: no row in" <<<"$out"; then
    echo "runner-economics --teeth: FAIL — case E green did not name the unmeasured identity's warning:" >&2
    echo "$out" >&2
    exit 1
  fi
  echo "runner-economics --teeth: case E unmeasured identity — greens with a named warning, not a guessed red"

  echo "runner-economics --teeth: PASS — (A) ungated macos-* reds and the job-level 'if:' repair greens; (B) a timeout-less job reds and adding timeout-minutes greens; (C) an under-1.5x-p99 timeout reds and raising it to the floor greens; (D) an over-60-minute timeout reds and capping it at 60 greens; (E) an identity absent from job-timeouts.tsv greens with a named warning rather than a guessed bound."
}

if [ "${1:-}" = "--teeth" ]; then teeth; exit 0; fi
check_runner_economics "$ROOT"
