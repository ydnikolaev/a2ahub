#!/usr/bin/env bash
# docs/features/active/operational-confidence-2026-08/audits/v0.19.1-provider-tier-deferral.md
# §"Scheduled follow-up" promises, in prose only, that the next release
# changing runtime/host/funnel/validator/authorization does not ship on the
# `logic-proven, provider-deferred` tier again until a provider-tier
# (live-e2e) run has happened. Two consecutive deferral records already
# exist; a third one added without an intervening live-e2e run in between
# would make that promise — and the known-issue text that repeats it — false.
# This gate makes the promise machine-refusable instead of just written down.
set -euo pipefail

# The override a third-or-later consecutive deferral must carry to ship.
#
# It is matched as a line in the newest outstanding record, so the signature
# lands in the same artifact that already carries the tier's per-candidate
# authorization — one file a later reader opens, not two. The count is part of
# the marker on purpose: "3rd" cannot be written once and inherited silently by
# a 4th, because the gate compares it to the number it actually counted.
OVERRIDE_MARKER_RE='^consecutive-deferral-acknowledged:[[:space:]]*[0-9]+[[:space:]]'
OVERRIDE_MARKER_EXAMPLE='consecutive-deferral-acknowledged: <N> — <who authorized shipping the Nth in a row, and why waiting was judged the larger risk>'

