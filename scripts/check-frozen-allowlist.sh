#!/usr/bin/env bash
# check-frozen-allowlist.sh — a phase PLAN whose `## Allowlist` grants a
# byte-frozen path refuses before the phase is dispatched, not after the
# ceiling catches the edit (rules-that-reach-2026-08 P2).
#
# The motivating incident: one-answer-2026-08 P6's SPEC `**Footprint**` line
# named no `schemas/` path at all — a gate intersecting Footprints with the
# ratchet would have stayed silent on the only case it was built for. P6's
# PLAN allowlist granted `schemas/release-notes/ (if the sentinel needs a
# schema edit)`, a DIRECTORY the ratchet freezes a file under
# (schemas/release-notes/v1/release-notes.schema.json,
# schemas/published-v1.sha256:17). The agent never left its allowlist; the
# allowlist was wrong. So this gate reads the PLAN's `## Allowlist`, never a
# spec's Footprint — see spec 02 §11 Amendments for the reconstruction.
#
# Two allowlist shapes, because a gate that knows only one is this epic's own
# defect (spec 02 §T5):
#   - an EPIC's per-phase grant: `docs/features/**/plans/*.plan.md` under a
#     `## Allowlist` (or `## Allowlists`) heading, one bullet per grant.
#   - a `kind: spec` feature's single `plan.md`, whose allowlist (if any) is a
#     COLUMN in a wave table rather than a heading. Measured 2026-08-20: the
#     three `kind: spec` features in the corpus (contract-confidence-loop-
#     2026-08, pre-p5-hardening-2026-07, site-2026-07 — all archived) carry no
#     plan.md at all, so this shape is exercised only by --teeth today; the
#     live scan NAMES each as unread rather than pretending to have judged it.
#
# The live scan must not red on a shipped phase (ADR-011 — no gate judges
# immutable history): a plan whose phase is `status: done` in its feature's
# tracker.yaml is SKIPPED, not judged. Two real historical grants prove this
# is not hypothetical — both already `done`:
#   - one-answer-2026-08 P6 (this gate's motivating incident, above).
#   - space-notify-2026-08 P1 granted `schemas/manifest/v1/space.schema.json`
#     directly (the exact frozen path) — the second incident
#     check-operational-confidence.sh's own teach_frozen_schema heredoc
#     already names ("notification_routes").
# The historical case is proven as a TEETH FIXTURE instead: a COPY of
# one-answer-2026-08 P6's real plan file, fed straight to the refusal path
# with no tracker in scope, so the shipped-phase skip cannot hide it.
#
# lane-inputs:
#   docs/features/**/plans/*.plan.md
#   docs/features/**/plan.md
#   docs/features/**/tracker.yaml
#   docs/features/**/README.md
#   schemas/published-v1.sha256
#   scripts/check-operational-confidence.sh
#
# lane-reads-opaque: the --teeth path rewrites a throwaway tracker in its own
#   mktemp dir ("$tmp/epic/tracker.yaml") to prove the shipped-phase skip is
#   keyed on `status:` and not merely on a tracker existing. The path is built
#   from a variable, so the classifier cannot resolve it; it is a fixture this
#   script creates and deletes, never a corpus input, and it changes no verdict
#   of the real scan.
#
# Landed together with the REPO_GATES entry, in one commit, for the reason the
# superseded note below records. Kept for the next author, because the rule it
# states outlives this file:
#
# (superseded 2026-08-20, on wiring) Deliberately carried NO `lane-inputs:` declaration while unwired. internal/lane's
# loader refuses the ENTIRE derivation when a `# lane-inputs:` block lands
# ahead of its REPO_GATES recipe (scripts/check-spec-verify-refs.sh's own
# header documents this, empirically). The block this gate wants is reported
# verbatim in this phase's report, for the lead to land atomically with
# REPO_GATES.
#
# Usage:
#   bash scripts/check-frozen-allowlist.sh          # live corpus scan
#   bash scripts/check-frozen-allowlist.sh --teeth   # offline self-test
set -euo pipefail

