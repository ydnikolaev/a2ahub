#!/usr/bin/env bash
# check-release-record.sh — the everyday-tree gate B12 was signed for
# (agent-exchange-2026-08 B12, operator 2026-08-09) and never got, until
# one-answer-2026-08 P6. Offline, no VERSION argument, every commit.
#
# scripts/release-preflight.sh already asserts most of the release-record
# invariants — but ONLY when someone runs `make release-preflight VERSION=…`,
# by hand, with the network, and it is explicitly NOT part of `make check`
# (Makefile:254). That gap is B12's whole subject: a tree that never reaches
# a release ceremony was never checked at all, and two of its own facts have
# been wrong since the notes were authored (see the AC1 red output this gate
# produces on today's tree).
#
# Three refusals, each keyed off a different discriminator:
#
#   1. released-without-tag  — a releasenotes/<v>.yaml carries a `released:`
#      DATE for a version with no `git tag` reachable from this checkout.
#      Notes may NAME an uncut version (docs/runbooks/release.md Phase 1 step
#      1); they may not claim it shipped on a date it hasn't. The sentinel
#      (`released: unreleased` — internal/notes.UnreleasedSentinel,
#      schemas/release-notes/v1/release-notes.schema.json) is what keeps that
#      window honest without blocking authoring ahead of the tag.
#
#   2. a2a-ref-default-without-tag — .github/workflows/a2a-validate-
#      reusable.yml's `a2a-ref` input names a version with no tag. A space's
#      caller passes no `a2a-ref` of its own (verified against `r22d222/a2a`
#      live — see that file's own header), so this default IS the validator
#      binary that runs at every space's merge gate the moment a tag carrying
#      it is published. Scoped to exactly this ONE file — not every
#      `*-reusable.yml` under .github/workflows/ — because this phase's
#      footprint grants write access to only this one (spec 06 Footprint);
#      the fleet-wide loop over every reusable workflow remains
#      release-preflight.sh's `assert_ref_default_matches` job, at the
#      ceremony tier, where the footprint to fix every file it finds exists.
#      Reuses `_ref_default_of` from release-preflight.sh (sourced below,
#      guarded so sourcing runs no ceremony) rather than re-deriving the
#      a2a-ref parsing — see that function's own doc comment for why the
#      PREDICATE differs (tag-existence here; equals-an-explicit-VERSION
#      there) even though the extraction is identical.
#
#   3. floor/pin moved backwards — space-template/space.yaml's
#      min_binary_version, or a space-template/.github/workflows/*.yml pin's
#      `@vX.Y.Z`, is LOWER than it was at the previous commit (HEAD~1). The
#      write floor and the workflow pins only ever move forward in normal
#      operation (`make space-template-baseline`, runbook Phase 4 step 15);
#      a backwards move is either an operator error or a bad revert, and
#      nothing else in this repository would notice one landing on main.
#      Compared against the WORKING TREE as "current" (not just HEAD), so a
#      local `make check` catches the regression before it is even
#      committed — that is strictly more useful than HEAD-vs-HEAD~1 and
#      degrades to exactly that comparison the moment the tree is clean.
#
# A shallow clone (or any checkout with no `v*` tags fetched, or with no
# parent commit for HEAD) cannot judge refusals 1/2 (no tag set to check
# against) or refusal 3 (no previous commit to diff against) — and REFUSES
# rather than silently passing. "Cannot check" is never "check passed": a
# false green here is worse than the red it would otherwise produce, the same
# reasoning scripts/check-provider-tier-deferral.sh's own shallow-checkout
# note gives (that file's `--is-shallow-repository` finding is why THIS gate
# keys on "zero tags found" / "no HEAD~1", not on `--is-shallow-repository`
# itself — this repository's own working tree answers that flag `true` while
# carrying 1000+ commits, so it is the wrong discriminator here too).
#
# lane-inputs:
#
#	releasenotes/*.yaml
#	.github/workflows/a2a-validate-reusable.yml
#	space-template/space.yaml
#	space-template/.github/workflows/*.yml
#
# lane-reads-opaque: the tag set (`git tag -l 'v*'`) and HEAD's parent
# commit are non-path inputs — a tag being cut, or a new commit landing with
# no diff to the globs above, flips this gate's verdict on its own.
#
# Usage:
#   bash scripts/check-release-record.sh          # the everyday check
#   bash scripts/check-release-record.sh --teeth  # offline self-test
set -euo pipefail

