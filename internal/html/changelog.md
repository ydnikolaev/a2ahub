# Changelog

Published releases, newest first.

## v0.16.3 — 2026-07-30

Statusline refresh now survives the one-shot command, while the merge gate becomes deterministic and cache-aware

### A stale statusline launches one durable refresh for the next render

**fix · normal**

The prompt-facing statusline still reads local cache only and keeps its existing text, JSON, and exit-code contract. When that cache is stale, it now atomically claims a short-lived local lease and starts the canonical `a2a sync` command as a detached process. The refresh can finish after the one-shot statusline process exits, concurrent prompt renders do not start a herd of duplicate syncs even while recovering an expired lease, and the next render sees the refreshed mirror. The previous in-process goroutine could be terminated by normal CLI exit or race a test fixture's cleanup.

Action scope: local. Update if statusline is embedded in a shell or agent prompt; no config change is required.


### The ordinary CI gate no longer depends on ambient Git identity or a cold duplicate Go cache

**fix · low**

Git-backed tests now receive a process-local fixture identity, the checkout-owned validation runner has a documented scoped-test entrypoint, GitHub's setup-go cache feeds the exact build cache consumed by `make check`, and workflow syntax validation fails closed with pinned actionlint. The public-only classification backstop no longer rejects the private source repository, and CodeQL has the read scope its analyze step needs under restrictive workflow permissions. CodeQL execution is scoped to the public repository where code scanning is enabled; private CI keeps its gosec, govulncheck, and gitleaks gates without spending minutes on an upload GitHub will refuse. Lifecycle wire parity retains every case while reusing one immutable repository fixture.

### Standing known issues no longer disappear on the next update

**fix · normal**

Current limitations now live once in a schema-validated standing list instead of being copied into every version file. `a2a whatsnew`, its MCP twin, the HTML release view, and the authored GitHub Release body append that list independently of the selected version range, including when there are no newer change entries. The existing release-array machine shape remains unchanged, and a resolved issue is removed in one place.

### The v0.16.3 runtime candidate passed all 50 declared live cells

**policy · low**

The immutable public runtime candidate d6418b926ec5363416ef8b42a572788e7e7e009a completed the full protected GitHub matrix with two independent identities: 50 pass, zero fail, zero timed-out, and zero not-run cells across CLI, MCP, lifecycle, contracts, authorization boundaries, failure recovery, thread reconstruction, and space migration. The audit observed 179 workflow runs; its two red runs were the expected and explicitly claimed cross-section boundary probes. The final tagged tree differs from that runtime candidate only by this evidence statement and the matching README baseline; local gates, release preflight, renderer checks, and the filtered-public leak proof are repeated on the exact tag candidate.

## v0.16.2 — 2026-07-30

The authored GitHub Release body now completes the coordinated release instead of stopping it on provider newline normalization

### Release-body verification accepts only GitHub's terminal newline normalization

**fix · normal**

v0.16.1 successfully published the 38 Go release assets and the complete authored release body, then its new post-publish assertion rejected one extra terminal newline added by GoReleaser. The intentional fail-closed dependency stopped the macOS, VS Code, and coordinated-cohort jobs before they could upload their eight remaining assets. The verifier now compares every content byte while normalizing only trailing CR/LF bytes, reads the published body through a one-megabyte bounded stdin, and still rejects any changed heading, sentence, command, or link. v0.16.2 reruns the complete release cohort from the same product behavior.

Action scope: local. Use v0.16.2 rather than the incomplete v0.16.1 cohort, especially when installing the macOS or VS Code notification companion.


### The macOS companion remains ad-hoc signed and may require manual approval

**known-issue · normal**

The release cohort authenticates the exact macOS app and ad-hoc signing protects its bundle integrity, but the app is not identified or notarized through a paid Apple Developer account. A quarantined download may require one explicit System Settings → Privacy & Security → Open Anyway decision. Interactive Notification Center, Focus, Login Items, VS Code trust/profile, and remote-window checks remain release-environment evidence rather than claims inferred from hermetic tests.

## v0.16.1 — 2026-07-30

Contract history documentation now matches the shipped Git-backed behavior

### Contract digests and version history no longer require unstable paths or a future commit SHA

**fix · normal**

The normative contract now states the behavior the product already implements: a contract digest is computed from contract-root-relative paths plus file-byte hashes, so identical contract trees have the same identity in different checkouts. An `id@version` lookup walks committed descriptor history and derives the introducing commit from Git rather than expecting a publish event to predict the SHA of its own future commit. The optional event `commit` field remains readable for legacy and reserved data, but current writers do not populate it and current version resolution does not depend on it. Existing spaces and artifacts require no migration.

### The project front door now explains the work a2ahub makes possible

**fix · normal**

The README no longer opens with implementation vocabulary, copies a command catalogue, or carries two MCP limitations that the product already fixed. It now starts from the coordination failure a2ahub solves, then names the concrete capabilities: readable work chains, computed next moves, versioned consumer-aware contracts, the validated Git write funnel, the local dashboard, notifications, CLI, and MCP. A deterministic release gate keeps the file below its compact size ceiling, requires exits to the canonical documentation, and rejects the known retired guidance; release review still owns the human judgement about clarity and tone.

### GitHub Releases now show the authored release notes instead of commit noise

**fix · normal**

The release workflow now renders the same versioned YAML corpus used by `a2a whatsnew` into a concise GitHub Release page with changes, known issues, actions, install guidance, and verification links. GoReleaser receives that file through its canonical custom-release-notes option and replaces stale bodies on retry. A post-publish assertion byte-compares the visible GitHub body with the rendered SSOT. This removes the generic platform table, raw commit list, and unsafe quarantine-removal advice that previously obscured why a release mattered.

### Release verification is selected by behavioral impact, not by the size of the version number

**policy · normal**

Full GitHub live verification remains mandatory when an exact public candidate changes runtime, funnel, validator, lifecycle, contract constraints, or space behavior. A targeted cell remains a diagnostic rerun, not proof for a shared-core change. Presentation-only candidates may now omit GitHub live when the exact public diff proves that no executable or protocol behavior moved; they still require the full hermetic gate, release preflight, immutable candidate, and a written reason. v0.16.1 uses that presentation-only tier. The latest full baseline, v0.16.0, passed 50 of 50 declared live cells; that is complete coverage of the declared matrix, not every theoretically possible state.

### The macOS companion remains ad-hoc signed and may require manual approval

**known-issue · normal**

The release cohort authenticates the exact macOS app and ad-hoc signing protects its bundle integrity, but the app is not identified or notarized through a paid Apple Developer account. A quarantined download may require one explicit System Settings → Privacy & Security → Open Anyway decision. Interactive Notification Center, Focus, Login Items, VS Code trust/profile, and remote-window checks remain release-environment evidence rather than claims inferred from hermetic tests.

## v0.16.0 — 2026-07-29

Autonomous exchanges now rest on fail-closed authority, durable retries, live MCP proof, and an exact release-candidate gate

### Ambiguous space authority and repository symlink writes fail closed

**fix · high**

One manifest-policy owner now rejects duplicate participant systems, duplicate active logins, missing active owners, overlapping sections, and departed participants attempting to authorize new writes. The same policy feeds manifest loading and the merge validator, so participant order can no longer choose an authorization result. Before any funnel mutation, every existing repository path component and final target is also checked with symlink-aware semantics. A committed link can no longer redirect an ordinary or infrastructure write outside the checkout. Skill discovery applies the same ownership principle: only a link resolving exactly to the configured install is managed without explicit `--force`.

Action scope: space. Existing valid manifests need no migration. If validation now reports REF-013, make participant systems, active GitHub owners, and top-level owned sections unambiguous before writing.


### A semantic retry remains one operation across midnight and partial GitHub failures

**fix · high**

`respond` and `contract deprecate` now derive a private, versioned operation key from canonical command intent instead of relying on the public date-bearing artifact id for retry identity. The same intent resolves the original artifact and PR across UTC midnight, an orphaned remote branch, an open PR, or stale local pending state after merge. Changed body, actor, parents, result, successor, sunset, fields, or contract version produce a different operation; an unproven orphan is refused rather than overwritten. CLI and MCP call the same implementation. Doctor now also names historical PRs that are explicitly green, unmerged, and not armed for auto-merge, including the safe exact retry command. Contract retirement refusals list every consumer still missing its acknowledgement.

Action scope: local. Retry the same semantic command after upgrading; it will recover the existing remote operation instead of minting a second one. Run doctor to expose older green PRs that predate this recovery path.


### Custom skill installs remain linked, refreshed, and visible to doctor

**feat · normal**

`a2a skill install --dir` now accepts only a normalized path beneath the project root and persists it as `skill_dir` in `.a2a/config.yaml`. Install, link, binary update, and both doctor skill checks resolve that one location. Existing configs keep `.a2ahub/skill`; repeating `a2a init` preserves a custom value and never moves or deletes the old tree implicitly. Doctor also treats the exact local version stamp `dev` as an unreleased-build advisory while continuing to fail every other malformed released version.

Action scope: local. No action is required for the default location. A custom location is now project-relative and team-visible; reinstall once with `--dir` to record an older untracked custom install.


### A release verdict exercises the exact public commit that is tagged

**fix · high**

Publishing first creates a temporary public candidate ref without moving public `main` or creating a tag. The full live space pins both reusable workflow source and its `a2a-ref` input to that immutable commit SHA. Promotion then accepts only the proven SHA, restores branch protection, tags that same commit, verifies the remote tag, and deletes the candidate. A red run therefore leaves the current public release untouched. The validator emits structured authorization annotations, so the cross-section row requires the intended code and path rather than any red check. The live matrix also drives the nine missing CLI/MCP parity cells through the shipped JSON-RPC stdio server, bringing the declared release matrix from 40 to 49 cells.

### The decomposition guide now cites one real three-document thread

**fix · low**

The shipped a2ahub skill previously illustrated announcement, question, and work request decomposition with three unrelated fixture documents and an explicit deviation. Its cited trio is now schema-valid on disk, carries three distinct single intents, and shares one thread. Agents can copy the demonstrated coordination shape without inventing links between independent examples.

### The shipped skill no longer warns agents away from fresh MCP reads

**fix · high**

MCP has refreshed the configured space before every tool call since the earlier read-freshness fix, but the installed skill and troubleshooting guide still described the old startup snapshot and told agents to switch to CLI. That stale instruction directly serialized autonomous loops that can safely use typed reads. The guide now matches the server: a successful pre-call refresh gives a current view; a failed refresh logs an explicit last-good-view warning that must not be treated as proof of absence. Troubleshooting also lists all fifteen current doctor checks, including the new historical stuck-green PR diagnostic and exact local `dev` version behavior.

Action scope: local. Refresh the installed skill. MCP agents may keep using typed reads for decision-bearing loops; react only when the server reports a failed pre-call refresh.


### The macOS companion remains ad-hoc signed and may require manual approval

**known-issue · normal**

The release cohort authenticates the exact macOS app and ad-hoc signing protects its bundle integrity, but the app is not identified or notarized through a paid Apple Developer account. A quarantined download may require one explicit System Settings → Privacy & Security → Open Anyway decision. Interactive Notification Center, Focus, Login Items, VS Code trust/profile, and remote-window checks remain release-environment evidence rather than claims inferred from hermetic tests.

### MCP refuses anonymous writes before mutation and live proof gates every lifecycle PR

**fix · high**

MCP write tools now resolve durable actor identity from explicit tool input, A2A_ACTOR_* environment values, then the local OS user. If no honest name exists, the tool returns actionable remedies before writing a draft, branch, or pull request. The schema engine classifies minLength failures as stable SCH-009 content violations instead of reporting internal registry drift. The live CLI and MCP lifecycle and contract-lifecycle rows also wait for the required check and merge of every created PR, including the final withdraw or retire operation; a parsed PR number can no longer hide a red candidate or leave main moving underneath the next scenario.

### MCP contract submit carries its schema and executable fixtures

**fix · high**

`a2a_new` already scaffolded a starter schema plus valid and invalid fixtures for a JSON-Schema contract, but MCP `a2a_submit` omitted those sidecars from the first-publish commit. Real space CI therefore rejected an otherwise valid MCP-authored contract with POL-009. MCP submit now uses the same bounded, symlink-refusing sidecar collector as CLI submit and carries the descriptor, schema, both fixture classes, and publish event atomically. The CLI≡MCP equivalence suite now covers this complete contract-submit path.

### Live verification tolerates GitHub list propagation without weakening its verdict

**fix · normal**

A newly-created pull request can be returned by GitHub's create endpoint before the same PR appears in the list endpoint. The live harness now waits within a small bounded window for that exact visibility gap instead of falsely failing a healthy write and forcing another full matrix. A dependent transition starts only after the merged commit is present on origin/main and the mirror working tree is parked on that base tip. Host idempotency lookup also recognises merged list entries through merged_at, matching GitHub's list response while retaining the older merged-boolean response shape.

### Destructive live-space resets no longer collide with retained semantic-operation history

**fix · normal**

