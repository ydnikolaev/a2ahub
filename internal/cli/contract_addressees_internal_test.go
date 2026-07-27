package cli

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// AC-971.2, asserted directly rather than through a proxy.
//
// The acceptance criterion is not "a registered consumer ends up addressed" —
// that is AC-971.1 and cmd_contract_test.go proves it end to end. AC-971.2 is
// the stronger claim §T4 actually rests on: deprecate's addressee set and
// retire's blocking-consumer set are computed by ONE shared D-022 scan
// (cache.FindRegisteredConsumers), not two independently-written call sites
// that happen to agree today. F3's deadlock existed precisely because they
// were different sets, and a phase that merely made them agree would leave
// the next divergence one refactor away.
//
// P4 wave 5 (Edge 1, 04-per-version-lifecycle.md §4) deliberately SPLITS
// this invariant along one axis, and the split is intentional, not a
// regression of it: `contractDeprecateAddressees` still calls the UNSCOPED
// `cache.FindRegisteredConsumers` (this file's own comparison below), while
// `contractBuildRetirePrecondition` calls `cache.FindRegisteredConsumersForMajor`
// — the major-filtered sibling, added because a consumer registered on a
// DIFFERENT major must not block retiring this line forever. Both functions
// share ONE implementation (`findRegisteredConsumers` in internal/cache),
// so the "one query, not two" guarantee this test protects is about the
// SCAN, not about deprecate and retire always resolving to the identical
// set — Edge 1 is the one place they are allowed to differ, by major, on
// purpose.
//
// This file is `package cli` for that reason: contractDeprecateAddressees
// is unexported (the registered-consumer scan itself moved to
// cache.FindRegisteredConsumers in P4 wave 5, decision 8 — ONE home shared
// with internal/mcp — and is exported there), and the external
// cmd_contract_test.go could only chain through `retire --override`'s note to
// infer the relationship. Inference is not the assertion.

