package html

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

func assertMappedField[T comparable](t *testing.T, mapper, field string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s dropped source field %s: got %#v, want %#v", mapper, field, got, want)
	}
}

func TestViewModelMappingSingleProducerCalls(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("assemble.go")
	if err != nil {
		t.Fatalf("read assemble.go: %v", err)
	}
	source := string(raw)
	for call, want := range map[string]int{
		"store.ContractDependencies(ctx)":         1,
		"store.DashboardSnapshot(ctx)":            1,
		"cache.BuildDashboardAggregateSets(":      1,
		"cache.OrderAttention(":                   1,
		"store.ShowMany(ctx, admittedDetailRefs)": 1,
		"item := toItem(source, now, self, open)": 1,
		"ReasonSentence: attentionSentence(it)":   1,
	} {
		if got := strings.Count(source, call); got != want {
			t.Errorf("assemble.go production call %q count = %d, want %d", call, got, want)
		}
	}
	for _, forbidden := range []string{"providerOf", "dependencyFacts", "ParseConsumes", "consumes.yaml"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("assemble.go still contains duplicate dependency truth %q", forbidden)
		}
	}
}

func TestDashboardAdmissionEveryFamily(t *testing.T) {
	t.Parallel()

	families := []struct {
		name  string
		limit int
	}{
		{"inbox", maximumDashboardInboxItems}, {"outbox", maximumDashboardOutboxItems}, {"archive", maximumDashboardArchiveItems},
		{"threads", maximumDashboardThreads}, {"thread artifacts", maximumDashboardThreadArtifacts},
		{"thread transcript", maximumDashboardThreadTranscriptRows}, {"thread open items", maximumDashboardThreadOpenItems},
		{"thread links", maximumDashboardThreadLinks}, {"thread flags", maximumDashboardThreadFlags},
		{"thread unresolved", maximumDashboardThreadUnresolved}, {"thread deliveries", maximumDashboardThreadDeliveries},
		{"artifact details", maximumDashboardArtifactDetails}, {"work reports", maximumDashboardWorkReports},
		{"contract edges", maximumDashboardContractEdges}, {"exchange edges", maximumDashboardExchangeEdges},
		{"flags", maximumDashboardFlags}, {"aggregate items", maximumDashboardAggregateItems},
		{"aggregate contract edges", maximumDashboardAggregateContractEdges},
	}
	wantLimits := []int{64, 64, 64, 8, 16, 32, 16, 64, 16, 16, 8, 32, 64, 128, 128, 64, 64, 128}
	for index, family := range families {
		if family.limit != wantLimits[index] {
			t.Errorf("%s cap = %d, want exact P7 cap %d", family.name, family.limit, wantLimits[index])
		}
		t.Run(family.name, func(t *testing.T) {
			t.Parallel()
			for _, size := range []int{family.limit, family.limit + 1} {
				source := make([]int, size)
				for i := range source {
					source[i] = i
				}
				original := append([]int(nil), source...)
				prefix, window := admitPrefix(source, family.limit)
				wantWindow := struct {
					total, shown int
					truncated    bool
				}{total: size, shown: family.limit, truncated: size > family.limit}
				if window.Total != wantWindow.total || window.Shown != wantWindow.shown || window.Truncated != wantWindow.truncated {
					t.Errorf("%s size %d window = %+v, want total=%d shown=%d truncated=%t", family.name, size, window, wantWindow.total, wantWindow.shown, wantWindow.truncated)
				}
				if !slices.Equal(prefix, source[:family.limit]) {
					t.Errorf("%s size %d prefix is not the stable source prefix", family.name, size)
				}
				if !slices.Equal(source, original) {
					t.Errorf("%s size %d admission mutated source", family.name, size)
				}
				if len(prefix) > 0 {
					prefix[0] = -1
					if source[0] == -1 {
						t.Errorf("%s size %d admission reused source backing storage", family.name, size)
					}
				}
				second, _ := admitPrefix(source[:family.limit], family.limit)
				if !slices.Equal(second, source[:family.limit]) {
					t.Errorf("%s size %d changed under a second admission", family.name, size)
				}
			}
		})
	}
}

