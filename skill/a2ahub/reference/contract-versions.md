# Contract versions — the rolling window, and how an interface retires

An interface between two systems is never one thing for long. The steady state
of a maintained contract is several versions alive at once: `1.0` retired,
`1.6` deprecated with a sunset date but still read, `1.6.1` published for the
consumers who have not moved, `2.0` published and being adopted. a2a calls that
the **rolling window**, and it is a first-class shape rather than a situation
you work around.

## What each version's state means

| State | For the producer | For a consumer on that line |
|---|---|---|
| `published` | live, and you may publish a compatible successor on this line | safe; no action |
| `deprecated` | you have announced a sunset; retirement is coming | **act**: migrate, and acknowledge the announcement |
| `retired` | withdrawn from the catalogue; the bytes remain resolvable forever | you should already be off this line |

The contract's OWN state, the one `a2a contracts` prints in the `state` column,
is a **projection** over those: `published` while any version is published,
`deprecated` once every published version is deprecated, `retired` when all of
them are. So a contract can read `published` while one of its lines is
deprecated and waiting on somebody's acknowledgement. That is the projection
working — the contract IS alive, on another line — and it is the single most
common thing a reader mistakes for a stale value.

To see the lines rather than the summary:

```sh
a2a contracts
# space   XC-gvcore-visa-status   gvcore   2.3.0   published   1.0.0=retired 1.4.0=deprecated 1.4.1=published 2.0.0=published
```

The sixth column appears only for a contract with more than one recorded
version. `a2a html` renders the same window under every contract you provide
and under every dependency you consume — with YOUR pinned line outlined, since
during a sunset the question is never "what exists" but "what is happening to
mine".

## Retiring a line, both sides

The cycle is deliberately slow, and every step is refusable rather than
destructive.

1. **`a2a contract deprecate <id> --version <v> --successor <id@v> --sunset
   <date>`** — announces it. The announcement goes to every REGISTERED consumer
   on any major, so someone pinned to `2.x` still hears that `1.x` is going.
2. **The consumer acknowledges** (`a2a ack <XA-id>`). This is not a courtesy:
   it is what unblocks step 3.
3. **`a2a contract retire <id> --version <v>`** — refused until every consumer
   registered ON THAT MAJOR has acknowledged and the sunset has passed. A
   consumer on a different major does not block you; they were told, and they
   were never on this line.

Two properties worth relying on:

- **Retiring one version never touches another.** `retire 1.6` while `2.0` is
  published is an ordinary act. The contract stays live and publishable.
- **Retirement removes nothing.** `id@version` resolves through the publish
  event's own commit forever, so a consumer pinned to a retired line can still
  read exactly what it agreed to. Retirement is a statement about support, not
  a deletion.

## Publishing on an older line

Publishing `1.6.2` while `2.0` exists is the normal act during a sunset, and it
is compared against `1.6.1` — the highest published version below the one you
are publishing — not against `2.0`.

**Use an explicit `--version` for a maintenance release.** `--bump` has to
choose a baseline before it knows your target, so it bumps the
globally-highest version and will mint `2.0.1` when you meant `1.6.2`. This is
the same reason `deprecate` and `retire` already require `--version` once more
than one version is published.

```sh
a2a contract publish <id> --version 1.6.2     # maintenance on the 1.x line
a2a contract publish <id> --bump major        # the newest line moves forward
```

## Registering, and why it is the whole basis of this

`a2a contract adopt <XC-id> --major <n>` writes your `consumes.yaml`. Nothing
above applies to you until you have run it:

- you receive deprecation announcements because you are registered, never
  because you were named in a contract's authoring-time `to:`;
- you block a retirement of your own line for the same reason;
- and if you adopt in the MIDDLE of a sunset window, you still see the
  deprecation that was announced before you arrived — your inbox matches an
  announcement's `deprecates:` against your own registry rather than against a
  recipient list frozen weeks earlier.

Unregistered consumption is invisible by design. Read someone's contract
without adopting it and nothing will ever tell you it is changing, because
nothing in the space knows you are there.
