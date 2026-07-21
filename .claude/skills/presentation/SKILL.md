---
name: presentation
description: Create a doc-presentation — a self-contained HTML scroll-document in the fleet-wide design standard (tokens + skeleton + voice). Use when the user asks for a "док-презентация", "presentation", a styled HTML overview/pitch/report for humans.
user-invocable: true
argument-hint: "[topic + audience; optionally destination and language]"
requires_capabilities: [read-files, edit-files]
allowed-tools: Read Edit Write
cites: [code/documentation.md, code/structure.md]
mate_synced: v0.98.0
---

# presentation

> **Thesis.** A doc-presentation is a **self-contained HTML scroll-document in
> the ONE fleet-wide standard** — same design tokens, same skeleton, same voice,
> in every project. The standard has exactly two homes: the design file
> (`.mate/design/presentation.css`, synced from the mate SSOT) and this skill
> (structure + writing rules). Never invent a new design, never fork the
> standard locally — a presentation is recognizable across the whole fleet, and
> a change to the standard is made ONCE upstream and inherited by every future
> document on the next pull.

## When to run

- The user asks for a "док-презентация" / "presentation" / a styled HTML
  overview, pitch, report, or explainer meant for a human reader (partner,
  operator, stakeholder).
- NOT for: plain working notes (write Markdown), specs (the feature's spec home
  owns those), or anything a code comment / README already covers.

## Procedure

1. **Audience + goal first.** One sentence each, from the ask: who reads this,
   and what they should understand or decide after reading. These drive the
   section plan and the tone — a partner overview and an internal data deep-dive
   are the same standard, different sections.

2. **Destination — one home, every project.** A doc-presentation lands in the
   **presentations home**: the project's `paths.presentations` from
   `.mate/config.yaml`, defaulting to `docs/presentations/` per the structure
   standard. Flat, no per-feature subdirs — a kebab-case basename carries the
   topic and the audience or genre (`reports-team-overview.html`,
   `getvisa-partner-overview.html`). One home is the point: a reader opens one
   directory and sees every presentation the project has, in every project.
   A user-named path still wins when the user names one.

   **Carve-out:** a page that is part of a *dated or generated record* — the
   day's progress report page, which the `create-report` skill composes into
   `<reports home>/<dd.mm.yyyy>/` — stays in that record's own home. This rule
   governs standalone presentations, not pages a record owns.

3. **Inline the design SSOT.** Read `.mate/design/presentation.css` and inline
   it VERBATIM (minus its provenance banner) at the top of the document's single
   `<style>` block. Self-containment is a hard constraint: the file must render
   opened as `file://`, offline — no `<link>`, no CDN, no web fonts, no
   JavaScript, no external requests, no images (visuals are pure CSS). If the
   design file is missing, STOP and say so (`mate pull` lays it) — do not
   substitute your own tokens.

4. **Compose per-document CSS below the inlined standard.** Build components ON
   the tokens (`var(--accent)`, `var(--card)`, …) with the signature constants:
   radius 14px cards / 100px pills / 6px inline highlights, 1px `var(--line)`
   borders, ~64px section rhythm, mono labels uppercase with 0.06–0.16em
   tracking. Compose freely — the pattern menu below names the proven shapes.

5. **Write the content** per the skeleton and writing rules below.

6. **Sidecar `.md`** — ONLY if the user asked for one: same basename, same
   destination, the same content as a plain Markdown document (not a dump of
   the HTML).

## Document skeleton

- `<!doctype html>`, `lang` from the language policy below, `<meta charset>` +
  viewport, title `<project/brand> — <topic>: <audience or qualifier>`.
- `body > .wrap` (add `.wide` only for table-heavy documents), then:
  `header.hero` (`p.brand` mono label → `h1` → `p.lede` → `.hero-meta` chips,
  key chip `.on`) → sections, each opened by a numbered `p.eyebrow`
  (`01 · Label`) + `h2` + `p.big` lead, separated by `div.route > span`
  dividers → `footer` (left: doc name; right: date).
- Exactly ONE honesty `.note` per document — the candor block that states
  limits, uncertainty, or what is not promised. A presentation that promises
  everything is not in the standard.

## Pattern menu (proven shapes; compose on the tokens, keep the class names)

- **Core (css-backed, always available):** hero, chips, route divider, eyebrow,
  big/dim text, `em.q` inline example highlight, callout with `.tag`, honesty
  note, footer.
- **Shared (rebuild per-document as needed):** `.flows > .flow` (mono `.dir`
  label column + `.what`); `.rules > .rule` numbered cards (CSS
  `counter(rule, decimal-leading-zero)` → 01, 02…); `.adv > .adv-item`
  border-separated advantage rows (`h3 > span.k` accent lead-in); `.tl` timeline
  (`.tl-when` + `.tl-rail`/`.tl-dot` + `.tl-body`, amber/accent state
  modifiers); `.tbl-scroll > table` (mono uppercase `th`, min-width for
  horizontal scroll).
- **By genre (one-offs, precedented):** verdict strips + status `.pill`s
  (legal-type); `.layers`/`.anatomy`/`.steps` (data-type); `.duo`/`.road`
  comparison and roadmap grids (narrative-type).

## Writing rules (the voice — as binding as the design)

1. **Impersonal register.** No «ты»; «вы» only in rare formal imperatives.
   «Мы» = the team behind the product.
2. **Thesis-first bold leads.** Key claims front-loaded in `<strong>`; each card
   or rule opens with a one-line bolded thesis, then the explanation. Headings
   are short declarative mini-theses (3–8 words); a set announces its size
   («Пять правил», «Три вывода»).
3. **Gloss every term inline on first use** — acronyms never stand bare:
   «GEO (оптимизация под ответы нейросетей)».
4. **Numbers are structured, never bare:** currency + qualifier («€90 ≈ $98, с
   датой курса»), ~/≈ for approximations, en-dash ranges («3–6 месяцев»).
   `.mono` is reserved for codes and paths, not prose numbers.
5. **Concrete example over abstraction** — a real scenario anchors each rule;
   verbatim queries/examples in `em.q`.
6. **One honesty note per document** (see skeleton) — state limits explicitly.
7. **Contrast constructions:** «не X, а Y», em-dash parallelism, medium
   sentences of one–two clauses.
8. **Plain human language** — no канцелярит, no unexplained jargon; if a
   sentence needs rereading, split it.

## Language policy

Default **Russian**. A project overrides via `.mate/config.yaml`:

```yaml
presentation:
  lang: en
```

An explicit per-ask language wins over both. The writing rules apply in any
language (register, thesis-first, glossing, structured numbers).

## Change policy (harness discipline)

The standard's two homes are upstream: `core/design/presentation.css` and this
skill in the mate SSOT. A design or voice change is promoted up
(`mate promote` / `/mate-promote`), released, and pulled — never patched into a
local copy (the synced copies are provenance-stamped and drift-gated). Existing
documents are artifacts of their time: a standard change restyles FUTURE
documents, nobody retro-edits shipped ones.

## Output

The `.html` file at the destination (plus the `.md` sidecar iff asked), and a
one-line report: destination, audience, language, and what to open.
