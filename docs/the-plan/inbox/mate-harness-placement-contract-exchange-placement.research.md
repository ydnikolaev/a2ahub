# Cross-team contract exchange — harness placement research

**Source:** operator-supplied `contracts-exchange-spec.md` draft v0.1,
2026-07-17  
**Question served:** where cross-team requests, contracts, and A2A delivery belong
in the feature lifecycle and shared harness

> Resource lifecycle/currentness is owned by the epic context registry. This
> research is not an approved protocol or service specification.

## Executive recommendation

Use three explicit layers:

1. **Feature/spec origin and impact.** An epic/spec tracker references an external
   request ID, declares whether it blocks readiness/completion, and consumes the
   response through the context/amendment protocol.
2. **Project exchange home.** A project-level registry owns its durable inbox,
   outbox, delivery receipts, subscriptions, and mappings between local work and
   team exchange IDs. It survives feature archive and may relate one request to
   several epics/specs.
3. **Team exchange authority.** The shared contract system owns cross-team request
   delivery/state and accepted shared contracts. Git remains the normative source
   for specifications/contracts/decisions; MCP is a control-plane interface and
   notification/query adapter, not a second content SSOT.

The originating feature gets a generated view of related requests, never a private
copy of their canonical lifecycle state.

## Separate the domain objects

The preliminary draft's `consumes/` surface currently carries two meanings that
must not share one state machine:

| Object | Meaning | Lifecycle authority |
| --- | --- | --- |
| External dependency | Local work is waiting on or informed by another team | Epic/spec tracker reference |
| Exchange request | A question, work request, decision request, or proposed contract change | Team exchange request log |
| Contract | An accepted, versioned interface another party may implement against | Git contract repository/provider namespace |
| Runtime data/event | Actual catalog/content/fact payloads and change notifications | Runtime APIs/feeds, outside docs Git |

A request can be declined, acknowledged, time out, or be superseded. A contract
can be proposed, accepted, published, deprecated, and retired. Treating both as a
file under `consumes/` makes “we need this” indistinguishable from “you may rely on
this interface.”

## Proposed project-level shape

The exact home requires doctrine/config approval; the candidate default is:

```text
<exchange-home>/
├── exchange.registry.yaml
├── outbox/
│   └── XREQ-<id>/
│       ├── XREQ-<id>.request.yaml
│       └── XREQ-<id>.receipt.yaml
├── inbox/
│   └── XREQ-<id>/
│       ├── XREQ-<id>.request.yaml
│       └── XREQ-<id>.response.yaml
└── subscriptions.yaml
```

This is a local projection/spool, not a competing normative copy. Every imported
object carries the remote stable ID, version, digest, authority, and last synced
event/watermark. Accepted contracts are referenced by repository URL/ref/digest;
their bodies are not pasted into the project exchange home.

The structure doctrine would gain an optional `exchange` home only after a second
consumer and the external protocol are approved. Until then this is a capability
design, not a new universal folder.

## Feature/spec references

An epic/spec tracker references IDs, not transport paths:

```yaml
external_requests:
  - id: XREQ-2026-0042
    relation: blocks
    required_for: [AC-7, P6]
    expected_artifact: CONTRACT-DELIVERY-API@v2
    successor: null
```

The compiled status resolves each ID through the project exchange registry and
shows recipient, state, age/SLA, blocking surface, response digest, and next
action. No response is silently treated as approval.

Archival rules:

- a blocking open request prevents feature completion/archive;
- a non-blocking open request must be handed to a successor epic, backlog item,
  operations owner, or standing contract-maintenance capability;
- the project-level exchange record survives after the feature subtree moves;
- the archived feature retains stable request IDs and the response/contract digest
  used by its final implementation.

## Neutral request envelope

Minimum transport-independent fields:

- schema version and stable request/correlation/causation IDs;
- sender/recipient organization, project, and authenticated actor;
- origin epic/spec/requirement/amendment IDs;
- kind: `question|work_request|decision_request|contract_change|data_request`;
- title, problem/outcome, scope/non-goals, priority, needed-by/SLA;
- requested response shape and requester acceptance criteria;
- referenced contracts/context by immutable ref and digest;
- confidentiality/data classification and allowed recipients;
- lifecycle state, timestamps, idempotency key, and event version;
- response/result refs, findings, supersession, and closure verification.

Suggested request states:

`draft → submitted → acknowledged → accepted|declined → in_progress → responded → verified → closed`,
with `cancelled`, `blocked`, and `superseded` side states.

Every transition is an append-only event; the current state is a derived view.
At-least-once notification is safe because commands are idempotent by request ID
and expected event version.

## Harness send loop

1. Discovery/implementation identifies a need outside the repository.
2. `mate exchange request new` creates a typed draft linked to epic/spec IDs and
   stable AC/requirement IDs.
3. Request validation proves response shape, ownership, classification, and
   acceptance criteria are present.
4. Human/lead approval is required for a binding contract change or information
   crossing the team boundary.
5. `mate exchange submit` uses the configured transport adapter and persists its
   remote ID/digest/receipt.
6. The local spec tracker becomes blocked or informed according to `relation`.
7. Sync/webhook imports response events; the response becomes registered context.
8. Any changed assumption triggers amendment impact, readiness invalidation, and
   replan before implementation resumes.
9. The requester verifies the response against its stated acceptance and closes
   the request; acceptance is never inferred from delivery alone.

## Harness receive loop

