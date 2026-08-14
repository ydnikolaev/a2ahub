package html

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/operational"
)

func TestCanonicalViewModelJSONIsCompactScriptSafeAndMeasuredLikeForLike(t *testing.T) {
	t.Parallel()

	data, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}
	data.Inbox[0].Title = `</script><img src=x onerror=alert(1)> &`
	before, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal input before encoding: %v", err)
	}
	compact, err := CanonicalViewModelJSON(data)
	if err != nil {
		t.Fatalf("CanonicalViewModelJSON: %v", err)
	}
	indented, err := MarshalData(data)
	if err != nil {
		t.Fatalf("MarshalData: %v", err)
	}
	after, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal input after encoding: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("canonical encoding mutated Data")
	}
	if !json.Valid(compact) || bytes.Contains(compact, []byte("\n  \"")) {
		t.Fatalf("canonical view model is not compact valid JSON: %q", compact)
	}
	if bytes.Contains(compact, []byte("</script>")) || !bytes.Contains(compact, []byte(`\u003c/script\u003e`)) ||
		!bytes.Contains(compact, []byte(`\u003e`)) || !bytes.Contains(compact, []byte(`\u0026`)) {
		t.Fatalf("canonical view model did not retain default HTML escaping: %s", compact)
	}
	if len(indented) <= len(compact) {
		t.Fatalf("same-value indented bytes = %d, compact bytes = %d", len(indented), len(compact))
	}
	t.Logf("view_model_lengths compact=%d indented=%d saving=%d", len(compact), len(indented), len(indented)-len(compact))
}

func TestCanonicalViewModelJSONEmptyData(t *testing.T) {
	t.Parallel()
	got, err := CanonicalViewModelJSON(Data{})
	if err != nil {
		t.Fatalf("CanonicalViewModelJSON: %v", err)
	}
	if !json.Valid(got) || bytes.Contains(got, []byte("\n")) {
		t.Fatalf("empty Data encoding = %q", got)
	}
}

func TestContentFingerprintExcludesOnlyObservationFieldsWithoutMutation(t *testing.T) {
	t.Parallel()

	base := fingerprintFixture()
	before, err := CanonicalViewModelJSON(base)
	if err != nil {
		t.Fatalf("encode input before fingerprint: %v", err)
	}
	want, err := ContentFingerprint(base)
	if err != nil {
		t.Fatalf("ContentFingerprint: %v", err)
	}
	exclusions := []struct {
		name   string
		mutate func(*Data)
	}{
		{"operational", func(data *Data) { data.Operational.Revision = "sha256:changed" }},
		{"generated_at", func(data *Data) { data.GeneratedAt = data.GeneratedAt.Add(time.Hour) }},
		{"tooling_cache_age", func(data *Data) { data.Tooling.CacheAge = "8m" }},
		{"space_sync_age", func(data *Data) { data.Spaces[0].SyncAge = "9m" }},
		{"thread_last_activity", func(data *Data) { data.Threads[0].LastActivity = "10m" }},
		{"inbox_age", func(data *Data) { data.Inbox[0].Age = "11m" }},
		{"outbox_age", func(data *Data) { data.Outbox[0].Age = "12m" }},
		{"archive_age", func(data *Data) { data.Archive[0].Age = "13m" }},
		{"need_you_age", func(data *Data) { data.Aggregates.NeedYou.Items[0].Age = "13m" }},
		{"no_human_move_age", func(data *Data) { data.Aggregates.NoHumanMove.Items[0].Age = "13m" }},
		{"you_await_age", func(data *Data) { data.Aggregates.YouAwait.Items[0].Age = "13m" }},
		{"exchange_max_stale", func(data *Data) { data.ExchangeEdges[0].MaxStale = "14m" }},
		{"artifact_detail_sync_age", func(data *Data) { data.ArtifactDetails[0].SyncAge = "15m" }},
	}
	for _, test := range exclusions {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := cloneFingerprintData(t, base)
			test.mutate(&candidate)
			got, fingerprintErr := ContentFingerprint(candidate)
			if fingerprintErr != nil {
				t.Fatalf("ContentFingerprint: %v", fingerprintErr)
			}
			if got != want {
				t.Fatalf("observation-only fingerprint = %s, want %s", got, want)
			}
		})
	}
	after, err := CanonicalViewModelJSON(base)
	if err != nil {
		t.Fatalf("encode input after fingerprint: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("ContentFingerprint mutated its Data input")
	}
}

