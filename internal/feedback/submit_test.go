package feedback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

type submitFunnelFunc func(context.Context, space.SubmitRequest) (space.WriteResult, error)

func (f submitFunnelFunc) Submit(ctx context.Context, req space.SubmitRequest) (space.WriteResult, error) {
	return f(ctx, req)
}

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
	funnel := space.NewWriteFunnel(fakeHost, nil, testBuild("0.1.0"))
	sub := NewSubmitter(funnel, ledgerPath, t.TempDir(), "test-repo", SubmitConfig{
		RemoteURL:  fx.RemoteURL(),
		Repo:       host.Repo{Owner: "a2ahub", Name: "a2ahub"},
		BaseBranch: "main",
	})
	// The fixture clone is already a real git working directory with an
	// initial commit (spacefixture.New's own seed commit) — reuse it
	// directly rather than re-cloning through space.CloneOrFetch, mirroring
	// internal/e2e's own TestSubmitDirectConstruction idiom
	// (mirrorDir = fx.Clone(...)).
	mirrorDir := fx.Clone("feedback")
	sub.SetMirrorDirForTest(func(string, string) string { return mirrorDir })
	sub.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return nil })
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
	if result.Branch != "a2a/feedback/submit/fb-20260723-abc123" {
		t.Fatalf("Branch = %q, want a2a/feedback/fb-20260723-abc123 (§11 A6)", result.Branch)
	}

	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one PushBranch + OpenPR call, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
	if len(fakeHost.Reads) != 1 {
		t.Fatalf("expected one orphan-branch observation before push, got %d", len(fakeHost.Reads))
	}
	if fakeHost.Pushes[0].Branch != "a2a/feedback/submit/fb-20260723-abc123" {
		t.Fatalf("push branch = %q, want a2a/feedback/fb-20260723-abc123", fakeHost.Pushes[0].Branch)
	}
	wantTitle := "feedback(bug): a2a sync reports clean but the mirror is stale"
	if fakeHost.Opens[0].Title != wantTitle {
		t.Fatalf("PR title = %q, want %q", fakeHost.Opens[0].Title, wantTitle)
	}
	if fakeHost.Opens[0].Head != "a2a/feedback/submit/fb-20260723-abc123" {
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

func TestSubmit_GitHubWithoutCredentialRefusesBeforeGit(t *testing.T) {
	t.Parallel()

	fakeHost := host.NewFakeHost()
	sub := NewSubmitter(
		space.NewWriteFunnel(fakeHost, nil, testBuild("0.13.0")),
		filepath.Join(t.TempDir(), "ledger.yaml"),
		t.TempDir(),
		"ydnikolaev-a2ahub",
		SubmitConfig{
			RemoteURL:  "https://github.com/ydnikolaev/a2ahub",
			Repo:       host.Repo{Owner: "ydnikolaev", Name: "a2ahub"},
			BaseBranch: "main",
		},
	)
	cloneCalls := 0
	sub.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error {
		cloneCalls++
		return nil
	})
	path := filepath.Join(t.TempDir(), "fb-20260723-abc123.yaml")
	if err := os.WriteFile(path, []byte(validFeedbackYAML), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	_, err := sub.Submit(context.Background(), path)
	if err == nil {
		t.Fatal("Submit succeeded without a GitHub credential")
	}
	for _, source := range []string{"A2A_FEEDBACK_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if !strings.Contains(err.Error(), source) {
			t.Errorf("error %q does not name %s", err, source)
		}
	}
	if cloneCalls != 0 || len(fakeHost.Pushes) != 0 || len(fakeHost.Opens) != 0 {
		t.Fatalf("credential refusal performed writes: clone=%d push=%d open=%d", cloneCalls, len(fakeHost.Pushes), len(fakeHost.Opens))
	}
}

