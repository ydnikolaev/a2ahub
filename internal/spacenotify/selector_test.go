package spacenotify

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// TestSelectorMatches_KindFiltersByFact is AC-1: a route selecting
// `kind: contract` keeps contract artifacts and excludes every other kind.
func TestSelectorMatches_KindFiltersByFact(t *testing.T) {
	t.Parallel()
	sel := Selector{Kind: []string{"contract"}}
	contract := cache.NotifyArtifact{Kind: "contract"}
	requirement := cache.NotifyArtifact{Kind: "requirement"}
	if !selectorMatches(contract, space.NotificationRoute{}, sel) {
		t.Fatalf("want contract kept by kind: [contract]")
	}
	if selectorMatches(requirement, space.NotificationRoute{}, sel) {
		t.Fatalf("want requirement excluded by kind: [contract]")
	}
}

// TestSelectorMatches_StateFiltersByFact is AC-2: a route selecting a
// state keeps only artifacts that reached it.
func TestSelectorMatches_StateFiltersByFact(t *testing.T) {
	t.Parallel()
	sel := Selector{State: []string{"published"}}
	published := cache.NotifyArtifact{State: "published"}
	draft := cache.NotifyArtifact{State: "draft"}
	if !selectorMatches(published, space.NotificationRoute{}, sel) {
		t.Fatalf("want state=published kept by state: [published]")
	}
	if selectorMatches(draft, space.NotificationRoute{}, sel) {
		t.Fatalf("want state=draft excluded by state: [published]")
	}
}

// TestSelectorMatches_DirectionInboundIsRouteScoped is AC-3: direction:
// inbound receives what is addressed to the route's own `for:` system, and
// excludes what is not — even when the artifact is addressed to somebody
// else, or nobody at all.
func TestSelectorMatches_DirectionInboundIsRouteScoped(t *testing.T) {
	t.Parallel()
	sel := Selector{Direction: "inbound"}
	route := space.NotificationRoute{For: "seomatrix"}
	toMe := cache.NotifyArtifact{Addressees: []string{"seomatrix"}}
	toSomebodyElse := cache.NotifyArtifact{Addressees: []string{"thirdsys"}}
	toNobody := cache.NotifyArtifact{}
	if !selectorMatches(toMe, route, sel) {
		t.Fatalf("want addressed-to-seomatrix kept by direction: inbound, for: seomatrix")
	}
	if selectorMatches(toSomebodyElse, route, sel) {
		t.Fatalf("want addressed-to-thirdsys excluded by direction: inbound, for: seomatrix")
	}
	if selectorMatches(toNobody, route, sel) {
		t.Fatalf("want an artifact with no addressees excluded by direction: inbound")
	}
}

// TestSelectorMatches_DirectionBroadcast is the `broadcast` half of AC-3.
func TestSelectorMatches_DirectionBroadcast(t *testing.T) {
	t.Parallel()
	sel := Selector{Direction: "broadcast"}
	if !selectorMatches(cache.NotifyArtifact{Broadcast: true}, space.NotificationRoute{}, sel) {
		t.Fatalf("want a broadcast artifact kept by direction: broadcast")
	}
	if selectorMatches(cache.NotifyArtifact{Addressees: []string{"seomatrix"}}, space.NotificationRoute{For: "seomatrix"}, sel) {
		t.Fatalf("want a non-broadcast artifact excluded by direction: broadcast, even when addressed")
	}
}

// TestSelectorMatches_DirectionInboundNoForNeverMatches documents the
// route-with-no-`for:` edge case selector.go's own doc comment names: there
// is no "own system" to be inbound to, so direction: inbound matches
// nothing rather than silently falling back to a wider meaning.
func TestSelectorMatches_DirectionInboundNoForNeverMatches(t *testing.T) {
	t.Parallel()
	sel := Selector{Direction: "inbound"}
	fa := cache.NotifyArtifact{Addressees: []string{"seomatrix"}, Broadcast: true}
	if selectorMatches(fa, space.NotificationRoute{}, sel) {
		t.Fatalf("want direction: inbound with no route.For to match nothing")
	}
}

