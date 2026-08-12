# Data exchange — every flag, and how a source directory maps to a contract

> **Answers:** what each flag on `a2a data pack`, `deliver`, `fetch` and
> `verify` DECIDES (not merely that it exists), the exit-code contract, and
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
