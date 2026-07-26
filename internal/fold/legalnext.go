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

func containsMove(moves []NextMove, m NextMove) bool {
	for _, existing := range moves {
		if existing == m {
			return true
		}
	}
	return false
}
