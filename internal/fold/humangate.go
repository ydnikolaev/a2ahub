package fold

// HumanGate names the §3.7 gate a transition sits behind, or "" when the
// transition is one agents make on their own.
//
// It exists because this fact was already written down twice — as a
// `GateMarker bool` in internal/cli's lifecycleVerbTable and again in
// internal/mcp's — and a third consumer arrived (internal/pendency, which
// must tell an agent that the move it is being handed cannot be self-served).
// Two hand-maintained copies of one rule is the point at which a registry
// belongs in the layer both surfaces already import, rather than a third
// copy in the newest one.
//
// Scope is deliberately narrow: the ONLY gate expressible as a property of a
// transition is G3. The others are properties of the ACT or the ARTIFACT, not
// of the verb, and inventing rows for them here would be this file claiming
// knowledge it does not have:
//
//   - G1 (first publish of a contract) and G2 (a breaking version) depend on
//     the contract's own history and the semver delta, not on `publish`.
//   - G4 (onboarding/offboarding) is a space-manifest PR, not an artifact
//     transition at all.
//   - G5 (crossing a classification limit) depends on the payload.
//
// So HumanGate answers "is this VERB always human-gated". A caller needing
// "is THIS act gated" must still consult the rule that owns the other
// condition — the same split internal/validate already draws for retire.
//
// Source: 03-domain.md §3.7 ("G3: `approve`/`reject` on a decision | event
// merged from a PR authored/approved by the human owner's own GitHub
// account"), 10-security.md §10.3 (the same rule, with the PR-identity
// mechanism), and CC-021 ("event by unauthorized actor class (agent approves
// a decision) → fold ignores + flags").
func HumanGate(transition string) string {
	return humanGates[transition]
}

// humanGates is the registry HumanGate reads. One row per always-gated
// transition; absence means "agents do this without a human", which is
// 03-domain.md §3.7's own default ("Everything else — drafting, submitting,
// acknowledging, accepting, responding, verifying, closing, broadcasting —
// agents do without humans").
var humanGates = map[string]string{
	TApprove: "G3",
	TReject:  "G3",
}
