package cache

import (
	"context"
	"os"
	"path/filepath"
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
// TestBuildIndex_NoHandoffMeansNoDeliveryClaimToJudge pins the boundary the
// conformance matrix taught this package on 2026-08-10, and it is the
// INVERSE of what this test asserted when it was written.
//
// It used to require DeliveryUnresolvable == true here, on the reasoning that
// a payload PR which never merged is a handoff the space never received. That
// reasoning assumes `result: delivered` claims bytes. It does not — `delivered`
// is the ordinary result word for a work_request, driven by six call sites in
// the conformance catalogue on exchanges with no data package anywhere near
// them, and five declared paths went red naming the sender's `close` replaced
// by the responder's `respond`.
//
// So the answer here is false, and the cost is named rather than hidden: the
// incident AC9 came from is exactly this shape, and this half does not cover
// it. Nothing in the space can tell "delivered, bytes pending" from
// "delivered, no bytes were ever involved" until a response can name its
// package — the same wire decision spec 04 §11 defers AC9's submit half on.
func TestBuildIndex_NoHandoffMeansNoDeliveryClaimToJudge(t *testing.T) {
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
	if wr.DeliveryUnresolvable {
		t.Fatal("DeliveryUnresolvable = true, want false: no handoff names this work_request in fulfills[], so there is no data deliverable whose absence could mean anything — `result: delivered` alone claims no bytes")
	}
}

// TestBuildIndex_DeliveryUnresolvableIsClearedWhenThePackageResolves is the
// paired positive control: the SAME shape, but with the handoff and its
// data package committed and resolvable — DeliveryUnresolvable must read
// false, and (without this test) a resolver mutated to always return true
// would still pass the red test above.
// TestBuildIndex_DeliveryUnresolvableWhenTheHandoffLandedAndItsPackageDidNot
// is the predicate's ONLY positive case, and until 2026-08-10 nothing asserted
// it. Both sibling tests below and above assert `false`, so neutering the
// `sawDataDeliverable` flag changed no test's verdict — the flag that makes
// "no handoff" and "a handoff whose package is missing" different answers was
// load-bearing and unwatched. Found by mutating it and seeing nothing red.
//
// This is the partial-write shape AC9's read half actually covers: the handoff
// merged, naming a data deliverable, and the package it points at is not in
// the space. A dangling reference, which is worse than no reference.
func TestBuildIndex_DeliveryUnresolvableWhenTheHandoffLandedAndItsPackageDidNot(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})

	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-kkkk.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-kkkk", "type": "work_request",
		"title": "wr", "space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitEvent("axon", fxULID(50), map[string]any{
		"subject": "XW-axon-20260701-kkkk", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base),
	})
	fx.commitEvent("seomatrix", fxULID(51), map[string]any{
		"subject": "XW-axon-20260701-kkkk", "transition": "acknowledge",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(time.Hour)),
	})
	fx.commitArtifactAndEvent(
		"seomatrix/exchanges/XS-seomatrix-20260701-llll.md",
		map[string]any{
			"schema": "envelope/v1", "id": "XS-seomatrix-20260701-llll", "type": "response",
			"title": "resp", "space": "fixture-space", "from": "seomatrix", "to": []string{"axon"},
			"parent": "XW-axon-20260701-kkkk", "result": "delivered",
			"actor": map[string]any{"kind": "agent", "name": "seo-bot"}, "created": fxAt(base.Add(2 * time.Hour)),
			"priority": "p2", "blocking": false, "classification": "internal",
		},
		"resp body",
		"seomatrix", fxULID(52),
		map[string]any{
			"subject": "XW-axon-20260701-kkkk", "transition": "respond",
			"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(2 * time.Hour)),
		},
	)

	// The handoff landed and names a data deliverable. Its package did NOT —
	// no manifest.json is written anywhere, which is the whole point.
	fx.commitArtifact("seomatrix/exchanges/XH-seomatrix-20260701-mmmm.md", map[string]any{
		"schema": "envelope/v1", "id": "XH-seomatrix-20260701-mmmm", "type": "handoff",
		"title": "delivery", "space": "fixture-space", "from": "seomatrix", "to": []string{"axon"},
		"actor": map[string]any{"kind": "agent", "name": "seo-bot"}, "created": fxAt(base.Add(2 * time.Hour)),
		"priority": "p3", "blocking": false, "classification": "internal",
		"fulfills": []string{"XW-axon-20260701-kkkk"},
		"deliverables": []map[string]any{
			{"name": "seomatrix-pack", "ref": "DP-seomatrix-20260701-c9h4", "kind": "data"},
		},
		"verification":        "Run `a2a data verify DP-seomatrix-20260701-c9h4`.",
		"acceptance_criteria": []string{"conforms"},
	}, "handoff body")

	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "acme", mustManifest(t, fx))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}

	wr := findArtifact(t, idx, "XW-axon-20260701-kkkk")
	if wr.Result.State != fold.StateResponded {
		t.Fatalf("work_request state = %q, want responded", wr.Result.State)
	}
	if !wr.DeliveryUnresolvable {
		t.Fatal("DeliveryUnresolvable = false, want true: the handoff names DP-seomatrix-20260701-c9h4 as a data deliverable and no manifest for it exists in this space")
	}
}

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

