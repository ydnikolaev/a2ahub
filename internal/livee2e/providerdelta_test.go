package livee2e

import (
	"reflect"
	"testing"
)

func TestAddedSinceIgnoresDisappearances(t *testing.T) {
	t.Parallel()
	// The exact LE-OC-06 shape: the head branch of the pull request merged just
	// before the sample is pruned asynchronously by delete_branch_on_merge. A
	// write-free assertion must stay green through that.
	before := []string{"main", "a2a/alpha/submit/XC-1", "a2a/alpha/contract-publish/op-v1-abc"}
	after := []string{"main", "a2a/alpha/contract-publish/op-v1-abc"}
	if added := addedSince(before, after); len(added) != 0 {
		t.Fatalf("a pruned branch was reported as a write: %v", added)
	}
}

func TestAddedSinceReportsOnlyWhatAppeared(t *testing.T) {
	t.Parallel()
	before := []string{"main", "keep"}
	after := []string{"keep", "new-one", "main", "new-two"}
	if added := addedSince(before, after); !reflect.DeepEqual(added, []string{"new-one", "new-two"}) {
		t.Fatalf("added = %v, want the two appearances in after's order", added)
	}
	// Pull request numbers are the other caller, and a repeat must be counted
	// rather than absorbed by set membership.
	if added := addedSince([]int{7, 7, 9}, []int{7, 7, 7, 9}); !reflect.DeepEqual(added, []int{7}) {
		t.Fatalf("added = %v, want the one extra occurrence", added)
	}
	if added := addedSince([]string{}, []string{}); len(added) != 0 {
		t.Fatalf("added = %v, want empty for two empty samples", added)
	}
}
