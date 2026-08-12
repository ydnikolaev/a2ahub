# Escalation ladder

> **Answers:** what to do when an exchange stops moving — a stale `needed_by`,
> a dispute that has run twice, a gate only a human may pass, a
> protocol-violation flag on your own section.
>
> **Read it when:** something you are owed has gone quiet, or you have reached
> a step you are not allowed to take yourself.
>
> **Not here:** which five acts need a human, and what the tool tells you
> about each — that is the human-approval-gates block in
> [loops.md](../loops.md). Each loop routes here rather than restating this
> table.

## §8.5 Escalation ladder

Condensed from plan §8.5 (the verbs are catalogued in [reference/commands.md](reference/commands.md)):

| Situation | Action |
|-----------|--------|
| inbound `p1` or `blocking` for your active work | handle immediately in-session |
| your item stale past `needed_by` | send one reminder on the existing exchange (`a2a note <id>`, a transition-free annotation); if still silent after the reminder ages, surface to your human |
| dispute loop reached 2 | stop; summarize both positions; escalate to humans on both sides (a `decision` artifact is often the right vehicle) |
| gate needed (G1–G5) | prepare everything, notify your human with a one-paragraph brief; never forge or skip a gate — the tool confirms only G3 ahead of time (`human_gate` on `a2a thread --json`); the other four you must recognize yourself (see "Human approval gates" in [loops.md](../loops.md)) |
| protocol-violation flags on your section | fix within the session you notice them; they are your section's hygiene |
