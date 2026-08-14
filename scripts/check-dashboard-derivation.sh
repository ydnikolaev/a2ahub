#!/usr/bin/env bash
# check-dashboard-derivation.sh — fail-closed guard against browser domain work.
#
# The scanned universe is the dc-import graph rooted at the local dashboard,
# not every public prototype. Three bounded syntax families are guarded here:
# runtime sorting, clock thresholds/arithmetic, and browser network APIs outside
# DashboardLive.dc.html. Geometry and user-scope selection stay legal.
#
# A presentation-only sort needs this exact, local opt-out block:
#   // dashboard-derivation: allow-sort-begin layout-only -- <specific reason>
#   ... exactly one .sort( within at most 12 source lines ...
#   // dashboard-derivation: allow-sort-end
# `server-order` is the other accepted category. Markers do not nest or span
# files; a malformed/empty/oversized block fails closed.
#
# Usage: bash scripts/check-dashboard-derivation.sh
#        bash scripts/check-dashboard-derivation.sh --teeth

# lane-inputs:
#   web/design-source/**
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   self-locates the shared helper through command substitution.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

CLOCK_DERIVATION_PATTERN='(Date\.now[[:space:]]*\(|performance\.now[[:space:]]*\(|Date\.parse[[:space:]]*\(|\.getTime[[:space:]]*\()[^;]*(\+|-|\*|/|<|>)|new[[:space:]]+Date[[:space:]]*\([^)]*\)[[:space:]]*(\+|-|\*|/|<|>)|(^|[^A-Za-z0-9_$])((const|let|var)[[:space:]]+)?(age|elapsed|duration|staleAfter|freshFor|deadline|expiresAt|cutoff|threshold)[[:space:]]*(=|<|>|<=|>=|===|==)[^;]*(\+|-|\*|/|<|>)|(^|[^A-Za-z0-9_$])(now|observedAt|generatedAt|updatedAt|createdAt|timestamp|timeMs)[[:space:]]*(\+|-|\*|/|<|>)'
NETWORK_API_PATTERN='(^|[^A-Za-z0-9_$])(fetch[[:space:]]*\(|XMLHttpRequest|EventSource|WebSocket)'

extract_import_names() { # $1 = component file
  grep -oE '<dc-import[^>]*name="[A-Za-z][A-Za-z0-9_-]*"' "$1" 2>/dev/null |
    sed -E 's/.*name="([A-Za-z][A-Za-z0-9_-]*)"/\1/' |
    sort -u
}

build_component_graph() { # $1 = source root, $2 = output file
  local source_root="$1" output="$2" queue current file dep seen="" import_tags import_count parsed_count
  : > "$output"
  queue="14-local-dashboard-v4"
  while [ -n "$queue" ]; do
    current="${queue%%$'\n'*}"
    if [ "$queue" = "$current" ]; then queue=""; else queue="${queue#*$'\n'}"; fi
    [ -z "$current" ] && continue
    if printf '%s\n' "$seen" | grep -Fqx "$current"; then continue; fi
    seen="${seen}${current}"$'\n'
    file="$source_root/$current.dc.html"
    if [ ! -f "$file" ]; then
      gate_fail "dashboard-derivation: missing dc-import target $current.dc.html in the local-dashboard graph"
      continue
    fi
    printf '%s\n' "$file" >> "$output"
    import_tags="$(grep -oE '<dc-import[^>]*>' "$file" 2>/dev/null || true)"
    import_count="$(grep -o '<dc-import' "$file" 2>/dev/null | wc -l | tr -d '[:space:]')"
    parsed_count="$(printf '%s\n' "$import_tags" | grep -cE 'name="[A-Za-z][A-Za-z0-9_-]*"' || true)"
    if [ "$import_count" -ne "$parsed_count" ]; then
      gate_fail "dashboard-derivation: $(basename "$file") has an unparseable dc-import edge — use one explicit name=\"Component\" per closed tag"
    fi
    while IFS= read -r dep; do
      [ -z "$dep" ] && continue
      queue="${queue}${queue:+$'\n'}${dep}"
    done <<< "$(extract_import_names "$file")"
  done
  sort -u "$output" -o "$output"
}

