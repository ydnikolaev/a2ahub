package validate

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// Draft is validate's own minimal view of an artifact to check:
// deliberately just repo-relative path + raw bytes. Every other package
// in this repo that needs to parse frontmatter/IDs/digests goes through
// internal/artifact — this package is no exception (rails: "never
// re-parse frontmatter or re-hash bytes locally").
type Draft struct {
	// Path is the artifact's repo-relative path (e.g.
	// "axon/exchanges/XQ-axon-20260721-k3f9.md") — its first segment is
	// the owning section (internal/artifact's "section" guard).
	Path string
	// Raw is the artifact's raw bytes, exactly as read from disk/staging
	// — never re-encoded.
	Raw []byte
}

// Engine is the compiled-schema-corpus-backed validation engine. Build
// one with New; ValidateDraft/ValidateForSubmit are then safe for
// concurrent use (the underlying *schema.Corpus is read-only after
// Load).
type Engine struct {
	corpus *schema.Corpus
}

// New builds an Engine from an already-loaded schema corpus (schema.Load
// is itself pure/side-effect-free — callers may share one Corpus across
// many Engines, or build one per Engine; both are fine).
func New(corpus *schema.Corpus) *Engine {
	return &Engine{corpus: corpus}
}

// ValidateDraft is the V1 (authoring) invocation point: schema class only
// on the single drafted artifact (§5.5's literal V1 scope), plus the two
// admission guards CC-006/CC-007 need before any schema validation can
// even run. CC-003 (ID/filename/prefix mismatch, referential class by
// substance) is deferred to ValidateForSubmit (V2) in this
// implementation, even though §6's CC-by-CC table lists it under a
// "V1 schema class" test row — see this phase's Deviations report: the
// spec's own Open Questions section flags a literal tension here (AC-
// 201.1's broad wording vs §5.5's schema-only V1 scope) and directs
// implementors to resolve it operationally without silently narrowing;
// running CC-003 at V1 breaks the golden fixture corpus's "exactly the
// sidecar's code" invariant whenever a malformed id ALSO fails the base
// schema's id pattern (both are true simultaneously for the one fixture
// that exercises this), so this implementation keeps V1 strictly
// schema-class-only and flags the resulting CC-003-at-V1 gap explicitly
// rather than picking a silent side.
func (e *Engine) ValidateDraft(d Draft) (Result, error) {
	const op = "ValidateDraft"
	violations, artifactID, err := e.runCommon(d)
	if err != nil {
		return Result{}, &Error{Op: op, Err: err}
	}
	return newResult(V1, artifactID, violations), nil
}

