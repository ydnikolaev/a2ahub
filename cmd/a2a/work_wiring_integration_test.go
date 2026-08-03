package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
	"github.com/ydnikolaev/a2ahub/testkit/fakegithub"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestWorkProductionWiringMutations drives every mutating work action through
// run -> buildCommands -> buildWorkCommand against a real throwaway Git space.
// It guards the production-DI seam that command-level tests with a fake
// WorkStarter/WorkProgressor cannot reach.
func TestWorkProductionWiringMutations(t *testing.T) {
	// reason: production path resolution reads process cwd/HOME and the funnel
	// consumes the build-time version; t.Chdir/t.Setenv therefore require a
	// serial test and the version override must not overlap another test.
	fixture := newWireFixture(t, "axon", "beta")
	activateWorkFloor(t, fixture)

	previousVersion := version
	version = "0.19.0"
	t.Cleanup(func() { version = previousVersion })

	github := fakegithub.New(t, fixture.OriginDir)
	t.Setenv("A2A_GITHUB_API", github.URL)

	const session = "session:production-wire"
	start := runWorkOperation(t, 0,
		"start", "--thread", e2eWorkThread, "--subject-ref", e2eWorkThread,
		"--mode", "implementing", "--summary", "production start",
		"--to", "beta", "--classification", "internal",
		"--actor-kind", "agent", "--actor-name", "e2e", "--session", session, "--json",
	)
	if start.WorkID == "" || start.Session != session || start.Action != workreport.ActionStart || start.SemanticSequence != 1 ||
		start.Local.State != workreport.LocalActive || start.Shared.Convergence != workreport.ConvergenceAccepted {
		t.Fatalf("work start result = %+v", start)
	}
	assertWorkStatus(t, start.WorkID, session, workreport.ModeImplementing, "production start", 1, 1, false)
	assertOriginWorkCheckpoints(t, fixture.OriginDir, start.WorkID, 1, "mode: implementing", "summary: production start")

	prsBeforeHeartbeat := len(github.PRs())
	heartbeat := runWorkOperation(t, 0,
		"heartbeat", "--work-id", start.WorkID, "--session", session, "--ttl", "20m", "--json",
	)
	if heartbeat.WorkID != start.WorkID || heartbeat.SemanticSequence != 1 ||
		heartbeat.Local.State != workreport.LocalActive || heartbeat.Shared.Attempted {
		t.Fatalf("work heartbeat result = %+v", heartbeat)
	}
	if got := len(github.PRs()); got != prsBeforeHeartbeat {
		t.Fatalf("heartbeat opened a PR: before=%d after=%d", prsBeforeHeartbeat, got)
	}
	assertWorkStatus(t, start.WorkID, session, workreport.ModeImplementing, "production start", 1, 2, false)
	assertOriginWorkCheckpoints(t, fixture.OriginDir, start.WorkID, 1, "mode: implementing")

	checkpoint := runWorkOperation(t, 0,
		"checkpoint", "--work-id", start.WorkID, "--session", session,
		"--mode", "testing", "--summary", "production progress", "--json",
	)
	if checkpoint.Action != workreport.ActionCheckpoint || checkpoint.SemanticSequence != 2 || checkpoint.Local.State != workreport.LocalActive ||
		checkpoint.Shared.Convergence != workreport.ConvergenceAccepted {
		t.Fatalf("work checkpoint result = %+v", checkpoint)
	}
	assertWorkStatus(t, start.WorkID, session, workreport.ModeTesting, "production progress", 2, 2, false)
	assertOriginWorkCheckpoints(t, fixture.OriginDir, start.WorkID, 2, "mode: testing", "summary: production progress")

	// Work publication intentionally has no fork fallback. Denying the real
	// origin therefore leaves the exact prepared wait operation resumable.
	github.DenyPushes("consumer")
	wait := runWorkOperation(t, 1,
		"wait", "--work-id", start.WorkID, "--session", session,
		"--summary", "production blocked", "--waiting-on", "system:beta:review", "--json",
	)
	if wait.SemanticSequence != 3 || wait.Action != workreport.ActionWait ||
		wait.Local.State != workreport.LocalActive || wait.Shared.Convergence != workreport.ConvergenceResumable ||
		wait.Shared.ErrorCode == "" {
		t.Fatalf("failed work wait result = %+v", wait)
	}
	assertWorkStatus(t, start.WorkID, session, workreport.ModeWaiting, "production blocked", 3, 2, true)
	assertOriginWorkCheckpoints(t, fixture.OriginDir, start.WorkID, 2, "mode: testing")

	removePushDenial(t, fixture.OriginDir)
	resumed := runWorkOperation(t, 0,
		"resume", "--work-id", start.WorkID, "--session", session, "--json",
	)
	if resumed.SemanticSequence != 3 || resumed.Action != workreport.ActionWait ||
		resumed.Local.State != workreport.LocalActive || resumed.Shared.Convergence != workreport.ConvergenceAccepted {
		t.Fatalf("work resume result = %+v", resumed)
	}
	assertWorkStatus(t, start.WorkID, session, workreport.ModeWaiting, "production blocked", 3, 2, false)
	assertOriginWorkCheckpoints(t, fixture.OriginDir, start.WorkID, 3, "mode: waiting", "summary: production blocked")

	stopped := runWorkOperation(t, 0,
		"stop", "--work-id", start.WorkID, "--session", session,
		"--result", "finished", "--summary", "production complete", "--json",
	)
	if stopped.SemanticSequence != 4 || stopped.Action != workreport.ActionStop ||
		stopped.Local.State != workreport.LocalClosing || stopped.Shared.Convergence != workreport.ConvergenceAccepted {
		t.Fatalf("work stop result = %+v", stopped)
	}
	assertWorkStatus(t, start.WorkID, session, workreport.ModeFinished, "production complete", 4, 2, true)
	assertOriginWorkCheckpoints(t, fixture.OriginDir, start.WorkID, 4, "mode: finished", "summary: production complete")
}

