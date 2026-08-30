#!/usr/bin/env bash
# Teeth for check-plugin-manifests.sh, modelled on
# scripts/tests/check_contract_carried_set_test.sh — copy/mutate a fixture
# tree, point PLUGIN_MANIFESTS_ROOT (the CHECKED side's own scan-root
# variable) at it, and assert the gate reds naming the drift.
#
# ONE case here decides whether half the gate is real: "asymmetry" below.
# The gate's four canonical facts (launch line, credential prefix, skill
# root, description) must be read from THIS repository's real, pristine
# $GATE_ROOT — never from whatever PLUGIN_MANIFESTS_ROOT points the CHECKED
# side at. The asymmetry case plants a MUTATED copy of
# internal/space/credential.go inside the fixture tree, right where a
# careless shared-helper refactor would read it from if the split ever
# collapsed, and still expects the SAME red a manifest-only mutation would
# produce. If a future edit gives the canonical readers a scan-root
# parameter "for symmetry" (the exact trap check_contract_carried_set.sh's
# own header names), this is the case that goes silently green while
# looking unchanged.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
GATE="$ROOT/scripts/check-plugin-manifests.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() {
  echo "plugin-manifests-test: FAIL — $*" >&2
  exit 1
}

# write_control: a scratch tree with one manifest declaring the launch
# line (command/args), the one-line description, and (an ADDITIONAL,
# non-authoritative checked side) a skillsPath key — plus a real
# skills/a2ahub/ directory carrying the shipped skill content the
# credential-sentence and skill-tree-directory checks now read directly.
# The credential doc mirrors skill/a2ahub/troubleshooting.md's own shape:
# BOTH placeholder spellings it actually uses for the same variable
# ("<SPACE_ID>" and "<SPACE>"), so a fixture exercising only one spelling
# could not catch a regex narrowed to the other.
write_control() { # $1 = root
  local root="$1"
  mkdir -p "$root/.claude-plugin" "$root/skills/a2ahub"
  cat > "$root/.claude-plugin/plugin.json" <<'JSON'
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
  cat > "$root/skills/a2ahub/troubleshooting.md" <<'MD'
credentials: the explicit `A2A_TOKEN_<SPACE_ID>` override first, the machine-config reference second.
space access: it says so and names the `A2A_TOKEN_<SPACE>` variable and the machine config file.
MD
}

expect_red() { # $1 = tree, $2 = needle, $3 = label
  local tree="$1" needle="$2" label="$3" output
  if output="$(PLUGIN_MANIFESTS_ROOT="$tree" bash "$GATE" 2>&1)"; then
    fail "$label stayed green: $output"
  fi
  if ! grep -Fq "$needle" <<<"$output"; then
    fail "$label red did not name '$needle': $output"
  fi
}

expect_green() { # $1 = tree, $2 = label
  # `output="$(cmd)"` as a plain statement, under `set -e`, exits THIS
  # SCRIPT immediately if cmd's status is non-zero — before the fail()
  # below ever runs, and silently (no ERR trap). Wrapping it as an if-
  # condition is what check_contract_carried_set_test.sh's own expect_red
  # already does, for the same reason; this helper needs the identical
  # guard because it also branches on a captured exit status.
  local tree="$1" label="$2" output
  if ! output="$(PLUGIN_MANIFESTS_ROOT="$tree" bash "$GATE" 2>&1)"; then
    fail "$label stayed red: $output"
  fi
}

expect_unmeasured() { # $1 = tree or "" for the real default root, $2 = label, $3 = optional needle
  local tree="$1" label="$2" needle="${3:-}" output rc
  if [ -n "$tree" ]; then
    if output="$(PLUGIN_MANIFESTS_ROOT="$tree" bash "$GATE" 2>&1)"; then rc=0; else rc=$?; fi
  else
    if output="$(bash "$GATE" 2>&1)"; then rc=0; else rc=$?; fi
  fi
  [ "$rc" -eq 3 ] || fail "$label did not report UNMEASURED (exit 3), got exit $rc: $output"
  grep -Fq "UNMEASURED" <<<"$output" || fail "$label missing the UNMEASURED token: $output"
  if [ -n "$needle" ]; then
    grep -Fq "$needle" <<<"$output" || fail "$label UNMEASURED did not name '$needle': $output"
  fi
}

