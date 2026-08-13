#!/usr/bin/env bash
# ci-usage-report.sh — turns the ad-hoc measurement behind
# docs/features/active/agent-ops-2026-07/audits/ci-economics-2026-08-12.md
# into a committed command (agent-ops-2026-07 P13 §3.9, wave K8).
#
# WHY THIS EXISTS. The audit's numbers — 4769 billed-equivalent minutes,
# 55.6% of it one macOS job, 33.7% of it per-job minute rounding — came from
# a hand-run query against the GitHub API. A saving nobody can re-measure is
# a claim, not a fact (spec §3.9): "CI got cheaper" has to be a re-run and a
# diff, the same discipline `make lane` already applies to its own medians
# (it prints "no estimate (n=k)" below three samples rather than a guessed
# number — this script applies the same rule to job-timeout sizing below).
#
# THREE MODES:
#   (default)  report equivalent-minute spend over a window: total, per
#              repository, per job identity, each identity's share, and the
#              per-job minute-rounding waste in ABSOLUTE minutes (AC14 — a
#              share is the wrong shape here: removing a long, efficient job
#              like macos-notifier RAISES the rounding-waste share even as
#              the bill falls, so a share would fail on a successful fix).
#   --p99      regenerate scripts/lib/job-timeouts.tsv — the artefact AC3's
#              gate reads (B1). A stale timeout must be a red line, not a
#              stale paragraph, and that requires this table to be
#              regenerable rather than archaeological.
#   --verify   AC12's replay: re-derive the pinned window's numbers from the
#              PERSISTED raw job join in scripts/lib/ci-usage-window.json —
#              never the network — and assert they still match the values
#              recorded there on the run that established them. A script
#              that hits the network to "verify" a historical claim is
#              answering a different, moving, question.
#   --billing  the account's billing-usage table for one month (default:
#              the current month), reproducing the shape of the spec's
#              Amendments table — repo / SKU / minutes / net.
#   --establish  (re-)fetch the window fresh and PERSIST it as the new
#              pinned raw join + established values that --verify replays.
#              Only the operator or a wave whose job is re-pinning the
#              window runs this; an ordinary report run never overwrites
#              the pin.
#
# THE METHOD NOTE THAT WILL BITE THE NEXT READER (audit §Method, spec §2.2,
# carried forward verbatim because the mistake is not obvious from the API
# docs): `GET /actions/runs/{id}/timing` is the DOCUMENTED endpoint for
# billable time, and it is unusable here — it returns "total_ms": 0 for
# every job in this account while reporting a non-zero run_duration_ms. Cost
# is computed from the JOBS endpoint's timestamps instead:
# `completed_at - started_at`, rounded UP to the whole minute per JOB
# (GitHub's own billing rule), times the runner factor read off the job's
# `labels` (ubuntu ×1, windows ×2, macos ×10). Do not "fix" this script to
# call /timing; it was tried first and produced 0.0 minutes for everything.
#
# JOB IDENTITY. "<workflow_name>/<job_name>", with ONE normalization: a
# Dependabot-triggered run (`event: "dynamic"`, `path:
# "dynamic/dependabot/…"`) reports `workflow_name` as the one-off PR/graph
# title ("npm_and_yarn in /web for js-yaml - Update #1512999040") — a string
# that never repeats and is not a job identity a YAML file's
# `timeout-minutes` could be sized against. Every such run collapses to the
# single identity "Dependabot", matching scripts/lib/job-timeouts.tsv's
# existing row. This was found empirically while building this script
# (`gh api .../actions/runs/{id}` on one of these → `event: "dynamic"`) and
# is recorded here so nobody rediscovers it by hand.
#
# THE WINDOW IS CLOSED, DELIBERATELY SHORT OF "TODAY" (AC12). The spec's
# own first cut used "..2026-08-12" and that was already false the day it
# was written: 2026-08-12 was the measurement day, the run population kept
# growing under an open upper bound, and a re-count found 910 runs against
# the audit's own 834. Every mode here defaults to the PINNED, closed
# window below; re-deriving after a wave lands (spec §3.6: "re-derive after
# the ceiling moves") means passing --since/--until explicitly for a new
# window, not moving the default.
#
# WHY THIS SCRIPT IS UNREACHABLE FROM `make check` / `make lane-run`. It
# calls the network (gh api against api.github.com). Per this wave's brief
# it is deliberately NOT wired into the Makefile, scripts/verify.sh, or a
# `lane-inputs:` header block — the Makefile is lead-reserved this wave, and
# (checked directly against internal/lane/declaration.go's
# loadScriptHeaders) any `lane-inputs:` block on a scripts/*.sh file that no
# REPO_GATES target invokes makes `lane.Load` refuse the ENTIRE tree, which
# would break check-lane-declarations.sh for every concurrent checkout, not
# just this one. Wiring — a Makefile target, and a
# scripts/lib/lane-ungated.txt row so the new tracked paths this wave adds
# do not read as an uncovered surface — is left to the lead.
#
# API READS COST NO ACTIONS MINUTES. Every network call here is a `gh api`
# GET against the REST API, not a workflow dispatch; this script cannot bill
# the thing it measures.
set -uo pipefail

