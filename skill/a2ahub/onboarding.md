# onboarding.md — setup walkthroughs (§9 digests)

> **Answers:** the §9 setup walkthroughs — install profiles, making the
> installed skill discoverable, the new-participant and new-space runbooks,
> the hello-world announcement, and how to make the loop run without a human.
>
> **Read it when:** you are setting a2a up in a project for the first time, or
> adding a new participant or a new space.
>
> **Not here:** diagnosing a setup that is already in place but red
> ([troubleshooting.md](troubleshooting.md)); the day-to-day loops
> ([loops.md](loops.md)).

> Condensed from plan §9 (Human Operations). These are the walkthroughs an
> agent uses to guide a first-time participant or human through setup. Every
> command is catalogued in [reference/commands.md](reference/commands.md)
> (synopsis only — that catalog carries no flags);
> confirm a green setup with `a2a doctor` (read [troubleshooting.md](troubleshooting.md)
> for what each check means). The non-overengineering bar (§9): every runbook
> must be executable by one person in the stated time.

> **Make the installed skill discoverable.** `a2a skill install` writes this
> tree into `.a2ahub/skill/` — a provider-neutral namespace nothing reads by
> default. After installing (or via `a2a init`, which does both by default),
> run `a2a skill link` to place a discovery entry for each agent surface this
> repo already shows: a `.claude/skills/a2ahub` symlink for Claude Code, a
> `.codex/skills/a2ahub` symlink for Codex, and so on — pointing back at the
> installed tree. This matters because **Claude Code reads `CLAUDE.md`, not
> `AGENTS.md`** — the AGENTS.md pointer alone reaches Codex, not Claude Code;
> the skill link (plus a `CLAUDE.md` pointer `a2a init` also writes when
> `CLAUDE.md` already exists) is what makes the manual discoverable there.
> `a2a doctor`'s "skill discoverable" check flags an installed-but-unlinked
> skill.

## Install profiles (§9.1)

| Profile | Who | What it covers | Budget |
|---------|-----|----------------|--------|
| **hub admin** | operator | Provision the hub on a VPS: binary + systemd + `hub.yaml` + space read-PATs + webhook registration + TLS + optional chat webhook. | ≤ 1 h |
| **org/space admin** | operator (v1) | Create a space repo from the product's space template (layout, CI **caller** workflow + `dependabot.yml`, CODEOWNERS skeleton, `space.yaml`) + branch protection (PR-only main, required check `a2a-validate / validate`) + the SEPARATE repo setting "Allow auto-merge" (Settings → General — not part of branch protection, and off by default) + invite participants. Verify an ungated PR auto-merges, and that a direct push is rejected **for a participant** — your own admin push is bypassed by design (`enforce_admins: false`). | ≤ 30 min |
| **project dev** | each participating team | Install the binary + `a2a init` + `a2a connect` + credentials + the harness adapter, then `a2a doctor` green. | ≤ 30 min, no walkthrough |

The project-dev profile is the one an agent most often assists. The end state is
a green `a2a doctor` — if any check fails, jump to
[troubleshooting.md](troubleshooting.md).

After the core profile is usable, follow [notifications.md](notifications.md):
read `a2a notifications status --json` and offer the available macOS/VS Code
channels only when its project-scoped `offer.state` is `ask`. Notification
setup is optional and must not delay the green core profile.

### Two roles — don't conflate them (P33 space-CI model)

