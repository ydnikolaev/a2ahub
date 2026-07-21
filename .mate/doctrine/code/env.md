<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Env doctrine — layered, modular, secret-safe configuration

> **Thesis.** Configuration has two halves that must never mix: the **schema**
> (what vars exist, with safe defaults) is public, committed, and the SSOT; the
> **secrets** (their production values) are encrypted at rest and never touch the
> repo in plaintext. Keep them apart and config becomes auditable, layerable, and
> safe to share — the same SSOT discipline the validation doctrine applies to
> derived artifacts, pointed at environment.

The env surface is the **third face of the uniform interface** (see
[interface.md](interface.md)): make-ABI, operator CLI, and env layout are the
three things an agent must be able to assume about any project.

---

## 0. When to apply (and when not)

Apply the moment a project has **more than one environment** (dev + anything) or **any
secret at all** — the two conditions that create the schema/secrets split this doctrine
exists to police. That is nearly every deployed project, and the cost of adopting it late
is the one cost that cannot be paid down cheaply: a secret in git history is a secret
forever.

The **weight tiers, not the principles**. A single-service project with three variables
needs the split (§1/§2) and one parity check (§4); it does not need a layered precedence
chain (§3) or a per-environment secret store — that machinery earns its place when
environments multiply. Adopt §1/§2 always; adopt §3 when the second environment appears;
adopt §5's single-reader boundary the moment a second module starts reading env directly.

Do **not** apply to a script with no deployment and no secrets: a hardcoded default in a
throwaway tool is not a config layer, and building one is the over-application §0 exists
to prevent.

## 1. Schema is the SSOT — public, committed, exhaustive

Every var a service reads is declared in a committed `*.example` schema file with
a **safe, non-secret default**. The schema is the single source of truth for
*what configuration exists*; nothing reads an env var that the schema doesn't
declare. *(axon: `deploy/env/_common/.env.<svc>.example` + per-project overrides;
the reader is `pkg/config/config.go`'s `getEnv()`.)*

The schema file **never holds a secret value** — it holds the var's *name* and a
dev-safe default or an empty placeholder. A real credential in an `.example` file
is the single most common env leak; the parity gate (§4) plus review guard it.

---

## 2. Secrets are encrypted at rest — age/SOPS, never plaintext

Production secret *values* live encrypted, committed as `*.sops` (age-encrypted
via [SOPS](https://github.com/getsops/sops)), decrypted only at deploy/runtime
with the age key held outside the repo. Benefits, all of which the doctrine wants:

- **Committed + versioned** — secrets are diffable and rollback-able like code,
  without ever being readable in the clone.
- **Per-recipient** — age lets you encrypt to multiple keys (CI, each operator)
  without a shared password.
- **Modular** — one `.sops` file per service × project, so a leak or rotation is
  scoped, not global.

Plaintext secrets **never** enter the repo, the shell history, or a log line. A
`.env.*` with real values is `.gitignore`'d; only `*.example` (schema) and
`*.sops` (encrypted) are committed.

---

## 3. Layered precedence — modular composition

Resolution order, highest wins:

```
env var  >  .env (local)  >  project defaults  >  base defaults
```

**Never hardcode at level 4 what belongs at level 1–2.** A per-deploy URL hardcoded
as a base default is the bug; it belongs in the env layer, injected per
environment. *(Scar: a CSP origin that fell back to `localhost` because a
per-deploy URL was baked at build time instead of injected at runtime.)*

The schema itself is **modular and layered the same way the knowledge model is**:
a shared `_common` schema + per-project overrides — `_common` is "core", the
per-project file is "project". Same composition shape, different domain.

---

## 4. Parity is gated — an instance of the validation doctrine

The schema⇄code relationship is a **parity invariant**, so it gets a gate (the
"Parity" row of the validatable-dimension taxonomy):

- **Bidirectional `env-check`**: every var the code reads (`getEnv("FOO")`) is
  declared in the schema, **and** every schema var is read somewhere — neither
  side drifts. Fail-closed: a var read but undeclared **fails the build** with an
  actionable message (`FOO is read by config.go but missing from .env.api.example`),
  never a silent default at runtime. *(axon: `make env-check`, AP#9.)*
- **The local-dev root `.env` is generated**, not hand-assembled: a generator
  composes `_common` + the active project's schema into a root `.env.example`,
  banner-stamped `GENERATED`, drift-gated. This is validation-doctrine §1 (rung 2:
  *generate*, don't hand-write) applied to env. *(axon: `generate-root-env.sh`.)*

Env is therefore not a doctrine *separate* from validation — it's validation
applied to configuration. The parity gate and the generated root-env are the
proof.

---

## 5. One reader, one boundary

Exactly **one** module reads the environment (`config.go` / `nuxt.config`'s
`runtimeConfig`); everything else receives typed config from it. No scattered
`os.Getenv` / `process.env` reads across the codebase. This is the "validate only
at system boundaries" rule pointed at env: the config layer is the boundary, it
validates once, and the rest of the code trusts typed values.

---

## Instantiation seam

What a project swaps into this frame (this section was titled *"what's agnostic vs
project"* until v0.77.0 — same content, the name the corpus's shape contract uses):

| Layer | What | Synced? |
|---|---|---|
| **Principle** | this doctrine (schema/secrets split, layering, parity-gated, one reader) | `mode:sync` |
| **The `env-check` gate** | the parity-check logic, reading `env_schema_dir` from `.mate/config.yaml` | `mode:sync` (path via config) |
| **The schema + `.sops` files** | the project's actual vars and secrets | **not synced** — project's own data |

The directory layout is **project data behind a config key** (`env_schema_dir`),
never a mandated path — a project without a `deploy/` tree puts its schema
elsewhere and points the key at it. (Cardinal rule: the doctrine carries the
principle; `deploy/env/_common` is axon's value, not the contract.)

---

## Anti-patterns

| ❌ | ✅ |
|---|---|
| Real secret value committed in an `.example` file | schema holds names + safe defaults only; values in `*.sops` |
| Plaintext `.env` with prod secrets committed | `.gitignore` it; commit `*.sops` (age-encrypted) |
| Per-deploy URL hardcoded as a base default | inject via the env layer per environment |
| `os.Getenv` / `process.env` scattered across the code | one reader at the config boundary; typed values downstream |
| Code reads a var the schema doesn't declare | `env-check` fails the build (fail-closed parity) |
| Root `.env` hand-edited | generated from `_common` + project schema, drift-gated |
| Doctrine mandates `deploy/env/` for every project | layout behind `env_schema_dir`; principle, not path |

---

## Porting checklist

- [ ] Every read var is declared in a committed `*.example` schema with a safe default.
- [ ] Secrets are age/SOPS-encrypted `*.sops`; no plaintext secret is committed.
- [ ] Precedence is env > `.env` > project > base; no per-deploy value baked at build.
- [ ] `env-check` runs in `make check`, bidirectional and fail-closed.
- [ ] The local-dev root `.env` is generated + drift-gated, not hand-assembled.
- [ ] Exactly one module reads the environment; `env_schema_dir` is set in config.

---

## Cross-links

[validation.md](validation.md) (env-check and the generated root-env
are instances of it) · [interface.md](interface.md) (env is the third face of the
uniform interface) · the deploy doctrine (`deploy.md`, planned — secrets are decrypted at deploy time)
· CSP-origins agent-scar (per-deploy URL must be runtime-injected, not built in).
