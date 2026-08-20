# troubleshooting.md — reading `a2a doctor`

> **Answers:** how to read `a2a doctor` output — what each check inspects,
> what a FAIL means, and what to do next.
>
> **Read it when:** `a2a doctor` is not green, or a command failed in a way
> that smells like setup rather than protocol.
>
> **Not here:** first-time setup itself ([onboarding.md](onboarding.md)); what
> to do when the tool is working and still got in your way — that is the
> feedback loop ([loops/feedback.md](loops/feedback.md)).

> **Defer to the binary.** This file interprets the output the `a2a doctor`
> command actually produces. It does NOT invent failure modes or restate
> aspirational checks that the binary does not yet run. When in doubt, run the
> command and read its own message — the FAIL detail string tells you the
> concrete cause. The verb catalog is
> [reference/commands.md](reference/commands.md) — synopsis only, no flags.

## What `a2a doctor` does

`a2a doctor` runs a fixed set of basic health checks for the local project and
its connected spaces, printing one line per check. It exits `0` only if every
check passes.

**Output shape.** One line per check:

```
credentials: PASS
space access: PASS
space identity: PASS
participant avatars: PASS · local participant avatars are missing (getvisa: ydnikolaev) — run `a2a sync`; initials remain available meanwhile
versions: FAIL: <detail>
CI presence: PASS
space scaffolding current: PASS · the space is behind this binary's template (workflow ref, write floor): ask the space admin to run `a2a space update`
auto-merge enabled: PASS · auto-merge unverified for <space>: the credential cannot read this repo's settings (a fine-grained token needs "Repository metadata: read")
stuck green PRs: PASS
default branch healthy: PASS
codeowners resolvable: PASS
threads intact: PASS
skipped mirror files: PASS
notification components: PASS · notifications not enabled for this project
statusline wiring: PASS
skill discoverable: PASS · no a2ahub skill installed
skill manual current: PASS · no skill installed
notify-routes: PASS · no notification routes configured for this space
notify-secret: PASS · no route names a secret, so none is required
notify-delivery: PASS · no a2a-notify run recorded yet for this space
repository visibility [getvisa]: PASS: no observed mismatch
```

All twenty-one fixed checks, in the order the binary prints them, followed by
one `repository visibility` row per connected space (this project has one,
`getvisa`) — that row is not one of the fixed twenty-one and always prints
LAST, after every one of them. **A `PASS` carrying a note is a pass** — do
not report it as a problem.

Twelve rows can print `PASS · <note>`:

| Row | When it carries a note |
|---|---|
| `credentials` | the token resolves but advertises no `workflow` scope |
| `versions` | a newer release is available (never a failure — the floor is a separate check) |
| `participant avatars` | one or more active participant owners have no validated image in the disposable local cache |
| `space scaffolding current` | the space is behind this binary's template, or the answer could not be determined. An **in-sync** space gets a bare `PASS` with no note |
| `auto-merge enabled` | the setting could not be read (no host reader wired, or the API refused) |
| `stuck green PRs` | open-PR state could not be read, so the result is unknown rather than healthy |
| `codeowners resolvable` | GitHub's CODEOWNERS diagnostics could not be read, so the result is unknown rather than healthy |
| `skill discoverable` | no skill installed, or installed but no agent surface links it |
| `skill manual current` | the installed manual is older than the binary |
| `threads intact` | the space holds artifacts written before threads existed, so they carry no `thread:` |
| `skipped mirror files` | a file in the mirror could not be decoded, so it is missing from every read verb's output |
| `notification components` | notifications are not enabled, or this build has no notification probe wired |
| `notify-routes` | a route in `space.yaml` is malformed, or names a participant the manifest does not list |
| `notify-secret` | a route names a secret the space repo does not hold — presence only; doctor never reads a secret's value |
| `notify-delivery` | the last `a2a-notify` run on `main` failed, or there has never been one |

The three `notify-*` rows are the SPACE plane — the chat notifications a space's
own CI sends. `notification components` just above them is the LOCAL plane
(macOS, VS Code). Two planes, adjacent names; see
[notifications.md](notifications.md) for which is which.

