package html

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/operational"
)

type demoFixturePair struct {
	family VocabularyFamily
	value  string
}

// TestDemoFixtureCoversBinaryVocabulary keeps the synthetic breadth fixture
// behind the same closed vocabulary the shipped binary reports. Values come
// from `a2a __catalog`; this test owns only the mapping from catalogue fields
// to the product fields that actually render those values.
func TestDemoFixtureCoversBinaryVocabulary(t *testing.T) {
	t.Parallel()

	catalogue := demoFixtureCatalogue(t)
	data, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	covered := make(map[demoFixturePair]bool)
	add := func(family VocabularyFamily, value string) {
		if value != "" {
			covered[demoFixturePair{family: family, value: value}] = true
		}
	}
	addItem := func(item Item) {
		add(VocabularyFamilyOutcome, string(item.Outcome))
		add(VocabularyFamilyLifecycleState, item.State)
		for _, reason := range item.Reasons {
			add(VocabularyFamilyReason, reason)
		}
		add(VocabularyFamilyGate, item.HumanGate)
		for _, operationalItem := range item.OperationalItems {
			add(VocabularyFamilyOperationalState, operationalItem.State)
		}
	}
	for _, items := range [][]Item{data.Inbox, data.Outbox, data.Archive} {
		for _, item := range items {
			addItem(item)
		}
	}
	for _, thread := range data.Threads {
		for _, member := range thread.Members {
			add(VocabularyFamilyLifecycleState, member.State)
		}
	}
	for _, view := range data.ThreadViews {
		for _, artifact := range view.Artifacts {
			add(VocabularyFamilyLifecycleState, artifact.State)
		}
		for _, row := range view.Transcript {
			if row.Event != nil {
				add(VocabularyFamilyTransition, row.Event.Transition)
			}
		}
		for _, item := range view.OpenItems {
			add(VocabularyFamilyOutcome, string(item.Outcome))
			add(VocabularyFamilyLifecycleState, item.State)
			add(VocabularyFamilyGate, item.HumanGate)
			for _, operationalItem := range item.OperationalItems {
				add(VocabularyFamilyOperationalState, operationalItem.State)
			}
		}
	}
	for _, detail := range data.ArtifactDetails {
		add(VocabularyFamilyOutcome, string(detail.Outcome))
		add(VocabularyFamilyLifecycleState, detail.State)
		for _, event := range detail.Events {
			add(VocabularyFamilyTransition, event.Transition)
		}
		for _, operationalItem := range detail.OperationalItems {
			add(VocabularyFamilyOperationalState, operationalItem.State)
		}
	}
	for _, contract := range data.Contracts {
		add(VocabularyFamilyLifecycleState, contract.State)
		for _, version := range contract.Versions {
			add(VocabularyFamilyLifecycleState, version.State)
			if version.Detail != nil {
				for _, event := range version.Detail.History {
					add(VocabularyFamilyTransition, event.Transition)
				}
			}
		}
	}
	for _, edge := range data.ContractEdges {
		add(VocabularyFamilyLifecycleState, edge.State)
		add(VocabularyFamilyLifecycleState, edge.PinnedState)
		add(VocabularyFamilyDependencyDrift, edge.Drift)
	}
	for _, source := range data.Operational.Sources {
		add(VocabularyFamilySourceFreshness, string(source.Freshness))
	}
	for _, row := range data.Operational.Timeline {
		if row.LatestMilestone != nil {
			add(VocabularyFamilyTransition, row.LatestMilestone.Transition)
		}
		for _, work := range row.Work {
			add(VocabularyFamilyFreshness, string(work.Freshness))
			add(VocabularyFamilyWorkMode, string(work.Mode))
		}
		for _, fact := range row.Consistency {
			add(VocabularyFamilyConsistencySeverity, string(fact.Severity))
		}
	}

	wanted := make(map[demoFixturePair]bool)
	for family, values := range catalogue {
		if family == VocabularyFamilyLiveTransport {
			continue // client-local state is exercised by P15's scenario matrix
		}
		for _, value := range values {
			pair := demoFixturePair{family: family, value: value}
			wanted[pair] = true
			if !covered[pair] {
				t.Errorf("demo fixture misses %s/%s", family, value)
			}
		}
	}
	for pair := range covered {
		if !wanted[pair] {
			t.Errorf("demo fixture carries non-catalogue %s/%s", pair.family, pair.value)
		}
	}
}

// TestDemoFixtureExercisesHonestBounds proves the breadth fixture contains
// real overflow, rather than merely carrying fields that describe overflow.
func TestDemoFixtureExercisesHonestBounds(t *testing.T) {
	t.Parallel()

	data, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}
	limits := operational.DefaultLimits()
	workBound, consistencyBound, codedConsistency, previewBound := false, false, false, false
	for _, row := range data.Operational.Timeline {
		if row.WorkWindow.Truncated && row.WorkWindow.Shown == limits.WorkPerRow &&
			row.WorkWindow.Total > row.WorkWindow.Shown && len(row.Work) == row.WorkWindow.Shown {
			workBound = true
		}
		if row.ConsistencyWindow.Truncated && row.ConsistencyWindow.Shown == limits.ConsistencyPerRow &&
			row.ConsistencyWindow.Total > row.ConsistencyWindow.Shown && len(row.Consistency) == row.ConsistencyWindow.Shown {
			consistencyBound = true
		}
		for _, fact := range row.Consistency {
			if fact.Code != "" && fact.Severity != "" && strings.TrimSpace(fact.Summary) != "" {
				codedConsistency = true
			}
		}
	}
	for _, contract := range data.Contracts {
		for _, version := range contract.Versions {
			if version.Detail == nil {
				continue
			}
			for _, document := range version.Detail.Documents {
				if document.Truncated && document.ShownBytes == int64(len(document.Preview)) &&
					document.ShownBytes < document.TotalBytes && document.TotalBytes == document.SizeBytes {
					previewBound = true
				}
			}
		}
	}
	if !workBound {
		t.Errorf("demo has no work window beyond DefaultWorkPerRow=%d", limits.WorkPerRow)
	}
	if !consistencyBound {
		t.Errorf("demo has no consistency window beyond DefaultConsistency=%d", limits.ConsistencyPerRow)
	}
	if !codedConsistency {
		t.Error("demo has no consistency anomaly with code, severity, and summary")
	}
	if !previewBound {
		t.Error("demo has no contract preview with shownBytes == len(preview) < totalBytes == sizeBytes and truncated=true")
	}
}

