package html

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/localserver"
)

func TestLargestAdmittedDashboardFixtureBudget(t *testing.T) {
	t.Parallel()

	data := largestAdmittedDashboardFixture(t)
	compact, err := CanonicalViewModelJSON(data)
	if err != nil {
		t.Fatalf("CanonicalViewModelJSON: %v", err)
	}
	docs, err := Docs()
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	shell, err := Render(DefaultTemplate(), data, docs)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := dashboardBudgetError("compact DATA", len(compact), localserver.DefaultMaxDashboardBytes, data); err != nil {
		t.Fatal(err)
	}
	if err := dashboardBudgetError("rendered shell", len(shell), localserver.DefaultMaxShellBytes, data); err != nil {
		t.Fatal(err)
	}
	t.Logf("largest_admitted_dashboard compact_data=%d data_limit=%d data_headroom=%d rendered_shell=%d shell_limit=%d shell_headroom=%d",
		len(compact), localserver.DefaultMaxDashboardBytes, localserver.DefaultMaxDashboardBytes-len(compact),
		len(shell), localserver.DefaultMaxShellBytes, localserver.DefaultMaxShellBytes-len(shell))
}

func TestDashboardBudgetDiagnostic(t *testing.T) {
	t.Parallel()

	data := largestAdmittedDashboardFixture(t)
	compact, err := CanonicalViewModelJSON(data)
	if err != nil {
		t.Fatalf("CanonicalViewModelJSON: %v", err)
	}
	top, nested := dashboardByteContributions(data)
	if len(top) == 0 || len(nested) == 0 {
		t.Fatalf("budget diagnostic inventories are empty: top=%d nested=%d", len(top), len(nested))
	}
	diagnostic := dashboardBudgetError("compact DATA", len(compact), len(compact)-1, data)
	if diagnostic == nil {
		t.Fatal("one-byte-lower budget unexpectedly passed; diagnostic has no tooth")
	}
	message := diagnostic.Error()
	for _, want := range []string{
		fmt.Sprintf("measured=%d", len(compact)), fmt.Sprintf("ceiling=%d", len(compact)-1),
		"top-level contributors:", top[0].name, "nested-thread contributors:", nested[0].name,
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("budget diagnostic does not name %q:\n%s", want, message)
		}
	}
}

