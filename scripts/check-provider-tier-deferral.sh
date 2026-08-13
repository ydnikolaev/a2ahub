#!/usr/bin/env bash
# docs/features/active/operational-confidence-2026-08/audits/v0.19.1-provider-tier-deferral.md
# §"Scheduled follow-up" promises, in prose only, that the next release
# changing runtime/host/funnel/validator/authorization does not ship on the
# `logic-proven, provider-deferred` tier again until a provider-tier
# (live-e2e) run has happened. Two consecutive deferral records already
# exist; a third one added without an intervening live-e2e run in between
# would make that promise — and the known-issue text that repeats it — false.
# This gate makes the promise machine-refusable instead of just written down.
#
# The deferral-record glob below is `audits/**/` , not `audits/*` : the honesty
# check derives what find_deferral_records() (line ~76) can actually return by
# intersecting its walk root with its own `-path '*/audits/*' -name
# '*-provider-tier-deferral.md'` filters, and that intersection reaches records
# nested deeper than one level under an audits/ directory. An earlier revision
# instead declared the bare walk roots ("docs/features" and ".") to satisfy the
# checker; that was a declaration bent to fit a tool, which is the failure this
# phase exists to stop. The extractor was made precise instead.
#
# docs/features/active/agent-ops-2026-07/audits/v0.20.0-provider-tier-deferral.md
# §"The count is 10 records, and there is at least one release it does not
# include" found the defect this revision fixes: the gate counted deferral
# RECORDS, and a release that simply omits the file was invisible to it —
# omitting the record was easier than writing it, so the streak the gate
# reported was only a lower bound. v0.19.11 shipped with no deferral record
# and no live-e2e evidence newer than it, and the record-counting gate said
# "ok". The unit of judgment is now the RELEASE, read from releasenotes/*.yaml:
# every released version must carry EITHER a deferral record named for it OR
# live-e2e evidence that covers it, and a release with neither is refused
# unconditionally — that refusal is NOT gated behind the streak-of-3 threshold
# below, because the streak-acknowledgement signature attests "we deferred
# again, on purpose"; a release with no record never made that decision on
# paper, and there is nothing for a count-signature to sign.

# lane-inputs:
#   docs/features/**/audits/**/*-provider-tier-deferral.md
#   **/audits/live-e2e-*
#   **/audits/live-e2e-*/**
#   releasenotes/**/*.yaml
# lane-reads-opaque: find_live_e2e_artifacts() (line ~84) walks the repo root
#   with `\( -path './.git' -o -name node_modules \) -prune -o -path '*/audits/*'
#   … -name 'live-e2e-*'`. That expression MIXES exclusion with selection, and
#   the classifier will not guess which arms select — the two globs above are
#   what it can actually return, verified by reading the invocation.
set -euo pipefail

# The override a third-or-later consecutive deferral must carry to ship.
#
# It is matched as a line in the newest outstanding record, so the signature
# lands in the same artifact that already carries the tier's per-candidate
# authorization — one file a later reader opens, not two. The count is part of
# the marker on purpose: "3rd" cannot be written once and inherited silently by
# a 4th, because the gate compares it to the number it actually counted.
# The trailing context is OPTIONAL: a line ending immediately after the
# number is a signature too, and requiring whitespace after the digits made
# the gate refuse a correctly-signed record for a reason it did not name.
# Fail-closed, but wrong-reason — the class this gate exists to end.
OVERRIDE_MARKER_RE='^consecutive-deferral-acknowledged:[[:space:]]*[0-9]+([[:space:]]|$)'
OVERRIDE_MARKER_EXAMPLE='consecutive-deferral-acknowledged: <N> — <who authorized shipping the Nth in a row, and why waiting was judged the larger risk>'

# The unix timestamp a path was first ADDED to git, or empty if the path has
# never been committed (still untracked / only staged). Filenames are not
# comparable across the two kinds of artifact this gate orders (one carries a
# VERSION, the other a DATE), so git history — not the filename, not mtime —
# is the only honest clock.
added_at() {
  git log --diff-filter=A --format=%ct -1 -- "$1" 2>/dev/null || true
}

