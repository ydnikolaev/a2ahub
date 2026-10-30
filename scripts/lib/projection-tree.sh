#!/usr/bin/env bash
# projection-tree.sh — "a commit's tree minus its own strip set", from git
# objects alone. Sourced, never executed.
#
# Readers: the publisher's identity proof and its carry-receipt consumer
# (docs/runbooks/publish-to-public.sh), and the carry's own plan
# (scripts/ci-parity.sh --carry). ci-parity.sh ships and the publisher is
# stripped, so the shared primitive lives here rather than in the publisher: a
# public gate may not source a private file (classify-guard check 5).

# The strip set a COMMIT lists, one path per line. Restates the rule of
# scripts/check-projection.sh's strip_entries (comments and blanks dropped) for
# a git object instead of a worktree; that script is itself on the strip list,
# so this file cannot source it. One rule, two readers, no third variant.
candidate_strip_entries() { # $1 = commit
  # `|| true`: grep exits 1 on no match; the caller owns the empty-set refusal.
  { git show "$1:scripts/lib/strip-set.txt" 2>/dev/null || true; } \
    | grep -Ev '^[[:space:]]*(#|$)' || true
}

# candidate_projection_tree <commit> — prints the git tree of <commit> with the
# strip set removed, the strip set being the one THAT commit lists. The single
# primitive behind two readers: candidate_identity_check (is the candidate the
# judged commit's projection?) and the carry receipt (do HEAD and its receipted
# ancestor project to the same tree?, scripts/ci-parity.sh --carry). Run from
# inside the repository that holds <commit>.
#
# Every failing command is checked explicitly: callers run this inside a
# command substitution, which does not inherit `set -e`.
candidate_projection_tree() { # $1 = commit
  local sha="$1" entry tmp idx tree
  local -a strip=()
  if ! git cat-file -e "${sha}^{commit}" 2>/dev/null; then
    echo "publish: ABORT — commit $sha is not readable here, so its projection tree could not be derived." >&2
    return 1
  fi
  while IFS= read -r entry; do
    [ -n "$entry" ] && strip+=("$entry")
  done < <(candidate_strip_entries "$sha")
  # The same false-green guard the publisher and check-projection.sh carry: an
  # empty set removes nothing, and every comparison made on the result would
  # then pass against a tree nobody stripped.
  if [ "${#strip[@]}" -eq 0 ]; then
    echo "publish: ABORT — $sha lists an EMPTY strip set (scripts/lib/strip-set.txt absent or all comments); the identity proof would have removed nothing." >&2
    return 1
  fi
  # Built in a throwaway index. `--literal-pathspecs` because git's default is
  # glob and filter-repo's --path is not; `-f` because this index is
  # deliberately unrelated to the HEAD of whatever checkout hosts it.
  tmp="$(mktemp -d)"
  idx="$tmp/index"
  if ! GIT_INDEX_FILE="$idx" git read-tree "$sha" ||
     ! GIT_INDEX_FILE="$idx" git --literal-pathspecs rm --cached -r -q -f --ignore-unmatch -- "${strip[@]}" >/dev/null ||
     ! tree="$(GIT_INDEX_FILE="$idx" git write-tree)"; then
    rm -rf "$tmp"
    echo "publish: ABORT — could not derive the expected tree of $sha minus its strip set." >&2
    return 1
  fi
  rm -rf "$tmp"
  printf '%s\n' "$tree"
}