GitHub correctly retains merged pull requests even when the dedicated throwaway live space force-resets main. Contract scenarios previously reused fixed standing ids, so an identical deprecation in a new reset generation could be mistaken for the prior merged semantic operation. Every live contract scenario that submits such an operation now scopes its standing slug to the post-reset PR watermark. Product idempotency remains unchanged; only disposable release-test data becomes generation-specific.

### Live required-check polling can no longer hang on one GitHub request

**fix · normal**

The live matrix already bounded how long it would poll for a required check, but its product CheckStatus path used Go's default HTTP client, whose individual requests have no timeout. A stalled GitHub round trip could therefore consume the entire matrix budget before the poll deadline was evaluated. Every live-harness GitHub client now shares a 30-second transport timeout beneath its existing fail-closed poll ceilings.

### Live merge verification tolerates delayed smart-Git visibility

**fix · normal**

GitHub can report a pull request merged through REST several minutes before the same base commit becomes visible through the authenticated smart-Git endpoint used by a2a sync. The live harness previously gave this split three minutes, producing a false failure twice at measured delays of nearly five minutes. It now polls for up to seven minutes and still passes only after the exact merge commit is present on origin/main and checked out in the mirror tree. The retry arithmetic includes both endpoints of that declared window, and a permanent CLI testscript now advances a real local origin between sync invocations to prove the remote-tracking ref and reader-visible tree both move.

### Maintainers can rerun one exact happy-path live matrix cell

**feat · normal**

A2A_LIVE_E2E_CELLS accepts an exact scenario/system/surface triple for diagnostic reruns inside the happy family. Unknown or unsupported selections refuse before the first write, while every unselected cell remains not-run, so a three-PR repair probe cannot be mistaken for the full release verdict.

### Live verification follows semantic-operation branches without scraping artifact IDs

**fix · normal**

D6 moved `respond` and `contract deprecate` from composite artifact-ID branches to opaque keys derived from canonical command intent. The live harness still searched those branches for generated artifact IDs, so a correct checked and merged deprecation PR could be reported missing and later response scenarios would fail the same way. Every affected scenario now derives the exact operation branch through the product's core helper and recovers generated response or announcement IDs only from the funnel-owned PR metadata used by retry recovery. A structural regression rejects any future composite lookup for these verbs.

### MCP contract submit validates the descriptor without misreading its baseline

**fix · high**

MCP first-publish now carries a contract descriptor, schema, valid fixtures, invalid fixtures, and publish event through one atomic write without treating the JSON sidecars as envelope frontmatter. The CLI, MCP, and merge validator consume one layout-owned reverse classifier for the contract descriptor and baseline path family, preventing the two submit adapters from drifting on which files are artifacts. Schema and fixture policy remains owned by the existing contract publishability and compatibility checks; this correction only routes each file to its canonical validator.

### Live contract-integrity verification follows run-scoped contract paths

**fix · normal**

Contract-integrity scenarios now derive staged schemas, fixtures, and landed-schema reads from the actual contract ID returned by authoring. Previously they created a run-scoped contract but later edited the unscoped base slug, so two healthy contract workflows were reported as missing local schema files. A path regression and structural wiring check keep both scenario families on the same destructive-space generation.

## v0.15.2 — 2026-07-29

The macOS notification companion now builds on a Swift 6 GitHub runner

### The hosted macOS job uses a toolchain that can build the notifier

**fix · high**

v0.15.1 introduced the accepted ad-hoc distribution path, but its GitHub job still ran on `macos-14`. That image selected Xcode 15.4 and Swift 5.10, so SwiftPM rejected the notifier's declared tools version 6.0 before compiling anything. The release now uses GitHub's `macos-15` ARM64 image, prints the selected Swift version in the log, and exercises the same Swift tests plus ad-hoc universal packager in ordinary CI before a tag is cut. The signing, Gatekeeper, cohort, and retry semantics from v0.15.1 are unchanged.

Action scope: local. Update to the completed cohort, then install the macOS channel. If Gatekeeper requests it, approve A2A Notifier under Privacy & Security and repeat the install command.


## v0.15.1 — 2026-07-29

The macOS notification companion now ships without requiring a paid Apple Developer account

### The macOS notification companion is present in the coordinated release

**fix · high**

v0.15.0 published the CLI and VS Code extension, but its macOS release job required Apple Developer ID and notarization credentials that the project does not have. The job failed closed, which also prevented the coordinated cohort and standalone release notes from being published. The release path now selects an explicit ad-hoc signing mode. It builds the same universal Apple-Silicon/Intel app, enables hardened-runtime options, verifies the ad-hoc code signature and literal `TeamIdentifier=not set`, and binds the exact app hash, bundle ID, version, and protocol into the Sigstore-signed release cohort. It does not claim Apple developer identity or notarization.

Action scope: local. Install the macOS channel normally. If Gatekeeper blocks its first launch, explicitly allow A2A Notifier under System Settings → Privacy & Security → Open Anyway, then repeat the install command. a2a never clears quarantine or changes Gatekeeper settings.


### The macOS companion is not identified or notarized by Apple

**known-issue · normal**

Ad-hoc code signing protects the app bundle's internal integrity and the signed release cohort authenticates the exact downloaded asset, but Gatekeeper cannot identify it as software from an enrolled Apple developer. A browser-quarantined copy may therefore require one manual Open Anyway decision. A future paid Developer ID can remove that friction through the already-separated developer-id packaging mode; until then the product reports the limitation instead of bypassing macOS security.

## v0.15.0 — 2026-07-29

Agent loops survive asynchronous merges and safe public intake, contract publishing is stricter, and notifications reach macOS and VS Code

### Feedback intake works on a fresh hub and a failed first attempt is safe to retry

**fix · high**

The intake workflow used labels that a fresh repository did not have, so the first valid report stopped before auto-merge. It now creates the fixed label set idempotently before applying it. A separate failure window could push the deterministic feedback branch and then fail while opening the PR. The next attempt found the orphaned branch and could not recover. GitHub submissions now refuse before any git write when no feedback credential resolves; after proving that no PR exists, a retry may replace only that tool-owned branch with an exact-SHA force-with-lease. A concurrently changed branch is refused, never overwritten. The local feedback ledger is also serialized, so two submissions cannot race an update and lose one successful filing.

Action scope: local. If feedback submission previously failed after pushing, retry it after upgrading. For a GitHub hub, provide a dedicated feedback token.


### One session can submit every grounded feedback item without batching them into one PR

**feat · normal**

`a2a feedback submit` accepts several report paths, and `--all` selects every draft that is not already in the local ledger. All selected reports are validated before the first write. Each item still becomes its own PR, preserving the intake quarantine and independent triage; if a later item fails, earlier successful PRs are reported honestly rather than described as rolled back.

Action scope: local. Use one invocation after a session that produced several independent, evidence-backed reports.


### Pending lifecycle and contract writes have a supported wait path

**feat · high**

Lifecycle and contract writes now persist the same pending marker as submit. `a2a await <artifact-id>` follows that recorded PR, waits for the required check and the merge, refreshes the mirror, then clears the marker. It refuses a red check, a closed-unmerged PR, cancellation, or a timeout; it never merges the PR itself. If a follow-up transition is attempted while the previous write is still pending, LFC-001 now names the actual PR and the exact await command instead of presenting a generic illegal-state refusal. Required-check lookup now pages through all GitHub check runs, so the waiter does not miss the compound required check when many integrations have attached checks to the same commit.

Action scope: local. Await the recorded write before issuing the next state transition.


### Every next command printed by a thread is invocable as written

**fix · normal**

The protocol transition is named `acknowledge`, but the CLI command is `ack`; thread text printed the protocol word and sent agents to a command that did not exist. Text rendering now maps protocol transitions to copyable CLI invocations, including the `contract` subcommand for contract transitions. JSON keeps the protocol vocabulary unchanged.

### Statusline integrations can consume facts without parsing presentation text

**feat · normal**

`a2a statusline --json` returns the counts, urgent item, stale/update facts, severity, and quiet state from the same computed result as the text renderer. `--sample` produces a deterministic non-empty result without config or network, so shell wiring can be tested before a space is connected. `--no-prefix` removes only the leading `a2a: ` from text. The default text, quiet behavior, and exit codes are byte-compatible.

Action scope: local. Prefer JSON for a host integration and use sample mode while wiring it.


### The merge gate rejects a hand-authored illegal or unauthorized lifecycle event

**fix · high**

Pre-write CLI and MCP checks were not a product boundary: a contributor could hand-author event YAML and open a PR around them. In v3-pr, each changed event now passes event/v1 first and is then checked as a candidate against merge-base history, with every changed candidate excluded from its own prior. The verdict comes from the same fold legality primitive as local writes, including response-scoped verify/dispute and per-version contract transitions. Illegal transitions report LFC-001 and unauthorized actors LFC-002 in the existing per-path CI result.

Action scope: space. Spaces receive this protection after updating their reusable validation workflow pin to this release.


### Pending-marker cache paths reject traversal-shaped identifiers

**fix · low**

Pending marker reads, writes, removals, and whole-space cleanup now require one safe path component for both the space and artifact id. Normal IDs are unchanged; malformed values containing separators or dot-components fail before touching a path outside the pending cache.

### Release notes can no longer silently lag behind product fixes

**feat · normal**

`make check` now anchors on the newest semver release-notes file and rejects later feat, fix, perf, or breaking commits that touch product surfaces. The failure lists every uncovered commit. Documentation, chores, tests, and refactors without a breaking marker do not create noise. The gate is offline and carries teeth proving an uncovered fix and a breaking change go red, a notes update clears them, and non-product work stays green. This is a freshness guarantee, not an automated claim that the prose describes every diff correctly; release review still owns that judgment.

### The per-version contract lifecycle is proven through the real protected write funnel

**fix · normal**

The exact release-candidate product tree completed the full two-identity live matrix against the protected GitHub test space: 40 of 40 rows passed with EXIT=0. The rolling-window row published several version lines, deprecated one, added a late adopter, published maintenance, acknowledged, retired the old line while newer lines stayed live, and published again after retirement. The same run proved required checks, auto-merge, stale-write refusal, unauthorized boundaries, idempotent recovery and both participants' view of the thread. This closes the verification limitation carried from 0.13.0 instead of inferring live behavior from hermetic coverage.

### MCP contract publishing refuses incompatible version claims before opening a PR

**fix · high**

`a2a_contract publish` now resolves the prior version from git history and calls the same publishability and computed-compatibility core as the CLI. A breaking schema declared as minor or patch returns POL-007 locally, naming the prior fixture that no longer validates, and neither surface reaches the write funnel. Major bumps and formats outside the JSON-Schema compatibility rule retain their existing behavior.

### Published contracts carry both positive and negative executable examples

**fix · normal**

POL-009 now enforces the plan's format-neutral baseline for every contract: at least one schema, valid fixture, and invalid fixture. For JSON-Schema contracts, `a2a contract new` and its MCP twin scaffold all three; tests compile the starter schema, prove the positive example passes, and prove the negative example fails. Other formats still leave deep fixture semantics to the producer's CI, but may no longer publish an empty baseline. A read-only corpus check found no published contract content requiring a migration in the public hub or the connected external space.

Action scope: local. Before publishing a hand-authored contract, provide schema plus valid and invalid fixture examples. For non-JSON formats, keep their semantic validation in the producer's CI.


### Shell completion includes the full skill command family

**fix · low**

`a2a skill install` and `a2a skill link` now come from one subcommand registry shared with completion. The dispatch parity test renders bash, zsh, and fish and verifies every registered family member is present.

Action scope: local. Regenerate or reload shell completion after upgrading if the current shell session cached an older script.


### Public feedback intake now has a branch-wide least-privilege merge policy

**fix · high**

The public product repository had no branch protection while its feedback workflow assumed protection would hold auto-merge. The intake check was also path-filtered, so it could not safely become a required branch-wide context. A universal read-only `merge-policy` now classifies every pull request. Ordinary code receives no write privilege; a pull request touching the quarantine must be exactly one newly added feedback YAML, validated by a pinned release without checking out its head. Only then does a separate job receive the closed-enum kind and permission to label and arm auto-merge. Feedback validation now rejects HTML comments, control bytes, zero-width characters, and bidirectional controls as FB-009. This guards the human/agent visibility boundary without a brittle blacklist that would reject legitimate reports discussing prompt injection. The indirect gRPC module is updated to the first release fixing GO-2026-6061.

### Install native macOS and VS Code notifications from the a2a release

**feat · high**

`a2a notifications install` installs or repairs a version-matched companion from the same signed release cohort as the CLI. On macOS 13+ this is a background-only Swift app with the fixed `io.a2ahub.notifier` bundle identity, Notification Center permission, and the system Login Items API. The release app is a universal Apple-Silicon/Intel binary, hardened, Developer ID signed, notarized, and stapled before upload. In VS Code the command installs the matching VSIX into the selected CLI/profile and exposes the same current level in the status bar plus transition notifications. Installation is per user; enrollment is per project, so one machine may watch only the repositories the user chooses. The terminal statusline remains a separate optional, user-owned surface. This command never edits Claude Code, shell, prompt, or provider config.

