---
name: commit
description: Stage only the files this session modified and create a conventional commit. Session-isolated — never `git add -A`.
user-invocable: true
argument-hint: "[optional message hint]"
requires_capabilities: [run-shell]
allowed-tools: Bash
mate_synced: v0.98.0
---

# commit

> **Thesis.** A commit records *this session's* work, in the project's convention,
> without sweeping in anyone else's. Two invariants make it safe everywhere:
> **session-isolated staging** (stage explicit paths you touched, never `-A`) and a
> **conventional message** (`type(scope): subject`). This skill is seam-light — it
> needs only a shell; the one project-specific value it defers to (the scope
> vocabulary) lives in the project's config seam (`.mate/config.yaml` `scopes:`), or
> a richer commit-convention rule — not baked in here.

## Procedure

1. **Build the session file list.** Scan your own tool-call history in this
   conversation and collect every path from:
   - `Write` — `file_path`
   - `Edit` / `MultiEdit` — `file_path`
   - `Bash` — only when the command created/moved/deleted files (`mv`, `cp`, `rm`,
     or a codegen/generate step that regenerates tracked files).

2. **Inspect repo state** in parallel:
   - `git status --short`
   - `git diff --stat` (unstaged) and `git diff --stat --staged`
   - `git log -n 5 --oneline` (match the repo's message style).

3. **Reconcile.** Files in `git status` that you did **not** touch belong to another
   session (another terminal, worktree, or a manual edit) — **do not stage them**.
   Note the skipped count in your report.

4. **Draft a conventional commit message:**
   - Format: `type(scope): subject` — lowercase, imperative, ≤72 chars, no period.
   - **Scope by the narrowest meaningful label:** a feature slug if one fits, else
     the stack/area, else the domain, else omit. The concrete scope vocabulary is a
     project value — resolve it in precedence order: (1) the project's own
     commit-convention **rule** if it has one, (2) else the **`scopes:` list in
     `.mate/config.yaml`**, (3) else this heuristic. A commit-convention rule also
     carries any trailer / co-author / attribution convention (config `scopes:` is
     the vocabulary only) — follow it. This skill defers these as project values
     rather than baking a house style in.
   - Body (optional) explains *why*, not *what*; blank line before it.
   - Respect the user's optional hint from `$ARGUMENTS`.

5. **Stage explicit paths and commit** (with a post-check):
   ```bash
   git add <file1> <file2> ...
   git commit -m "$(cat <<'EOF'
   type(scope): subject

   Optional body.
   EOF
   )"
   git status --short   # sequential after commit — confirm a clean tree
   ```

6. **Never** use `git add -A`, `git add .`, `git add *`, or `--no-verify` unless the
   user explicitly instructs it (then comply and note it).

7. **Report** in ≤3 lines: commit hash + subject · files staged (count) · files
   skipped (count, if any — they belong to other sessions).

## Stop conditions

- No session-modified files → tell the user; do not create an empty commit.
- Files to stage include likely secrets (`.env*`, `*credential*`, `*.pem`, keys) →
  warn and ask before staging.
- A pre-commit hook rejects the commit → fix the underlying issue, re-stage, and
  create a **new** commit. Never `--amend` past a hook rejection.
