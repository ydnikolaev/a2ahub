package validate

// to_cardinality_test.go proves D6's `to` cardinality split (no-silent-yes-
// 2026-08-28, spec 05-a-contract-says-what-it-must.md §11's 2026-08-28
// amendment #1): the schema ALREADY enforces "to: all" as legal on the
// MULTI-ADDRESSABLE group (contract, requirement, announcement, decision) and
// refused on the X exchange class (response, work_request, question,
// handoff). Measured 2026-08-28 by driving Corpus.ValidateEnvelope
// directly, one instance per type and version — this file reproduces that
// measurement as a durable, running test.
//
// The group is called MULTI-ADDRESSABLE rather than "standing" on purpose:
// 03-domain.md §3.1's own taxonomy puts `decision` in the X exchange class
// (individually excepted from the cardinality rule at §3.4.3) and
// `announcement` in its own B broadcast class, so only {contract,
// requirement} are S standing types. Naming the cardinality grouping after a
// lifecycle class it does not match is the same defect this epic is about,
// one document up. The plan carries the same correction.
//
// THIS IS A TEST, NOT A RULE. It adds no validation code: a second
// enforcement site for a rule the schema already holds is exactly what
// ADR-019 forbids. If the split below turns out not to hold, the finding
// belongs in the phase's report, not in a fix bent to match a wrong
// assumption.
//
// Driven per (type, version) rather than "the eight types", because a
// version-blind table would half-miss: envelope/v2 registers decoders for
// only base, contract, announcement, work_request and response
// (internal/schema/corpus.go's corpusDefinitions) — question, handoff,
// decision and requirement have no v2 schema at all and are tested at v1
// here. response and work_request redeclare `to` in BOTH v1 and v2; this
// file exercises them at v2, matching the lead's own measurement table.
//
// Each case below also carries a `to: [one-system]` regression check: the
// move D6 makes widens what a contract may declare, it must never narrow
// what is already committed — every existing `to: [one]` document, on
// either side of the class split, must keep validating.

