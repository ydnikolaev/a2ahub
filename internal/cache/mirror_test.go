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

	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "acme", mustManifest(t, fx))
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

	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "acme", mustManifest(t, fx))
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

// TestBuildIndex_ParentDisputeReopenFailedIsCarriedToTheResponse pins spec
// 06's 2026-08-09 amendment (the known partial recorded 2026-08-10): when a
// dispute event's own attempt to reopen its parent to `in_progress` is
// itself illegal (fold.go's D-024 comment: the parent was not `responded`
// at the time — here, already closed), the RESPONSE artifact's own
// ParentDisputeReopenFailed must read true, resolved from the PARENT's own
// Result.Flags because the response's own independent fold never sees the
// dispute event at all (gatherEvents excludes it from the response's own
// stream).
func TestBuildIndex_ParentDisputeReopenFailedIsCarriedToTheResponse(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})

	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-dddd.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-dddd", "type": "work_request",
		"title": "wr", "space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitEvent("axon", fxULID(20), map[string]any{
		"subject": "XW-axon-20260701-dddd", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base),
	})
	fx.commitEvent("seomatrix", fxULID(21), map[string]any{
		"subject": "XW-axon-20260701-dddd", "transition": "acknowledge",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(time.Hour)),
	})

	// Response + its paired "respond" event, co-committed (D-026) — puts
	// the parent at `responded` and the response at `submitted`.
	fx.commitArtifactAndEvent(
		"seomatrix/exchanges/XS-seomatrix-20260701-eeee.md",
		map[string]any{
			"schema": "envelope/v1", "id": "XS-seomatrix-20260701-eeee", "type": "response",
			"title": "resp", "space": "fixture-space", "from": "seomatrix", "to": []string{"axon"},
			"parent": "XW-axon-20260701-dddd", "result": "answered",
			"actor": map[string]any{"kind": "agent", "name": "seo-bot"}, "created": fxAt(base.Add(2 * time.Hour)),
			"priority": "p2", "blocking": false, "classification": "internal",
		},
		"resp body",
		"seomatrix", fxULID(22),
		map[string]any{
			"subject": "XW-axon-20260701-dddd", "transition": "respond",
			"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(2 * time.Hour)),
		},
	)

	// axon closes the work_request BEFORE disputing the response — the
	// parent is no longer `responded` when the dispute lands, so D-024's
	// reopen is itself illegal.
	fx.commitEvent("axon", fxULID(23), map[string]any{
		"subject": "XW-axon-20260701-dddd", "transition": "close",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base.Add(3 * time.Hour)),
	})
	fx.commitEvent("axon", fxULID(24), map[string]any{
		"subject": "XS-seomatrix-20260701-eeee", "transition": "dispute",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base.Add(4 * time.Hour)),
	})

	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "acme", mustManifest(t, fx))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}

	resp := findArtifact(t, idx, "XS-seomatrix-20260701-eeee")
	if resp.Result.State != fold.StateDisputed {
		t.Fatalf("response state = %q, want disputed", resp.Result.State)
	}
	if !resp.ParentDisputeReopenFailed {
		t.Fatal("ParentDisputeReopenFailed = false, want true: the parent was `closed`, not `responded`, when the dispute landed, so D-024's reopen was itself illegal")
	}

	parent := findArtifact(t, idx, "XW-axon-20260701-dddd")
	if parent.Result.State != fold.StateClosed {
		t.Fatalf("parent state = %q, want closed (the dispute's reopen must NOT have moved it back to in_progress)", parent.Result.State)
	}
}

// TestBuildIndex_DeliveryUnresolvableWhenNoHandoffFulfillsTheDelivery pins
// AC9's read half (spec 06 §8's 2026-08-09 amendment, from
// fb-20260808-d5740f): a response claiming `result: delivered` for a
// work_request, with NO handoff in the mirror naming that work_request in
// its own `fulfills[]` at all — the "payload PR stayed open and red" shape
// — must leave the work_request's own DeliveryUnresolvable true.
func TestBuildIndex_DeliveryUnresolvableWhenNoHandoffFulfillsTheDelivery(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})

	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-ffff.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-ffff", "type": "work_request",
		"title": "wr", "space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitEvent("axon", fxULID(30), map[string]any{
		"subject": "XW-axon-20260701-ffff", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base),
	})
	fx.commitEvent("seomatrix", fxULID(31), map[string]any{
		"subject": "XW-axon-20260701-ffff", "transition": "acknowledge",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(time.Hour)),
	})

	// The response claims `delivered` and the parent's respond event lands
	// (D-026 co-commit) — but the payload PR (the handoff + package) never
	// merged, so nothing in this mirror names XW-axon-20260701-ffff in a
	// `fulfills[]` at all.
	fx.commitArtifactAndEvent(
		"seomatrix/exchanges/XS-seomatrix-20260701-gggg.md",
		map[string]any{
			"schema": "envelope/v1", "id": "XS-seomatrix-20260701-gggg", "type": "response",
			"title": "resp", "space": "fixture-space", "from": "seomatrix", "to": []string{"axon"},
			"parent": "XW-axon-20260701-ffff", "result": "delivered",
			"actor": map[string]any{"kind": "agent", "name": "seo-bot"}, "created": fxAt(base.Add(2 * time.Hour)),
			"priority": "p2", "blocking": false, "classification": "internal",
		},
		"resp body",
		"seomatrix", fxULID(32),
		map[string]any{
			"subject": "XW-axon-20260701-ffff", "transition": "respond",
			"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(2 * time.Hour)),
		},
	)

	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "acme", mustManifest(t, fx))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}

	wr := findArtifact(t, idx, "XW-axon-20260701-ffff")
	if wr.Result.State != fold.StateResponded {
		t.Fatalf("work_request state = %q, want responded", wr.Result.State)
	}
	if !wr.DeliveryUnresolvable {
		t.Fatal("DeliveryUnresolvable = false, want true: the response claims `delivered` but no handoff in the mirror names this work_request's own id in fulfills[]")
	}
}

