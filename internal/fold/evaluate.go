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

// EvaluateCandidate is EvaluateCandidateWithSuccessor with no resolved
// successor facts — the same shape legality.go's CheckCandidate is to
// CheckCandidateWithSuccessor, kept at its existing signature so every one
// of this package's ~20 already-wired callers (internal/cli, internal/mcp,
// internal/cache, internal/contractwiring, internal/space) keeps compiling
// unchanged.
//
// Passing nil is NOT "skip the precondition" — CheckCandidateWithSuccessor
// (legality.go) treats nil as "unresolved", and D9's own rule (types.go's
// SuccessorPrecondition doc comment) refuses a Precondition-bearing row
// (the two decision-supersede rows, table.go) wherever today's behaviour
// would otherwise be a silent grant. So an EvaluateCandidate caller that
// has not resolved the successor sees those two rows refuse
// UNCONDITIONALLY — see EvaluateCandidateWithSuccessor's own doc comment
// for the caller that resolves it and calls that variant directly.
func EvaluateCandidate(kind Kind, prior Result, candidate Event, env Envelope, membership MembershipView) CandidateEvaluation {
	return EvaluateCandidateWithSuccessor(kind, prior, candidate, env, membership, nil)
}

// EvaluateCandidateWithSuccessor is the single writer-facing seam for
// candidate legality, receipt applicability, scalar outcome and the
// complete post-candidate fold — extended (legality.go's own
// CheckCandidate -> CheckCandidateWithSuccessor precedent, applied here
// identically) to accept the caller-resolved facts about the SUCCESSOR
// artifact a Precondition-bearing row (table.go) may check. It composes
// CheckCandidateWithSuccessor and Apply rather than duplicating either
// one's lifecycle rules, performs no I/O and never mutates its arguments.
//
// successor is nil (unresolved) for every candidate that carries no
// Precondition-bearing transition — the vast majority — where it is never
// consulted at all (preconditionTable, table.go, has no entry for those
// rows). Only internal/cli's lifecycleEvaluateCandidate (cmd_lifecycle.go),
// the CLI's own pre-write UX gate for `a2a supersede`, resolves a real
// *SuccessorFacts today, via the SAME validate.SuccessorResolver capability
// (internal/cli/adapters.go's MirrorResolver.Successor) the SUBMIT
// validation path already uses through resolveSuccessorEnvelope — never a
// second, independently-typed successor reader.
func EvaluateCandidateWithSuccessor(kind Kind, prior Result, candidate Event, env Envelope, membership MembershipView, successor *SuccessorFacts) CandidateEvaluation {
	checkPrior := prior
	if candidate.Transition == TVerify || candidate.Transition == TDispute {
		checkPrior = Result{Kind: KindResponse, State: prior.Responses[candidate.Subject]}
	}

	status := MembershipUnknown
	if membership != nil {
		status = membership(candidate.Actor.System)
	}
	verdict := CheckCandidateWithSuccessor(
		kind,
		checkPrior,
		candidate.Transition,
		candidate.Version,
		env,
		candidate.Actor,
		status,
		successor,
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
