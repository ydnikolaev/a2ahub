# Benign lookalike: git commit SHA cited as a pinned ref

Body content that would cross the space boundary and MUST pass the secret
scanner (§10.4, §13.4) — a 40-char hex string superficially resembles a
secret but is a publish event's recorded commit SHA (§5.2.2):

```
refs:
  - {ref: "XC-axon-ingest@1.1.0", note: "commit 4f0c3a2e9b7d1f5860e2c4a8d6b1f3e7a9c0d2e4"}
```
