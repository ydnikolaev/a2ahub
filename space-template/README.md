# Space template

This directory is the scaffold a new a2ahub exchange space repo is created
from (plan §4.2 layout, minus per-system sections — those are added by each
system's onboarding PR, US-102). It is pure data: no Go code, no imports.

**If you are reading this inside your own space repo, delete this file.** It
documents the template directory, not your space; `a2a space init` copies the
whole template and `a2a space update` will never remove it for you.

Do not fill this in by hand beyond the placeholders already marked
`REPLACE_WITH_*`. The ordered from-zero path — including the two orderings
that brick a space when taken early — is printed by `a2a space init` itself
and written out in the space-admin runbook in the shipped skill
(`skill/a2ahub/onboarding.md`, "Creating a space from zero"). The settings
checklist is `BRANCH-PROTECTION.md`, which ships beside this file into your
space.

Contents:

- `space.yaml` — the manifest (schema `space/v1`); ships with zero
  participants so CI is green on the empty space (AC-101.1).
- `CODEOWNERS` — gated paths only (`/space.yaml`, `/decisions/**`); no
  `/<system>/provides/**` entries until a system onboards.
- `.github/workflows/a2a-validate.yml` — the V3 CI gate (blocking on PRs,
  flag-only post-merge).
- `BRANCH-PROTECTION.md` — the settings checklist P11's runbook applies.
- `decisions/`, `vendored/` — empty placeholders per plan §4.2/§4.4.
