<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- cites: [agent/harness-layers.md] -->

# Harness discipline — whose surface is this?

Always-on. Before writing to any harness artifact (skill, rule, gate, doctrine, config) — including the ambient "keep this for next time":

- **Never edit a managed copy.** A file with a mate provenance stamp (or in `.mate/lock.json`) is read-only here — the fix is authored upstream in the mate SSOT, released, and pulled back; editing it locally forks the source of truth and the next pull eats it.
- **Classify before placing; project-only is the default.** A project value or project-specific artifact stays home (native name, own file, or `.mate/config.yaml` — the seam synced skills already read); only a change with a nameable second consumer promotes up (`mate promote` → `/mate-promote` classifies core/profile/operator — or refuses).
- **Principle into the shared body, value into the config.** A shared artifact never carries one project's paths, commands, thresholds, or ids — and a fix to a *shared* artifact never stays local.
- **A provider fact is read, written down, and branched on by surface — never recalled.** Designing anything that touches the agent runtime starts at *its current docs*, not memory; what you verify goes into the adapter's surface registry with the build you checked it against; and the code asks that surface ("has a rules home?"), never the provider's name.

Full treatment: the harness-layers doctrine (`.mate/doctrine/`).