Action scope: local. Choose either supported surface or both. macOS asks for Notification Center and Login Item approval; VS Code may ask to trust the extension publisher. Repeat the install command to repair an a2a-owned install.


### Notification clicks open a trusted local A2A view, including future What’s New

**feat · normal**

macOS and VS Code receive only bounded, sanitized projection data and an opaque route minted by the CLI. Open A2A resolves that route from the machine-local ledger and opens the existing local HTML dashboard focused on the item; artifact text cannot choose a URL, path, or command. Update availability is projected into every enabled surface as well. An older binary renders future release prose only from a separately signed and schema-validated release-notes asset cached from the target cohort. If that detail is missing, malformed, unknown, or unverified, the card shows the current/latest/floor versions, cache provenance, and a trusted release link instead of pretending its embedded notes know the future. A click never starts `a2a update`.

### Protected signing and interactive platform matrices require release-environment proof

**known-issue · normal**

Hermetic Go, Swift, and VS Code tests cover projection, leases, routing, permission denial, install repair, rollback, profile ownership, and both macOS architecture slices. Local builds cannot prove the protected Developer ID/notary secret path or replace clean-machine interaction tests for Notification Center, Focus, Login Items, VS Code Restricted Mode, profiles, disablement, and remote windows. Those release checks remain explicit and must not be inferred from unit-test success.

### The shipped skill no longer tells MCP agents to avoid contract publishing

**fix · normal**

The MCP contract publisher now runs the same computed compatibility check as the CLI, but the hand-maintained skill and troubleshooting page still described the old limitation and sent agents back to the CLI. Release review now removes that stale warning. The review checklist also covers the hand-maintained notifications, thread, and contract-version pages added since its original file list, so future releases cannot tick a completeness claim over a partial inventory.

## v0.13.0 — 2026-07-27

A contract's versions now hold independent states, so the deprecation cycle finishes instead of bricking the contract, a maintenance release is compared against its own line, and a consumer who adopts late still hears that the contract is sunsetting

### The contract lifecycle is per VERSION, and the cycle that used to destroy a contract now completes

**break · high**

This is the release the previous one told you to wait for. Read it before your first contract evolution; it changes what several commands answer. Until now a contract's lifecycle was recorded per CONTRACT. One state for the whole thing, with no way out of `retired`. So the steady state of a maintained interface — 1.0 published, 1.1 published, 2.0 published, 1.x deprecated with a sunset, 0.x retired, all at once — could not be written down at all, and the documented deprecation cycle behaved differently depending on the order you took its steps in. One order refused loudly. The other committed and left the whole contract permanently inoperable while a newer version was live and being consumed by the other side. Versions now hold their own states. `deprecate 1.0` while 2.0 is published leaves the contract `published`, because it is — 2.0 is live. `retire 1.0` in that situation SUCCEEDS, and is the ordinary act it should always have been. Both orders of the cycle complete. The contract-level state every read surface shows you is now a projection over the versions: published while any version is published, deprecated once every published version is deprecated, retired when they all are. So inbox, statusline, thread and the retire precondition keep answering the same question and start answering it truthfully. POL-011 — 0.12.0's fail-closed guard, which refused the retire rather than let it brick the contract — is REMOVED. It existed only until this shipped. If you already reached the broken state, nothing needs undoing: state is recomputed from immutable history on every read and every event carries its own version, so upgrading repairs the contract. What exactly changes for an existing history is listed separately below.

Action scope: local. Nothing to migrate and no flag to set — but a retire you were told was impossible is now available, and a contract you believed was dead may not be. Re-read the contracts you had given up on.


### A maintenance release is compared against its own line, not against the newest major

**fix · high**

Publishing 1.2 while 2.0 exists — the normal act during a sunset window — compared 1.2's schema against 2.0's fixtures, minted 2.1 under `--bump minor`, and computed the major-bump gate against the wrong line entirely. The baseline was whichever version was globally highest, regardless of which line you were publishing on. The baseline is now the highest published version strictly BELOW the one you are publishing. The old behaviour is that rule's special case, for when the new version happens to be the highest. Two consequences worth knowing rather than discovering. `--bump` still bumps the globally-highest version, because it has to choose before it knows your target — so publish a maintenance release with an explicit `--version`, the same way `deprecate` and `retire` already require one. And a publish with no prior version below it (opening a lower line while a higher one is live) has no baseline at all: it computes no compatibility and is gated for human review, exactly as a first publish is.

Action scope: local. Only if you maintain more than one line. A minor or patch published on an older line under `--bump` picked the wrong baseline before this; use `--version` and the comparison lands where you meant it.


### A consumer on a newer major no longer blocks retiring an old line forever

**fix · high**

The retire precondition asked whether every registered consumer of the CONTRACT had acknowledged the deprecation. A consumer pinned to major 2 therefore blocked retiring the 1.x line indefinitely — they had nothing to acknowledge, and no way to stop blocking you. It now asks about consumers registered on the major being retired. Their pin is already in `consumes.yaml`; no registry change, no migration. The deprecation ANNOUNCEMENT is deliberately unaffected and still goes to every registered consumer on any major. So "who was told" is the larger set and "who can block me" the smaller one — a consumer on major 2 hears that 1.x is sunsetting and is simply not standing in the way of it.

### A consumer who adopts DURING a sunset window now sees the deprecation that was announced before they arrived

**fix · high**

A deprecation announcement's `to:` is computed once, when the producer runs `deprecate`, and then frozen. The announcement's id is derived without the recipient set, so re-running the command after a new `adopt` is deduplicated by the write funnel and the newcomer is never addressed. Adopt during a sunset window and you were simply never told — permanently, and with nothing to notice. Under a rolling window, deprecations are the steady state rather than an occasional event, so this stopped being an edge case. Your inbox no longer asks `to:` alone: an announcement whose `deprecates:` names a contract in your own `consumes.yaml` is yours, addressed or not. `to:` becomes a courtesy snapshot — still honoured, never authoritative. It is a union, never a swap. If you were named in `to:` and have since removed the dependency, the announcement stays in your inbox while you migrate off; nothing disappears. One degradation is deliberate and reported rather than silent: if your own `consumes.yaml` cannot be read, the inbox falls back to `to:` alone and names the file in its skipped-files advisory. The retire gate fails CLOSED on that same input, for the opposite reason — there, unreadable means "I cannot prove nobody depends on this".

Action scope: local. Run your inbox after upgrading: a deprecation announced before you adopted may be waiting, and it may already be inside its sunset window.


### A consumer registered by a satisfied requirement now actually blocks retire — that half of the check never fired

**fix · high**

A system counts as a registered consumer of a contract in two ways: a `consumes.yaml` entry, or a satisfied requirement naming the contract as its `target_contract`. The second half could never fire. The scan folded each candidate requirement with an incomplete envelope, omitting `to:`. A requirement reaches `satisfied` only via an acknowledge by its TARGET, and the authorization check resolves the target from that field — so the acknowledge was rejected, the fold stopped at `published`, and every satisfied requirement in the space read as unsatisfied. The consequence was not cosmetic. A gate written to fail closed was failing OPEN down one of its two branches, invisibly, because the branch never evaluated true rather than because it evaluated wrongly: a consumer who registered by filing a requirement never blocked the retirement of the contract they depend on. If you have been relying on a satisfied requirement as your registration, a retire that previously would have proceeded without your acknowledgement will now wait for it.

Action scope: space. The producer side may now find a retire blocked that was not blocked before, by a consumer who was always supposed to be counted. That is the check working. If you cannot identify who is blocking you, the refusal names them.


### What re-folding an existing history changes, in full

**break · normal**

State is never stored — it is recomputed from immutable history on every read — so upgrading re-folds every contract you already have. Three subject-state outcomes change, and all three come from ONE correction: the previous engine had an interim rule that let a publish from `deprecated` silently re-publish the contract, and a version number that has already been minted can no longer be published again. A contract that went publish, deprecate, publish-the-same-version now reads `deprecated` where it read `published`: the republish is refused rather than resurrecting it. A contract that then went on to retire reads `retired` where it read `published`: with the bogus republish refused, the version was still deprecated, so the retire — previously blocked — lands. This is the cycle completing, seen as a state change. A contract that deprecated once more after that reads `retired` where it read `deprecated`, for the same reason one step further along. Every one of these is the corrected answer, and every one is reachable only through a history that republished a version number it had already used. A history that publishes each version once is completely unaffected — that is checked by an exhaustive comparison against a frozen copy of the previous engine, over every sequence of contract transitions up to length seven, which is past the point where new differences can appear.

Action scope: local. Nothing to run and nothing to repair — the record was never wrong, only its interpretation. Re-read any contract whose state surprises you against the three cases above.


### `a2a contracts` and the dashboard show the rolling window, not just the newest version

**feat · normal**

A contract with several live version lines used to render as one version and one state — the newest published, and the whole contract's projected state. Both are still there and still mean the same thing, but neither can say "1.0 retired, 1.4.1 published, 2.0 published", and a reader shown only the summary cannot tell a contract with one live line from one with three. `a2a contracts` now prints a sixth column for any contract with more than one recorded version — `1.0.0=retired 1.4.1=published 2.0.0=published`, oldest first. The five existing columns are byte-identical, and a contract with fewer than two versions prints exactly as before, so nothing that parses this output breaks. `a2a contracts --json` gains a `versions` array on the same condition, and `a2a html` renders the window under each contract you provide, with a deprecated line highlighted because it is the one you have to act on. Worth knowing when you read a state that looks wrong: a contract reads `published` while one of its lines is deprecated — because it is published, on another line. That is the projection working, not a stale value.

### The dashboard grades a dependency by YOUR pinned line, not by the contract's overall state

**fix · normal**

Now that a contract holds several version lines at once, its own state is a projection over them — so grading your dependency by that state answers the wrong question. A contract reading `published` (because 2.0 is live) told you `current` while the 1.x line you are actually pinned to had been deprecated for weeks. `a2a html` now grades each dependency from the state of the major you registered on: deprecated shows deprecated, retired shows retired, and a newer major existing beside yours shows behind. A new grade, `missing major`, appears when the major in your `consumes.yaml` is not in the provider's version window at all — previously indistinguishable from being up to date. Each dependency also carries its line's sunset date and named successor where the provider set them, so the migration target is on the same row as the warning. The validation-flags panel now shows real facts from the read model — fold's own protocol flags and the cache's skipped-file reports, each naming its source — instead of the two invented codes the demo fixture had been carrying.

Action scope: local. Re-open the dashboard after upgrading. A dependency you believed was current may be on a deprecated or absent line; that was true before this release too, and the page simply could not say so.


### The per-version lifecycle has not been exercised against a real space with a real counterparty

**known-issue · normal**

Everything above is verified hermetically — unit tests, an exhaustive migration comparison, and an adversarial audit that reproduced each of its findings before reporting them. None of it has run through the live matrix against real GitHub with a second participant. The row that would do it is authored and not dispatched: publish 1.0, 1.1, 2.0, deprecate 1.1, publish a maintenance 1.2, retire the 1.0 line — in both orders of the deprecation cycle. It needs a real space and a real counterparty, and this release did not have one. Recorded here rather than discovered later. The parts most likely to differ under a real run are the ones a hermetic test structurally cannot see: the write funnel's own deduplication of a re-run `deprecate`, and CI's behaviour on a maintenance-line publish.

## v0.11.0 — 2026-07-27

Your space's merge gate may have been running a validator two releases old — bump the pin; and a template placeholder can no longer merge into the shared record, a nested field can be filled without a text editor, and a document the read model could not decode now says so instead of vanishing

### A frontmatter value still holding its template placeholder is refused at submit (POL-010)

**policy · high**

`a2a new question` produced a draft whose `expected_response.shape` still read `<what a good answer looks like>`, `a2a validate` returned ZERO violations on it, and `a2a submit` accepted it. So the field whose entire job is to tell a responder what a good answer looks like could reach a counterparty containing template prose. Verified on the released 0.10.0 binary before being fixed, not reasoned about. POL-010 now refuses any frontmatter value — at any depth, including inside a mapping like `expected_response.shape` or a list element like `refs[].ref` — that is still, verbatim, the template's own `<...>` placeholder. It fires at V2 only: `a2a submit` and `validate --ci`. `a2a validate` on a fresh draft deliberately still passes, because a draft is meant to be inspectable before it is filled; the refusal belongs at the boundary where a draft becomes a shared record, not at the authoring point. Two things about the message are deliberate. It names the field by its full dotted path, so there is no guessing which one. And it offers BOTH remedies, because for an optional field the fix is DELETION, not invention: `needed_by`, `valid_until` and `env_requirements` ship placeholders and reach this check as schema-valid strings, and an author told only to "fill it" would invent a date the requirement does not have. For a value inside a list, where no flag can reach it, the message names the file edit instead of a flag that would fail. What this costs you: a value that GENUINELY is exactly `<foo>` is refused too, and must be rephrased. Across the eight envelope types no field has such a legitimate value.

