## D002-write-path — adjust

REASONING:
The plan's stated write-path mechanics are not implementable on github.com, so "keep" is out on factual grounds alone. Three findings:

1. **The enforcement claims are false as written.** §10.3 claims "write own section = {CODEOWNERS+V2/V3}" and "write other section = ✘ {CODEOWNERS}"; §5.5 V3 says "merge/push blocked (branch protection)". On github.com, CODEOWNERS and required status checks bind only PR merges into a protected branch. Branch protection is binary: either it requires PRs (blocking ALL direct pushes, contradicting §4.2 "sections whose gates don't apply push directly to main") or it doesn't (and then a fine-grained PAT with contents:RW — §10.5 — can push anywhere in the repo, any section, including a silent breaking contract bump straight to main). Pre-receive hooks exist only on GHES. Push rulesets are repo-wide path blocks with bypass lists — not a per-actor→section map, tier-dependent, GitHub-only (would violate the D-019 portability seam). There is no configuration on github.com that makes the current §4.2 text true.

2. **Gate integrity currently hangs on client politeness.** With a direct-push lane open, G1/G2/G4 fire only if `a2a submit` correctly classifies the change and voluntarily opens a PR. One client bug (or one agent running raw git) silently bypasses the product's core safety promise — exactly the failure class D-010 calls "the #1 failure of the md era", on a YMYL chain. CC-064 accepts G3/G5 forgery as audit-detectable residual risk (fine — the fold's rules 2–3 already neutralize unauthorized *events*), but content tampering and gate bypass on *artifact files* have no such fold-level defense.

3. **The fix is cheaper than the current design, not heavier.** Collapsing to a single write path (PR-always, auto-merge on green) deletes the dual-path logic and moves gate enforcement from client code to declarative host config. Latency cost is ~30–90 s of fire-and-forget auto-merge per verb — irrelevant against the multi-hour human-relay baseline being replaced, and trivially fine at 2→10 systems' write rates. It also lands the exact shape the public-mode hub-write upgrade needs (D-002's "designed-for" claim becomes structurally true: same single funnel, enforcement point swapped).

Rejected alternatives: **flag-only inside the trust circle** — almost defensible for v1 accidents-not-malice, but it leaves the plan's written claims false, leaves G1/G2 to convention, and buys nothing since PR-always costs less to build; **V3-on-push + auto-revert bot** — introduces a privileged cross-section writer (new attack surface), races the fold, and puts invalid states on the normative main; **per-section branches** — needs a merge bot (same problem) and breaks "main is the only normative branch"; **pre-receive** — unavailable on the chosen host.

One prerequisite surfaced by the analysis: agent credentials must be per-system machine accounts (or a GitHub App), not the owner's own PAT — otherwise the PR author IS the code owner and GitHub forbids self-approval, deadlocking G1/G2; separate identity also makes §2.1's commit-author cross-check meaningful.

ADJUSTMENT:
**Adjusted D-002 write path: single funnel — PR + auto-merge, CODEOWNERS only on gated paths, V3 as the merge gate.**

Normative design (edits per section):

**D-002 (17-decisions.md)** — reword: "v1 writes go through the binary as validate-then-PR-with-auto-merge; `main` accepts no direct pushes from anyone; ungated PRs merge automatically on green V3 with zero human involvement; gated paths additionally require code-owner review. Hub-mediated write remains the designed-for public-mode path (same funnel, enforcement point moves server-side)."

**§4.2 (04-topology.md)** — replace the two write-path rules with:
- `main` is the only normative branch and is protected: PRs required for all actors (admin bypass reserved for incident recovery and alarmed via F-7), required status check `a2a-validate` (V3), force-push forbidden, auto-merge enabled, "require branches up to date" OFF (concurrent event PRs must not serialize; the post-merge V3 run on main + V4 are the backstop for cross-PR races).
- CODEOWNERS lists ONLY gated paths: `/<system>/provides/**` → that system's human owners (G1/G2); `/space.yaml` → space admins (G4); `/decisions/**` → all parties' humans. All other paths (`exchanges/`, `events/`, `requires/`, `docs/`) have NO owner entry, so with "require review from Code Owners" enabled, PRs touching only those paths need no human and auto-merge on green. NOTE: this over-gates minor/patch contract publishes (any `provides/**` change gets owner review) — accepted, documented as intentional conservatism per D-010.
- Section single-writer is enforced by V3-as-required-check: changed paths ⊆ the authoring system's section (+`decisions/` via decision flow), author mapped GitHub-login→system-id via `space.yaml` participants. This replaces the (incorrect) CODEOWNERS claim for ungated writes.

