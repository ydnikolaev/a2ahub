package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

func TestContractTargetArgsSelectsOnlyXCArtifact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "publish flags before id", args: []string{"publish", "--version", "1.2.3", "XC-beta-orders"}, want: []string{"XC-beta-orders"}},
		{name: "exact reference", args: []string{"materialize", "--to", "generated", "XC-axon-orders@1.2.3"}, want: []string{"XC-axon-orders"}},
		{name: "staging value cannot masquerade", args: []string{"publish", "--staging", "XC-beta-wrong", "--version", "1.2.3", "XC-axon-orders"}, want: []string{"XC-axon-orders"}},
		{name: "payload value cannot masquerade", args: []string{"check", "--payload", "XC-beta-wrong", "XC-axon-orders@1.2.3"}, want: []string{"XC-axon-orders"}},
		{name: "destination value cannot masquerade", args: []string{"materialize", "--to", "XC-beta-wrong", "XC-axon-orders@1.2.3"}, want: []string{"XC-axon-orders"}},
		{name: "local value cannot masquerade", args: []string{"verify-export", "--local", "XC-beta-wrong", "XC-axon-orders@1.2.3"}, want: []string{"XC-axon-orders"}},
		{name: "equals flag value cannot masquerade", args: []string{"publish", "--staging=XC-beta-wrong", "XC-axon-orders", "--version=1.2.3"}, want: []string{"XC-axon-orders"}},
		{name: "new slug is not target", args: []string{"new", "orders"}},
		{name: "subcommand only", args: []string{"diff"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := contractTargetArgs(test.args); !slices.Equal(got, test.want) {
				t.Fatalf("contractTargetArgs(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestMCPContractP6RouterRequiresPerRequestSpaceAcrossMultipleConnections(t *testing.T) {
	t.Parallel()

	orders := &contractP6Core{spaceID: "orders"}
	billing := &contractP6Core{spaceID: "billing"}
	router := mcpContractP6Router{bySpace: map[string]*contractP6Core{"orders": orders, "billing": billing}}
	if got, err := router.coreFor("orders"); err != nil || got != orders {
		t.Fatalf("explicit orders route = %p, %v", got, err)
	}
	if _, err := router.coreFor(""); err == nil || !strings.Contains(err.Error(), "space is required when multiple spaces are connected") {
		t.Fatalf("ambiguous route error = %v", err)
	}
	if _, err := router.coreFor("missing"); err == nil || !strings.Contains(err.Error(), `space "missing" is not connected`) {
		t.Fatalf("unknown route error = %v", err)
	}
	single := mcpContractP6Router{bySpace: map[string]*contractP6Core{"orders": orders}}
	if got, err := single.coreFor(""); err != nil || got != orders {
		t.Fatalf("single-space default route = %p, %v", got, err)
	}
	if _, err := (mcpContractP6Router{}).coreFor(""); err == nil || !strings.Contains(err.Error(), "no connected space") {
		t.Fatalf("empty route error = %v", err)
	}
}

func TestReadBoundedProjectFileEnforcesContainmentAndLimit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "payloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "payloads", "valid.json"), []byte(`{"id":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "too-large.json"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "payloads", "valid.json"), filepath.Join(root, "linked.json")); err != nil {
		t.Fatal(err)
	}

	raw, err := readBoundedProjectFile(root, "payloads/valid.json", 8)
	if err != nil || string(raw) != `{"id":1}` {
		t.Fatalf("bounded valid read = %q, %v", raw, err)
	}
	for _, name := range []string{"../outside.json", "./payloads/valid.json", "/tmp/outside.json", "linked.json", "too-large.json"} {
		if _, err := readBoundedProjectFile(root, name, 4); err == nil {
			t.Fatalf("unsafe or oversized path %q was accepted", name)
		}
	}
}

func TestMaterializeContractClosesHeldRootOnEveryOutcome(t *testing.T) {
	t.Parallel()

	operationErr := fmt.Errorf("materialize failed")
	closeErr := fmt.Errorf("close failed")
	for _, test := range []struct {
		name      string
		operation error
		close     error
		wantErr   string
	}{
		{name: "success"},
		{name: "operation failure", operation: operationErr, wantErr: operationErr.Error()},
		{name: "close failure", close: closeErr, wantErr: "close contract materializer: " + closeErr.Error()},
		{name: "both failures", operation: operationErr, close: closeErr, wantErr: operationErr.Error() + "\nclose contract materializer: " + closeErr.Error()},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			materializer := &contractMaterializeCapabilityFake{operationErr: test.operation, closeErr: test.close}
			_, err := materializeContractAndClose(t.Context(), materializer, space.HistoricalSnapshot{}, "generated")
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != test.wantErr {
				t.Fatalf("materialize error = %q, want %q", got, test.wantErr)
			}
			if materializer.materializeCalls != 1 || materializer.closeCalls != 1 {
				t.Fatalf("calls = materialize %d close %d, want 1 each", materializer.materializeCalls, materializer.closeCalls)
			}
		})
	}
}

type contractMaterializeCapabilityFake struct {
	operationErr     error
	closeErr         error
	materializeCalls int
	closeCalls       int
}

func (f *contractMaterializeCapabilityFake) Materialize(context.Context, space.HistoricalSnapshot, string) (space.ContractMaterializeResult, error) {
	f.materializeCalls++
	return space.ContractMaterializeResult{}, f.operationErr
}

func (f *contractMaterializeCapabilityFake) Close() error {
	f.closeCalls++
	return f.closeErr
}

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
			engine, err := newEngine()
			if err != nil {
				t.Fatal(err)
			}
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
			if err := contractValidationResultError("publication event", result); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContractHistoryDocumentEngineUsesCanonicalSchemas(t *testing.T) {
	t.Parallel()

	engine, err := newEngine()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := os.ReadFile(filepath.Join("..", "..", "schemas", "envelope", "v2", "fixtures", "valid", "XC-axon-order-api.md"))
	if err != nil {
		t.Fatal(err)
	}
	event, err := os.ReadFile(filepath.Join("..", "..", "schemas", "event", "v2", "fixtures", "valid", "contract-publish.json"))
	if err != nil {
		t.Fatal(err)
	}
	validator := contractHistoryDocumentEngine{engine: engine}
	documents := space.ContractHistoryDocuments{
		Descriptor:   space.ContractHistoryDocument{Path: "axon/provides/order-api/contract.md", Schema: "envelope/v2", Raw: descriptor},
		PublishEvent: space.ContractHistoryDocument{Path: "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M7.yaml", Schema: "event/v2", Raw: event},
	}
	if err := validator.ValidateHistoricalContractDocuments(t.Context(), documents); err != nil {
		t.Fatalf("canonical historical documents rejected: %v", err)
	}
	documents.PublishEvent.Raw = []byte(strings.Replace(string(event), `"schema": "event/v2"`, `"schema": "event/v9"`, 1))
	if err := validator.ValidateHistoricalContractDocuments(t.Context(), documents); err == nil {
		t.Fatal("unknown historical event schema was accepted")
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
