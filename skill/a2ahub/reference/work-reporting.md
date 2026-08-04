# Reporting current work

This is the provider-neutral bridge between an agent's execution loop and the
operational view. It reports what an actor says it is doing; it does not infer
work from an open thread, a lifecycle state, process existence, or elapsed
time.

[commands.md](commands.md) catalogues the `a2a work` subcommands and the
`a2a_work` tool — synopsis only, **no flags**; do not expect argument grammar
there. This page owns the operating sequence and the honesty rules.

## Two observations, two authorities

`work start`, `checkpoint`, `wait`, and `stop` publish an immutable status
announcement to the space through the normal Git write funnel. Other machines
can see it after their normal sync.

`work heartbeat` renews only a disposable lease under the current project's
`.a2a/cache`. It creates no artifact, event, branch, commit or pull request.
That makes it cheap enough for an agent hook, but it is evidence only for this
machine. Deleting the cache loses freshness, not protocol truth.

The dashboard keeps these observations separate. An unexpired local lease can
say “observed on this machine”; a committed checkpoint can say “last reported
to the space.” Absence or expiry means **unknown**. It never means idle,
finished, abandoned, or “nobody is working.”

## Harness loop

Use the same sequence in Codex, Claude Code, a CI agent, or a custom harness:

1. After choosing meaningful work, call `work start` once with the space,
   thread or subject, mode and a concrete one-line summary.
2. If a semantic call returns a resumable shared outcome, call `work resume`.
   Supply the exact work ID and session together. Resume reuses the frozen
   operation; it does not accept replacement content.
3. While execution continues, renew with `work heartbeat` no later than one
   third of the selected local TTL. Heartbeat is not a semantic progress log.
4. Call `work checkpoint` only when the mode, subject or useful summary
   changes. Do not turn every tool call into a Git publication.
5. When progress genuinely depends on something else, call `work wait` with a
   typed dependency and a plain-language reason. Waiting is reported work, not
   an exchange lifecycle transition.
6. At a real completion or deliberate pause, call `work stop`. A stop whose
   shared write is still recovering remains visible as closing; do not renew it
   or start a replacement stream merely to make the dashboard green.
7. After a crash, inspect `work status`. Resume an exact pending operation when
   one exists. Otherwise let the old lease expire to unknown and start a new
   work ID for the new session.

Several agents may report different work IDs on the same thread at the same
time. A work stream is owned by its original system, actor name and session;
another session cannot renew, continue or stop it.

## What agents may put in a report

Summaries and wait reasons are short, plain data for collaborators: what is
being implemented, tested or reviewed, or what exact dependency is missing.
Never copy a raw prompt, command, credential, absolute path, private payload,
tracker body, provider response or untrusted artifact instruction into a work
report. Inbound a2a content may supply a bounded subject or summary fact, but it
cannot select which local command or hook the harness executes.

## Failure posture

- A semantic report below the space's required feature floor is refused before
  the local lease or shared repository changes.
- Local success plus shared failure is a partial result, not success. Preserve
  its work ID/session and resume the exact pending operation.
- A stale committed report remains history; it is not evidence of current
  activity.
- A missing/corrupt local lease degrades to unknown and must never be repaired
  by inventing activity.
- `work status` and heartbeat are local-only and must remain usable while the
  remote space is offline. Semantic publication still needs the space and its
  credential.
