#!/usr/bin/env bash
# check-release-notes-freshness.sh — keep user-visible product commits from
# outrunning the authored release-notes corpus.
#
# The anchor is the last commit touching the highest semver-shaped
# releasenotes/*.yaml file, never a local tag: public-release tags are cut in a
# filtered repository and may not exist in this checkout. Every later
# feat/fix/perf or breaking-marked conventional commit that touches a product
# surface must be covered by a newer notes commit.

# lane-inputs:
#   releasenotes/*.yaml
#   internal/**
#   cmd/**
#   schemas/**
#   space-template/**
#   skill/**
#   !internal/livee2e/**
#   !internal/lane/**
#   !internal/e2e/**
#   !**/*_test.go
set -uo pipefail

SCRIPT_ABS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

latest_notes_file() {
  git -C "$ROOT" ls-files 'releasenotes/*.yaml' |
    awk -F'[/.]' '
      $2 ~ /^[0-9]+$/ && $3 ~ /^[0-9]+$/ && $4 ~ /^[0-9]+$/ && $5 == "yaml" {
        printf "%012d.%012d.%012d\t%s\n", $2, $3, $4, $0
      }
    ' |
    sort |
    tail -n 1 |
    cut -f2-
}

needs_notes() {
  case "$1" in
    feat:\ *|feat\(*\):\ *|feat!:\ *|feat\(*\)!:\ *|\
    fix:\ *|fix\(*\):\ *|fix!:\ *|fix\(*\)!:\ *|\
    perf:\ *|perf\(*\):\ *|perf!:\ *|perf\(*\)!:\ *|\
    [a-z]*!:\ *|[a-z]*\(*\)!:\ *)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

