# Reports — the derived progress record

Everything in here is **derived**, never hand-authored. The numbers come from the
machine; a report's prose cites them and adds only judgment. Nothing in this folder
is a place to type a number into.

## Layout

```
reports/
  index.html          the aggregate — every day, one self-contained page, date-navigable
  14.07.2026/
    facts.json        the day's whole durable record: machine facts + the frozen summary
    report.md         the report FOR AN AI SYSTEM to consume (English, canonical)
    report.html       the report for humans (partners, the team)
  15.07.2026/
    ...
```

## How a day is made

`/create-report` runs the pipeline: `mate report facts` collects the window's git
facts and (if this project implements the optional `make report-epics` target) each
epic's phase state; the skill writes the narrative; `mate report build` regenerates
`index.html` from every day's `facts.json`.

## Two rules that keep this trustworthy

- **A published day is immutable.** Once a day's directory exists it is not
  regenerated — a report someone has already read must not silently become a
  different report. `index.html`, by contrast, is deterministic and is rebuilt every
  time, so the history can never drift from the facts.
- **Every number names its method.** Hours are derived from commit timestamps — a
  proxy for effort, not a measurement of it — and each report says so. Estimates are
  ranges with a stated basis, never a point number nobody can defend.
