package cache

// observed_consumers_test.go covers FindObservedConsumers — the PRODUCER
// direction of the same evidence FindUnadoptedConsumption already walks
// (spec 03-observed-consumption.md, judge-the-thing-2026-08): "who does my
// own space's committed record show consuming this contract, declared or
// not?".
//
// Fixtures are registered_consumers_test.go's own plain on-disk helpers
// (rcWriteFile/rcWriteConsumesYAML/rcWriteHandoff/rcWriteHandoffLifecycle/
// rcWriteDataPackageManifest) — no git commit is needed, because neither
// this scan nor the one it reuses resolves anything through git history.

import (
	"fmt"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// ocManifest builds the minimal space.Manifest FindObservedConsumers reads:
// participant systems and their membership status, nothing else.
func ocManifest(systems ...string) space.Manifest {
	m := space.Manifest{Schema: "space/v1", Space: "fixture-space"}
	for _, s := range systems {
		m.Participants = append(m.Participants, space.Participant{System: s, Status: fold.MembershipMember})
	}
	return m
}

// ocWriteSatisfiedRequirement writes a requirement from `from` to `to`
// naming targetContract, folded all the way to `satisfied` — the D-022
// union's OTHER declaration half, the one FindUnadoptedConsumption cannot
// see (it reads consumes.yaml only). The event ULIDs are prefixed 01HFOC so
// they never collide with rcWriteHandoffLifecycle's own 01HFXH namespace in
// the same fixture.
func ocWriteSatisfiedRequirement(t *testing.T, mirrorDir, from, to, targetContract string) {
	t.Helper()
	id := "XR-" + from + "-observed"
	rcWriteFile(t, mirrorDir, from+"/requires/"+id+".md",
		"---\nschema: envelope/v1\nid: "+id+"\ntype: requirement\ntitle: t\nspace: fixture-space\n"+
			"from: "+from+"\nto: ["+to+"]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\n"+
			"category: new-capability\npriority: p3\nblocking: true\nclassification: internal\n"+
			"target_contract: "+targetContract+"\nacceptance_criteria: [\"ac1\"]\n---\nbody\n")
	for i, ev := range []struct{ transition, system, at string }{
		{"create", from, "2026-07-21T10:00:00Z"},
		{"publish", from, "2026-07-21T11:00:00Z"},
		{"acknowledge", to, "2026-07-21T12:00:00Z"},
		{"satisfy", from, "2026-07-21T13:00:00Z"},
	} {
		ulid := fmt.Sprintf("01HFOC00000000000000000%03d", i+1)
		rcWriteFile(t, mirrorDir, fmt.Sprintf("%s/events/2026/%s.yaml", from, ulid),
			fmt.Sprintf("schema: event/v1\nevent: %s\nspace: fixture-space\n"+
				"subject: %s\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\n"+
				"at: %s\n", ulid, id, ev.transition, ev.system, ev.at))
	}
}

// TestFindObservedConsumers_NamesUndeclaredSystemWithPackageCount is
// §8 criterion 1's own read half (US-1): three verify-passed deliveries pin
// XC-seomatrix-regime-corpus and axon's registry declares nothing, so the
// producer's retire decision must be able to see axon.
//
// TEETH: drop the per-participant FindUnadoptedConsumption call and this
// reds with an empty set — which is exactly today's behaviour, the defect
// fb-20260820-0cb8c8 reports.
func TestFindObservedConsumers_NamesUndeclaredSystemWithPackageCount(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-n2yp", "XC-seomatrix-regime-corpus@1.0.0#bbb222")

	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", "DP-seomatrix-20260818-n2yp")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", true)

	got, err := FindObservedConsumers(mirrorDir, "XC-seomatrix-regime-corpus", ocManifest("axon", "seomatrix"))
	if err != nil {
		t.Fatalf("FindObservedConsumers: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d observed consumers, want exactly 1: %+v", len(got), got)
	}
	if got[0].System != "axon" {
		t.Fatalf("got system %q, want axon", got[0].System)
	}
	if got[0].Packages != 2 {
		t.Fatalf("got Packages=%d, want 2 distinct packages", got[0].Packages)
	}
}

// TestFindObservedConsumers_DeclaredConsumerIsNeverObserved is §6's own
// "observed system already declared (no double-count)" edge: axon's
// consumes.yaml names the contract, so axon is a DECLARED consumer and must
// never also be reported as observed — the two are different kinds of fact
// (§T2) and one system must not be counted under both.
func TestFindObservedConsumers_DeclaredConsumerIsNeverObserved(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteConsumesYAML(t, mirrorDir, "axon", "XC-seomatrix-regime-corpus", 1)

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)

	got, err := FindObservedConsumers(mirrorDir, "XC-seomatrix-regime-corpus", ocManifest("axon", "seomatrix"))
	if err != nil {
		t.Fatalf("FindObservedConsumers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty — axon DECLARED this contract, so it is a registered consumer, never an observed one", got)
	}
}

// TestFindObservedConsumers_SatisfiedRequirementConsumerIsNeverObserved is
// the D-022 union's OTHER declaration half. A system that registered by
// filing a requirement (not by writing consumes.yaml) is equally declared,
// and FindUnadoptedConsumption alone cannot see that — it filters on
// consumes.yaml only. Without subtracting the full D-022 set here, such a
// system would be named twice: once as a blocking registered consumer and
// once as "observed and undeclared".
func TestFindObservedConsumers_SatisfiedRequirementConsumerIsNeverObserved(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")
	ocWriteSatisfiedRequirement(t, mirrorDir, "axon", "seomatrix", "XC-seomatrix-regime-corpus")

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)

	got, err := FindObservedConsumers(mirrorDir, "XC-seomatrix-regime-corpus", ocManifest("axon", "seomatrix"))
	if err != nil {
		t.Fatalf("FindObservedConsumers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty — axon's satisfied requirement already registers it (D-022), so it is declared, not observed", got)
	}
}