func demoFixtureCatalogue(t *testing.T) map[VocabularyFamily][]string {
	t.Helper()
	root := demoFixtureRepoRoot(t)
	var command *exec.Cmd
	if binary := os.Getenv("A2A_VERIFY_BINARY"); binary != "" {
		command = exec.Command(binary, "__catalog", "--vocabulary", "--json")
	} else {
		command = exec.Command("go", "run", "./cmd/a2a", "__catalog", "--vocabulary", "--json")
		command.Env = append(os.Environ(), "GOWORK=off")
	}
	command.Dir = root
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run a2a __catalog --vocabulary --json: %v\nstderr: %s", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("a2a __catalog wrote to stderr: %s", stderr.String())
	}

	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		t.Fatalf("decode vocabulary catalogue: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("vocabulary catalogue has trailing content: %v", err)
	}
	gotKeys := make([]string, 0, len(raw))
	for key := range raw {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	wantKeys := []string{
		"consistency_severity", "dependency_drift", "freshness", "gate", "human_gates",
		"live_transport", "operational_state", "outcomes", "reason", "rule_identity",
		"source_freshness", "states", "transitions", "work_mode",
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("unknown vocabulary catalogue shape: keys=%v, want %v", gotKeys, wantKeys)
	}

	states := decodeDemoCatalogueField[map[string][]string](t, raw, "states")
	if len(states) == 0 {
		t.Fatal("vocabulary catalogue states is empty")
	}
	lifecycle := make(map[string]bool)
	for kind, values := range states {
		if kind == "" || len(values) == 0 {
			t.Fatalf("vocabulary catalogue states contains empty kind/family: %q=%v", kind, values)
		}
		for _, value := range values {
			lifecycle[value] = true
		}
	}
	if values := decodeDemoCatalogueField[[]string](t, raw, "transitions"); len(values) == 0 {
		t.Fatal("vocabulary catalogue transitions is empty")
	}
	if values := decodeDemoCatalogueField[map[string]string](t, raw, "human_gates"); len(values) == 0 {
		t.Fatal("vocabulary catalogue human_gates is empty")
	}
	if values := decodeDemoCatalogueField[[]string](t, raw, "rule_identity"); len(values) == 0 {
		t.Fatal("vocabulary catalogue rule_identity is empty")
	}

	catalogue := map[VocabularyFamily][]string{
		VocabularyFamilyFreshness:           decodeDemoCatalogueField[[]string](t, raw, "freshness"),
		VocabularyFamilySourceFreshness:     decodeDemoCatalogueField[[]string](t, raw, "source_freshness"),
		VocabularyFamilyOutcome:             decodeDemoCatalogueField[[]string](t, raw, "outcomes"),
		VocabularyFamilyLifecycleState:      sortedDemoCatalogueSet(lifecycle),
		VocabularyFamilyTransition:          decodeDemoCatalogueField[[]string](t, raw, "transitions"),
		VocabularyFamilyReason:              decodeDemoCatalogueField[[]string](t, raw, "reason"),
		VocabularyFamilyGate:                decodeDemoCatalogueField[[]string](t, raw, "gate"),
		VocabularyFamilyWorkMode:            decodeDemoCatalogueField[[]string](t, raw, "work_mode"),
		VocabularyFamilyDependencyDrift:     decodeDemoCatalogueField[[]string](t, raw, "dependency_drift"),
		VocabularyFamilyConsistencySeverity: decodeDemoCatalogueField[[]string](t, raw, "consistency_severity"),
		VocabularyFamilyOperationalState:    decodeDemoCatalogueField[[]string](t, raw, "operational_state"),
		VocabularyFamilyLiveTransport:       decodeDemoCatalogueField[[]string](t, raw, "live_transport"),
	}
	for family, values := range catalogue {
		seen := make(map[string]bool)
		if len(values) == 0 {
			t.Fatalf("vocabulary catalogue family %q is empty", family)
		}
		for _, value := range values {
			if value == "" || seen[value] {
				t.Fatalf("vocabulary catalogue family %q has empty or duplicate value %q", family, value)
			}
			seen[value] = true
		}
	}
	return catalogue
}

func decodeDemoCatalogueField[T any](t *testing.T, raw map[string]json.RawMessage, key string) T {
	t.Helper()
	var value T
	field, ok := raw[key]
	if !ok {
		t.Fatalf("vocabulary catalogue omits %q", key)
	}
	if err := json.Unmarshal(field, &value); err != nil {
		t.Fatalf("vocabulary catalogue field %q has unknown shape: %v", key, err)
	}
	return value
}

func sortedDemoCatalogueSet(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func demoFixtureRepoRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while resolving repository root")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolved repository root %s has no go.mod: %v", root, err)
	}
	return root
}
