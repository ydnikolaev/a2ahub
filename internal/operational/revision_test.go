package operational

import (
	"strings"
	"testing"
	"time"
)

func TestSemanticRevisionIncludesSourceStateButExcludesPollObservation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	input := baseInput(now)
	first, err := Build(input, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(first) error = %v", err)
	}
	input.Sources[0].ObservedAt = now.Add(time.Hour)
	second, err := Build(input, fixedClock{now.Add(time.Hour)}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(second) error = %v", err)
	}
	if first.Revision != second.Revision {
		t.Fatalf("observation changed revision: %s != %s", first.Revision, second.Revision)
	}
	input.Sources[0].Freshness = SourceStale
	third, err := Build(input, fixedClock{now.Add(time.Hour)}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(third) error = %v", err)
	}
	if third.Revision == second.Revision {
		t.Fatal("source freshness did not change revision")
	}
	if !strings.HasPrefix(third.Revision, "sha256:") || len(third.Revision) != len("sha256:")+64 {
		t.Fatalf("revision = %q", third.Revision)
	}
}

func TestSemanticRevisionExcludesLocalLeasePollObservation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	lease := validLease("work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "safe-session", now)
	input := baseInput(now)
	input.LocalLeases = []LocalLeaseEvidence{{Lease: lease, ObservedAt: now}}
	first, err := Build(input, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(first) error = %v", err)
	}
	input.LocalLeases[0].ObservedAt = now.Add(time.Minute)
	second, err := Build(input, fixedClock{now.Add(time.Minute)}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(second) error = %v", err)
	}
	if first.Revision != second.Revision {
		t.Fatalf("lease poll observation changed revision: %s != %s", first.Revision, second.Revision)
	}
	if first.Timeline[0].Work[0].ObservedAt.Equal(*second.Timeline[0].Work[0].ObservedAt) {
		t.Fatal("test did not vary the response-only observation timestamp")
	}
}
