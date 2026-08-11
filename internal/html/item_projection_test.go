package html

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// itemFieldsThisPackageDeliberatelyDrops names the cache.Item fields Item is
// allowed NOT to carry under the SAME name, each with the reason it is a
// decision rather than an oversight.
//
// Two different things land here, and both are deliberate:
//
//   - A field this package renames on projection. toItem (assemble.go)
//     already renames LatestEventAt/LatestEventSeq/LatestEventID to
//     MovedAt/ActivitySeq/ActivityEventID — real fields carrying the real
//     fact, just not the same identifier. The reflection check below only
//     matches NAMES (openitem_projection_test.go's own convention, chosen
//     there for the same reason), so a rename reads exactly like a drop and
//     has to be argued into this map like one.
//   - A field this package genuinely does not carry per-row because a
//     DIFFERENT allowlisted consumer already reads it. WaitingOn and
//     ExpectedTransition are pendency.Resolve's verdict for one artifact
//     (mirrored 1:1 from cache.OpenItem's own field names); exchangeEdges
//     aggregates them into ExchangeEdge.OwedCount, and no per-row Item field
//     duplicates that today.
var itemFieldsThisPackageDeliberatelyDrops = map[string]string{
	"LatestEventAt":      "renamed to MovedAt by toItem (assemble.go) — the activity clock, distinct from CreatedAt",
	"LatestEventSeq":     "renamed to ActivitySeq by toItem — the committed-order tie-breaker for MovedAt",
	"LatestEventID":      "renamed to ActivityEventID by toItem — the current transition identity",
	"WaitingOn":          "consumed by the exchange overlay's edge aggregation (exchangeEdges -> ExchangeEdge.OwedCount), not by the per-row Item — see ExchangeEdge's own doc comment",
	"ExpectedTransition": "same as WaitingOn: an edge-level pendency fact today, not a per-row one",
	"OperationalItems": "P5 AC4's operational projection reaches this SURFACE through " +
		"ArtifactDetail and ThreadOpenItem, which both carry it — not through this list " +
		"row. The criterion asks that `undeclared` read distinctly from `absent` on the " +
		"html surface, and the artifact page is where a reader asks that question; a " +
		"contract's row in a list answers `what is this and who owes a move`, and three " +
		"per-item readiness rows on every contract in the list is noise, not fidelity. " +
		"Carry it here the day a list view has a reason to show it.",
	"Why": "same again. P2 put the relation's justification on inbox/outbox --json, where a reader has no other way to ask why; the dashboard shows it on the THREAD item (ThreadOpenItem.Why), which is where a reader who wants the reasoning already is",
}

// TestItemCarriesEveryCacheItemField is the drift gate for the projection
// cache.Item -> html.Item. internal/html/openitem_projection_test.go already
// guards cache.OpenItem -> html.ThreadOpenItem by reflection; nothing guarded
// this half, and that is exactly how a field can be added to cache.Item and
// never reach the dashboard without a single compile error or red test
// anywhere — both types are free to diverge in Go, and the loss is only
// found by rendering a real space and diffing the JSON by eye (same failure
// mode openitem_projection_test.go's own doc comment names).
//
// It deliberately checks names, not types or tags, for the same reason that
// file does: a rename on either side is a real event worth failing on, while
// the JSON tag is this package's own choice.
func TestItemCarriesEveryCacheItemField(t *testing.T) {
	t.Parallel()

	source := fieldNames(reflect.TypeOf(cache.Item{}))
	projection := map[string]bool{}
	for _, name := range fieldNames(reflect.TypeOf(Item{})) {
		projection[name] = true
	}

	if len(source) == 0 || len(projection) == 0 {
		t.Fatal("one of the two types has no exported fields — reflection found nothing to compare and a green result here would mean nothing")
	}

	var missing []string
	for _, name := range source {
		if projection[name] {
			continue
		}
		if reason, deliberate := itemFieldsThisPackageDeliberatelyDrops[name]; deliberate {
			t.Logf("cache.Item.%s is deliberately not projected under that name: %s", name, reason)
			continue
		}
		missing = append(missing, name)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("Item drops %d field(s) of cache.Item: %s\n"+
			"Either carry it (under the same name), or add it to itemFieldsThisPackageDeliberatelyDrops with the reason it is a decision.",
			len(missing), strings.Join(missing, ", "))
	}
}
