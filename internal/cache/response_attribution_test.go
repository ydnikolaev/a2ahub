package cache

import (
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestOnlyResponseScopedEventsReachTheParent pins gatherEvents' attribution
// rule: of a response's own events, exactly `verify` and `dispute` cross over
// into its parent's stream, and nothing else does.
//
// The rule is not a style choice — it is the difference between a thread that
// renders clean and one that never can again. A response's own `submit` is
// keyed on the RESPONSE, and handing it to the parent makes fold evaluate
// (parentKind, parentState, submit). For a requirement no such row exists
// (requirementRows carries neither submit nor respond), so the event folds to
// a permanent illegal-transition flag on the requirement itself — and spec 46's
// reader promises a flagged conversation never renders clean.
//
// That is exactly what shipped, and the kind it bit is the one kind with no
// `respond` verb: for a question or work_request, `a2a respond` authors the
// PARENT's own respond event and no response-subject event at all, so the
// unfiltered append happened to carry nothing. A requirement's only prescribed
// fulfilment path is `a2a new response --field parent=<XR-id>` + `a2a submit`,
// so it was the one kind that fell into the hole. Found by W3c's requirement
// conformance path, not by any single-transition test.
//
// The gate is on the attribution, not on one kind's symptom, because the same
// hole reopens for any kind that later loses (or never had) a respond row.
func TestOnlyResponseScopedEventsReachTheParent(t *testing.T) {
	t.Parallel()

	const parentID, responseID = "XR-alpha-widget", "XS-beta-20260806-r1a2"

	parentOf := map[string]string{responseID: parentID}
	eventsBySubject := map[string][]fold.Event{
		parentID: {
			{ULID: "01A", Subject: parentID, Transition: fold.TPublish},
			{ULID: "01B", Subject: parentID, Transition: fold.TAcknowledge},
		},
		responseID: {
			// The response's OWN lifecycle. Neither belongs to the parent.
			{ULID: "01C", Subject: responseID, Transition: fold.TCreate},
			{ULID: "01D", Subject: responseID, Transition: fold.TSubmit},
			// Response-SCOPED: these do belong to the parent, because fold
			// resolves them against the parent's envelope (the documented
			// response-scoped trap in legality.go).
			{ULID: "01E", Subject: responseID, Transition: fold.TVerify},
			{ULID: "01F", Subject: responseID, Transition: fold.TDispute},
		},
	}

	got := gatherEvents(parentID, parentOf, eventsBySubject)

	var crossed []string
	own := map[string]bool{"01A": true, "01B": true}
	for _, ev := range got {
		if !own[ev.ULID] {
			crossed = append(crossed, ev.Transition)
		}
	}
	sort.Strings(crossed)
	want := []string{fold.TDispute, fold.TVerify}
	sort.Strings(want)

	if strings.Join(crossed, ",") != strings.Join(want, ",") {
		t.Fatalf("events crossing from the response into %s's stream = %v, want %v.\n"+
			"Anything else here is applied against the PARENT's kind and state: a "+
			"response's own submit against a requirement has no row, folds to a "+
			"permanent illegal-transition flag, and the thread never renders clean.",
			parentID, crossed, want)
	}

	// The parent's own events must survive untouched — a filter that dropped
	// them would also make this test's first half pass.
	if len(got) != len(own)+len(want) {
		t.Fatalf("gatherEvents returned %d events, want %d (2 of the parent's own + 2 response-scoped) — "+
			"the parent's own stream must be preserved whole", len(got), len(own)+len(want))
	}
}

// TestAResponseKeepsItsOwnLifecycleEvents is the paired control: the same
// filter must not strip a response's own create/submit from the RESPONSE's
// stream while removing verify/dispute from it. Without this, a filter that
// simply dropped everything would satisfy the test above.
func TestAResponseKeepsItsOwnLifecycleEvents(t *testing.T) {
	t.Parallel()

	const parentID, responseID = "XR-alpha-widget", "XS-beta-20260806-r1a2"
	parentOf := map[string]string{responseID: parentID}
	eventsBySubject := map[string][]fold.Event{
		responseID: {
			{ULID: "01C", Subject: responseID, Transition: fold.TCreate},
			{ULID: "01D", Subject: responseID, Transition: fold.TSubmit},
			{ULID: "01E", Subject: responseID, Transition: fold.TVerify},
		},
	}

	got := gatherEvents(responseID, parentOf, eventsBySubject)

	var kept []string
	for _, ev := range got {
		kept = append(kept, ev.Transition)
	}
	sort.Strings(kept)
	want := []string{fold.TCreate, fold.TSubmit}
	sort.Strings(want)
	if strings.Join(kept, ",") != strings.Join(want, ",") {
		t.Fatalf("a response's own stream = %v, want %v (its own lifecycle stays, "+
			"verify/dispute leave for the parent)", kept, want)
	}
}