import (
	"fmt"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// toClass is which cardinality rule a type's `to` field is subject to.
type toClass int

const (
	// classStanding inherits base.schema.json's permissive `to` oneOf:
	// an array of at least one system, OR the literal "all". No maxItems.
	classStanding toClass = iota
	// classExchange redeclares `to` at the type's own schema:
	// {type: array, minItems: 1, maxItems: 1} — no "all" branch at all.
	classExchange
)

// toCardinalityCase names one (type, version) cell of D6's split, plus a
// minimal schema-legal document carrying a single "%s" placeholder that
// stands in for the `to:` line's value alone (e.g. "all", "[beta]",
// "[beta, gamma]") — every other required field is already filled in with
// a schema-legal value so the only variable under test is `to`.
type toCardinalityCase struct {
	typ, version string
	class        toClass
	docTemplate  string
}

const contractV2Doc = `
schema: envelope/v2
id: XC-axon-widget
type: contract
title: t
space: fixture-space
from: axon
to: %s
actor: {kind: agent, name: bot}
created: "2026-08-10T10:00:00Z"
priority: p3
blocking: true
classification: internal
thread: thread:axon-20260810-ab12
category: api
version: "1.0.0"
compat_policy: strict-semver
schema_format: json-schema-2020-12
artifacts:
  - path: schema/widget.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
`

const announcementV2Doc = `
schema: envelope/v2
id: XA-axon-20260810-ab12
type: announcement
title: t
space: fixture-space
from: axon
to: %s
actor: {kind: agent, name: bot}
created: "2026-08-10T10:00:00Z"
priority: p3
blocking: true
classification: internal
thread: thread:axon-20260810-ab12
category: release
`

const decisionV1Doc = `
schema: envelope/v1
id: XD-axon-20260810-ab12
type: decision
title: t
space: fixture-space
from: axon
to: %s
actor: {kind: agent, name: bot}
created: "2026-08-10T10:00:00Z"
priority: p3
blocking: true
classification: internal
thread: thread:axon-20260810-ab12
required_approvers: [axon]
context: "why"
options_considered: ["opt1", "opt2"]
`

const requirementV1Doc = `
schema: envelope/v1
id: XR-axon-1
type: requirement
title: t
space: fixture-space
from: axon
to: %s
actor: {kind: agent, name: bot}
created: "2026-08-10T10:00:00Z"
priority: p3
blocking: true
classification: internal
thread: thread:axon-20260810-ab12
category: other
acceptance_criteria: ["done"]
`

const responseV2Doc = `
schema: envelope/v2
id: XS-axon-20260810-ab12
type: response
title: t
space: fixture-space
from: axon
to: %s
actor: {kind: agent, name: bot}
created: "2026-08-10T10:00:00Z"
priority: p3
blocking: true
classification: internal
thread: thread:axon-20260810-ab12
parent: XW-axon-1
result: answered
`

const workRequestV2Doc = `
schema: envelope/v2
id: XW-axon-20260810-ab12
type: work_request
title: t
space: fixture-space
from: axon
to: %s
actor: {kind: agent, name: bot}
created: "2026-08-10T10:00:00Z"
priority: p3
blocking: true
classification: internal
thread: thread:axon-20260810-ab12
category: data
acceptance_criteria: ["done"]
`

const questionV1Doc = `
schema: envelope/v1
id: XQ-axon-20260810-ab12
type: question
title: t
space: fixture-space
from: axon
to: %s
actor: {kind: agent, name: bot}
created: "2026-08-10T10:00:00Z"
priority: p3
blocking: true
classification: internal
thread: thread:axon-20260810-ab12
category: clarification
`

const handoffV1Doc = `
schema: envelope/v1
id: XH-axon-20260810-ab12
type: handoff
title: t
space: fixture-space
from: axon
to: %s
actor: {kind: agent, name: bot}
created: "2026-08-10T10:00:00Z"
priority: p3
blocking: true
classification: internal
thread: thread:axon-20260810-ab12
deliverables:
  - {name: x, ref: y, kind: doc}
verification: "manual check"
acceptance_criteria: ["done"]
limitations: []
fulfills: ["XW-axon-1"]
`

// toCardinalityCases is the split the lead measured 2026-08-28, reproduced
// as a running table: four MULTI-ADDRESSABLE types (contract, announcement,
// decision, requirement) and the four X exchange types (response,
// work_request, question, handoff).
var toCardinalityCases = []toCardinalityCase{
	{"contract", "v2", classStanding, contractV2Doc},
	{"announcement", "v2", classStanding, announcementV2Doc},
	{"decision", "v1", classStanding, decisionV1Doc},
	{"requirement", "v1", classStanding, requirementV1Doc},
	{"response", "v2", classExchange, responseV2Doc},
	{"work_request", "v2", classExchange, workRequestV2Doc},
	{"question", "v1", classExchange, questionV1Doc},
	{"handoff", "v1", classExchange, handoffV1Doc},
}

// TestToCardinality_StandingAcceptsAllExchangeRefuses is the split itself:
// `to: all` validates cleanly on every MULTI-ADDRESSABLE type and is refused, with
// the registered SCH-006 code, on every X exchange type.
func TestToCardinality_StandingAcceptsAllExchangeRefuses(t *testing.T) {
	t.Parallel()
	corpus := mustLoadCorpus(t)

	for _, tc := range toCardinalityCases {
		t.Run(tc.typ+"_"+tc.version, func(t *testing.T) {
			t.Parallel()
			fvs := validateToDoc(t, corpus, tc, "all")

			switch tc.class {
			case classStanding:
				if len(fvs) != 0 {
					t.Fatalf("%s %s: to: all expected to validate (multi-addressable, D6) — got violations: %+v", tc.typ, tc.version, fvs)
				}
			case classExchange:
				if len(fvs) != 1 {
					t.Fatalf("%s %s: to: all expected exactly one violation (X exchange class, D6) — got: %+v", tc.typ, tc.version, fvs)
				}
				if fvs[0].Path != "to" {
					t.Fatalf("%s %s: to: all violation path = %q, want %q: %+v", tc.typ, tc.version, fvs[0].Path, "to", fvs[0])
				}
				if fvs[0].Keyword != "type" {
					t.Fatalf("%s %s: to: all violation keyword = %q, want %q: %+v", tc.typ, tc.version, fvs[0].Keyword, "type", fvs[0])
				}
				violations, err := mapSchemaViolations(fvs)
				if err != nil {
					t.Fatalf("mapSchemaViolations: %v", err)
				}
				if len(violations) != 1 || violations[0].Code != "SCH-006" {
					t.Fatalf("%s %s: to: all mapped code = %+v, want exactly one SCH-006", tc.typ, tc.version, violations)
				}
			}
		})
	}
}

// TestToCardinality_ExchangeRefusesMultipleTargets is D6's OTHER enforced
// half: the X exchange class stays EXACTLY-one, refusing two named systems
// (not only the broadcast literal) with the registered SCH-004 code. The
// The multi-addressable group accepts it — base's oneOf array branch carries no maxItems.
func TestToCardinality_ExchangeRefusesMultipleTargets(t *testing.T) {
	t.Parallel()
	corpus := mustLoadCorpus(t)

	for _, tc := range toCardinalityCases {
		t.Run(tc.typ+"_"+tc.version, func(t *testing.T) {
			t.Parallel()
			fvs := validateToDoc(t, corpus, tc, "[beta, gamma]")

			switch tc.class {
			case classStanding:
				if len(fvs) != 0 {
					t.Fatalf("%s %s: to: [beta, gamma] expected to validate (multi-addressable, no maxItems on the inherited oneOf) — got violations: %+v", tc.typ, tc.version, fvs)
				}
			case classExchange:
				if len(fvs) != 1 {
					t.Fatalf("%s %s: to: [beta, gamma] expected exactly one violation (X exchange class EXACTLY-one, D6) — got: %+v", tc.typ, tc.version, fvs)
				}
				if fvs[0].Path != "to" {
					t.Fatalf("%s %s: to: [beta, gamma] violation path = %q, want %q: %+v", tc.typ, tc.version, fvs[0].Path, "to", fvs[0])
				}
				if fvs[0].Keyword != "maxItems" {
					t.Fatalf("%s %s: to: [beta, gamma] violation keyword = %q, want %q: %+v", tc.typ, tc.version, fvs[0].Keyword, "maxItems", fvs[0])
				}
				violations, err := mapSchemaViolations(fvs)
				if err != nil {
					t.Fatalf("mapSchemaViolations: %v", err)
				}
				if len(violations) != 1 || violations[0].Code != "SCH-004" {
					t.Fatalf("%s %s: to: [beta, gamma] mapped code = %+v, want exactly one SCH-004", tc.typ, tc.version, violations)
				}
			}
		})
	}
}

// TestToCardinality_SingleTargetStaysValid is the "the move widens, it
// never narrows" regression check (spec AC 14's own wording, proven here
// directly rather than only through the untouched txtar fixture): a
// single-system `to: [one]` — the shape every already-committed document
// carries — must keep validating on BOTH sides of the class split.
func TestToCardinality_SingleTargetStaysValid(t *testing.T) {
	t.Parallel()
	corpus := mustLoadCorpus(t)

	for _, tc := range toCardinalityCases {
		t.Run(tc.typ+"_"+tc.version, func(t *testing.T) {
			t.Parallel()
			fvs := validateToDoc(t, corpus, tc, "[beta]")
			if len(fvs) != 0 {
				t.Fatalf("%s %s: to: [beta] (the committed single-target shape) expected to validate on both sides of D6's split — got violations: %+v", tc.typ, tc.version, fvs)
			}
		})
	}
}

func mustLoadCorpus(t *testing.T) *schema.Corpus {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	return corpus
}

// validateToDoc fills tc's docTemplate with toValue as the `to:` line's
// value, decodes it exactly as internal/schema's own callers do (never a
// naive yaml.Unmarshal-into-any + json round-trip — see
// schema.DecodeYAMLInstance's own doc comment), and runs it through
// ValidateEnvelope.
func validateToDoc(t *testing.T, corpus *schema.Corpus, tc toCardinalityCase, toValue string) []schema.FieldViolation {
	t.Helper()
	doc := fmt.Sprintf(tc.docTemplate, toValue)
	instance, err := schema.DecodeYAMLInstance([]byte(doc))
	if err != nil {
		t.Fatalf("%s %s: DecodeYAMLInstance: %v\ndoc:\n%s", tc.typ, tc.version, err, doc)
	}
	fvs, err := corpus.ValidateEnvelope(tc.typ, tc.version, instance)
	if err != nil {
		t.Fatalf("%s %s: ValidateEnvelope: %v", tc.typ, tc.version, err)
	}
	return fvs
}