**§2.2/§10.5** — agent credential = per-system machine account with fine-grained PAT (or GitHub App installation) scoped to the space repos; human owners approve gated PRs from their own accounts. Rationale: CODEOWNERS self-approval prohibition + attribution cross-check.

**§10.3** — rewrite enforcement braces: write own section = {branch protection: PR-only + V3 required check}; write other section = ✘ {V3 diff-authz fails required check → unmergeable}; G1/G2/G4 = {CODEOWNERS required review}; G3/G5 unchanged (V3 content checks + audit, CC-064 residual). Rewrite the "dual enforcement" paragraph: git-host mechanics enforce at merge time; V4 re-checks content-wise post-merge; hub-write mode later reuses the same engine as a third point.

**§5.5 V3 row** — trigger: "PR (required status check) + post-merge run on main"; on failure: "merge blocked; post-merge violations flag-only (V4-equivalent)". Remove "push blocked".

**§7.2** — OP-205/OP-211/OP-212 semantics: commit to ephemeral branch `a2a/<system>/<id>`, push, open PR with auto-merge, return immediately (fire-and-forget); branch auto-deleted on merge; idempotent by artifact/event ID. Each verb's artifacts+events travel in ONE PR (self-contained referential closure; refs resolve against main ∪ the PR itself). §7.5/7.4: local cache gains a `pending-merge` state so submit-then-read is coherent before the merge lands.

**§6.7** — new failure mode F-10: required-check pipeline outage blocks all space writes; behavior: `a2a doctor` diagnoses, operator may temporarily lift the required check (runbook §9.4, logged, announcement to space afterwards). 

**§12** — CC-060 rewrite: cross-section write attempt → V3 required check fails, PR unmergeable; direct push to main → rejected by branch protection regardless of actor. CC-064 narrows to G3/G5 (G1/G2/G4 now mechanically human-gated). New CCs: (a) two concurrent green PRs create a full-repo violation (duplicate ID / supersession fork) invisible to each PR's check → post-merge V3 on main + V4 flag, remedy = follow-up fix by owner; (b) CI outage write-freeze (→ F-10); (c) agent PAT identity not mapped in space.yaml → V3 check fails with actionable error.

**§15 L0** — exit criteria add: branch protection verified to reject a direct push and to auto-merge an ungated hello-world PR without human touch; a gated-path PR verified to demand owner review.

**§9.2 runbook** — participant onboarding includes creating the machine account, issuing its PAT, and adding the login→system mapping to space.yaml.

Portability note (D-019): PR+required-check+owner-approval maps 1:1 to GitLab MR+pipeline+approval rules — the adapter seam survives; push rulesets/pre-receive would not.

---

## D016-single-intent — adjust

REASONING:
See above.

ADJUSTMENT:
Keep D-016 verbatim (one artifact = one intent, no bundle type, thread correlates). Add the following, as edits to the named sections:

1. §7.2 (command surface):
- OP-203 extended: `a2a new <type> [--thread <id>]` — attach the draft to an existing thread; when omitted and other drafts are pending, the CLI offers the pending drafts' thread. First artifact of a set mints the thread (§3.8 unchanged).
- New OP-220 `a2a submit --batch <artifact...>` (or `--drafts` for all pending): validates ALL drafts (V2), then ONE commit + ONE push carrying N artifact files + N submit events. All-or-nothing pre-push: any validation failure aborts the whole batch with per-artifact errors. Per-artifact idempotency (by ID) preserved; note reconciling text for CC-061 ("submit is atomic per artifact or per explicit batch — never partially pushed").
- §7.7: MCP `a2a_new` accepts `items[]` (type + envelope fields + body each), returns thread-linked drafts; `a2a_submit` accepts an ID array. Keeps CLI/MCP parity (R-018).

