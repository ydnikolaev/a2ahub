#!/usr/bin/env bash
# check-render-ledger.sh — the render-ledger gate (answers-that-hold-2026-08
# P3, spec 03 §T1 "the gate: a render LEDGER, not a disjunction").
#
# THE RULE THE SPEC NAMES: "every computed field is reachable from the human
# path OR from --json" cannot red on the very class this gate exists to
# catch, because the phase that fixes a dropped field is often the SAME
# phase that adds --json — after which every tagged field satisfies the
# second disjunct by construction. So every field in this gate's universe is
# instead declared `human` (rendered on the human path, or its consequence
# is textually distinguishable there) or `json-only` (deliberately not —
# WITH A STRUCTURAL REASON, never a deferral: "not yet"/"later"/"TODO"/a
# bare phase id reopens the exact gap this ledger exists to close).
#
# THE UNIVERSE is (field:<surface>:<key>), DERIVED — never hand-listed —
# from internal/cli/cmd_contract_p6_test.go's own TestRenderLedgerSurfaceDump:
# internal/skillcoverage cannot import internal/cli/internal/contract/
# internal/space without becoming a domain package (see its own package doc
# comment), and a standalone `go run` of a file outside this module's tree
# cannot import an `internal/` package at all (Go's own internal-visibility
# rule) — so the reflection this gate depends on can only live inside a Go
# file that already imports those types, which is that test file. This gate
# asks the REAL test (never a synthetic universe) even in --teeth mode; only
# the LEDGER under test is a fixture, matching check-loop-coverage.sh's own
# "the vocabulary is real even while the ledger is not" precedent.
#
# This is a SEPARATE universe from schemas/prose-coverage.yaml's own
# `a2a __catalog --surfaces --json` (cli-inbox/cli-outbox/cli-thread/
# cli-show/mcp-item, plus whatever internal/skillcoverage.Register()ed
# surface cmd/a2a/catalog.go's catalogSurfaces() merges in) — see
# schemas/render-ledger.yaml's own header for why registering the contract
# surfaces this gate polices into that SHARED registry would also enroll
# them in prose-coverage's universe, reding that gate on rows nobody could
# add in this phase's allowlist.
#
# Usage: bash scripts/check-render-ledger.sh            # check the real tree
#        bash scripts/check-render-ledger.sh --teeth    # self-test on fixtures

# lane-inputs:
#   schemas/render-ledger.yaml
#   internal/cli/**
#   internal/contract/**
#   internal/space/**
#   internal/skillcoverage/**
#   internal/mcp/**
#   go.mod
#   go.sum
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   below self-locates scripts/lib/gate-lib.sh from this script's own path,
#   so the classifier cannot resolve the $(dirname ...) substitution to a
#   literal. `--teeth`'s own fixture writes under a `mktemp -d` directory are
#   the SYNTHETIC ledger this gate's teeth judge, never the real tree.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

# render_ledger_dump runs internal/cli's own TestRenderLedgerSurfaceDump and
# prints the JSON it writes — the ONE ask-the-program call site this whole
# gate depends on (see this file's own header for why it cannot be a plain
# `go run`). Fails CLOSED: an unreadable/empty dump would let this gate pass
# while policing nothing.
render_ledger_dump() {
  local dump
  dump="$(mktemp -d)/render-ledger-dump.json" || return 1
  if ! ( cd "$GATE_ROOT" && GOWORK=off RENDER_LEDGER_DUMP="$dump" go test ./internal/cli/... -run '^TestRenderLedgerSurfaceDump$' -count=1 ) >&2; then
    return 1
  fi
  if [ ! -s "$dump" ]; then
    return 1
  fi
  cat "$dump"
  rm -f "$dump"
}

# dump_surface_names prints every top-level surface name the dump's JSON
# object carries, one per line. Matches Go's `json.MarshalIndent(v, "", "
# ")`: every top-level key is indented by exactly two spaces — the same
# shape check-prose-coverage.sh's own surface_names already parses.
dump_surface_names() { # $1 = dump json
  printf '%s\n' "$1" | grep -E '^  "[A-Za-z0-9_-]+": \[$' | sed -E 's/^  "//; s/": \[$//'
}