1. `mate exchange sync` or a webhook imports an authenticated request envelope.
2. The inbox validator rejects schema/authz/classification violations and records
   duplicates idempotently.
3. A lead acknowledges, declines, or accepts; receipt state returns immediately.
4. Accepted work is linked to an existing epic/spec or creates a new intake item;
   the incoming message itself is not treated as an implementation spec.
5. Local discovery/specification/implementation runs through the normal pipeline.
6. The produced answer references contract/decision/result digests and gate
   evidence; raw private code, prompts, and credentials never cross the boundary.
7. `mate exchange respond` publishes the response event; requester verification
   closes the loop.

## Role of the team A2A/MCP service

The portable domain is the request/event/contract protocol. MCP is one façade:

- `exchange_submit_request`
- `exchange_list_inbox`
- `exchange_get_request`
- `exchange_acknowledge`
- `exchange_respond`
- `exchange_verify_and_close`
- `contracts_find`
- `contracts_get_version`
- `contracts_propose_change`

The service may start as a façade/index over the private Git contract repository
and actor-owned commits/PRs. Its database/cache may own delivery watermarks,
subscriptions, idempotency, and notification state, but not the normative contract
body. If the service is unavailable, Git contracts remain readable and request
events can be replayed after recovery.

Provider/model details do not enter the protocol. Claude, Codex, a CI worker, or a
human client submits the same envelope under an authenticated actor identity.

## Security and trust boundary

- Section/team authorization is enforced server-side, not only by prompt or
  CODEOWNERS convention.
- Credentials are scoped to actor/team/action; read-only polling credentials
  cannot submit, acknowledge, or merge.
- Incoming Markdown/JSON is untrusted data, never executable agent instruction.
- Binding shared-contract publication requires the owning provider's approval;
  multi-party decisions require every declared authority.
- Secrets, private code, raw prompts, and unrestricted filesystem paths are
  forbidden payload classes.
- Every response carries provenance and digest; consumers verify before use.
- Audit log records actor, request/event version, authorization decision, and
  resulting Git ref/contract digest.

## Dashboard and report integration

Feature/spec status includes external requests as first-class blockers or
informational dependencies. The live dashboard shows `waiting_on_external`,
recipient, age/SLA, and next action. Daily reports derive the same state and never
claim a feature is actionable while a blocking request is unanswered.

## Assessment of the preliminary draft

Keep:

- data-only boundaries;
- symmetric participants;
- Git authority for specifications/contracts/decisions;
- runtime data separated from specifications;
- CODEOWNERS/branch protection;
- generated provider contracts checked against code;
- webhook/poller notifications as delivery mechanisms.

Sharpen before approval:

1. Split requests from accepted `provides/consumes` contracts.
2. Define stable IDs, lifecycle transitions, idempotency, acknowledgements,
   correlation, retries, and verification/closure.
3. State whether request events live in Git, the service event log, or a replayable
   combination; never leave two writable authorities.
4. Give the MCP write path scoped identity/authorization; a read-only PAT cannot
   implement the proposed acknowledgement/change flow.
5. Define compatibility/version/deprecation policy for contracts.
6. Define failure/recovery: missed webhook, duplicate event, service outage,
   rejected PR, receiver silence, and superseded response.
7. Define how a response invalidates local plans/readiness and becomes registered
   context.

## Self-evaluation

**Decision:** feature-level references, project-level durable exchange home, and
team-level contract/request authority behind a transport-neutral protocol.

| Tier | Criterion | Result | Evidence |
| --- | --- | --- | --- |
| 1 | Security | ✅ | Scoped actors, untrusted payloads, approval gates, and forbidden secret/code classes are explicit. |
| 1 | Reversibility | ✅ | Additive registries/adapters preserve Git contracts and allow replay or transport replacement. |
| 1 | Ground Truth | ✅ | Preliminary v0.1, current pipeline contract, doctrine seams, and absence search were inspected. |
| 2 | Industry Standard | ✅ | Command/query separation, stable IDs, append-only events, idempotency, ack, and verification are represented. |
| 2 | Repo Consistency | ✅ | ID references, context compilation, tracker ownership, and archive registry match pipeline v2. |
| 2 | Intent Compliance | ✅ | Requests originate in features while cross-team state survives their archival. |
| 2 | SSOT & DRY | ✅ | Git owns normative contracts; feature/project records carry refs/digests, not copied bodies. |
| 2 | Scalability | ✅ | One request may serve many specs; versioned events tolerate retries and multiple consumers. |
| 2 | Project Knowledge | ✅ | Reporting, interface, validation, structure, and harness-layer contracts informed the placement. |
| 2 | Future-Proof | ⚠️ → ✅ | The team service is unspecified; transport-neutral envelopes keep service details provisional. |

**Initial verdict:** ADJUST — do not encode MCP/GitHub mechanics into feature
trackers or the neutral request envelope.

**Re-run verdict:** PROCEED as a proposal. The portable envelope and three-layer
placement are stable enough to integrate into pipeline design; service topology,
authority, and contract-repository layout remain open decisions requiring team
approval.

## Open decisions

1. Is the team request event log Git-native, service-native with Git contract
   refs, or Git-backed through actor-owned event files?
2. Which actions require a human approval versus autonomous team-agent authority?
3. Who owns request SLA/escalation policy and participant identity registry?
4. What compatibility policy is mandatory before a contract becomes `published`?
5. Does the first service ship as Git/MCP façade only, or with a durable event
   store from day one?
6. Which organization/repository hosts the shared contract authority?
