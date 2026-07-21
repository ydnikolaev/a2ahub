# §11 Observability & Visualization

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).

## 11.1 Hub dashboard (R-009)

Audience: all participants (humans primarily; agents use APIs). Access:
behind hub token auth (10.5) — never public in v1.

Views (all fed by OP-106/107/108/110 + SSE for live updates):

| View | Content |
|---|---|
| **Graph** (landing) | systems as nodes; edges = open exchanges (weighted by count, colored by max priority/staleness) + contract dependencies (consumer→provider, marked by version currency); live pulse on new events |
| Space board | per space: open items by state lane (submitted/acked/accepted/in-progress/responded), staleness highlighted, gates pending |
| Thread detail | conversation timeline: artifacts + events + folded state + validation flags |
| Contracts | catalog: provider, version, folded lifecycle state (published/deprecated/retired), code-backed?, registered consumers, deprecations with sunset countdowns |
| Health | sync lag per space, V4 flags, fold errors, webhook status (OP-110/111) |

Design direction (shared with 7.6): modern technical aesthetic, light/dark,
information-dense but calm; the graph is the product's face — it must make
"who exchanges what with whom" legible in one glance (wish #3). Detailed
visual design is an implementation-phase deliverable with its own review;
this plan fixes only the content and interaction inventory above.

## 11.2 Local HTML (R-008)

Per 7.6: same design language, scoped to one system's perspective, static,
generated from local cache — works fully offline and with no hub.

## 11.3 Notification routing & noise budget

Matrix is hub config (6.5); defaults:

| Event | Agent statusline | Human chat |
|---|---|---|
| new inbound artifact for your system | ✔ | ✘ |
| p1 or blocking inbound | ✔ (severity exit code) | ✔ |
| your outbox: response arrived / declined / disputed | ✔ | ✘ |
| gate pending (G1–G5) | ✔ | ✔ |
| deprecation targeting you | ✔ | ✘ |
| new version of a contract in your `consumes.yaml` published (any bump) | ✔ | ✘ |
| validation flags on your section | ✔ | ✘ |
| staleness threshold crossed on your item | ✔ | weekly digest |
| `note` reminder on an item you hold | ✔ | ✘ |
| everything else | ✘ | ✘ |

All rendered artifact-controlled strings pass the 10.8 sanitization rules
(dashboard, local HTML, statusline alike).

Noise rule: a channel that cries wolf gets ignored by agents and humans
alike; default-off is the stance, opt-in per space via manifest/hub config.

## 11.4 Hub self-observability

OP-111 metrics: per-space last-fetch age, webhook counter, fold error count,
notification queue depth, DB size. Minimum alerting (6.6): webhook silence
anomaly, fold errors growth, disk. Logs: structured, request-scoped; no
artifact bodies in logs (classification hygiene, 10.4).
