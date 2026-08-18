#!/usr/bin/env bash
# release-preflight.sh — the check that must pass BEFORE cutting a release tag.
#
# Why this exists (P33, 2026-07-24): the space-CI redesign shipped with the
# space-template caller pinned to `@v0.4.0` — a version that was ALREADY
# released and that PREDATES the reusable workflow the caller references. Every
# space created from that template would have failed with "workflow not found".
# The root cause was reading release state from LOCAL `git tag` (stale) instead
# of the release remote. This gate makes that class impossible to ship blind.
#
# Assertions, all keyed off the RELEASE REMOTE (never local-only state):
#   (a) the version you are about to cut is FREE on the remote;
#   (b) every reusable-workflow ref the space-template pins resolves to a tag
#       whose TREE ACTUALLY CONTAINS that workflow file.
#   (c) the space-template's declared min_binary_version does not EXCEED the
#       version being cut. That field is the fleet WRITE FLOOR: `a2a space
#       update` propagates it to every space, and the funnel then refuses any
#       write from an older binary (CC-085). A floor ahead of the newest
#       release refuses EVERYONE — including the person trying to fix it —
#       so a one-character typo here is a fleet-wide outage.
#   (d) the reverse of (c), one release LATE by construction: if the PREVIOUS
#       release (prev) added a new (family, version, type) corpus row that the
#       one before it (prev2) did not have — a schema no binary released
#       before prev can decode — the write floor must already equal prev's
#       version by the time you preflight the NEXT release. Pre-tag, the floor
#       cannot name the version being cut (runbook Phase 1 step 4: it still
#       names the previous release); it is only raised, to the version JUST
#       published, by `make space-template-baseline` AFTER the tag (runbook
#       Phase 4 step 15). So `floor == version-being-cut` is structurally
#       impossible to demand pre-tag, and the earliest moment this can be
#       judged is the NEXT preflight — this assertion catches a skipped or
#       wrong Phase 4 step 15 one release late, not the release that made the
#       miss. (c) forbids the floor overshooting; (d) forbids it undershooting
#       the one time undershooting is dangerous. Both hold; neither implies
#       the other.
#
# Determinism note (validation doctrine §5): the only non-deterministic step is
# the `git fetch` of the release remote. Everything after it is a pure local git
# read of the fetched refs — which is exactly what `--teeth` exercises offline
# against a fixture repo. Because it needs the network, this is NOT part of
# `make check`; it is its own target, like `make vulncheck`.
#
# Usage:
#   bash scripts/release-preflight.sh v0.6.0        # before cutting v0.6.0
#   bash scripts/release-preflight.sh --teeth       # offline self-test (the gate bites)
set -euo pipefail

REMOTE="${A2A_RELEASE_REMOTE:-public}"
REUSABLE_PATH=".github/workflows/a2a-validate-reusable.yml"
REUSABLE_DIR=".github/workflows"
TEMPLATE_WORKFLOWS="space-template/.github/workflows"
TEMPLATE_MANIFEST="space-template/space.yaml"

