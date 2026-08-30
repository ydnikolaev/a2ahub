#!/usr/bin/env bash
# check-plugin-manifests.sh — refuses a plugin manifest that disagrees with
# the binary (built-not-listed-2026-08 P6, spec 06 §T6).
#
# The install contract states four facts once — the launch line, the
# skill-tree path, the credential sentence, the one-line description — and
# names the SHIPPING artifact that owns each (docs/install-contract.md,
# which is itself a reading surface, never a citable source: it lives under
# docs/, in scripts/lib/strip-set.txt, and does not exist in a public
# checkout). This gate compares every manifest against the shipping
# artifact directly, never against that page, so it holds in every checkout
# the page itself does not survive in.
#
#   1. the launch line             — the binary's own dispatch table, read
#                                     via `a2a __catalog` (never a manifest's
#                                     own idea of what the binary accepts)
#   2. the skill-tree path         — skill/embed.go's `//go:embed all:`
#                                     root, checked against each discovered
#                                     PLUGIN ROOT's own skills/<root> entry
#                                     (the directory hosts actually scan,
#                                     Claude Code docs: "the default skills/
#                                     directory is always scanned" — no
#                                     manifest key required). A manifest
#                                     that happens to declare a skill path
#                                     is an ADDITIONAL checked side, not the
#                                     authoritative one.
#   3. the credential sentence     — internal/space.CredentialEnvVar's
#                                     "A2A_TOKEN_" prefix, extracted from
#                                     source, never hand-typed here, compared
#                                     against every credential-variable-
#                                     shaped token found in the files
#                                     reachable under each plugin's own
#                                     skills/ tree — the SHIPPED SKILL
#                                     CONTENT, not the manifest. No host
#                                     schema this gate covers has a
#                                     credential field to declare (see the
#                                     KNOWN LIMIT note below for why).
#   4. the one-line description    — README.md's own opening bold sentence
#
# A fifth, non-comparison assertion covers spec 06's T6 fourth table row:
# the CI substitution step that rewrites `version`/`identifier`/
# `fileSha256` in a manifest before `mcp-publisher publish` — those values
# are inert placeholders at rest (spec 05 §T2), so there is nothing to diff;
# the gate instead asserts the REWRITING MECHANISM exists and runs before
# the publish call. P5's workflow file does not exist yet at the time this
# gate was written, so an ABSENT .github/workflows/publish-mcp.yml is
# simply not checked — see check_publish_workflow below.
#
# THE ASYMMETRY IS LOAD-BEARING AND MUST NOT BE REFACTORED AWAY.
#
# The canonical side (what facts 1-4 are checked AGAINST) always reads from
# $GATE_ROOT — this repository's real, pristine checkout, resolved by
# gate-lib.sh from THIS SCRIPT'S OWN PATH — and is never redirected by
# PLUGIN_MANIFESTS_ROOT, the variable the teeth use to point the CHECKED
# side at a scratch fixture tree. The checked side — every candidate
# manifest file, plus (for facts 2 and 3) every file reachable under each
# discovered plugin root's own skills/ tree — is read off disk at
# $PLUGIN_MANIFESTS_ROOT (default $GATE_ROOT), which
# scripts/tests/check_plugin_manifests_test.sh DOES repoint at a mutated
# copy for its drift fixtures.
#
# Two parsers, two code paths, and deliberately NO shared "read the launch
# line" (or credential prefix, or skill root) helper spanning both sides.
# `scripts/check_contract_carried_set.sh`'s own header names this trap and
# it applies unchanged here: the obvious implementation is one helper used
# for both sides, because that looks like the DRY thing to do. Then the
# teeth mutate a manifest, the shared helper is applied to both the
# canonical read and the checked read, both sides move together, and the
# gate compares the mutated copy against itself and agrees. Green, forever,
# proving nothing. A reviewer who files "these read the same shape, factor
# them out" is proposing the defect, not the cleanup — read this paragraph
# before agreeing with them.
#
# What follows from the asymmetry: the four canonical-fact readers below
# (canonical_launch_line, canonical_credential_prefix, canonical_skill_root,
# canonical_description) take NO scan-root argument at all — they close
# over $GATE_ROOT directly — while every checked-side function takes
# scan_root as an explicit, first, positional argument. That shape
# difference is the tell that the split survived: a refactor that gave the
# canonical readers a root parameter "for symmetry" is the first step of
# recreating the shared-helper trap.
#
# Verdict vocabulary (scripts/lib/gate-lib.sh, three-valued): a manifest
# that is missing, unreadable or unparseable is gate_unmeasured, never
# gate_fail. An EMPTY manifest set is also gate_unmeasured — a gate that
# judges nothing must say so rather than print a green line. A manifest
# that parses but declares none of the four facts (Antigravity's schema is
# name+description only) contributes zero comparisons, same as an absent
# fact — not a failure.
#
# KNOWN LIMIT (spec 06 §9 residue 2), carried forward rather than hidden:
# the credential check can only catch a WRONG credential-variable prefix,
# never prose that documents the credential-resolution story without ever
# spelling out a `*TOKEN_<placeholder>`-shaped variable at all (a rewrite
# that drops the pattern entirely contributes zero comparisons — same as
# an absent fact, not a failure).
#
# The checked side moved here from the manifest to the shipped skill
# content (P6 residue, fix to a mis-scoped first cut of this gate): the
# canonical sentence this check ORIGINALLY triggered on — "a2a resolves a
# GitHub credential locally" — lives only on docs/install-contract.md, a
# page that is itself a reading surface (§8, stripped from every public
# checkout) and never ships inside a plugin. Two of the three host schemas
# this gate reads (Antigravity's `{name, description}`-only schema; Agent
# Plugins v1's fixed field list) have NO credential field to carry that
# sentence into, and Claude Code's `userConfig` substitutes as
# `${user_config.KEY}` — a key that would need the SPACE ID, which does not
# exist at plugin-install time. A hollow prompt for a value nothing
# consumes is worse than none. The guidance instead reaches users through
# the skill tree every host already scans — skill/a2ahub/troubleshooting.md
# names it today — so THAT is the checked side now.
#
# The trigger is the SHAPE `[A-Za-z0-9_]*TOKEN_<[A-Za-z0-9_]+>`, not one
# fixed placeholder spelling: the shipped doc uses both
# "A2A_TOKEN_<SPACE_ID>" and "A2A_TOKEN_<SPACE>" for the same variable, and
# an earlier draft of this fix anchored the trigger to the "<SPACE_ID>"
# spelling alone — a rename to the "<SPACE>" spelling would then have
# dropped comparisons to zero and silently downgraded a real FAIL to
# UNMEASURED, the vacuous-tooth defect in a new costume. Every match is
# compared WHOLE: the text up to and including the final "TOKEN_" is
# extracted from the SAME matched token and compared to the canonical
# prefix — never "is the correct string present somewhere in the file",
# which would pass on a file that merely mentions the right name near a
# wrong one. Extracting and comparing the one matched token is what keeps
# this non-vacuous without needing two hand-picked, independently-fragile
# substrings.
#
# Usage: bash scripts/check-plugin-manifests.sh            # check the real tree
#        bash scripts/check-plugin-manifests.sh --teeth    # self-test on fixtures

