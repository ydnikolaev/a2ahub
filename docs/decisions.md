# Decisions

<!-- Seeded once by mate (structure standard: .mate/doctrine/code/structure.md).
     This file is YOURS: mate will never touch it again. Role: the decision/ADR
     log — why things are the way they are. One dated entry per decision;
     never retro-edit an entry, supersede it with a new one. -->

> Plan-level decisions live in the architecture plan's §17 (D-###) — that log
> has precedence. This file records **implementation-repo** decisions the plan
> deliberately leaves to the implementers.

## ADR-001 — Go package layout & import boundaries (2026-07-21)

**Context.** The plan mandates one binary (`a2a`) whose core packages are
reused by every surface — CLI, MCP, space CI, future hub (§7.1, R-004,
D-011: one validation engine, five invocation points). It specifies behavior,
not Go layout. Coder agents need hard boundaries to work in parallel without
architectural drift.

**Decision.** Normative layout of the product repo:

| Path | Owns | May import |
|---|---|---|
| `cmd/a2a/` | wiring only — the single DI point | anything under `internal/` |
| `internal/artifact/` | artifact model: IDs (§3.3), md+YAML frontmatter parse/serialize, digests (§5.7) | stdlib + ADR-002 deps only |
| `internal/schema/` | embedded product schemas (envelope/event/manifest/consumes, §5.1) + version handling | `artifact` |
| `internal/fold/` | lifecycle engine: transition tables (§3.4), fold rules (§3.5). **Pure** — no I/O, no git | `artifact` |
| `internal/validate/` | THE one validation engine: schema / referential / lifecycle / policy classes, machine-readable codes (§5.5) | `artifact`, `schema`, `fold` |
| `internal/host/` | host adapter interface + GitHub impl: PR funnel mechanics, checks, reviews (§4.5, D-019) | `artifact` |
| `internal/space/` | space layout (§4.2), manifest, mirror clones, the write funnel (validate → ephemeral branch → PR, D-002) | `artifact`, `schema`, `validate`, `host` |
| `internal/cache/` | local cache, read cursors, computed inbox/outbox queries (§7.2, D-018) | `artifact`, `fold`, `space` |
| `internal/template/` | embedded per-type templates — projections of schemas (§5.6) | `artifact`, `schema` |
| `internal/cli/` | OP-2xx verb surface — thin frontend | core packages above |
| `internal/mcp/` | MCP façade over the same core (§7.7, R-018) | core packages above (never `cli`) |
| `schemas/` | SSOT JSON Schema sources + golden fixtures (T1; = plan Appendix B examples) | data, embedded via `internal/schema` |
| `space-template/` | space repo scaffold + V3 CI workflow | data |

Rules: core packages never import `cli`/`mcp`; `fold` and `validate` are pure
(no git/network — only `space`/`host` touch the outside world); the machine
error-code registry is **authored as data in `schemas/`** (SSOT, versioned
with the schemas) and **embedded/served by `internal/validate`** (the only
code surface that reads it); log-or-return per
[.claude/rules/go-conventions.md](../.claude/rules/go-conventions.md).

**Consequences.** Phase footprints in the v1-min epic map 1:1 to these
packages, so file-disjoint parallel waves are safe; the hub (v2) mounts
`fold`/`validate`/`schema` unchanged (D-011/D-012); moving a concern across
these boundaries requires a new ADR, never a drive-by.

## ADR-002 — Approved third-party dependencies, v1 core (2026-07-21)

**Context.** go-conventions says stdlib-first and makes every new module a
lead-level decision. The plan's normative formats make three concerns
impossible on stdlib alone: YAML (artifact frontmatter, event files,
manifests — D-009), JSON Schema 2020-12 validation (§5.1), and ULID event
IDs (§5.2.2). Hand-rolling any of these is correctness risk with zero
product value.

**Decision.** The approved dependency set for the v1 core is exactly:

| Module | For | Used by |
|---|---|---|
| `gopkg.in/yaml.v3` | YAML parse/serialize | `artifact`, `schema`, `space` |
| `github.com/santhosh-tekuri/jsonschema/v6` | JSON Schema 2020-12 engine | `validate` (via `schema`) |
| `github.com/oklog/ulid/v2` | ULID mint/parse for events | `artifact` |

Test-only helpers stay stdlib (`testing`, golden files). Anything beyond
this table — including git/GitHub client libraries for `host`/`space`
(default posture: shell out to system `git`; GitHub REST via `net/http`) —
requires a new ADR entry before it enters `go.mod`.

**Consequences.** ADR-001's `internal/artifact` row reads "stdlib + ADR-002
deps"; coder-agent briefs keep the "no new dependencies" invariant — the
allowlist above is already in `go.mod` terms the lead's, not the wave's.
