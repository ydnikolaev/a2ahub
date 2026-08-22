#!/usr/bin/env bash
# release-postflight.sh — the mirror image of release-preflight.sh, run AFTER
# a tag is cut and promoted, to verify the release's OUTCOME instead of its
# readiness.
#
# Why this exists (release-loop-2026-08 P6): v0.19.0 through v0.19.3 were cut
# and announced while a2ahub.dev served 0.18.2, for five days, because
# pages.yml had been red on main since 2026-08-01 and nothing in the runbook
# looked. Runbook steps 14/14b were two example commands in a code block a
# human was expected to remember, and the check itself spent its whole prior
# life UNABLE TO FAIL: a trailing-slash URL that 404s, `curl -s` without `-f`,
# and a pattern that matched digits out of the error page and printed
# "022.617.46". This script makes those steps a command instead of a promise.
#
# Seven assertions (spec §T5), all of it today's prose made mechanical:
#   1. the tag exists on the public remote and resolves to the promoted SHA
#   2. a GitHub Release exists for it, not draft, not prerelease, and
#      releases/latest names it
#   3. its asset count is at or above the cohort's own build matrix (derived
#      from .goreleaser.yaml — see _cohort_min_assets; no minimum is declared
#      anywhere else in this repository, which is a gap of its own — see
#      that function's comment)
#   4. its body is the RENDERED release notes, not a commit changelog —
#      assert a known line from releasenotes/<v>.yaml appears
#   5. pages.yml's run FOR THE PROMOTED COMMIT concluded success. pages.yml
#      fires on BOTH the tag push and the promotion's push to main, under
#      `concurrency: cancel-in-progress: true`, so one run cancels the
#      other — a cancelled run is NOT a success, and nothing before this
#      script read the SURVIVING run's conclusion against the promoted
#      commit. Filtered by `gh run list --commit <sha>`, never "--branch
#      main --limit 1" (release-preflight.sh's own selector): that reads
#      the newest run on the branch, which is exactly the wrong side of a
#      concurrent cancel-in-progress pair to trust.
#   6. `curl -fsS https://a2ahub.dev/changelog.md` contains an ANCHORED
#      `## v<version>` line. Three corrections carried verbatim from the
#      runbook's own history (corrected 2026-08-07): `-f` (an HTTP error
#      becomes a non-zero exit instead of printing the error body), the
#      `.md` route (the site build emits changelog.html, not
#      changelog/index.html — the original trailing-slash URL is a 404),
#      and the anchored pattern (a bare `grep -oE 'v[0-9]+...'` matched
#      digits out of the 404 page itself).
#   7. the space template's write floor and every reusable-workflow pin
#      name the published tag (runbook Phase 4 step 15,
#      `make space-template-baseline`) — this assertion is what makes that
#      step's omission visible, since nothing else runs after it.
#
# Reused from release-preflight.sh (sourced below, never edited): REMOTE,
# TEMPLATE_WORKFLOWS, TEMPLATE_MANIFEST, REUSABLE_DIR, and
# judge_pages_conclusion (assertion 5's SUCCESS/not-success half — the
# CANCELLED-vs-FAILURE distinction assertion 5 also owes its reader is
# layered on top here, by judge_all_cancelled, since that distinction did
# not exist before this phase).
#
# gate_unmeasured (scripts/lib/gate-lib.sh) is used everywhere a shell-out
# FAILS TO RUN — an absent `gh`/`curl`, a DNS failure, a rate limit — rather
# than where a shell-out ran and reported a real, ugly answer. Both stay
# red; what differs is whether the reader is sent to fix the release or to
# fix their own network, and the exit code a caller can branch on (1 vs
# GATE_EXIT_UNMEASURED).
#
# The bypass (US-4, T7) is a COUNTED, DATED, human-authored record — never a
# bare flag. A2A_SKIP_POSTFLIGHT_SITE_CHECK=1 skips assertions 5/6 (the ones
# that need pages.yml/the site to already be green) ONLY when
# "$EVIDENCE_DIR/<version>-postflight-bypass.md" exists, is dated, and states
# a reason; the THIRD such record in a row with no clean (unbypassed) run in
# between additionally requires that record to carry
# `postflight-bypass-acknowledged: <N>` naming the exact streak — the same
# discipline scripts/check-provider-tier-deferral.sh already applies to the
# provider-tier deferral, scaled down to this one check. This script never
# WRITES that record (an operator authors it, by hand, the same way a
# deferral record is authored) — it only reads and counts.
#
# Usage:
#   bash scripts/release-postflight.sh v0.24.0     # after promoting v0.24.0
#   bash scripts/release-postflight.sh --teeth     # offline self-test
#
# The declaration below was deliberately withheld until the Makefile target
# existed: `make lane`'s loader refuses the WHOLE repo's derivation when a
# script declares lane-inputs and no recipe invokes it, and this phase's
# implementer verified that empirically before leaving the note. The lead wired
# `make release-postflight` afterwards, so it belongs here now.
#
# NO lane-inputs HERE, and that is not an oversight: `make lane`'s loader
# refuses the WHOLE repo's derivation when a script declares lane-inputs and no
# CORPUS PHASE's recipe invokes it. `make release-postflight` is a ceremony
# target, not a phase — the same position release-preflight.sh sits in, which
# carries no declaration either. Both need the network and a published tag, so
# no diff could select them anyway.
# lane-reads-opaque: three reads the classifier cannot resolve, every one of
#   them this script's own machinery rather than a repository input. It sources
#   release-preflight.sh through "$(dirname "${BASH_SOURCE[0]}")" to reuse
#   judge_pages_conclusion rather than re-decide what a workflow conclusion
#   means; it reads "$notes", which is releasenotes/<VERSION>.yaml with VERSION
#   an argument, so the path exists only at call time; and it reaches GitHub and
#   the live site, which no path glob can name. Declared rather than silenced:
#   an unresolved read nobody explains is how a gate ends up judging something
#   it never named.
set -euo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"
# shellcheck source=scripts/release-preflight.sh
source "$(dirname "${BASH_SOURCE[0]}")/release-preflight.sh"
set -euo pipefail

# ── pure judges — no network, so --teeth can exercise the decision offline ──

# judge_release_state: does this already-fetched release satisfy "published,
# not draft, not prerelease, and named by releases/latest"?
judge_release_state() { # $1=isDraft $2=isPrerelease $3=latest-tag $4=want-tag
  local draft="$1" prerelease="$2" latest="$3" want="$4" problems=""
  [ "$draft" = "true" ] && problems="${problems:+$problems$'\n'}    - the release is a DRAFT"
  [ "$prerelease" = "true" ] && problems="${problems:+$problems$'\n'}    - the release is marked PRERELEASE"
  [ "$latest" = "$want" ] || problems="${problems:+$problems$'\n'}    - releases/latest names '$latest', not '$want'"
  if [ -n "$problems" ]; then
    printf '%s\n' "$problems"
    return 1
  fi
  return 0
}

# judge_body_carries_line: is $2 (a known line straight out of
# releasenotes/<v>.yaml) present verbatim in $1 (the GitHub Release body)? A
# commit changelog never carries release-note PROSE, only commit subjects, so
# this is enough to tell the two apart without parsing either format.
judge_body_carries_line() { # $1=body $2=known-line
  case "$1" in
    *"$2"*) return 0 ;;
    *) return 1 ;;
  esac
}

# judge_pages_for_sha: did AT LEAST ONE completed run among $* (every
# conclusion recorded for the promoted commit) succeed?
judge_pages_for_sha() { # $* = every completed conclusion for the promoted SHA
  local c
  for c in "$@"; do
    [ "$c" = "success" ] && return 0
  done
  return 1
}

# judge_all_cancelled: were EVERY one of $* (non-empty) exactly "cancelled"?
# Split from judge_pages_for_sha so the CANCELLED-vs-FAILURE message can
# differ, per spec T4 ("RED, distinctly from failure") — pages.yml fires on
# both the tag push and the promotion push under cancel-in-progress, so an
# all-cancelled result names that race, while any real failure names the
# build.
judge_all_cancelled() { # $* = every completed conclusion for the promoted SHA
  local c
  [ "$#" -eq 0 ] && return 1
  for c in "$@"; do
    [ "$c" = "cancelled" ] || return 1
  done
  return 0
}

# judge_site_changelog: does $1 (the fetched body of changelog.md) contain an
# ANCHORED "## v<version>" heading — never a bare substring match, which is
# exactly how the 404 error page's own digits were misread as a version
# number for this check's whole prior life.
judge_site_changelog() { # $1=body $2=version
  local body="$1" version="${2#v}"
  # A HERESTRING, never `printf | grep -q`: on the real (338KB) changelog
  # page, grep -q closes its stdin the instant it finds the (early) match,
  # SIGPIPE-killing the upstream printf, and `set -o pipefail` (active
  # throughout this script) then reports the PRINTF's non-zero status for a
  # pipeline whose grep half actually matched — a real, reproduced false RED
  # this exact function produced against the live site before the fix.
  grep -qE "^## v${version}([^0-9.]|\$)" <<<"$body"
}