# reusable_workflow_paths prints every *-reusable.yml under $1/$REUSABLE_DIR,
# repo-relative, one per line, sorted. DERIVED rather than a hardcoded single
# path — same discipline as internal/notes' own reusableWorkflowPaths (see
# that test's doc comment): a hardcoded const/var is exactly the shape that
# let this coupling ship wrong twice, because nothing forced a NEW reusable
# workflow to be added to the guard. A future third reusable workflow is
# covered by construction, not by remembering to touch this file again.
reusable_workflow_paths() { # $1 = repo
  local repo="$1" f
  for f in "$repo/$REUSABLE_DIR"/*-reusable.yml; do
    [ -e "$f" ] || continue
    printf '%s\n' "${REUSABLE_DIR}/$(basename "$f")"
  done
}

fail() { printf '\033[31m✗\033[0m %s\n' "$1" >&2; return 1; }
ok()   { printf '\033[32m✓\033[0m %s\n' "$1"; }

# ── core assertions — pure LOCAL git reads over already-fetched refs ──────────

# (a) the version being cut must not already exist as a tag.
assert_version_free() { # $1 = repo, $2 = version
  local repo="$1" want="$2"
  if git -C "$repo" tag -l "$want" | grep -qx "$want"; then
    fail "release-preflight: $want ALREADY EXISTS as a tag — pick the next free version.
    (This is the P33 miss: v0.4.0 was already released and predated the workflow.)"
    return 1
  fi
  ok "release-preflight: $want is free"
}

# (b) every pinned reusable-workflow ref must resolve to a tag whose tree
#     carries the reusable workflow. A pin to a tag that predates the workflow
#     fails every space's CI with "workflow not found".
#
# The referenced PATH is extracted per line (`${REUSABLE_DIR}/<basename before
# the @>`), never assumed to be $REUSABLE_PATH — so this covers EVERY
# `*-reusable.yml` caller the template carries (space-notify-2026-08 P5 added
# a second one, a2a-notify.yml -> a2a-notify-reusable.yml), not only the
# validator, with no second copy of this function.
#
# ONE case is legitimately unresolvable and is NOT this gate's failure: a
# reusable workflow introduced BY THE RELEASE BEING CUT. No published tag can
# carry a file that no tag exists for yet, and the runbook REQUIRES the pin to
# keep naming the previous release until Phase 4 step 15 (Phase 1 step 4: "do
# NOT touch the space-template pin or floor" — a floor naming an unpublished
# tag is the 2026-07-26 outage). Those two rules together make the first
# release of any new reusable workflow an automatic red, which is how
# space-notify-2026-08 refused `v0.23.0` on 2026-08-18 with nothing wrong.
#
# The two cases are told apart by asking the REMOTE, not by trusting the pin:
#   * some published tag carries the file, and the pin names an older one
#     -> the P33 miss. RED, unchanged.
#   * NO published tag carries it, and the working tree does
#     -> its first release. GREEN, with step 15 named out loud, because the
#        template is only correct again AFTER that step runs.
#   * NO published tag carries it, and the working tree does not either
#     -> the pin references a workflow that exists nowhere. RED.
#
# `a2a space init` rewrites a scaffolded space's pin to the RUNNING BINARY's
# version (cmd_space.go spaceNotifyWorkflowRefPattern), so the template's
# literal pin is not what a real space gets — which is why the first-release
# window is a documentation obligation rather than a live exposure.
assert_pins_resolve() { # $1 = repo, $2 = version being cut (for the message)
  local repo="$1" version="${2:-the release being cut}" rc=0 found=0 line ref wf_file wf_path tag carried
  while IFS= read -r line; do
    # strip comments, take the token after the last '@'
    line="${line%%#*}"
    case "$line" in *-reusable.yml@*) ;; *) continue ;; esac
    ref="${line##*@}"
    ref="$(printf '%s' "$ref" | tr -d '[:space:]')"
    [ -n "$ref" ] || continue
    wf_file="${line##*/}"
    wf_file="${wf_file%%@*}"
    wf_path="${REUSABLE_DIR}/${wf_file}"
    found=$((found + 1))
    if ! git -C "$repo" rev-parse -q --verify "refs/tags/$ref" >/dev/null 2>&1; then
      fail "release-preflight: space-template pins @$ref, which is NOT a known tag on '$REMOTE'.
    Fix: pin a released version, or cut $ref first."
      rc=1
      continue
    fi
    if ! git -C "$repo" cat-file -e "$ref:$wf_path" 2>/dev/null; then
      # Is this workflow published in ANY tag, or is it new in this release?
      carried=""
      while IFS= read -r tag; do
        [ -n "$tag" ] || continue
        if git -C "$repo" cat-file -e "$tag:$wf_path" 2>/dev/null; then carried="$tag"; break; fi
      done < <(git -C "$repo" tag -l 'v*')

      if [ -n "$carried" ]; then
        fail "release-preflight: space-template pins @$ref, but that tag's tree does NOT contain
    $wf_path — every space created from this template would fail
    'workflow not found'. Fix: pin the release that ADDED the reusable workflow
    (@$carried carries it)."
        rc=1
        continue
      fi

      if [ ! -f "$repo/$wf_path" ]; then
        fail "release-preflight: space-template pins @$ref for $wf_path, and that file exists
    in NO published tag AND NOT in the working tree — the pin names a reusable
    workflow that exists nowhere. Fix: add the workflow, or drop the caller."
        rc=1
        continue
      fi

      ok "release-preflight: pin @$ref does not carry $wf_path, and neither does any published
    tag — the workflow is NEW IN $version. Pre-tag this is the only legal state (runbook
    Phase 1 step 4 forbids moving the pin). \`make space-template-baseline\` (Phase 4 step 15)
    is MANDATORY after the tag, or the template ships a caller nothing resolves."
      continue
    fi
    ok "release-preflight: pin @$ref resolves and carries $wf_path"
  done < <(grep -rhE -- '-reusable\.yml@' "$repo/$TEMPLATE_WORKFLOWS" 2>/dev/null || true)

  if [ "$found" -eq 0 ]; then
    fail "release-preflight: no reusable-workflow pin found under $TEMPLATE_WORKFLOWS —
    the template must reference the reusable workflow (did a refactor drop it?)."
    return 1
  fi
  return "$rc"
}

# (c) the template's write floor must be <= the version being cut, or every
#     space that takes the update locks out every binary in existence.
assert_floor_not_ahead() { # $1 = repo, $2 = version (vX.Y.Z or X.Y.Z)
  local repo="$1" want="${2#v}" floor
  floor="$(sed -n 's/^min_binary_version:[[:space:]]*"\{0,1\}\([0-9][0-9.]*\)"\{0,1\}.*/\1/p' "$repo/$TEMPLATE_MANIFEST" 2>/dev/null | head -1)"
  if [ -z "$floor" ]; then
    fail "release-preflight: $TEMPLATE_MANIFEST declares no min_binary_version —
    \`a2a space update\` could not decide any space's write floor."
    return 1
  fi
  # Highest of the two per `sort -V`; if that is the floor and they differ, the
  # floor is ahead of the release.
  if [ "$(printf '%s\n%s\n' "$want" "$floor" | sort -V | tail -1)" = "$floor" ] && [ "$want" != "$floor" ]; then
    fail "release-preflight: space-template min_binary_version is $floor, AHEAD of the
    $2 being cut. \`a2a space update\` would push that floor to every space and the
    funnel would then refuse writes from every existing binary (CC-085) — including
    the one you would need to fix it. Fix: lower the floor, or cut >= $floor."
    return 1
  fi
  ok "release-preflight: template write floor $floor is not ahead of ${2}"
}

# _CORPUS_ROW_ANCHOR is the anchor for every corpusDefinitions row in
# internal/schema/corpus.go: `{key: corpusKey{...}, path: "..."}`, one per
# line. `_corpus_triples` and its self-check both key off this exact prefix so
# they read the same set of lines.
_CORPUS_FILE="internal/schema/corpus.go"
_CORPUS_ROW_ANCHOR='^[[:space:]]*\{key: corpusKey\{'

# _corpus_triples reads a corpus.go source (on stdin) and emits the sorted,
# de-duplicated set of "family version typ" triples it declares — one per
# corpusDefinitions row. `family` and `typ` are resolved through that same
# revision's own `family*`/`type*` const block, so a bare identifier
# (`typeBase`) and an equivalent quoted literal (`"contract"`) compare equal
# and a const rename does not manufacture a spurious triple.
#
# Self-check: the number of triples extracted must equal the number of lines
# matching `_CORPUS_ROW_ANCHOR`. If a future row shape breaks the regex (e.g.
# a row wrapped across lines), this must be caught loudly rather than let the
# gate quietly stop seeing new rows.
_corpus_triples() {
  local src rows mapfile triples n_rows n_triples
  src="$(cat)"
  rows="$(printf '%s\n' "$src" | grep -cE "$_CORPUS_ROW_ANCHOR" || true)"

  mapfile="$(mktemp)"
  printf '%s\n' "$src" \
    | grep -E '^[[:space:]]*(family|type)[A-Za-z]+[[:space:]]*=[[:space:]]*"[^"]*"' \
    | sed -E 's/^[[:space:]]*([A-Za-z0-9_]+)[[:space:]]*=[[:space:]]*"([^"]*)".*/\1 \2/' \
    > "$mapfile" || true

  triples="$(printf '%s\n' "$src" \
    | grep -E "$_CORPUS_ROW_ANCHOR" \
    | grep -oE 'corpusKey\{family: *[A-Za-z0-9_"]+, *version: *[0-9]+, *typ: *[A-Za-z0-9_"]+\}' \
    | sed -E 's/corpusKey\{family: *([A-Za-z0-9_"]+), *version: *([0-9]+), *typ: *([A-Za-z0-9_"]+)\}/\1 \2 \3/' \
    | tr -d '"' \
    | while read -r fam ver typ; do
        rfam="$(awk -v k="$fam" '$1==k{print $2; f=1} END{if(!f) print k}' "$mapfile")"
        rtyp="$(awk -v k="$typ" '$1==k{print $2; f=1} END{if(!f) print k}' "$mapfile")"
        printf '%s %s %s\n' "$rfam" "$ver" "$rtyp"
      done \
    | sort -u || true)"
  rm -f "$mapfile"

  n_rows="$rows"
  n_triples="$(printf '%s\n' "$triples" | grep -c . || true)"
  if [ "$n_rows" != "$n_triples" ]; then
    fail "release-preflight: corpus.go row shape changed — this extractor can no longer
    read it ($n_rows row(s) matched $_CORPUS_ROW_ANCHOR but $n_triples triple(s) were
    extracted). assert_floor_moves_with_schema would silently stop seeing new rows."
    return 1
  fi
  printf '%s\n' "$triples"
}

# _release_preflight_baseline_tag prints the highest vX.Y.Z-shaped tag in
# $repo that sorts below $version per `sort -V`, or nothing if none does
# (there is no released state below the version being cut, e.g. the very
# first tag ever).
_release_preflight_baseline_tag() { # $1 = repo, $2 = version (vX.Y.Z)
  local repo="$1" version="$2"
  { git -C "$repo" tag -l | grep -E '^v?[0-9]+(\.[0-9]+){1,2}$' || true; printf '%s\n' "$version"; } \
    | sort -V -u \
    | awk -v v="$version" '$0==v{exit} {print}' \
    | tail -1
}

# (d) one release LATE, by construction — see the header comment's (d) for
#     why this cannot be asked any earlier. Let prev = the highest published
#     tag below the version being cut, and prev2 = the highest published tag
#     below prev.
#
#     If prev is the FIRST tag in the repository, prev2 is empty and prev2's
#     triple set is empty, so every row prev declares counts as new and the
#     floor is required to name prev. That is intended — a first release ships
#     the whole corpus, and its floor should say so — and it is stated here
#     rather than tested, because a2ahub is forty tags past the case and a
#     tooth for an unreachable path is a tooth nobody maintains.
#
#     Not provable against this repository today, and said plainly: every
#     published tag from v0.19.6 to v0.19.9 carries the SAME 28 corpus rows,
#     so no (prev, prev2) pair on the real tree reaches the floor comparison
#     at all. HEAD carries 30 — the two rows this epic added — so the branch
#     goes live at the preflight AFTER the release that publishes them. Until
#     then the floor-mismatch path is proven by teeth 13/14 alone, which drive
#     this exact function against real fixture repositories. If prev added a (family, version, type) corpus row that
#     prev2 did not have, the write floor — read from the CURRENT (working
#     tree) state of $TEMPLATE_MANIFEST, i.e. "as of right now, about to cut
#     the NEXT release" — must equal prev's version. This is the earliest
#     point every existing invariant can hold at once: it is what runbook
#     Phase 1 step 4 asserts (the floor still names the previous release);
#     it is what keeps the floor pointing at a tag assert_pins_resolve above
#     could actually verify — `bump-space-template.sh` / `make
#     space-template-baseline` moves the floor and the workflow pins as ONE
#     PAIR and REFUSES to write a tag that is not published (the 2026-07-26
#     outage this guards), so forcing the floor to name $VERSION pre-tag
#     would drag the paired pin to a ref assert_pins_resolve cannot resolve
#     either, not just the floor to an unpublished tag; and it is what
#     `space-template-baseline-check` (Phase 4 step 15's own gate) already
#     guards from the other side — it asserts floor and pins move together as
#     a pair and never sit ahead of the newest authored note, but it cannot
#     see whether Phase 4 step 15 was skipped for the PREVIOUS release, because
#     a lagging floor is exactly its steady state. This assertion is what
#     catches that miss — one ceremony late, not the one that made it.
#
#     Composition with (c) above: assert_floor_not_ahead requires floor <=
#     version, ALWAYS — overshooting the floor locks out every existing
#     binary. This assertion requires floor == prev's version, ONLY when prev
#     added a corpus row prev2 did not have — undershooting is only dangerous
#     the moment a schema a released binary cannot decode ships, because
#     `a2a space update` would then propagate a row no old binary can
#     validate. Both hold together; neither implies the other, and collapsing
#     them loses the "only when a row is added" half.
#
#     The trigger is a corpus ROW, not a file: schemas/envelope/v2/fixtures/
#     and schemas/event/v2/schema_test.go both live under schemas/**, so a
#     file-based trigger would RED on a fixture or test-only commit that adds
#     no new decodable type — and a gate that reds on innocent commits gets
#     switched off within a week. `corpusDefinitions` in
#     internal/schema/corpus.go is the one place a new supported
#     (family, version, type) is declared, one explicit literal struct per
#     row, so a shell can read it deterministically.
assert_floor_moves_with_schema() { # $1 = repo, $2 = version (vX.Y.Z or X.Y.Z) about to be cut
  local repo="$1" version="$2"
  local prev prev2 want prev_triples prev2_triples new_triples floor formatted

  prev="$(_release_preflight_baseline_tag "$repo" "$version")"
  if [ -z "$prev" ]; then
    ok "release-preflight: no released tag below $version — nothing to check the floor against"
    return 0
  fi
  want="${prev#v}"

  # prev2 may be empty (prev is the very first tag ever) — every triple prev
  # declares then counts as new against an empty baseline, same as
  # corpus.go's own absence below.
  prev2="$(_release_preflight_baseline_tag "$repo" "$prev")"

  # $_CORPUS_FILE may not have existed yet at $prev or $prev2 (e.g. it was
  # added after that release) — that is an empty corpus, not a failure, so
  # it is checked explicitly rather than folded into a swallowed `git show`
  # error that would otherwise surface as an unexplained RED.
  if git -C "$repo" cat-file -e "$prev:$_CORPUS_FILE" 2>/dev/null; then
    prev_triples="$(git -C "$repo" show "$prev:$_CORPUS_FILE" | _corpus_triples)" || return 1
  else
    prev_triples=""
  fi

  if [ -n "$prev2" ] && git -C "$repo" cat-file -e "$prev2:$_CORPUS_FILE" 2>/dev/null; then
    prev2_triples="$(git -C "$repo" show "$prev2:$_CORPUS_FILE" | _corpus_triples)" || return 1
  else
    prev2_triples=""
  fi

  new_triples="$(awk 'NR==FNR{seen[$0]; next} !($0 in seen) && NF' \
    <(printf '%s\n' "$prev2_triples") <(printf '%s\n' "$prev_triples"))"

  if [ -z "$new_triples" ]; then
    ok "release-preflight: $prev added no new corpus row since ${prev2:-the beginning} — the write floor is not required to name $prev"
    return 0
  fi

  floor="$(sed -n 's/^min_binary_version:[[:space:]]*"\{0,1\}\([0-9][0-9.]*\)"\{0,1\}.*/\1/p' "$repo/$TEMPLATE_MANIFEST" 2>/dev/null | head -1)"
  formatted="$(printf '%s\n' "$new_triples" | awk '{printf "      - %s v%s %s\n", $1, $2, $3}')"

  if [ "$floor" != "$want" ]; then
    fail "release-preflight: $prev added a new schema corpus row not present at ${prev2:-<none — prev is the first release>} —
$formatted
    — but $TEMPLATE_MANIFEST's min_binary_version is ${floor:-<unset>}, not $want. $prev shipped
    a schema no binary released before it can decode, so ITS release should have raised the
    write floor to $want (runbook Phase 4 step 15, \`make space-template-baseline\`) — or
    \`a2a space update\` propagates a row no older binary can validate. Fix: set
    min_binary_version: \"$want\" in $TEMPLATE_MANIFEST before cutting $version.
    (This check runs one release late, by design: pre-tag the floor cannot yet name $version
    itself — runbook Phase 1 step 4 — so the earliest moment this miss is checkable is
    preflight for the release AFTER the one that added the row.)"
    return 1
  fi

  ok "release-preflight: $prev's new corpus row(s) since ${prev2:-the beginning} are accompanied by a write floor already at $want —
$formatted"
}

# _assert_ref_default_matches_one is the single-file check; assert_ref_
# default_matches below loops it over the DERIVED set (reusable_workflow_
# paths), the same generalization notes_test.go's own
# TestReusableRefDefaultMatchesTheNewestAuthoredVersion applies — see that
# test's doc comment for why a single hardcoded path shipped this coupling
# wrong twice.
_assert_ref_default_matches_one() { # $1 = repo, $2 = wf_path (repo-relative), $3 = version (vX.Y.Z)
  local repo="$1" wf_path="$2" want="$3" got
  # The `a2a-ref` input's own default inside the reusable workflow: the module
  # version a SPACE's CI will `go run`/`go install` when its caller does not
  # override it.
  got="$(sed -n '/^      a2a-ref:/,/^      [a-z-]*:/p' "$repo/$wf_path" 2>/dev/null \
    | sed -n 's/^ *default:[[:space:]]*"\{0,1\}\(v\{0,1\}[0-9][0-9.]*\)"\{0,1\}.*/\1/p' | head -1)"
  if [ -z "$got" ]; then
    fail "release-preflight: $wf_path declares no a2a-ref default —
    every space caller would have to name a version itself."
    return 1
  fi
  if [ "${got#v}" != "${want#v}" ]; then
    fail "release-preflight: $wf_path's a2a-ref default is $got, but you are
    cutting $want. A space pins the WORKFLOW by tag and inherits this default for the
    binary it runs, so the two skewing means every space silently runs $got while
    believing it runs $want.
    This is not hypothetical: v0.7.0 shipped a2a-validate-reusable.yml's default still at
    v0.5.0, so every space at @v0.7.0 ran a validator two releases old, missing every
    binary-side \`validate --ci\` fix since — including the computed contract-compatibility
    check, so a breaking change labelled minor would not be caught at merge there. (It did
    NOT reopen the v0.6.4 diff-authz bypass: that fix is workflow-side, passing --author
    explicitly, so the pinned WORKFLOW carries it whichever binary it runs.)
    Fix: set \`default: \"$want\"\` in $wf_path before tagging."
    return 1
  fi
  ok "release-preflight: $wf_path's a2a-ref default $got matches $want"
}

assert_ref_default_matches() { # $1 = repo, $2 = version (vX.Y.Z)
  local repo="$1" want="$2" rc=0 wf_path found=0
  while IFS= read -r wf_path; do
    [ -n "$wf_path" ] || continue
    found=$((found + 1))
    _assert_ref_default_matches_one "$repo" "$wf_path" "$want" || rc=1
  done < <(reusable_workflow_paths "$repo")

  if [ "$found" -eq 0 ]; then
    fail "release-preflight: no *-reusable.yml file found under $repo/$REUSABLE_DIR —
    the discovery glob is broken, not the workflows."
    return 1
  fi
  return "$rc"
}

# judge_pages_conclusion decides, from the last Pages run's conclusion alone,
# whether a release may be cut. Split out from the network call so `--teeth`
# can exercise the decision offline.
judge_pages_conclusion() { # $1 = conclusion ("" when unknown)
  case "$1" in
    success) return 0 ;;
    *) return 1 ;;
  esac
}

# assert_site_publishes: the website's own deploy workflow must be GREEN on
# main before a release is cut.
#
# This gate exists because its absence cost four releases. `pages.yml` builds
# and publishes the public site; it runs `npm run check` first and had been
# failing on every push to main since 2026-08-01, when a commit added a
# TypeScript import to two test files that `node --test` could not load. So
# v0.19.0, v0.19.1, v0.19.2 and v0.19.3 were all tagged, built, and published
# as GitHub Releases while the site stayed frozen at 0.18.2 — announcing none
# of them.
#
# Nothing noticed, and the reasons are worth naming because each is a separate
# hole: `make check` does not run the web lane at all (it is a hand-run
# convention), the runbook's post-tag verification looked at the RELEASE —
# assets, latest, body — and never at the site, and the tag-triggered Pages run
# is cancelled by concurrency in favour of the main run that was failing.
#
# Checked BEFORE the tag on purpose. A red site pipeline means the release you
# are about to cut cannot reach the site, and that is knowable in advance.
assert_site_publishes() { # $1 = repo (for gh --repo), unused when gh is absent
  local slug conclusion
  slug="${A2A_PUBLIC_SLUG:-ydnikolaev/a2ahub}"

  if ! command -v gh >/dev/null 2>&1; then
    fail "release-preflight: gh is not installed, so the site's deploy status cannot be read.
    The public site is published by pages.yml; a release cut while that workflow is red is
    a release the site never announces. Install gh, or set A2A_SKIP_PAGES_CHECK=1 to state
    deliberately that you are cutting without that assurance."
    return 1
  fi

  conclusion="$(gh run list --repo "$slug" --workflow=pages.yml --branch main \
    --limit 1 --json conclusion --jq '.[0].conclusion // ""' 2>/dev/null || true)"

  if judge_pages_conclusion "$conclusion"; then
    ok "release-preflight: the site's deploy workflow is green on main"
    return 0
  fi

  fail "release-preflight: the site's deploy workflow (pages.yml on main) last concluded
    '${conclusion:-unknown}'.
    Cutting now produces a tag and a GitHub Release that the public site never announces —
    it keeps serving whatever last deployed successfully. That is not hypothetical: four
    consecutive releases shipped that way while the site sat at 0.18.2.
    Fix the site build first (\`npm --prefix web run check\` reproduces it locally), or set
    A2A_SKIP_PAGES_CHECK=1 to record that you are cutting without it."
  return 1
}

# ── the feedback hub's protection contract (agent-ops-2026-07 P15 AC5) ───────

# judge_hub_protection: does this protection blob satisfy the contract the hub
# branch exists to provide? Split out from the network read for the same
# reason judge_pages_conclusion is — so --teeth exercises the DECISION offline,
# against blobs it constructs, rather than asserting that a network call
# happened.
#
# WHY THIS IS A CHECK AND NOT A PARAGRAPH. P15 AC5 asks for exactly this:
# "branch protection asserted by a check, the same way the publisher already
# asserts main's". The protection was set on 2026-08-13 and verified by
# attempting a real force-push and watching it refused — once, by hand. A
# hand-verification is a gate nobody has written yet: it says the rule held at
# one instant, and says nothing about the next time somebody edits a
# repository setting. The publisher already treats main's force-push toggle
# this way (assert_public_force_pushes, publish-to-public.sh) and the hub
# deserves the same, because the whole of P15 is the claim that a release
# cannot destroy a stranger's bug report.
#
# Each clause is here for a stated reason, not for completeness:
#
#   allow_force_pushes=false  — AC5 itself. The letterbox must not be
#                               replaceable.
#   allow_deletions=false     — a deleted branch destroys the corpus exactly
#                               as thoroughly as a force-push over it, and
#                               AC5's wording would not have caught it.
#   enforce_admins=true       — §3.2 asks for a rule "so it cannot by
#                               accident", and the publisher runs with an
#                               admin token. Without this clause the rule
#                               binds everyone EXCEPT the one identity the
#                               phase is defending against.
#   merge-policy required     — the hub takes external writes, and this is
#                               what `a2a feedback submit`'s auto-merge arms
#                               against. Dropping it does not merely weaken
#                               review; it breaks intake, because GitHub's
#                               auto-merge needs something to wait for.
judge_hub_protection() { # $1 = protection JSON
  local blob="$1" problems=""

  _flag() { problems="${problems:+$problems$'\n'}    - $1"; }

  # Direct `== false` / `== true` comparisons, NOT `.x // "missing"`.
  #
  # jq's alternative operator treats `false` as EMPTY, so
  # `.allow_force_pushes.enabled // "missing"` yields "missing" for a branch
  # that correctly refuses force-pushes — the first draft of this function did
  # exactly that and rejected the correct contract. Caught by the very first
  # teeth case, which is the whole argument for writing the green case as well
  # as the red ones: a gate that only ever refuses looks identical to a gate
  # that always refuses.
  #
  # `== false` is also stricter than the `//` form was trying to be: an ABSENT
  # key compares as null, so an unprotected branch fails every clause instead
  # of accidentally matching a default.
  jq -e '.allow_force_pushes.enabled == false' <<<"$blob" >/dev/null 2>&1 \
    || _flag "force-pushes are NOT refused — a release could overwrite the corpus, which is the entire defect P15 removed"
  jq -e '.allow_deletions.enabled == false' <<<"$blob" >/dev/null 2>&1 \
    || _flag "branch deletion is NOT refused — deleting the branch destroys the corpus as completely as force-pushing over it"
  jq -e '.enforce_admins.enabled == true' <<<"$blob" >/dev/null 2>&1 \
    || _flag "enforce_admins is off — the rule binds everyone EXCEPT an admin token, and the publisher runs with one"
  jq -e '.required_status_checks.contexts // [] | index("merge-policy")' <<<"$blob" >/dev/null 2>&1 \
    || _flag "merge-policy is not a required context — inbound feedback auto-merge has nothing to wait for, so intake breaks"

  if [ -n "$problems" ]; then
    printf '%s\n' "$problems"
    return 1
  fi
  return 0
}

assert_feedback_hub_protected() {
  local slug branch blob problems
  slug="${A2A_PUBLIC_SLUG:-ydnikolaev/a2ahub}"
  branch="${A2A_FEEDBACK_HUB_BRANCH:-feedback-hub}"

  if ! command -v gh >/dev/null 2>&1; then
    fail "release-preflight: gh is not installed, so the feedback hub's protection cannot be read.
    The hub branch is where every shipped binary files a report; unprotected, a release can
    destroy one. Install gh, or set A2A_SKIP_HUB_PROTECTION_CHECK=1 to state deliberately
    that you are cutting without that assurance."
    return 1
  fi

  if ! blob="$(gh api "repos/$slug/branches/$branch/protection" 2>/dev/null)"; then
    fail "release-preflight: could not read protection for $slug@$branch.
    Either the branch does not exist, or it carries no protection at all — and an
    unreadable answer is treated as a failing one on purpose: 'I could not check' is not
    'it is fine'."
    return 1
  fi

  if problems="$(judge_hub_protection "$blob")"; then
    ok "release-preflight: $slug@$branch refuses force-pushes and deletions, binds admins, and requires merge-policy"
    return 0
  fi

  fail "release-preflight: $slug@$branch does not satisfy the hub protection contract (P15 AC5):
$problems
    This branch is the feedback hub of record — the default submit target of every shipped
    binary. Restore the contract before cutting, or set A2A_SKIP_HUB_PROTECTION_CHECK=1 to
    record that you are cutting without it."
  return 1
}

# ── teeth: the gate must go RED on a violating fixture (offline) ──────────────

teeth() {
  local tmp rc out case1 case2 case3 case4
  tmp="$(mktemp -d)" || { echo "release-preflight --teeth: mktemp failed" >&2; exit 1; }
  trap 'rm -rf "$tmp"' RETURN

  git -C "$tmp" init -q -b main
  git -C "$tmp" config user.email teeth@example.com
  git -C "$tmp" config user.name teeth

  # v0.1.0 — BEFORE the reusable workflow exists.
  mkdir -p "$tmp/$TEMPLATE_WORKFLOWS"
  echo "placeholder" > "$tmp/README.md"
  git -C "$tmp" add -A && git -C "$tmp" commit -qm "pre-workflow"
  git -C "$tmp" tag v0.1.0

  # v0.2.0 — ADDS the reusable workflow.
  mkdir -p "$tmp/$(dirname "$REUSABLE_PATH")"
  echo "on: workflow_call" > "$tmp/$REUSABLE_PATH"
  git -C "$tmp" add -A && git -C "$tmp" commit -qm "add reusable workflow"
  git -C "$tmp" tag v0.2.0

  # --- teeth 1: a pin to the PRE-workflow tag must go RED (the P33 miss) ---
  echo "    uses: o/r/$REUSABLE_PATH@v0.1.0" > "$tmp/$TEMPLATE_WORKFLOWS/a2a-validate.yml"
  if out="$(assert_pins_resolve "$tmp" 2>&1)"; then
    echo "release-preflight --teeth: FAILED — gate stayed GREEN on a pin to a tag that predates the workflow" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "does NOT contain" || {
    echo "release-preflight --teeth: FAILED — red, but not with the predates-the-workflow message" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 1: pin to a pre-workflow tag → RED"

  # --- teeth 2: a pin to a NONEXISTENT tag must go RED ---
  echo "    uses: o/r/$REUSABLE_PATH@v9.9.9" > "$tmp/$TEMPLATE_WORKFLOWS/a2a-validate.yml"
  if out="$(assert_pins_resolve "$tmp" 2>&1)"; then
    echo "release-preflight --teeth: FAILED — gate stayed GREEN on a pin to a nonexistent tag" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 2: pin to a nonexistent tag → RED"

  # --- teeth 3: a correct pin must go GREEN (no false positive) ---
  echo "    uses: o/r/$REUSABLE_PATH@v0.2.0" > "$tmp/$TEMPLATE_WORKFLOWS/a2a-validate.yml"
  if ! out="$(assert_pins_resolve "$tmp" 2>&1)"; then
    echo "release-preflight --teeth: FAILED — gate went RED on a correct pin" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 3: correct pin → GREEN"

  # --- teeth 4 (ADD direction): a SECOND caller with a bad pin must be caught ---
  echo "    uses: o/r/$REUSABLE_PATH@v0.1.0" > "$tmp/$TEMPLATE_WORKFLOWS/extra.yml"
  if out="$(assert_pins_resolve "$tmp" 2>&1)"; then
    echo "release-preflight --teeth: FAILED — a newly ADDED caller with a bad pin slipped through" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 4: newly added caller with a bad pin → RED (ADD direction)"

  # --- teeth 4a: a reusable workflow NEW IN THE RELEASE BEING CUT → GREEN ---
  # No published tag can carry it; the runbook forbids moving the pin pre-tag.
  # This is the case that refused v0.23.0 on 2026-08-18 with nothing wrong.
  rm -f "$tmp/$TEMPLATE_WORKFLOWS/extra.yml"
  echo "on: workflow_call" > "$tmp/$REUSABLE_DIR/a2a-brandnew-reusable.yml"
  echo "    uses: o/r/$REUSABLE_DIR/a2a-brandnew-reusable.yml@v0.2.0" > "$tmp/$TEMPLATE_WORKFLOWS/a2a-brandnew.yml"
  if ! out="$(assert_pins_resolve "$tmp" v0.3.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — a workflow new in the release being cut was refused" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "NEW IN v0.3.0" || {
    echo "release-preflight --teeth: FAILED — green, but it did not name the first-release case" >&2
    echo "$out" >&2; exit 1; }
  printf '%s\n' "$out" | grep -q "space-template-baseline" || {
    echo "release-preflight --teeth: FAILED — the first-release pass did not name step 15" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 4a: reusable workflow new in the release being cut → GREEN, naming step 15"

  # --- teeth 4b: a pin to a reusable workflow that exists NOWHERE → RED ---
  # The exemption above must not become "any missing file is fine".
  rm -f "$tmp/$REUSABLE_DIR/a2a-brandnew-reusable.yml"
  if out="$(assert_pins_resolve "$tmp" v0.3.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — a pin to a workflow that exists nowhere stayed GREEN" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "exists nowhere" || {
    echo "release-preflight --teeth: FAILED — red, but not with the exists-nowhere message" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 4b: pin to a workflow present in no tag and no working tree → RED"

  # --- teeth 4c: the P33 miss must survive the exemption ---
  # A workflow that IS carried by a published tag, pinned at an older one, is
  # still the original red — and must now name the tag that carries it.
  rm -f "$tmp/$TEMPLATE_WORKFLOWS/a2a-brandnew.yml"
  echo "    uses: o/r/$REUSABLE_PATH@v0.1.0" > "$tmp/$TEMPLATE_WORKFLOWS/a2a-validate.yml"
  if out="$(assert_pins_resolve "$tmp" v0.3.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — the exemption swallowed the P33 miss" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "@v0.2.0 carries it" || {
    echo "release-preflight --teeth: FAILED — red, but it did not name the tag that carries the workflow" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 4c: pre-workflow pin still RED, and names the tag that carries it"
  echo "    uses: o/r/$REUSABLE_PATH@v0.2.0" > "$tmp/$TEMPLATE_WORKFLOWS/a2a-validate.yml"

  # --- teeth 6/7/8: the write-floor assertion ---
  # 6: a floor AHEAD of the version being cut → RED (the fleet-lockout case).
  printf 'schema: space/v1\nmin_binary_version: 9.9.9\n' > "$tmp/space-template/space.yaml"
  if out="$(assert_floor_not_ahead "$tmp" v1.0.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — gate stayed GREEN on a floor ahead of the release" >&2
    echo "$out" >&2; exit 1
  fi
  case "$out" in *AHEAD*) ;; *)
    echo "release-preflight --teeth: FAILED — red, but not with the floor-ahead message" >&2
    echo "$out" >&2; exit 1 ;;
  esac
  ok "teeth 6: template floor ahead of the release → RED"

  # 7: a floor at or below the version → GREEN (no false positive), including
  #    the equal case and a two-digit component sort -V must order correctly.
  printf 'schema: space/v1\nmin_binary_version: 0.9.0\n' > "$tmp/space-template/space.yaml"
  if ! out="$(assert_floor_not_ahead "$tmp" v0.10.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — gate went RED on 0.9.0 <= 0.10.0 (version sort is not numeric)" >&2
    echo "$out" >&2; exit 1
  fi
  printf 'schema: space/v1\nmin_binary_version: 1.0.0\n' > "$tmp/space-template/space.yaml"
  if ! out="$(assert_floor_not_ahead "$tmp" v1.0.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — gate went RED on floor == version" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 7: floor <= release (incl. equal, incl. 0.9.0 < 0.10.0) → GREEN"

  # 8: a MISSING floor → RED (space update could not decide any space's floor).
  printf 'schema: space/v1\n' > "$tmp/space-template/space.yaml"
  if out="$(assert_floor_not_ahead "$tmp" v1.0.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — gate stayed GREEN on a template with no min_binary_version" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 8: template with no min_binary_version → RED"

  # --- teeth 5: version-free assertion bites on an existing tag ---
  if out="$(assert_version_free "$tmp" v0.2.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — version-free stayed GREEN on an existing tag" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 5: cutting an already-released version → RED"

  if ! assert_version_free "$tmp" v0.3.0 >/dev/null 2>&1; then
    echo "release-preflight --teeth: FAILED — version-free went RED on a free version" >&2; exit 1
  fi
  ok "teeth 9: a free version → GREEN"

  # --- teeth 10/11/12: the a2a-ref-default assertion, both directions ---
  # This is the one that reproduces a REAL miss: v0.7.0 shipped with the default
  # still at v0.5.0, so every space at @v0.7.0 validated with a two-release-old
  # binary. Seed both the skewed and the matching shape.
  mkdir -p "$tmp/$(dirname "$REUSABLE_PATH")"
  printf 'on:\n  workflow_call:\n    inputs:\n      a2a-ref:\n        type: string\n        default: "v0.5.0"\n      space-path:\n        type: string\n' > "$tmp/$REUSABLE_PATH"
  if out="$(assert_ref_default_matches "$tmp" v0.8.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — ref-default stayed GREEN while skewed (v0.5.0 vs v0.8.0)" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 10: a2a-ref default behind the version being cut → RED"

  printf 'on:\n  workflow_call:\n    inputs:\n      a2a-ref:\n        type: string\n        default: "v0.8.0"\n      space-path:\n        type: string\n' > "$tmp/$REUSABLE_PATH"
  if ! assert_ref_default_matches "$tmp" v0.8.0 >/dev/null 2>&1; then
    echo "release-preflight --teeth: FAILED — ref-default went RED when it matched" >&2; exit 1
  fi
  ok "teeth 11: a2a-ref default equal to the version → GREEN"

  printf 'on:\n  workflow_call:\n    inputs:\n      a2a-ref:\n        type: string\n      space-path:\n        type: string\n' > "$tmp/$REUSABLE_PATH"
  if out="$(assert_ref_default_matches "$tmp" v0.8.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — ref-default stayed GREEN with NO default at all" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 12: a2a-ref input with no default at all → RED"

  # --- teeth 13-16: assert_floor_moves_with_schema, four cases, each its own
  #     throwaway repo under $tmp (cleaned up by the trap set above; no second
  #     trap is installed here, which would replace rather than compose it).
  #
  #     Each case now needs THREE points in history, because the assertion is
  #     one release late by design: prev2 (tagged v1.0.0, the base), prev
  #     (tagged v1.1.0, the release under scrutiny), and the working-tree
  #     state — which stands in for "about to cut v1.2.0, the NEXT release" —
  #     whose min_binary_version is what the assertion actually reads. ---
  _teeth_corpus_base() { # $1 = target path
    printf 'package schema\n\nvar corpusDefinitions = []corpusDefinition{\n\t{key: corpusKey{family: "envelope", version: 1, typ: "contract"}, path: "envelope/v1/contract.schema.json"},\n\t{key: corpusKey{family: "envelope", version: 2, typ: "contract"}, path: "envelope/v2/contract.schema.json"},\n}\n' > "$1"
  }
  _teeth_corpus_plus_row() { # $1 = target path — base + one NEW corpus row
    printf 'package schema\n\nvar corpusDefinitions = []corpusDefinition{\n\t{key: corpusKey{family: "envelope", version: 1, typ: "contract"}, path: "envelope/v1/contract.schema.json"},\n\t{key: corpusKey{family: "envelope", version: 2, typ: "contract"}, path: "envelope/v2/contract.schema.json"},\n\t{key: corpusKey{family: "envelope", version: 2, typ: "response"}, path: "envelope/v2/response.schema.json"},\n}\n' > "$1"
  }
  _teeth_new_case_repo() { # $1 = case dir — inits a repo, commits+tags prev2 (v1.0.0)
    local dir="$1"
    mkdir -p "$dir/internal/schema" "$dir/space-template"
    git -C "$dir" init -q -b main
    git -C "$dir" config user.email teeth@example.com
    git -C "$dir" config user.name teeth
    _teeth_corpus_base "$dir/internal/schema/corpus.go"
    printf 'schema: space/v1\nmin_binary_version: 1.0.0\n' > "$dir/space-template/space.yaml"
    git -C "$dir" add -A && git -C "$dir" commit -qm "baseline v1.0.0"
    git -C "$dir" tag v1.0.0
  }

  # case 1: prev (v1.1.0) added a corpus row vs prev2 (v1.0.0), and the
  # write floor already names prev → GREEN when preflighting v1.2.0.
  case1="$tmp/case1"
  _teeth_new_case_repo "$case1"
  _teeth_corpus_plus_row "$case1/internal/schema/corpus.go"
  printf 'schema: space/v1\nmin_binary_version: 1.1.0\n' > "$case1/space-template/space.yaml"
  git -C "$case1" add -A && git -C "$case1" commit -qm "add envelope/v2/response, raise floor"
  git -C "$case1" tag v1.1.0
  if ! out="$(assert_floor_moves_with_schema "$case1" v1.2.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — case 1 (prev added a row, floor == prev) went RED" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 13: prev added a corpus row vs prev2, floor already == prev → GREEN"

  # case 2: prev (v1.1.0) added a corpus row vs prev2 (v1.0.0), but the
  # write floor still names prev2, not prev → RED. This is Phase 4 step 15
  # having been skipped for prev's own release, caught one ceremony late.
  case2="$tmp/case2"
  _teeth_new_case_repo "$case2"
  _teeth_corpus_plus_row "$case2/internal/schema/corpus.go"
  # space.yaml untouched — floor stays 1.0.0 (prev2) while prev is tagged 1.1.0.
  git -C "$case2" add -A && git -C "$case2" commit -qm "add envelope/v2/response, floor NOT raised"
  git -C "$case2" tag v1.1.0
  if out="$(assert_floor_moves_with_schema "$case2" v1.2.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — case 2 (prev added a row, floor still at prev2) stayed GREEN" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s\n' "$out" | grep -q "envelope v2 response" || {
    echo "release-preflight --teeth: FAILED — case 2 red, but the message does not name the triple" >&2
    echo "$out" >&2; exit 1; }
  printf '%s\n' "$out" | grep -q "1\.0\.0" || {
    echo "release-preflight --teeth: FAILED — case 2 red, but the message does not name the current (wrong) floor 1.0.0" >&2
    echo "$out" >&2; exit 1; }
  printf '%s\n' "$out" | grep -q "1\.1\.0" || {
    echo "release-preflight --teeth: FAILED — case 2 red, but the message does not name the required floor 1.1.0 (prev)" >&2
    echo "$out" >&2; exit 1; }
  ok "teeth 14: prev added a corpus row vs prev2, floor still at prev2 → RED (names the triple and both versions)"

  # case 3: prev (v1.1.0) added NO corpus row vs prev2 → GREEN, regardless of
  # where the floor sits (no constraint is imposed either way).
  case3="$tmp/case3"
  _teeth_new_case_repo "$case3"
  git -C "$case3" commit -q --allow-empty -m "no schema change"
  git -C "$case3" tag v1.1.0
  # floor left lagging at prev2's value (1.0.0) — must still be GREEN.
  if ! out="$(assert_floor_moves_with_schema "$case3" v1.2.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — case 3 (prev added no row) went RED" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 15: prev added no corpus row vs prev2, floor lagging → GREEN (no constraint)"

  # case 4 — THE deliverable case: prev adds only a fixture / _test.go under
  # schemas/** (not a corpus row) with the floor still lagging at prev2 →
  # GREEN. If a file-based trigger were used instead of a corpus-row-based
  # one, this would falsely RED and the gate would get switched off.
  case4="$tmp/case4"
  _teeth_new_case_repo "$case4"
  mkdir -p "$case4/schemas/envelope/v2/fixtures" "$case4/schemas/event/v2"
  echo '{"ok": true}' > "$case4/schemas/envelope/v2/fixtures/valid.json"
  echo 'package schema_test' > "$case4/schemas/event/v2/schema_test.go"
  git -C "$case4" add -A && git -C "$case4" commit -qm "add a fixture and a _test.go under schemas/**, no corpus row"
  git -C "$case4" tag v1.1.0
  # floor untouched — still 1.0.0 (prev2) — must still be GREEN.
  if ! out="$(assert_floor_moves_with_schema "$case4" v1.2.0 2>&1)"; then
    echo "release-preflight --teeth: FAILED — case 4 (fixture/test-only file in prev, no corpus row, floor lagging) went RED" >&2
    echo "$out" >&2; exit 1
  fi
  ok "teeth 16: fixture/test-only file in prev (no new corpus row), floor lagging → GREEN"

  # The site-deploy decision, exercised offline through judge_pages_conclusion.
  # Every non-success conclusion must refuse — including the empty one. "The
  # workflow's status could not be read" is not evidence that the site is fine,
  # and treating unknown as green would reproduce the exact silence this gate
  # exists to end: four releases published while the site stayed at 0.18.2 and
  # nothing said so.
  judge_pages_conclusion success || {
    echo "release-preflight --teeth: FAILED — a green pages run was refused" >&2
    exit 1
  }
  for bad in failure cancelled startup_failure timed_out skipped "" action_required; do
    if judge_pages_conclusion "$bad"; then
      echo "release-preflight --teeth: FAILED — pages conclusion '${bad:-<empty>}' was accepted as publishable" >&2
      exit 1
    fi
  done

  # ── P15 AC5: the feedback hub's protection contract ────────────────────────
  #
  # Exercised against constructed blobs, never the network: these must be able
  # to fail on a laptop with no credentials, and the network half is one
  # `gh api` call whose only job is to fetch what this judges.
  _hub_blob() { # $1=force $2=deletions $3=admins $4=contexts-json
    printf '{"allow_force_pushes":{"enabled":%s},"allow_deletions":{"enabled":%s},"enforce_admins":{"enabled":%s},"required_status_checks":{"contexts":%s}}' \
      "$1" "$2" "$3" "$4"
  }

  if ! judge_hub_protection "$(_hub_blob false false true '["merge-policy"]')" >/dev/null; then
    echo "release-preflight --teeth: FAILED — the correct hub protection contract was refused" >&2
    exit 1
  fi
  echo "  ✓ teeth 17: hub protection correct → GREEN"

  hub_out="$(judge_hub_protection "$(_hub_blob true false true '["merge-policy"]')" || true)"
  case "$hub_out" in
    *"force-pushes are NOT refused"*) echo "  ✓ teeth 18: hub allows force-push → RED (names it)" ;;
    *) echo "release-preflight --teeth: FAILED — a hub allowing force-push was not refused by name: $hub_out" >&2; exit 1 ;;
  esac

  # Deletion is the clause AC5's own wording would have missed. A deleted
  # branch destroys the corpus exactly as thoroughly as overwriting it.
  hub_out="$(judge_hub_protection "$(_hub_blob false true true '["merge-policy"]')" || true)"
  case "$hub_out" in
    *"branch deletion is NOT refused"*) echo "  ✓ teeth 19: hub allows deletion → RED (the clause AC5 did not name)" ;;
    *) echo "release-preflight --teeth: FAILED — a deletable hub was not refused by name: $hub_out" >&2; exit 1 ;;
  esac

  # Without enforce_admins the rule binds everyone EXCEPT the identity the
  # phase defends against: the publisher runs with an admin token.
  hub_out="$(judge_hub_protection "$(_hub_blob false false false '["merge-policy"]')" || true)"
  case "$hub_out" in
    *"enforce_admins is off"*) echo "  ✓ teeth 20: hub exempts admins → RED (the publisher IS an admin)" ;;
    *) echo "release-preflight --teeth: FAILED — an admin-exempt hub was not refused by name: $hub_out" >&2; exit 1 ;;
  esac

  hub_out="$(judge_hub_protection "$(_hub_blob false false true '[]')" || true)"
  case "$hub_out" in
    *"merge-policy is not a required context"*) echo "  ✓ teeth 21: hub drops merge-policy → RED (intake auto-merge breaks)" ;;
    *) echo "release-preflight --teeth: FAILED — a hub with no required context was not refused by name: $hub_out" >&2; exit 1 ;;
  esac

  # An EMPTY protection blob is the shape an unprotected branch returns, and
  # it must fail on every clause rather than throw.
  hub_out="$(judge_hub_protection '{}' || true)"
  case "$hub_out" in
    *"force-pushes are NOT refused"*"merge-policy is not a required context"*)
      echo "  ✓ teeth 22: an unprotected hub reds on every clause at once" ;;
    *) echo "release-preflight --teeth: FAILED — an empty protection blob did not red on every clause: $hub_out" >&2; exit 1 ;;
  esac

  echo "release-preflight --teeth: all teeth bite."
}

