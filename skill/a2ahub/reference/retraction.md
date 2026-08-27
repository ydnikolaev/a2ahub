# retraction.md — withdrawing a datum that is already live

> **Answers:** how to withdraw a datum that is already live in front of end
> users, using a `work_request` carrying an `x_retraction` block —
> schema-valid today, no release needed.
>
> **Read it when:** a value your system published onto a rendered surface is
> wrong, and cancelling the exchange that produced it would change nothing
> downstream.
>
> **Not here:** no longer wanting the EXCHANGE itself — `a2a cancel` / `a2a
> withdraw` in [loops/send.md](../loops/send.md); retiring an INTERFACE
> ([contract-versions.md](contract-versions.md)); replacing a DECISION that
> was rejected or approved — `a2a supersede`, below.

> **The rule this demonstrates**: `retire` is for an INTERFACE — it waits
> for every registered consumer to acknowledge, because a contract version
> going away is (by design) not urgent enough to skip that wait. A
> **retraction** is different: a *datum* is already live in front of end
> users (a website, an app, a feed a third party republished) and it is
> wrong, and it may not be safe to wait. This page is the convention for
> that case, and it needs no schema change and no release — it is
> schema-valid **today**.

**Not `cancel`, not `withdraw` either.** Those exits (`a2a cancel <id>` /
`a2a withdraw <id>`, [../loops.md](../loops.md) §8.2) say "I no longer need
this EXCHANGE" — the artifact you sent. A retraction says the opposite: the
exchange did its job; what's wrong is a downstream, already-rendered value
that was never itself sent as an exchange. Cancelling or withdrawing the
request that originally produced that datum does nothing to the wrong value
still live on the surface — reach for this page instead.

## Not this either: superseding a decision (LFC-005)

A `decision` is not a datum on a rendered surface, so it never goes through
this page — but it has its own "replace something already recorded" verb,
`a2a supersede <decision-id> --refs <successor-id>`, and it is refused
(**LFC-005**) unless the successor satisfies §3.4.4's own precondition:

- superseding a **rejected** decision: the supersede event's own actor must
  be the **author of the successor decision** — the fold checks this
  against the successor artifact's own `from`, not the predecessor's.
- superseding an **approved** decision: the named successor must itself be
  an **approved decision** — any actor may act, but an unapproved or
  unresolvable successor refuses.

If the successor cannot be resolved at all (an unparseable ref, a
successor no local mirror knows about), the refusal still fires — LFC-005
never grants on "I could not check" — and an accompanying **LFC-006**
(severity: unmeasured, never itself blocking) names that the precondition
was unevaluated rather than evaluated-and-failed. The refusal message
names the fix directly: point `--refs` at a successor that is either
authored by you (superseding a rejection) or already approved (superseding
an approval).

## Why an `x_` block and not a new `category`

The obvious design is a closed enum value — `category: retraction` — or a
new envelope type. Neither ships here, deliberately:

- **A closed-enum addition means an OLD binary refuses the new value.**
  Every consumer's validator, every space's pinned floor, would have to
  move first before a retraction artifact could even be submitted
  space-wide. That is exactly the wrong shape for "I need to withdraw this
  now."
- **Enum values, once artifacts carry them, are frozen forever.** The
  identity/keying rule, the required-action vocabulary, the ack model —
  none of it has been used by a second real reporter yet. Picking the enum
  value now would freeze a shape nobody has field-tested.

