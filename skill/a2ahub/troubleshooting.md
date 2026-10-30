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
its connected spaces, printing one line per check. It exits `0` unless a check
FAILs: a check that could not tell prints `UNVERIFIED` (below), which is
advisory and never changes the exit code.

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
local land guard: PASS
local history shape: PASS
local space writable: PASS
space policy: PASS · getvisa: gates review, flow full
repository visibility [getvisa]: PASS: no observed mismatch
```

All twenty-five fixed checks, in the order the binary prints them, followed by
one `repository visibility` row per connected space (this project has one,
`getvisa`) — that row is not one of the fixed twenty-five and always prints
LAST, after every one of them. **A `PASS` carrying a note is a pass** — do
not report it as a problem.

Eighteen rows can print `PASS · <note>`:

> The count said **twelve** until 2026-08-28 and the table under it listed fifteen. It was then sixteen, `default branch healthy` having joined. It was twenty once `CI presence` could print a note because a local space is n/a there and the three local-space rows joined. It is now eighteen: those three rows no longer print a note, because a local space they cannot judge is `UNVERIFIED`, not a `PASS` with a caveat (see below), and `space policy` joins. Counted, not eyeballed.

| Row | When it carries a note |
|---|---|
| `credentials` | the token resolves but advertises no `workflow` scope, or a space is local and needs no credential (`<id>: local space, no credential needed`) |
| `CI presence` | a space is local, so its writes are validated at land and it carries no CI workflow (`<id>: validated at land, no CI workflow`) |
| `default branch healthy` | always, whenever any space is connected — it appends each connected space's own derived base branch, or names that it could not be determined |
| `versions` | a newer release is available (never a failure — the floor is a separate check) |
| `participant avatars` | one or more active participant owners have no validated image in the disposable local cache |
| `space scaffolding current` | the space is behind this binary's template, or the answer could not be determined. An **in-sync** space gets a bare `PASS` with no note |
| `auto-merge enabled` | the setting could not be read (no host reader wired, or the API refused) |
| `stuck green PRs` | open-PR state could not be read, so the result is unknown rather than healthy |
| `codeowners resolvable` | GitHub's CODEOWNERS diagnostics could not be read, so the result is unknown rather than healthy |
| `skill discoverable` | no skill installed, or installed but no agent surface links it |
| `skill manual current` | always — the installed manual is older or newer than the binary, is a `dev` build, or — once the stamp names this binary — its on-disk tree was walked against the embed and matches (this row never returns a bare `PASS` with no note) |
| `threads intact` | the space holds artifacts written before threads existed, so they carry no `thread:` |
| `skipped mirror files` | a file in the mirror could not be decoded, so it is missing from every read verb's output |
| `notification components` | notifications are not enabled, or this build has no notification probe wired |
| `notify-routes` | a route in `space.yaml` is malformed, or names a participant the manifest does not list |
| `notify-secret` | a route names a secret the space repo does not hold — presence only; doctor never reads a secret's value |
| `notify-delivery` | the last `a2a-notify` run on `main` failed, or there has never been one |
| `space policy` | always, whenever any space is connected — it names each space's gates mode and flow (`<id>: gates <mode>, flow <flow>`) |

The three `notify-*` rows are the SPACE plane — the chat notifications a space's
own CI sends. `notification components` just above them is the LOCAL plane
(macOS, VS Code). Two planes, adjacent names; see
[notifications.md](notifications.md) for which is which.

Five of those rows — `participant avatars`, `space scaffolding current`,
`skill discoverable`, `threads intact` and `skipped mirror files` — can
**never** FAIL. The other thirteen can.

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

**A third outcome: `<name>: UNVERIFIED: <detail>`.** The check could not tell,
so it says so instead of passing. It is the same status the `repository
visibility` rows have always used, it is **advisory** (it does not change the
exit code), and the detail names the space and why it could not be judged. Four
fixed rows use it: `local land guard`, `local history shape` and `local space
writable` (a local space could not be inspected, or `git --version` could not be
read), and `space policy` (a space whose host could not be opened, or whose
`space.yaml` could not be read). **Read UNVERIFIED as "unknown", not as "good"
and not as "broken"**: the question was not answered. Where a `FAIL` and an
`UNVERIFIED` could both be reported, the `FAIL` wins.

**Exit codes.**

| Code | Meaning |
|------|---------|
| `0` | Every check passed or was `UNVERIFIED`; none FAILed. |
| `1` | One or more checks failed, OR the local project/machine config could not be loaded (in which case doctor prints a `doctor: cannot load … config` line to stderr before exiting). |
| `2` | Usage error — including **`doctor --space`**, which is the v2 admin host-drift diff and is explicitly rejected in v1-min (doctor prints `doctor: --space: v1-min: not available`). Doctor's flag only — `a2a validate --ci --space <id>` is a real flag on another verb. |

## The twenty-five checks

`doctor` prints a fixed set of exactly twenty-five checks — that fixed count is
what this heading names, and it is why the count and the heading are kept in
lockstep by the test guarding this file. That lockstep was NOT holding: this
heading still said "seventeen" on 2026-08-18 while the sentence forty lines up
said "twenty". The tripwire did check the heading — against the hardcoded
string `"## The seventeen checks"`, frozen at the roster size on the day it was
written. An expectation spelled as a literal cannot notice that the thing it
describes has moved; it can only notice somebody editing the doc without
editing the test, which is the opposite of its job. The number is now DERIVED
from the binary's own roster, and the summary sentence — which had no assertion
at all — is checked too. **Each of the twenty-five
runs once per PROJECT, not once per connected space**: when a check must reason about
several connected spaces, it folds any per-space detail into a single line
(`space access` and `credentials`, for example, join per-space failures with
`; `). They print in the order below, top to bottom, matching `doctor`'s own
output.

The table carries a twenty-sixth row, `repository visibility`, because it is
real output you will see — but it is NOT one of the fixed twenty-five and shares
neither their order nor their cardinality. It prints ONE row PER CONNECTED
SPACE, after every fixed check, so it is always LAST in the output and absent
entirely from a project with no connected spaces. Its printed name carries
the space id (`repository visibility [getvisa]`), which no other row does.

**So the table below is in output order, all twenty-six rows** — read it top to
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
| **versions** | This build is not older than each space's `min_binary_version` pin in `space.yaml`. Three kinds of build read differently. A stamp of exactly `dev` (a plain `go build`) reports ` · unreleased local build (dev); released-version floor comparison skipped` and skips the comparison; any other malformed version still fails. A **local build** (put here by `make deliver`; `a2a version` says `local build of <version> (<sha>)`) is compared as its stamped release version and the detail ends ` · local build of <version> (<sha>); floors compared as <version>`; it is never advised to `a2a update` and gets no "update available" or "no release check has ever run" line, only the space-template-pin advisory. A released build gets the cached-release advisories as before. | Your `a2a` binary is older than the space requires (or the space's `space.yaml` could not be read/parsed). A released binary: upgrade it; the write funnel will otherwise refuse your writes. A local build: the failure line carries the suffix ` (run make deliver from the a2ahub checkout once main carries the release that raised it)` — `a2a update` leaves a local build in place, so the remedy is a delivery from the a2ahub checkout, not an update. |
| **CI presence** | The space's mirror carries `.github/workflows/a2a-validate.yml`. | The validation workflow file is missing from the space's mirror. |
| **space scaffolding current** | Whether the SPACE is behind what THIS binary's embedded template would write — the pinned reusable-workflow ref, `min_binary_version`, and the template-managed files. This is the REVERSE of `versions`, which only asks whether your binary is too old for the space. | **Advisory only — this row never FAILs**, because a behind space still accepts writes; what it costs is a weaker gate. A note means the space's CI may be running an older validator than you think, so a rule your binary enforces locally might not be enforced at merge. The note names who can act: if your credential has push/admin on the space repo it tells you to run `a2a space update` (which opens a PR — a2a changes no repo setting); if it does not, it gives you one sentence to hand the space admin. A note saying it could not be checked means no mirror, an unreadable manifest, or a dev build with no release version to compare against. |
| **auto-merge enabled** | The space repo's GitHub `allow_auto_merge` setting is ON. It is OFF by default on a freshly created repository. | Every write stalls: `a2a submit` opens a PR and arms auto-merge, so with the setting off the PR sits there and the counterparty never sees the artifact. Enable Settings → General → "Allow auto-merge". **A PASS carrying `· auto-merge unverified` is not a failure** — it means your credential cannot read repo settings (a fine-grained token needs `Repository metadata: read`), so the answer is unknown rather than bad. |
| **stuck green PRs** | Every historical open PR whose required check is explicitly green has auto-merge armed. | A named PR is green and unmerged but no automatic merge is armed. Doctor prints the exact safe `gh pr merge <n> --auto --repo <owner>/<repo>` remedy and never merges it itself. A PASS carrying `· stuck-green state unverified` means host state or credentials were unavailable, not that no stuck PR exists. |
| **default branch healthy** | The space's own DERIVED default branch — read from that remote's `refs/remotes/origin/HEAD`, never assumed to be `main` — is not failing its own post-merge audit: the conclusion of the required check run on that branch, read through the same selection the PR gate uses. | The branch every participant pulls from and merges onto is red, and the detail names the space, the check run that answered, and its conclusion. Fix the branch before merging onto it: every subsequent merge stacks onto an already-failing default branch. **It also always carries `· base branch: <space-id>: <branch>`** per connected space — or `base branch could not be determined (checked "main")`, a report and never an assertion — so "which branch will my submit push at" is answered BEFORE your first write. **A PASS carrying `· unverified` or `· still running` is not a failure** — the credential could not read the host, no required check run matched, or a run is still in flight, so the answer is unknown rather than bad. This row exists because it did not: on 2026-08-12 `doctor` answered PASS on every row while a space's default branch had been red for four hours (`fb-20260812-ee6dcd`). |
| **codeowners resolvable** | Every owner named in the space's `CODEOWNERS` actually resolves — read from GitHub's own `repos/{owner}/{repo}/codeowners/errors`, with the line number. | GitHub **ignores** an owner it cannot resolve rather than rejecting it, so a `CODEOWNERS` naming a team nobody created looks like it gates `/space.yaml` and gates nothing — and code-owner review is the only thing standing behind the file that decides who may write where. A FAIL quotes GitHub's own suggestion, which names all three conditions an owner must meet: the team exists, is publicly visible, and has write access to the repo. Individual logins avoid all three. **A PASS carrying `· CODEOWNERS unverified` is not a failure** — your credential cannot read that endpoint (a fine-grained token needs `Repository metadata: read`), so the answer is unknown rather than bad. |
| **threads intact** | Every artifact in the space carries a `thread:`, so `a2a thread` can place it in a conversation. | Advisory only — this row never FAILs. A note counts the artifacts written before threads existed and names their spaces. Those predate thread propagation: `a2a respond` refuses to reply to one, and they cannot appear in any thread view. Nothing repairs them in place; a new exchange that references them starts a fresh thread. |
| **skipped mirror files** | Every `.md` artifact and every event file in each mirror actually decodes, so the read model holds the whole space. | Advisory only — this row never FAILs. A note names each undecodable file and why (`unreadable`, `not-frontmatter-shaped`, `undecodable-yaml`, `no-id`, `unrelativizable-path`). **This is the row to read when a document you know exists does not show up.** A skipped file is missing from `search`, `inbox`, `outbox`, `thread` and `statusline` — `show` still prints it, because that path parses the file itself. Fixable only by whoever owns that file's section, which under diff-authz may not be you. |
| **notification components** | Every channel enabled for this project has an installed component with a compatible CLI handshake; macOS permission/login-item state is healthy when available. | The companion is absent, version-skewed, permission-denied, awaiting the user's Gatekeeper decision, or its login item is missing. Run `a2a notifications status --probe --json`, then repeat `a2a notifications install --channel <channel>` to repair it. For macOS `approval-required`, the human opens A2A Notifier once and explicitly chooses **System Settings → Privacy & Security → Open Anyway**; never clear quarantine or disable Gatekeeper for them. |
| **statusline wiring** | The `git` binary is on `PATH` (the prerequisite for §7.5's hub-less statusline-refresh fallback). | `git` is not on `PATH`, so the statusline's git-fetch fallback refresh cannot run. |
| **skill discoverable** | The `a2ahub` skill tree is installed and reachable by your agent harness. | Advisory only — this row never FAILs. It reports whether a skill is installed at all. |
| **skill manual current** | Whether the installed skill's on-disk tree matches THIS BINARY's own embedded copy — but only once the version stamp names this binary's own version; a stamp naming a different release compares against a tree this binary does not carry, so that case is answered from the stamp alone, never from a tree walk. | A stamp that is honestly older, newer, or from a `dev` build is not a FAIL — that PASSes with its own note (`a2a skill install` fixes an older one; a `dev` note only ever fires when its own tree walk finds drift). **Once the stamp names this binary, a FAIL means the on-disk tree disagrees with the embed** — a file is missing, a file's bytes differ, or a file exists on disk that the binary does not ship — and names the differing paths (up to five, then a count). The remedy is `a2a skill install`, and it says plainly that the reinstall **overwrites local edits** — a hand-edited file under an otherwise a2ahub-owned install is exactly the case this FAILs for. A directory with no a2ahub provenance marker is someone else's and is never judged at all. |
| **automation coverage** | Nothing — and it says so. It names the two automations `onboarding.md` §8.6 recommends (a harness session-start hook, and a scheduled `a2a-poll.yml` in the PARTICIPANT's own repository) and states that doctor cannot observe either: the first is your harness's own config, which D-021 forbids a2a to read or write, and the second lives in a repository doctor is never pointed at. | It cannot FAIL. Always PASS-with-advisory, because a FAIL would claim doctor saw a broken state it structurally cannot see, and a bare PASS would be silent about something every new participant is told to set up. Check both by hand. |
| **notify-routes** | Every `notification_routes[]` entry in the space's `space.yaml` is well-formed and names a participant the manifest actually lists. | A route is malformed, or its `for:` names a system absent from `participants[]`. The space's own PR gate refuses both (POL-021 / REF-022), so this row failing means a route reached `main` before the rule existed, or the manifest changed under it. Fix the route in `space.yaml` through the normal write funnel. |
| **notify-secret** | Every secret a route names exists on the space repo. **Presence only — doctor never reads a secret's value.** | A route names a secret the repo does not hold, so its messages cannot be sent. Run `a2a notify setup`, or set it directly with `gh secret set <NAME> --repo <space-repo>`. A route with no secret configured is not an error until it has one to lose. |
| **notify-delivery** | The most recent `a2a-notify` run on `main` concluded green. | The last run failed, or there has never been one. A failed run is the honest state a silent channel would hide — read it with `gh run list --workflow a2a-notify.yml --repo <space-repo>`. "Never run" is normal for a space that has just configured its first route and not yet pushed. |
| **local land guard** | For every connected LOCAL space: its `pre-receive` hook is installed, executable and byte-for-byte what this binary installs, **and** the installed git is at least 2.38 (read from `git --version`). A project with no local space reads a plain `PASS`. A local space that cannot be inspected, or a `git --version` that cannot be read, reads `UNVERIFIED`. | The hook is what stops a raw `git push` from moving the base branch around the validated land, so a missing, non-executable, edited or symlinked hook leaves the space unguarded. Re-run `a2a space init <id> --local --dir <path>` (the line names both); it is idempotent and installs the hook again without touching history. A git older than 2.38 makes an installed hook useless: upgrade git. **`UNVERIFIED`** (never a `PASS` with a note) means the space or the git version could not be read — the answer is unknown, not good; the detail names which. A git below 2.38 is a `FAIL` even when the space itself could not be inspected. |
| **local history shape** | For every connected local space: after the root commit, every commit on the base branch's first-parent chain is a two-parent land. | A one-parent commit on the base branch means something moved it around the funnel (a raw push, or a hand-run `git update-ref`). The detail names the newest such commits. A space that cannot be inspected reads `UNVERIFIED`. **The row is shape-only**: a two-parent merge forged by hand is not caught, because nothing cross-checks a merge against its pull-request record. |
| **local space writable** | For every connected local space: this user can create files under the bare repository's `refs/` and `objects/`. | A land writes there, so a repository this user cannot write fails every write to it midway. The detail names what blocked the probe; fix the ownership or permissions of the directory. A space that cannot be inspected reads `UNVERIFIED`. |
| **space policy** | For every connected space: the **gates** mode that is in force for ITS host (`review` on a host that reviews writes, `confirm` or `autonomous` on a local space) and the **flow** (`full` or `express`) its `space.yaml` declares, read through the same reader the write paths use. A project with no connected space reads a plain `PASS`. | A space on a host that reviews writes (GitHub) whose `space.yaml` says `gates: autonomous`. The host's pull-request review is the gate there, so the reader **ignores** the word (the effective mode stays `review`) and a new write that sets it is refused with POL-028 (below); this `FAIL` is how a manifest merged before that rule existed gets seen. Change `gates` to `default` or `confirm` through a reviewed pull request. **`UNVERIFIED`** means the space could not be judged: its host could not be opened (a malformed or unsupported `repo_url`; the detail carries the open error) or its `space.yaml` could not be read or parsed from the mirror. A space with no host is never `PASS`, whatever its manifest says, and `autonomous` is never honoured for it. |
| **repository visibility** | Whether the space repo's GitHub visibility matches what the material inside it is classified as. Visibility is read from the host; classification comes from the artifacts themselves. | **This row never FAILs — it PASSes or reports UNVERIFIED**, because visibility is evidence, never write authority. A **WARN** on a PUBLIC repo means it carries higher-classified material than `public`: that is the one reading to act on, and the fix is the repository's visibility or the material's classification, not this row. On a **private or internal** repo carrying nothing `restricted`, it PASSes as "no observed mismatch". On a private repo that DOES carry `restricted` material it PASSes only when a bilateral proof source is named; otherwise it reports UNVERIFIED with "repository privacy alone does not prove intended pairwise audience" — which is **not a problem to fix**. It says the honest thing: a private repo proves the audience is small, never that it is the specific pair the material was restricted to. A space on this machine (a local space) is never shared, so there is nothing to compare: it reads `PASS: n/a: on this machine, never shared`. An UNVERIFIED reading "visibility could not be verified" means the credential could not read repo metadata (a fine-grained token needs `Repository metadata: read`), so the answer is unknown rather than bad. |
| **consumed contract** | Whether this system verify-passed any data-package delivery — a handoff deliverable of kind `data`, accepted (verify-passed) — pinning a contract absent from its own `consumes.yaml` (P6, defects-fix-2026-08, `docs/inbox/defects/07-the-consumer-registered-nowhere.md`: a real space had verify-passed four such handoffs while its own `dependencies:` sat `[]`). | **This row never FAILs, and prints nothing at all when there is nothing to report** — detection only, never a gate: `a2a doctor`'s exit code and `contract retire`'s own precondition (D-022) are both unaffected by it. A **WARN** names the contract, the number of distinct verify-passed packages conforming to it, and the exact fix — `a2a contract adopt <XC-id>` — because consuming without adopting is a real risk (the retire gate and the deprecation notice cannot protect a dependency nobody registered) but not necessarily a mistake: the operator may have reasons. An **UNVERIFIED** means this system's own `consumes.yaml` exists but could not be read as a real `consumes/v1` registry, so the advisory is skipped rather than risking a false "unadopted" claim against a registration it simply could not parse. A contract this system publishes itself is never named here — a producer is never counted as its own unadopted consumer. |
| **observed consumer** | The PRODUCER direction of the row above, new in v0.25.7: a system whose committed artifacts show it consuming a contract THIS system provides without declaring it — **and the version it pinned**. Retire is per-version, so "something consumes this" was not actionable alone. | **Never FAILs; prints nothing when there is nothing to report.** A **WARN** names the system, the contract, the version and how many verify-passed packages back it. It gates nothing — it exists so a retire decision is not made blind. Deliberately an UNDERCOUNT: a participant whose registry or descriptor cannot be parsed is EXCLUDED rather than reported against. |
| **notify selectivity** | Whether a configured notification route matched nothing while qualifying traffic existed. New in v0.25.7 alongside the route selector dimensions — see [reference/notify.md](reference/notify.md). | **Never FAILs; prints nothing when there is nothing to report.** A **WARN** reads `route <label> matched NOTHING while N artifact(s) qualified`, the shape that silenced a real space: a route can be valid and still be unsatisfiable. A genuinely quiet space produces no row — nothing qualified, so nothing was missed. |

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

- A commit in a space's history may end in an **`A2A-Built-By: a2a <version>
  (<sha>)`** paragraph (`(local, <sha>)` when the writer was a local build, i.e.
  unpublished code put there by `make deliver`). It is the build note every
  write adds to its own commit message, above the funnel's trailer, and it
  lives only there: no event, artifact or fold field carries it, and nothing
  reads it back as data. Use it to tell which build made a write when a space's
  CI refused or accepted a shape you did not expect; a write by a build with no
  known commit carries no note.

If a user needs the stronger guarantees (expiry warnings, host-drift diff),
that is a v2/`--space` concern — say so rather than implying doctor already
covers it.

## Common resolutions

The symptom-to-first-move table lives on its own page,
[resolutions.md](resolutions.md): look up the line or refusal you are holding
there.