const (
	e2eWorkThread  = "thread:axon-20260721-k3f9"
	e2eWorkSubject = "XQ-axon-20260803-wire"
)

type productionWorkOperation struct {
	WorkID           string            `json:"work_id"`
	Session          string            `json:"session"`
	Action           workreport.Action `json:"action"`
	SemanticSequence uint64            `json:"semantic_sequence"`
	Local            struct {
		State     workreport.LocalState `json:"state"`
		ErrorCode string                `json:"error_code"`
	} `json:"local"`
	Shared struct {
		Attempted   bool                   `json:"attempted"`
		Convergence workreport.Convergence `json:"convergence"`
		ErrorCode   string                 `json:"error_code"`
	} `json:"shared"`
}

type productionWorkStatus struct {
	Items []struct {
		WorkID            string                 `json:"work_id"`
		Session           string                 `json:"session"`
		Mode              workreport.Mode        `json:"mode"`
		Summary           string                 `json:"summary"`
		HeartbeatSequence uint64                 `json:"heartbeat_sequence"`
		SemanticSequence  uint64                 `json:"semantic_sequence"`
		Pending           *productionPendingWork `json:"pending"`
	} `json:"items"`
}

type productionPendingWork struct {
	Action           workreport.Action `json:"action"`
	SemanticSequence uint64            `json:"semantic_sequence"`
}

func runWorkOperation(t *testing.T, wantCode int, args ...string) productionWorkOperation {
	t.Helper()
	var stdout, stderr strings.Builder
	code := run(append([]string{"work"}, args...), &stdout, &stderr)
	if code != wantCode {
		t.Fatalf("a2a work %v: exit=%d want=%d\nstdout: %s\nstderr: %s", args, code, wantCode, stdout.String(), stderr.String())
	}
	var result productionWorkOperation
	if err := json.Unmarshal([]byte(stdout.String()), &result); err != nil {
		t.Fatalf("decode a2a work %v output: %v\nstdout: %s\nstderr: %s", args, err, stdout.String(), stderr.String())
	}
	return result
}

