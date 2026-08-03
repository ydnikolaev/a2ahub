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
	// V3 is the merge-gate invocation point. It consumes the same pure
	// validators as V2, but its caller supplies the candidate assembled from
	// the complete base-to-head repository diff (including deleted paths).
	V3 InvocationPoint = "V3"
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

// Violation is one machine-readable finding, per spec 03 §7. The json tags
// are the §7 wire contract (snake_case) — the shape every consumer parses:
// `a2a validate`'s CLI output, the V3 CI check, the v2 hub, and P13's
// generated command reference. Changing a tag is a breaking wire change.
type Violation struct {
	// Code is the machine-readable registry code (schemas/errors/v1/
	// registry.yaml), e.g. "SCH-007", "REF-001", "LFC-002", "POL-001".
	// Never empty (AC row 8: every violation carries a non-empty
	// registry code).
	Code string `json:"code"`
	// Class is one of §5.5's four validation classes.
	Class Class `json:"class"`
	// Path is a JSON-pointer-style field path (e.g. "id", "actor.kind"),
	// or "event[N]" for a lifecycle violation on the Nth accompanying
	// event, or "" for a whole-document finding.
	Path string `json:"path"`
	// Message is a human-readable, one-line explanation.
	Message string `json:"message"`
	// CCRef is the corner-case ID this rule enforces (§12), when
	// applicable — omitted from JSON when empty (§7 `cc_ref?`).
	CCRef string `json:"cc_ref,omitempty"`
	// Severity distinguishes reject from warning-only (see Severity doc
	// comment). Zero value behaves as SeverityReject (every construction
	// site in this package sets it explicitly; the zero-value fallback
	// is a defensive default, not a relied-upon path).
	Severity Severity `json:"severity"`
	// Subjects carries the complete machine-readable entity set implicated by
	// a bounded human message (for example every consumer blocking POL-006).
	Subjects []string `json:"subjects,omitempty"`
}

// ConsistencyFlagKind identifies a non-blocking diagnostic whose evidence is
// useful to readers but which is not a schema, lifecycle, policy or security
// violation. It deliberately does not use the stable error-code registry.
type ConsistencyFlagKind string

const (
	// ConsistencyStateClaimMismatch means the producer's scoped evaluation
	// receipt differs from the outcome independently computed by fold.
	ConsistencyStateClaimMismatch ConsistencyFlagKind = "state-claim-mismatch"
)

// ConsistencyScope is the JSON-safe projection of the scalar fold scope used
// to compare one producer evaluation receipt.
type ConsistencyScope struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Version string `json:"version,omitempty"`
}

// ConsistencyActor carries diagnostic attribution only. None of these fields
// participates in lifecycle authorization or changes the authoritative fold.
type ConsistencyActor struct {
	Kind    string `json:"kind,omitempty"`
	Name    string `json:"name,omitempty"`
	System  string `json:"system,omitempty"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session,omitempty"`
}

// ConsistencyProducer identifies the tool/version that recorded a receipt.
// It is self-asserted attribution, never an integrity or authorization fact.
type ConsistencyProducer struct {
	Tool    string `json:"tool,omitempty"`
	Version string `json:"version,omitempty"`
}

// ConsistencyFlag is the evidence-rich, non-blocking receipt diagnostic from
// P1. Actual is authoritative; Claimed is retained only for comparison.
type ConsistencyFlag struct {
	Kind      ConsistencyFlagKind `json:"kind"`
	EventULID string              `json:"event_ulid"`
	Subject   string              `json:"subject"`
	Scope     ConsistencyScope    `json:"scope"`
	Claimed   string              `json:"claimed"`
	Actual    string              `json:"actual"`
	Actor     ConsistencyActor    `json:"actor"`
	Producer  ConsistencyProducer `json:"producer"`
	Cause     string              `json:"cause"`
}

// isReject reports whether v should flip Result.Valid to false.
func (v Violation) isReject() bool {
	return v.Severity != SeverityWarning
}

// Result is the JSON output shape shared by ValidateDraft (V1),
// ValidateForSubmit (V2) and contextual merge-gate validators (V3) — spec 03
// §7. Every invocation point returns this same shape so a caller gets
// "identical results everywhere" for shared violations (AC-201.2).
type Result struct {
	// Valid is true iff zero Reject-severity violations were found.
	Valid bool `json:"valid"`
	// ArtifactID echoes the artifact's own frontmatter `id` (empty if
	// the artifact couldn't even be parsed far enough to read one — see
	// the malformed-frontmatter POL code).
	ArtifactID string `json:"artifact_id"`
	// InvocationPoint is which scope ran.
	InvocationPoint InvocationPoint `json:"invocation_point"`
	// Violations is empty when Valid is true.
	Violations []Violation `json:"violations"`
	// ConsistencyFlags contains non-blocking evidence such as a producer
	// evaluation mismatch. These flags never affect Valid.
	ConsistencyFlags []ConsistencyFlag `json:"consistency_flags,omitempty"`
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
