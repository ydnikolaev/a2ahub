package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

type it10MaterializeOperation struct {
	project  string
	snapshot space.HistoricalSnapshot
}

func (o it10MaterializeOperation) MaterializeContract(ctx context.Context, request mcp.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	materializer, err := space.NewContractMaterializer(o.project)
	if err != nil {
		return space.ContractMaterializeResult{}, err
	}
	defer func() { _ = materializer.Close() }() // reason: operation error is the assertion surface
	return materializer.Materialize(ctx, o.snapshot, request.Destination)
}

type it10ContractOperations struct{}

func (it10ContractOperations) Preflight(context.Context, mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return space.ContractPublicationResult{}, nil
}
func (it10ContractOperations) Publish(context.Context, mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return space.ContractPublicationResult{}, nil
}
func (it10ContractOperations) CheckContract(context.Context, mcp.ContractCheckRequest) (contract.ConformanceResult, error) {
	return contract.ConformanceResult{}, nil
}
func (it10ContractOperations) DiffContract(context.Context, mcp.ContractDiffRequest) (mcp.ContractDiffResult, error) {
	return mcp.ContractDiffResult{}, nil
}
func (it10ContractOperations) VerifyContractExport(context.Context, mcp.ContractVerifyExportRequest) (mcp.ContractVerifyExportResult, error) {
	return mcp.ContractVerifyExportResult{}, nil
}

func TestIT10MCPFilesystemAndProtocolHistoryFailClosed(t *testing.T) {
	t.Parallel()

	t.Run("MCP absolute and traversal materialization destinations", func(t *testing.T) {
		t.Parallel()
		project := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside")
		operations := it10ContractOperations{}
		registry := mcp.BuildRegistryWithContractOperations(nil, mcp.WriteDeps{}, "", nil, mcp.NewDeps{}, mcp.ContractToolOperations{
			Publication: operations,
			Materialize: it10MaterializeOperation{project: project, snapshot: it10HistoricalSnapshot(t)},
			Check:       operations,
			Inspection:  operations,
		})
		tool, ok := registry.Get("a2a_contract")
		if !ok {
			t.Fatal("a2a_contract is not registered")
		}
		for _, destination := range []string{outside, "../outside", "vendor/../../outside"} {
			raw, err := json.Marshal(map[string]any{"action": "materialize", "ref": "XC-axon-orders@1.2.3", "to": destination})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := tool.Handler(t.Context(), raw); !errors.Is(err, space.ErrContractUnsafePath) {
				t.Fatalf("destination %q error=%v, want contained unsafe-path refusal", destination, err)
			}
		}
		if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("MCP destination escaped project root: stat=%v", err)
		}
	})

	t.Run("protocol history delete has no public authority", func(t *testing.T) {
		t.Parallel()
		funnel := space.NewWriteFunnel(nil, nil, "0.18.2")
		_, err := funnel.Submit(t.Context(), space.SubmitRequest{
			System: "axon", Verb: "contract-publish", ArtifactID: "XC-axon-orders",
			Repo: hostRepoForIT10(),
			Mutations: []space.Mutation{{
				Path: "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M7.yaml", Operation: space.MutationDelete,
				ExpectedBefore: &space.MutationPrecondition{Digest: "sha256:" + strings.Repeat("a", 64), Mode: "100644"},
			}},
		})
		if !errors.Is(err, space.ErrMutationInvalid) {
			t.Fatalf("protocol-history deletion error=%v, want closed delete-shape refusal", err)
		}
	})

}

func hostRepoForIT10() host.Repo {
	return host.Repo{Owner: "fixture", Name: "space"}
}

func it10HistoricalSnapshot(t *testing.T) space.HistoricalSnapshot {
	t.Helper()
	descriptorRaw := []byte("---\nschema: envelope/v2\nid: XC-axon-orders\nversion: 1.2.3\nschema_format: json-schema-2020-12\nartifacts:\n" +
		"  - {path: schema/order.json, role: schema, normative: true, media_type: application/schema+json}\n" +
		"  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.json}\n" +
		"  - {path: fixtures/invalid/order.json, role: invalid-fixture, normative: false, media_type: application/json, conforms_to: schema/order.json}\n---\nOrders contract.\n")
	descriptor, err := contract.ParseDescriptor(descriptorRaw)
	if err != nil {
		t.Fatal(err)
	}
	files := []space.ContractSnapshotFile{
		{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Mode: "100644", Raw: descriptorRaw},
		{Path: "fixtures/invalid/order.json", Kind: contract.CandidateRegular, Mode: "100644", Raw: []byte(`{}`)},
		{Path: "fixtures/valid/order.json", Kind: contract.CandidateRegular, Mode: "100644", Raw: []byte(`{"id":"1"}`)},
		{Path: "schema/order.json", Kind: contract.CandidateRegular, Mode: "100644", Raw: []byte(`{"type":"object","required":["id"]}`)},
	}
	core := contract.CandidateSnapshot{Descriptor: contract.CandidateFile{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Raw: descriptorRaw}}
	for _, file := range files[1:] {
		core.Files = append(core.Files, contract.CandidateFile{Path: file.Path, Kind: file.Kind, Raw: file.Raw})
	}
	set, issues := contract.ValidateCarriedSet(contract.ProfileContractSetV2, descriptor, core)
	if len(issues) != 0 {
		t.Fatalf("fixture carried set: %+v", issues)
	}
	eventID := "01K1A2B3C4D5E6F7G8H9J0K1M7"
	eventRaw := []byte(fmt.Sprintf("schema: event/v2\nevent: %s\nsubject: XC-axon-orders\ntransition: publish\nversion: 1.2.3\ndigest: %s\ndigest_profile: contract-set-v2\nactor:\n  system: axon\n", eventID, set.AggregateDigest))
	return space.HistoricalSnapshot{
		ContractID: "XC-axon-orders", Version: "1.2.3", CommitSHA: strings.Repeat("a", 40),
		DescriptorPath: "axon/provides/orders/contract.md", PublishEventPath: "axon/events/2026/" + eventID + ".yaml",
		PublishEventRaw: eventRaw, EventSchema: "event/v2", DigestProfile: contract.ProfileContractSetV2,
		PublishedDigest: set.AggregateDigest, AggregateVerification: space.ContractVerificationEventDigest,
		DescriptorVerification: space.ContractVerificationEventDigest, Descriptor: descriptor, CarriedSet: set, Files: files,
	}
}