# ── entrypoint ───────────────────────────────────────────────────────────────

if [ "${1:-}" = "--teeth" ]; then
  teeth
  exit 0
fi

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "usage: bash scripts/release-preflight.sh <version-to-cut>   # e.g. v0.6.0" >&2
  echo "       bash scripts/release-preflight.sh --teeth" >&2
  exit 2
fi

ROOT="$(git rev-parse --show-toplevel)"

if ! git -C "$ROOT" remote get-url "$REMOTE" >/dev/null 2>&1; then
  echo "release-preflight: no '$REMOTE' remote — set A2A_RELEASE_REMOTE to the release remote." >&2
  exit 1
fi

echo "release-preflight: fetching tags from '$REMOTE' (the authoritative release state)…"
git -C "$ROOT" fetch --quiet "$REMOTE" --tags

rc=0
assert_version_free "$ROOT" "$VERSION" || rc=1
assert_pins_resolve "$ROOT" "$VERSION" || rc=1
assert_floor_not_ahead "$ROOT" "$VERSION" || rc=1
assert_floor_moves_with_schema "$ROOT" "$VERSION" || rc=1
assert_ref_default_matches "$ROOT" "$VERSION" || rc=1
if [ "${A2A_SKIP_PAGES_CHECK:-}" = "1" ]; then
  echo "release-preflight: SKIPPING the site-deploy check (A2A_SKIP_PAGES_CHECK=1) — the site may not announce $VERSION."
else
  assert_site_publishes "$ROOT" || rc=1
fi
if [ "${A2A_SKIP_HUB_PROTECTION_CHECK:-}" = "1" ]; then
  echo "release-preflight: SKIPPING the feedback-hub protection check (A2A_SKIP_HUB_PROTECTION_CHECK=1) — a release cut now could destroy an inbound report."
else
  assert_feedback_hub_protected || rc=1
fi

if [ "$rc" -ne 0 ]; then
  echo "release-preflight: FAIL — do not cut $VERSION until the above is fixed." >&2
  exit 1
fi
echo "release-preflight: OK — safe to cut $VERSION."