runtime_records() { # $1 = dc component; emits line<TAB>BEGIN|END|BAD|CODE<TAB>text
  awk '
    function strip_comments(text, out, start, rest, finish, slash) {
      out=""
      while (1) {
        if (in_block) {
          finish=index(text, "*/")
          if (!finish) return out
          text=substr(text, finish+2)
          in_block=0
        }
        start=index(text, "/*")
        slash=index(text, "//")
        if (slash && (!start || slash < start)) return out substr(text, 1, slash-1)
        if (!start) return out text
        out=out substr(text, 1, start-1)
        rest=substr(text, start+2)
        finish=index(rest, "*/")
        if (!finish) { in_block=1; return out }
        text=substr(rest, finish+2)
      }
    }
    /<script[^>]*data-dc-script/ {
      in_script=1
      text=$0
      sub(/^.*<script[^>]*data-dc-script[^>]*>/, "", text)
      closing="</" "script>"
      finish=index(text, closing)
      if (finish) {
        text=substr(text, 1, finish-1)
        in_script=0
      }
      if (text != "") printf "%d\tCODE\t%s\n", FNR, strip_comments(text)
      next
    }
    in_script && $0 ~ ("</" "script>") {
      text=$0
      closing="</" "script>"
      finish=index(text, closing)
      text=substr(text, 1, finish-1)
      in_script=0
      if (text != "") printf "%d\tCODE\t%s\n", FNR, strip_comments(text)
      next
    }
    !in_script { next }
    /dashboard-derivation:[[:space:]]*allow-sort-begin/ {
      if ($0 ~ /^[[:space:]]*\/[\/][[:space:]]*dashboard-derivation:[[:space:]]*allow-sort-begin[[:space:]]+(server-order|layout-only)[[:space:]]+--[[:space:]]+[^[:space:]].*$/)
        printf "%d\tBEGIN\t%s\n", FNR, $0
      else printf "%d\tBAD\t%s\n", FNR, $0
      next
    }
    /dashboard-derivation:[[:space:]]*allow-sort-end/ {
      if ($0 ~ /^[[:space:]]*\/[\/][[:space:]]*dashboard-derivation:[[:space:]]*allow-sort-end[[:space:]]*$/)
        printf "%d\tEND\t%s\n", FNR, $0
      else printf "%d\tBAD\t%s\n", FNR, $0
      next
    }
    { printf "%d\tCODE\t%s\n", FNR, strip_comments($0) }
  ' "$1"
}

scan_component() { # $1 = file, $2 = exact live owner
  local file="$1" live_owner="$2" lineno record content marker=0 marker_line=0 marker_lines=0 marker_sorts=0 hits remaining
  while IFS=$'\t' read -r lineno record content; do
    case "$record" in
      BEGIN)
        if [ "$marker" -eq 1 ]; then
          gate_fail "dashboard-derivation: $(basename "$file"):$lineno nests an allow-sort marker — keep each exception bounded and independent"
        else
          marker=1; marker_line="$lineno"; marker_lines=0; marker_sorts=0
        fi
        ;;
      END)
        if [ "$marker" -eq 0 ]; then
          gate_fail "dashboard-derivation: $(basename "$file"):$lineno has an allow-sort end without a matching begin"
        else
          [ "$marker_sorts" -eq 1 ] || gate_fail "dashboard-derivation: $(basename "$file"):$marker_line allow-sort block contains $marker_sorts sorts; require exactly one"
          marker=0
        fi
        ;;
      BAD)
        gate_fail "dashboard-derivation: $(basename "$file"):$lineno has a malformed allow-sort marker — use the exact documented begin/end syntax with a non-empty reason"
        ;;
      CODE)
        if [ "$marker" -eq 1 ]; then
          marker_lines=$((marker_lines + 1))
          if [ "$marker_lines" -gt 12 ]; then
            gate_fail "dashboard-derivation: $(basename "$file"):$marker_line allow-sort block exceeds 12 source lines — narrow the exception"
            marker=0
          fi
        fi
        hits=0
        remaining="$content"
        while [[ "$remaining" =~ \.sort[[:space:]]*\( ]]; do
          hits=$((hits + 1))
          remaining="${remaining#*"${BASH_REMATCH[0]}"}"
        done
        if [ "$hits" -gt 0 ]; then
          if [ "$marker" -eq 1 ]; then
            marker_sorts=$((marker_sorts + hits))
          else
            gate_fail "dashboard-derivation: $(basename "$file"):$lineno sorts in browser runtime code — preserve the carried server order; presentation-only exceptions need one bounded allow-sort block"
          fi
        fi

        if [[ "$content" =~ $CLOCK_DERIVATION_PATTERN ]]; then
          gate_fail "dashboard-derivation: $(basename "$file"):$lineno derives a clock threshold/arithmetic verdict — carry freshness/expiry from the server; display-only timestamp formatting is allowed"
        fi

        if [ "$file" != "$live_owner" ] && [[ "$content" =~ $NETWORK_API_PATTERN ]]; then
          gate_fail "dashboard-derivation: $(basename "$file"):$lineno uses a browser network API outside DashboardLive.dc.html — keep loopback transport in its sole owner"
        fi
        ;;
    esac
  done <<< "$(runtime_records "$file")"
  if [ "$marker" -eq 1 ]; then
    gate_fail "dashboard-derivation: $(basename "$file"):$marker_line has an unclosed allow-sort block"
  fi
}

