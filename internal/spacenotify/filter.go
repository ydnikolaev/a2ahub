package spacenotify

import (
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// addressed reports whether participant is a party this artifact concerns
// — spec 03's own "Recipient filtering": the route's participant appears
// in `to` (inbound) or equals `from` (outbound). "Inbound" here reads the
// COMPLETE addressee set BuildNotifyIndex resolved (fa.Addressees: `to`
// normalized, UNIONED with every participant whose own consumes.yaml
// names a contract this artifact deprecates) rather than the envelope's
// literal `to:` alone.
//
// DEVIATION from spec 03's own literal wording (recorded in this phase's
// report): the "Recipient filtering" prose names only `to`/`from`, but
// AC11b requires a late adopter whose consumes.yaml is the ONLY evidence
// to be reachable — under a literal `to`-only reading that adopter never
// appears in any route's kept set and AC11b is unwritable. This widens
// "inbound" the same way addressedToMe (internal/cache/inbox.go) already
// widens it for one system, generalized to every participant.
func addressed(fa cache.NotifyArtifact, participant string) bool {
	if participant == "" {
		return false
	}
	if fa.From == participant {
		return true
	}
	if fa.Broadcast {
		return true
	}
	for _, a := range fa.Addressees {
		if a == participant {
			return true
		}
	}
	return false
}

// routeMatches reports whether fa concerns route at all — the `for` half
// of step 4 ("receiving participant matches for (absent = all)"). A route
// naming no participant matches every artifact; a route naming one
// matches only artifacts that participant is addressed to (addressed,
// above).
func routeMatches(fa cache.NotifyArtifact, route space.NotificationRoute) bool {
	if route.For == "" {
		return true
	}
	return addressed(fa, route.For)
}