# Every release note on disk, in ascending semver order, as
# "<version><TAB><path>". Disk-based `find`, NOT `git ls-files`: this must see
# a release note that has not been committed yet, for the same reason
# find_deferral_records() below is disk-based — a release-cutting flow runs
# this gate BEFORE committing the release it is about to ship, and the
# missing-record case that matters most is the one about to happen, not one
# already sitting in history. (check-release-notes-freshness.sh's
# latest_notes_file() uses `git ls-files` for the same directory, but that gate
# only ever needs the newest ALREADY-COMMITTED note as an anchor; it has no
# pre-commit promise to keep.)
#
# The zero-padded sort key is the same idiom latest_notes_file() uses, for the
# same reason: plain lexical sort puts "0.19.10.yaml" before "0.19.2.yaml",
# which is wrong by an order of magnitude the moment a patch number reaches two
# digits — the exact boundary the "newest record" lookup two functions below
# already had to be fixed for once, at v0.19.10.
find_release_versions() {
  find releasenotes -maxdepth 1 -type f -name '*.yaml' 2>/dev/null |
    awk -F'[/.]' '
      $2 ~ /^[0-9]+$/ && $3 ~ /^[0-9]+$/ && $4 ~ /^[0-9]+$/ && $5 == "yaml" {
        printf "%012d.%012d.%012d\t%s.%s.%s\t%s\n", $2, $3, $4, $2, $3, $4, $0
      }
    ' |
    sort |
    cut -f2-
}

find_deferral_records() {
  find docs/features -path '*/audits/*' -type f -name '*-provider-tier-deferral.md' 2>/dev/null | sort
}

# "any audits/ directory" — not just docs/features/**/audits/ — since a
# live-e2e evidence artifact clearing a deferral could in principle land
# under a differently rooted audits/ tree; .git and node_modules are pruned
# so the walk stays cheap and irrelevant vendored trees never match.
find_live_e2e_artifacts() {
  find . \( -path './.git' -o -name node_modules \) -prune -o \
    -path '*/audits/*' \( -type f -o -type d \) -name 'live-e2e-*' -print \
    2>/dev/null | sed 's#^\./##' | sort
}

# The deferral record whose filename names this exact release, matched
# against the list of paths passed in $2..$N (one call per lookup already
# has find_deferral_records() re-walked disk; the caller hoists it once and
# passes the list so N releases cost one walk, not N). Echoes the path, or
# nothing. `return 0` is explicit and unconditional: under `set -e`, a
# function whose last command is a failed `case` match would otherwise exit
# non-zero and kill the script from inside a `recpath="$(...)"` command
# substitution — the exact mute-failure class the override lookup further
# down already carries a comment warning about.
deferral_record_for_version() {
  local version="$1" rec
  shift
  for rec in "$@"; do
    [ -n "$rec" ] || continue
    case "$(basename "$rec")" in
      "v${version}-provider-tier-deferral.md")
        echo "$rec"
        return 0
        ;;
    esac
  done
  return 0
}

# True if some live-e2e artifact's own filename names this release
# (…-v0.18.2-…, …-v0.15.0-release-gate.txt…). This is the fallback the global
# `newest_live` comparison in check_provider_tier_deferral() cannot give: that
# comparison asks "did ANY live-e2e run happen after this release's note was
# committed", which is false — and wrongly outstanding — for a release whose
# OWN evidence was recorded before its own note commit (verify-then-cut, not
# cut-then-verify) while a still-newer unrelated artifact does not exist to
# rescue it. A name match is direct evidence for the one release it names,
# independent of where it falls in the global timeline.
live_e2e_names_version() {
  local version="$1" art
  while IFS= read -r art; do
    [ -n "$art" ] || continue
    case "$(basename "$art")" in
      *"v${version}"*) return 0 ;;
    esac
  done <<<"$(find_live_e2e_artifacts)"
  return 1
}

