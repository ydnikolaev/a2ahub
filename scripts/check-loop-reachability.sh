#!/usr/bin/env bash
# check-loop-reachability.sh — every page skill/a2ahub/docs-manifest.json
# lists in the Concepts, Reference, or Authoring group must be reachable
# from skill/a2ahub/loops.md ALONE by transitively following markdown
# links (agent-exchange P7, AC5).
#
# `loops.md` is the ONLY root. A markdown link `[text](href)` on a page
# already reached is followed IF that page is allowed to ROUTE (see below);
# a link whose href resolves to a DIRECTORY reaches exactly the manifest
# pages listed under that directory (any manifest "file" path prefixed by
# it) — those pages are then themselves graph nodes, so a page reached only
# via a directory link still has its own outbound links followed, subject
# to the same routing rule. http(s)/mailto links and bare #fragments are
# not page links and are ignored. No page may be exempted: a gap reds
# naming the exact manifest file path that is unreachable.
#
# ROUTING RULE — the one thing this gate exists to enforce, spelled out
# because it is not obvious from "follow markdown links" alone: only
# `loops.md` itself (the root) and pages the manifest classifies Reference
# or Authoring may act as a SOURCE of further edges. A page reached that is
# Concepts (other than the root), Start, Help, or unlisted in the manifest
# at all is a valid DESTINATION — it counts as reached — but its own
# outbound links are never followed. Spec 07-loops.md's own words: "Five
# hand-maintained reference pages ... are reviewed each release, listed in
# the TOC, and carry zero inbound links from loops.md. Content that is
# current and unreachable through the READING PATH THE SKILL ITSELF
# PRESCRIBES." Without this restriction, SKILL.md's own summary table (a
# Concepts-group overview page, reached from loops.md via
# troubleshooting.md) fans out to every page in the corpus and the gate
# stays green forever regardless of whether any LOOP STEP actually routes
# an agent anywhere — exactly the table-of-contents loophole the criterion
# exists to close. A page still gets to be a graph NODE (so a legitimate
# instrument-to-instrument link, e.g. one Reference page citing another,
# keeps counting); it just cannot be the thing that makes an otherwise-
# orphaned page look reachable by virtue of appearing in an index.
#
# docs-manifest.json is read directly (not "the binary") because it is a
# static repo artifact, not domain state the fold/binary could drift from
# underneath a gate — the "ask the binary" rule guards against exactly
# that drift, and there is none to guard against here.
#
# Parsed with grep/sed rather than jq — jq is not a dependency of
# `make check` (the `check-view-vocabulary.sh` precedent). The manifest is
# one JSON object per physical line, the shape docs-manifest.json actually
# uses.
#
# Usage: bash scripts/check-loop-reachability.sh            # check the real tree
#        bash scripts/check-loop-reachability.sh --teeth    # self-test on fixtures