Action scope: local. A submit that used to succeed now fails if a draft still carries a placeholder. The refusal names the field and the fix, so it costs one command — but an automated caller that never reads stderr will see an exit code where it used to see success.


### --field accepts a dotted path, so a nested field no longer needs a text editor

**feat · normal**

`--field expected_response.shape=...` did nothing reachable: the fill mechanism walked only TOP-LEVEL frontmatter keys, so a nested key could not be supplied at all except by hand-editing the drafted file — which is not an option for the automated caller this product is built for. A dotted `--field` key (and, over MCP, a dotted `fields` key) now descends nested mappings. Mappings only, by design: `refs[0].ref` remains unsupported rather than shipping a half-invented list-index grammar. An unresolvable dotted path is still an ERROR naming the full path and the exact segment where descent stopped — never silence, which was the original defect and must not reappear on a new axis. One consequence worth knowing: an explicit `--field actor.name=X` now wins over the actor resolved from your environment and config. That is the documented resolution order (explicit flags first), not an accident.

### A file the read model cannot decode is now named, instead of silently missing from every read

**fix · high**

Observed, not theorised. A `thread:` key accidentally written twice in one envelope made the artifact undecodable, and the mirror walk simply skipped it: `a2a show` still printed the document while `a2a search`, `a2a inbox`, `a2a statusline` and `a2a thread` all returned nothing for it, with no error anywhere. It took four test failures across unrelated areas to trace back to one duplicated line. This is the failure mode this product most has to avoid: an artifact that is silently absent reads exactly like an artifact that does not exist. In a shared record spanning two companies, a counterparty's document dropping out of your index without a word is worse than a hard read error. The walk stays best-effort — one bad file must never blind you to every other document in the space — and the missing half, the report, now exists. `a2a search`, `inbox`, `outbox` and `thread` write an out-of-band advisory to STDERR naming each undecodable file and why (`unreadable`, `not-frontmatter-shaped`, `undecodable-yaml`, `no-id`, `unrelativizable-path`). Their stdout is byte-for-byte unchanged, deliberately, so existing consumers of the JSON item array are untouched. `a2a doctor` gained a `skipped mirror files` row that names the same files. `a2a statusline` is deliberately NOT wired: its whole output is one line bound for a shell prompt. The doctor row PASSES with an advisory rather than failing, and the reason matters: the unreadable file may sit in a COUNTERPARTY's own section of the space, where you are structurally unable to edit it. A row permanently red on someone else's file is a row nobody reads.

### The MCP read tools return an object with the item list inside it, not a bare array

**break · normal**

`a2a_inbox`, `a2a_outbox` and `a2a_search` returned a bare JSON array as their structured result. They now return `{"items": [...], "skipped": [...]}`, and `a2a_thread` gained a top-level `"skipped"` alongside its existing fields. Why the shape had to change rather than ride a side-channel: the fact being reported — which files the read model could not decode, and therefore which documents are missing from this answer — is about the SPACE, not about any one item, and an array has nowhere to put it. The CLI puts it on stderr; an MCP tool has no stderr, its result IS the channel. Putting it in the human-readable text content instead was considered and rejected: an agent reads the structured content, so the report would have been present and unread, which is the defect rather than the fix.

Action scope: local. Any MCP caller that indexed these results as an array must read `.items` instead. This is a one-line change at each call site and there is no compatibility shim — the product is pre-1.0 and a silent dual shape would be worse than a named break.


### The live matrix reports an undispatched test row in milliseconds instead of an hour

**fix · low**

Harness-only, no product surface. A declared live-matrix row whose scenario family nothing dispatched used to surface as `not-run` at the END of a 59-minute run — the tier caught it, which is correct, but reported in an hour what was knowable instantly. Each row now declares its family, an untagged test asserts every declared family is dispatched and every dispatched family is declared, and the runner asserts the same before its first API call. `make check` additionally type-checks the live tier's build-tagged tree, which nothing did before — a compile error there previously surfaced only when somebody started a two-hour run.

### Your space's merge gate may have been validating with a binary two releases old — bump the workflow pin

**fix · high**

Read this one even if you change nothing else, because the fix is in YOUR space's repository and not in this binary. A space's `.github/workflows/a2a-validate.yml` is a thin caller: it passes `mode` and `base` and nothing else, so which `a2a` actually runs `validate --ci` at your merge gate is decided by the `a2a-ref` DEFAULT inside the reusable workflow you pin. v0.10.0 shipped with that default still reading `v0.9.1`. A tag is immutable, so every space pinned `@v0.10.0` has been running the v0.9.1 validator at its gate — and enforcing NONE of the CI-side work v0.10.0's own notes announced: `space.yaml` was not validated (the document that decides who may write where), events were not validated against event/v1, the producer stamp was not checked, and `thread` was not required. Nothing reported it. This is the second occurrence of exactly this skew; v0.7.0 shipped with the default at v0.5.0. What changed here: the v0.11.0 workflow carries `v0.11.0`, and the coupling is now held by a test that runs offline on every `make check` in this repository rather than by a pre-release script somebody has to remember to invoke. The pre-release script had been asserting it correctly since the v0.7.0 miss; being opt-in is what made it miss the second one. What it does NOT do: repair a space already pinned to an older tag. That takes one command in the space, below.

Action scope: space. A space pinned to a tag before v0.11.0 keeps running that tag's `a2a-ref` default until its workflow pin moves. Until it does, your gate is not enforcing the rules this binary refuses locally — so a counterparty on an older binary can still merge what your side would refuse. Bumping the pin is the whole remedy.


### With no space connected, MCP now serves a2a_read and a2a_whatsnew — not six tool names retired before the first public release

**break · normal**

`a2a mcp` has two degraded modes: no space connected yet, and a space that is unreachable at startup. Both served `a2a_inbox`, `a2a_outbox`, `a2a_show`, `a2a_thread`, `a2a_search` and `a2a_contracts` — the per-verb read tools that were folded into `a2a_read`'s `view` enum before v0.1.0, so they have never been part of the connected surface any published release offered. They were folded in the CONNECTED registry only; the wiring for the no-space case carried its own copy of the registration and was never migrated, in every release since. So the very first MCP session in a fresh repository — before `a2a connect` — was handed the one tool surface that nothing documents: `skill/a2ahub/reference/commands.md`, the catalogue an agent reads, is generated from the connected registry and lists `a2a_read`. Those six names also vanish the moment you connect. `a2a_whatsnew` was missing from that surface entirely, even though its handler reads only the corpus embedded in this binary and needs no space at all. The agent best placed to ask "what changed since the version I know?" — one just arriving in an unfamiliar repository — was the one that could not ask. Both are fixed by deleting the second copy rather than keeping it in step: one registrar now serves every tool that needs nothing but the local cache, and the connected registry composes it, so a degraded session offers a strict SUBSET of a healthy one by construction instead of by discipline.

Action scope: local. If you called `a2a_inbox` (or the other five) against a session with no connected space, use `a2a_read` with `view: "inbox"` — the same call you already make once a space IS connected. There is no compatibility shim: a surface that answers to two different sets of names depending on connection state is worse than a named break, and this product is pre-1.0.


### MCP's contract publish still gets its refusal from CI rather than locally, and `a2a skill link` is still missing from shell completion

**known-issue · normal**

Carried forward from v0.10.0's RN-01000-1, narrowed by what this release fixed, and repeated here because a standing limitation that only appears in the release that first declared it goes silent for everyone who updates past it — you would be reading nothing at all about this if it were not restated. Still open: `a2a_contract publish` over MCP calls neither of the two local pre-flight checks the CLI runs before a contract write (CheckContractPublishable, CheckComputedCompatibility). It is NOT a correctness hole — `validate --ci` is path-and-diff based and fires against whoever opened the pull request, so an illegal publish is still refused at the merge gate. What it costs is the loop: a refusal the CLI delivers in a second, locally, with the reason in front of you, arrives from CI a minute later attached to a pull request instead. Use the CLI for contract publishes if you want the refusal locally. Deferred by an explicit decision, not by oversight. Also still open, and separate: `a2a skill link` does not appear in `a2a completion bash|zsh|fish`. Cosmetic, CLI-side, and named here only because v0.10.0's entry lumped it in with the MCP gap. NOW CLOSED, and no longer part of this entry: the MCP tool- registration gap it also named. `a2a_whatsnew` is registered, and the forked no-space surface underneath that gap is gone — see RN-01100-7.

## v0.10.0 — 2026-07-26

The document that decides who may write where was validated by nothing; a deliberately red check now says so; and the MCP write surface is named for what it is

### The MCP write tools are not at parity with the CLI, and this release does not change that

**known-issue · high**

Stated plainly because the alternative is you finding out from a refusal. `a2a_contract publish` over MCP calls neither of the two local pre-flight checks the CLI runs before a contract write: CheckContractPublishable and CheckComputedCompatibility. It is NOT a correctness hole — `validate --ci` is path-and-diff based and fires against whoever opened the pull request, so an illegal publish is still refused at the merge gate. What it costs is the loop: a refusal that the CLI delivers in a second, locally, with the reason in front of you, arrives from CI a minute later instead, attached to a pull request. For the automated caller this product is built for, that is the difference between a corrected write and a stalled turn. Two low-severity tool-registration gaps ride along in the same surface. Deferred by an explicit operator decision on 2026-07-26 rather than by oversight, and this entry exists so the decision is visible rather than implied. Two things it is worth being precise about, because the last two releases were not: v0.8.0's RN-0800-8 claimed "the MCP write tools are fine", which was false and was corrected in RN-0900-2 — and this entry is not the opposite claim. MCP writes work. They are second-class in where their refusals come from, not broken. One thing this decision was briefly thought to block, and does not. The named missing-actor refusal (RN-01000-8) looked like it needed the MCP surface, on the reading that every single door for it crossed over. It does not: the two surfaces have always had their own separate actor resolvers, so the CLI fix is entirely contained and MCP keeps its own behaviour untouched. Recorded because the earlier reading was reasoned about rather than read, and an MCP fence that gets credited with blocking things it does not block is how a deferral quietly grows.

### space.yaml is validated — the file that decides who may write where was checked nowhere

**fix · high**

`space.yaml` is the space's trust document: the CI gate authorises every pull request against its participants and their sections. Nothing validated it. Not weakly — nowhere. A schema for it exists, a validator seam for it exists, and an adapter implementing that seam against the real schema corpus exists; on 2026-07-26 all three turned out to be wired to no production path at all. Every read went through a shape-only decode that says as much in its own comment. Found by writing a manifest by hand while following the published from-zero walkthrough. It omitted two schema-REQUIRED participant fields. `a2a connect` accepted it, `a2a doctor` accepted it, and `a2a validate --ci --mode=v3-full-repo` answered `"valid": true` — while the schema, asked directly, rejects it. So a manifest could be merged in a shape the schema forbids, and the guard that depends on it went on reading it. Now checked at the CI gate: a pull request that touches `space.yaml` is validated against the schema, and a full-repo audit validates it unconditionally. Deliberately NOT checked on the read path — making that strict would mean a space whose manifest drifted stops being READABLE, and a participant who did nothing wrong would get a hard failure on every verb. An unvalidated manifest is a better product than an unreadable space. Equally deliberate: a pull request that touches only an artifact is not reddened by a manifest it did not propose. Otherwise a manifest merged before this check existed becomes a tripwire on every unrelated write, with the only way out — a manifest pull request — blocked by the same gate. Every manifest in existence was run through the schema before this shipped: the live production space, the test space, and the template all pass. Three TEST fixtures did not, which is how three tiers had been agreeing with each other about a document none of them would ever meet.

Action scope: space. the check arrives per-space when the space re-pins the reusable workflow — until then its manifest is still unchecked at merge

### a2a doctor asks GitHub whether your CODEOWNERS owners actually exist

**fix · normal**

GitHub IGNORES a CODEOWNERS owner it cannot resolve — it does not reject the line. So a file naming a team nobody created looks like it gates `/space.yaml` and gates nothing, and code-owner review is the entire mechanism behind that gate. An inert CODEOWNERS is an ungated trust root with nothing at merge time to tell you. This shipped twice as a documentation fix before becoming a check, and the second time is the instructive one. The placeholder became `@REPLACE_WITH_ORG/REPLACE_WITH_TEAM_OR_LOGIN` with an instruction to replace BOTH halves — and replacing both halves literally produces `@your-org/your-login`, which is still a team reference, still to a team that does not exist. Following the instruction exactly still produced an inert file. Two changes, then. The placeholder is now a single login, because a shape that cannot be filled in wrongly beats a warning about filling it in wrongly. And the check is automated: `a2a doctor` reads GitHub's own `codeowners/errors` endpoint, which answers with line numbers and names all three conditions an owner must satisfy. The template had been telling operators that GitHub's web view was "the only feedback you will get" — wrong, and once a thing is machine-readable, asking a human to check it by eye is a choice. A credential that cannot read that endpoint gets an advisory, never a FAIL: a fine-grained token without repository-metadata read is a legitimate working setup, and a gate that reds on a working setup is a gate people stop reading.

