package feedback

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// validFeedbackYAML is a minimal, honestly-cleared bug report — schema +
// checks + status all green, the state Submit requires before it will
// touch the write funnel.
const validFeedbackYAML = `feedback: v1
id: fb-20260723-abc123
kind: bug
severity: major
title: "a2a sync reports clean but the mirror is stale"
summary: "after a fetch, sync printed clean but HEAD did not advance"
context:
  a2a_version: v0.1.1
  os_arch: darwin/arm64
  surface: cli
evidence:
  steps:
    - "push a new commit upstream"
    - "run a2a sync"
  expected: "mirror HEAD advances"
  actual: "mirror HEAD stays put"
checks:
  docs_consulted: true
  grounded_in_real_work: true
  not_space_specific: true
  no_sensitive_content: true
  duplicates_checked: true
status: new
`

func newTestSubmitter(t *testing.T, fx *spacefixture.Fixture, fakeHost *host.FakeHost, ledgerPath string) *Submitter {
	t.Helper()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	sub := NewSubmitter(funnel, ledgerPath, t.TempDir(), "test-repo", SubmitConfig{
		RemoteURL:  fx.RemoteURL(),
		Repo:       host.Repo{Owner: "a2ahub", Name: "a2ahub"},
		BaseBranch: "main",
	})
	// The fixture clone is already a real git working directory with an
	// initial commit (spacefixture.New's own seed commit) — reuse it
	// directly rather than re-cloning through space.CloneOrFetch, mirroring
	// internal/e2e's own TestT3Submit idiom (mirrorDir = fx.Clone(...)).
	mirrorDir := fx.Clone("feedback")
	sub.SetMirrorDirForTest(func(string, string) string { return mirrorDir })
	sub.SetCloneOrFetchForTest(func(context.Context, string, string) error { return nil })
	sub.SetClockForTest(func() time.Time { return time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC) })
	return sub
}

func TestSubmit_HappyPath(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "feedback")
	fakeHost := host.NewFakeHost()
	ledgerPath := filepath.Join(t.TempDir(), "ledger.yaml")
	sub := newTestSubmitter(t, fx, fakeHost, ledgerPath)

	path := filepath.Join(t.TempDir(), "fb-20260723-abc123.yaml")
	if err := os.WriteFile(path, []byte(validFeedbackYAML), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	result, err := sub.Submit(context.Background(), path)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.AlreadyOpen {
		t.Fatal("expected a fresh submit, not AlreadyOpen")
	}
	if result.Branch != "a2a/feedback/fb-20260723-abc123" {
		t.Fatalf("Branch = %q, want a2a/feedback/fb-20260723-abc123 (§11 A6)", result.Branch)
	}

	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one PushBranch + OpenPR call, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
	if fakeHost.Pushes[0].Branch != "a2a/feedback/fb-20260723-abc123" {
		t.Fatalf("push branch = %q, want a2a/feedback/fb-20260723-abc123", fakeHost.Pushes[0].Branch)
	}
	wantTitle := "feedback(bug): a2a sync reports clean but the mirror is stale"
	if fakeHost.Opens[0].Title != wantTitle {
		t.Fatalf("PR title = %q, want %q", fakeHost.Opens[0].Title, wantTitle)
	}
	if fakeHost.Opens[0].Head != "a2a/feedback/fb-20260723-abc123" {
		t.Fatalf("PR head = %q, want a2a/feedback/fb-20260723-abc123", fakeHost.Opens[0].Head)
	}

	items, err := ReadLedger(ledgerPath)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(items) != 1 || items[0].ID != "fb-20260723-abc123" || items[0].PRURL != result.PRURL {
		t.Fatalf("ledger = %+v, want one row for fb-20260723-abc123 with PRURL %s", items, result.PRURL)
	}

	// Idempotent resubmit: the SAME file, resubmitted, is a no-op — no
	// second push/open, FakeHost's own FindPRByHeadBranch short-circuit
	// fires (default behavior: looks up a prior OpenPR by branch name).
	result2, err := sub.Submit(context.Background(), path)
	if err != nil {
		t.Fatalf("Submit (retry): %v", err)
	}
	if !result2.AlreadyOpen {
		t.Fatal("expected the retry to report AlreadyOpen")
	}
	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("expected STILL exactly one push/open after the retry, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
	items, err = ReadLedger(ledgerPath)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected the ledger to still have exactly one row after the idempotent retry, got %+v", items)
	}
}

func TestSubmit_RefusesRed(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "feedback")
	fakeHost := host.NewFakeHost()
	sub := newTestSubmitter(t, fx, fakeHost, filepath.Join(t.TempDir(), "ledger.yaml"))

	red := `feedback: v1
id: fb-20260723-red000
kind: docs
severity: minor
title: "an otherwise fine report with a false gate"
summary: "checks.docs_consulted is false"
context:
  a2a_version: v0.1.1
  os_arch: darwin/arm64
  surface: docs
checks:
  docs_consulted: false
  grounded_in_real_work: true
  not_space_specific: true
  no_sensitive_content: true
  duplicates_checked: true
status: new
`
	path := filepath.Join(t.TempDir(), "fb-20260723-red000.yaml")
	if err := os.WriteFile(path, []byte(red), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	if _, err := sub.Submit(context.Background(), path); err == nil {
		t.Fatal("expected Submit to refuse a red (checks-false) report")
	}
	if len(fakeHost.Pushes) != 0 || len(fakeHost.Opens) != 0 {
		t.Fatalf("expected no funnel call for a refused submit, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
}
