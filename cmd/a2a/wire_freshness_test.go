package main

import (
	"sort"
	"testing"
)

// TestFreshReadVerbsClassifiesEveryReadVerb is the guard AC-1050.4 needs and
// cannot get from an argument.
//
// The claim being defended: `statusline` performs no synchronous mirror fetch.
// The reason a comment is not enough: statusline's <100ms budget is asserted on
// cache.Store.Statusline (internal/cache/statusline_test.go), NOT through
// cmd/a2a's wiring — so adding "statusline" to freshReadVerbs would put a
// `git fetch` in every shell prompt while every existing unit test stayed
// green. A hand-verified invariant is a gate nobody wrote yet.
//
// It guards from BOTH sides deliberately:
//
//   - statusline is not in the set (the regression that matters);
//   - every member of the set is a real read verb (no dead entry drifting after
//     a rename);
//   - the set plus statusline covers readVerbs() EXACTLY, so a NEW read verb
//     reds this test until someone classifies it. That is the point: the
//     default for a new read verb must be a decision, not silence — an
//     unclassified verb would silently join the non-fetching side and
//     reproduce the very defect P45 exists to kill.
func TestFreshReadVerbsClassifiesEveryReadVerb(t *testing.T) {
	t.Parallel()

	all := readVerbs()

	if freshReadVerbs["statusline"] {
		t.Error("statusline must NOT be in freshReadVerbs: it renders in a shell prompt under a <100ms budget " +
			"and already refreshes via its own detached goroutine — a synchronous git fetch there is the regression " +
			"this test exists to catch, and internal/cache's perf assertion cannot see it")
	}

	for verb := range freshReadVerbs {
		if _, ok := all[verb]; !ok {
			t.Errorf("freshReadVerbs names %q, which readVerbs() does not dispatch — a stale entry after a rename", verb)
		}
	}

	var unclassified []string
	for verb := range all {
		if verb == "statusline" {
			continue // the one deliberate exclusion, asserted above
		}
		if !freshReadVerbs[verb] {
			unclassified = append(unclassified, verb)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("read verb(s) %v are in neither freshReadVerbs nor the statusline exclusion: classify them. "+
			"A read verb that silently skips the staleness refresh reports \"nothing\" for an artifact the "+
			"counterparty already published (spec 45 AC-1050.1)", unclassified)
	}
}
