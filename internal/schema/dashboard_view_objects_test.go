package schema_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	dashboardhtml "github.com/ydnikolaev/a2ahub/internal/html"
	dashboardschema "github.com/ydnikolaev/a2ahub/internal/schema"
)

type dashboardViewMapping struct {
	kind           string
	definition     string
	sourcePath     string
	component      string
	selectionPaths [][]string
	identityFields []string
}

var dashboardViewMappings = []dashboardViewMapping{
	{kind: "item", definition: "item", sourcePath: "Data.inbox[] / Data.outbox[] / Data.archive[]", component: "ArtifactDetail", selectionPaths: [][]string{{"inbox"}, {"outbox"}, {"archive"}}, identityFields: []string{"space", "id"}},
	{kind: "thread", definition: "threadView", sourcePath: "Data.threadViews[]", component: "ThreadsView", selectionPaths: [][]string{{"threadViews"}}, identityFields: []string{"space", "thread"}},
	{kind: "contract", definition: "contract", sourcePath: "Data.contracts[]", component: "ContractsView", selectionPaths: [][]string{{"contracts"}}, identityFields: []string{"space", "id"}},
	{kind: "cver", definition: "contractVersion", sourcePath: "Data.contracts[].versions[]", component: "ContractsView", selectionPaths: [][]string{{"contracts", "versions"}}, identityFields: []string{"space", "id", "version"}},
	{kind: "work-report", definition: "workReport", sourcePath: "Data.workReports[]", component: "ExchangeView", selectionPaths: [][]string{{"workReports"}}, identityFields: []string{"space", "artifact_id"}},
	{kind: "space", definition: "space", sourcePath: "Data.spaces[]", component: "SpacesView", selectionPaths: [][]string{{"spaces"}}, identityFields: []string{"id"}},
	{kind: "operational-process", definition: "operationalProcess", sourcePath: "Data.operational.timeline[]", component: "Overview", selectionPaths: [][]string{{"operational", "timeline"}}, identityFields: []string{"space", "thread"}},
}