2. §7.5 statusline + §11.3 notifications + OP-207 inbox: thread is the default presentation/coalescing unit. `a2a inbox` groups same-thread items under one header (counts per state); hub notification fan-out coalesces same-thread items arriving in one push into one notification. This removes the "wall of tiny artifacts" penalty on the receive side. `a2a thread` (OP-210) already covers the drill-down.

3. §8.2 send loop, step 1: append normative text — "A composite need is the NORMAL case, not an edge case: classify it into its parts, draft each with `a2a new --thread`, then submit them together with one `a2a submit --batch`. Never park a secondary intent in the body of another artifact — the receiver is entitled to decline `split-required`." The 8.7 skill's authoring guides get a worked decompose example (announcement + question + work_request on one thread — add it to A2 as a golden fixture, reusing the existing B.2 thread).

4. CC-009 + event schema (§5, event fields): add optional machine-readable `reason_code` on decline events with a small closed enum including `split-required`. Sender-side tooling, on seeing `split-required`, prints/returns the recovery recipe: draft the parts on the same thread, `supersedes:` the declined artifact, one batch submit. CC-009 expected behavior gains: "decline with reason_code split-required; sender's tooling guides re-submission as a superseding thread batch." Keep enforcement honest: no prose-sniffing validator heuristics — schema-level policy checks (5.5 "single-intent structural rules") stay limited to what is provable (e.g. a question field-set inside an announcement envelope), and the rest remains template guidance + receiver decline, exactly as CC-009 already states.

5. §17: amend D-016 rationale line to record the ergonomics corollary: "rule is kept enforceable by making compliance cheaper than violation: batch draft/submit (OP-220), thread-grouped presentation, machine-readable split-required decline."

Explicitly rejected: a `bundle` artifact type (reintroduces bundle-level lifecycle ambiguity, guaranteed abuse vector at OS scale); an LLM-powered `a2a split` decompose command in the binary (v1 overengineering — the agent is the decomposer, the binary supplies mechanics); auto-splitting by the validator (cannot see intent in prose, false positives would erode trust in V2).

---

## D017-events-fold — keep

REASONING:
D-017 looks like event-sourcing ceremony, but under this plan's own constraints it is actually the SIMPLEST coherent design, and both proposed alternatives are net-more-complex or violate anchor decisions.

1) The hybrid ("owner edits status field; counterparty events only for cross-party transitions") does not eliminate the event mechanism — it adds a second one beside it. Section ownership (§4.2: sole writer per section, CODEOWNERS-enforced) means the target can NEVER touch the sender's artifact file, and most transitions in §3.4.3 (acknowledge, accept, start, decline, block, respond) are target-authored. So events are unavoidable; the hybrid means state = f(in-place field, event set), which is precisely the two-truths problem D-001 exists to kill. It forces a reconciliation rule ("field says withdrawn, later event says responded — who wins?") and requires a total order BETWEEN file edits and events, which git does not give (commit timestamps skew across machines; ULID-ordered events do give one, §3.5 rule 1, CC-012/CC-022). The pure fold is one mechanism, one ordering, one truth. Additionally, in-place status edits churn the artifact digest on every transition, breaking `id#digest` pinning (§5.7, §3.8) and the immutability claims of §3.4.1/3.4.7 — a structural conflict the hybrid cannot patch cheaply.

2) Git conflict behavior: one-ULID-file-per-event in the actor's own `events/<year>/` makes merge conflicts impossible by construction — the plan already banks on this (CC-046, CC-091: two agents of one system, no locking). Status-field editing reintroduces same-file concurrent writes as the common case for the busiest object class. Concurrency safety would regress exactly where v1 has real concurrency (multiple harnesses per system).

