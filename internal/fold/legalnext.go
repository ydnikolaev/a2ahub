package fold

// NextMove is one legal (transition, Role) pair reachable from a given
// (kind, fromState) — LegalNext's return shape. To is carried alongside
// for callers that want it (it may be StateDynamic, the same sentinel
// Row.To ever carries — see table.go's own comment on why the generic
// table lookup never resolves it directly; LegalNext is read-only and
// informational, so exposing the sentinel as-is costs nothing and saves
// a second table).
//
// Role is NOT resolved to a concrete system id here — the caller holds
// the artifact's own Envelope (and, for verify/dispute, the PARENT's),
// and role -> system id is exactly the derivation spec §T3 warns a weak
// agent will botch if it is left to guesswork instead of one shared
// function. See legality.go's roleAuthorizes/legalRole for that
// resolution; LegalNext does not duplicate it.
type NextMove struct {
	Transition string
	To         State
	Role       Role
}

// LegalNext answers "which transitions are legal from here, and who may
// make them" as a pure filter over the existing rows table (table.go) —
// never a second reading of the rules CheckLegality already enforces
// pre-write and Fold/Apply already enforce post-write.
//
// Two things this function's contract depends on, both documented on
// CheckLegality itself (legality.go:20-45) and repeated here because a
// caller reading only this doc comment must not get them wrong:
//
//  1. `verify` is RESPONSE-scoped: its only row is keyed on
//     Kind: KindResponse (responseRows(), table.go), never on the
//     parent's own kind. This function needs no special case for that —
//     the generic filter below already returns it only when the caller
//     queries LegalNext(KindResponse, <the response's own closure
//     sub-state, i.e. Result.Responses[id], NOT the parent's Result.State>).
//     A caller building a thread's open items must resolve the returned
//     RoleOwner against the PARENT's envelope (RoleOwner -> parent's
//     From), exactly as CheckLegality's own doc comment requires —
//     never against a response's own facts, which this package's model
//     gives no meaning to.
//     `dispute` genuinely has TWO rows, not one, and both are real:
//     responseRows() gives the response its own submitted->disputed row
//     (query via KindResponse, same caller contract as verify above),
//     and exchangeRows() SEPARATELY gives the parent exchange a
//     responded->in_progress row documenting D-024's "dispute
//     ADDITIONALLY reopens the parent" side effect (applyResponseScoped,
//     fold.go) — real, caller-visible information ("from `responded`,
//     the owner may dispute, which reopens the exchange"), even though
//     an actual `dispute` EVENT is always dispatched through
//     applyResponseScoped by transition name (fold.go:89-98), never
//     through the parent's own generic per-kind table lookup. LegalNext
//     reports both, unmodified — a pure filter has no dispatch logic to
//     apply here, and dropping the parent-kind row would be exactly the
//     kind of undocumented narrowing this package avoids.
//  2. `acknowledge` on an announcement (D-025) is transition-free and
//     deliberately carries NO row in announcementRows() — CheckLegality
//     answers it via a dedicated branch instead of a table lookup, and
//     that branch's rule (membership alone; addressing is irrelevant;
//     see broadcastAckPermitted, fold.go) has no Role value that
//     expresses it without lying: RoleAny waives the system match but
//     LegalNext takes no membership parameter, so it cannot state "any
//     CURRENT MEMBER" — only a caller holding membership data can.
//     LegalNext therefore does NOT report announcement-acknowledge as a
//     next move; see the package deviations note for the full reasoning.
//     A caller presenting "whose move is it" for an announcement must add
//     that case itself, the same way CheckLegality's dedicated branch
//     does — never by inventing a row here.
//
// Rows that share one (Kind, From, Transition) key but differ only in
// Scenario (unblock's per-pre-block-state documentation rows; decision
// approve's quorum-reached/not-reached rows) collapse to one NextMove —
// Scenario carries no runtime meaning (table.go's own comment on Row).
func LegalNext(kind Kind, from State) []NextMove {
	var out []NextMove
	for _, r := range rows {
		if r.Kind != kind || r.From != from {
			continue
		}
		move := NextMove{Transition: r.Transition, To: r.To, Role: r.Role}
		if containsMove(out, move) {
			continue
		}
		out = append(out, move)
	}
	return out
}

