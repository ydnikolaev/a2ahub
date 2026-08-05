#!/usr/bin/env bash
# Move the space template's authoring baseline to the release just tagged.
#
# This is release-runbook Phase 4 step 15, which was two hand-run `sed`
# substitutions over two files that must move as a PAIR. The pair is
# load-bearing and the failure is not loud:
#
#   space-template/space.yaml `min_binary_version` (the write FLOOR)
#     -> the exact-candidate builder stamps the live binary with that floor
#     -> `a2a space init` pins a scaffolded space's workflow to the running
#        binary's version
#     -> that space's CI resolves
#        ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@vX.Y.Z
#
# A floor naming a tag that is not PUBLISHED makes the reusable workflow
# unresolvable in every space the live tier provisions, and GitHub reports that
# as a run with NO JOBS rather than a failing check: the required context never
# appears, so every row waits for a verdict that will not come and times out an
# hour later. That happened on 2026-07-26 and killed a live run.
#
# `TestTemplateFloorMatchesTemplateWorkflowPin` catches the two drifting apart,
# after the fact, in a Go test. This script removes the step that made them
# drift, and adds the guard no human performs: it REFUSES to write a version
# whose tag is not published yet. That inverts the 2026-07-26 failure from
# "possible if you forget the order" to "impossible without --force".
set -euo pipefail

MANIFEST="space-template/space.yaml"
WORKFLOW="space-template/.github/workflows/a2a-validate.yml"
WORKFLOW_REF_COUNT=2   # the caller invokes the reusable workflow twice (PR + post-merge)
PUBLIC_REMOTE="${A2A_PUBLIC_REMOTE:-public}"

usage() {
  cat >&2 <<'USAGE'
usage: bump-space-template.sh [--check] [--allow-unpublished]

  (no flags)            rewrite the template's floor and workflow refs to the
                        newest authored release-notes version, after proving
                        that tag is published.
  --check               report whether they already match; write nothing.
                        Exit 0 if in sync, 1 if not.
  --allow-unpublished   skip the published-tag proof. Only for --teeth and for
                        a deliberate pre-tag rehearsal; using it for a real
                        bump reintroduces the 2026-07-26 failure by hand.
  --teeth               self-test.
USAGE
  exit 2
}

# The version being targeted, derived rather than passed. An argument would put
# a human back in the loop and a typo back on the table; the newest authored
# release-notes file is the same SSOT `a2a-ref`'s default and candidate.sh's
# REQUIRED_FLOOR already derive from, and in Phase 4 it IS the tag just cut.
newest_authored_version() {
  local newest
  newest="$(find releasenotes -maxdepth 1 -type f -name '[0-9]*.[0-9]*.[0-9]*.yaml' -print 2>/dev/null | sort -V | tail -1)"
  [ -n "$newest" ] || return 1
  basename "$newest" .yaml
}

tag_is_published() { # $1 = vX.Y.Z
  [ -n "$(git ls-remote --tags "$PUBLIC_REMOTE" "refs/tags/$1" 2>/dev/null)" ]
}

# Rewrite one file, then PROVE the rewrite landed the expected number of times.
#
# A sed expression that matches nothing exits 0 and changes nothing, so a
# template whose shape drifts would leave this script reporting success while
# doing exactly what the hand-editing did wrong. The count is the difference
# between a script and a wish.
rewrite() { # $1 = file, $2 = ERE matching the OLD line, $3 = replacement line, $4 = expected count
  local file="$1" pattern="$2" replacement="$3" want="$4" got
  got="$(grep -Ec "$pattern" "$file" || true)"
  if [ "$got" -ne "$want" ]; then
    echo "bump-space-template: FAIL — expected $want line(s) matching /$pattern/ in $file, found $got." >&2
    echo "The template's shape changed. Refusing to write: a substitution that matches nothing succeeds silently," >&2
    echo "which is precisely the hand-edit failure this script exists to remove." >&2
    return 1
  fi
  local tmp
  tmp="$(mktemp)"
  sed -E "s|$pattern|$replacement|" "$file" >"$tmp"
  mv "$tmp" "$file"
}

floor_now()  { sed -nE 's/^min_binary_version:[[:space:]]*"?([0-9]+\.[0-9]+\.[0-9]+)"?.*/\1/p' "$MANIFEST" | head -1; }
pins_now()   { sed -nE 's|.*a2a-validate-reusable\.yml@(v[0-9]+\.[0-9]+\.[0-9]+).*|\1|p' "$WORKFLOW" | sort -u; }

