package space

import (
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

func sidecarDeletePlan(digest, root string, paths ...string) contract.PublicationPlan {
	plan := contract.PublicationPlan{PlanDigest: digest}
	for _, p := range paths {
		plan.Mutations = append(plan.Mutations, contract.PublicationMutation{
			Action: contract.MutationDelete, Path: p, BeforeMode: "100644",
			DeleteDomain: contract.ContractSidecarDeleteDomain, ContractRoot: root,
		})
	}
	return plan
}

// A delete with no digest given is not a plan that changed: it is a plan that
// has not been confirmed yet, and the refusal says what to run.
func TestPublicationMutations_DeleteWithoutExpectPlanNamesTheDigest(t *testing.T) {
	t.Parallel()
	const digest, root = "sha256:plan", "axon/provides/export"
	service := &ContractPublicationService{}
	planning := ContractPublicationPlanningContext{ContractRoot: root}

	_, err := service.publicationMutations(sidecarDeletePlan(digest, root, "artifacts/a.md", "artifacts/b.md"), planning, "")
	if !errors.Is(err, ErrContractPublicationPlanChanged) {
		t.Fatalf("error = %v, want it to stay ErrContractPublicationPlanChanged", err)
	}
	for _, want := range []string{"deletes 2 sidecar file(s)", "--expect-plan " + digest} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to say %q", err, want)
		}
	}
}

// A real mismatch keeps the plain error: a digest was given and is wrong, so
// advising to re-run with this plan's digest would be advice to skip the check.
func TestPublicationMutations_RealPlanChangeKeepsThePlainError(t *testing.T) {
	t.Parallel()
	const digest, root = "sha256:plan", "axon/provides/export"
	service := &ContractPublicationService{}
	planning := ContractPublicationPlanningContext{ContractRoot: root}

	for name, tc := range map[string]struct {
		plan   contract.PublicationPlan
		expect string
	}{
		"a different digest":          {sidecarDeletePlan(digest, root, "artifacts/a.md"), "sha256:other"},
		"no digest over a stray root": {sidecarDeletePlan(digest, "beta/provides/export", "artifacts/a.md"), ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := service.publicationMutations(tc.plan, planning, tc.expect)
			if !errors.Is(err, ErrContractPublicationPlanChanged) || err.Error() != ErrContractPublicationPlanChanged.Error() {
				t.Fatalf("error = %v, want exactly the plain plan-changed", err)
			}
		})
	}
}