Action scope: local. the new doctor row ships in the binary; check your own space with it

### A transient GitHub is retried and named, instead of reported as a refusal

**fix · normal**

Observed, not theorised. During GitHub's Pull Requests outage on 2026-07-24 every `a2a submit` died instantly on a bare `github api request failed: status 500:` with an empty body. The push had landed; only the pull request had not. Nothing in that message let an agent tell a platform outage from a real refusal, and the one correct next move — re-run, which is safe because the write funnel is idempotent by head branch — was named nowhere. A 5xx, a rate limit and a transport error are now retried on a bounded schedule at the single point every REST and GraphQL call passes through, with the whole window capped. A refusal still fails on the FIRST response, exactly as fast as before: burning the retry budget on a 403 or a 422 only delays an answer that will not change. When a transient failure survives the retries, the message says so — transient, the write may or may not have landed, re-running is safe, and why. Two details worth knowing. A 403 is decided by its headers, because GitHub uses that status for both a rate limit and a hard permission refusal and the status alone cannot tell them apart; a bare 403 stays fatal. And if a write DID land while GitHub failed to say so, the retry is refused as a duplicate — which is now named as exactly that, rather than surfacing as a puzzling 422 for a write you believe you never made.

Action scope: local. the fix is in the binary

### The branch-protection checklist stopped contradicting itself about admins

**fix · normal**

The checklist claimed direct pushes to `main` are "forbidden for all actors, including admin" two rows above `enforce_admins: false` — which is precisely what makes that untrue. Driven against a fresh space on 2026-07-26: an owner's `git push origin main` SUCCEEDS, with GitHub answering `remote: Bypassed rule violations for refs/heads/main`. The published onboarding then told you to "confirm by hand that a direct push to main is rejected". So following the documentation makes a correctly configured space look broken, and the natural conclusion — that protection never armed — is wrong. The property that matters is that a PARTICIPANT cannot push to `main`; an admin bypass is the configuration working, because a sole code owner must be able to land their own space-level edits. Both documents now say this, the checklist ships the two `gh api` calls that apply it (with a read-back so you verify what landed rather than trusting the call), and a test holds the document to the configuration the test rig actually applies — including the prose claim, which a value-by-value comparison structurally cannot see.

Action scope: space. if you armed protection from the old checklist the VALUES are fine; what was wrong is what you were told to verify

### A deliberately failing test check now says it is deliberate

**fix · low**

The live test matrix opens one pull request whose check MUST go red — it proves the gate refuses a write into another system's section, and keeps refusing after a privileged re-trigger. That red run appears in the test space's Actions tab and in its failure mail, and it was titled "probe xsec" with an empty body. Every failed run in that space over three hours and three matrix runs was this probe: the product was working and the operator was reading failure notifications for a row that was passing. The probe now labels itself where it is actually seen, and the matrix accounts for the runs it reddened by RUN ID — so a red run that is not on that list becomes a reported finding rather than something to squint at. Keyed on ids rather than a branch-name pattern deliberately: a pattern would absolve the next probe somebody adds without its having asserted anything. Test infrastructure only. Nothing in the product changes; the next release is checked harder.

### Every event now names the tool and version that wrote it — the first server-side stop for a stale binary

**schema · high**

`min_binary_version` is checked INSIDE the writer's own binary. That binds a binary which honours the check and does nothing whatever to one that does not — and that is not hypothetical: before 0.9.0 every lifecycle and contract verb wrote with an EMPTY floor, because the shared request builder never populated it, so raising a space's floor could not have gated those writes even in principle. Nothing server-side read the floor at all. event/v1 gains an optional `produced_by: {tool, version}`, written by the write funnel for every event it commits, and `validate --ci` requires it on every changed event — at MERGE, from the document, where an old binary can finally be refused. **The switch is your space's own floor, and the sequencing is automatic.** A write stamps only once `min_binary_version` has reached 0.10.0, and the gate requires it under exactly the same condition. That is not caution: an event carries no unknown fields by schema, and a space's CI validator is pinned BY THE SPACE — so a stamped event landing in a space still pinned to an older release would be REJECTED outright, every write refused over a field its validator has never heard of. `a2a space update` re-pins the validator AND raises the floor in one pull request, and that pull request carries no events, so the older validator accepts it. The field therefore cannot appear in a space before the validator that understands it. Nothing to coordinate between machines, no migration window, and no release that has to ship before another. **What it stops, precisely**: a colleague on a stale binary. Not forgery — the stamp is written by the very client the space is distrusting, so anything hand-crafting an event can claim any version. Said plainly here because a field that looks like a security control and is not is worse than no field at all.

Action scope: space. until the space raises min_binary_version to 0.10.0 nothing stamps and nothing is required — one `a2a space update` re-pins the validator and raises the floor together

### A write with nobody attached is refused by name instead of by a schema violation

**fix · normal**

In a container `os/user` has no passwd entry and `$USER` is unset, so every source that could name the acting identity is empty. The write was already refused there — `actor.name` carries a minimum length in both event and envelope schemas, so there was never a correctness hole — but the message was a schema violation about a field the caller never knowingly set, naming neither `--actor-name` nor `A2A_ACTOR_NAME`. It now names both, and says why a write is refused rather than attributed to nobody: the actor is recorded permanently in a shared log. CLI only. The MCP surface keeps its own resolver and its own schema-level refusal, by the same decision that leaves that surface alone this release (see the known issue above). The two surfaces have always had separate resolvers, so this is not a new divergence — a fact worth stating because an earlier note in this repo claimed the opposite, having reasoned about it instead of reading it.

Action scope: local. the fix is in the binary

### The contract template gives its prose four named sections

**feat · low**

A contract's machine surface is checked hard: the schema is validated, and breaking changes are COMPUTED from it at publish and at merge. Its prose had no agreed shape at all — so nothing ensured a contract said the things the schema cannot express, which are exactly the things a consumer needs. The template `a2a new contract` renders now carries: what it covers (including what is deliberately out of scope), error shape, compatibility intent beyond what the computed check enforces, and who to ask. A convention, not a rule — nothing rejects a contract that renames or drops them. Enforcing prose structure would mean a warning nobody is obliged to act on, and a gate that fires on something not broken is a gate people stop reading. The template is the enforcement that fits: it is what the author actually fills in.

### Events are validated against their own schema at the merge gate — until now they were not validated at all

**fix · normal**

`validate --ci` checked artifacts, the consumer registry, contracts and (as of this release) the space manifest. It did not check EVENTS. A changed event merged as long as it was parseable YAML, and the fold — which decides what state an artifact is in — read it afterwards. So an event missing `transition`, the field the fold reads to know what happened, would land and simply fold to nothing. The same shape as the manifest hole in this release: a schema that exists, and documents nobody fed to it. Wired only after the corpus was checked, which is the point worth carrying forward. Every event in the live production space and in the test space — 46 of them — was run through the schema by hand first, and all passed. A gate is not switched on to find out whether it reds; the check on your own space arrives when the space re-pins the validator, so if anything there were invalid you would learn it on your next write rather than from a release note. One trap it would otherwise have hit on every single write: an event's `at:` is a timestamp, and unquoted YAML timestamps decode to a native time value the JSON-schema validator cannot represent — it aborts with an internal "schema drift" error instead of validating anything. Caught in development and normalised, with an unquoted timestamp driven through the whole gate as a test.

Action scope: space. the check arrives per-space when the space re-pins the reusable workflow

### `a2a new --field to=beta` works — it used to be accepted and silently dropped

**fix · normal**

A `--field` override on a LIST field did nothing. The flag was accepted, the template's placeholder survived, and you found out minutes later when `submit` refused the write with "`to` includes an unknown system: <recipient-system>" — a complaint about a placeholder you believed you had replaced, with nothing anywhere saying your flag had been ignored. Found by drafting an announcement on a real space and reading the refusal. Three syntaxes were checked before calling it a defect rather than a typo: `to=beta`, `to=[beta]` and `to=- beta` all did nothing. Both halves are fixed. A list field now takes a YAML list (`[beta]`, `- beta`) or a bare scalar (`beta`, read as the one-element list — what anyone writing that means). And an override the renderer CANNOT apply is now refused where you give it, naming the field and what shape the template expects. That second half matters more: the old behaviour was not "unsupported", it was unsupported and silent. Both surfaces get it. `a2a new` and MCP's `a2a_new` render through the same template code, so the fix reaches both — which is worth saying because this was filed long ago as deferred FOR being cross-surface. It never was.

Action scope: local. the fix is in the binary

### Host errors say `host:` once instead of twice

**fix · low**

Every failure from the GitHub adapter read `host: OpenPR: host: …` — the prefix twice, in the middle of the sentence you actually read. Cosmetic right up until the live matrix printed it inside the transient-failure message that RN-01000-4 exists to make readable under pressure. Fixed so a wrapped error carries the prefix once while an error printed on its own still says which layer it came from. Nothing about error handling changes — only the text.

Action scope: local. message-only

### `a2a doctor` stops telling a project with no agent surface to run `a2a skill link`

**fix · low**

The "skill discoverable" advisory collapsed two situations into one message. If your project has an agent surface and has not linked the skill, `a2a skill link` is exactly right. If it has NO surface, that command answers "nothing to link", changes nothing, and the advisory repeats verbatim next time — so following the advice taught you the check was unreliable. Split. No surface now says plainly that there is nothing to link and that this is expected for a project no agent drives; a surface that exists gets the remedy AND the surface named, so you know what would be linked. Found by following the tool's own advice and watching nothing happen. Loud, specific and unactionable is the same family as a gate that names the wrong cause: the cost is not the wasted command, it is that you stop trusting the check.

Action scope: local. message-only

### "skill installed (version unknown)" now says WHY, and stops pretending there is something to fix

**fix · low**

One phrase answered three different situations and named neither a cause nor an action. It also fired constantly in ordinary development: a build from source stamps `a2a dev`, the version reader requires a digit, so every locally-installed skill reported "version unknown" with no explanation. Now each case says what it is. Installed by a development build — expected when running from source, nothing to fix, and deliberately NO remedy named, because re-installing from a dev build re-stamps `dev` and changes nothing. A stamp that is present but not a release version, or absent entirely — those DO name the action, because there it works. Found by reading real `a2a doctor` output rather than a test, hunting for siblings of the advisory that told you to run a command which could not help. This is the milder form of the same defect: a note that names neither cause nor action. Naming a remedy that cannot work is the louder form.

### A multi-step exchange between two agents is now one conversation you can read in a single call

**feat · high**

The chain a real negotiation walks — question, answer, follow-up request, answer, contract, agreement, a contract update, confirmation — was representable before this release and legible nowhere. Four things were wrong at once, and all four were deviations from rules the architecture already decided rather than gaps in it. `a2a new --thread <id>` had never worked. The flag parsed, set the field, and the value was silently discarded before it reached the document, because template filling only ever visited keys the template already carried and no template carried `thread`. That single defect explains why no artifact in any live space had a thread: the tool could not write one. The root cause is fixed generically — any `--field` naming a key the template does not have is now an ERROR — which immediately surfaced a second silent drop on the MCP respond path. Now: `a2a new` MINTS a thread, `a2a respond` and `contract deprecate`'s linked announcement INHERIT their source's, and an explicit thread that contradicts the source is refused naming both values. An agent never types or invents a thread id in the ordinary case. `thread` is required and its grammar is pinned in the envelope schema, so an artifact belonging to no conversation is not a document this product can produce. And `a2a thread` answers what an agent resuming a negotiation actually needs, in ONE read: the transcript of artifacts AND events in the order they were committed, and an OPEN block naming whose move it is. It takes a thread id or ANY artifact id from the conversation, because a caller pastes whichever id it is holding. `a2a show` names the thread an artifact belongs to; `a2a search --thread` filters by it; the local dashboard renders conversations and, for the first time, the links between documents.

Action scope: space. Two things. `thread` is now REQUIRED on every artifact, so a space whose pinned validator predates this release will start refusing writes from a newer binary and vice versa — re-pin it with `a2a space update`. And artifacts committed before this release carry no thread at all: they cannot be repaired in place (git is the archive, artifacts never move), `a2a respond` REFUSES to reply to one rather than silently starting a new conversation, and `a2a doctor`'s new `threads intact` row counts them so you know before an agent hits that wall. For a space with no live exchanges, reseeding is the clean fix.


### `a2a thread` used to print the answer above the question

**fix · high**

The ordering was derived from each artifact's most recent event, counting only events filed against that artifact. A response's `respond` event is filed against its PARENT, so a response had no events of its own, sorted first, and the reply appeared above the question it answered — every time, not occasionally. It is now ordered by the space's own commit sequence, which is the order the state computation already trusts. That matters beyond tidiness: a transcript ordered by a clock somebody else's machine set can disagree with the states printed beside it, and a conversation that reads authoritative while being wrong is the failure this whole feature exists to prevent. When the repository history cannot be read the reader falls back to declared timestamps and SAYS so, rather than degrading silently. Two more from the same pass. The reader listed artifacts only, so acks, responses, verifications and contract publishes — most of the actual history, none of which creates an artifact — were invisible. And it merged every connected space into one view, which the architecture forbids precisely because two conversations rendered as one is unreadable in the worst way; a thread id that resolves in two spaces now refuses and prints the command to disambiguate.

