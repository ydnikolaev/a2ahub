# Feedback digest

## 2026-07-23 Triage

- fb-20260723-9ae145 (bug -> shipped): submit blocks every contract: REF-001 from fixed contract.md filename

## 2026-07-29 Triage

- fb-20260728-1e7d7f (bug -> shipped): submit leaves an orphaned branch that wedges every retry
- fb-20260728-5aed4a (friction -> shipped): LFC-001 refusal is opaque while the prerequisite transition is pending-merge
- fb-20260728-6fc628 (friction -> shipped): one-item-per-session cap forces a round-trip and drops real findings
- fb-20260728-a85a71 (feature -> shipped): statusline is not machine-readable and cannot be verified when quiet
- fb-20260728-b10a10 (bug -> shipped): thread next= names verbs the CLI does not have
- fb-20260728-d33e33 (bug -> shipped): feedback intake can never label, so no item can auto-merge
- fb-20260728-df74bb (friction -> shipped): no supported way to wait for a pending-merge transition

## 2026-08-01 Triage

- fb-20260801-457629 (bug -> accepted): note mints an event the space validator rejects, and opens the PR anyway

## 2026-08-02 Shipped

- fb-20260801-457629 (bug -> shipped in v0.18.2): notes are state-free audit events; the submitted lifecycle family passed the targeted live gate

## 2026-08-02 Triage

- fb-20260802-10d983 (friction -> accepted): export bytes can only be verified after the irreversible publish
- fb-20260802-191c66 (protocol -> accepted): a consumer has no supported way to materialize a pinned contract version
- fb-20260802-d64d19 (bug -> accepted): submit reports failure on squash-only repos after the PR already exists
- fb-20260802-e2d28b (feature -> accepted): classification is a label the space transport never honours
- fb-20260802-e6d436 (feature -> accepted): schema_format admits OpenAPI and proto but only a schema dir can travel
- fb-20260802-f04608 (feature -> accepted): a contract ships an executable fixture suite that nothing can execute

## 2026-08-09 Triage

- fb-20260806-3539ac (bug -> shipped): contract publish planner refuses every contract the shipped template scaffolds
- fb-20260806-c6ad38 (bug -> shipped): no contract declaring a companion artifact can be published
- fb-20260806-eb224e (bug -> shipped): export-source-v1 digest cannot be computed before the first publish
- fb-20260806-2af06c (docs -> shipped): the carried-set path grammar has no home for a fixture-suite manifest
- fb-20260806-cd2d11 (docs -> accepted): publication never states its anchor, and never says no git tag is minted
- fb-20260808-5c73a9 (bug -> accepted): data pack writes a role:readme file its own validator rejects with POL-002

## 2026-08-05 Shipped

- fb-20260802-191c66 (protocol -> shipped in v0.19.0): a consumer has no supported way to materialize a pinned contract version
- fb-20260802-d64d19 (bug -> shipped in v0.19.0): submit reports failure on squash-only repos after the PR already exists
- fb-20260802-e2d28b (feature -> shipped in v0.19.0): classification is a label the space transport never honours
- fb-20260802-f04608 (feature -> shipped in v0.19.0): a contract ships an executable fixture suite that nothing can execute

## 2026-08-06 Shipped

- fb-20260802-10d983 (friction -> shipped in v0.19.8): export bytes can only be verified after the irreversible publish

## 2026-08-20 Re-verified against the code, one-answer-2026-08 P7

Each of the seven records below was re-checked against the code, not
transcribed from any prior table — see the phase's own report for what was
read at each site.

- fb-20260806-cd2d11 (docs -> shipped): publication never states its anchor, and never says no git tag is minted
- fb-20260808-5c73a9 (bug -> shipped in v0.19.10): data pack writes a role:readme file its own validator rejects with POL-002
- fb-20260812-d31acb (bug -> shipped in v0.20.0): an undecodable packed README is listed as noise by inbox and outbox
- fb-20260812-f9cfac (bug -> shipped in v0.20.0): an undecodable packed README leaves repository-visibility UNVERIFIED
- fb-20260812-e6d189 (bug -> shipped in v0.20.0): REF-017 retroactively rejects merged artifacts and reddens the default branch
- fb-20260812-ee6dcd (bug -> shipped in v0.20.0): doctor reports a healthy space while its default branch is failing CI
- fb-20260812-755a23 (friction -> shipped in v0.21.0): a one-file feedback record costs eleven CI jobs, including a macOS build
- fb-20260818-76f29d (bug -> accepted): verify --verdict indexes an unseen list and only range-checks it

## 2026-08-27 Triage

- fb-20260820-02f576 (bug -> shipped in v0.24.0): notify-reusable still go-installs a2a, retired the same day in validate
- fb-20260820-72a4a1 (bug -> shipped in v0.24.0): an unset number input is 0, not empty, so every space push fails on --limit 0
- fb-20260820-166de0 (bug -> shipped in v0.24.0): a2a update's skill refresh writes only the version stamp, and doctor passes it
- fb-20260820-a3f169 (bug -> shipped in v0.24.0): notify setup reads the one file that proves the checkout, and only at the end
- fb-20260820-d1e370 (bug -> shipped in v0.24.0): submit carries a declared companion that validate --ci rejects, unseen locally
- fb-20260820-0cb8c8 (protocol -> shipped in v0.24.0): an observed consumer should inform retire and deprecate without registering
- fb-20260827-bc1f13 (bug -> accepted): verify-export hides the only file that can explain its digest mismatch
- fb-20260827-a84550 (bug -> accepted): verify-export against a staged candidate can never match its published version
- fb-20260827-455fca (feature -> accepted): no verb answers whether a producer's published contracts still match its code
- fb-20260827-4b121a (friction -> accepted): validate --ci outside a space names a missing file, not the caller's state

## 2026-08-27 Triage

- fb-20260827-47069c (bug -> accepted): a no-op version bump publishes the OLD bytes under the NEW number
- fb-20260827-5b6a1c (friction -> accepted): a verb usage names its flags, never its workflow, so a loop page is unreachable
