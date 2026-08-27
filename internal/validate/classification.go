// no-silent-yes-2026-08/P3 stage 2 (spec 03 §8 AC 4/5/13, DECISIONS.md
// § D3/§ D9, plan §10.4 / plan/10-security.md:60): "classification:
// restricted" is a label with nothing behind it today — the plan already
// says "the validator enforces 'restricted ⇒ bilateral space'" and it does
// not. This file closes that plan-vs-code mismatch: a `restricted`
// artifact whose space carries an ACTIVE participant outside {from} ∪ to
// is refused (POL-024), and a submission this package cannot even ASK
// (no capability to enumerate the space's own active participants) is
// refused too — with the SAME POL-024, D9's own words: "an ordinary
// reject", the RULE's own code, never a second reject code minted just for
// the capability-miss branch — carrying D9's UNMEASURED explanation
// (POL-026) alongside it, D9's first consumer.
//
// # Wave 1 minted POL-025; this wave folds it back into POL-024
//
// Wave 1 of this epic gave the capability-miss branch its own reject code,
// POL-025, reasoning that "the SAME finding at two severities" needed two
// codes because the registry's own severity field is per-code (see
// classificationCapabilityMissViolations' own doc comment below for that
// argument, still true). But D9's own text does not ask for a second
// REJECT code — it asks for UNMEASURED to ride "alongside an ordinary
// reject", and the rule's own ordinary reject for "restricted cannot be
// confirmed bilateral" is POL-024, the same code a genuine audience-exceeds
// finding raises. Once both concrete Resolvers (internal/cli's and
// internal/mcp's MirrorResolver) implemented ActiveParticipantLister for
// real, POL-025 became a code a correctly-wired binary can never provoke —
// unreachable end-to-end, and a conformance path asserting it would assert
// something that cannot happen. POL-025 is deleted; the capability-miss
// branch now emits POL-024 (reused, not reinvented) plus POL-026.
//
// # Reaching ActiveParticipants: a RECEIVE, not a second derivation
//
// The spec's own SECOND-WRITER CORRECTION (§11) is explicit:
// pendency.Input.ActiveParticipants (internal/pendency/pendency.go:41,
// "ACTIVE manifest participant systems (broadcast expansion)") already
// computes this fact, and a consilium answer that proposed recomputing it
// (a new `ParticipantLister` interface) was corrected for exactly that.
// The derivation itself — walk manifest.Participants, keep Status ==
// "active" — lives in internal/cache/threadview.go's own activeParticipants
// (off this stage's footprint, and internal/validate must not import
// internal/cache: "Pure core", seam.go's own doc comment), so this package
// still cannot COMPUTE the set; it can only be TOLD it by a caller that
// can do I/O. What was open was how internal/validate REACHES that told
// fact at submit time.
//
// seam.go's own doc comment already answers the shape question for a
// SIMILAR situation and explicitly forecloses the other one: "This
// interface [Resolver] is deliberately NOT widened for optional,
// rule-specific facts a Resolver may also be able to answer —
// ParentCriteriaCounter (incompleteness.go) is exactly such a
// consumer-side optional upgrade, type-asserted against a Resolver rather
// than added here." ActiveParticipantLister below is that same shape,
// not a new one: a consumer-side optional interface, type-asserted from
// ctx.Resolver, exactly like ParentCriteriaCounter. Resolver.System
// (seam.go:86-98) is confirmed to be a per-system PROBE with no
// enumeration ("System reports whether system is a known member... and,
// if known, whether left") — it cannot answer "list every active system",
// so widening it with a new REQUIRED method would break every existing
// Resolver implementation for a fact only one caller needs; an optional
// upgrade breaks none.
//
// # Where this diverges from ParentCriteriaCounter, deliberately
//
// ParentCriteriaCounter's own doc comment: "A concrete Resolver that does
// not implement this interface simply never triggers
// checkUnmetIndexRange — degrade, never panic." That is silent
// degradation to NO violation. D9 explicitly forecloses the identical
// shape for THIS rule: an unresolvable participant list must never look
// like "checked and clean" — checkClassificationBilateral degrades to
// POL-024 + POL-026 instead, never to nothing. Two optional interfaces
// with the same shape (an optional Resolver capability, type-asserted) and
// two different, DELIBERATE degrade behaviours — see this rule's own doc
// comment below for why.
package validate

// ActiveParticipantLister is validate's own consumer-side optional
// upgrade to Resolver — the SAME pattern seam.go's own doc comment
// establishes for ParentCriteriaCounter (incompleteness.go), applied to a
// different fact: the space's own ACTIVE manifest participant systems
// (the same fact pendency.Input.ActiveParticipants already carries as a
// caller-resolved FACT, internal/pendency/pendency.go:41). This interface
// adds NO new derivation — it is only a way for internal/validate to
// REACH a fact a concrete Resolver may already know how to answer; the
// derivation itself (manifest.Participants, Status == "active") stays
// wherever the caller already computes it.
//
// A concrete Resolver that does not implement this interface cannot be
// asked. Unlike ParentCriteriaCounter, that is NOT a silent "rule never
// fires" here — see checkClassificationBilateral's own doc comment: D9
// requires a capability miss on a `restricted` artifact to refuse loudly
// (POL-024 + POL-026), never to pass silently.
type ActiveParticipantLister interface {
	// ActiveParticipants reports the space's own ACTIVE manifest
	// participant systems, and whether the list could be resolved at
	// all. ok=false means "cannot enumerate" — a capability miss, D9's
	// own first consumer (spec 03 §8 AC 13).
	ActiveParticipants() (systems []string, ok bool)
}

