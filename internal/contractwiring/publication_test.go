package contractwiring

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// newTestEngine builds a *validate.Engine over the repo's real schema
// corpus, the same way cmd/a2a's own newEngine (cmd/a2a/wire.go) does, via
// the exported internal/schema.Load — this package's own copy because
// cmd/a2a's newEngine is unexported and unreachable from here.
func newTestEngine(t *testing.T) *validate.Engine {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	return validate.New(corpus)
}

// TestContractPublicationEventBuilderProducesV1AndV2Receipts is ported from
// cmd/a2a's own contract_p6_wiring_test.go (pre-dating the move of
// contractPublicationEventBuilder into this package) — the widest-surface
// function this package carries, BuildContractPublicationEvent, exercised
// across both the legacy fixed-v1 and declared-v2 inventory floors.
func TestContractPublicationEventBuilderProducesV1AndV2Receipts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		floor    string
		declared bool
		want     string
	}{
		{name: "legacy v1", floor: "0.18.9", want: "event/v1"},
		{name: "declared v2", floor: contract.ContractPublicationFloor, declared: true, want: "event/v2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan := testContractPublicationPlan(t, test.floor, test.declared)
			engine := newTestEngine(t)
			builder := &contractPublicationEventBuilder{
				mirrorDir: t.TempDir(), spaceID: "test", ownSystem: "axon",
				actor: template.Actor{Kind: "agent", Name: "publisher", Model: "codex"}, engine: engine,
				now:     func() time.Time { return time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC) },
				entropy: bytes.NewReader(make([]byte, 16)),
				loadManifest: func() (space.Manifest, error) {
					return space.Manifest{Participants: []space.Participant{{System: "axon", Status: "active"}}}, nil
				},
			}
			write, err := builder.BuildContractPublicationEvent(t.Context(), plan)
			if err != nil {
				t.Fatalf("BuildContractPublicationEvent: %v", err)
			}
			var document contractPublicationEventDocument
			if err := yaml.Unmarshal(write.Content, &document); err != nil {
				t.Fatal(err)
			}
			if document.Schema != test.want || document.State != string(fold.StatePublished) || (document.Publication != nil) != test.declared {
				t.Fatalf("publication event = %+v, want schema=%s state=published v2-publication=%t", document, test.want, test.declared)
			}
			result, err := engine.ValidateEventWithEvaluation(write.Content, "0.0.0", builder.evaluation)
			if err != nil {
				t.Fatal(err)
			}
			if err := validationResultError("publication event", result); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func testContractPublicationPlan(t *testing.T, floor string, declared bool) contract.PublicationPlan {
	t.Helper()

	artifacts := ""
	if declared {
		artifacts = `artifacts:
  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}
  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
  - {path: fixtures/invalid/missing-id.json, role: invalid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
`
	}
	descriptor := []byte(fmt.Sprintf(`---
schema: envelope/v1
id: XC-axon-orders
type: contract
title: Orders
space: test
from: axon
to: [beta]
actor: {kind: agent, name: publisher, model: codex}
created: 2026-08-03T12:00:00Z
category: api
priority: p2
blocking: false
classification: internal
version: 0.0.0
schema_format: json-schema-2020-12
compat_policy: default
thread: thread:axon-20260803-a1b2
%s---
# Orders
`, artifacts))
	files := []contract.CandidateFile{
		{Path: "schema/order.schema.json", Kind: contract.CandidateRegular, Raw: []byte(`{"type":"object","required":["id"]}`)},
		{Path: "fixtures/valid/order.json", Kind: contract.CandidateRegular, Raw: []byte(`{"id":"1"}`)},
		{Path: "fixtures/invalid/missing-id.json", Kind: contract.CandidateRegular, Raw: []byte(`{}`)},
	}
	candidate, issues := contract.BuildCandidateIntent(contract.CandidateSnapshot{
		Descriptor: contract.CandidateFile{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Raw: descriptor}, Files: files,
	})
	if len(issues) != 0 {
		t.Fatalf("BuildCandidateIntent: %+v", issues)
	}
	plan, issues := contract.PlanPublication(contract.PublicationInput{
		System: "axon", ContractID: "XC-axon-orders", Selector: "explicit:1.0.0", AuthoringFloor: floor,
		Candidate: candidate, CandidateSource: contract.CandidateSource{Kind: contract.CandidateSourceMirror, Location: strings.Repeat("a", 40), Fingerprint: "sha256:" + strings.Repeat("b", 64)},
		ContractRoot: "axon/provides/orders",
	}, validate.ContractCompatibilityAdapter{})
	if len(issues) != 0 {
		t.Fatalf("PlanPublication: %+v", issues)
	}
	return plan
}