# Sourced for _ref_default_of only — see refusal 2's header note above. The
# BASH_SOURCE guard at the bottom of release-preflight.sh means this does not
# run that file's ceremony (it needs an explicit VERSION and the network).
# shellcheck source=scripts/release-preflight.sh
source "$(dirname "${BASH_SOURCE[0]}")/release-preflight.sh"

UNRELEASED_SENTINEL="unreleased"
A2A_REF_WORKFLOW=".github/workflows/a2a-validate-reusable.yml"
TEMPLATE_MANIFEST="space-template/space.yaml"
TEMPLATE_WORKFLOWS_DIR="space-template/.github/workflows"

# ── shared readers ────────────────────────────────────────────────────────

# _released_field prints releasenotes/<v>.yaml's top-level `released:` scalar
# with any quoting stripped ('2026-08-13', "2026-08-13", or unreleased alike).
_released_field() { # $1 = path
  sed -n "s/^released:[[:space:]]*['\"]\\{0,1\\}\\([^'\"]*\\)['\"]\\{0,1\\}[[:space:]]*\$/\\1/p" "$1" 2>/dev/null | head -1
}

# _min_binary_version_from reads space-template/space.yaml's
# min_binary_version off STDIN — the same field release-preflight.sh's
# assert_floor_not_ahead reads from a file directly; this reads from stdin so
# the identical sed can be pointed at either the working tree or a
# `git show HEAD~1:...` blob without a second copy of the pattern.
_min_binary_version_from() { # stdin
  sed -n 's/^min_binary_version:[[:space:]]*"\{0,1\}\([0-9][0-9.]*\)"\{0,1\}.*/\1/p' | head -1
}

# _pin_versions_from prints every `-reusable.yml@vX.Y.Z` version token found
# on STDIN, one per line — every reusable-workflow pin a space-template
# caller file declares, whichever workflow it names.
_pin_versions_from() { # stdin
  grep -oE -- '-reusable\.yml@v?[0-9]+(\.[0-9]+)+' 2>/dev/null | sed -E 's/.*@v?//'
}