func TestDashboardViewObjects(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join("..", "..")
	schemaPath := filepath.Join(repoRoot, "schemas", "dashboard-view-objects.schema.json")
	schemaRaw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read portable view-object schema: %v", err)
	}
	schemaDocument := dashboardReadJSONObject(t, schemaPath, schemaRaw)

	t.Run("seven-kind mapping matches the manifest", func(t *testing.T) {
		dashboardAssertManifestMapping(t, repoRoot)
	})

	t.Run("thread view nested cores are ref backed", func(t *testing.T) {
		definitions := dashboardObject(t, schemaDocument, "$defs")
		threadView := dashboardObject(t, definitions, "threadView")
		properties := dashboardObject(t, threadView, "properties")
		want := map[string]string{
			"opener":     "threadViewOpener",
			"artifacts":  "threadViewArtifact",
			"transcript": "threadTranscriptRow",
			"open_items": "threadOpenItem",
			"flags":      "threadViewFlag",
			"unresolved": "threadUnresolvedRef",
			"windows":    "threadViewWindows",
			"deliveries": "delivery",
		}
		for member, definition := range want {
			property := dashboardObject(t, properties, member)
			if _, array := property["items"]; array {
				property = dashboardObject(t, property, "items")
			}
			if got := dashboardString(t, property, "$ref"); got != "#/$defs/"+definition {
				t.Fatalf("threadView.%s ref = %q, want #/$defs/%s", member, got, definition)
			}
		}
		openItem := dashboardObject(t, definitions, "threadOpenItem")
		openProperties := dashboardObject(t, openItem, "properties")
		for _, viewerMember := range []string{"your_move", "prompt"} {
			if _, governed := openProperties[viewerMember]; governed {
				t.Fatalf("threadOpenItem.%s must remain additive viewer state", viewerMember)
			}
		}
	})

	validRoot := filepath.Join(repoRoot, "schemas", "fixtures", "dashboard-view-objects", "valid")
	validPaths, err := filepath.Glob(filepath.Join(validRoot, "*.json"))
	if err != nil {
		t.Fatalf("glob valid dashboard view-object fixtures: %v", err)
	}
	if len(validPaths) != 2 {
		t.Fatalf("valid fixtures: got %d, want exactly 2", len(validPaths))
	}
	sort.Strings(validPaths)
	validFixtures := make(map[string]map[string]any, len(validPaths))
	for _, path := range validPaths {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read valid fixture %s: %v", path, readErr)
		}
		fixture := dashboardReadJSONObject(t, path, raw)
		validFixtures[filepath.Base(path)] = fixture
		dashboardValidateMappedInstances(t, schemaRaw, filepath.Base(path), fixture)
		for definition, instance := range dashboardSupportInstances(t, fixture) {
			t.Run(fmt.Sprintf("valid/%s/support/%s", filepath.Base(path), definition), func(t *testing.T) {
				dashboardRequireValid(t, dashboardCompileDefinition(t, schemaRaw, definition), instance)
			})
		}
	}

	canonical := validFixtures["seven-card-kinds.json"]
	if canonical == nil {
		t.Fatal("valid fixture seven-card-kinds.json was not loaded")
	}
	additive := validFixtures["additive-local-fields.json"]
	if additive == nil {
		t.Fatal("valid fixture additive-local-fields.json was not loaded")
	}

	t.Run("named invalid fixtures reject by definition and member", func(t *testing.T) {
		invalidRoot := filepath.Join(repoRoot, "schemas", "fixtures", "dashboard-view-objects", "invalid")
		invalidPaths, globErr := filepath.Glob(filepath.Join(invalidRoot, "*.json"))
		if globErr != nil {
			t.Fatalf("glob invalid dashboard view-object fixtures: %v", globErr)
		}
		if len(invalidPaths) != 11 {
			t.Fatalf("invalid fixtures: got %d, want exactly 11", len(invalidPaths))
		}
		for _, path := range invalidPaths {
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("read invalid fixture %s: %v", path, readErr)
			}
			fixture := dashboardReadJSONObject(t, path, raw)
			definition := dashboardString(t, fixture, "definition")
			member := dashboardString(t, fixture, "missing")
			instance := dashboardObject(t, fixture, "instance")
			t.Run(fmt.Sprintf("%s/%s/%s", filepath.Base(path), definition, member), func(t *testing.T) {
				dashboardRequireViolation(t, dashboardCompileDefinition(t, schemaRaw, definition), instance, member)
			})
		}
	})

	t.Run("every governed member has mutation teeth", func(t *testing.T) {
		instances := dashboardMutationInstances(t, canonical)
		definitions := dashboardObject(t, schemaDocument, "$defs")
		if len(instances) != len(definitions) {
			t.Fatalf("mutation instance coverage: got %d definitions, schema has %d", len(instances), len(definitions))
		}
		for definition, rawDefinition := range definitions {
			instance, ok := instances[definition]
			if !ok {
				t.Fatalf("schema definition %q has no canonical mutation instance", definition)
			}
			definitionObject, ok := rawDefinition.(map[string]any)
			if !ok {
				t.Fatalf("schema definition %q is %T, want object", definition, rawDefinition)
			}
			properties := dashboardObject(t, definitionObject, "properties")
			required := dashboardStringSet(t, definitionObject["required"])
			sch := dashboardCompileDefinition(t, schemaRaw, definition)

			for member := range required {
				t.Run(fmt.Sprintf("required/%s/%s", definition, member), func(t *testing.T) {
					mutated := dashboardCloneObject(t, instance)
					delete(mutated, member)
					dashboardRequireViolation(t, sch, mutated, member)
				})
			}
			for member, propertySchema := range properties {
				if _, isRequired := required[member]; isRequired {
					continue
				}
				if _, present := instance[member]; !present {
					t.Fatalf("canonical %s instance omits governed optional member %q", definition, member)
				}
				t.Run(fmt.Sprintf("optional-type/%s/%s", definition, member), func(t *testing.T) {
					mutated := dashboardCloneObject(t, instance)
					mutated[member] = dashboardWrongType(t, propertySchema)
					dashboardRequireViolation(t, sch, mutated, member)
				})
			}

			t.Run(fmt.Sprintf("additive/%s/future_member", definition), func(t *testing.T) {
				mutated := dashboardCloneObject(t, instance)
				mutated["future_member"] = map[string]any{"nested": true}
				dashboardRequireValid(t, sch, mutated)
			})
		}
	})

	t.Run("local and viewer fields remain ungoverned", func(t *testing.T) {
		dashboardAssertLocalFixtureTeeth(t, additive)
		for definition, instance := range dashboardSupportInstances(t, additive) {
			dashboardRequireValid(t, dashboardCompileDefinition(t, schemaRaw, definition), instance)
		}
		dashboardValidateMappedInstances(t, schemaRaw, "additive-local-fields.json", additive)
	})

	t.Run("space support objects stay separate from local work", func(t *testing.T) {
		operational := dashboardObject(t, canonical, "operational")
		sources := dashboardObjectSlice(t, operational, "sources")
		spaceSources := dashboardObjectsMatching(t, sources, "kind", "space")
		localSources := dashboardObjectsMatching(t, sources, "kind", "local-work")
		if len(spaceSources) != 1 || len(localSources) != 1 {
			t.Fatalf("source selection: got %d space and %d local-work, want one each", len(spaceSources), len(localSources))
		}
		dashboardRequireValid(t, dashboardCompileDefinition(t, schemaRaw, "spaceSource"), spaceSources[0])

		unavailable := dashboardObjectSlice(t, operational, "unavailable")
		spaceUnavailable := dashboardObjectsMatching(t, unavailable, "source_kind", "space")
		localUnavailable := dashboardObjectsMatching(t, unavailable, "source_kind", "local-work")
		if len(spaceUnavailable) != 1 || len(localUnavailable) != 1 {
			t.Fatalf("unavailable selection: got %d space and %d local-work, want one each", len(spaceUnavailable), len(localUnavailable))
		}
		dashboardRequireValid(t, dashboardCompileDefinition(t, schemaRaw, "spaceUnavailable"), spaceUnavailable[0])
	})

	t.Run("operational identity is space qualified and accent free", func(t *testing.T) {
		rows := dashboardObjectSlice(t, dashboardObject(t, canonical, "operational"), "timeline")
		if len(rows) != 2 {
			t.Fatalf("timeline rows: got %d, want 2", len(rows))
		}
		firstThread := dashboardString(t, rows[0], "thread")
		secondThread := dashboardString(t, rows[1], "thread")
		if firstThread != secondThread {
			t.Fatalf("identity tooth needs the same thread token, got %q and %q", firstThread, secondThread)
		}
		firstIdentity := dashboardString(t, rows[0], "space") + "\x00" + firstThread
		secondIdentity := dashboardString(t, rows[1], "space") + "\x00" + secondThread
		if firstIdentity == secondIdentity {
			t.Fatalf("space-qualified operational identities collapsed: %q", firstIdentity)
		}
		if dashboardSchemaHasProperty(schemaDocument, "accent") {
			t.Fatal("portable schema must not define an accent property")
		}
		if strings.Contains(strings.ToLower(string(schemaRaw)), "accent") {
			t.Fatal("portable schema must not carry card accent policy")
		}
	})

	t.Run("transition family is admitted without a row ceiling", func(t *testing.T) {
		vocabulary := dashboardObject(t, canonical, "vocabulary")
		entries := dashboardObjectSlice(t, vocabulary, "entries")
		if len(entries) == 0 || dashboardString(t, entries[0], "family") != "transition" {
			t.Fatal("canonical vocabulary fixture must carry a transition entry")
		}
		entrySchema := dashboardCompileDefinition(t, schemaRaw, "vocabularyEntry")
		dashboardRequireValid(t, entrySchema, entries[0])

		defs := dashboardObject(t, schemaDocument, "$defs")
		tableDefinition := dashboardObject(t, defs, "vocabularyTable")
		entryArray := dashboardObject(t, dashboardObject(t, tableDefinition, "properties"), "entries")
		if _, exists := entryArray["maxItems"]; exists {
			t.Fatal("vocabularyTable.entries must not encode the observed 96 rows as maxItems")
		}

		mutatedSchema := dashboardCloneObject(t, schemaDocument)
		mutatedDefs := dashboardObject(t, mutatedSchema, "$defs")
		entryDefinition := dashboardObject(t, mutatedDefs, "vocabularyEntry")
		familyDefinition := dashboardObject(t, dashboardObject(t, entryDefinition, "properties"), "family")
		families := dashboardStringSlice(t, familyDefinition["enum"])
		wantFamilies := make([]string, len(dashboardhtml.VocabularyFamilies()))
		for index, family := range dashboardhtml.VocabularyFamilies() {
			wantFamilies[index] = string(family)
		}
		if len(wantFamilies) != 12 {
			t.Fatalf("canonical Go producer has %d vocabulary families, want the frozen 12-family contract", len(wantFamilies))
		}
		if !reflect.DeepEqual(families, wantFamilies) {
			t.Fatalf("vocabulary family enum = %v, want exact Go producer set %v", families, wantFamilies)
		}
		withoutTransition := make([]any, 0, len(families)-1)
		for _, family := range families {
			if family != "transition" {
				withoutTransition = append(withoutTransition, family)
			}
		}
		familyDefinition["enum"] = withoutTransition
		mutatedRaw, marshalErr := json.Marshal(mutatedSchema)
		if marshalErr != nil {
			t.Fatalf("marshal transition-minus-one schema: %v", marshalErr)
		}
		dashboardRequireViolation(t, dashboardCompileDefinition(t, mutatedRaw, "vocabularyEntry"), entries[0], "family")

		ninetySix := make([]any, 0, 97)
		for i := 0; i < 96; i++ {
			entry := dashboardCloneObject(t, entries[0])
			entry["value"] = fmt.Sprintf("additive-%02d", i+1)
			ninetySix = append(ninetySix, entry)
		}
		row97 := dashboardCloneObject(t, entries[0])
		row97["value"] = "additive-97"
		ninetySix = append(ninetySix, row97)
		largeTable := dashboardCloneObject(t, vocabulary)
		largeTable["entries"] = ninetySix
		dashboardRequireValid(t, dashboardCompileDefinition(t, schemaRaw, "vocabularyTable"), largeTable)
	})
}