# dump_surface_keys prints one surface's own key list, one per line,
# stripped of quotes and any trailing comma.
dump_surface_keys() { # $1 = dump json, $2 = surface name
  local json="$1" surface="$2" line
  line="  \"$surface\": ["
  printf '%s\n' "$json" | awk -v want="$line" '
    $0 == want { f=1; next }
    f && $0 ~ /^  \]/ { f=0; next }
    f { print }
  ' | sed -E 's/^[[:space:]]*"//; s/",?[[:space:]]*$//'
}

# ledger_surfaces prints render-ledger.yaml's own `surfaces:` block-list
# entries, one per line, in file order — the check-prose-coverage.sh
# precedent, duplicated rather than sourced (that file runs its own
# run_check/run_teeth at the bottom of the script; sourcing it would
# execute prose-coverage's OWN gate as a side effect of loading a helper).
ledger_surfaces() { # $1 = yaml path
  awk '
    /^surfaces:[ \t]*$/ { insec=1; next }
    /^[a-zA-Z_]+:[ \t]*$/ { insec=0 }
    insec && /^[ \t]*-[ \t]/ {
      line = $0
      sub(/^[ \t]*-[ \t]*/, "", line)
      gsub(/[ \t]+$/, "", line)
      print line
    }
  ' "$1"
}

# ledger_keys prints every key declared inside one top-level section
# (`human:` or `json-only:`) of render-ledger.yaml, one per line, in file
# order. A `# comment` line at the same 2-space indent is skipped.
ledger_keys() { # $1 = yaml path, $2 = section ("human:" or "json-only:")
  awk -v section="$2" '
    BEGIN { insec = 0 }
    /^[a-zA-Z_-]+:[ \t]*$/ {
      insec = ($0 == section) ? 1 : 0
      next
    }
    insec == 1 {
      if (substr($0, 1, 2) != "  ") next
      body = substr($0, 3)
      if (substr(body, 1, 1) == "#") next
      colon = index(body, ": ")
      if (colon == 0) next
      print substr(body, 1, colon - 1)
    }
  ' "$1"
}

ledger_has_key() { # $1 = yaml path, $2 = section, $3 = key
  ledger_keys "$1" "$2" | grep -qxF "$3"
}

