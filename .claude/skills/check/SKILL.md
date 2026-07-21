---
name: check
description: Run the project's quality gate(s) for the stack(s) touched this session and report a tight pass/fail with actionable next steps.
user-invocable: true
argument-hint: "[optional stack/area to force]"
requires_capabilities: [run-shell]
allowed-tools: Bash
mate_synced: v0.98.0
---

# check

> **Thesis.** Before you call work done, run the machine's verdict on it — the
> project's gate suite, scoped to what *this session* touched. The universal part
> is the loop (detect touched stacks → run their gate → report tight, actionable
> pass/fail); the project-specific part (which paths map to which stack, the exact
> gate command, the thresholds) is a **value** the project's own check-convention
> rule supplies. This skill runs the loop and defers those values. It is the
> operational face of the validation doctrine: type/test passing is not the same
> as the gate passing, and a green gate is the offload — machines hold the
> invariants so your attention stays on design and correctness.

## Procedure

1. **Build the session stack set.** Scan your own tool-call history in this
   conversation for the paths you created/edited, and map each to the stack that
   owns it. **The path→stack map and the per-stack gate command live in the
   project's own check-convention rule — read it and follow it.** If the user
   passed an argument (`backend`, `frontend`, an area name), honor it over
   detection. Nothing touched and no argument → ask which stack, don't guess.

2. **Run each affected stack's gate**, each in its own working directory, in
   parallel when more than one applies. Use the exact command the project rule
   defines (a project typically wraps its suite as `make check` or equivalent) —
   this skill does not hardcode a command or a threshold.

3. **Parse results by failure class**, not as a wall of output:
   - all green → report `PASS` with the stack(s) that ran;
   - any red → name the failing tool and the first relevant error line, grouped
     as compilation/type · lint · test · coverage · compliance.

4. **Suggest the next step by class:**
   - compilation/type → the file(s) to fix;
   - lint → the project's autofix (if any) or the targeted edit;
   - test → rerun just the failing suite first;
   - coverage under gate → which package/area is below its threshold;
   - compliance/policy → which rule tripped (as reported by the gate).

## Output format

Keep it tight — one line per stack, then only the failing lines. Do **not** paste
full stack traces unless the user asks.

```
Backend:  PASS (12.4s)
Frontend: FAIL (4 lint, 1 test)
  - <file>:<line> — <rule>
  - <suite> — expected X, got Y
```

## Stop conditions

- No session-touched stack and no argument → ask; never run every gate blindly.
- The gate command is not defined for a detected stack (no project rule, or the
  rule omits it) → say so and ask, rather than inventing a command.
- A gate is red → report it faithfully with the failing output; never declare
  done on a red gate, and never pass `--no-verify`-style bypasses unless the user
  explicitly instructs it (then comply and note it).