func dashboardAssertManifestMapping(t *testing.T, repoRoot string) {
	t.Helper()
	type manifestCard struct {
		Kind           string     `json:"kind"`
		Component      string     `json:"component"`
		SelectionPaths [][]string `json:"selectionPaths"`
		IdentityFields []string   `json:"identityFields"`
	}
	var manifest struct {
		Cards []manifestCard `json:"cards"`
	}
	path := filepath.Join(repoRoot, "web", "design-source", "cards.manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read card manifest: %v", err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode card manifest: %v", err)
	}
	if len(manifest.Cards) != len(dashboardViewMappings) {
		t.Fatalf("manifest cards: got %d, mapping has %d", len(manifest.Cards), len(dashboardViewMappings))
	}
	want := make(map[string]dashboardViewMapping, len(dashboardViewMappings))
	for _, row := range dashboardViewMappings {
		if row.kind == "" || row.definition == "" || row.sourcePath == "" || row.component == "" || len(row.selectionPaths) == 0 || len(row.identityFields) == 0 {
			t.Fatalf("incomplete canonical mapping row: %+v", row)
		}
		if _, duplicate := want[row.kind]; duplicate {
			t.Fatalf("duplicate canonical mapping kind %q", row.kind)
		}
		want[row.kind] = row
	}
	seen := make(map[string]struct{}, len(manifest.Cards))
	for _, card := range manifest.Cards {
		row, exists := want[card.Kind]
		if !exists {
			t.Fatalf("manifest kind %q is not in the seven-row mapping", card.Kind)
		}
		if row.component != card.Component || !reflect.DeepEqual(row.selectionPaths, card.SelectionPaths) || !reflect.DeepEqual(row.identityFields, card.IdentityFields) {
			t.Fatalf("manifest kind %q = component %q paths %v identity %v, want component %q paths %v identity %v", card.Kind, card.Component, card.SelectionPaths, card.IdentityFields, row.component, row.selectionPaths, row.identityFields)
		}
		if _, duplicate := seen[card.Kind]; duplicate {
			t.Fatalf("manifest duplicates kind %q", card.Kind)
		}
		seen[card.Kind] = struct{}{}
	}
}

