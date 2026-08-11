#!/usr/bin/env bash
# check-prose-roster.sh — a new hand-maintained prose page under
# `skill/a2ahub/` must be registered in every place that names the roster of
# hand-maintained files, not just some of them. `skill/RELEASE-CHECKLIST.md`'s
# own last row records this failing TWICE — `reference/feedback.md` and
# `reference/data-exchange.md` each shipped with no review row — and asks for
# exactly this gate, priced at "a dozen lines of shell". This is that gate.
#
# FIVE lists are compared. The first is the ground truth; the other four each
# name their own subset of it and must match exactly, an omission-or-excess
# both being a real bug:
#
#   1. ON DISK   — every `*.md` under `skill/a2ahub/` MINUS the generated
#      tree (`reference/commands.md`, `reference/authoring/*.md` — byte-gated
#      by `skill-drift` instead, per this file's own D-015 section and
#      `skill/RELEASE-CHECKLIST.md`'s header).
#   2. TOC       — `skill/a2ahub/SKILL.md`'s `## Table of contents` table,
#      one markdown-link row per file (generated rows included there for a
#      human reader, but excluded from THIS comparison since they are not
#      hand-maintained).
#   3. D-015     — the prose enumeration in `SKILL.md`'s own
#      `## Sourcing & drift (D-015)` section, the backtick-quoted file list
#      in its FIRST sentence only (its second paragraph backtick-quotes the
#      generated files BY NAME to explain the exemption — reading the whole
#      section would misread that explanation as a hand-maintained claim).
#   4. CHECKLIST — `skill/RELEASE-CHECKLIST.md`'s
#      `## Prose files under review` table, first cell of each row only (a
#      cell's PROSE may mention another file in backticks — e.g. the SKILL.md
#      row's cell names `reference/commands.md` — and a naive whole-row scan
#      would misfile that mention as a roster entry).
#   5. MANIFEST  — `skill/a2ahub/docs-manifest.json`'s `file` fields, filtered
#      to the same generated exclusion as list 1.
#
# Decision: docs-manifest.json IS a fifth list, not just read by a sibling
# gate. `check-loop-reachability.sh` reads it as the reachability universe,
# but only checks that pages IT LISTS are reachable — it has no opinion on
# whether a hand-maintained page that exists is listed there at all. That is
# exactly the "shipped with no row" failure mode this gate exists to close,
# just at a different registration point than the two documented incidents,
# so leaving it out would reopen the same hole one list over.
#
# Two rows in RELEASE-CHECKLIST.md's table are NOT ordinary per-file rows,
# and are excluded from list 4 rather than forced to match a file that isn't
# theirs:
#   - `skill/RELEASE-CHECKLIST.md` (this file) — its own completeness row,
#     reviewing itself. It is not a `skill/a2ahub/**` file at all (the
#     extraction below requires the first cell to contain the literal
#     substring `skill/a2ahub/`, which this row's cell does not), so it is
#     dropped by construction rather than by a name-specific special case.
#   - `skill/a2ahub/SKILL.md` § "Which surface to work through" — a SECTION
#     of SKILL.md, not a second file. Dropped because its cell contains `§`;
#     SKILL.md itself still has its own ordinary row and is not lost.
#
# One asymmetry is real and intentional, not a bug to paper over: SKILL.md
# does not list itself in its own `## Table of contents` (no file lists
# itself in its own table of contents) — but D-015, the checklist, and the
# manifest all name SKILL.md as an ordinary entry, because those three are
# not "this file's own index of other files", they are external rosters that
# happen to live in / next to it. So SKILL.md is exempted ONLY from the TOC
# membership requirement (list 2); all other lists require it like any other
# hand-maintained page.
#
# Parsed with grep/sed, not jq — jq is not a dependency of `make check`
# (`check-view-vocabulary.sh`'s own precedent); `docs-manifest.json` is one
# JSON object per physical line, the shape `check-loop-reachability.sh`
# already relies on.
#
# Usage: bash scripts/check-prose-roster.sh            # check the real tree
#        bash scripts/check-prose-roster.sh --teeth    # self-test on fixtures

