# `feedback-hub` — the feedback hub of record

This branch carries the a2a feedback corpus, and nothing else. It is not a copy
of the project: there is no source, no history of `main`, and no build here. It
is an orphan branch whose whole content is `feedback/`.

## Why this branch exists

`main` in this repository is a **publication**. Every release rewrites it
wholesale with a force-push of a filtered tree. That was also, until now, the
branch every `a2a feedback submit` targeted — so a report filed by an outside
reporter lived on a ref that a release could overwrite, and an open feedback
pull request went out of date the moment a release landed.

Measured on 2026-08-12: two open feedback pull requests were both `MERGEABLE`
and `BEHIND` after a publish, both with auto-merge armed and neither firing;
merging the first put the second back into `BEHIND`. A report submitted after
that same publish merged in twenty-three seconds. The difference was entirely
whether a release happened while the record was open.

A letterbox nailed to a door that gets replaced. This branch is the letterbox
moved off the door.

## What is guaranteed here

- **The publisher never touches this branch.** It pushes exactly three refs —
  `main`, a candidate ref it creates, and the same candidate it deletes.
- **Force-pushes are refused**, by branch protection, so history here is
  append-only in practice.
- **A record filed here survives every release**, and a verdict written here
  reaches its reporter immediately rather than at the next release.

## How to file a report

Use the tool — it does the right thing on its own:

```
a2a feedback submit
```

It opens a pull request against this branch adding exactly one file,
`feedback/inbox/fb-<YYYYMMDD>-<6hex>.yaml`. The intake workflow validates that
file with a pinned released binary, without ever checking out the pull
request's head, then labels the request and arms auto-merge.

## How to read a verdict

Statuses are readable directly, no clone required:

```
https://raw.githubusercontent.com/ydnikolaev/a2ahub/feedback-hub/feedback/inbox/<id>.yaml
```

`a2a feedback status` reads exactly that.

## The rollover window

Binaries released before this branch existed still submit to `main`, and they
cannot be updated retroactively. For as long as any of them are in the field,
**both inboxes are live** and reports arriving on `main` are carried here. That
window ends after thirty consecutive days with no feedback pull request opened
against `main`; until it does, nothing that guards the old path is removed.

If you filed a report before this branch existed, it is here — the corpus was
migrated by copy, and the copy on `main` was left in place.