# lane-inputs:
#   plugins/**
#   .claude-plugin/**
#   integrations/dsh/**
#   server.json
#   .github/workflows/publish-mcp.yml
#   skill/**
# skill/** is claimed on purpose, unlike the four canonical readers below:
# the skill-tree-directory and credential-sentence CHECKED sides read the
# shipped skill content, and on the real tree that content is reached
# through plugins/a2ahub/skills/a2ahub's symlink into skill/a2ahub — a
# git path the plugins/** glob does not cover. An edit to, say, the
# credential sentence in skill/a2ahub/troubleshooting.md changes this
# gate's verdict and must select it; without this line that edit would be
# invisible to `make lane` the same way the two facts this whole brief
# exists to fix were invisible to the ORIGINAL manifest-only checked side.
# lane-reads-opaque: `source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"`
#   below self-locates scripts/lib/gate-lib.sh from this script's own path,
#   so the classifier cannot resolve the $(dirname ...) substitution to a
#   literal. The canonical-side reads (internal/space/credential.go,
#   skill/embed.go, README.md, the binary via __catalog) are NOT declared
#   here on purpose: they anchor to $GATE_ROOT — the real repository,
#   unconditionally — never to a diff's changed-file set, so a change to any
#   of them cannot be "selected" by a manifest-only diff and does not belong
#   in this glob list; a change to one of those four files is exactly what
#   `logic-e2e`'s `**/*.go` (Go sources) or the ceiling's other members
#   (README.md has no dedicated gate; it is read here and by nothing else)
#   already reach.
set -uo pipefail

# shellcheck source=scripts/lib/gate-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/gate-lib.sh"

SCRIPT_ABS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"

# ── Canonical side — always $GATE_ROOT, never PLUGIN_MANIFESTS_ROOT ────────

# canonical_catalog_text: the SAME projection skill/a2ahub/reference/
# commands.md is generated from (`a2a __catalog`). Borrowed verbatim from
# check-skill-citations.sh's catalog_text(): the outer verification runner
# supplies the shared binary via $A2A_VERIFY_BINARY; direct invocation falls
# back to `go run` from $GATE_ROOT. A `go run` failure fails CLOSED (empty
# string, no rows) rather than silently greening on an empty catalog.
canonical_catalog_text() {
  if [ -n "${A2A_VERIFY_BINARY:-}" ]; then
    if [ ! -x "$A2A_VERIFY_BINARY" ]; then
      echo "A2A_VERIFY_BINARY is not executable: $A2A_VERIFY_BINARY"
      return 1
    fi
    "$A2A_VERIFY_BINARY" __catalog 2>&1
    return
  fi
  ( cd "$GATE_ROOT" && GOWORK=off go run ./cmd/a2a __catalog ) 2>&1
}

