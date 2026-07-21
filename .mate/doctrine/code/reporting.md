<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Reporting doctrine — the numbers are derived, the judgment is written

> **Thesis.** A progress report is read by people who **cannot check it** — a
> partner, a client, a stakeholder who will never open the repository. That
> asymmetry is the whole problem: every other artifact a team produces is checked
> by the machine that consumes it, and a report is checked by nobody. So the report
> must check itself. **Every number is derived by a script from the repository;
> every judgment is written by a human or an agent and is labelled as judgment; and
> every derivation that is a proxy says so, in the document.** A report that cannot
> say where a number came from has no business carrying it.

## 0. When to apply (and when not)

The trigger is **any recurring artifact that tells someone outside the work how the
work is going** — a daily progress page, a weekly client update, a sprint summary, a
partner deck. It applies whenever the reader cannot verify the claim themselves.

It does NOT apply to a changelog (the commits *are* the record, and the reader can read
them), to a status file (an internal orientation note for people who can open the repo),
or to a one-off ad-hoc answer to "how's it going?". And a project that reports to nobody
needs none of this: the machinery earns its place only when a report is recurring, and
recurring means someone will start trusting it.

## 1. The numbers are derived, never authored

**Every quantity in a report is computed by a script from the repository, and the
narrative cites those quantities rather than restating them from memory.** An agent
asked how many commits landed will produce a plausible number; `git` produces the true
one. The gap between plausible and true is invisible to the reader — which is exactly
why it is intolerable.

The rule has a mechanical consequence: the facts are **persisted** (a machine-readable
record per period), and the prose is written *against* that file. A number that is not
in the facts file does not go in the report — there is no third source. This also makes
the report's history reproducible: the prose can be rewritten, the numbers cannot drift.

A derived **number** is safe to embed as-is; a derived **string** is not — a commit
subject or an agent-written headline is untrusted the moment it reaches HTML or YAML,
regardless of how trustworthy its source (`git`) is, and is escaped or quoted at that
boundary rather than interpolated raw.

## 2. Progress comes from the work-item SSOT, never from prose

**Completion is read from the machine-readable state of the work items themselves —
never from checkboxes, headings, or any other mark inside a human-written document.**

This is not fastidiousness. A real epic under this harness carried 0 of 41 ticked
acceptance boxes across its markdown while 7 of its 17 tracked phases were **done** — the boxes had
simply never been the thing anyone maintained. A checkbox-counting report would have told
the reader **0%**: not imprecise, but the exact opposite of true, in the one direction
that destroys trust fastest.

Where a project has no machine-readable work-item state, the report shows **no progress
number and says why**. A stated absence is information; an invented number is a lie with
a progress bar around it.

## 3. Every proxy names itself

**A derived quantity that is not the thing it stands for must carry its method into the
document.** Hours inferred from commit timestamps are the canonical case: a commit is
evidence that work happened, not a measurement of how long it took. The inference is
still worth making — it is the best available signal — but it ships with its own
disclosure, in the report, in plain words, not in a code comment nobody reads.

The mechanism that makes this survive: the method travels **inside the facts file** (the
session threshold, the exclusions, the sentence itself), so the rendered report cannot be
produced without it being available to state.

### A measurement retires a proxy only when it says what it measured

A better source appears — the tooling starts keeping its own logs, the runtime records what
it actually did — and the temptation is to swap the proxy out and keep the old label. That
is how a report acquires its most dangerous kind of number: precise, sourced, and answering
a different question than the one on the page.

Two failures, both live, both caught only by looking at the first real output:

- **Parallel work does not add up on a calendar.** Summing every agent session's active time
  produced *25.3 hours inside a 24-hour day* — true as machine effort (sessions run
  concurrently: subagents, worktrees, two terminals), and impossible as elapsed time. Both
  numbers are worth having, and neither may wear the other's name: effort is **summed**,
  elapsed time is the **union** of the intervals.
- **A total is not a measurement.** The same run "processed" 1.85 billion tokens — 98% of
  them cache reads, the same context re-served turn after turn. As a headline that number is
  spectacular and meaningless. What means something is fresh input and output; the rest is
  named separately or not at all.

And the units of a measured quantity are themselves a fact to verify, not to assume. Two
runtimes counted their cached tokens on opposite sides of the same line (one *inside* the
input count, one *beside* it) — so a single "input tokens" column across both, added without
checking, would have been a number nobody could defend. Where the sources disagree, the
report normalizes them and records **which convention each one used**.

## 4. Estimates are ranges with a basis