func dashboardValidateMappedInstances(t *testing.T, schemaRaw []byte, fixtureName string, fixture map[string]any) {
	t.Helper()
	for _, row := range dashboardViewMappings {
		matched := dashboardMappedInstances(t, fixture, row)
		if len(matched) == 0 {
			t.Fatalf("fixture %s mapping %s (%s) matched no objects", fixtureName, row.kind, row.sourcePath)
		}
		for index, instance := range matched {
			t.Run(fmt.Sprintf("valid/%s/%s/%d", fixtureName, row.definition, index), func(t *testing.T) {
				dashboardRequireValid(t, dashboardCompileDefinition(t, schemaRaw, row.definition), instance)
			})
		}
	}
}

func dashboardMappedInstances(t *testing.T, fixture map[string]any, row dashboardViewMapping) []map[string]any {
	t.Helper()
	var matched []map[string]any
	for _, path := range row.selectionPaths {
		matched = append(matched, dashboardObjectsAtPath(t, fixture, path)...)
	}
	return matched
}

func dashboardObjectsAtPath(t *testing.T, value any, path []string) []map[string]any {
	t.Helper()
	if len(path) > 0 {
		switch typed := value.(type) {
		case map[string]any:
			next, exists := typed[path[0]]
			if !exists {
				t.Fatalf("selection path %v has no member %q", path, path[0])
			}
			return dashboardObjectsAtPath(t, next, path[1:])
		case []any:
			var objects []map[string]any
			for _, item := range typed {
				objects = append(objects, dashboardObjectsAtPath(t, item, path)...)
			}
			return objects
		default:
			t.Fatalf("selection path %v reached %T", path, value)
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		return []map[string]any{typed}
	case []any:
		objects := make([]map[string]any, len(typed))
		for index, item := range typed {
			object, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("selected object[%d] is %T, want object", index, item)
			}
			objects[index] = object
		}
		return objects
	default:
		t.Fatalf("selected value is %T, want object or array", value)
	}
	return nil
}

