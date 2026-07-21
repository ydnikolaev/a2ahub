# §12 Corner-Case Catalog

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> Numbered CC-###; each states scenario → expected behavior (with the rule
> that mandates it). §13 maps every CC to a test; §14 ACs cite CCs. This
> catalog is append-only during implementation: new cases get new numbers.

## A. Document level

| CC | Scenario | Expected behavior |
|---|---|---|
| CC-001 | malformed frontmatter (broken YAML) | V2 refuses write; if merged anyway, V3 blocks, V4 flags (5.5) |
| CC-002 | schema-valid but wrong section (`from` ≠ owning section) | V2/V3 reject (authz class) |
| CC-003 | ID ↔ filename mismatch, or ID prefix ≠ `type` | reject (5.2) |
| CC-004 | duplicate ID (random collision or copy-paste) | V3 full-repo check rejects second occurrence; collision across spaces is legal (IDs are space-scoped; cross-space refs carry space ID, 4.3) |
| CC-005 | unknown envelope schema version (newer binary wrote it) | older binary: read-only + loud warning, refuses to write to that space until updated (5.4, 7.3) |
| CC-006 | oversized artifact (pathological body) | validator size limit (configurable, generous) → reject with guidance to use refs; protects tooling and reviews |
| CC-007 | non-UTF-8 / encoding garbage | reject at V2/V3 |
| CC-008 | `to` includes unknown system / system marked `left` | reject at V2; if membership changed after submit, V4 flags orphaned addressee (see CC-062) |
| CC-009 | multi-intent smuggling (question embedded in announcement body) | schema can't fully see prose (3.2 claims only structural checks); template guidance + skill discourage; recipient declines with `reason_code: split-required`; sender-side tooling then guides re-submission: parts drafted on the same thread, superseding the declined artifact, one batch submit (OP-220) |
| CC-010 | secret-pattern or forbidden payload detected | V2/V3 block, G5 override only, incident flow 10.7 |
| CC-011 | `blocking: false` without `interim_behavior` on requirement/work_request | schema reject (5.2) |
| CC-012 | clock skew: `created`/`at` in the future | V3 warns (tolerance window); fold order unaffected — commit order on `main` is authoritative, timestamps are display metadata (3.5 rule 1) |

## B. Lifecycle