func TestSubmit_PreservesPartialPRAndRecordsLedger(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("auto-merge arm refused")
	partial := space.WriteResult{
		Branch: "a2a/feedback/submit/fb-20260723-abc123", PRNumber: 42,
		PRURL: "https://example.test/pr/42", Stage: space.WriteStagePRCreated,
		State: space.WriteStateNeedsAttention, RemainingAction: space.RemainingActionResolvePR,
		Note: "arm auto-merge manually",
	}
	ledgerPath := filepath.Join(t.TempDir(), "ledger.yaml")
	sub := NewSubmitter(submitFunnelFunc(func(context.Context, space.SubmitRequest) (space.WriteResult, error) {
		return partial, wantErr
	}), ledgerPath, t.TempDir(), "test-repo", SubmitConfig{RemoteURL: t.TempDir(), BaseBranch: "feedback-hub"})
	sub.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return nil })
	path := filepath.Join(t.TempDir(), "fb-20260723-abc123.yaml")
	if err := os.WriteFile(path, []byte(validFeedbackYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := sub.Submit(context.Background(), path)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Submit error = %v, want wrapped %v", err, wantErr)
	}
	if got.PRNumber != 42 || got.PRURL != partial.PRURL || got.Stage != partial.Stage ||
		got.State != partial.State || got.RemainingAction != partial.RemainingAction ||
		got.Note != partial.Note || !got.LedgerRecorded || got.ErrorCode != ErrorCodeSharedWrite {
		t.Fatalf("partial result was collapsed or reinterpreted: %+v", got)
	}
	items, readErr := ReadLedger(ledgerPath)
	if readErr != nil || len(items) != 1 || items[0].PRURL != partial.PRURL {
		t.Fatalf("confirmed partial PR was not ledgered once: items=%+v err=%v", items, readErr)
	}
}

func TestSubmit_DoesNotLedgerUnconfirmedOutcome(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("push acknowledgement unknown")
	ledgerPath := filepath.Join(t.TempDir(), "ledger.yaml")
	sub := NewSubmitter(submitFunnelFunc(func(context.Context, space.SubmitRequest) (space.WriteResult, error) {
		return space.WriteResult{
			Branch: "a2a/feedback/submit/fb-20260723-abc123", Stage: space.WriteStagePushed,
			State: space.WriteStateOutcomeUnknown, RemainingAction: space.RemainingActionObserveProviderOutcome,
		}, wantErr
	}), ledgerPath, t.TempDir(), "test-repo", SubmitConfig{RemoteURL: t.TempDir(), BaseBranch: "feedback-hub"})
	sub.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return nil })
	path := filepath.Join(t.TempDir(), "fb-20260723-abc123.yaml")
	if err := os.WriteFile(path, []byte(validFeedbackYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := sub.Submit(context.Background(), path)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Submit error = %v, want wrapped %v", err, wantErr)
	}
	if got.LedgerRecorded || got.PRNumber != 0 || got.Stage != space.WriteStagePushed {
		t.Fatalf("unconfirmed outcome was overclaimed: %+v", got)
	}
	if items, readErr := ReadLedger(ledgerPath); readErr != nil || len(items) != 0 {
		t.Fatalf("unconfirmed outcome wrote ledger: items=%+v err=%v", items, readErr)
	}
}

