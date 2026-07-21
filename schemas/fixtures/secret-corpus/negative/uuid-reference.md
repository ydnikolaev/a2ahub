# Benign lookalike: v4 UUID used as a correlation ID

Body content that would cross the space boundary and MUST pass the secret
scanner (§10.4, §13.4) — a bare UUID superficially resembles a key/token but
is not credential-shaped:

```
correlation_id: 3fa85f64-5717-4562-b3fc-2c963f66afa6
```