// TestSelectorMatches_Intersects is AC-4: dimensions INTERSECT — kind +
// direction keeps only an artifact satisfying BOTH.
func TestSelectorMatches_Intersects(t *testing.T) {
	t.Parallel()
	sel := Selector{Kind: []string{"contract"}, Direction: "inbound"}
	route := space.NotificationRoute{For: "seomatrix"}

	both := cache.NotifyArtifact{Kind: "contract", Addressees: []string{"seomatrix"}}
	onlyKind := cache.NotifyArtifact{Kind: "contract", Addressees: []string{"thirdsys"}}
	onlyDirection := cache.NotifyArtifact{Kind: "requirement", Addressees: []string{"seomatrix"}}

	if !selectorMatches(both, route, sel) {
		t.Fatalf("want an artifact satisfying kind AND direction kept")
	}
	if selectorMatches(onlyKind, route, sel) {
		t.Fatalf("want an artifact satisfying only kind excluded (direction fails)")
	}
	if selectorMatches(onlyDirection, route, sel) {
		t.Fatalf("want an artifact satisfying only direction excluded (kind fails)")
	}
}

// TestSelectorMatches_Traffic20260827 is AC-5: the incident's own traffic —
// a contract publish and an announcement addressed to a participant — is
// selectable by `kind: [contract, announcement]` without also selecting
// every requirement/question/etc note.
func TestSelectorMatches_Traffic20260827(t *testing.T) {
	t.Parallel()
	sel := Selector{Kind: []string{"contract", "announcement"}}
	contractPublish := cache.NotifyArtifact{Kind: "contract", State: "published"}
	announcement := cache.NotifyArtifact{Kind: "announcement", Addressees: []string{"seomatrix"}}
	ordinaryNote := cache.NotifyArtifact{Kind: "requirement"}

	if !selectorMatches(contractPublish, space.NotificationRoute{}, sel) {
		t.Fatalf("want the contract publish selected")
	}
	if !selectorMatches(announcement, space.NotificationRoute{}, sel) {
		t.Fatalf("want the announcement selected")
	}
	if selectorMatches(ordinaryNote, space.NotificationRoute{}, sel) {
		t.Fatalf("want an ordinary requirement note NOT selected")
	}
}

// TestSelectorMatches_UrgencyIsOR is the urgency dimension's own OR-within
// convention: a route naming several urgencies keeps an artifact
// satisfying ANY one of them.
func TestSelectorMatches_UrgencyIsOR(t *testing.T) {
	t.Parallel()
	sel := Selector{Urgency: []string{"p1", "blocking"}}
	p1 := cache.NotifyArtifact{Priority: "p1"}
	blocking := cache.NotifyArtifact{Blocking: true}
	neither := cache.NotifyArtifact{Priority: "p3"}
	if !selectorMatches(p1, space.NotificationRoute{}, sel) {
		t.Fatalf("want p1 kept by urgency: [p1, blocking]")
	}
	if !selectorMatches(blocking, space.NotificationRoute{}, sel) {
		t.Fatalf("want blocking kept by urgency: [p1, blocking]")
	}
	if selectorMatches(neither, space.NotificationRoute{}, sel) {
		t.Fatalf("want neither excluded")
	}
}

// TestSelectorMatches_ZeroValueConstrainsNothing is AC-7's own mechanism:
// an absent Selector keeps whatever routeMatches/events already kept.
func TestSelectorMatches_ZeroValueConstrainsNothing(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{Kind: "contract", State: "draft", Priority: "p3"}
	if !selectorMatches(fa, space.NotificationRoute{}, Selector{}) {
		t.Fatalf("want a zero-value Selector to constrain nothing")
	}
}

// TestSelectorMatches_KindNeverAppearingReachesAnythingByStringEquality is
// AC-13's own structural guarantee (there is no enumerated kind/state list
// in this package): a made-up kind name never produced by fold today is
// still handled by plain string equality, never a hardcoded switch that
// would need updating.
func TestSelectorMatches_KindNeverAppearingReachesAnythingByStringEquality(t *testing.T) {
	t.Parallel()
	sel := Selector{Kind: []string{"a-kind-fold-does-not-have-yet"}}
	fa := cache.NotifyArtifact{Kind: "a-kind-fold-does-not-have-yet"}
	if !selectorMatches(fa, space.NotificationRoute{}, sel) {
		t.Fatalf("want string equality to select ANY kind name, including one this test invented")
	}
}

