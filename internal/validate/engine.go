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
	violations, artifactID, env, ok, err := e.runCommonEnvelope(d)
	if err != nil {
		return Result{}, &Error{Op: op, Err: err}
	}
	if !ok {
		// Admission or frontmatter failure already short-circuited
		// further processing (runCommonEnvelope's ok=false path).
		return newResult(V2, artifactID, violations), nil
	}

	violations = append(violations, checkIDForm(env, d.Path)...)
	violations = append(violations, checkRefs(env, ctx.Resolver)...)
	violations = append(violations, checkAuthz(env, ctx.OwnSystem)...)
	violations = append(violations, checkAddressees(env, ctx.Resolver)...)

	lifecycleViolations, err := checkLifecycle(events, ctx.Legality)
	if err != nil {
		return Result{}, &Error{Op: op, Err: err}
	}
	violations = append(violations, lifecycleViolations...)

	violations = append(violations, scanForSecrets(d.Raw)...)

	return newResult(V2, artifactID, violations), nil
}

// runCommon runs the shared V1/V2 prefix (admission guards, frontmatter
// parse, schema class, ID-form) and returns just the accumulated
// violations + artifact ID — used by ValidateDraft, which never needs the
// decoded envelope itself.
func (e *Engine) runCommon(d Draft) ([]Violation, string, error) {
	violations, artifactID, _, _, err := e.runCommonEnvelope(d)
	return violations, artifactID, err
}

// runCommonEnvelope is runCommon's fuller sibling: it also returns the
// decoded envelope and whether processing reached that far (ok=false
// means an admission/frontmatter failure already terminated the run —
// the caller should not attempt referential/authz/lifecycle/policy
// checks against a zero-value envelope).
func (e *Engine) runCommonEnvelope(d Draft) (violations []Violation, artifactID string, env envelope, ok bool, err error) {
	violations = append(violations, checkAdmission(d.Raw)...)
	for _, v := range violations {
		if v.Severity == SeverityReject {
			// CC-006/CC-007: cannot safely proceed to parse the
			// artifact at all.
			return violations, "", envelope{}, false, nil
		}
	}

	fm, ferr := artifact.ParseFrontmatter(d.Raw)
	if ferr != nil {
		violations = append(violations, malformedFrontmatterViolation())
		return violations, "", envelope{}, false, nil
	}

	env, instance, derr := decodeEnvelope(fm.YAML)
	if derr != nil {
		violations = append(violations, malformedFrontmatterViolation())
		return violations, "", envelope{}, false, nil
	}
	artifactID = env.ID

	n, vok := schema.ParseVersion(env.Schema)
	if !vok || !schema.AcceptsEnvelopeVersion(n) {
		violations = append(violations, Violation{
			Code:     "POL-005",
			Class:    ClassPolicy,
			Path:     "schema",
			Message:  "envelope schema version is outside the one-cycle overlap window this binary understands",
			CCRef:    "CC-005",
			Severity: SeverityReject,
		})
		return violations, artifactID, env, false, nil
	}

	if !isKnownEnvelopeType(env.Type) {
		return violations, artifactID, env, false, &Error{Err: fmt.Errorf("%w: %q", ErrUnknownEnvelopeType, env.Type)}
	}

	fieldViolations, serr := e.corpus.ValidateEnvelope(env.Type, env.Schema, instance)
	if serr != nil {
		return violations, artifactID, env, false, serr
	}
	schemaViolations, merr := mapSchemaViolations(fieldViolations)
	if merr != nil {
		return violations, artifactID, env, false, merr
	}
	violations = append(violations, schemaViolations...)

	return violations, artifactID, env, true, nil
}

func isKnownEnvelopeType(t string) bool {
	for _, known := range schema.EnvelopeTypes() {
		if t == known {
			return true
		}
	}
	return false
}
