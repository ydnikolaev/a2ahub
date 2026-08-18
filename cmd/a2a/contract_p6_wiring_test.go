package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
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
