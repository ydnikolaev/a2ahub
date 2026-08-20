#!/usr/bin/env bash
# check-dashboard-cards.sh — structural guard for the one-card contract.
#
# This gate deliberately owns only three invariants: the exact seven-row card
# registry, openCard as the sole detail door, and Modal.dc.html as the sole
# dialog engine. Tone, vocabulary, DOM equality and accent policy are owned by
# other executable evidence.
#
# Usage: bash scripts/check-dashboard-cards.sh
#        bash scripts/check-dashboard-cards.sh --teeth

# lane-inputs:
#   web/design-source/**
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   self-locates the shared helper through command substitution.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# `item` moved to ExchangeView on 2026-08-19, for the same reason `work-report`
# already lived there: the projection that knows how to build a document's
# detail — its events, its callout, its metadata, its rendered body — is
# ExchangeView.detailFor. While the card pointed at ArtifactDetail directly it
# could only reach the thinner card-shaped rendering, so a reader who clicked a
# document got a worse view of it than the pane beside the list was already
# showing. ArtifactDetail still DRAWS the card; it just no longer has to
# reconstruct what to draw.
EXPECTED_CARD_PAIRS='item|ExchangeView
thread|ThreadsView
contract|ContractsView
cver|ContractsView
work-report|ExchangeView
space|SpacesView
operational-process|Overview'

manifest_pairs() { # $1 = manifest path
  awk '
    function value(line, key, out) {
      out=line
      sub("^.*\\\"" key "\\\"[[:space:]]*:[[:space:]]*\\\"", "", out)
      sub("\\\".*$", "", out)
      return out
    }
    /"kind"[[:space:]]*:[[:space:]]*"[^"]+"/ {
      if (pending != "") exit 20
      pending=value($0, "kind")
      next
    }
    /"component"[[:space:]]*:[[:space:]]*"[^"]+"/ {
      if (pending == "") exit 21
      print pending "|" value($0, "component")
      pending=""
    }
    END { if (pending != "") exit 22 }
  ' "$1"
}

check_manifest() { # $1 = source root, $2 = manifest
  local source_root="$1" manifest="$2" pairs rc pair kind component duplicate count
  if [ ! -f "$manifest" ]; then
    gate_fail "dashboard-cards: missing cards.manifest.json — the registry cannot be checked"
    return
  fi
  pairs="$(manifest_pairs "$manifest")"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    gate_fail "dashboard-cards: could not parse kind/component rows in cards.manifest.json (parser exit $rc) — keep each card row explicit and adjacent"
    return
  fi
  count="$(printf '%s\n' "$pairs" | sed '/^$/d' | wc -l | tr -d '[:space:]')"
  if [ "$count" -ne 7 ]; then
    gate_fail "dashboard-cards: cards.manifest.json has $count kind/component rows; require exactly the seven canonical card kinds"
  fi
  duplicate="$(printf '%s\n' "$pairs" | cut -d'|' -f1 | sed '/^$/d' | sort | uniq -d)"
  if [ -n "$duplicate" ]; then
    while IFS= read -r kind; do
      [ -z "$kind" ] || gate_fail "dashboard-cards: cards.manifest.json registers kind '$kind' more than once — one kind must resolve to one card"
    done <<< "$duplicate"
  fi
  while IFS= read -r pair; do
    [ -z "$pair" ] && continue
    if ! printf '%s\n' "$EXPECTED_CARD_PAIRS" | grep -Fqx "$pair"; then
      gate_fail "dashboard-cards: unexpected or mismatched manifest row '$pair' — restore the canonical kind/component mapping"
      continue
    fi
    component="${pair#*|}"
    if [ ! -f "$source_root/$component.dc.html" ]; then
      gate_fail "dashboard-cards: manifest row '$pair' names missing component $component.dc.html"
    fi
  done <<< "$pairs"
  while IFS= read -r pair; do
    [ -z "$pair" ] && continue
    if ! printf '%s\n' "$pairs" | grep -Fqx "$pair"; then
      gate_fail "dashboard-cards: cards.manifest.json is missing canonical row '$pair'"
    fi
  done <<< "$EXPECTED_CARD_PAIRS"
}

check_open_card_door() { # $1 = source root
  local source_root="$1" root="$1/14-local-dashboard-v4.dc.html" definitions line file lineno
  if [ ! -f "$root" ]; then
    gate_fail "dashboard-cards: missing 14-local-dashboard-v4.dc.html — the canonical openCard owner is absent"
  else
    definitions="$(grep -nE '^[[:space:]]*openCard[[:space:]]*\(' "$root" 2>/dev/null || true)"
    if [ "$(printf '%s\n' "$definitions" | sed '/^$/d' | wc -l | tr -d '[:space:]')" -ne 1 ]; then
      gate_fail "dashboard-cards: 14-local-dashboard-v4.dc.html must define exactly one openCard(kind, id, accent) door"
    fi
  fi

  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file="${line%%:*}"
    lineno="${line#*:}"; lineno="${lineno%%:*}"
    gate_fail "dashboard-cards: $(basename "$file"):$lineno invokes a detail/modal door other than openCard — route the object through actions.openCard(kind, id, accent)"
  done <<< "$(grep -rnE --include='*.dc.html' '((open|show)(Modal|Detail)|setCard)[[:space:]]*\(|actions\.(modal|detail|openModal|showModal|openDetail|showDetail|setCard)[[:space:]]*\(' "$source_root" 2>/dev/null || true)"

  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file="${line%%:*}"
    [ "$file" = "$root" ] && continue
    lineno="${line#*:}"; lineno="${lineno%%:*}"
    gate_fail "dashboard-cards: $(basename "$file"):$lineno writes the #card route outside the openCard owner — route the object through actions.openCard(kind, id, accent)"
  done <<< "$(grep -rnF --include='*.dc.html' '#card=' "$source_root" 2>/dev/null || true)"
}