func TestDashboardAdmissionNestedAndPairedWindows(t *testing.T) {
	t.Parallel()

	threads := make([]Thread, maximumDashboardThreads+1)
	views := make([]ThreadView, maximumDashboardThreads+1)
	for index := range threads {
		spaceID := "space"
		threadID := "thread:" + string(rune('a'+index))
		threads[index] = Thread{
			Space: spaceID, ID: threadID,
			Members: make([]ThreadMember, maximumDashboardThreadArtifacts+1),
			Links:   make([]DocLink, maximumDashboardThreadLinks+1),
		}
		views[index] = ThreadView{
			Space: spaceID, Thread: threadID,
			Artifacts:  make([]ThreadViewArtifact, maximumDashboardThreadArtifacts+1),
			Transcript: make([]ThreadTranscriptRow, maximumDashboardThreadTranscriptRows+1),
			OpenItems:  make([]ThreadOpenItem, maximumDashboardThreadOpenItems+1),
			Flags:      make([]ThreadViewFlag, maximumDashboardThreadFlags+1),
			Unresolved: make([]ThreadUnresolvedRef, maximumDashboardThreadUnresolved+1),
			Deliveries: make([]Delivery, maximumDashboardThreadDeliveries+1),
		}
	}
	admitThreadChildren(threads, views)
	for index := range threads {
		if got := threads[index].Windows.Members; got != (operational.Window{Total: 17, Shown: 16, Truncated: true}) {
			t.Fatalf("thread[%d] member window = %+v", index, got)
		}
		if got := threads[index].Windows.Links; got != (operational.Window{Total: 65, Shown: 64, Truncated: true}) {
			t.Fatalf("thread[%d] link window = %+v", index, got)
		}
		want := ThreadViewWindows{
			Artifacts: operational.Window{Total: 17, Shown: 16, Truncated: true}, Transcript: operational.Window{Total: 33, Shown: 32, Truncated: true},
			OpenItems: operational.Window{Total: 17, Shown: 16, Truncated: true}, Flags: operational.Window{Total: 17, Shown: 16, Truncated: true},
			Unresolved: operational.Window{Total: 17, Shown: 16, Truncated: true}, Deliveries: operational.Window{Total: 9, Shown: 8, Truncated: true},
		}
		if !reflect.DeepEqual(views[index].Windows, want) {
			t.Fatalf("threadViews[%d] windows = %+v, want %+v", index, views[index].Windows, want)
		}
	}
	threadPrefix, viewPrefix, window, err := admitThreadPairs(threads, views, maximumDashboardThreads)
	if err != nil {
		t.Fatalf("admitThreadPairs: %v", err)
	}
	if window != (operational.Window{Total: 9, Shown: 8, Truncated: true}) {
		t.Fatalf("paired thread window = %+v", window)
	}
	for index := range threadPrefix {
		if threadPrefix[index].Space != viewPrefix[index].Space || threadPrefix[index].ID != viewPrefix[index].Thread {
			t.Fatalf("paired row %d identities differ", index)
		}
	}
}