# Reuse the operational-confidence gate's ROOT resolution and its manifest —
# sourcing it (now BASH_SOURCE-guarded, see that file's own header) defines
# functions/vars and runs no ceremony. teach_frozen_schema is quoted VERBATIM
# from there for every refusal below, per spec 02 §T5's "already correct, must
# not be re-authored" rule.
#
# check_hashes() (that file) reads schemas/published-v1.sha256 INLINE, in a
# `while read -r expected path` loop with no standalone "list the frozen
# paths" function to call — and the spec's own "no logic change" restriction
# on that file (its Footprint entry is a dispatcher guard ONLY) means that
# loop cannot be factored into one here. So the manifest-path extraction below
# (`awk '{print $2}'`) is the IDENTICAL one-liner check_hashes() already uses
# for the same purpose (that file, `listed="$(awk '{print $2}' "$manifest" |
# sort)"`) and the same idiom run_teeth() uses to walk it
# (`while read -r _ path`) — not a second, drifting grammar of the ratchet.
# shellcheck source=scripts/check-operational-confidence.sh
source "$(dirname "${BASH_SOURCE[0]}")/check-operational-confidence.sh"

MANIFEST="$ROOT/schemas/published-v1.sha256"

note() { printf '  \033[33m•\033[0m %s\n' "$1"; }
ok()   { printf '  \033[32m✓\033[0m %s\n' "$1"; }

# frozen_paths prints every repo-relative path schemas/published-v1.sha256
# freezes, one per line — column 2 of `<sha256>  <path>`, the same field
# check_hashes() reads (see the header note above).
frozen_paths() {
  awk '{print $2}' "$MANIFEST"
}

# refuse prints the refusal — naming the plan, the granted entry, the frozen
# file it covers — then the sanctioned alternative, quoted verbatim from the
# sourced gate (US-2).
refuse() { # $1=plan(repo-relative) $2=entry $3=frozen-path
  echo "check-frozen-allowlist: FAIL — $1's allowlist grants '$2', which covers the byte-frozen $3 (schemas/published-v1.sha256)." >&2
  teach_frozen_schema "$3"
}

# entry_covers_frozen: does allowlist ENTRY (already stripped of its
# parenthetical caveat) cover FROZEN? Three admissible shapes (spec 02 §T5):
# exact path, a directory grant (trailing `/`, covers everything beneath —
# the P6 incident's own shape), or a glob (matched with bash `[[ == ]]`
# pattern semantics, which do not treat `/` specially, so `a/**` / `a/*/b`
# both work without relying on extglob/globstar).
entry_covers_frozen() { # $1=entry $2=frozen
  local entry="$1" frozen="$2"
  [ -n "$entry" ] || return 1
  [ "$entry" = "$frozen" ] && return 0
  case "$entry" in
    */) case "$frozen" in "$entry"*) return 0 ;; esac ;;
  esac
  # shellcheck disable=SC2053 # deliberate glob match — $entry IS the glob pattern
  case "$entry" in
    *'*'*) [[ "$frozen" == $entry ]] && return 0 ;;
  esac
  return 1
}

# plan_allowlist_entries prints one candidate repo-relative path per line,
# extracted from every backtick-quoted span inside a plan's `## Allowlist` /
# `## Allowlists` section (through the next `## ` heading) — but ONLY from its
# bullet lines (`^- ` and their indented continuation lines, up to the next
# blank line or the next bullet). This excludes prose paragraphs that can
# share the same heading: agent-ops-2026-07 P12's `## Allowlists` opens with a
# "Lead-reserved throughout — in no agent's allowlist" paragraph naming
# several backtick paths that are explicitly NOT grants; scoping to bullets
# only keeps those out of judgement.
#
# A trailing parenthetical caveat and trailing comma/period are stripped from
# each extracted token, so `schemas/release-notes/ (if the sentinel needs a
# schema edit)` still yields `schemas/release-notes/` — the P6 incident's own
# shape, and the parser-tolerance row spec 02 §6 asks for.
plan_allowlist_entries() { # $1=plan file
  # shellcheck disable=SC2016 # the backtick-matching grep/sed below are literal patterns, not command substitution
  awk '
    /^## Allowlists?( |$)/ { insection=1; next }
    insection && /^## / { insection=0 }
    insection {
      if ($0 ~ /^- /) { inbullet=1; print; next }
      if ($0 ~ /^[[:space:]]*$/) { inbullet=0; next }
      if (inbullet) { print }
    }
  ' "$1" \
    | grep -oE '`[^`]+`' \
    | sed -E 's/^`//; s/`$//' \
    | sed -E 's/[[:space:]]*\([^)]*\)[[:space:]]*$//' \
    | sed -E 's/[,.]+$//' \
    | sed -E 's/[[:space:]]+$//'
}

