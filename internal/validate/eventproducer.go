package validate

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

// ValidateEventProducer requires an event to name the tool and version that
// produced it, once the space's own min_binary_version has reached the release
// that introduced the field (version.ProducerStampFloor).
//
// # This is the only place an old binary can actually be stopped
//
// `min_binary_version` is checked inside the WRITER's own binary
// (internal/space's funnel, step 1b). That binds a binary which honours the
// check and does nothing whatever to one that does not — and "one that does
// not" is not hypothetical: before 0.9.0 every lifecycle and contract verb
// wrote with an EMPTY floor, because the shared request builder never populated
// it. Raising a space's floor could not gate those writes even in principle.
//
// The stamp closes that, because it is read at MERGE, by the space, from the
// document itself.
//
// # What it does not do
//
// It checks PRESENCE, not truth. The stamp is emitted by the very client the
// space is distrusting, so anything hand-crafting an event can claim any
// version. Said plainly rather than left implied, because a field that looks
// like a security control and is not is worse than no field: what this catches
// is a colleague on a stale binary, which is the failure that actually
// happened.
//
// # Why the floor gates the RULE and not just the value
//
// Returns ok=false with no violations — nothing to report — when the space's
// floor has not reached the field. That is not a pass in disguise: with the
// floor lower, the requirement does not exist yet, and reporting a verdict for
// a rule not in force would put noise on every write in every space that has
// not migrated. version.ProducerStampActive's own doc comment carries the
// reason the floor is the switch: event/v1 is additionalProperties:false and the
// validator is pinned by the space, so the field cannot be REQUIRED before it
// can be ACCEPTED.
func (e *Engine) validateEventProducer(raw []byte, spaceFloor string) (result Result, required bool, err error) {
	const op = "validateEventProducer"

	active, aerr := version.ProducerStampActive(spaceFloor)
	if aerr != nil {
		return Result{}, false, &Error{Op: op, Err: aerr}
	}
	if !active {
		return Result{}, false, nil
	}

	probe, parseable := decodeEventProducer(raw)
	if !parseable {
		return newResult(V2, "", []Violation{malformedEventViolation()}), true, nil
	}

	if probe.ProducedBy.Tool != "" && probe.ProducedBy.Version != "" {
		return newResult(V2, probe.Event, nil), true, nil
	}
	return newResult(V2, probe.Event, []Violation{{
		Code:  "POL-012",
		Class: ClassPolicy,
		Path:  "produced_by",
		Message: fmt.Sprintf("event does not name the tool and version that produced it, and this space's "+
			"min_binary_version (%s) has reached %s, where that became required. The binary that wrote it "+
			"is older than this space's floor and did not stamp the write — run `a2a update`. Note that "+
			"min_binary_version alone cannot stop that binary: it is checked INSIDE the writer, so a "+
			"binary predating the check ignores it entirely, which is exactly why this is checked here.",
			spaceFloor, version.ProducerStampFloor),
		CCRef:    "CC-085",
		Severity: SeverityReject,
	}}), true, nil
}

// eventProducerProbe is validate's bounded typed projection of the event fields
// used by producer, actor-provenance and receipt policy.
type eventProducerProbe struct {
	Event      string  `yaml:"event"`
	Subject    string  `yaml:"subject"`
	Transition string  `yaml:"transition"`
	State      *string `yaml:"state"`
	Version    string  `yaml:"version"`
	Actor      struct {
		Kind    string  `yaml:"kind"`
		Name    string  `yaml:"name"`
		System  string  `yaml:"system"`
		Model   *string `yaml:"model"`
		Session *string `yaml:"session"`
	} `yaml:"actor"`
	ProducedBy struct {
		Tool    string `yaml:"tool"`
		Version string `yaml:"version"`
	} `yaml:"produced_by"`
}

// decodeEventProducer reports ok=false when the document is not YAML at all.
//
// A bool rather than an error, for the same reason ValidateManifest's own decode
// helper does: "this file is not YAML" is a verdict about CONTENT, which belongs
// in the Result as a POL-002 violation, while an error returned from
// ValidateEventProducer means the ENGINE failed — a different thing a caller
// handles differently. Letting the decode error cross that boundary and then
// discarding it is what the linter objects to, and silencing the linter would
// hide a real distinction rather than a false positive.
func decodeEventProducer(raw []byte) (probe eventProducerProbe, ok bool) {
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return eventProducerProbe{}, false
	}
	return probe, true
}

// malformedEventViolation is the event twin of malformedConsumesViolation, and
// reuses POL-002 for the same reason: it is the registry's one "this document is
// not valid YAML" code.
func malformedEventViolation() Violation {
	return Violation{
		Code:     "POL-002",
		Class:    ClassPolicy,
		Path:     "",
		Message:  "event document is not valid YAML",
		CCRef:    "CC-001",
		Severity: SeverityReject,
	}
}

