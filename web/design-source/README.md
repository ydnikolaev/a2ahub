# a2ahub — design v4 → implementation package

Everything an implementing agent needs to build the v4 design for real. Read this file first,
then `IMPLEMENTATION-NOTE.md`.

```
export/
  README.md                 this file — what is here, what the stack is, where each page goes
  IMPLEMENTATION-NOTE.md    tokens, component inventory, shared vs surface-specific, data projections
  tokens.css                the semantic token layer (light + dark) + page shell + focus/motion rules
  prototype/                the runnable design source: 10 pages, 9 components, the fixture
```

## 1. Two implementation targets — do not blur them

| Surface | Stack that ships today | What to build from this package |
|---|---|---|
| Public site | Astro `^7.1.1`, static output, `build.format: "file"`, GitHub Pages, origin `https://ydnikolaev.github.io`, base `/a2ahub`, no runtime or third-party requests | Astro components with the same token and component names; every link and asset specimen base-aware |
| Local dashboard | one Go command emits one self-contained, read-only HTML file with embedded data and no network | semantic HTML + `tokens.css` inlined + small vanilla JS; data written into the file, never fetched |
| Future SaaS UI | Nuxt is an owner-supplied future direction, not a shipped plan | keep token names, states, component boundaries and data contracts re-expressible as Vue components; generate no Nuxt code now |

## 2. Page → production route

| Prototype file (`prototype/`) | Production target | Notes |
|---|---|---|
| `12-design-system-v4.dc.html` | internal, not shipped | token inventory, every component and state, patterns, rules |
| `13-public-home-v4.dc.html` | `/a2ahub/index.html` + `index.md` | hero proves the operating model with real dashboard parts; the map section embeds `NetworkMap` |
| `16-docs-v4.dc.html` | `/a2ahub/docs.html`, `/a2ahub/docs/<section>.html` | section nav, search, TOC, anchors, source note; bodies the site does not own show the honest injection block |
| `20-dashboard-example-v4.dc.html` | `/a2ahub/dashboard.html` | public framing, synthetic label, same components and same fixture as the local dashboard |
| `17-security-reliability-v4.dc.html` | `/a2ahub/security.html` + `/a2ahub/reliability.html` | one prototype page, two routes, section boundaries preserved |
| `15-changelog-v4.dc.html` | `/a2ahub/changelog.html` | projected from `releasenotes/*.yaml`; ends at published v0.16.3 |
| `19-roadmap-v4.dc.html` | `/a2ahub/roadmap.html` | shipped / next proposal / exploring; P5 is proposal-only with its open gates visible |
| `18-install-v4.dc.html` | `/a2ahub/install.html` | shell install, agent-led Sporo seed with copy / copied / failed / unavailable states |
| `21-not-found-v4.dc.html` | `/a2ahub/404.html` | plus head and route conventions the build owns |
| `14-local-dashboard-v4.dc.html` | output of `a2a html` | 9 views: Overview, Work, Threads, Contracts, Network, Spaces, Integrity, Versions, Guide |

## 3. How to read the prototype files

`prototype/*.dc.html` are Design Components: a template plus a small logic class, loaded by
`support.js`. Open any of them in a browser to see and click the real thing — nothing needs a build.
They are the **design source of truth**, not code to ship: lift the markup structure, the inline
values, the layout maths and the interaction logic, and re-express them in Astro / Go templates / Vue.

The logic classes are plain JavaScript and read like specifications — `NetworkMap.dc.html`
in particular carries the whole exchange-map layout algorithm (panel geometry, strand routing,
badge placement, hover and selection rules) in `buildCanvas()`.

## 4. Engineering constraints that are part of the design

* No external runtime dependencies, CDNs, analytics, trackers, remote fonts, images or icon kits.
  Fonts are self-hosted in production (the prototype links Google Fonts only for convenience —
  do not carry that over).
* Icons and diagrams are inline SVG or CSS shapes; no vendor artwork.
* Every artifact title, description, release line and ID is untrusted data:
  insert it with `textContent`. The sole rich-text exception is artifact
  Markdown rendered server-side through safe GFM; raw HTML is escaped,
  dangerous URLs are rejected and remote images are removed before the
  renderer-owned projection reaches the dashboard.
* The local dashboard stays fully useful opened as one file with no network.
* No write actions on the dashboard. "Copy command" and "open source record" are allowed;
  approve / merge / retire buttons are not.
* Layout is robust at 1440, 1024, 768 and 390 CSS px — the prototype is measured at all four.
* WCAG AA contrast, visible keyboard focus, logical tab order, named controls, non-colour status
  cues, `prefers-reduced-motion`. No essential information is hover-only: hover highlights, click
  commits and opens the detail.
* One SSOT per fact: command lists, lifecycle tables, artifact fields, release notes and the
  Sporo seed are projected at build time, never hand-copied.
* No login, pricing, waitlist, demo capture, testimonials, logos, uptime or certification claims.

## 5. Data

`prototype/demo-data.json` is the required fixture (`meta.schema: "a2a-design-demo/v3"`,
`meta.synthetic: true`, 4 spaces, 10 systems, 12 contracts, 15 contract edges, 20 exchange edges,
6 threads, 11 inbox, 8 outbox, 4 flags, 6 thread views, 8 artifact details).
`prototype/demo-data.js` sets `window.A2A_DEMO` — that is the pattern the Go renderer must use.
Pages try the JSON first and fall back to the embedded copy.

Fixture inconsistencies are **reported, not repaired**: a wrong `meta.schema` renders an error
panel instead of a dashboard.