check_provider_tier_deferral() {
  local artifact rec ts newest_live=0
  local -a uncleared=()
  local -a uncleared_records=()
  local -a recordless=()

  # Find the most recently ADDED live-e2e evidence artifact. An untracked
  # artifact (no git add-date) is treated as though it does not exist yet
  # (timestamp 0): this gate follows committed git history, not the working
  # tree, so a staged-but-uncommitted "evidence" file can never silently
  # clear outstanding releases — the one direction that would let a third
  # deferral ship unrefused. This half of the untracked rule is the
  # fail-SAFE one.
  while IFS= read -r artifact; do
    [ -n "$artifact" ] || continue
    ts="$(added_at "$artifact")"
    ts="${ts:-0}"
    if [ "$ts" -gt "$newest_live" ]; then
      newest_live="$ts"
    fi
  done <<<"$(find_live_e2e_artifacts)"

  # Hoist the deferral-record walk once; deferral_record_for_version() is
  # called once per release below and a fresh `find docs/features` per call
  # would re-walk the same tree N times for no reason.
  local -a all_records=()
  while IFS= read -r rec; do
    [ -n "$rec" ] || continue
    all_records+=("$rec")
  done <<<"$(find_deferral_records)"

  # Walk every RELEASE (not every deferral record — that is the fix). A
  # release is outstanding if no live-e2e run is known to have happened after
  # it (by git history, or by an artifact naming it directly) — and an
  # UNCOMMITTED release note counts as outstanding immediately, the same
  # fail-CLOSED asymmetry find_deferral_records() consumers already rely on:
  # a release-cutting flow runs this gate before committing the note it is
  # about to ship, and that is exactly the moment a missing record must be
  # catchable.
  local relver relpath rel_ts recpath
  while IFS=$'\t' read -r relver relpath; do
    [ -n "$relver" ] || continue
    rel_ts="$(added_at "$relpath")"
    if [ -n "$rel_ts" ] && [ "$rel_ts" -le "$newest_live" ]; then
      continue
    fi
    if live_e2e_names_version "$relver"; then
      continue
    fi
    recpath="$(deferral_record_for_version "$relver" "${all_records[@]}")"
    if [ -n "$recpath" ]; then
      uncleared_records+=("$recpath")
      if [ -z "$rel_ts" ]; then
        uncleared+=("v$relver ($(basename "$recpath"), release note uncommitted)")
      else
        uncleared+=("v$relver ($(basename "$recpath"))")
      fi
    else
      if [ -z "$rel_ts" ]; then
        uncleared+=("v$relver (RELEASED, uncommitted — no deferral record, no live-e2e evidence)")
      else
        uncleared+=("v$relver (RELEASED — no deferral record, no live-e2e evidence)")
      fi
      recordless+=("v$relver")
    fi
  done <<<"$(find_release_versions)"

  local count=${#uncleared[@]}

  # A recordless release is refused UNCONDITIONALLY — not gated behind the
  # streak-of-3 threshold below, and never silenceable by the
  # streak-acknowledgement marker. That marker signs for a DECISION ("we
  # deferred again, on purpose"); a release with no record made no such
  # decision on paper, so there is nothing for a count-signature to attest
  # to. This branch runs before the threshold check specifically so a
  # recordless release inside an already-signed streak still surfaces by
  # name, rather than being swallowed by a signature that matched the wrong
  # thing (see the audit this revision fixes: a signature for "10 records"
  # said nothing about the release that had none).
  if [ "${#recordless[@]}" -gt 0 ]; then
    echo "provider-tier-deferral: FAIL — $count release(s) are outstanding since the last live-e2e evidence, and ${#recordless[@]} of them shipped with NEITHER a deferral record NOR live-e2e evidence:" >&2
    local r
    for r in "${uncleared[@]}"; do
      echo "  - $r" >&2
    done
    echo "" >&2
    echo "A recordless release cannot be covered by consecutive-deferral-acknowledged: that marker signs for a" >&2
    echo "deferral decision that was written down; a release with no record never made one on paper. Add" >&2
    echo "docs/features/<epic>/audits/v<version>-provider-tier-deferral.md for each release named above (dated" >&2
    echo "honestly, not backfilled to look better than it was), or clear the streak with a live-e2e run." >&2
    return 1
  fi

  if [ "$count" -ge 3 ]; then
    # Refusal is OVERRIDABLE, and deliberately so. The tier this guards already
    # requires a written operator authorization per candidate; a third
    # consecutive use requires a STRONGER one, stated inside the newest record
    # itself. The gate's job is to make the cost visible and signed, not
    # unavailable.
    #
    # Why not a hard block: the promise this encodes was authored by the agent
    # cutting v0.19.1, not by the operator it binds. A gate stating a rule its
    # owner does not hold is a gate that gets deleted the first time it fires —
    # the same failure mode as prose, with more ceremony. Overridable-but-
    # recorded is the version that survives contact with the person it binds.
    # The newest record is the one belonging to the newest RELEASE, and the
    # two wrong answers this line has already given are both worth keeping.
    #
    # (1) A FILENAME SORT stopped meaning "newest" at v0.19.10: as strings,
    #     "v0.19.9-..." sorts AFTER "v0.19.10-...", so the gate asked the
    #     v0.19.9 record to sign for a streak of nine — a signature its author
    #     could not have made and its file has no business carrying. Found on
    #     2026-08-12, cutting the first release whose patch number has two
    #     digits.
    #
    # (2) GIT ADD-TIME replaced it, with an untracked record short-circuiting
    #     to "newest" so a release flow could sign before committing. That
    #     held for exactly one real use. On 2026-08-13 this revision's own
    #     re-keying created THREE retrospective records at once (v0.19.4,
    #     v0.19.5, v0.19.11 — releases that had shipped with nothing), all
    #     untracked, and the loop took the first it happened to iterate. The
    #     gate then demanded that the operator sign for a thirteen-release
    #     streak inside a record about v0.19.4, a release from a week earlier.
    #     A signature in that file would be a lie about when it was given.
    #
    # `uncleared_records` is appended in RELEASE-VERSION order by the loop
    # above, which is fed by find_release_versions()'s own version sort. So
    # the newest release's record is simply the last element — no clock, no
    # filename, no git index, and nothing that a backfill can reorder. The
    # "sign before committing" property survives untouched: an uncommitted
    # record for the newest release is still that release's record.
    local newest=""
    if [ "${#uncleared_records[@]}" -gt 0 ]; then
      newest="${uncleared_records[$(( ${#uncleared_records[@]} - 1 ))]}"
    fi
    local signed=""
    if [ -n "$newest" ]; then
      # `|| true` is load-bearing under `set -e`: an unsigned record makes both
      # greps exit non-zero, and a failing command substitution in an
      # assignment kills the script — which presents as an EMPTY refusal, the
      # one output a gate must never produce. A gate that dies silently reads
      # as a gate that refused for reasons it declined to give.
      local marker_count
      marker_count="$(grep -cE "$OVERRIDE_MARKER_RE" "$newest" 2>/dev/null || true)"
      [ -n "$marker_count" ] || marker_count=0
      # TWO markers in one record is a refusal, not a "read the first one".
      # Correcting a signature by APPENDING the new line is what a person
      # actually does — it is what happened while writing this gate's own
      # case 8 — and `head -1` would then keep reading the superseded count
      # forever, silently, which is the exact failure mode the per-record
      # count exists to prevent. Ambiguity about which signature is in force
      # must be loud.
      # A PLACEHOLDER IS NOT A SIGNATURE. The marker's own documented shape
      # is `consecutive-deferral-acknowledged: <N> — <who authorized ...>`,
      # and a record that quotes that shape as an EXAMPLE — in a fenced block,
      # in an instruction to the operator, anywhere — used to satisfy this
      # gate outright if the author wrote a literal count into the example.
      #
      # Found by stepping in it: v0.21.0's record was written deliberately
      # UNSIGNED, with a fenced example telling the operator what to add, and
      # the gate reported "acknowledges the streak of 14" and went green. An
      # unsigned record read as signed, by the gate whose entire purpose is
      # refusing exactly that. Any angle-bracket placeholder left in the line
      # now disqualifies it.
      if grep -E "$OVERRIDE_MARKER_RE" "$newest" 2>/dev/null | grep -q '<[^>]*>'; then
        echo "provider-tier-deferral: FAIL — $newest carries a marker line that still contains a <placeholder>:" >&2
        grep -E "$OVERRIDE_MARKER_RE" "$newest" | grep '<[^>]*>' | sed 's/^/    /' >&2
        echo "" >&2
        echo "That is the documented EXAMPLE shape, not a signature. Replace the placeholders with who authorized" >&2
        echo "shipping the Nth in a row and why waiting was judged the larger risk — a record that merely quotes" >&2
        echo "the instruction has made no decision." >&2
        return 1
      fi
      if [ "$marker_count" -gt 1 ]; then
        echo "provider-tier-deferral: FAIL — $newest carries $marker_count streak signatures; exactly one must be in force." >&2
        echo "Correcting a signature means EDITING the line, not adding a second one: with two present, a reader" >&2
        echo "(and this gate) cannot tell which count the operator stands behind." >&2
        return 1
      fi
      signed="$(grep -oE "$OVERRIDE_MARKER_RE" "$newest" 2>/dev/null | grep -oE '[0-9]+' | head -1 || true)"
    fi
    if [ -n "$signed" ]; then
      if [ "$signed" -eq "$count" ]; then
        echo "provider-tier-deferral: ok — $count outstanding, and $newest acknowledges the streak of ${count}."
        return 0
      fi
      # A marker naming a DIFFERENT count is a signature carried forward from an
      # earlier streak position, which is exactly the silent inheritance this
      # gate exists to prevent: signing for the 3rd must not license the 4th —
      # and, since the unit changed from records to releases, a marker written
      # under the old record-count is a stale signature by construction the
      # first time this revision runs against it, and must re-read as one.
      echo "provider-tier-deferral: FAIL — $newest acknowledges $signed consecutive deferral(s), but $count release(s) are outstanding:" >&2
      local r2
      for r2 in "${uncleared[@]}"; do
        echo "  - $r2" >&2
      done
      echo "" >&2
      echo "A signature for an earlier position in the streak does not carry forward. Re-sign for $count, or clear the streak with a live-e2e run." >&2
      return 1
    fi
    echo "provider-tier-deferral: FAIL — $count consecutive release(s) since the last live-e2e evidence; the scheduled follow-up promise is now false:" >&2
    local r3
    for r3 in "${uncleared[@]}"; do
      echo "  - $r3" >&2
    done
    echo "" >&2
    echo "Clear it by running the promised provider tier (a live-e2e-* evidence artifact added to an audits/ dir after these releases)," >&2
    echo "or, if shipping anyway is the deliberate call, state it in $newest as a line reading:" >&2
    echo "  $OVERRIDE_MARKER_EXAMPLE" >&2
    echo "That line is the operator signing for the streak, not merely for this candidate. It is not paperwork: it is the only" >&2
    echo "thing that keeps a third deferral a decision rather than a habit nobody noticed forming." >&2
    return 1
  fi

  echo "provider-tier-deferral: ok — $count release(s) outstanding since the last live-e2e evidence, all recorded (fails at 3 unless the newest record acknowledges the streak)."
}

teeth() {
  local out
  # tmp1..tmp7 are deliberately NOT `local`: the cleanup trap below is an
  # EXIT trap (failure paths call `exit 1` directly, which bypasses a RETURN
  # trap), and it fires from the top-level script after this function has
  # already returned — a `local` variable would already be unbound by then,
  # silently turning `rm -rf` into a no-op and leaking every temp dir.
  # Every tmp the cases below create must appear in BOTH lines. Cases 5, 5b
  # and 6 were added later and their dirs were in neither, so each
  # `make harness-check` leaked two temp git repos — in a trap whose own
  # comment explains this exact hazard for the vars it did cover. Case 7
  # (recordless release) follows the same rule.
  tmp1="" tmp2="" tmp3="" tmp4="" tmp5="" tmp6="" tmp7=""
  trap 'rm -rf "${tmp1:-}" "${tmp2:-}" "${tmp3:-}" "${tmp4:-}" "${tmp5:-}" "${tmp6:-}" "${tmp7:-}"' EXIT

  init_repo() {
    local dir="$1"
    mkdir -p "$dir/docs/features/x/audits" "$dir/releasenotes"
    (
      cd "$dir"
      git init -q
      git config user.email test@example.invalid
      git config user.name teeth
    )
  }

  commit_at() {
    local dir="$1" epoch="$2" msg="$3"
    (
      cd "$dir"
      git add -A
      GIT_AUTHOR_DATE="@${epoch} +0000" GIT_COMMITTER_DATE="@${epoch} +0000" \
        git commit -q -m "$msg"
    )
  }

  # Case 1: 3 releases, each carrying a matching deferral record, no live-e2e
  # artifact at all → RED, naming each record. The release note and its
  # record are committed TOGETHER, mirroring how this repo actually cuts a
  # release (verified against real git history: releasenotes/0.20.0.yaml and
  # its deferral record share one add-timestamp).
  tmp1="$(mktemp -d)"
  init_repo "$tmp1"
  echo r1 >"$tmp1/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp1/releasenotes/0.1.0.yaml"
  commit_at "$tmp1" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp1/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp1/releasenotes/0.2.0.yaml"
  commit_at "$tmp1" 200 "add v0.2.0 deferral"
  echo r3 >"$tmp1/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  echo 'version: "0.3.0"' >"$tmp1/releasenotes/0.3.0.yaml"
  commit_at "$tmp1" 300 "add v0.3.0 deferral"

  if out="$(cd "$tmp1" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — 3 uncleared releases without an intervening live-e2e run stayed green" >&2
    exit 1
  fi
  for name in v0.1.0-provider-tier-deferral.md v0.2.0-provider-tier-deferral.md v0.3.0-provider-tier-deferral.md; do
    printf '%s' "$out" | grep -q "$name" || {
      echo "provider-tier-deferral --teeth: FAILED — red did not name $name" >&2
      exit 1
    }
  done

  # Case 2: exactly 2 uncleared releases (both recorded) → GREEN.
  tmp2="$(mktemp -d)"
  init_repo "$tmp2"
  echo r1 >"$tmp2/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp2/releasenotes/0.1.0.yaml"
  commit_at "$tmp2" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp2/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp2/releasenotes/0.2.0.yaml"
  commit_at "$tmp2" 200 "add v0.2.0 deferral"

  if ! out="$(cd "$tmp2" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — exactly 2 uncleared releases went red" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q '^provider-tier-deferral: ok — 2 ' || {
    echo "provider-tier-deferral --teeth: FAILED — green did not report the outstanding count" >&2
    exit 1
  }

  # Case 3: 3 releases exist, but a live-e2e artifact was added (by git
  # history) after the OLDEST of them, before the 2nd and 3rd → only the
  # oldest is cleared, 2 remain outstanding → GREEN, same threshold as case
  # 2. The artifact is deliberately named with a DATE that sorts and reads
  # as much older than the VERSION-named records around it, and its mtime is
  # bumped to "now" (the newest thing in the whole tree) after every commit
  # — with no git-visible change — so that filename-lexical or mtime-based
  # ordering would get this case wrong while git add-date gets it right.
  tmp3="$(mktemp -d)"
  init_repo "$tmp3"
  echo r1 >"$tmp3/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp3/releasenotes/0.1.0.yaml"
  commit_at "$tmp3" 100 "add v0.1.0 deferral"
  mkdir -p "$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run"
  echo evidence >"$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run/manifest.txt"
  commit_at "$tmp3" 150 "add live-e2e evidence after the oldest deferral"
  echo r2 >"$tmp3/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp3/releasenotes/0.2.0.yaml"
  commit_at "$tmp3" 200 "add v0.2.0 deferral"
  echo r3 >"$tmp3/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  echo 'version: "0.3.0"' >"$tmp3/releasenotes/0.3.0.yaml"
  commit_at "$tmp3" 300 "add v0.3.0 deferral"
  # Bump mtime only, no git-visible change (content is identical), so mtime
  # is now the newest path in the tree while its git add-date stays 150.
  touch "$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run/manifest.txt"
  touch "$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run"

  if ! out="$(cd "$tmp3" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a live-e2e run after the oldest of 3 releases should leave only 2 outstanding and stay green" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q '^provider-tier-deferral: ok — 2 ' || {
    echo "provider-tier-deferral --teeth: FAILED — green after the mid-run live-e2e evidence did not report 2 outstanding" >&2
    exit 1
  }

  # Case 4: 2 committed releases + 1 UNTRACKED (uncommitted) third release
  # (note and record both) → RED. Proves the untracked rule is asymmetric: an
  # uncommitted release counts as outstanding immediately rather than hiding
  # at timestamp 0 until it is committed (which would let a third deferral
  # ship unrefused by any gate that runs pre-commit).
  tmp4="$(mktemp -d)"
  init_repo "$tmp4"
  echo r1 >"$tmp4/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp4/releasenotes/0.1.0.yaml"
  commit_at "$tmp4" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp4/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp4/releasenotes/0.2.0.yaml"
  commit_at "$tmp4" 200 "add v0.2.0 deferral"
  echo r3 >"$tmp4/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  echo 'version: "0.3.0"' >"$tmp4/releasenotes/0.3.0.yaml"
  # v0.3.0 deliberately left untracked — no commit_at call.

  if out="$(cd "$tmp4" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — 2 committed + 1 untracked release stayed green" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'v0.3.0 (v0.3.0-provider-tier-deferral.md, release note uncommitted)' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not name the untracked third release" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 5: 3 uncleared releases, but the NEWEST carries an acknowledgement
  # signed for exactly 3 → GREEN. The refusal is an escalation, not a wall:
  # the tier already requires a per-candidate authorization, and this is the
  # operator additionally signing for the streak.
  tmp5="$(mktemp -d)"
  init_repo "$tmp5"
  echo r1 >"$tmp5/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp5/releasenotes/0.1.0.yaml"
  commit_at "$tmp5" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp5/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp5/releasenotes/0.2.0.yaml"
  commit_at "$tmp5" 200 "add v0.2.0 deferral"
  printf 'r3\nconsecutive-deferral-acknowledged: 3 — operator signed for the streak\n' \
    >"$tmp5/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  echo 'version: "0.3.0"' >"$tmp5/releasenotes/0.3.0.yaml"
  commit_at "$tmp5" 300 "add v0.3.0 deferral with the streak acknowledgement"

  if ! out="$(cd "$tmp5" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — an acknowledged 3rd deferral must ship, not be blocked" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'acknowledges the streak of 3' || {
    echo "provider-tier-deferral --teeth: FAILED — green did not report that the streak was acknowledged" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 5b: the signed record is UNTRACKED — which is the normal state at the
  # moment a release authors it, and therefore the case that matters most.
  #
  # This is a regression test for a real defect in this gate, caught by
  # running it against the actual v0.19.2 record rather than a fixture: the
  # outstanding list carried DISPLAY strings, so an untracked entry read
  # "<path> (uncommitted)", and the override lookup opened that annotated
  # string as a filename. It found nothing, the grep failed, and `set -e`
  # killed the script mid-refusal — producing exit 1 with NO output at all.
  # A gate that dies silently is worse than one that never ran: it reads as a
  # refusal whose reasons were withheld. Cases 1-5 all used committed
  # fixtures, where the display string happens to equal the path, so none of
  # them could see it.
  tmp6="$(mktemp -d)"
  init_repo "$tmp6"
  echo r1 >"$tmp6/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  echo 'version: "0.1.0"' >"$tmp6/releasenotes/0.1.0.yaml"
  commit_at "$tmp6" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp6/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  echo 'version: "0.2.0"' >"$tmp6/releasenotes/0.2.0.yaml"
  commit_at "$tmp6" 200 "add v0.2.0 deferral"
  printf 'r3\nconsecutive-deferral-acknowledged: 3 — signed before the record was committed\n' \
    >"$tmp6/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"   # deliberately NOT committed
  echo 'version: "0.3.0"' >"$tmp6/releasenotes/0.3.0.yaml"             # deliberately NOT committed

  if ! out="$(cd "$tmp6" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a signed but uncommitted 3rd record must ship" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'acknowledges the streak of 3' || {
    echo "provider-tier-deferral --teeth: FAILED — the override was not read from an untracked record" >&2
    echo "$out" >&2
    exit 1
  }

  # ...and unsigned-and-untracked must refuse WITH a message, never die mute.
  echo r3 >"$tmp6/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  if out="$(cd "$tmp6" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — an unsigned 3rd record shipped" >&2
    exit 1
  fi
  [ -n "$out" ] || {
    echo "provider-tier-deferral --teeth: FAILED — the refusal produced NO output; a mute gate reads as one that withheld its reasons" >&2
    exit 1
  }

  # Case 6: a 4th release COPIED from the 3rd, stale "3" and all → RED.
  #
  # This is the realistic inheritance path and the reason the count is inside
  # the marker: the next deferral record gets written by copying the last one,
  # and a signature that carried forward unread would let the streak grow
  # under a signature nobody re-made. (A 4th carrying NO marker is covered by
  # case 1's generic refusal, which correctly tells the author to sign.)
  echo r4 >"$tmp5/docs/features/x/audits/v0.4.0-provider-tier-deferral.md"
  printf 'consecutive-deferral-acknowledged: 3 — copied from the previous record and never re-read\n' \
    >>"$tmp5/docs/features/x/audits/v0.4.0-provider-tier-deferral.md"
  echo 'version: "0.4.0"' >"$tmp5/releasenotes/0.4.0.yaml"
  commit_at "$tmp5" 400 "add a 4th deferral carrying the 3rd's stale acknowledgement"

  if out="$(cd "$tmp5" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a 4th deferral shipped on the 3rd's signature" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'does not carry forward' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not explain that an earlier signature is not inherited" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 7: the actual defect this revision fixes. A release ships with NO
  # deferral record and NO live-e2e evidence at all (v0.19.11's real shape) →
  # RED, naming it as recordless, and NOT silenceable by a streak signature —
  # even a streak of exactly 1 (well under the streak-of-3 threshold) must
  # refuse, because "every release carries one or the other" is unconditional.
  tmp7="$(mktemp -d)"
  init_repo "$tmp7"
  echo 'version: "0.1.0"' >"$tmp7/releasenotes/0.1.0.yaml"
  # No matching v0.1.0-provider-tier-deferral.md anywhere, and no live-e2e
  # artifact in the repo at all.
  commit_at "$tmp7" 100 "ship v0.1.0 with no provider-tier evidence of any kind"

  if out="$(cd "$tmp7" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a recordless release with no live-e2e evidence stayed green" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'v0.1.0 (RELEASED — no deferral record, no live-e2e evidence)' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not name the recordless release" >&2
    echo "$out" >&2
    exit 1
  }
  printf '%s' "$out" | grep -q 'A recordless release cannot be covered' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not explain that a recordless release cannot be signed for" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 8: THE BACKFILL. Several retrospective records are added at once for
  # OLD releases, all untracked, while the newest release's record already
  # carries the signature. The streak signature must be read from the newest
  # RELEASE's record — not from whichever backfilled file the loop reached
  # first.
  #
  # This is not hypothetical: it is what happened the first time this
  # revision ran for real. Re-keying the gate from records to releases
  # surfaced three recordless releases at once; writing their records made
  # the gate demand a thirteen-release signature inside a record about a
  # release from the previous week. A signature there would have been a lie
  # about when it was given, and the only thing that made it visible was
  # someone reading the refusal instead of obeying it.
  tmp8="$(mktemp -d)"
  init_repo "$tmp8"
  for v in 0.1.0 0.2.0 0.3.0; do
    echo "version: \"$v\"" >"$tmp8/releasenotes/$v.yaml"
    echo "r-$v" >"$tmp8/docs/features/x/audits/v$v-provider-tier-deferral.md"
  done
  printf 'consecutive-deferral-acknowledged: 3 — signed in the NEWEST release, where the decision was made\n' \
    >>"$tmp8/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  commit_at "$tmp8" 100 "three releases, the newest carrying the signature"

  # Now backfill two OLDER releases' records, uncommitted — the exact shape
  # that broke the add-time heuristic.
  for v in 0.1.5 0.2.5; do
    echo "version: \"$v\"" >"$tmp8/releasenotes/$v.yaml"
    echo "retrospective, unsigned by design" >"$tmp8/docs/features/x/audits/v$v-provider-tier-deferral.md"
  done
  # The streak is now 5, so the newest record's "3" is genuinely stale and
  # must red — but it must red for the RIGHT reason, naming the newest
  # RELEASE's record, never a backfilled one.
  if out="$(cd "$tmp8" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a streak of 5 shipped on a signature for 3" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'v0.3.0-provider-tier-deferral.md acknowledges 3' || {
    echo "provider-tier-deferral --teeth: FAILED — the signature was not read from the NEWEST RELEASE's record; a backfilled older record was picked instead:" >&2
    echo "$out" >&2
    exit 1
  }
  # APPENDING a corrected signature instead of editing the old one must RED,
  # not silently keep the stale count. This case exists because it is what the
  # author of this very teeth block did by reflex.
  printf 'consecutive-deferral-acknowledged: 5 — appended instead of edited\n' \
    >>"$tmp8/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  if out="$(cd "$tmp8" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a record carrying TWO signatures shipped on the first one" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'carries 2 streak signatures' || {
    echo "provider-tier-deferral --teeth: FAILED — two signatures did not red as ambiguous:" >&2
    echo "$out" >&2
    exit 1
  }

  # And EDITING it to the true count greens — proving the backfilled records
  # did not need signatures of their own.
  perl -0pi -e 's/^consecutive-deferral-acknowledged: 3 .*\n//m' \
    "$tmp8/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  if ! out="$(cd "$tmp8" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — re-signing the newest release's record for the true count did not green:" >&2
    echo "$out" >&2
    exit 1
  fi

  # Case 9: an UNSIGNED record that quotes the signature's own shape as an
  # example, literal count and all. This is not hypothetical — it is how
  # v0.21.0's record was first written, and the gate greened on it.
  printf 'consecutive-deferral-acknowledged: 5 — <who authorized shipping the 5th in a row>\n' \
    >>"$tmp8/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  if out="$(cd "$tmp8" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a <placeholder> example counted as a signature" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'still contains a <placeholder>' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not name the placeholder:" >&2
    echo "$out" >&2
    exit 1
  }

  echo "provider-tier-deferral --teeth: 3 uncleared releases red and names them; 2 uncleared greens; a live-e2e run after the oldest of 3 clears it to 2 and greens by git history, not mtime or filename; an untracked 3rd release reds too; an acknowledged 3rd ships; a 4th does NOT inherit the 3rd's signature; a recordless release reds unconditionally, even a lone one; a backfill of older records leaves the signature in the NEWEST RELEASE's record; a <placeholder> example is not a signature."
}

if [ "${1:-}" = "--teeth" ]; then teeth; exit 0; fi
check_provider_tier_deferral
