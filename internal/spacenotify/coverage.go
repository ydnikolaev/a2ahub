package spacenotify

// This file carries P11's own AC-11 written-reason note (answers-that-hold-
// 2026-08 spec 11 §11 amendment "AC-11's coverage obligation meets P5's
// funnel guard") — read by scripts/check-notify-selector-coverage.sh, never
// by any Go caller. It is a doc-comment marker, not code, because AC-11
// asks for a WRITTEN reason and the honest place to write one is beside the
// vocabulary it is about, not in docs/ (scripts/lib/strip-set.txt line 43
// removes that tree from every public checkout, which would make a gate
// reading it red locally and silently skip in public — see this package's
// own selector.go for the shape of "decode the same bytes a second time
// rather than depend on an off-limits file").
//
// AC-11 requires every fold.Kind and every fold.State to be reachable by
// some selector, or to carry a written reason. Because kind/state are
// matched by RAW STRING EQUALITY (selector.go's selectorMatches) with NO
// enumerated list anywhere in this package (AC-13: "no kind or state list
// exists in internal/spacenotify"), every kind and every state IS reachable
// by construction — a kind or state fold.BuildVocabulary() adds tomorrow is
// selectable with zero code change here. The note below names the ONE
// distinction that could be mistaken for a gap in that guarantee.
//
// coverage-reason: response/submitted — after no-silent-yes-2026-08 P5
// (W2), the draft→submitted transition (fold/table.go's TSubmit row,
// KindResponse/StateDraft -> StateSubmitted) stays legal-by-table while
// `a2a submit` can no longer reach it; it remains reachable via
// `a2a respond`'s own four declared conformance paths. The STATE itself is
// unaffected by that change and stays selectable via `state: [submitted]`
// exactly as before — the VERB that produced a state is not a fold
// vocabulary and must never become a selector dimension here.