# judge_curl_outcome: categorizes a non-zero curl exit code. 0 = success
# (caller does not reach this), 1 = curl RAN and the SERVER answered with an
# HTTP error (curl -f turns a 404 into exit 22 — this is the historical
# defect: the ORIGINAL check used `curl -s` with no `-f` and read a 404 body
# as if it were content), 2 = curl could not even reach the server (DNS,
# connect, timeout) — a fact about THIS RUN'S network, never about the site.
judge_curl_outcome() { # $1 = curl's exit code (non-zero)
  case "$1" in
    22) return 1 ;;
    *) return 2 ;;
  esac
}

# resolve_evidence_dir: which bucket currently holds the epic that owns this
# gate? The verdict file is evidence, and evidence written into a path nobody
# reads is the failure class this whole epic was cut against — so the bucket is
# RESOLVED rather than frozen. An epic moves from docs/features/active/ to
# docs/features/archive/ when it ships; freezing "active" here would mean the
# first release after that archive writes its verdict into a directory that no
# longer exists, and the operator would learn it from a stack trace at the end
# of a publish.
#
# Neither bucket present is a REFUSAL, never a fallback: inventing a directory
# would produce a verdict file the corpus does not carry.
resolve_evidence_dir() { # $1=repo root -> prints the audits dir, or nothing
  local root="$1" bucket dir
  for bucket in active archive; do
    dir="$root/docs/features/$bucket/release-loop-2026-08/audits"
    if [ -d "$dir" ]; then
      printf '%s\n' "$dir"
      return 0
    fi
  done
  return 1
}

# judge_source_sha: which PRIVATE commit was this release cut from? The tag
# names a PUBLIC, filtered commit that exists in no private history, so after
# the fact "what has changed since the release?" is unanswerable from the
# repository alone — it cost an hour of comparing published file content on
# 2026-08-22 to establish that v0.25.0 came from 371d15d1.
#
# The ship gate already knows: the publisher refuses a HEAD that carries no
# passing receipt, so at publish time HEAD *is* the source, and its receipt
# names it. This reads that same fact minutes later and writes it into the
# verdict, which is committed — so the answer stops living in a gitignored
# cache directory on one machine.
#
# It never guesses. A HEAD with no receipt (the tree moved on after the
# publish) is recorded as unrecorded, naming the SHA it looked at.
judge_source_sha() { # $1=repo root -> prints the source sha, or an "unrecorded" line
  local root="$1" head=""
  head="$(git -C "$root" rev-parse HEAD 2>/dev/null || true)"
  if [ -z "$head" ]; then
    printf 'unrecorded (no git HEAD)\n'
    return 0
  fi
  if [ -f "$root/.a2a/release-gate/$head.json" ] &&
     grep -q '"verdict"[[:space:]]*:[[:space:]]*"pass"' "$root/.a2a/release-gate/$head.json"; then
    printf '%s\n' "$head"
    return 0
  fi
  printf 'unrecorded (HEAD %s carries no passing ship-gate receipt)\n' "$head"
  return 0
}

# judge_template_names_tag: does the write floor equal $2, and does every
# workflow pin in $3.. equal $2? (Stricter than release-preflight.sh's own
# assert_floor_not_ahead, which only demands floor <= version — this
# assertion is POST-tag, after `make space-template-baseline` should have
# run, so "not ahead" is no longer enough; it must now equal the tag just
# published.)
judge_template_names_tag() { # $1=floor $2=version, then every pin found
  local floor="$1" version="$2" want problems="" ref
  shift 2
  want="${version#v}"
  [ "${floor:-}" = "$want" ] || problems="${problems:+$problems$'\n'}    - min_binary_version is ${floor:-<unset>}, not $want"
  for ref in "$@"; do
    [ "$ref" = "$version" ] || problems="${problems:+$problems$'\n'}    - a workflow pin names $ref, not $version"
  done
  if [ -n "$problems" ]; then
    printf '%s\n' "$problems"
    return 1
  fi
  return 0
}

# ── derivation helpers ───────────────────────────────────────────────────

# _cohort_min_assets: derives a LOWER BOUND on the release's asset count from
# .goreleaser.yaml's own build matrix (goos × goarch platforms, 6 artifacts
# each — archive + its cosign bundle + its sbom + the sbom's cosign bundle,
# raw binary + its cosign bundle — plus SHA256SUMS and its own bundle).
#
# THIS IS A LOWER BOUND, NOT THE DECLARED MINIMUM, AND THAT IS A GAP STATED
# HERE RATHER THAN PAPERED OVER: no file in this repository declares a
# cohort's asset-count floor (checked: .goreleaser.yaml, release.yml,
# scripts/build-release-cohort.sh, scripts/check-release-record.sh — none of
# them). release.yml appends assets goreleaser never produces (the macOS
# notifier zip, the VS Code extension, release-cohort.json,
# release-notes-<v>.yaml, each with its own cosign bundle), so the real
# published count is always higher than this derivation — v0.24.0 and
# v0.23.0 both carry 46 while this derives 38. That headroom is deliberate:
# this assertion can only ever catch a GROSS shortfall (a broken matrix,
# half the platforms missing), not confirm the exact expected count, because
# nothing in the repository states what the exact count should be.
_cohort_min_assets() { # $1=repo -> prints an integer, or nothing on parse failure
  local repo="$1" gorel n_goos n_goarch
  gorel="$repo/.goreleaser.yaml"
  [ -f "$gorel" ] || return 1
  n_goos="$(awk '
    /^ *goos:/ { in_goos=1; next }
    in_goos && /^ *- / { c++; next }
    in_goos && /^ *[A-Za-z]/ { in_goos=0 }
    END { print c+0 }
  ' "$gorel")"
  n_goarch="$(awk '
    /^ *goarch:/ { in_goarch=1; next }
    in_goarch && /^ *- / { c++; next }
    in_goarch && /^ *[A-Za-z]/ { in_goarch=0 }
    END { print c+0 }
  ' "$gorel")"
  if [ "${n_goos:-0}" -eq 0 ] || [ "${n_goarch:-0}" -eq 0 ]; then
    return 1
  fi
  printf '%s\n' "$((n_goos * n_goarch * 6 + 2))"
}

# _known_release_note_line: the first change's `subject:` line, verbatim —
# assertion 4's "known line from releasenotes/<v>.yaml". `subject:` (not the
# folded `headline:`) is used because it is emitted on ONE physical line by
# every notes file this repository has authored, so a plain `sed` extraction
# does not need to un-fold YAML block scalars to read it.
_known_release_note_line() { # $1=repo $2=version -> prints the line, or empty
  local repo="$1" version="$2" notes
  notes="$repo/releasenotes/${version#v}.yaml"
  [ -f "$notes" ] || return 0
  sed -n 's/^  subject:[[:space:]]*//p' "$notes" | head -1
}