3) Agent ergonomics: agents never author events by hand — `a2a ack/accept/respond/verify/dispute` (§8.2/8.3) generate them, and V2 validates lifecycle legality before push. The fold's complexity is paid once, in the binary, and reused verbatim by the hub (D-011, §6.3). At v1 scale a fold is a ~100-line table-driven function over a handful of events per artifact, with golden fixtures (§13) — this is not CQRS/ES infrastructure, it is "sort files, apply transition table, flag illegal". "Ignored+flagged, never crash" (§3.5 rules 2-4) is exactly the right partial-failure posture for V4/F-6.

4) Hub simplicity: with the fold, the hub is pure derivation (fetch → fold → index), trivially rebuildable (D-012, F-4), and flag-only. With a status field the hub must trust an in-place claim AND cross-check events to detect illegal edits — more code, weaker violation detection, and CC-064-style forgery becomes harder to audit (append-only events give a clean commit-author-vs-claimed-actor trail).

5) The PR-comment alternative is disqualified on anchor decisions: it moves durable lifecycle state out of git files into host-API objects, breaking D-001 (git SSOT — a fresh clone would no longer contain the truth), D-019/Q-004 (GitLab/Gitea portability), hub rebuild-from-git (F-4), offline drafting (CC-048), and schema validation (comments are unschematizable free text). It optimizes for human browsing, which the plan already covers via dashboard/local HTML/statusline.

Residual costs of D-017 are real but small and already mitigated in-plan: raw-repo browsing doesn't show current state (mitigated by `a2a show`, local HTML, dashboard); event-file accumulation (year-sharded, §4.2); wall-clock-ish ULID ordering for near-simultaneous contradictory events (accepted and surfaced, CC-022). None warrants a design change, and at open-source scale the properties D-017 buys (deterministic replay, host independence, conflict-free multi-writer, audit trail) become more valuable, not less. Keep as specified; no plan edits required.

ADJUSTMENT:
-

---

## D010-retire-block — adjust

REASONING:
The hard-block DEFAULT is right; the FOREVER is wrong, and the plan already contradicts itself about it.

1) Internal inconsistency that must be resolved either way: 3.4.1's retire row says "only after sunset and zero registered consumers, ELSE GATE" — implying a human-gated override exists — while CC-082 says the block is absolute ("the block is the feature") and 5.4/D-010 say "blocked until acks" with no escape. An implementer agent reading 3.4.1 vs CC-082 will build different validators. This alone forces an edit.

2) Why hard-block-forever is the wrong terminal state. Retire looks like cheap hygiene (old versions stay published, git keeps everything), but a published contract version describes a REAL interface the owner must keep operating. With a dead/abandoned consumer, forever-block gives the owner two options: keep running the old endpoint indefinitely (unbounded real cost imposed by a ghost), or kill the endpoint while the space still says "published" — recreating exactly the two-truths drift the product exists to eliminate (D-001 rationale, R-004). The block converts the ack's purpose (guaranteed AWARENESS — the actual YMYL value) into an unbounded consumer VETO over the provider's infrastructure. At v1 (two systems, one operator) this is survivable via a phone call; at open-source scale, any abandoned consumer blocks every provider forever — untenable, and the foundation should be correct now since the fix is cheap (it reuses existing gate machinery, no new subsystem).

3) Why the other alternatives lose:
- "Sunset date wins after N reminders" (auto-retire on timer): contradicts the plan's load-bearing stance that NOTHING auto-transitions on time — 3.4.3 ("no auto-transition... never auto-closed, silence must stay visible, S-7"), CC-026, needed_by semantics ("staleness reference, never auto-close"). It would be the only time-driven transition in the whole event model, and it silently breaks a live-but-negligent consumer — precisely the #1 md-era failure D-010 exists to prevent. Reject.
- "Per-space policy knob": no consumer at v1 scale (R-013/R-021 anti-overengineering bar), adds config surface and test matrix; 3.7 already permits per-space gate tightening, which covers spaces that want stricter. Reject as day-one machinery.
- "Operator override gate": correct shape, and exactly what 3.7's gate philosophy prescribes — a human exactly where an error is expensive and hard to reverse. Retiring over a silent consumer's head IS such an error. This is what 3.4.1's "else gate" already hints at.

