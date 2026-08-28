package spacenotify

import (
	"fmt"
	"slices"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// Selector is P11's own widening (answers-that-hold-2026-08 spec 11 §T1):
// the four dimensions a route may ADD on top of its existing `for`/`events`
// filter — kind, state, direction, urgency — each a pure predicate over
// facts cache.NotifyArtifact already carries. A zero-value Selector
// constrains nothing: an existing `events: [...]` route with none of these
// keys behaves EXACTLY as it did before this phase (AC-7, "a widening,
// never a migration").
//
// Deliberately no field here is a fold.Kind/fold.State — both are matched
// by RAW STRING EQUALITY against fa.Kind/fa.State (themselves already
// plain strings on cache.NotifyArtifact), so this package never needs its
// own copy of either vocabulary (AC-13: "no kind or state list exists in
// internal/spacenotify"). Every kind or state fold can ever produce is
// therefore selectable the moment it starts appearing on artifacts, with
// zero code change here — the property spec 11 US-5 asks for.
type Selector struct {
	// Kind is a subset of fold.Kind values (matched as plain strings); nil
	// or empty means "any kind".
	Kind []string
	// State is a subset of fold.State values (matched as plain strings);
	// nil or empty means "any state".
	State []string
	// Direction is "", "any", "inbound", or "broadcast". "" and "any" both
	// mean unconstrained. "inbound" means the artifact's Addressees name
	// THIS route's own `for:` participant (route-scoped — see
	// directionMatches' own doc comment for why this must NOT be
	// globalInbound). "broadcast" means fa.Broadcast.
	Direction string
	// Urgency is a subset of {"human-gate", "p1", "blocking", "overdue"};
	// nil or empty means "any urgency". An artifact matches when it
	// satisfies AT LEAST ONE named urgency (OR within the dimension, the
	// same convention Kind/State use).
	Urgency []string
}

// selectorMatches ANDs Selector's four dimensions on top of keepForRoute's
// existing `for`/`events` checks — an absent dimension constrains nothing
// (T1's "widening"). It reads only facts already resolved once per
// artifact (fa) and the route's own declared `for:`; it performs no I/O and
// no per-route re-resolution of any artifact fact (spec 11's own
// constraint).
func selectorMatches(fa cache.NotifyArtifact, route space.NotificationRoute, sel Selector) bool {
	if len(sel.Kind) > 0 && !slices.Contains(sel.Kind, fa.Kind) {
		return false
	}
	if len(sel.State) > 0 && !slices.Contains(sel.State, fa.State) {
		return false
	}
	if !directionMatches(fa, route, sel.Direction) {
		return false
	}
	if len(sel.Urgency) > 0 && !urgencyMatches(fa, sel.Urgency) {
		return false
	}
	return true
}

// directionMatches is the `direction` dimension (spec 11 §T1's table row):
// "inbound to this route's `for:` system · broadcast · any".
//
// "inbound" is deliberately ROUTE-SCOPED — fa.Addressees must name THIS
// route's own `route.For` — never globalInbound's route-independent "has
// any addressee at all". A route with no `for:` has no "own system" to be
// inbound to, so `direction: inbound` matches nothing for it (visible via
// the accounting line, per US-2, rather than silently falling back to a
// different meaning).
func directionMatches(fa cache.NotifyArtifact, route space.NotificationRoute, direction string) bool {
	switch direction {
	case "", "any":
		return true
	case "inbound":
		return route.For != "" && slices.Contains(fa.Addressees, route.For)
	case "broadcast":
		return fa.Broadcast
	default:
		// An unrecognised value never matches anything — the same
		// fail-closed answer an unrecognised kind/state name gets from
		// slices.Contains above, and the SAME reason: a typo surfaces as a
		// visible zero-keep route (US-2), not a silent fallback to "any".
		return false
	}
}

// urgencyMatches reports whether fa satisfies at least one of want's named
// urgencies — human-gate | p1 | blocking | overdue, the existing
// predicates already computed on fa, named individually instead of
// pre-mixed into one class (spec 11 §T1).
func urgencyMatches(fa cache.NotifyArtifact, want []string) bool {
	for _, w := range want {
		switch w {
		case "human-gate":
			if fa.Verdict.HumanGate != "" {
				return true
			}
		case "p1":
			if fa.Priority == "p1" {
				return true
			}
		case "blocking":
			if fa.Blocking {
				return true
			}
		case "overdue":
			if fa.Overdue {
				return true
			}
		}
	}
	return false
}

// notifyRouteSelectorProbe is the raw-YAML shape selector.go decodes P11's
// four new keys from — the SAME "decode `Raw` a second time, narrowly"
// pattern internal/validate/manifest.go's own manifestProbe/
// manifestRouteParsed already establishes for exactly this reason: a
// frozen, additionalProperties:true schema accepts a key that
// space.NotificationRoute (internal/space/manifest.go) does not itself
// declare, and this package's own allowlist does not extend to that file
// — see spec 11 §11 amendment "the manifest schema is not edited at all".
type notifyRouteSelectorProbe struct {
	Kind      any    `yaml:"kind"`
	State     any    `yaml:"state"`
	Direction string `yaml:"direction"`
	Urgency   any    `yaml:"urgency"`
}

type notifyManifestSelectorProbe struct {
	NotificationRoutes []notifyRouteSelectorProbe `yaml:"notification_routes"`
}

// decodeSelectors re-decodes manifest.Raw's own `notification_routes[]`
// array into one Selector per route, index-aligned with routes (the
// exact array space.ParseManifest already decoded manifest.NotificationRoutes
// from — same bytes, same document, deterministic array order).
//
// Empty/absent raw (every existing spacenotify test's own
// manifestWith-built fixture, which never sets Manifest.Raw) decodes to
// exactly len(routes) zero-value Selectors — "no selector keys declared",
// byte-identical to this phase's own behaviour before it shipped (AC-7).
//
// A non-empty raw that does not re-decode to the SAME route count as
// routes is refused rather than guessed at: silently zipping a
// mismatched-length selector list against routes would risk attributing
// route i's selector to route j, which is worse than refusing outright.
func decodeSelectors(raw []byte, routes []space.NotificationRoute) ([]Selector, error) {
	out := make([]Selector, len(routes))
	if len(raw) == 0 {
		return out, nil
	}
	var probe notifyManifestSelectorProbe
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		// Malformed YAML at this point would already have failed
		// space.ParseManifest, which produced `routes` in the first
		// place — unreachable in practice, but fail closed (no
		// selectors) rather than guess.
		return out, nil
	}
	if len(probe.NotificationRoutes) != len(routes) {
		return nil, fmt.Errorf(
			"notify render: manifest.Raw re-decodes %d notification_routes, but the parsed manifest carries %d — refusing to attribute a selector to the wrong route",
			len(probe.NotificationRoutes), len(routes),
		)
	}
	for i, p := range probe.NotificationRoutes {
		out[i] = Selector{
			Kind:      toStringSlice(p.Kind),
			State:     toStringSlice(p.State),
			Direction: p.Direction,
			Urgency:   toStringSlice(p.Urgency),
		}
	}
	return out, nil
}

// toStringSlice accepts either a bare scalar (`kind: contract`) or a list
// (`kind: [contract, announcement]`) — yaml.v3 decodes both into `any`
// differently (string vs []any) — and normalizes to []string. Any other
// shape (a mapping, a number) yields nil, i.e. "no values named" — the
// SAME fail-closed convention directionMatches' default case documents:
// a malformed or empty selector value constrains nothing beyond what an
// absent key already would.
func toStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
