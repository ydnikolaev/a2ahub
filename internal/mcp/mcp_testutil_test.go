package mcp

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
)

// fakeFunnel is a hand-written Funnel test double (mirrors internal/cli's
// own fakeLifecycleFunnel idiom).
type fakeFunnel struct {
	calls  []space.SubmitRequest
	result space.WriteResult
	err    error
}

func (f *fakeFunnel) Submit(_ context.Context, req space.SubmitRequest) (space.WriteResult, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return space.WriteResult{}, f.err
	}
	if f.result.State == "" {
		return space.WriteResult{State: space.WriteStatePendingMerge, PRNumber: len(f.calls), PRURL: "https://example.invalid/pr/x", Branch: req.ArtifactID}, nil
	}
	return f.result, nil
}

func testManifest() space.Manifest {
	return space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"},
		{System: "beta", Status: "active"},
	}}
}

func testHostConfig() SubmitHostConfig {
	return SubmitHostConfig{
		RemoteURL: "https://example.invalid/org/space.git", Repo: host.Repo{Owner: "org", Name: "space"},
		BaseBranch: "main", Credential: host.Credential{Token: "test-token"},
		CommitAuthorName: "a2a-beta", CommitAuthorEmail: "a2a-beta@a2ahub.invalid",
	}
}

func fixedActorResolver(kind, name string) func(ActorInput) template.Actor {
	return func(ActorInput) template.Actor { return template.Actor{Kind: kind, Name: name} }
}

// repeatingReader is an unlimited io.Reader over a fixed byte pattern —
// tests that mint many ULIDs/exchange ids in one run never hit EOF the
// way a bounded strings.Reader would.
type repeatingReader struct{ pattern []byte }

func (r repeatingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.pattern[i%len(r.pattern)]
	}
	return len(p), nil
}

func testWriteDeps(mirrorDir string, funnel Funnel) WriteDeps {
	return WriteDeps{
		Funnel: funnel, MirrorDir: mirrorDir, SpaceID: "fixture-space", OwnSystem: "beta",
		Manifest: testManifest(), HostCfg: testHostConfig(), ResolveActor: fixedActorResolver("agent", "bot"),
		Now:     func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) },
		Entropy: repeatingReader{pattern: []byte("0123456789abcdef")}, ReadFile: os.ReadFile,
	}
}

// writeFileAllDirs writes content to full, creating parent directories as
// needed — used to materialize a fakeFunnel's recorded FileWrites onto
// disk so a follow-up call in the same test can read them back (a fake
// funnel records the call but never touches disk, unlike a real one).
func writeFileAllDirs(full string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, content, 0o644)
}

func writeMirrorFile(t *testing.T, mirrorDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(mirrorDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("writeMirrorFile: mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writeMirrorFile: write %s: %v", full, err)
	}
}

func writeQuestionArtifact(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + to + "]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

// writeLifecycleEvent seeds a pre-existing committed event, ordered
// strictly before any event a command under test mints at the fixed
// 2026-07-21 clock (mirrors internal/cli's own helper).
func writeLifecycleEvent(t *testing.T, mirrorDir, actingSystem string, seq int, subject, transition, actorSystem string) {
	t.Helper()
	id, err := artifact.MintULIDAt(time.Date(2020, 1, 1, 0, 0, seq, 0, time.UTC), rand.Reader)
	if err != nil {
		t.Fatalf("writeLifecycleEvent: mint ulid: %v", err)
	}
	content := fmt.Sprintf(
		"schema: event/v1\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\nat: 2020-01-01T00:00:00Z\n",
		id.String(), subject, transition, actorSystem)
	writeMirrorFile(t, mirrorDir, actingSystem+"/events/2020/"+id.String()+".yaml", content)
}
