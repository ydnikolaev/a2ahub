package livee2e

import (
	"fmt"
	"sort"
	"strings"
)

// verbclasses.go is P10's own machinery (answers-that-hold-2026-08 spec 10
// "Two verbs cannot answer one question differently" §T1): THREE verb
// classes, TWO assertion shapes, and the classify-or-declare obligation
// over the roster of verbs this phase's own declared pairs name.
//
// # Why three classes, not two
//
// A design carrying only {previewing, acting} would classify a CHECKING
// verb — one that runs AFTER the actor, over what the actor already
// accepted — as either a preview or an actor by default. Filed as a
// preview, its own refusal lands inside the legitimate "a conservative
// preview may refuse" exemption (assertDirectionalCell's own doc comment),
// and the epic's own flagship instance (fb-20260827-a84550,
// `verify-export` refusing an input `publish` already accepted) would stay
// green under that model. `pairActingChecking` exists so that polarity is
// never protected by the previewing/acting exemption — the SAME assertion
// function, assertDirectionalCell, is used for both, but the CLASS a pair
// declares is never inferred from the verbs' names; it is stated once, by
// the caller, and carried through to every report.
//
// # Why two assertion shapes, not one
//
// previewing→acting and acting→checking are both DIRECTIONAL: one verb's
// PASS forbids the other's REFUSE, and the converse is legitimate (a
// conservative preview, or a checker with nothing to check, may refuse
// where the other side did not). reader↔reader carries NO direction at
// all — every reader of one question must return the SAME answer for one
// input, full stop. A design that only knows direction would construct the
// two reader↔reader cells this epic needs (the packed-README and
// blob-payload path-shape disagreements) and pass them by never asking the
// right question of them.
type pairClass string

const (
	// pairPreviewingActing is class 1: preview PASS ⇒ the actor must not
	// REFUSE. The converse (preview refuses where the actor would have
	// accepted) is legitimate and must never red.
	pairPreviewingActing pairClass = "previewing_acting"
	// pairActingChecking is class 2: the actor accepted ⇒ the checker must
	// not REFUSE. This is the polarity a two-class model would exempt —
	// see this file's own doc comment.
	pairActingChecking pairClass = "acting_checking"
	// pairReaderReader is class 3: no direction — every reader of one
	// question returns the same answer for one input, or declares itself
	// out of scope for that input.
	pairReaderReader pairClass = "reader_reader"
)

// verbRole is the classify-or-declare vocabulary a catalogue verb carries:
// which of the three classes' two roles (previewing/acting/checking) it
// plays, or "reader" for the non-directional class. The zero value is
// deliberately invalid — see catalogueVerb's own DeclaredReason.
type verbRole string

const (
	roleAdvertiserPreview verbRole = "previewing"
	roleActor             verbRole = "acting"
	roleChecker           verbRole = "checking"
	roleReader            verbRole = "reader"
)

// catalogueVerb is one entry in the classify-or-declare roster (US-5): a
// verb this phase's own declared pairs or replay entries name, with either
// a Role or a written Reason for carrying none — never both empty, which
// is exactly what unclassifiedCatalogueVerbs below refuses.
type catalogueVerb struct {
	// Verb is the stable name a declared pair or replay entry cites — a
	// short, human-legible description of the concrete surface (e.g. "a2a
	// contract publish"), not a Go symbol, because several of these verbs
	// are CLI surfaces this phase's own allowlist forbids editing.
	Verb string
	// Role is one of the four verbRole constants, or "" if this verb is
	// declared to belong to none of them.
	Role verbRole
	// DeclaredReason is required when Role == "" — an unclassified verb
	// with no reason reds (AC-11); a verb legitimately outside all three
	// classes still needs the reason on record, never silent omission.
	DeclaredReason string
}

// verbCatalogue is the roster of verbs the declared pairs in
// scenarios_verb_agreement.go (the live cells) and scenarios_incidents.go's
// verbAgreementReplays (the documentary replay cells) actually name. It is
// intentionally the verbs THIS PHASE'S OWN PAIRS reference — the roster
// this epic's eight instances are drawn from — not a transcription of
// every verb the `a2a` binary ships (that roster has no single enumerator
// this package can read without opening cmd/a2a, off this wave's
// allowlist; see this file's own classify-or-declare test for how an
// UNCLASSIFIED entry among these is caught instead).
func verbCatalogue() []catalogueVerb {
	return []catalogueVerb{
		{Verb: "a2a note (advertiser)", Role: roleAdvertiserPreview},
		{Verb: "fold.CheckLegality (space's required V2 lifecycle check)", Role: roleActor},
		{Verb: "a2a contract publish", Role: roleActor},
		{Verb: "a2a contract preflight", Role: roleAdvertiserPreview},
		{Verb: "a2a contract verify-export --local <staging> <id>@<version>", Role: roleChecker},
		{Verb: "a2a data pack", Role: roleActor},
		{Verb: "a2a validate --ci / the space's required check (POL-002)", Role: roleChecker},
		{Verb: "a2a submit", Role: roleActor},
		{Verb: "a2a template show contract (preview of the accepted shape)", Role: roleAdvertiserPreview},
		{Verb: "validator (a2a validate --ci / merge gate)", Role: roleReader},
		{Verb: "mirror decoder (a2a inbox / a2a outbox)", Role: roleReader},
		{Verb: "doctor repository-visibility scan", Role: roleReader},
	}
}

