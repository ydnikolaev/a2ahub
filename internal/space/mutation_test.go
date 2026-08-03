package space

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func TestContractSidecarDeletePermitIsInvocationLocalAndDigestBound(t *testing.T) {
	t.Parallel()

	funnel := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0")
	binding := testDeleteBinding("atlas/provides/demo/schema/old.json")
	mutation, err := funnel.mintContractSidecarDelete(binding)
	if err != nil {
		t.Fatalf("mintContractSidecarDelete: %v", err)
	}
	if err := funnel.validateMutationSet(binding.OperationKey, []Mutation{mutation}); err != nil {
		t.Fatalf("validate minted mutation: %v", err)
	}

	tests := []struct {
		name      string
		operation string
		mutate    func(*Mutation)
		funnel    *WriteFunnel
	}{
		{"different funnel", binding.OperationKey, func(*Mutation) {}, NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0")},
		{"different operation", "op-v1-" + strings.Repeat("b", 64), func(*Mutation) {}, funnel},
		{"different path", binding.OperationKey, func(m *Mutation) { m.Path = "atlas/provides/demo/schema/other.json" }, funnel},
		{"different root", binding.OperationKey, func(m *Mutation) {
			m.ContractRoot = "atlas/provides/other"
			m.Path = "atlas/provides/other/schema/old.json"
		}, funnel},
		{"different plan", binding.OperationKey, func(m *Mutation) { m.PlanDigest = "sha256:" + strings.Repeat("f", 64) }, funnel},
		{"different prior digest", binding.OperationKey, func(m *Mutation) { m.ExpectedBefore.Digest = "sha256:" + strings.Repeat("9", 64) }, funnel},
		{"different prior mode", binding.OperationKey, func(m *Mutation) { m.ExpectedBefore.Mode = "100755" }, funnel},
		{"missing permit", binding.OperationKey, func(m *Mutation) { m.deletePermit = nil }, funnel},
		{"corrupt permit", binding.OperationKey, func(m *Mutation) { m.deletePermit.mac[0] ^= 0xff }, funnel},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			candidate := cloneMutation(mutation)
			if candidate.deletePermit != nil {
				permit := *candidate.deletePermit
				candidate.deletePermit = &permit
			}
			tc.mutate(&candidate)
			if err := tc.funnel.validateMutationSet(tc.operation, []Mutation{candidate}); !errors.Is(err, ErrMutationUnauthorized) {
				t.Fatalf("validateMutationSet error = %v, want ErrMutationUnauthorized", err)
			}
		})
	}
}

func TestDeletePermitHasNoSerializedForm(t *testing.T) {
	t.Parallel()

	funnel := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0")
	binding := testDeleteBinding("atlas/provides/demo/fixtures/valid/old.json")
	mutation, err := funnel.mintContractSidecarDelete(binding)
	if err != nil {
		t.Fatalf("mintContractSidecarDelete: %v", err)
	}
	raw, err := json.Marshal(mutation)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(raw), "permit") || strings.Contains(string(raw), contractSidecarDeleteDomain) {
		t.Fatalf("serialized mutation leaked capability material: %s", raw)
	}
	var decoded Mutation
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if err := funnel.validateMutationSet(binding.OperationKey, []Mutation{decoded}); !errors.Is(err, ErrMutationUnauthorized) {
		t.Fatalf("decoded delete error = %v, want ErrMutationUnauthorized", err)
	}
}

func TestContractSidecarDeletePathMatrix(t *testing.T) {
	t.Parallel()

	funnel := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0")
	allowed := []string{
		"atlas/provides/demo/schema/old.json",
		"atlas/provides/demo/fixtures/valid/old.json",
		"atlas/provides/demo/artifacts/example.bin",
	}
	for _, path := range allowed {
		if _, err := funnel.mintContractSidecarDelete(testDeleteBinding(path)); err != nil {
			t.Errorf("allowed path %q: %v", path, err)
		}
	}
	for _, path := range []string{
		"atlas/provides/demo/contract.md",
		"atlas/events/2026/01K1.yaml",
		"atlas/exchanges/XQ-atlas-20260803-abcd.md",
		"atlas/consumes.yaml",
		"space.yaml",
		"atlas/provides/demo/schema",
		"atlas/provides/demo/schema/../contract.md",
		"atlas/provides/other/schema/old.json",
	} {
		binding := testDeleteBinding(path)
		if strings.Contains(path, "/other/") {
			binding.ContractRoot = "atlas/provides/demo"
		}
		if _, err := funnel.mintContractSidecarDelete(binding); !errors.Is(err, ErrMutationInvalid) {
			t.Errorf("forbidden path %q error = %v, want ErrMutationInvalid", path, err)
		}
	}
}

