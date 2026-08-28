package spacenotify

import "github.com/ydnikolaev/a2ahub/internal/space"

// Accounting is P11's own per-Render denominator/kept report (spec 11
// ACs 8-10, US-2: "a run that matched nothing SAYS so"). Qualified is the
// candidate count BEFORE any per-route filtering (step 1's mode
// selection — push range / --all / --only); PerRoute carries one entry per
// DECLARED route, in manifest order, each with its own Kept count — never
// omitted for a zero-keep route, so a route that matched nothing is
// present and visibly Kept: 0, distinguishable from a route that was never
// declared at all, and Qualified: 0 is distinguishable from every route
// simply keeping zero of a nonzero qualifying set.
type Accounting struct {
	Qualified int
	PerRoute  []RouteAccounting
}

// RouteAccounting is one declared route's own identity (enough to label it
// on a report line — the same (channel, chat, topic, for) tuple
// checkNotificationRoutes already dedups routes by) plus how many
// artifacts it kept.
type RouteAccounting struct {
	Route space.NotificationRoute
	Kept  int
}
