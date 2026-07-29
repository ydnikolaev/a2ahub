package livee2e

import (
	"net/http"
	"time"
)

// githubRequestTimeout bounds one GitHub round trip made by the live harness.
// Poll ceilings bound how long a condition may remain false; they do not help
// when a single HTTP request never returns. Keep the transport bound separate
// so every client used by the harness shares the same fail-closed floor.
const githubRequestTimeout = 30 * time.Second

func newBoundedGitHubHTTPClient() *http.Client {
	return &http.Client{Timeout: githubRequestTimeout}
}
