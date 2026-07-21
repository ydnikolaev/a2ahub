# Known-pattern fixture: GitHub personal access token

Body content that would cross the space boundary and MUST be blocked by the
secret scanner (§10.4, §13.4):

```
GITHUB_TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a
```

(fabricated token — matches GitHub's PAT shape `ghp_[A-Za-z0-9]{36}`, not a
live credential.)