func TestContractDependencyMappingFieldsAndWindows(t *testing.T) {
	t.Parallel()

	source := cache.ContractDependencyEdge{
		From: "checkout", To: "atlas", Space: "space-sentinel", Contract: "XC-atlas-api",
		PinnedMajor: 1, PinnedVersion: "1.9.0", PinnedState: "deprecated", ProviderVersion: "2.1.0", State: "published",
		AvailableMajors: []int{1, 2}, Drift: cache.DependencyDriftDeprecated, Sunset: "2026-12-31",
		Successor: "XC-atlas-api@2.0.0", Description: "description-sentinel",
	}
	got := mapContractDependencyEdges([]cache.ContractDependencyEdge{source})
	want := []ContractEdge{{
		From: source.From, To: source.To, Space: source.Space, Contract: source.Contract,
		PinnedMajor: source.PinnedMajor, PinnedVersion: source.PinnedVersion, PinnedState: source.PinnedState,
		ProviderVersion: source.ProviderVersion, State: source.State, AvailableMajors: []int{1, 2}, Drift: source.Drift,
		Sunset: source.Sunset, Successor: source.Successor, Description: source.Description,
	}}
	assertMappedValue(t, "mapContractDependencyEdges", "ContractDependencyEdge", got, want)
	got[0].AvailableMajors[0] = 99
	if source.AvailableMajors[0] == 99 {
		t.Fatal("mapContractDependencyEdges reused source AvailableMajors backing storage")
	}

	degradation := cache.ContractDependencyDegradation{
		Space: "space-sentinel", System: "checkout", Path: "checkout/consumes.yaml",
		Code: cache.DependencyDegradationMalformedRegistry, Reason: "reason-sentinel",
	}
	assertMappedValue(t, "mapContractDependencyDegradations", "ContractDependencyDegradation",
		mapContractDependencyDegradations([]cache.ContractDependencyDegradation{degradation}),
		[]DependencyDegradation{{Space: degradation.Space, System: degradation.System, Path: degradation.Path, Code: degradation.Code, Reason: degradation.Reason}})

	edges := make([]ContractEdge, maximumDashboardContractEdges+1)
	root, rootWindow := admitPrefix(edges, maximumDashboardContractEdges)
	aggregate, aggregateWindow := admitPrefix(edges, maximumDashboardAggregateContractEdges)
	if len(root) != 128 || rootWindow != (operational.Window{Total: 129, Shown: 128, Truncated: true}) {
		t.Fatalf("root dependency window = %+v with %d items", rootWindow, len(root))
	}
	if len(aggregate) != 128 || aggregateWindow != (operational.Window{Total: 129, Shown: 128, Truncated: true}) {
		t.Fatalf("aggregate dependency window = %+v with %d items", aggregateWindow, len(aggregate))
	}
}

func TestDashboardAdmissionAggregateOrdering(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	inbox := []cache.Item{
		{Space: "space", ID: "blocking", Blocking: true},
		{Space: "space", ID: "first-no-human"},
		{Space: "space", ID: "your-move", YourMove: true, HumanGate: "G3"},
		{Space: "space", ID: "second-no-human"},
	}
	outbox := []cache.Item{
		{Space: "space", ID: "first-await", WaitingOn: []string{"checkout"}},
		{Space: "space", ID: "second-await", WaitingOn: []string{"checkout"}},
	}
	dependencies := cache.ContractDependencyProjection{Complete: true, Edges: []cache.ContractDependencyEdge{
		{Space: "space", From: "checkout", Contract: "XC-atlas-first", PinnedMajor: 1, Drift: cache.DependencyDriftBehind},
		{Space: "space", From: "payments", Contract: "XC-atlas-second", PinnedMajor: 1, Drift: cache.DependencyDriftRetired},
	}}
	mappedItems := map[dashboardItemKey]Item{}
	for _, source := range append(append([]cache.Item{}, inbox...), outbox...) {
		mappedItems[dashboardItemKey{space: source.Space, id: source.ID}] = Item{Space: source.Space, ID: source.ID}
	}
	mappedEdges := mapContractDependencyEdges(dependencies.Edges)
	projected := projectDashboardAggregates(inbox, outbox, "atlas", now, dependencies, mappedItems, mappedEdges, nil)
	ids := func(items []Item) []string {
		out := make([]string, len(items))
		for index := range items {
			out[index] = items[index].ID
		}
		return out
	}
	if got := ids(projected.NeedYou.Items); !slices.Equal(got, []string{"your-move", "blocking"}) {
		t.Fatalf("NeedYou order = %v, want cache.OrderAttention order", got)
	}
	if got := ids(projected.NoHumanMove.Items); !slices.Equal(got, []string{"first-no-human", "second-no-human"}) {
		t.Fatalf("NoHumanMove order = %v, want P2 source order", got)
	}
	if got := ids(projected.YouAwait.Items); !slices.Equal(got, []string{"first-await", "second-await"}) {
		t.Fatalf("YouAwait order = %v, want P2 source order", got)
	}
	if !reflect.DeepEqual(projected.LinesOffCurrent.Items, mappedEdges) {
		t.Fatalf("LinesOffCurrent order changed\n got: %#v\nwant: %#v", projected.LinesOffCurrent.Items, mappedEdges)
	}
	if projected.NeedYou.Window.Total != 2 || projected.NoHumanMove.Window.Total != 2 || projected.YouAwait.Window.Total != 2 || projected.LinesOffCurrent.Window.Total != 2 {
		t.Fatalf("aggregate KPI totals are not window totals: %+v", projected)
	}
}

