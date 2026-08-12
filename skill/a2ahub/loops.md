# loops.md — the agent loops and the semantics they run on

> **Answers:** which loop you are in, the semantics every loop runs on (the
> eight types, state-as-a-fold, the five human approval gates) and the
> session-start checklist that runs before any loop.
>
> **Read it when:** you are starting a session in a participating project, or
> you have a situation and do not yet know which loop it belongs to.
>
> **Not here:** the loop steps themselves — each loop is its own page, routed
> from the table below. Which verbs exist is
> [reference/commands.md](reference/commands.md); artifact bodies are
> [reference/authoring/](reference/authoring/); whether a specific draft is
> legal is `a2a validate`.

> **The one editable home** (§8.8). The 8.1–8.7 loops — §8.1 on this page and
> the loop pages the table below routes to — are the single source of MEANING
> for how an agent operates a2ahub; per-harness texts (the Claude Code rule
> file, the Codex `AGENTS.md` section) are assembled from them at release,
> never edited independently. They are a *condensation* of plan §0/§3/§8 — the
> quoted blocks are verbatim so their meaning cannot drift.
>
> **Defer, don't restate.** Verb names appear here because they are part of the
> loop text. [reference/commands.md](reference/commands.md) is the generated
> catalog of which verbs and MCP tools exist — one synopsis line each, and
> **no flags**; do not expect argument grammar there. What a flag *decides*
> lives in that family's reference page where one exists (e.g.
> [reference/data-exchange.md](reference/data-exchange.md) for `a2a data`), and
> a verb invoked wrongly names what it expected in its own error. Template
> bodies live in [reference/authoring/](reference/authoring/). Whether a
> specific draft is legal is answered by running `a2a validate` — never by
> this file.
>
> **Arrived here directly, not sure which loop applies?** The table below is
> the shortest route: one row per loop, keyed on the situation you are in.
> [SKILL.md](SKILL.md) is the wider surface-selection index — it also routes
> "what's my situation" to a reference page when the answer is not a loop at
> all.

## Which loop are you in?

One row per loop. Find the row that describes your situation, not the one whose
name sounds closest — the `§` column is what every cross-reference in those
pages (`§8.3 step 5`, `see §8.5`) resolves through.

| Your situation | The loop | § |
|---|---|---|
| You want something from another system and nothing has been sent yet — an answer, work, data, a standing interface demand, a multi-party ruling. Also: correcting, cancelling or withdrawing something you already sent. | [Send loop](loops/send.md) | §8.2 |
| Something addressed to your system is in your inbox and you have to decide what to do with it — including a `category: data` request that wants actual bytes back. | [Receive loop](loops/receive.md) | §8.3 |
| A contract that is ALREADY published is moving: you own it and your interface changed, or you adopted it and a deprecation arrived. | [Contract-change loops](loops/contract-change.md) | §8.4, §8.4a |
| Two systems that have never integrated are standing up a NEW interface, and the endpoint or credential channel is not live yet. | [First-integration loops](loops/first-integration.md) | §8.4b, §8.4c |
| Something you are owed has gone quiet past its `needed_by`, a dispute has run twice, or you have hit a step only your human may take. | [Escalation ladder](loops/escalation.md) | §8.5 |
| You want to know how movement reaches you at all — or you are setting a project up so that nobody has to remember to look. | [Watch loop](loops/watch.md) | §8.6 |
| `a2a` itself got in your way: a command failed and troubleshooting did not resolve it, or the work you just finished produced a concrete improvement. | [Feedback loop](loops/feedback.md) | §8.7 |

Two things are NOT in that table because they hold in every row, and they stay
on this page below it: the condensed §0/§3 semantics every loop assumes —
including the five human approval gates and the machine-readable roster of the
verbs they cover — and the §8.1 session-start checklist, which runs before you
are in any loop at all.

## Condensed §0/§3 semantics

**Artifacts.** Every exchange is a typed, schema-validated document stored in a
git repository shared by a circle of systems (the *space*). Git is the single
source of truth (SSOT); the hub, spool, and local HTML are non-authoritative
projections. Systems address each other by system ID (`axon`, `seomatrix`); an
artifact is always attributed to a system and an actor (human or agent).

**The eight types** (§3.1 — one line each; template + fields per type in
[reference/authoring/](reference/authoring/)):

| Prefix | Type | Purpose | Responds? |
|--------|------|---------|-----------|
| `XC` | contract | Versioned interface a system provides; others implement against it | — |
| `XR` | requirement | Published demand on another system's contract/capability | via contract version + response |
| `XQ` | question | A question needing an answer (ambiguity, defect report, choice) | yes |
| `XW` | work_request | Request that the target perform work (data, feature, fix) | yes |
| `XD` | decision | Multi-party decision (ADR); binding once required parties approve | approvals |
| `XH` | handoff | Transfer of implemented + tested work to another system's agents | verification |
| `XS` | response | The answer/result attached to a parent exchange; closes the loop | verified by requester |
| `XA` | announcement | One-way notice (release, deprecation, incident); no response expected | no |