func readWorkStatus(t *testing.T, workID string) productionWorkStatus {
	t.Helper()
	var stdout, stderr strings.Builder
	code := run([]string{"work", "status", "--work-id", workID, "--include-expired", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("a2a work status: exit=%d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	var status productionWorkStatus
	if err := json.Unmarshal([]byte(stdout.String()), &status); err != nil {
		t.Fatalf("decode work status: %v\nstdout: %s", err, stdout.String())
	}
	return status
}

func assertWorkStatus(t *testing.T, workID, session string, mode workreport.Mode, summary string, semantic, heartbeat uint64, pending bool) {
	t.Helper()
	status := readWorkStatus(t, workID)
	if len(status.Items) != 1 {
		t.Fatalf("work status items = %+v, want exactly one", status.Items)
	}
	item := status.Items[0]
	if item.WorkID != workID || item.Session != session || item.Mode != mode || item.Summary != summary ||
		item.SemanticSequence != semantic || item.HeartbeatSequence != heartbeat || (item.Pending != nil) != pending {
		t.Fatalf("work status item = %+v", item)
	}
	if pending {
		wantAction := workreport.ActionWait
		if mode == workreport.ModeFinished || mode == workreport.ModePaused {
			wantAction = workreport.ActionStop
		}
		if item.Pending.Action != wantAction || item.Pending.SemanticSequence != semantic {
			t.Fatalf("pending work status = %+v, want action=%s sequence=%d", item.Pending, wantAction, semantic)
		}
	}
}

func assertOriginWorkCheckpoints(t *testing.T, origin, workID string, want int, snippets ...string) {
	t.Helper()
	paths := strings.Fields(gitFixtureOutput(t, origin, "ls-tree", "-r", "--name-only", "main"))
	var matching []string
	for _, path := range paths {
		if !strings.HasPrefix(path, "axon/exchanges/") || !strings.HasSuffix(path, ".md") {
			continue
		}
		raw := gitFixtureOutput(t, origin, "show", "main:"+path)
		if strings.Contains(raw, workID) {
			matching = append(matching, raw)
		}
	}
	if len(matching) != want {
		t.Fatalf("origin work checkpoints = %d, want %d", len(matching), want)
	}
	joined := strings.Join(matching, "\n")
	for _, snippet := range snippets {
		if !strings.Contains(joined, snippet) {
			t.Errorf("origin work checkpoints do not contain %q\n%s", snippet, joined)
		}
	}
}

func activateWorkFloor(t *testing.T, fixture *wireFixture) {
	t.Helper()
	dir := t.TempDir()
	runGitFixture(t, "", "clone", fixture.OriginDir, dir)
	manifestPath := filepath.Join(dir, "space.yaml")
	updated := "schema: space/v1\n" +
		"space: fixture-space\n" +
		"min_binary_version: 0.19.0\n" +
		"participants:\n" +
		"  - system: axon\n    org: fixture\n    section: axon\n    owners: [axon-bot]\n    status: active\n    joined: \"2026-01-01\"\n" +
		"  - system: beta\n    org: fixture\n    section: beta\n    owners: [beta-bot]\n    status: active\n    joined: \"2026-01-01\"\n"
	if err := os.WriteFile(manifestPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("write work manifest: %v", err)
	}
	artifact := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + e2eWorkSubject + "\n" +
		"type: question\n" +
		"title: production work subject\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + e2eWorkThread + "\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-08-03T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	artifactPath := filepath.Join(dir, "axon", "exchanges", e2eWorkSubject+".md")
	if err := os.WriteFile(artifactPath, []byte(artifact), 0o644); err != nil {
		t.Fatalf("write work subject: %v", err)
	}
	runGitFixture(t, dir, "add", "space.yaml", "axon/exchanges/"+e2eWorkSubject+".md")
	runGitFixture(t, dir, "commit", "-m", "test: activate work feature floor")
	runGitFixture(t, dir, "push", "origin", "main")

	mirror := filepath.Join(fixture.ProjectRoot, ".a2a", "cache", "mirrors", fixture.SpaceID)
	runGitFixture(t, mirror, "fetch", "origin", "main")
	runGitFixture(t, mirror, "checkout", "-B", "main", "origin/main")
}

func removePushDenial(t *testing.T, repo string) {
	t.Helper()
	if err := os.Remove(filepath.Join(repo, "hooks", "pre-receive")); err != nil {
		t.Fatalf("remove push denial from %s: %v", repo, err)
	}
}

func gitFixtureOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%q): %v\n%s", args, dir, err, out)
	}
	return string(out)
}
