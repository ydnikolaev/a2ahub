package validate

// InvocationPoint is which §5.5 scope ran (spec 03 §7).
type InvocationPoint string

const (
	// V1 is the authoring invocation point (`a2a new`/`a2a validate`):
	// schema class only, on the single drafted artifact.
	V1 InvocationPoint = "V1"
	// V2 is the pre-write invocation point (`a2a submit`): schema +
	// referential + authz + lifecycle legality of the accompanying
	// events.
	V2 InvocationPoint = "V2"
)

// Class is one of §5.5's four validation classes. There is no separate
// "authz" enum value: the registry's code-prefix set is exactly
// {SCH, REF, LFC, POL} (schemas/errors/v1/registry.yaml's own header
// comment), so authz-class checks (from==own section, decision
// exception, CC-002/CC-008) are reported under Referential — they are,
// in substance, checks that a declared identity (a `from`/`to` system)
// resolves correctly against a known set, which is what "referential"
// means throughout this spec (see this phase's Deviations report for the
// explicit call-out).
type Class string

const (
	// ClassSchema indicates a schema validation violation.
	ClassSchema Class = "schema"
	// ClassReferential indicates a referential validation violation.
	ClassReferential Class = "referential"
	// ClassLifecycle indicates a lifecycle validation violation.
	ClassLifecycle Class = "lifecycle"
	// ClassPolicy indicates a policy validation violation.
	ClassPolicy Class = "policy"
)

// Severity distinguishes a hard reject from a flag-only warning. §7's
// literal table has no severity field, but §3.8 (unpinned refs) and §5.5
// (V2 policy class's G5 override — "V2 only *flags* the override path,
// never grants it") both require a violation that does NOT flip `valid`
// to false. This is this phase's own refinement of the §7 contract (the
// spec explicitly allows implementor field refinement) — every OTHER
// consumer of this shape (P6, P9/hub) must honor it identically (D-011).
type Severity string

const (
	// SeverityReject is the default: any Reject-severity violation makes
	// Result.Valid false.
	SeverityReject Severity = "reject"
	// SeverityWarning flags a violation without failing validation
	// (unpinned refs §3.8, G5 override attempts §5.5).
	SeverityWarning Severity = "warning"
)

// Violation is one machine-readable finding, per spec 03 §7.
type Violation struct {
	// Code is the machine-readable registry code (schemas/errors/v1/
	// registry.yaml), e.g. "SCH-007", "REF-001", "LFC-002", "POL-001".
	// Never empty (AC row 8: every violation carries a non-empty
	// registry code).
	Code string
	// Class is one of §5.5's four validation classes.
	Class Class
	// Path is a JSON-pointer-style field path (e.g. "id", "actor.kind"),
	// or "event[N]" for a lifecycle violation on the Nth accompanying
	// event, or "" for a whole-document finding.
	Path string
	// Message is a human-readable, one-line explanation.
	Message string
	// CCRef is the corner-case ID this rule enforces (§12), when
	// applicable — "" when not.
	CCRef string
	// Severity distinguishes reject from warning-only (see Severity doc
	// comment). Zero value behaves as SeverityReject (every construction
	// site in this package sets it explicitly; the zero-value fallback
	// is a defensive default, not a relied-upon path).
	Severity Severity
}

// isReject reports whether v should flip Result.Valid to false.
func (v Violation) isReject() bool {
	return v.Severity != SeverityWarning
}

// Result is the JSON output shape shared by ValidateDraft (V1) and
// ValidateForSubmit (V2) — spec 03 §7. Both invocation points return this
// same shape so a caller gets "identical results everywhere" for shared
// (schema-class) violations (AC-201.2).
type Result struct {
	// Valid is true iff zero Reject-severity violations were found.
	Valid bool
	// ArtifactID echoes the artifact's own frontmatter `id` (empty if
	// the artifact couldn't even be parsed far enough to read one — see
	// the malformed-frontmatter POL code).
	ArtifactID string
	// InvocationPoint is which scope ran.
	InvocationPoint InvocationPoint
	// Violations is empty when Valid is true.
	Violations []Violation
}

// newResult builds a Result from an accumulated violation list, computing
// Valid from Severity per Violation.isReject.
func newResult(point InvocationPoint, artifactID string, violations []Violation) Result {
	valid := true
	for _, v := range violations {
		if v.isReject() {
			valid = false
			break
		}
	}
	if violations == nil {
		violations = []Violation{}
	}
	return Result{
		Valid:           valid,
		ArtifactID:      artifactID,
		InvocationPoint: point,
		Violations:      violations,
	}
}