func TestDashboardAdmissionArtifactDetailRefsNeedYouFirst(t *testing.T) {
	t.Parallel()

	data := Data{
		Aggregates: DashboardAggregates{
			NeedYou:         DashboardItemSet{Items: []Item{{Space: "space", ID: "need"}, {Space: "space", ID: "duplicate"}}},
			NoHumanMove:     DashboardItemSet{Items: []Item{{Space: "space", ID: "no-human"}}},
			YouAwait:        DashboardItemSet{Items: []Item{{Space: "space", ID: "await"}}},
			LinesOffCurrent: DashboardContractEdgeSet{Items: []ContractEdge{{Space: "space", Contract: "XC-aggregate"}}},
		},
		Inbox:   []Item{{Space: "space", ID: "duplicate"}, {Space: "space", ID: "inbox"}},
		Outbox:  []Item{{Space: "space", ID: "outbox"}},
		Archive: []Item{{Space: "space", ID: "archive"}},
		Threads: []Thread{{Space: "space", ID: "thread:one", Members: []ThreadMember{{ID: "member"}}}},
		ThreadViews: []ThreadView{{Space: "space", Thread: "thread:one",
			Artifacts:  []ThreadViewArtifact{{ID: "view-artifact"}},
			Transcript: []ThreadTranscriptRow{{Artifact: &TranscriptArtifact{ID: "transcript-artifact"}}},
			OpenItems:  []ThreadOpenItem{{ID: "open-item"}}, Flags: []ThreadViewFlag{{Subject: "thread-flag"}},
		}},
		WorkReports:   []WorkReport{{Space: "space", ArtifactID: "work-report", SubjectRef: "work-subject"}},
		ContractEdges: []ContractEdge{{Space: "space", Contract: "XC-root"}},
		Flags:         []Flag{{Space: "space", Artifact: "root-flag"}},
	}
	want := []string{
		"space:need", "space:duplicate", "space:no-human", "space:await", "space:inbox", "space:outbox", "space:archive",
		"space:member", "space:view-artifact", "space:transcript-artifact", "space:open-item", "space:work-report", "space:work-subject",
		"space:XC-root", "space:XC-aggregate", "space:root-flag", "space:thread-flag",
	}
	if got := dashboardArtifactDetailRefs(data); !slices.Equal(got, want) {
		t.Fatalf("artifact detail ref order\n got: %v\nwant: %v", got, want)
	}
	refs := make([]string, maximumDashboardArtifactDetails+1)
	for index := range refs {
		refs[index] = fmt.Sprintf("space:XW-%02d", index)
	}
	prefix, window := admitPrefix(refs, maximumDashboardArtifactDetails)
	if len(prefix) != 32 || prefix[0] != refs[0] || prefix[31] != refs[31] || window != (operational.Window{Total: 33, Shown: 32, Truncated: true}) {
		t.Fatalf("artifact detail prefix/window = %v %+v", prefix, window)
	}
}