4) The dead-consumer case has TWO distinct sub-cases the plan should route differently: (a) consumer formally dead → offboard via existing G4 runbook (manifest status `left`, CC-062); `left` systems must simply not count as registered consumers for retire-blocking — the plan currently never says this, and CC-062 already flags their open items as orphaned; (b) consumer ghosting but not offboardable → human-gated override with preconditions (sunset passed + reminder on record), loud and audited, never silent.

The adjustment keeps the default strict (validator blocks, agents can never retire past un-acked consumers autonomously), keeps silence visible (the block never expires on its own; a HUMAN decides, and the overridden consumers are named in an audit event and flagged on the dashboard), resolves the 3.4.1↔CC-082 contradiction in favor of the gate, and closes the dead-consumer hole with zero new subsystems.

ADJUSTMENT:
Concrete plan edits (5 files):

1. 05-schemas.md §5.4, replace bullet 3 with:
"Old versions remain published until `retire` (§3.4.1). Retire of a deprecated version is blocked by the validator until every registered consumer has acked the deprecation announcement. Two bounded exceptions, both human-only:
  (a) Systems with manifest status `left` do not count as registered consumers (their exposure is already surfaced per CC-062); offboarding a dead consumer (G4 runbook, §9.2) is the normal resolution for an abandoned system.
  (b) Gated override: retire MAY proceed over un-acked consumers only when ALL hold — the sunset date has passed; at least one reminder note event (8.5 ladder) is recorded on the deprecation thread; and the retire event is authored by a human actor via PR review (G2-class gate). The override event MUST list the overridden consumer systems. V3 enforces all three preconditions; V4/dashboard flag each overridden consumer as `retired-unacked` and the hub notifies them (11.3 matrix: deprecation targeting you).
Retire is never time-triggered: sunset passing alone changes nothing (S-7 — silence stays visible; a human decides)."

2. 03-domain.md §3.4.1 retire row, make the existing "else gate" explicit:
"deprecated | retire | retired | owner; autonomous only after sunset AND all registered consumers acked (`left` systems excluded); otherwise human-gated override per §5.4 (sunset passed + reminder recorded + G2-class PR, overridden consumers listed)"

3. 12-corner-cases.md CC-082, replace expected behavior with:
"retire stays blocked by default — no timer ever lifts it; the stalemate surfaces on the dashboard → human conversation. Resolution paths: dead consumer → G4 offboarding (`left` ⇒ no longer counts, CC-062); ghosting consumer → owner's human overrides via the §5.4 gated path (sunset passed + reminder recorded + G2-class PR); overridden consumers are flagged `retired-unacked` and notified. Awareness is guaranteed either by ack or by loud, audited, human-authorized override — never by silent expiry."
Add CC-086: "retire attempted via override before sunset, or with no reminder on record, or by agent actor | V2/V3 reject (policy class); only the full §5.4(b) precondition set + human PR passes"

4. 17-decisions.md D-010, replace decision text with:
"Contract semver; breaking ⇒ major + G2 + deprecation announcement + consumer acks; retire blocked until acks — except `left` consumers don't count, and a human-gated override (sunset passed + reminder + G2-class PR, audited, overridden consumers flagged+notified) exists for unresponsive consumers; never time-based auto-retire" — rationale append: "awareness is the YMYL guarantee, not consumer veto; a dead consumer must not block forever (open-source scale)".

5. 14-us-ac.md US-202, add:
"AC-202.3 Given un-acked registered consumers and sunset passed, when retire is attempted by an agent actor or without a recorded reminder, then the validator blocks it; when submitted as a human-reviewed override PR meeting all §5.4 preconditions, then retire succeeds and each overridden consumer is flagged `retired-unacked` on dashboard and notified. [CC-082, CC-086]"

Optional consistency touch: 07-client.md OP-212 note that `a2a contract retire` detects the override case and opens a PR (gate awareness) rather than pushing directly — wording already covers this via "opens PR when gate applies"; extend the parenthetical to "(opens PR when G1/G2 or the 5.4 retire-override gate applies)".

