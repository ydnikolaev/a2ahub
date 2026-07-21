<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- cites: [code/framework-first.md] -->

# Framework-first — use the canon, custom code is debt

Always-on. When you reach for custom code — a plugin, wrapper, low-level flag, or hack — to make something behave the way a framework or library might already own:

- **Search the canon first, in order.** Framework docs → library docs → repo precedent → only then custom. "I didn't know it could do that" is the normal result of searching, not an embarrassment.
- **Configure at the highest-level knob that owns the concern.** Don't drop to a lower layer (raw runtime flag, bundler option) to force behavior the higher one governs — that coupling breaks silently on upgrade.
- **Treat custom code as debt to retire, not precedent to extend.** An existing wrapper never justifies the next one; removing custom code beats adding it.
- **Round-N stop.** After a few failed speculative fixes, stop guessing — switch to a minimal reproduction and the canonical doc read end-to-end.

Full treatment: the framework-first doctrine (`.mate/doctrine/`); the concrete search order, config layering, escape hatches, and Round-N threshold for a given stack live in its `.mate/profiles/<stack>/` refinement.
