# Secret-scan corpus (§13.4)

Golden content for the boundary secret/forbidden-payload scanner (§10.4,
P3's `internal/validate` scanner). This phase supplies the fixtures only —
the scanner itself is out of footprint.

Scan scope per §10.4: ALL text content crossing the boundary — envelopes,
bodies, event notes, `provides/**/schema/`, `fixtures/**`, `docs/**` — not
only `.md` envelopes. It is best-effort, with a documented false-negative
reality (encoded secrets, UUID-shaped keys, PII cannot be reliably
pattern-matched).

## positive/ — known-pattern fixtures (MUST block)

| Fixture | Pattern | Note |
|---|---|---|
| `aws-access-key.md` | AWS access key ID shape (`AKIA[0-9A-Z]{16}`) | uses AWS's own public documentation placeholder (`AKIAIOSFODNN7EXAMPLE`), not a live credential |
| `github-pat.md` | GitHub personal access token shape (`ghp_[A-Za-z0-9]{36}`) | fabricated token, not a live credential |
| `private-key-block.md` | PEM private-key block (`-----BEGIN ... PRIVATE KEY-----`) | fabricated key material, not a live credential |

## negative/ — benign lookalikes (MUST pass)

| Fixture | Why it looks risky | Why it's benign | False-positive budget |
|---|---|---|---|
| `uuid-reference.md` | a bare hex/dash string superficially resembles a key | it's a v4 UUID used as a correlation ID (`refs`), not a credential | budget: 0 — UUIDs MUST NOT trip the AWS/GitHub/PEM patterns above; a pattern that fires on a v4 UUID is too broad and must be tightened |
| `commit-sha-reference.md` | a 40-char hex string superficially resembles an API secret | it's a git commit SHA cited in a `refs` pin, the digest form §5.2.2 records for publish events | budget: 0 — bare hex SHAs are common and legitimate; the scanner must not flag hex-only strings without a recognized credential prefix |
| `placeholder-env-var.md` | contains the literal string `API_KEY=` | the value is the literal placeholder text `<your-key-here>`, not a secret shape | budget: 0 — placeholder/example syntax (`<...>`, `${...}`) MUST NOT trip the scanner; documented here as the one class of accepted false-negative risk this corpus deliberately does not test (a real key pasted over a placeholder would evade a naive same-line check — flagged as a known limitation per §10.4's "best-effort" framing, not a gap this phase can close) |