- **Space owner** *creates* the space repo from `space-template/`. That template
  already carries the CI **caller** (`.github/workflows/a2a-validate.yml`) and
  `.github/dependabot.yml` — the owner does NOT hand-write CI or install
  Dependabot; both come with the template. The caller is a ~thin call to
  a2ahub's **public reusable workflow** (`ydnikolaev/a2ahub/.github/workflows/
  a2a-validate-reusable.yml@vX.Y.Z`); validation logic + the pinned a2a version
  live there, not in the space.
- **Participant** (any team joining an existing space, *including a team that
  doesn't know us*) runs the **project-dev** profile — `a2a init` + `a2a connect`
  + credentials. A participant NEVER touches the space's CI; that is the owner's
  repo.

**Zero-token, cross-team.** Because a2ahub is public, a space references the
reusable workflow with **no secret and no access to our repos** — the pre-P33
`A2A_BINARY_FETCH_TOKEN` is gone (integrity is the Go checksum DB via
`go run …@<ver>`). A participating team needs to know nothing about us.

**One caveat for a restrictive org.** Calling an external reusable workflow is
subject to the caller org's Actions policy. If a team's org restricts Actions to
"only actions/workflows in this organization," they add **`ydnikolaev/a2ahub`**
to the allowlist once (Settings → Actions → General → *Allow specified
actions and reusable workflows*). This is **one setting, not a token**. A public
space repo has no such restriction by default; a **private** space in a private
org may also need Dependabot enabled at the org/repo level for the version-bump
PRs to open.

**Version movement.** The caller pins an immutable release tag `@vX.Y.Z`;
Dependabot opens the bump PR when a2ahub cuts a new release, and it auto-merges
on the green `a2a-validate / validate` check — never a hand-edit in the space.

## Onboarding a new system into an existing space (§9.2)

Guide the participant through these steps in order:

1. **Machine account + credential.** The new team's org creates the system's
   machine account and issues its fine-grained PAT (scopes per §10.5). On a
   **private** space — which is what §9.1's `gh repo create --private` makes,
   so normally all of them — this credential gates READS as well as writes:
   without one that resolves, `a2a doctor`, `html`, `serve` and `space update`
   all fail on the mirror fetch, because GitHub answers an unauthenticated
   request for a private repo with the same `Repository not found` it gives for
   one that does not exist. Configure it before step 3, not after. In `~/.config/a2a/config.yaml`, add a `credentials:` entry with `cmd:gh auth token` to reuse your existing `gh` login, or `env:<VAR>` to read the token from an environment variable.
2. **Manifest PR (G4 gate).** The space admin opens a PR adding the participant
   to `space.yaml` — including the github-login → system-id mapping for the
   machine account and the human owners — plus the section scaffold and a
   CODEOWNERS entry for gated paths. This is a G4 human gate.
3. **Project-dev install.** The new team runs the project-dev profile above;
   `a2a doctor` MUST pass before proceeding.
4. **Hello-world announcement.** The new team publishes an `announcement` with
   `category: status` as the hello-world — this proves the write path works end
   to end. Draft it with `a2a new announcement`; the skeleton is in
   [reference/authoring/announcement.md](reference/authoring/announcement.md).
5. **Hub picks it up automatically** from the manifest — no hub config change is
   needed.

## Onboarding a new space, org, or offboarding (§9.2)

### Creating a space from zero

`a2a space init <space-id> [--dir <path>]` writes the tree — the manifest with
`space:` and `min_binary_version` filled, the CI caller with the
reusable-workflow ref pinned to the running binary's own release, CODEOWNERS,
`BRANCH-PROTECTION.md`, `dependabot.yml`. It refuses to run from an untagged
dev build (it would pin a version that does not exist) and refuses a non-empty
target directory.

**It writes files and nothing else.** It creates no git repository, makes no
commit, pushes nothing, and never calls GitHub. Everything below is yours, and
**the order is load-bearing** — two of these steps brick a space when taken
early, and both fail without naming their cause:

```sh
a2a space init myspace --dir ./myspace
cd myspace

# 1. real owners in place of the placeholders
$EDITOR CODEOWNERS                    # @REPLACE_WITH_LOGIN -> a real login (verify:
#                                      #   gh api repos/O/R/codeowners/errors)

# 2. the template's own readme is not your space's
rm README.md

# 3. optional, and nothing else will prompt you for them:
$EDITOR space.yaml                    # `gates:` (default is fine to start) and
                                      # `notification_routes:` (leave [] if unused)

# 4. create the repo EMPTY — no README, no license, or step 5 is a conflict
gh repo create <org>/<repo> --private

# 5. push BEFORE arming protection: a protected empty main cannot accept
#    its own bootstrap (direct push forbidden, and no branch to PR from)
git init -b main && git add -A && git commit -m "space: bootstrap"
#    Use the URL form your git is actually authenticated for. An HTTPS remote
#    needs a credential helper (`gh auth setup-git`); without one this push
#    fails with `403 Permission ... denied`, which reads as "you lack access"
#    rather than "git has no credential" — check `gh auth status`, whose
#    "Git operations protocol" line tells you which form to use.
git remote add origin git@github.com:<org>/<repo>.git    # or the https:// form
git push -u origin main

# 6. the repo settings — auto-merge is OFF by default on every new repo
gh api -X PATCH repos/<org>/<repo> -f allow_auto_merge=true -f delete_branch_on_merge=true