run_check() {
  local newest anchor
  newest="$(latest_notes_file)"
  if [ -z "$newest" ]; then
    echo "release-notes-freshness: FAIL — no tracked semver releasenotes/*.yaml file found." >&2
    return 1
  fi
  anchor="$(git -C "$ROOT" log -1 --format=%H -- "$newest")"
  if [ -z "$anchor" ]; then
    echo "release-notes-freshness: FAIL — $newest has no commit anchor." >&2
    return 1
  fi

  local uncovered=""
  while IFS=$'\t' read -r commit subject; do
    [ -n "$commit" ] || continue
    if needs_notes "$subject"; then
      uncovered+="${commit}"$'\t'"${subject}"$'\n'
    fi
  done < <(
    # Two exclusions, both saying the same thing: a release note describes
    # behaviour a USER can observe, and neither of these ships any.
    #
    # `_test.go` is the stronger of the two, because it is true by
    # construction rather than by convention: the Go toolchain does not put a
    # test file in the binary, so a commit touching only test files cannot
    # have changed what anyone runs. Demanding a note for one teaches exactly
    # the wrong reflex — invent a note, or reword an unrelated entry to
    # re-anchor the gate.
    #
    # internal/livee2e is the live TEST tier: it ships in the public tree, but
    # its only consumer is `make live-e2e` and most of its files are NOT
    # `_test.go` (they are `//go:build livee2e` scenario bodies), so the rule
    # above does not reach them. A `fix(livee2e)` commit repairs a scenario,
    # never behaviour a note could describe.
    #
    # internal/lane is excluded on the same principle as internal/livee2e and
    # for a reason that is checkable rather than asserted: it is the P12 lane
    # deriver, imported by NOTHING in the module (only its own //go:build
    # ignore runner), so it is never linked into cmd/a2a and cannot reach a
    # user. A release note about it would not be missing information — it would
    # be false, telling a reader about something that does not exist for them.
    # The exclusion holds ONLY ALONE, exactly like livee2e's: a commit that
    # also touches real product code still reds.
    #
    # internal/e2e is the offline integration tier and belongs to the same
    # class, by the same checkable test: nothing outside the package imports
    # it, so it is never linked into cmd/a2a. Most of it IS `_test.go` and was
    # already covered; what was not is coverage.go, the coverage manifest the
    # tier's own parity gate reads. Added 2026-08-11 (P11 wave C), where a
    # commit that only made that manifest declare its evidence tier demanded a
    # release note for a change no user can observe — the exact reflex the
    # first paragraph above warns against.
    #
    # The teeth below pin every half: a test-only commit stays green, a
    # livee2e-only commit stays green, a lane-only commit stays green, an
    # e2e-only commit stays green, and a commit that ALSO touches real product
    # code still reds in every case.
    #
    # testdata/ joined the list on 2026-08-21, by the same checkable test and
    # for the same reason as the three packages above. It is the directory the
    # Go toolchain itself ignores — nothing under it is ever compiled into
    # cmd/a2a — so no user can observe a byte of it. It was found the way the
    # e2e exclusion was: a commit that ONLY regenerated
    # internal/cli/testdata/html-demo-json.golden after a release-notes file
    # was added demanded a release note for its own bookkeeping. The golden
    # embeds the notes corpus, so the notes edit moved it; asking that commit
    # to describe a user-visible change would have meant inventing one.
    #
    # `*_test.go` above already covered most fixtures. What it missed is
    # exactly the ones that are NOT Go source — goldens, txtar scripts, JSON
    # and YAML fixtures — which is most of what testdata/ holds.
    git -C "$ROOT" log "$anchor..HEAD" --format='%H%x09%s' -- \
      internal/ cmd/ schemas/ space-template/ skill/ \
      ':(exclude)internal/livee2e/' \
      ':(exclude)internal/lane/' \
      ':(exclude)internal/e2e/' \
      ':(exclude,glob)**/testdata/**' \
      ':(exclude,glob)**/*_test.go'
  )

  if [ -n "$uncovered" ]; then
    echo "release-notes-freshness: FAIL — product commits landed after $newest was last authored:" >&2
    while IFS=$'\t' read -r commit subject; do
      [ -n "$commit" ] || continue
      echo "  ${commit:0:12} $subject" >&2
    done <<< "$uncovered"
    echo "Add the next releasenotes/<version>.yaml entry (or amend the newest notes if it is still unreleased)." >&2
    return 1
  fi

  echo "release-notes-freshness: $newest covers every later feat/fix/perf/breaking product commit."
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || {
    echo "release-notes-freshness --teeth: mktemp failed" >&2
    exit 1
  }
  trap "rm -rf -- '$tmp'" EXIT

  git -C "$tmp" init -q -b main
  git -C "$tmp" config user.email teeth@example.invalid
  git -C "$tmp" config user.name "release notes teeth"
  mkdir -p "$tmp/releasenotes" "$tmp/internal/widget" "$tmp/docs"
  printf '%s\n' 'version: "0.1.0"' >"$tmp/releasenotes/0.1.0.yaml"
  printf '%s\n' 'package widget' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add releasenotes/0.1.0.yaml internal/widget/widget.go
  git -C "$tmp" commit -q -m 'chore: seed release'

  printf '%s\n' 'package widget' 'const Fixed = true' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add internal/widget/widget.go
  git -C "$tmp" commit -q -m 'fix(widget): close visible defect'
  local bad_commit out rc
  bad_commit="$(git -C "$tmp" rev-parse --short=12 HEAD)"
  out="$(ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check 2>&1)"
  rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q "$bad_commit" <<<"$out"; then
    echo "release-notes-freshness --teeth: FAILED — uncovered fix did not red with its commit id:" >&2
    echo "$out" >&2
    exit 1
  fi

  printf '%s\n' 'version: "0.2.0"' >"$tmp/releasenotes/0.2.0.yaml"
  git -C "$tmp" add releasenotes/0.2.0.yaml
  git -C "$tmp" commit -q -m 'docs: author 0.2.0 release notes'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — a newer notes commit did not green the gate." >&2
    exit 1
  fi

  printf '%s\n' 'documentation only' >"$tmp/docs/readme.md"
  git -C "$tmp" add docs/readme.md
  git -C "$tmp" commit -q -m 'docs: explain widget'
  printf '%s\n' 'package widget' 'const Fixed = true' 'const Metadata = true' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add internal/widget/widget.go
  git -C "$tmp" commit -q -m 'chore(widget): refresh metadata'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — docs/chore commits demanded release notes." >&2
    exit 1
  fi

  printf '%s\n' 'package widget' 'const Fixed = true' 'const Metadata = true' 'const Breaking = true' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add internal/widget/widget.go
  git -C "$tmp" commit -q -m 'refactor(widget)!: remove legacy behavior'
  out="$(ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check 2>&1)"
  rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q 'refactor(widget)!:' <<<"$out"; then
    echo "release-notes-freshness --teeth: FAILED — breaking product commit stayed green:" >&2
    echo "$out" >&2
    exit 1
  fi

  # Re-anchor: the breaking commit above deliberately left the fixture red, and
  # the next two assertions are about what a livee2e commit does to a GREEN
  # gate. Without this they would inherit that red and pass for the wrong
  # reason — which is how a teeth test stops testing anything.
  printf '%s\n' 'version: "0.3.0"' >"$tmp/releasenotes/0.3.0.yaml"
  git -C "$tmp" add releasenotes/0.3.0.yaml
  git -C "$tmp" commit -q -m 'docs: author 0.3.0 release notes'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — re-anchor did not green the gate." >&2
    exit 1
  fi

  # The live test tier is excluded from the product surface — but only alone.
  mkdir -p "$tmp/internal/livee2e"
  printf '%s\n' 'package livee2e' >"$tmp/internal/livee2e/scenario.go"
  git -C "$tmp" add internal/livee2e/scenario.go
  git -C "$tmp" commit -q -m 'fix(livee2e): repair a scenario step'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — a livee2e-only fix demanded release notes." >&2
    exit 1
  fi

  mkdir -p "$tmp/internal/lane"
  printf '%s\n' 'package lane' >"$tmp/internal/lane/derive.go"
  git -C "$tmp" add internal/lane/derive.go
  git -C "$tmp" commit -q -m 'fix(agent-ops): repair the lane deriver'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — a lane-only fix demanded release notes." >&2
    exit 1
  fi

  mkdir -p "$tmp/internal/e2e"
  printf '%s\n' 'package e2e' >"$tmp/internal/e2e/coverage.go"
  git -C "$tmp" add internal/e2e/coverage.go
  git -C "$tmp" commit -q -m 'fix(agent-exchange): declare the coverage manifest tier'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — an e2e-only fix demanded release notes." >&2
    exit 1
  fi

  # And the e2e exclusion holds ONLY ALONE, like the two above it.
  printf '%s\n' 'package e2e' 'const Touched = true' >"$tmp/internal/e2e/coverage.go"
  mkdir -p "$tmp/internal/widget"
  printf '%s\n' 'package widget' 'const Fixed = true' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add internal/e2e/coverage.go internal/widget/widget.go
  git -C "$tmp" commit -q -m 'fix(cli): repair a widget, and touch e2e alongside it'
  if ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null 2>&1; then
    echo "release-notes-freshness --teeth: FAILED — an e2e commit that ALSO touched product code stayed green." >&2
    exit 1
  fi
  # Re-anchor with a version of its own, not 0.4.0 — the assertions further
  # down author 0.4.0 themselves, and a duplicate here leaves them with
  # nothing to commit and therefore no anchor move.
  printf '%s\n' 'version: "0.3.5"' >"$tmp/releasenotes/0.3.5.yaml"
  git -C "$tmp" add releasenotes/0.3.5.yaml
  git -C "$tmp" commit -q -m 'docs: author 0.3.5 release notes'

  printf '%s\n' 'package lane' 'const Touched = true' >"$tmp/internal/lane/derive.go"
  mkdir -p "$tmp/internal/gadget"
  printf '%s\n' 'package gadget' 'const Fixed = true' >"$tmp/internal/gadget/gadget.go"
  git -C "$tmp" add internal/lane/derive.go internal/gadget/gadget.go
  git -C "$tmp" commit -q -m 'fix(gadget): repair behaviour, and the lane that selects it'
  out="$(ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check 2>&1)"
  rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q 'fix(gadget): repair behaviour' <<<"$out"; then
    echo "release-notes-freshness --teeth: FAILED — a commit touching product AND lane stayed green:" >&2
    echo "$out" >&2
    exit 1
  fi

  printf '%s\n' 'package livee2e' 'const Touched = true' >"$tmp/internal/livee2e/scenario.go"
  printf '%s\n' 'package widget' 'const Fixed = true' 'const Metadata = true' 'const Breaking = true' 'const AlsoProduct = true' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add internal/livee2e/scenario.go internal/widget/widget.go
  git -C "$tmp" commit -q -m 'fix(widget): repair behaviour, and its live row'
  out="$(ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check 2>&1)"
  rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q 'fix(widget): repair behaviour' <<<"$out"; then
    echo "release-notes-freshness --teeth: FAILED — a commit touching product AND livee2e stayed green:" >&2
    echo "$out" >&2
    exit 1
  fi

  # Re-anchor again: the assertion above deliberately left the fixture red, and
  # the two below are about what a TEST-ONLY commit does to a GREEN gate.
  printf '%s\n' 'version: "0.4.0"' >"$tmp/releasenotes/0.4.0.yaml"
  git -C "$tmp" add releasenotes/0.4.0.yaml
  git -C "$tmp" commit -q -m 'docs: author 0.4.0 release notes'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — the second re-anchor did not green the gate." >&2
    exit 1
  fi

  # A _test.go file cannot ship user-visible behaviour: the Go toolchain does
  # not put it in the binary. Note this one sits under internal/widget, NOT
  # under the internal/livee2e path the earlier exclusion covers — so it can
  # only stay green through the *_test.go rule itself.
  printf '%s\n' 'package widget' 'func TestFixed(t *testing.T) {}' >"$tmp/internal/widget/widget_test.go"
  git -C "$tmp" add internal/widget/widget_test.go
  git -C "$tmp" commit -q -m 'fix(widget): cover the repaired behaviour'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — a test-only fix demanded release notes." >&2
    exit 1
  fi

  printf '%s\n' 'package widget' 'func TestFixed(t *testing.T) {}' 'func TestMore(t *testing.T) {}' >"$tmp/internal/widget/widget_test.go"
  printf '%s\n' 'package widget' 'const Fixed = true' 'const Metadata = true' 'const Breaking = true' 'const AlsoProduct = true' 'const AndAgain = true' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add internal/widget/widget_test.go internal/widget/widget.go
  git -C "$tmp" commit -q -m 'fix(widget): repair behaviour, and cover it'
  out="$(ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check 2>&1)"
  rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q 'fix(widget): repair behaviour, and cover it' <<<"$out"; then
    echo "release-notes-freshness --teeth: FAILED — a commit touching product AND its test stayed green:" >&2
    echo "$out" >&2
    exit 1
  fi

  # Re-anchor a third time: the pair above deliberately left the fixture red,
  # and the two below are about what a TESTDATA-ONLY commit does to a GREEN
  # gate. Inheriting the previous red would let them pass for the wrong reason.
  printf '%s\n' 'version: "0.5.0"' >"$tmp/releasenotes/0.5.0.yaml"
  git -C "$tmp" add releasenotes/0.5.0.yaml
  git -C "$tmp" commit -q -m 'docs: author 0.5.0 release notes'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — the third re-anchor did not green the gate." >&2
    exit 1
  fi

  # testdata/ — the directory the Go toolchain itself ignores. A fixture there
  # is not Go source, so the *_test.go rule above cannot reach it, and it sits
  # under internal/widget rather than any excluded package, so it can only stay
  # green through the testdata rule itself. This is the real shape: a golden
  # that embeds the release-notes corpus, regenerated because a notes file was
  # added.
  mkdir -p "$tmp/internal/widget/testdata"
  printf '%s\n' '{"regenerated": true}' >"$tmp/internal/widget/testdata/demo.golden"
  git -C "$tmp" add internal/widget/testdata/demo.golden
  git -C "$tmp" commit -q -m 'fix(widget): regenerate the golden after a notes edit'
  if ! ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check >/dev/null; then
    echo "release-notes-freshness --teeth: FAILED — a testdata-only regeneration demanded release notes." >&2
    exit 1
  fi

  # And ONLY ALONE, exactly like every exclusion above it.
  printf '%s\n' '{"regenerated": true, "again": true}' >"$tmp/internal/widget/testdata/demo.golden"
  printf '%s\n' 'package widget' 'const Fixed = true' 'const Metadata = true' 'const Breaking = true' 'const AlsoProduct = true' 'const AndAgain = true' 'const WithGolden = true' >"$tmp/internal/widget/widget.go"
  git -C "$tmp" add internal/widget/testdata/demo.golden internal/widget/widget.go
  git -C "$tmp" commit -q -m 'fix(widget): change behaviour and its golden together'
  out="$(ROOT="$tmp" bash "$SCRIPT_ABS" _internal-check 2>&1)"
  rc=$?
  if [ "$rc" -eq 0 ] || ! grep -q 'fix(widget): change behaviour and its golden together' <<<"$out"; then
    echo "release-notes-freshness --teeth: FAILED — a commit touching product AND a golden stayed green:" >&2
    echo "$out" >&2
    exit 1
  fi

  echo "release-notes-freshness --teeth: uncovered fix reds with id; notes touch greens; docs/chore stay green; breaking commit reds; livee2e-only greens; livee2e+product reds; lane-only greens; lane+product reds; e2e-only greens; e2e+product reds; test-only greens; test+product reds; testdata-only greens; testdata+product reds."
}

case "${1:-check}" in
  check) run_check ;;
  --teeth) run_teeth ;;
  _internal-check) run_check ;;
  *)
    echo "usage: $0 [check|--teeth]" >&2
    exit 2
    ;;
esac