# canonical_launch_line: "a2a mcp" — the binary name (this repository's own
# module/binary, a fixed literal the way check-skill-citations.sh hardcodes
# "a2a " in its own citation regex) plus the "mcp" row's presence as a whole
# catalog command name. `a2a mcp` is documented as taking NO arguments
# (install-contract.md §1, "stdio, no arguments"), so the canonical line is
# exactly two tokens.
#
# Prints the line on success; prints nothing and returns 1 if the catalog
# could not be read or does not carry the "mcp" row — the caller turns that
# into gate_unmeasured, never gate_fail, per gate-lib's own rule for a
# shell-out that failed to run rather than a finding.
canonical_launch_line() {
  local catalog names
  catalog="$(canonical_catalog_text)"
  if [ -z "$catalog" ] || ! printf '%s' "$catalog" | grep -q '^## Commands'; then
    return 1
  fi
  names="$(printf '%s\n' "$catalog" | awk '
    /^## Commands/ { f=1; next }
    /^## MCP tools/ { f=0 }
    f && /^- `/ {
      line=$0
      sub(/^- `/, "", line)
      sub(/`.*$/, "", line)
      print line
    }
  ')"
  if ! printf '%s\n' "$names" | grep -qxF "mcp"; then
    return 1
  fi
  printf '%s\n' "a2a mcp"
}

# canonical_credential_prefix: the "A2A_TOKEN_" literal EXTRACTED from
# internal/space/credential.go's own CredentialEnvVar body, never
# hand-typed. Scoped to the function's own lines (not a bare grep of the
# whole file) so a same-looking literal elsewhere in the file cannot be
# picked up by accident.
canonical_credential_prefix() {
  local credfile="$GATE_ROOT/internal/space/credential.go"
  [ -f "$credfile" ] || return 1
  local body prefix
  body="$(awk '/^func CredentialEnvVar\(/{flag=1} flag{print} /^}/{if(flag){exit}}' "$credfile")"
  [ -n "$body" ] || return 1
  prefix="$(printf '%s\n' "$body" | grep -oE 'return "[A-Za-z0-9_]+" \+' | head -1 | sed -E 's/^return "//; s/" \+$//')"
  [ -n "$prefix" ] || return 1
  printf '%s\n' "$prefix"
}

# canonical_skill_root: the embed root skill/embed.go declares
# (`//go:embed all:a2ahub` → "a2ahub").
canonical_skill_root() {
  local embedfile="$GATE_ROOT/skill/embed.go"
  [ -f "$embedfile" ] || return 1
  local root
  root="$(grep -oE '//go:embed all:[A-Za-z0-9_.-]+' "$embedfile" | head -1 | sed -E 's#//go:embed all:##')"
  [ -n "$root" ] || return 1
  printf '%s\n' "$root"
}

# canonical_description: README.md's opening bold sentence, unwrapped from
# its markdown line wrap and joined on a single space — never hand-copied
# (the 91-char literal install-contract.md quotes is exactly the
# hand-maintained-copy defect check_contract_carried_set.sh's header warns
# against; this reads the bytes instead).
canonical_description() {
  local readme="$GATE_ROOT/README.md"
  [ -f "$readme" ] || return 1
  local text
  text="$(awk '
    BEGIN { collecting = 0; text = "" }
    {
      line = $0
      if (!collecting) {
        idx = index(line, "**")
        if (idx == 0) next
        rest = substr(line, idx + 2)
        collecting = 1
      } else {
        rest = line
      }
      idx2 = index(rest, "**")
      if (idx2 > 0) {
        text = text (text == "" ? "" : " ") substr(rest, 1, idx2 - 1)
        print text
        exit
      } else {
        text = text (text == "" ? "" : " ") rest
      }
    }
  ' "$readme" | tr -s ' ')"
  [ -n "$text" ] || return 1
  printf '%s\n' "$text"
}

# ── Checked side — reads $PLUGIN_MANIFESTS_ROOT (default $GATE_ROOT), the
#    variable the teeth repoint at a mutated copy ────────────────────────

# find_manifests: every JSON manifest candidate under scan_root matching
# this gate's own lane-inputs globs (plugins/**, .claude-plugin/**,
# integrations/dsh/**, server.json) plus a root-level plugin.json (Codex's
# fallback location per install-contract.md's Hosts table). `find` rather
# than a hand-rolled walk keeps this in step with an arbitrary P4 layout —
# any JSON file under the claimed roots is a candidate; one that carries
# none of the four facts contributes zero comparisons rather than failing.
find_manifests() { # $1 = scan_root
  local scan_root="$1"
  {
    [ -f "$scan_root/server.json" ] && printf '%s\n' "$scan_root/server.json"
    [ -f "$scan_root/plugin.json" ] && printf '%s\n' "$scan_root/plugin.json"
    find "$scan_root/plugins" -type f -name '*.json' 2>/dev/null
    find "$scan_root/.claude-plugin" -type f -name '*.json' 2>/dev/null
    find "$scan_root/integrations/dsh" -type f \( -name '*.json' -o -name '*.yml' -o -name '*.yaml' \) 2>/dev/null
  } | sort -u
}

# manifest_readable: JSON manifests are decoded with jq (established
# precedent — check-notify-secrets.sh and others already depend on it).
# `integrations/dsh/**` may carry YAML (cordis.patch.yml per spec 06's
# reasoning for including that glob); those are treated as raw text below,
# same as the launch-line/credential/description text scans, since jq has
# no YAML mode here and none of this gate's facts require structured YAML
# access.
manifest_is_json() { # $1 = path
  case "$1" in
    *.json) return 0 ;;
    *) return 1 ;;
  esac
}

# find_plugin_roots: the directory each `.claude-plugin/plugin.json`
# describes — its PARENT (`plugins/a2ahub/.claude-plugin/plugin.json` →
# `plugins/a2ahub`; a manifest sitting at $scan_root's own top level, the
# shape the teeth fixtures use, roots at $scan_root itself).
#
# Deliberately narrower than find_manifests: this repo's OTHER manifest
# sources have NO skills/ convention at all, and treating them as plugin
# roots is a real false-red this gate hit once already (found via a
# concurrently-landed `server.json` and `integrations/dsh/package.json`
# mid-session) — `server.json` is an MCP REGISTRY manifest describing
# downloadable `.mcpb` packages, not a Claude Code plugin; dsh's
# `package.json` is an npm-shaped bundle descriptor whose skill-equivalent
# guidance is transcribed inline into `cordis.patch.yml` (a YAML comment,
# never a `skills/` directory) — dsh's own host loads that patch file
# directly, no directory scan involved. Matching the LITERAL filename
# `.claude-plugin/plugin.json` — Claude Code's own specific per-plugin
# format, the only host this gate has documented evidence for ("the
# default skills/ directory is always scanned") — already excludes both,
# and also excludes a catalogue like this repo's own
# `.claude-plugin/marketplace.json` (a different filename) with no
# separate content-shape check needed. A root-level, `.claude-plugin`-less
# `plugin.json` (Agent Plugins v1's own format, or Codex's fallback
# location) is EXCLUDED on the same evidentiary ground: this gate has no
# documented skills-scanning behaviour for either host to check against.
#
# Emits ZERO OR MORE roots, not deduplicated — callers pipe through
# `sort -u`.
find_plugin_roots() { # $1 = scan_root
  local scan_root="$1" manifest
  {
    find "$scan_root/plugins" -type f -path '*/.claude-plugin/plugin.json' 2>/dev/null
    [ -f "$scan_root/.claude-plugin/plugin.json" ] && printf '%s\n' "$scan_root/.claude-plugin/plugin.json"
  } | while IFS= read -r manifest; do
    [ -n "$manifest" ] || continue
    dirname "$(dirname "$manifest")"
  done
}