func TestSubmit_LedgerFailureReturnsConfirmedPROutcome(t *testing.T) {
	t.Parallel()
	ledgerErr := errors.New("disk full")
	confirmed := space.WriteResult{
		Branch: "a2a/feedback/submit/fb-20260723-abc123", PRNumber: 42,
		PRURL: "https://example.test/pr/42", Stage: space.WriteStageAutoMergeArmed,
		State: space.WriteStatePendingMerge, RemainingAction: space.RemainingActionWaitForGates,
	}
	sub := NewSubmitter(submitFunnelFunc(func(context.Context, space.SubmitRequest) (space.WriteResult, error) {
		return confirmed, nil
	}), filepath.Join(t.TempDir(), "ledger.yaml"), t.TempDir(), "test-repo", SubmitConfig{RemoteURL: t.TempDir(), BaseBranch: "feedback-hub"})
	sub.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return nil })
	sub.SetAppendLedgerForTest(func(string, LedgerItem) error { return ledgerErr })
	path := filepath.Join(t.TempDir(), "fb-20260723-abc123.yaml")
	if err := os.WriteFile(path, []byte(validFeedbackYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := sub.Submit(context.Background(), path)
	var typed *LedgerRecordError
	if !errors.As(err, &typed) || !errors.Is(err, ledgerErr) {
		t.Fatalf("Submit error = %v, want LedgerRecordError wrapping %v", err, ledgerErr)
	}
	if got.PRNumber != 42 || got.PRURL != confirmed.PRURL || got.LedgerRecorded ||
		got.ErrorCode != ErrorCodeLocalLedger || got.Error != "local ledger was not recorded" {
		t.Fatalf("ledger failure erased or leaked into confirmed PR outcome: %+v", got)
	}
}

// TestSubmitOfAFiledIDSendsOnlyTheFiledBytes is spec 08 §11 "Delivery sends the
// filed bytes" and US-6 on the hub route: once an id is filed to the local inbox
// and not yet delivered, Submit and ValidatePath of an EXPLICIT draft path
// accept it only when its normalized record is byte-for-byte the filed copy. An
// edited draft is refused with InboxConflictError / ErrorCodeInboxConflict
// before any network work; a draft that differs only by comments is not a
// conflict, because the inbox holds the normalized record; and a row whose
// inbox copy is gone leaves the draft deliverable, which is the remedy
// `submit --all` prints for exactly that case.
func TestSubmitOfAFiledIDSendsOnlyTheFiledBytes(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (inboxRig, string, string, *int) {
		t.Helper()
		rig := newInboxRig(t)
		path := writeInboxDraft(t, "# a skeleton comment the record must not carry\n"+validFeedbackYAML)
		filed, err := rig.sub.FileToInbox(context.Background(), path)
		if err != nil {
			t.Fatalf("FileToInbox: %v", err)
		}
		rig.sub.cfg.Credential.Token = "dummy-token"
		clones := 0
		rig.sub.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error {
			clones++
			return errors.New("test stops at the clone: the pre-network checks passed")
		})
		return rig, path, filed.Inbox, &clones
	}

	t.Run("an edited draft is refused before any network work", func(t *testing.T) {
		t.Parallel()
		rig, path, inbox, clones := setup(t)
		edited := strings.Replace(validFeedbackYAML, "mirror HEAD stays put", "mirror HEAD moved backwards", 1)
		if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}

		var conflict *InboxConflictError
		if err := rig.sub.ValidatePath(path); !errors.As(err, &conflict) {
			t.Fatalf("ValidatePath = %v, want *InboxConflictError", err)
		}
		if conflict.ID != "fb-20260723-abc123" || conflict.Inbox != inbox || conflict.Path != path {
			t.Fatalf("conflict = %+v, want id, inbox %q and draft %q", conflict, inbox, path)
		}
		result, err := rig.sub.Submit(context.Background(), path)
		if !errors.As(err, &conflict) {
			t.Fatalf("Submit = %+v, %v, want *InboxConflictError", result, err)
		}
		if result.ErrorCode != ErrorCodeInboxConflict || result.Inbox != inbox || result.PRURL != "" {
			t.Fatalf("result = %+v, want inbox-conflict naming %q and no PR", result, inbox)
		}
		for _, want := range []string{inbox, path, "a2a feedback new"} {
			if !strings.Contains(result.Error, want) {
				t.Fatalf("result.Error %q must name %q", result.Error, want)
			}
		}
		if *clones != 0 {
			t.Fatalf("an inbox conflict reached the clone %d time(s)", *clones)
		}
		if got, err := os.ReadFile(inbox); err != nil || string(got) != validFeedbackYAML {
			t.Fatalf("the filed copy changed: %q, %v", got, err)
		}
	})

	t.Run("the unedited, still commented draft is the filed record", func(t *testing.T) {
		t.Parallel()
		rig, path, inbox, clones := setup(t)
		if err := rig.sub.ValidatePath(path); err != nil {
			t.Fatalf("ValidatePath of the unedited draft = %v, want nil", err)
		}
		if err := rig.sub.ValidatePath(inbox); err != nil {
			t.Fatalf("ValidatePath of the filed copy itself (what --all sends) = %v, want nil", err)
		}
		if _, err := rig.sub.Submit(context.Background(), path); err == nil || *clones != 1 {
			t.Fatalf("Submit = %v after %d clone(s), want it to pass the checks and stop at the clone", err, *clones)
		}
	})

	t.Run("a gone inbox copy leaves the draft deliverable", func(t *testing.T) {
		t.Parallel()
		rig, path, inbox, _ := setup(t)
		if err := os.Remove(inbox); err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(validFeedbackYAML, "mirror HEAD stays put", "mirror HEAD moved backwards", 1)
		if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := rig.sub.ValidatePath(path); err != nil {
			t.Fatalf("ValidatePath with the inbox copy gone = %v, want nil", err)
		}
	})
}
