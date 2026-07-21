# §16 Handoff Directive

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> Fulfills R-017 and the operator brief item 7 ("директива по созданию
> handoff документа"). This directive governs how ANY completed, tested body
> of work is packaged as an `XH` handoff artifact for agents in other
> systems (deployment, integration, continuation) — including, eventually,
> the handoff of a2ahub's own implementation.

## 16.1 When a handoff is produced

A handoff is REQUIRED when responsibility for work crosses a system
boundary: delivering an implemented capability another team deploys or
builds upon, transferring ownership, or completing a `work_request` whose
result is a body of work rather than an answer (then the `XS` response
references the `XH`).

Preconditions (validator-checkable where marked):

1. The work is implemented AND its stated verification has been executed
   with passing results — a handoff of untested work is a protocol
   violation.
2. Everything the receiver needs is referenced by stable refs (✓ schema:
   envelope `refs` non-empty and pinned; `deliverables[].ref` entries are
   additionally each pinned — both are checked).
3. The producing system's owner has not gated it (handoffs are ungated by
   default; a space MAY add a gate via manifest).

## 16.2 Required content (envelope extension + body sections)

Envelope (`XH` extension, schema-enforced):

| Field | Content |
|---|---|
| `deliverables[]` | each: name, ref (repo/ref/digest or contract `id@version`), kind (code\|contract\|config\|doc\|data) |
| `verification` | how the work was verified: suites run, results summary, how the RECEIVER can re-run or independently check each claim |
| `acceptance_criteria` | inherited from the originating request where one exists; restated measurably |
| `limitations[]` | known gaps, deferred items, assumptions — an empty list is a claim, not an omission |
| `env_requirements?` | what the receiver's environment must provide |
| `fulfills[]` | originating exchange/requirement IDs (a2ahub artifact IDs — NOT the base `origin` field, which stays reserved for local tracker IDs per §5.2) |

Body sections (template-provided, zero-context rule):

1. **Context** — why this work exists; written for a reader with NO access
   to the producer's private repo, chats, or history. No unexplained
   internal names.
2. **What was built** — narrative of the deliverables and how they fit
   together.
3. **How to verify** — step-by-step, executable by the receiver alone;
   every AC mapped to a check.
4. **How to operate/integrate** — the receiver's first actions; runtime
   channels touched (informational, NG-1).
5. **Limitations & next steps** — honest state, suggested continuations.

Forbidden (10.4 applies fully): private source beyond agreed interfaces,
secrets, prompts, absolute paths.

## 16.3 Lifecycle & verification duty

Per §3.4.5: the receiver MUST execute "How to verify" before `verify-pass` —
accepting a handoff unverified is equivalent to skipping tests (the skill
and rules texts state this explicitly). `verify-fail` MUST carry concrete
findings; resubmission is a new revision on the same thread, superseding the
rejected one. Acceptance transfers responsibility: after `accepted`, the
receiver owns the delivered state; subsequent issues are new exchanges, not
re-litigations of the handoff.

## 16.4 Directive-as-deliverable

The implementation epic derived from this plan MUST include producing the
handoff-authoring guide (template + checklist generated from this section)
as part of the templates work (E4/E11), and the a2ahub v1 implementation
itself MUST be delivered to the operator as an `XH` conforming to this
section — the system's first real handoff is the system.