### A correctly verified response was flagging its own conversation as suspect

**fix · normal**

Every response that had been verified carried an `unauthorized-actor` flag against its own event. The state each read verb printed was right, which is why nobody noticed: the flag lived in a field the correcting overlay never touched. The cause is a scoping mistake. A response's verify and dispute events belong to its PARENT — that is where they are authorized and where they record their outcome — but the response's own state computation was being handed them too, and there the authorizing party resolves to the responder, who is of course never the one entitled to verify their own answer. It surfaced only because the new conversation view makes flags a signal an agent is told to trust: a healthy exchange would have reported itself broken. Found while building a test fixture, confirmed with a two-line probe before anything was changed, and pinned by a regression that asserts a healthy chain reads healthy end to end.

## v0.9.1 — 2026-07-26

Three things v0.9.0 shipped wrong: a read that could not see a merge, a CODEOWNERS placeholder that gated nothing, and a red gate reported as slowness

### A just-merged artifact was invisible to your reads for up to five minutes

**fix · high**

This is RN-0900-8 closed. v0.8.0 made read verbs refresh a stale mirror before reading, but "stale" was measured against the STATUSLINE's five-minute window, because both callers shared one setting. So a mirror fetched moments earlier — by your own `a2a submit`, for instance — counted as fresh, and an artifact whose pull request merged just after that fetch stayed invisible to `a2a inbox`, `outbox` and `show` for up to five minutes. There was no error: `show` simply answered "artifact not found". Found end to end against a live space, where one explicit `a2a sync` revealed the artifact at once. Five minutes is exactly the window in which your counterparty's merge matters most, and the session-start loop every agent follows says an empty inbox means proceed — so this reproduced the original blocker in a narrower and likelier form. The two callers were never asking the same question: the statusline renders on every shell prompt and needs a cheap window, while a read verb is asked deliberately by someone who wants the current answer. Reads now have their own 30-second window: long enough to dedupe the burst of inbox/show/thread a single agent turn fires seconds apart, short enough that a merge surfaces on your next question rather than after the conversation has moved on.

Action scope: local. the fix is in the binary; the `a2a sync` workaround RN-0900-8 described is no longer needed

### The CODEOWNERS placeholder produced a file that gated nothing

**fix · high**

The space template shipped `/space.yaml @REPLACE_WITH_ORG/space-admins`, and the instruction said to replace the `@REPLACE_WITH_ORG` placeholder. So the natural literal edit swaps the org and keeps the team name — and that team does not exist, because nothing creates it. GitHub does not reject an unknown owner; it ignores the line. The result was a CODEOWNERS file that looked like it protected `space.yaml` and protected nothing, with no error anywhere to say so. That matters because code-owner review is the only mechanism behind the manifest gate: an inert file means anyone with write access can edit the document that decides who may write where, unreviewed. Found by rebuilding a real space from zero and following only the published steps. Both halves of the placeholder are now placeholders, so leaving one is visibly unfinished; the template explains why an unknown owner fails silently and recommends individual logins for a small space; and `a2a space init` says the same in its printed steps, because that is what you are reading at the time. **Check your own space** — if your CODEOWNERS names a team, confirm the team exists.

Action scope: space. an existing space may already carry an inert CODEOWNERS — the template fix does not repair yours

### The space validation gate is checked out correctly, and a red gate is no longer called slowness

**fix · normal**

Two test-infrastructure fixes that both hid real defects. The live matrix reported a failing required check as a TIMEOUT, because the scenario waited for a merge that could never happen; a timeout reads as latency rather than as a finding, which is how the v0.9.0 gate defect survived a whole run unnamed. The wait now notices a concluded failure on every poll and reports that instead. Separately, the matrix now also refuses a clean verdict while any workflow run in the space failed for a reason it did not deliberately cause — twice it reported all-green while the space's own Actions tab was red, visible only to whoever read the repository's email.

## v0.9.0 — 2026-07-26

The write path stops trusting itself: a version floor that binds every verb, an MCP session that sees the space instead of its own last write, and an unlocked mutation that no longer compiles

### `min_binary_version` bound `a2a submit` and nothing else — every lifecycle and contract verb ignored it

**fix · high**

A space's `min_binary_version` is the one lever an operator has for keeping a stale binary out. It was disconnected from almost every write. The funnel checks the floor only when a write actually carries it, which is reasonable on its own — but three of six write-building sites never set it, and those three are all 15 lifecycle verbs, `contract publish/deprecate/retire`, and their MCP equivalents. So raising a floor gated `a2a submit` and left every other verb writable by any binary, silently, with no error and no warning. This is not theoretical. The branch name the funnel uses as its idempotency key changed grammar in 0.4.0, and a live space still carries two open pull requests for ONE `contract publish` of one artifact — identical contract body, same transition, two branch shapes — because a pre-0.4.0 binary was allowed to write. Raising the floor is exactly how you prevent that, and it was doing nothing on the verb that caused it. Both surfaces are fixed together: a floor that binds the CLI but not MCP would only move the stale write to the other door. A structural gate now fails the build if a new write is added without one. Know what the floor still does NOT do, because it is easy to over-read. The check runs in the WRITER's own binary; nothing server-side reads the field, and the space's CI does not enforce it. So raising your floor binds every 0.9.0-or-newer writer on every verb, and every version's `a2a submit` — but it cannot retroactively gate an older binary's `contract publish`, because that binary's code never populates the field to check. The way to close that gap is for everyone to be on 0.9.0, not to trust the number in space.yaml.

Action scope: space. the guard is in the binary, but it only bites if your space.yaml declares a floor — check yours

### An `a2a mcp` session no longer reads a space frozen at the moment it started

**fix · high**

Two defects met here, and together they meant a long-running MCP session could validate a transition against a state the space had never agreed to. First, the write funnel checked out an ephemeral branch to commit on and never left it; the only code that moved the mirror back lived inside the clone-or-fetch step. Second, an MCP server ran that step exactly once, at startup, and then served every later tool call from that view — where the CLI re-resolves everything on every invocation. So the second write in a session folded its legality over a working tree standing on the FIRST write's unmerged branch. Both are fixed. The funnel now restores the mirror to its base branch before releasing its lock, so a write leaves the mirror the way it found it; and the MCP session refreshes the mirror before EVERY tool call, in one place rather than in each of ten handlers. That second half also closes the read-staleness gap v0.8.0 documented: an MCP session now sees what your counterparty published after it started. v0.8.0's own release note said the MCP write tools were fine — that sentence was wrong, and this is the correction. One limit remains and is deliberate: if the refresh fails (an unreachable origin), the session logs a warning and serves your last good view rather than refusing to work offline.

Action scope: local. the fix is in the binary; nothing in your space changes

### A mirror mutated without holding its lock is now a compile error rather than a review note

**fix · normal**

The three defects the live matrix found in July — a lost write, a crossed write where one system's pull request carried another system's files, and a branch forked from a stale head — were one shape: a shared mirror mutated by two processes at once. Two of them had been read in the source and judged latent before the matrix found them. Every working-tree mutation now takes the mirror lock as a parameter, so an unlocked mutation cannot be written; a runtime check covers the half a type cannot express, that the lock is for THIS mirror; and a structural scan catches a future mutation added through a fresh helper that takes no lock. No behaviour changes for a correct caller.

### The live-matrix runbook told you to run a path no checkout carries

**fix · low**

The committed runbook's commands named `.agents/scripts/live-e2e/...`, which `.gitignore` excludes entirely — so every reader was pointed at files git does not track, while an untracked local copy drifted from the tracked one with nothing able to notice the difference. One tracked home now, and the documented paths resolve. Also adds `detached.sh`, which runs the matrix detached from whatever launched it: the run takes about an hour, and every attempt driven from an agent session had been killed mid-row — silently, with no verdict — which read as rate limiting and was not.

### A contract could never publish a successor once any version had been deprecated

**fix · high**

`a2a contract deprecate --version 1.0.0` deprecates a VERSION — that is what the flag means and what you intend. Internally the lifecycle recorded that state against the whole CONTRACT, and there was no legal way back to published, so every later publish was refused as an illegal transition. The ordinary life of a live contract is publish 1.0, publish 2.0, deprecate 1.0, publish 3.0 — and it hit that wall on the last step, permanently. The wall only appears once a contract has been in use for a while, which is why nothing found it earlier. Publishing now returns the contract to published; retire still requires a deprecation first, so nothing else moved. This is an interim: the real fix is tracking state per version, so that "1.0.0 is deprecated" and "2.0.0 is published" can both be true at once instead of competing for one state. That needs its own design and a migration, and is filed rather than rushed.

Action scope: local. the fix is in the binary; a contract already stuck at deprecated can publish again as soon as you update

### `a2a mcp` no longer refuses to start when the space is unreachable

**fix · normal**

The server cloned or fetched the space at startup, so a network blip, an expired credential, or a laptop with no connection stopped it from starting at all — even though the read tools need nothing but the local mirror already on disk. You lost your inbox for a reason that had nothing to do with reading it. It now degrades instead: the write tools are not registered, the read tools serve from the local mirror, and the reason is printed so an agent sees why a tool is missing rather than guessing. The CLI's read path has behaved this way for several releases; this brings the second surface in line.

Action scope: local. the fix is in the binary; nothing in your space changes

### Your pull request was refused when your counterparty merged first

**fix · high**

The space's own validation gate blamed the wrong author. On a pull request it checked out the branch ALREADY MERGED into the current base, then diffed that against the base as of when the event fired. Those two drift apart the moment anybody else merges — and the diff then swept in every commit that had landed in between, attributing another system's files to your pull request. The result was a refusal naming paths you never touched, in exactly the situation a shared space exists for: two systems writing at the same time. Observed live on 2026-07-26 — a pull request that changed four files, all in its own section, refused with two authorization violations naming the other system's section, because the other system merged while it was open. The gate now checks out the pull request's own head, so its diff means what the authorization check asks: what did THIS author change, relative to where they forked. Validating the merged result was never the question — the post-merge audit on main is what checks the whole tree. NOTE: your space inherits this only when it re-pins the workflow. Run `a2a space update` after upgrading; `a2a doctor`'s "space scaffolding current" row tells you whether you are behind.

Action scope: space. the fix lives in the reusable workflow your space pins by tag — the space must re-pin to get it

### A just-merged artifact could stay invisible to your reads for five minutes (fixed in 0.9.1)

**known-issue · high**

v0.8.0 made every read verb refresh a stale mirror before reading, which closed the case where a contract published on the other side was silently invisible. It did not close the narrower one, and the narrower one is more likely: "stale" was measured against the STATUSLINE's five-minute window, because both callers shared one setting. So a mirror fetched moments earlier — by your own `a2a submit`, for instance — counted as fresh, and an artifact whose pull request merged just after that fetch stayed invisible to `a2a inbox`, `outbox` and `show` for up to five minutes. No error: `show` answered "artifact not found". Found end to end against a live space on 2026-07-26, and one explicit `a2a sync` revealed the artifact at once. Five minutes is exactly the window in which your counterparty's merge matters most, and the documented session-start loop says an empty inbox means proceed. If you are on 0.9.0 and a read looks emptier than you expect, run `a2a sync` and read again — that is the reliable answer until you upgrade.

Action scope: local. run `a2a sync` before trusting an empty read on 0.9.0; the fix ships in 0.9.1

## v0.8.0 — 2026-07-25

The read path tells you what the other side published, and a green pull request no longer sits forever

### `a2a inbox` reported nothing while the other side had already published

**fix · high**

Every read verb read the local mirror and never fetched, and none of them said a word about how old that mirror was. Put that next to the protocol's own session-start floor — run `a2a inbox`, and if it is empty, proceed — and the result is that a contract your counterparty published after your last `a2a sync` was invisible, while the documented loop told your agent that invisible means nothing to do. No error anywhere, on either side. `inbox`, `outbox`, `show`, `thread`, `search`, `contracts`, `dashboard` and `html` now refresh a stale mirror before reading it: bounded (5s per space, 10s in total), and never fatal — an unreachable origin prints a note and still gives you the local answer, so an offline machine keeps working. `a2a statusline` is deliberately excluded and keeps its existing background refresh: a synchronous fetch does not belong in a shell prompt.

Action scope: local. the fix is in the binary; nothing in your space changes

### A pull request GitHub was ready to merge could sit open forever, while the write reported success

**fix · high**

When a2a opens a pull request it arms auto-merge. GitHub declines to arm it on a request that is ALREADY mergeable — there is nothing to wait for — and a2a treated that refusal as "no repair needed" and reported the write successful. For a person reading a terminal the printed hint said "merge it". For an automated exchange it was a full stop: success reported, the artifact never on the base branch, the counterparty never seeing it. Found on a live space holding three such requests, each with a green required check, the oldest three days old. a2a now lands that request itself — but only after reading its required check and finding it present and explicitly successful. A check that is pending, absent, ambiguous or failing is never merged, and a space that requires review keeps its request blocked rather than mergeable, so this can only ever complete what auto-merge would have completed a moment later.