func dashboardSupportInstances(t *testing.T, fixture map[string]any) map[string]map[string]any {
	t.Helper()
	operational := dashboardObject(t, fixture, "operational")
	vocabulary := dashboardObject(t, fixture, "vocabulary")
	spaceSources := dashboardObjectsMatching(t, dashboardObjectSlice(t, operational, "sources"), "kind", "space")
	spaceUnavailable := dashboardObjectsMatching(t, dashboardObjectSlice(t, operational, "unavailable"), "source_kind", "space")
	if len(spaceSources) == 0 || len(spaceUnavailable) == 0 {
		t.Fatal("fixture must contain a selected space source and space unavailable fact")
	}
	return map[string]map[string]any{
		"threadSummary":      dashboardObjectSlice(t, fixture, "threads")[0],
		"spaceSource":        spaceSources[0],
		"spaceUnavailable":   spaceUnavailable[0],
		"vocabularyTable":    vocabulary,
		"vocabularyEntry":    dashboardObjectSlice(t, vocabulary, "entries")[0],
		"vocabularyFallback": dashboardObject(t, vocabulary, "unknown"),
		"window":             dashboardObject(t, dashboardObjectSlice(t, operational, "timeline")[0], "work_window"),
	}
}

func dashboardMutationInstances(t *testing.T, fixture map[string]any) map[string]map[string]any {
	t.Helper()
	instances := dashboardSupportInstances(t, fixture)
	for _, row := range dashboardViewMappings {
		matched := dashboardMappedInstances(t, fixture, row)
		if _, exists := instances[row.definition]; !exists {
			instances[row.definition] = matched[0]
		}
	}
	item := instances["item"]
	thread := instances["threadSummary"]
	report := instances["workReport"]
	process := instances["operationalProcess"]
	milestone := dashboardObject(t, process, "latest_milestone")
	work := dashboardObjectSlice(t, process, "work")[0]
	instances["localizedText"] = dashboardObject(t, item, "reasonSentence")
	instances["operationalItem"] = dashboardObjectSlice(t, item, "operationalItems")[0]
	instances["threadOpener"] = dashboardObject(t, thread, "opener")
	instances["threadMember"] = dashboardObjectSlice(t, thread, "members")[0]
	instances["docLink"] = dashboardObjectSlice(t, thread, "links")[0]
	instances["threadWindows"] = dashboardObject(t, thread, "windows")
	threadView := instances["threadView"]
	instances["threadViewOpener"] = dashboardObject(t, threadView, "opener")
	instances["threadViewArtifact"] = dashboardObjectSlice(t, threadView, "artifacts")[0]
	transcript := dashboardObjectSlice(t, threadView, "transcript")
	artifactRows := dashboardObjectsMatching(t, transcript, "kind", "artifact")
	eventRows := dashboardObjectsMatching(t, transcript, "kind", "event")
	derivedRows := dashboardObjectsMatching(t, transcript, "kind", "derived")
	if len(artifactRows) != 1 || len(eventRows) != 1 || len(derivedRows) != 1 {
		t.Fatalf("canonical thread transcript variants = artifact:%d event:%d derived:%d, want one each", len(artifactRows), len(eventRows), len(derivedRows))
	}
	transcriptRow := dashboardCloneObject(t, artifactRows[0])
	transcriptRow["event"] = dashboardObject(t, eventRows[0], "event")
	transcriptRow["derived"] = dashboardObject(t, derivedRows[0], "derived")
	instances["threadTranscriptRow"] = transcriptRow
	instances["transcriptArtifact"] = dashboardObject(t, artifactRows[0], "artifact")
	instances["transcriptEvent"] = dashboardObject(t, eventRows[0], "event")
	instances["transcriptDerived"] = dashboardObject(t, derivedRows[0], "derived")
	openItem := dashboardObjectSlice(t, threadView, "open_items")[0]
	instances["threadOpenItem"] = openItem
	instances["threadNextAction"] = dashboardObjectSlice(t, openItem, "next_actions")[0]
	instances["threadViewFlag"] = dashboardObjectSlice(t, threadView, "flags")[0]
	instances["threadUnresolvedRef"] = dashboardObjectSlice(t, threadView, "unresolved")[0]
	instances["threadViewWindows"] = dashboardObject(t, threadView, "windows")
	delivery := dashboardCloneObject(t, dashboardObjectSlice(t, threadView, "deliveries")[0])
	delivery["unavailable"] = "synthetic optional-member mutation baseline"
	instances["delivery"] = delivery
	instances["deliveryFailure"] = dashboardObjectSlice(t, delivery, "failures")[0]
	instances["deliveryChainEntry"] = dashboardObjectSlice(t, delivery, "chain")[0]
	instances["workReportActor"] = dashboardObject(t, report, "actor")
	instances["workReportWait"] = dashboardObjectSlice(t, report, "waiting_on")[0]
	instances["actor"] = dashboardObject(t, milestone, "actor")
	instances["protocol"] = dashboardObject(t, process, "protocol")
	instances["milestone"] = milestone
	instances["operationalWork"] = work
	instances["committedCheckpoint"] = dashboardObject(t, work, "committed_checkpoint")
	instances["waitingOn"] = dashboardObjectSlice(t, work, "waiting_on")[0]
	instances["consistency"] = dashboardObjectSlice(t, process, "consistency")[0]
	version := instances["contractVersion"]
	detail := dashboardObject(t, version, "detail")
	instances["contractVersionDetail"] = detail
	instances["contractVersionConsumerPin"] = dashboardObjectSlice(t, detail, "consumerPins")[0]
	instances["contractVersionDocument"] = dashboardObjectSlice(t, detail, "documents")[0]
	instances["contractVersionHistory"] = dashboardObjectSlice(t, detail, "history")[0]
	instances["contractVersionProvenance"] = dashboardObject(t, detail, "provenance")
	return instances
}