# lane-inputs:
#   skill/a2ahub/**
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   below self-locates scripts/lib/gate-lib.sh from this script's own path, so
#   the classifier cannot resolve the $(dirname ...) substitution to a literal.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# manifest_all_pages prints "relpath<TAB>group" for EVERY manifest entry,
# any group, with the leading path segment (the skill directory name, e.g.
# "a2ahub/") stripped from "file" so the result is relative to that
# directory. One JSON object per physical line, matching docs-manifest.json's
# own formatting. This is the routing-eligibility table: every page this
# gate ever visits is looked up here to decide whether it may route.
manifest_all_pages() { # $1 = manifest path
  local line file group
  while IFS= read -r line; do
    file="$(printf '%s' "$line" | grep -oE '"file":"[^"]+"' | sed -E 's/^"file":"//; s/"$//')"
    [ -n "$file" ] || continue
    group="$(printf '%s' "$line" | grep -oE '"group":"[^"]+"' | sed -E 's/^"group":"//; s/"$//')"
    file="$(printf '%s' "$file" | sed -E 's#^[^/]+/##')"
    printf '%s\t%s\n' "$file" "$group"
  done <"$1"
}

# manifest_universe prints one relpath per line — the required-reachable
# set — for every entry whose group is Concepts, Reference, or Authoring.
manifest_universe() { # $1 = manifest_all_pages table
  awk -F'\t' '$2=="Concepts"||$2=="Reference"||$2=="Authoring"{print $1}' <<<"$1"
}

# page_group prints the manifest group of one relpath, or nothing if the
# page is not manifest-listed at all.
page_group() { # $1 = manifest_all_pages table, $2 = relpath
  awk -F'\t' -v p="$2" '$1==p{print $2; exit}' <<<"$1"
}

# may_route reports (via exit status) whether $2 is allowed to be a SOURCE
# of further edges: the root itself, or a manifest Reference/Authoring page.
may_route() { # $1 = manifest_all_pages table, $2 = relpath, $3 = root page relpath (loops.md)
  local table="$1" relpath="$2" root="$3"
  [ "$relpath" = "$root" ] && return 0
  local group
  group="$(page_group "$table" "$relpath")"
  [ "$group" = "Reference" ] || [ "$group" = "Authoring" ]
}

# page_links prints every markdown link HREF on a page, one per line,
# stripped of any #fragment — `[text](href)` only, title-suffixed hrefs
# (`(href "title")`) keep only the href token. http(s)/mailto links and
# hrefs that are only a fragment (now-empty after stripping) are dropped;
# they cannot name a skill page.
page_links() { # $1 = md file
  grep -oE '\[[^]]*\]\([^)]+\)' "$1" 2>/dev/null |
    sed -E 's/^\[[^]]*\]\(//; s/\)$//' |
    awk '{print $1}' |
    sed -E 's/#.*$//' |
    while IFS= read -r href; do
      case "$href" in
        ''|http://*|https://*|mailto:*) continue ;;
        *) printf '%s\n' "$href" ;;
      esac
    done
}

# resolve_link normalizes an href found on one page into a path relative to
# $skill_abs, and reports whether that target is a directory or a file on
# disk. Prints "relpath<TAB>DIR" or "relpath<TAB>FILE"; returns non-zero for
# a link that escapes the skill root or points at nothing that exists —
# such a link is silently not-a-page-edge, not this gate's separate concern.
resolve_link() { # $1 = skill_abs, $2 = page_rel (source page, relative to skill_abs), $3 = href
  local skill_abs="$1" page_rel="$2" href="$3"
  local page_dir
  page_dir="$(dirname "$page_rel")"
  [ "$page_dir" = "." ] && page_dir=""
  local stripped="${href%/}"
  [ -z "$stripped" ] && stripped="."
  local joined
  if [ -n "$page_dir" ]; then joined="$page_dir/$stripped"; else joined="$stripped"; fi
  local jdir jbase jdir_abs abs
  jdir="$(dirname "$joined")"
  jbase="$(basename "$joined")"
  jdir_abs="$(cd "$skill_abs/$jdir" 2>/dev/null && pwd -P)" || return 1
  abs="$jdir_abs/$jbase"
  case "$abs" in
    "$skill_abs"|"$skill_abs"/*) : ;;
    *) return 1 ;;
  esac
  local rel="${abs#"$skill_abs"/}"
  if [ -d "$abs" ]; then
    printf '%s\tDIR\n' "$rel"
  elif [ -f "$abs" ]; then
    printf '%s\tFILE\n' "$rel"
  else
    return 1
  fi
}

# run_check walks the reachability graph rooted at loops.md and reports
# every manifest (Concepts/Reference/Authoring) page never reached. $1 is
# the root to read skill/a2ahub/{docs-manifest.json,loops.md} from
# (GATE_ROOT for a real run; a fixture directory for --teeth).
run_check() { # $1 = root
  local root="$1"
  local skill_dir="$root/skill/a2ahub"
  local manifest="$skill_dir/docs-manifest.json"
  local loops="$skill_dir/loops.md"
  local root_page="loops.md"

  if [ ! -f "$manifest" ]; then
    gate_fail "loop-reachability: $manifest does not exist"
    gate_summary "loop-reachability"
    return $?
  fi
  if [ ! -f "$loops" ]; then
    gate_fail "loop-reachability: $loops does not exist"
    gate_summary "loop-reachability"
    return $?
  fi

  local all_pages universe
  all_pages="$(manifest_all_pages "$manifest")"
  universe="$(manifest_universe "$all_pages" | sort -u)"
  if [ -z "$universe" ]; then
    gate_fail "loop-reachability: $manifest lists no page in Concepts, Reference, or Authoring — failing closed rather than policing nothing"
    gate_summary "loop-reachability"
    return $?
  fi

  local skill_abs
  skill_abs="$(cd "$skill_dir" && pwd -P)"

  local visited="$root_page" queue="$root_page"
  while [ -n "$queue" ]; do
    local current rest
    current="${queue%%$'\n'*}"
    if [ "$current" = "$queue" ]; then rest=""; else rest="${queue#*$'\n'}"; fi
    queue="$rest"

    if ! may_route "$all_pages" "$current" "$root_page"; then
      continue
    fi

    local page_path="$skill_abs/$current"
    [ -f "$page_path" ] || continue

    local href
    while IFS= read -r href; do
      [ -z "$href" ] && continue
      local resolved rel kind
      resolved="$(resolve_link "$skill_abs" "$current" "$href")" || continue
      rel="${resolved%%$'\t'*}"
      kind="${resolved##*$'\t'}"
      if [ "$kind" = "DIR" ]; then
        local u
        while IFS= read -r u; do
          [ -z "$u" ] && continue
          case "$u" in
            "$rel"/*) : ;;
            *) continue ;;
          esac
          if ! printf '%s\n' "$visited" | grep -qxF "$u"; then
            visited="$visited
$u"
            queue="$queue
$u"
          fi
        done <<<"$universe"
      else
        case "$rel" in
          *.md) : ;;
          *) continue ;;
        esac
        if ! printf '%s\n' "$visited" | grep -qxF "$rel"; then
          visited="$visited
$rel"
          queue="$queue
$rel"
        fi
      fi
    done < <(page_links "$page_path")
  done

  local page
  while IFS= read -r page; do
    [ -z "$page" ] && continue
    if ! printf '%s\n' "$visited" | grep -qxF "$page"; then
      gate_fail "loop-reachability: $page is not reachable from loops.md by transitively following markdown links (a link from a Concepts/Start/Help page other than loops.md itself does not count — see this script's own header)"
    fi
  done <<<"$universe"

  gate_summary "loop-reachability"
}

run_teeth() {
  local tmp
  tmp="$(mktemp -d)" || { echo "loop-reachability --teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN
  mkdir -p "$tmp/skill/a2ahub/ref" "$tmp/skill/a2ahub/auth"

  # write_good_fixture writes a small but FULLY reachable synthetic skill
  # tree with its own manifest — never a mutation of the real one. Shape:
  #   loops.md --(direct)--> index.md            [Concepts, non-root: a leaf]
  #           --(direct)--> ref/alpha.md --(direct, 2-hop)--> ref/beta.md
  #           --(directory)--> auth/  reaches auth/gamma.md, auth/delta.md
  #                              gamma.md --(direct, hop past a directory
  #                                          reach)--> auth/epsilon.md
  # plus one http(s) link and one bare #fragment link, to prove those are
  # ignored rather than mishandled. Every mutation below starts from this
  # baseline and breaks exactly one edge.
  write_good_fixture() {
    cat >"$tmp/skill/a2ahub/docs-manifest.json" <<'EOF'
{"id":"overview","group":"Concepts","title":"Overview","file":"a2ahub/index.md"}
{"id":"loops","group":"Concepts","title":"Loops","file":"a2ahub/loops.md"}
{"id":"alpha","group":"Reference","title":"Alpha","file":"a2ahub/ref/alpha.md"}
{"id":"beta","group":"Reference","title":"Beta","file":"a2ahub/ref/beta.md"}
{"id":"gamma","group":"Authoring","title":"Gamma","file":"a2ahub/auth/gamma.md"}
{"id":"delta","group":"Authoring","title":"Delta","file":"a2ahub/auth/delta.md"}
{"id":"epsilon","group":"Authoring","title":"Epsilon","file":"a2ahub/auth/epsilon.md"}
{"id":"helppage","group":"Help","title":"Help","file":"a2ahub/help.md"}
EOF
    cat >"$tmp/skill/a2ahub/loops.md" <<'EOF'
# loops fixture

[index.md](index.md), [ref/alpha.md](ref/alpha.md), [auth/](auth/).
An external link: [example](https://example.com/nope) and a bare
[fragment](#nowhere) — neither is a page.
EOF
    printf '# index fixture\n' >"$tmp/skill/a2ahub/index.md"
    printf '# alpha fixture\n\n[beta.md](beta.md)\n' >"$tmp/skill/a2ahub/ref/alpha.md"
    printf '# beta fixture\n' >"$tmp/skill/a2ahub/ref/beta.md"
    printf '# gamma fixture\n\n[epsilon.md](epsilon.md)\n' >"$tmp/skill/a2ahub/auth/gamma.md"
    printf '# delta fixture\n' >"$tmp/skill/a2ahub/auth/delta.md"
    printf '# epsilon fixture\n' >"$tmp/skill/a2ahub/auth/epsilon.md"
  }

  write_good_fixture
  if ! (run_check "$tmp") >/dev/null 2>&1; then
    echo "loop-reachability --teeth: FAILED — a fully reachable fixture tree stayed red" >&2
    return 1
  fi

  # 1. Drop the direct link to index.md (the overview page) — it must red
  # naming exactly that manifest path, nothing else.
  write_good_fixture
  sed -i.bak 's#\[index.md\](index.md), ##' "$tmp/skill/a2ahub/loops.md"
  rm -f "$tmp/skill/a2ahub/loops.md.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "index.md is not reachable"; then
    echo "loop-reachability --teeth: FAILED — dropping the direct link to index.md did not red naming it" >&2
    return 1
  fi

  # 2. Drop the SECOND-HOP link (alpha.md -> beta.md) — beta.md must red
  # even though loops.md -> alpha.md is untouched, proving transitivity is
  # actually walked rather than only checking loops.md's own links.
  write_good_fixture
  printf '# alpha fixture, no outbound link\n' >"$tmp/skill/a2ahub/ref/alpha.md"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "ref/beta.md is not reachable"; then
    echo "loop-reachability --teeth: FAILED — dropping the alpha->beta hop did not red naming ref/beta.md" >&2
    return 1
  fi
  if printf '%s\n' "$out" | grep -qF "ref/alpha.md is not reachable"; then
    echo "loop-reachability --teeth: FAILED — ref/alpha.md itself (still directly linked from loops.md) was wrongly reported unreachable" >&2
    return 1
  fi

  # 3. Drop the DIRECTORY link ([auth/](auth/)) — gamma.md, delta.md, AND
  # epsilon.md (reachable only past gamma, which is itself only reachable
  # via the directory link) must all red, proving the directory-reach
  # mechanism — not individually-existing links — was carrying them, and
  # that a page reached via a directory link still routes onward.
  write_good_fixture
  sed -i.bak 's#, \[auth/\](auth/)##' "$tmp/skill/a2ahub/loops.md"
  rm -f "$tmp/skill/a2ahub/loops.md.bak"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "auth/gamma.md is not reachable"; then
    echo "loop-reachability --teeth: FAILED — dropping the directory link did not red naming auth/gamma.md" >&2
    return 1
  fi
  if ! printf '%s\n' "$out" | grep -qF "auth/delta.md is not reachable"; then
    echo "loop-reachability --teeth: FAILED — dropping the directory link did not red naming auth/delta.md" >&2
    return 1
  fi
  if ! printf '%s\n' "$out" | grep -qF "auth/epsilon.md is not reachable"; then
    echo "loop-reachability --teeth: FAILED — dropping the directory link did not cascade to auth/epsilon.md (reached only past gamma)" >&2
    return 1
  fi

  # 4. THE ROUTING RULE ITSELF: a page reachable ONLY via a link parked on
  # index.md (Concepts, non-root — an index/TOC-shaped page) must stay RED
  # even though a byte-level "is there a markdown link path" scan would
  # call it reachable. This is the exact loophole spec 07-loops.md names:
  # a page "listed in the TOC" is not "reachable through the reading path
  # the skill itself prescribes." Without this rule the gate cannot ever
  # red once one Concepts page fans out to the rest of the corpus.
  write_good_fixture
  printf '# index fixture\n\n[ref/beta.md](ref/beta.md)\n' >"$tmp/skill/a2ahub/index.md"
  printf '# alpha fixture, no outbound link\n' >"$tmp/skill/a2ahub/ref/alpha.md"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "ref/beta.md is not reachable"; then
    echo "loop-reachability --teeth: FAILED — a page reachable only via index.md's own link stayed green; the TOC loophole is open" >&2
    return 1
  fi

  # 5. A manifest with no Concepts/Reference/Authoring entry at all must
  # fail CLOSED, not pass vacuously.
  write_good_fixture
  cat >"$tmp/skill/a2ahub/docs-manifest.json" <<'EOF'
{"id":"helppage","group":"Help","title":"Help","file":"a2ahub/help.md"}
EOF
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "lists no page in Concepts, Reference, or Authoring"; then
    echo "loop-reachability --teeth: FAILED — an empty derived universe did not fail closed with that message" >&2
    return 1
  fi

  # 6. Missing loops.md must red naming it.
  write_good_fixture
  rm -f "$tmp/skill/a2ahub/loops.md"
  out="$(run_check "$tmp" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$tmp/skill/a2ahub/loops.md does not exist"; then
    echo "loop-reachability --teeth: FAILED — a missing loops.md did not red naming it" >&2
    return 1
  fi

  echo "loop-reachability --teeth: ok"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

run_check "$GATE_ROOT"
exit $?
