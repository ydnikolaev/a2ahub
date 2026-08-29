package lane

import "fmt"

// claimVerdict is P1's single predicate: does d CLAIM path (via Inputs for
// KindScoped, Claims for KindAlways — declarationPatterns), and if so, is
// that specific glob BACKED — found by the reads extractor, or exempt
// because the honesty question has no subject (PhaseBacking's own doc
// comment)? Coverage and Derive both call this and ONLY this to decide
// whether a claim counts as judged, so the three-valued verdict cannot
// disagree between `--verify` and `--derive` — derive.go's own comments
// name that disagreement as a contradiction the operator has no way to
// resolve. A missing backing[d.Phase] entry (the zero PhaseBacking) is
// honestly "no evidence", i.e. unbacked — precision over recall would
// rather a real claim be reported unbacked than silently assumed backed
// because nobody computed its evidence.
//
// PhaseBacking.Opaque IS consulted here, and the reason is worth the
// paragraph because the first implementation of this predicate did the
// opposite and it was wrong in a measurable way.
//
// A lane-reads-opaque directive says: this phase reads something the
// classifier cannot resolve, and here is why. That is EVIDENCE OF A READ,
// stated by the only party that can state it. Refusing to let it back the
// phase's globs treats an honest declaration as no declaration at all.
//
// The argument for excluding it was that one directive could launder every
// path under a wide glob into "covered" — `projection`'s `**` being the
// proof case. That risk is real and it is the reason US-5's ceiling exists:
// an UNBOUNDED escape hatch is a hole, a COUNTED one is a debt with a size,
// which is how check-convention.md has always treated this number. The
// ceiling bounds the hatch; the predicate does not have to.
//
// Excluding it was measured and it failed: 176 declared globs across six
// phases (feedback-corpus, frozen-allowlist, feature-lint, epic-drift,
// card-content, operational-confidence-guard) reported unbacked, and every
// one hand-checked was a phase that ALREADY carried an honest directive
// naming the exact variable-built read the extractor cannot resolve. The
// refusal below even tells them to add the directive they already have.
// A gate whose advice is already taken is not measuring anything.
//
// What Opaque does NOT do is make a phase exempt from the unbacked verdict
// for globs it never claimed — declarationPatterns still decides claiming,
// and a phase with no directive and no resolved read still reaches the
// CLAIMED-BUT-UNBACKED refusal below.
func claimVerdict(d Declaration, path string, backing map[string]PhaseBacking) (claims, backed bool, pattern string) {
	ok, pat := MatchingInput(declarationPatterns(d), path)
	if !ok {
		return false, false, ""
	}
	pb := backing[d.Phase]
	if pb.NoSubject || pb.Opaque || pb.BackedPatterns[pat] {
		return true, true, pat
	}
	return true, false, pat
}

// unbackedRefusal is the CLAIMED-BUT-UNBACKED verdict's own Refusal shape
// (spec §T5/AC-7): name the offending phase, the glob that decided it, and
// BOTH legal fixes — narrow the glob, or declare the read opaque.
func unbackedRefusal(path string, d Declaration, pattern string) Refusal {
	return Refusal{
		Subject: path,
		Problem: fmt.Sprintf("phase %q claims %s via %s, but no read backs it and it is not declared lane-reads-opaque", d.Phase, path, pattern),
		Fix:     fmt.Sprintf("narrow %s's glob so it no longer claims %s, or add a `# lane-reads-opaque: <reason>` line (`//` in a .go file) to the phase's own declaration window", d.Phase, path),
	}
}

// Coverage checks that every path in universe is claimed AND BACKED by at
// least one KindScoped declaration's Inputs, a KindAlways declaration's
// Claims (D-10 — a gate that is universal for one reason and scoped for
// another, e.g. epic-drift/docs/status.md), or is explicitly listed in
// ungated (D-3). A KindAlways declaration with no Claims and every
// KindNever declaration still claim nothing — a universal gate running on
// every path does not mean every path has been JUDGED by something that
// reads it, which is the property Coverage checks.
//
// P1 makes "claimed" three-valued rather than boolean: a glob no extracted
// read covers, and that its phase does not declare lane-reads-opaque for,
// does NOT count as coverage — it reaches the CLAIMED-BUT-UNBACKED refusal
// below rather than being silently treated as judged. backing is
// HonestyCheck's own output (lanecheck.go reorders the two calls so this
// data exists before Coverage runs); passing nil is legal and means "no
// evidence for anything", so every claim reports unbacked rather than
// crashing — a caller that has not wired backing yet gets a loud, correct
// answer, not a silent regression to the old boolean behaviour.
//
// ungated is a list of GLOBS (scripts/lib/lane-ungated.txt's own documented
// grammar — Match's, the same one Inputs/Claims use), matched with Match,
// never exact string membership: D-6's adjudication is written root-shaped
// ("integrations/macos-notifier/**", 40 files, one line) and an exact-match
// set would silently match none of them.
func Coverage(decls []Declaration, universe, ungated []string, backing map[string]PhaseBacking) []Refusal {
	var claimers []Declaration // KindScoped (via Inputs) + KindAlways (via Claims, D-10)
	for _, d := range decls {
		switch {
		case d.Kind == KindScoped:
			claimers = append(claimers, d)
		case d.Kind == KindAlways && len(d.Claims) > 0:
			claimers = append(claimers, d)
		}
	}

	var refusals []Refusal
	for _, path := range universe {
		if MatchesAnyGlob(ungated, path) {
			continue
		}
		backedFound := false
		var unbackedBy []Declaration
		var unbackedPattern []string
		for _, d := range claimers {
			claims, backed, pat := claimVerdict(d, path, backing)
			if !claims {
				continue
			}
			if backed {
				backedFound = true
				break
			}
			unbackedBy = append(unbackedBy, d)
			unbackedPattern = append(unbackedPattern, pat)
		}
		if backedFound {
			continue
		}
		if len(unbackedBy) > 0 {
			for i, d := range unbackedBy {
				refusals = append(refusals, unbackedRefusal(path, d, unbackedPattern[i]))
			}
			continue
		}
		refusals = append(refusals, Refusal{
			Subject: path,
			Problem: fmt.Sprintf("no gate claims %s", path),
			Fix:     "add it to a gate's inputs or declare it explicitly ungated",
		})
	}
	return refusals
}