func dashboardAssertLocalFixtureTeeth(t *testing.T, fixture map[string]any) {
	t.Helper()
	if _, exists := fixture["self"]; exists {
		t.Fatal("additive fixture must remove Data.self")
	}
	if _, ok := fixture["aggregates"].(string); !ok {
		t.Fatal("additive fixture must type-change KPI aggregates")
	}
	if _, ok := fixture["windows"].(bool); !ok {
		t.Fatal("additive fixture must type-change envelope windows")
	}
	item := dashboardObjectSlice(t, fixture, "inbox")[0]
	if _, ok := item["yourMove"].(string); !ok {
		t.Fatal("additive fixture must type-change Item.yourMove")
	}
	prompt := dashboardObject(t, item, "prompt")
	if _, ok := prompt["moves"].(string); !ok {
		t.Fatal("additive fixture must type-change prompt moves")
	}
	if _, ok := prompt["askFirst"].(bool); !ok {
		t.Fatal("additive fixture must type-change prompt askFirst")
	}
	thread := dashboardObjectSlice(t, fixture, "threads")[0]
	if _, ok := thread["yourMove"].(map[string]any); !ok {
		t.Fatal("additive fixture must type-change Thread.yourMove")
	}
	if _, ok := thread["waitingOthers"].(string); !ok {
		t.Fatal("additive fixture must type-change Thread.waitingOthers")
	}
	threadView := dashboardObjectSlice(t, fixture, "threadViews")[0]
	openItem := dashboardObjectSlice(t, threadView, "open_items")[0]
	if _, ok := openItem["your_move"].(string); !ok {
		t.Fatal("additive fixture must type-change ThreadOpenItem.your_move")
	}
	openPrompt := dashboardObject(t, openItem, "prompt")
	if _, ok := openPrompt["moves"].(string); !ok {
		t.Fatal("additive fixture must type-change ThreadOpenItem prompt permissions")
	}
	space := dashboardObjectSlice(t, fixture, "spaces")[0]
	if _, ok := space["repoURL"].(float64); !ok {
		t.Fatal("additive fixture must type-change SpaceHealth.repoURL")
	}
	if _, ok := space["syncAge"].([]any); !ok {
		t.Fatal("additive fixture must type-change SpaceHealth.syncAge")
	}
	operational := dashboardObject(t, fixture, "operational")
	if _, ok := operational["timeline_window"].(string); !ok {
		t.Fatal("additive fixture must type-change Snapshot.timeline_window")
	}
	spaceSource := dashboardObjectsMatching(t, dashboardObjectSlice(t, operational, "sources"), "kind", "space")[0]
	if _, ok := spaceSource["observed_at"].(float64); !ok {
		t.Fatal("additive fixture must type-change space Source.observed_at")
	}
	process := dashboardObjectSlice(t, operational, "timeline")[0]
	protocol := dashboardObject(t, process, "protocol")
	if _, ok := protocol["your_move"].(string); !ok {
		t.Fatal("additive fixture must type-change Protocol.your_move")
	}
	work := dashboardObjectSlice(t, process, "work")[0]
	if _, ok := work["observed_at"].(bool); !ok {
		t.Fatal("additive fixture must type-change Work.observed_at")
	}
	if _, ok := work["shared_pending"].(string); !ok {
		t.Fatal("additive fixture must type-change Work.shared_pending")
	}
	if got := len(dashboardObjectsMatching(t, dashboardObjectSlice(t, operational, "sources"), "kind", "local-work")); got != 1 {
		t.Fatalf("additive fixture local-work sources = %d, want 1", got)
	}
	if got := len(dashboardObjectsMatching(t, dashboardObjectSlice(t, operational, "unavailable"), "source_kind", "local-work")); got != 1 {
		t.Fatalf("additive fixture local-work unavailable = %d, want 1", got)
	}
}

