package spacenotify

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestAddressed_ToFromAndBroadcast(t *testing.T) {
	t.Parallel()
	inbound := cache.NotifyArtifact{From: "axon", To: []string{"seomatrix"}, Addressees: []string{"seomatrix"}}
	if !addressed(inbound, "seomatrix") {
		t.Fatalf("want seomatrix addressed (inbound, in `to`)")
	}
	if addressed(inbound, "thirdsys") {
		t.Fatalf("want thirdsys NOT addressed")
	}
	if !addressed(inbound, "axon") {
		t.Fatalf("want axon addressed (outbound, == from)")
	}

	broadcast := cache.NotifyArtifact{From: "axon", Broadcast: true}
	if !addressed(broadcast, "anyone") {
		t.Fatalf("want a broadcast artifact addressed to any participant")
	}

	if addressed(inbound, "") {
		t.Fatalf("empty participant must never be addressed")
	}
}

// TestAddressed_LateAdopter is the filter half of AC11b/AC13's "reader
// this epic exists for": a system named ONLY through the complete
// addressee set (never in the envelope's own `to`) must still count as
// addressed, because cache.BuildNotifyIndex already folded it into
// fa.Addressees.
func TestAddressed_LateAdopter(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{From: "axon", To: []string{"seomatrix"}, Addressees: []string{"seomatrix", "latecomer"}}
	if !addressed(fa, "latecomer") {
		t.Fatalf("want the late adopter addressed via the complete Addressees set")
	}
}

func TestRouteMatches_ForAbsentMatchesEverything(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{From: "axon", To: []string{"seomatrix"}, Addressees: []string{"seomatrix"}}
	if !routeMatches(fa, space.NotificationRoute{}) {
		t.Fatalf("a route with no `for` must match every artifact")
	}
}

func TestRouteMatches_ForNarrows(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{From: "axon", To: []string{"seomatrix"}, Addressees: []string{"seomatrix"}}
	if !routeMatches(fa, space.NotificationRoute{For: "seomatrix"}) {
		t.Fatalf("want match: for=seomatrix, seomatrix is a receiving participant")
	}
	if routeMatches(fa, space.NotificationRoute{For: "thirdsys"}) {
		t.Fatalf("want NO match: for=thirdsys, thirdsys is not a party")
	}
}

// TestRouteMatches_MultipleToNames is AC12: a route matches when its own
// participant is one of SEVERAL systems named in `to`.
func TestRouteMatches_MultipleToNames(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{From: "axon", To: []string{"seomatrix", "thirdsys"}, Addressees: []string{"seomatrix", "thirdsys"}}
	if !routeMatches(fa, space.NotificationRoute{For: "thirdsys"}) {
		t.Fatalf("want match: thirdsys is one of several `to` names")
	}
}