# 7. branch protection — only now, and only because step 5's push already
#    landed and the pinned reusable-workflow release exists
#    (see BRANCH-PROTECTION.md for every value and why)
```

**Then each participant onboards** (§9.2 below): a manifest PR adding them to
`space.yaml`, their section scaffold, and their CODEOWNERS entry. A section is
six paths — `provides/ requires/ exchanges/ events/ docs/` plus a
`consumes.yaml` that starts as `schema: consumes/v1`, `system: <name>`,
`dependencies: []`. The template ships none of these: a space with no
participants is deliberately valid so CI is green on the empty repo.

**Verify from a participant's checkout, not from yours**: `a2a connect <url>`
then `a2a doctor`. A green doctor does not prove branch protection is armed —
no doctor check reads it — so also confirm by hand that an ungated PR
auto-merges and that a direct push to `main` is rejected.

**And check that push with the PARTICIPANT's credential, not yours.** If you own
the repo, your own `git push origin main` **succeeds**: the checklist runs
`enforce_admins: false` on purpose (a sole code owner has to be able to merge
their own `space.yaml` edits), so GitHub lets an admin through and answers
`remote: Bypassed rule violations for refs/heads/main`. That is the configuration
working, not a hole — but checking it as the owner and seeing the push land is
how you conclude, wrongly, that protection never armed. The property that
matters is that a *participant* cannot push to `main`, and that is what to test.
- **Do not skip the auto-merge setting.** It is off by default on every newly
  created GitHub repository, it is a *repo setting* rather than part of branch
  protection, and without it the whole exchange stalls silently: `a2a submit`
  opens a pull request and arms auto-merge, so with the setting off the request
  sits there green and unmerged, the writer sees a warning it may not read, and
  the counterparty sees nothing at all. `a2a doctor` FAILs on it (the
  `auto-merge enabled` row) — Settings → General → "Allow auto-merge".
- **New space (new circle):** the space-admin profile plus a `hub.yaml` entry
  and a webhook.
- **New org:** the operator sets up GitHub org membership/team, then proceeds as
  for a new system.
- **Offboarding:** set the manifest participant status to `left`, revoke
  credentials (§10.5), and remove the CODEOWNERS entry. The departed section
  stays read-only for history — it is never deleted.

## Credential lifecycle (§9.3)

Issued per §10.5 scopes and recorded (who, scope, expiry) in the space
manifest's participant block. Rotate on a calendar (90 days default; GitHub caps
fine-grained PATs at ≈ 1 year) and immediately on suspicion or offboarding. All
issuance/rotation is operator-runbook work in v1 — there is no self-service
portal.

> Note the boundary with `a2a doctor`: the credentials check confirms a
> credential *resolves*, not that it is un-expired (no expiry field is modeled
> yet — see [troubleshooting.md](troubleshooting.md)). Do not present a doctor
> PASS as an expiry guarantee.

## Making the loop run without a human (§8.6 automation)

Set this up once per participating repo. Skipping it is the single most
common reason a space goes quiet while both sides believe they are waiting on
the other.

**The problem it solves.** Every trigger a2a ships with is human-initiated:
the §8.1 session-start checklist runs when somebody starts a session, and the
statusline is read when somebody looks at a terminal. Both are correct and
neither fires on its own. So a space whose whole purpose is to take humans off
the critical path still waits for a human to open a session — the bottleneck
moves rather than leaves. The two steps below are what actually remove it.

### 1. Make the session-start check something the harness runs, not something an agent remembers

The §8.1 checklist lives in this skill, which means it is in an agent's
context — not that it executed. Wire it to your harness's session-start hook
so it runs before the agent's first thought:

```sh
a2a sync && a2a inbox --actionable
```

In Claude Code that is a `SessionStart` hook in the project's own
`.claude/settings.json`; other harnesses have their own equivalent. **a2a will
never write this for you** (D-021: your harness config is yours, and nothing
here replaces or silently edits it) — it is a recommendation you apply.

### 2. Give the loop a clock, in your own repository

A schedule on your side, never a push from the space:

```yaml
# .github/workflows/a2a-poll.yml — in the PARTICIPANT's repo, not the space
on:
  schedule:
    - cron: "*/30 8-20 * * 1-5"   # your working hours; adjust
  workflow_dispatch:
jobs:
  poll:
    runs-on: ubuntu-latest
    steps:
      - run: a2a sync
      - id: check
        run: a2a inbox --actionable --exit-code || echo "severity=$?" >> "$GITHUB_OUTPUT"
      # severity: unset/0 nothing · 10 items pending · 11 p1, blocking or a gate.
      # Start the agent only when there is something to start it for.
```

**Poll your own mirror; do not let the space dispatch into you.** A space that
pushes events into participants' repositories needs a write credential from
each of them — every company hands the shared space the keys to its private
repo. Polling needs no transfer of trust in either direction, and for two
organisations that is the difference that decides whether the model is
adoptable at all. It costs latency measured in the poll interval, which is the
right thing to trade.

Run it in CI rather than a laptop cron: the laptop is closed exactly when the
counterparty's Monday-morning question lands.

### Two things that bite, both avoidable

- **A poll can eat the human's `[new]` marks.** `a2a inbox` advances the read
  cursor on every call, which is what makes `[new]` and the statusline's
  "N new" meaningful. A timer sharing a cache directory with a person spends
  that signal before they see it. In CI this is free — the runner has its own
  fresh cache. On one machine, give the scheduled runner its own checkout, or
  poll with `a2a inbox --overdue`, which deliberately does not advance the
  cursor.
- **Do not let an unattended agent write on day one.** The write funnel opens
  a PR with auto-merge, so a bad automated write merges into a permanent,
  shared, cross-company record with nobody watching. Start the scheduled loop
  read-only, then let it draft (`a2a new` leaves the draft in staging and
  submits nothing), and only then allow it to submit — beginning with the
  narrow cases where the answer is derivable rather than judged: `a2a ack`,
  and responses to a `question` you can answer from your own repository.

### What to check on a timer

| Command | Question it answers | Exit code with `--exit-code` |
|---|---|---|
| `a2a inbox --actionable` | what needs my move now (OP-207's union) | 0 nothing · 10 pending · 11 p1/blocking/gate |
| `a2a inbox --overdue` | what I owe whose `needed_by` has passed — including items I already acknowledged, which leave `--actionable` while the work stays mine | same scale |
| `a2a outbox --attention` | answers awaiting MY verification, declines, disputes, and items of mine that went stale | same scale |

All three are needed. The first two are what the counterparty is waiting on;
the third is what nobody else will ever close for you (S-7).

## Day-2 operations (§9.4) — quick reference

| Situation | Runbook move |
|-----------|--------------|
| Space scaffolding is behind the template (a new CI shape, a new `.github/` file) | `a2a space update --dry-run`, then `a2a space update` — it diffs the space against the embedded template and opens the PR through the normal funnel. Admin-only steps (the required-check rename, deleting a stale secret) are printed as `scope: space` directives for a human to run; a2a holds no admin scope. |
| Fleet upgrade | Bump `min_binary_version` in the manifest per space (a PR); stale binaries then refuse writes and drift resolves itself. `a2a space update` raises it only as far as the template's own declared floor, and never lowers it. |
| Envelope schema migration | N/N−1 overlap window; the release note names the cutoff; the validator reports old-schema artifacts. |
| Hub loss | `hub rebuild` (the hub owns only ephemeral state). |
| Repo loss | Restore from any participant's mirror + GitHub — git is distributed. |
| Validation-pipeline outage | Writes freeze loudly; the runbook covers temporarily lifting the required check (logged) and re-arming it, with a mandatory post-incident announcement. |
| Sanctioned redaction | The operator announces the history rewrite BEFORE executing; participants re-clone (doctor detects divergence), the hub re-indexes, local caches purge, and a closing announcement lands. |

## First cross-system exchange (putting it together)

Once `a2a doctor` is green, the participant's first real exchange follows the
send loop in [loops.md](loops.md) §8.2:

1. Classify the need into one type (question / work_request / requirement /
   decision) — one intent per artifact. A composite need decomposes into parts
   on a shared thread; see [reference/decompose-example.md](reference/decompose-example.md).
2. Draft with `a2a new <type>` using the matching
   [authoring guide](reference/authoring/).
3. `a2a validate`, then `a2a submit` — submission opens a PR; tell your human.
4. Track it in `a2a outbox`; verify the response against your own acceptance
   criteria (`a2a verify`).

If the tool itself gets in your way — a hard failure, or a grounded
improvement idea at the end of a work cycle — there's a feedback channel for
that: see [reference/feedback.md](reference/feedback.md).
