# Security Policy

## Supported versions

Only the **latest release** of `a2a` receives security fixes. Upgrade with
`a2a update` (or reinstall from the latest GitHub release) before reporting
an issue, and confirm it still reproduces there.

## Reporting a vulnerability

Please report security issues **privately** — do not open a public issue.

Use GitHub's private advisory flow: go to the repository's **Security** tab
and click **[Report a vulnerability](https://github.com/ydnikolaev/a2ahub/security/advisories/new)**.
This opens a private channel visible only to the maintainers.

Include, where you can:

- the `a2a version` you reproduced on, and your OS/arch;
- the exact steps or a minimal recipe/command that triggers the issue;
- what an attacker gains, and any known workaround.

## Response expectations

- **Acknowledgement:** within 3 business days.
- **Assessment and next steps:** within 7 business days.
- **Fix:** shipped in the next release once a fix is ready; you'll be
  credited in the advisory unless you ask otherwise.

## Verifying release binaries

Every release publishes a `SHA256SUMS` file plus a per-asset cosign bundle
and carries build provenance, so you can confirm a binary was built by this
repository's release workflow before trusting it. Substitute the asset name
for your platform (e.g. `a2a-linux-amd64`, `a2a-darwin-arm64`,
`a2a_<version>_linux_amd64.tar.gz`, ...):

```bash
# SLSA build provenance (needs only the GitHub CLI):
gh attestation verify a2a-<os>-<arch> -R ydnikolaev/a2ahub

# cosign keyless signature over that specific asset:
cosign verify-blob \
  --bundle a2a-<os>-<arch>.cosign.bundle \
  --certificate-identity-regexp '^https://github.com/ydnikolaev/a2ahub/.github/workflows/release.yml@refs/tags/v.*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  a2a-<os>-<arch>

# SHA-256 integrity against the published checksums:
grep "  a2a-<os>-<arch>\$" SHA256SUMS | sha256sum -c -
```

`scripts/install.sh` and `a2a update` both run the SHA-256 check
automatically (fail-closed: they refuse to install on a mismatch or a
missing `SHA256SUMS` entry); the cosign/attestation commands above are for
anyone who wants to verify provenance by hand.