// unclassifiedCatalogueVerbs reports every verb name usedVerbs cites that
// verbCatalogue() does not carry with a non-empty Role AND non-empty
// DeclaredReason are mutually exclusive by construction (an entry MUST
// carry exactly one) — see catalogueWellFormednessErrors for that half.
// This function is the OTHER half of US-5: a verb no catalogue entry names
// at all, classified or declared, reds by name rather than by omission
// nobody notices.
func unclassifiedCatalogueVerbs(usedVerbs []string, catalogue []catalogueVerb) []string {
	known := make(map[string]bool, len(catalogue))
	for _, c := range catalogue {
		known[c.Verb] = true
	}
	var out []string
	for _, v := range usedVerbs {
		if !known[v] {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// catalogueWellFormednessErrors reports every entry that carries BOTH a
// Role and a DeclaredReason, or NEITHER — the classify-or-declare
// obligation's own well-formedness half, exactly the shape
// TestIncidentReplaysAreWellFormed already applies to a different registry
// one file over.
func catalogueWellFormednessErrors(catalogue []catalogueVerb) []error {
	var errs []error
	for _, c := range catalogue {
		hasRole := c.Role != ""
		hasReason := strings.TrimSpace(c.DeclaredReason) != ""
		switch {
		case hasRole && hasReason:
			errs = append(errs, fmt.Errorf("%s: carries both a Role (%s) and a DeclaredReason — exactly one is required", c.Verb, c.Role))
		case !hasRole && !hasReason:
			errs = append(errs, fmt.Errorf("%s: carries neither a Role nor a DeclaredReason — an unclassified verb must red (US-5)", c.Verb))
		}
	}
	return errs
}

// assertDirectionalCell is the previewing→acting / acting→checking
// assertion shape (US-1/US-2): leftAccepted (the preview passed, or the
// actor accepted) together with rightRefused (the actor refused, or the
// checker refused) is the ONLY disagreement this shape reports. The
// converse — leftAccepted is false while rightRefused is true — is
// LEGITIMATE (a conservative preview, or a checker with nothing to check,
// may refuse where the other side did not) and must never be reported as
// an error.
//
// class is carried through to the message rather than inferred from
// leftVerb/rightVerb's own names, because the whole reason this phase
// carries three classes instead of two is that a class must be stated,
// never guessed from which verb ran first.
func assertDirectionalCell(class pairClass, leftVerb string, leftAccepted bool, rightVerb string, rightRefused bool) error {
	if leftAccepted && rightRefused {
		return fmt.Errorf("%s disagreement: %q accepted the input but %q refused the SAME input — one predicate, two verdicts", class, leftVerb, rightVerb)
	}
	return nil
}

// assertReaderAgreement is the reader↔reader assertion shape (US-3): no
// direction at all. Every reader named in verdicts must agree, UNLESS it
// is named in exempt — a reader legitimately out of scope for this
// question's input must be DECLARED there, not silently excluded by the
// caller building a shorter verdicts map (Testing requirements table,
// "roster" row: "one reader legitimately out of scope for a path must be
// declared, not silently unequal").
func assertReaderAgreement(question string, verdicts map[string]bool, exempt map[string]string) error {
	present := make([]string, 0, len(verdicts))
	for reader := range verdicts {
		if _, isExempt := exempt[reader]; isExempt {
			continue
		}
		present = append(present, reader)
	}
	sort.Strings(present)
	if len(present) < 2 {
		return nil
	}
	first := verdicts[present[0]]
	var disagree []string
	for _, r := range present[1:] {
		if verdicts[r] != first {
			disagree = append(disagree, r)
		}
	}
	if len(disagree) == 0 {
		return nil
	}
	pairs := make([]string, 0, len(present))
	for _, r := range present {
		pairs = append(pairs, fmt.Sprintf("%s=%v", r, verdicts[r]))
	}
	return fmt.Errorf("%s: readers disagree — %s", question, strings.Join(pairs, ", "))
}