# ledger_value prints the (quote-stripped, `\"`-unescaped) value of one key
# inside one section, or nothing if that key has no row there.
ledger_value() { # $1 = yaml path, $2 = section, $3 = key
  awk -v section="$2" -v key="$3" '
    BEGIN { insec = 0 }
    /^[a-zA-Z_-]+:[ \t]*$/ {
      insec = ($0 == section) ? 1 : 0
      next
    }
    insec == 1 {
      if (substr($0, 1, 2) != "  ") next
      body = substr($0, 3)
      if (substr(body, 1, 1) == "#") next
      colon = index(body, ": ")
      if (colon == 0) next
      k = substr(body, 1, colon - 1)
      if (k != key) next
      v = substr(body, colon + 2)
      gsub(/^"/, "", v)
      gsub(/"[ \t]*$/, "", v)
      gsub(/\\"/, "\"", v)
      print v
      exit
    }
  ' "$1"
}

# is_deferral_reason — DUPLICATED from check-prose-coverage.sh's own
# function of the same name and same rule (spec 13 §4 AC5's words, applied
# here by spec 03 §T1's identical ask): a reason that reads as a deferral —
# "not yet documented", "later", "TODO", a bare phase/wave id — reopens the
# exact gap this ledger exists to close, and must be refused rather than
# accepted as a decision. Not sourced from that file: it executes its own
# run_check/run_teeth at file scope, so `source`ing it would run prose-
# coverage's OWN gate as a side effect of loading one helper function.
is_deferral_reason() { # $1 = reason text
  printf '%s' "$1" | grep -qiE \
    '\btodo\b|\bfixme\b|not (yet )?documented|\blater\b|\bsoon\b|\bpending\b|\bbacklog\b|\bdeferred\b|\bplanned\b|\bcoming\b|\bwill be\b|\bphase [0-9]+\b|\bwave[ -][0-9a-zA-Z]+\b|\bP[0-9]+\b|\bH[0-9]+\b'
}

# check_declared_keys refuses a human:/json-only: key that is not part of
# the derived universe — a stale/renamed field: entry.
check_declared_keys() { # $1 = yaml, $2 = universe(nl, sorted)
  local yaml="$1" universe="$2" section key
  for section in "human:" "json-only:"; do
    while IFS= read -r key; do
      [ -z "$key" ] && continue
      if ! printf '%s\n' "$universe" | grep -qxF "$key"; then
        gate_fail "render-ledger: declared key \"$key\" in $section is not part of the derived universe (a stale/renamed field: entry)"
      fi
    done < <(ledger_keys "$yaml" "$section")
  done
}

# run_check walks the whole derived universe against render-ledger.yaml at
# $1 (GATE_ROOT for a real run, a fixture directory for --teeth) and
# reports. $2 is the REAL dump JSON — always asked of the real test, even
# under --teeth (see this file's own header: the ledger under test is
# synthetic, the universe it is judged against never is).
run_check() { # $1 = root, $2 = dump json
  local root="$1" json="$2"
  local yaml="$root/schemas/render-ledger.yaml"

  if [ ! -f "$yaml" ]; then
    gate_fail "render-ledger: $yaml does not exist"
    gate_summary "render-ledger"
    return $?
  fi
  if ! grep -qx 'schema: render-ledger/v1' "$yaml"; then
    gate_fail "render-ledger: $yaml is missing the \"schema: render-ledger/v1\" line"
  fi

  local snames
  snames="$(dump_surface_names "$json" | sort -u)"
  if [ -z "$snames" ]; then
    gate_fail "render-ledger: the render-ledger dump returned an empty surface set — failing closed rather than policing nothing"
    gate_summary "render-ledger"
    return $?
  fi

  # The surfaces: cross-check — a surface the dump derives but the ledger's
  # own surfaces: list does not declare (or vice versa) REDS by name. This
  # is this gate's own form of criterion 6 (US-5): a result type the
  # program actually serializes and the ledger does not yet know about.
  local ledger_snames s
  ledger_snames="$(ledger_surfaces "$yaml" | sort -u)"
  while IFS= read -r s; do
    [ -z "$s" ] && continue
    if ! printf '%s\n' "$ledger_snames" | grep -qxF "$s"; then
      gate_fail "render-ledger: the render-ledger dump derives surface \"$s\", which $yaml's own surfaces: list does not declare"
    fi
  done <<<"$snames"
  while IFS= read -r s; do
    [ -z "$s" ] && continue
    if ! printf '%s\n' "$snames" | grep -qxF "$s"; then
      gate_fail "render-ledger: $yaml's surfaces: list declares \"$s\", which the render-ledger dump no longer derives"
    fi
  done <<<"$ledger_snames"

  local universe="" k
  while IFS= read -r s; do
    [ -z "$s" ] && continue
    while IFS= read -r k; do
      [ -z "$k" ] && continue
      universe="$universe"$'field:'"$s"':'"$k"$'\n'
    done < <(dump_surface_keys "$json" "$s")
  done <<<"$snames"
  universe="$(printf '%s' "$universe" | sed '/^$/d' | sort -u)"
  if [ -z "$universe" ]; then
    gate_fail "render-ledger: the derived universe is empty — failing closed rather than policing nothing"
    gate_summary "render-ledger"
    return $?
  fi

  local member has_human has_json value reason
  while IFS= read -r member; do
    [ -z "$member" ] && continue
    if ledger_has_key "$yaml" "human:" "$member"; then has_human=1; else has_human=0; fi
    if ledger_has_key "$yaml" "json-only:" "$member"; then has_json=1; else has_json=0; fi

    if [ "$((has_human + has_json))" -gt 1 ]; then
      gate_fail "render-ledger: $member is declared in more than one place (human: and json-only:) in $yaml — pick one"
      continue
    fi
    if [ "$((has_human + has_json))" -eq 0 ]; then
      gate_fail "render-ledger: $member has no ledger entry in $yaml — neither human: nor json-only:"
      continue
    fi

    if [ "$has_human" -eq 1 ]; then
      value="$(ledger_value "$yaml" "human:" "$member")"
      if [ -z "$(printf '%s' "$value" | tr -d '[:space:]')" ]; then
        gate_fail "render-ledger: $member is declared human: in $yaml with a blank value — a row with nothing written reads as a decision nobody made"
        continue
      fi
      if is_deferral_reason "$value"; then
        gate_fail "render-ledger: $member's human: value reads as a DEFERRAL, not a structural fact: \"$value\""
      fi
      continue
    fi

    reason="$(ledger_value "$yaml" "json-only:" "$member")"
    if [ -z "$(printf '%s' "$reason" | tr -d '[:space:]')" ]; then
      gate_fail "render-ledger: $member is declared json-only: in $yaml with a blank reason — an exemption without a reason reads as a decision"
      continue
    fi
    if is_deferral_reason "$reason"; then
      gate_fail "render-ledger: $member's json-only: reason reads as a DEFERRAL, not a structural reason: \"$reason\" — a computed field rendered nowhere is a gap, not something to schedule"
    fi
  done <<<"$universe"

  check_declared_keys "$yaml" "$universe"

  gate_summary "render-ledger"
}

run_teeth() {
  local json
  if ! json="$(render_ledger_dump)"; then
    echo "render-ledger --teeth: could not read the render-ledger dump from internal/cli's own TestRenderLedgerSurfaceDump" >&2
    return 1
  fi

  local tmp
  tmp="$(mktemp -d)" || { echo "render-ledger --teeth: mktemp failed" >&2; return 1; }
  trap 'rm -rf "$tmp"' RETURN

  local snames victim_surface victim_field victim_field2
  snames="$(dump_surface_names "$json" | sort -u)"
  victim_surface="$(printf '%s\n' "$snames" | head -1)"
  victim_field="field:$victim_surface:$(dump_surface_keys "$json" "$victim_surface" | sort -u | head -1)"
  victim_field2="field:$victim_surface:$(dump_surface_keys "$json" "$victim_surface" | sort -u | sed -n '2p')"

  # write_good_fixture writes a small but FULLY declared, self-consistent
  # ledger against the REAL derived universe: every member is `json-only:`
  # with a genuine-sounding structural reason, except victim_field, which
  # is `human:`. Every mutation below starts from this baseline and breaks
  # exactly one thing.
  write_good_fixture() {
    mkdir -p "$tmp/schemas"
    {
      echo "schema: render-ledger/v1"
      echo "surfaces:"
      local s
      while IFS= read -r s; do
        [ -z "$s" ] && continue
        echo "  - $s"
      done <<<"$snames"
      echo "human:"
      echo "  $victim_field: \"fixture human reason — not a real classification\""
      echo "json-only:"
      local sur k m
      while IFS= read -r sur; do
        [ -z "$sur" ] && continue
        while IFS= read -r k; do
          [ -z "$k" ] && continue
          m="field:$sur:$k"
          [ "$m" = "$victim_field" ] && continue
          echo "  $m: \"structural fixture reason — not a real classification\""
        done < <(dump_surface_keys "$json" "$sur" | sort -u)
      done <<<"$snames"
    } >"$tmp/schemas/render-ledger.yaml"
  }

  write_good_fixture
  if ! (run_check "$tmp" "$json") >/dev/null 2>&1; then
    echo "render-ledger --teeth: FAILED — a fully declared, self-consistent fixture ledger stayed red" >&2
    return 1
  fi

  # (a) a field with no ledger row at all — PLANTED by removing its own
  # row, not merely absent from a from-scratch fixture (the "prove the
  # tooth RED by planting a field" ask) — must red naming it.
  write_good_fixture
  grep -v "^  $victim_field: " "$tmp/schemas/render-ledger.yaml" >"$tmp/schemas/render-ledger.yaml.new"
  mv "$tmp/schemas/render-ledger.yaml.new" "$tmp/schemas/render-ledger.yaml"
  out="$(run_check "$tmp" "$json" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_field has no ledger entry"; then
    echo "render-ledger --teeth: FAILED (a) — a field with no ledger row did not red naming it" >&2
    return 1
  fi

  # (b) a json-only: reason that reads as a deferral must red naming the
  # member and the finding.
  write_good_fixture
  sed -i.bak "s#^  $victim_field2: \".*\"#  $victim_field2: \"not documented yet\"#" "$tmp/schemas/render-ledger.yaml"
  rm -f "$tmp/schemas/render-ledger.yaml.bak"
  out="$(run_check "$tmp" "$json" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_field2" || ! printf '%s\n' "$out" | grep -q "reads as a DEFERRAL"; then
    echo "render-ledger --teeth: FAILED (b) — a deferral-shaped json-only: reason did not red naming the member and 'reads as a DEFERRAL'" >&2
    return 1
  fi

  # (c) a blank json-only: reason must red naming the member and "blank
  # reason".
  write_good_fixture
  sed -i.bak "s#^  $victim_field2: \".*\"#  $victim_field2: \"\"#" "$tmp/schemas/render-ledger.yaml"
  rm -f "$tmp/schemas/render-ledger.yaml.bak"
  out="$(run_check "$tmp" "$json" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_field2 is declared json-only:" || ! printf '%s\n' "$out" | grep -q "blank reason"; then
    echo "render-ledger --teeth: FAILED (c) — a blank json-only: reason did not red naming the member and 'blank reason'" >&2
    return 1
  fi

  # (d) a blank human: value must red naming the member.
  write_good_fixture
  sed -i.bak "s#^  $victim_field: \".*\"#  $victim_field: \"\"#" "$tmp/schemas/render-ledger.yaml"
  rm -f "$tmp/schemas/render-ledger.yaml.bak"
  out="$(run_check "$tmp" "$json" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_field is declared human:" || ! printf '%s\n' "$out" | grep -q "blank value"; then
    echo "render-ledger --teeth: FAILED (d) — a blank human: value did not red naming the member and 'blank value'" >&2
    return 1
  fi

  # (e) SEED THE ABSENCE: a whole SURFACE the dump derives but the ledger's
  # own surfaces: list does not declare must red naming it — the
  # substitution-style bug ("a hand map misses a newly added SURFACE", spec
  # 03 §T1's own "second trap") caught structurally rather than by a
  # single planted field.
  write_good_fixture
  grep -v "^  - $victim_surface\$" "$tmp/schemas/render-ledger.yaml" >"$tmp/schemas/render-ledger.yaml.new"
  mv "$tmp/schemas/render-ledger.yaml.new" "$tmp/schemas/render-ledger.yaml"
  out="$(run_check "$tmp" "$json" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "derives surface \"$victim_surface\", which"; then
    echo "render-ledger --teeth: FAILED (e) — a surface absent from surfaces: did not red naming it" >&2
    return 1
  fi

  # (f) a member declared BOTH human: and json-only: must red.
  write_good_fixture
  printf '  %s: "duplicated on purpose"\n' "$victim_field" >>"$tmp/schemas/render-ledger.yaml"
  out="$(run_check "$tmp" "$json" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF "$victim_field is declared in more than one place"; then
    echo "render-ledger --teeth: FAILED (f) — a member declared both human: and json-only: did not red naming it" >&2
    return 1
  fi

  # (g) a ledger key outside the derived universe must red naming it.
  write_good_fixture
  printf '  field:not-a-real-surface:not-a-real-key: "bogus fixture row"\n' >>"$tmp/schemas/render-ledger.yaml"
  out="$(run_check "$tmp" "$json" 2>&1 || true)"
  if ! printf '%s\n' "$out" | grep -qF 'declared key "field:not-a-real-surface:not-a-real-key" in json-only: is not part of the derived universe'; then
    echo "render-ledger --teeth: FAILED (g) — a ledger key outside the derived universe did not red naming it" >&2
    return 1
  fi

  echo "render-ledger --teeth: ok"
}

if [ "${1:-}" = "--teeth" ]; then
  run_teeth
  exit $?
fi

_real_json="$(render_ledger_dump)" || {
  gate_fail "render-ledger: could not read the render-ledger dump from internal/cli's own TestRenderLedgerSurfaceDump — failing closed rather than policing nothing"
  gate_summary "render-ledger"
  exit $?
}
run_check "$GATE_ROOT" "$_real_json"
exit $?