**An estimate is a range, and it names what it rests on; a point estimate with no basis
is a fabrication with a decimal point.** "Two to three days: two dependency waves remain,
the first of which is four parallel phases" is an estimate. "2.5 days" is a guess wearing
a suit.

The mechanical skeleton (how many dependency waves remain, how many phases can start now)
is derived like any other number. The judgment on top of it — how long a wave takes here,
with this team, at this load — is judgment, and is labelled as such.

## 5. A published report is immutable; the aggregate is generated

**Once a period's report is published it does not change; the cumulative view is
regenerated from the persisted facts on every build.** A reader who saw Monday's report
must find the same report on Friday — silently rewriting history is the fastest way to
make every future report worthless.

The two therefore have opposite policies, and the difference is not arbitrary: the daily
report contains prose (not reproducible, therefore frozen), while the aggregate contains
only what the facts say (reproducible, therefore regenerated — and drift-gated like any
other generated artifact). The aggregate is built from the **facts**, never by parsing
the published prose: a generated artifact that parses generated prose is a house of cards.

A generated artifact must also contain **no clock** — a "generated on <date>" line makes
its own drift gate red on every run, and a gate that cries wolf is worse than none.

The same cry-wolf test decides what the drift gate compares. The aggregate carries its
**presentation** inline (that is what keeps it one shareable file), and presentation is
released centrally — so a byte-for-byte comparison would red every project the day the
shared design changes a token, for a difference the project did not make and cannot
re-derive. **Gate the substance, not the skin:** the comparison covers what the facts
say, and the presentation refreshes on the next build. What that costs — an aggregate
wearing last release's skin until the next report — is stated, not discovered.

## 6. The link from work to record is written at commit time

**The attribution of a commit to the work item it advances is captured when the commit is
made — not reconstructed afterwards from its message.** Reconstruction is lossy and it
undercounts in a specific, embarrassing direction: real work on an epic lands under
sub-module scopes (`feat(destinations)` for a visa-launch phase), so a scope-matching
report shows less progress than actually happened. The cheapest capture — a trailer, a
list on the work item, one line either way — is worth more than any parser.

Where a report must fall back on inference, it records **how** each link was made, and
the weaker links are labelled. A report that cannot distinguish what it knows from what
it guessed will eventually present a guess as a fact.

## 7. Anti-patterns

| ❌ Anti-pattern | ✅ Instead |
|---|---|
| **The recalled number** — an agent writes "about 30 commits, roughly 5k lines" from having read the diff | every quantity is read out of the facts file the script produced |
| **The checkbox percentage** — progress counted from ticked boxes in a document | the work items' own machine-readable state (§2) |
| **The bare hour** — "11.5 hours of work" with no method | the number *and* its method, in the document (§3) |
| **The confident point estimate** — "ready in 2.5 days" | a range with its basis; or, absent a basis, what it depends on (§4) |
| **The retconned day** — yesterday's report quietly regenerated with today's better prose | the day is frozen; only the aggregate rebuilds (§5) |
| **The missing epic** — an unstarted epic omitted because 0% "looks bad" | the row stays, with the plain-language reason it has not started |
| **The hidden harness** — the tooling's own commits dropped so the product story looks bigger | shown, and shown last (honest, and not self-absorbed) |
| **The unescaped untrusted string** — a commit subject like `</script>` or one containing a colon breaks the rendered page or the frontmatter parser | escape or quote every untrusted string at the point it enters HTML or YAML (§1) |

## 8. Instantiation seam

What each project swaps: **where the reports live** (the reports home, through the
structure standard's `paths:` seam); **how work items are enumerated and counted** (the
project owns its tracker schema — the harness declares only the normalized contract it
consumes, so no project's schema ever enters the shared body); **the method values** (the
session threshold, the excluded paths, which commit types count as product work); **the
audience and the language** of the human-facing document. Under mate: `mate report facts`
derives the facts, `mate report build` generates the aggregate, the `create-report` skill
carries the judgment, and `.mate/config.yaml` under `report:` carries every value above.

## 9. Cross-links

- [validation.md](validation.md) — the aggregate is a generated artifact, so it is
  drift-gated like any other; the reports gate rides in the consumer's existing check
  rather than as a gate of its own (a gate registered where the surface is not is a gate
  that passes forever).
- [structure.md](structure.md) — the reports home and its `paths:` override; the day
  directory is the durable unit.
- [documentation.md](documentation.md) — the generated-vs-hand-written split this
  doctrine applies to a report: the aggregate is *reference* (generated, gated), the
  day's page is a *guide* (written, frozen).
- [verification-honesty.md](../agent/verification-honesty.md) — the register a report
  writes in: claim only the strength you checked, and name the degradation.