Explicitly NOT added: per-space policy knob (no v1 consumer; 3.7 already allows stricter per-space gates) and any sunset-auto-retire timer (contradicts S-7/3.4.3).

---

## D018-inbox-query — keep

REASONING:
D-018 (inbox-as-query, no spool in space, local cache only) is correct at both v1 and open-source scale. Deliberation:

1. WHAT THE MATE SPOOL WAS FOR, AND WHAT REPLACED EACH FUNCTION. The research doc's project-level spool (outbox/ + inbox/ with receipt.yaml, registry, survives feature archive) was designed BEFORE a team-level durable SSOT existed — the spool WAS the durable record. In the approved architecture every function it served has a strictly stronger home:
   - Durable receipts → the space repo itself: artifact + append-only lifecycle events (D-017, §3.5), never deleted (§3.8 archival). A counterparty's ack event committed in THEIR section under THEIR git identity is a stronger receipt than a self-written receipt.yaml, which is self-attested and can drift.
   - Survives feature archive → space artifacts outlive any project-local structure by construction; AM-a2a-3 adopts the research doc's archival rules unchanged (blocking open request prevents feature completion; archived feature retains stable IDs + response digest).
   - Local-work↔exchange mapping → bidirectional without a registry: envelope `origin` field (§3.6, exchange→tracker ID) plus `external_refs` in mate pipeline docs (AM-a2a-3, feature→exchange, N:M capable).
   - Durable LOCAL copy → the local space mirror clone (§7.4 config: repo URL + local mirror path) IS a full durable local spool at zero design cost — complete history, survives hub loss and even remote repo loss. The research doc's own constraint ("local projection/spool, not a competing normative copy") is satisfied more honestly by a git clone than by a curated folder that something must keep in sync.

2. WHY A MATERIALIZED SPOOL WOULD BE ACTIVELY HARMFUL. A committed spool needs a writer. Whoever updates it already has network + the binary — at which point the query is available and the copy is redundant. An un-updated spool silently presents stale state as current, the exact "two-truths" failure D-001/D-017 exist to kill, and worse than the plan's explicit staleness surfacing (CC-049: cache-first reads show sync age). At open-source scale, per-consumer materialized inboxes inside spaces mean N copies of every artifact, section-ownership violations (delivery would write into the recipient's section — breaking D-017's single-writer invariant), merge noise, and repo bloat. Addressing metadata + fold is the correct derivation seam.

3. HARNESS-LESS AGENTS (CODEX IN CI). Checked against §8.8: CI workers get "validation invocation only" — they are not inbox consumers by design; V3 needs no inbox. An interactive Codex agent gets CLI + AGENTS.md and runs the 8.1 checklist (`a2a inbox --actionable`) — a plain CLI call, no harness features needed; the statusline is explicitly an optional layer with 8.1 as the guaranteed floor (D-021). The only real degradation is a network-less environment with no pre-existing mirror: it cannot compute the inbox. But a committed spool would not fix that correctly — it would show unverifiable stale state; CC-048's "fail loudly, never silently pend" is the right behavior. A CI pipeline that genuinely needs inbox data can run `a2a sync` in a networked step and read from cache afterward — reads are cache-first by design (7.5, CC-049), and CLI/MCP structured output parity is a CI-checked invariant (7.1).

4. RESIDUAL NON-BLOCKING NOTES for the implementer (no plan edit required, both are arguably already implied): (a) make explicit in §7.2 that OP-207/OP-208/OP-209 read paths MUST work offline from the local mirror + cache with sync-age flagged — currently stated for statusline (7.5) and cache semantics (CC-049) but not verbatim for inbox/outbox/show; (b) OP-207/208 should guarantee machine-readable (JSON) output like OP-204 does, so harness-less scripts consume them without parsing prose — effectively guaranteed by 7.1 CLI/MCP parity but cheap to state. Neither changes D-018.

