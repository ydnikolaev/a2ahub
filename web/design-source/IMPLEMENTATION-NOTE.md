# Implementation note — tokens, components, data projections

Companion to `README.md`. This is the short note the brief asks for: token names, component names,
what is shared across surfaces, what is surface-specific, and the data projections the dashboard
design needs from the Go read model.

## 1. Token layers

Three layers, all in `tokens.css`. Only the semantic names appear in components.

1. **Primitive** — the raw colour values, radii (8 / 10 / 12 / 14 / 18 / 20 px), shadow recipes and
   the two type families. Never referenced directly by a component.
2. **Semantic** — the names components use:
   * surfaces: `--canvas`, `--page`, `--surface`, `--sink`, `--inverse`, `--inverse-2`, `--inverse-3`
   * lines: `--border`, `--border-strong`, `--hairline`, `--inverse-line`
   * text: `--text`, `--body`, `--muted`, `--faint`, `--logo-dim`, `--on-inverse`, `--on-inverse-2`,
     `--on-inverse-muted`, `--on-inverse-faint`, `--on-dark`, `--on-solid`
   * state families, each with solid / tint / ink / strong / line members:
     `--teal-*` (agent path, links, focus), `--attention-*` + `--ochre` (human gate, drift, staleness),
     `--blocking-*` + `--red` (literal blockage or failure only), `--healthy-*` + `--green` (verified),
     `--plum` (decision), `--selection`, `--focus`
3. **Component** — expressed as the small set of recurring recipes rather than extra variables:
   card = `--surface` + radius 14–18 + `0 1px 2px rgba(33,30,23,.06)`; raised panel adds
   `0 20px 44px -30px`; inset ring = `inset 0 0 0 1px var(--border)`; selected = `0 0 0 2px var(--focus)`.

**Themes.** `:root` is light; `html[data-theme="dark"]` re-points the same names. Hue is preserved so
semantics survive the flip; only lightness changes. The header switch writes `a2a-theme` to
`localStorage`. Astro, the Go renderer and a future Nuxt UI ship the same two sets.

**Status rule.** A status is never colour alone: it always carries a word (`current`, `behind`,
`sunset`, `retired`, `missing`, `dangling`, `blocking`, `p1`…`p4`) and, on the map, a line style
(solid = contract dependency, dashed = open exchange) plus an arrowhead for direction.

## 2. Component inventory

### Shared by all three surfaces (public site, Go renderer, future Nuxt)

| Component | Contract | Notes |
|---|---|---|
| `StatusPill` | `text`, `tone` (`healthy` `info` `attention` `blocking` `blockingStrong` `neutral` `outline`), `size` | the only status chip; word + tone, never colour alone |
| `TypeBadge` | `type` (one of the eight artifact types), `size` | always shows prefix or type text |
| `FactTable` | `rows: [{label, value, mono?}]` | label / value evidence table |
| `ArtifactRow` | `type`, `title`, `status`, `tone`, `time`, `from`, `to`, `fromCode`, `toCode`, `meta`, `fromSelf`, `toSelf`, `selected`, `onSelect` | human title first, IDs and evidence after |
| `ArtifactDetail` | `det` (projection object) | envelope, folded state, events, flags, references, callout |
| `NetworkMap` | `data`, `space`, `spaceFilter`, `heading`, `mode`, `wide`, `showMatrix`, `showSpaceTabs`, `collapseExtras`, `onSpaceChange`, `onOpenContract`, `onOpenItem` | the exchange map; see §4 |
| `LinkDetail` | `det: {title, kindLabel, lead, note?, rowsLabel, rows[], rows2Label?, rows2[], close}` | the map's detail modal body |

### Surface-specific

| Component | Surface | Notes |
|---|---|---|
| `SiteHeader` | public site only | nav, language popover, theme switch, Ask-an-agent menu with every machine-readable action; sticky at `top:0` |
| `SiteFooter` | public site only | product statement, three link columns, the loop visual, release/licence line |
| dashboard shell | local dashboard only | sticky header with system chip, space chips with participant faces, adaptive nav that folds overflow views into **More**, update-required banner, footer snapshot line |
| docs shell, changelog list, roadmap horizons, install blocks | public site only | each projects a single SSOT source |

