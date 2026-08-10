package cache

// operational_debt_test.go is P5 AC1's own test file (specs/05-declared-
// nature.md): a published contract's operational debt derives from
// registration and publication ALONE — no x_operational field is ever
// written by any test in this file, which is exactly §6's own testing row
// ("published version + registered consumer ⇒ a live integration with an
// owner, with NO voluntary field set — this is the P-1 assertion; it must
// pass with every new field absent").

import (
	"context"
	"strings"
	"testing"
	"time"
)

// odContractFields builds a minimal contract envelope carrying NO
// x_operational field — this package does not decode that field for this
// derivation at all (contractOperationalDebtOwed reads registration and
// publication only), so its absence here is the point, not an oversight.
func odContractFields(id, from string, to []string, created time.Time) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "contract", "title": "widget contract",
		"space": "fixture-space", "from": from, "to": to,
		"actor":   map[string]any{"kind": "agent", "name": from + "-bot"},
		"created": fxAt(created), "priority": "p2", "blocking": false, "classification": "internal",
	}
}

// odSetManifest overwrites fx's space.yaml directly on disk (mustManifest
// reads live disk state, not git history — no commit needed) with the
// given min_binary_version, the ONE knob §8 AC1's floor gate needs. Kept
// local to this file rather than parameterizing fixtureSpace.writeManifest,
// so this wave's own tests cannot perturb every other test in this
// package that relies on that method's fixed "0.0.0" default.
func odSetManifest(t *testing.T, fx *fixtureSpace, minBinaryVersion string, participants ...fixtureParticipant) {
	t.Helper()
	var b strings.Builder
	b.WriteString("schema: space/v1\nspace: fixture-space\nmin_binary_version: " + minBinaryVersion + "\nparticipants:\n")
	for _, p := range participants {
		status := p.Status
		if status == "" {
			status = "active"
		}
		b.WriteString("  - system: " + p.System + "\n    org: " + p.System + "-org\n    section: " + p.System +
			"\n    owners: [" + p.System + "-human]\n    status: " + status + "\n    joined: \"2026-01-01\"\n")
	}
	rcWriteFile(t, fx.dir, "space.yaml", b.String())
}

// TestBuildIndex_OperationalDebtOwed_P1CoreAssertion is spec 05's own
// testing row and the phase's whole point: a published contract with a
// registered consumer, above the space's floor, yields OperationalDebtOwed
// = true and internal/pendency names the producer owing `activate` — with
// NO x_operational field anywhere in the fixture.
func TestBuildIndex_OperationalDebtOwed_P1CoreAssertion(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-widget"

	fx.commitArtifact("axon/provides/widget/contract.md",
		odContractFields(contractID, "axon", nil, base), "contract body")
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": contractID, "transition": "publish", "version": "1.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})
	rcWriteConsumesYAML(t, fx.dir, "beta", contractID, 1)

	manifest := mustManifest(t, fx)
	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "axon", manifest)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, contractID)
	if fa.Result.State != "published" {
		t.Fatalf("state = %q, want published", fa.Result.State)
	}
	if !fa.OperationalDebtOwed {
		t.Fatal("OperationalDebtOwed = false, want true: published + registered consumer + above floor, with no x_operational field set anywhere")
	}

	verdict := resolveVerdict(fa, "axon", manifest, "")
	if len(verdict.Owners) != 1 || verdict.Owners[0] != "axon" {
		t.Fatalf("Owners = %v, want [axon] — the producer, with no voluntary field set", verdict.Owners)
	}
	if verdict.Expected != "activate" {
		t.Fatalf("Expected = %q, want %q", verdict.Expected, "activate")
	}
	if !strings.Contains(verdict.Why, "P5 AC1") {
		t.Fatalf("Why = %q, want it to name P5 AC1", verdict.Why)
	}
}

// TestBuildIndex_OperationalDebtOwed_GatedOnFloor is §8 AC1's own gate:
// "GATED ON THE SPACE'S OWN FLOOR — below it, no derived row is emitted
// and the thread reads exactly as it does today." Identical fixture to
// the core assertion above, MinBinaryVersion left at the fixture's
// default ("0.0.0", below contract.ContractPublicationFloor).
func TestBuildIndex_OperationalDebtOwed_GatedOnFloor(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-widget"

	fx.commitArtifact("axon/provides/widget/contract.md",
		odContractFields(contractID, "axon", nil, base), "contract body")
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": contractID, "transition": "publish", "version": "1.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})
	rcWriteConsumesYAML(t, fx.dir, "beta", contractID, 1)

	manifest := mustManifest(t, fx)
	if manifest.MinBinaryVersion != "0.0.0" {
		t.Fatalf("fixture default MinBinaryVersion = %q, want 0.0.0 (below floor) — this test's own control", manifest.MinBinaryVersion)
	}
	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "axon", manifest)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, contractID)
	if fa.OperationalDebtOwed {
		t.Fatal("OperationalDebtOwed = true, want false: this space is below contract.ContractPublicationFloor")
	}

	verdict := resolveVerdict(fa, "axon", manifest, "")
	if verdict.Owners != nil {
		t.Fatalf("Owners = %v, want nil: below the floor no derived row is emitted", verdict.Owners)
	}
	if verdict.Expected != "" {
		t.Fatalf("Expected = %q, want \"\"", verdict.Expected)
	}
	wantWhy := "alive and settled: the owner MAY publish a successor or deprecate, but neither is a move anyone waits for"
	if verdict.Why != wantWhy {
		t.Fatalf("Why = %q, want the unchanged settled text %q — \"the thread reads exactly as it does today\"", verdict.Why, wantWhy)
	}
}