bump() {
  local mode="$1" allow_unpublished="$2" version tag floor pins

  version="$(newest_authored_version)" || {
    echo "bump-space-template: FAIL — no authored release-notes version found under releasenotes/." >&2
    return 1
  }
  tag="v$version"

  floor="$(floor_now)"
  pins="$(pins_now)"

  if [ "$mode" = "check" ]; then
    # The invariant is NOT "template == newest authored note". The runbook
    # requires them to DIFFER for the whole of a release: the note is authored
    # in Phase 1 while the template must keep naming the previous, PUBLISHED
    # release until Phase 4, because a floor naming an unpublished tag is the
    # 2026-07-26 outage. Asserting equality here reds `make check` from the
    # moment a release starts until it ends — which is exactly when the gate is
    # most in the way and least right.
    #
    # What must hold at ALL times, and is checkable offline:
    #   1. the floor and every workflow pin name ONE version (they move as a
    #      pair or the pair is broken), and
    #   2. that version is not AHEAD of the newest authored note (ahead means
    #      it names something nobody has even written notes for).
    # "Is it published?" needs the network and belongs to release-preflight and
    # to the write path below, not to the static lane.
    if [ -z "$floor" ] || [ -z "$pins" ]; then
      echo "bump-space-template: FAIL — could not read the template's floor and/or workflow pins." >&2
      echo "  floor: ${floor:-<unreadable>}   pins: ${pins:-<unreadable>}" >&2
      return 1
    fi
    if [ "$(printf '%s\n' "$pins" | wc -l | tr -d ' ')" -ne 1 ] || [ "$pins" != "v$floor" ]; then
      echo "bump-space-template: FAIL — the template's floor and workflow pin(s) disagree." >&2
      echo "  floor: $floor   pins: $(printf '%s ' $pins)" >&2
      echo "They are one decision — the release this template targets — and a space scaffolded from a" >&2
      echo "split pair validates with a different binary than its own floor demands." >&2
      echo "Run 'make space-template-baseline' (release runbook Phase 4 step 15)." >&2
      return 1
    fi
    if [ "$(printf '%s\n%s\n' "$floor" "$version" | sort -V | tail -1)" = "$floor" ] && [ "$floor" != "$version" ]; then
      echo "bump-space-template: FAIL — the template targets $floor, ahead of the newest authored release notes ($version)." >&2
      echo "A template cannot target a release nobody has written notes for." >&2
      return 1
    fi
    if [ "$floor" = "$version" ]; then
      echo "bump-space-template: ok — template targets v$floor, the newest authored release."
    else
      echo "bump-space-template: ok — template targets v$floor, published; v$version is authored and not yet tagged (expected mid-release)."
    fi
    return 0
  fi

  if [ "$floor" = "$version" ] && [ "$pins" = "$tag" ]; then
    echo "bump-space-template: ok — template already targets $tag (floor $floor, $WORKFLOW_REF_COUNT pin(s) at $tag)."
    return 0
  fi

  if [ "$allow_unpublished" != "yes" ] && ! tag_is_published "$tag"; then
    echo "bump-space-template: FAIL — $tag is not published on remote '$PUBLIC_REMOTE'." >&2
    echo "" >&2
    echo "The floor and the pin must never name an unpublished tag. A space scaffolded against one gets a" >&2
    echo "reusable workflow that cannot resolve, and GitHub reports that as a run with NO JOBS — the required" >&2
    echo "check never appears, so nothing fails and everything waits. Cut the tag first; this step is Phase 4." >&2
    return 1
  fi

  rewrite "$MANIFEST" \
    '^min_binary_version:[[:space:]]*"?[0-9]+\.[0-9]+\.[0-9]+"?' \
    "min_binary_version: $version" 1
  rewrite "$WORKFLOW" \
    'a2a-validate-reusable\.yml@v[0-9]+\.[0-9]+\.[0-9]+' \
    "a2a-validate-reusable.yml@$tag" "$WORKFLOW_REF_COUNT"

  floor="$(floor_now)"
  pins="$(pins_now)"
  if [ "$floor" != "$version" ] || [ "$pins" != "$tag" ]; then
    echo "bump-space-template: FAIL — post-write verification disagrees: floor=$floor pins=$(echo "$pins" | tr '\n' ' ')" >&2
    return 1
  fi
  echo "bump-space-template: moved the template baseline to $tag (floor and $WORKFLOW_REF_COUNT workflow pin(s), together)."
}