// LegalNextFor answers "which transitions are legal from here" using the
// full prior Result rather than a single State. For a contract on the
// per-version path it answers about THAT version — never the projected
// subject-level State, which cannot express "1.0 deprecated, 2.0
// published" as two different answers for the same contract. Everything
// else — every non-contract kind, and a contract still on the legacy
// version-less path (prior.Versions empty and no version named, the same
// fallback applyContractScoped and CheckCandidate apply) — delegates
// unchanged to LegalNext(kind, prior.State). LegalNext itself is
// untouched by this function.
//
// The contract answer is derived from contractVersionVerdict, NOT from a
// table lookup on prior.Versions[version]. That is not a stylistic
// preference; the table lookup is wrong for the single question this
// function exists to answer during a rolling window. An unrecorded
// version maps to StateNone, and StateNone's only contract row is
// `create` — so "what may I do with 3.0.0?" would answer "create the
// contract" when the true answer is "publish it". Deriving from the same
// predicate applyContractScoped and CheckCandidate read keeps all three
// answers to one rule (contract.go), which is what this package's own
// broadcastAckPermitted comment exists to insist on.
//
// internal/cache/threadview.go composes LegalNext with CheckLegality
// today to answer "whose move is it" for a thread's open items; leaving
// the contract case subject-state-only here would reintroduce the exact
// split brain one layer up that plan decision 7 closes inside this
// package. That caller migrates to LegalNextFor in wave 4.
func LegalNextFor(kind Kind, prior Result, version string) []NextMove {
	if kind == KindContract && (version != "" || len(prior.Versions) > 0) {
		var out []NextMove
		for _, t := range []string{TPublish, TDeprecate, TRetire} {
			if !contractMoveAvailable(prior.Versions, t, version) {
				continue
			}
			out = append(out, NextMove{Transition: t, To: contractVersionOutcome(t), Role: contractVersionRole})
		}
		return out
	}
	return LegalNext(kind, prior.State)
}

// contractMoveAvailable is LegalNextFor's own predicate, and it differs
// from contractVersionVerdict in EXACTLY ONE case, deliberately: the
// whole-contract form of `publish`.
//
// The two functions answer different questions. contractVersionVerdict
// answers "may this EVENT be committed", and a publish event carrying no
// version has no meaning in the per-version model, so it refuses.
// LegalNextFor answers "what may this participant DO next", and
// publishing the next version is always something a contract's owner may
// do — they name the version as part of doing it (`a2a contract publish`
// resolves one from --version or --bump before it authors anything).
//
// Collapsing the two costs a real surface. `a2a thread`'s open items are
// one entry per ARTIFACT, so internal/cache asks the whole-contract form;
// deriving that answer from the event predicate dropped `publish` from
// the next actions of every contract that had ever published a version —
// which is to say, from the single most common thing an owner does, on
// every contract in real use. Found by reading what the caller migration
// actually rendered, not by a failing test, which is why it is written
// down here rather than left as a shape someone re-derives.
//
// TestLegalNextForAgreesWithCheckCandidate pins both halves: agreement
// for every NAMED version, and this one deliberate difference.
func contractMoveAvailable(versions map[string]State, transition, version string) bool {
	if transition == TPublish && version == "" {
		return true
	}
	return contractVersionVerdict(versions, transition, version) == VerdictLegal
}

// RoleAuthorizes reports whether actorSystem satisfies role against env's
// own facts — the exported form of the resolution CheckCandidate, Apply
// and CheckLegality all perform internally. It answers ONLY the role
// question: membership validity is the caller's own (it is a fact about
// the manifest as of a commit, which this package never reads), and so is
// whether the transition is legal from the current state at all.
//
// It exists for the caller that has already established legality by
// asking LegalNextFor, and now needs "which systems does this move belong
// to". internal/cache/threadview.go's legalSystems is that caller, and
// its doc comment used to explain that it called CheckLegality once per
// participant PRECISELY BECAUSE this resolution was unexported — a
// workaround, described as one, that then broke the moment a move existed
// which is offered as an affordance but refused as an event (the
// whole-contract `publish`; see contractMoveAvailable). Exporting the
// resolution the workaround was approximating is the fix.
//
// This adds no new rule: NextMove.Role comes from the same table
// CheckCandidate reads, and roleAuthorizes below is the same function
// both paths already used.
func RoleAuthorizes(role Role, env Envelope, actorSystem string) bool {
	return roleAuthorizes(role, env, actorSystem)
}

func containsMove(moves []NextMove, m NextMove) bool {
	for _, existing := range moves {
		if existing == m {
			return true
		}
	}
	return false
}
