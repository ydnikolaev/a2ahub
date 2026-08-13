#!/usr/bin/env bash
# ci-skill-drift.sh — the generated half of the a2ahub skill must stay
# byte-identical to a fresh regeneration from the built binary.
# (spec 13 T4/T5, AC-602.1 machine clause; agent-ops-2026-07 P13 K5, AC5.)
#
# Prose paths (SKILL.md / loops.md / …) are deliberately OUT: they are
# release-checklist-gated (D-015), never machine-gated. The spec's path-scoped
# trigger is intentionally widened to always-run — the regenerate is cheap and
# an always-green gate cannot silently skip.
#
# WHY THIS IS A SCRIPT NOW. It was a workflow job of its own, and it billed a
# whole runner minute for twenty-four seconds of work, on every push, in two
# repositories — one of the four sub-minute identities P13 F13 measured at 595
# equivalent minutes for 133 minutes of actual work. The billing unit is a
# whole minute PER JOB, so N cheap jobs cost N minutes however little they do.
#
# Spec 13 §3.4 allows a merge only where trigger set and permission set
# already match, and `skill-drift` is the one identity that qualifies:
# `feedback-intake` runs on `pull_request_target`, CodeQL needs
# `security-events: write`, `gitleaks` wants `fetch-depth: 0`, and
# `classify-guard` is excluded BY DECISION (A7) because its whole stated value
# is being a backstop that does not depend on `make check`.
#
# It is called as a step by whichever single ubuntu job the run already pays
# for — `check` on a ceiling tier, `lane` on a lane tier, exactly one of which
# runs (scripts/ci-changes.sh resolves that once). Being a script rather than
# an inline `run:` block is what keeps it ONE copy across those two callers,
# and it also means it can be run at a terminal, which a YAML block never
# could.
#
# THE BETTER END-STATE, named so it is not mistaken for done: this belongs as
# a real `make` phase with its own `lane-inputs:` declaration over `skill/**`
# and the binary, so the derived lane would select it exactly when it can
# change its verdict. That is a larger change than K5 and would touch the
# ceiling's own phase list; it is not this wave's.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/authoring"

# commands.md == `a2a __catalog` (command + MCP-tool catalog).
go run ./cmd/a2a __catalog > "$tmp/commands.md"
diff -u skill/a2ahub/reference/commands.md "$tmp/commands.md"

# authoring/<type>.md == `a2a template show <type>` (OP-219), one per type.
for t in $(go run ./cmd/a2a template list); do
  go run ./cmd/a2a template show "$t" > "$tmp/authoring/$t.md"
done

for f in "$tmp"/authoring/*.md; do
  committed="skill/a2ahub/reference/authoring/$(basename "$f")"
  if [ ! -f "$committed" ]; then
    echo "skill-drift: new template type with no committed authoring file: $(basename "$f")" >&2
    exit 1
  fi
  diff -u "$committed" "$f"
done

# A committed authoring file whose type no longer exists is also drift.
for f in skill/a2ahub/reference/authoring/*.md; do
  test -f "$tmp/authoring/$(basename "$f")" || {
    echo "skill-drift: committed authoring file with no live template type: $(basename "$f")" >&2
    exit 1
  }
done

echo "skill-drift: the generated skill reference matches a fresh regeneration."
