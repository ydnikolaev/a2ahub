# Known-pattern fixture: AWS access key ID

Body content that would cross the space boundary and MUST be blocked by the
secret scanner (§10.4, §13.4):

```
AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
```

(`AKIAIOSFODNN7EXAMPLE` is AWS's own published documentation placeholder —
not a live credential — but it matches the real AWS access-key-ID shape
`AKIA[0-9A-Z]{16}`, which is what the scanner pattern-matches on.)