# The unix timestamp a path was first ADDED to git, or empty if the path has
# never been committed (still untracked / only staged). Filenames are not
# comparable across the two kinds of artifact this gate orders (one carries a
# VERSION, the other a DATE), so git history — not the filename, not mtime —
# is the only honest clock.
added_at() {
  git log --diff-filter=A --format=%ct -1 -- "$1" 2>/dev/null || true
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

check_provider_tier_deferral() {
  local artifact rec ts newest_live=0
  local -a uncleared=()

  # Find the most recently ADDED live-e2e evidence artifact. An untracked
  # artifact (no git add-date) is treated as though it does not exist yet
  # (timestamp 0): this gate follows committed git history, not the working
  # tree, so a staged-but-uncommitted "evidence" file can never silently
  # clear outstanding deferral records — the one direction that would let a
  # third deferral ship unrefused. This half of the untracked rule is the
  # fail-SAFE one.
  while IFS= read -r artifact; do
    [ -n "$artifact" ] || continue
    ts="$(added_at "$artifact")"
    ts="${ts:-0}"
    if [ "$ts" -gt "$newest_live" ]; then
      newest_live="$ts"
    fi
  done <<<"$(find_live_e2e_artifacts)"

  # Count deferral records added after that point (all of them, if no
  # live-e2e artifact was ever found — newest_live stays 0). The untracked
  # rule is DELIBERATELY ASYMMETRIC here: an uncommitted deferral record
  # counts as outstanding immediately, the moment it exists on disk, rather
  # than collapsing to timestamp 0 and hiding until someone commits it. A
  # release flow that runs this gate before committing the record it is
  # about to ship on is exactly the case "refusable by a machine" has to
  # catch — waiting for git history here would make the gate a lagging
  # indicator instead of a refusal.
  while IFS= read -r rec; do
    [ -n "$rec" ] || continue
    ts="$(added_at "$rec")"
    if [ -z "$ts" ] || [ "$ts" -gt "$newest_live" ]; then
      if [ -z "$ts" ]; then
        uncleared+=("$rec (uncommitted)")
      else
        uncleared+=("$rec")
      fi
    fi
  done <<<"$(find_deferral_records)"

  local count=${#uncleared[@]}
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
    local newest="${uncleared[${#uncleared[@]}-1]}"
    local signed
    signed="$(grep -oE "$OVERRIDE_MARKER_RE" "$newest" 2>/dev/null | grep -oE '[0-9]+' | head -1)"
    if [ -n "$signed" ]; then
      if [ "$signed" -eq "$count" ]; then
        echo "provider-tier-deferral: ok — $count outstanding, and $newest acknowledges being the ${count}th in a row."
        return 0
      fi
      # A marker naming a DIFFERENT count is a signature carried forward from an
      # earlier streak position, which is exactly the silent inheritance this
      # gate exists to prevent: signing for the 3rd must not license the 4th.
      echo "provider-tier-deferral: FAIL — $newest acknowledges $signed consecutive deferral(s), but $count are outstanding." >&2
      echo "A signature for an earlier position in the streak does not carry forward. Re-sign for $count, or clear the streak with a live-e2e run." >&2
      return 1
    fi
    echo "provider-tier-deferral: FAIL — $count consecutive provider-tier deferral(s) since the last live-e2e evidence; the scheduled follow-up promise is now false:" >&2
    local r
    for r in "${uncleared[@]}"; do
      echo "  - $r" >&2
    done
    echo "" >&2
    echo "Clear it by running the promised provider tier (a live-e2e-* evidence artifact added to an audits/ dir after these records)," >&2
    echo "or, if shipping anyway is the deliberate call, state it in $newest as a line reading:" >&2
    echo "  $OVERRIDE_MARKER_EXAMPLE" >&2
    echo "That line is the operator signing for the streak, not merely for this candidate. It is not paperwork: it is the only" >&2
    echo "thing that keeps a third deferral a decision rather than a habit nobody noticed forming." >&2
    return 1
  fi

  echo "provider-tier-deferral: ok — $count provider-tier deferral record(s) outstanding since the last live-e2e evidence (fails at 3 unless the newest record acknowledges the streak)."
}

teeth() {
  local out
  # tmp1..tmp4 are deliberately NOT `local`: the cleanup trap below is an
  # EXIT trap (failure paths call `exit 1` directly, which bypasses a RETURN
  # trap), and it fires from the top-level script after this function has
  # already returned — a `local` variable would already be unbound by then,
  # silently turning `rm -rf` into a no-op and leaking every temp dir.
  tmp1="" tmp2="" tmp3="" tmp4=""
  trap 'rm -rf "${tmp1:-}" "${tmp2:-}" "${tmp3:-}" "${tmp4:-}"' EXIT

  init_repo() {
    local dir="$1"
    mkdir -p "$dir/docs/features/x/audits"
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

  # Case 1: 3 uncleared records, no live-e2e artifact at all → RED, naming
  # each record.
  tmp1="$(mktemp -d)"
  init_repo "$tmp1"
  echo r1 >"$tmp1/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  commit_at "$tmp1" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp1/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  commit_at "$tmp1" 200 "add v0.2.0 deferral"
  echo r3 >"$tmp1/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  commit_at "$tmp1" 300 "add v0.3.0 deferral"

  if out="$(cd "$tmp1" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — 3 uncleared deferral records without an intervening live-e2e run stayed green" >&2
    exit 1
  fi
  for name in v0.1.0-provider-tier-deferral.md v0.2.0-provider-tier-deferral.md v0.3.0-provider-tier-deferral.md; do
    printf '%s' "$out" | grep -q "$name" || {
      echo "provider-tier-deferral --teeth: FAILED — red did not name $name" >&2
      exit 1
    }
  done

  # Case 2: exactly 2 uncleared records → GREEN.
  tmp2="$(mktemp -d)"
  init_repo "$tmp2"
  echo r1 >"$tmp2/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  commit_at "$tmp2" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp2/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  commit_at "$tmp2" 200 "add v0.2.0 deferral"

  if ! out="$(cd "$tmp2" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — exactly 2 uncleared deferral records went red" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q '^provider-tier-deferral: ok — 2 ' || {
    echo "provider-tier-deferral --teeth: FAILED — green did not report the outstanding count" >&2
    exit 1
  }

  # Case 3: 3 records exist, but a live-e2e artifact was added (by git
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
  commit_at "$tmp3" 100 "add v0.1.0 deferral"
  mkdir -p "$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run"
  echo evidence >"$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run/manifest.txt"
  commit_at "$tmp3" 150 "add live-e2e evidence after the oldest deferral"
  echo r2 >"$tmp3/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  commit_at "$tmp3" 200 "add v0.2.0 deferral"
  echo r3 >"$tmp3/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  commit_at "$tmp3" 300 "add v0.3.0 deferral"
  # Bump mtime only, no git-visible change (content is identical), so mtime
  # is now the newest path in the tree while its git add-date stays 150.
  touch "$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run/manifest.txt"
  touch "$tmp3/docs/features/x/audits/live-e2e-2020-01-01-mid-run"

  if ! out="$(cd "$tmp3" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — a live-e2e run after the oldest of 3 deferrals should leave only 2 outstanding and stay green" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q '^provider-tier-deferral: ok — 2 ' || {
    echo "provider-tier-deferral --teeth: FAILED — green after the mid-run live-e2e evidence did not report 2 outstanding" >&2
    exit 1
  }

  # Case 4: 2 committed records + 1 UNTRACKED (uncommitted) third record →
  # RED. Proves the untracked rule is asymmetric: an uncommitted record
  # counts as outstanding immediately rather than hiding at timestamp 0
  # until it is committed (which would let a third deferral ship unrefused
  # by any gate that runs pre-commit).
  tmp4="$(mktemp -d)"
  init_repo "$tmp4"
  echo r1 >"$tmp4/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  commit_at "$tmp4" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp4/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  commit_at "$tmp4" 200 "add v0.2.0 deferral"
  echo r3 >"$tmp4/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  # v0.3.0 deliberately left untracked — no commit_at call.

  if out="$(cd "$tmp4" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — 2 committed + 1 untracked deferral record stayed green" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'v0.3.0-provider-tier-deferral.md (uncommitted)' || {
    echo "provider-tier-deferral --teeth: FAILED — red did not name the untracked third record" >&2
    exit 1
  }

  # Case 5: 3 uncleared records, but the NEWEST carries an acknowledgement
  # signed for exactly 3 → GREEN. The refusal is an escalation, not a wall:
  # the tier already requires a per-candidate authorization, and this is the
  # operator additionally signing for the streak.
  tmp5="$(mktemp -d)"
  init_repo "$tmp5"
  echo r1 >"$tmp5/docs/features/x/audits/v0.1.0-provider-tier-deferral.md"
  commit_at "$tmp5" 100 "add v0.1.0 deferral"
  echo r2 >"$tmp5/docs/features/x/audits/v0.2.0-provider-tier-deferral.md"
  commit_at "$tmp5" 200 "add v0.2.0 deferral"
  printf 'r3\nconsecutive-deferral-acknowledged: 3 — operator signed for the streak\n' \
    >"$tmp5/docs/features/x/audits/v0.3.0-provider-tier-deferral.md"
  commit_at "$tmp5" 300 "add v0.3.0 deferral with the streak acknowledgement"

  if ! out="$(cd "$tmp5" && check_provider_tier_deferral 2>&1)"; then
    echo "provider-tier-deferral --teeth: FAILED — an acknowledged 3rd deferral must ship, not be blocked" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'acknowledges being the 3th in a row' || {
    echo "provider-tier-deferral --teeth: FAILED — green did not report that the streak was acknowledged" >&2
    echo "$out" >&2
    exit 1
  }

  # Case 6: a 4th record COPIED from the 3rd, stale "3" and all → RED.
  #
  # This is the realistic inheritance path and the reason the count is inside
  # the marker: the next deferral record gets written by copying the last one,
  # and a signature that carried forward unread would let the streak grow
  # under a signature nobody re-made. (A 4th carrying NO marker is covered by
  # case 1's generic refusal, which correctly tells the author to sign.)
  printf 'r4\nconsecutive-deferral-acknowledged: 3 — copied from the previous record and never re-read\n' \
    >"$tmp5/docs/features/x/audits/v0.4.0-provider-tier-deferral.md"
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

  echo "provider-tier-deferral --teeth: 3 uncleared reds and names them; 2 uncleared greens; a live-e2e run after the oldest of 3 clears it to 2 and greens by git history, not mtime or filename; an untracked 3rd record reds too; an acknowledged 3rd ships; a 4th does NOT inherit the 3rd's signature."
}

if [ "${1:-}" = "--teeth" ]; then teeth; exit 0; fi
check_provider_tier_deferral
