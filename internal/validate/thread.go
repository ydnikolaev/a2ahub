package validate

import (
	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// ThreadResolver is an OPTIONAL capability a concrete Resolver may also
// implement, obtained via a type assertion (the same pattern
// internal/space/funnel.go uses for host.Merger/host.Forker) rather than
// added to Resolver itself. Resolver (internal/validate/seam.go) is not in
// this brief's allowlist, and its three methods — KnownArtifact, Digest,
// System — genuinely cannot answer either question below: KnownArtifact
// is existence-only (no field read), and none of the three can tell
// whether some OTHER known artifact already carries a given `thread`
// value. Widening seam.go was the alternative; per the brief's explicit
// instruction ("no new Resolver method ... report it rather than widening
// the interface") this file adds a second, narrower interface instead and
// degrades to "no violation" wherever a supplied Resolver does not
// implement it (see checkFork/checkForeignMint below).
type ThreadResolver interface {
	// ThreadOf returns the `thread` value recorded for a known artifact
	// id in the local cache, and whether id resolved at all.
	ThreadOf(id string) (thread string, found bool)
	// ThreadExists reports whether some known artifact in the local cache
	// already carries this exact thread value.
	ThreadExists(thread string) bool
}

// checkParentResolves is REF-003 (computed-not-listed-2026-08 P7),
// WIDENED onto `parent` rather than left to `checkRefs` (referential.go):
// checkRefs only ever loops env.Refs, and never once reads env.Parent — a
// response whose `parent` names nothing that exists anywhere validated
// clean before this fix, because no rule anywhere checked it (spec 07 §0.5
// "the deferring rule", measured with the built binary). registry.yaml's
// REF-003 row states this widened scope in its own `applies_to`.
//
// This lives in thread.go, called from checkFork's own body, rather than
// as a fifth call `engine.go:111-112` would need to grow: that call site
// is the anti-duplication rule this file's own package already follows
// (§5 "one place to reach, not five"), and it already reaches checkFork on
// every V2 submission.
//
// Two non-reject outcomes, both distinct from "resolves cleanly":
//   - env.Parent == "" — nothing named, nothing to resolve.
//   - resolver == nil — genuinely UNMEASURABLE (D9's SeverityUnmeasured,
//     result.go:67-72), not "resolves to nothing": a caller that supplied
//     no resolver at all cannot be told apart from one whose resolver
//     correctly reports absence, and conflating the two would either
//     silently pass an orphan parent (the old bug) or reject a caller for
//     its own missing capability (a false reject). This is the "third
//     state" this phase's own anti-duplication note names: a parent that
//     cannot be resolved BECAUSE THE RESOLVER IS ABSENT, distinct from one
//     that resolves to nothing.
//
// Otherwise resolver.KnownArtifact(env.Parent) decides: known means
// nothing to report here (checkFork's own fork comparison continues
// below); unknown is REF-003, reject.
func checkParentResolves(env envelope, resolver Resolver) []Violation {
	if env.Parent == "" {
		return nil
	}
	if resolver == nil {
		return []Violation{{
			Code:     "REF-003",
			Class:    ClassReferential,
			Path:     "parent",
			Message:  "parent " + env.Parent + " cannot be checked: no resolver supplied",
			Severity: SeverityUnmeasured,
		}}
	}
	if resolver.KnownArtifact(env.Parent) {
		return nil
	}
	return []Violation{{
		Code:     "REF-003",
		Class:    ClassReferential,
		Path:     "parent",
		Message:  "parent " + env.Parent + " does not resolve to a known artifact",
		CCRef:    "CC-070",
		Severity: SeverityReject,
	}}
}

// checkFork is REF-009 (reject): an artifact that carries `parent` and
// whose `thread` differs from the parent's `thread`. Plain equality — the
// anti-fork core of §T2.
//
// Vacuity, all "no violation, not merely no crash":
//   - env.Parent == "" — nothing to fork from.
//   - env.Thread == "" — `thread` is still schema-optional today (the
//     schema tightening is the lead's wave, not this one); an artifact
//     that carries no thread of its own cannot be compared.
//   - the parent's own thread is unknown or itself "" — comparing against
//     an unknown or threadless parent is not "differs", it is
//     "incomparable". This is WIDER than §T2's literal text ("with
//     thread required, there is no empty case to carve out") because
//     that sentence describes the POST-tightening world; today every
//     existing artifact and every existing parent is threadless, and a
//     strict inequality read literally would REF-009-reject every reply
//     in the corpus the moment this lands. Documented as a deviation.
//   - resolver is nil, or does not resolve the parent via KnownArtifact
//     (the same seam REF-003 uses), or does not implement ThreadResolver
//     — an unresolvable or unmeasurable parent is checkParentResolves'
//     own REF-003 rejection to make (see above), not this rule's; "cannot
//     see the parent" is never itself a REF-009 reject. As of this phase,
//     that deferred rejection actually fires — the sentence used to
//     describe a promise the tree did not keep (checkRefs never reached
//     env.Parent); it now does, through this function's own first call.
func checkFork(env envelope, resolver Resolver) []Violation {
	if pv := checkParentResolves(env, resolver); len(pv) != 0 {
		return pv
	}
	if env.Parent == "" || env.Thread == "" {
		return nil
	}
	if resolver == nil || !resolver.KnownArtifact(env.Parent) {
		return nil
	}
	tr, ok := resolver.(ThreadResolver)
	if !ok {
		return nil
	}
	parentThread, found := tr.ThreadOf(env.Parent)
	if !found || parentThread == "" {
		return nil
	}
	if env.Thread == parentThread {
		return nil
	}
	return []Violation{{
		Code:     "REF-009",
		Class:    ClassReferential,
		Path:     "thread",
		Message:  "artifact's thread differs from its parent's thread (fork)",
		Severity: SeverityReject,
	}}
}

// checkForeignMint is REF-010 (reject): `thread` names no artifact
// already in the space, AND introducing it is not a legal mint. A legal
// mint (§T1 "you may only mint under your own name") is: grammar-valid
// per artifact.ParseThreadID AND its parsed <system> token equals this
// artifact's own `from` — so a typo in someone ELSE's thread id is
// rejected as a foreign mint, while genuinely starting a new conversation
// under your own name is fine, and a thread that already exists (however
// it looks) is always fine, because it names something real rather than
// introducing anything.
//
// Vacuity:
//   - env.Thread == "" — still schema-optional today; nothing to check.
//   - resolver is nil or does not implement ThreadResolver — existence is
//     UNKNOWABLE, not "false". Firing on "not a legal mint" alone without
//     being able to confirm non-existence would reject every legitimate
//     cross-system reply whose thread was minted by a third party (e.g.
//     thread system "axon", replying system "seomatrix") purely because
//     this call's Resolver happens not to expose ThreadExists. So an
//     absent capability silences REF-010 entirely rather than firing
//     on incomplete information.
func checkForeignMint(env envelope, resolver Resolver) []Violation {
	if env.Thread == "" {
		return nil
	}
	if resolver == nil {
		return nil
	}
	tr, ok := resolver.(ThreadResolver)
	if !ok {
		return nil
	}
	if tr.ThreadExists(env.Thread) {
		return nil
	}

	parsed, err := artifact.ParseThreadID(env.Thread)
	if err != nil || parsed.System != env.From {
		return []Violation{{
			Code:     "REF-010",
			Class:    ClassReferential,
			Path:     "thread",
			Message:  "thread names no known artifact and is not a legal mint under this artifact's own system",
			Severity: SeverityReject,
		}}
	}
	return nil
}

// checkSupersedeThreadContinuity is REF-012 (warning): a superseding
// artifact sits on a different thread from its predecessor. Legal (a
// contract renegotiated a year later legitimately opens a new
// conversation) but never silent.
//
// This is a pure function over the two thread values, not wired to
// anything in this package: §T2 states the link is the `supersede`
// EVENT's required refs (cmd_lifecycle.go:498's RequireRefs: true), NOT
// the dead `supersedes` envelope field — and this package's event view,
// CandidateEvent (seam.go, off this brief's allowlist), carries no refs
// at all. There is today no caller inside internal/validate that can
// supply successorThread/predecessorThread; wiring one would mean adding
// a field to CandidateEvent (a seam.go change) or inventing an events
// pipeline this package does not have. Reported as a deviation rather
// than done silently or worked around.
//
// Vacuity: either side empty (schema-optional today, or an unresolvable
// predecessor) means nothing to compare — no violation.
func checkSupersedeThreadContinuity(successorThread, predecessorThread string) []Violation {
	if successorThread == "" || predecessorThread == "" {
		return nil
	}
	if successorThread == predecessorThread {
		return nil
	}
	return []Violation{{
		Code:     "REF-012",
		Class:    ClassReferential,
		Path:     "thread",
		Message:  "superseding artifact sits on a different thread from its predecessor",
		Severity: SeverityWarning,
	}}
}