# has_allowlist_heading: does this plan carry a `## Allowlist(s)` heading at
# all? A plan with none is COUNTED and NAMED by the caller, never silently
# skipped and never a crash (US-1, the missing-section testing row).
has_allowlist_heading() { # $1=plan file
  grep -qE '^## Allowlists?( |$)' "$1"
}

# judge_plan_entries refuses (via `refuse`) every entry in $1's allowlist that
# covers a frozen path. Pure judgement — no tracker/shipped-phase awareness,
# so the AC3 teeth fixture below can call it directly on a COPY of the
# historical file with no tracker in scope.
judge_plan_entries() { # $1=plan file, $2=label to report (repo-relative-ish)
  local plan="$1" label="$2" entry frozen rc=0
  while IFS= read -r entry; do
    [ -n "$entry" ] || continue
    while IFS= read -r frozen; do
      [ -n "$frozen" ] || continue
      if entry_covers_frozen "$entry" "$frozen"; then
        refuse "$label" "$entry" "$frozen"
        rc=1
      fi
    done < <(frozen_paths)
  done < <(plan_allowlist_entries "$plan")
  return "$rc"
}

# tracker_status_for_plan prints the `status:` of the tracker.yaml phase entry
# whose `plan:` field equals $1's basename under plans/ — or nothing if no
# tracker, or no matching phase entry, is found. ADR-011's shipped-phase
# carve-out reads THIS, not the plan file's own (inconsistent — some plans
# carry YAML frontmatter `status:`, others a prose `**Status**:` line, see
# spec 02 §T5) status text.
#
# Reads the WHOLE phase block (one `- id:` to the next, or EOF) before
# deciding, rather than exiting the moment `plan:` is seen — field order is
# NOT stable across trackers. agent-exchange-2026-08's P5 entry carries ~170
# lines of comment BETWEEN `plan:` and `status:` (its `status:` comes after),
# while its own P6 entry carries `status:` before `plan:`. An exit-on-`plan:`
# reader would silently read P5 as status "" (not done) instead of its real
# value — safe by accident here only because P5 also carries no `##
# Allowlist` heading; a differently-shaped block could have made this a false
# red on shipped history. Found and fixed while proving AC4 on the real
# corpus, not guessed.
tracker_status_for_plan() { # $1=plan file (absolute), $2=feature dir (absolute)
  local plan="$1" feature_dir="$2" tracker rel
  tracker="$feature_dir/tracker.yaml"
  [ -f "$tracker" ] || return 0
  rel="plans/$(basename "$plan")"
  awk -v rel="$rel" '
    function flush() {
      if (blk_plan == rel) { print blk_status }
    }
    /^[[:space:]]*-[[:space:]]+id:/ {
      flush(); blk_plan=""; blk_status=""
    }
    /^[[:space:]]*status:/ && blk_status == "" {
      s=$0; sub(/^[[:space:]]*status:[[:space:]]*/, "", s); blk_status=s
    }
    /^[[:space:]]*plan:/ && blk_plan == "" {
      p=$0; sub(/^[[:space:]]*plan:[[:space:]]*/, "", p); sub(/[[:space:]]*$/, "", p); blk_plan=p
    }
    END { flush() }
  ' "$tracker"
}

# ── the spec-mode (kind: spec) wave-table shape ─────────────────────────────