| CC | Scenario | Expected behavior |
|---|---|---|
| CC-020 | event encodes illegal transition (respond on closed exchange) | fold ignores + flags (3.5 rule 2); event stays in history |
| CC-021 | event by unauthorized actor class (agent approves a decision) | fold ignores + flags (3.5 rule 3, G3) |
| CC-022 | two contradictory events near-simultaneously (accept + decline) | merge order on `main` wins (3.5 rule 1); loser becomes illegal-transition flag; parties see the conflict on the thread view |
| CC-023 | response to a cancelled/superseded exchange | fold flags the response as illegal-transition; the flag IS the record — no courtesy transitions exist; successor unaffected |
| CC-024 | supersession fork (two successors claim one predecessor) | V3 rejects second (linearity, 3.8) |
| CC-025 | supersession cycle | V3 rejects (graph check) |
| CC-026 | `needed_by` passed, target silent | no auto-transition; staleness surfaces in statusline/dashboard/weekly digest (3.4.3); escalation ladder 8.5 |
| CC-027 | dispute loop (verify-fail ×2) | protocol stop: 8.5 mandates human escalation; validator warns on 3rd dispute event |
| CC-028 | decline of a `blocking` item | sender's statusline flags at p1-equivalent severity; expected next: sender escalates or re-routes; nothing auto-happens |
| CC-029 | multiple partial responses, sender verifies some | per-response `verify` events fold each `XS`; parent stays `responded` until the sender's explicit `close` event (3.4.6 — the single closure model) |
| CC-030 | requirement satisfied by contract version later deprecated | requirement stays satisfied (historical truth); dashboard shows consumer-on-deprecated flag via 5.4 registry |
| CC-031 | withdraw after target already responded | legal (sender's right); response preserved; folded state `withdrawn`, flagged for visibility |
| CC-032 | decision approver system leaves the space mid-vote | remaining required approvers govern; manifest change (G4 review) is the audit trail; validator recomputes quorum from current manifest and flags the change on the thread |

## C. Transport & hub

| CC | Scenario | Expected behavior |
|---|---|---|
| CC-040 | missed webhook | reconcile ≤5 min (F-1) |
| CC-041 | duplicate/out-of-order webhooks | idempotent by SHA; fold by commit order (F-2) — arrival order irrelevant |
| CC-042 | hub down a day | zero durable loss; direct git flows keep working; catch-up collapses notifications into current-state (F-3) |
| CC-043 | hub DB lost | `hub rebuild` (F-4) |
| CC-044 | space repo unreachable from hub | space marked stale, others unaffected (F-5) |
| CC-045 | force-push / history rewrite on space | full re-index + operator alert (F-7); spaces are append-only, reverts not rewrites — the sole exception is a pre-announced sanctioned redaction (10.7, CC-098) |
| CC-046 | two agents of one system push concurrently (non-conflicting files) | git merge naturally; file-per-artifact/event makes conflicts rare by construction (4.2) |
| CC-047 | true git conflict (same artifact edited twice) | normal git resolution by the owning team; validator re-runs post-merge; no a2ahub-specific machinery |
| CC-048 | offline agent (laptop, no network) | drafts locally (V1), submit queues fail loudly; nothing silently pends — `a2a doctor`/submit error is explicit |
| CC-049 | stale local cache renders old state | cache-first reads show sync age (7.5); mutating commands always re-fetch first (V2 includes freshness pull) |

## D. AuthZ & membership

| CC | Scenario | Expected behavior |
|---|---|---|
| CC-060 | cross-section write attempt | direct push to `main`: rejected by branch protection regardless of actor; PR into a foreign section: V3 diff-authz fails the required check → unmergeable (§4.2, 10.3) |
| CC-061 | revoked credential mid-session | git push fails; `a2a doctor` explains; no partial state (submit is atomic per artifact or per explicit batch — never partially pushed) |
| CC-062 | participant offboarded with open exchanges | manifest `left` (9.2); open items flagged `orphaned-counterparty` on dashboard; senders cancel or re-route; section read-only history |
| CC-063 | machine-RO token used for write attempt | host rejects (scope); logged (T-1 blast radius) |
| CC-064 | human gate event forged by agent (`actor.kind: human` faked) | G1/G2/G4 are mechanically human-gated (CODEOWNERS review); G3/G5 are bound to the PR's authenticated GitHub identity checked by V3 against `space.yaml` owners (10.3) — forging requires the human's own account, i.e. account takeover (T-9); full fix = signed events (10.6, public mode) |

## E. Federation

| CC | Scenario | Expected behavior |
|---|---|---|
| CC-070 | cross-space ref unresolvable for a reader without access | validator warning, not error (4.3); tooling shows "restricted ref" |
| CC-071 | same system ID claimed in two unrelated spaces by different teams | operator's fleet registry check at onboarding (4.3/9.2); collision blocks the join runbook |
| CC-072 | space repo renamed/moved | `space.yaml` is in-repo (stable); clients update config remote URL; hub config updated; runbook note (9.4) |
| CC-073 | one system in N spaces gets same-thread traffic in both | threads are space-local (thread ID minted per space, 3.8); cross-space linking only via explicit refs — no silent merging |
| CC-074 | vendored mirror drifts from vendor reality | staleness surfacing (4.4); maintaining system owns refresh; consumers treat as informational only |

## F. Versioning & compatibility

| CC | Scenario | Expected behavior |
|---|---|---|
| CC-080 | breaking change published as minor | owner's CI compat check (5.4) should catch; V3 cannot always prove semantics — consumers' fixtures failing against new schema is the backstop test (13); protocol remedy: consumer files `question(defect)` + owner republishes correctly as major |
| CC-081 | retire attempted with registered consumers un-acked | validator blocks (5.4) |
| CC-082 | consumer never acks deprecation | retire stays blocked by default — no timer ever lifts it; the stalemate surfaces on the dashboard → human conversation. Resolutions: dead consumer → G4 offboarding (`left` ⇒ no longer counts, CC-062); ghosting consumer → human-gated override per §5.4 (sunset passed + `note` reminder recorded + G2-class PR); overridden consumers flagged `retired-unacked` and notified. Awareness is guaranteed by ack or by loud audited override — never by silent expiry |
| CC-083 | fixtures contradict schema in a contract dir | V3 rejects publish (5.3: fixtures must validate) |
| CC-084 | `generated_from` digest mismatch (owner forgot to re-export) | owner's project CI fails (5.3); space-side V4 flags code-backed contract with stale digest marker |
| CC-085 | binary older than `min_binary_version` tries to write | refuse write, read-only + warning (7.3) |
| CC-086 | retire override attempted before sunset, or with no reminder on record, or by an agent actor | V3 rejects (policy class); only the full §5.4 precondition set + human-reviewed PR passes |

## G. Tooling & environment

| CC | Scenario | Expected behavior |
|---|---|---|
| CC-090 | template edited/forked in a project | validator ignores (templates aren't validated at use-site), but `x_`-allowlist rule (5.6) rejects unknown envelope keys — fork visibly fails fast |
| CC-091 | two harnesses (Claude + Codex) act for one system concurrently | both are actors of the same system; attribution distinguishes them; no locking — same as CC-046 |
| CC-092 | statusline called with no config/space | silent exit 0 (never break a prompt); `a2a doctor` is the diagnostic path (7.5) |
| CC-093 | MCP server and CLI used in same session | same core, same cache, idempotent ops — safe by construction (7.1) |
| CC-094 | `a2a update` mid-flight breaking local cache format | cache is disposable (`.a2a/cache/`, 7.4); rebuild on next sync |

## H. Write funnel & redaction (post-audit additions)

| CC | Scenario | Expected behavior |
|---|---|---|
| CC-095 | two concurrent green PRs jointly create a full-repo violation (duplicate ID, supersession fork) invisible to each PR's own check | both merge (branches-up-to-date is OFF by design); post-merge V3 run on `main` + V4 flag it; remedy = follow-up fix by the owning system; never auto-revert |
| CC-096 | V3 pipeline outage freezes all writes | F-10: loud freeze; operator may temporarily lift the required check per §9.4 runbook (logged + post-incident announcement) |
| CC-097 | PR author's GitHub identity not mapped to a system in `space.yaml` | V3 required check fails with an actionable error naming the missing mapping (§4.2) |
| CC-098 | force-push: sanctioned redaction vs hostile rewrite | sanctioned = announced BEFORE execution per §9.4/10.7 (operator confirms to hub); anything unannounced = hostile → F-7 alert + incident flow |
| CC-099 | backdated event (wrong clock or deliberate) tries to reorder settled history | impossible by construction: fold order is commit order on `main` (3.5 rule 1); the event folds at its merge position; a timestamp wildly inconsistent with merge time is V3-warned |
| CC-100 | manifest ↔ host drift (participant `left` but team/PAT not revoked; new section without CODEOWNERS entry; protection rule disabled) | `a2a doctor --space` diffs manifest vs actual host config and flags divergence (OP-218); until reconciled, V3 diff-authz still blocks wrong-section merges content-wise |
