// P5 "declared nature" (spec docs/features/archive/agent-exchange-2026-08/
// specs/05-declared-nature.md, AC5/US-3): "As an agent, I want the system
// to tell me a counterparty cannot receive what I am about to send." §7
// keys the field as manifest CONFIG: "what this system can receive: file
// delivery only, HTTP push, both — so a counterparty reads instead of
// guessing." AC5 itself: `capabilities` is a per-participant key on
// manifest/v1 (`participants[].capabilities`, schemas/manifest/v1/
// space.schema.json), checkable by a POLICY check here — NOT a new
// manifest generation. `additionalProperties: true` on the participant
// item already made the key legal to write; this file adds the typing
// (internal/space.Capabilities) and the check.
//
// # requiredDelivery is a caller-resolved fact, not a decode
//
// This function does not read an envelope, resolve a `to` address, or
// import internal/space (ADR-001 — validate does not import the I/O-facing
// space package; see manifest.go's manifestProbe for the same reasoning
// applied to the authority map). No shipped surface today records whether
// a submission is a file-delivered or an HTTP-push-delivered payload, and
// engine.go/envelope.go (the wiring point and the shared envelope decode)
// are outside this wave's allowlist — a concurrent wave already owns
// engine.go this cycle. So the required delivery mode arrives as an
// explicit parameter, the same shape this spec's own 2026-08-10 amendment
// already decided for `CheckContractPublishable`'s binding fact,
// `blocked_by` reaching pendency, and the hub-freshness verdict resolved
// at the CLI and passed into `Triage`: resolve once at the caller, hand the
// fact down, keep this package from growing a second opinion about where
// the fact lives.
//
// # Three states, not two — the P-1 discipline applied to a manifest field
//
// A recipient's declared `capabilities.delivery` set is either present
// (a real, checkable claim) or absent (UNDECLARED, per P-1: "a field
// nobody is obliged to fill closes nothing" — silence must not read as
// "can receive anything" any more than it reads as "can receive
// nothing"). Two different codes carry the two different consequences,
// following manifest_policy.go's own contrast: REF-013 groups branches
// into one code because "every branch has the same consequence"; these
// branches deliberately do not, so they are not one code with two
// severities.
//
//   - declared, includes requiredDelivery: no violation.
//   - declared, excludes requiredDelivery: POL-019, SeverityReject — the
//     recipient said so in machine-readable form; this is certainty, not
//     a guess, and §6's testing row ("sending a push-only payload to a
//     file-only system is refused") asks for a refusal here.
//   - undeclared (nil/empty declaredDelivery): POL-020, SeverityWarning —
//     the same "warn on unknown, never on certainty" shape P4's POL-017
//     heuristic already ships (its own registry row: "a heuristic
//     WARNING, never a refusal — only checkable facts refuse").
//
// # Deviation: POL-019/POL-020 are not yet in schemas/errors/v1/registry.yaml
//
// schemas/** is lead-reserved for this wave's allowlist (two waves running
// concurrently), so this package cannot append the rows itself.
// schemas/errors/v1/history.tsv carries `reserved` rows for both codes (the
// same convention REF-015 and POL-018 already use while their registry
// rows are pending) so scripts/check-operational-confidence.sh's
// check_history does not fail on a live/absent mismatch — but that same
// script's "live Go source emits unregistered <code>" check WILL fail
// until the registry rows land, exactly as declaration_block.go's own doc
// comment records for POL-018 before its row was added.
//
// # Deviation: not wired into ValidateForSubmit this wave
//
// AC5's own "How to verify" column is fully satisfiable inside this
// allowlist: "readable... checkable by a POLICY check in internal/
// validate, NOT by a new manifest generation." §6's testing row asks for
// the check to fire "before submit" — that half needs a caller at V2
// (`ValidateForSubmit`) that resolves requiredDelivery from the envelope
// being submitted and the recipient participant from `env.To`, and that
// caller lives in engine.go/envelope.go, both outside this wave's
// allowlist. Wiring is deferred to whichever wave owns those files next;
// this is the same "a rule with no verb that reaches it" shape this epic
// has already named three times (B8, B23, and P5's own operational-debt
// clearing-write gap) — recorded here rather than silently shipped as
// done.
package validate

import "fmt"

// Delivery modes manifest/v1 `participants[].capabilities.delivery` may
// carry — schemas/manifest/v1/space.schema.json's own enum. §7's prose
// ("file delivery only, HTTP push, both") is membership in this set, not a
// third "both" literal.
const (
	CapabilityDeliveryFile     = "file"
	CapabilityDeliveryHTTPPush = "http_push"
)

// CheckCapabilityMismatch is AC5/US-3's policy check: does a recipient's
// declared manifest capabilities permit the delivery mode a submission
// requires?
//
// requiredDelivery is the caller-resolved fact this package does not
// derive (see the file doc comment) — typically one of
// CapabilityDeliveryFile/CapabilityDeliveryHTTPPush, though this function
// does not itself enforce that enum (the schema does, on write; an unknown
// string here simply cannot match any declared capability and behaves as
// any other mismatch would).
//
// declaredDelivery is the recipient participant's manifest
// capabilities.delivery value(s); pass nil (not an empty non-nil slice —
// though both behave identically here) when the participant's manifest
// entry carries no `capabilities` key at all, i.e. internal/space.
// Participant.Capabilities == nil.
//
// An empty requiredDelivery means the caller has nothing to check (no
// delivery-mode fact was resolved) and CheckCapabilityMismatch returns no
// violation — silence about the REQUIREMENT is a caller's decision to make
// no claim, which is a different thing from silence about the RECIPIENT's
// capacity (the undeclared branch below), and only the latter is this
// check's subject.
func CheckCapabilityMismatch(requiredDelivery string, declaredDelivery []string) []Violation {
	if requiredDelivery == "" {
		return nil
	}
	if len(declaredDelivery) == 0 {
		return []Violation{{
			Code:     "POL-020",
			Class:    ClassPolicy,
			Path:     "participants[].capabilities.delivery",
			Message:  fmt.Sprintf("recipient's capabilities are undeclared; cannot confirm it can receive a %q delivery", requiredDelivery),
			Severity: SeverityWarning,
		}}
	}
	for _, declared := range declaredDelivery {
		if declared == requiredDelivery {
			return nil
		}
	}
	return []Violation{{
		Code:     "POL-019",
		Class:    ClassPolicy,
		Path:     "participants[].capabilities.delivery",
		Message:  fmt.Sprintf("recipient declares capabilities.delivery=%v, which does not include the required %q delivery", declaredDelivery, requiredDelivery),
		Severity: SeverityReject,
	}}
}
