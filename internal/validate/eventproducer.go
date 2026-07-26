package validate

import (
	"fmt"

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
func (e *Engine) ValidateEventProducer(raw []byte, spaceFloor string) (result Result, required bool, err error) {
	const op = "ValidateEventProducer"

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
		Code:  "POL-005",
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

// eventProducerProbe is the two things this check reads: the event id (for the
// report) and the stamp.
type eventProducerProbe struct {
	Event      string `yaml:"event"`
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