func largestAdmittedDashboardFixture(t *testing.T) Data {
	t.Helper()

	data, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}
	admit := func(name string, source any, maximum int) any {
		t.Helper()
		value := reflect.ValueOf(source)
		if value.Kind() != reflect.Slice || value.Len() == 0 {
			t.Fatalf("DemoData has no real fixture rows for %s", name)
		}
		repeated := reflect.MakeSlice(value.Type(), maximum, maximum)
		for index := 0; index < maximum; index++ {
			repeated.Index(index).Set(value.Index(index % value.Len()))
		}
		return repeated.Interface()
	}

	fullInbox := admit("inbox", data.Inbox, maximumDashboardInboxItems).([]Item)
	data.Inbox, data.Windows.Inbox = admitPrefix(fullInbox, maximumDashboardInboxItems)
	fullOutbox := admit("outbox", data.Outbox, maximumDashboardOutboxItems).([]Item)
	data.Outbox, data.Windows.Outbox = admitPrefix(fullOutbox, maximumDashboardOutboxItems)
	fullArchive := admit("archive", data.Archive, maximumDashboardArchiveItems).([]Item)
	data.Archive, data.Windows.Archive = admitPrefix(fullArchive, maximumDashboardArchiveItems)
	fullContractEdges := admit("contract edges", data.ContractEdges, maximumDashboardContractEdges).([]ContractEdge)
	data.ContractEdges, data.Windows.ContractEdges = admitPrefix(fullContractEdges, maximumDashboardContractEdges)
	fullExchangeEdges := admit("exchange edges", data.ExchangeEdges, maximumDashboardExchangeEdges).([]ExchangeEdge)
	data.ExchangeEdges, data.Windows.ExchangeEdges = admitPrefix(fullExchangeEdges, maximumDashboardExchangeEdges)
	fullFlags := admit("flags", data.Flags, maximumDashboardFlags).([]Flag)
	data.Flags, data.Windows.Flags = admitPrefix(fullFlags, maximumDashboardFlags)
	fullWorkReports := admit("work reports", data.WorkReports, maximumDashboardWorkReports).([]WorkReport)
	data.WorkReports, data.Windows.WorkReports = admitPrefix(fullWorkReports, maximumDashboardWorkReports)
	fullDetails := admit("artifact details", data.ArtifactDetails, maximumDashboardArtifactDetails).([]ArtifactDetail)
	data.ArtifactDetails, data.Windows.ArtifactDetails = admitPrefix(fullDetails, maximumDashboardArtifactDetails)

	data.Aggregates.NeedYou.Items, data.Aggregates.NeedYou.Window = admitPrefix(admit("NeedYou", data.Inbox, maximumDashboardAggregateItems).([]Item), maximumDashboardAggregateItems)
	data.Aggregates.NoHumanMove.Items, data.Aggregates.NoHumanMove.Window = admitPrefix(admit("NoHumanMove", data.Inbox, maximumDashboardAggregateItems).([]Item), maximumDashboardAggregateItems)
	data.Aggregates.YouAwait.Items, data.Aggregates.YouAwait.Window = admitPrefix(admit("YouAwait", data.Outbox, maximumDashboardAggregateItems).([]Item), maximumDashboardAggregateItems)
	data.Aggregates.LinesOffCurrent.Items, data.Aggregates.LinesOffCurrent.Window = admitPrefix(admit("LinesOffCurrent", data.ContractEdges, maximumDashboardAggregateContractEdges).([]ContractEdge), maximumDashboardAggregateContractEdges)
	data.Aggregates.LinesOffCurrent.Complete = true
	data.Aggregates.LinesOffCurrent.Degradations = []DependencyDegradation{}

	artifactSeed := firstNonEmpty(data.ThreadViews, func(view ThreadView) []ThreadViewArtifact { return view.Artifacts })
	transcriptSeed := firstNonEmpty(data.ThreadViews, func(view ThreadView) []ThreadTranscriptRow { return view.Transcript })
	openItemSeed := firstNonEmpty(data.ThreadViews, func(view ThreadView) []ThreadOpenItem { return view.OpenItems })
	viewFlagSeed := firstNonEmpty(data.ThreadViews, func(view ThreadView) []ThreadViewFlag { return view.Flags })
	unresolvedSeed := firstNonEmpty(data.ThreadViews, func(view ThreadView) []ThreadUnresolvedRef { return view.Unresolved })
	deliverySeed := firstNonEmpty(data.ThreadViews, func(view ThreadView) []Delivery { return view.Deliveries })
	memberSeed := firstNonEmpty(data.Threads, func(thread Thread) []ThreadMember { return thread.Members })
	linkSeed := firstNonEmpty(data.Threads, func(thread Thread) []DocLink { return thread.Links })

	threadSource := admit("threads", data.Threads, maximumDashboardThreads).([]Thread)
	viewSource := admit("thread views", data.ThreadViews, maximumDashboardThreads).([]ThreadView)
	for index := range threadSource {
		threadID := fmt.Sprintf("thread:budget-%02d", index)
		spaceID := fmt.Sprintf("budget-space-%02d", index)
		threadSource[index].ID, threadSource[index].Space = threadID, spaceID
		viewSource[index].Thread, viewSource[index].Space = threadID, spaceID
		threadSource[index].Members, threadSource[index].Windows.Members = admitPrefix(admit("thread members", memberSeed, maximumDashboardThreadArtifacts).([]ThreadMember), maximumDashboardThreadArtifacts)
		threadSource[index].Links, threadSource[index].Windows.Links = admitPrefix(admit("thread links", linkSeed, maximumDashboardThreadLinks).([]DocLink), maximumDashboardThreadLinks)
		viewSource[index].Artifacts, viewSource[index].Windows.Artifacts = admitPrefix(admit("thread artifacts", artifactSeed, maximumDashboardThreadArtifacts).([]ThreadViewArtifact), maximumDashboardThreadArtifacts)
		viewSource[index].Transcript, viewSource[index].Windows.Transcript = admitPrefix(admit("thread transcript", transcriptSeed, maximumDashboardThreadTranscriptRows).([]ThreadTranscriptRow), maximumDashboardThreadTranscriptRows)
		viewSource[index].OpenItems, viewSource[index].Windows.OpenItems = admitPrefix(admit("thread open items", openItemSeed, maximumDashboardThreadOpenItems).([]ThreadOpenItem), maximumDashboardThreadOpenItems)
		viewSource[index].Flags, viewSource[index].Windows.Flags = admitPrefix(admit("thread flags", viewFlagSeed, maximumDashboardThreadFlags).([]ThreadViewFlag), maximumDashboardThreadFlags)
		viewSource[index].Unresolved, viewSource[index].Windows.Unresolved = admitPrefix(admit("thread unresolved", unresolvedSeed, maximumDashboardThreadUnresolved).([]ThreadUnresolvedRef), maximumDashboardThreadUnresolved)
		viewSource[index].Deliveries, viewSource[index].Windows.Deliveries = admitPrefix(admit("thread deliveries", deliverySeed, maximumDashboardThreadDeliveries).([]Delivery), maximumDashboardThreadDeliveries)
	}
	data.Threads, data.ThreadViews, data.Windows.Threads, err = admitThreadPairs(threadSource, viewSource, maximumDashboardThreads)
	if err != nil {
		t.Fatalf("admitThreadPairs: %v", err)
	}
	return data
}

