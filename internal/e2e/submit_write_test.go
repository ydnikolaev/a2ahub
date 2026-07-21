package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// writeQuestionDraft writes a staged (uncommitted) question draft, the
// same shape internal/cli's cmd_submit_test.go uses.
func writeQuestionDraft(t *testing.T, dir, id, from, to string) string {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: " + from + "\n" +
		"to: [" + to + "]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}
	return path
}

// TestT3Submit is OP-205 `a2a submit`'s end-to-end round trip: real V2
// validation (internal/validate's real Engine, not a stub) + real
// space.WriteFunnel + host.NewFakeHost + a spacefixture clone — the
// cmd_submit_test.go:200-245 idiom, this package's own copy.
func TestT3Submit(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("axon")
	stagingDir := t.TempDir()
	path := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-t001", "axon", "beta")

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", e2eManifest())
	resolver := cli.NewMirrorResolver(mirrorDir, e2eManifest())
	submitValidator := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, submitValidator, "0.1.0")
	hostCfg := e2eHostConfig("axon", fx.RemoteURL())

	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, hostCfg)
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "opened PR") {
		t.Fatalf("expected an 'opened PR' message; got %q", out.String())
	}
	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one real PushBranch + OpenPR call, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}

	// AC-301.1 idempotent re-run: the SAME draft, resubmitted, is a no-op —
	// the committed event from the first submit is now on disk (real
	// funnel), so the local "already submitted" short-circuit fires before
	// the funnel is reached a second time.
	io2, out2, _ := newIO()
	code2 := cmd.Run(context.Background(), []string{path}, io2)
	if code2 != 0 {
		t.Fatalf("retry: code = %d, want 0 (idempotent no-op); stdout=%s", code2, out2.String())
	}
	if !strings.Contains(out2.String(), "already submitted") {
		t.Fatalf("expected the retry's stdout to report the already-submitted idempotent path, got %q", out2.String())
	}
	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("expected STILL exactly one push/open after the retry, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
}