# --- internal worker (recursive self-invocation) --------------------------
#
# Fetching jobs for ~800 runs one at a time is what made the original
# hand-run measurement slow to reproduce. Rather than ship a second script
# file under scripts/lib (outside this wave's allowlist) this file re-execs
# ITSELF as an `xargs -P` worker: `xargs` cannot call a shell function
# defined in the parent process (function definitions do not survive
# `export -f` across every platform's `xargs`/`sh` — verified empirically
# while building this: it works under a plain subshell and silently reports
# "command not found" once xargs forks through `/bin/sh -c`), but it CAN
# call an executable path, and this file is one. `_fetch-jobs REPO RUN_ID
# EVENT` is not a public mode; it is reached only from fetch_window below.
if [ "${1:-}" = "_fetch-jobs" ]; then
  _repo="${2:?}"; _run_id="${3:?}"; _event="${4:-}"
  gh api --paginate --slurp \
    "/repos/${_repo}/actions/runs/${_run_id}/jobs?per_page=100" 2>/dev/null |
    jq -c --arg repo "$_repo" --arg event "$_event" '
      .[].jobs[]
      | {
          repo: $repo,
          run_id,
          event: $event,
          workflow_name,
          job_name: .name,
          job_id: .id,
          started_at,
          completed_at,
          status,
          conclusion,
          labels
        }'
  exit 0
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SELF="$ROOT/scripts/$(basename "${BASH_SOURCE[0]}")"
LIB_DIR="$ROOT/scripts/lib"
TIMEOUTS_TSV="$LIB_DIR/job-timeouts.tsv"
WINDOW_JSON="$LIB_DIR/ci-usage-window.json"

# The pinned, closed window AC12 asserts against. See the header note above
# for why the upper bound is one day short of the audit's own and never
# "today".
DEFAULT_SINCE="2026-08-01"
DEFAULT_UNTIL="2026-08-11"
DEFAULT_REPOS=("ydnikolaev/a2ahub" "ydnikolaev/a2ahub-private")

