package e2e

import (
	"context"
	"net/http"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

// TestHostLoopCheckStatusResolvesBothCheckNameShapes is P34's e2e tier
// (AC-940.5, as re-scoped at intake — see
// docs/features/v1-min-2026-07/plans/34-checkstatus-compound-context.plan.md).
//
// The PR is opened by the REAL binary through cmd/a2a/wire.go against the
// rig's fake host — real config load, real credential resolution, real mirror
// clone, real `git push`, real PR open — and the gate result is then read by
// the REAL GitHubHost over HTTP, decoding a real check-runs payload. Nothing
// about the selection is stubbed: the only synthetic part is which name the
// host serves, which is precisely the variable under test.
//
// It stops at the host boundary rather than driving a verb, because
// `CheckStatus` has no CLI consumer — no command waits for green (see the
// phase plan's intake finding and the docs/backlog.md row). When one ships,
// this test grows a verb-level twin and earns a coverage-manifest row; a row
// today would claim verb coverage that does not exist.
func TestHostLoopCheckStatusResolvesBothCheckNameShapes(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")
	// Leave the PR open: the gate result only matters while the PR is still
	// waiting on it.
	r.gh.AutoMerge = false

	id := "XQ-axon-20260724-c001"
	r.mustRun("submit", r.stageQuestion(id, "beta"))

	prs := r.gh.PRs()
	if len(prs) != 1 {
		t.Fatalf("PRs = %d, want 1 (host calls: %v)", len(prs), r.gh.Requests())
	}
	prNumber := prs[0].Number

	gh := host.NewGitHubHost(&http.Client{}, r.gh.URL)
	req := host.StatusRequest{
		Repo:       host.Repo{Owner: "fixture", Name: "space"},
		PRNumber:   prNumber,
		Credential: host.Credential{Token: "dummy-token"},
	}

	for _, tc := range []struct {
		name      string
		checkName string
	}{
		// A P33-migrated space: the caller job's run is compound-named.
		{name: "compound", checkName: "a2a-validate / validate"},
		// An un-migrated space in the same mixed fleet.
		{name: "flat", checkName: "a2a-validate"},
	} {
		r.gh.CheckName = tc.checkName
		got, err := gh.CheckStatus(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: CheckStatus: %v", tc.name, err)
		}
		if got.State != "completed" || got.Conclusion != "success" {
			t.Fatalf("%s: CheckStatus = %+v, want completed/success", tc.name, got)
		}
		if got.Name != tc.checkName {
			t.Fatalf("%s: CheckStatus.Name = %q, want %q", tc.name, got.Name, tc.checkName)
		}
	}

	// The post-merge audit shares the workflow file but is push-triggered and
	// is never the required check (spec 09 §5.5) — reading it as the gate
	// would report a green space write that the gate never approved.
	r.gh.CheckName = "a2a-postmerge-audit / validate"
	got, err := gh.CheckStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("postmerge-audit: CheckStatus: %v", err)
	}
	if got.State != "queued" || got.Name != "" {
		t.Fatalf("postmerge-audit: CheckStatus = %+v, want the no-check result", got)
	}
}
