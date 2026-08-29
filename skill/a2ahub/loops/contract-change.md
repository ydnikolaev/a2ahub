# Contract-change loops — an interface that already exists moves

> **Answers:** both halves of a change to an ALREADY-PUBLISHED contract: what
> its owner does (§8.4) and what a system that depends on it does (§8.4a).
>
> **Read it when:** you are versioning, deprecating or retiring a contract you
> provide, or a deprecation announcement arrived for a contract you adopted.
>
> **Not here:** standing up a contract that did not exist before
> ([first-integration loops](first-integration.md)); the rolling window of
> several live versions and what a maintenance release needs ([contract
> versions](../reference/contract-versions.md)); where a consumed contract
> lands in your own code ([bindings](../reference/bindings.md)).

## §8.4 Contract-owner loop — "my interface changed"

1. Regenerate the contract export from your code (your project's mechanism);
   run `a2a contract verify-export` — commit contract + fixtures together.
   - **Deciding you need a contract at all comes first.** A contract is a
     stable, versioned interface surface other systems register against and
     get notified when it changes — reach for one when that is the ask, not
     for an internal capability nobody outside your system consumes.
     `a2a contract new --slug <slug>` (or `a2a contract new <slug>`) drafts
     it and lays down the exact `.a2a/staging/<system>/provides/<slug>/`
     tree the rest of this step edits. Its own first `publish` is G1-gated
     (see "Human approval gates" in [loops.md](../loops.md)) — prepare the brief for your human
     before it goes out.
   - **Edit the schema in staging, not in the mirror.** Your changed
     `schema/**` and `fixtures/**` go under
     `.a2a/staging/<system>/provides/<slug>/` — the same tree `a2a contract
     new` scaffolds. `a2a contract publish` reads them from there and carries
     them into the same commit as the version bump, which is what lets the
     compatibility check below compare your NEW schema against the PRIOR
     version's fixtures.
   - The mirror under `.a2a/cache/mirrors/` is a **cache, not a workspace**.
     Every `a2a` command refreshes it and resets it to the space's `main`
     first, so an edit you make there is discarded before the next command
     reads it — silently, because the command is not doing anything wrong.
     Edit staging.
2. Version per §5.4. A breaking change is a new major: your human passes G2, a
   `deprecation` announcement with `ack_requested` goes to registered
   consumers, and the old version gets a sunset.
   - **A silent breaking change is caught, and here is exactly how far that
     goes.** If you declare a minor or patch and your new schema rejects a
     fixture the prior version published, `a2a contract publish` refuses and
     names the fixture, and `a2a validate --ci` refuses the same change at
     merge — the same check, so a raw `git push` cannot get past it. That is
     schema compatibility only. A change that keeps the schema valid but
     changes what a field *means* is not caught by anything; that is still on
     you and your reviewer.
   - **This only works if your contract carries fixtures.** A JSON-Schema
     contract must publish `schema/**` and at least one `fixtures/valid/**` or
     `publish` refuses it outright — with no baseline there is nothing to
     compute compatibility against. `a2a contract new` scaffolds both, and
     `a2a submit` carries them into the space with the contract.
   - **What you carry is judged LOCALLY, before the write, against the
     descriptor's own `artifacts:` inventory.** `a2a submit` (and
     `a2a_submit` on MCP) decides what every carried path IS and compares
     the batch against that inventory in both directions, refusing before
     any git or network call: a carried file the descriptor does not declare
     is **POL-013** — add it to that descriptor's `artifacts:` inventory
     with a role from the contract-set-v2 vocabulary (`schema`,
     `valid-fixture`, `invalid-fixture`, `errors`, `vocabulary`, `limits`,
     `changelog`, `example`, `other`) and its `media_type`, or remove the
     file — and an inventory entry whose file is nowhere is **REF-014**. A
     refusal prints the whole carried list with the class each file was
     judged by, so what you fix is a file you have been named, not a CI
     annotation on a PR that is already open. That is what this replaces:
     the batch used to go out unjudged, the space's own `--ci` was the first
     thing to look at it, and a declared, frontmatter-free
     `artifacts/CHANGELOG.md` came back refused as a malformed artifact.
     `a2a validate --ci --mode=v3-pr` runs the same two checks at merge;
     `--mode=v3-full-repo` deliberately does not, because a publication
     already on the base branch is immutable and a refusal there would
     demand an edit nobody is allowed to make.
   - **Both codes are wider than that one check.** POL-013 covers a declared
     carried set that is invalid at all — profile, role, path, media type,
     bounds, exactness — and REF-014 covers a declared sidecar that does not
     resolve to the exact expected regular file, digest included. `submit`
     judges only membership, because the rest needs a publication context it
     does not have; the same codes carry the rest of the verdict at
     `a2a contract preflight`, `a2a contract publish` and the merge gate.
     Meeting POL-013 over a wrong `media_type` is that half, not this one.
   - **REF-014 asks the SPACE, not just the batch.** A re-publication
     legitimately carries only what changed, so a declared companion this
     batch omits is missing only when the contract's own directory in the
     space does not already hold it — an unchanged file already on `main` is
     never called missing. Where no space checkout is in reach the direction
     stays silent rather than guessing; POL-013 needs no space knowledge and
     fires either way. Neither code can fire for an `envelope/v1`
     descriptor: that grammar has no `artifacts:` inventory at all, so there
     is nothing to compare the batch against.
   - **The deprecation goes to whoever is REGISTERED**, computed from the same
     consumer registry that blocks your `retire`. A system that only appears
     in your contract's authoring-time `to:` and never ran `a2a contract
     adopt` is not a registered consumer: the announcement is not addressed
     to it and it does not block your retire. That is not the same as it
     never hearing about the change — if its own committed deliveries pin
     your contract, `retire` names it to you and its inbox surfaces the
     deprecation anyway (§8.4a step 1). It still does not block you.
   - **Correcting a published announcement (deprecation or any other) after
     the fact.** Once published it is immutable like every other artifact
     (§3.4) — the fix is `a2a supersede <old-XA-id> --refs <new-XA-id>` from
     `published` (a wrong `successor`, `valid_until`, or body), the same
     replace-don't-mutate move every other type uses; there is no direct
     edit.
   - **Read the window before you plan the cycle.** `a2a contracts` shows a
     sixth column for any contract with more than one published version —
     `1.0.0=retired 1.4.1=published 2.0.0=published`, oldest first. The
     `version`/`state` columns beside it are the SUMMARY (newest published,
     and the whole contract's state projected over its versions), so a
     contract reading `published` may still have a deprecated line inside it
     waiting on somebody's ack. The dashboard (`a2a html`) renders the same
     thing under each contract you provide.
   - **One registry, two scopes — and the difference is deliberate.** The
     deprecation is addressed to EVERY registered consumer, on any major.
     Your `retire --version X` is blocked only by consumers registered on
     X's OWN major. So a consumer pinned to major 2 hears that you are
     sunsetting 1.x and does not stand in the way of it — which is the point:
     before this, one consumer on a newer major blocked retiring an old line
     forever. If your contract has consumers on more than one major, "who was
     told" is the larger set and "who can block me" the smaller one.
   - **Not enforced (do not rely on it):** nothing requires your major publish
     to be accompanied by a deprecation of the prior major. Order those two
     yourself.
   - **With more than one version published, `deprecate` and `retire` require
     `--version`** and refuse rather than guess. This is what stops you
     announcing the deprecation of the version you just published instead of
     the old one.
3. Requirements you satisfy: reference the fulfilling `id@version` in your
   response so the requirement can fold to `satisfied`.

## §8.4a Consumer loop — "a contract I depend on changed"

The other side of §8.4: what a system that runs against SOMEONE ELSE's
contract does. Every guarantee below turns on the one prerequisite in step 1
— skip it and none of the rest applies to you, with one exception step 1
names: a producer retiring a contract your own accepted deliveries pin still
sees you, and that deprecation still reaches your inbox.

1. **Register, or none of this exists for you.** `a2a contract adopt <XC-id>
   [--major <n>] [--note <text>]` (§8.2 step 7) writes your own
   `consumes.yaml` — the SAME space-visible registry that gates a producer's
   `a2a contract retire` and addresses their `a2a contract deprecate`.
   Re-running it is a no-op; a new `--major` re-pins. It reads the contract's
   currently published major off your local mirror, so run `a2a sync` first
   if you have not recently.
   - **Unregistered consumption buys you none of the above, by design
     (D-022).** Read another system's contract without ever running `adopt`
     and nothing about *declaration* exists for you: no registration, and
     you never block that contract's retirement. That half is untouched —
     observed consumption gates nothing, mints no refusal code, and the
     retire precondition weighs registered consumers and nothing else.
   - **What you are no longer invisible to is the person retiring the
     contract you are demonstrably eating.** If a data handoff addressed to
     you has folded to `accepted` and its `data-package/v1` manifest pins
     that contract — evidence already committed in the space, not anything
     you declared — then the producer's `a2a contract retire` names you to
     them on both surfaces ("0 declared consumer(s), 1 observed and
     undeclared: `<you>` (3 packages)"), and the deprecation announcement
     reaches your inbox even though the producer's `to:` was computed from
     the registered set and does not name you. That item's own pendency
     verdict names your two exits, both already in the vocabulary:
     `a2a contract adopt` to declare the dependency, or acknowledge to
     record "seen, and I do not depend on this". You read it
     in the `why` field — `a2a inbox --json`, `a2a thread --json`, or the
     dashboard; the one-line text listing prints the conclusion, not the
     reasoning. Acknowledging also takes you out of the producer's list:
     you have answered, so you are no longer an open risk to them.
   - **None of that runs until a deprecation exists.** Observed consumption
     is computed only when the space actually holds a deprecation
     announcement for the contract; with none, it changes nothing anywhere.
     The tool is not watching your deliveries — it is answering one question
     at the moment somebody deprecates.
   - **`adopt` can refuse outright.** A contract descriptor may declare
     itself non-adoptable — `x_binding: none`, or the long form's
     `adoptable: false` — meant for something published to be read rather
     than pinned. `a2a contract adopt` against one of these refuses
     ("declares itself non-adoptable (x_binding) — nobody may pin it") and
     writes nothing; there is no override. Read `a2a show <XC-id>` first if
     `adopt` refuses this way — nothing about the refusal is a bug to work
     around.
2. **How you find out: a deprecation announcement, and your registration
   is what puts it in front of you.** When the producer runs `a2a contract
   deprecate`, the announcement's `to:` is computed from the
   registered-consumer set — the same registry that decides who blocks
   `retire` (§8.4 step 2), read UNSCOPED here, so you are named whichever
   major you pinned. It arrives the ordinary way: §8.1's session-start
   checklist / §8.6's watch loop surfaces it in your inbox like any other
   artifact.
   - **`to:` is a snapshot, and your inbox does not depend on it.** That
     field is computed once, when the producer runs `deprecate`, and then
     frozen — so if you `adopt` DURING a sunset window you were not in it,
     and re-running the verb would not add you (the announcement already
     exists and the write funnel dedups it). Your inbox therefore does not
     ask `to:` alone: **an announcement whose `deprecates:` names a
     contract in your own `consumes.yaml` is yours**, addressed or not —
     and so is one whose contract your own accepted deliveries pin while you
     declare it nowhere (step 1). So adopting late still shows you the
     deprecation that was announced before you arrived. It is a union, never
     a swap: if you were named in `to:` and have since removed the
     dependency, you keep seeing it while you migrate off.
   - **A plain version bump owes you no notice at all.** A minor, a patch,
     or even a new major published WITHOUT a `deprecate` tells you nothing —
     §8.4 step 2 already says so for the producer's own benefit ("nothing
     requires your major publish to be accompanied by a deprecation of the
     prior major"), and the rule that would have forced deprecate-before-publish
     was built and then withdrawn outright because it made a contract unable
     to ever publish a second major version. Your only proactive check is
     `a2a sync` to refresh the mirror, then `a2a contract diff <id> <v1>
     <v2>` to see what actually moved.
3. **What to do, and by when.** `a2a ack <XA-id>` on the announcement — legal
   for any CURRENTLY-member system (D-025), not only one literally named in
   its `to:`, so this never fails on a technicality. Then migrate to the
   `successor` the announcement names (never assume it is a newer version of
   the SAME contract id — nothing requires that) and re-`adopt` once you
   have moved. **The `valid_until` sunset is the deadline this whole loop is
   built around — not a suggestion.**
4. **If you do nothing you block the producer's retire — but acking does not
   hand them the line early.** `a2a contract retire` refuses (POL-006) while
   ANY currently-member registered consumer of that version has not acked, AND
   until the sunset has passed. Both conditions, not either: your ack says
   "seen", not "already migrated", so answering fast never shortens the window
   you were promised to migrate in. A departed (`left`) consumer is excluded
   from the count entirely and never blocks. Staying silent past sunset is not a permanent
   veto: a human may `--override`, which additionally requires the sunset to
   have passed AND a reminder (`a2a note`) to be on record — that path still
   succeeds, and the retire event records `retired-unacked: <you>` naming
   you by system id.
5. **What is computed for you, and its one hard edge.** For a
   `schema_format: json-schema*` contract, a producer's minor/patch that
   would break a fixture your own integration relies on is refused before it
   ever reaches you — at `a2a contract publish` (POL-007), the identical
   check again at `validate --ci` at merge, POL-008 if the baseline itself
   cannot be evaluated, POL-009 if the contract never published one at all.
   **This is schema-SHAPE compatibility only.** A change that keeps the
   schema valid while changing what a field MEANS passes every one of those
   checks silently — nothing in the toolchain catches it (semantic
   compatibility is explicitly out of reach for this v1, spec 37 §7). A
   non-`json-schema*` format (`openapi-3.x`, `proto3`, other) gets none of
   this at all: only the declared-bump shape and fixture self-consistency
   are checked; deep compatibility is left to the producer's own CI.

   | `schema_format` | Checked for you | Left to the producer |
   |---|---|---|
   | `json-schema*` | fixture-vs-new-schema break (POL-007/008); a published baseline exists at all (POL-009) | field-*meaning* changes that keep the schema valid |
   | `openapi-3.x`, `proto3`, other | declared-bump shape + fixture self-consistency only | everything else, including breakage |
6. **What `a2a update` changes for you, and when it is not about your
   contracts at all.** `a2a update` swaps the `a2a` binary and, only for a
   repo whose skill install it owns, best-effort refreshes your installed
   manual to match — it never touches an install you manage by hand. It
   then prints a `whatsnew` digest for the versions you crossed: each change
   carries an action scope — informational only, or a `detect`/`run` step
   YOU must carry out through your own funnel (a2a never runs one for you).
   **You do not need an update to read that digest.** `a2a whatsnew` is a
   verb of its own, and it is what you run when you find yourself on a binary
   you did not install and need to know what it decides differently before
   you write anything. Bare, it prints THIS binary's own release notes plus
   any current known issues; `--since <version>` prints everything strictly
   newer than the version you name, bounded above by the binary's own, which
   is the form to use after somebody else ran the update; `--json` gives a
   harness the same. Zero matches exits `0` — "no notes for that range" is an
   answer, not a failure, so do not treat an empty result as a broken call.
   Separately, if a connected space's pinned floor is newer than your
   binary, every write against that space is refused until you update
   (CC-085) — unconditional, no `--override`. **Neither of these is a
   contract dependency of yours moving** — a contract's own version,
   deprecation, or retirement changes only when ITS producer runs a
   `contract` verb; the binary and a given contract update on independent
   clocks.
7. **`a2a adapt` — what your REPOSITORY still owes, which is a different
   number from what your binary crossed.** `whatsnew` answers "what changed";
   `adapt` answers "what have I not done about it yet", and those diverge the
   moment a repository sits still while binaries move. Two clocks: the binary
   has its version, and the repository has `adapted_through` in its own
   `.a2a/config.yaml`. `a2a update` used to print a digest since the version
   it REPLACED — correct for one hop, silently lossy across two. A binary
   taken 0.19 to 0.25 while nobody adapted the repo leaves every obligation
   in between behind, and the next update's digest can never show them again.

   So `adapt` walks from `adapted_through`, not from the binary's previous
   version, and prints only what OBLIGES you — a change whose
   `action.scope` is `none` is context and stays in `whatsnew`, where
   context belongs. Over the current corpus that is the difference between
   172 items and 44. The list is ordered so the first item is always one you
   can act on alone: local changes carrying a runnable command, then local
   changes without one, then anything needing another party. It exits
   non-zero while anything remains, so a harness can gate on it. `--json` emits
   the same pending set with `count`, the baseline it walked from, and
   `obligations_remain` carrying the SAME verdict the exit code encodes — so a
   caller never has to infer it from the count. `--done --json` has its own
   shape, reporting what was recorded, how many obligations were `verified` by
   a `detect:` run, and whether the record was refused and why.

   **`a2a adapt --done` records that you adapted**, in the repository rather
   than on your machine, so a teammate's agent cloning the repo neither
   redoes finished work nor skips work it wrongly believes done. It runs
   every `detect:` command in the pending set first and refuses if one still
   fires, naming the change. When the pending set carries no `detect:` at
   all, it records AND SAYS the record is unverified — most obligations are
   prose today, and `--done` will not pretend otherwise.

   Two refusals worth knowing before you meet them. A baseline NEWER than
   your binary refuses rather than walking backwards, naming both versions —
   that is a downgrade, and adapt will not guess what you meant. And a
   binary whose version is not a comparable release, a `dev` build, refuses
   too: it cannot compute the range, and "nothing to adapt" would be a claim
   about your repository that nothing had measured.

This describes what the `a2a` TOOL itself guarantees, nothing more — a space
may layer its own conventions on top (a stricter review step, an extra
notification channel, a house rule about which contracts need sign-off) that
this manual has no way to know about.