usage() {
  cat <<'USAGE'
ci-usage-report.sh — CI equivalent-minute spend, re-derivable (P13 K8)

Usage:
  ci-usage-report.sh [--since YYYY-MM-DD --until YYYY-MM-DD] [--repo OWNER/NAME ...]
      Default report: total / per-repo / per-identity equivalent minutes,
      each identity's share, and the absolute rounding-waste minutes.
      Defaults to the pinned window 2026-08-01..2026-08-11 over
      ydnikolaev/a2ahub and ydnikolaev/a2ahub-private. Always hits the
      network (gh api reads; no Actions minutes are spent by this script).

  ci-usage-report.sh --p99 [--since ... --until ... --repo ...]
      Regenerate scripts/lib/job-timeouts.tsv from the (network-fetched)
      window — the artefact AC3's gate reads.

  ci-usage-report.sh --verify
      AC12's replay. No network. Recomputes the report from the persisted
      raw job join in scripts/lib/ci-usage-window.json and asserts it
      matches the values recorded there. Exit 0 = still matches.

  ci-usage-report.sh --establish [--since ... --until ... --repo ...]
      (Re-)fetch the window fresh and OVERWRITE
      scripts/lib/ci-usage-window.json with the new raw join + established
      values --verify will replay from now on. Only run this to move the
      pin (e.g. spec §3.6's "re-derive after the ceiling moves") — never as
      part of an ordinary report.

  ci-usage-report.sh --billing [YYYY-MM]
      Billing-usage table (repo / SKU / minutes / net), reproducing the
      shape of the spec's Amendments table. Defaults to the current UTC
      month. Requires the `user` billing scope (`gh auth refresh -h
      github.com -s user` if this 404s or 403s).

  -h, --help   this text
USAGE
}

die() { echo "ci-usage-report: $*" >&2; exit 1; }

require_cmds() {
  local c
  for c in gh jq; do
    command -v "$c" >/dev/null 2>&1 || die "'$c' is required and was not found on PATH"
  done
}

# --- jq program fragments --------------------------------------------------
#
# Shared across the report and --p99 computations so the definition of
# "identity", "factor" and "wall clock seconds" cannot drift between the two
# — a report that used one rule to price a job and a different rule to size
# its timeout would be internally inconsistent in a way nothing would catch.
#
# shellcheck disable=SC2016
# These three assignments (JQ_PRELUDE, JQ_REPORT, JQ_P99) are jq PROGRAM
# TEXT, deliberately single-quoted so bash never touches jq's own `$s`/`$n`/
# `$rank` variables and `.field` syntax — double-quoting would make bash try
# (and fail) to expand them as shell variables before jq ever sees the
# program. The one place a bash value needs to reach the program is done
# the correct way instead: `jq --arg name "$value"` at the call site.
JQ_PRELUDE='
def factor:
  (.labels // [] | map(ascii_downcase)) as $l
  | if ($l | any(test("macos"))) then 10
    elif ($l | any(test("windows"))) then 2
    else 1 end;

def wall:
  ((.completed_at | fromdateiso8601) - (.started_at | fromdateiso8601));

# "Dependabot" collapses every dynamic dependency-update/graph-update run
# (event == "dynamic") into one identity — see the header note on why their
# workflow_name is a one-off title, not a reusable job identity.
def ident:
  if .event == "dynamic" then "Dependabot"
  else (.workflow_name + "/" + .job_name) end;

def completed_jobs:
  [ .[]
    | select(.status == "completed" and .started_at != null and .completed_at != null)
    | . + {wall: wall, factor: factor, identity: ident}
    | . + {billed: (((.wall/60)|ceil) * .factor), work: ((.wall/60) * .factor)}
  ];
'

# compute_report_json JOBS_NDJSON_FILE — the pure aggregation. Called both
# by a live fetch (default / --establish) and by --verify against the
# persisted raw join, so the SAME code path proves both "what is the spend"
# and "did the spend computation stay honest over time".
# shellcheck disable=SC2016 # jq program text — see JQ_PRELUDE's note above.
JQ_REPORT="$JQ_PRELUDE"'
completed_jobs as $jobs
| ($jobs | map(.billed) | add // 0) as $total_billed
| ($jobs | map(.work) | add // 0) as $total_work
| {
    n_jobs: ($jobs | length),
    total_equiv_minutes: $total_billed,
    total_work_minutes: $total_work,
    rounding_waste_minutes: ($total_billed - $total_work),
    per_repo: (
      $jobs | group_by(.repo)
      | map({
          repo: .[0].repo,
          n: length,
          equiv_minutes: (map(.billed) | add),
          work_minutes: (map(.work) | add)
        })
      | sort_by(-.equiv_minutes)
    ),
    per_identity: (
      $jobs | group_by(.identity)
      | map({
          identity: .[0].identity,
          n: length,
          equiv_minutes: (map(.billed) | add),
          work_minutes: (map(.work) | add),
          rounding_waste_minutes: ((map(.billed) | add) - (map(.work) | add)),
          share_pct: (if $total_billed > 0 then (map(.billed) | add) / $total_billed * 100 else 0 end)
        })
      | sort_by(-.equiv_minutes)
    )
  }
'

# compute_p99_json JOBS_NDJSON_FILE — one row per job identity, THE SIZING
# RULE lifted verbatim from scripts/lib/job-timeouts.tsv's own header
# (P13 §3.6, B1): the max term is load-bearing, not decorative — a p99-only
# rule sized Pages/deploy at 26 minutes and would have cancelled an observed
# 1768 s SUCCESSFUL run. Identities with fewer than three samples are
# dropped rather than guessed, matching `make lane`'s own "no estimate
# (n=k)" discipline.
# shellcheck disable=SC2016 # jq program text — see JQ_PRELUDE's note above.
JQ_P99="$JQ_PRELUDE"'
def pctl(p):
  sort as $s | ($s | length) as $n
  | (($n * p / 100) | ceil) as $rank
  | $s[ ([$rank - 1, 0] | max) as $i | ([$i, $n - 1] | min) ];

completed_jobs
| group_by(.identity)
| map({identity: .[0].identity, secs: map(.wall)})
| map(select((.secs | length) >= 3))
| map(
    (.secs) as $s
    | {
        identity,
        n: ($s | length),
        p50_s: ($s | pctl(50)),
        p99_s: ($s | pctl(99)),
        max_s: ($s | max)
      }
    | . + {
        timeout_min: (
          [60, ([5, ((1.5 * .p99_s / 60) | ceil), (((.max_s / 60) | ceil) + 2)] | max)]
          | min
        )
      }
  )
| sort_by(-.p99_s)
'

# --- fetching ---------------------------------------------------------------

# fetch_window SINCE UNTIL REPO... — pulls every completed job for the
# closed window [SINCE, UNTIL] (both dates inclusive, GitHub's own `created`
# qualifier semantics) across the given repos, and prints TWO lines to
# stdout: the NDJSON job-records file path, then the runs.tsv (repo,
# run_id, event) path — the raw materials --establish persists. Returns
# non-zero (via `die`, which the CALLER must check — see the callers'
# `|| die ...` — a `die` inside a `$(...)` command substitution only kills
# that subshell, not the parent script) if EITHER repo's run list fails or
# the total is zero, so a partial, silently-short window (e.g. one repo's
# call 403ing while the other succeeds) is a hard failure, never a quietly
# incomplete report.
fetch_window() {
  local since="$1" until="$2"; shift 2
  local -a repos=("$@")
  local tmpdir; tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/ci-usage-report.XXXXXX")"

  local runs_tsv="$tmpdir/runs.tsv"
  : > "$runs_tsv"
  local repo
  for repo in "${repos[@]}"; do
    if ! gh api --paginate --slurp \
      "/repos/${repo}/actions/runs?created=${since}..${until}&per_page=100" 2>"$tmpdir/gh.err" |
      jq -r --arg repo "$repo" '.[].workflow_runs[] | "\($repo)\t\(.id)\t\(.event)"' \
      >> "$runs_tsv"
    then
      cat "$tmpdir/gh.err" >&2
      die "failed to list runs for ${repo} (${since}..${until}) — see the gh/jq error above (a GitHub rate limit — check 'gh api rate_limit' — is the usual cause)"
    fi
  done

  local n_runs; n_runs="$(wc -l < "$runs_tsv" | tr -d ' ')"
  [ "$n_runs" -gt 0 ] || die "no runs found for ${since}..${until} across: ${repos[*]}"
  echo "ci-usage-report: ${n_runs} runs in ${since}..${until}; fetching jobs (parallel)…" >&2

  local jobs_ndjson="$tmpdir/jobs.ndjson"
  xargs -P 8 -n 3 "$SELF" _fetch-jobs < "$runs_tsv" > "$jobs_ndjson"

  local n_jobs; n_jobs="$(wc -l < "$jobs_ndjson" | tr -d ' ')"
  [ "$n_jobs" -gt 0 ] || die "0 jobs returned for ${n_runs} runs — API or auth problem?"
  echo "ci-usage-report: ${n_jobs} completed jobs joined." >&2

  # Coverage sanity check. Each `_fetch-jobs` worker swallows its own
  # per-run error (a failed run contributes zero lines, not a fatal xargs
  # exit) so a mid-fetch rate-limit trip is otherwise INVISIBLE — the script
  # would silently report a smaller, wrong window as if it were complete.
  # Compare distinct covered runs against the run list instead of trusting
  # "n_jobs > 0" alone.
  local n_covered_runs
  n_covered_runs="$(jq -r '.run_id' "$jobs_ndjson" | sort -u | wc -l | tr -d ' ')"
  local coverage_pct=$(( n_covered_runs * 100 / n_runs ))
  if [ "$coverage_pct" -lt 90 ]; then
    die "only ${n_covered_runs}/${n_runs} runs (${coverage_pct}%) yielded a job record — a rate limit or transient API failure almost certainly truncated this fetch mid-way; rerun once 'gh api rate_limit' shows headroom rather than trusting this window"
  fi

  printf '%s\n%s\n' "$jobs_ndjson" "$runs_tsv"
}

# --- rendering ---------------------------------------------------------------

# render_report REPORT_JSON_STRING SINCE UNTIL REPO... — jq computes the
# NUMBERS (it owns rounding, so a printed figure and a re-derived one can
# never drift by a formatting bug); bash's `printf` owns COLUMN WIDTH. jq's
# own string-padding functions get unreadable fast once more than one
# column needs to line up — plain TSV-out-then-printf is the same idiom
# scripts/build-release-cohort.sh already uses for tabular output.
#
# Takes the JSON as a STRING (not a file/process-substitution path):
# `<(...)` process substitution is a one-shot pipe on macOS, and this
# function reads its input three separate times (summary, per-repo,
# per-identity) — a second read on the same `/dev/fd/N` would just hang or
# return empty. `<<<"$report_json"` here-string input is re-creatable per
# `jq` call because bash holds the whole string in memory already.
render_report() {
  local report_json="$1" since="$2" until="$3"; shift 3
  local -a repos=("$@")

  local summary
  summary="$(jq -r '
    def fmtn(x): (x*10 | round) / 10;
    "\(.n_jobs)\t\(fmtn(.total_equiv_minutes))\t\(fmtn(.total_work_minutes))\t\(fmtn(.rounding_waste_minutes))"
  ' <<<"$report_json")"
  IFS=$'\t' read -r n_jobs total_equiv total_work total_waste <<<"$summary"

  echo "CI usage report — agent-ops-2026-07 P13 (scripts/ci-usage-report.sh)"
  echo "window: ${since}..${until} (closed)   repos: ${repos[*]}"
  echo
  echo "jobs measured: ${n_jobs}"
  echo "total equivalent minutes: ${total_equiv}"
  echo "  work minutes (unrounded, factor-applied): ${total_work}"
  echo "  rounding waste (ABSOLUTE minutes, billed - work — AC14 wants this, not a share): ${total_waste}"
  echo
  echo "per repository:"
  jq -r '
    def fmtn(x): (x*10 | round) / 10;
    .per_repo[] | "\(.repo)\t\(.n)\t\(fmtn(.equiv_minutes))"
  ' <<<"$report_json" | while IFS=$'\t' read -r repo n equiv; do
    printf '  %-28s %5s jobs   %10s equiv-min\n' "$repo" "$n" "$equiv"
  done
  echo
  echo "per job identity (sorted by equivalent minutes, desc):"
  printf '  %-46s %5s %10s %8s %10s %10s\n' \
    identity n equiv-min "share%" work-min waste-min
  jq -r '
    def fmtn(x): (x*10 | round) / 10;
    .per_identity[]
    | "\(.identity)\t\(.n)\t\(fmtn(.equiv_minutes))\t\(fmtn(.share_pct))\t\(fmtn(.work_minutes))\t\(fmtn(.rounding_waste_minutes))"
  ' <<<"$report_json" | while IFS=$'\t' read -r identity n equiv share work waste; do
    printf '  %-46s %5s %10s %7s%% %10s %10s\n' "$identity" "$n" "$equiv" "$share" "$work" "$waste"
  done
}

# --- modes -------------------------------------------------------------------

mode_report() {
  local since="$1" until="$2"; shift 2
  local -a repos=("$@")
  local files jobs_ndjson
  files="$(fetch_window "$since" "$until" "${repos[@]}")" || die "fetch_window failed for ${since}..${until}"
  jobs_ndjson="$(echo "$files" | sed -n '1p')"

  local report_json; report_json="$(jq -s "$JQ_REPORT" "$jobs_ndjson")"
  render_report "$report_json" "$since" "$until" "${repos[@]}"
}

mode_p99() {
  local since="$1" until="$2"; shift 2
  local -a repos=("$@")
  local files jobs_ndjson
  files="$(fetch_window "$since" "$until" "${repos[@]}")" || die "fetch_window failed for ${since}..${until}"
  jobs_ndjson="$(echo "$files" | sed -n '1p')"

  local rows; rows="$(jq -s -r "$JQ_P99"' | .[] | "\(.identity)\t\(.p50_s)\t\(.p99_s)\t\(.max_s)\t\(.n)\t\(.timeout_min)"' "$jobs_ndjson")"
  [ -n "$rows" ] || die "--p99 produced zero rows (every identity had fewer than 3 samples?)"

  write_timeouts_tsv "$since" "$until" "${repos[*]}" "$rows"
  echo "ci-usage-report: wrote $TIMEOUTS_TSV ($(echo "$rows" | wc -l | tr -d ' ') identities, window ${since}..${until})." >&2
}

write_timeouts_tsv() {
  # repos_str (not "repos" — that name is an ARRAY in the calling functions,
  # and shellcheck (correctly) flags reusing it for a plain joined string
  # here as suspicious even though bash's per-function `local` scoping makes
  # it safe).
  local since="$1" until="$2" repos_str="$3" rows="$4"
  {
    cat <<HEADER
# job-timeouts.tsv — the measured basis for every workflow's \`timeout-minutes\`.
#
# agent-ops-2026-07 P13 §3.6 / AC3 (B1). The first draft of that spec printed
# four p99s in prose and then said they were explicitly not the basis, so AC3
# had no source at all — the same "a print cannot red" defect the review had
# just caught one level up. This file IS the source: the gate reads it, so a
# stale timeout is a red line rather than a stale paragraph.
#
# REGENERATED, not hand-maintained: \`scripts/ci-usage-report.sh --p99\`
# (P13 K8) produces this file from GitHub's API. Do not hand-edit a row —
# rerun the command with the window you mean and commit the diff.
#
# Window: ${since}..${until} (closed, both dates inclusive), repositories:
# ${repos_str}. Durations are wall clock (completed_at - started_at) from
# GET /repos/{o}/{r}/actions/runs/{id}/jobs. \`/actions/runs/{id}/timing\`
# reports "total_ms": 0 for every job in this account and is unusable — do
# not regenerate from it (see this script's own header for how that was
# found).
#
# Identities with fewer than three samples are omitted rather than guessed,
# matching \`make lane\`'s own "no estimate (n=k)" discipline.
#
# THE SIZING RULE, and it is deliberately not just 1.5x p99:
#
#   timeout_min = min(60, max(5, ceil(1.5 * p99 / 60), ceil(max / 60) + 2))
#
# The \`max\` term is load-bearing. \`Pages/deploy\` has a p99 far below its
# max, and its longest observed runs concluded success — 1.5x p99 alone
# would size a timeout below an observed green run and cancel it. That is
# exactly the error an earlier hand-computed draft of this table made (a
# 20-minute \`check\` timeout below two successful 1313 s and 1239 s runs). A
# percentile describes the body; a timeout must clear the tail.
#
# The 60-minute CAP is a bound on the failure, not a performance target: at
# the macOS x10 multiplier a capped job bills 600 equivalent minutes,
# survivable inside the 1500-minute monthly budget (ADR-012), where the
# 360-minute GitHub default bills 3600 — more than the whole month. An
# identity that genuinely needs more than the cap is a finding, not a
# configuration.
#
# RE-DERIVE AFTER THE CEILING MOVES. Spec §3.6: once a wave relocates the
# private repo's \`make check\` off push-to-main, this table's tail (today
# dominated by that traffic) will fall, and every timeout sized from it
# becomes over-generous until it is regenerated against the new window —
# pass --since/--until explicitly rather than moving this file's default.
#
# identity	p50_s	p99_s	max_s	n	timeout_min
HEADER
    echo "$rows"
  } > "$TIMEOUTS_TSV"
}

mode_establish() {
  local since="$1" until="$2"; shift 2
  local -a repos=("$@")
  local files jobs_ndjson runs_tsv
  files="$(fetch_window "$since" "$until" "${repos[@]}")" || die "fetch_window failed for ${since}..${until}"
  jobs_ndjson="$(echo "$files" | sed -n '1p')"
  runs_tsv="$(echo "$files" | sed -n '2p')"

  local report_json; report_json="$(jq -s "$JQ_REPORT" "$jobs_ndjson")"
  local jobs_array; jobs_array="$(jq -s '.' "$jobs_ndjson")"
  local run_ids; run_ids="$(jq -R -s '
    split("\n") | map(select(length > 0) | split("\t")) as $rows
    | ($rows | group_by(.[0])
       | map({key: .[0][0], value: (map(.[1] | tonumber) | sort)}))
    | from_entries
  ' "$runs_tsv")"

  local established_at; established_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  jq -n \
    --arg since "$since" --arg until "$until" --arg established_at "$established_at" \
    --argjson repos "$(printf '%s\n' "${repos[@]}" | jq -R . | jq -s .)" \
    --argjson run_ids "$run_ids" \
    --argjson jobs "$jobs_array" \
    --argjson established "$report_json" \
    '{
      window: {since: $since, until: $until},
      established_at: $established_at,
      repos: $repos,
      run_ids: $run_ids,
      jobs: $jobs,
      established: $established
    }' > "$WINDOW_JSON"

  echo "ci-usage-report: pinned window ${since}..${until} → $WINDOW_JSON" >&2
  echo "  ($(jq '.jobs | length' "$WINDOW_JSON") jobs persisted; total_equiv_minutes=$(jq '.established.total_equiv_minutes' "$WINDOW_JSON"))" >&2
  render_report "$report_json" "$since" "$until" "${repos[@]}"
}

mode_verify() {
  [ -f "$WINDOW_JSON" ] || die "$WINDOW_JSON does not exist — run --establish first"

  # NO NETWORK. The whole point of --verify is that AC12's replay proves
  # itself from what was persisted, not from asking GitHub the same question
  # again — the raw job join in .jobs is fed straight back through the SAME
  # aggregation program (JQ_REPORT) that produced .established, and the two
  # results must still agree.
  local recomputed established
  recomputed="$(jq '.jobs' "$WINDOW_JSON" | jq "$JQ_REPORT")"
  established="$(jq '.established' "$WINDOW_JSON")"

  local since until
  since="$(jq -r '.window.since' "$WINDOW_JSON")"
  until="$(jq -r '.window.until' "$WINDOW_JSON")"

  if [ "$(jq -n --argjson a "$recomputed" --argjson b "$established" '$a == $b')" = "true" ]; then
    echo "ci-usage-report --verify: PASS — window ${since}..${until} replays to the pinned values." >&2
    render_report "$recomputed" "$since" "$until" "$(jq -r '.repos | join(", ")' "$WINDOW_JSON")"
    return 0
  fi

  echo "ci-usage-report --verify: FAIL — recomputing the persisted raw join for ${since}..${until} no longer matches the pinned values in $WINDOW_JSON." >&2
  echo "--- pinned (established) ---" >&2
  echo "$established" | jq '{total_equiv_minutes, total_work_minutes, rounding_waste_minutes}' >&2
  echo "--- recomputed just now ---" >&2
  echo "$recomputed" | jq '{total_equiv_minutes, total_work_minutes, rounding_waste_minutes}' >&2
  return 1
}

mode_billing() {
  local yearmonth="$1"
  local year="${yearmonth%%-*}" month="${yearmonth##*-}"
  # Strip a leading zero (e.g. "08") before arithmetic-context use so bash
  # doesn't read "08" as an (invalid) octal literal.
  month="${month#0}"

  local raw
  raw="$(gh api "/users/ydnikolaev/settings/billing/usage?year=${year}&month=${month}" 2>&1)" ||
    die "billing usage API call failed — needs the 'user' scope: gh auth refresh -h github.com -s user
$raw"

  echo "$raw" | jq -r --arg ym "$yearmonth" '
    def fmtn(x): (x*100 | round) / 100;
    "Billing usage — " + $ym + " (gross vs NET, i.e. after the included allowance and any discount)",
    "",
    "repository                SKU                      minutes    gross$    net$",
    (
      [.usageItems[] | select(.unitType == "Minutes" and .product == "actions")]
      | group_by([.repositoryName, .sku])
      | map({
          repo: .[0].repositoryName, sku: .[0].sku,
          minutes: (map(.quantity) | add),
          gross: (map(.grossAmount) | add),
          net: (map(.netAmount) | add)
        })
      | sort_by(-.minutes)
      | .[]
      | "\(.repo | . + (" " * ([26 - length, 1] | max)))\(.sku | . + (" " * ([25 - length, 1] | max)))\(fmtn(.minutes) | tostring | . + (" " * ([8 - (.|tostring|length), 1] | max)))  \(fmtn(.gross))   \(fmtn(.net))"
    ),
    "",
    "READ THE NET COLUMN ABOVE. Do not carry forward any figure from prose, including this project'"'"'s own. On 2026-08-12 the spec'"'"'s Amendment #2 claimed the public repository nets $0.87 on its macOS line and that axon dominates the account; on 2026-08-13 a direct read RETRACTED both — the $0.87 belongs to a2ahub-PRIVATE, the public repo nets $0.00 on both SKUs, and axon is third at 690 minutes. This endpoint reports usage CUMULATIVE TO QUERY TIME for the calendar month, so it is a reading and not a pin: unlike --verify, there is no persisted raw join to replay. Two numbers written a day apart will differ legitimately, which is exactly how the retracted claim survived a day."
  '
}

# --- argument parsing --------------------------------------------------------

MODE="report"
SINCE="$DEFAULT_SINCE"
UNTIL="$DEFAULT_UNTIL"
REPOS=("${DEFAULT_REPOS[@]}")
REPOS_OVERRIDDEN=0
BILLING_MONTH=""

while [ $# -gt 0 ]; do
  case "$1" in
    --p99) MODE="p99"; shift ;;
    --verify) MODE="verify"; shift ;;
    --establish) MODE="establish"; shift ;;
    --billing)
      MODE="billing"; shift
      if [ $# -gt 0 ] && [[ "$1" != --* ]]; then BILLING_MONTH="$1"; shift; fi
      ;;
    --since) [ $# -ge 2 ] || die "--since needs a YYYY-MM-DD argument"; SINCE="$2"; shift 2 ;;
    --until) [ $# -ge 2 ] || die "--until needs a YYYY-MM-DD argument"; UNTIL="$2"; shift 2 ;;
    --repo)
      [ $# -ge 2 ] || die "--repo needs an OWNER/NAME argument"
      if [ "$REPOS_OVERRIDDEN" -eq 0 ]; then REPOS=(); REPOS_OVERRIDDEN=1; fi
      REPOS+=("$2"); shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo "ci-usage-report: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

for d in "$SINCE" "$UNTIL"; do
  [[ "$d" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "not a YYYY-MM-DD date: $d"
done

require_cmds

case "$MODE" in
  report)    mode_report "$SINCE" "$UNTIL" "${REPOS[@]}" ;;
  p99)       mode_p99 "$SINCE" "$UNTIL" "${REPOS[@]}" ;;
  establish) mode_establish "$SINCE" "$UNTIL" "${REPOS[@]}" ;;
  verify)    mode_verify ;;
  billing)
    [ -n "$BILLING_MONTH" ] || BILLING_MONTH="$(date -u +%Y-%m)"
    [[ "$BILLING_MONTH" =~ ^[0-9]{4}-[0-9]{2}$ ]] || die "not a YYYY-MM month: $BILLING_MONTH"
    mode_billing "$BILLING_MONTH"
    ;;
  *) die "unreachable mode: $MODE" ;;
esac