// checkClassificationBilateral is §10.4's "restricted ⇒ bilateral space"
// rule (plan/10-security.md:60), enforced from the manifest's own
// participants[] rather than a minted maximum-classification key (D3: no
// new key at all — enforce §10.4's already-normative rule directly; see
// DECISIONS.md § D3 for the withdrawn key's own name, kept out of THIS
// file so a repo-wide grep for it stays clean, per this stage's own
// acceptance check).
//
// Applies only to `classification: restricted` — every other value
// (public, internal, the schema's own default) is untouched; this rule
// never probes the resolver at all for those, so a space whose concrete
// Resolver has no ActiveParticipantLister capability sees NO new noise
// on an ordinary (non-restricted) submission.
//
// A `to: "all"` (broadcast) recipient set is read as "the space's own
// full active membership" — the recipient set can then never legally be
// EXCEEDED by that same membership, so a broadcast restricted artifact is
// not flagged by this rule specifically. Recorded as a deliberate reading
// of §10.4's literal wording ("MUST NOT be placed in a space whose
// membership exceeds the recipient set") rather than an oversight — see
// this stage's own Deviations report for the product-finding this raises
// (a broadcast is, in substance, the least-restricted possible audience,
// so relying on this rule ALONE to police a broadcast `restricted`
// artifact would be a false sense of safety; nothing in spec 03 asks for
// a second rule here, so none is added speculatively).
func checkClassificationBilateral(env envelope, resolver Resolver) []Violation {
	if env.Classification != "restricted" {
		return nil
	}

	lister, capable := resolver.(ActiveParticipantLister)
	var active []string
	var resolved bool
	if capable {
		active, resolved = lister.ActiveParticipants()
	}
	if !resolved {
		// D9's own first consumer (AC 13): a rule that cannot be
		// evaluated at all must refuse AND explain, never silently
		// grant. Two violations, always together — see
		// classificationCapabilityMissViolations' own doc comment for
		// why this reuses POL-024 rather than minting a second reject
		// code.
		return classificationCapabilityMissViolations()
	}

	recipients, isAll := toSystems(env.To)
	if isAll {
		return nil
	}

	allowed := make(map[string]bool, len(recipients)+1)
	allowed[env.From] = true
	for _, s := range recipients {
		allowed[s] = true
	}

	for _, s := range active {
		if !allowed[s] {
			return []Violation{classificationBilateralViolation()}
		}
	}
	return nil
}

// classificationBilateralViolation is POL-024: a `restricted` artifact
// whose space carries at least one ACTIVE participant outside {from} ∪
// to. One violation regardless of how many active participants sit
// outside the recipient set — the repair (narrow `to`, or reclassify) is
// the same either way, and REF-013/REF-022's own grouping precedent
// (manifest.go) already established "every branch has the same
// consequence, one stable code" for exactly this shape.
//
// This is also the SAME violation classificationCapabilityMissViolations
// below reuses for a capability miss (D9: "an ordinary reject", not a
// second code) — the message states the RULE's own requirement, which
// holds whether the audience was measured and found to exceed it, or
// could not be measured at all; a capability miss is always paired with
// POL-026 (severity: unmeasured), which is what explains WHY the audience
// could not be confirmed.
func classificationBilateralViolation() Violation {
	return Violation{
		Code:     "POL-024",
		Class:    ClassPolicy,
		Path:     "classification",
		Message:  "classification: restricted requires the space's ACTIVE participants not exceed {from} ∪ to (plan/10-security.md §10.4, \"restricted ⇒ bilateral space\")",
		Severity: SeverityReject,
	}
}

// classificationCapabilityMissViolations is D9's first consumer: TWO
// violations, always emitted together — POL-024 (D9's own "an ordinary
// reject": the RULE's own code, reused rather than a second reject code
// minted just for this branch — wave 1 tried that shape as POL-025 and it
// was folded back here once both concrete Resolvers implemented
// ActiveParticipantLister for real, making POL-025 a code a correctly-
// wired binary could never provoke) and POL-026 (severity: unmeasured,
// D9's own vocabulary for "this could not be checked" separate from "this
// failed a check") — never one code at two severities, because the
// registry's own severity field is per-CODE (obligation 0: the registry's
// declared severity for a code must agree with the Go literal nearest that
// code), so "the same finding at two severities" has no single-code
// expression in this corpus.
func classificationCapabilityMissViolations() []Violation {
	return []Violation{
		classificationBilateralViolation(),
		{
			Code:     "POL-026",
			Class:    ClassPolicy,
			Path:     "classification",
			Message:  "classification: restricted requires the space's ACTIVE participant list, which this Resolver cannot enumerate (no ActiveParticipantLister capability) — refusing rather than silently granting",
			Severity: SeverityUnmeasured,
		},
	}
}