# check_launch_line: for every JSON manifest, every object anywhere in the
# document carrying a "command" key is a launch-line declaration. Combine
# command + args (if an array) into a single space-joined line and compare
# to canonical_launch_line's "a2a mcp". A manifest with no such object
# declares no launch line and contributes nothing to THAT manifest (not a
# failure — the skill-only or description-only manifest shapes the install
# contract documents, e.g. Antigravity's name+description schema). But if
# the WHOLE manifest SET yields zero launch-line comparisons, that is
# UNMEASURED, not a silent pass: the install contract says every manifest
# restates this fact, so a set that names none of them is a fact this run
# could not measure, same shape as US-3's "an empty manifest set is
# UNMEASURED" one level down — spec 06 never carves out "found nothing to
# compare" as a legitimate green.
check_launch_line() { # $1 = scan_root, $2 = canonical line
  local scan_root="$1" canonical="$2" manifest comparisons=0
  while IFS= read -r manifest; do
    [ -n "$manifest" ] || continue
    manifest_is_json "$manifest" || continue
    local doc
    doc="$(jq -c '[.. | objects | select(has("command"))]' "$manifest" 2>/dev/null)"
    if [ -z "$doc" ] || [ "$doc" = "null" ]; then
      if ! jq -e . "$manifest" >/dev/null 2>&1; then
        gate_unmeasured "$manifest: not parseable as JSON"
      fi
      continue
    fi
    local count i
    count="$(printf '%s' "$doc" | jq 'length')"
    for ((i = 0; i < count; i++)); do
      local line
      line="$(printf '%s' "$doc" | jq -r --argjson i "$i" \
        '.[$i] as $o | ([$o.command] + ($o.args // [] | map(tostring))) | join(" ")')"
      comparisons=$((comparisons + 1))
      if [ "$line" != "$canonical" ]; then
        gate_fail "$manifest: launch line \"$line\" disagrees with the binary's own \"$canonical\" (a2a __catalog)"
      fi
    done
  done < <(find_manifests "$scan_root")
  if [ "$comparisons" -eq 0 ]; then
    gate_unmeasured "no manifest under $scan_root declares a launch line (\"command\"/\"args\") to compare against the binary's own \"$canonical\""
  fi
}

# check_skill_path: an ADDITIONAL, non-authoritative checked side — see
# check_skill_tree_directory below for the one that decides UNMEASURED.
# Any key whose name contains "skill" (case-insensitive) anywhere in a
# JSON manifest, whose value is a string, must name a path containing the
# canonical embed root as a whole path COMPONENT — not a substring of an
# unrelated longer name (e.g. "a2ahub-legacy" must not match "a2ahub"). A
# manifest set that declares no such key contributes zero comparisons here
# and that is simply "nothing extra to check", not UNMEASURED: no host
# schema this gate reads REQUIRES a skill-path key (Claude Code's own docs
# say the skills/ directory is always scanned with no manifest entry at
# all), so its absence is not a fact this run failed to measure.
check_skill_path() { # $1 = scan_root, $2 = canonical root
  local scan_root="$1" canonical="$2" manifest
  while IFS= read -r manifest; do
    [ -n "$manifest" ] || continue
    manifest_is_json "$manifest" || continue
    local doc
    doc="$(jq -c '[.. | objects | to_entries[]? | select(.key | test("skill";"i")) | select(.value | type == "string")]' "$manifest" 2>/dev/null)"
    [ -n "$doc" ] && [ "$doc" != "null" ] && [ "$doc" != "[]" ] || continue
    local count i
    count="$(printf '%s' "$doc" | jq 'length')"
    for ((i = 0; i < count; i++)); do
      local key value
      key="$(printf '%s' "$doc" | jq -r ".[$i].key")"
      value="$(printf '%s' "$doc" | jq -r ".[$i].value")"
      if ! printf '%s\n' "$value" | tr '/' '\n' | grep -qxF "$canonical"; then
        gate_fail "$manifest: skill-tree declaration \"$key\": \"$value\" does not carry \"$canonical\" (skill/embed.go's own embed root) as a path component"
      fi
    done
  done < <(find_manifests "$scan_root")
}

# check_skill_tree_directory: the AUTHORITATIVE skill-tree check — every
# host discovers skills by SCANNING A DIRECTORY, not by reading a manifest
# key (Claude Code docs: "the default skills/ directory is always
# scanned"), so the checked side is the directory itself: each plugin
# root find_plugin_roots discovers must carry a `skills/<canonical>`
# entry directly underneath it, and that entry must RESOLVE. A missing
# entry and a DANGLING symlink are both real, distinct failure modes —
# the embed root can be renamed in skill/embed.go while a plugin's
# symlink still points at the old name, shipping a plugin whose skill
# tree silently vanishes at install time — so both are checked and given
# their own message. Same zero-comparisons-across-the-whole-set rule as
# the other facts: no discovered plugin root at all is UNMEASURED, not a
# silent pass. SCOPE: this covers Claude-Code-format plugins only
# (find_plugin_roots' own header explains why) — the only host this gate
# has documented skills-scanning behaviour for; a manifest source with no
# such documented convention is not held to this requirement.
check_skill_tree_directory() { # $1 = scan_root, $2 = canonical root
  local scan_root="$1" canonical="$2" root comparisons=0
  while IFS= read -r root; do
    [ -n "$root" ] || continue
    comparisons=$((comparisons + 1))
    local entry="$root/skills/$canonical"
    if [ -L "$entry" ] && [ ! -e "$entry" ]; then
      gate_fail "$root: skills/$canonical is a symlink that does not resolve (dangling → $(readlink "$entry")) — the embed root skill/embed.go declares must exist where it points"
    elif [ ! -e "$entry" ]; then
      gate_fail "$root: no skills/$canonical entry directly under the plugin root — every host scans skills/ for a directory by this name, skill/embed.go's own embed root"
    elif [ ! -d "$entry" ]; then
      gate_fail "$root: skills/$canonical exists but is not a directory"
    fi
  done < <(find_plugin_roots "$scan_root" | sort -u)
  if [ "$comparisons" -eq 0 ]; then
    gate_unmeasured "no plugin root under $scan_root carries a skills/ entry to compare against skill/embed.go's own \"$canonical\" embed root"
  fi
}

