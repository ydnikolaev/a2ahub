package skillcoverage

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// --- local fixture types, never the real domain types, so this file keeps
// asserting the WALKER's behaviour rather than re-describing cache.Item
// every time a real field is added or renamed. ---

type fixtureInner struct {
	Kept    string `json:"kept"`
	Skipped string `json:"-"`
	Bare    string // no tag at all: encoding/json uses the Go field name
}

type fixtureUntaggedEmbed struct {
	fixtureInner // no json tag: inlined, contributes its OWN keys, no "fixtureinner" key
	Own          string `json:"own"`
}

type fixtureTaggedEmbed struct {
	Inner fixtureInner `json:"inner"` // named field of embed type: NOT anonymous, ordinary nesting
	Own   string       `json:"own2"`
}

type fixtureUnexported struct {
	Visible string `json:"visible"`
	hidden  string //nolint:unused // exists only to prove reflection skips unexported fields
}

type fixturePointerSliceMap struct {
	Ptr   *fixtureInner            `json:"ptr,omitempty"`
	Slice []fixtureInner           `json:"slice,omitempty"`
	Map   map[string]fixtureInner  `json:"map,omitempty"`
	Grid  [][]fixtureInner         `json:"grid,omitempty"`
	MapP  map[string]*fixtureInner `json:"mapp,omitempty"`
}

type fixtureCyclic struct {
	Self  *fixtureCyclic `json:"self,omitempty"`
	Value string         `json:"value"`
}

func mustEqualSet(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSurfaceKeysPlainStruct(t *testing.T) {
	t.Parallel()
	got := SurfaceKeys(reflect.TypeOf(fixtureInner{}))
	mustEqualSet(t, got, "kept", "Bare")
}

func TestSurfaceKeysHonoursJSONDash(t *testing.T) {
	t.Parallel()
	got := SurfaceKeys(reflect.TypeOf(fixtureInner{}))
	for _, k := range got {
		if k == "Skipped" || k == "skipped" {
			t.Fatalf("json:\"-\" field leaked into the key set: %v", got)
		}
	}
}

func TestSurfaceKeysInlinesUntaggedEmbed(t *testing.T) {
	t.Parallel()
	got := SurfaceKeys(reflect.TypeOf(fixtureUntaggedEmbed{}))
	// The embed contributes its OWN fields directly, no "fixtureinner" or
	// "FixtureInner" key of its own — that is what "inlined" means.
	mustEqualSet(t, got, "kept", "Bare", "own")
}

func TestSurfaceKeysNestsTaggedEmbed(t *testing.T) {
	t.Parallel()
	got := SurfaceKeys(reflect.TypeOf(fixtureTaggedEmbed{}))
	// A NAMED field of the embed's type is ordinary nesting: "inner" is a
	// key in its own right, and its own fields are still reachable (the
	// walk still recurses into it) but do not require a SEPARATE key,
	// because this is a flat key SET, not a path set (see this package's
	// own SurfaceKeys doc comment).
	mustEqualSet(t, got, "inner", "own2", "kept", "Bare")
}

func TestSurfaceKeysSkipsUnexportedFields(t *testing.T) {
	t.Parallel()
	got := SurfaceKeys(reflect.TypeOf(fixtureUnexported{}))
	mustEqualSet(t, got, "visible")
}

func TestSurfaceKeysUnwrapsPointerSliceMap(t *testing.T) {
	t.Parallel()
	got := SurfaceKeys(reflect.TypeOf(fixturePointerSliceMap{}))
	// Every wrapper key itself, PLUS fixtureInner's own keys reached
	// through every one of the five unwrapping paths (pointer, slice, map
	// value, nested slice-of-slice, map-of-pointer).
	mustEqualSet(t, got, "ptr", "slice", "map", "grid", "mapp", "kept", "Bare")
}

func TestSurfaceKeysCyclicTypeTerminates(t *testing.T) {
	t.Parallel()
	done := make(chan []string, 1)
	go func() { done <- SurfaceKeys(reflect.TypeOf(fixtureCyclic{})) }()
	select {
	case got := <-done:
		mustEqualSet(t, got, "self", "value")
	case <-time.After(2 * time.Second):
		// A cyclic-type bug here is an infinite loop, not a slow one — a
		// short bound is enough to fail fast without making this test
		// flaky under load.
		t.Fatal("SurfaceKeys did not terminate on a self-referential type")
	}
}

func TestSurfaceKeysNonStructReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := SurfaceKeys(reflect.TypeOf("")); len(got) != 0 {
		t.Fatalf("SurfaceKeys(string) = %v, want empty", got)
	}
	if got := SurfaceKeys(reflect.TypeOf(42)); len(got) != 0 {
		t.Fatalf("SurfaceKeys(int) = %v, want empty", got)
	}
}

// TestSurfaceKeysOverCacheItem is the smoke test against a REAL domain
// type: proves the six derived-state fields P13 exists to close
// (agent-exchange-2026-08 spec 13 §3.1 / audit finding) are reachable by
// this walker, and that a Go-only (`json:"-"`) field is not.
func TestSurfaceKeysOverCacheItem(t *testing.T) {
	t.Parallel()
	got := SurfaceKeys(reflect.TypeOf(cache.Item{}))
	set := map[string]bool{}
	for _, k := range got {
		set[k] = true
	}
	for _, want := range []string{"outcome", "terminal", "state_since", "state_by", "state_event"} {
		if !set[want] {
			t.Errorf("cache.Item's reflected key set is missing %q — the whole point of this reflection is that it cannot miss a wire field", want)
		}
	}
	for _, mustNotAppear := range []string{"Description", "LatestEventAt", "LatestEventSeq", "LatestEventID", "CreatedAt", "CreatedSeq", "CreatedOrderKnown", "YourMove"} {
		if set[mustNotAppear] {
			t.Errorf("cache.Item's reflected key set wrongly includes %q, which is json:\"-\" (Go-only, never on the wire)", mustNotAppear)
		}
	}
}