Action scope: local. the fix is in the binary; nothing in your space changes

### A space created from the template could not merge anything, and nothing told you

**fix · high**

GitHub's `allow_auto_merge` repository setting is off by default on a newly created repository — which is exactly what `a2a space init` produces. The printed setup steps never mentioned it and `a2a doctor` never checked it, so the funnel armed auto-merge on a repository that forbids it and every write stalled behind a request nothing would merge. `a2a doctor` gains a row for it, `a2a space init` names it among the steps you still have to perform, and the template's branch-protection checklist now states the values every operated space actually runs — `required_approving_review_count: 0`, `require_code_owner_reviews: true`, `enforce_admins: false` — each with the reason, because a checklist that gives a value without its reason gets "corrected" by the next reader. Note the doctor row reports UNVERIFIED rather than failing when your credential cannot read repository settings: a fine-grained token without "Repository metadata: read" is a working setup and should not turn `a2a doctor` red.

Action scope: space. the checklist values live in your space's own BRANCH-PROTECTION.md; the doctor row and the init step are in the binary

### `a2a init --space <url>` followed by `a2a connect <url>` left two mirrors, and read verbs used the wrong one

**fix · normal**

`init --space` recorded the space without a mirror location, which always resolves project-locally and ignores a configured `mirror_root` entirely. `connect` then cloned to the mirror_root path but never repaired the recorded entry, so the clone and every later read looked in different directories. `a2a doctor` happened to heal it by cloning into the path the read verbs use, which is why this stayed latent for a month — the residue was an orphaned second clone that even `a2a disconnect` never removed. `connect` now repairs the recorded mirror location, so the sequence the tool's own output recommends leaves exactly one mirror.

Action scope: local. the repair happens on your next `a2a connect`; an orphaned mirror from before can be deleted by hand

### A space id with a hyphen produced a credential variable your shell refuses to set

**fix · normal**

`A2A_TOKEN_<SPACE_ID>` was built by upper-casing the space id, so a space called `my-space` yielded `A2A_TOKEN_MY-SPACE` — which bash and zsh both reject as an invalid identifier. That export is the literal remedy `a2a connect` prints, so the documented fallback was untypeable. Masked for anyone whose `gh` CLI is authenticated, since a2a prefers `gh auth token`, and therefore a dead end specifically for an agent in a container. Hyphens now map to underscores, and the space manifest schema pins the id grammar so the `my-space` / `my_space` collision the mapping could otherwise introduce cannot be constructed.

Action scope: local. no existing space id changes — every id in use already conformed

### A space pinned to v0.7.0 was validating with the v0.5.0 binary

**fix · high**

A space pins the reusable validation workflow by release tag and inherits that workflow's own default for which a2a version to `go run`. That default was left at v0.5.0 when v0.6.x and v0.7.0 shipped, so a space pinned at `@v0.7.0` ran its CI gate with a binary two releases old while believing it ran what it pinned. Every binary-side `validate --ci` improvement since v0.5.0 was therefore absent from that space's gate — most consequentially the computed contract-compatibility check RN-0700-3 added, so a breaking change labelled `minor` would not have been caught at merge there. To be precise about what this was NOT, since an earlier draft of this note overstated it: the v0.6.4 diff-authorization fix is a WORKFLOW-side change (it passes `--author <the pull request's author>` explicitly instead of letting the binary fall back to whoever re-triggered the run), so any space pinning the v0.6.4-or-later workflow carried that fix regardless of which binary the workflow ran. No space kept that bypass open because of this. The default now moves with the release, and `make release-preflight` refuses a cut whose default disagrees with the tag — the skew cannot recur silently.

Action scope: space. your space's CI resolves the validator through the workflow it pins; re-pin to this release

### Two agents writing the same space at the same time could lose one of the writes

**fix · high**

Every project's clone of a space lives in one place — that is what `mirror_root` is for — and a2a refreshes it before a write by moving its working tree onto the freshly fetched head. Nothing serialised that between processes, so two a2a invocations against the same space could collide on git's own lock file and one of them died outright with `fatal: Unable to create ... .git/index.lock: File exists`. The write it was carrying was simply lost. Two agents are the normal case for this product, and a shell prompt's statusline refresh racing a foreground write is enough to trigger it on one machine. a2a now waits for git's lock instead of treating it as fatal: a bounded retry (jittered, so concurrent waiters do not wake in lockstep) covering all four lock files these operations take, then a typed refusal naming the contention if the window expires. Found by the live matrix's own concurrent-writes row, not in review — a concurrency defect is the kind a real run settles and reading does not.

Action scope: local. the fix is in the binary; nothing in your space changes

### `make check` went red at random, and the installer's shell hook had no regression net

**fix · normal**

Two developer-facing surfaces, both guarded by nothing. Git spawns background maintenance inside test fixtures and outlived the temporary directories being cleaned up, so the repository's own gate failed for no reason — and a gate that fails at random teaches you to re-run it instead of read it. Every fixture git invocation now routes through one owner that disables that maintenance, the test binaries harden their environment so a git process started by production code under test is covered too, and a structural test fails per site if a new invocation skips it. Separately, `install.sh`'s shell-integration hook — four shell branches, hand-verified once — is now driven by a committed test per shell, including the idempotency case.

Action scope: local. repository-internal quality work; nothing to do

### Use the CLI as your working surface. The MCP tools are a typed façade that is behind on two things

**known-issue · high**

`a2a mcp` exposes the same core through typed tools, and for drafting and writing it is equivalent to the CLI — that equivalence is gated per verb. Two gaps mean it should not be your primary surface yet, and an agent that knows about them will not be surprised by them. FIRST, and this is the one that matters: the MCP read tools do NOT refresh a stale mirror. RN-0800-1 fixed that for the CLI's read verbs, but an MCP session builds its view once at startup and keeps it, so a long-running session will not see what your counterparty published after it started — the same silence RN-0800-1 describes, on the surface built for agents. Read through the CLI (`a2a inbox`, `a2a show`), or run `a2a sync` and restart the MCP session, until this is fixed. SECOND: `a2a_contract publish` does not run the client-side compatibility check the CLI's `a2a contract publish` runs. Nothing incorrect merges — the space's own CI performs the same check on the pull request — but you learn about a refusal one round trip later, at the PR instead of at the command. Neither gap loses data or lets an invalid artifact land. Both are about where and when you find out.

### The fleet write floor stays at 0.7.0

**policy · normal**

Deliberately not raised. Nothing in this release is a write-path guard a pre-0.8.0 binary could bypass: every fix here either corrects what a verb reports to its own operator or completes a write that was already correctly validated. Raising the floor would refuse writes from a colleague who has not updated yet, in exchange for nothing — so it holds at 0.7.0, and you can update the two ends of an exchange independently.

Action scope: local. no space.yaml change is required by this release

## v0.7.0 — 2026-07-25

Contract compatibility is computed on the write path — and four verbs that could not complete at all now can

### `a2a respond` could not complete at all, and thirteen verbs rejected a flag written after their positional argument

**fix · high**

Two independent breakages that made ordinary invocations impossible, both green on every hermetic tier and both found by driving the full live matrix against real GitHub for the first time. `a2a respond` wrote three of its template's placeholders verbatim — including the response's own `to:` — and the write funnel then refused the artifact the command had just produced, so the verb never finished for anyone. Separately, Go's `flag` package stops parsing at the first non-flag token, so thirteen verbs silently ignored (or errored on) a flag written after their id: `a2a ack EX-… --actor-name me` did not do what it reads like. Flags are now parsed on both sides of the positional argument for every verb that takes one.

Action scope: local. the fix is in the binary; nothing in your space changes

### `a2a ack` on an announcement was refused unconditionally, leaving `contract retire` satisfiable only by --override

**fix · high**

`fold.CheckLegality` never implemented the D-025 broadcast exemption that the fold's own `applyBroadcastAck` already had, so a consumer could not acknowledge an announcement addressed to it — the pre-write legality check refused every attempt. Because `contract retire` requires the registered consumers to have acked the deprecation, the only way to retire a contract was `--override`, which is exactly the guard the precondition exists to provide. Acking a broadcast now goes through the normal path.

Action scope: local. the legality check runs in the binary before the write

### A contract's declared version bump is now COMPUTED against the previous schema, at both layers, off one core

**feat · high**

§5.4b's compatibility rule existed only as an opinion inside an e2e test file: a producer could publish a breaking change labeled `minor` and both the client and the space CI would accept it. The rule now runs on the write path — `a2a contract publish` refuses a mislabeled bump (POL-007), naming the schema field and the fixture that prove it — and again at merge inside `a2a validate --ci`, so a hand-crafted PR cannot bypass the client. Both layers call ONE exported core in `internal/validate`; the previous two independent implementations had provably diverged. What is computed is schema SHAPE only — semantic compatibility is still the author's claim.

Action scope: space. the merge-time half of the check only reaches your space once its reusable-workflow ref points at 0.7.0 or later

### A JSON-Schema contract must publish a schema and a valid fixture (POL-009), and `contract publish` now carries them

**policy · high**

The compatibility check has nothing to compute against unless the schema it names is actually in the space, and no contract in existence shipped one. POL-009 makes the baseline a publication requirement: `a2a contract new` scaffolds the schema and a fixture that validates against it, and `submit` carries both through the funnel. `contract publish` also used to read the mirror tree its own invocation had just hard-reset, so the compatibility check compared the new version against itself and always found full compatibility; the staged schema now travels in the same commit as the version bump.

Action scope: local. a contract published before 0.7.0 has no baseline schema — the next publish will ask for one

### A deprecation now addresses exactly the consumers who can block its retire

**feat · normal**

`contract adopt` registered a consumer in `consumes.yaml` but never made that consumer an addressee, so a registered system blocked the producer's `retire` while never being told the contract was going away — a deadlock constructible with two ordinary commands. The deprecation's `to:` is now the same consumer-registry query that gates retire, so "who blocks retire" and "who was told" are one question. `deprecate` and `retire` also refuse an omitted `--version` once more than one version is published, rather than guessing, and `contract diff` now reports a changed `compat_policy`.

### An empty `actor.name` is rejected at validation, and the CLI stops minting anonymous writes

**schema · normal**

Every event and artifact a2a minted recorded `actor: {kind: agent, name: \"\"}` by default, and both schemas accepted it — listing a field in `required` guarantees presence, not content. An exchange log whose permanent, shared entries do not say who acted is a log you cannot answer a question with. `actor.name` now carries `minLength: 1` in both `event/v1` and `envelope/v1`, and `ResolveActor` falls back to the OS user so ordinary CLI writes carry a real name. NB this reds any existing artifact with an empty name — check your space's corpus. A hard refusal for environments where no OS user exists (CI, containers) is still an open decision, not shipped here.

Action scope: local. set A2A_ACTOR_NAME (or pass --actor-name) anywhere `os/user` is unavailable, or the write will fail validation

### A retried submit could report success over a PR that never merges, and GraphQL failures were swallowed silently

**fix · normal**

Four host-layer defects found by the live matrix. A retried `submit` re-used an existing branch and reported success even when the PR behind it was in a state that could never merge. Every GraphQL failure — including the auto-merge arming that decides whether a green PR ever lands — was discarded without a word; they are surfaced now, and surfacing them does not turn `submit` into a hard failure. `CheckStatus` could answer from a stale re-triggered run and now reports which ref the check actually ran against. And a merged PR is still recognised as the PR the write opened.

Action scope: local. host-layer behaviour, no space change

### Onboarding papercuts: a lying `a2a new`, a wrong credential hint, an id the validator rejected, a mis-ranked statusline

**fix · normal**

`a2a new` left the space unfilled while `validate`/`submit` reported otherwise; `a2a init` printed a credential instruction that was wrong in two ways and now defers it to `connect`; `a2a contract new` accepts `--slug`, matching `a2a new contract`; a2a could mint an id its own validator then rejected; the contract sub-verbs accept the argument order their own `--help` prints; the statusline ranked a p3 artifact as p1 and hid the rest; and the shipped command catalog advertised seven commands that do not exist — which every agent reading it took at face value.

### `a2a space update` says up front when it cannot verify the token's workflow scope

**fix · normal**

`space update` writes `.github/workflows/`, which GitHub refuses from a token without `workflow` scope — a scope `gh auth token` does not carry by default. The refusal landed AFTER the full diff and both `scope: space` directives had been printed, so it read like the command had worked and then died. It now reports the missing or unverifiable scope before printing a plan it cannot execute.

Action scope: local. if it reports a missing scope: gh auth refresh -h github.com -s workflow

### The fleet write floor rises to 0.7.0

**policy · high**

`min_binary_version` moves from 0.6.4 to 0.7.0. The reason is RN-0700-3: the client-side half of the compatibility check is a write-path guard, and a pre-0.7.0 binary bypasses it entirely — it can publish a breaking change labeled `minor` into a space whose CI has not yet been updated to catch it at merge. Once a space takes this floor its funnel refuses writes from any older binary, so update before (or with) `a2a space update`.