# wave_table_allowlist_entries prints every backtick-quoted path found in the
# column of $1's first markdown table whose header cell matches /allowlist/i
# (case-insensitive — this repo's own epic index tables spell it "Files
# (allowlist)", spec 02 §T5's "wave-table COLUMN, not a heading"). No live
# example exists in the corpus today (see header note); proven by --teeth.
wave_table_allowlist_entries() { # $1=plan.md
  # shellcheck disable=SC2016 # the backtick-matching grep/sed below are literal patterns, not command substitution
  awk -F'|' '
    /^\|/ {
      if (colidx == 0) {
        for (i = 1; i <= NF; i++) {
          h = $i; gsub(/^[[:space:]]+|[[:space:]]+$/, "", h)
          if (h ~ /[Aa]llowlist/) { colidx = i; break }
        }
        next
      }
      if ($0 ~ /^\|[[:space:]]*[-:]+[[:space:]]*\|/) next
      if (colidx > 0 && colidx <= NF) print $(colidx)
    }
    !/^\|/ { colidx = 0 }
  ' "$1" \
    | grep -oE '`[^`]+`' \
    | sed -E 's/^`//; s/`$//' \
    | sed -E 's/[[:space:]]*\([^)]*\)[[:space:]]*$//' \
    | sed -E 's/[,.]+$//' \
    | sed -E 's/[[:space:]]+$//'
}

has_wave_table_allowlist_column() { # $1=plan.md
  awk -F'|' '/^\|/ { for (i=1;i<=NF;i++) { h=$i; gsub(/^[[:space:]]+|[[:space:]]+$/,"",h); if (h ~ /[Aa]llowlist/) { print "y"; exit } } }' "$1" | grep -q y
}

judge_wave_table() { # $1=plan.md, $2=label
  local plan="$1" label="$2" entry frozen rc=0
  while IFS= read -r entry; do
    [ -n "$entry" ] || continue
    while IFS= read -r frozen; do
      [ -n "$frozen" ] || continue
      if entry_covers_frozen "$entry" "$frozen"; then
        refuse "$label" "$entry" "$frozen"
        rc=1
      fi
    done < <(frozen_paths)
  done < <(wave_table_allowlist_entries "$plan")
  return "$rc"
}

# ── the live corpus scan (AC4/AC9/AC9b) ─────────────────────────────────────

scan_corpus() {
  local root="$1" plan rel feature_dir status
  local total=0 skipped_done=0 unreadable=0 judged=0 red=0
  local spec_total=0 spec_unread=0

  while IFS= read -r plan; do
    total=$((total + 1))
    rel="${plan#"$root"/}"
    feature_dir="$(dirname "$(dirname "$plan")")"
    status="$(tracker_status_for_plan "$plan" "$feature_dir")"
    if [ "$status" = "done" ]; then
      skipped_done=$((skipped_done + 1))
      continue
    fi
    if ! has_allowlist_heading "$plan"; then
      unreadable=$((unreadable + 1))
      note "$rel carries no ## Allowlist heading — counted, not judged"
      continue
    fi
    judged=$((judged + 1))
    judge_plan_entries "$plan" "$rel" || red=$((red + 1))
  done < <(find "$root/docs/features" -path '*/plans/*.plan.md' | sort)

  while IFS= read -r readme; do
    kind="$(sed -n 's/^kind:[[:space:]]*//p' "$readme" | head -1)"
    [ "$kind" = "spec" ] || continue
    spec_total=$((spec_total + 1))
    feature_dir="$(dirname "$readme")"
    plan_md="$feature_dir/plan.md"
    rel="${plan_md#"$root"/}"
    if [ ! -f "$plan_md" ]; then
      spec_unread=$((spec_unread + 1))
      note "${feature_dir#"$root"/} is kind:spec with no plan.md — no wave-table allowlist to read, named unread"
      continue
    fi
    if ! has_wave_table_allowlist_column "$plan_md"; then
      spec_unread=$((spec_unread + 1))
      note "$rel carries no wave-table Allowlist column — counted, not judged"
      continue
    fi
    judged=$((judged + 1))
    judge_wave_table "$plan_md" "$rel" || red=$((red + 1))
  done < <(find "$root/docs/features" -name README.md | sort)

  echo "check-frozen-allowlist: scanned $total epic phase plan(s) — $skipped_done shipped/done skipped (ADR-011), $unreadable with no ## Allowlist heading (counted above); $spec_total kind:spec feature(s) — $spec_unread with no wave-table allowlist to read (counted above); $judged judged."
  if [ "$red" -ne 0 ]; then
    echo "check-frozen-allowlist: FAIL — $red plan(s) grant a byte-frozen path (see refusals above)." >&2
    return 1
  fi
  echo "check-frozen-allowlist: OK — no live plan grants a byte-frozen path."
  return 0
}

