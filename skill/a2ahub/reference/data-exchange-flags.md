# Data exchange — every flag, and how a source directory maps to a contract

> **Answers:** what each flag on `a2a data pack`, `request`, `deliver`, `fetch` and
> `verify` DECIDES (including the express `--for` forms; not merely that it exists), the exit-code contract, and
> the rule mapping your source tree onto the pinned contract's schema entries.
>
> **Read it when:** you are composing an `a2a data` command and need to know
> what a flag commits you to, or `pack` refused your directory layout.
>
> **Not here:** the order the four verbs run in ([data
> exchange](data-exchange.md)); what a refusal means once you have hit one
> ([refusals and verdicts](data-exchange-refusals.md)).

## Flags, and what each one decides

| Verb | Flag | Required | What it decides |
|---|---|---|---|
| `pack` | `--contract <XC-id>@<version>` | yes | The exact contract version every entry is checked against, pinned into the manifest. A digest suffix (`#sha256:…`) is accepted and re-proven. |
| `pack` | `--from <dir>` | yes | The local source tree. Read-only; nothing under it is modified. See the directory-mapping section below — this is where most packs fail. |
| `pack` | `--profile synthetic\|sanitized` | yes | The data-handling claim recorded in the manifest. `synthetic` asserts the payload was generated and never derived from real records; `sanitized` asserts it was derived from real data with identifying content removed. **Nothing verifies which is true** — it is your assertion, carried to the consumer, so claim the weaker one when unsure: anything derived from production data is `sanitized`, never `synthetic`. `production` is refused outright and is not a third option. |
| `pack` | `--format json\|ndjson` | yes | How every dataset entry is parsed and counted. `ndjson` is one JSON document per line and is what makes a violation report an exact **record number**; `json` is one document per file and reports the file only. Choose `ndjson` for anything row-shaped — the consumer's feedback is only as aimed as this choice. |
| `pack` | `--expires <duration>` | no (default `168h`) | How long the attempt stays fetchable. Non-positive is refused. No maximum. |
| `pack` | `--fulfills <XW-id>` | no | The `work_request` this attempt will answer. Optional here — it only supplies the thread — but **required at `deliver`**, so pass it at both and the two never disagree. |
| `pack` | `--supersedes <DP-id>` | no | The prior attempt this one replaces. **This is the only place supersession is set** — it is baked into the manifest here. |
| `pack` | `--max-attempts <n>` | no (default `0` = no ceiling) | An escalation guard. When set, packing an attempt whose number exceeds `n` is refused with *"escalate rather than retry"* instead of minting attempt N+1. Use it when a loop could otherwise retry forever; leave it unset and nothing is ever refused on this ground. |
| `pack` | `--json` | no | Machine-readable result, including `staging_root`. |
| `deliver` | `<staging-root>` (positional) | yes | The path `pack` printed. Do not reconstruct it from the package id. |
| `deliver` | `--fulfills <XW-id>` | yes | The request this delivery answers. Required here even if `pack` also received it. |
| `deliver` | `--expect-pack <digest>` | no | Binds this call to the exact `aggregate_digest` `pack` printed; refuses if the staged manifest changed underneath you. |
| `deliver` | `--supersedes` | — | **Refused.** Belongs to `pack`. |
| `deliver` | `--json` | no | Machine-readable result. |
| `fetch` | `<DP-id>` (positional) | yes | The package to retrieve. |
| `fetch` | `--to <dir>` | yes | Destination. A divergent existing destination is refused untouched; a byte-identical one succeeds as `already-present`. |
| `fetch` | `--json` | no | Machine-readable result. |
| `verify` | `<DP-id>` (positional) | yes | The package to judge. |
| `verify` | `--record` | no | Performs the ONE funnel write (report + lifecycle event). Without it, nothing is written anywhere. |
| `verify` | `--json` | no | Machine-readable result — see the verdict-vs-error rule in [the consumer sequence](data-exchange.md). |
| `deliver` | `--for <XW-id>` | no | Selects the express mode: acknowledge, accept, pack, deliver and respond to that request in one run (see "Express flow" in [data exchange](data-exchange.md)). It derives the staging root, `--fulfills` and `--supersedes` itself, so a positional staging root, `--fulfills` and `--supersedes` are refused beside it. Every flag in the next four rows applies only with it. |
| `deliver --for` | `--from <dir>`, `--profile synthetic\|sanitized`, `--format json\|ndjson` | yes | Exactly the `pack` flags of the same names, with the same meaning and the same directory-mapping rule below. |
| `deliver --for` | `--contract <XC-id>@<version>` | only when the request does not pin exactly one contract | Which pinned contract to pack against. When the work request pins more than one, the run refuses with *"pass --contract <XC-id>@<version> to choose"*; a ref that is not one the request pins is refused too. |
| `deliver --for` | `--expires <duration>`, `--max-attempts <n>` | no (defaults `168h`, `0` = no ceiling) | Exactly the `pack` flags of the same names. |
| `deliver --for` | `--timeout <duration>` | no (default `10m`) | How long EACH wait for a pending merge may take. It bounds every step's wait, not the whole invocation. |
| `verify` | `--for <XW-id>` | no | Selects the express mode: judge the latest handoff answering that request. It finds the package itself, so a positional package id is refused beside it. Without `--record` it only judges and writes nothing; with `--record` it also acknowledges, verifies, then closes on a pass or disputes on a fail. The next four rows apply only with it. |
| `verify --for` | `--to <dir>` | no | Fetch the package into this directory before judging. Omitted, nothing is fetched. |
| `verify --for` | `--verdict <index-or-criterion-id>:<met\|unmet\|not_warranted\|not_exercised>:<cause_owner>` | when the request has more than its first criterion and you are closing | One verdict per extra criterion, passed through to the close. Repeatable. The verb refuses rather than invent a verdict you did not give. |
| `verify --for` | `--reason <text>` | no | Overrides the dispute reason that is otherwise derived from the failing findings. |
| `verify --for` | `--timeout <duration>` | no (default `10m`) | As for `deliver --for`. |
| `request` | `--producer <system>` | yes | The system asked to deliver the data. |
| `request` | `--contract <XC-id>@<version>` | yes | The pinned contract the data must conform to; cited on the work request. |
| `request` | `--interim-behavior <text>` | yes | What the consumer does while it waits. |
| `request` | `--title <text>` | no (default `Data conforming to <XC>@<version>`) | The request title. |
| `request` | `--needed-by YYYY-MM-DD` | no (default two days after the run, UTC) | The date the data is needed by. Any other format is refused. |
| `request` | `--acceptance-criterion <text>` | no, repeatable | An extra acceptance criterion. `ac1` is always "the delivered package passes `a2a data verify`" and is not replaceable. |
| `request` | `--timeout <duration>` | no (default `10m`) | As for `deliver --for`. |
| `request` | `--json` | no | Machine-readable result. |