Six of those rows — `participant avatars`, `space scaffolding current`, both
`skill` rows, `threads intact` and `skipped mirror files` — can **never** FAIL.
The other ten can.

`threads intact` and `skipped mirror files` never FAIL for the same reason and it is worth understanding,
because it changes what you do about the note: **neither condition is
necessarily yours to fix.** A pre-thread artifact cannot be repaired in place
at all (git history is the archive — artifacts never move or get deleted), and
a malformed file may sit in a COUNTERPARTY's section of a shared space, where
this project's own diff-authz means you structurally cannot edit it. A row that
went red on someone else's file would be permanently red, and a permanently red
row is one nobody reads. So both report, name the paths, and pass.

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

## The twenty-one checks

`doctor` prints a fixed set of exactly twenty-one checks — that fixed count is
what this heading names, and it is why the count and the heading are kept in
lockstep by the test guarding this file. That lockstep was NOT holding: this
heading still said "seventeen" on 2026-08-18 while the sentence forty lines up
said "twenty". The tripwire did check the heading — against the hardcoded
string `"## The seventeen checks"`, frozen at the roster size on the day it was
written. An expectation spelled as a literal cannot notice that the thing it
describes has moved; it can only notice somebody editing the doc without
editing the test, which is the opposite of its job. The number is now DERIVED
from the binary's own roster, and the summary sentence — which had no assertion
at all — is checked too. **Each of the twenty-one
runs once per PROJECT, not once per connected space**: when a check must reason about
several connected spaces, it folds any per-space detail into a single line
(`space access` and `credentials`, for example, join per-space failures with
`; `). They print in the order below, top to bottom, matching `doctor`'s own
output.

The table carries a twenty-second row, `repository visibility`, because it is
real output you will see — but it is NOT one of the fixed twenty-one and shares
neither their order nor their cardinality. It prints ONE row PER CONNECTED
SPACE, after every fixed check, so it is always LAST in the output and absent
entirely from a project with no connected spaces. Its printed name carries
the space id (`repository visibility [getvisa]`), which no other row does.

**So the table below is in output order, all twenty-two rows** — read it top to
bottom and you are reading `doctor`'s own sequence. That is worth stating
because it has now been false TWICE. The first time, until 2026-08-12, this row
sat eighth in the table under a sentence promising output order while the
binary printed it last. The second time, until 2026-08-18, the three `notify-*`
rows sat AHEAD of the two `skill-*` rows here while the binary prints them
after — shipped by the epic that added them, under the same promise, and found
only because a different change made the roster tripwire count wrong. A
sentence promising an order that nothing checks will keep going stale; the
tripwire now asserts the ORDER, not only the membership and the count.

