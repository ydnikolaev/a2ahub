#!/usr/bin/env bash
# parse_tracker.sh — parse an epic's tracker.yaml and list actionable (unblocked) phases.
#
# v2 (2026-07-13): reads the canonical tracker.yaml (docs/_templates/feature/
# tracker.template.yaml schema). The legacy markdown-tracker (⬜/✅ emoji tables)
# support is dropped — those trackers live in docs/archive/ only.
#
# A phase is ACTIONABLE when status == pending and every blocked_by entry is done.
# Cross-epic deps ("<epic-slug>:PN") resolve against docs/features/<slug>/tracker.yaml
# relative to the repo root; an unreadable target keeps the phase blocked (fail-closed)
# and is flagged.
#
# Usage:
#   bash .agents/scripts/parse_tracker.sh <tracker.yaml>
#   bash .agents/scripts/parse_tracker.sh --json <tracker.yaml>
#   bash .agents/scripts/parse_tracker.sh --help
#
# Examples:
#   bash .agents/scripts/parse_tracker.sh docs/features/hub-service-2026-07/tracker.yaml
#   bash .agents/scripts/parse_tracker.sh --json docs/features/hub-service-2026-07/tracker.yaml

set -euo pipefail

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
    cat <<'EOF'
parse_tracker.sh — list actionable (unblocked) phases of an epic tracker.yaml

USAGE:
  bash .agents/scripts/parse_tracker.sh [--json|--waves] <tracker.yaml>

OUTPUT:
  Actionable phases (status=pending, all blocked_by done), one per line:
    <id>  <title>  [spec: <path>]
  Phases blocked only by an unresolvable cross-epic dep are listed separately.

FLAGS:
  --json      Output as a JSON array of {id, title, spec, blocked_by}
  --waves     Print topological wave layers for ALL unfinished phases —
              /teamlead's S3 wave-table seed (layer N+1 unlocks when layer ≤N
              and done phases satisfy its deps; cross-epic deps resolve live,
              pending ones hold the phase in a trailing "held" bucket)
  --contract  Emit mate's normalized epic contract for one tracker (one JSON object:
              slug, title, status, phases{done,total}, blocked_by, waves, actionable,
              commits). This is the seam `make report-epics` prints — the tracker
              schema stays in this repo, only the normalized shape crosses.
  --help      Show this help
EOF
    exit 0
fi

JSON_MODE=false
MODE="list"
if [[ "${1:-}" == "--json" ]]; then
    JSON_MODE=true
    shift
elif [[ "${1:-}" == "--waves" ]]; then
    MODE="waves"
    shift
elif [[ "${1:-}" == "--contract" ]]; then
    MODE="contract"
    shift
fi

TRACKER_FILE="${1:?Usage: parse_tracker.sh [--json] <tracker.yaml>}"
[[ -f "$TRACKER_FILE" ]] || { echo "parse_tracker: $TRACKER_FILE not found" >&2; exit 1; }

REPO_ROOT="$(git -C "$(dirname "$TRACKER_FILE")" rev-parse --show-toplevel 2>/dev/null || pwd)"

# Run on a DECLARED interpreter first (a project .venv, if one exists), and fall
# back to an ambient one only if it can demonstrably do the job — `import yaml`,
# not merely "exists on PATH".
#
# This is not defensive padding. `make` and an operator's shell can resolve
# DIFFERENT python3s on the same machine, and only one may have PyYAML: the
# script works when a human runs it and dies under `make`, where its caller
# reads the traceback as "this tracker is invalid" — blaming the data for a
# broken toolchain. Exit 2 is reserved for that, so a caller can tell "your YAML
# is bad" (exit 1) from "your python is" (exit 2), and never print the first
# when it means the second.
PYTHON=""
for cand in "${PYTHON_BIN:-}" "$REPO_ROOT/.venv/bin/python3" python3 "${HOME:-}/.pyenv/shims/python3" /usr/bin/python3; do
    [[ -n "$cand" ]] || continue
    command -v "$cand" >/dev/null 2>&1 || continue
    if "$cand" -c 'import yaml' >/dev/null 2>&1; then PYTHON="$cand"; break; fi
done
if [[ -z "$PYTHON" ]]; then
    echo "parse_tracker: no python here can 'import yaml'. The trackers are fine; the toolchain is not." >&2
    echo "               fix: pip install pyyaml   (or point PYTHON_BIN at an interpreter that has it)" >&2
    exit 2
fi

JSON_MODE="$JSON_MODE" MODE="$MODE" REPO_ROOT="$REPO_ROOT" "$PYTHON" - "$TRACKER_FILE" <<'PYEOF'
import json, os, sys

import yaml

tracker_path = sys.argv[1]
json_mode = os.environ.get("JSON_MODE") == "true"
mode = os.environ.get("MODE", "list")
repo_root = os.environ.get("REPO_ROOT", ".")

with open(tracker_path) as f:
    data = yaml.safe_load(f)

phases = data.get("phases") or []
by_id = {p.get("id"): p for p in phases if p.get("id")}

def cross_epic_done(dep: str):
    """Resolve '<epic-slug>:PN' against that epic's tracker. Returns True/False/None(unresolvable)."""
    slug, _, pid = dep.partition(":")
    path = os.path.join(repo_root, "docs", "features", slug, "tracker.yaml")
    try:
        with open(path) as f:
            other = yaml.safe_load(f)
    except OSError:
        return None
    for p in other.get("phases") or []:
        if p.get("id") == pid:
            return p.get("status") == "done"
    return None