# _release_versions_desc_from: every releasenotes/*.yaml version <= $2,
# newest first — the same zero-padded `sort -V` idiom
# check-provider-tier-deferral.sh / release-preflight.sh already use for
# ordering dotted versions correctly (0.9.0 < 0.10.0).
_release_versions_desc_from() { # $1=repo $2=version(vX.Y.Z)
  local repo="$1" version="$2" f base
  for f in "$repo"/releasenotes/*.yaml; do
    [ -e "$f" ] || continue
    base="$(basename "$f" .yaml)"
    case "$base" in
      [0-9]*.[0-9]*.[0-9]*) ;;
      *) continue ;;
    esac
    printf '%s\n' "v$base"
  done | sort -V | awk -v v="$version" '
    { a[NR]=$0 }
    $0==v { stop=NR }
    END { if (!stop) stop=NR; for (i=stop; i>=1; i--) print a[i] }
  '
}

# _bypass_streak_for: how many CONSECUTIVE releases ending at $3 (inclusive)
# carry a "$2-postflight-bypass.md" record under $1 — walking releasenotes
# versions newest-first and stopping at the first one WITHOUT a record (a
# clean, unbypassed postflight run resets the streak, the same "counts
# consecutive deferral records since the newest live-e2e evidence" shape
# check-provider-tier-deferral.sh already applies one level up).
_bypass_streak_for() { # $1=evidence_dir $2=repo $3=version -> prints a count
  local dir="$1" repo="$2" version="$3" v count=0
  while IFS= read -r v; do
    [ -f "$dir/${v#v}-postflight-bypass.md" ] || break
    count=$((count + 1))
  done < <(_release_versions_desc_from "$repo" "$version")
  printf '%s\n' "$count"
}

# ── assertion 1 ──────────────────────────────────────────────────────────

PROMOTED_SHA=""

assert_tag_resolves() { # $1=repo $2=version — sets PROMOTED_SHA on success
  local repo="$1" version="$2"
  if ! command -v git >/dev/null 2>&1; then
    gate_unmeasured "postflight: git is not available — cannot resolve $version."
    return 1
  fi
  if ! git -C "$repo" remote get-url "$REMOTE" >/dev/null 2>&1; then
    gate_unmeasured "postflight: no '$REMOTE' remote configured in $repo — set A2A_RELEASE_REMOTE to the release remote."
    return 1
  fi
  if ! git -C "$repo" fetch --quiet "$REMOTE" --tags >/dev/null 2>&1; then
    gate_unmeasured "postflight: could not fetch tags from '$REMOTE' — the network may be unavailable. Not evidence the tag is missing; evidence this run could not check it."
    return 1
  fi
  PROMOTED_SHA="$(git -C "$repo" rev-parse -q --verify "refs/tags/${version}^{commit}" 2>/dev/null || true)"
  if [ -z "$PROMOTED_SHA" ]; then
    gate_fail "postflight: $version does not resolve to a commit on '$REMOTE' after fetching tags.
    Fix: confirm runbook Phase 3 step 13 actually pushed the tag, or that VERSION is spelled correctly (e.g. v0.24.0)."
    return 1
  fi
  gate_ok "postflight: $version resolves to $PROMOTED_SHA on '$REMOTE'"
  return 0
}

# ── assertions 2/3/4 — one memoized `gh release view`, three judgments ────

_RELEASE_JSON=""
_RELEASE_JSON_RC=""
_RELEASE_JSON_ERR=""
_RELEASE_JSON_KEY=""

# _ensure_release_json fetches `gh release view` exactly once PER (slug,
# version) PAIR (memoized) so assertions 2, 3 and 4 do not triple the API
# calls a single `make release-postflight` makes, while still reporting each
# as its own independent PASS/FAIL/UNMEASURED row. Keyed on "$1/$2", not on
# "has this ever been fetched" — an earlier draft memoized unconditionally
# (any non-empty _RELEASE_JSON_RC short-circuited every later call), which is
# correct for today's single-version invocation but would silently judge a
# SECOND version against the FIRST one's cached JSON the moment this is ever
# called twice in one process (teeth caught exactly this: T6a's fixture read
# T5's stale commit-changelog body until the key was added).
_ensure_release_json() { # $1=slug $2=version
  [ "$_RELEASE_JSON_KEY" = "$1/$2" ] && [ -n "$_RELEASE_JSON_RC" ] && return 0
  _RELEASE_JSON_KEY="$1/$2"
  if ! command -v gh >/dev/null 2>&1; then
    _RELEASE_JSON_RC="no-gh"
    return 0
  fi
  local err rc
  err="$(mktemp)"
  _RELEASE_JSON="$(gh release view "$2" --repo "$1" --json isDraft,isPrerelease,assets,body 2>"$err")"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    _RELEASE_JSON_ERR="$(cat "$err")"
    if grep -qi "release not found" "$err"; then
      _RELEASE_JSON_RC="not-found"
    else
      _RELEASE_JSON_RC="network"
    fi
  else
    _RELEASE_JSON_RC="ok"
  fi
  rm -f "$err"
  return 0
}

assert_release_state() { # $1=slug $2=version
  local slug="$1" version="$2" latest_err latest latest_rc draft prerelease problems
  _ensure_release_json "$slug" "$version"
  case "$_RELEASE_JSON_RC" in
    no-gh)
      gate_unmeasured "postflight: gh is not installed — cannot confirm $version's GitHub Release state."
      return 1 ;;
    not-found)
      gate_fail "postflight: no GitHub Release exists for $version on $slug.
    Fix: re-run runbook Phase 3 step 13's promotion — a tag with no Release means goreleaser's release.yml did not complete."
      return 1 ;;
    network)
      gate_unmeasured "postflight: could not read the GitHub Release for $version ($_RELEASE_JSON_ERR) — not evidence the release is missing; evidence this run could not check it."
      return 1 ;;
  esac

  draft="$(printf '%s' "$_RELEASE_JSON" | jq -r '.isDraft')"
  prerelease="$(printf '%s' "$_RELEASE_JSON" | jq -r '.isPrerelease')"

  if ! command -v gh >/dev/null 2>&1; then
    gate_unmeasured "postflight: gh is not installed — cannot confirm releases/latest for $version."
    return 1
  fi
  latest_err="$(mktemp)"
  latest="$(gh api "repos/$slug/releases/latest" --jq .tag_name 2>"$latest_err")"
  latest_rc=$?
  if [ "$latest_rc" -ne 0 ]; then
    gate_unmeasured "postflight: could not read repos/$slug/releases/latest ($(cat "$latest_err")) — cannot confirm $version is the named latest release."
    rm -f "$latest_err"
    return 1
  fi
  rm -f "$latest_err"

  if problems="$(judge_release_state "$draft" "$prerelease" "$latest" "$version")"; then
    gate_ok "postflight: $version is published, non-draft, non-prerelease, and releases/latest names it"
    return 0
  fi
  gate_fail "postflight: $version's GitHub Release does not satisfy the release-state contract:
$problems
    Fix: publish/un-draft it, demote it from prerelease, or re-run promotion so releases/latest resolves to $version."
  return 1
}

assert_asset_count() { # $1=repo $2=slug $3=version
  local repo="$1" slug="$2" version="$3" count min
  _ensure_release_json "$slug" "$version"
  case "$_RELEASE_JSON_RC" in
    no-gh)
      gate_unmeasured "postflight: gh is not installed — cannot count $version's release assets."
      return 1 ;;
    not-found)
      gate_fail "postflight: no GitHub Release exists for $version on $slug, so it carries 0 assets.
    Fix: re-run runbook Phase 3 step 13's promotion."
      return 1 ;;
    network)
      gate_unmeasured "postflight: could not read $version's assets ($_RELEASE_JSON_ERR) — not evidence assets are missing; evidence this run could not check it."
      return 1 ;;
  esac

  count="$(printf '%s' "$_RELEASE_JSON" | jq -r '.assets | length')"
  min="$(_cohort_min_assets "$repo" || true)"
  if [ -z "$min" ]; then
    gate_unmeasured "postflight: could not derive the cohort's minimum asset count from $repo/.goreleaser.yaml's build matrix — cannot judge whether $version's $count assets are enough."
    return 1
  fi
  if [ "$count" -ge "$min" ]; then
    gate_ok "postflight: $version carries $count assets (>= the derived cohort floor of $min from goreleaser's own build matrix)"
    return 0
  fi
  gate_fail "postflight: $version carries only $count assets, below the derived cohort floor of $min (goreleaser's build matrix: goos x goarch platforms x 6 archive/raw/sbom/cosign artifacts, + SHA256SUMS + its bundle).
    Fix: check release.yml's run for $version — a partial goreleaser/cosign/sbom/notifier upload sheds assets silently."
  return 1
}

assert_release_body() { # $1=repo $2=slug $3=version
  local repo="$1" slug="$2" version="$3" body known
  _ensure_release_json "$slug" "$version"
  case "$_RELEASE_JSON_RC" in
    no-gh)
      gate_unmeasured "postflight: gh is not installed — cannot read $version's release body."
      return 1 ;;
    not-found)
      gate_fail "postflight: no GitHub Release exists for $version on $slug, so it has no body to check.
    Fix: re-run runbook Phase 3 step 13's promotion."
      return 1 ;;
    network)
      gate_unmeasured "postflight: could not read $version's release body ($_RELEASE_JSON_ERR) — not evidence it is wrong; evidence this run could not check it."
      return 1 ;;
  esac

  body="$(printf '%s' "$_RELEASE_JSON" | jq -r '.body')"
  known="$(_known_release_note_line "$repo" "$version")"
  if [ -z "$known" ]; then
    gate_unmeasured "postflight: releasenotes/${version#v}.yaml has no readable 'subject:' line for its first change — cannot assert the release body is the rendered notes rather than a commit changelog."
    return 1
  fi
  if judge_body_carries_line "$body" "$known"; then
    gate_ok "postflight: $version's release body carries '$known' — it reads as the rendered release notes, not a commit changelog"
    return 0
  fi
  gate_fail "postflight: $version's GitHub Release body does NOT contain '$known' (releasenotes/${version#v}.yaml's own text) — it looks like a commit changelog or generic text, not the rendered notes.
    Fix: re-run release.yml with --release-notes pointed at the rendered corpus file (goreleaser's release.mode: replace)."
  return 1
}

# ── assertion 5 — the one with no predecessor at all ───────────────────────

assert_pages_for_promoted_sha() { # $1=slug $2=sha $3=version
  local slug="$1" sha="$2" version="$3" err json rc conclusions category

  if [ -z "$sha" ]; then
    gate_unmeasured "postflight: no promoted SHA known for $version — assertion 1 did not resolve one, so pages.yml cannot be checked against it."
    return 1
  fi
  if ! command -v gh >/dev/null 2>&1; then
    gate_unmeasured "postflight: gh is not installed — cannot read pages.yml's runs for $sha."
    return 1
  fi

  err="$(mktemp)"
  json="$(gh run list --repo "$slug" --workflow=pages.yml --commit "$sha" --json conclusion,status 2>"$err")"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    gate_unmeasured "postflight: could not read pages.yml's runs for the promoted commit $sha ($(cat "$err")) — not evidence the site failed to build; evidence this run could not check it."
    rm -f "$err"
    return 1
  fi
  rm -f "$err"

  conclusions="$(printf '%s' "$json" | jq -r '.[] | select(.status=="completed") | .conclusion')"
  if [ -z "$conclusions" ]; then
    gate_fail "postflight: pages.yml has NO completed run at all for the promoted commit $sha. The build that should have deployed $version never ran, or never finished, against it.
    Fix: trigger pages.yml for $sha by hand (gh run list --repo $slug --workflow=pages.yml --commit $sha) and re-run this check."
    return 1
  fi

  # shellcheck disable=SC2086
  if judge_pages_for_sha $conclusions; then
    gate_ok "postflight: pages.yml has a successful completed run for the promoted commit $sha"
    return 0
  fi

  # shellcheck disable=SC2086
  if judge_all_cancelled $conclusions; then
    category="cancelled"
  else
    category="failed"
  fi

  if [ "$category" = "cancelled" ]; then
    gate_fail "postflight: every pages.yml run for the promoted commit $sha was CANCELLED — never a build failure.
    pages.yml fires on BOTH the tag push and the promotion's push to main, under concurrency: cancel-in-progress, so one
    run cancels the other — and a cancelled run is not evidence the site published.
    Fix: manually re-run pages.yml for $sha (gh run list --repo $slug --workflow=pages.yml --commit $sha), then re-run this check."
  else
    gate_fail "postflight: pages.yml's run(s) for the promoted commit $sha did not succeed ($(printf '%s' "$conclusions" | tr '\n' ',' | sed 's/,$//')).
    The site that $version's release announces was never actually deployed from this exact commit.
    Fix: fix the site build (npm --prefix web run check reproduces it locally) and re-run pages.yml for $sha."
  fi
  return 1
}

# ── assertion 6 — the check that could not fail, corrected ────────────────

assert_site_names_version() { # $1=url $2=version
  local url="$1" version="$2" body rc category

  if ! command -v curl >/dev/null 2>&1; then
    gate_unmeasured "postflight: curl is not installed — cannot read $url."
    return 1
  fi

  rc=0
  body="$(curl -fsS "$url" 2>/dev/null)" || rc=$?
  if [ "$rc" -ne 0 ]; then
    if judge_curl_outcome "$rc"; then
      category=0
    else
      category=$?
    fi
    if [ "$category" -eq 1 ]; then
      gate_fail "postflight: curl -fsS $url returned an HTTP error (curl exit 22 — an HTTP 4xx/5xx; -f turns that into a
    failure instead of printing the error page as content). The site is not serving $version's changelog entry from this URL.
    Fix: confirm pages.yml deployed successfully and that $url is still the live route."
    else
      gate_unmeasured "postflight: curl -fsS $url could not reach the server (curl exit $rc — DNS/connect/timeout) — not
    evidence the site is stale; evidence this run could not check it."
    fi
    return 1
  fi

  if judge_site_changelog "$body" "$version"; then
    gate_ok "postflight: $url names $version"
    return 0
  fi
  gate_fail "postflight: $url does not contain an anchored '## v${version#v}' heading — the site is not (yet) serving
    $version's changelog entry.
    Fix: confirm web/scripts/release-index.mjs's build imported this tag (it admits a version only once the tag exists
    in the checkout that built the site), and that pages.yml's run for the promoted commit actually succeeded."
  return 1
}

# ── the counted, dated bypass for 5+6 (US-4 / T7) ──────────────────────────

# _ordinal prints "1st", "2nd", "3rd", "4th", ... — used so the streak
# message reads "3rd" rather than "3th", including the 11th/12th/13th
# exception.
_ordinal() { # $1=N
  local n="$1"
  case "$((n % 100))" in
    11 | 12 | 13)
      printf '%dth\n' "$n"
      return ;;
  esac
  case "$((n % 10))" in
    1) printf '%dst\n' "$n" ;;
    2) printf '%dnd\n' "$n" ;;
    3) printf '%drd\n' "$n" ;;
    *) printf '%dth\n' "$n" ;;
  esac
}

assert_postflight_bypass_recorded() { # $1=repo $2=version $3=evidence_dir
  local repo="$1" version="$2" dir="$3" record streak ack nth

  record="$dir/${version#v}-postflight-bypass.md"
  if [ ! -f "$record" ]; then
    gate_fail "postflight: A2A_SKIP_POSTFLIGHT_SITE_CHECK=1 was set for $version, but $record does not exist.
    A bypass must be a counted, dated record — never a bare flag (the exact shape A2A_SKIP_PAGES_CHECK in
    release-preflight.sh already is, and the one this replaces).
    Fix: author that file with at least a 'bypassed: <YYYY-MM-DD>' line and a 'reason: <why>' line before shipping this
    way, or unset the flag and let assertions 5/6 actually run."
    return 1
  fi
  if ! grep -qE '^bypassed:[[:space:]]*[0-9]{4}-[0-9]{2}-[0-9]{2}' "$record"; then
    gate_fail "postflight: $record exists but carries no 'bypassed: YYYY-MM-DD' line — an undated record is exactly the
    free bare flag this replaces.
    Fix: add the date the bypass was used."
    return 1
  fi
  if ! grep -qE '^reason:[[:space:]]*[^[:space:]]' "$record"; then
    gate_fail "postflight: $record carries no 'reason:' line — a bypass with no stated reason cannot be reviewed later.
    Fix: state why the pages.yml/site check was skipped for $version."
    return 1
  fi

  streak="$(_bypass_streak_for "$dir" "$repo" "$version")"
  if [ "$streak" -lt 3 ]; then
    gate_ok "postflight: $version's site check was bypassed, and the bypass is recorded and dated ($record) — $streak consecutive."
    return 0
  fi

  nth="$(_ordinal "$streak")"
  ack="$(grep -oE '^postflight-bypass-acknowledged:[[:space:]]*[0-9]+' "$record" 2>/dev/null | grep -oE '[0-9]+' | head -1 || true)"
  if [ "${ack:-}" = "$streak" ]; then
    gate_ok "postflight: $version is the $nth CONSECUTIVE bypassed site check, and $record explicitly acknowledges the streak of $streak."
    return 0
  fi

  gate_fail "postflight: $version is the $nth CONSECUTIVE release whose site check was bypassed, with no clean
    (unbypassed) postflight run in between — and $record does not carry a matching acknowledgment (found: ${ack:-none}, need: $streak).
    A third-or-later consecutive bypass needs a STRONGER, explicit signature — not a habit nobody noticed forming.
    Fix: add a line to $record reading exactly:
        postflight-bypass-acknowledged: $streak — <who authorized shipping the $nth in a row, and why waiting was judged the larger risk>
    or clear the streak: unset A2A_SKIP_POSTFLIGHT_SITE_CHECK and let assertions 5/6 actually run and pass."
  return 1
}

# ── assertion 7 ─────────────────────────────────────────────────────────

assert_template_names_tag() { # $1=repo $2=version
  local repo="$1" version="$2" floor line ref problems refs=()

  floor="$(sed -n 's/^min_binary_version:[[:space:]]*"\{0,1\}\([0-9][0-9.]*\)"\{0,1\}.*/\1/p' "$repo/$TEMPLATE_MANIFEST" 2>/dev/null | head -1)"

  while IFS= read -r line; do
    line="${line%%#*}"
    case "$line" in *-reusable.yml@*) ;; *) continue ;; esac
    ref="${line##*@}"
    ref="$(printf '%s' "$ref" | tr -d '[:space:]')"
    [ -n "$ref" ] || continue
    refs+=("v${ref#v}")
  done < <(grep -rhE -- '-reusable\.yml@' "$repo/$TEMPLATE_WORKFLOWS" 2>/dev/null || true)

  if [ "${#refs[@]}" -eq 0 ]; then
    gate_fail "postflight: no reusable-workflow pin found under $repo/$TEMPLATE_WORKFLOWS — cannot confirm the template names $version."
    return 1
  fi

  if problems="$(judge_template_names_tag "${floor:-}" "$version" "${refs[@]}")"; then
    gate_ok "postflight: space-template's write floor and every workflow pin name $version"
    return 0
  fi
  gate_fail "postflight: space-template does not yet name $version everywhere:
