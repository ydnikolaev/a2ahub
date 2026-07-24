# troubleshooting.md — reading `a2a doctor`

> **Defer to the binary.** This file interprets the output the `a2a doctor`
> command actually produces. It does NOT invent failure modes or restate
> aspirational checks that the binary does not yet run. When in doubt, run the
> command and read its own message — the FAIL detail string tells you the
> concrete cause. Invocation syntax: [reference/commands.md](reference/commands.md).

## What `a2a doctor` does

`a2a doctor` runs a fixed set of basic health checks for the local project and
its connected spaces, printing one line per check. It exits `0` only if every
check passes.

**Output shape.** One line per check:

```
credentials: PASS
space access: PASS
versions: FAIL: <detail>
CI presence: PASS
statusline wiring: PASS
```

A passing check prints `<name>: PASS`. A failing check prints
`<name>: FAIL: <detail>`, where `<detail>` names the offending space and the
concrete reason (for example, which file was unreadable or which version pin was
violated).

**Exit codes.**

| Code | Meaning |
|------|---------|
| `0` | Every check passed. |
| `1` | One or more checks failed, OR the local project/machine config could not be loaded (in which case doctor prints a `doctor: cannot load … config` line to stderr before exiting). |
| `2` | Usage error — including the `--space` flag, which is the v2 admin host-drift diff and is explicitly rejected in v1-min (doctor prints `doctor: --space: v1-min: not available`). |

## The five checks

Each check runs once per connected space (a project with no connected spaces
passes every check trivially).

| Check | What it verifies | A FAIL means |
|-------|------------------|--------------|
| **credentials** | A write credential resolves for every connected space, via the machine-config reference (`~/.config/a2a/config.yaml`). | The space has no configured credential reference, or the reference did not resolve. Re-check your machine config's credential entry for that space. |
| **space access** | Every connected space's mirror clone is fetchable (clones on first use, fetches thereafter). | The mirror could not be cloned or fetched — a bad repo URL, a network/auth failure, or a missing local mirror path. |
| **versions** | This build is not older than each space's `min_binary_version` pin in `space.yaml`. | Your local `a2a` binary is older than the space requires (or the space's `space.yaml` could not be read/parsed). Upgrade the binary; the write funnel will otherwise refuse your writes. |
| **CI presence** | The space's mirror carries `.github/workflows/a2a-validate.yml`. | The validation workflow file is missing from the space's mirror. |
| **statusline wiring** | The `git` binary is on `PATH` (the prerequisite for §7.5's hub-less statusline-refresh fallback). | `git` is not on `PATH`, so the statusline's git-fetch fallback refresh cannot run. |

## Known limits — do NOT over-read a PASS

The binary's checks are intentionally lightweight. A PASS on these does not
imply the stronger property the plan's §9.3 runbook eventually describes:

- **credentials** verifies the credential is present and resolvable, NOT that
  it is un-expired. There is no credential-expiry field in the model today, so
  "warns on approaching expiry" (§9.3) is not yet enforced by this check — do
  not tell a user their credential is fresh on the strength of a PASS.
- **CI presence** verifies the workflow *file* exists in the mirror, NOT that a
  required status check named `a2a-validate / validate` (the P33 compound
  context — caller job `a2a-validate` calls a2ahub's reusable `validate` job) is
  *configured* in the host's branch-protection settings. The full host-drift
  diff is `a2a doctor --space`
  territory (v2, rejected in v1-min).
- **statusline wiring** is a presence check for the git fallback only, NOT a
  test of statusline rendering.

If a user needs the stronger guarantees (expiry warnings, host-drift diff),
that is a v2/`--space` concern — say so rather than implying doctor already
covers it.

## Common resolutions

| Symptom | First move |
|---------|-----------|
| `cannot load project/machine config` (exit 1, stderr) | Run `a2a init` / `a2a connect` first — the config does not exist or is malformed. See [onboarding.md](onboarding.md). |
| `credentials: FAIL` | Add or fix the space's credential reference in the machine config; confirm the token is valid. |
| `space access: FAIL` | Check the repo URL and your network/auth; a first `a2a sync` clones the mirror. |
| `versions: FAIL: … older than min_binary_version` | Upgrade the `a2a` binary to at least the pinned version. |
| `CI presence: FAIL` | The space repo is missing the validate workflow — a space-admin fix (see [onboarding.md](onboarding.md), space-admin profile). |
| `statusline wiring: FAIL` | Install `git` / put it on `PATH`. |
| `--space: v1-min: not available` (exit 2) | The host-drift diff is v2; drop the flag. |