func firstNonEmpty[Container, Row any](containers []Container, rows func(Container) []Row) []Row {
	for _, container := range containers {
		if found := rows(container); len(found) > 0 {
			return found
		}
	}
	return nil
}

type byteContribution struct {
	name  string
	bytes int
}

func dashboardByteContributions(data Data) ([]byteContribution, []byteContribution) {
	value := reflect.ValueOf(data)
	typeOfData := value.Type()
	top := make([]byteContribution, 0, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		name := strings.Split(typeOfData.Field(index).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			name = typeOfData.Field(index).Name
		}
		encoded, _ := json.Marshal(value.Field(index).Interface())
		top = append(top, byteContribution{name: name, bytes: len(encoded)})
	}
	nested := make([]byteContribution, 0, len(data.ThreadViews)*6)
	for _, view := range data.ThreadViews {
		prefix := fmt.Sprintf("threadViews[%s/%s].", view.Space, view.Thread)
		for _, component := range []struct {
			name  string
			value any
		}{
			{"artifacts", view.Artifacts}, {"transcript", view.Transcript}, {"open_items", view.OpenItems},
			{"flags", view.Flags}, {"unresolved", view.Unresolved}, {"deliveries", view.Deliveries},
		} {
			encoded, _ := json.Marshal(component.value)
			nested = append(nested, byteContribution{name: prefix + component.name, bytes: len(encoded)})
		}
	}
	order := func(rows []byteContribution) {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].bytes != rows[j].bytes {
				return rows[i].bytes > rows[j].bytes
			}
			return rows[i].name < rows[j].name
		})
	}
	order(top)
	order(nested)
	return top, nested
}

func dashboardBudgetError(label string, measured, ceiling int, data Data) error {
	if measured < ceiling {
		return nil
	}
	top, nested := dashboardByteContributions(data)
	var report strings.Builder
	fmt.Fprintf(&report, "%s measured=%d ceiling=%d headroom=%d\ntop-level contributors:", label, measured, ceiling, ceiling-measured)
	for _, contribution := range top {
		fmt.Fprintf(&report, "\n  %s=%d", contribution.name, contribution.bytes)
	}
	report.WriteString("\nnested-thread contributors:")
	for _, contribution := range nested {
		fmt.Fprintf(&report, "\n  %s=%d", contribution.name, contribution.bytes)
	}
	return fmt.Errorf("%s", report.String())
}
