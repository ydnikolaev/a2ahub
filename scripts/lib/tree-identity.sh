# shellcheck shell=bash
# tree-identity.sh — the ONE definition of a tree identity.
#
# verify.sh records it beside every pass (the `tree` field of its telemetry
# line); deliver.sh recomputes it from git objects to learn whether a commit's
# bytes were judged (fast-loop-2026-10 P1, T2). They source this file, so the
# two cannot drift: a different formula in either would make every recorded
# identity silently stop matching.
#
# tree_identity <head> <mode> <lane_files>
#
# reads one line per changed path on stdin:
#
#   <blob> <path>     the path's content hash (`git hash-object` of the working
#                     file, or `git rev-parse <commit>:<path>` of a commit)
#   absent <path>     the path is not a readable regular file (a deletion)
#
# and prints the git hash of
#
#   head=<head>\nmode=<mode>\nlane_files=<lane_files>\n<those lines>\n
#
# It is a hash over the lines it is given, so the CALLER owns which paths are
# listed and in which order (sorted, `LC_ALL=C sort -u`). An empty stdin is the
# identity of a clean tree. `ROOT` names the repository `git hash-object` runs
# in, as it always has in verify.sh.
#
# Sourced, not executed: it defines one function and has no top-level effect.
tree_identity() {
  local head="$1" mode="$2" lane_files="$3" hashes
  # Captured into a variable on purpose: `$(...)` strips the trailing newline,
  # and the identity recorded since release-cost-2026-08 P2 was computed over
  # exactly that text. Changing it would orphan every record already written.
  hashes="$(cat)"
  printf 'head=%s\nmode=%s\nlane_files=%s\n%s\n' \
    "$head" "$mode" "$lane_files" "$hashes" |
    git -C "${ROOT:-.}" hash-object --stdin 2>/dev/null
}