// ValidateForSubmit is the V2 (pre-write) invocation point: everything
// ValidateDraft does, plus referential ref-resolution, authz, lifecycle
// legality of the accompanying events, and the policy secret scan.
func (e *Engine) ValidateForSubmit(d Draft, events []CandidateEvent, ctx LocalContext) (Result, error) {
	const op = "ValidateForSubmit"
	// V2 authz (CC-002) compares `from` against the caller's own system;
	// an empty OwnSystem would silently skip that check for EVERY
	// submission (fail-open). A V2 call without OwnSystem is a caller
	// misconfiguration, not a valid document — fail closed and loud,
	// mirroring internal/fold's nil-membership fail-closed default.
	if ctx.OwnSystem == "" {
		return Result{}, &Error{Op: op, Err: ErrNoOwnSystem}
	}
	violations, artifactID, env, instance, ok, err := e.runCommonEnvelope(d)
	if err != nil {
		return Result{}, &Error{Op: op, Err: err}
	}
	if !ok {
		// Admission or frontmatter failure already short-circuited
		// further processing (runCommonEnvelope's ok=false path).
		return newResult(V2, artifactID, violations), nil
	}

	// POL-010, V2-only (see placeholder.go's doc comment for why this
	// never runs at V1/ValidateDraft — AC-401.1).
	violations = append(violations, checkUnfilledPlaceholders(instance)...)

	// POL-022, V2-only for the SAME reason POL-010 is, and the reason is not
	// a style choice: `a2a new handoff` renders the five §16.2 headings as
	// empty placeholders, so refusing an empty section at V1 would refuse the
	// tool's own fresh draft. The refusal belongs at the boundary where a
	// draft becomes a shared record — which is also exactly where ADR-011 D3
	// puts it (reject at v3-pr, silent at v3-full-repo).
	//
	// This is the THIRD body reader in this package and the first POSITIVE
	// one: possession and the declaration block each refuse a specific wrong
	// thing, this one requires a right thing to be there.
	if fm, ferr := artifact.ParseFrontmatter(d.Raw); ferr == nil {
		sectionViolations, sverr := checkHandoffSections(fm.Body, env.Type)
		if sverr != nil {
			return Result{}, &Error{Op: op, Err: sverr}
		}
		violations = append(violations, sectionViolations...)
	}

	violations = append(violations, checkIDForm(env, d.Path)...)
	violations = append(violations, checkRefs(env, ctx.Resolver)...)
	violations = append(violations, checkFork(env, ctx.Resolver)...)
	violations = append(violations, checkForeignMint(env, ctx.Resolver)...)
	violations = append(violations, checkAuthz(env, ctx.OwnSystem)...)
	violations = append(violations, checkAddressees(env, ctx.Resolver)...)

	// no-silent-yes-2026-08/P3 stage 2 (spec 03 §8 AC 4/5/13): §10.4's
	// "restricted ⇒ bilateral space" rule, checked against ctx.Resolver's
	// own optional ActiveParticipantLister capability (classification.go).
	// Placed beside checkAddressees deliberately: both read env.To against
	// the manifest's own participant facts through ctx.Resolver, and
	// neither needs events/instance.
	violations = append(violations, checkClassificationBilateral(env, ctx.Resolver)...)

	// P6 incompleteness (incompleteness.go): AC1's unmet[]-index-range
	// guard and AC8's residue guard. Both are cross-artifact checks that
	// need `events` and/or `ctx.Resolver`, so — unlike P4's possession
	// check — they cannot live in runCommonEnvelope, which only ever sees
	// a single Draft's own body+instance and runs at V1 too. This makes
	// both rules V2-only (`a2a submit` / `validate --ci`), never
	// `a2a validate`'s plain V1 path — a deviation from the brief's "wire
	// it where plain `a2a validate` reaches it" (see this phase's
	// Deviations report).
	violations = append(violations, checkIncompleteness(env, instance, events, ctx.Resolver)...)

	lifecycleViolations, err := checkLifecycle(events, ctx.Legality)
	if err != nil {
		return Result{}, &Error{Op: op, Err: err}
	}
	violations = append(violations, lifecycleViolations...)

	violations = append(violations, scanForSecrets(d.Raw)...)

	return newResult(V2, artifactID, violations), nil
}

// ValidateLifecycleCandidates runs only the lifecycle class over candidate
// events. It exists for the V3 merge gate: the event document has already
// passed ValidateEvent, while its legality must be re-derived against the
// merge-base history rather than the PR checkout's post-write history.
//
// Keeping the LFC verdict mapping here means submit-time and merge-time
// validation cannot grow separate translations of fold's three-valued
// verdict into registry codes.
func (e *Engine) ValidateLifecycleCandidates(events []CandidateEvent, checker LegalityChecker) (Result, error) {
	const op = "ValidateLifecycleCandidates"
	violations, err := checkLifecycle(events, checker)
	if err != nil {
		return Result{}, &Error{Op: op, Err: err}
	}
	artifactID := ""
	if len(events) > 0 {
		artifactID = events[0].Subject
	}
	return newResult(V2, artifactID, violations), nil
}

// runCommon runs the shared V1/V2 prefix (admission guards, frontmatter
// parse, schema class, ID-form) and returns just the accumulated
// violations + artifact ID — used by ValidateDraft, which never needs the
// decoded envelope itself.
func (e *Engine) runCommon(d Draft) ([]Violation, string, error) {
	violations, artifactID, _, _, _, err := e.runCommonEnvelope(d)
	return violations, artifactID, err
}