check_modal_owner() { # $1 = source root
  local source_root="$1" owner="$1/Modal.dc.html" line file lineno content
  if [ ! -f "$owner" ]; then
    gate_fail "dashboard-cards: missing Modal.dc.html — failing closed because the sole dialog owner is absent"
    return
  fi
  if ! grep -Eq "role[[:space:]]*=[[:space:]]*[\"']dialog[\"']" "$owner" ||
     ! grep -Eq "aria-modal[[:space:]]*=[[:space:]]*[\"']true[\"']" "$owner"; then
    gate_fail "dashboard-cards: Modal.dc.html must own the accessible role=dialog and aria-modal=true contract"
  fi

  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file="${line%%:*}"
    [ "$file" = "$owner" ] && continue
    content="${line#*:}"; lineno="${content%%:*}"; content="${content#*:}"
    if printf '%s\n' "$content" | grep -Eq "role[[:space:]]*=[[:space:]]*[\"']dialog[\"']|aria-modal[[:space:]]*=[[:space:]]*[\"']true[\"']|<dialog([[:space:]>])|data-card-modal|\\.showModal[[:space:]]*\\(|trap(Card|Modal)Tab|((lock|unlock)(Body|Scroll))[[:space:]]*\\(|body\\.style\\.overflow[[:space:]]*=[[:space:]]*[\"']hidden[\"']"; then
      gate_fail "dashboard-cards: $(basename "$file"):$lineno contains dialog markup/mechanics outside Modal.dc.html — keep one modal engine and import the registered card component there"
    fi
  done <<< "$(grep -rnE --include='*.dc.html' 'role[[:space:]]*=|aria-modal|<dialog|data-card-modal|\.showModal[[:space:]]*\(|trap(Card|Modal)Tab|((lock|unlock)(Body|Scroll))[[:space:]]*\(|body\.style\.overflow' "$source_root" 2>/dev/null || true)"

  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file="${line%%:*}"
    [ "$file" = "$owner" ] && continue
    content="${line#*:}"; lineno="${content%%:*}"
    gate_fail "dashboard-cards: $(basename "$file"):$lineno contains dialog markup/mechanics outside Modal.dc.html — keep one modal engine and import the registered card component there"
  done <<< "$(find "$source_root" -type f -name '*.dc.html' -exec awk '
    function fixed_backdrop(value) {
      return value ~ /position[[:space:]]*:[[:space:]]*fixed/ && value ~ /inset[[:space:]]*:[[:space:]]*0([^0-9]|$)/
    }
    {
      if (!collecting && index($0, "{") > 0) {
        collecting=1
        start=FNR
        block=$0
      } else if (collecting) {
        block=block " " $0
      }
      if (collecting && index($0, "}") > 0) {
        if (fixed_backdrop(block)) print FILENAME ":" start ":" block
        collecting=0
        block=""
      } else if (!collecting && fixed_backdrop($0)) {
        print FILENAME ":" FNR ":" $0
      }
    }
  ' {} + 2>/dev/null)"
}

run_check() { # optional: source root, manifest path
  local source_root="${1:-$GATE_ROOT/web/design-source}"
  local manifest="${2:-$source_root/cards.manifest.json}"
  if [ ! -d "$source_root" ]; then
    gate_fail "dashboard-cards: source root '$source_root' is missing"
    return
  fi
  check_manifest "$source_root" "$manifest"
  check_open_card_door "$source_root"
  check_modal_owner "$source_root"
}

write_manifest_fixture() { # $1 = path, $2 = mode
  local path="$1" mode="$2" pair kind component
  printf '%s\n' '{' '  "version": 1,' '  "cards": [' > "$path"
  while IFS= read -r pair; do
    [ -z "$pair" ] && continue
    kind="${pair%%|*}"; component="${pair#*|}"
    [ "$mode" = "missing" ] && [ "$kind" = "operational-process" ] && continue
    printf '%s\n' \
      '    {' \
      "      \"kind\": \"$kind\"," \
      "      \"component\": \"$component\"," \
      '      "accentFamilies": []' \
      '    },' >> "$path"
  done <<< "$EXPECTED_CARD_PAIRS"
  if [ "$mode" = "duplicate" ]; then
    printf '%s\n' \
      '    {' \
      '      "kind": "item",' \
      '      "component": "ArtifactDetail",' \
      '      "accentFamilies": []' \
      '    },' >> "$path"
  fi
  printf '%s\n' '    { "fixtureSentinel": true }' '  ]' '}' >> "$path"
}

