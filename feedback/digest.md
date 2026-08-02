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
