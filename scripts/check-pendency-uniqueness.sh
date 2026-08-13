#!/usr/bin/env bash
# check-pendency-uniqueness.sh — refuses a second computation of the
# pendency relation anywhere in the tree (P2 AC3/AC4).
#
# internal/pendency.Resolve is the ONE entry point to the "who owes a move"
# relation (I7). internal/cache/pendency_callsite_test.go already refuses a
# second call site WITHIN internal/cache — that test is package-scoped, so
# it structurally cannot see a second call site growing in another package.
# This gate is the repo-wide half: it does not repeat cache's own
# within-package proof, it extends the same rule to every OTHER package in
# the tree.
#
# It refuses two distinct things:
#
#   1. A pendency.Resolve call outside the sanctioned call site(s) named in
#      CALL_SITE_ALLOWLIST below. Constructing pendency.Input is a decision
#      — which carried facts (ExtraAddressees, ActiveParticipants,
#      LeftParticipants — see pendency_callsite_test.go's own comment on
#      why those three are structurally unrecoverable from outside) get
#      set. Two independent construction sites can decide those facts
#      differently, which is exactly the defect class I7 exists to close:
#      inbox.go and threadview.go disagreed about ExtraAddressees for one
#      commit before pendency_callsite_test.go started catching it.
#
#   2. Any package OTHER than internal/pendency (the relation's home) and
#      internal/cache (the sanctioned caller) importing internal/pendency
#      in NON-test code. internal/html in particular must read cache's
#      already-computed verdict rather than resolving its own —
#      internal/html/assemble.go's own comments at :296 and :1341 already
#      claim exactly that ("never a second pendency.Resolve call"); this
#      gate is what turns the claim into something checkable instead of
#      prose.
#
# Scope note: BOTH rules scan only non-test .go files. A _test.go calling
# pendency.Resolve directly to test the relation's own table — as
# internal/cache/openstates_gate_test.go and internal/pendency's own tests
# both do — is not a second computation of anything; it is calling the
# canonical function to assert against it, which is what a test of the
# relation itself is supposed to do. Forbidding that would forbid testing
# pendency. internal/cache/pendency_callsite_test.go draws the identical
# line (its file walk skips anything ending in "_test.go"); this gate
# matches that precedent rather than inventing a stricter one.
#
# Where this gate ends: both rules are syntactic, not semantic — a call to
# `pendency.Resolve` or an import of the package. Neither can see a THIRD
# derivation shaped like the real defect that already escaped once:
# `exchangeActive` reconstructed "does anybody still owe an acknowledgement"
# straight from `ack_requested`, `to:` and the folded ack set, never calling
# Resolve and never importing internal/pendency at all — a pure
# re-implementation of the relation's IDEA under a different name. That
# evasion is knowingly NOT covered here, measured rather than assumed (the
# same honesty check-view-vocabulary.sh applies to its own four evasions).
# Closing it needs a differential test against pendency's own table, not a
# wider glob on this gate — a stricter import/call check cannot see logic
# that touches neither.
#
# Usage: bash scripts/check-pendency-uniqueness.sh          # check the repo
#        bash scripts/check-pendency-uniqueness.sh --teeth  # self-test

# lane-inputs:
#   **/*.go
#   !**/*_test.go
#   internal/cache/pendency_callsite_test.go
#   docs/decisions.md
#
# The last line re-admits ONE test file the exclusion above would drop. This
# gate does not scan it — it names it, as the within-package half of the same
# rule. Re-declaring it is deliberate over-declaration: if that test is
# weakened or deleted, this gate becomes the only remaining proof and should
# re-run to say so. Over-declaring costs a second; under-declaring is a false
# green.
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   below self-locates scripts/lib/gate-lib.sh from this script's own path, so
#   the classifier cannot resolve the $(dirname ...) substitution to a literal.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# CALL_SITE_ALLOWLIST — files whose pendency.Resolve call is sanctioned, one
# entry per line as "repo-relative/path.go|reason". Repo-root-relative so
# the same entries match whether run_check scans the real repo or a
# --teeth fixture tree built with the identical internal/... layout.
CALL_SITE_ALLOWLIST=(
  "internal/cache/inbox.go|resolveVerdict is cache's own sanctioned resolver home; internal/cache/pendency_callsite_test.go already proves it is the ONLY function in that package calling pendency.Resolve — this entry is what lets that within-package proof and this repo-wide one agree on where the one call site is"
)