Conclusion: outbox + git history + local mirror + `origin`/`external_refs` linkage adequately and superiorly replaces the durable project-level spool; A1.1's supersession of the research sketch is sound. Keep D-018 as written.

ADJUSTMENT:
-

---

## D012-hub-scope — keep

REASONING:
The hub as scoped (optional, read-only, ephemeral-state-only, SQLite, L3) is the right call for 2-10 systems. Deliberation across the three candidate scopes:

1. PURE CLIENT-SIDE (no hub; statusline from local sync + static HTML on GitHub Pages) — rejected, three concrete failure modes:
(a) Privacy: exchange metadata is explicitly a protected asset (10.1: "who builds what" is business-sensitive). GitHub Pages is public unless on Enterprise Cloud; a fleet dashboard published there leaks the graph. Locking it behind Cloudflare Access or Enterprise costs the same or more operator burden than the hub, on infra the operator does NOT already run — whereas the hub lands on the existing Timeweb VPS (≤1h budget, 9.1).
(b) No always-on party: chat notifications for gates/p1 (11.3 matrix, wish-sourced) need something running when no agent session is open. The serverless substitute is per-space GitHub Actions cron + Telegram secrets replicated in every space repo — logic scattered into YAML across N repos, violating the R-004 zero-drift stance, with Actions queue latency and quota as new failure modes. One hub binary reusing the same Go core packages (7.1) is strictly less total machinery at N spaces.
(c) Realtime is an explicit operator requirement (R-009, wish #3, "live pulse" 11.1), and AC-502.1 distinguishes seconds-fresh (hub) from 5-min TTL (fallback). Pages-on-push gives minutes at best and no SSE.
Critically, the plan ALREADY CONTAINS the pure client-side option as its degraded mode: AC-301.3 (everything works with no hub), F-3 (hub down → direct git sync), 7.5 cache-first statusline with 5-min TTL satisfying S-3, 7.6 offline HTML. So "cut the hub" would not remove a dependency — the hub is not one (S-8: destroyable, rebuildable, zero durable loss; T-6: worst-case compromise = wrong dashboard). It would only remove the seconds-freshness, the human push channel, and the fleet view, while keeping all the fallback code that must exist anyway.

2. BIGGER HUB (auth portal / identity service) — rejected as overengineering AND as an architecture violation. A user/credential store is durable state on the hub, breaking D-001's ephemeral-only invariant (4.1) and the "destroy and rebuild from git" property (S-8, F-4). 9.3 explicitly rules out a self-service portal (R-013) for a v1 of ~3-5 humans; static bearer tokens in hub.yaml (10.5) are proportionate. Real identity (signed events, hub-issued identities) is correctly parked behind Q-003/10.6 with a named trigger (first untrusted participant / public mode) — that is the right time, because the identity design depends on who the untrusted parties are.

3. KEEP, including at open-source scale: the read-only hub is the load-bearing platform seam for public mode. D-002 designates hub-mediated write as the public-mode path, and 10.3's dual-enforcement note makes the hub's ingest+validation pipeline the future server-side enforcement point. Shipping the hub in v1 — read-only — establishes deployment, token model, webhook ingest, and the fold/validate engine on the server, so public mode becomes "add a write path to an existing service" instead of "build a service from scratch". Per the operator's own doctrine this is a certainty-seam, not premature abstraction.

Risk/burden accounting for the kept scope: SQLite single-file, fully derived (D-012) — no backup discipline, `hub rebuild` is the recovery story; failure catalog F-1..F-9 shows every mode degrades to the git path; phase ordering already minimizes sunk cost (L2 pain-closing ships BEFORE the hub at L3; kill criteria at each phase exit). Two watch-items for implementers, not plan changes: (i) OP-109 (events-since-watermark API) has no v1 consumer — statusline uses OP-108, chat fan-out is hub-internal, agents sync via git — implement it last within L3 or let it slip to L4 without guilt; (ii) the dual freshness paths (hub-fed vs TTL-fallback) must both stay tested forever — AC-502.1 and E2E-7 already pin this, keep them non-negotiable in CI.

ADJUSTMENT:
-

---

