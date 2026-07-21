package cache

import (
	"context"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

func TestBuildIndex_SimpleWorkRequestLifecycle(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})

	fx.commitArtifact("axon/exchanges/XW-axon-20260701-aaaa.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-aaaa", "type": "work_request",
		"title": "todo feed pagination", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(time.Now()), "priority": "p2", "blocking": false, "classification": "internal",
	}, "body")

	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": "XW-axon-20260701-aaaa", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base),
	})
	fx.commitEvent("seomatrix", fxULID(2), map[string]any{
		"subject": "XW-axon-20260701-aaaa", "transition": "acknowledge",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"},
		"at":    fxAt(base.Add(time.Hour)),
	})

	idx, err := buildIndex(context.Background(), "sp1", fx.dir, mustManifest(t, fx))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, "XW-axon-20260701-aaaa")
	if fa.Result.State != fold.StateAcknowledged {
		t.Fatalf("state = %q, want acknowledged", fa.Result.State)
	}
	if len(fa.Result.Flags) != 0 {
		t.Fatalf("unexpected flags: %+v", fa.Result.Flags)
	}
}

func TestBuildIndex_ParentResponseDisputeGather(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})

	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-bbbb.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-bbbb", "type": "work_request",
		"title": "wr", "space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitEvent("axon", fxULID(10), map[string]any{
		"subject": "XW-axon-20260701-bbbb", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base),
	})
	fx.commitEvent("seomatrix", fxULID(11), map[string]any{
		"subject": "XW-axon-20260701-bbbb", "transition": "acknowledge",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(time.Hour)),
	})
	fx.commitEvent("seomatrix", fxULID(12), map[string]any{
		"subject": "XW-axon-20260701-bbbb", "transition": "accept",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(2 * time.Hour)),
	})

	// Response + its paired "respond" event, co-committed (D-026) — this
	// package's correlation key (same commit) links the response
	// artifact to the parent's respond event.
	fx.commitArtifactAndEvent(
		"seomatrix/exchanges/XS-seomatrix-20260701-cccc.md",
		map[string]any{
			"schema": "envelope/v1", "id": "XS-seomatrix-20260701-cccc", "type": "response",
			"title": "resp", "space": "fixture-space", "from": "seomatrix", "to": []string{"axon"},
			"parent": "XW-axon-20260701-bbbb", "result": "answered",
			"actor": map[string]any{"kind": "agent", "name": "seo-bot"}, "created": fxAt(base.Add(3 * time.Hour)),
			"priority": "p2", "blocking": false, "classification": "internal",
		},
		"resp body",
		"seomatrix", fxULID(13),
		map[string]any{
			"subject": "XW-axon-20260701-bbbb", "transition": "respond",
			"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(3 * time.Hour)),
		},
	)

	// axon disputes the response — subject == response id (D-024).
	fx.commitEvent("axon", fxULID(14), map[string]any{
		"subject": "XS-seomatrix-20260701-cccc", "transition": "dispute",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base.Add(4 * time.Hour)),
	})

	idx, err := buildIndex(context.Background(), "sp1", fx.dir, mustManifest(t, fx))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	parent := findArtifact(t, idx, "XW-axon-20260701-bbbb")
	if parent.Result.State != fold.StateInProgress {
		t.Fatalf("parent state = %q, want in_progress (dispute reopen)", parent.Result.State)
	}
	respState, ok := parent.Result.Responses["XS-seomatrix-20260701-cccc"]
	if !ok || respState != fold.StateDisputed {
		t.Fatalf("parent.Result.Responses[response] = %q, ok=%v, want disputed", respState, ok)
	}

	resp := findArtifact(t, idx, "XS-seomatrix-20260701-cccc")
	if resp.Result.State != fold.StateDisputed {
		t.Fatalf("response's own state = %q, want disputed", resp.Result.State)
	}
}

func findArtifact(t *testing.T, idx []foldedArtifact, id string) foldedArtifact {
	t.Helper()
	for _, a := range idx {
		if a.Env.ID == id {
			return a
		}
	}
	t.Fatalf("artifact %q not found in index (%d items)", id, len(idx))
	return foldedArtifact{}
}