func TestViewModelMappingToThreadViewSentinels(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 14, 10, 11, 12, 0, time.UTC)
	record := int64(9)
	consistency := &cache.ReceiptMismatch{Kind: "state", EventULID: "01CONSISTENCY", Subject: "XW-sentinel"}
	result := cache.ThreadResult{
		Thread: "thread:sentinel", Space: "space-sentinel", Order: cache.ThreadOrderCommitted,
		Opener:       cache.ThreadOpener{ID: "XW-sentinel", Title: "opener-sentinel", From: "atlas"},
		Participants: []string{"atlas", "checkout"}, SyncStale: true,
		Artifacts: []cache.ThreadArtifact{{
			ID: "XW-sentinel", Type: "work_request", From: "atlas", To: []string{"checkout"},
			Title: "artifact-sentinel", State: "submitted", Parent: "parent-sentinel",
			Refs: []cache.ThreadRef{{Ref: "ref-sentinel"}},
		}},
		Transcript: []cache.TranscriptEntry{
			{Seq: 1, Kind: "artifact", At: at, Artifact: &cache.TranscriptArtifact{
				ID: "XA-status", Type: "announcement", From: "atlas", To: []string{"checkout"}, Title: "status-sentinel",
				Work: &cache.TranscriptWorkReport{
					ArtifactID: "XA-status", WorkID: "work:sentinel", SubjectRef: "XW-sentinel", Mode: workreport.ModeTesting,
					Summary: "summary-sentinel", Actor: cache.TranscriptEventActor{Kind: "agent", Name: "codex", System: "atlas", Model: "model-sentinel", Session: "session-sentinel"},
					WaitingOn:  []workreport.WaitingOn{{Kind: workreport.WaitTool, ID: "tool-sentinel", Summary: "wait-sentinel"}},
					ReportedAt: at, ValidUntil: at.Add(time.Hour), CommitSequence: 77,
				},
			}},
			{Seq: 2, Kind: "event", At: at.Add(time.Minute), Event: &cache.TranscriptEvent{
				ULID: "01EVENT", Subject: "XW-sentinel", Transition: "accept", ClaimedState: "accepted",
				Actor:      cache.TranscriptEventActor{Kind: "agent", Name: "codex", System: "atlas", Model: "model-sentinel", Session: "session-sentinel"},
				ProducedBy: provenance.Producer{Tool: "a2a", Version: "v-sentinel"}, Consistency: consistency,
				ResponseID: "XS-response", Version: "2.0.0", Note: "note-sentinel", ReasonCode: "reason-code-sentinel",
				TransitionFree: true, Verdicts: []cache.TranscriptVerdict{{Index: 3, Verdict: "fail", CauseOwner: "checkout"}},
			}},
			{Seq: 3, Kind: "derived", At: at.Add(2 * time.Minute), Derived: &cache.TranscriptDerivedAdoption{ContractID: "XC-atlas-api", System: "checkout", Major: 2, Since: "2026-08-01"}},
		},
		OpenItems: []cache.OpenItem{{
			ID: "XW-sentinel", Type: "work_request", State: "submitted", Blocking: true, NeededBy: "2026-08-15",
			NextActions: []cache.NextAction{{Transition: "accept", By: []string{"checkout"}}},
			WaitingOn:   []string{"checkout"}, YourMove: true, ExpectedTransition: "accept", Why: "why-sentinel", HumanGate: "G3",
			Outcome: fold.OutcomeOpen, Terminal: true, StateSince: at, StateBy: "atlas", StateEvent: "01STATE",
			OperationalItems: []cache.OperationalItem{{Name: "endpoint", State: cache.OperationalStateReady}},
		}},
		Flags:      []cache.ThreadFlag{{Kind: "flag-sentinel", Subject: "XW-sentinel", EventULID: "01FLAG", Consistency: consistency}},
		Unresolved: []cache.UnresolvedFact{{Kind: "parent", ID: "XW-missing"}},
		Deliveries: []cache.Delivery{{
			HandoffID: "XH-sentinel", Name: "data-sentinel", Ref: "DP-sentinel", Status: cache.DeliveryAvailable,
			PackageID: "DP-sentinel", Attempt: 2, Supersedes: "DP-earlier", Verdict: cache.DeliveryVerdictFail, ReportID: "VR-sentinel",
			Failures: []cache.DeliveryFailure{{EntryPath: "data.ndjson", Rule: "schema", Record: &record}}, Chain: []cache.DeliveryChainEntry{},
		}},
	}
	mappedRule := cache.RuleIdentity("work_request/submitted/target")
	mappedSentence := LocalizedText{RU: "sentinel-ru", EN: "sentinel-en"}
	got := toThreadViewWithMappedItems(result, "atlas", map[dashboardItemKey]Item{
		{space: result.Space, id: "XW-sentinel"}: {RuleIdentity: mappedRule, ReasonSentence: mappedSentence},
	})

	checks := []struct {
		field     string
		got, want any
	}{
		{"Thread", got.Thread, result.Thread}, {"Space", got.Space, result.Space}, {"Order", got.Order, result.Order},
		{"Opener", got.Opener, ThreadViewOpener{ID: result.Opener.ID, Title: result.Opener.Title, From: result.Opener.From}},
		{"Participants", got.Participants, result.Participants}, {"SyncStale", got.SyncStale, result.SyncStale},
		{"Artifacts", got.Artifacts[0], ThreadViewArtifact{ID: "XW-sentinel", Type: "work_request", From: "atlas", To: []string{"checkout"}, Title: "artifact-sentinel", State: "submitted", Parent: "parent-sentinel", Refs: []map[string]any{{"ref": "ref-sentinel"}}}},
		{"TranscriptEntry.Seq", got.Transcript[0].Seq, result.Transcript[0].Seq}, {"TranscriptEntry.Kind", got.Transcript[0].Kind, result.Transcript[0].Kind},
		{"TranscriptEntry.At", got.Transcript[0].At, result.Transcript[0].At.Format(time.RFC3339)},
		{"TranscriptArtifact.ID", got.Transcript[0].Artifact.ID, result.Transcript[0].Artifact.ID}, {"TranscriptArtifact.Type", got.Transcript[0].Artifact.Type, result.Transcript[0].Artifact.Type},
		{"TranscriptArtifact.From", got.Transcript[0].Artifact.From, result.Transcript[0].Artifact.From}, {"TranscriptArtifact.To", got.Transcript[0].Artifact.To, result.Transcript[0].Artifact.To},
		{"TranscriptArtifact.Title", got.Transcript[0].Artifact.Title, result.Transcript[0].Artifact.Title},
		{"TranscriptWorkReport.ArtifactID", got.Transcript[0].Artifact.Work.ArtifactID, result.Transcript[0].Artifact.Work.ArtifactID},
		{"TranscriptWorkReport.WorkID", got.Transcript[0].Artifact.Work.WorkID, result.Transcript[0].Artifact.Work.WorkID},
		{"TranscriptWorkReport.SubjectRef", got.Transcript[0].Artifact.Work.SubjectRef, result.Transcript[0].Artifact.Work.SubjectRef},
		{"TranscriptWorkReport.Mode", got.Transcript[0].Artifact.Work.Mode, string(result.Transcript[0].Artifact.Work.Mode)},
		{"TranscriptWorkReport.Summary", got.Transcript[0].Artifact.Work.Summary, result.Transcript[0].Artifact.Work.Summary},
		{"TranscriptWorkReport.Actor", got.Transcript[0].Artifact.Work.Actor, WorkReportActor{Kind: "agent", Name: "codex", System: "atlas", Model: "model-sentinel", Session: "session-sentinel"}},
		{"TranscriptWorkReport.WaitingOn", got.Transcript[0].Artifact.Work.WaitingOn, []WorkReportWait{{Kind: "tool", ID: "tool-sentinel", Summary: "wait-sentinel"}}},
		{"TranscriptWorkReport.ReportedAt", got.Transcript[0].Artifact.Work.ReportedAt, at.Format(time.RFC3339Nano)},
		{"TranscriptWorkReport.ValidUntil", got.Transcript[0].Artifact.Work.ValidUntil, at.Add(time.Hour).Format(time.RFC3339Nano)},
		{"TranscriptWorkReport.CommitSequence", got.Transcript[0].Artifact.Work.CommitSequence, uint64(77)},
		{"TranscriptEvent.ULID", got.Transcript[1].Event.ULID, result.Transcript[1].Event.ULID},
		{"TranscriptEvent.Subject", got.Transcript[1].Event.Subject, result.Transcript[1].Event.Subject},
		{"TranscriptEvent.Transition", got.Transcript[1].Event.Transition, result.Transcript[1].Event.Transition},
		{"TranscriptEvent.ClaimedState", got.Transcript[1].Event.ClaimedState, result.Transcript[1].Event.ClaimedState},
		{"TranscriptEvent.Actor", got.Transcript[1].Event.Actor, map[string]any{"kind": "agent", "name": "codex", "system": "atlas", "model": "model-sentinel", "session": "session-sentinel"}},
		{"TranscriptEvent.ProducedBy", got.Transcript[1].Event.ProducedBy, result.Transcript[1].Event.ProducedBy},
		{"TranscriptEvent.Consistency", got.Transcript[1].Event.Consistency, result.Transcript[1].Event.Consistency},
		{"TranscriptEvent.ResponseID", got.Transcript[1].Event.ResponseID, result.Transcript[1].Event.ResponseID},
		{"TranscriptEvent.Version", got.Transcript[1].Event.Version, result.Transcript[1].Event.Version},
		{"TranscriptEvent.Note", got.Transcript[1].Event.Note, result.Transcript[1].Event.Note},
		{"TranscriptEvent.ReasonCode", got.Transcript[1].Event.ReasonCode, result.Transcript[1].Event.ReasonCode},
		{"TranscriptEvent.TransitionFree", got.Transcript[1].Event.TransitionFree, result.Transcript[1].Event.TransitionFree},
		{"TranscriptEvent.Verdicts", got.Transcript[1].Event.Verdicts, result.Transcript[1].Event.Verdicts},
		{"TranscriptDerivedAdoption", got.Transcript[2].Derived, &TranscriptDerived{ContractID: "XC-atlas-api", System: "checkout", Major: 2, Since: "2026-08-01"}},
		{"OpenItem.RuleIdentity", got.OpenItems[0].RuleIdentity, mappedRule}, {"OpenItem.ReasonSentence", got.OpenItems[0].ReasonSentence, mappedSentence},
		{"OpenItem.ID", got.OpenItems[0].ID, result.OpenItems[0].ID}, {"OpenItem.Type", got.OpenItems[0].Type, result.OpenItems[0].Type},
		{"OpenItem.State", got.OpenItems[0].State, result.OpenItems[0].State}, {"OpenItem.Blocking", got.OpenItems[0].Blocking, result.OpenItems[0].Blocking},
		{"OpenItem.NeededBy", got.OpenItems[0].NeededBy, result.OpenItems[0].NeededBy},
		{"OpenItem.NextActions", got.OpenItems[0].NextActions, []ThreadNextAction{{Transition: "accept", By: []string{"checkout"}}}},
		{"OpenItem.WaitingOn", got.OpenItems[0].WaitingOn, result.OpenItems[0].WaitingOn}, {"OpenItem.YourMove", got.OpenItems[0].YourMove, result.OpenItems[0].YourMove},
		{"OpenItem.ExpectedTransition", got.OpenItems[0].ExpectedTransition, result.OpenItems[0].ExpectedTransition}, {"OpenItem.Why", got.OpenItems[0].Why, result.OpenItems[0].Why},
		{"OpenItem.HumanGate", got.OpenItems[0].HumanGate, result.OpenItems[0].HumanGate}, {"OpenItem.Outcome", got.OpenItems[0].Outcome, result.OpenItems[0].Outcome},
		{"OpenItem.Terminal", got.OpenItems[0].Terminal, result.OpenItems[0].Terminal}, {"OpenItem.StateSince", got.OpenItems[0].StateSince, result.OpenItems[0].StateSince},
		{"OpenItem.StateBy", got.OpenItems[0].StateBy, result.OpenItems[0].StateBy}, {"OpenItem.StateEvent", got.OpenItems[0].StateEvent, result.OpenItems[0].StateEvent},
		{"OpenItem.OperationalItems", got.OpenItems[0].OperationalItems, result.OpenItems[0].OperationalItems},
		{"ThreadFlag", got.Flags[0], ThreadViewFlag{Kind: "flag-sentinel", Subject: "XW-sentinel", EventULID: "01FLAG", Consistency: consistency}},
		{"UnresolvedFact", got.Unresolved[0], ThreadUnresolvedRef{Kind: "parent", ID: "XW-missing"}},
		{"Deliveries", got.Deliveries, ProjectDeliveries(result.Deliveries)},
	}
	for _, check := range checks {
		assertMappedValue(t, "toThreadView", check.field, check.got, check.want)
	}

	unmatched := toThreadViewWithMappedItems(result, "atlas", map[dashboardItemKey]Item{})
	assertMappedValue(t, "toThreadView", "unmatched RuleIdentity", unmatched.OpenItems[0].RuleIdentity, cache.RuleIdentity(""))
	assertMappedValue(t, "toThreadView", "unmatched ReasonSentence", unmatched.OpenItems[0].ReasonSentence, LocalizedText{})
}