func dashboardCompileDefinition(t *testing.T, schemaRaw []byte, definition string) *dashboardschema.ExternalSchema {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(schemaRaw, &document); err != nil {
		t.Fatalf("decode schema for definition %s: %v", definition, err)
	}
	defs, ok := document["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no object $defs")
	}
	if _, exists := defs[definition]; !exists {
		t.Fatalf("schema has no definition %q", definition)
	}
	document["$ref"] = "#/$defs/" + definition
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal schema definition %s: %v", definition, err)
	}
	sch, err := dashboardschema.CompileExternal("dashboard-view-objects-"+definition, raw)
	if err != nil {
		t.Fatalf("CompileExternal(%s): %v", definition, err)
	}
	return sch
}

func dashboardRequireValid(t *testing.T, sch *dashboardschema.ExternalSchema, instance map[string]any) {
	t.Helper()
	raw, err := json.Marshal(instance)
	if err != nil {
		t.Fatalf("marshal valid instance: %v", err)
	}
	if err := sch.ValidateInstance(raw); err != nil {
		t.Fatalf("expected portable instance to validate: %v\ninstance: %s", err, raw)
	}
}

func dashboardRequireViolation(t *testing.T, sch *dashboardschema.ExternalSchema, instance map[string]any, member string) {
	t.Helper()
	raw, err := json.Marshal(instance)
	if err != nil {
		t.Fatalf("marshal invalid instance for %s: %v", member, err)
	}
	err = sch.ValidateInstance(raw)
	if err == nil {
		t.Fatalf("expected governed member %q to reject mutation\ninstance: %s", member, raw)
	}
	if !errors.Is(err, dashboardschema.ErrExternalSchemaViolation) {
		t.Fatalf("member %q returned non-validation error: %v", member, err)
	}
	if !strings.Contains(err.Error(), member) {
		t.Fatalf("member %q absent from validation diagnostic: %v", member, err)
	}
}

