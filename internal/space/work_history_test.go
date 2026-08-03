package space

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

type countingWorkPublisher struct{ calls int }

type fixedWorkLeaseLister struct{ leases []workreport.Lease }

func (l fixedWorkLeaseLister) ListWork(context.Context, string, string, string, bool) ([]workreport.Lease, error) {
	return l.leases, nil
}

func (p *countingWorkPublisher) SubmitPrepared(context.Context, workreport.PreparedJournal, workreport.WriteResultJournal) (workreport.PublishAttempt, error) {
	p.calls++
	return workreport.PublishAttempt{}, nil
}

func TestWorkHistoryPublisherConvergesExactMainBytesWithoutDelegate(t *testing.T) {
	t.Parallel()
	prepared, journal := testWorkPreparedJournal(t)
	repo := newContractHistoryRepo(t)
	for _, mutation := range prepared.Mutations() {
		path := filepath.Join(repo, filepath.FromSlash(mutation.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, mutation.Bytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	contractTestGit(t, repo, "add", "--", ".")
	contractTestGit(t, repo, "commit", "-q", "-m", "land exact work checkpoint\n\nA2A-Operation-Key: "+prepared.OperationKey())
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")

	delegate := &countingWorkPublisher{}
	publisher, err := NewWorkHistoryPublisher(repo, delegate)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := publisher.SubmitPrepared(t.Context(), journal, workreport.WriteResultJournal{})
	if err != nil {
		t.Fatal(err)
	}
	if delegate.calls != 0 || attempt.Convergence != workreport.ConvergenceTerminal || !attempt.Attempted {
		t.Fatalf("delegate=%d attempt=%+v", delegate.calls, attempt)
	}
	result, err := DecodeWriteResultJournal(attempt.WriteResult.Bytes())
	if err != nil || result.State != WriteStateAlreadyMerged || result.Stage != WriteStageMerged {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestWorkHistoryPublisherDelegatesWithoutExactSingleCommitProvenance(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		split       bool
		preexisting bool
		key         string
	}{
		{name: "wrong operation key", key: "op-v1-" + strings.Repeat("f", 64)},
		{name: "artifact and event introduced separately", split: true},
		{name: "same bytes predate claimed provenance", preexisting: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared, journal := testWorkPreparedJournal(t)
			repo := newContractHistoryRepo(t)
			key := test.key
			if key == "" {
				key = prepared.OperationKey()
			}
			if test.preexisting {
				mutation := prepared.Mutations()[0]
				path := filepath.Join(repo, filepath.FromSlash(mutation.Path))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, mutation.Bytes, 0o644); err != nil {
					t.Fatal(err)
				}
				contractTestGit(t, repo, "add", "--", mutation.Path)
				contractTestGit(t, repo, "commit", "-q", "-m", "preexisting bytes")
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for index, mutation := range prepared.Mutations() {
				path := filepath.Join(repo, filepath.FromSlash(mutation.Path))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, mutation.Bytes, 0o644); err != nil {
					t.Fatal(err)
				}
				contractTestGit(t, repo, "add", "--", filepath.ToSlash(mutation.Path))
				if test.split || index == len(prepared.Mutations())-1 {
					contractTestGit(t, repo, "commit", "-q", "-m", "land bytes\n\nA2A-Operation-Key: "+key)
				}
			}
			contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")
			delegate := &countingWorkPublisher{}
			publisher, err := NewWorkHistoryPublisher(repo, delegate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := publisher.SubmitPrepared(t.Context(), journal, workreport.WriteResultJournal{}); err != nil {
				t.Fatal(err)
			}
			if delegate.calls != 1 {
				t.Fatalf("delegate calls=%d", delegate.calls)
			}
		})
	}
}

func TestWorkHistoryPublisherDelegatesWhenAnyMainByteDiffers(t *testing.T) {
	t.Parallel()
	prepared, journal := testWorkPreparedJournal(t)
	repo := newContractHistoryRepo(t)
	mutations := prepared.Mutations()
	for index, mutation := range mutations {
		path := filepath.Join(repo, filepath.FromSlash(mutation.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		content := mutation.Bytes
		if index == 0 {
			content = []byte("different")
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	contractTestGit(t, repo, "add", "--", ".")
	contractTestGit(t, repo, "commit", "-q", "-m", "land different bytes")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")

	delegate := &countingWorkPublisher{}
	publisher, err := NewWorkHistoryPublisher(repo, delegate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.SubmitPrepared(t.Context(), journal, workreport.WriteResultJournal{}); err != nil {
		t.Fatal(err)
	}
	if delegate.calls != 1 {
		t.Fatalf("delegate calls = %d", delegate.calls)
	}
}

func TestWorkLeaseContinuationResolverReadsOnlyReceiptBoundCommitAndArtifact(t *testing.T) {
	t.Parallel()
	repo := newContractHistoryRepo(t)
	artifactID := "XA-axon-20260803-b3c4"
	path := filepath.Join(repo, "axon", "exchanges", artifactID+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(workValidatorArtifact), 0o644); err != nil {
		t.Fatal(err)
	}
	contractTestGit(t, repo, "add", "--", ".")
	contractTestGit(t, repo, "commit", "-q", "-m", "accepted work checkpoint")
	commit := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD"))
	result, err := finalizeWriteResult(WriteResult{
		ArtifactIDs: []string{artifactID}, Branch: "a2a/axon/work", CommitSHA: commit,
		Stage: WriteStageAutoMergeArmed, State: WriteStatePendingMerge,
	})
	if err != nil {
		t.Fatal(err)
	}
	resultRaw, err := EncodeWriteResultJournal(result)
	if err != nil {
		t.Fatal(err)
	}
	resultJournal, err := workreport.NewWriteResultJournal(resultRaw)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewWorkLeaseContinuationResolver(fixedWorkLeaseLister{leases: []workreport.Lease{{
		Identity:   workreport.Identity{ProjectID: "project", Space: "fixture-space", WorkID: "work:01K20ABCDEFHJKMNPQRSTVWXYZ", Actor: workreport.Actor{System: "axon"}},
		LastResult: &workreport.PublicationReceipt{ArtifactID: artifactID, SemanticSequence: 1, WriteResult: resultJournal},
	}}}, "project", "fixture-space", repo)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := resolver.ResolveWorkContinuation(t.Context(), "work:01K20ABCDEFHJKMNPQRSTVWXYZ", 1)
	if err != nil || !found || got.ArtifactID != artifactID || string(got.Artifact) != workValidatorArtifact {
		t.Fatalf("continuation=%+v found=%v err=%v", got, found, err)
	}
}

var _ workreport.Publisher = (*countingWorkPublisher)(nil)
var _ WorkLeaseLister = fixedWorkLeaseLister{}
