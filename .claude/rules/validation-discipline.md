<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- cites: [code/validation.md, agent/verification-honesty.md] -->

# Validation discipline — verify before you conclude, guard what you verified

Always-on. On every claim you build on and every invariant you touch:

- **Verify before you conclude.** A claim about a system you don't own — a library, a provider, a version, a flag — rests on that system's own current source or an empirical check, never on memory; verify at the point of assertion, before the conclusion is load-bearing.
- **Claim only the strength you checked.** "Proven / verified / confirmed" must match what you actually ran; name degradation and uncertainty instead of rounding up, and when correcting a wrong claim re-anchor on the source rather than swinging to the opposite one.
- **Guard what you verified once (the maniac loop).** A hand-verification you did once is a gate you haven't written yet — new entity, SSOT, boundary, or convention means propose the gate, don't wait to be asked.

Full treatment: the validation and verification-honesty doctrines (`.mate/doctrine/`).
