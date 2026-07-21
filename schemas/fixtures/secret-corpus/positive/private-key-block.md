# Known-pattern fixture: PEM private-key block

Body content that would cross the space boundary and MUST be blocked by the
secret scanner (§10.4, §13.4):

```
-----BEGIN PRIVATE KEY-----
MIIFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE
FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAK
-----END PRIVATE KEY-----
```

(fabricated key material — matches the PEM private-key header/footer shape,
not a live credential.)