# lane-inputs:
#   skill/a2ahub/**
#   skill/RELEASE-CHECKLIST.md
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# is_generated reports (via exit status) whether $1 — a path relative to
# skill/a2ahub/ — is part of the generated tree: the command catalogue or any
# per-type authoring guide. Both are byte-gated by `skill-drift`, never
# hand-reviewed, and must never appear in a hand-maintained roster list.
is_generated() { # $1 = relpath
  case "$1" in
    reference/commands.md) return 0 ;;
    reference/authoring/*) return 0 ;;
    *) return 1 ;;
  esac
}

# extract_section prints every line strictly between the line matching the
# awk regex $2 (a `## ` heading, matched whole-line) and the next `## `
# heading (or EOF). Neither heading line itself is printed.
#
# A literal `(`/`)` in $2 must be passed doubly-escaped (`\\(`, `\\)`):
# macOS's awk (the one-true-awk on this machine, 20200816) runs one extra
# round of backslash processing on a `-v`-assigned string before it is used
# as a dynamic regexp, so a single `\(` arrives as a bare `(` (silent
# non-match) — confirmed empirically against a literal `/\(…\)/` in the same
# awk, which matches correctly with single escaping.
extract_section() { # $1 = file, $2 = start-heading awk regex (whole-line anchored)
  awk -v start="$2" '
    $0 ~ start { infound=1; next }
    infound && /^## / { exit }
    infound { print }
  ' "$1"
}

# disk_hand_maintained prints every hand-maintained `*.md` relpath (relative
# to $1) under $1 — the ground-truth list.
disk_hand_maintained() { # $1 = skill_dir (…/skill/a2ahub)
  local skill_dir="$1" f rel
  while IFS= read -r f; do
    rel="${f#"$skill_dir"/}"
    is_generated "$rel" && continue
    printf '%s\n' "$rel"
  done < <(find "$skill_dir" -name '*.md' -type f)
}

# toc_hand_maintained prints the hand-maintained relpaths named as markdown
# links in SKILL.md's `## Table of contents` table (generated rows, and the
# `reference/authoring/` directory row, dropped).
toc_hand_maintained() { # $1 = SKILL.md path
  local href
  while IFS= read -r href; do
    [ -z "$href" ] && continue
    case "$href" in
      */) continue ;; # a directory link (reference/authoring/) — generated
    esac
    is_generated "$href" && continue
    printf '%s\n' "$href"
  done < <(
    extract_section "$1" '^## Table of contents$' |
      grep -oE '^\| \[[^]]*\]\([^)]+\)' |
      sed -E 's/^\| \[[^]]*\]\(//; s/\)$//'
  )
}

# d015_list prints the backtick-quoted file names in SKILL.md's D-015
# section, FIRST SENTENCE only (up to and including the line containing
# "hand-maintained" — the sentence's own close). The section's second
# paragraph names the generated files in backticks too, to explain why they
# are exempt; reading past the cut would wrongly harvest those as roster
# entries.
d015_list() { # $1 = SKILL.md path
  extract_section "$1" '^## Sourcing & drift \\(D-015\\)$' |
    sed -n '1,/hand-maintained/p' |
    grep -oE '`[^`]+`' |
    tr -d '`'
}

# checklist_hand_maintained prints the relpaths (stripped of the
# `skill/a2ahub/` prefix) named in RELEASE-CHECKLIST.md's review table, first
# cell of each data row only. A row is a data row if its first cell starts
# with a backtick (`| \``); this naturally excludes the header and separator
# rows. Two rows are dropped by the filters below rather than by name (see
# this script's header): the checklist's own self-row (first cell has no
# `skill/a2ahub/` substring) and the SKILL.md section row (first cell
# contains `§`).
checklist_hand_maintained() { # $1 = RELEASE-CHECKLIST.md path
  local line cell path
  while IFS= read -r line; do
    cell="$(printf '%s' "$line" | sed -E 's/^\| *//; s/ *\|.*$//')"
    case "$cell" in
      *'§'*) continue ;;
    esac
    case "$cell" in
      *'skill/a2ahub/'*) : ;;
      *) continue ;;
    esac
    path="$(printf '%s' "$cell" | grep -oE '`[^`]+`' | head -1 | tr -d '`')"
    path="${path#skill/a2ahub/}"
    [ -n "$path" ] && printf '%s\n' "$path"
  done < <(
    extract_section "$1" '^## Prose files under review \\(D-015 hand-maintained set\\)$' |
      grep -E '^\| `'
  )
}