`deliver` and `verify` also accept the ordinary lifecycle actor flags
(`--actor-kind`, `--actor-name`, `--actor-model`), because both write a
lifecycle event; they behave exactly as they do on any other transition verb
and are normally left to the configured identity —
[actor-identity.md](actor-identity.md) owns what that resolves to.

**Exit codes.** `0` success. `1` a failing verdict **or** a refusal — these
are distinguished by content, not by code (see the `--json` rule in
[the consumer sequence](data-exchange.md)). `2` a usage error: a missing required flag, an unknown
one, or a malformed package id.

## How your source directory maps to the contract's schemas

`pack` needs to know, for every file, which of the pinned contract's schema
entries it is checked against (`conforms_to`). The rule (one file is walked
recursively per top-level directory whose name matches a schema's own
**stem** — the schema's own file name with its directory and
`.schema.json`/`.json` suffix stripped):

```
<source>/
├── order/                      # matches contract schema "schema/order.schema.json" (stem "order")
│   ├── 2026-08-01.json         # conforms to that schema
│   └── nested/2026-08-02.json  # also conforms — any depth under order/ counts
├── shipment/                   # matches "schema/shipment.schema.json" (stem "shipment")
│   └── 2026-08-01.json
├── README.md                   # role=readme — ONLY recognized at the source ROOT
└── index.json                  # role=index — ONLY recognized at the source ROOT
```

- **A file under `<source>/<schema-stem>/…`, at any depth, conforms to that
  schema entry.** This is the layout to use whenever the contract declares
  more than one schema.
- **A flat source (files directly at the root, no per-schema
  subdirectories) works only when the contract declares exactly one
  schema** — every dataset file at the root then conforms to that sole
  schema.
- **A flat root file against a multi-schema contract is refused**, naming
  every schema the contract declares and the stem each one expects, e.g.
  *"entry `orders.json` is at the source root, but the pinned contract
  declares 2 schema entries (`schema/order.schema.json`,
  `schema/shipment.schema.json`) — place it under one of
  `<source>/<schema-stem>/` instead"*. This is deliberate: a pack that
  silently guessed one of several schemas would produce a package whose
  verdict means nothing.
- **A top-level directory that matches no schema's stem is refused the same
  way**, naming the schemas the contract does declare.
- **Two of the contract's schemas whose stems collide** (say
  `schema/order.schema.json` and `orders/order.json` — both stem `order`)
  refuse the pack outright, because a directory name cannot then say which
  one is meant. This is a problem with the CONTRACT, not with your source
  tree: the producer cannot fix it by rearranging files, and it needs a
  contract version whose schema entries have distinct stems.
- **`README.md` and `index.json`/`index.ndjson` are role-recognized only at
  the source root.** A `README.md` placed *inside* `order/` is not treated
  specially — everything under a matched schema directory becomes a
  `dataset` entry checked against that schema, so a stray README or index
  file nested under a schema directory will fail conformance rather than be
  classified as documentation. Keep them at the top level.