# ── teeth ────────────────────────────────────────────────────────────────

expect_red() { # $1=label $2=needle, then the command
  local label="$1" needle="$2"; shift 2
  local out rc=0
  out="$("$@" 2>&1)" || rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "check-frozen-allowlist --teeth: FAILED — $label stayed GREEN" >&2
    echo "$out" >&2; exit 1
  fi
  printf '%s' "$out" | grep -q "$needle" || {
    echo "check-frozen-allowlist --teeth: FAILED — $label reds, but not with '$needle'" >&2
    echo "$out" >&2; exit 1
  }
  ok "$label → RED, naming '$needle'"
}

expect_green() { # $1=label, then the command
  local label="$1"; shift
  local out rc=0
  out="$("$@" 2>&1)" || rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "check-frozen-allowlist --teeth: FAILED — $label stayed RED" >&2
    echo "$out" >&2; exit 1
  fi
  ok "$label → GREEN"
}

teeth() {
  local tmp
  tmp="$(mktemp -d)" || { echo "check-frozen-allowlist --teeth: mktemp failed" >&2; exit 1; }
  trap 'rm -rf "$tmp"' RETURN

  # AC1 — a direct grant of the exact frozen path → RED, naming both.
  cat > "$tmp/direct.plan.md" <<'EOF'
# P-teeth — direct grant

## Allowlist (repo-relative)

- `schemas/manifest/v1/space.schema.json`
- `internal/cli/adapters.go`
EOF
  expect_red "AC1 direct grant of a frozen file" \
    "schemas/manifest/v1/space.schema.json" \
    judge_plan_entries "$tmp/direct.plan.md" "teeth/direct.plan.md"

  # AC2 — a DIRECTORY grant covering a frozen file beneath it, with a
  # parenthetical caveat inside the backticks (the P6 incident's exact shape)
  # → RED, naming the granted entry and the frozen file it covers.
  cat > "$tmp/dir.plan.md" <<'EOF'
# P-teeth — directory grant

## Allowlist (repo-relative)

- `scripts/some-new-gate.sh (NEW)`
- `schemas/release-notes/ (if the sentinel needs a schema edit)`
EOF
  out="$(judge_plan_entries "$tmp/dir.plan.md" "teeth/dir.plan.md" 2>&1)" && {
    echo "check-frozen-allowlist --teeth: FAILED — AC2 directory grant stayed GREEN" >&2; echo "$out" >&2; exit 1; }
  printf '%s' "$out" | grep -q "schemas/release-notes/" || {
    echo "check-frozen-allowlist --teeth: FAILED — AC2 red, but did not name the granted entry" >&2; echo "$out" >&2; exit 1; }
  printf '%s' "$out" | grep -q "schemas/release-notes/v1/release-notes.schema.json" || {
    echo "check-frozen-allowlist --teeth: FAILED — AC2 red, but did not name the frozen file it covers" >&2; echo "$out" >&2; exit 1; }
  printf '%s' "$out" | grep -q "how to add a field to a frozen v1 schema" || {
    echo "check-frozen-allowlist --teeth: FAILED — AC2 red, but did not quote the sanctioned alternative" >&2; echo "$out" >&2; exit 1; }
  ok "AC2/AC5 directory grant (parenthetical caveat inside backticks) → RED, naming plan/entry/frozen-file and quoting the sanctioned alternative"

  # AC3 — one-answer-2026-08 P6's REAL plan file, copied unmodified, fed
  # straight to the refusal path (no tracker in scope, so the shipped-phase
  # skip cannot hide it). If this does not red, the gate is not written yet.
  local p6="$ROOT/docs/features/archive/one-answer-2026-08/plans/06-the-release-record-and-the-world-agree.plan.md"
  if [ -f "$p6" ]; then
    cp "$p6" "$tmp/p6.plan.md"
    out="$(judge_plan_entries "$tmp/p6.plan.md" "teeth/p6.plan.md (one-answer-2026-08 P6, copied)" 2>&1)" && {
      echo "check-frozen-allowlist --teeth: FAILED — AC3 P6's real plan allowlist stayed GREEN" >&2; echo "$out" >&2; exit 1; }
    printf '%s' "$out" | grep -q "schemas/release-notes/" || {
      echo "check-frozen-allowlist --teeth: FAILED — AC3 red, but did not name P6's granted entry" >&2; echo "$out" >&2; exit 1; }
    ok "AC3 one-answer-2026-08 P6's real plan file (copied, unmodified) → RED"
  else
    echo "check-frozen-allowlist --teeth: SKIP AC3 — $p6 absent (fixture source not in this checkout)"
  fi

  # control — a clean allowlist (this very phase's own plan) → GREEN, no
  # false positive.
  local p2="$ROOT/docs/features/archive/rules-that-reach-2026-08/plans/02-a-frozen-path-refuses-before-the-work.plan.md"
  if [ -f "$p2" ]; then
    cp "$p2" "$tmp/p2.plan.md"
    expect_green "control: this phase's own clean plan allowlist" \
      judge_plan_entries "$tmp/p2.plan.md" "teeth/p2.plan.md"
  fi

  # missing `## Allowlist` heading → counted, never a crash.
  cat > "$tmp/noheading.plan.md" <<'EOF'
# P-teeth — no allowlist heading at all

## Goal

Nothing here names a section this gate can read.
EOF
  if has_allowlist_heading "$tmp/noheading.plan.md"; then
    echo "check-frozen-allowlist --teeth: FAILED — a plan with no ## Allowlist heading was reported as having one" >&2; exit 1
  fi
  ok "AC9 a plan with no ## Allowlist heading → not judged, not a crash (caller counts and names it)"

  # parser tolerance — trailing comma, and a glob that matches a frozen path.
  cat > "$tmp/tolerance.plan.md" <<'EOF'
## Allowlist

- `internal/cli/adapters.go`,
- `schemas/manifest/v1/*.schema.json`
EOF
  out="$(judge_plan_entries "$tmp/tolerance.plan.md" "teeth/tolerance.plan.md" 2>&1)" && {
    echo "check-frozen-allowlist --teeth: FAILED — a glob grant covering a frozen path stayed GREEN" >&2; echo "$out" >&2; exit 1; }
  printf '%s' "$out" | grep -q "schemas/manifest/v1/space.schema.json" || {
    echo "check-frozen-allowlist --teeth: FAILED — glob-grant red, but did not name the frozen file" >&2; echo "$out" >&2; exit 1; }
  ok "parser tolerance: trailing comma ignored; a glob entry (schemas/manifest/v1/*.schema.json) still covers and reds"

  # lead-reserved / off-limits prose sharing the SAME `## Allowlist` heading
  # (agent-ops-2026-07 P12's real shape) must not be read as a grant.
  cat > "$tmp/prose.plan.md" <<'EOF'
## Allowlists

**Lead-reserved throughout — in no agent's allowlist except where named:**
`schemas/manifest/v1/space.schema.json`, `Makefile`.

- `internal/cli/adapters.go`
EOF
  expect_green "prose naming a frozen path OUTSIDE any bullet is not read as a grant (agent-ops-2026-07 P12 shape)" \
    judge_plan_entries "$tmp/prose.plan.md" "teeth/prose.plan.md"

  # the spec-mode (kind: spec) wave-table shape — no live example exists
  # today (see header), so this is the only place it is proven at all.
  cat > "$tmp/wave.plan.md" <<'EOF'
# Plan — teeth spec-mode feature

| Wave | Phases | Files (allowlist) |
|---|---|---|
| W1 | P1 | `schemas/manifest/v1/space.schema.json` |
| W2 | P2 | `internal/cli/adapters.go` |
EOF
  out="$(judge_wave_table "$tmp/wave.plan.md" "teeth/wave.plan.md" 2>&1)" && {
    echo "check-frozen-allowlist --teeth: FAILED — AC9b wave-table Allowlist column grant stayed GREEN" >&2; echo "$out" >&2; exit 1; }
  printf '%s' "$out" | grep -q "schemas/manifest/v1/space.schema.json" || {
    echo "check-frozen-allowlist --teeth: FAILED — AC9b red, but did not name the frozen file" >&2; echo "$out" >&2; exit 1; }
  ok "AC9b spec-mode wave-table Allowlist COLUMN grant → RED (proven by fixture — no live example exists today)"

  cat > "$tmp/wave-clean.plan.md" <<'EOF'
| Wave | Phases | Files (allowlist) |
|---|---|---|
| W1 | P1 | `internal/cli/adapters.go` |
EOF
  expect_green "AC9b wave-table Allowlist column, clean" judge_wave_table "$tmp/wave-clean.plan.md" "teeth/wave-clean.plan.md"

  # shipped-phase skip (ADR-011): a `status: done` tracker entry hides a
  # frozen grant from the SCAN (not from judge_plan_entries directly — that
  # is exactly the AC3/AC4 split spec 02 §11 resolves).
  mkdir -p "$tmp/epic/plans"
  cat > "$tmp/epic/plans/01-x.plan.md" <<'EOF'
## Allowlist

- `schemas/manifest/v1/space.schema.json`
EOF
  cat > "$tmp/epic/tracker.yaml" <<'EOF'
phases:
  - id: P1
    title: teeth fixture
    status: done
    plan: plans/01-x.plan.md
EOF
  mkdir -p "$tmp/docs/features/active"
  ln -s "$tmp/epic" "$tmp/docs/features/active/teeth-epic-2026-08" 2>/dev/null || cp -R "$tmp/epic" "$tmp/docs/features/active/teeth-epic-2026-08"
  status="$(tracker_status_for_plan "$tmp/docs/features/active/teeth-epic-2026-08/plans/01-x.plan.md" "$tmp/docs/features/active/teeth-epic-2026-08")"
  [ "$status" = "done" ] || {
    echo "check-frozen-allowlist --teeth: FAILED — tracker_status_for_plan did not read status: done (got '$status')" >&2; exit 1; }
  ok "shipped-phase (status: done) is read correctly from tracker.yaml — the scan's skip rule (ADR-011)"

  sed -i.bak 's/status: done/status: pending/' "$tmp/epic/tracker.yaml"
  rm -f "$tmp/epic/tracker.yaml.bak"
  cp "$tmp/epic/tracker.yaml" "$tmp/docs/features/active/teeth-epic-2026-08/tracker.yaml" 2>/dev/null || true
  status="$(tracker_status_for_plan "$tmp/docs/features/active/teeth-epic-2026-08/plans/01-x.plan.md" "$tmp/docs/features/active/teeth-epic-2026-08")"
  [ "$status" = "pending" ] || {
    echo "check-frozen-allowlist --teeth: FAILED — tracker_status_for_plan did not follow status back to pending (got '$status')" >&2; exit 1; }
  ok "an un-shipped phase (status: pending) is not exempt — the discriminator is status, not merely 'has a tracker'"

  echo "check-frozen-allowlist --teeth: all teeth bite."
}

# ── entrypoint ───────────────────────────────────────────────────────────

if [ "${1:-}" = "--teeth" ]; then
  teeth
  exit 0
fi

scan_corpus "$ROOT"