# manifest_hand_maintained prints every hand-maintained relpath named by a
# `"file":"…"` field in docs-manifest.json (one JSON object per physical
# line — the shape `check-loop-reachability.sh` already relies on), with the
# leading skill-directory segment stripped, generated entries dropped.
manifest_hand_maintained() { # $1 = docs-manifest.json path
  local line file rel
  while IFS= read -r line; do
    file="$(printf '%s' "$line" | grep -oE '"file":"[^"]+"' | sed -E 's/^"file":"//; s/"$//')"
    [ -n "$file" ] || continue
    rel="$(printf '%s' "$file" | sed -E 's#^[^/]+/##')"
    is_generated "$rel" && continue
    printf '%s\n' "$rel"
  done <"$1"
}

# compare_list reports every disagreement between $2 (required, the on-disk
# ground truth) and $3 (actual, one derived list), by name:
#   - a required page absent from actual (unless listed in $4, the
#     space-separated allowed-missing set — used only for SKILL.md's TOC
#     self-omission);
#   - an actual entry that is a GENERATED page (must never appear in a
#     hand-maintained list);
#   - an actual entry that names no page on disk at all.
compare_list() { # $1 = list name, $2 = required (newline list), $3 = actual (newline list), $4 = allowed-missing (space list, optional)
  local name="$1" required="$2" actual="$3" allowed="${4:-}"
  local page
  while IFS= read -r page; do
    [ -z "$page" ] && continue
    case " $allowed " in
      *" $page "*) continue ;;
    esac
    if ! printf '%s\n' "$actual" | grep -qxF "$page"; then
      gate_fail "prose-roster: $page is a hand-maintained file on disk but is missing from $name"
    fi
  done <<<"$required"
  while IFS= read -r page; do
    [ -z "$page" ] && continue
    if is_generated "$page"; then
      gate_fail "prose-roster: $name lists $page, which is a GENERATED page — it is byte-gated by skill-drift, not hand-reviewed, and must not appear in $name"
    elif ! printf '%s\n' "$required" | grep -qxF "$page"; then
      gate_fail "prose-roster: $name lists $page, which is not a hand-maintained file on disk under skill/a2ahub/"
    fi
  done <<<"$actual"
}

