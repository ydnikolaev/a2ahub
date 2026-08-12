# Feedback loop — "the tool itself got in my way"

> **Answers:** when to file feedback against a2ahub itself, the five gates a
> report must pass before it is filed, and the sequence that files it.
>
> **Read it when:** an `a2a` command failed or misbehaved and
> [troubleshooting.md](../troubleshooting.md) did not resolve it, or a
> just-finished work cycle produced a concrete, grounded improvement idea.
>
> **Not here:** anything about your space, your counterparty or your own repo
> — none of that is feedback about the product. Kind taxonomy, worked examples
> and sanitization guidance are in
> [reference/feedback.md](../reference/feedback.md): this page is the rubric,
> that page is the how-to.

## §8.7 Feedback loop — "the tool itself got in my way"

> This section transcribes plan §2 (25-agent-feedback.md) verbatim in intent —
> it is the SSOT for the rubric; `reference/feedback.md`, `onboarding.md`, and
> the dashboard Guide text all derive from it, never the reverse. If those
> surfaces ever drift from the list below, this file wins.

Feedback targets the a2ahub *product itself* (the tool/protocol/docs) — never
your space, your counterparty, or your own repo. It is filed with `a2a
feedback new/validate/submit`, not `a2a new`.

**Two trigger points, nothing else:**

1. **Hard failure**: an `a2a` command failed or misbehaved and
   [troubleshooting.md](troubleshooting.md) did not resolve it.
2. **End of a work cycle**: the just-completed work produced a *concrete,
   grounded* improvement idea (never mid-task, never speculative).

**All five gates must hold before filing** (the same list the schema's
`checks` block enforces — validate fails unless every one is `true`):

| Gate | Meaning |
|------|---------|
| `docs_consulted` | you read `troubleshooting.md` + the relevant `reference/` page first; the answer isn't there |
| `grounded_in_real_work` | the report cites work you actually did this session — no "wouldn't it be nice" |
| `not_space_specific` | it's about the a2a tool/protocol/docs — NOT about your counterparty, your space's content, or your own repo |
| `no_sensitive_content` | body sanitized: no space payloads, secrets, tokens, real system/actor IDs, private URLs |
| `duplicates_checked` | you checked your ledger (`a2a feedback status`) and searched `feedback/inbox/` + `feedback/backlog.yaml` on the hub repo for the same report |

**Batch policy:** file every independent item that passes all five gates.
`a2a feedback submit <file...>` and `--all` remove needless operator
round-trips, but each report still opens its own quarantine PR; never combine
several reports into one YAML file or one PR.

**`kind: feature` and `kind: friction` require a human check-in first** (prose
rule, not schema): surface the idea to your operator — "is this actually worth
the maintainers' time?" — and file only on their nod. `kind: bug` and `kind:
docs` may be filed autonomously.

**The sequence:**

1. `a2a feedback new <kind>` drafts `.a2a/feedback/<id>.yaml` from the
   embedded template.
2. Fill the body honestly and flip every `checks.*` gate consciously — the
   drafter starts them all `false`.
3. `a2a feedback validate <file>` — refuse to submit red.
4. Export a GitHub token as `A2A_FEEDBACK_TOKEN` (fallbacks:
   `GITHUB_TOKEN`, then `GH_TOKEN`), then run
   `a2a feedback submit <file...>` (or `--all`) — opens one PR per report
   against the hub repo; ledger rows are appended locally; a resubmit of an
   already-submitted id is an idempotent no-op.
5. Later, check what happened: `a2a feedback status` reports the hub-side
   `status`/`resolution` for everything you've filed — this is also how
   `duplicates_checked` gets fed honestly next time (see §8.1 step 4).
6. Reading the queue itself, if you are the one triaging:
   `a2a feedback triage` lists every `status: new` report with its dedupe
   candidates beside it, and `--apply <file>` records a verdict.

**The refusal `a2a feedback triage` will meet you with, and why it is not a
bug.** The LISTING form is a network read. Run from a git work tree it
resolves the hub of record FIRST and **refuses rather than reporting** in
three cases: the hub's default branch carries a report this tree does not
have; the comparison could not be completed at all (hub unreachable, fetch
failed, no such branch); or no hub of record is configured. The refusal names
the reason and names the hub, and in none of those cases will it print
`inbox clean` — that line is reachable only after the hub is confirmed
current, or when the directory is not a git work tree at all and there is no
hub to diverge from. So **bare `triage` now needs the network**, and offline
it refuses. That is the feature: before this, triage read only the local tree
and printed `inbox clean` while three real reports sat unread on the hub for
up to three days, one of them blocking a live delivery — an unchecked "clean"
is a worse answer than a refusal, because you act on it. Sync the hub and
re-run. `--apply` and every other feedback verb are unaffected and stay
offline-capable; only the listing checks.

Kind taxonomy, worked examples, and concrete sanitization guidance for
`no_sensitive_content` live in [reference/feedback.md](reference/feedback.md) —
this section is the rubric, that page is the how-to.
