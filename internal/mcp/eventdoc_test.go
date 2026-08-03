package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

type partialResultFunnel struct {
	result space.WriteResult
	err    error
}

func (f partialResultFunnel) Submit(context.Context, space.SubmitRequest) (space.WriteResult, error) {
	return f.result, f.err
}

func TestWriteDepsSubmitPreservesPartialWriteResultOnError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("auto-merge arm failed")
	partial := space.WriteResult{
		Branch:          "a2a/beta/XQ-axon-20260803-partial",
		PRNumber:        42,
		PRURL:           "https://example.invalid/pr/42",
		CommitSHA:       "0123456789abcdef",
		State:           space.WriteStateNeedsAttention,
		Stage:           space.WriteStagePRCreated,
		ArtifactIDs:     []string{"XQ-axon-20260803-partial"},
		MergeMethod:     host.MergeMethodSquash,
		RemainingAction: space.RemainingActionResolvePR,
		Note:            "auto-merge is not armed",
	}
	deps := WriteDeps{Funnel: partialResultFunnel{result: partial, err: wantErr}}

	result, err := deps.submit(context.Background(), space.SubmitRequest{}, "ack", []string{"candidate-id"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("submit error = %v, want wrapped %v", err, wantErr)
	}
	got, ok := result.(submitResult)
	if !ok {
		t.Fatalf("submit result type = %T, want submitResult", result)
	}
	if got.Verb != "ack" || len(got.IDs) != 1 || got.IDs[0] != partial.ArtifactIDs[0] {
		t.Fatalf("submit identity = %+v, want funnel artifact identity", got)
	}
	if got.Branch != partial.Branch || got.PRNumber != partial.PRNumber || got.PRURL != partial.PRURL ||
		got.CommitSHA != partial.CommitSHA || got.State != string(partial.State) || got.Stage != string(partial.Stage) ||
		got.MergeMethod != string(partial.MergeMethod) || got.RemainingAction != string(partial.RemainingAction) ||
		got.Note != partial.Note {
		t.Fatalf("partial result was reduced: got %+v, want %+v", got, partial)
	}
}

func TestWriteDepsSubmitKeepsZeroResultErrorCompatibility(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("validation failed before write")
	deps := WriteDeps{Funnel: partialResultFunnel{err: wantErr}}
	result, err := deps.submit(context.Background(), space.SubmitRequest{}, "ack", []string{"candidate-id"})
	if result != nil {
		t.Fatalf("zero funnel result became structured output: %#v", result)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("submit error = %v, want wrapped %v", err, wantErr)
	}
}