teeth() {
  local tmp out
  tmp="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT

  seed() { # $1 = root, $2 = floor version, $3 = pin tag, $4 = newest notes version
    rm -rf "${1:?}/releasenotes" "${1:?}/space-template"
    mkdir -p "$1/releasenotes" "$1/space-template/.github/workflows"
    printf 'version: "%s"\n' "$4" >"$1/releasenotes/$4.yaml"
    printf 'schema: space/v1\nspace: X\nmin_binary_version: %s            # comment\n' "$2" \
      >"$1/space-template/space.yaml"
    printf 'jobs:\n  a:\n    uses: o/r/.github/workflows/a2a-validate-reusable.yml@%s\n  b:\n    uses: o/r/.github/workflows/a2a-validate-reusable.yml@%s\n' \
      "$3" "$3" >"$1/space-template/.github/workflows/a2a-validate.yml"
  }

  # Case 1: an unpublished tag is REFUSED. This is the 2026-07-26 failure, and
  # it is the one case a human performing this step never checks.
  seed "$tmp" 0.1.0 v0.1.0 0.2.0
  if out="$(cd "$tmp" && A2A_PUBLIC_REMOTE=definitely-no-such-remote bump write no 2>&1)"; then
    echo "bump-space-template --teeth: FAILED — an unpublished tag was written" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'NO JOBS' || {
    echo "bump-space-template --teeth: FAILED — refusal did not name what an unpublished tag actually costs" >&2
    exit 1
  }
  [ "$(cd "$tmp" && floor_now)" = "0.1.0" ] || {
    echo "bump-space-template --teeth: FAILED — a refused bump still modified the manifest" >&2
    exit 1
  }

  # Case 2: both values move TOGETHER, from one derived version.
  seed "$tmp" 0.1.0 v0.1.0 0.2.0
  (cd "$tmp" && bump write yes >/dev/null)
  [ "$(cd "$tmp" && floor_now)" = "0.2.0" ] || {
    echo "bump-space-template --teeth: FAILED — floor did not move" >&2; exit 1; }
  [ "$(cd "$tmp" && pins_now)" = "v0.2.0" ] || {
    echo "bump-space-template --teeth: FAILED — workflow pins did not all move to one version" >&2; exit 1; }

  # Case 3: idempotent — a second run is a no-op, not an error, or --check
  # could not reuse it.
  (cd "$tmp" && bump write yes >/dev/null) || {
    echo "bump-space-template --teeth: FAILED — a second run errored instead of no-opping" >&2; exit 1; }

  # Case 4: --check agrees with the state.
  (cd "$tmp" && bump check no >/dev/null) || {
    echo "bump-space-template --teeth: FAILED — --check went red on an in-sync template" >&2; exit 1; }

  # Case 4b: MID-RELEASE is green — the template one release behind the newest
  # AUTHORED note. This is the state the runbook mandates from Phase 1 until
  # Phase 4 (the template must keep naming a PUBLISHED tag), so a --check that
  # demanded equality would red `make check` for the entire duration of every
  # release. It did, on the first release after the gate landed; the gate had
  # never been run in the state its subject occupies during a release.
  seed "$tmp" 0.1.0 v0.1.0 0.2.0
  if ! out="$(cd "$tmp" && bump check no 2>&1)"; then
    echo "bump-space-template --teeth: FAILED — --check reds mid-release, when the template legitimately trails the newest authored note" >&2
    echo "$out" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'expected mid-release' || {
    echo "bump-space-template --teeth: FAILED — mid-release green did not say why the two differ" >&2
    exit 1
  }

  # Case 4c: a SPLIT pair reds — floor and pin naming different versions is the
  # actual defect this gate exists to catch, at any point in the cycle.
  seed "$tmp" 0.1.0 v0.2.0 0.2.0
  if (cd "$tmp" && bump check no >/dev/null 2>&1); then
    echo "bump-space-template --teeth: FAILED — --check stayed green on a floor and pin that disagree" >&2; exit 1
  fi

  # Case 4d: a template AHEAD of every authored note reds — it targets a
  # release nobody has written notes for, which is how the unpublished-tag
  # outage begins.
  seed "$tmp" 0.9.0 v0.9.0 0.2.0
  if (cd "$tmp" && bump check no >/dev/null 2>&1); then
    echo "bump-space-template --teeth: FAILED — --check stayed green on a template ahead of the newest authored note" >&2; exit 1
  fi

  # Case 5: a template whose shape changed REFUSES rather than silently
  # writing nothing. A sed that matches nothing exits 0 — that is the whole
  # reason the counts exist, and without this case the counts are decoration.
  seed "$tmp" 0.1.0 v0.1.0 0.2.0
  printf 'schema: space/v1\nspace: X\n# the floor moved somewhere else\n' >"$tmp/space-template/space.yaml"
  if out="$(cd "$tmp" && bump write yes 2>&1)"; then
    echo "bump-space-template --teeth: FAILED — a template with no floor line reported success" >&2
    exit 1
  fi
  printf '%s' "$out" | grep -q 'matches nothing succeeds silently' || {
    echo "bump-space-template --teeth: FAILED — refusal did not explain the silent-no-op hazard" >&2
    exit 1
  }

  # Case 6: a workflow carrying only ONE of its two refs refuses too — a
  # partial pin is a space validating with a different binary than it thinks.
  seed "$tmp" 0.1.0 v0.1.0 0.2.0
  printf 'jobs:\n  a:\n    uses: o/r/.github/workflows/a2a-validate-reusable.yml@v0.1.0\n' \
    >"$tmp/space-template/.github/workflows/a2a-validate.yml"
  if (cd "$tmp" && bump write yes >/dev/null 2>&1); then
    echo "bump-space-template --teeth: FAILED — a workflow with 1 of 2 refs was accepted" >&2
    exit 1
  fi

  echo "bump-space-template --teeth: unpublished tag refused with its real cost named; floor and both pins move together from one derived version; idempotent; --check agrees both ways; a drifted template shape refuses instead of silently writing nothing; a partial pin set refuses."
}

mode=write
allow_unpublished=no
while [ $# -gt 0 ]; do
  case "$1" in
    --check) mode=check ;;
    --allow-unpublished) allow_unpublished=yes ;;
    --teeth) teeth; exit 0 ;;
    -h|--help) usage ;;
    *) echo "bump-space-template: unknown argument: $1" >&2; usage ;;
  esac
  shift
done
bump "$mode" "$allow_unpublished"
