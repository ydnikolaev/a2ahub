#!/usr/bin/env bash
# The two things that must be true before a2ahub's server.json may be
# published to the MCP Registry, as a SCRIPT rather than as shell inlined in
# publish-mcp.yml — because inline workflow shell that only a dispatch ever
# runs is exactly how v0.26.0 and v0.26.1 shipped broken (see
# scripts/tests/build_mcpb_test.sh's header). A predicate in a file can be
# executed by a test; the same predicate in a `run:` block cannot.
#
# WHY THESE TWO. The registry FREEZES what it accepts: a version is unique per
# publication and metadata cannot be edited afterwards. So a row published
# against the wrong tag, or against a release carrying no bundles, is not a
# mistake that can be corrected — only superseded by another version.
#
#   1. the tag input is shaped vX.Y.Z. The first real dispatch, 2026-08-31,
#      was typed `0.26.2` and died on a bare `release not found` that named
#      neither the input nor the shape it wanted.
#   2. the release's own signed checksum set names at least one .mcpb. Every
#      package row points at one; v0.26.0 and v0.26.1 were both tagged,
#      released and carrying none.
#
# lane-inputs: this script judges only its ARGUMENTS, never repository state.
# It is reached by publish-mcp.yml and by its own --teeth; no diff selects it.
# lane-inputs: NEVER
# lane-reason: a precondition check over a dispatch input and a downloaded
#   checksum set — it reads no tracked path, so no change to the repository
#   can alter its verdict.
set -uo pipefail

usage() {
  echo "usage: check-mcp-publish-preconditions.sh <tag> <sha256sums-path>" >&2
  echo "       check-mcp-publish-preconditions.sh --teeth" >&2
}

# ci_error prints in the runner's own vocabulary when it is one, so a refusal
# is surfaced as an annotation rather than buried in a log — the same
# GITHUB_ACTIONS branch scripts/lib/gate-lib.sh takes.
ci_error() {
  if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
    echo "::error::$*"
  else
    echo "check-mcp-publish-preconditions: $*" >&2
  fi
}

check_tag() { # $1 = tag
  case "${1:-}" in
    v[0-9]*.[0-9]*.[0-9]*) return 0 ;;
  esac
  ci_error "tag \"${1:-}\" is not shaped vX.Y.Z. The releases this publishes from are tagged WITH the leading v (v0.26.2), while releasenotes/*.yaml drop it (0.26.2.yaml); this input wants the TAG. Re-run naming it exactly as the release does."
  return 1
}

check_bundles() { # $1 = tag, $2 = SHA256SUMS path
  local tag="${1:-}" sums="${2:-}"
  if [ ! -r "$sums" ]; then
    ci_error "no readable checksum set at \"$sums\" for $tag. That file is uploaded by release.yml's cohort job — if it is absent, that job did not finish, and the bundles this row points at do not exist either."
    return 1
  fi
  local n
  n="$(grep -c '\.mcpb$' "$sums")" || n=0
  if [ "${n:-0}" -eq 0 ]; then
    ci_error "$tag's checksum set names no .mcpb bundle, so every package row would point at an asset that does not exist. The registry cannot edit metadata after publication. Publish from a tag whose cohort job succeeded."
    return 1
  fi
  echo "check-mcp-publish-preconditions: $tag is well-formed and carries $n .mcpb bundle(s)."
}

run_teeth() {
  local tmp; tmp="$(mktemp -d)" || { echo "--teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN
  local failures=0
  bad() { echo "check-mcp-publish-preconditions --teeth: FAIL — $1" >&2; failures=$((failures + 1)); }

  # T1 — the shape that actually happened: no leading v.
  if check_tag "0.26.2" 2>/dev/null; then bad "T1: \"0.26.2\" was accepted; the leading v is part of the tag's identity"; fi
  # T2 — the good shape.
  check_tag "v0.26.2" 2>/dev/null || bad "T2: \"v0.26.2\" was refused"
  # T3 — empty, and a branch name, and a bare v.
  for t in "" "main" "v" "0.26" "release-0.26.2"; do
    if check_tag "$t" 2>/dev/null; then bad "T3: \"$t\" was accepted as a tag"; fi
  done
  # T4 — the refusal NAMES the value it was given, or it is no better than
  # the `release not found` it replaces.
  local out
  out="$(check_tag "0.26.2" 2>&1)"
  printf '%s' "$out" | grep -q '0.26.2' || bad "T4: the refusal does not quote the value it rejected"

  # T5 — a checksum set carrying bundles passes; T6 — one carrying none reds.
  printf 'abc  a2a-linux-amd64\ndef  a2a-linux-amd64.mcpb\n' > "$tmp/withbundles"
  check_bundles v0.26.2 "$tmp/withbundles" >/dev/null 2>&1 || bad "T5: a checksum set naming a .mcpb was refused"
  printf 'abc  a2a-linux-amd64\ndef  SHA256SUMS.cosign.bundle\n' > "$tmp/nobundles"
  if check_bundles v0.26.1 "$tmp/nobundles" >/dev/null 2>&1; then bad "T6: a checksum set naming no .mcpb was accepted — this is v0.26.0's and v0.26.1's exact state"; fi

  # T7 — an absent checksum set is refused as a MISSING PRECONDITION naming
  # the cohort job, never silently treated as "no bundles".
  out="$(check_bundles v0.26.2 "$tmp/does-not-exist" 2>&1)"
  { [ -n "$out" ] && printf '%s' "$out" | grep -q "cohort job"; } || bad "T7: an absent checksum set did not name the job that produces it"

  # T8 — `.mcpb` must be matched at END of line, so an asset merely mentioning
  # it (a signature over one, say) cannot satisfy the precondition alone.
  printf 'abc  a2a-linux-amd64.mcpb.cosign.bundle\n' > "$tmp/onlysig"
  if check_bundles v0.26.2 "$tmp/onlysig" >/dev/null 2>&1; then bad "T8: a signature OVER a bundle was counted as the bundle"; fi

  if [ "$failures" -gt 0 ]; then
    echo "check-mcp-publish-preconditions --teeth: $failures assertion(s) failed" >&2
    return 1
  fi
  echo "check-mcp-publish-preconditions --teeth: ok — a tag without the leading v is refused NAMING the value given (the shape the first real dispatch actually took), a well-formed tag passes, empty/branch/partial forms are refused, a checksum set with a .mcpb passes, one with none reds (v0.26.0's and v0.26.1's exact state), an absent set is refused naming the cohort job, and a signature over a bundle does not count as the bundle"
}

case "${1:-}" in
  --teeth) run_teeth; exit $? ;;
  "") usage; exit 2 ;;
  *)
    [ $# -eq 2 ] || { usage; exit 2; }
    check_tag "$1" || exit 1
    check_bundles "$1" "$2" || exit 1
    ;;
esac