# NOTE: deliberately no assertion against the real tree's OWN current
# manifest set. This checkout is shared with concurrent sibling work — P4's
# manifests landed under plugins/**/.claude-plugin/** mid-session, after
# this gate was written against an empty tree — so a fixed verdict against
# ambient, externally-mutable state would be a race, not a test. The
# "empty manifest set is UNMEASURED" requirement (spec 06 §6) is covered
# below against a scan root this suite fully controls instead.

# An explicitly empty scan root: UNMEASURED — an empty manifest set judges
# nothing (spec 06 §6, "unmeasured" row).
empty_root="$WORK/empty"
mkdir -p "$empty_root"
expect_unmeasured "$empty_root" "an explicitly empty manifest set"

# Control: every fact correct → green.
control="$WORK/control"
mkdir -p "$control"
write_control "$control"
expect_green "$control" "control (every fact correct)"

# Launch-line drift: an invented verb.
launch_drift="$WORK/launch-drift"
mkdir -p "$launch_drift"
write_control "$launch_drift"
sed -i.bak 's/"command": "a2a"/"command": "a2a-frobnicate"/' "$launch_drift/.claude-plugin/plugin.json"
expect_red "$launch_drift" "launch line" "launch-line drift (invented verb)"

# Launch-line drift, second shape: a REAL verb with DIFFERENT arguments.
# `a2a mcp` is documented stdio/no-arguments; a manifest that launches it
# with an extra flag is drift the gate must catch even though "mcp" itself
# is a real catalog row.
args_drift="$WORK/args-drift"
mkdir -p "$args_drift"
write_control "$args_drift"
sed -i.bak 's/"args": \["mcp"\]/"args": ["mcp", "--http"]/' "$args_drift/.claude-plugin/plugin.json"
expect_red "$args_drift" "launch line" "launch-line drift (real verb, wrong args)"

# Credential sentence PRESENT BUT ALTERED, inside the SHIPPED SKILL
# CONTENT: the surrounding prose stays, the variable's prefix changes.
# Deliberately mutates the "<SPACE>" spelling, not "<SPACE_ID>" — the
# trigger is the credential-variable SHAPE, not one fixed placeholder, so
# this is the mutation that would have gone silently UNMEASURED (not
# FAILED) under a trigger anchored to the other spelling alone. It is the
# case spec 06 §6 calls out by name as the one that makes the credential
# half real rather than decorative.
credential_altered="$WORK/credential-altered"
mkdir -p "$credential_altered"
write_control "$credential_altered"
sed -i.bak 's/A2A_TOKEN_<SPACE>/A2A_SPACE_TOKEN_<SPACE>/' "$credential_altered/skills/a2ahub/troubleshooting.md"
expect_red "$credential_altered" "credential sentence present but altered" "credential sentence present-but-altered (<SPACE> spelling)"

# Credential sentence ABSENT from ONE plugin's skill content (a second,
# genuinely separate plugin root under plugins/minimal/, carrying its OWN
# resolvable skills/a2ahub directory — so the MANDATORY directory check
# still passes for it — but content that never mentions the credential
# variable) while the control's skills/a2ahub content still carries it
# correctly: must stay green, distinct from the altered case above. A
# plugin whose skill tree has nothing to say about credentials
# contributes zero comparisons for THAT root, not a wrong one, as long as
# the SET as a whole still measures the fact somewhere.
credential_absent="$WORK/credential-absent"
mkdir -p "$credential_absent"
write_control "$credential_absent"
mkdir -p "$credential_absent/plugins/minimal/skills/a2ahub"
cat > "$credential_absent/plugins/minimal/plugin.json" <<'JSON'
{ "name": "a2ahub-minimal" }
JSON
cat > "$credential_absent/plugins/minimal/skills/a2ahub/notes.md" <<'MD'
This plugin's own skill tree has no credential guidance of its own.
MD
expect_green "$credential_absent" "credential sentence absent from one plugin's skill content, present on another"