func assertMappedValue(t *testing.T, mapper, field string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s dropped source field %s: got %#v, want %#v", mapper, field, got, want)
	}
}

func TestViewModelMappingToItemSentinels(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	created := now.Add(-2 * time.Hour)
	moved := now.Add(-time.Hour)
	rule := cache.RuleIdentity("decision/proposed")
	source := cache.Item{
		Space: "space-sentinel", ID: "XD-sentinel", Type: "decision", Title: "title-sentinel",
		From: "atlas", To: []string{"checkout"}, State: "proposed", Priority: "p1", Blocking: true,
		NeededBy: "2026-08-15", Thread: "thread:sentinel", New: true, Reasons: []string{string(cache.ReasonGatePendingOnMe)},
		Overdue: true, ActivationOwed: true, PendingMerge: true, SyncStale: true,
		CreatedAt: created, CreatedSeq: 17, CreatedOrderKnown: true,
		LatestEventAt: moved, LatestEventSeq: 23, LatestEventID: "01SENTINEL", Description: "description-sentinel",
		Outcome: fold.OutcomeOpen, Terminal: true, StateSince: moved, StateBy: "checkout", StateEvent: "01STATE",
		YourMove: true, WaitingOn: []string{"atlas"}, ExpectedTransition: "approve", Why: "why-sentinel",
		HumanGate: "G3", RuleIdentity: rule,
		OperationalItems: []cache.OperationalItem{{Name: "endpoint", State: cache.OperationalStateReady}},
	}

	got := toItem(source, now, "atlas", openItemIndex{})
	checks := []struct {
		field     string
		got, want any
	}{
		{"Space", got.Space, source.Space}, {"ID", got.ID, source.ID}, {"Type", got.Type, source.Type},
		{"Title", got.Title, source.Title}, {"From", got.From, source.From}, {"To", got.To, source.To},
		{"State", got.State, source.State}, {"Priority", got.Priority, source.Priority}, {"Blocking", got.Blocking, source.Blocking},
		{"NeededBy", got.NeededBy, source.NeededBy}, {"Thread", got.Thread, source.Thread}, {"New", got.New, source.New},
		{"Reasons", got.Reasons, source.Reasons}, {"Overdue", got.Overdue, source.Overdue}, {"ActivationOwed", got.ActivationOwed, source.ActivationOwed},
		{"PendingMerge", got.PendingMerge, source.PendingMerge}, {"SyncStale", got.SyncStale, source.SyncStale},
		{"CreatedAt", got.CreatedAt, created.Format(time.RFC3339)}, {"CreatedSeq", got.CreatedSeq, source.CreatedSeq},
		{"CreatedOrderKnown", got.CreatedOrderKnown, source.CreatedOrderKnown}, {"LatestEventAt", got.MovedAt, moved.Format(time.RFC3339)},
		{"LatestEventSeq", got.ActivitySeq, source.LatestEventSeq}, {"LatestEventID", got.ActivityEventID, source.LatestEventID},
		{"Description", got.Description, source.Description}, {"Outcome", got.Outcome, source.Outcome}, {"Terminal", got.Terminal, source.Terminal},
		{"StateSince", got.StateSince, source.StateSince}, {"StateBy", got.StateBy, source.StateBy}, {"StateEvent", got.StateEvent, source.StateEvent},
		{"YourMove", got.YourMove, source.YourMove}, {"WaitingOn", got.WaitingOn, source.WaitingOn},
		{"ExpectedTransition", got.ExpectedTransition, source.ExpectedTransition}, {"Why", got.Why, source.Why},
		{"HumanGate", got.HumanGate, source.HumanGate}, {"RuleIdentity", got.RuleIdentity, source.RuleIdentity},
		{"OperationalItems", got.OperationalItems, source.OperationalItems}, {"ReasonSentence", got.ReasonSentence, attentionSentence(source)},
	}
	for _, check := range checks {
		assertMappedValue(t, "toItem", check.field, check.got, check.want)
	}
	assertMappedField(t, "toItem", "HumanGate->GatePending", got.GatePending, true)

	reasonOnly := toItem(cache.Item{Reasons: []string{string(cache.ReasonGatePendingOnMe)}, RuleIdentity: rule}, now, "atlas", openItemIndex{})
	if reasonOnly.GatePending {
		t.Fatal("toItem GatePending followed a reason literal without the typed HumanGate verdict")
	}
	gateOnly := toItem(cache.Item{HumanGate: "G3"}, now, "atlas", openItemIndex{})
	if !gateOnly.GatePending {
		t.Fatal("toItem GatePending dropped the typed HumanGate verdict when Reasons did not agree")
	}
	archived := mapDashboardItems([]cache.Item{source}, now, "atlas", openItemIndex{}, true)
	if len(archived) != 1 || !archived[0].Archived || archived[0].Severity != "normal" {
		t.Fatalf("archive projection = %+v, want Archived true and normal severity", archived)
	}
}