# run_check compares all five lists for the tree rooted at $1 (GATE_ROOT for
# a real run, a fixture directory for --teeth).
run_check() { # $1 = root
  local root="$1"
  local skill_dir="$root/skill/a2ahub"
  local skill_md="$skill_dir/SKILL.md"
  local checklist="$root/skill/RELEASE-CHECKLIST.md"
  local manifest="$skill_dir/docs-manifest.json"

  if [ ! -f "$skill_md" ]; then
    gate_fail "prose-roster: $skill_md does not exist"
    gate_summary "prose-roster"
    return $?
  fi
  if [ ! -f "$checklist" ]; then
    gate_fail "prose-roster: $checklist does not exist"
    gate_summary "prose-roster"
    return $?
  fi
  if [ ! -f "$manifest" ]; then
    gate_fail "prose-roster: $manifest does not exist"
    gate_summary "prose-roster"
    return $?
  fi

  local required
  required="$(disk_hand_maintained "$skill_dir" | sort -u)"
  if [ -z "$required" ]; then
    gate_fail "prose-roster: no hand-maintained *.md found under $skill_dir — failing closed rather than policing nothing"
    gate_summary "prose-roster"
    return $?
  fi

  compare_list "the TOC (SKILL.md § Table of contents)" "$required" "$(toc_hand_maintained "$skill_md")" "SKILL.md"
  compare_list "the D-015 list (SKILL.md § Sourcing & drift)" "$required" "$(d015_list "$skill_md")"
  compare_list "RELEASE-CHECKLIST.md's review table" "$required" "$(checklist_hand_maintained "$checklist")"
  compare_list "docs-manifest.json" "$required" "$(manifest_hand_maintained "$manifest")"

  gate_summary "prose-roster"
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || { echo "prose-roster --teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN
  mkdir -p "$tmp/skill/a2ahub/reference/authoring"

  # write_good_fixture writes a small but FULLY consistent synthetic tree —
  # never a mutation of the real skill/. Ground truth (on disk, minus
  # generated): SKILL.md, alpha.md, reference/beta.md — three files. Plus one
  # generated file each of the two generated shapes, present on disk but
  # excluded from every hand-maintained list. Every mutation below starts
  # from this baseline and breaks exactly one thing.
  write_good_fixture() {
    cat >"$tmp/skill/a2ahub/SKILL.md" <<'EOF'
# fixture skill

## Table of contents

| File | What it carries |
|------|-----------------|
| [alpha.md](alpha.md) | Alpha fixture. |
| [reference/beta.md](reference/beta.md) | Beta fixture. |
| [reference/commands.md](reference/commands.md) | **Generated from the binary.** |
| [reference/authoring/](reference/authoring/) | **Generated from schemas.** |

## Sourcing & drift (D-015)

The prose files in this skill — `SKILL.md`, `alpha.md`, `reference/beta.md` — are **hand-maintained** and single-sourced here.

The `reference/commands.md` and `reference/authoring/*.md` files are **generated** and are byte-diffed by the `skill-drift` CI job — do not hand-edit them.
EOF
    printf '# alpha fixture\n' >"$tmp/skill/a2ahub/alpha.md"
    printf '# beta fixture\n' >"$tmp/skill/a2ahub/reference/beta.md"
    printf '# commands fixture (generated)\n' >"$tmp/skill/a2ahub/reference/commands.md"
    printf '# gamma fixture (generated)\n' >"$tmp/skill/a2ahub/reference/authoring/gamma.md"
    cat >"$tmp/skill/RELEASE-CHECKLIST.md" <<'EOF'
# fixture checklist

## Prose files under review (D-015 hand-maintained set)

| Prose file | Reviewed |
|------------|:--------:|
| `skill/a2ahub/SKILL.md` | ☑ |
| `skill/a2ahub/alpha.md` | ☑ |
| `skill/a2ahub/reference/beta.md` | ☑ |
| `skill/a2ahub/SKILL.md` § "Which surface to work through" | ☑ |
| `skill/RELEASE-CHECKLIST.md` (this file) | ☑ |
EOF
    cat >"$tmp/skill/a2ahub/docs-manifest.json" <<'EOF'
{"id":"overview","group":"Concepts","title":"Overview","file":"a2ahub/SKILL.md"}
{"id":"alpha","group":"Reference","title":"Alpha","file":"a2ahub/alpha.md"}
{"id":"beta","group":"Reference","title":"Beta","file":"a2ahub/reference/beta.md"}
{"id":"commands","group":"Reference","title":"Commands","file":"a2ahub/reference/commands.md"}
{"id":"authoring-gamma","group":"Authoring","title":"Gamma","file":"a2ahub/reference/authoring/gamma.md"}
EOF
  }

  write_good_fixture
  if ! (run_check "$tmp") >/dev/null 2>&1; then
    echo "prose-roster --teeth: FAILED — a fully consistent fixture tree stayed red" >&2
    return 1
  fi

  # 1. alpha.md missing from the TOC (present on disk, D-015, checklist,
  # manifest) must red naming it against the TOC specifically.
  write_good_fixture
  sed -i.bak '/\[alpha.md\](alpha.md)/d' "$tmp/skill/a2ahub/SKILL.md"
  rm -f "$tmp/skill/a2ahub/SKILL.md.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "alpha.md is a hand-maintained file on disk but is missing from the TOC"; then
    echo "prose-roster --teeth: FAILED — dropping alpha.md from the TOC did not red naming it against the TOC" >&2
    return 1
  fi

  # 2. alpha.md missing from D-015.
  write_good_fixture
  sed -i.bak 's/`SKILL.md`, `alpha.md`, /`SKILL.md`, /' "$tmp/skill/a2ahub/SKILL.md"
  rm -f "$tmp/skill/a2ahub/SKILL.md.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "alpha.md is a hand-maintained file on disk but is missing from the D-015 list"; then
    echo "prose-roster --teeth: FAILED — dropping alpha.md from D-015 did not red naming it against the D-015 list" >&2
    return 1
  fi

  # 3. alpha.md missing from the checklist.
  write_good_fixture
  sed -i.bak '/`skill\/a2ahub\/alpha.md`/d' "$tmp/skill/RELEASE-CHECKLIST.md"
  rm -f "$tmp/skill/RELEASE-CHECKLIST.md.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "alpha.md is a hand-maintained file on disk but is missing from RELEASE-CHECKLIST.md's review table"; then
    echo "prose-roster --teeth: FAILED — dropping alpha.md from the checklist did not red naming it against RELEASE-CHECKLIST.md" >&2
    return 1
  fi

  # 4. alpha.md missing from docs-manifest.json.
  write_good_fixture
  sed -i.bak '/"file":"a2ahub\/alpha.md"/d' "$tmp/skill/a2ahub/docs-manifest.json"
  rm -f "$tmp/skill/a2ahub/docs-manifest.json.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "alpha.md is a hand-maintained file on disk but is missing from docs-manifest.json"; then
    echo "prose-roster --teeth: FAILED — dropping alpha.md from docs-manifest.json did not red naming it against docs-manifest.json" >&2
    return 1
  fi

  # 5. A generated page (reference/commands.md) added to the D-015 list must
  # red naming it as generated, not silently pass because it also exists on
  # disk.
  write_good_fixture
  sed -i.bak 's/`reference\/beta.md` — are/`reference\/beta.md`, `reference\/commands.md` — are/' "$tmp/skill/a2ahub/SKILL.md"
  rm -f "$tmp/skill/a2ahub/SKILL.md.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "the D-015 list (SKILL.md § Sourcing & drift) lists reference/commands.md, which is a GENERATED page"; then
    echo "prose-roster --teeth: FAILED — adding reference/commands.md to D-015 did not red naming it as generated" >&2
    return 1
  fi

  # 5b. Same shape, but on the checklist — the OTHER list the negative rule
  # names.
  write_good_fixture
  sed -i.bak 's/| `skill\/a2ahub\/alpha.md` | ☑ |/| `skill\/a2ahub\/alpha.md` | ☑ |\n| `skill\/a2ahub\/reference\/commands.md` | ☑ |/' "$tmp/skill/RELEASE-CHECKLIST.md"
  rm -f "$tmp/skill/RELEASE-CHECKLIST.md.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "RELEASE-CHECKLIST.md's review table lists reference/commands.md, which is a GENERATED page"; then
    echo "prose-roster --teeth: FAILED — adding reference/commands.md to the checklist did not red naming it as generated" >&2
    return 1
  fi

  # 6. A list naming a page that does not exist on disk at all must red
  # naming it, distinct from the generated-page case above.
  write_good_fixture
  sed -i.bak 's#| \[reference/beta.md\](reference/beta.md) | Beta fixture. |#| [reference/beta.md](reference/beta.md) | Beta fixture. |\n| [nowhere.md](nowhere.md) | Ghost fixture. |#' "$tmp/skill/a2ahub/SKILL.md"
  rm -f "$tmp/skill/a2ahub/SKILL.md.bak"
  out="$(run_check "$tmp" 2>&1 >/dev/null || true)"
  if ! printf '%s\n' "$out" | grep -qF "the TOC (SKILL.md § Table of contents) lists nowhere.md, which is not a hand-maintained file on disk"; then
    echo "prose-roster --teeth: FAILED — a TOC row naming a nonexistent page did not red naming it as absent from disk" >&2
    return 1
  fi

  echo "prose-roster --teeth: ok"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

run_check "$GATE_ROOT"
exit $?
