package validate

import (
	"regexp"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

var safeFirstPartySession = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// firstPartyReceiptStatus classifies only event-local provenance. Third-party
// version formats never enter first-party rollout policy; an event claiming to
// be from a2a must carry a non-empty, readable version and fails closed with
// POL-012 otherwise.
func firstPartyReceiptStatus(probe eventProducerProbe) (atFloor bool, invalid *Violation) {
	if probe.ProducedBy.Tool != "a2a" {
		return false, nil
	}
	if probe.ProducedBy.Version == "" {
		return false, invalidFirstPartyProducerVersionViolation()
	}
	older, err := version.OlderThan(probe.ProducedBy.Version, version.ClaimReceiptFloor)
	if err != nil {
		return false, invalidFirstPartyProducerVersionViolation()
	}
	return !older, nil
}

func invalidFirstPartyProducerVersionViolation() *Violation {
	return &Violation{
		Code:     "POL-012",
		Class:    ClassPolicy,
		Path:     "produced_by.version",
		Message:  "first-party event carries an empty or unreadable produced_by.version; update a2a and author the event again",
		CCRef:    "CC-085",
		Severity: SeverityReject,
	}
}

// checkFirstPartyActor enforces the frozen v0.19 first-party provenance
// bounds. Actor attribution remains unused by fold; this is an authoring
// policy only. Omitted optional fields remain valid.
func checkFirstPartyActor(probe eventProducerProbe) []Violation {
	var out []Violation
	if probe.Actor.Model != nil {
		count := utf8.RuneCountInString(*probe.Actor.Model)
		if count < 1 || count > 96 {
			out = append(out, Violation{
				Code:     "POL-016",
				Class:    ClassPolicy,
				Path:     "actor.model",
				Message:  "first-party actor.model must contain between 1 and 96 Unicode scalar values",
				Severity: SeverityReject,
			})
		}
	}

	if probe.Actor.Session == nil {
		return out
	}
	if secretViolations := scanSecretIdentifier(*probe.Actor.Session, "actor.session"); len(secretViolations) > 0 {
		return append(out, secretViolations...)
	}
	if !safeFirstPartySession.MatchString(*probe.Actor.Session) {
		out = append(out, Violation{
			Code:     "POL-016",
			Class:    ClassPolicy,
			Path:     "actor.session",
			Message:  "first-party actor.session must match [A-Za-z0-9._:-]{1,128}",
			Severity: SeverityReject,
		})
	}
	return out
}

// checkEventReceipt consumes fold's canonical evaluation. It never derives
// applicability or an outcome from transition names. Missing applicable
// first-party receipts are lifecycle violations; a wrong present receipt is
// non-blocking consistency evidence and cannot alter evaluation.Result.
func checkEventReceipt(probe eventProducerProbe, firstPartyAtFloor bool, evaluation fold.CandidateEvaluation) ([]Violation, []ConsistencyFlag) {
	if firstPartyAtFloor && evaluation.Applicable && (probe.State == nil || *probe.State == "") {
		return []Violation{{
			Code:     "LFC-003",
			Class:    ClassLifecycle,
			Path:     "state",
			Message:  "applicable first-party event is missing the producer evaluation receipt computed for this candidate",
			Severity: SeverityReject,
		}}, nil
	}

	if !evaluation.Applicable || probe.State == nil || fold.State(*probe.State) == evaluation.Outcome {
		return nil, nil
	}

	return nil, []ConsistencyFlag{{
		Kind:      ConsistencyStateClaimMismatch,
		EventULID: probe.Event,
		Subject:   probe.Subject,
		Scope: ConsistencyScope{
			Kind:    string(evaluation.Scope.Kind),
			Subject: evaluation.Scope.Subject,
			Version: evaluation.Scope.Version,
		},
		Claimed: *probe.State,
		Actual:  string(evaluation.Outcome),
		Actor: ConsistencyActor{
			Kind:    probe.Actor.Kind,
			Name:    probe.Actor.Name,
			System:  probe.Actor.System,
			Model:   optionalString(probe.Actor.Model),
			Session: safeSessionEvidence(probe.Actor.Session),
		},
		Producer: ConsistencyProducer{
			Tool:    probe.ProducedBy.Tool,
			Version: probe.ProducedBy.Version,
		},
		Cause: "unknown",
	}}
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func safeSessionEvidence(session *string) string {
	if session == nil {
		return ""
	}
	return provenance.SafeSessionEvidence(*session)
}
