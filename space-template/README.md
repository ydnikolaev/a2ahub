# Space template

This directory is the scaffold a new a2ahub exchange space repo is created
from (plan §4.2 layout, minus per-system sections — those are added by each
system's onboarding PR, US-102). It is pure data: no Go code, no imports.

Do not fill this in by hand beyond the placeholders already marked
`REPLACE_WITH_*`. Space creation and participant onboarding are runbook-
driven — see the org/space admin and onboarding runbooks in the a2ahub
product docs, plan §9.2 "Onboarding runbooks"
(`docs/the-plan/plan/09-human-ops.md#92-onboarding-runbooks`).

Contents:

- `space.yaml` — the manifest (schema `space/v1`); ships with zero
  participants so CI is green on the empty space (AC-101.1).
- `CODEOWNERS` — gated paths only (`/space.yaml`, `/decisions/**`); no
  `/<system>/provides/**` entries until a system onboards.
- `.github/workflows/a2a-validate.yml` — the V3 CI gate (blocking on PRs,
  flag-only post-merge).
- `BRANCH-PROTECTION.md` — the settings checklist P11's runbook applies.
- `decisions/`, `vendored/` — empty placeholders per plan §4.2/§4.4.
