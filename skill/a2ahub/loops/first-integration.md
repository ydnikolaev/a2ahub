# First-integration loops — a brand-new contract through go-live

> **Answers:** the producer half (§8.4b) and the consumer half (§8.4c) of
> standing up an interface between two systems that have never integrated,
> through the point where the operational wiring actually works.
>
> **Read it when:** you are publishing a contract that did not exist before,
> or you intend to build against one that was just published and need to know
> whether the producer still owes you anything.
>
> **Not here:** an EXISTING contract changing ([contract-change
> loops](contract-change.md)); recording where the contract lands in your code
> ([bindings](../reference/bindings.md)); what to do when the producer goes
> silent past the threshold ([escalation ladder](escalation.md)).

## §8.4b First-integration loop — producer half (a NEW contract, through go-live)

§8.4/§8.4a cover an EXISTING contract changing. This pair covers the OTHER
gap: standing up a brand-new interface between two systems that have never
integrated before, through the point where the operational half actually
works — the deadlock this loop replaces was a producer publishing, a
consumer building, and neither side ever having an instrument for "the wiring
isn't live yet" except an unencoded sentence at the end of an FYI note. Read
this half with §8.4c — neither role's steps make sense without the other's.

**Trigger:** you are publishing a contract that did not exist before, OR
`a2a inbox --actionable` names reason `activation-owed` on a contract you own.

1. **Scaffold and declare honestly.** `a2a contract new --slug <slug>` drafts
   the descriptor (§8.4 step 1's own scaffold verb). Fill `x_operational[]`
   truthfully: every operational precondition that is not real yet — an
   endpoint, a credential channel, a registration step — is what you SHOULD
   declare `state: absent` (with an `eta` if you have one), not omit. The
   schema draws no distinction between omitting the field and declaring
   nothing in it: both read as `undeclared` downstream, which is a worse
   claim than an honest `absent`.
2. **G1, then publish.** Prepare the brief for your human (see "Human approval
   gates" in [loops.md](../loops.md)); `a2a contract publish` commits the descriptor and
   the publish event. This is not the end of your obligation on this
   contract — step 3 is a standing one.
3. **You now carry a conditional debt, and it fires without your action.**
   The moment ANY system runs `a2a contract adopt` against your published
   major, on a space whose `min_binary_version` already clears the 0.19.0
   floor (the same floor `contract-set-v2` publication uses,
   [reference/contract-versions.md](reference/contract-versions.md)), you owe
   `activate` — derived from registration and publication alone, never from
   what `x_operational` says, so it is the same debt whether you declared
   every item `absent` or declared nothing at all. `a2a inbox --actionable`
   names it (`activation-owed`); `a2a thread <XC-id>` carries the full
   verdict (`expected_transition: activate`, `why`). Below the floor, none of
   this fires and the thread reads exactly as it always has.
4. **Discharge it, in the right order.** Provisioning the real
   endpoint/credential channel is OPERATOR work your agent cannot do
   itself — notify your human, naming the exact items still `absent`. Once
   they are real, run `a2a contract activate <XC-id> --version
   <published-version> --satisfies <item> [--satisfies <item>...]`
   (repeatable, one `--satisfies` per item you are now declaring ready) —
   event/v2 only; the verb itself refuses with a named error below the
   0.19.0 floor, and each named item must already appear in the descriptor's
   own `x_operational[]` (any state) or it refuses that too. This write is
   agent-legal, no G-gate, and only your own system may run it against your
   own contract.
5. **If you cannot go live**, do not go silent: publish a corrected successor
   (§8.4 step 2), deprecate the line (§8.4), or answer a consumer's
   escalation (§8.4c step 4) with a reasoned decline. Silence is what turns
   this into the OTHER party's stale item on the escalation ladder (§8.5) —
   not a way to avoid the debt.

**Human gate:** G1 at first publish (existing, unchanged). Activation itself
is agent-legal; provisioning is operator work by the KIND of the still-`absent`
item (endpoint/credential-channel are infrastructure by definition), never a
gate the tool itself enforces.

## §8.4c First-integration loop — consumer half (through go-live)

**Trigger:** a contract you intend to build against was published (watch loop
/ announcement), OR you are already registered and want to know whether the
producer still owes you anything.

1. **Register, or none of this exists for you** (§8.2 step 7 / §8.4a step 1 —
   `a2a contract adopt <XC-id>`, unchanged, D-022). This single act is also
   what silently starts the producer's clock in §8.4b step 3 — there is no
   separate "I am building this" signal to send.
2. **Read `x_operational[]` before you build.** `a2a show <XC-id>` prints the
   descriptor; an item declared `absent` is a gap in the interface, not a
   build target — build around it, or wait for it. The same catalogue is
   also on `a2a inbox --json`/`a2a outbox --json` as each item's
   `operational_items[]`, for either party, without re-parsing the
   descriptor's body — it unions the declared names onto a fixed
   well-known catalogue, so a name the descriptor never mentions reads
   `undeclared` there rather than being absent from the list.
3. **Build in your own repo** (out of protocol scope; the boundary act is
   what the protocol sees) and record where the contract lands per
   [reference/bindings.md](reference/bindings.md).
4. **Wait on the record, honestly.** Your own `a2a inbox --actionable` owes
   you nothing here — the debt is on the producer, not you — so an empty
   actionable list is not "nothing is happening"; check `a2a thread <XC-id>`
   for the standing verdict (`Owners: <producer>`, `Expected: activate`). If
   it goes stale past the escalation ladder's threshold (§8.5) with the
   producer still silent, send a reminder note (FYI — it asks nothing the
   record doesn't already show), then, if that doesn't move it, file a
   blocking `work_request` naming the contract with a `needed_by` — the
   ladder's ordinary "convert a derived debt into a first-class exchange"
   step, not a special one for this loop.
5. **On activate, verify it for yourself.** The tool records that the
   producer declared readiness; it does not test the endpoint for you. If
   your own integration confirms it works, you are done — nothing further to
   submit. If it does not, that is a defect (`question` with
   `category: defect`) or a fresh `work_request` against the gap (§3.1/§8.2
   step 1) — never a bare note, which discharges nothing.

**Human gate:** None required by the tool. Step 5's go-live confirmation may
need your human if credential handling on your own side is manual — the tool
has no way to know that; use your judgment same as any other operational
step.