prepare_card_fixture() { # $1 = dir, $2 = manifest mode
  local dir="$1" mode="${2:-good}" component
  mkdir -p "$dir"
  write_manifest_fixture "$dir/cards.manifest.json" "$mode"
  for component in ArtifactDetail ThreadsView ContractsView ExchangeView SpacesView Overview; do
    : > "$dir/$component.dc.html"
  done
  printf '%s\n' '<script type="text/x-dc" data-dc-script>' 'class Component {' '  openCard(kind, id, accent) { return [kind, id, accent]; }' '  render() { const actions = {}; actions.openCard("item", "space:item", ""); }' '}' '</script>' > "$dir/14-local-dashboard-v4.dc.html"
  printf '%s\n' '<div data-card-modal style="position:fixed; inset:0">' '  <article role="dialog" aria-modal="true"></article>' '</div>' > "$dir/Modal.dc.html"
}

teeth_expect() { # $1 = label, $2 = red|green, $3 = needle, $4 = source root
  local label="$1" verdict="$2" needle="$3" root="$4" out rc
  set +e
  out="$( (_GATE_ERRORS=0; run_check "$root" "$root/cards.manifest.json"; gate_summary "dashboard-cards-teeth") 2>&1)"
  rc=$?
  set -e
  if [ "$verdict" = "red" ]; then
    if [ "$rc" -eq 0 ] || ! printf '%s\n' "$out" | grep -Fq "$needle"; then
      echo "check-dashboard-cards --teeth: FALSE GREEN — $label did not red with '$needle':" >&2
      echo "$out" >&2
      return 1
    fi
    echo "check-dashboard-cards --teeth: $label reds"
  elif [ "$rc" -ne 0 ]; then
    echo "check-dashboard-cards --teeth: FALSE RED — $label should green:" >&2
    echo "$out" >&2
    return 1
  else
    echo "check-dashboard-cards --teeth: $label greens"
  fi
}

run_teeth() {
  local good fixture
  TEETH_WORK="$(mktemp -d)" || return 1
  trap 'rm -rf -- "${TEETH_WORK:-}"' EXIT

  good="$TEETH_WORK/good"
  prepare_card_fixture "$good" good
  teeth_expect "good registry/openCard/Modal control (including two ContractsView kinds)" green "" "$good" || return 1

  fixture="$TEETH_WORK/manifest-duplicate"; prepare_card_fixture "$fixture" duplicate
  teeth_expect "manifest duplicate kind" red "registers kind 'item' more than once" "$fixture" || return 1

  fixture="$TEETH_WORK/manifest-missing"; prepare_card_fixture "$fixture" missing
  teeth_expect "manifest missing kind" red "missing canonical row 'operational-process|Overview'" "$fixture" || return 1

  fixture="$TEETH_WORK/direct-detail"; prepare_card_fixture "$fixture" good
  printf '%s\n' '<script type="text/x-dc" data-dc-script>actions.openDetail("item", "space:item")</script>' > "$fixture/Bypass.dc.html"
  teeth_expect "direct detail bypass" red "detail/modal door other than openCard" "$fixture" || return 1

  fixture="$TEETH_WORK/role-dialog"; prepare_card_fixture "$fixture" good
  printf '%s\n' '<section role="dialog"></section>' > "$fixture/Rogue.dc.html"
  teeth_expect "role=dialog outside Modal" red "dialog markup/mechanics outside Modal.dc.html" "$fixture" || return 1

  fixture="$TEETH_WORK/aria-modal"; prepare_card_fixture "$fixture" good
  printf '%s\n' '<section aria-modal="true"></section>' > "$fixture/Rogue.dc.html"
  teeth_expect "aria-modal outside Modal" red "dialog markup/mechanics outside Modal.dc.html" "$fixture" || return 1

  fixture="$TEETH_WORK/fixed-backdrop"; prepare_card_fixture "$fixture" good
  printf '%s\n' '<section style="position:fixed; inset:0"></section>' > "$fixture/Rogue.dc.html"
  teeth_expect "fixed backdrop outside Modal" red "dialog markup/mechanics outside Modal.dc.html" "$fixture" || return 1

  fixture="$TEETH_WORK/multiline-fixed-backdrop"; prepare_card_fixture "$fixture" good
  printf '%s\n' '<style>' '.rogue {' '  position: fixed;' '  inset: 0;' '}' '</style>' > "$fixture/Rogue.dc.html"
  teeth_expect "multiline fixed backdrop outside Modal" red "dialog markup/mechanics outside Modal.dc.html" "$fixture" || return 1

  echo "check-dashboard-cards --teeth: PASS — registry duplicate/missing, direct bypass and single-/multiline modal-owner shapes red; good shared-component control greens"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
else
  run_check
  gate_summary "dashboard-cards"
fi