// contractAddresseeFixture builds a mirror where the descriptor's
// authoring-time `to:` and the registered-consumer set DELIBERATELY disagree,
// so that any implementation reading the wrong one is distinguishable.
func contractAddresseeFixture(t *testing.T) (mirrorDir, contractID, from string, descriptorTo []string) {
	t.Helper()
	mirrorDir = t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(mirrorDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// axon publishes the contract and names ONLY "delta" in `to:` — a system
	// that never registered.
	write("axon/provides/widget/contract.md",
		"---\nschema: envelope/v1\nid: XC-axon-widget\ntype: contract\ntitle: t\nspace: fixture-space\n"+
			"from: axon\nto: [delta]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\n"+
			"category: api\npriority: p3\nblocking: false\nclassification: internal\n"+
			"version: \"1.0.0\"\ncompat_policy: default\nschema_format: json-schema-2020-12\n---\nbody\n")

	// Two systems DID register, neither of them "delta". Sorted order is
	// deliberately not authoring order.
	for _, sys := range []string{"seomatrix", "beta"} {
		write(sys+"/consumes.yaml",
			"schema: consumes/v1\nsystem: "+sys+"\ndependencies:\n  - contract: XC-axon-widget\n    major: 1\n    since: 2026-07-21\n")
	}

	// The producer registers against its own contract too — a real shape
	// (a system may consume what it also provides) that must be excluded
	// from the recipients, since addressing yourself is not a notification.
	write("axon/consumes.yaml",
		"schema: consumes/v1\nsystem: axon\ndependencies:\n  - contract: XC-axon-widget\n    major: 1\n    since: 2026-07-21\n")

	return mirrorDir, "XC-axon-widget", "axon", []string{"delta"}
}

// TestDeprecateAddresseesAreTheRetireBlockingSet is AC-971.2.
//
// TEETH: change contractDeprecateAddressees to read the descriptor's `to:`
// (F3's pre-P37 behaviour), to consult a second/parallel source, or to drop
// the sort, and this reds — the fixture's two sets are disjoint by
// construction, so no wrong implementation can coincidentally match.
func TestDeprecateAddresseesAreTheRetireBlockingSet(t *testing.T) {
	t.Parallel()
	mirrorDir, contractID, from, descriptorTo := contractAddresseeFixture(t)

	// The UNSCOPED registered-consumer set — the same
	// cache.FindRegisteredConsumers call contractDeprecateAddressees below
	// makes. contractBuildRetirePrecondition itself calls the major-scoped
	// sibling, cache.FindRegisteredConsumersForMajor (Edge 1); both share
	// ONE implementation, which is the invariant this test protects — see
	// this file's own header for why "same query" no longer means
	// "identical scope" post P4 wave 5.
	blocking, err := cache.FindRegisteredConsumers(mirrorDir, contractID)
	if err != nil {
		t.Fatalf("cache.FindRegisteredConsumers: %v", err)
	}
	want := make([]string, 0, len(blocking))
	for sys := range blocking {
		if sys == from {
			continue // a producer does not notify itself
		}
		want = append(want, sys)
	}
	sort.Strings(want)

	// The set a deprecation addresses.
	got, err := contractDeprecateAddressees(mirrorDir, contractID, from, descriptorTo)
	if err != nil {
		t.Fatalf("contractDeprecateAddressees: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("addressed %v, retire-blocking %v — these must be ONE set (§T4); differing lengths mean two queries", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("addressed %v != retire-blocking %v at index %d — the deadlock F3 closed is back the moment these differ", got, want, i)
		}
	}

	// And the fixture really was discriminating: the descriptor's own `to:`
	// must NOT be what came back, or this test would pass on the old
	// behaviour too.
	for _, addressed := range got {
		for _, authored := range descriptorTo {
			if addressed == authored {
				t.Fatalf("fixture is not discriminating: %q appears in both the registered set and the descriptor's to:", addressed)
			}
		}
	}
	if len(got) == 0 {
		t.Fatal("fixture registered two consumers; an empty addressee set means the registry was not read at all")
	}
}

// TestDeprecateAddresseesExcludeTheProducer keeps the one exclusion the
// shared query cannot express: `cache.FindRegisteredConsumers` legitimately
// returns the producer when it consumes its own contract, but a deprecation
// addressed to its own author is noise, and `to:` naming `from` is refused
// by the envelope's own referential rules.
func TestDeprecateAddresseesExcludeTheProducer(t *testing.T) {
	t.Parallel()
	mirrorDir, contractID, from, descriptorTo := contractAddresseeFixture(t)

	blocking, err := cache.FindRegisteredConsumers(mirrorDir, contractID)
	if err != nil {
		t.Fatalf("cache.FindRegisteredConsumers: %v", err)
	}
	if !blocking[from] {
		t.Fatalf("fixture is not discriminating: %q must be in the registered set for this test to mean anything", from)
	}

	got, err := contractDeprecateAddressees(mirrorDir, contractID, from, descriptorTo)
	if err != nil {
		t.Fatalf("contractDeprecateAddressees: %v", err)
	}
	for _, sys := range got {
		if sys == from {
			t.Fatalf("the producer %q addressed its own deprecation: %v", from, got)
		}
	}
}

// TestDeprecateAddresseesFallBackOnlyWhenNobodyRegistered pins the one case
// where the two sets legitimately differ: nobody has adopted yet, so the
// registered set is empty and an announcement with no recipients would be
// refused by the envelope schema — which is how `deprecate` became
// unauthorable, and therefore how `retire` became unreachable, in the first
// place. The fallback is the descriptor's `to:`, and it applies ONLY here.
func TestDeprecateAddresseesFallBackOnlyWhenNobodyRegistered(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	full := filepath.Join(mirrorDir, "axon", "provides", "widget", "contract.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("---\nschema: envelope/v1\nid: XC-axon-widget\nfrom: axon\nto: [delta]\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := contractDeprecateAddressees(mirrorDir, "XC-axon-widget", "axon", []string{"delta"})
	if err != nil {
		t.Fatalf("contractDeprecateAddressees: %v", err)
	}
	if len(got) != 1 || got[0] != "delta" {
		t.Fatalf("with nobody registered the descriptor's own to: is the fallback; got %v", got)
	}

	// With no fallback either, it refuses rather than authoring an
	// announcement the space would reject.
	if _, err := contractDeprecateAddressees(mirrorDir, "XC-axon-widget", "axon", nil); err == nil {
		t.Fatal("expected a refusal when there is neither a registered consumer nor a fallback recipient")
	}
}