Action scope: space. the floor only reaches a space when its space.yaml is updated; every participant must be on 0.7.0 first

### The seeded skill gains its consumer-side loop

**feat · normal**

The embedded a2ahub manual described how to PRODUCE a contract and said nothing about what a consumer does when one changes underneath it. §8.4a now carries that loop, and states the limits as plainly as the guarantees: unregistered consumption is invisible by design, a plain version bump owes you no notice, and what the compatibility check computes is schema shape only. Every verb and error code the prose cites is gated against the binary, so the manual cannot drift from the surface it documents. `a2a update` refreshes the installed skill, so this reaches every wired agent surface automatically.

Action scope: local. a2a update refreshes the installed skill; re-run `a2a skill install` if you vendored it by hand

## v0.6.4 — 2026-07-24

SECURITY: diff-authz checked the wrong person — the one who re-ran CI, not the PR author

### V3 diff-authz resolves the PR author from the pull request, not from GITHUB_ACTOR

**fix · high**

The space CI passed no author to `a2a validate --ci`, so the validator fell back to `GITHUB_ACTOR` — the account that TRIGGERED the workflow run, which is not the PR's author. diff-authz asks "is every changed path inside the author's own section", so the verdict depended on who pressed the button. Two consequences, one of them serious: a maintainer re-running a contributor's PR saw it checked against the MAINTAINER's section and go red on a legal PR; and — the direction that matters — a PR from system B illegally writing into system A's section PASSED whenever anyone from A re-triggered it, defeating the single-writer boundary V3 exists to enforce, via a request as innocuous as "could you re-run CI?". The reusable workflow now reads `github.event.pull_request.user.login` directly; GITHUB_ACTOR remains only as the fallback for non-PR contexts. Found while performing a real space migration.

Action scope: space. your space CI keeps checking the wrong identity until its reusable-workflow ref points at 0.6.4 or later

## v0.6.3 — 2026-07-24

You learn about a missing `workflow` scope from doctor, not from a git push rejection

### a2a space update checks it CAN write workflows before printing a plan it cannot execute

**fix · normal**

`space update` rewrites `.github/workflows/`, which GitHub refuses from a token without the `workflow` scope — and `gh auth token` does not carry it by default. Until now the command computed the whole diff, printed it with both `scope: space` directives, and only then died on a raw git push rejection: it read as "it worked, then broke". It now probes the credential first and refuses up front, naming the scopes your token actually reports and the exact remedy. `--dry-run` still shows the diff (that is what it is for) but says the real run would be refused, so you learn this before it matters.

### a2a doctor reports a missing `workflow` scope as an advisory, not a failure

**feat · low**

Doctor's credentials check now notes when a space's token lacks the `workflow` scope, and is explicit that this is FINE for participating: submit, the lifecycle verbs and the contract verbs are confined to your own section by the write funnel and never touch `.github/`. Only a space OWNER running `a2a space update` needs it. A fine-grained PAT or a GitHub App token does not advertise scopes at all, and that silence is deliberately reported as nothing — an advisory that fired on the most narrowly scoped credentials would just train people to ignore it.

Action scope: local. run doctor once if you own a space, to see whether you can migrate its scaffolding

## v0.6.2 — 2026-07-24

The fleet write floor moves to 0.6.2 — and a gate now stops it running ahead of the release

### space-template's write floor raised to 0.6.2

**feat · high**

`min_binary_version` in the space template is the fleet WRITE FLOOR: the funnel refuses a write from any binary older than it (CC-085). It sat at 0.4.0, which admitted binaries that predate `a2a space update`, `a2a space init` and the compound required-check fix. Raised to 0.6.2, so every space that takes an update requires a binary that actually understands the current space contract. `a2a space update` propagates it — and only upward, never lowering a space that already pins higher.

Action scope: space. every participant in a space you own must be on 0.6.2+ before the floor lands, or their writes will be refused

### release-preflight refuses to cut a release whose template floor is ahead of it

**fix · normal**

Nothing checked that the template's declared `min_binary_version` was reachable. A floor ahead of the newest release would propagate to every space through `a2a space update` and then refuse writes from every binary in existence — including the one needed to fix it. `make release-preflight VERSION=…` now asserts the floor is not ahead of the version being cut, and that the template declares one at all, with offline teeth covering the lockout case, the equal case, numeric ordering (0.9.0 < 0.10.0) and a missing field.

## v0.6.1 — 2026-07-24

space update no longer adds a second CODEOWNERS beside your real one

### a2a space update respects a CODEOWNERS kept at .github/ or docs/

**fix · high**

The template ships `CODEOWNERS` at the repo root, and 0.6.0's `space update` asked only "does that exact path exist?". A space that keeps its real owners at `.github/CODEOWNERS` — where GitHub looks FIRST — answered no, so the update proposed adding a root `CODEOWNERS` full of `@REPLACE_WITH_ORG/...` placeholders beside the real file. Harmless on day one (GitHub prefers the `.github/` copy) but it is junk in the repo root, an error in GitHub's CODEOWNERS UI, and a gates-nothing takeover the day the real file moves. A seeded file is now matched by its ROLE across GitHub's own resolution order (`.github/`, root, `docs/`); the location difference is reported as advisory drift instead of written. Found by the first dry-run against a real space.

Action scope: local. if you ran `a2a space update` on 0.6.0 and it offered to add a root CODEOWNERS, drop that file from the PR — or re-run the dry-run on 0.6.1

## v0.6.0 — 2026-07-24

Spaces are self-service: scaffold one, and keep an existing one current, with a command

### a2a space init — scaffold a ready-to-push space tree from the embedded template

**feat · normal**

`a2a space init <space-id> [--dir <path>]` writes a complete space repo tree from the template embedded in your binary: the CI caller pinned to THIS binary's release, dependabot.yml, space.yaml, CODEOWNERS and the branch-protection checklist. Creating a space no longer means copying files out of a2ahub by hand and filling placeholders. Two residual manual steps remain and are printed: replace the CODEOWNERS placeholders with real teams, and arm branch protection with the required check `a2a-validate / validate`. A dev build refuses to scaffold — it would pin a release tag that does not exist.

Action scope: local. use it instead of hand-copying the template when you create your next space

### a2a space update — template drift becomes a command, not a runbook

**feat · high**

Dependabot moves your space CI's version ref, but it cannot add a file, rename a job, or change the caller's shape — so every STRUCTURAL template change used to be a manual per-space copy job. `a2a space update [--dry-run]` diffs your connected space against the embedded template and opens ONE reviewable PR through the normal write funnel. It never overwrites what the space owns: your real CODEOWNERS teams and your space.yaml participants survive verbatim, and `min_binary_version` moves only as far as the template's own declared floor, and only upward. Steps that need repo-admin scope — renaming the required status check, deleting a stale A2A_BINARY_FETCH_TOKEN secret — are PRINTED as directives for you to run; a2a asks for no admin credential and calls no admin API.

Action scope: space. if you own a space created before the reusable-workflow CI, this is now the supported way to migrate it

### the V3 gate result resolves on a migrated space (compound required-check context)

**fix · high**

Since the space CI became a caller of a2ahub's reusable workflow, GitHub names the check run `a2a-validate / validate` rather than the flat `a2a-validate`. The host's check-run query filtered on the flat name exactly, so on a migrated space it matched nothing and reported "no check" instead of the real conclusion. It now lists the head commit's runs and selects by anchored prefix, so BOTH shapes resolve — a fleet mid-migration needs no per-space configuration — while the push-triggered `a2a-postmerge-audit / validate` can never be mistaken for the gate. Found before any space was migrated, so nothing broke in the field.

## v0.5.0 — 2026-07-24

Space CI is a referenced reusable workflow — no copied logic, no fetch-token, version by Dependabot

### space CI validates through a2ahub's reusable workflow — copy/token/manual-pin drift is gone

**feat · normal**

A space's `.github/workflows/a2a-validate.yml` is now a thin CALLER of a2ahub's public reusable workflow (`a2a-validate-reusable.yml`), pinned to an immutable release tag and bumped by Dependabot. The validation logic + the pinned a2a version live in that one versioned unit, so a space can no longer drift a flag surface past its pinned binary (the outage that froze pinned-binary spaces). The `A2A_BINARY_FETCH_TOKEN` secret is gone — a2ahub is public and integrity is the Go checksum DB via `go run …@<ver>`. The required status check is `a2a-validate / validate` (caller job / reusable job). a2ahub dogfoods the same workflow on its own PRs.

Action scope: space. if you own a pre-existing space, migrate its CI to the reusable caller — one-time, and it ends the flag-drift outages

## v0.4.0 — 2026-07-23

Second writes on one artifact no longer vanish; other systems' work is finally visible; the skill is discoverable

### a write's branch names the write, not the artifact — a second transition no longer silently drops

**fix · high**

Through 0.3.0 the write funnel keyed its branch on the ARTIFACT, so once any write by your system on an artifact had merged, every LATER write by your system on that same artifact short-circuited with exit 0 and wrote nothing. `ack` then `accept` lost the accept; a contract's publish, deprecate and retire all collapsed into its submit. Fixed: the branch now names the verb too, so each transition is its own write.

Action scope: space. any second transition on one artifact you believe you recorded on 0.2.0/0.3.0 may not actually exist in the space

### a2a sync moves the mirror's working tree — other systems' published work is now visible

**fix · high**

Through 0.3.0 `a2a sync` fetched refs but never moved the mirror's working tree, and every read walks the tree — so nothing another system published after your clone was ever visible, and the fail-closed retire guard was reading stale data. Fixed: sync now checks out the fetched head.

Action scope: local. re-run sync once on 0.4.0 to finally see everything other systems published since your clone

### contract deprecate authors a schema-valid announcement — retire is reachable

**fix · normal**

Through 0.3.0 `contract deprecate` left the announcement template's space/to/title placeholders unfilled, so V2 refused it every time and retire (which needs a prior deprecate) was unreachable. Fixed on both the CLI and MCP surfaces — the end-of-life path works.

### the a2ahub skill is discoverable by your agent's runtime (a2a skill link)

**feat · normal**

`a2a skill install` writes the operating manual into .a2ahub/skill/, but your agent's runtime reads it from its own home (Claude Code from .claude/skills/ + CLAUDE.md, Codex from .codex/skills/ + AGENTS.md). `a2a skill link` now installs a discovery entry (a symlink, or a stub) pointing at the one installed tree, and `a2a update` refreshes it — so the manual your agent reads is never older than your binary.

Action scope: local. link the installed manual into your agent's skills home so it is actually read

### a2a whatsnew — the release directives you are reading right now

**feat · low**

An authored, version-keyed, embedded release-notes corpus surfaced as `a2a whatsnew [--since <v>] [--json]` (and an MCP twin), rendered after every `a2a update`. a2a informs with structured directives; your agent decides and runs them. A `scope: space` directive is emitted as a command for you to run through the funnel — a2a never runs it for you.

## v0.3.0 — 2026-07-23

Contracts and decisions are publishable again; feedback works without write access

### the contract/decision family is publishable again (placement-aware id guard)

**fix · high**

Before 0.3.0 a one-shape id guard (CC-003) reddened EVERY contract with REF-001 and EVERY decision with REF-002 — the whole family was unpublishable, with no workaround (a hand PR reddened identically in V3). The guard is now placement-aware, so contracts and space-level decisions validate and publish.

Action scope: space. anything you tried to publish before and could not can now be re-submitted

### a2a feedback submit works without write access (fork flow)

**feat · high**

The verb the skill tells every consumer agent to use pushed straight into the product repo, so a submitter without write access got a 403 and no PR. It now falls back to a fork + pull request automatically (ADR-003 optional host.Forker capability), so a non-collaborator's report goes through the normal verb instead of out-of-band.

### a2a contract adopt writes consumes.yaml; the consumer registry fails closed at retire

**feat · normal**

`a2a contract adopt <XC-id>` is the consumes.yaml writer (schema consumes/v1), and `contract retire` reads that registry fail-closed — a registry it cannot read counts as a blocking consumer, not zero, so a contract can no longer be retired out from under a subscribed system.

Action scope: space. your space's consumes.yaml files must be schema-shaped or V3 reds them

### credential precedence honoured; connect repairs a stale space id; --help needs no config

**fix · normal**

A2A_TOKEN_<SPACE_ID> now resolves everywhere; `a2a connect` repairs a stale space id and `a2a doctor` gained a space-identity check (a wrong id no longer survives to the first submit); every verb's `--help` answers with no project config or credential.

Action scope: local. re-run doctor to catch a stale space id before your next write

## v0.2.0 — 2026-07-23

The first version an external system tested against

### the v1-min core — schemas, one validation engine, lifecycle verbs, submit/sync/inbox, contract ops

**feat · normal**

Baseline. The `a2a` binary that a second system first pointed at a live space: the typed artifact model, the single validation engine (V1/V2/V3), the fold/lifecycle transitions, submit/sync/inbox/outbox, the contract family, templates, and the feedback intake path.
