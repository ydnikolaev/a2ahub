package validate

// checkAuthz is the V2 authz class (§10.3, §5.5): `from` must equal the
// caller's own configured system, with a decision-type exception (§5.2:
// `from` = drafting system, authz routed via the decision flow — G3's
// PR-identity check, out of this pure engine's reach; V2 only checks
// transition-table legality + actor-class shape for decisions, per §7).
// Reported under Class "referential" — see the Class type's doc comment
// for why there is no separate "authz" enum value.
func checkAuthz(env envelope, ownSystem string) []Violation {
	if env.Type == "decision" {
		// decision-type exception (§5.2): authz for decisions is routed
		// via the decision flow (G3), never the generic from==section
		// check.
		return nil
	}
	if ownSystem == "" {
		// No configured own system to compare against — the caller
		// (V1, or a V2 caller that didn't populate LocalContext.
		// OwnSystem) hasn't asked for this check; skipping is correct,
		// not silently passing a real check.
		return nil
	}
	if env.From != ownSystem {
		return []Violation{{
			Code:     "REF-005",
			Class:    ClassReferential,
			Path:     "from",
			Message:  "`from` does not match this system's configured own section",
			CCRef:    "CC-002",
			Severity: SeverityReject,
		}}
	}
	return nil
}

// checkAddressees is CC-008 (`to` includes an unknown system, or a system
// marked `left`) — checked against the local manifest cache via
// Resolver.System.
func checkAddressees(env envelope, resolver Resolver) []Violation {
	if resolver == nil {
		return nil
	}
	systems, isAll := toSystems(env.To)
	if isAll {
		return nil
	}
	var out []Violation
	for _, sys := range systems {
		member, left := resolver.System(sys)
		if !member {
			out = append(out, Violation{
				Code:     "REF-006",
				Class:    ClassReferential,
				Path:     "to",
				Message:  "`to` includes an unknown system: " + sys,
				CCRef:    "CC-008",
				Severity: SeverityReject,
			})
			continue
		}
		if left {
			out = append(out, Violation{
				Code:     "REF-006",
				Class:    ClassReferential,
				Path:     "to",
				Message:  "`to` includes a system marked `left`: " + sys,
				CCRef:    "CC-008",
				Severity: SeverityReject,
			})
		}
	}
	return out
}