// TestBuildIndex_DeliveryUnresolvableIsClearedWhenThePackageResolves is the
// paired positive control: the SAME shape, but with the handoff and its
// data package committed and resolvable — DeliveryUnresolvable must read
// false, and (without this test) a resolver mutated to always return true
// would still pass the red test above.
func TestBuildIndex_DeliveryUnresolvableIsClearedWhenThePackageResolves(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})

	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-hhhh.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-hhhh", "type": "work_request",
		"title": "wr", "space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitEvent("axon", fxULID(40), map[string]any{
		"subject": "XW-axon-20260701-hhhh", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base),
	})
	fx.commitEvent("seomatrix", fxULID(41), map[string]any{
		"subject": "XW-axon-20260701-hhhh", "transition": "acknowledge",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(time.Hour)),
	})

	fx.commitArtifactAndEvent(
		"seomatrix/exchanges/XS-seomatrix-20260701-iiii.md",
		map[string]any{
			"schema": "envelope/v1", "id": "XS-seomatrix-20260701-iiii", "type": "response",
			"title": "resp", "space": "fixture-space", "from": "seomatrix", "to": []string{"axon"},
			"parent": "XW-axon-20260701-hhhh", "result": "delivered",
			"actor": map[string]any{"kind": "agent", "name": "seo-bot"}, "created": fxAt(base.Add(2 * time.Hour)),
			"priority": "p2", "blocking": false, "classification": "internal",
		},
		"resp body",
		"seomatrix", fxULID(42),
		map[string]any{
			"subject": "XW-axon-20260701-hhhh", "transition": "respond",
			"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(2 * time.Hour)),
		},
	)

	// The handoff carrying the package — committed this time — names the
	// work_request in fulfills[] and the package in deliverables[].
	fx.commitArtifact("seomatrix/exchanges/XH-seomatrix-20260701-jjjj.md", map[string]any{
		"schema": "envelope/v1", "id": "XH-seomatrix-20260701-jjjj", "type": "handoff",
		"title": "delivery", "space": "fixture-space", "from": "seomatrix", "to": []string{"axon"},
		"actor": map[string]any{"kind": "agent", "name": "seo-bot"}, "created": fxAt(base.Add(2 * time.Hour)),
		"priority": "p3", "blocking": false, "classification": "internal",
		"fulfills": []string{"XW-axon-20260701-hhhh"},
		"deliverables": []map[string]any{
			{"name": "seomatrix-pack", "ref": "DP-seomatrix-20260701-b7g2", "kind": "data"},
		},
		"verification":        "Run `a2a data verify DP-seomatrix-20260701-b7g2`.",
		"acceptance_criteria": []string{"conforms"},
	}, "handoff body")

	// The package's own manifest, resolvable at the path
	// packageresolver.go's dataPackageManifestPath computes.
	manifestPath := fx.dir + "/seomatrix/data/DP-seomatrix-20260701-b7g2/manifest.json"
	fxMkdirAll(t, fx.dir+"/seomatrix/data/DP-seomatrix-20260701-b7g2")
	fxWriteFile(t, manifestPath, `{"schema":"data-package/v1","id":"DP-seomatrix-20260701-b7g2"}`)
	fxCommitAndPush(t, fx.dir, "fixture: package manifest")

	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "acme", mustManifest(t, fx))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}

	wr := findArtifact(t, idx, "XW-axon-20260701-hhhh")
	if wr.Result.State != fold.StateResponded {
		t.Fatalf("work_request state = %q, want responded", wr.Result.State)
	}
	if wr.DeliveryUnresolvable {
		t.Fatal("DeliveryUnresolvable = true, want false: the handoff fulfilling this work_request names a package that resolves in the mirror")
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