// runCommonEnvelope is runCommon's fuller sibling: it also returns the
// decoded envelope, the raw JSON-Schema-validatable frontmatter instance
// (map[string]any / []any / scalars, per schema.DecodeYAMLInstance — used
// by ValidateForSubmit's V2-only POL-010 placeholder check, never by V1),
// and whether processing reached that far (ok=false means an admission/
// frontmatter failure already terminated the run — the caller should not
// attempt referential/authz/lifecycle/policy checks against a zero-value
// envelope).
func (e *Engine) runCommonEnvelope(d Draft) (violations []Violation, artifactID string, env envelope, instance any, ok bool, err error) {
	violations = append(violations, checkAdmission(d.Raw)...)
	for _, v := range violations {
		if v.Severity == SeverityReject {
			// CC-006/CC-007: cannot safely proceed to parse the
			// artifact at all.
			return violations, "", envelope{}, nil, false, nil
		}
	}

	fm, ferr := artifact.ParseFrontmatter(d.Raw)
	if ferr != nil {
		violations = append(violations, malformedFrontmatterViolation())
		return violations, "", envelope{}, nil, false, nil
	}

	env, instance, derr := decodeEnvelope(fm.YAML)
	if derr != nil {
		violations = append(violations, malformedFrontmatterViolation())
		return violations, "", envelope{}, nil, false, nil
	}
	artifactID = env.ID

	n, vok := schema.ParseVersion(env.Schema)
	if !vok || !schema.AcceptsEnvelopeVersion(n) {
		violations = append(violations, Violation{
			Code:     "POL-005",
			Class:    ClassPolicy,
			Path:     "schema",
			Message:  unreadableOrUnacceptedSchemaMessage("this artifact", "envelope", env.Schema),
			CCRef:    "CC-005",
			Severity: SeverityReject,
		})
		return violations, artifactID, env, instance, false, nil
	}

	if !isKnownEnvelopeType(env.Type) {
		return violations, artifactID, env, instance, false, &Error{Err: fmt.Errorf("%w: %q", ErrUnknownEnvelopeType, env.Type)}
	}

	fieldViolations, serr := e.corpus.ValidateEnvelope(env.Type, env.Schema, instance)
	if serr != nil {
		return violations, artifactID, env, instance, false, serr
	}
	schemaViolations, merr := mapSchemaViolations(fieldViolations)
	if merr != nil {
		return violations, artifactID, env, instance, false, merr
	}
	violations = append(violations, schemaViolations...)

	// P4 possession (REF-017/POL-017, possession.go): digest-versus-
	// declaration over the body's own text, runs here rather than being
	// threaded through a new return value, because both ValidateDraft
	// (V1, via runCommon) and ValidateForSubmit (V2) already reach this
	// single success path with fm.Body and the decoded instance in
	// scope. That is also what makes `a2a validate` (no --ci) catch it
	// locally: ValidateDraft's only route to a decoded instance IS this
	// function (ground truth: engine.go's ValidateDraft -> runCommon ->
	// runCommonEnvelope).
	violations = append(violations, checkPossession(fm.Body, instance)...)

	// P5 declared-nature (POL-018, declaration_block.go): a hand-rolled
	// `key = value` body declaration whose key matches a field this
	// artifact's OWN (version, typ) schema already declares. Runs here for
	// the same reason checkPossession does — both ValidateDraft (V1) and
	// ValidateForSubmit (V2) reach this single success path with fm.Body
	// and the already-parsed (n, env.Type) in scope, so `a2a validate`
	// (no --ci) catches it locally too.
	declBlockViolations, dberr := checkDeclarationBlock(fm.Body, n, env.Type)
	if dberr != nil {
		return violations, artifactID, env, instance, false, dberr
	}
	violations = append(violations, declBlockViolations...)

	// POL-023 (defects-fix-2026-08 P8): a contract inviting a runtime pin
	// against an operational half it declares ABSENT. A warning, not a
	// refusal — ADR-011 D2: publishing a promise before activating it is a
	// designed sequence, so the conjunction is a claim ahead of the facts
	// rather than an invalid document. Mode scope `both`, and a warning may
	// judge history because it refuses nothing.
	violations = append(violations, checkOperationalClaim(instance)...)

	return violations, artifactID, env, instance, true, nil
}

func isKnownEnvelopeType(t string) bool {
	for _, known := range schema.EnvelopeTypes() {
		if t == known {
			return true
		}
	}
	return false
}