$problems
    This is runbook Phase 4 step 15 (\`make space-template-baseline\`) — it must run AFTER every tag, and this
    assertion is what makes its omission visible instead of silently lagging.
    Fix: run \`make space-template-baseline VERSION=$version\`."
  return 1
}

# ── orchestration ──────────────────────────────────────────────────────────

RESULTS=""

# _run_assertion runs one assertion and appends a PASS/FAIL/UNMEASURED line
# to RESULTS, read off gate-lib's OWN counters (the SSOT gate_summary itself
# reads) rather than a second, parallel bookkeeping scheme. `|| true` is
# load-bearing under `set -e`: without it, the first assertion that calls
# gate_fail/gate_unmeasured and returns non-zero would abort the whole script
# instead of letting every assertion run and be recorded — exactly the
# collect-all shape gate-lib's own header describes.
_run_assertion() { # $1=label, then the assertion command + its args
  local label="$1" before_err before_un
  shift
  before_err=$_GATE_ERRORS
  before_un=$_GATE_UNMEASURED
  "$@" || true
  if [ "$_GATE_UNMEASURED" -gt "$before_un" ]; then
    RESULTS="${RESULTS}${label}: UNMEASURED"$'\n'
  elif [ "$_GATE_ERRORS" -gt "$before_err" ]; then
    RESULTS="${RESULTS}${label}: FAIL"$'\n'
  else
    RESULTS="${RESULTS}${label}: PASS"$'\n'
  fi
}

# ── teeth: every judge and every network-shaped failure, exercised offline ──

teeth() {
  local tmp fakebin mindir oldpath ctl
  tmp="$(mktemp -d)" || { echo "release-postflight --teeth: mktemp failed" >&2; exit 1; }
  trap 'rm -rf "$tmp"' RETURN
  fakebin="$tmp/bin"
  ctl="$tmp/ctl"
  mkdir -p "$fakebin" "$ctl"
  oldpath="$PATH"

  # _teeth_capture runs an assertion DIRECTLY (never inside "$(...)") so
  # gate-lib's global counters (_GATE_ERRORS/_GATE_UNMEASURED) update in
  # THIS shell — a command substitution forks a subshell, and an increment
  # made inside it is invisible the moment the subshell exits. Output is
  # still captured, via redirection to a file read back afterward.
  _teeth_capture() { # $1=outfile, then the command + its args
    local outfile="$1"
    shift
    "$@" >"$outfile" 2>&1 || true
  }

  # ── pure judge unit tests — no fixtures, no network at all ──────────────

  if ! judge_release_state false false v0.24.0 v0.24.0 >/dev/null; then
    echo "release-postflight --teeth: FAILED — judge_release_state refused a correct release" >&2; exit 1
  fi
  out="$(judge_release_state true false v0.24.0 v0.24.0 2>&1 || true)"
  grep -q "DRAFT" <<<"$out" || { echo "release-postflight --teeth: FAILED — a draft release was not named" >&2; exit 1; }
  out="$(judge_release_state false true v0.24.0 v0.24.0 2>&1 || true)"
  grep -q "PRERELEASE" <<<"$out" || { echo "release-postflight --teeth: FAILED — a prerelease was not named" >&2; exit 1; }
  out="$(judge_release_state false false v0.23.0 v0.24.0 2>&1 || true)"
  grep -q "v0.23.0" <<<"$out" || { echo "release-postflight --teeth: FAILED — a wrong releases/latest was not named" >&2; exit 1; }
  echo "  + judge_release_state: correct → GREEN, each violation named"

  if judge_body_carries_line "some commit changelog text" "an unmistakable notes subject"; then
    echo "release-postflight --teeth: FAILED — judge_body_carries_line matched text that is not present" >&2; exit 1
  fi
  if ! judge_body_carries_line "prefix an unmistakable notes subject suffix" "an unmistakable notes subject"; then
    echo "release-postflight --teeth: FAILED — judge_body_carries_line missed a real substring" >&2; exit 1
  fi
  echo "  + judge_body_carries_line: commit changelog → RED, rendered notes → GREEN"

  if judge_pages_for_sha; then
    echo "release-postflight --teeth: FAILED — judge_pages_for_sha accepted zero runs" >&2; exit 1
  fi
  if judge_pages_for_sha cancelled cancelled; then
    echo "release-postflight --teeth: FAILED — judge_pages_for_sha accepted an all-cancelled run" >&2; exit 1
  fi
  if judge_pages_for_sha failure cancelled; then
    echo "release-postflight --teeth: FAILED — judge_pages_for_sha accepted failure+cancelled" >&2; exit 1
  fi
  if ! judge_pages_for_sha cancelled success; then
    echo "release-postflight --teeth: FAILED — judge_pages_for_sha refused a genuine success among the pair" >&2; exit 1
  fi
  echo "  + judge_pages_for_sha: no success among the promoted SHA's runs → RED, one success → GREEN"

  if judge_all_cancelled; then
    echo "release-postflight --teeth: FAILED — judge_all_cancelled accepted zero runs as all-cancelled" >&2; exit 1
  fi
  if ! judge_all_cancelled cancelled cancelled; then
    echo "release-postflight --teeth: FAILED — judge_all_cancelled missed a genuine all-cancelled set" >&2; exit 1
  fi
  if judge_all_cancelled cancelled failure; then
    echo "release-postflight --teeth: FAILED — judge_all_cancelled called a mixed cancelled+failure set all-cancelled" >&2; exit 1
  fi
  echo "  + judge_all_cancelled: distinguishes CANCELLED from a real failure"

  if judge_site_changelog "nothing here" v0.24.0; then
    echo "release-postflight --teeth: FAILED — judge_site_changelog matched an absent version" >&2; exit 1
  fi
  if judge_site_changelog "## v0.24.0.5 unrelated" v0.24.0; then
    echo "release-postflight --teeth: FAILED — judge_site_changelog matched an unanchored prefix (v0.24.0.5 vs v0.24.0)" >&2; exit 1
  fi
  if ! judge_site_changelog "$(printf '## v0.24.0\ntext')" v0.24.0; then
    echo "release-postflight --teeth: FAILED — judge_site_changelog missed a genuine anchored heading" >&2; exit 1
  fi
  echo "  + judge_site_changelog: anchored match only, no substring/prefix false positives"

  judge_curl_outcome 22 && rc22=0 || rc22=$?
  [ "$rc22" -eq 1 ] || { echo "release-postflight --teeth: FAILED — judge_curl_outcome(22) did not report category 1 (HTTP error)" >&2; exit 1; }
  judge_curl_outcome 6 && rc6=0 || rc6=$?
  [ "$rc6" -eq 2 ] || { echo "release-postflight --teeth: FAILED — judge_curl_outcome(6) did not report category 2 (network)" >&2; exit 1; }
  judge_curl_outcome 7 && rc7=0 || rc7=$?
  [ "$rc7" -eq 2 ] || { echo "release-postflight --teeth: FAILED — judge_curl_outcome(7) did not report category 2 (network)" >&2; exit 1; }
  echo "  + judge_curl_outcome: exit 22 (HTTP error) is category 1, DNS/connect/timeout is category 2 — never the same category"

  if judge_template_names_tag "0.23.0" v0.24.0 v0.24.0 >/dev/null; then
    echo "release-postflight --teeth: FAILED — judge_template_names_tag accepted a lagging floor" >&2; exit 1
  fi
  if judge_template_names_tag "0.24.0" v0.24.0 v0.23.0 >/dev/null; then
    echo "release-postflight --teeth: FAILED — judge_template_names_tag accepted a lagging pin" >&2; exit 1
  fi
  if ! judge_template_names_tag "0.24.0" v0.24.0 v0.24.0 v0.24.0 >/dev/null; then
    echo "release-postflight --teeth: FAILED — judge_template_names_tag refused a floor and pins that all match" >&2; exit 1
  fi
  echo "  + judge_template_names_tag: floor lagging → RED, a pin lagging → RED, everything matching → GREEN"

  # _cohort_min_assets, against a throwaway .goreleaser.yaml
  mkdir -p "$tmp/cohort"
  printf 'builds:\n  - id: a2a\n    goos:\n      - darwin\n      - linux\n      - windows\n    goarch:\n      - amd64\n      - arm64\n    flags:\n      - -trimpath\n' > "$tmp/cohort/.goreleaser.yaml"
  min="$(_cohort_min_assets "$tmp/cohort")"
  [ "$min" = "38" ] || { echo "release-postflight --teeth: FAILED — _cohort_min_assets derived $min from a 3x2 matrix, wanted 38 (3*2*6+2)" >&2; exit 1; }
  echo "  + _cohort_min_assets: a 3 goos x 2 goarch matrix derives 38"
  rm -f "$tmp/cohort/.goreleaser.yaml"
  if _cohort_min_assets "$tmp/cohort" >/dev/null 2>&1; then
    echo "release-postflight --teeth: FAILED — _cohort_min_assets returned success with no .goreleaser.yaml at all" >&2; exit 1
  fi
  echo "  + _cohort_min_assets: no .goreleaser.yaml → refuses rather than guessing"

  # resolve_evidence_dir — BOTH buckets and the absence. A one-sided tooth
  # asserting only "active resolves" cannot tell a resolver from the frozen
  # path it replaced; the archive direction is the whole point, because that is
  # the state this repository enters the moment the epic ships.
  mkdir -p "$tmp/ev/docs/features/active/release-loop-2026-08/audits"
  got="$(resolve_evidence_dir "$tmp/ev" || true)"
  [ "$got" = "$tmp/ev/docs/features/active/release-loop-2026-08/audits" ] || {
    echo "release-postflight --teeth: FAILED — resolve_evidence_dir did not find the active bucket (got '$got')" >&2; exit 1; }
  rm -rf "$tmp/ev/docs/features/active"
  mkdir -p "$tmp/ev/docs/features/archive/release-loop-2026-08/audits"
  got="$(resolve_evidence_dir "$tmp/ev" || true)"
  [ "$got" = "$tmp/ev/docs/features/archive/release-loop-2026-08/audits" ] || {
    echo "release-postflight --teeth: FAILED — resolve_evidence_dir did not follow the epic into archive/ (got '$got')" >&2; exit 1; }
  rm -rf "$tmp/ev/docs/features/archive"
  if resolve_evidence_dir "$tmp/ev" >/dev/null 2>&1; then
    echo "release-postflight --teeth: FAILED — resolve_evidence_dir invented a directory when neither bucket exists" >&2; exit 1
  fi
  echo "  + resolve_evidence_dir: follows the epic into archive/, and refuses when it is in neither bucket"

  # judge_source_sha — BOTH directions. The receipt-present case is the one
  # that answers "what shipped"; the receipt-absent case is the one that must
  # not quietly name a commit the ship gate never judged.
  git init -q -b main "$tmp/src"
  git -C "$tmp/src" config user.email teeth@example.com
  git -C "$tmp/src" config user.name teeth
  printf 'x\n' > "$tmp/src/f"; git -C "$tmp/src" add -A
  git -C "$tmp/src" commit -qm "fixture"
  local src_head
  src_head="$(git -C "$tmp/src" rev-parse HEAD)"
  got="$(judge_source_sha "$tmp/src")"
  case "$got" in
    unrecorded*) ;;
    *) echo "release-postflight --teeth: FAILED — judge_source_sha named a SHA with no receipt at all (got '$got')" >&2; exit 1 ;;
  esac
  mkdir -p "$tmp/src/.a2a/release-gate"
  printf '{ "sha": "%s", "verdict": "fail" }\n' "$src_head" > "$tmp/src/.a2a/release-gate/$src_head.json"
  got="$(judge_source_sha "$tmp/src")"
  case "$got" in
    unrecorded*) ;;
    *) echo "release-postflight --teeth: FAILED — judge_source_sha accepted a FAILING receipt as the release source (got '$got')" >&2; exit 1 ;;
  esac
  printf '{ "sha": "%s", "verdict": "pass", "verify_phases": 71 }\n' "$src_head" > "$tmp/src/.a2a/release-gate/$src_head.json"
  got="$(judge_source_sha "$tmp/src")"
  [ "$got" = "$src_head" ] || {
    echo "release-postflight --teeth: FAILED — judge_source_sha did not name the SHA its passing receipt covers (got '$got')" >&2; exit 1; }
  echo "  + judge_source_sha: a passing receipt names the private source; no receipt and a failing one are both recorded as unrecorded"

  # ── fixture repo (acts as both the private ROOT and the public remote) ──

  local remote_bare="$tmp/public.git" work="$tmp/work"
  git init -q --bare "$remote_bare"
  git init -q -b main "$work"
  git -C "$work" config user.email teeth@example.com
  git -C "$work" config user.name teeth
  mkdir -p "$work/releasenotes" "$work/space-template/.github/workflows" "$work/.github/workflows"
  printf 'schema: release-notes/v1\nversion: 0.24.0\nreleased: "2026-08-21"\nheadline: test headline\nchanges:\n- id: RN-0\n  kind: fix\n  subject: an unmistakable notes subject nothing else would produce\n  detail: x\n' > "$work/releasenotes/0.24.0.yaml"
  printf 'schema: release-notes/v1\nversion: 0.23.0\nreleased: "2026-08-14"\nheadline: prior\nchanges:\n- id: RN-1\n  kind: fix\n  subject: a prior unmistakable subject\n  detail: x\n' > "$work/releasenotes/0.23.0.yaml"
  printf 'schema: release-notes/v1\nversion: 0.22.0\nreleased: "2026-08-01"\nheadline: older\nchanges:\n- id: RN-2\n  kind: fix\n  subject: an older unmistakable subject\n  detail: x\n' > "$work/releasenotes/0.22.0.yaml"
  printf 'schema: space/v1\nmin_binary_version: 0.24.0\n' > "$work/space-template/space.yaml"
  echo 'on: workflow_call' > "$work/.github/workflows/a2a-validate-reusable.yml"
  printf '    uses: o/r/.github/workflows/a2a-validate-reusable.yml@v0.24.0\n' > "$work/space-template/.github/workflows/a2a-validate.yml"
  printf 'builds:\n  - id: a2a\n    goos:\n      - darwin\n      - linux\n      - windows\n    goarch:\n      - amd64\n      - arm64\n    flags:\n      - -trimpath\n' > "$work/.goreleaser.yaml"
  git -C "$work" add -A && git -C "$work" commit -qm "fixture"
  git -C "$work" tag v0.24.0
  git -C "$work" tag v0.23.0
  git -C "$work" remote add public "$remote_bare"
  git -C "$work" push -q public main --tags

  # ── fake gh / curl, controlled by files under $ctl ───────────────────────

  cat > "$fakebin/gh" <<EOF
#!/usr/bin/env bash
CTL="$ctl"
if [ "\$1 \$2" = "release view" ]; then
  mode="\$(cat "\$CTL/release_view_mode" 2>/dev/null || echo ok)"
  case "\$mode" in
    notfound) echo "release not found" >&2; exit 1 ;;
    network) echo "connection error: dial tcp: lookup api.github.com: no such host" >&2; exit 1 ;;
    *) cat "\$CTL/release_view_json"; exit 0 ;;
  esac
elif [ "\$1 \$2" = "run list" ]; then
  mode="\$(cat "\$CTL/run_list_mode" 2>/dev/null || echo empty)"
  case "\$mode" in
    network) echo "connection error: dial tcp: no such host" >&2; exit 1 ;;
    empty) echo "[]" ; exit 0 ;;
    *) cat "\$CTL/run_list_json"; exit 0 ;;
  esac
elif [ "\$1" = "api" ]; then
  mode="\$(cat "\$CTL/latest_mode" 2>/dev/null || echo ok)"
  case "\$mode" in
    network) echo "connection error" >&2; exit 1 ;;
    *) cat "\$CTL/latest_tag" ;;
  esac
  exit 0
else
  echo "fake gh: unhandled args: \$*" >&2
  exit 99
fi
EOF
  chmod +x "$fakebin/gh"

  cat > "$fakebin/curl" <<EOF
#!/usr/bin/env bash
CTL="$ctl"
mode="\$(cat "\$CTL/curl_mode" 2>/dev/null || echo ok)"
case "\$mode" in
  http-error) exit 22 ;;
  network) exit 6 ;;
  *) cat "\$CTL/curl_body"; exit 0 ;;
esac
EOF
  chmod +x "$fakebin/curl"

  echo "v0.24.0" > "$ctl/latest_tag"
  cat > "$ctl/release_view_json" <<'JSON'
{"isDraft":false,"isPrerelease":false,"assets":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31,32,33,34,35,36,37,38],"body":"an unmistakable notes subject nothing else would produce"}
JSON

  export PATH="$fakebin:$oldpath"
  hash -r 2>/dev/null || true

  # T1: a release that landed correctly → every assertion GREEN.
  echo ok > "$ctl/release_view_mode"
  echo ok > "$ctl/latest_mode"
  echo custom > "$ctl/run_list_mode"
  cat > "$ctl/run_list_json" <<'JSON'
[{"status":"completed","conclusion":"cancelled"},{"status":"completed","conclusion":"success"}]
JSON
  echo ok > "$ctl/curl_mode"
  printf '## v0.24.0\nsome text\n## v0.23.0\n' > "$ctl/curl_body"

  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  assert_tag_resolves "$work" v0.24.0 || true
  assert_release_state ignored v0.24.0 || true
  _RELEASE_JSON=""; _RELEASE_JSON_RC=""; _RELEASE_JSON_ERR=""
  assert_asset_count "$work" ignored v0.24.0 || true
  _RELEASE_JSON=""; _RELEASE_JSON_RC=""; _RELEASE_JSON_ERR=""
  assert_release_body "$work" ignored v0.24.0 || true
  assert_pages_for_promoted_sha ignored "$PROMOTED_SHA" v0.24.0 || true
  assert_site_names_version "http://ignored/changelog.md" v0.24.0 || true
  assert_template_names_tag "$work" v0.24.0 || true
  if [ "$_GATE_ERRORS" -ne 0 ] || [ "$_GATE_UNMEASURED" -ne 0 ]; then
    echo "release-postflight --teeth: FAILED — T1 (a release that landed correctly) was not all-GREEN: $_GATE_ERRORS error(s), $_GATE_UNMEASURED unmeasured" >&2
    exit 1
  fi
  echo "  + T1: a release that landed correctly → GREEN across every assertion"

  # T2: the site serving a PREVIOUS version → RED naming both.
  printf '## v0.23.0\nolder text\n' > "$ctl/curl_body"
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  _teeth_capture "$tmp/out.txt" assert_site_names_version "http://ignored/changelog.md" v0.24.0
  out="$(cat "$tmp/out.txt")"
  if [ "$_GATE_ERRORS" -eq 0 ]; then
    echo "release-postflight --teeth: FAILED — T2 (site serving a previous version) stayed GREEN: $out" >&2; exit 1
  fi
  grep -q "v0.24.0" <<<"$out" || { echo "release-postflight --teeth: FAILED — T2 did not name the wanted version: $out" >&2; exit 1; }
  echo "  + T2: the site serving a previous version → RED"
  printf '## v0.24.0\nsome text\n## v0.23.0\n' > "$ctl/curl_body"

  # T3: the site URL returning 404 → RED, from the EXIT CODE, never a
  # parsed error page — the historical defect this phase exists to end.
  echo http-error > "$ctl/curl_mode"
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  _teeth_capture "$tmp/out.txt" assert_site_names_version "http://ignored/changelog.md" v0.24.0
  out="$(cat "$tmp/out.txt")"
  if [ "$_GATE_ERRORS" -eq 0 ]; then
    echo "release-postflight --teeth: FAILED — T3 (404) did not go RED: $out" >&2; exit 1
  fi
  grep -qi "HTTP error" <<<"$out" || { echo "release-postflight --teeth: FAILED — T3 red, but did not name an HTTP error (exit code), not a parsed body: $out" >&2; exit 1; }
  echo "  + T3: the site URL returning 404 → RED, from curl's exit code"
  echo ok > "$ctl/curl_mode"

  # T4: pages.yml's run for the promoted commit CANCELLED → RED, distinctly
  # from a real failure.
  cat > "$ctl/run_list_json" <<'JSON'
[{"status":"completed","conclusion":"cancelled"},{"status":"completed","conclusion":"cancelled"}]
JSON
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  _teeth_capture "$tmp/out_cancelled.txt" assert_pages_for_promoted_sha ignored "$PROMOTED_SHA" v0.24.0
  out_cancelled="$(cat "$tmp/out_cancelled.txt")"
  cancelled_errors=$_GATE_ERRORS
  cat > "$ctl/run_list_json" <<'JSON'
[{"status":"completed","conclusion":"failure"}]
JSON
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  _teeth_capture "$tmp/out_failure.txt" assert_pages_for_promoted_sha ignored "$PROMOTED_SHA" v0.24.0
  out_failure="$(cat "$tmp/out_failure.txt")"
  failure_errors=$_GATE_ERRORS
  if [ "$cancelled_errors" -eq 0 ] || [ "$failure_errors" -eq 0 ]; then
    echo "release-postflight --teeth: FAILED — T4 (cancelled/failure) did not both go RED" >&2; exit 1
  fi
  grep -qi "CANCELLED" <<<"$out_cancelled" || { echo "release-postflight --teeth: FAILED — the all-cancelled case did not name CANCELLED: $out_cancelled" >&2; exit 1; }
  grep -qi "CANCELLED" <<<"$out_failure" && { echo "release-postflight --teeth: FAILED — a real failure was described as CANCELLED, the two cases collapsed into one message" >&2; exit 1; }
  [ "$out_cancelled" != "$out_failure" ] || { echo "release-postflight --teeth: FAILED — cancelled and failure produced the IDENTICAL message" >&2; exit 1; }
  echo "  + T4: cancelled → RED naming the cancel-in-progress race, a real failure → RED with a DIFFERENT message"
  cat > "$ctl/run_list_json" <<'JSON'
[{"status":"completed","conclusion":"cancelled"},{"status":"completed","conclusion":"success"}]
JSON

  # T5: the release body being a commit changelog instead of the notes → RED.
  cat > "$ctl/release_view_json" <<'JSON'
{"isDraft":false,"isPrerelease":false,"assets":[1,2,3],"body":"* abc1234 fix(x): something\n* def5678 feat(y): another"}
JSON
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  _RELEASE_JSON=""; _RELEASE_JSON_RC=""; _RELEASE_JSON_ERR=""
  _teeth_capture "$tmp/out.txt" assert_release_body "$work" ignored v0.24.0
  out="$(cat "$tmp/out.txt")"
  if [ "$_GATE_ERRORS" -eq 0 ]; then
    echo "release-postflight --teeth: FAILED — T5 (commit changelog body) stayed GREEN: $out" >&2; exit 1
  fi
  grep -q "does NOT contain" <<<"$out" || { echo "release-postflight --teeth: FAILED — T5 red, but not with the not-rendered-notes message: $out" >&2; exit 1; }
  echo "  + T5: a commit-changelog release body → RED"
  cat > "$ctl/release_view_json" <<'JSON'
{"isDraft":false,"isPrerelease":false,"assets":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31,32,33,34,35,36,37,38],"body":"an unmistakable notes subject nothing else would produce"}
JSON

  # T6a: gh ABSENT → the MEASUREMENT-FAILED refusal, never a green, never a
  # domain verdict. A minimal, symlinked PATH is built so this is true
  # regardless of the host's own layout (no reliance on "gh happens to live
  # outside /usr/bin on this machine").
  mindir="$tmp/minbin"
  mkdir -p "$mindir"
  local tool realpath_bin
  for tool in git grep sed awk cat mktemp printf mkdir sort tr jq basename dirname rm cp mv chmod head tail wc find xargs env bash true false hash; do
    realpath_bin="$(command -v "$tool" 2>/dev/null || true)"
    [ -n "$realpath_bin" ] && ln -sf "$realpath_bin" "$mindir/$tool" 2>/dev/null || true
  done
  ln -sf "$fakebin/curl" "$mindir/curl"
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  out="$(PATH="$mindir" bash -c '
    command -v gh >/dev/null 2>&1 && { echo "gh is still resolvable — the minimal PATH trick failed" >&2; exit 1; }
    exit 0
  ' 2>&1)" || { echo "release-postflight --teeth: FAILED — could not build a PATH without gh: $out" >&2; exit 1; }
  _RELEASE_JSON=""; _RELEASE_JSON_RC=""; _RELEASE_JSON_ERR=""
  PATH="$mindir" assert_release_state ignored v0.24.0 2>"$tmp/t6a_err.txt" 1>/dev/null || true
  t6a_err="$(cat "$tmp/t6a_err.txt" 2>/dev/null)"
  if [ "$_GATE_UNMEASURED" -eq 0 ]; then
    echo "release-postflight --teeth: FAILED — T6a (gh absent) did not produce an UNMEASURED refusal: $t6a_err" >&2; exit 1
  fi
  if [ "$_GATE_ERRORS" -ne 0 ]; then
    echo "release-postflight --teeth: FAILED — T6a (gh absent) was reported as a domain FAIL, not UNMEASURED" >&2; exit 1
  fi
  grep -q "UNMEASURED" <<<"$t6a_err" || { echo "release-postflight --teeth: FAILED — T6a's own message does not carry UNMEASURED: $t6a_err" >&2; exit 1; }
  echo "  + T6a: gh absent → UNMEASURED (never a green, never a domain verdict)"

  # T6b: gh present but the NETWORK unavailable → the same MEASUREMENT-FAILED
  # refusal, not a green and not a domain verdict.
  echo network > "$ctl/release_view_mode"
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  _RELEASE_JSON=""; _RELEASE_JSON_RC=""; _RELEASE_JSON_ERR=""
  _teeth_capture "$tmp/out.txt" assert_release_state ignored v0.24.0
  out="$(cat "$tmp/out.txt")"
  if [ "$_GATE_UNMEASURED" -eq 0 ]; then
    echo "release-postflight --teeth: FAILED — T6b (network unavailable) did not produce an UNMEASURED refusal: $out" >&2; exit 1
  fi
  if [ "$_GATE_ERRORS" -ne 0 ]; then
    echo "release-postflight --teeth: FAILED — T6b (network unavailable) was reported as a domain FAIL, not UNMEASURED" >&2; exit 1
  fi
  echo "  + T6b: gh present, network unavailable → UNMEASURED (never a green, never a domain verdict)"
  echo ok > "$ctl/release_view_mode"

  # T6c: a genuine 404-shaped absence ("release not found") is still a
  # MEASURED, real fail — T6 must not swallow every gh failure into
  # unmeasured, only the ones that are not a real answer.
  echo notfound > "$ctl/release_view_mode"
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  _RELEASE_JSON=""; _RELEASE_JSON_RC=""; _RELEASE_JSON_ERR=""
  assert_release_state ignored v0.24.0 || true
  if [ "$_GATE_ERRORS" -eq 0 ] || [ "$_GATE_UNMEASURED" -ne 0 ]; then
    echo "release-postflight --teeth: FAILED — T6c (release genuinely not found) was not a measured FAIL" >&2; exit 1
  fi
  echo "  + T6c: gh runs and genuinely finds no release → measured FAIL, not unmeasured"
  echo ok > "$ctl/release_view_mode"

  # ── T7: the counted, dated bypass ────────────────────────────────────────

  local evdir="$tmp/audits"
  mkdir -p "$evdir"

  # A bare flag, no record at all → RED (this is the exact shape being replaced).
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  _teeth_capture "$tmp/out.txt" assert_postflight_bypass_recorded "$work" v0.24.0 "$evdir"
  out="$(cat "$tmp/out.txt")"
  if [ "$_GATE_ERRORS" -eq 0 ]; then
    echo "release-postflight --teeth: FAILED — an unrecorded bypass (bare flag) stayed GREEN: $out" >&2; exit 1
  fi
  grep -q "does not exist" <<<"$out" || { echo "release-postflight --teeth: FAILED — the unrecorded-bypass message did not name the missing record: $out" >&2; exit 1; }
  echo "  + T7a: bypass used with no record at all → RED (never a bare flag)"

  # A CONSECUTIVE streak of three recorded bypasses (v0.22.0, v0.23.0,
  # v0.24.0), none acknowledged yet.
  printf 'bypassed: 2026-08-01\nreason: pages.yml known broken, fix already in flight\n' > "$evdir/0.22.0-postflight-bypass.md"
  printf 'bypassed: 2026-08-08\nreason: same incident, still open\n' > "$evdir/0.23.0-postflight-bypass.md"
  printf 'bypassed: 2026-08-21\nreason: same incident, still open\n' > "$evdir/0.24.0-postflight-bypass.md"
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  _teeth_capture "$tmp/out.txt" assert_postflight_bypass_recorded "$work" v0.24.0 "$evdir"
  out="$(cat "$tmp/out.txt")"
  if [ "$_GATE_ERRORS" -eq 0 ]; then
    echo "release-postflight --teeth: FAILED — the 3rd consecutive UNACKNOWLEDGED bypass stayed GREEN: $out" >&2; exit 1
  fi
  grep -q "3rd" <<<"$out" || { echo "release-postflight --teeth: FAILED — the 3rd-consecutive message did not name the streak: $out" >&2; exit 1; }
  echo "  + T7b: the 3rd CONSECUTIVE unremediated bypass → RED, naming the streak"

  # The same streak, now acknowledged with the RIGHT count → GREEN.
  printf 'bypassed: 2026-08-21\nreason: same incident, still open\npostflight-bypass-acknowledged: 3 — teeth, shipping is the smaller risk\n' > "$evdir/0.24.0-postflight-bypass.md"
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  assert_postflight_bypass_recorded "$work" v0.24.0 "$evdir" || true
  if [ "$_GATE_ERRORS" -ne 0 ]; then
    echo "release-postflight --teeth: FAILED — a correctly acknowledged 3rd-consecutive bypass was still refused" >&2; exit 1
  fi
  echo "  + T7c: the 3rd consecutive bypass, correctly acknowledged (postflight-bypass-acknowledged: 3) → GREEN"

  # An acknowledgment signed for the WRONG position does not carry forward.
  printf 'bypassed: 2026-08-21\nreason: same incident, still open\npostflight-bypass-acknowledged: 2 — wrong position\n' > "$evdir/0.24.0-postflight-bypass.md"
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  assert_postflight_bypass_recorded "$work" v0.24.0 "$evdir" || true
  if [ "$_GATE_ERRORS" -eq 0 ]; then
    echo "release-postflight --teeth: FAILED — an acknowledgment signed for streak 2 was accepted for streak 3" >&2; exit 1
  fi
  echo "  + T7d: an acknowledgment signed for an earlier streak position does not carry forward"

  # A single (1st) bypass, no acknowledgment needed → GREEN.
  rm -f "$evdir/0.22.0-postflight-bypass.md" "$evdir/0.23.0-postflight-bypass.md"
  printf 'bypassed: 2026-08-21\nreason: isolated one-off\n' > "$evdir/0.24.0-postflight-bypass.md"
  _GATE_ERRORS=0; _GATE_WARNINGS=0; _GATE_UNMEASURED=0
  assert_postflight_bypass_recorded "$work" v0.24.0 "$evdir" || true
  if [ "$_GATE_ERRORS" -ne 0 ]; then
    echo "release-postflight --teeth: FAILED — a lone, first-time, recorded bypass was refused" >&2; exit 1
  fi
  echo "  + T7e: a single recorded, dated, reasoned bypass → GREEN"

  export PATH="$oldpath"
  hash -r 2>/dev/null || true

  echo "release-postflight --teeth: all teeth bite."
}

# ── entrypoint ───────────────────────────────────────────────────────────

if [ "${BASH_SOURCE[0]}" = "$0" ]; then

if [ "${1:-}" = "--teeth" ]; then
  teeth
  exit 0
fi

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "usage: bash scripts/release-postflight.sh <version-just-cut>   # e.g. v0.24.0" >&2
  echo "       bash scripts/release-postflight.sh --teeth" >&2
  exit 2
fi
case "$VERSION" in
  v*) ;;
  *) VERSION="v$VERSION" ;;
esac

ROOT="$(git rev-parse --show-toplevel)"
SLUG="${A2A_PUBLIC_SLUG:-ydnikolaev/a2ahub}"
SITE_URL="${A2A_POSTFLIGHT_SITE_URL:-https://a2ahub.dev/changelog.md}"
EVIDENCE_DIR="${A2A_POSTFLIGHT_EVIDENCE_DIR:-}"
if [ -z "$EVIDENCE_DIR" ]; then
  EVIDENCE_DIR="$(resolve_evidence_dir "$ROOT" || true)"
fi
if [ -z "$EVIDENCE_DIR" ]; then
  echo "release-postflight: cannot resolve where to write the verdict — the" >&2
  echo "  release-loop-2026-08 audits directory is in neither docs/features/active/" >&2
  echo "  nor docs/features/archive/. Point A2A_POSTFLIGHT_EVIDENCE_DIR at the" >&2
  echo "  directory that should hold it rather than letting this gate invent one." >&2
  exit 2
fi

_run_assertion "1-tag-resolves-on-remote" assert_tag_resolves "$ROOT" "$VERSION"
_run_assertion "2-release-published-state" assert_release_state "$SLUG" "$VERSION"
_run_assertion "3-asset-count" assert_asset_count "$ROOT" "$SLUG" "$VERSION"
_run_assertion "4-release-body-is-rendered-notes" assert_release_body "$ROOT" "$SLUG" "$VERSION"

if [ "${A2A_SKIP_POSTFLIGHT_SITE_CHECK:-}" = "1" ]; then
  # Two explicit rows, not a silent absence: a reader of a bypassed verdict
  # file must be able to tell "the site check was skipped" from "the site
  # check row was never written" (AC3 asks for EVERY assertion's result).
  _run_assertion "5-6-bypass-record" assert_postflight_bypass_recorded "$ROOT" "$VERSION" "$EVIDENCE_DIR"
  RESULTS="${RESULTS}5-pages-conclusion-for-promoted-commit: BYPASSED"$'\n'
  RESULTS="${RESULTS}6-site-serves-this-version: BYPASSED"$'\n'
else
  _run_assertion "5-pages-conclusion-for-promoted-commit" assert_pages_for_promoted_sha "$SLUG" "$PROMOTED_SHA" "$VERSION"
  _run_assertion "6-site-serves-this-version" assert_site_names_version "$SITE_URL" "$VERSION"
fi

_run_assertion "7-template-names-published-tag" assert_template_names_tag "$ROOT" "$VERSION"

mkdir -p "$EVIDENCE_DIR" 2>/dev/null || true
{
  echo "schema: postflight-verdict/v1"
  echo "version: $VERSION"
  echo "checked: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "bypass: ${A2A_SKIP_POSTFLIGHT_SITE_CHECK:-0}"
  echo "source-sha: $(judge_source_sha "$ROOT")"
  echo
  printf '%s' "$RESULTS"
} > "$EVIDENCE_DIR/${VERSION#v}-postflight-verdict.md" 2>/dev/null || true

set +e
gate_summary "release-postflight"
rc=$?
set -e

if [ "$rc" -eq 0 ]; then
  echo "release-postflight: OK — $VERSION landed everywhere it is supposed to."
else
  echo "release-postflight: the tag is immutable now — fix the named remedies above and re-run to confirm; nothing here un-publishes $VERSION." >&2
fi
exit "$rc"

fi # BASH_SOURCE guard