# check_credential_sentence: the checked side is the SHIPPED SKILL
# CONTENT — every file reachable under each discovered plugin root's own
# skills/ tree (see the KNOWN LIMIT header comment above for why the
# manifest itself cannot carry this fact). The trigger is the SHAPE of a
# credential variable reference, `[A-Za-z0-9_]*TOKEN_<[A-Za-z0-9_]+>` —
# deliberately not anchored to one placeholder spelling, since the shipped
# doc uses both "A2A_TOKEN_<SPACE_ID>" and "A2A_TOKEN_<SPACE>" for the
# same variable — and the comparison extracts the SAME matched token's
# prefix (everything up to and including the final "TOKEN_") and checks
# it, whole, against internal/space.CredentialEnvVar's derived prefix.
# Extracting and comparing the ONE matched occurrence — never "is the
# correct string present somewhere in the file" — is what keeps a renamed
# variable from passing just because the correct name still appears
# elsewhere; the "present but altered" fixture (spec 06 §6) is exactly a
# file that keeps talking about the credential while getting the name
# wrong. Same zero-comparisons-across-the-whole-set rule as the other
# facts: no file under any plugin's skills/ tree naming the shape at all
# is UNMEASURED for the credential sentence, not a silent pass.
check_credential_sentence() { # $1 = scan_root, $2 = canonical prefix
  local scan_root="$1" prefix="$2" root file comparisons=0
  while IFS= read -r root; do
    [ -n "$root" ] || continue
    local skills_dir="$root/skills"
    [ -d "$skills_dir" ] || continue
    while IFS= read -r file; do
      [ -n "$file" ] || continue
      [ -f "$file" ] || continue
      local matches
      matches="$(grep -oE '[A-Za-z0-9_]*TOKEN_<[A-Za-z0-9_]+>' "$file" 2>/dev/null || true)"
      [ -n "$matches" ] || continue
      while IFS= read -r token; do
        [ -n "$token" ] || continue
        comparisons=$((comparisons + 1))
        local token_prefix="${token%%<*}"
        if [ "$token_prefix" != "$prefix" ]; then
          gate_fail "$file: credential sentence present but altered — names \"$token\" whose prefix \"$token_prefix\" disagrees with internal/space.CredentialEnvVar's derived \"$prefix\""
        fi
      done <<< "$matches"
    done < <(find -L "$skills_dir" -type f 2>/dev/null)
  done < <(find_plugin_roots "$scan_root" | sort -u)
  if [ "$comparisons" -eq 0 ]; then
    gate_unmeasured "no file under any plugin's skills/ tree under $scan_root names a credential variable (a *_TOKEN_<...>-shaped token) to compare against internal/space.CredentialEnvVar's derived \"$prefix\" prefix"
  fi
}

# check_description: any JSON string value equal (after trim) to the
# canonical description is fine; this only flags a manifest field that
# NAMES itself as the one-line description (key "description", any
# nesting depth) and disagrees. Same zero-comparisons-across-the-whole-set
# rule as check_launch_line: a manifest set carrying no "description" key
# anywhere is UNMEASURED for this fact, not a silent pass.
#
# KNOWN LIMIT, not fixed here: this fires on EVERY "description" key at
# any depth, so a manifest carrying more than one — e.g. a marketplace
# catalogue's own top-level blurb plus a per-plugin entry's — is compared
# to the SAME canonical sentence for both. If a future manifest shape gives
# those two fields legitimately different prose (a catalogue-level blurb
# distinct from the product's one-line description), this reds on a file
# that is not wrong; narrowing which "description" key is THE one-line
# description is future work, not attempted here.
check_description() { # $1 = scan_root, $2 = canonical description
  local scan_root="$1" canonical="$2" manifest comparisons=0
  while IFS= read -r manifest; do
    [ -n "$manifest" ] || continue
    manifest_is_json "$manifest" || continue
    local values
    values="$(jq -r '[.. | objects | .description? // empty | select(type == "string")] | .[]' "$manifest" 2>/dev/null)"
    [ -n "$values" ] || continue
    while IFS= read -r value; do
      [ -n "$value" ] || continue
      comparisons=$((comparisons + 1))
      if [ "$value" != "$canonical" ]; then
        gate_fail "$manifest: description \"$value\" disagrees with README.md's own opening sentence \"$canonical\""
      fi
    done <<< "$values"
  done < <(find_manifests "$scan_root")
  if [ "$comparisons" -eq 0 ]; then
    gate_unmeasured "no manifest under $scan_root declares a \"description\" to compare against README.md's own opening sentence \"$canonical\""
  fi
}