# _version_lt: true when $1 sorts strictly before $2 as a vX.Y.Z version.
# Used by refusal 1's immutable-history carve-out (ADR-011) — sort -V is the
# same comparator the release runbook's own ordering assumes.
_version_lt() { # $1 = a, $2 = b (both bare X.Y.Z)
  [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" = "$1" ]
}

# _newest_tag prints the highest vX.Y.Z-shaped tag in $1, or nothing.
_newest_tag() { # $1 = repo
  git -C "$1" tag -l 'v*' 2>/dev/null | grep -E '^v[0-9]+(\.[0-9]+){2}$' | sort -V | tail -1
}

# _has_any_version_tag: does $1 carry at least one vX.Y.Z tag at all? The
# discriminator for the shallow-clone / untagged-checkout refusal — see the
# header comment for why this, and not `--is-shallow-repository`.
_has_any_version_tag() { # $1 = repo
  git -C "$1" tag -l 'v*' 2>/dev/null | grep -qE '^v[0-9]+(\.[0-9]+){2}$'
}

# _is_behind: is $1 strictly less than $2 by `sort -V` (semver-ish, dotted —
# never lexicographic: a string compare would rank 0.9.1 above 0.10.0)?
_is_behind() { # $1=cur $2=prev
  [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1)" = "$2" ]
}

# ── refusal 1: released: date claimed for an untagged version ──────────────

assert_released_dates_match_tags() { # $1 = repo
  local repo="$1" f version released newest rc=0 checked=0

  if ! _has_any_version_tag "$repo"; then
    fail "check-release-record: this checkout carries no vX.Y.Z tags at all — a shallow clone
    (actions/checkout's default fetch-depth: 1) does not fetch tags, and this check cannot tell
    'released: DATE for a version authored ahead of its tag' apart from 'released: DATE for a
    version that was actually cut' without the tag set. cannot check is never check passed: use
    fetch-depth: 0, or fetch --tags first."
    return 1
  fi

  for f in "$repo"/releasenotes/*.yaml; do
    [ -e "$f" ] || continue
    checked=$((checked + 1))
    version="$(basename "$f" .yaml)"
    released="$(_released_field "$f")"

    if [ -z "$released" ] || [ "$released" = "$UNRELEASED_SENTINEL" ]; then
      ok "check-release-record: releasenotes/$version.yaml carries no released date yet (the $UNRELEASED_SENTINEL sentinel or absent) — nothing to check against a tag"
      continue
    fi

    if ! git -C "$repo" rev-parse -q --verify "refs/tags/v$version" >/dev/null 2>&1; then
      newest="$(_newest_tag "$repo")"
      # ADR-011: no rule judges immutable history. A version OLDER than the
      # newest existing tag was either cut under a different number or
      # abandoned; either way the release did or did not happen years of
      # commits ago and no edit today changes it. Only a version AHEAD of the
      # newest tag is a live claim about a cut that has not happened yet —
      # that is the one this gate exists to refuse. Measured 2026-08-20:
      # releasenotes/0.12.0.yaml claims released: 2026-07-28 with no v0.12.0
      # tag while v0.11.0 and v0.13.0 both exist, so its content shipped under
      # another number. Refusing it would demand rewriting a settled record;
      # reporting it is what is actually useful.
      if [ -n "$newest" ] && _version_lt "$version" "${newest#v}"; then
        ok "check-release-record: releasenotes/$version.yaml claims released: $released with no v$version tag, but $version predates the newest tag $newest — immutable history, not judged (ADR-011). Reported, not refused."
        continue
      fi
      # THE CUT WINDOW. A date without a tag is a lie about the PAST — except
      # for the hours between stamping the date and creating the tag, when it
      # is a statement about the next few minutes.
      #
      # The window is not avoidable by ordering. The tag is created at the
      # CANDIDATE's SHA (release.md Phase 3 step 13), so whatever the candidate
      # carries is what gets tagged; the date must be inside the candidate, and
      # the candidate is published before any tag exists. Meanwhile
      # `release-preflight` REFUSES the sentinel at cut time, because a tagged
      # version whose date is not a date kills the whole public site — not
      # hypothetical: v0.23.0 shipped that way on 2026-08-20 and pages.yml has
      # been failing with `RangeError: Invalid time value` since.
      #
      # So this gate and release-preflight demanded opposite things of one
      # field and both were right. This gate was authored 2026-08-20, the same
      # day v0.23.0 was cut; v0.24.0 is the FIRST release to meet it, and it
      # blocked the candidate's own `make check` — which the provider-tier
      # deferral requires as its evidence.
      #
      # TODAY is the discriminator, and it is deliberately self-closing. A cut
      # in progress stamps today; a lie about a ship date is about a day that
      # has already passed. If the tag never lands this greens for hours and
      # reds tomorrow — an abandoned cut is caught by the same rule, one day
      # later, with no state to keep and nothing to remember to clean up.
      #
      # NOT the candidate ref on the remote, which would be better evidence:
      # this gate runs offline on every commit, by design.
      if [ "$released" = "$(date -u +%Y-%m-%d)" ]; then
        ok "check-release-record: releasenotes/$version.yaml is dated today ($released) with no v$version tag — the cut window (Phase 1 stamps the date, Phase 3 tags the candidate's SHA). Legal today; reds tomorrow if the tag never lands."
        continue
      fi

      fail "check-release-record: releasenotes/$version.yaml claims released: $released, but v$version
    is not a tag reachable from this checkout. The newest existing tag is ${newest:-<none>}.
    Notes may NAME an uncut version (docs/runbooks/release.md Phase 1 step 1); they may not claim it
    was RELEASED on a date it has not been — that is the discriminator this gate exists to check
    (one-answer-2026-08 P6, agent-exchange-2026-08 B12).
    Fix: set released: $UNRELEASED_SENTINEL in releasenotes/$version.yaml until v$version is actually tagged."
      rc=1
      continue
    fi
    ok "check-release-record: releasenotes/$version.yaml's released: $released matches tag v$version"
  done

  if [ "$checked" -eq 0 ]; then
    fail "check-release-record: no releasenotes/*.yaml found under $repo/releasenotes —
    the corpus scan is broken, not the notes."
    return 1
  fi
  return "$rc"
}

# ── refusal 2: a2a-ref default naming an untagged version ──────────────────

assert_ref_default_is_tagged() { # $1 = repo
  local repo="$1" got tag newest

  if ! _has_any_version_tag "$repo"; then
    fail "check-release-record: this checkout carries no vX.Y.Z tags at all — a shallow clone
    does not fetch tags, and this check cannot tell an $A2A_REF_WORKFLOW default naming a
    published tag apart from one naming nothing real. cannot check is never check passed: use
    fetch-depth: 0, or fetch --tags first."
    return 1
  fi

  got="$(_ref_default_of "$repo" "$A2A_REF_WORKFLOW")"
  if [ -z "$got" ]; then
    fail "check-release-record: $A2A_REF_WORKFLOW declares no a2a-ref default —
    every space caller would then have to name a validator version itself."
    return 1
  fi

  tag="v${got#v}"
  if ! git -C "$repo" rev-parse -q --verify "refs/tags/$tag" >/dev/null 2>&1; then
    newest="$(_newest_tag "$repo")"
    # The runbook's step 2 sets this default to the version being cut, and
    # step 12 cuts the tag — so between them the default LEGITIMATELY names an
    # uncut version, and a gate that refused that would red every real release
    # sequence. The discriminator is the same one refusal 1 uses, keyed to the
    # version THIS DEFAULT NAMES rather than to the newest authored one (they
    # diverge the moment two versions are authored ahead of a cut): while that
    # version's own notes carry the $UNRELEASED_SENTINEL, the tree is mid-sequence
    # and this is legal. Once those notes claim a DATE, the record says the
    # release happened and the tag must exist.
    # This is also what keeps internal/notes'
    # TestReusableRefDefaultMatchesTheNewestAuthoredVersion true: that test
    # holds the default equal to the newest AUTHORED version, and this gate no
    # longer contradicts it.
    notes="$repo/releasenotes/${got#v}.yaml"
    if [ -f "$notes" ] && [ "$(_released_field "$notes")" = "$UNRELEASED_SENTINEL" ]; then
      ok "check-release-record: $A2A_REF_WORKFLOW's a2a-ref default $got has no tag yet, and releasenotes/${got#v}.yaml carries the $UNRELEASED_SENTINEL sentinel — mid-release-sequence, legal (docs/runbooks/release.md Phase 1 steps 1-2)"
      return 0
    fi
    # The same cut window the released-date check admits, for the same reason
    # and with the same self-closing bound: once Phase 1 stamps the date the
    # sentinel is gone, and this row would otherwise red for the hours between
    # that stamp and Phase 3's tag while the release proceeds correctly.
    if [ -f "$notes" ] && [ "$(_released_field "$notes")" = "$(date -u +%Y-%m-%d)" ]; then
      ok "check-release-record: $A2A_REF_WORKFLOW's a2a-ref default $got has no tag yet, and releasenotes/${got#v}.yaml is dated today — the cut window, legal (release.md Phase 1 step 2 through Phase 3 step 13). Reds tomorrow if the tag never lands."
      return 0
    fi
    fail "check-release-record: $A2A_REF_WORKFLOW's a2a-ref default is $got, but $tag is not a tag
    reachable from this checkout. The newest existing tag is ${newest:-<none>}.
    A space's caller passes no a2a-ref of its own, so this default IS the validator that runs at every
    space's merge gate the moment a tag carrying it is published — naming an unpublished tag makes the
    reusable workflow unresolvable and GitHub reports a run with NO JOBS rather than a failing check
    (this happened on 2026-07-26, docs/runbooks/release.md's preamble).
    Fix: point the default at ${newest:-the newest published tag} until $tag is actually cut
    (docs/runbooks/release.md Phase 1 step 2 moves it back to the version being cut at that point)."
    return 1
  fi
  ok "check-release-record: $A2A_REF_WORKFLOW's a2a-ref default $got names an existing tag"
}

# ── refusal 3: the floor or a template pin moved backwards ─────────────────

assert_floor_and_pins_not_backwards() { # $1 = repo
  local repo="$1" rc=0

  if ! git -C "$repo" rev-parse -q --verify HEAD~1 >/dev/null 2>&1; then
    fail "check-release-record: this checkout has no parent commit for HEAD (a single-commit checkout,
    e.g. actions/checkout's default fetch-depth: 1) — the floor/pin-direction check compares the working
    tree to the PREVIOUS commit and cannot judge 'backwards' with no previous commit to read.
    cannot check is never check passed: use fetch-depth: 0."
    return 1
  fi

  local cur_floor prev_floor
  cur_floor=""
  [ -f "$repo/$TEMPLATE_MANIFEST" ] && cur_floor="$(_min_binary_version_from < "$repo/$TEMPLATE_MANIFEST")"
  prev_floor="$(git -C "$repo" show "HEAD~1:$TEMPLATE_MANIFEST" 2>/dev/null | _min_binary_version_from || true)"

  if [ -z "$prev_floor" ]; then
    ok "check-release-record: $TEMPLATE_MANIFEST has no min_binary_version at the previous commit — nothing to compare"
  elif [ -z "$cur_floor" ]; then
    fail "check-release-record: $TEMPLATE_MANIFEST declared min_binary_version $prev_floor at the previous
    commit and declares none now — that is a regression to undefined, not merely backwards.
    Fix: restore min_binary_version >= $prev_floor."
    rc=1
  elif _is_behind "$cur_floor" "$prev_floor"; then
    fail "check-release-record: $TEMPLATE_MANIFEST's min_binary_version moved BACKWARDS, from $prev_floor
    (the previous commit) to $cur_floor. The write floor only ever moves forward in normal operation
    (\`make space-template-baseline\`, docs/runbooks/release.md Phase 4 step 15); a backwards move is
    unnoticed unless something asks.
    Fix: restore it to >= $prev_floor, or state explicitly why this is an intentional rollback."
    rc=1
  else
    ok "check-release-record: $TEMPLATE_MANIFEST's min_binary_version ($cur_floor) has not moved backwards from $prev_floor"
  fi

  local f base cur_pin prev_pin
  for f in "$repo/$TEMPLATE_WORKFLOWS_DIR"/*.yml; do
    [ -e "$f" ] || continue
    base="${f#"$repo"/}"
    cur_pin="$(_pin_versions_from < "$f" | sort -V | head -1)"
    [ -n "$cur_pin" ] || continue
    prev_pin="$(git -C "$repo" show "HEAD~1:$base" 2>/dev/null | _pin_versions_from | sort -V | head -1 || true)"

    if [ -z "$prev_pin" ]; then
      ok "check-release-record: $base has no reusable-workflow pin at the previous commit — nothing to compare"
    elif _is_behind "$cur_pin" "$prev_pin"; then
      fail "check-release-record: $base pins a reusable workflow at v$cur_pin, BACKWARDS from v$prev_pin
    at the previous commit. A space that takes this template now runs an OLDER validator/notifier than it
    did before, unnoticed unless something asks.
    Fix: restore the pin to >= v$prev_pin."
      rc=1
    else
      ok "check-release-record: $base's pin (v$cur_pin) has not moved backwards from v$prev_pin"
    fi
  done

  return "$rc"
}

# ── teeth: the gate must go RED on a violating fixture (offline) ───────────

teeth() {
  local tmp solo
  tmp="$(mktemp -d)" || { echo "check-release-record --teeth: mktemp failed" >&2; exit 1; }
  # A SIBLING temp dir, not nested under $tmp — teeth 8's single-commit repo
  # must not become an embedded git repository inside $tmp's own worktree
  # (which `git add -A` there would then warn about and track).
  solo="$(mktemp -d)" || { echo "check-release-record --teeth: mktemp failed" >&2; exit 1; }
  trap 'rm -rf "$tmp" "$solo"' RETURN

  git -C "$tmp" init -q -b main
  git -C "$tmp" config user.email teeth@example.com
  git -C "$tmp" config user.name teeth

  # ── shallow-clone / no-tags refusal, BEFORE any tag exists ───────────────
  mkdir -p "$tmp/releasenotes" "$tmp/.github/workflows" "$tmp/space-template/.github/workflows"
  printf 'schema: release-notes/v1\nversion: "0.1.0"\nreleased: unreleased\nheadline: h\nchanges:\n  - id: RN-1\n    kind: feat\n    impact: low\n    subject: s\n    detail: d\n    action:\n      scope: none\n      why: y\n' \
    > "$tmp/releasenotes/0.1.0.yaml"
  printf 'on:\n  workflow_call:\n    inputs:\n      a2a-ref:\n        type: string\n        default: "v0.1.0"\n' \
    > "$tmp/.github/workflows/a2a-validate-reusable.yml"
  git -C "$tmp" add -A && git -C "$tmp" commit -qm "no tags yet"

  if out="$(assert_released_dates_match_tags "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — released-date check stayed GREEN with zero tags in the checkout" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "no vX.Y.Z tags at all" || {
    echo "check-release-record --teeth: FAILED — red, but not with the no-tags message" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 1: releasenotes checked with zero tags in the checkout → RED (cannot check is never check passed)"

  if out="$(assert_ref_default_is_tagged "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — a2a-ref check stayed GREEN with zero tags in the checkout" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "no vX.Y.Z tags at all" || {
    echo "check-release-record --teeth: FAILED — red, but not with the no-tags message" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 2: a2a-ref default checked with zero tags in the checkout → RED (cannot check is never check passed)"

  # ── now give the checkout one real tag: v0.1.0 ────────────────────────────
  git -C "$tmp" tag v0.1.0

  # --- teeth 3: released: DATE for a version with no tag → RED, names both ---
  printf 'schema: release-notes/v1\nversion: "0.2.0"\nreleased: "2026-01-05"\nheadline: h\nchanges:\n  - id: RN-1\n    kind: feat\n    impact: low\n    subject: s\n    detail: d\n    action:\n      scope: none\n      why: y\n' \
    > "$tmp/releasenotes/0.2.0.yaml"
  if out="$(assert_released_dates_match_tags "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — gate stayed GREEN on 0.2.0's released: date with no v0.2.0 tag" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "0.2.0.yaml claims released: 2026-01-05" || {
    echo "check-release-record --teeth: FAILED — red, but did not name the version/date" >&2
    echo "$out" >&2; exit 1; }
  printf '%s\n' "$out" | grep -q "newest existing tag is v0.1.0" || {
    echo "check-release-record --teeth: FAILED — red, but did not name the newest existing tag" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 3: released: DATE for an untagged version → RED, naming the version and the newest tag"

  # --- teeth 3a/3b: THE CUT WINDOW, both directions. -------------------------
  # 3a proves the loosening works; 3b proves it is a WINDOW and not a hole.
  # Yesterday's date is one day outside it and must still red — otherwise the
  # rule would say "a date without a tag is fine", which is the opposite of
  # what this gate is for.
  _teeth_notes() { # $1 = path, $2 = released value
    printf 'schema: release-notes/v1\nversion: "0.2.0"\nreleased: "%s"\nheadline: h\nchanges:\n  - id: RN-1\n    kind: feat\n    impact: low\n    subject: s\n    detail: d\n    action:\n      scope: none\n      why: y\n' "$2" > "$1"
  }

  _teeth_notes "$tmp/releasenotes/0.2.0.yaml" "$(date -u +%Y-%m-%d)"
  if ! out="$(assert_released_dates_match_tags "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — 3a: TODAY's date with no tag was refused; the cut window cannot open" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "the cut window" || {
    echo "check-release-record --teeth: FAILED — 3a: green, but not by the cut-window branch (it may have passed for the wrong reason)" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 3a: TODAY's date with no tag → GREEN, by the cut window and saying so"

  _teeth_notes "$tmp/releasenotes/0.2.0.yaml" "$(date -u -v-1d +%Y-%m-%d 2>/dev/null || date -u -d 'yesterday' +%Y-%m-%d)"
  if out="$(assert_released_dates_match_tags "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — 3b: YESTERDAY's date with no tag stayed GREEN; the window is a hole, not a window" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "is not a tag reachable" || {
    echo "check-release-record --teeth: FAILED — 3b: red, but not with the untagged-claim message" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 3b: YESTERDAY's date with no tag → still RED — an abandoned cut is caught one day later"

  # --- teeth 4: the sentinel clears it → GREEN ---
  printf 'schema: release-notes/v1\nversion: "0.2.0"\nreleased: unreleased\nheadline: h\nchanges:\n  - id: RN-1\n    kind: feat\n    impact: low\n    subject: s\n    detail: d\n    action:\n      scope: none\n      why: y\n' \
    > "$tmp/releasenotes/0.2.0.yaml"
  if ! out="$(assert_released_dates_match_tags "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — the unreleased sentinel was refused" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 4: released: unreleased for an untagged version → GREEN"

  # --- teeth 5: a real date WITH a matching tag → GREEN (no false positive) ---
  printf 'schema: release-notes/v1\nversion: "0.1.0"\nreleased: "2026-01-01"\nheadline: h\nchanges:\n  - id: RN-1\n    kind: feat\n    impact: low\n    subject: s\n    detail: d\n    action:\n      scope: none\n      why: y\n' \
    > "$tmp/releasenotes/0.1.0.yaml"
  if ! out="$(assert_released_dates_match_tags "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — a released: date matching an existing tag was refused" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 5: released: DATE for a tagged version → GREEN"
  rm -f "$tmp/releasenotes/0.2.0.yaml"

  # --- teeth 5b: a version OLDER than the newest tag, claiming a date, with
  # no tag of its own → GREEN, reported not refused (ADR-011: no rule judges
  # immutable history). This is releasenotes/0.12.0.yaml's real shape — cut
  # under another number years of commits ago; refusing it would demand
  # rewriting a settled record. Paired with teeth 3, which proves a version
  # AHEAD of the newest tag in the same shape still REDS. ---
  printf 'schema: release-notes/v1\nversion: "0.0.9"\nreleased: "2026-01-01"\nheadline: skipped long ago\n' \
    > "$tmp/releasenotes/0.0.9.yaml"
  if ! out="$(assert_released_dates_match_tags "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — a version predating the newest tag was REFUSED; that demands rewriting immutable history (ADR-011)" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "immutable history, not judged" || {
    echo "check-release-record --teeth: FAILED — green, but did not say WHY it was not judged" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 5b: a version predating the newest tag, claiming a date with no tag → GREEN, reported as immutable history (ADR-011)"
  rm -f "$tmp/releasenotes/0.0.9.yaml"

  # --- teeth 6: a2a-ref default naming an untagged version → RED, names both ---
  printf 'on:\n  workflow_call:\n    inputs:\n      a2a-ref:\n        type: string\n        default: "v0.3.0"\n' \
    > "$tmp/.github/workflows/a2a-validate-reusable.yml"
  if out="$(assert_ref_default_is_tagged "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — a2a-ref default naming an untagged version stayed GREEN" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "a2a-ref default is v0.3.0" || {
    echo "check-release-record --teeth: FAILED — red, but did not name the default" >&2
    echo "$out" >&2; exit 1; }
  printf '%s\n' "$out" | grep -q "newest existing tag is v0.1.0" || {
    echo "check-release-record --teeth: FAILED — red, but did not name the newest existing tag" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 6: a2a-ref default naming an untagged version → RED, naming the default and the newest tag"

  # --- teeth 7: a2a-ref default naming an existing tag → GREEN ---
  printf 'on:\n  workflow_call:\n    inputs:\n      a2a-ref:\n        type: string\n        default: "v0.1.0"\n' \
    > "$tmp/.github/workflows/a2a-validate-reusable.yml"
  if ! out="$(assert_ref_default_is_tagged "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — a2a-ref default naming an existing tag was refused" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 7: a2a-ref default naming an existing tag → GREEN"

  # --- teeth 7b: a2a-ref default naming an UNTAGGED version whose own notes
  # carry the sentinel → GREEN (the runbook's step-2-before-step-12 window).
  # Without this the gate would red every legitimate release sequence, and the
  # carve-out is the whole reason internal/notes'
  # TestReusableRefDefaultMatchesTheNewestAuthoredVersion can stay true. ---
  printf 'schema: release-notes/v1\nversion: "0.3.0"\nreleased: %s\nheadline: mid-sequence\n' \
    "$UNRELEASED_SENTINEL" > "$tmp/releasenotes/0.3.0.yaml"
  printf 'on:\n  workflow_call:\n    inputs:\n      a2a-ref:\n        type: string\n        default: "v0.3.0"\n' \
    > "$tmp/.github/workflows/a2a-validate-reusable.yml"
  if ! out="$(assert_ref_default_is_tagged "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — a2a-ref default naming an untagged version whose notes carry the sentinel was refused (this reds every real release sequence)" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 7b: a2a-ref default naming an untagged version whose notes carry the sentinel → GREEN (mid-sequence)"

  # --- teeth 7c: the SAME default, once those notes claim a DATE → RED.
  # Proves the sentinel is the discriminator and not a blanket exemption. ---
  printf 'schema: release-notes/v1\nversion: "0.3.0"\nreleased: "2026-01-01"\nheadline: claims a cut\n' \
    > "$tmp/releasenotes/0.3.0.yaml"
  if out="$(assert_ref_default_is_tagged "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — a2a-ref default naming an untagged version whose notes claim a DATE stayed GREEN" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 7c: the same default, once its notes claim a released DATE → RED (the sentinel is the discriminator, not a blanket exemption)"
  rm -f "$tmp/releasenotes/0.3.0.yaml"

  # ── refusal 3: floor / pin direction ──────────────────────────────────────

  # --- teeth 8: a fresh single-commit repo has no HEAD~1 → RED (cannot check) ---
  git -C "$solo" init -q -b main
  git -C "$solo" config user.email teeth@example.com
  git -C "$solo" config user.name teeth
  mkdir -p "$solo/space-template"
  printf 'schema: space/v1\nmin_binary_version: "0.1.0"\n' > "$solo/space-template/space.yaml"
  git -C "$solo" add -A && git -C "$solo" commit -qm "only commit"
  if out="$(assert_floor_and_pins_not_backwards "$solo" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — floor/pin check stayed GREEN with no parent commit" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "no parent commit" || {
    echo "check-release-record --teeth: FAILED — red, but not with the no-parent-commit message" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 8: single-commit checkout (no HEAD~1) → RED (cannot check is never check passed)"

  # --- teeth 9/10: floor moved backwards → RED, naming both versions; then a
  #     fixture commit pair that GREENs (floor held or advanced) ---
  printf 'schema: space/v1\nmin_binary_version: "0.2.0"\n' > "$tmp/space-template/space.yaml"
  printf '    uses: o/r/.github/workflows/a2a-validate-reusable.yml@v0.2.0\n' \
    > "$tmp/space-template/.github/workflows/a2a-validate.yml"
  git -C "$tmp" add -A && git -C "$tmp" commit -qm "floor and pin at 0.2.0"

  printf 'schema: space/v1\nmin_binary_version: "0.1.0"\n' > "$tmp/space-template/space.yaml"
  printf '    uses: o/r/.github/workflows/a2a-validate-reusable.yml@v0.1.0\n' \
    > "$tmp/space-template/.github/workflows/a2a-validate.yml"
  git -C "$tmp" add -A && git -C "$tmp" commit -qm "regress floor and pin to 0.1.0"

  if out="$(assert_floor_and_pins_not_backwards "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — a floor+pin regression vs the previous commit stayed GREEN" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "min_binary_version moved BACKWARDS, from 0.2.0" || {
    echo "check-release-record --teeth: FAILED — red, but did not name the floor regression" >&2
    echo "$out" >&2; exit 1; }
  printf '%s\n' "$out" | grep -q "pins a reusable workflow at v0.1.0, BACKWARDS from v0.2.0" || {
    echo "check-release-record --teeth: FAILED — red, but did not name the pin regression" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 9: floor AND pin both moved backwards vs the previous commit → RED, naming both regressions (fixture commit pair)"

  # advance forward again (working tree only, no new commit needed — this is
  # comparing the WORKING TREE against HEAD~1, which is now the 0.2.0 commit)
  printf 'schema: space/v1\nmin_binary_version: "0.3.0"\n' > "$tmp/space-template/space.yaml"
  printf '    uses: o/r/.github/workflows/a2a-validate-reusable.yml@v0.3.0\n' \
    > "$tmp/space-template/.github/workflows/a2a-validate.yml"
  git -C "$tmp" add -A && git -C "$tmp" commit -qm "advance floor and pin to 0.3.0"
  if ! out="$(assert_floor_and_pins_not_backwards "$tmp" 2>&1)"; then
    echo "check-release-record --teeth: FAILED — an advancing floor+pin was refused" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 10: floor and pin both advance vs the previous commit → GREEN"

  echo "check-release-record --teeth: all teeth bite."
}

# ── entrypoint ───────────────────────────────────────────────────────────────

if [ "${1:-}" = "--teeth" ]; then
  teeth
  exit 0
fi

ROOT="$(git rev-parse --show-toplevel)"

rc=0
assert_released_dates_match_tags "$ROOT" || rc=1
assert_ref_default_is_tagged "$ROOT" || rc=1
assert_floor_and_pins_not_backwards "$ROOT" || rc=1

if [ "$rc" -ne 0 ]; then
  echo "check-release-record: FAIL — the release record and the world disagree; see above." >&2
  exit 1
fi
echo "check-release-record: OK — the release record and the world agree."
