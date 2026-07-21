#!/usr/bin/env python3
# mate_synced: v0.98.0
"""mate's conditional-rule router for codex (spec §16.10, v0.74.0).

WHAT THIS IS. Claude Code loads a `paths:`-conditional rule by itself: the rule file
declares its globs and the provider injects it when a matching file is opened. Codex
has no rules home and no such mechanism — so until now a `paths:` rule simply did not
reach a codex session at all (a declared degradation, reported on every pull).

This script is codex's channel for the same contract, built from what codex ACTUALLY
offers, verified against codex-cli 0.142.5 on 2026-07-14:

  - `PreToolUse` fires before a tool call, and its `matcher` filters on the TOOL NAME.
  - A hook may return `hookSpecificOutput.additionalContext`, and codex adds that text
    as model-visible developer context. (Verified empirically, not merely read: a
    sentinel word minted seconds earlier, present nowhere but this script's stdout,
    came back out of the model.)
  - File edits arrive as `tool_name: "apply_patch"`, and the path is NOT a structured
    field — it is inside `tool_input.command`, the raw v4a patch text. Hence the
    parser below, which is not a hack: it is the shape of codex's payload.

THE HONEST DEGRADATION, and it is not small. Codex has NO read tool — the model reads
files through the shell. So this router fires when the agent is about to EDIT a
matching file, never when it merely reads one. Claude Code's `paths:` fires on READ.
That difference is declared in adapters/codex/surface.yaml (`fires_on: edit`) and
printed on every pull. It is not parity and must never be described as parity. (It is
arguably the more useful moment — "you are about to write a Nuxt component, here are
the rules" beats "you glanced at one" — but that is a consolation, not a defence.)

SILENT UNLESS TRUSTED. Codex runs no non-managed hook until the operator reviews and
trusts it (`/hooks`). An untrusted hook is SKIPPED — not warned about, at the moment
it would have mattered. mate therefore prints the registration + trust step as a
loud, standing instruction, never a footnote.

WHY THE RULES ARE NOT IN THIS FILE. They live in the sibling `mate-rules.data.json`,
regenerated on every pull, so a rule change never edits this script — it only rewrites
data this script reads.

That split is NOT what keeps codex's hook trust alive, and it is worth being exact
about which is which. Trust is recorded in config.toml `[hooks.state]` against a hash
of the hook DEFINITION (the command in hooks.json), not of this file's contents:
measured on 0.142.5 by changing this script's bytes and watching `trusted_hash` stay
put and the hook keep firing. So trust would survive an embedded router too. (And this
file's bytes are NOT stable anyway — `mate pull` stamps it with the release.)

What the split actually buys is that mate's own correctness stops depending on that
finding. A hashing policy is the provider's to change; a design that only works while
codex keeps hashing definitions rather than contents fails on codex's schedule, in
silence (an untrusted hook is SKIPPED, not warned about), at the moment a rule was
supposed to fire. The data file costs nothing and removes that dependency entirely.
"""
import fnmatch
import json
import os
import re
import sys

PATCH_PATH = re.compile(r"^\*\*\* (?:Update|Add|Delete) File: (.+)$", re.M)
PATCH_MOVE = re.compile(r"^\*\*\* Move to: (.+)$", re.M)


def touched_paths(tool_input):
    """Every repo path the pending apply_patch would write.

    `tool_input.command` is the raw v4a patch, not a path field. Codex documents this
    ("Bash and apply_patch use tool_input.command"); the shape below is what 0.142.5
    actually sent.
    """
    if not isinstance(tool_input, dict):
        return []
    cmd = tool_input.get("command") or ""
    if not isinstance(cmd, str):
        return []
    out = PATCH_PATH.findall(cmd) + PATCH_MOVE.findall(cmd)
    return [p.strip() for p in out if p.strip()]


def matches(path, globs):
    """Glob-match a repo-relative path, with `**` spanning directories.

    fnmatch alone treats `*` as matching `/`, which would make `apps/*/x.ts` match
    `apps/a/b/x.ts` — a rule firing on files it does not govern is as wrong as one
    that never fires.
    """
    for g in globs:
        rx = []
        i, n = 0, len(g)
        while i < n:
            if g.startswith("**/", i):
                rx.append("(?:.*/)?")
                i += 3
            elif g.startswith("**", i):
                rx.append(".*")
                i += 2
            elif g[i] == "*":
                rx.append("[^/]*")
                i += 1
            elif g[i] == "?":
                rx.append("[^/]")
                i += 1
            else:
                rx.append(re.escape(g[i]))
                i += 1
        if re.fullmatch("".join(rx), path):
            return True
        # A bare-name glob (`*.vue`) is idiomatic for "anywhere in the tree" in a
        # rules file; honour that reading rather than silently matching nothing.
        if "/" not in g and fnmatch.fnmatch(os.path.basename(path), g):
            return True
    return False


def already_sent(session_id, names):
    """Fire each rule ONCE per session.

    Without this, every single edit to a matching file re-injects the whole rule body.
    The context cost is paid per tool call, and a rule the model has already been given
    is not made truer by repetition — it is made expensive.

    THE COST OF THE CHOICE, stated rather than hidden: the marker outlives the model's
    memory of the rule. If the session compacts, the injected text can fall out of
    context while this file still says "already sent" — and the rule goes quiet for the
    rest of the session. Re-injecting on every edit would trade that rare, partial loss
    for a certain, permanent tax on every tool call. The tax is worse. If codex ever
    exposes a compaction signal to a PreToolUse hook, this is the first thing to fix.
    """
    if not session_id:
        return set()
    tmp = os.environ.get("TMPDIR") or "/tmp"
    state = os.path.join(tmp, "mate-rules-%s.seen" % re.sub(r"[^A-Za-z0-9_.-]", "_", session_id))
    seen = set()
    try:
        with open(state) as fh:
            seen = {ln.strip() for ln in fh if ln.strip()}
    except OSError:
        pass
    fresh = [n for n in names if n not in seen]
    if fresh:
        try:
            with open(state, "a") as fh:
                fh.write("".join(n + "\n" for n in fresh))
        except OSError:
            pass  # a router that cannot write state still routes; it just repeats
    return seen


def main():
    try:
        payload = json.load(sys.stdin)
    except Exception:
        return 0  # a malformed payload is codex's problem, not a reason to block an edit

    if payload.get("tool_name") != "apply_patch":
        return 0

    data_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "mate-rules.data.json")
    try:
        with open(data_path) as fh:
            rules = json.load(fh).get("rules") or []
    except OSError:
        return 0  # no data file = no conditional rules for this consumer

    paths = touched_paths(payload.get("tool_input"))
    if not paths:
        return 0

    hit = [r for r in rules if any(matches(p, r.get("paths") or []) for p in paths)]
    if not hit:
        return 0

    seen = already_sent(payload.get("session_id"), [r["name"] for r in hit])
    fresh = [r for r in hit if r["name"] not in seen]
    if not fresh:
        return 0

    body = "\n\n".join(
        "# %s\n(mate rule, loaded because this edit touches %s)\n\n%s"
        % (r["name"], ", ".join(r.get("paths") or []), r.get("body") or "")
        for r in fresh
    )
    json.dump(
        {"hookSpecificOutput": {"hookEventName": "PreToolUse", "additionalContext": body}},
        sys.stdout,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
