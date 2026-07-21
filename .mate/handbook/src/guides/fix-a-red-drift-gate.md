<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
# Fix a red drift gate

**What it is.** Resolving a failing drift gate — `make mate-check`, `mate docs
check`, or `mate registry lint` reporting that a synced or generated artifact no
longer matches its source.

**When to use.** CI (or a local `make check`) reddened with a drift, collision, or
coverage failure and you need to get back to green *correctly* — not by silencing
the gate.

**When NOT to use.** Not for a genuine authoring change to the SSOT — if you
*meant* to change a doctrine or a generated page's source, the fix is to
regenerate/re-pull, not to suppress. Never reach for `--no-verify`.

**How.**
1. **Generated-drift** (`mate docs check` → stale page): a reference page was
   hand-edited or its source changed. Run `mate docs gen` and commit the
   regenerated page. Never hand-edit a `GENERATED` page.
2. **Coverage** (undocumented surface): a new verb / skill / registry / doctrine /
   gate landed without a handbook page. Add its reference (usually just
   `mate docs gen`) or the guide it needs — docs move *with* the change.
3. **Sync-drift / collision** (`make mate-check`): a consumer edited a
   provenance-stamped file in place. Re-pull to restore it, or promote the change
   upstream if it belongs in the SSOT. A collision (unstamped local file in the
   way) resolves delete-local → re-pull → stamped.
4. **Dead-link**: a moved or renamed target. Fix the link to point at the real
   file.

**Typical mistakes.**
- Hand-editing a generated page to "fix" drift — it re-reddens next run.
- Suppressing the gate (`@ts-ignore`, `--no-verify`, commenting it out) instead of
  fixing the root cause. The gate is the messenger.
- Editing a synced copy in the consumer instead of the SSOT source.

**Links.**
- [validation doctrine (gates, teeth)](../../../doctrine/code/validation.md)
- [documentation doctrine (the doc gates)](../../../doctrine/code/documentation.md)
- [Gates reference](../reference/gates.md)