func TestNormalizeMutationsRejectsDuplicateAndMalformedWrites(t *testing.T) {
	t.Parallel()

	if _, err := normalizeMutations(
		[]FileWrite{{Path: "atlas/docs/x.md", Content: []byte("x")}},
		[]Mutation{{Path: "atlas/docs/x.md", Operation: MutationWrite, Bytes: []byte("y")}},
	); !errors.Is(err, ErrMutationDuplicatePath) {
		t.Fatalf("duplicate error = %v, want ErrMutationDuplicatePath", err)
	}
	funnel := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0")
	if err := funnel.validateMutationSet("", []Mutation{{Path: "atlas/docs/x.md", Operation: MutationWrite}}); !errors.Is(err, ErrMutationInvalid) {
		t.Fatalf("nil write bytes error = %v, want ErrMutationInvalid", err)
	}
	if err := funnel.validateMutationSet("", []Mutation{{Path: "atlas/docs/x.md", Operation: "remove", Bytes: []byte{}}}); !errors.Is(err, ErrMutationInvalid) {
		t.Fatalf("unknown operation error = %v, want ErrMutationInvalid", err)
	}
}

func TestFunnelDefaultDeniesDeleteBeforeExternalCalls(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "atlas")
	layout, err := NewLayout("atlas")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "atlas", layout)
	binding := testDeleteBinding("atlas/provides/demo/schema/old.json")
	req.Verb, req.OperationKey = "contract-publish", binding.OperationKey
	req.Mutations = []Mutation{{
		Path: binding.Path, Operation: MutationDelete,
		ExpectedBefore: &MutationPrecondition{Digest: binding.PriorDigest, Mode: binding.PriorMode},
		ContractRoot:   binding.ContractRoot, PlanDigest: binding.PlanDigest,
	}}
	fake := host.NewFakeHost()
	var reads atomic.Int32
	fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
		reads.Add(1)
		return nil, nil
	}

	result, err := NewWriteFunnel(fake, testNoSubmitValidation{}, "0.19.0").Submit(context.Background(), req)
	if !errors.Is(err, ErrMutationUnauthorized) {
		t.Fatalf("Submit error = %v, want ErrMutationUnauthorized", err)
	}
	if result.Stage != WriteStageNone || reads.Load() != 0 || len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("default-denied delete crossed a boundary: result=%+v reads=%d pushes=%d opens=%d",
			result, reads.Load(), len(fake.Pushes), len(fake.Opens))
	}
}