// TestWalkArtifacts_DataPackageReadmeIsNotASkippedFile is the regression for
// fb-20260812-d31acb: `a2a data pack` writes a data package's own readme at
// "<system>/data/<DP-id>/README.md" WITHOUT frontmatter by design (spec 04
// §11's AC8 amendment), and its bytes are sealed by the package's own
// manifest.json digest — so it can never gain frontmatter without breaking
// that seal. Before this fix walkArtifacts tried to decode it as an
// envelope, failed artifact.ParseFrontmatter, and reported
// SkipReasonNotFrontmatterShaped for it on every real `a2a inbox`/`outbox`
// call — mirroring TestWalkArtifacts_InfrastructureIsNotASkippedArtifact
// (skipped_test.go)'s own shape for the sibling infrastructure exclusion.
func TestWalkArtifacts_DataPackageReadmeIsNotASkippedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("seomatrix/data/DP-seomatrix-20260808-7xaa/README.md", "# Order export\n\nNo frontmatter here.\n")
	write("seomatrix/data/DP-seomatrix-20260808-7xaa/manifest.json", `{"schema":"data-package/v1"}`)
	write("axon/README.md", "# Participant prose\n")

	artifacts, skips, err := walkArtifacts(dir)
	if err != nil {
		t.Fatalf("walkArtifacts: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("artifacts = %+v, want none", artifacts)
	}
	if len(skips) != 1 || skips[0].Path != "axon/README.md" || skips[0].Reason != SkipReasonNotFrontmatterShaped {
		t.Fatalf("skips = %+v, want only the genuine participant prose reported, and the packed data-package README skipped SILENTLY", skips)
	}
}

// TestWalkArtifacts_BlobPayloadIsNotASkippedFile is the regression for
// docs/backlog.md "`walkArtifacts` has the same hole for blob payloads
// (2026-08-12)": `a2a attach` lands a blob's payload bytes at
// "<system>/blobs/<BL-id>/..." (space.BlobForPath's own grammar), and
// nothing about that directory's contents is an envelope draft — a
// frontmatter-bearing .md file filed there is the blob's own payload, sealed
// by the blob's digest sidecar, never the artifact this walk is looking for.
// Before this fix walkArtifacts tried to decode it as an envelope, failed
// artifact.ParseFrontmatter (or worse, succeeded on an accidental frontmatter
// shape and folded garbage into the index), and reported
// SkipReasonNotFrontmatterShaped for it on every real `a2a inbox`/`outbox`
// call — the identical defect
// TestWalkArtifacts_DataPackageReadmeIsNotASkippedFile fixed for its own
// twin, closed here the same way: silently skip it, don't report it.
func TestWalkArtifacts_BlobPayloadIsNotASkippedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("axon/blobs/BL-axon-20260811-ab12/notes.md", "# Attached notes\n\nNo frontmatter here — this is the blob's own payload, not a draft.\n")
	write("axon/blobs/BL-axon-20260811-ab12/notes.md.sha256", "deadbeef  notes.md\n")

	artifacts, skips, err := walkArtifacts(dir)
	if err != nil {
		t.Fatalf("walkArtifacts: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("artifacts = %+v, want none: a blob payload file must not be folded in as an envelope draft", artifacts)
	}
	if len(skips) != 0 {
		t.Fatalf("skips = %+v, want none: a blob payload file must be skipped SILENTLY, not reported as a malformed artifact", skips)
	}
}

// TestWalkArtifacts_MalformedArtifactBesideBlobsStillReported is
// TestWalkArtifacts_BlobPayloadIsNotASkippedFile's own negative control: the
// blob exemption is narrow to the blob's own directory grammar
// ("<system>/blobs/<BL-id>/..."), never "skip this whole system section" —
// cmd_validate_ci.go's isBlobPayloadPath doc comment says so explicitly, and
// this test proves walkArtifacts honours the same narrowness. A genuinely
// malformed artifact filed elsewhere under the SAME system (axon, here
// outside any blobs/ subtree) must still reach artifact discovery and still
// be reported as a skip.
func TestWalkArtifacts_MalformedArtifactBesideBlobsStillReported(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("axon/blobs/BL-axon-20260811-ab12/notes.md", "blob payload prose, not an artifact\n")
	write("axon/malformed-draft.md", "not frontmatter shaped at all\n")

	artifacts, skips, err := walkArtifacts(dir)
	if err != nil {
		t.Fatalf("walkArtifacts: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("artifacts = %+v, want none", artifacts)
	}
	if len(skips) != 1 || skips[0].Path != "axon/malformed-draft.md" || skips[0].Reason != SkipReasonNotFrontmatterShaped {
		t.Fatalf("skips = %+v, want only the genuinely malformed artifact outside blobs/ reported, and the blob payload skipped silently", skips)
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