# check_publish_workflow: spec 06 §T6's fourth row, an EXISTENCE assertion,
# not a comparison — the values it would diff are inert placeholders at
# rest (spec 05 §T2). Absent workflow file: simply not checked (P5 has not
# landed). Present: the substitution of version/identifier/fileSha256 must
# appear BEFORE the `mcp-publisher publish` call, not merely co-present
# anywhere in the file — a step ORDER guard, not a bag-of-words guard.
check_publish_workflow() { # $1 = scan_root
  local scan_root="$1"
  local wf="$scan_root/.github/workflows/publish-mcp.yml"
  [ -f "$wf" ] || return 0

  local publish_line
  publish_line="$(grep -n 'mcp-publisher publish' "$wf" | head -1 | cut -d: -f1)"
  if [ -z "$publish_line" ]; then
    gate_fail "$wf: present but names no \`mcp-publisher publish\` step to guard"
    return
  fi

  local before missing=""
  before="$(head -n "$((publish_line - 1))" "$wf")"
  for token in version identifier fileSha256; do
    if ! printf '%s\n' "$before" | grep -qF "$token"; then
      missing="$missing $token"
    fi
  done
  if [ -n "$missing" ]; then
    gate_fail "$wf: no step before \`mcp-publisher publish\` (line $publish_line) rewrites:$missing"
  fi
}

# ── Entry points ─────────────────────────────────────────────────────────

run_check() { # $1 = scan_root (default: $PLUGIN_MANIFESTS_ROOT or $GATE_ROOT)
  local scan_root="${1:-${PLUGIN_MANIFESTS_ROOT:-$GATE_ROOT}}"
  [ -d "$scan_root" ] || { gate_unmeasured "scan root $scan_root does not exist"; gate_summary "check-plugin-manifests"; exit $?; }

  local manifests manifest_count
  manifests="$(find_manifests "$scan_root")"
  manifest_count="$(printf '%s\n' "$manifests" | grep -c . || true)"
  if [ "$manifest_count" -eq 0 ]; then
    gate_unmeasured "no plugin manifest found under $scan_root (plugins/**, .claude-plugin/**, integrations/dsh/**, server.json, plugin.json) — an empty manifest set judges nothing"
    gate_summary "check-plugin-manifests"
    exit $?
  fi

  local launch_line credential_prefix skill_root description
  if ! launch_line="$(canonical_launch_line)"; then
    gate_unmeasured "could not read the binary's own launch line via a2a __catalog"
  fi
  if ! credential_prefix="$(canonical_credential_prefix)"; then
    gate_unmeasured "could not read internal/space/credential.go's CredentialEnvVar prefix"
  fi
  if ! skill_root="$(canonical_skill_root)"; then
    gate_unmeasured "could not read skill/embed.go's go:embed root"
  fi
  if ! description="$(canonical_description)"; then
    gate_unmeasured "could not read README.md's opening sentence"
  fi

  [ -n "${launch_line:-}" ] && check_launch_line "$scan_root" "$launch_line"
  [ -n "${credential_prefix:-}" ] && check_credential_sentence "$scan_root" "$credential_prefix"
  if [ -n "${skill_root:-}" ]; then
    check_skill_tree_directory "$scan_root" "$skill_root"
    check_skill_path "$scan_root" "$skill_root"
  fi
  [ -n "${description:-}" ] && check_description "$scan_root" "$description"
  check_publish_workflow "$scan_root"

  gate_summary "check-plugin-manifests"
  exit $?
}