// -- decodeSelectors ---------------------------------------------------

// TestDecodeSelectors_EmptyRawMeansNoSelectors is the "existing test
// fixture" case: manifestWith-built manifests never set Raw, and this must
// decode to len(routes) zero-value selectors, not an error.
func TestDecodeSelectors_EmptyRawMeansNoSelectors(t *testing.T) {
	t.Parallel()
	routes := []space.NotificationRoute{{Channel: "telegram", Chat: "-100"}}
	sels, err := decodeSelectors(nil, routes)
	if err != nil {
		t.Fatalf("decodeSelectors: %v", err)
	}
	if len(sels) != 1 {
		t.Fatalf("len(sels) = %d, want 1", len(sels))
	}
	if len(sels[0].Kind) != 0 || len(sels[0].State) != 0 || sels[0].Direction != "" || len(sels[0].Urgency) != 0 {
		t.Fatalf("sels[0] = %+v, want a zero-value Selector", sels[0])
	}
}

// TestDecodeSelectors_DecodesKindStateDirectionUrgency proves the raw-YAML
// round trip: a route's kind/state/direction/urgency keys decode into the
// aligned Selector.
func TestDecodeSelectors_DecodesKindStateDirectionUrgency(t *testing.T) {
	t.Parallel()
	raw := []byte(`
notification_routes:
  - channel: telegram
    chat: "-100"
    events: [published]
    kind: [contract, announcement]
    state: [published]
    direction: inbound
    urgency: [p1, blocking]
`)
	routes := []space.NotificationRoute{{Channel: "telegram", Chat: "-100", Events: []string{"published"}}}
	sels, err := decodeSelectors(raw, routes)
	if err != nil {
		t.Fatalf("decodeSelectors: %v", err)
	}
	want := Selector{Kind: []string{"contract", "announcement"}, State: []string{"published"}, Direction: "inbound", Urgency: []string{"p1", "blocking"}}
	got := sels[0]
	if len(got.Kind) != 2 || got.Kind[0] != "contract" || got.Kind[1] != "announcement" {
		t.Fatalf("Kind = %v, want %v", got.Kind, want.Kind)
	}
	if len(got.State) != 1 || got.State[0] != "published" {
		t.Fatalf("State = %v, want %v", got.State, want.State)
	}
	if got.Direction != want.Direction {
		t.Fatalf("Direction = %q, want %q", got.Direction, want.Direction)
	}
	if len(got.Urgency) != 2 || got.Urgency[0] != "p1" || got.Urgency[1] != "blocking" {
		t.Fatalf("Urgency = %v, want %v", got.Urgency, want.Urgency)
	}
}

// TestDecodeSelectors_ScalarKindIsOneElementList: `kind: contract` (a bare
// scalar) decodes the same as `kind: [contract]`.
func TestDecodeSelectors_ScalarKindIsOneElementList(t *testing.T) {
	t.Parallel()
	raw := []byte(`
notification_routes:
  - channel: telegram
    chat: "-100"
    events: [published]
    kind: contract
`)
	routes := []space.NotificationRoute{{Channel: "telegram", Chat: "-100", Events: []string{"published"}}}
	sels, err := decodeSelectors(raw, routes)
	if err != nil {
		t.Fatalf("decodeSelectors: %v", err)
	}
	if len(sels[0].Kind) != 1 || sels[0].Kind[0] != "contract" {
		t.Fatalf("Kind = %v, want [contract]", sels[0].Kind)
	}
}

// TestDecodeSelectors_CountMismatchRefuses guards the alignment: a raw
// notification_routes array that does not re-decode to the same length as
// the parsed routes must refuse rather than mis-attribute a selector.
func TestDecodeSelectors_CountMismatchRefuses(t *testing.T) {
	t.Parallel()
	raw := []byte(`
notification_routes:
  - {channel: telegram, chat: "-100", events: [published]}
  - {channel: telegram, chat: "-200", events: [published]}
`)
	routes := []space.NotificationRoute{{Channel: "telegram", Chat: "-100", Events: []string{"published"}}}
	if _, err := decodeSelectors(raw, routes); err == nil {
		t.Fatalf("decodeSelectors: want a refusal on a route-count mismatch, got nil error")
	}
}