run_check() { # optional: source root
  local source_root="${1:-$GATE_ROOT/web/design-source}" graph file live_owner
  if [ ! -d "$source_root" ]; then
    gate_fail "dashboard-derivation: source root '$source_root' is missing"
    return
  fi
  graph="$(mktemp)" || { gate_fail "dashboard-derivation: mktemp failed while deriving the component graph"; return; }
  build_component_graph "$source_root" "$graph"
  live_owner="$source_root/DashboardLive.dc.html"
  if [ ! -f "$live_owner" ]; then
    gate_fail "dashboard-derivation: DashboardLive.dc.html is missing from the source root — the sole network owner is absent"
  elif ! grep -Fqx "$live_owner" "$graph"; then
    gate_fail "dashboard-derivation: DashboardLive.dc.html is not reachable from 14-local-dashboard-v4.dc.html"
  fi
  while IFS= read -r file; do
    [ -z "$file" ] || scan_component "$file" "$live_owner"
  done < "$graph"
  rm -f -- "$graph"
}

prepare_graph_fixture() { # $1 = directory
  local dir="$1"
  mkdir -p "$dir"
  printf '%s\n' '<dc-import name="View"></dc-import>' '<dc-import name="DashboardLive"></dc-import>' '<script type="text/x-dc" data-dc-script>' 'const root = true;' '</script>' > "$dir/14-local-dashboard-v4.dc.html"
  printf '%s\n' '<script type="text/x-dc" data-dc-script>' '// dashboard-derivation: allow-sort-begin layout-only -- restore authored tabs after responsive layout' 'tabs.sort((a, b) => positions.indexOf(a) - positions.indexOf(b));' '// dashboard-derivation: allow-sort-end' 'const label = new Date(row.observedAt).toISOString();' 'const geometry = window.innerWidth - box.width;' 'const selected = rows.filter(row => row.space === chosenSpace);' '</script>' > "$dir/View.dc.html"
  printf '%s\n' '<script type="text/x-dc" data-dc-script>' 'fetch("/api/v1/dashboard");' 'const events = new EventSource("/api/v1/events");' '</script>' > "$dir/DashboardLive.dc.html"
}

teeth_expect() { # $1 = label, $2 = red|green, $3 = needle, $4 = source root
  local label="$1" verdict="$2" needle="$3" root="$4" out rc
  set +e
  out="$( (_GATE_ERRORS=0; run_check "$root"; gate_summary "dashboard-derivation-teeth") 2>&1)"
  rc=$?
  set -e
  if [ "$verdict" = "red" ]; then
    if [ "$rc" -eq 0 ] || ! printf '%s\n' "$out" | grep -Fq "$needle"; then
      echo "check-dashboard-derivation --teeth: FALSE GREEN — $label did not red with '$needle':" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-dashboard-derivation --teeth: $label reds"
  elif [ "$rc" -ne 0 ]; then
    echo "check-dashboard-derivation --teeth: FALSE RED — $label should green:" >&2
    echo "$out" >&2
    return 1
  else
    echo "check-dashboard-derivation --teeth: $label greens"
  fi
}