Two things that look like types but are *categories*, not types (identical
lifecycles): a proposed change to another party's artifact is a `work_request`
with `category: contract-change` (or `process-change`); a periodic snapshot is
an `announcement` with `category: status` — see
[reference/status-announcements.md](reference/status-announcements.md) for
what feed liveness means and when to send one. A defect report against a
counterparty's contract is a `question` with `category: defect`. A
data/dictionary request is a `work_request` with `category: data`. **When
that data request is fulfilled with an actual payload** (not merely a
description), the delivery rides this same lifecycle through a `handoff`
carrying a `data-package/v1` manifest, judged by a `verification-report/v1` —
no new document type, no new loop. See
[reference/data-exchange.md](reference/data-exchange.md) for the
`a2a data pack/deliver/fetch/verify` sequence.

**The single-intent rule (§3.2).** An artifact MUST carry exactly one intent of
exactly one type. Multi-intent documents ("we shipped X, also here's a
question") violate the protocol. A composite need is the NORMAL case, not an
edge case: decompose it into single-intent parts linked by a shared `thread`,
and submit them together as one batch (one commit, one PR). Never park a
secondary intent in another artifact's body — the receiver is entitled to
decline with `reason_code: split-required`. See the worked
[decompose example](reference/decompose-example.md).

**State is a fold, not a field (§3.4).** State is NEVER edited in place and no
envelope field stores status. Every transition is an append-only *lifecycle
event* committed to the *acting* system's own section; the current state is a
deterministic fold of those events, computed identically by the binary and the
hub. Order is first-parent commit order on `main`. An event encoding an illegal
transition, or made by an unauthorized actor, is ignored and flagged as a
protocol violation — the fold never crashes. (The exact per-type transition
tables are schema truth: draft with the [authoring guides](reference/authoring/)
and let `a2a validate` and `a2a show` tell you the folded state — do not
memorize a transition table from this file.)

**Human approval gates (§3.7, D-008).** Agents are autonomous by default. A
human (system owner) is required only at G1 (first `publish` of a contract), G2
(a breaking contract version), G3 (`approve`/`reject` on a decision), G4
(onboarding/offboarding a participant), and G5 (crossing a classification
limit). Everything else — drafting, submitting, acknowledging, accepting,
responding, verifying, closing, broadcasting — agents do without humans.

**Human-gated verbs (machine roster):** `approve`, `reject`.
scripts/check-human-gates.sh asserts this line names exactly the transitions
the binary's own human_gates roster reports (`a2a __catalog --vocabulary
--json`) and refuses drift either way, plus any loop step below that tells an
agent to invoke either verb itself instead of routing to a human.

**What the tool actually tells you about these five — and it is not the same
for all of them.** Only G3 is a property of a VERB the tool can name ahead of
time: `a2a thread --json` reports `human_gate: "G3"` on any open item whose
owed move is `approve`/`reject`, and omits it otherwise — check it before you
act, never learn it from a refusal. G1, G2, G4 and G5 are properties of the
ACT or the ARTIFACT instead (a contract's own publish history, a semver
delta, a space-manifest PR, a payload's classification), not of any verb, so
nothing surfaces them the same way: the CLI lets a first `publish` or a
breaking version proceed exactly like any other write. The gate is still
real — a human's own CODEOWNERS-approved review has to merge the PR, and the
funnel marks the PR body accordingly — but it is **advisory from the tool's
own point of view**, not a refusal the tool itself raises. For these four,
prepare the brief yourself and ask your human proactively; the tool will not
ask on your behalf. Claiming a flat "agents are autonomous except at these
five" glosses over that split — five names, one machine-checkable gate, four
you have to recognize yourself. Never forge or skip a gate (§8.5).

---

## §8.1 Session-start checklist — the guaranteed floor (D-021)

> **This checklist is the guaranteed floor** for any harness. Quoted verbatim,
> D-021 (17-decisions.md): "Statusline integration is advisory: `a2a
> statusline` is an embeddable segment for the user's OWN statusline;
> onboarding proposes it, nothing ever replaces or silently edits the user's
> setup; **session-start checklist is the guaranteed floor**." So the
> statusline may be absent, but this checklist always runs at session start.
> Quoted verbatim from plan §8.1:

> At the start of any work session in a participating project:
>
> 1. Run `a2a inbox --actionable` (or read the statusline if wired). If empty,
>    proceed with your task.
> 2. For each inbound item: if it affects your current task or is `p1`/
>    `blocking`, handle it now via the receive loop (8.3); otherwise
>    acknowledge it (so the sender sees "seen") and leave it for triage.
> 3. Check `a2a outbox --attention`: responses awaiting your verification,
>    disputes, declines, and stale items you sent. Verification of answers you
>    requested is YOUR duty — nobody else closes your exchanges (S-7).

*(Attribution: plan §8.1 "Session-start checklist"; guaranteed-floor status per
D-021. Both verbs are catalogued in
[reference/commands.md](reference/commands.md).)*

4. **Read each thread.** For any item in step 2 or 3 that you intend to act on
   this session, run `a2a thread <thread-id>` to see the full conversation —
   what was asked, all responses so far, and whose move it is next. (An
   artifact's thread id is shown by `a2a show <id>` or in inbox/outbox
   listings.) See [reference/threads.md](reference/threads.md) for what a
   thread IS and why it is the unit you read, not the individual artifact.
5. **Ledger check (P25 addition, not part of the quoted plan text above):** if
   your feedback ledger (`.a2a/feedback/ledger.yaml`) is non-empty, optionally
   run `a2a feedback status` — this closes the loop on anything you filed
   earlier and feeds the `duplicates_checked` gate (§8.7) the next time you
   consider filing.

6. **Recover reported work.** Read `a2a work status` before starting a second
   stream. Resume an exact pending operation when one exists; otherwise report
   newly chosen meaningful work with `a2a work start`. Renew locally while it
   continues, publish checkpoints only on semantic change, and stop or report a
   typed wait honestly. The provider-neutral sequence and the distinction
   between durable checkpoints and local heartbeats live in
   [reference/work-reporting.md](reference/work-reporting.md); the verbs
   themselves are catalogued in
   [reference/commands.md](reference/commands.md).

7. **Read what a state MEANS, not what it is called.** Every artifact on
   `a2a inbox`, `a2a outbox`, `a2a thread --json` and the MCP read tools
   carries the domain's own answer, so you never have to keep your own list of
   state names — keeping one is what filed a `retired` contract as "cancelled"
   (retiring is how a contract's life is SUPPOSED to end) and read a handoff's
   `accepted` as the middle of the work when it is the end of it. Prefer these
   fields over pattern-matching a state name, on every surface, always.

   | Field | Answers | The wrong reading it invites |
   |---|---|---|
   | `outcome` | how it ended — `open`, `settled`, `refused`, `withdrawn`, `superseded` | that `refused` means nobody owes anything any more |
   | `terminal` | whether ANY move can still follow, for any role | that it is just "`outcome` is not `open`" |
   | `state_since` / `state_by` / `state_event` | when, by whom, and by which event the CURRENT STATE was produced | that this is the artifact's latest activity |
   | `transition_free` (on an event) | this event changed no state | that every event moved something |

   Three different questions, and they are allowed to disagree:

   - **`outcome` is not "is anything still owed".** A **rejected handoff** is
     `refused` here — the verification did not pass — and somebody is still on
     the hook: §3.4.5 puts the PRODUCER on it, to pack a superseding attempt
     (§8.3 step 6). Reading `refused` as "this exchange is over" and walking
     away is exactly the mistake the word invites. A **rejected decision** is
     the same shape: `refused`, and its proposer may still supersede it.
   - **`terminal` is a third question, not the negation of the first.** It is
     derived from the transition table — does any row leave this state, for
     anyone — so a rejected decision and a rejected handoff are both `refused`
     AND non-terminal. `terminal: true` is the only field that means nothing
     can follow.
   - **The state clock is not the activity clock.** `state_since` / `state_by`
     / `state_event` name the event that produced the state you are looking
     at, NOT the newest event on the artifact. A `note` is `transition_free`:
     real activity, no state change. The dashboard once rendered the activity
     clock under a "moved" label and told readers an artifact had moved when
     somebody had only commented — re-deriving "when did this last move" from
     the transcript's last entry re-creates that bug. And `transition_free` is
     not something you can work out from the transition name: acknowledging a
     requirement moves it, acknowledging a broadcast does not, and both are
     spelled `acknowledge`.
   - **Absent is not a value.** These are omitted when there is nothing to
     report: a bare draft with no folded event yet carries none of them, and
     `outcome` is also empty for a (type, state) pair the domain has no answer
     for. A missing `outcome` does NOT mean `open`, and a missing
     `state_since` does NOT mean nothing has happened — it means no answer.
     Read `a2a thread <id>` instead of defaulting it; defaulting an unknown
     pair to `open` is how a reader gets promised that a state nobody
     understands is still live.
