#!/usr/bin/env bash
# check-feature-lint.sh — validate docs/features/<slug>/ against the canonical
# feature template (SSOT: docs/_templates/feature/ + docs/features/README.md).
#
# Ratchet design (per feedback_ratchet_pins_ssot_not_enum): features that OPT IN to
# the standard — a README.md whose YAML frontmatter carries `kind:` (the distinguishing
# canonical field; no legacy format had it) — are ENFORCED (errors fail the build).
# Features with partial/legacy frontmatter (or none) are grandfathered
# (reported as INFO, never fail). New features scaffolded from docs/_templates/feature/
# carry the frontmatter and are therefore enforced from creation — so the standard
# ratchets forward without a big-bang retro-migration of completed history.
#
# Tracker DAG checks (2026-07-13): for kind:epic trackers the gate additionally
# validates the blocked_by dependency graph — the thing /teamlead turns into
# parallel waves and /discover authors at decomposition time:
#   - every phase `spec:` path exists on disk;
#   - phase ids are unique;
#   - every local blocked_by id exists in phases:;
#   - cross-epic refs (`<epic-slug>:PN`) resolve to an existing epic tracker
#     that contains phase PN;
#   - the local DAG is acyclic;
#   - status: done (audit != n/a) requires commits: non-empty (research phases
#     with audit: n/a are exempt).
#
# Usage: bash scripts/check-feature-lint.sh            # lint docs/features/
#        bash scripts/check-feature-lint.sh --teeth    # self-test on broken fixtures
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

SCRIPT_ABS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
FEAT_DIR="docs/features"
STATUSES="draft active shipped superseded archived"
PHASE_STATUSES="pending in-progress done deferred"

# Extract the YAML frontmatter block (between the first two `---` lines).
frontmatter() { awk '/^---[[:space:]]*$/{c++; next} c==1{print} c>=2{exit}' "$1"; }

# Is $1 (a value) a member of the space-separated set $2 ?
in_set() { case " $2 " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }

# Emit one pipe-delimited record per phase: id|status|spec|blocked_csv|ncommits|audit
# Comment lines inside the tracker are ignored; blocked_by/commits accept the
# inline-flow form (`[a, b]`) and commits also the block-list form.
extract_phases() { # $1 = tracker path
  awk '
    function emit() { if (id != "") print id "|" status "|" spec "|" blocked "|" ncommits "|" audit }
    /^[^[:space:]]/ {
      if ($0 ~ /^phases:/) { inph = 1; next }
      if (inph) { emit(); id = "" ; inph = 0 }
      next
    }
    !inph { next }
    { sub(/\r$/, "") }
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*-[[:space:]]+id:/ {
      emit()
      v = $0; sub(/^[[:space:]]*-[[:space:]]+id:[[:space:]]*/, "", v); gsub(/["'\''[:space:]]/, "", v)
      id = v; status = ""; spec = ""; blocked = ""; ncommits = 0; audit = ""; incommits = 0
      next
    }
    incommits && /^[[:space:]]*-[[:space:]]/ { ncommits++; next }
    incommits { incommits = 0 }
    /^[[:space:]]+status:/  { v = $0; sub(/^[[:space:]]+status:[[:space:]]*/, "", v);  gsub(/[[:space:]]+$/, "", v); status = v; next }
    /^[[:space:]]+audit:/   { v = $0; sub(/^[[:space:]]+audit:[[:space:]]*/, "", v);   gsub(/[[:space:]]+$/, "", v); audit = v;  next }
    /^[[:space:]]+spec:/    { v = $0; sub(/^[[:space:]]+spec:[[:space:]]*/, "", v);    gsub(/["'\''"]/, "", v); gsub(/[[:space:]]+$/, "", v); spec = v; next }
    /^[[:space:]]+blocked_by:/ {
      v = $0; sub(/^[[:space:]]+blocked_by:[[:space:]]*/, "", v)
      gsub(/[][ "'\'' ]/, "", v)
      blocked = v; next
    }
    /^[[:space:]]+commits:/ {
      v = $0; sub(/^[[:space:]]+commits:[[:space:]]*/, "", v)
      if (v ~ /^\[/) { gsub(/[][ ]/, "", v); ncommits = (v == "" ? 0 : split(v, _a, ",")) }
      else { incommits = 1 }
      next
    }
    END { emit() }
  ' "$1"
}

# Validate one epic tracker's phase DAG. $1 = slug, $2 = tracker path, $3 = epic dir.
lint_tracker_dag() {
  local slug="$1" tracker="$2" dir="$3"
  local records all_ids="" pid pstatus pspec pblocked pncommits paudit

  records="$(extract_phases "$tracker")"
  [ -n "$records" ] || { gate_fail "$slug: tracker.yaml has 'phases:' but no parsable '- id:' entries"; return; }

  # Pass 1 — ids, uniqueness, spec paths, done⇒commits.
  while IFS='|' read -r pid pstatus pspec pblocked pncommits paudit; do
    [ -n "$pid" ] || continue
    if in_set "$pid" "$all_ids"; then gate_fail "$slug: duplicate phase id '$pid' in tracker.yaml"; fi
    all_ids="$all_ids $pid"
    if [ -n "$pspec" ] && [ ! -f "$dir/$pspec" ]; then
      gate_fail "$slug: phase $pid spec path '$pspec' does not exist under $dir/"
    fi
    if [ "$pstatus" = "done" ] && [ "$paudit" != "n/a" ] && [ "${pncommits:-0}" -eq 0 ]; then
      gate_fail "$slug: phase $pid is done (audit: ${paudit:-open}) but commits: is empty — record the shipping commits or mark audit: n/a"
    fi
  done <<< "$records"

  # Pass 2 — blocked_by referential integrity (local + cross-epic).
  local dep deps
  while IFS='|' read -r pid pstatus pspec pblocked pncommits paudit; do
    [ -n "$pid" ] && [ -n "$pblocked" ] || continue
    IFS=',' read -ra deps <<< "$pblocked"
    for dep in "${deps[@]}"; do
      [ -n "$dep" ] || continue
      case "$dep" in
        *:*)
          local xslug="${dep%%:*}" xpid="${dep##*:}"
          local xtracker="$FEAT_DIR/$xslug/tracker.yaml"
          if [ ! -f "$xtracker" ]; then
            gate_fail "$slug: phase $pid cross-epic dep '$dep' — no tracker at $xtracker"
          elif ! grep -Eq "^[[:space:]]*-[[:space:]]+id:[[:space:]]*[\"']?${xpid}[\"']?[[:space:]]*$" "$xtracker"; then
            gate_fail "$slug: phase $pid cross-epic dep '$dep' — phase '$xpid' not found in $xtracker"
          elif ! frontmatter "$dir/README.md" | grep -q "$xslug"; then
            gate_fail "$slug: phase $pid cross-epic dep '$dep' — '$xslug' not listed in the README's related: (the dependency contract requires it)"
          fi
          ;;
        *)
          in_set "$dep" "$all_ids" || gate_fail "$slug: phase $pid blocked_by '$dep' — no such phase id in tracker.yaml"
          ;;
      esac
    done
  done <<< "$records"

  # Pass 3 — local DAG acyclicity (iterative elimination; dangling/cross-epic deps
  # count as resolved — they are flagged above, not here).
  local pending="" resolved="" entry changed leftover
  while IFS='|' read -r pid pstatus pspec pblocked pncommits paudit; do
    [ -n "$pid" ] || continue
    local localdeps=""
    if [ -n "$pblocked" ]; then
      IFS=',' read -ra deps <<< "$pblocked"
      for dep in "${deps[@]}"; do
        case "$dep" in *:*) ;; *) if in_set "$dep" "$all_ids"; then localdeps="$localdeps,$dep"; fi ;; esac
      done
    fi
    pending="$pending $pid=${localdeps#,}"
  done <<< "$records"

  changed=1
  while [ "$changed" -eq 1 ]; do
    changed=0
    local next_pending=""
    for entry in $pending; do
      pid="${entry%%=*}"
      local depcsv="${entry#*=}" unmet=0
      if [ -n "$depcsv" ]; then
        IFS=',' read -ra deps <<< "$depcsv"
        for dep in "${deps[@]}"; do in_set "$dep" "$resolved" || unmet=1; done
      fi
      if [ "$unmet" -eq 0 ]; then resolved="$resolved $pid"; changed=1; else next_pending="$next_pending $entry"; fi
    done
    pending="$next_pending"
  done
  leftover="$(printf '%s' "$pending" | tr ' ' '\n' | cut -d= -f1 | grep -v '^$' | tr '\n' ' ')"
  [ -z "${leftover// /}" ] || gate_fail "$slug: blocked_by cycle detected among phases: ${leftover% }"
}

run_lint() {
  local enforced=0 legacy=0

  [ -d "$FEAT_DIR" ] || { echo "feature-lint: $FEAT_DIR not found"; exit 0; }

  for readme in "$FEAT_DIR"/*/README.md; do
    [ -f "$readme" ] || continue
    dir="$(dirname "$readme")"
    slug="$(basename "$dir")"
    fm="$(frontmatter "$readme")"

    # Opt-in detection: frontmatter carries `kind:` (the distinguishing canonical field).
    if ! printf '%s\n' "$fm" | grep -q '^kind:'; then
      legacy=$((legacy + 1))
      continue
    fi
    enforced=$((enforced + 1))

    # Required frontmatter fields.
    for field in slug title kind status owner created; do
      printf '%s\n' "$fm" | grep -q "^${field}:" || gate_fail "$slug: README frontmatter missing '${field}:'"
    done

    # slug must match the directory name.
    fm_slug="$(printf '%s\n' "$fm" | sed -n 's/^slug:[[:space:]]*//p' | head -1)"
    [ "$fm_slug" = "$slug" ] || gate_fail "$slug: frontmatter slug '$fm_slug' != dir name '$slug'"

    # kind ∈ {epic, spec}.
    kind="$(printf '%s\n' "$fm" | sed -n 's/^kind:[[:space:]]*//p' | head -1)"
    in_set "$kind" "epic spec" || gate_fail "$slug: kind '$kind' not in {epic, spec}"

    # status ∈ canonical enum.
    status="$(printf '%s\n' "$fm" | sed -n 's/^status:[[:space:]]*//p' | head -1)"
    in_set "$status" "$STATUSES" || gate_fail "$slug: status '$status' not in {$STATUSES}"

    # superseded_by set ⇒ status MUST be superseded.
    sup_by="$(printf '%s\n' "$fm" | sed -n 's/^superseded_by:[[:space:]]*//p' | head -1)"
    if [ -n "$sup_by" ] && [ "$status" != "superseded" ]; then
      gate_fail "$slug: superseded_by set but status is '$status' (must be 'superseded')"
    fi

    # Epics MUST carry a tracker.yaml with a valid phase schema + a sane DAG.
    if [ "$kind" = "epic" ]; then
      tracker="$dir/tracker.yaml"
      if [ ! -f "$tracker" ]; then
        gate_fail "$slug: kind:epic requires $tracker (none found)"
      else
        grep -q '^slug:' "$tracker"   || gate_fail "$slug: tracker.yaml missing 'slug:'"
        grep -q '^status:' "$tracker" || gate_fail "$slug: tracker.yaml missing 'status:'"
        grep -q '^phases:' "$tracker" || gate_fail "$slug: tracker.yaml missing 'phases:'"
        # Every phase status must be in the canonical phase enum.
        while IFS= read -r ph; do
          in_set "$ph" "$PHASE_STATUSES" || gate_fail "$slug: tracker phase status '$ph' not in {$PHASE_STATUSES}"
        done < <(sed -n 's/^[[:space:]]*status:[[:space:]]*//p' "$tracker" | tail -n +2)
        # DAG integrity (spec paths, refs, cycles, done⇒commits).
        if grep -q '^phases:' "$tracker"; then
          lint_tracker_dag "$slug" "$tracker" "$dir"
        fi
      fi
    fi
  done

  echo "feature-lint: $enforced enforced, $legacy legacy (grandfathered), $_GATE_ERRORS error(s)"
  if [ "$_GATE_ERRORS" -eq 0 ]; then
    echo "✅ feature-lint: all standardized features conform to the canonical template."
  fi
  gate_summary "check-feature-lint" || { echo "❌ feature-lint failed — fix the canonical-template violations above."; exit 1; }
  exit 0
}

# ── Teeth — the gate must red on each broken fixture and green on the control ──
teeth_fixture() { # $1 = root, $2 = slug, $3 = tracker phases body, $4 = extra frontmatter (optional)
  local root="$1" slug="$2" extra="${4:-}"
  mkdir -p "$root/docs/features/$slug/specs"
  printf -- '---\nslug: %s\ntitle: teeth fixture\nkind: epic\nstatus: active\nowner: teeth\ncreated: 2026-01-01\n%s---\n# t\n' "$slug" "$extra" \
    > "$root/docs/features/$slug/README.md"
  printf 'slug: %s\nstatus: active\nupdated: 2026-01-01\n\nphases:\n%s\n' "$slug" "$3" \
    > "$root/docs/features/$slug/tracker.yaml"
  touch "$root/docs/features/$slug/specs/01-a.md"
}

teeth_expect() { # $1 = fixture root, $2 = green|red, $3 = must-grep (red only), $4 = label
  local out rc
  out="$( (cd "$1" && bash "$SCRIPT_ABS") 2>&1 )"; rc=$?
  if [ "$2" = "green" ]; then
    [ "$rc" -eq 0 ] || { echo "feature-lint --teeth: FAILED — $4: gate red on a valid fixture:"; echo "$out"; exit 1; }
  else
    { [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "$3"; } || {
      echo "feature-lint --teeth: FAILED — $4: gate did not red with '$3':"; echo "$out"; exit 1; }
  fi
}

run_teeth() {
  local tmp; tmp="$(mktemp -d)" || { echo "feature-lint --teeth: mktemp failed"; exit 1; }
  trap 'rm -rf "$tmp"' EXIT

  local P_OK='  - id: P1
    status: done
    spec: specs/01-a.md
    blocked_by: []
    commits: [abc1234]
    audit: done
  - id: P2
    status: done
    spec: specs/01-a.md
    blocked_by: [P1]
    commits: []
    audit: n/a
  - id: P3
    status: pending
    spec: specs/01-a.md
    blocked_by: [P1, "other-epic:P1"]
    commits: []
    audit: open'
  local P_OTHER='  - id: P1
    status: pending
    spec: specs/01-a.md
    blocked_by: []
    commits: []
    audit: open'

  # Control: valid DAG + valid cross-epic ref (listed in related:) + done-with-n/a exemption → green.
  mkdir -p "$tmp/control"; teeth_fixture "$tmp/control" "good-epic" "$P_OK" 'related: [other-epic]
'; teeth_fixture "$tmp/control" "other-epic" "$P_OTHER"
  teeth_expect "$tmp/control" green "" "control"

  # A — cycle.
  mkdir -p "$tmp/cycle"; teeth_fixture "$tmp/cycle" "bad-cycle" '  - id: P1
    status: pending
    spec: specs/01-a.md
    blocked_by: [P2]
    commits: []
    audit: open
  - id: P2
    status: pending
    spec: specs/01-a.md
    blocked_by: [P1]
    commits: []
    audit: open'
  teeth_expect "$tmp/cycle" red "cycle detected" "cycle"

  # B — dangling local dep.
  mkdir -p "$tmp/dangling"; teeth_fixture "$tmp/dangling" "bad-dangling" '  - id: P1
    status: pending
    spec: specs/01-a.md
    blocked_by: [PX]
    commits: []
    audit: open'
  teeth_expect "$tmp/dangling" red "no such phase id" "dangling"

  # C — missing spec path.
  mkdir -p "$tmp/nospec"; teeth_fixture "$tmp/nospec" "bad-nospec" '  - id: P1
    status: pending
    spec: specs/99-missing.md
    blocked_by: []
    commits: []
    audit: open'
  teeth_expect "$tmp/nospec" red "does not exist" "missing-spec"

  # D — done without commits (audit: open, not exempt).
  mkdir -p "$tmp/donecommits"; teeth_fixture "$tmp/donecommits" "bad-done" '  - id: P1
    status: done
    spec: specs/01-a.md
    blocked_by: []
    commits: []
    audit: open'
  teeth_expect "$tmp/donecommits" red "commits: is empty" "done-without-commits"

  # E — cross-epic ref to a missing epic.
  mkdir -p "$tmp/xepic"; teeth_fixture "$tmp/xepic" "bad-xepic" '  - id: P1
    status: pending
    spec: specs/01-a.md
    blocked_by: ["ghost-epic:P9"]
    commits: []
    audit: open'
  teeth_expect "$tmp/xepic" red "no tracker at" "cross-epic-missing"

  # F — cross-epic ref valid but target epic not in the README's related:.
  mkdir -p "$tmp/norelated"; teeth_fixture "$tmp/norelated" "bad-norelated" '  - id: P1
    status: pending
    spec: specs/01-a.md
    blocked_by: ["other-epic:P1"]
    commits: []
    audit: open'
  teeth_fixture "$tmp/norelated" "other-epic" "$P_OTHER"
  teeth_expect "$tmp/norelated" red "not listed in the README" "cross-epic-unrelated"

  echo "✓ feature-lint --teeth: control greens; cycle, dangling dep, missing spec, done-without-commits, unresolved/unrelated cross-epic all red"
  exit 0
}

case "${1:-check}" in
  --teeth) run_teeth ;;
  check) run_lint ;;
  *) echo "feature-lint: unknown mode '${1}' (check | --teeth) — fail-closed."; exit 2 ;;
esac