func TestContentFingerprintRetainsSemanticFieldsByDefault(t *testing.T) {
	t.Parallel()

	base := fingerprintFixture()
	want, err := ContentFingerprint(base)
	if err != nil {
		t.Fatalf("ContentFingerprint: %v", err)
	}
	inclusions := []struct {
		name   string
		mutate func(*Data)
	}{
		{"tooling_checked_at", func(data *Data) { data.Tooling.CheckedAt = data.Tooling.CheckedAt.Add(time.Hour) }},
		{"tooling_fresh", func(data *Data) { data.Tooling.Fresh = !data.Tooling.Fresh }},
		{"space_stale", func(data *Data) { data.Spaces[0].Stale = !data.Spaces[0].Stale }},
		{"space_revision", func(data *Data) { data.Spaces[0].Revision = "def" }},
		{"space_readable", func(data *Data) { data.Spaces[0].Readable = !data.Spaces[0].Readable }},
		{"item_reasons", func(data *Data) { data.Inbox[0].Reasons = []string{"overdue"} }},
		{"item_severity", func(data *Data) { data.Inbox[0].Severity = "blocking" }},
		{"item_sync_stale", func(data *Data) { data.Inbox[0].SyncStale = true }},
		{"item_state_since", func(data *Data) { data.Inbox[0].StateSince = data.Inbox[0].StateSince.Add(time.Hour) }},
		{"aggregate_window", func(data *Data) { data.Aggregates.NeedYou.Window.Total++ }},
		{"vocabulary_label", func(data *Data) { data.Vocabulary.Entries[0].LabelEN = "changed" }},
		{"thread_window", func(data *Data) { data.Threads[0].Windows.Members.Total++ }},
		{"dependency_drift", func(data *Data) { data.ContractEdges[0].Drift = "behind" }},
		{"attachment_lapse", func(data *Data) { data.ArtifactDetails[0].Attachments[0].Lapsed = true }},
		{"degradation", func(data *Data) { data.Unavailable[0].Reason = "changed" }},
		{"non_excluded_default", func(data *Data) { data.Meta.Notice = "a newly carried semantic field" }},
	}
	for _, test := range inclusions {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := cloneFingerprintData(t, base)
			test.mutate(&candidate)
			got, fingerprintErr := ContentFingerprint(candidate)
			if fingerprintErr != nil {
				t.Fatalf("ContentFingerprint: %v", fingerprintErr)
			}
			if got == want {
				t.Fatalf("semantic fingerprint = %s, want a value different from %s", got, want)
			}
		})
	}
}

func fingerprintFixture() Data {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	return Data{
		Meta: DemoMeta{Schema: "dashboard-demo/v1", Notice: "baseline"}, GeneratedAt: now, Self: "atlas",
		Tooling:       Tooling{CheckedAt: now, CacheAge: "1m", Fresh: true},
		Spaces:        []SpaceHealth{{ID: "space-a", SyncAge: "2m", Revision: "abc", Readable: true}},
		ContractEdges: []ContractEdge{{From: "atlas", To: "beta", Drift: "current"}},
		ExchangeEdges: []ExchangeEdge{{From: "atlas", To: "beta", MaxStale: "3m", Count: 1}},
		Threads: []Thread{{
			ID: "thread-1", LastActivity: "4m", OpenCount: 1,
			Windows: ThreadWindows{Members: operational.Window{Total: 1, Shown: 1}},
		}},
		Inbox:  []Item{{ID: "in", Age: "5m", Severity: "normal", StateSince: now}},
		Outbox: []Item{{ID: "out", Age: "6m"}}, Archive: []Item{{ID: "archive", Age: "7m"}},
		Aggregates: DashboardAggregates{
			NeedYou:     DashboardItemSet{Items: []Item{{ID: "need", Age: "5m"}}, Window: operational.Window{Total: 1, Shown: 1}},
			NoHumanMove: DashboardItemSet{Items: []Item{{ID: "idle", Age: "6m"}}, Window: operational.Window{Total: 1, Shown: 1}},
			YouAwait:    DashboardItemSet{Items: []Item{{ID: "await", Age: "7m"}}, Window: operational.Window{Total: 1, Shown: 1}},
		},
		Vocabulary: VocabularyTable{Entries: []VocabularyEntry{{
			Family: "freshness", Value: "current", LabelEN: "Current", LabelRU: "Актуально",
		}}},
		ArtifactDetails: []ArtifactDetail{{
			ID: "artifact", SyncAge: "8m", Digest: "sha256:artifact",
			Attachments: []datapackage.AttachmentClaim{{VerificationClaim: "digest verified"}},
		}},
		Unavailable: []UnavailableFact{{ID: "bounded", Scope: "dashboard", Title: "Bounded", Reason: "baseline"}},
		Operational: operational.Snapshot{
			SchemaVersion: operational.SchemaVersion, GeneratedAt: now, Revision: "sha256:operational",
		},
	}
}

func cloneFingerprintData(t *testing.T, data Data) Data {
	t.Helper()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal fingerprint fixture: %v", err)
	}
	var clone Data
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatalf("unmarshal fingerprint fixture: %v", err)
	}
	return clone
}

func TestCanonicalViewModelJSONDoesNotEmitRawScriptText(t *testing.T) {
	t.Parallel()
	encoded, err := CanonicalViewModelJSON(Data{Self: `<>&`})
	if err != nil {
		t.Fatalf("CanonicalViewModelJSON: %v", err)
	}
	if strings.Contains(string(encoded), `<`) || strings.Contains(string(encoded), `>`) || strings.Contains(string(encoded), `&`) {
		t.Fatalf("raw script-significant text leaked: %s", encoded)
	}
}
