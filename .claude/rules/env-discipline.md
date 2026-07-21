---
paths:
  - "**/.env*"
description: Env discipline — schema public and committed, secrets encrypted and never plaintext (loads only when env files are touched)
mate_synced: v0.98.0
---
<!-- cites: [code/env.md] -->

# Env discipline — the schema is public, the secrets never are

Loaded when touching `.env*` files. On every env/config change:

- **Schema and secrets never mix.** The schema (what vars exist, with safe defaults) is committed and public — the SSOT of configuration; the production values are encrypted at rest and never touch the repo in plaintext, not even "temporarily".
- **Every new var enters through the schema first** — name, safe default, comment — then gets its real value through the project's secret channel; a var that exists only in a live `.env` is configuration nobody can audit or reproduce.
- **Layering is by precedence, not by copy-paste:** env var > local `.env` > project defaults > base defaults — never hardcode at a lower layer what a higher one owns.

Full treatment: the env doctrine (`.mate/doctrine/`); the schema dir location and the secret channel are project values (`.mate/config.yaml` `env.schema_dir` + the project's own convention).