# ── Teeth ────────────────────────────────────────────────────────────────
run_teeth() {
  local tmp; tmp="$(mktemp -d)" || { echo "check-plugin-manifests --teeth: mktemp failed"; exit 1; }
  trap 'rm -rf "$tmp"' EXIT
  local failures=0

  bad() { echo "check-plugin-manifests --teeth: FAIL — $1"; failures=$((failures + 1)); }

  # NOTE: deliberately no assertion against the real tree's OWN current
  # manifest set here. This repository is shared with concurrent sibling
  # work (this very gate was written before P4's manifests landed, and they
  # landed mid-session) — asserting a fixed verdict against ambient,
  # externally-mutable state is a race, not a test. T1 below covers the
  # "empty manifest set is UNMEASURED" requirement (spec 06 §6) against a
  # scan root this suite fully controls instead.
  local out rc

  # T1 — an explicitly empty scan root: UNMEASURED.
  mkdir -p "$tmp/empty"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/empty" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  if [ "$rc" -ne "$GATE_EXIT_UNMEASURED" ] || ! printf '%s' "$out" | grep -q "UNMEASURED"; then
    bad "T1 (empty manifest set): wanted UNMEASURED/exit $GATE_EXIT_UNMEASURED, got exit $rc: $out"
  fi

  # Shared fixture builder — a scratch tree with one JSON manifest carrying
  # the launch line, description and (additional) skill-path key, PLUS a
  # skills/a2ahub/ directory carrying the actual shipped skill content the
  # credential-sentence and skill-tree-directory checks now read — so the
  # control run is green end to end. The credential doc mirrors the real
  # skill/a2ahub/troubleshooting.md shape: BOTH placeholder spellings the
  # shipped file actually uses ("<SPACE_ID>" and "<SPACE>") for the same
  # variable, so a fixture that only ever exercised one spelling could not
  # catch a regex narrowed to the other.
  write_control() { # $1 = root
    mkdir -p "$1/.claude-plugin" "$1/skills/a2ahub"
    cat > "$1/.claude-plugin/plugin.json" <<'JSON'
{
  "name": "a2ahub",
  "description": "Reliable handoffs between autonomous agents, using a Git repository both sides can inspect.",
  "mcpServers": {
    "a2ahub": {
      "command": "a2a",
      "args": ["mcp"]
    }
  },
  "skillsPath": "skills/a2ahub"
}
JSON
    cat > "$1/skills/a2ahub/troubleshooting.md" <<'MD'
credentials: the explicit `A2A_TOKEN_<SPACE_ID>` override first, the machine-config reference second.
space access: it says so and names the `A2A_TOKEN_<SPACE>` variable and the machine config file.
MD
  }

  # T2 — control: every fact correct → green.
  mkdir -p "$tmp/control"
  write_control "$tmp/control"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/control" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  [ "$rc" -eq 0 ] || bad "T2 (control, all facts correct): wanted exit 0, got $rc: $out"

  # T3 — launch-line drift: a verb the binary does not have.
  mkdir -p "$tmp/badverb"
  write_control "$tmp/badverb"
  sed -i.bak 's/"command": "a2a"/"command": "a2a-frobnicate"/' "$tmp/badverb/.claude-plugin/plugin.json"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/badverb" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  { [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "launch line"; } || bad "T3 (launch-line drift): wanted a red naming the launch line, got exit $rc: $out"

  # T4 — an existing verb with different args ("a2a mcp --http" instead of
  # the documented no-argument stdio form) must ALSO red — the spec calls
  # this out explicitly as a distinct edge case from an invented verb.
  mkdir -p "$tmp/badargs"
  write_control "$tmp/badargs"
  sed -i.bak 's/"args": \["mcp"\]/"args": ["mcp", "--http"]/' "$tmp/badargs/.claude-plugin/plugin.json"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/badargs" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  { [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "launch line"; } || bad "T4 (real verb, wrong args): wanted a red naming the launch line, got exit $rc: $out"

  # T5 — credential sentence PRESENT BUT ALTERED, inside the SHIPPED SKILL
  # CONTENT: the surrounding prose stays, the variable's prefix changes.
  # Deliberately mutates the "<SPACE>" spelling, not "<SPACE_ID>" — the
  # regex trigger is the credential-variable SHAPE, not one fixed
  # placeholder, so this is the mutation that would have gone silently
  # UNMEASURED (not FAILED) under a trigger anchored to the other spelling
  # alone, per the KNOWN LIMIT header note.
  mkdir -p "$tmp/badcred"
  write_control "$tmp/badcred"
  sed -i.bak 's/A2A_TOKEN_<SPACE>/A2A_SPACE_TOKEN_<SPACE>/' "$tmp/badcred/skills/a2ahub/troubleshooting.md"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/badcred" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  { [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "credential sentence present but altered"; } || bad "T5 (credential sentence present-but-altered, skill content, <SPACE> spelling): wanted a red naming the altered sentence, got exit $rc: $out"

  # T6 — a SECOND, genuinely separate plugin root (its own manifest under
  # plugins/minimal/, no .claude-plugin nesting) that carries its OWN
  # resolvable skills/a2ahub directory (satisfying the MANDATORY directory
  # check — every discovered plugin root must carry one, proven by T7b
  # below when it doesn't) but whose content never mentions the credential
  # variable, alongside the control's skills/a2ahub content which does,
  # must NOT be confused with T5: a plugin whose skill tree simply has
  # nothing to say about credentials contributes zero comparisons for
  # THAT root, not a wrong one, as long as the SET as a whole still
  # measures the fact somewhere.
  mkdir -p "$tmp/nocred"
  write_control "$tmp/nocred"
  mkdir -p "$tmp/nocred/plugins/minimal/skills/a2ahub"
  cat > "$tmp/nocred/plugins/minimal/plugin.json" <<'JSON'
{ "name": "a2ahub-minimal" }
JSON
  cat > "$tmp/nocred/plugins/minimal/skills/a2ahub/notes.md" <<'MD'
This plugin's own skill tree has no credential guidance of its own.
MD
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/nocred" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  [ "$rc" -eq 0 ] || bad "T6 (credential sentence absent from one plugin's skill content, present on another): wanted exit 0, got $rc: $out"

  # T6b — the WHOLE SET carrying zero discovered PLUGIN ROOTS (only a
  # catalogue manifest — this repo's own .claude-plugin/marketplace.json
  # shape, filename "marketplace.json" — with no per-plugin
  # ".claude-plugin/plugin.json" reachable underneath it) is UNMEASURED
  # for BOTH the skill-tree-directory and credential-sentence facts:
  # find_plugin_roots matches only the LITERAL filename
  # ".claude-plugin/plugin.json" (header comment above find_plugin_roots),
  # so a catalogue is excluded by filename alone, no content inspection
  # needed — a set with only a catalogue and nothing else measures
  # neither fact, not a silent pass, and distinct from T1's "literally no
  # manifest file at all" (this scan root DOES carry a manifest; it just
  # names no Claude-Code-format plugin).
  mkdir -p "$tmp/catalogonly/.claude-plugin"
  cat > "$tmp/catalogonly/.claude-plugin/marketplace.json" <<'JSON'
{
  "name": "a2ahub",
  "plugins": [
    { "name": "a2ahub", "source": "./plugins/a2ahub" }
  ]
}
JSON
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/catalogonly" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  { [ "$rc" -eq "$GATE_EXIT_UNMEASURED" ] \
      && printf '%s' "$out" | grep -q "names a credential variable" \
      && printf '%s' "$out" | grep -q "carries a skills/ entry"; } \
    || bad "T6b (catalogue-only set: no discovered plugin root measures either fact): wanted UNMEASURED naming both the credential variable and the skills/ entry, got exit $rc: $out"

  # T7 — skill-tree path drift: names a directory the embed root does not
  # carry.
  mkdir -p "$tmp/badskill"
  write_control "$tmp/badskill"
  sed -i.bak 's#"skills/a2ahub"#"skills/a2ahub-legacy"#' "$tmp/badskill/.claude-plugin/plugin.json"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/badskill" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  { [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "skill-tree declaration"; } || bad "T7 (skill-path drift, manifest key — the ADDITIONAL checked side): wanted a red naming the skill-tree declaration, got exit $rc: $out"

  # T7b — DANGLING skills symlink: the AUTHORITATIVE directory check.
  # skills/a2ahub exists as a symlink but resolves nowhere — the shape a
  # renamed skill/embed.go embed root, or a moved skill/a2ahub/, leaves
  # behind. The manifest's own skillsPath key is left untouched and
  # correct (control's own value), so this isolates the directory check
  # from the manifest-key check T7 above: only the dangling-symlink
  # message may appear.
  mkdir -p "$tmp/danglingskill"
  write_control "$tmp/danglingskill"
  rm -rf "$tmp/danglingskill/skills/a2ahub"
  ln -s "../does-not-exist" "$tmp/danglingskill/skills/a2ahub"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/danglingskill" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  { [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "dangling"; } || bad "T7b (dangling skills/a2ahub symlink): wanted a red naming the dangling symlink, got exit $rc: $out"

  # T8 — description drift.
  mkdir -p "$tmp/baddesc"
  write_control "$tmp/baddesc"
  sed -i.bak 's/"description": "[^"]*"/"description": "Handoffs between agents."/' "$tmp/baddesc/.claude-plugin/plugin.json"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/baddesc" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  { [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "description"; } || bad "T8 (description drift): wanted a red naming the description, got exit $rc: $out"

  # T9 — an unparseable manifest: UNMEASURED, never gate_fail.
  mkdir -p "$tmp/badjson/.claude-plugin"
  printf '{ this is not json' > "$tmp/badjson/.claude-plugin/plugin.json"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/badjson" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  { [ "$rc" -eq "$GATE_EXIT_UNMEASURED" ] && printf '%s' "$out" | grep -q "UNMEASURED"; } || bad "T9 (unparseable manifest): wanted UNMEASURED/exit $GATE_EXIT_UNMEASURED, got exit $rc: $out"

  # T10 — the asymmetry: point the scan root at a tree that mutates BOTH
  # the shipped skill content AND a copy of internal/space/credential.go
  # the same way. The canonical side must still reach the REAL $GATE_ROOT
  # source, so this must stay red exactly like T5 — a shared-helper
  # regression would make it silently pass because both sides would read
  # the mutated copy.
  mkdir -p "$tmp/asymmetry/internal/space"
  write_control "$tmp/asymmetry"
  sed -i.bak 's/A2A_TOKEN_<SPACE>/A2A_SPACE_TOKEN_<SPACE>/' "$tmp/asymmetry/skills/a2ahub/troubleshooting.md"
  sed 's/A2A_TOKEN_/A2A_SPACE_TOKEN_/' "$GATE_ROOT/internal/space/credential.go" > "$tmp/asymmetry/internal/space/credential.go"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/asymmetry" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  { [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "credential sentence present but altered"; } || bad "T10 (asymmetry — scan root cannot redirect the canonical side): wanted the SAME red as T5 even with a mutated internal/space/credential.go sitting inside the scan root, got exit $rc: $out"

  # T11 — publish-mcp.yml absent: simply not checked, stays whatever the
  # rest of the fixture would already be (control fixture here → green).
  mkdir -p "$tmp/noworkflow"
  write_control "$tmp/noworkflow"
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/noworkflow" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  [ "$rc" -eq 0 ] || bad "T11 (publish-mcp.yml absent): wanted exit 0 (not checked), got $rc: $out"

  # T12 — publish-mcp.yml present but missing the substitution step before
  # the publish call → red.
  mkdir -p "$tmp/badworkflow/.github/workflows"
  write_control "$tmp/badworkflow"
  cat > "$tmp/badworkflow/.github/workflows/publish-mcp.yml" <<'YAML'
name: publish-mcp
on:
  push:
    tags: ["v*"]
jobs:
  publish:
    steps:
      - run: mcp-publisher publish
YAML
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/badworkflow" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  { [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "publish-mcp.yml"; } || bad "T12 (publish-mcp.yml missing substitution step): wanted a red naming publish-mcp.yml, got exit $rc: $out"

  # T13 — publish-mcp.yml present WITH the substitution step correctly
  # placed before the publish call → green.
  mkdir -p "$tmp/goodworkflow/.github/workflows"
  write_control "$tmp/goodworkflow"
  cat > "$tmp/goodworkflow/.github/workflows/publish-mcp.yml" <<'YAML'
name: publish-mcp
on:
  push:
    tags: ["v*"]
jobs:
  publish:
    steps:
      - name: rewrite version, identifier and fileSha256
        run: ./scripts/rewrite-server-json.sh "$GITHUB_REF_NAME"
      - run: mcp-publisher publish
YAML
  out="$(PLUGIN_MANIFESTS_ROOT="$tmp/goodworkflow" bash "$SCRIPT_ABS" check 2>&1)"; rc=$?
  [ "$rc" -eq 0 ] || bad "T13 (publish-mcp.yml with substitution before publish): wanted exit 0, got $rc: $out"

  if [ "$failures" -gt 0 ]; then
    echo "check-plugin-manifests --teeth: $failures assertion(s) failed"
    exit 1
  fi
  echo "✓ check-plugin-manifests --teeth: T1 (empty set UNMEASURED) T2 (control green) T3 (invented verb) T4 (real verb, wrong args) T5 (credential present-but-altered in shipped skill content, <SPACE> spelling) T6 (credential absent from one plugin root, present on another, stays green) T6b (catalogue-only set: zero plugin roots is UNMEASURED for both credential and skill-tree-directory) T7 (skill-path drift, manifest key, additional) T7b (dangling skills symlink, authoritative directory check) T8 (description drift) T9 (unparseable JSON UNMEASURED) T10 (asymmetry: scan-root mutation of credential.go cannot redirect the canonical side) T11 (workflow absent, not checked) T12 (workflow missing substitution) T13 (workflow correct) — all as wanted"
  exit 0
}

case "${1:-check}" in
  --teeth) run_teeth ;;
  check) run_check ;;
  *) echo "check-plugin-manifests: unknown mode '${1}' (check | --teeth) — fail-closed."; exit 2 ;;
esac
