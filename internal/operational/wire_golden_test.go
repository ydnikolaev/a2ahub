package operational

import (
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

func TestSnapshotV1CompleteWireGolden(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	validUntil := at.Add(time.Hour)
	syncedAt := at.Add(-time.Minute)
	snapshot := Snapshot{
		SchemaVersion: 1, GeneratedAt: at, Revision: "sha256:" + strings.Repeat("a", 64),
		Sources: []Source{
			{Kind: SourceSpace, Space: "space-a", Revision: "commit-a", SyncedAt: &syncedAt, ObservedAt: at, Freshness: SourceCurrent},
			{Kind: SourceLocalWork, Revision: "sha256:" + strings.Repeat("b", 64), ObservedAt: at, Freshness: SourceDegraded},
		},
		Timeline: []TimelineRow{{
			Space: "space-a", Thread: "thread:one", Title: "Process", Participants: []string{"atlas", "checkout"},
			Protocol:        Protocol{Settled: false, OpenCount: 1, WaitingOn: []string{"atlas"}, YourMove: true, BlockingBy: []string{"review"}},
			LatestMilestone: &Milestone{Kind: "event", At: syncedAt, Actor: Actor{Kind: "agent", Name: "codex", System: "atlas", Model: "gpt", Session: "session-a"}, Transition: "respond", Subject: "XW-one"},
			Work: []Work{{
				WorkID: "work:01ARZ3NDEKTSV4RRFFQ69G5FAV", SubjectRef: "XC-one@2.0.0", Mode: workreport.ModeTesting,
				Summary: "Running tests", Actor: Actor{Kind: "agent", Name: "codex", System: "atlas", Model: "gpt", Session: "session-a"},
				Freshness: FreshnessLocalCurrent, ReportedAt: &syncedAt, ObservedAt: &at, ValidUntil: &validUntil, Source: "local-lease",
				CommittedCheckpoint: &CommittedCheckpoint{ArtifactID: "XA-one", ReportedAt: syncedAt, ValidUntil: &validUntil},
				SharedPending:       &SharedPending{OperationID: "op-v1-one", Action: "checkpoint", Stage: "pushed", State: "outcome-unknown", RemainingAction: "observe-provider-outcome"},
				WaitingOn:           []workreport.WaitingOn{},
			}},
			WorkWindow:        Window{Total: 1, Shown: 1},
			Consistency:       []Consistency{{Code: "future-clock-anomaly", SubjectRef: "XC-one@2.0.0", Severity: "warning", Summary: "Clock differs"}},
			ConsistencyWindow: Window{Total: 1, Shown: 1},
		}},
		TimelineWindow: Window{Total: 1, Shown: 1},
		Unavailable:    []Unavailable{{SourceKind: SourceSpace, Space: "space-b", Code: "mirror-missing", Summary: "Mirror unavailable"}},
	}
	raw, err := CanonicalJSON(snapshot)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	want := `{"schema_version":1,"generated_at":"2026-08-03T12:00:00Z","revision":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sources":[{"kind":"space","space":"space-a","revision":"commit-a","synced_at":"2026-08-03T11:59:00Z","observed_at":"2026-08-03T12:00:00Z","freshness":"current"},{"kind":"local-work","revision":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","observed_at":"2026-08-03T12:00:00Z","freshness":"degraded"}],"timeline":[{"space":"space-a","thread":"thread:one","title":"Process","participants":["atlas","checkout"],"protocol":{"settled":false,"open_count":1,"waiting_on":["atlas"],"your_move":true,"blocking_by":["review"]},"latest_milestone":{"kind":"event","at":"2026-08-03T11:59:00Z","actor":{"kind":"agent","name":"codex","system":"atlas","model":"gpt","session":"session-a"},"transition":"respond","subject":"XW-one"},"work":[{"work_id":"work:01ARZ3NDEKTSV4RRFFQ69G5FAV","subject_ref":"XC-one@2.0.0","mode":"testing","summary":"Running tests","actor":{"kind":"agent","name":"codex","system":"atlas","model":"gpt","session":"session-a"},"freshness":"local-current","reported_at":"2026-08-03T11:59:00Z","observed_at":"2026-08-03T12:00:00Z","valid_until":"2026-08-03T13:00:00Z","source":"local-lease","committed_checkpoint":{"artifact_id":"XA-one","reported_at":"2026-08-03T11:59:00Z","valid_until":"2026-08-03T13:00:00Z"},"shared_pending":{"operation_id":"op-v1-one","action":"checkpoint","stage":"pushed","state":"outcome-unknown","remaining_action":"observe-provider-outcome"},"waiting_on":[]}],"work_window":{"truncated":false,"total":1,"shown":1},"consistency":[{"code":"future-clock-anomaly","subject_ref":"XC-one@2.0.0","severity":"warning","summary":"Clock differs"}],"consistency_window":{"truncated":false,"total":1,"shown":1}}],"timeline_window":{"truncated":false,"total":1,"shown":1},"unavailable":[{"source_kind":"space","space":"space-b","code":"mirror-missing","summary":"Mirror unavailable"}]}`
	if string(raw) != want {
		t.Fatalf("snapshot v1 wire changed\n got: %s\nwant: %s", raw, want)
	}
}
