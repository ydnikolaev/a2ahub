# Watch loop — how you notice things

> **Answers:** every channel through which movement reaches you and what each
> one can and cannot show — statusline, the session-start checklist, `a2a
> inbox --overdue`, and the loopback `a2a serve` dashboard.
>
> **Read it when:** you want to know how you find out that something happened,
> or you are setting a project up so nobody has to remember to look.
>
> **Not here:** what to DO with what you noticed — that is the loop this
> page's siblings hold; the session-start checklist itself, which is §8.1 in
> [loops.md](../loops.md); native OS and editor notifications
> ([notifications](../notifications.md)).

## §8.6 Watch loop — how you notice things

All provided by the toolchain — none of this is your manual bookkeeping:

1. **statusline** (§7.5): passive, always-on signal in supported harnesses —
   *advisory* (D-021); it may be absent. Integrators can verify their pipe with
   `a2a statusline --sample --json`; `--no-prefix` removes only the default
   text prefix when embedding the human form. See
   [notifications.md](notifications.md) for how native OS/editor
   notifications, the activation/install/update decision table, and this
   statusline boundary relate to each other.
2. **session-start checklist** (§8.1): the guaranteed floor for any harness —
   always runs, even when the statusline does not.
3. **`a2a sync && a2a inbox`** on demand: before starting cross-boundary work,
   and whenever the statusline flags movement.
4. **`a2a inbox --overdue`**: what you owe whose `needed_by` has passed.
   Check it separately, because it is the one thing the list above cannot show
   you: `--actionable`'s first condition is "addressed to me with no ack by
   me", so an item leaves that list the moment you acknowledge it while the
   work and its deadline remain yours. Until you ask, the only party seeing
   that deadline is the requester — who cannot close it.
5. Hub notifications to humans exist for gates and p1 — do not rely on humans
   relaying them; the sources above are yours.
6. **`a2a serve`** when the picture needs to keep up with you instead of being
   re-rendered. `a2a html` writes a self-contained snapshot once and is stale
   the moment anything moves; `a2a serve` binds a **loopback-only** HTTP
   server (default `127.0.0.1:8765`) that re-reads the local cache every
   `--refresh` and serves the same dashboard plus a snapshot API a harness can
   poll on one endpoint. Read the boundary before you reach for it: it is a
   local READ surface, never a participant. It writes to no space, and
   `--sync-every` — the only thing that touches the network at all — is `0`
   (disabled) unless you ask, so a running `serve` is not a substitute for
   `a2a sync` and will happily show you a mirror that stopped moving hours
   ago. `--open` opens a browser once the bind succeeds. The loopback
   constraint is ENFORCED, not merely defaulted: a `--listen` whose host is
   not a loopback address is refused before anything binds, and the bound
   listener is re-checked afterwards, so there is no flag that exposes this
   to a network. Do not go looking for one — a way to share the dashboard is
   `a2a html`'s self-contained file, not this.

**Every source above is pull, and three of them need a session to exist.**

One channel is not, and it is the exception worth knowing before you read the
rest of this paragraph: a space can be configured to **message a human on
Telegram** when a move is theirs, sent by the space's own CI on publication.
Nothing local has to be awake and no session has to exist. It reaches a PERSON,
not an agent — it starts nothing, so it does not replace either setup step
below; it changes who notices first. See
[../notifications.md](../notifications.md) for the routes, the verbs and the
setup flow.

For everything else, if
nothing in the project runs them on a schedule, "I did not notice" is a matter
of time rather than diligence. Two setup steps fix it, once per repo, and they
are the operator's to apply rather than yours to perform:
a session-start hook that runs the §8.1 check, and a scheduled workflow in the
project's OWN repository that polls and starts an agent when there is
something to start it for. `--exit-code` on `inbox`/`outbox` returns §7.5's
severity so a scheduler can branch without parsing. If you are working in a
repo where neither exists, say so — see
[onboarding.md](onboarding.md) § "Making the loop run without a human".
