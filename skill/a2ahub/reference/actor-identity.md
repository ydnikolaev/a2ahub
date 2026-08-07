# Who gets recorded as the actor

Every artifact and every event carries an `actor`, and it is written into a
shared append-only log. Nobody can rewrite it later. This page is about what
lands in that field, because until v0.19.9 the answer was "whatever the caller
typed", and two different wrong answers were reaching real spaces.

## Two identities, and only one of them is written

| | What it names | Where it lives |
|---|---|---|
| the **system** | the participant on whose behalf the write happens (`axon`, `seomatrix`) | `actor.system`, written on the record |
| the **actor** | what performed the write — an agent id, or a person | `actor.kind` + `actor.name`, written on the record |
| the **principal** | the human the agent acted for | **not written**; derived when reading, from the space manifest's `owners` for that system |

The principal is deliberately absent from the record. It already lives in the
manifest, where it stays correct when ownership changes — copying it onto every
event would freeze a stale answer into a log nobody can edit.

## Do not type your own name

This is the short version of the whole page.

The authoring templates ship `actor: {kind: agent, name: <filled by a2a>,
model: <filled by a2a>}`. Leave those alone. `a2a` fills them at write time
from what it can actually establish about the process that is running.

They used to read `<agent-name>`, and that invitation is exactly how a contract
publish in a live space came to record `kind: agent, name: codex` for a write
codex did not perform. Nothing verified the name, and `event/v1` carries no
provenance field that could have settled it afterwards.

## How the actor is resolved

1. **Detection.** If the environment identifies the coding agent that is
   running, that verdict wins — over a flag, over an env var, over a harness
   default, over config. An agent can be talked into naming the wrong tool; an
   environment cannot.
2. Otherwise the ordinary chain, most explicit first: `--actor-*` flag (or the
   MCP tool input) → `A2A_ACTOR_*` → harness adapter default → project config
   → OS username.
3. If none of those resolve a name, the write is **refused**, not attributed to
   nobody:

   ```
   cannot determine who is acting: pass --actor-name <name>, or set
   A2A_ACTOR_NAME. Every artifact and event records its actor permanently, so a
   write without one is refused rather than attributed to nobody
   ```

   Expect this in a container or CI runner with no `/etc/passwd` entry.

Detection only overrules a name that claims to be a **different agent**. A name
that is not an agent id at all — a bot handle, a service account — stands, and
detection merely adds the model it knows.

`--actor-kind human` (or `A2A_ACTOR_KIND=human`) suppresses detection entirely.
A person saying "this was me" is a claim about something else, and the tool
does not argue with it.

## When your agent is not detected

Detection is wired only for vendors whose environment has been read on a real
machine. Today that is **Claude Code**, plus a generic `AI_AGENT` hint. Every
other vendor has a recognized id and no detector — because inventing one from
memory would produce a confident wrong attribution, which is the failure this
whole mechanism exists to end.

So name yourself once, in the environment rather than on each command line:

```sh
export A2A_ACTOR_AGENT=codex          # the id, from the list below
export A2A_ACTOR_AGENT_VERSION=0.5.1  # optional
export A2A_ACTOR_MODEL=gpt-5-codex    # optional
export A2A_ACTOR_EFFORT=high          # optional
```

Recognized ids:

`claude-code` · `codex` · `github-copilot` · `cursor` · `windsurf` ·
`gemini-cli` · `antigravity` · `cline` · `roo-code` · `kilo-code` ·
`opencode` · `aider` · `continue` · `amp` · `goose` · `devin` · `zed` ·
`jetbrains` · `replit` · `warp` · `trae` · `augment`

Common aliases resolve to their id (`claude` → `claude-code`, `copilot` →
`github-copilot`, `gemini` → `gemini-cli`, `roo` → `roo-code`, `kilo` →
`kilo-code`, `junie` → `jetbrains`). Matching is exact after folding, never by
prefix — `claude-code-review` is not `claude-code`.

An id off this list is what makes `a2a html` render your agent's mark beside
the owner's face. A name that matches nothing is treated as a person and gets
no badge.

## The one place this can bite you: work reporting

A work id's reporter identity is **immutable for the life of that id**.
`actor.system`, `actor.name` and `actor.session` are all pinned by the first
checkpoint, and a continuation that changes any of them is refused:

```
a work id cannot change reporter name
```

Upgrading across v0.19.9 changes what `actor.name` resolves to — from your OS
username to the detected agent id. A work stream you opened before the upgrade
and continue after it will therefore be refused. Two ways out:

- finish it with `--actor-name <the original>`, or
- start a new work id.

Neither loses anything: the checkpoints already published stand, and the refusal
is the immutability rule working, not a bug.

## What a recorded name does and does not prove

For an event written from v0.19.9 onward, a recognized agent id means detection
established it, or an operator declared it in the environment.

For an event written **before** that, a recognized id means only that the record
says so — and `codex` on the getvisa publish is the standing proof that the
record could be wrong. `event/v1` has no field distinguishing a detected
identity from a typed one, so no surface can tell them apart after the fact.
Read an older actor as the event's own claim, not as an established fact.

## Where it shows up

- `a2a thread` / `a2a html` — each row's face is the system's owner; the agent,
  when the record names one, is the badge on that face.
- `a2a validate` — the schema still enforces `actor.name` as a backstop; the
  refusal above fires first so the message names a remedy rather than a field.
- The authoring templates under [authoring/](authoring/) — every one of them
  carries the `<filled by a2a>` placeholders described here.