### Interaction rules the implementation must keep

* Adaptive nav: measure the nav's own box, show the leading run of views that fits, fold the rest
  into **More**; the active view is always visible; re-measure after each render so it expands again.
* Space chips carry participant faces. The prototype renders monograms because it makes no network
  requests; production substitutes the participant's GitHub avatar and keeps the monogram fallback.
* The map's control bar is sticky and stacks **below** whatever sticky app header the host page has
  (measured at runtime, not hard-coded); its full-width strip appears only while pinned.
* Hover highlights (edge, badge or system → its strands and endpoints, everything else dimmed to 12–40%),
  click pins the selection and opens the detail modal. Escape and backdrop close it.

## 3. Data projections the dashboard needs

The browser must compute nothing protocol-shaped — no fold tables, authorization, severity, drift,
deadline policy or validation. The Go assembler should extend `a2a html` data with:

* per-item `neededBy`, `pendingMerge`, `syncStale`, actionable `reasons[]`, `severity`;
* `artifactDetails[]` — raw body, folded state, digest, events, flags, reference resolution facts;
* `threadViews[]` — committed vs declared order, opener, participants, interleaved transcript,
  open items, legal next actions with the systems allowed to perform them, unresolved references;
* `contractEdges[]` with per-consumer `pinnedMajor`, `pinnedVersion`, `pinnedState`,
  `providerVersion`, `availableMajors`, `drift`, `sunset`, `successor`;
* `exchangeEdges[]` with `count`, `maxPriority`, `blocking`, `maxStale`;
* `contracts[]` with the rolling version window (`versions[] {version, state, successor, deprecationID}`),
  `consumers[]`, `codeBacked`, `generatedTool`, `sourceDigest`;
* `spaces[]` with the four independent compatibility axes (`schemaVersion`, `minBinaryVersion`,
  `workflowVersion`, `workflowRef`) plus `revision`, `syncAge`, `stale`, `readable`;
* `unavailable[]` — every scope that was **not** evaluated in this static read, with its reason.

Facts are `canonical`, `derived` or `unavailable`. Unavailable renders as a scoped
"not evaluated in this static view" block — never green, zero or blank.

## 4. The exchange map, in one page

`NetworkMap` is the one non-trivial layout. `buildCanvas()` in `prototype/NetworkMap.dc.html` is the
reference implementation; the geometry is deterministic and needs no DOM measurement beyond the
container width:

* canvas width = measured box width (min 780); 16px inset on all four sides;
* space panel width = `clamp(420, 0.46 × W, 840)`, right-aligned, and the corridor between the hub
  card and the panel is never allowed below 180px (the panel shrinks, not the corridor);
* inside a panel: 24px padding, header height derived from the meta line's wrapped height,
  member cards 62px tall with a 14px gap, and a 64–130px right gutter for intra-panel routes;
* the hub ("you") card grows with the number of strands landing on it (`62 + 15 × strands`, capped);
* self ↔ member strands are cubic béziers fanned across the hub card edge, sorted by target y;
  member ↔ member strands are orthogonal routes in the panel gutter, 24px apart;
* badges (drift word or open-item count) are placed by sampling points along their own strand and
  choosing the first candidate that collides with nothing already placed, cards included;
* the table under the map lists every edge with the reason it exists — the required non-graph fallback.

## 5. Quality gate before shipping

* No hosted service, database, public endpoint or purchasable tier presented as current.
* Contract drift is computed on the consumer's pinned major line; code-backed metadata is never
  called verified or current; missing validation context is unavailable, not green.
* v0.16.3 owns the 50/50 live-matrix claim, with its coverage qualifier attached.
* Every graph edge explains why it exists; every thread exposes causal order and whose move it is;
  every contract exposes its rolling window and the consumer pin.
* Priority, blocking, lifecycle state, human gate, deadline and stale snapshot never collapse into
  one badge.
