package operational

import (
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
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

func TestSemanticRevisionTracksCoreCurrentBoundary(t *testing.T) {
	t.Parallel()
	boundary := time.Date(2026, 8, 3, 13, 0, 0, 0, time.UTC)
	input := baseInput(boundary)
	input.CommittedWork = []CommittedWorkEvidence{checkpoint(
		"work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "session-a", workreport.ModeTesting,
		boundary.Add(-time.Hour), boundary, 1,
	)}

	current, err := Build(input, fixedClock{boundary}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	stale, err := Build(input, fixedClock{boundary.Add(time.Nanosecond)}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !current.Timeline[0].Work[0].Current || stale.Timeline[0].Work[0].Current {
		t.Fatalf("current boundary = %v -> %v", current.Timeline[0].Work[0].Current, stale.Timeline[0].Work[0].Current)
	}
	if current.Revision == stale.Revision {
		t.Fatal("current boundary did not change semantic revision")
	}
}

func TestSemanticRevisionIncludesCurrentWireFact(t *testing.T) {
	t.Parallel()
	snapshot := Snapshot{
		SchemaVersion: SchemaVersion,
		Sources:       []Source{},
		Timeline: []TimelineRow{{
			Space: "space-a", Thread: "thread:one", Participants: []string{},
			Protocol: Protocol{WaitingOn: []string{}, BlockingBy: []string{}},
			Work: []Work{{
				WorkID: "work:01ARZ3NDEKTSV4RRFFQ69G5FAV", SubjectRef: "XC-one@1.0.0",
				Freshness: FreshnessLocalCurrent, Current: false, WaitingOn: []workreport.WaitingOn{},
			}},
			Consistency: []Consistency{},
		}},
		Unavailable: []Unavailable{},
	}
	withoutCurrent, err := semanticRevision(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Timeline[0].Work[0].Current = true
	withCurrent, err := semanticRevision(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if withoutCurrent == withCurrent {
		t.Fatal("current wire fact is absent from semantic revision")
	}
}