# IMPORT_ALLOWLIST — package directories permitted to import internal/pendency
# in non-test code, one entry per line as "repo-relative/dir|reason".
IMPORT_ALLOWLIST=(
  "internal/pendency|the relation's own home; listed for completeness even though a Go package cannot import itself"
  "internal/cache|houses resolveVerdict, the one sanctioned pendency.Resolve call site (see CALL_SITE_ALLOWLIST above) — every other package must read cache's already-computed verdict instead of resolving its own"
)

call_site_allowed() { # $1 = repo-relative file path
  local f="$1" entry path
  for entry in "${CALL_SITE_ALLOWLIST[@]}"; do
    path="${entry%%|*}"
    [ "$f" = "$path" ] && return 0
  done
  return 1
}

import_allowed() { # $1 = repo-relative directory
  local d="$1" entry path
  for entry in "${IMPORT_ALLOWLIST[@]}"; do
    path="${entry%%|*}"
    [ "$d" = "$path" ] && return 0
  done
  return 1
}

run_check() { # $1 = scan root (repo root, or a --teeth fixture tree)
  local root="$1" file rel lineno content dir trimmed
  root="$(cd "$root" && pwd -P)"

  # ADR-001's original cache row does not list pendency. ADR-016 is the
  # explicit narrow grant which makes the allowlist below architecture, not a
  # gate-local exception. Teeth fixtures normally omit decisions.md; when a
  # repository supplies the record, refuse if that exact grant disappears.
  if [ -f "$root/docs/decisions.md" ] &&
      ! grep -Fq 'Boundary grant: `internal/cache` may import `internal/pendency`.' "$root/docs/decisions.md"; then
    gate_fail "docs/decisions.md does not record ADR-016's cache → pendency boundary grant — IMPORT_ALLOWLIST would otherwise contradict ADR-001"
  fi

  while IFS= read -r file; do
    rel="${file#"$root"/}"

    # Rule 1 — pendency.Resolve call sites.
    while IFS=: read -r lineno content; do
      [ -z "$lineno" ] && continue
      trimmed="$(printf '%s' "$content" | sed -E 's/^[[:space:]]+//')"
      if ! call_site_allowed "$rel"; then
        gate_fail "$rel:$lineno calls pendency.Resolve directly (\`$trimmed\`) — this is a second computation of the pendency relation; read the sanctioned call site's already-computed verdict instead (see CALL_SITE_ALLOWLIST in scripts/check-pendency-uniqueness.sh for where that is and why)"
      fi
    done < <(grep -nE 'pendency\.Resolve[[:space:]]*\(' "$file" 2>/dev/null || true)

    # Rule 2 — internal/pendency imports.
    while IFS=: read -r lineno content; do
      [ -z "$lineno" ] && continue
      trimmed="$(printf '%s' "$content" | sed -E 's/^[[:space:]]+//')"
      dir="$(dirname "$rel")"
      if ! import_allowed "$dir"; then
        gate_fail "$rel:$lineno imports internal/pendency from outside the sanctioned homes (\`$trimmed\`) — read the verdict internal/cache already computed instead of resolving your own (see IMPORT_ALLOWLIST in scripts/check-pendency-uniqueness.sh)"
      fi
    done < <(grep -nE '"[^"]*/internal/pendency"' "$file" 2>/dev/null || true)
  # PRUNE the trees that are not this repository's source. `.a2a/` is the
  # project's own gitignored working directory, and `a2a`'s feedback reader
  # clones the PUBLIC repo into `.a2a/cache/feedback-repo/<slug>/` — a full
  # copy of this codebase, whose `internal/cache/inbox.go` then trips this
  # gate against itself. Found 2026-08-12 by running the feedback loop
  # end-to-end: `make check-validators` went red on any machine that had ever
  # read feedback, naming a file nobody had edited. The walk is by filesystem,
  # not by `git ls-files`, so being gitignored was no protection at all.
  #
  # Same prune idiom as check-provider-tier-deferral.sh, plus `.a2a`.
  done < <(find "$root" \( -path '*/.git' -o -path '*/.a2a' -o -name node_modules \) -prune -o \
    -type f -name '*.go' ! -name '*_test.go' -print)

  gate_summary "pendency-uniqueness"
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || { echo "pendency-uniqueness --teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN

  # Fixture 1 — a second pendency.Resolve call site outside the allowlist
  # must red, ISOLATED from Rule 2: this file lives in internal/cache, which
  # IS in IMPORT_ALLOWLIST, so only an unsanctioned CALL SITE can red it.
  # Deliberately the historically real shape — pendency_callsite_test.go's
  # own comment records that inbox.go and threadview.go both resolved for
  # one commit, disagreeing about ExtraAddressees, before that within-
  # package gate started catching it. A file under internal/html here would
  # ALSO red via Rule 2's import check, so it would prove nothing about
  # Rule 1 in isolation — confirmed by deleting Rule 1's block entirely and
  # rerunning --teeth: it stayed green, because Rule 2 alone reddened both
  # fixture 1 and fixture 2.
  #
  # Each fixture runs run_check in its own SUBSHELL: gate-lib's error
  # counters are process-global, so a second run_check call in the same
  # shell would inherit the first one's tally and every later fixture
  # would red (or stay green) for the wrong reason.
  mkdir -p "$tmp/internal/cache"
  cat > "$tmp/internal/cache/threadview.go" <<'FIXTURE'
package cache

import "github.com/ydnikolaev/a2ahub/internal/pendency"

func resolveVerdictAgain() {
	_, _ = pendency.Resolve(pendency.Input{})
}
FIXTURE
  if ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "pendency-uniqueness --teeth: FAILED — an unsanctioned pendency.Resolve call site stayed green" >&2
    return 1
  fi
  rm -rf "$tmp/internal"

  # Fixture 2 — importing internal/pendency from outside the sanctioned
  # homes must red even without a literal Resolve( call — Rule 2 is
  # import-based, not call-based, because a package could import it and use
  # a different exported symbol without ever writing "pendency.Resolve(".
  # The import itself is the tell.
  mkdir -p "$tmp/internal/html"
  cat > "$tmp/internal/html/waiting.go" <<'FIXTURE'
package html

import "github.com/ydnikolaev/a2ahub/internal/pendency"

var _ = pendency.Verdict{}
FIXTURE
  if ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "pendency-uniqueness --teeth: FAILED — an unsanctioned internal/pendency import stayed green" >&2
    return 1
  fi
  rm -rf "$tmp/internal"

  # Fixture 3 — the import allowlist must remain tied to an architecture
  # decision rather than becoming a gate-local boundary exception.
  mkdir -p "$tmp/docs"
  printf '%s\n' '# decisions without the cache-to-pendency grant' > "$tmp/docs/decisions.md"
  if ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "pendency-uniqueness --teeth: FAILED — a repository missing ADR-016's cache → pendency grant stayed green" >&2
    return 1
  fi
  printf '%s\n' 'Boundary grant: `internal/cache` may import `internal/pendency`.' > "$tmp/docs/decisions.md"
  if ! ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "pendency-uniqueness --teeth: FAILED — the recorded cache → pendency grant was refused" >&2
    return 1
  fi
  rm -rf "$tmp/docs"

  # Fixture 4 — the real, sanctioned shape (internal/pendency defines
  # Resolve, internal/cache/inbox.go's resolveVerdict is the one caller)
  # must stay green.
  mkdir -p "$tmp/internal/pendency" "$tmp/internal/cache"
  cat > "$tmp/internal/pendency/pendency.go" <<'FIXTURE'
package pendency

type Input struct{}
type Verdict struct{}

func Resolve(in Input) (Verdict, error) {
	return Verdict{}, nil
}
FIXTURE
  cat > "$tmp/internal/cache/inbox.go" <<'FIXTURE'
package cache

import "github.com/ydnikolaev/a2ahub/internal/pendency"

func resolveVerdict() {
	_, _ = pendency.Resolve(pendency.Input{})
}
FIXTURE
  if ! ( run_check "$tmp" ) >/dev/null 2>&1; then
    echo "pendency-uniqueness --teeth: FAILED — the sanctioned call site/import shape was refused" >&2
    return 1
  fi

  echo "pendency-uniqueness --teeth: ok"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

run_check "$GATE_ROOT"
exit $?