# Credential sentence (and the skill-tree directory) absent from the
# WHOLE SET: a scan root carrying ONLY a catalogue manifest (this repo's
# own .claude-plugin/marketplace.json shape, filename "marketplace.json")
# with no per-plugin ".claude-plugin/plugin.json" reachable underneath
# it. find_plugin_roots matches only the LITERAL filename
# ".claude-plugin/plugin.json", so a catalogue is excluded by filename
# alone — zero plugin roots are discovered, UNMEASURED for BOTH facts,
# not a silent pass, and distinct from the "explicitly empty scan root"
# case above (this root DOES carry a manifest; it just names no
# Claude-Code-format plugin).
credential_absent_set="$WORK/credential-absent-set"
mkdir -p "$credential_absent_set/.claude-plugin"
cat > "$credential_absent_set/.claude-plugin/marketplace.json" <<'JSON'
{
  "name": "a2ahub",
  "plugins": [
    { "name": "a2ahub", "source": "./plugins/a2ahub" }
  ]
}
JSON
expect_unmeasured "$credential_absent_set" "catalogue-only set (zero discovered plugin roots): credential fact" "names a credential variable"
expect_unmeasured "$credential_absent_set" "catalogue-only set (zero discovered plugin roots): skill-tree-directory fact" "carries a skills/ entry"

# Skill-tree path drift, MANIFEST KEY (the ADDITIONAL, non-authoritative
# checked side) — names a directory the embed root does not carry, while
# the real skills/a2ahub directory (the AUTHORITATIVE side) is untouched
# and still resolves.
skill_drift="$WORK/skill-drift"
mkdir -p "$skill_drift"
write_control "$skill_drift"
sed -i.bak 's#"skills/a2ahub"#"skills/a2ahub-legacy"#' "$skill_drift/.claude-plugin/plugin.json"
expect_red "$skill_drift" "skill-tree declaration" "skill-path drift (manifest key)"

# DANGLING skills symlink — the AUTHORITATIVE directory check. skills/
# a2ahub exists as a symlink but resolves nowhere, the shape a renamed
# skill/embed.go embed root or a moved skill/a2ahub/ leaves behind. The
# manifest's own skillsPath key is left correct, isolating this from the
# manifest-key drift case above: only the dangling-symlink message may
# appear for this reason.
skill_dangling="$WORK/skill-dangling"
mkdir -p "$skill_dangling"
write_control "$skill_dangling"
rm -rf "$skill_dangling/skills/a2ahub"
ln -s "../does-not-exist" "$skill_dangling/skills/a2ahub"
expect_red "$skill_dangling" "dangling" "dangling skills/a2ahub symlink"

# One-line description drift.
description_drift="$WORK/description-drift"
mkdir -p "$description_drift"
write_control "$description_drift"
sed -i.bak 's/"description": "[^"]*"/"description": "Handoffs between agents."/' "$description_drift/.claude-plugin/plugin.json"
expect_red "$description_drift" "description" "description drift"

# An unparseable manifest: UNMEASURED, never a silent pass and never a
# false gate_fail (gate-lib's own rule: a shell-out/parse that could not
# run is a measurement failure, not a finding).
unparseable="$WORK/unparseable"
mkdir -p "$unparseable/.claude-plugin"
printf '{ this is not json' > "$unparseable/.claude-plugin/plugin.json"
expect_unmeasured "$unparseable" "an unparseable manifest"