func TestFunnelAppliesAuthorizedMixedMutationsInOneCommit(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "atlas")
	layout, err := NewLayout("atlas")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	fake := host.NewFakeHost()
	fake.PushBranchFunc = func(ctx context.Context, req host.PushBranchRequest) (host.PushBranchResult, error) {
		if err := runGit(ctx, req.RepoDir, "push", req.RemoteURL, req.LocalRef+":refs/heads/"+req.Branch); err != nil {
			return host.PushBranchResult{}, err
		}
		if err := runGit(ctx, req.RepoDir, "fetch", "origin", "refs/heads/"+req.Branch+":refs/remotes/origin/"+req.Branch); err != nil {
			return host.PushBranchResult{}, err
		}
		return host.PushBranchResult{Branch: req.Branch}, nil
	}
	funnel := NewWriteFunnel(fake, testNoSubmitValidation{}, "0.19.0")

	sidecarPath := "atlas/provides/demo/schema/old.json"
	firstReq := newTestSubmitRequest(fx, "atlas", layout)
	firstReq.Files = append(firstReq.Files, FileWrite{Path: sidecarPath, Content: []byte("old\n")})
	first, err := funnel.Submit(context.Background(), firstReq)
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	binding := testDeleteBinding(sidecarPath)
	binding.PriorDigest = typedDigest([]byte("old\n"))
	deleteMutation, err := funnel.mintContractSidecarDelete(binding)
	if err != nil {
		t.Fatalf("mintContractSidecarDelete: %v", err)
	}
	secondReq := newTestSubmitRequest(fx, "atlas", layout)
	secondReq.Verb, secondReq.ArtifactID = "contract-publish", "XC-atlas-demo"
	secondReq.ArtifactIDs = []string{"XC-atlas-demo"}
	secondReq.OperationKey, secondReq.BaseBranch = binding.OperationKey, first.Branch
	secondReq.Files = []FileWrite{{
		Path:    layout.EventFile("2026", "01K1A2B3C4D5E6F7G8H9J0K1M2"),
		Content: []byte("event: contract-publish\n"),
	}}
	secondReq.Mutations = []Mutation{
		{Path: "atlas/provides/demo/schema/new.json", Operation: MutationWrite, Bytes: []byte("new\n")},
		deleteMutation,
	}
	second, err := funnel.Submit(context.Background(), secondReq)
	if err != nil {
		t.Fatalf("second Submit: %v", err)
	}
	if second.CommitSHA == "" || len(fake.Pushes) != 2 || len(fake.Opens) != 2 {
		t.Fatalf("mixed mutation result/calls = %+v pushes=%d opens=%d", second, len(fake.Pushes), len(fake.Opens))
	}
	if _, err := runGitOutput(context.Background(), secondReq.RepoDir, nil, "rev-parse", second.Branch+":"+sidecarPath); err == nil {
		t.Fatalf("deleted sidecar %s still exists on %s", sidecarPath, second.Branch)
	}
	newRaw, err := runGitOutput(context.Background(), secondReq.RepoDir, nil, "show", second.Branch+":atlas/provides/demo/schema/new.json")
	if err != nil || newRaw != "new" {
		t.Fatalf("new sidecar = %q, %v; want new", newRaw, err)
	}
	count, err := runGitOutput(context.Background(), secondReq.RepoDir, nil, "rev-list", "--count", first.Branch+".."+second.Branch)
	if err != nil || count != "1" {
		t.Fatalf("mixed mutation commits ahead = %q, %v; want exactly 1", count, err)
	}
	fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
		return host.CheckStatusResult{State: "queued", HeadSHA: "checked-head"}, nil
	}
	retried, err := funnel.Submit(context.Background(), secondReq)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if retried.PRNumber != second.PRNumber || len(fake.Pushes) != 2 || len(fake.Opens) != 2 {
		t.Fatalf("retry duplicated mixed mutation: result=%+v pushes=%d opens=%d", retried, len(fake.Pushes), len(fake.Opens))
	}
}

func TestFunnelRefusesChangedDeletePreconditionBeforePush(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "atlas")
	layout, err := NewLayout("atlas")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	fake := host.NewFakeHost()
	fake.PushBranchFunc = func(ctx context.Context, req host.PushBranchRequest) (host.PushBranchResult, error) {
		if err := runGit(ctx, req.RepoDir, "push", req.RemoteURL, req.LocalRef+":refs/heads/"+req.Branch); err != nil {
			return host.PushBranchResult{}, err
		}
		if err := runGit(ctx, req.RepoDir, "fetch", "origin", "refs/heads/"+req.Branch+":refs/remotes/origin/"+req.Branch); err != nil {
			return host.PushBranchResult{}, err
		}
		return host.PushBranchResult{Branch: req.Branch}, nil
	}
	funnel := NewWriteFunnel(fake, testNoSubmitValidation{}, "0.19.0")
	sidecarPath := "atlas/provides/demo/schema/old.json"
	firstReq := newTestSubmitRequest(fx, "atlas", layout)
	firstReq.Files = append(firstReq.Files, FileWrite{Path: sidecarPath, Content: []byte("actual\n")})
	first, err := funnel.Submit(context.Background(), firstReq)
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	binding := testDeleteBinding(sidecarPath)
	binding.PriorDigest = typedDigest([]byte("different\n"))
	deleteMutation, err := funnel.mintContractSidecarDelete(binding)
	if err != nil {
		t.Fatalf("mintContractSidecarDelete: %v", err)
	}
	deleteReq := newTestSubmitRequest(fx, "atlas", layout)
	deleteReq.Verb, deleteReq.ArtifactID = "contract-publish", "XC-atlas-demo"
	deleteReq.OperationKey, deleteReq.BaseBranch = binding.OperationKey, first.Branch
	deleteReq.Mutations = []Mutation{deleteMutation}
	_, err = funnel.Submit(context.Background(), deleteReq)
	if !errors.Is(err, ErrMutationPrecondition) {
		t.Fatalf("Submit error = %v, want ErrMutationPrecondition", err)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("changed precondition crossed remote boundary: pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
}

func testDeleteBinding(path string) contractSidecarDeleteBinding {
	return contractSidecarDeleteBinding{
		OperationKey: "op-v1-" + strings.Repeat("a", 64),
		PlanDigest:   "sha256:" + strings.Repeat("d", 64),
		ContractRoot: "atlas/provides/demo",
		Path:         path,
		PriorDigest:  "sha256:" + strings.Repeat("e", 64),
		PriorMode:    "100644",
	}
}
