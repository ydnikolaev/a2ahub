package spacenotify

import (
	"sort"
	"strconv"
)

// sortMessages applies spec 03's own deterministic order: class first
// (human-gate, blocking, published, then digest last — a coalesced
// summary is never as urgent a read as a single named gate), then
// deadline, then artifact id. sort.SliceStable so two runs over an
// identical, already-deterministic build order (route declaration order x
// artifact-id order, both fixed upstream) produce byte-identical output
// (AC9).
func sortMessages(msgs []Message) {
	sort.SliceStable(msgs, func(i, j int) bool {
		a, b := msgs[i], msgs[j]
		if ra, rb := classRank(a.Class), classRank(b.Class); ra != rb {
			return ra < rb
		}
		if da, db := deadlineKey(a), deadlineKey(b); da != db {
			return da < db
		}
		return messageSortID(a) < messageSortID(b)
	})
}

func classRank(class string) int {
	switch class {
	case ClassHumanGate:
		return 0
	case ClassBlocking:
		return 1
	case ClassPublished:
		return 2
	default: // ClassDigest
		return 3
	}
}

// deadlineKey sorts an absent deadline LAST — spec 03's own field doc
// ("absent rather than guessed") extends here: nothing is inferred, an
// artifact with no needed_by simply sorts after every dated one.
func deadlineKey(m Message) string {
	if m.Urgency != nil && m.Urgency.Deadline != "" {
		return m.Urgency.Deadline
	}
	return "9999-99-99"
}

// messageSortID is the tiebreak key: the artifact id for a per-artifact
// message, or a deterministic composite of the route's own identity for a
// digest (which carries no single artifact id) — so multiple digests
// (different routes, same class/deadline rank) still sort stably against
// each other rather than depending on map iteration order anywhere
// upstream.
func messageSortID(m Message) string {
	if m.Artifact != nil {
		return m.Artifact.ID
	}
	if m.Digest != nil {
		topic := ""
		if m.Route.Topic != nil {
			topic = strconv.Itoa(*m.Route.Topic)
		}
		return "digest:" + m.Route.Chat + ":" + topic + ":" + m.Route.For
	}
	return ""
}
