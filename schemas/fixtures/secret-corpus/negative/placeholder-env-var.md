# Benign lookalike: placeholder env-var syntax in documentation

Body content that would cross the space boundary and MUST pass the secret
scanner (§10.4, §13.4) — the literal string `API_KEY=` looks risky but the
value is placeholder syntax, not a secret shape:

```
API_KEY=<your-key-here>
```
