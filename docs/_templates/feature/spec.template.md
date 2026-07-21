# <Feature / Phase Name> — Specification

> Unified spec template. Keep only the track section (§T*) matching your **Track** below; delete the rest.
> Tracks: **cli** (§T1) · **schemas** (§T2) · **space** (§T3) · **docs** (§T4) · **ci** (§T5) · shared sections (§0, §5–§11) always apply.
> **Describe contracts as tables — do NOT paste Go/shell/YAML implementation code.** Implementation is the implementor's job.
> Acceptance criteria (§8) are agent-testable. Deviations during impl → §11 Amendments (append-only).

**Slug**: `<slug>`  ·  **Track**: cli | schemas | space | docs | ci  ·  **Status**: draft
**Created**: YYYY-MM-DD  ·  **Owner**: <owner>
**Footprint**: `<dir/module 1>`, `<dir/module 2>` — <one clause; the files this phase touches. A lead derives file-disjoint parallel waves from this + the tracker DAG>

---

## 0. User stories

> 5–10 stories written BEFORE the spec. Referenced as `US-N` in §8.

| ID | User story |
|----|------------|
| US-1 | As a <role>, I want <action>, so that <outcome> |

## 0.5 Required domain knowledge

> Docs to read before implementing this spec — link them; do not restate their content here.

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| `<domain>` | [<path/to/doc.md>](<relative/path.md>) | <what to pay attention to> |

---

## Track-specific contract (§T*) — keep only the section matching your Track above; delete the rest.

### T1. CLI surface (track: cli)

> The `a2a` binary's commands/flags this phase adds or changes.

| Command | Flags | Input | Output | Notes |
|---------|-------|-------|--------|-------|
| `a2a <verb>` | `--flag` | <arg / stdin> | <stdout shape / exit code> | <e.g. reads `.a2a/config.yaml`> |

### T2. Schema fields (track: schemas)

> JSON Schema is the SSOT for artifact/wire shape. Describe fields as a table — the schema file itself is the implementation, not prose here.

| Field | Type | Required | Constraints | Example |
|-------|------|----------|-------------|---------|
| `id` | string | yes | pattern `^X[A-Z]-[0-9]+$` | `XC-042` |

### T3. Space template layout (track: space)

> Changes to the git-repo template an exchange space is scaffolded from.

| Path | Purpose | Generated or static |
|------|---------|----------------------|
| `<path>` | <what it holds> | generated / static |

### T4. Docs changes (track: docs)

| Doc | Section | Change |
|-----|---------|--------|
| `<path>` | `<heading>` | <what changes and why> |

### T5. CI pipeline (track: ci)

| Workflow | Trigger | Steps | Gate it enforces |
|----------|---------|-------|-------------------|
| `<workflow>.yml` | <push / PR / schedule> | <steps> | <what it must catch> |

---

## 5. Existing patterns to reuse (anti-duplication)

> Name the patterns/utilities the implementor must reuse instead of re-rolling. Grep first.

- [ ] <pattern — e.g. shared validation helpers, existing CLI flag-parsing conventions>

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| <command / schema / workflow> | <behavior> | <invalid input, missing config, empty set> |

## 7. Schema / contract delta

> Exact JSON Schema (or other cross-boundary contract) changes this phase introduces.

```yaml
# schema / contract delta
```

## 8. Acceptance criteria

> Written by the spec author; the implementor does NOT modify them. Each is agent-testable with a concrete value.

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-1 | <concrete observable> | `<command / test / view>` |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | <can variants be added without core edits?> |
| Coupling | <hard (shared state) vs soft (IDs/events)?> |
| Migration path | low / medium / high |
| Roadmap conflicts | <in-flight features that interact> |

## 10. Implementor entry point

> Execute as a single spec, or as one wave of an orchestrated epic. TDD default; framework-first; log-or-return.
> Full loop (README/tracker/specs shapes, lint gate): [docs/features/README.md](../../features/README.md).

## 11. Amendments

> Append-only. When the shipped reality deviates from this spec, record it here AND amend any downstream spec.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