run_teeth() {
  local good fixture
  TEETH_WORK="$(mktemp -d)" || return 1
  trap 'rm -rf -- "${TEETH_WORK:-}"' EXIT

  good="$TEETH_WORK/good"; prepare_graph_fixture "$good"
  teeth_expect "layout/formatting/live-owner controls" green "" "$good" || return 1

  fixture="$TEETH_WORK/domain-sort"; prepare_graph_fixture "$fixture"
  sed -i.bak '/const root = true;/a\
items.sort((a, b) => a.rank - b.rank);
' "$fixture/14-local-dashboard-v4.dc.html"; rm -f "$fixture/14-local-dashboard-v4.dc.html.bak"
  teeth_expect "unmarked domain sort" red "sorts in browser runtime code" "$fixture" || return 1

  fixture="$TEETH_WORK/marker-escape"; prepare_graph_fixture "$fixture"
  sed -i.bak '/allow-sort-end/a\
items.sort((a, b) => a.rank - b.rank);
' "$fixture/View.dc.html"; rm -f "$fixture/View.dc.html.bak"
  teeth_expect "sort outside a closed marker" red "sorts in browser runtime code" "$fixture" || return 1

  fixture="$TEETH_WORK/marker-empty"; prepare_graph_fixture "$fixture"
  sed -i.bak 's/allow-sort-begin layout-only -- restore authored tabs after responsive layout/allow-sort-begin layout-only --/' "$fixture/View.dc.html"; rm -f "$fixture/View.dc.html.bak"
  teeth_expect "malformed marker with empty reason" red "malformed allow-sort marker" "$fixture" || return 1

  fixture="$TEETH_WORK/marker-zero-sort"; prepare_graph_fixture "$fixture"
  sed -i.bak 's/tabs\.sort((a, b) => positions\.indexOf(a) - positions\.indexOf(b));/const tabsCopy = tabs.slice();/' "$fixture/View.dc.html"; rm -f "$fixture/View.dc.html.bak"
  teeth_expect "marker without its one sort" red "allow-sort block contains 0 sorts" "$fixture" || return 1

  fixture="$TEETH_WORK/marker-multiple-sorts"; prepare_graph_fixture "$fixture"
  sed -i.bak '/tabs.sort/a\
labels.sort();
' "$fixture/View.dc.html"; rm -f "$fixture/View.dc.html.bak"
  teeth_expect "marker with more than one sort" red "allow-sort block contains 2 sorts" "$fixture" || return 1

  fixture="$TEETH_WORK/clock-threshold"; prepare_graph_fixture "$fixture"
  sed -i.bak '/const root = true;/a\
const stale = Date.now() - Date.parse(row.observedAt) > 60000;
' "$fixture/14-local-dashboard-v4.dc.html"; rm -f "$fixture/14-local-dashboard-v4.dc.html.bak"
  teeth_expect "clock threshold" red "derives a clock threshold/arithmetic verdict" "$fixture" || return 1

  fixture="$TEETH_WORK/off-owner-fetch"; prepare_graph_fixture "$fixture"
  sed -i.bak '/const root = true;/a\
fetch("/api/v1/dashboard");
' "$fixture/14-local-dashboard-v4.dc.html"; rm -f "$fixture/14-local-dashboard-v4.dc.html.bak"
  teeth_expect "off-owner fetch" red "network API outside DashboardLive.dc.html" "$fixture" || return 1

  fixture="$TEETH_WORK/inline-runtime"; prepare_graph_fixture "$fixture"
  printf '%s\n' '<script type="text/x-dc" data-dc-script>items.sort((a, b) => a.rank - b.rank); fetch("/api/v1/dashboard");</script>' > "$fixture/Inline.dc.html"
  sed -i.bak '/<dc-import name="View"/a\
<dc-import name="Inline">
' "$fixture/14-local-dashboard-v4.dc.html"; rm -f "$fixture/14-local-dashboard-v4.dc.html.bak"
  teeth_expect "inline script-tag runtime" red "sorts in browser runtime code" "$fixture" || return 1

  fixture="$TEETH_WORK/closing-line-runtime"; prepare_graph_fixture "$fixture"
  printf '%s\n' '<script type="text/x-dc" data-dc-script>' 'items.sort((a, b) => a.rank - b.rank);</script>' > "$fixture/ClosingLine.dc.html"
  sed -i.bak '/<dc-import name="View"/a\
<dc-import name="ClosingLine">
' "$fixture/14-local-dashboard-v4.dc.html"; rm -f "$fixture/14-local-dashboard-v4.dc.html.bak"
  teeth_expect "closing script-tag runtime" red "sorts in browser runtime code" "$fixture" || return 1

  fixture="$TEETH_WORK/missing-import"; prepare_graph_fixture "$fixture"
  printf '%s\n' '<dc-import name="Missing"></dc-import>' >> "$fixture/View.dc.html"
  teeth_expect "missing import target" red "missing dc-import target Missing.dc.html" "$fixture" || return 1

  echo "check-dashboard-derivation --teeth: PASS — graph closure, multiline/inline sort boundaries, marker shape/count, clock threshold and live-only network each have a red tooth; layout/formatting/live-owner controls green"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
else
  run_check
  gate_summary "dashboard-derivation"
fi
