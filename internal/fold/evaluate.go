package fold

// EvaluationScopeKind identifies the one scalar lifecycle scope a producer
// evaluation receipt describes.
type EvaluationScopeKind string

const (
	// EvaluationScopePrimary identifies the candidate's primary artifact.
	EvaluationScopePrimary EvaluationScopeKind = "primary"
	// EvaluationScopeResponse identifies one attached response.
	EvaluationScopeResponse EvaluationScopeKind = "response"
	// EvaluationScopeContractVersion identifies one explicit contract version.
	EvaluationScopeContractVersion EvaluationScopeKind = "contract-version"
)

// EvaluationScope names the single scalar lifecycle value represented by an
// applicable producer evaluation receipt. Version is populated only for a
// contract-version scope.
type EvaluationScope struct {
	Kind    EvaluationScopeKind
	Subject string
	Version string
}

// CandidateEvaluation is the pure pre-write evaluation of one candidate
// event. Result is the complete post-candidate fold when Verdict is legal and
// a deep clone of the prior result otherwise. Scope and Outcome are populated
// only when Applicable is true.
type CandidateEvaluation struct {
	Verdict    Verdict
	Applicable bool
	Scope      EvaluationScope
	Outcome    State
	Result     Result
}

// EvaluateCandidate is the single writer-facing seam for candidate legality,
// receipt applicability, scalar outcome and the complete post-candidate fold.
// It composes CheckCandidate and Apply rather than duplicating either one's
// lifecycle rules, performs no I/O and never mutates its arguments.
func EvaluateCandidate(kind Kind, prior Result, candidate Event, env Envelope, membership MembershipView) CandidateEvaluation {
	checkPrior := prior
	if candidate.Transition == TVerify || candidate.Transition == TDispute {
		checkPrior = Result{Kind: KindResponse, State: prior.Responses[candidate.Subject]}
	}

	status := MembershipUnknown
	if membership != nil {
		status = membership(candidate.Actor.System)
	}
	verdict := CheckCandidate(
		kind,
		checkPrior,
		candidate.Transition,
		candidate.Version,
		env,
		candidate.Actor,
		status,
	)
	if verdict != VerdictLegal {
		return CandidateEvaluation{Verdict: verdict, Result: prior.clone()}
	}

	result := Apply(kind, env, prior, candidate, membership)
	scope, applicable := candidateEvaluationScope(kind, candidate)
	evaluation := CandidateEvaluation{
		Verdict:    verdict,
		Applicable: applicable,
		Scope:      scope,
		Result:     result,
	}
	if applicable {
		evaluation.Outcome = evaluationOutcome(scope, result)
	}
	return evaluation
}

// candidateEvaluationScope classifies only the shape of a legal candidate's
// effects. It deliberately has no lifecycle transition map: legality and the
// actual result remain owned by CheckCandidate and Apply.
func candidateEvaluationScope(kind Kind, candidate Event) (EvaluationScope, bool) {
	switch {
	case candidate.Transition == TNote:
		return EvaluationScope{}, false
	case kind == KindAnnouncement && candidate.Transition == TAcknowledge:
		return EvaluationScope{}, false
	case candidate.Transition == TRespond || candidate.Transition == TDispute:
		return EvaluationScope{}, false
	case candidate.Transition == TVerify:
		return EvaluationScope{Kind: EvaluationScopeResponse, Subject: candidate.Subject}, true
	case kind == KindContract && isContractVersionTransition(candidate.Transition):
		if candidate.Version != "" {
			return EvaluationScope{
				Kind:    EvaluationScopeContractVersion,
				Subject: candidate.Subject,
				Version: candidate.Version,
			}, true
		}
		if candidate.Transition == TDeprecate || candidate.Transition == TRetire {
			return EvaluationScope{}, false
		}
	}

	return EvaluationScope{Kind: EvaluationScopePrimary, Subject: candidate.Subject}, true
}

func evaluationOutcome(scope EvaluationScope, result Result) State {
	switch scope.Kind {
	case EvaluationScopePrimary:
		return result.State
	case EvaluationScopeResponse:
		return result.Responses[scope.Subject]
	case EvaluationScopeContractVersion:
		return result.Versions[scope.Version]
	default:
		return StateNone
	}
}