def wave_layers():
    """Topological wave layers over ALL unfinished phases. done phases (and done cross-epic
    deps) are pre-resolved; a pending/unresolvable cross-epic dep holds its phase in the
    trailing `remaining` bucket (fail-closed). Shared by --waves and --contract: the layering
    is the one thing both modes mean by "how deep is this epic", and two copies of it would
    drift into two different answers to the same question."""
    unfinished = [p for p in phases if p.get("status") not in ("done", "deferred")]
    resolved = {p.get("id") for p in phases if p.get("status") == "done"}
    remaining = {p.get("id"): p for p in unfinished}
    layers = []
    while remaining:
        layer = []
        for pid, p in list(remaining.items()):
            ok = True
            for dep in p.get("blocked_by") or []:
                dep = str(dep)
                if ":" in dep:
                    if cross_epic_done(dep) is not True:
                        ok = False
                elif dep not in resolved:
                    ok = False
            if ok:
                layer.append(pid)
        if not layer:
            break  # leftovers are held by cross-epic deps (or a cycle — feature-lint gates that)
        for pid in layer:
            resolved.add(pid)
            remaining.pop(pid)
        layers.append(layer)
    return layers, resolved, remaining, unfinished

if mode == "contract":
    # mate's normalized EPIC CONTRACT (the `make report-epics` seam, mate v0.80.0). This
    # project's tracker schema stays HERE; only the normalized shape crosses the boundary,
    # so the harness never learns what a tracker is. Every field is READ from the tracker —
    # a field we cannot fill is omitted, never estimated.
    layers, _, _, unfinished = wave_layers()
    done = sum(1 for p in phases if p.get("status") == "done")

    # Epic-level blockers: the OTHER epics whose phases hold ours. This is what lets a report
    # say "not started — it waits on the theming epic" instead of showing a bare, mute 0%.
    blockers = []
    for p in unfinished:
        for dep in p.get("blocked_by") or []:
            dep = str(dep)
            if ":" in dep and cross_epic_done(dep) is not True:
                slug = dep.split(":", 1)[0]
                if slug not in blockers:
                    blockers.append(slug)

    title = data.get("slug", "?")
    readme = os.path.join(os.path.dirname(tracker_path), "README.md")
    try:
        with open(readme) as f:
            for line in f:
                if line.startswith("# "):
                    title = line[2:].strip()
                    break
    except OSError:
        pass

    commits = []
    for p in phases:
        for h in p.get("commits") or []:
            commits.append(str(h))

    print(json.dumps({
        "slug": data.get("slug"),
        "title": title,
        "status": data.get("status"),
        "phases": {"done": done, "total": len(phases)},
        "blocked_by": blockers,
        "waves": len(layers),
        "actionable": layers[0] if layers else [],
        "commits": commits,
    }, ensure_ascii=False))
    sys.exit(0)

if mode == "waves":
    layers, resolved, remaining, unfinished = wave_layers()
    print(f"tracker: {data.get('slug','?')} — {len(layers)} wave layer(s) over {len(unfinished)} unfinished phase(s)")
    for i, layer in enumerate(layers, 1):
        print(f"  wave {i}: {', '.join(layer)}")
    if remaining:
        parts = []
        for pid, p in remaining.items():
            unmet = []
            for dep in p.get("blocked_by") or []:
                dep = str(dep)
                if ":" in dep:
                    if cross_epic_done(dep) is not True:
                        unmet.append(dep)
                elif dep not in resolved:
                    unmet.append(dep)
            parts.append(f"{pid} ← {', '.join(dict.fromkeys(unmet))}")
        print(f"  held (cross-epic deps not done, or downstream of held): {'; '.join(parts)}")
    print("  note: same-layer phases run in parallel ONLY if their Footprints are disjoint (/teamlead S3).")
    sys.exit(0)

actionable, cross_blocked = [], []
for p in phases:
    if p.get("status") != "pending":
        continue
    unmet, unresolved = [], []
    for dep in p.get("blocked_by") or []:
        dep = str(dep)
        if ":" in dep:
            r = cross_epic_done(dep)
            if r is None:
                unresolved.append(dep)
            elif not r:
                unmet.append(dep)
        else:
            if by_id.get(dep, {}).get("status") != "done":
                unmet.append(dep)
    if not unmet and not unresolved:
        actionable.append(p)
    elif not unmet and unresolved:
        cross_blocked.append((p, unresolved))

if json_mode:
    out = [
        {"id": p.get("id"), "title": p.get("title"), "spec": p.get("spec"),
         "blocked_by": p.get("blocked_by") or []}
        for p in actionable
    ]
    print(json.dumps(out, ensure_ascii=False, indent=2))
else:
    slug = data.get("slug", "?")
    print(f"tracker: {slug} — {len(actionable)} actionable phase(s)")
    for p in actionable:
        spec = f"  [spec: {p.get('spec')}]" if p.get("spec") else ""
        print(f"  {p.get('id')}  {p.get('title')}{spec}")
    for p, deps in cross_blocked:
        print(f"  ({p.get('id')} held by unresolvable cross-epic dep(s): {', '.join(deps)} — fail-closed)")
PYEOF