// TestFindObservedConsumers_ZeroObservedOnAPlainSpace is §6's zero case and
// §8 criterion 4's floor: a space where nobody received anything pinning the
// contract grows no observed consumer, so nothing anywhere changes.
func TestFindObservedConsumers_ZeroObservedOnAPlainSpace(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	got, err := FindObservedConsumers(mirrorDir, "XC-seomatrix-regime-corpus", ocManifest("axon", "seomatrix"))
	if err != nil {
		t.Fatalf("FindObservedConsumers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}
}

// TestFindObservedConsumers_ProducerIsNeverItsOwnObservedConsumer: a system
// may receive a delivery pinning a contract IT publishes (a round trip
// through a third party, a self-addressed test). FindUnadoptedConsumption
// already excludes that by parsing the contract id's own system half; this
// pins that the producer direction inherits the exclusion rather than
// re-deriving it.
func TestFindObservedConsumers_ProducerIsNeverItsOwnObservedConsumer(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "seomatrix/consumes.yaml", "schema: consumes/v1\nsystem: seomatrix\ndependencies: []\n")

	rcWriteDataPackageManifest(t, mirrorDir, "DP-axon-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteHandoff(t, mirrorDir, "XH-axon-20260817-aaaa", "axon", "seomatrix", "DP-axon-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-axon-20260817-aaaa", "axon", "seomatrix", true)

	got, err := FindObservedConsumers(mirrorDir, "XC-seomatrix-regime-corpus", ocManifest("axon", "seomatrix"))
	if err != nil {
		t.Fatalf("FindObservedConsumers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty — seomatrix publishes this contract; a producer is never its own consumer", got)
	}
}

// TestFindObservedConsumers_LeftParticipantIsExcluded mirrors §5.4 bullet
// (a)/CC-062, which excludes `left` systems from the ack set entirely: a
// system that has left the space is not a risk a producer needs named at
// retire time.
func TestFindObservedConsumers_LeftParticipantIsExcluded(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)

	m := ocManifest("seomatrix")
	m.Participants = append(m.Participants, space.Participant{System: "axon", Status: fold.MembershipLeft})

	got, err := FindObservedConsumers(mirrorDir, "XC-seomatrix-regime-corpus", m)
	if err != nil {
		t.Fatalf("FindObservedConsumers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty — axon has left the space (§5.4 bullet (a), CC-062)", got)
	}
}

// TestObservedUndeclaredContracts_ProjectsTheConsumerSideSet is the cache
// half of §7's caller-resolved fact: the set of contract ids ownSystem
// consumes and declares nowhere, which is what a deprecation announcement's
// own `deprecates` id is looked up against.
func TestObservedUndeclaredContracts_ProjectsTheConsumerSideSet(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteConsumesYAML(t, mirrorDir, "axon", "XC-seomatrix-c4-export", 1)

	// Declared: XC-seomatrix-c4-export. Undeclared: XC-seomatrix-regime-corpus.
	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-n2yp", "XC-seomatrix-c4-export@1.0.0#bbb222")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", "DP-seomatrix-20260818-n2yp")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", true)

	got, err := ObservedUndeclaredContracts(mirrorDir, "axon")
	if err != nil {
		t.Fatalf("ObservedUndeclaredContracts: %v", err)
	}
	if !got["XC-seomatrix-regime-corpus"] {
		t.Fatalf("got %v, want XC-seomatrix-regime-corpus — axon verify-passed a package pinning it and declares it nowhere", got)
	}
	if got["XC-seomatrix-c4-export"] {
		t.Fatalf("got %v — XC-seomatrix-c4-export is DECLARED in axon's own consumes.yaml and must never be in the observed set", got)
	}
}

// TestObservedUndeclaredContracts_EmptyWithoutAnOwnSystem is the fail-open
// floor: a caller with no identity (BuildNotifyIndex's own ownSystem="")
// gets an empty set, never a walk and never a synthesized obligation.
func TestObservedUndeclaredContracts_EmptyWithoutAnOwnSystem(t *testing.T) {
	t.Parallel()
	got, err := ObservedUndeclaredContracts(t.TempDir(), "")
	if err != nil {
		t.Fatalf("ObservedUndeclaredContracts: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// TestObservedUndeclaredContracts_MalformedRegistryDegradesToEmpty pins the
// direction of failure (§8 criterion 2's discipline, and
// myDependencyContracts' own documented asymmetry): a consumes.yaml that
// exists but is not a consumes/v1 registry must degrade to "nothing
// observed" — reporting every contract as undeclared because the file
// listing the declarations could not be read would be the false-advisory
// direction, and this fact drives a next MOVE.
func TestObservedUndeclaredContracts_MalformedRegistryDegradesToEmpty(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "consumes: []\n") // placeholder shape, refused by parseConsumesStrict

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)

	got, err := ObservedUndeclaredContracts(mirrorDir, "axon")
	if err != nil {
		t.Fatalf("ObservedUndeclaredContracts: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty — the registry could not be read, so nothing may be called undeclared", got)
	}
}
