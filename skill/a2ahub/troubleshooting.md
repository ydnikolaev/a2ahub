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
space identity: PASS
versions: FAIL: <detail>
CI presence: PASS
space scaffolding current: PASS · the space is behind this binary's template (workflow ref, write floor): ask the space admin to run `a2a space update`
auto-merge enabled: PASS · auto-merge unverified for <space>: the credential cannot read this repo's settings (a fine-grained token needs "Repository metadata: read")
statusline wiring: PASS
skill discoverable: PASS · no a2ahub skill installed
skill manual current: PASS · no skill installed
```

All ten lines, in the order the binary prints them. **A `PASS` carrying a note
is a pass** — do not report it as a problem.

SIX rows can print `PASS · <note>`, not four:

| Row | When it carries a note |
|---|---|
| `credentials` | the token resolves but advertises no `workflow` scope |
| `versions` | a newer release is available (never a failure — the floor is a separate check) |
| `space scaffolding current` | the space is behind this binary's template, or the answer could not be determined. An **in-sync** space gets a bare `PASS` with no note |
| `auto-merge enabled` | the setting could not be read (no host reader wired, or the API refused) |
| `skill discoverable` | no skill installed, or installed but no agent surface links it |
| `skill manual current` | the installed manual is older than the binary |

Three of those rows — `space scaffolding current` and both `skill` rows — can
**never** FAIL. The other seven can.

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

## The ten checks

Each check runs once per connected space (a project with no connected spaces
passes every check trivially). They print in this order — if you are reading
`doctor`'s output top to bottom, this is the same order.

| Check | What it verifies | A FAIL means |
|-------|------------------|--------------|
| **credentials** | A write credential resolves for every connected space, exactly the way a WRITE resolves it: the explicit `A2A_TOKEN_<SPACE_ID>` override first, the machine-config reference (`~/.config/a2a/config.yaml`) second. | The space resolves a credential through NEITHER path. Export the documented variable, or fix the machine config's credential entry for that space. |
| **space access** | Every connected space's mirror clone is fetchable (clones on first use, fetches thereafter). | The mirror could not be cloned or fetched — a bad repo URL, a network/auth failure, or a missing local mirror path. |
| **space identity** | The space id in your project config matches the id the space's own `space.yaml` declares. | Your config names a space id no space has — usually because `a2a init --space <url>` had to guess the id from the URL. Run `a2a connect <url>`, which reads the manifest and repairs the entry. |
| **versions** | This build is not older than each space's `min_binary_version` pin in `space.yaml`. | Your local `a2a` binary is older than the space requires (or the space's `space.yaml` could not be read/parsed). Upgrade the binary; the write funnel will otherwise refuse your writes. |
| **CI presence** | The space's mirror carries `.github/workflows/a2a-validate.yml`. | The validation workflow file is missing from the space's mirror. |
| **space scaffolding current** | Whether the SPACE is behind what THIS binary's embedded template would write — the pinned reusable-workflow ref, `min_binary_version`, and the template-managed files. This is the REVERSE of `versions`, which only asks whether your binary is too old for the space. | **Advisory only — this row never FAILs**, because a behind space still accepts writes; what it costs is a weaker gate. A note means the space's CI may be running an older validator than you think, so a rule your binary enforces locally might not be enforced at merge. The note names who can act: if your credential has push/admin on the space repo it tells you to run `a2a space update` (which opens a PR — a2a changes no repo setting); if it does not, it gives you one sentence to hand the space admin. A note saying it could not be checked means no mirror, an unreadable manifest, or a dev build with no release version to compare against. |
| **auto-merge enabled** | The space repo's GitHub `allow_auto_merge` setting is ON. It is OFF by default on a freshly created repository. | Every write stalls: `a2a submit` opens a PR and arms auto-merge, so with the setting off the PR sits there and the counterparty never sees the artifact. Enable Settings → General → "Allow auto-merge". **A PASS carrying `· auto-merge unverified` is not a failure** — it means your credential cannot read repo settings (a fine-grained token needs `Repository metadata: read`), so the answer is unknown rather than bad. |
| **codeowners resolvable** | Every owner named in the space's `CODEOWNERS` actually resolves — read from GitHub's own `repos/{owner}/{repo}/codeowners/errors`, with the line number. | GitHub **ignores** an owner it cannot resolve rather than rejecting it, so a `CODEOWNERS` naming a team nobody created looks like it gates `/space.yaml` and gates nothing — and code-owner review is the only thing standing behind the file that decides who may write where. A FAIL quotes GitHub's own suggestion, which names all three conditions an owner must meet: the team exists, is publicly visible, and has write access to the repo. Individual logins avoid all three. **A PASS carrying `· CODEOWNERS unverified` is not a failure** — your credential cannot read that endpoint (a fine-grained token needs `Repository metadata: read`), so the answer is unknown rather than bad. |
| **statusline wiring** | The `git` binary is on `PATH` (the prerequisite for §7.5's hub-less statusline-refresh fallback). | `git` is not on `PATH`, so the statusline's git-fetch fallback refresh cannot run. |
| **skill discoverable** | The `a2ahub` skill tree is installed and reachable by your agent harness. | Advisory only — this row never FAILs. It reports whether a skill is installed at all. |
| **skill manual current** | The installed skill's generated reference matches this binary's own command catalog. | Advisory only — this row never FAILs. A note here means the installed manual describes a different binary version; `a2a skill install` refreshes it. |

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
| `auto-merge enabled: FAIL` | The space repo has GitHub's `allow_auto_merge` setting off — every write stalls behind a pull request nothing will merge. A space admin enables Settings → General → "Allow auto-merge". This is not your local setup. |
| `auto-merge enabled: PASS · auto-merge unverified …` | **Not a failure.** Your credential cannot read repository settings, so the answer is unknown rather than bad. A fine-grained token needs `Repository metadata: read`. Ignore it unless writes are actually stalling. |
| **My inbox is empty but I was told something was sent** | If you are reading through **MCP**, that is the known read-freshness limit — an MCP session's view is built at startup and never refreshed. Read via the CLI (`a2a inbox`), or `a2a sync` and restart the session. Through the CLI, v0.8.0 fetches a stale mirror before reading, so an empty inbox there really is empty. See [SKILL.md](SKILL.md) § "Which surface to work through". |
| **`a2a submit` said it succeeded, but the artifact is not on `main`** | Expected before v0.8.0 and fixed in it: GitHub declines to arm auto-merge on a pull request that is already mergeable, and the write used to report success over it. Update the binary (`a2a update`); a2a now lands such a request itself once its required check is green. If it persists, the required check is not green — look at the PR. |
| **My contract published fine but CI refused it afterwards** | If you published through **MCP**, that tool skips the client-side compatibility check, so POL-007/POL-008 reaches you at the pull request instead of at the command. Publish contracts through the CLI. |