# ── THE ASYMMETRY — the case that decides whether half this gate is real ──
#
# Plant a MUTATED copy of internal/space/credential.go inside the scan
# root, at the exact relative path the real gate reads its canonical
# credential prefix from — alongside the SHIPPED SKILL CONTENT mutated the
# SAME way. If the canonical reader is (or ever becomes) redirectable by
# PLUGIN_MANIFESTS_ROOT, both sides move together and this fixture goes
# green: the mutated copy would agree with itself. The gate must instead
# still reach the REAL $ROOT/internal/space/credential.go and produce the
# exact same red as the plain "credential sentence present but altered"
# case above.
asymmetry="$WORK/asymmetry"
mkdir -p "$asymmetry/internal/space"
write_control "$asymmetry"
sed -i.bak 's/A2A_TOKEN_<SPACE>/A2A_SPACE_TOKEN_<SPACE>/' "$asymmetry/skills/a2ahub/troubleshooting.md"
sed 's/A2A_TOKEN_/A2A_SPACE_TOKEN_/' "$ROOT/internal/space/credential.go" > "$asymmetry/internal/space/credential.go"
grep -Fq 'A2A_SPACE_TOKEN_' "$asymmetry/internal/space/credential.go" || fail "could not seed the asymmetry fixture's mutated credential.go copy"
expect_red "$asymmetry" "credential sentence present but altered" "the asymmetry (scan root cannot redirect the canonical side)"

# publish-mcp.yml absent: P5's workflow file does not exist yet, so it is
# simply not checked — the control fixture carries none, and stays green.
workflow_absent="$WORK/workflow-absent"
mkdir -p "$workflow_absent"
write_control "$workflow_absent"
expect_green "$workflow_absent" "publish-mcp.yml absent (not checked)"

# publish-mcp.yml present but missing the version/identifier/fileSha256
# substitution step before the publish call.
workflow_missing="$WORK/workflow-missing"
mkdir -p "$workflow_missing/.github/workflows"
write_control "$workflow_missing"
cat > "$workflow_missing/.github/workflows/publish-mcp.yml" <<'YAML'
name: publish-mcp
on:
  push:
    tags: ["v*"]
jobs:
  publish:
    steps:
      - run: mcp-publisher publish
YAML
expect_red "$workflow_missing" "publish-mcp.yml" "publish-mcp.yml missing the substitution step"

# publish-mcp.yml present WITH the substitution step correctly placed
# before the publish call → green.
workflow_present="$WORK/workflow-present"
mkdir -p "$workflow_present/.github/workflows"
write_control "$workflow_present"
cat > "$workflow_present/.github/workflows/publish-mcp.yml" <<'YAML'
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
expect_green "$workflow_present" "publish-mcp.yml with the substitution step correctly placed"

# The gate's own embedded --teeth self-test must also pass (belt and
# suspenders: the two suites cover the same drift classes through two
# different entry points — this file's copy-and-mutate shape, and the
# gate's own harness-check-registered `--teeth`).
bash "$GATE" --teeth >/dev/null 2>&1 || fail "the gate's own --teeth self-test did not pass"

echo "plugin-manifests-test: PASS — empty set UNMEASURED, control green, launch-line drift (invented verb and real-verb-wrong-args) red, credential present-but-altered (in shipped skill content, <SPACE> spelling) red, credential absent from ONE plugin's skill content but present on another stays green, a catalogue-only set (zero discovered plugin roots) is UNMEASURED for both the credential and skill-tree-directory facts, skill-path drift (manifest key) red, a dangling skills/a2ahub symlink (authoritative directory check) red, description drift red, unparseable manifest UNMEASURED, THE ASYMMETRY still reds with a mutated internal/space/credential.go sitting inside the scan root, publish-mcp.yml absent/missing-step/correct all as wanted, and the gate's own --teeth passes"