// ValidateEvent validates one committed event document: its event/v1 SCHEMA, and
// — once the space's floor has reached version.ProducerStampFloor — that it names
// the producer that wrote it.
//
// # The schema half is new, and its absence was the same hole twice over
//
// Until 2026-07-26 `validate --ci` validated `*.md` artifacts, `consumes.yaml`,
// contracts and (from the same day) `space.yaml` — and events not at all. A
// changed event merged as long as it was parseable YAML, and the fold read it
// afterwards. That is the identical shape as the two holes closed alongside it:
// a schema that exists, and documents nobody fed to it.
//
// Wired only AFTER the corpus was checked, which is the discipline the manifest
// check established: every event in the live production space and in the test
// space — 46 of them — was run through this schema by hand first, and all
// passed. A gate is not turned on to find out whether it reds.
//
// ONE Result for both halves, so a reader gets one verdict per event rather than
// two entries for the same file disagreeing about how bad it is.
func (e *Engine) ValidateEvent(raw []byte, spaceFloor string) (Result, error) {
	return e.validateEvent(raw, spaceFloor, nil)
}

// ValidateEventWithEvaluation is the contextual V2/V3 event-validation path.
// The caller supplies the exact fold.EvaluateCandidate result it used for
// legality, so receipt applicability and outcome cannot grow a second state
// machine in validation. ValidateEvent remains the raw/full-history path for
// callers that do not have candidate context.
func (e *Engine) ValidateEventWithEvaluation(raw []byte, spaceFloor string, evaluation fold.CandidateEvaluation) (Result, error) {
	return e.validateEvent(raw, spaceFloor, &evaluation)
}

func (e *Engine) validateEvent(raw []byte, spaceFloor string, evaluation *fold.CandidateEvaluation) (Result, error) {
	const op = "ValidateEvent"

	instance, probe, parseable := decodeEvent(raw)
	if !parseable {
		return newResult(V2, "", []Violation{malformedEventViolation()}), nil
	}

	fieldViolations, serr := e.corpus.ValidateEvent("event/v1", instance)
	if serr != nil {
		return Result{}, &Error{Op: op, Err: serr}
	}
	violations, merr := mapSchemaViolations(fieldViolations)
	if merr != nil {
		return Result{}, &Error{Op: op, Err: merr}
	}

	stampResult, required, perr := e.validateEventProducer(raw, spaceFloor)
	if perr != nil {
		return Result{}, perr
	}
	if required {
		violations = append(violations, stampResult.Violations...)
	}

	firstPartyAtFloor, producerViolation := firstPartyReceiptStatus(probe)
	if producerViolation != nil && !hasViolationCode(violations, producerViolation.Code) {
		violations = append(violations, *producerViolation)
	}
	if firstPartyAtFloor {
		violations = append(violations, checkFirstPartyActor(probe)...)
	}

	var consistency []ConsistencyFlag
	if evaluation != nil {
		receiptViolations, receiptConsistency := checkEventReceipt(probe, firstPartyAtFloor, *evaluation)
		violations = append(violations, receiptViolations...)
		consistency = append(consistency, receiptConsistency...)
	}

	result := newResult(V2, probe.Event, violations)
	result.ConsistencyFlags = consistency
	return result, nil
}

func hasViolationCode(violations []Violation, code string) bool {
	for _, violation := range violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}

// decodeEvent decodes raw twice — once as a schema instance, once into the
// producer probe — and reports ok=false when it is not YAML at all.
//
// A BOOL rather than an error, for the same reason ValidateManifest's own decode
// helper uses one: "this file is not YAML" is a verdict about CONTENT that
// belongs in the Result as POL-002, while an error returned from ValidateEvent
// means the ENGINE failed, which a caller handles differently.
//
// schema.DecodeYAMLInstance, NOT a plain unmarshal: yaml.v3 auto-types an
// unquoted date/time as a time.Time, which the JSON-schema validator cannot
// represent — it aborts with "unmapped schema-class keyword" instead of
// validating anything. Every event carries `at:`, so this would have fired on
// literally every write. It was hit twice the same day on manifests, and is the
// reason this is a shared helper rather than an inline unmarshal per caller.
func decodeEvent(raw []byte) (instance any, probe eventProducerProbe, ok bool) {
	decoded, err := schema.DecodeYAMLInstance(raw)
	if err != nil {
		return nil, eventProducerProbe{}, false
	}
	p, ok := decodeEventProducer(raw)
	if !ok {
		return nil, eventProducerProbe{}, false
	}
	return decoded, p, true
}