// TestBuildIndex_OperationalDebtOwed_NoRegisteredConsumerStillOwesNobody is
// the paired control: above floor, published, but NOBODY is registered.
// "A rule that fires on everything is not a rule."
func TestBuildIndex_OperationalDebtOwed_NoRegisteredConsumerStillOwesNobody(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-widget"

	fx.commitArtifact("axon/provides/widget/contract.md",
		odContractFields(contractID, "axon", nil, base), "contract body")
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": contractID, "transition": "publish", "version": "1.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})
	// Deliberately no consumes.yaml, no satisfied requirement — nobody
	// registered against this contract at all.

	manifest := mustManifest(t, fx)
	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "axon", manifest)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, contractID)
	if fa.OperationalDebtOwed {
		t.Fatal("OperationalDebtOwed = true, want false: no registered consumer exists")
	}
}

// TestBuildIndex_OperationalDebtOwed_DifferentMajorNotRegistered proves the
// derivation is major-scoped to the contract's own LatestPublishVersion
// (spec 05's 2026-08-08 amendment, "operational[]'s key scope ... never
// stated relative to consumes.yaml's (contract, major) registration" —
// resolved via the already-shipped FindRegisteredConsumersForMajor): a
// consumer pinned to a DIFFERENT major than the one just published does
// not trigger the debt.
func TestBuildIndex_OperationalDebtOwed_DifferentMajorNotRegistered(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-widget"

	fx.commitArtifact("axon/provides/widget/contract.md",
		odContractFields(contractID, "axon", nil, base), "contract body")
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": contractID, "transition": "publish", "version": "2.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})
	// beta is pinned to major 1; the contract just published major 2.
	rcWriteConsumesYAML(t, fx.dir, "beta", contractID, 1)

	manifest := mustManifest(t, fx)
	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "axon", manifest)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, contractID)
	if fa.OperationalDebtOwed {
		t.Fatal("OperationalDebtOwed = true, want false: the registered consumer is pinned to a different major than the one just published")
	}
}

// odCommitActivate commits an `activate` event (26A's `a2a contract
// activate`) naming version in its own `activation.version` block —
// P5 AC1's discharge half.
func odCommitActivate(t *testing.T, fx *fixtureSpace, system, ulid, contractID, version string, at time.Time) {
	t.Helper()
	fx.commitEvent(system, ulid, map[string]any{
		"subject": contractID, "transition": "activate",
		"actor": map[string]any{"kind": "agent", "name": system + "-bot", "system": system},
		"at":    fxAt(at),
		"activation": map[string]any{
			"version": version, "status": "live", "satisfies": []string{"endpoint"},
		},
	})
}

// TestBuildIndex_OperationalDebtOwed_ClearedByActivation is the brief's
// own instrument-and-clear requirement (spec 05's 2026-08-10 amendment:
// "a derived obligation with no instrument to discharge it ... this epic
// has now found that defect three times; it will not ship it
// deliberately"). 26A shipped `a2a contract activate` in this same tree;
// this proves the row this wave adds actually reads it.
func TestBuildIndex_OperationalDebtOwed_ClearedByActivation(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-widget"

	fx.commitArtifact("axon/provides/widget/contract.md",
		odContractFields(contractID, "axon", nil, base), "contract body")
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": contractID, "transition": "publish", "version": "1.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})
	rcWriteConsumesYAML(t, fx.dir, "beta", contractID, 1)
	odCommitActivate(t, fx, "axon", fxULID(2), contractID, "1.0.0", base.Add(2*time.Hour))

	manifest := mustManifest(t, fx)
	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "axon", manifest)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, contractID)
	if fa.OperationalDebtOwed {
		t.Fatal("OperationalDebtOwed = true, want false: this published version was already activated")
	}

	verdict := resolveVerdict(fa, "axon", manifest, "")
	if verdict.Owners != nil {
		t.Fatalf("Owners = %v, want nil: the activation already discharged the debt", verdict.Owners)
	}
	wantWhy := "alive and settled: the owner MAY publish a successor or deprecate, but neither is a move anyone waits for"
	if verdict.Why != wantWhy {
		t.Fatalf("Why = %q, want the unchanged settled text %q", verdict.Why, wantWhy)
	}
}

