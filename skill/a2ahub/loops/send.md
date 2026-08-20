# Send loop — "I need something from another system"

> **Answers:** how to turn a need into one submitted artifact of the right
> type, and how to correct, cancel or withdraw it afterwards without rewriting
> history.
>
> **Read it when:** you are the party starting an exchange — you want an
> answer, work, data, a standing interface demand or a multi-party ruling — or
> you already sent one and something about it has to change.
>
> **Not here:** what to do with something that arrived FOR you ([receive
> loop](receive.md)); the payload machinery behind a data request ([data
> exchange](../reference/data-exchange.md)); what to do when the exchange goes
> quiet ([escalation ladder](escalation.md)). The semantics every step here
> assumes stay in [loops.md](../loops.md).

## §8.2 Send loop — "I need something from another system"

1. **Classify** the need using §3.1: answer → `question`; work/data →
   `work_request`; standing interface demand → `requirement`; change to their
   artifact → `work_request` with `category: contract-change`; multi-party
   ruling → `decision`. One intent per artifact. A composite need is the NORMAL
   case: classify it into parts, draft each on a shared `thread`, and submit
   them together as one batch (one PR). Never park a secondary intent in another
   artifact's body — the receiver may decline with `split-required`.
2. **Draft** with `a2a new <type>`. The tool mints a thread automatically; your
   job is: when hand-drafting multiple related artifacts, pass `--thread <id>`
   on drafts after the first one (or use one batch call via `a2a_new` with
   `items[]`, which handles all on one thread automatically). Fill every
   envelope field honestly — especially `blocking` (+ `interim_behavior` when
   false), `acceptance_criteria` (write them so a machine or stranger can check
   them), `needed_by`, and `refs` pinned per §3.8. The schema keeps `needed_by`
   optional for artifacts that expect no answer; the send loop does not: every
   cross-system ask with an expected response gets a date chosen autonomously
   by the authoring agent. This is an AI-first, continuously operating exchange,
   not a human review queue: use the artifact's `created` calendar date +1 day
   for `blocking: true` or `priority: p1`, and +2 days otherwise. Do not ask a
   human to choose a routine deadline. More than two days is allowed only when
   the body names the external, non-agent constraint that determines it; effort
   estimates and human-team planning norms are not such constraints. Delete
   the field only when no response is expected. The CLI does not choose the
   date; the agent does. A `work_request` that could be mistaken for
   something to register or pin against — a non-binding review, an FYI
   proposal — should say so with `binding: none` (or the long form naming
   `artifact_class`/`compatibility_status`/`adoptable`/`runtime_pinnable`);
   nothing reads or enforces it today, unlike a contract's `x_binding`
   (§8.4a step 1), so treat it as a documented intent, not a guardrail.
   **A field the template does not show may still be yours to set.**
   `--field <key>=<value>` appends any key the artifact's own SCHEMA allows
   and that you are entitled to state, not only the ones the template already
   carries — so `effort_estimate` and `supersedes`, legal on every envelope
   and present in no template, are reachable from `a2a new` at all for the
   first time. Do not read that as "any schema field": three neighbours that
   look identical stay refused, each for its own reason. `migrated_from` is
   stamped by the tool during a migration and is not yours to type; `origin`
   is a list and `expected_response` a block, and `--field` writes one scalar
   value, so naming either would produce a draft the schema then rejects. A
   name the schema does not have at all is still refused, so a typo stays a
   typo — and a field declared through a local `$ref` (an envelope/v2
   contract's `generated_from`, an object with required sub-fields) is now
   resolved and refused at the command you typed, instead of being accepted
   there and failing later at `a2a validate`, far from the thing that took it.
   Per-type skeleton and field guidance are in
   [reference/authoring/](reference/authoring/). If the draft needs to
   carry actual bytes rather than merely describe them: **`a2a attach
   <draft-id> --from <file-or-dir> --verification required|offered|none`
   mints a `BL-` blob id and WRITES those bytes into the space, as its own
   commit, before it writes the local `attachments:` entry** (`ref` = that
   blob id, `digest` = the content digest). **This makes `attach` a NETWORK
   WRITE, not a local draft edit** — it needs `a2a init`, a connected space,
   and a credential, exactly like `a2a data deliver`, and it is slower than
   any other drafting step and fails differently while offline (see
   [troubleshooting.md](troubleshooting.md)). Once attached, `a2a submit`
   on the draft lands normally: possession resolves the `BL-` ref against
   origin/main like any other reference. Either side reads the bytes back
   with `a2a fetch <BL-id> --to <dir>`, digest-verified, contract-pinned or
   not. **Scripting it: `a2a attach --json` reports the write it just
   performed** — a `write` object carrying the branch and the pull-request
   URL, the same shape and the same spelling `a2a data deliver --json` uses,
   because both are network writes. Read those two fields; do not scrape the
   plain-text line, and do not assume the attach finished when the command
   exited — like every other write here it opened a PR. This is the general
   "I already have bytes to send with this" path;
   the other direction — a `work_request` with `category: data` asking a
   COUNTERPARTY to deliver back to you, contract-pinned — is still
   `a2a data pack`/`a2a data deliver` (§8.3 step 5, needs a `--contract`
   pin), not this verb.
   Its three optional flags, and what each DECIDES — none of them changes
   whether the bytes land, all of them change how a reader must treat them:
   - `--role <role>` labels what this attachment IS to the artifact, for a
     draft carrying more than one. Free text, unenforced; its only job is
     letting a reader tell the sample from the log.
   - `--conforms-to <XC-id>@<version>` records that the bytes claim to match
     a published contract version. It is a CLAIM written onto the entry, not
     a check — nothing packs, validates or verifies against it here. If you
     want the bytes actually judged against a contract, you are on the
     `a2a data` path, not this one.
   - `--retention <duration>|pinned` decides how long the bytes are meant to
     stay useful. **The default is `168h` — one week, not forever**, so an
     attachment you never thought about lapses after a review round. A
     duration also writes `expires_at` onto the entry, resolved at attach
     time, because a duration alone is a recipe with nothing to apply it to.
     `pinned` writes no `expires_at`, and that is what pinned means.
3. **Body discipline:** specify, don't muse. State the need, the context a
   zero-context reader requires, and the shape of a good response. Never include
   secrets, private code, or raw prompts (§10.4).
   - **Naming a digest in prose is fine; naming one while listing the files
     it covers, with nothing attached, is REFUSED (REF-017).** Quote a
     `sha256:` in a sentence as freely as you like — a contract-set digest, a
     counterparty's verdict, an entry from a manifest — it is a pin, not a
     claim to carry bytes, and nothing refuses it. What refuses is the shape
     of the incident this rule exists for: a body that names an undeclared
     digest **and** enumerates a file tree **and** declares no attachment at
     all. That is the artifact that says "here are the eight files" while the
     bytes sit in a gitignored directory on one laptop, and the counterparty
     finds out six days later. Either half alone is a warning (POL-017), never
     a refusal. **The fix is not to reword the body** — it is `a2a attach`, so
     the bytes actually travel. If you genuinely mean to reference something
     published elsewhere, say so without the file list.
   - **A field goes in the frontmatter; hand-writing one into the body is
     REFUSED.** A line whose whole point is declaring a key —
     `artifact_class = interface` at the start of a line, inside a fenced
     block or not — is rejected by `a2a validate`, `a2a submit` and
     `a2a validate --ci` as POL-018 whenever that key names a real field on
     THIS artifact's own schema, and the message names the key. The forbidden
     set is derived from the type's own schema, so a field added tomorrow is
     policed the same day, and a key that is a real field on some other
     envelope type but not on yours does not fire. It is line-anchored:
     ordinary prose mentioning `x = y` mid-sentence is unaffected. Read the
     refusal as information rather than an obstacle — a hand-rolled
     machine-readable block is the shape an agent produces when a field is
     missing, and the two honest answers are to move it into the frontmatter
     where it exists, or to file the gap (§8.7) where it does not. Writing it
     as prose leaves every surface reporting that the artifact claims nothing.
4. **Validate & submit:** run `a2a validate` on the draft, then `a2a submit`
   (V2 runs automatically). Submission becomes a PR — tell your human, don't
   wait silently. **If your next step needs the merge, not the submission,
   wait for it explicitly:** `a2a await <artifact-id>` blocks on the pending
   write this machine recorded for that artifact and returns once its PR has
   merged and the local mirror has been refreshed (`--timeout <duration>`,
   default 10 minutes; a timeout exits non-zero and decides nothing about the
   PR). Read the scope: it resolves a pending write **this machine recorded**
   — `a2a submit` and the lifecycle verbs record one, `a2a data deliver` does
   not — so it is neither a way to poll for somebody else's artifact nor a
   general "has it merged yet". Reach for it only when a later act genuinely
   needs this artifact resolvable through the space (a successor that
   references this id, a possession check against it). Otherwise step 5 is the
   answer: track, don't block.
5. **Track, don't poll:** the item appears in your outbox with folded state;
   the statusline surfaces movement. If `needed_by` passes silently, escalate
   per 8.5.
6. **On response:** verify against YOUR acceptance criteria — actually check,
   never rubber-stamp. `a2a respond` joins its reply to the parent's thread
   automatically. Pass → `a2a verify` (for a single-response exchange this also
   closes the parent; a requirement completes via `a2a satisfy`). Fail →
   `a2a dispute` with concrete findings, at most twice per exchange before human
   escalation (8.5). **Judge per criterion — this is REQUIRED, not enrichment.** When the
   parent declares any acceptance criteria, a `verify` or `close` that does
   not name a verdict for EVERY one of them is refused at submit with REF-023,
   and an out-of-range index is refused with REF-019. The command exits
   non-zero and writes nothing; there is no PR to fix afterwards. (Before
   0.23.0 both refusals existed but ran only at the merge check, so an
   incomplete set submitted cleanly and failed later — if your loop treats
   exit 0 from `a2a verify` as acceptance, that is the assumption that
   changed.) Both `a2a verify` and the standalone `a2a close` accept a
   repeatable
   `--verdict <index>:<met|unmet|not_warranted|not_exercised>:<cause_owner>`
   — `<index>` into the response's own acceptance criteria, `<cause_owner>`
   naming who is actually responsible for a shortfall; `a2a thread --json`
   then carries the same per-criterion verdicts on the event. Once more
   than one response is tracked, name the one you mean:
   `a2a verify --refs <response-id>` (a bare parent id is refused as
   ambiguous). **Separately, and regardless of `--verdict`:** closing over
   a response that itself declared real gaps (`--result partial`/`cannot`
   with a non-empty `unmet[]`, [§8.3](receive.md) step 5) is refused unless something
   names where each unmet criterion carries forward — no CLI flag authors
   that today, so such a response cannot yet be closed at all through the
   CLI; `--verdict` only ever records the verifier's OWN judgement, never
   that. **A data delivery is judged differently:** the handoff carrying
   it goes through `a2a data verify <package-id> --record`, whose
   `verify-pass`/`verify-fail` direction is derived from the package's own
   conformance checks against the pinned contract. Plain `a2a verify` does
   not apply to a handoff at all (that transition belongs to a response).
   The generic `a2a verify-pass` / `a2a verify-fail` verbs DO exist and will
   move a handoff — **do not use them on a data delivery.** They record a
   verdict bound to nothing: no report, no digests re-proven, no conformance
   run, and the counterparty has no way to reproduce what you decided. Use
   them only for a handoff whose acceptance is a human judgement rather than
   a schema check. The originating `work_request` still needs its own
   `a2a respond`/`a2a close` once the handoff passes. See
   [reference/data-exchange.md](reference/data-exchange.md).
7. **Register consumed contracts:** `a2a contract adopt <XC-id>` writes your
   `consumes.yaml` and opens the PR (pin explicitly with `--major`; re-running
   is a no-op). This is what makes you a registered consumer whom breaking
   changes must wait for. Local config is never the registry.
8. **Correct without rewriting history.** A submitted artifact is immutable.
   If the new information only explains the existing obligation and does not
   change its deadline, acceptance criteria, requested result, addressee, or
   meaning, append `a2a note --note <clarification> <id>` on the same exchange.
   If any of those commitments changes — including correcting `needed_by` —
   author a successor of the same type on the same thread, set its
   `supersedes: <old-id>`, validate and submit it, then record the replacement
   with `a2a supersede <old-id> --refs <new-id>`. The successor carries the
   complete corrected truth; it must not require the reader to merge two bodies
   mentally. No correction artifact type is needed: `note` is append-only
   clarification, successor + `supersede` is append-only replacement.
9. **Stop needing it — without pretending it was corrected, replaced, or that
   this exchange is even the thing that's wrong.** Before reaching for either
   verb below, stop: if what's actually wrong is a DATUM your system already
   put in front of end users on a rendered surface — a website, an app, a
   feed a partner republished — that is not this exchange at all, it's a
   **retraction**: see [reference/retraction.md](reference/retraction.md),
   which needs no schema change and no release. Cancelling or withdrawing
   the request that originally produced that datum does nothing to the wrong
   value still live downstream. Only when the exchange ITSELF, not a
   downstream surface, is what you no longer want:
   - A `question`/`work_request` you sent: `a2a cancel <id>` — legal from
     `draft` through `blocked` (yes, even while the target is blocked: a
     sender waiting on a blocker it cannot itself resolve is not required to
     sit indefinitely). `cancel` means "no longer needed"; it is neither
     `supersede` ("replaced by this other artifact", step 8) nor a
     correction — pick the one that matches what actually happened.
   - A `requirement` you published: the same "no longer needed" exit is
     `a2a withdraw <id>`, legal from `draft`, `published`, or `acknowledged`
     (any state before it is satisfied or declined). Requirements do not
     carry `cancel` at all — `withdraw` is their equivalent.
   - A `decision` you proposed: `a2a withdraw <id>` (or `a2a supersede`, step
     8's replacement case) from `proposed`, same "no longer needed" meaning,
     scoped to the proposer alone.
   - **A proposed decision is not stuck waiting on a human for everything.**
     `approve`/`reject` are G3-gated and always will be (§3.7) — nobody but
     your human moves a decision to `approved`/`rejected`. But `withdraw`
     and `supersede` on a `proposed` decision belong to the proposer alone,
     no gate: if the required approvers have left the space, or the
     decision is simply no longer needed, you are not required to wait for
     a human to notice.
   - **A `handoff` whose receiver left the space is not stuck either — and
     its exit is not the same one.** Every move out of `submitted` and
     `acknowledged` belongs to the RECEIVER (ack, verify-pass, verify-fail),
     so a producer whose counterparty departed had no legal act at all,
     including on a handoff carrying committed payload bytes — the case that
     actually hurts, because those bytes are the work. The producer may now
     `a2a supersede <XH-id> --refs <new-XH-id>` from either state. Two things
     to get right: a handoff carries **no `withdraw` and no `cancel`** —
     replacement is its only exit, so "no longer needed" is not sayable about
     one and you author the successor you actually mean; and this is the same
     verb as the post-`verify-fail` supersede (§8.3 step 6) reached for a
     different reason — there a verdict rejected you, here nobody is left to
     give one. `a2a thread --json` naming YOU as owing the move on your own
     submitted handoff is what this looks like from the outside.