| Check | What it verifies | A FAIL means |
|-------|------------------|--------------|
| **credentials** | A credential resolves for every connected space, by the write precedence: the explicit `A2A_TOKEN_<SPACE_ID>` override first, the machine-config reference (`~/.config/a2a/config.yaml`) second. **The same credential now serves reads**, so on a PRIVATE space this row failing is what makes every read verb fail too. | The space resolves a credential through NEITHER path. Export the documented variable, or fix the machine config's credential entry for that space. A `credentials:` entry may be `env:<VAR>` (an environment variable) or `cmd:gh auth token` (reusing an existing `gh` login rather than requiring a new token). On a public space a missing credential costs only writes — reads deliberately degrade to unauthenticated and keep working. |
| **space access** | Every connected space's mirror clone is fetchable (clones on first use, fetches thereafter), using the space's resolved credential. | The mirror could not be cloned or fetched — a bad repo URL, a network/auth failure, or a missing local mirror path. **When GitHub answered `Repository not found`, read the rest of the line before doing anything**: that error means "does not exist" and "you may not see it" identically, so doctor appends what it already knows. If the API can read the repo with the same credential it says the repository EXISTS and is private or internal — the repo is fine and the fetch was the problem. If no credential resolved at all it says so and names the `A2A_TOKEN_<SPACE>` variable and the machine config file. A public repo gets no such sentence, and then the 404 really does mean a wrong URL or a deleted repo. |
| **space identity** | The space id in your project config matches the id the space's own `space.yaml` declares. | Your config names a space id no space has — usually because `a2a init --space <url>` had to guess the id from the URL. Run `a2a connect <url>`, which reads the manifest and repairs the entry. |
| **participant avatars** | Every active owner in each readable `space.yaml` has a validated image in the project-local disposable cache. The check makes no network request. | Advisory only — this row never FAILs. A note names supported owners whose image is missing and tells the agent to run `a2a sync`, which refreshes the cache idempotently. An owner identifier outside GitHub's login grammar is named separately and must be corrected in `space.yaml`; doctor never recommends a sync that cannot help. Until then every UI keeps the owner's initials, so work and offline rendering remain fully available. |
| **versions** | This build is not older than each space's `min_binary_version` pin in `space.yaml`. The literal local build stamp `dev` reports an unreleased-build advisory and skips the comparison; any other malformed version still fails. | Your released `a2a` binary is older than the space requires (or the space's `space.yaml` could not be read/parsed). Upgrade the binary; the write funnel will otherwise refuse your writes. |
| **CI presence** | The space's mirror carries `.github/workflows/a2a-validate.yml`. | The validation workflow file is missing from the space's mirror. |
| **space scaffolding current** | Whether the SPACE is behind what THIS binary's embedded template would write — the pinned reusable-workflow ref, `min_binary_version`, and the template-managed files. This is the REVERSE of `versions`, which only asks whether your binary is too old for the space. | **Advisory only — this row never FAILs**, because a behind space still accepts writes; what it costs is a weaker gate. A note means the space's CI may be running an older validator than you think, so a rule your binary enforces locally might not be enforced at merge. The note names who can act: if your credential has push/admin on the space repo it tells you to run `a2a space update` (which opens a PR — a2a changes no repo setting); if it does not, it gives you one sentence to hand the space admin. A note saying it could not be checked means no mirror, an unreadable manifest, or a dev build with no release version to compare against. |
| **auto-merge enabled** | The space repo's GitHub `allow_auto_merge` setting is ON. It is OFF by default on a freshly created repository. | Every write stalls: `a2a submit` opens a PR and arms auto-merge, so with the setting off the PR sits there and the counterparty never sees the artifact. Enable Settings → General → "Allow auto-merge". **A PASS carrying `· auto-merge unverified` is not a failure** — it means your credential cannot read repo settings (a fine-grained token needs `Repository metadata: read`), so the answer is unknown rather than bad. |
| **stuck green PRs** | Every historical open PR whose required check is explicitly green has auto-merge armed. | A named PR is green and unmerged but no automatic merge is armed. Doctor prints the exact safe `gh pr merge <n> --auto --repo <owner>/<repo>` remedy and never merges it itself. A PASS carrying `· stuck-green state unverified` means host state or credentials were unavailable, not that no stuck PR exists. |
| **default branch healthy** | The space's default branch (`main`) is not failing its own post-merge audit — the conclusion of the required check run on that branch, read through the same selection the PR gate uses. | The branch every participant pulls from and merges onto is red, and the detail names the space, the check run that answered, and its conclusion. Fix the branch before merging onto it: every subsequent merge stacks onto an already-failing main. **A PASS carrying `· unverified` or `· still running` is not a failure** — the credential could not read the host, no required check run matched, or a run is still in flight, so the answer is unknown rather than bad. This row exists because it did not: on 2026-08-12 `doctor` answered PASS on every row while a space's default branch had been red for four hours (`fb-20260812-ee6dcd`). |
| **codeowners resolvable** | Every owner named in the space's `CODEOWNERS` actually resolves — read from GitHub's own `repos/{owner}/{repo}/codeowners/errors`, with the line number. | GitHub **ignores** an owner it cannot resolve rather than rejecting it, so a `CODEOWNERS` naming a team nobody created looks like it gates `/space.yaml` and gates nothing — and code-owner review is the only thing standing behind the file that decides who may write where. A FAIL quotes GitHub's own suggestion, which names all three conditions an owner must meet: the team exists, is publicly visible, and has write access to the repo. Individual logins avoid all three. **A PASS carrying `· CODEOWNERS unverified` is not a failure** — your credential cannot read that endpoint (a fine-grained token needs `Repository metadata: read`), so the answer is unknown rather than bad. |
| **threads intact** | Every artifact in the space carries a `thread:`, so `a2a thread` can place it in a conversation. | Advisory only — this row never FAILs. A note counts the artifacts written before threads existed and names their spaces. Those predate thread propagation: `a2a respond` refuses to reply to one, and they cannot appear in any thread view. Nothing repairs them in place; a new exchange that references them starts a fresh thread. |
| **skipped mirror files** | Every `.md` artifact and every event file in each mirror actually decodes, so the read model holds the whole space. | Advisory only — this row never FAILs. A note names each undecodable file and why (`unreadable`, `not-frontmatter-shaped`, `undecodable-yaml`, `no-id`, `unrelativizable-path`). **This is the row to read when a document you know exists does not show up.** A skipped file is missing from `search`, `inbox`, `outbox`, `thread` and `statusline` — `show` still prints it, because that path parses the file itself. Fixable only by whoever owns that file's section, which under diff-authz may not be you. |
| **notification components** | Every channel enabled for this project has an installed component with a compatible CLI handshake; macOS permission/login-item state is healthy when available. | The companion is absent, version-skewed, permission-denied, awaiting the user's Gatekeeper decision, or its login item is missing. Run `a2a notifications status --probe --json`, then repeat `a2a notifications install --channel <channel>` to repair it. For macOS `approval-required`, the human opens A2A Notifier once and explicitly chooses **System Settings → Privacy & Security → Open Anyway**; never clear quarantine or disable Gatekeeper for them. |
| **statusline wiring** | The `git` binary is on `PATH` (the prerequisite for §7.5's hub-less statusline-refresh fallback). | `git` is not on `PATH`, so the statusline's git-fetch fallback refresh cannot run. |
| **skill discoverable** | The `a2ahub` skill tree is installed and reachable by your agent harness. | Advisory only — this row never FAILs. It reports whether a skill is installed at all. |
| **skill manual current** | The installed skill's generated reference matches this binary's own command catalog. | Advisory only — this row never FAILs. A note here means the installed manual describes a different binary version; `a2a skill install` refreshes it. |
| **automation coverage** | Nothing — and it says so. It names the two automations `onboarding.md` §8.6 recommends (a harness session-start hook, and a scheduled `a2a-poll.yml` in the PARTICIPANT's own repository) and states that doctor cannot observe either: the first is your harness's own config, which D-021 forbids a2a to read or write, and the second lives in a repository doctor is never pointed at. | It cannot FAIL. Always PASS-with-advisory, because a FAIL would claim doctor saw a broken state it structurally cannot see, and a bare PASS would be silent about something every new participant is told to set up. Check both by hand. |
| **notify-routes** | Every `notification_routes[]` entry in the space's `space.yaml` is well-formed and names a participant the manifest actually lists. | A route is malformed, or its `for:` names a system absent from `participants[]`. The space's own PR gate refuses both (POL-021 / REF-022), so this row failing means a route reached `main` before the rule existed, or the manifest changed under it. Fix the route in `space.yaml` through the normal write funnel. |
| **notify-secret** | Every secret a route names exists on the space repo. **Presence only — doctor never reads a secret's value.** | A route names a secret the repo does not hold, so its messages cannot be sent. Run `a2a notify setup`, or set it directly with `gh secret set <NAME> --repo <space-repo>`. A route with no secret configured is not an error until it has one to lose. |
| **notify-delivery** | The most recent `a2a-notify` run on `main` concluded green. | The last run failed, or there has never been one. A failed run is the honest state a silent channel would hide — read it with `gh run list --workflow a2a-notify.yml --repo <space-repo>`. "Never run" is normal for a space that has just configured its first route and not yet pushed. |
| **repository visibility** | Whether the space repo's GitHub visibility matches what the material inside it is classified as. Visibility is read from the host; classification comes from the artifacts themselves. | **This row never FAILs — it PASSes or reports UNVERIFIED**, because visibility is evidence, never write authority. A **WARN** on a PUBLIC repo means it carries higher-classified material than `public`: that is the one reading to act on, and the fix is the repository's visibility or the material's classification, not this row. On a **private or internal** repo carrying nothing `restricted`, it PASSes as "no observed mismatch". On a private repo that DOES carry `restricted` material it PASSes only when a bilateral proof source is named; otherwise it reports UNVERIFIED with "repository privacy alone does not prove intended pairwise audience" — which is **not a problem to fix**. It says the honest thing: a private repo proves the audience is small, never that it is the specific pair the material was restricted to. An UNVERIFIED reading "visibility could not be verified" means the credential could not read repo metadata (a fine-grained token needs `Repository metadata: read`), so the answer is unknown rather than bad. |
| **consumed contract** | Whether this system verify-passed any data-package delivery — a handoff deliverable of kind `data`, accepted (verify-passed) — pinning a contract absent from its own `consumes.yaml` (P6, defects-fix-2026-08, `docs/inbox/defects/07-the-consumer-registered-nowhere.md`: a real space had verify-passed four such handoffs while its own `dependencies:` sat `[]`). | **This row never FAILs, and prints nothing at all when there is nothing to report** — detection only, never a gate: `a2a doctor`'s exit code and `contract retire`'s own precondition (D-022) are both unaffected by it. A **WARN** names the contract, the number of distinct verify-passed packages conforming to it, and the exact fix — `a2a contract adopt <XC-id>` — because consuming without adopting is a real risk (the retire gate and the deprecation notice cannot protect a dependency nobody registered) but not necessarily a mistake: the operator may have reasons. An **UNVERIFIED** means this system's own `consumes.yaml` exists but could not be read as a real `consumes/v1` registry, so the advisory is skipped rather than risking a false "unadopted" claim against a registration it simply could not parse. A contract this system publishes itself is never named here — a producer is never counted as its own unadopted consumer. |

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
  test of statusline rendering. Test the render/consumer pipe separately with
  `a2a statusline --sample --json`; it needs no connected space or cache.

If a user needs the stronger guarantees (expiry warnings, host-drift diff),
that is a v2/`--space` concern — say so rather than implying doctor already
covers it.

## Common resolutions

| Symptom | First move |
|---------|-----------|
| `cannot load project/machine config` (exit 1, stderr) | Run `a2a init` / `a2a connect` first — the config does not exist or is malformed. See [onboarding.md](onboarding.md). |
| `credentials: FAIL` | Add or fix the space's credential reference in the machine config; confirm the token is valid. |
| `space access: FAIL` | Read the whole line first — on a `Repository not found` it already names the cause. "the repository EXISTS and is private/internal" means set the credential, not fix the URL; "ran unauthenticated" names the variable to set. Absent such a sentence, check the repo URL and your network/auth; a first `a2a sync` clones the mirror. |
| `participant avatars: PASS · local participant avatars are missing …` | No human action is required. The agent runs `a2a sync`; the next dashboard build uses the local images and initials remain available until then. |
| `participant avatars: PASS · owner identifiers cannot be fetched …` | Correct the named owner in the shared `space.yaml` to its GitHub login. Sync is deliberately not prescribed because it cannot repair an invalid identifier. |
| `versions: FAIL: … older than min_binary_version` | Upgrade the `a2a` binary to at least the pinned version. |
| `CI presence: FAIL` | The space repo is missing the validate workflow — a space-admin fix (see [onboarding.md](onboarding.md), space-admin profile). |
| `notification components: FAIL` | Inspect `a2a notifications status --probe --json`; repair the named channel with `a2a notifications install --channel <channel>`. |
| `statusline wiring: FAIL` | Install `git` / put it on `PATH`. |
| `--space: v1-min: not available` (exit 2) | The host-drift diff is v2; drop the flag. |
| `auto-merge enabled: FAIL` | The space repo has GitHub's `allow_auto_merge` setting off — every write stalls behind a pull request nothing will merge. A space admin enables Settings → General → "Allow auto-merge". This is not your local setup. |
| `auto-merge enabled: PASS · auto-merge unverified …` | **Not a failure.** Your credential cannot read repository settings, so the answer is unknown rather than bad. A fine-grained token needs `Repository metadata: read`. Ignore it unless writes are actually stalling. |
| **My inbox is empty but I was told something was sent** | CLI reads and MCP tool calls both fetch before reading. Check stderr/server logs for `pre-call mirror refresh failed; serving from the last good view`: that warning means MCP deliberately served the last good mirror after a network/auth failure. Restore access and retry (or run `a2a sync` to diagnose it). Without a refresh warning or skipped-file advisory, the current mirror has no matching actionable item. See [SKILL.md](SKILL.md) § "Which surface to work through". |
| **`a2a submit` said it succeeded, but the artifact is not on `main`** | Expected before v0.8.0 and fixed in it: GitHub declines to arm auto-merge on a pull request that is already mergeable, and the write used to report success over it. Update the binary (`a2a update`); a2a now lands such a request itself once its required check is green. If it persists, the required check is not green — look at the PR. |
| **`a2a submit` refused with POL-010 "still the template's own placeholder value"** | Exactly what it says: a frontmatter field still carries the literal `<…>` the template shipped, and the refusal names the field by its full path. `a2a validate` passes on the same file ON PURPOSE — a draft is allowed to hold its placeholders so you can inspect it; the refusal lands where the draft becomes a shared record. Two ways to clear it, and the message names the one that applies: **fill it** (`a2a new … --field <path>=<value>` — the path may be dotted, e.g. `--field expected_response.shape="yes/no plus the flag that controls it"`), or **delete the line** when the field is genuinely unused (`needed_by` only when no response is expected; `valid_until` / `env_requirements` when inapplicable). A response-bearing cross-system ask gets its autonomous +1/+2-day `needed_by` per [the send loop](loops.md). For a value inside a LIST (`refs`, `deliverables`, `acceptance_criteria`) `--field` cannot reach it — edit the drafted file. |
| **A document I know exists does not appear in `search`/`inbox`/`thread`** | Run `a2a doctor` and read the `skipped mirror files` row. If the file could not be decoded it is missing from every read verb and the verbs now say so on **stderr** (`note: … could not be decoded …`) — check whether you were discarding stderr. `a2a show <id>` still prints such a file, which is why the two can disagree. A skipped file is fixable only by whoever owns its section. |
| **`a2a validate` / `a2a submit` refused with REF-017 "body names a digest no declared attachment carries"** | Three things are true at once and all three must be, so read which one you can change: the body names a `sha256:` no `attachments[].digest` carries, **and** the body enumerates what reads as a file tree, **and** the envelope declares no attachment at all. This is the "referenced but not received" shape — the artifact promises bytes and carries none. **The fix is `a2a attach`**, not a reworded sentence. A digest named in ordinary prose, with no file list, does **not** refuse: it warns (POL-017), because a pin is not a possession claim. Note the scope: this refuses at the write gate (`a2a validate`, `a2a submit`, `a2a validate --ci` on a pull request) and returns no verdict in the post-merge audit — a rule that could only be satisfied by editing an immutable artifact must not judge history that cannot be repaired. |
| **`a2a validate` / `a2a submit` refused with POL-022 "handoff body carries N of 5 §16.2 required section(s) with no content"** | A `handoff`'s five body sections (`## Context`, `## What was built`, `## How to verify`, `## How to operate`, `## Limitations & next steps` — the exact headings [`schemas/templates/v1/handoff.md`](../../schemas/templates/v1/handoff.md) ships) are REQUIRED CONTENT per [the-plan §16.2](../../docs/the-plan/plan/16-handoff-directive.md#162-required-content-envelope-extension--body-sections), written for a reader with **no access to your private repo, chats, or history** — not a template's suggestion. The message names every section that is either missing its heading or carries zero non-whitespace text beneath it; the fix is to write that section, not to pad it. There is no length floor: one real sentence, a fenced command block, a bullet list, or a single link all count as content — the rule measures **emptiness**, never quality, so a judgement call ("is this section good enough") is never what refuses. Scope, corrected 2026-08-20 after it was measured rather than read: this refuses at `a2a validate` and at `a2a validate --ci` on a pull request. It does **NOT** refuse at `a2a submit` — on either surface. `SubmitValidatorAdapter.ValidateSubmit` (`internal/cli/adapters.go`, and its `internal/mcp` twin) partitions the outgoing `FileWrite` set into events and drafts and runs the referential checks over DRAFTS only; a `verify`/`close` submission carries event files exclusively, so that loop never executes. `a2a verify` with an incomplete `verdicts[]` therefore exits 0 locally and is refused later, at the merge gate. Filed in `docs/backlog.md` and returns no verdict in the post-merge audit, mirroring REF-017's own reasoning — a merged handoff's body is immutable, and no verb rewrites it. |
| **`a2a verify` / `a2a close` refused with REF-023 "verdicts\[\] names N of M declared acceptance criteria... missing: ..."** | The parent you are verifying or closing declares `M` `acceptance_criteria[]`, and your event's `verdicts[]` names fewer than `M` of them (measured by DISTINCT criteria, not by entry count — naming the same one twice still leaves the others un-judged). The message lists every criterion left out, by its `id` when the parent declares ids (P3) or by its 0-based ordinal position otherwise. This is deliberately strict, and it is affordable because it is: the enum carries `not_warranted` and `not_exercised` alongside `met`/`unmet`, so a criterion that genuinely does not apply still gets a real entry — **"every criterion needs a verdict" never forces you to assert something false.** `--verdict <index-or-criterion-id>:<verdict>:<cause_owner>` (repeatable) is how `a2a verify`/`a2a close` compose one; run without `--verdict` and the tool writes `verdicts: []`, which this rule now refuses the moment the parent declares even one criterion. Scope: this refuses at the write gate (`a2a validate`, `a2a submit`, `a2a validate --ci` on a pull request) and returns no verdict in the post-merge audit, the same ADR-011 D3 seat as REF-017/POL-022 above — a merged verify/close event is immutable and no verb rewrites its `verdicts[]`. |
| **`a2a submit` refused with `space: attachment does not resolve through the space's own resolution path`** | `a2a attach` writes its blob into the space as its own commit before it ever writes the local `attachments:` entry, so this should not fire for anything `a2a attach` itself minted. It still fires on a hand-written `ref` (never run through `a2a attach`), or a blob whose attach commit did not actually land. Run `a2a sync` to refresh the mirror, confirm the blob resolves with `a2a fetch <BL-id> --to <dir>`, and re-run `a2a attach` from the source file if it does not. |
| **An MCP write is refused with `space is required when multiple spaces are connected` or `space "X" is not connected`** | The session is connected to more than one space, and this call could not name which one it targets. **There is no default, deliberately** — picking the first connected space is how a write lands in the wrong repo. Each tool resolves its own target: `a2a_submit` reads the DRAFT's own `space:` field (so the fix is the draft, not the call — an unfilled `<...>` placeholder gets its own message saying so); `a2a_lifecycle` and `a2a_exchange` derive it from the artifact ids you passed, so `no connected space's mirror holds <id>` means run `a2a sync` or check the id; `a2a_contract`'s `deprecate`/`retire`/`adopt`/`activate` take an explicit `"space"` input, the same field its `publish`/`preflight`/`materialize`/`check` siblings already take — a contract's committed path carries no artifact id, so it cannot be derived. A batch whose ids span two spaces is refused naming both: one call targets one space. A session with ONE connected space never sees any of this. |
| **`a2a attach` sits for a long time and then fails offline, naming `FindPRByHeadBranch`** | `attach` is a network write, same as `a2a data deliver` — it needs `a2a init`, a connected space, and a resolvable credential. Offline or against an unreachable host, the write funnel calls GitHub before anything else runs, so you wait out the timeout and then see a raw transport error (`host: FindPRByHeadBranch: github api request failed: status 404`) rather than a clean "you're offline" message (epic-backlog B29). It is not a sign of a corrupt draft or a bad blob: restore network/host access and retry. |