// TestBuildIndex_OperationalDebtOwed_ActivationForADifferentVersionDoesNotClear
// is the paired control for spec 05's imported key-scope answer
// ("readiness is a property of a published version, registration is a
// standing relationship to a major"): an activation naming version 1.0.0
// must not clear the debt this contract's OWN latest published version,
// 2.0.0, still carries.
func TestBuildIndex_OperationalDebtOwed_ActivationForADifferentVersionDoesNotClear(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-widget"

	fx.commitArtifact("axon/provides/widget/contract.md",
		odContractFields(contractID, "axon", nil, base), "contract body")
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": contractID, "transition": "publish", "version": "1.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})
	odCommitActivate(t, fx, "axon", fxULID(2), contractID, "1.0.0", base.Add(2*time.Hour))
	// A second publish supersedes the activated version — beta is
	// registered against major 2, the one actually in force now.
	fx.commitEvent("axon", fxULID(3), map[string]any{
		"subject": contractID, "transition": "publish", "version": "2.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(3 * time.Hour)),
	})
	rcWriteConsumesYAML(t, fx.dir, "beta", contractID, 2)

	manifest := mustManifest(t, fx)
	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "axon", manifest)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, contractID)
	if fa.LatestPublishVersion != "2.0.0" {
		t.Fatalf("LatestPublishVersion = %q, want 2.0.0 — this test's own control", fa.LatestPublishVersion)
	}
	if !fa.OperationalDebtOwed {
		t.Fatal("OperationalDebtOwed = false, want true: the activation on record names 1.0.0, not this contract's own published 2.0.0")
	}
}

// TestStoreInbox_OperationalDebtSurfacesUnderActionable is the brief's own
// warning made concrete (epic-backlog B19: "--actionable drops a row whose
// Expected is empty, and a row nobody can see is not a named owner"): the
// producer's own published contract is never addressed to itself (it is
// never in its own `to:`), so `a2a inbox` (no flag) would never list it —
// Store.inbox's `actionableOnly` branch is the one scope that does not
// filter by addressedToMe, and that is exactly where this row must land.
func TestStoreInbox_OperationalDebtSurfacesUnderActionable(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-widget"

	fx.commitArtifact("axon/provides/widget/contract.md",
		odContractFields(contractID, "axon", nil, base), "contract body")
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": contractID, "transition": "publish", "version": "1.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})
	rcWriteConsumesYAML(t, fx.dir, "beta", contractID, 1)

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}},
		func() time.Time { return base.Add(24 * time.Hour) }, 0)

	plain, err := store.Inbox(context.Background(), false)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if containsItemID(plain, contractID) {
		t.Fatalf("plain inbox (no flag) unexpectedly lists the producer's own contract: got %v — "+
			"a contract is never addressed to its own producer, this belongs on outbox/actionable, not here",
			itemIDs(plain))
	}

	actionable, err := store.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("Inbox(actionable): %v", err)
	}
	if !containsItemID(actionable, contractID) {
		t.Fatalf("`a2a inbox --actionable` does not surface the producer's own operational debt: got %v — "+
			"the row internal/pendency computes is unreachable, which is exactly epic-backlog B19's warning "+
			"(\"a row nobody can see is not a named owner\")", itemIDs(actionable))
	}
	for _, it := range actionable {
		if it.ID != contractID {
			continue
		}
		if len(it.WaitingOn) != 1 || it.WaitingOn[0] != "axon" {
			t.Errorf("WaitingOn = %v, want [axon]", it.WaitingOn)
		}
		if it.ExpectedTransition != "activate" {
			t.Errorf("ExpectedTransition = %q, want %q", it.ExpectedTransition, "activate")
		}
		found := false
		for _, r := range it.Reasons {
			if r == "activation-owed" {
				found = true
			}
		}
		if !found {
			t.Errorf("Reasons = %v, want it to contain %q", it.Reasons, "activation-owed")
		}
	}

	// Outbox (OP-208) is the other, unconditional surface: every open own
	// item carries the verdict regardless of any --actionable-style flag
	// (store.go's Outbox doc comment).
	outbox, err := store.Outbox(context.Background(), false)
	if err != nil {
		t.Fatalf("Outbox: %v", err)
	}
	if !containsItemID(outbox, contractID) {
		t.Fatalf("outbox does not list the producer's own open contract: got %v", itemIDs(outbox))
	}
}
