## [stale-refs] §3.6 Envelope metadata (03-domain.md:242)
CLAIM: The per-type extensions summary still lists `stability` as a contract field: "Per-type extensions (contract: `version`, `stability`, `generated_from?`, `compat_policy`; …)". The `stability` frontmatter field was removed — D-023 (§17) and §5.2.1 both state contracts carry NO `stability` field because draft/published/deprecated/retired is folded lifecycle state, never frontmatter.
REC: Delete `stability` from the §3.6 contract extension list (and, if desired, add `schema_format` to match §5.2.1's authoritative set).

## [lifecycle-walk] 03-domain.md §3.4.3 / §3.4.6 vs 05-schemas.md §5.2.2 (D-024)
CLAIM: The `dispute` event's subject and effect are described three different ways. §3.4.3 (line 134) renders dispute as a PARENT state row `responded --dispute--> in_progress` (subject = parent). §3.4.6 (lines 166-176) says 'verify AND dispute events target a RESPONSE (subject = the XS ID) and fold that response to disputed', explicitly reserving parent movement for `close` only. §5.2.2 (line 74) defines subject as 'the artifact (or XS for response-verify)' — carving out the XS subject for VERIFY only, implying dispute's subject is the parent, contradicting §3.4.6.
REC: Pick one model and state it in all three places: e.g. dispute's `subject` = the XS (folds response→disputed) AND the fold reopens the parent responded→in_progress (needed because `respond` is illegal from `responded`, so the target otherwise cannot answer again). Fix the §5.2.2 subject note to name dispute alongside verify, and reword §3.4.6's 'parent closes only via close' so it doesn't forbid dispute-driven reopen.

## [lifecycle-walk] 03-domain.md §3.4.1 (line 103) vs 05-schemas.md §5.4 (line 140)
CLAIM: The autonomous-retire precondition disagrees. §3.4.1 permits autonomous retire 'only after sunset AND all registered consumers acked'. §5.4 base case says retire 'is blocked until every registered consumer acked' with no sunset requirement — sunset appears only in override exception (b). For the normal path where all consumers ack BEFORE the sunset date, §3.4.1 forces waiting for sunset while §5.4 allows immediate retire.
REC: Reconcile the base-path gate: either add 'AND sunset passed' to §5.4's base blocking condition, or drop 'sunset AND' from §3.4.1 so full-ack retire is allowed pre-sunset. AC-202.2/.3 only cover the un-acked override and do not settle this.

## [lifecycle-walk] 03-domain.md §3.6 (line 242) vs 05-schemas.md §5.2.1 (line 55) / §17 D-023
CLAIM: §3.6 lists `stability` as a contract per-type extension field ('contract: `version`, `stability`, `generated_from?`, `compat_policy`'). §5.2.1 explicitly states the contract type has 'no `stability` field: draft/published is folded lifecycle state, never frontmatter (D-017)', and D-023 abolished the stability frontmatter field.
REC: Remove `stability` from the §3.6 contract extension list — stale reference to the pre-D-023 design.

## [lifecycle-walk] A2-examples.md B.9 (lines 224-245) vs 05-schemas.md §5.2.1 (line 62) / §3.4.7
CLAIM: B.9 is a `category: deprecation` announcement declared (per the appendix preamble) a verbatim T1 valid golden fixture, but it omits the schema-REQUIRED `deprecates: <XC-id>@<version>` field. §5.2.1 and §3.4.7 both make `deprecates` mandatory for deprecation announcements; B.9 instead carries only `refs: [{ref: XC-axon-ingest@2.0.0, note: successor}]`.
REC: Add `deprecates: XC-axon-ingest@1.x` (the deprecated version) to B.9 so the golden valid fixture actually validates.

## [id-integrity] A2-examples.md B.9 (announcement, deprecation)
CLAIM: The B.9 deprecation announcement fixture omits the required `deprecates: <XC-id>@<version>` field. §5.2.1 and §3.4.7 both state that category `deprecation` MUST carry it; the fixture only has `refs: [{ref: XC-axon-ingest@2.0.0, note: successor}]` and `valid_until`. The `deprecates` field is entirely absent (grep confirms it appears nowhere in B.9).
REC: Add `deprecates: XC-axon-ingest@1.0.0` (the version being sunset, distinct from the successor already in `refs`) to B.9's envelope.

## [id-integrity] A2-examples.md B.6 (decision)
CLAIM: ID↔filename mismatch: the B.6 header path is `decisions/XD-getvisa-20260802-r1w9.md` (system token `getvisa`, the space ID) but the fixture's `id` is `XD-axon-20260802-r1w9` (system token `axon`), and the inline comment confirms `<system> = drafting system per §3.3`, `from: axon`.
REC: Fix the header path to `decisions/XD-axon-20260802-r1w9.md`.

## [id-integrity] 03-domain.md §3.6 (envelope metadata, per-type extensions line)
CLAIM: §3.6 still lists the removed `stability` field as a contract per-type extension: "contract: `version`, `stability`, `generated_from?`, `compat_policy`". It also omits the now-required `schema_format`.
REC: Replace `stability` with `schema_format` in §3.6's contract extension list to match §5.2.1: "contract: `version`, `compat_policy`, `generated_from?`, `schema_format`".

## [fresh-eyes] §2.2 Roles and permissions (02-actors.md:36,41)
CLAIM: §2.2 still describes enforcement as "CODEOWNERS per section" and the system owner as the "CODEOWNERS target" for changes "in the system's section" — the pre-revision model. The revised funnel (§4.2, §10.3, D-002) enforces section ownership via V3 diff-authz and states explicitly that "CODEOWNERS lists ONLY gated paths" (provides/**, space.yaml, decisions/), while exchanges/, events/, requires/, consumes.yaml, docs/ have no owner and "need no human".
REC: Rewrite §2.2 enforcement to match §4.2: CODEOWNERS covers only gated paths (provides/**→owners G1/G2, space.yaml→admins G4, decisions/ advisory); section containment is V3 diff-authz (github-login→system). Fix line 36 to say the owner is the CODEOWNERS target for the section's gated paths (provides/**), not the whole section.

## [fresh-eyes] §3.6 Envelope metadata (03-domain.md:242) + §11.1 Contracts view (11-observability.md:17)
CLAIM: Both places still list a contract `stability` field: §3.6 names it as a contract per-type extension ("contract: `version`, `stability`, `generated_from?`, `compat_policy`") and §11.1's dashboard Contracts view lists a "stability" column. D-023 and §5.2.1 explicitly removed it (§5.2.1: "no `stability` field: draft/published is folded lifecycle state, never frontmatter").
REC: Delete `stability` from §3.6's contract extension list. In §11.1 replace the "stability" column with folded lifecycle state (published/deprecated/retired) or drop it.

