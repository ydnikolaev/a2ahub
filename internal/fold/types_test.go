package fold

import (
	"reflect"
	"testing"
)

// TestVersionNumbers pins the sorted accessor added alongside
// Result.Versions, in the shape of AckedRecipients/ResponseIDs — maps
// have no iteration order, so any output rendering sorts.
func TestVersionNumbers(t *testing.T) {
	t.Parallel()

	t.Run("sorted_regardless_of_insertion_order", func(t *testing.T) {
		t.Parallel()
		r := Result{Kind: KindContract, Versions: map[string]State{
			"2.0.0": StatePublished, "1.0.0": StateDeprecated, "1.5.0": StateRetired,
		}}
		got := r.VersionNumbers()
		want := []string{"1.0.0", "1.5.0", "2.0.0"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("VersionNumbers() = %v, want %v", got, want)
		}
	})

	t.Run("nil_versions_returns_empty_not_nil", func(t *testing.T) {
		t.Parallel()
		r := Result{Kind: KindQuestion}
		got := r.VersionNumbers()
		if got == nil {
			t.Fatal("VersionNumbers() returned nil, want an empty (non-nil) slice")
		}
		if len(got) != 0 {
			t.Fatalf("VersionNumbers() = %v, want empty", got)
		}
	})
}

// TestCloneDeepCopiesVersions pins that clone() deep-copies Versions
// (via the existing copyStateMap, per the brief) — Apply must never
// mutate a shared "prior" Versions map out from under a concurrent
// caller, exactly like every other mutable field on Result.
func TestCloneDeepCopiesVersions(t *testing.T) {
	t.Parallel()

	original := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}
	cloned := original.clone()

	cloned.Versions["1.0.0"] = StateDeprecated
	cloned.Versions["2.0.0"] = StatePublished

	if original.Versions["1.0.0"] != StatePublished {
		t.Fatalf("mutating the clone's Versions map mutated the original: %+v", original.Versions)
	}
	if _, exists := original.Versions["2.0.0"]; exists {
		t.Fatalf("a key added to the clone's Versions leaked into the original: %+v", original.Versions)
	}

	t.Run("nil_versions_stays_nil_through_clone", func(t *testing.T) {
		t.Parallel()
		r := Result{Kind: KindQuestion}
		if got := r.clone().Versions; got != nil {
			t.Fatalf("clone() of a nil Versions map produced %+v, want nil", got)
		}
	})
}

// TestApplyNeverMutatesPriorVersions is the Apply-level version of the
// clone guarantee above: two calls to Apply against the SAME prior
// Result must not see each other's Versions mutations (t.Parallel()-safe
// under -race, this package's own standing constraint).
func TestApplyNeverMutatesPriorVersions(t *testing.T) {
	t.Parallel()

	env := contractOwnerEnv()
	prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}

	eventA := Event{ULID: "01MUTATE0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Version: "2.0.0", Actor: contractOwnerActor()}
	eventB := Event{ULID: "01MUTATE0000000000000002", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Version: "3.0.0", Actor: contractOwnerActor()}

	gotA := Apply(KindContract, env, prior, eventA, alwaysMember)
	gotB := Apply(KindContract, env, prior, eventB, alwaysMember)

	if len(prior.Versions) != 1 {
		t.Fatalf("the shared prior's Versions must be untouched by either Apply call: %+v", prior.Versions)
	}
	if _, exists := gotA.Versions["3.0.0"]; exists {
		t.Fatalf("gotA leaked eventB's version: %+v", gotA.Versions)
	}
	if _, exists := gotB.Versions["2.0.0"]; exists {
		t.Fatalf("gotB leaked eventA's version: %+v", gotB.Versions)
	}
}