`work_request`'s envelope already carries `patternProperties: {"^x_":
true}` — any `x_`-prefixed key is schema-valid on every artifact type,
today, on the binary already shipped. An `x_retraction` block costs nothing
to try and produces the evidence (does the shape hold across a second real
retraction?) that would justify promoting it to a real `category` later.
This has been verified empirically, not just read off the schema: a filled
copy of the template below returns `valid: true` from `a2a validate`
against the shipped `envelope/v1/work_request.schema.json` — see
"Verification" below.

## Why `work_request`, not the reporter's proposed standalone type

The proposed three-state ack with evidence (`acknowledged` → `applied` →
`verified`) maps exactly onto the shipped exchange lifecycle — no new
state machine needed:

| Reporter's proposed state | Shipped equivalent |
|---|---|
| `acknowledged` (read) | `acknowledge` event (`a2a ack`) |
| `applied` (live surface changed, with URLs + resulting HTTP status) | `respond --result delivered` (`XS` response, evidence in its body) |
| `verified` (confirmed absent from render + index) | the retractor's `a2a verify` against `acceptance_criteria` |

"Skip the queue" — the reporter's `severity: unsafe` fast-path — is already
`priority: p1` + `blocking: true` on the envelope. Nothing new to model.

`category: data` is the closest shipped `work_request` category. The
retraction's actual semantics (severity, identity, required actions) live
entirely inside `x_retraction`, not in the category — inventing a
`category: retraction` value would be the same enum-freeze this whole
design avoids, just moved one field over.

## Template

```yaml
---
schema: envelope/v1
id: XW-<system>-<YYYYMMDD>-<rand4>
type: work_request
title: Retract <what> for <where> (<why, one clause>)
space: <space-id>
from: <producing-system>
to: [<consumer-system>]                # the system whose surface renders the wrong datum
actor: {kind: agent, name: <written by a2a — never hand-author>}
created: <RFC-3339 UTC>
category: data                         # closest shipped category; the retraction lives in x_retraction below
priority: p1                           # p1 + blocking:true = the reporter's "skip the queue"
blocking: true
interim_behavior: "the wrong value stays live until <consumer> applies required_action"
refs:
  - {ref: "<XC-id>@<version>"}         # pins the contract the retracted datum came from
acceptance_criteria:                   # what `a2a verify` checks — name the actual surface(s)
  - "<url> no longer renders the retracted value"
expected_response:
  shape: "confirmation with the live URL and its resulting HTTP status"
classification: internal
thread: <thread:system-YYYYMMDD-rand4 — a2a new mints it>
x_retraction:
  severity: <correction|unsafe|withdrawn>       # unsafe = take down before a replacement exists
  reason: "<what went wrong and how it was discovered>"
  identity:
    <key>: <value>                              # the key tuple identifying the specific datum, not the whole feed
  effective:
    from: <RFC-3339 UTC>                        # when the datum became wrong
    to: null                                     # null = open-ended, still wrong until replaced
  required_action: [<unpublish|deindex|replace|annotate>]   # one or more
  replacement: "<XC-id>@<version> — optional, where the corrected value now lives>"
  user_notification: <none|banner|email>          # optional — did/should the consumer's own users be told
---
Body: what was wrong, how it was discovered, and what "corrected" looks like
at each surface named in `acceptance_criteria`.
```

## When an agent writes one — the anchored moment

Exactly one moment, not a batch job: **the moment a producer determines a
datum it already published is live on a rendered surface and wrong.** Not
at the next scheduled sync, not bundled with the next regular update —
retraction exists precisely because that wait is what it's built to skip.
The response side has its own anchored moment too: **`a2a respond
--result delivered` is written only after `required_action` has actually
been applied to the live surface**, never before — the URLs and HTTP
status the response cites are evidence, and evidence written ahead of the
fact is not evidence.

## Verification — this validates today, no release

A filled copy of the template above (`type: work_request`, `category:
data`, `x_retraction: {...}`) was round-tripped through `a2a validate`
against the shipped `schemas/envelope/v1/work_request.schema.json` and
returned:

```json
{"valid": true, "artifact_id": "XW-axon-20260727-k3f9", "invocation_point": "V1", "violations": []}
```

No fixture ships in this skill tree from this page (this is prose, not a
schema deliverable) — a project adopting this convention should keep its
own worked fixture the same way the corpus does for every shipped type.

## Measurement moment, and what would falsify this

The measurement moment is **the first real retraction filed by either
pilot repo against data already live on a counterparty's surface.**

This convention is falsified — meaning the `x_retraction` shape needs
revision before it earns a `category` value — if either of these happens:

- **A field the real retraction needed is missing.** If `severity`,
  `required_action`, or `identity` cannot express what the reporter
  actually needed to say, that is a concrete, evidence-backed gap to close
  before promotion, not a reason to work around the block with prose.
- **A consumer's tooling doesn't render `x_` blocks, so the retraction is
  acknowledged but never acted on.** `x_retraction` is schema-valid but
  invisible to any inbox/dashboard rendering that only shows modelled
  fields — if a real consumer's agent reads the artifact, sees a normal
  `work_request`, and misses the retraction inside it, that is decisive
  evidence the convention needs a `category` (so tooling can render it
  distinctly) sooner than the "evidence first" default assumes.

## The guard, deliberately deferred

No lint or doctor row checks that a retraction's `required_action` was
actually applied — that is stack-specific (it depends on how the consumer
renders its surface) and is exactly the residual-rot boundary the
conventions spec draws for the whole phase (R-018): a2ahub standardizes
the guard shape, never the semantic check.
