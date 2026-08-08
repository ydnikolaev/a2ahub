package cache

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// This file closes the LAST unguarded projection boundary in the read path.
//
// internal/html already guards its two: cache.Item -> html.Item and
// cache.OpenItem -> html.ThreadOpenItem, both by reflection over field names.
// The boundary BELOW those — foldedArtifact (the fold's own output plus this
// package's side tables) -> cache.Item — is hand-built in toItem and
// buildOpenItems and was checked by nothing.
//
// That gap is worse than it sounds, and worse than either of the two that were
// already covered: a field lost HERE never reaches html.Item either, so both
// existing gates stay green while the fact is invisible on every surface at
// once. P0 is the first phase to cross this boundary and P3 through P6 all
// cross it again, which is why the gate lands with the first crossing rather
// than after the first loss.

// foldedFieldsItemDeliberatelyDrops names the foldedArtifact fields cache.Item
// is allowed not to carry, each with the reason it is a decision. Same
// convention as internal/html's two gates: names, not types or tags, because a
// rename on either side is a real event worth failing on.
var foldedFieldsItemDeliberatelyDrops = map[string]string{
	"RelPath":              "the mirror path on disk; an Item is addressed by (Space, ID), and leaking a checkout-relative path onto the wire would make the JSON machine-specific",
	"Raw":                  "the artifact bytes. Item carries Description, a bounded summary derived from them (bodySummary); shipping the whole body would put an unbounded, untrusted payload on inbox/outbox --json",
	"Digest":               "content addressing for the mirror's own change detection, not a fact about the exchange",
	"Env":                  "the envelope probe is EXPLODED into Item rather than nested: Space, ID, Type, Title, From, To, Priority, Blocking, NeededBy and Thread all come from it",
	"Result":               "the fold result is likewise exploded: State comes from it, and P0 added Outcome and Terminal as the domain's own reading of it",
	"Events":               "the event stream itself. `a2a thread` renders it; an inbox row is a summary, and carrying every event per row would make the list quadratic in history",
	"EventEvidence":        "receipt/actor/producer diagnostics, rendered by the thread transcript rather than the row",
	"ReceiptMismatches":    "consistency diagnostics; surfaced as ThreadFlag on the thread view, where a reader can act on them",
	"EventRefs":            "per-event relationship metadata, consumed by the thread view's own link resolution",
	"EventAt":              "the ULID -> timestamp side table. Item carries the two instants that matter — CreatedAt and LatestEventAt — plus, since P0, StateSince",
	"EventNotes":           "per-event note bodies, rendered by the transcript",
	"EventReasonCodes":     "per-event machine-readable reason codes, rendered by the transcript beside the note they used to be substituted for. An inbox row shows the artifact's current state, not why one particular event was refused",
	"LatestPublishVersion": "a contract-only projection consumed by the contract views, not by a generic item row",
	"SpaceID":              "renamed to Space by toItem",
	"StateEventID":         "renamed to StateEvent by toItem — the ULID of the event that produced State",
	"Seq":                  "renamed to CreatedSeq by toItem — the artifact's own creation order",
	"OrderKnown":           "renamed to CreatedOrderKnown by toItem",
	// The two overlay booleans below are computed per-artifact during the
	// mirror walk and consumed by callers that build their own views from
	// foldedArtifact directly.
	"DeprecatesMyDependency": "a dependency-drift overlay consumed by the contract/drift views, not a per-row inbox fact",
	"FulfillingResponse":     "a thread-shaped overlay: whether this response fulfils its parent, which the thread view resolves against the parent it already has",
}

// TestItemCarriesEveryFoldedArtifactField is the third projection gate.
func TestItemCarriesEveryFoldedArtifactField(t *testing.T) {
	t.Parallel()

	source := exportedFieldNames(reflect.TypeOf(foldedArtifact{}))
	projection := map[string]bool{}
	for _, name := range exportedFieldNames(reflect.TypeOf(Item{})) {
		projection[name] = true
	}

	if len(source) == 0 || len(projection) == 0 {
		t.Fatal("one of the two types has no exported fields — reflection found nothing to compare, and a green result here would mean nothing")
	}

	var missing []string
	for _, name := range source {
		if projection[name] {
			continue
		}
		if reason, deliberate := foldedFieldsItemDeliberatelyDrops[name]; deliberate {
			t.Logf("foldedArtifact.%s is deliberately not projected: %s", name, reason)
			continue
		}
		missing = append(missing, name)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("cache.Item drops %d field(s) of foldedArtifact: %s\n"+
			"A field lost at THIS boundary never reaches html.Item either, so both existing projection gates stay green while it is invisible everywhere.\n"+
			"Either carry it, or add it to foldedFieldsItemDeliberatelyDrops with the reason it is a decision.",
			len(missing), strings.Join(missing, ", "))
	}
}

// TestDropListNamesOnlyRealFields keeps the exemption list honest in the other
// direction. A stale entry — a field that was renamed or removed — reads as a
// considered decision forever while guarding nothing, which is how an
// allowlist becomes a place to hide things.
func TestDropListNamesOnlyRealFields(t *testing.T) {
	t.Parallel()

	real := map[string]bool{}
	for _, name := range exportedFieldNames(reflect.TypeOf(foldedArtifact{})) {
		real[name] = true
	}
	for name := range foldedFieldsItemDeliberatelyDrops {
		if !real[name] {
			t.Errorf("foldedFieldsItemDeliberatelyDrops names %q, which foldedArtifact no longer has — delete the entry rather than leaving a dead exemption", name)
		}
	}
}

// TestOpenOutcomeImpliesOpenState holds the two kind-aware maps to the one
// direction that must be true: if the DOMAIN calls a state live, this
// package's own openStates allowlist must agree it is live, or an artifact
// the protocol considers unfinished silently stops appearing in the open set.
//
// The converse is deliberately NOT asserted, and the cell that proves why is
// handoff/rejected: openStates lists it (§3.4.5 puts the producer on the hook
// to resubmit, guarded by TestOpenStatesReachesEveryStateSomebodyOwesAMoveFrom)
// while fold calls it `refused`, because the verification did not pass. Both
// are true. Three axes, three homes: what a state MEANS (fold.OutcomeOf),
// whether anything can follow it (fold.Terminal), and whether somebody owes a
// move (openStates, then pendency).
func TestOpenOutcomeImpliesOpenState(t *testing.T) {
	t.Parallel()

	for _, krs := range fold.RestingStates() {
		if fold.OutcomeOf(krs.Kind, krs.State) != fold.OutcomeOpen {
			continue
		}
		if !isOpen(krs.Kind, krs.State) {
			t.Errorf("fold calls (%s, %s) OutcomeOpen but this package's openStates does not list it — an artifact the protocol considers live would vanish from every open-set surface", krs.Kind, krs.State)
		}
	}
}

func exportedFieldNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.IsExported() {
			out = append(out, f.Name)
		}
	}
	return out
}
