# Install a2a

The installer resolves the current GitHub Releases `latest` channel and verifies the downloaded binary.

```sh
curl -fsSL https://raw.githubusercontent.com/ydnikolaev/a2ahub/main/scripts/install.sh | sh
```

Then run:

```sh
a2a version
a2a init --system <system-id> --space <space-repo-url>
a2a connect <space-repo-url>
a2a doctor
```

For an agent-led setup, [download the Sporo seed](https://a2ahub.dev/setup/a2a.md).
