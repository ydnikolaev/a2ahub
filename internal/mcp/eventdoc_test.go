package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
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

// --- resolveWriteSpace ------------------------------------------------

func TestResolveWriteSpaceNilResolverReturnsUnchanged(t *testing.T) {
	t.Parallel()

	deps := WriteDeps{MirrorDir: "/single-space/mirror", SpaceID: "only-space"}
	got, err := resolveWriteSpace(deps, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MirrorDir != deps.MirrorDir || got.SpaceID != deps.SpaceID {
		t.Fatalf("resolveWriteSpace(nil resolver) = %+v, want unchanged %+v", got, deps)
	}
}

func TestResolveWriteSpaceExplicitSpaceID(t *testing.T) {
	t.Parallel()

	want := WriteDeps{MirrorDir: "/space-a/mirror", SpaceID: "space-a"}
	deps := WriteDeps{
		ResolveSpace: func(spaceID string) (WriteDeps, error) {
			if spaceID != "space-a" {
				t.Fatalf("ResolveSpace called with %q, want %q", spaceID, "space-a")
			}
			return want, nil
		},
		SpaceOfArtifacts: func(ids []string) (string, error) {
			t.Fatalf("SpaceOfArtifacts must not be called when spaceID is explicit; got ids=%v", ids)
			return "", nil
		},
	}
	got, err := resolveWriteSpace(deps, "space-a", []string{"XQ-ignored"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MirrorDir != want.MirrorDir || got.SpaceID != want.SpaceID {
		t.Fatalf("resolveWriteSpace(explicit id) = %+v, want %+v", got, want)
	}
}

func TestResolveWriteSpaceDerivedFromArtifactIDs(t *testing.T) {
	t.Parallel()

	want := WriteDeps{MirrorDir: "/space-b/mirror", SpaceID: "space-b"}
	deps := WriteDeps{
		ResolveSpace: func(spaceID string) (WriteDeps, error) {
			if spaceID != "space-b" {
				t.Fatalf("ResolveSpace called with %q, want %q", spaceID, "space-b")
			}
			return want, nil
		},
		SpaceOfArtifacts: func(ids []string) (string, error) {
			if !slices.Equal(ids, []string{"XQ-beta-1"}) {
				t.Fatalf("SpaceOfArtifacts called with %v, want [XQ-beta-1]", ids)
			}
			return "space-b", nil
		},
	}
	got, err := resolveWriteSpace(deps, "", []string{"XQ-beta-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MirrorDir != want.MirrorDir || got.SpaceID != want.SpaceID {
		t.Fatalf("resolveWriteSpace(derived) = %+v, want %+v", got, want)
	}
}

func TestResolveWriteSpaceSplitBatchRefusedNamingBoth(t *testing.T) {
	t.Parallel()

	deps := WriteDeps{
		ResolveSpace: func(string) (WriteDeps, error) {
			t.Fatal("ResolveSpace must not be called once SpaceOfArtifacts refuses")
			return WriteDeps{}, nil
		},
		SpaceOfArtifacts: func(ids []string) (string, error) {
			return "", errors.New(`mcp: batch spans multiple spaces: a resolves to "space-a", b resolves to "space-b"`)
		},
	}
	_, err := resolveWriteSpace(deps, "", []string{"a", "b"})
	if err == nil {
		t.Fatal("expected a split-batch refusal")
	}
	for _, want := range []string{"space-a", "space-b"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("split-batch error = %v, want it to name %q", err, want)
		}
	}
}

func TestResolveWriteSpaceUnknownIDRefusedNamingConnectedSpaces(t *testing.T) {
	t.Parallel()

	deps := WriteDeps{
		ResolveSpace: func(string) (WriteDeps, error) {
			t.Fatal("ResolveSpace must not be called once SpaceOfArtifacts refuses")
			return WriteDeps{}, nil
		},
		SpaceOfArtifacts: func(ids []string) (string, error) {
			return "", errors.New("mcp: no connected space's mirror holds XQ-unknown; connected spaces are space-a, space-b")
		},
	}
	_, err := resolveWriteSpace(deps, "", []string{"XQ-unknown"})
	if err == nil {
		t.Fatal("expected an unknown-id refusal")
	}
	for _, want := range []string{"space-a", "space-b"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("unknown-id error = %v, want it to name %q", err, want)
		}
	}
}

func TestResolveWriteSpaceNoIDNoExplicitSpaceRefusedNamingConnectedSpaces(t *testing.T) {
	t.Parallel()

	deps := WriteDeps{
		ResolveSpace: func(spaceID string) (WriteDeps, error) {
			if spaceID != "" {
				t.Fatalf("ResolveSpace called with %q, want empty string (no id, no explicit space)", spaceID)
			}
			return WriteDeps{}, errors.New(
				"mcp: space is required when multiple spaces are connected; connected spaces are space-a, space-b")
		},
		SpaceOfArtifacts: func(ids []string) (string, error) {
			t.Fatalf("SpaceOfArtifacts must not be called with no ids; got %v", ids)
			return "", nil
		},
	}
	_, err := resolveWriteSpace(deps, "", nil)
	if err == nil {
		t.Fatal("expected a refusal naming the connected spaces")
	}
	for _, want := range []string{"space-a", "space-b"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("no-id/no-space error = %v, want it to name %q", err, want)
		}
	}
}
