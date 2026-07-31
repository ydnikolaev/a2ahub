# Reliability

Reliability is a chain of bounded evidence, not a blanket badge.

- Schemas validate document shape.
- The fold validates lifecycle and authority.
- Reference resolution validates causal links.
- Git and pull requests preserve the inspectable record.
- Release verification checks the binary and published evidence.
- Missing scope is reported as unavailable, never green.

## Latest release evidence

Statusline refresh now survives the one-shot command, while the merge gate becomes deterministic and cache-aware

- **A stale statusline launches one durable refresh for the next render:** The prompt-facing statusline still reads local cache only and keeps its existing text, JSON, and exit-code contract. When that cache is stale, it now atomically claims a short-lived local lease and starts the canonical `a2a sync` command as a detached process. The refresh can finish after the one-shot statusline process exits, concurrent prompt renders do not start a herd of duplicate syncs even while recovering an expired lease, and the next render sees the refreshed mirror. The previous in-process goroutine could be terminated by normal CLI exit or race a test fixture's cleanup.
- **The ordinary CI gate no longer depends on ambient Git identity or a cold duplicate Go cache:** Git-backed tests now receive a process-local fixture identity, the checkout-owned validation runner has a documented scoped-test entrypoint, GitHub's setup-go cache feeds the exact build cache consumed by `make check`, and workflow syntax validation fails closed with pinned actionlint. The public-only classification backstop no longer rejects the private source repository, and CodeQL has the read scope its analyze step needs under restrictive workflow permissions. CodeQL execution is scoped to the public repository where code scanning is enabled; private CI keeps its gosec, govulncheck, and gitleaks gates without spending minutes on an upload GitHub will refuse. Lifecycle wire parity retains every case while reusing one immutable repository fixture.
- **Standing known issues no longer disappear on the next update:** Current limitations now live once in a schema-validated standing list instead of being copied into every version file. `a2a whatsnew`, its MCP twin, the HTML release view, and the authored GitHub Release body append that list independently of the selected version range, including when there are no newer change entries. The existing release-array machine shape remains unchanged, and a resolved issue is removed in one place.
- **The v0.16.3 runtime candidate passed all 50 declared live cells:** The immutable public runtime candidate d6418b926ec5363416ef8b42a572788e7e7e009a completed the full protected GitHub matrix with two independent identities: 50 pass, zero fail, zero timed-out, and zero not-run cells across CLI, MCP, lifecycle, contracts, authorization boundaries, failure recovery, thread reconstruction, and space migration. The audit observed 179 workflow runs; its two red runs were the expected and explicitly claimed cross-section boundary probes. The final tagged tree differs from that runtime candidate only by this evidence statement and the matching README baseline; local gates, release preflight, renderer checks, and the filtered-public leak proof are repeated on the exact tag candidate.