func dashboardReadJSONObject(t *testing.T, path string, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode strict JSON object %s: %v", path, err)
	}
	if value == nil {
		t.Fatalf("decode strict JSON object %s: got null", path)
	}
	return value
}

func dashboardObject(t *testing.T, object map[string]any, member string) map[string]any {
	t.Helper()
	value, ok := object[member].(map[string]any)
	if !ok {
		t.Fatalf("member %q is %T, want object", member, object[member])
	}
	return value
}

func dashboardObjectSlice(t *testing.T, object map[string]any, member string) []map[string]any {
	t.Helper()
	raw, ok := object[member].([]any)
	if !ok {
		t.Fatalf("member %q is %T, want array", member, object[member])
	}
	objects := make([]map[string]any, len(raw))
	for i, value := range raw {
		item, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("member %q[%d] is %T, want object", member, i, value)
		}
		objects[i] = item
	}
	return objects
}

func dashboardObjectsMatching(t *testing.T, objects []map[string]any, member, value string) []map[string]any {
	t.Helper()
	var matching []map[string]any
	for _, object := range objects {
		if raw, ok := object[member].(string); ok && raw == value {
			matching = append(matching, object)
		}
	}
	return matching
}

func dashboardString(t *testing.T, object map[string]any, member string) string {
	t.Helper()
	value, ok := object[member].(string)
	if !ok {
		t.Fatalf("member %q is %T, want string", member, object[member])
	}
	return value
}

func dashboardStringSlice(t *testing.T, value any) []string {
	t.Helper()
	raw, ok := value.([]any)
	if !ok {
		t.Fatalf("value is %T, want array", value)
	}
	result := make([]string, len(raw))
	for i, item := range raw {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("value[%d] is %T, want string", i, item)
		}
		result[i] = text
	}
	return result
}

func dashboardStringSet(t *testing.T, value any) map[string]struct{} {
	t.Helper()
	if value == nil {
		return map[string]struct{}{}
	}
	values := dashboardStringSlice(t, value)
	result := make(map[string]struct{}, len(values))
	for _, item := range values {
		result[item] = struct{}{}
	}
	return result
}

func dashboardCloneObject(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal clone: %v", err)
	}
	var cloned map[string]any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		t.Fatalf("unmarshal clone: %v", err)
	}
	return cloned
}

func dashboardWrongType(t *testing.T, rawSchema any) any {
	t.Helper()
	schema, ok := rawSchema.(map[string]any)
	if !ok {
		t.Fatalf("property schema is %T, want object", rawSchema)
	}
	if _, ref := schema["$ref"].(string); ref {
		return "not-an-object"
	}
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "string":
		return false
	case "boolean", "integer", "number", "array", "object":
		return "wrong-type"
	default:
		t.Fatalf("property schema has no supported single type: %+v", schema)
		return nil
	}
}

func dashboardSchemaHasProperty(value any, wanted string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if properties, ok := typed["properties"].(map[string]any); ok {
			if _, exists := properties[wanted]; exists {
				return true
			}
		}
		for _, child := range typed {
			if dashboardSchemaHasProperty(child, wanted) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if dashboardSchemaHasProperty(child, wanted) {
				return true
			}
		}
	}
	return false
}
