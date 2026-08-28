package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/notes"
)

// TestBuildRegistryExpectedToolCount proves BuildRegistry registers
// exactly the P15 capability-grouped tool set plus P31's standalone
// a2a_whatsnew, spec 05a's a2a_data, and space-notify-2026-08 P6's
// a2a_notify: a2a_read + a2a_new + a2a_submit + a2a_lifecycle +
// a2a_exchange + a2a_contract + a2a_data + a2a_whatsnew + a2a_work +
// a2a_notify = 10 tools (spec 15 §T1/§8 AC #1, extended P31, extended spec
// 05a, extended spec 06). BuildRegistry registers a2a_data AND a2a_notify
// degraded (zero-value DataToolDeps{}/NotifyToolDeps{}, via
// BuildRegistryWithOperations) even though this constructor takes no
// data/notify-operations argument — the same "degraded but present"
// precedent ContractToolOperations{}'s zero value already sets for
// a2a_contract. cmd/a2a/mcp_parity_test.go is the authoritative
// capability-parity check against the CLI's own verb set; this is a
// package-local sanity count.
func TestBuildRegistryExpectedToolCount(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	store := cache.NewStore("beta", t.TempDir(), nil, time.Now, 0)
	fake := &fakeFunnel{}
	write := testWriteDeps(mirrorDir, fake)
	legality := NewLegalityAdapter(mirrorDir, "beta", testManifest())
	newDeps := testNewDeps(t.TempDir())

	registry := BuildRegistry(store, write, mirrorDir, legality, newDeps)
	names := registry.ToolNames()
	want := []string{
		"a2a_contract", "a2a_data", "a2a_exchange", "a2a_lifecycle",
		"a2a_new", "a2a_notify", "a2a_read", "a2a_submit", "a2a_whatsnew", "a2a_work",
	}
	if len(names) != len(want) {
		t.Fatalf("expected %d tools, got %d: %v", len(want), len(names), names)
	}
	for i, name := range want {
		if names[i] != name {
			t.Fatalf("tool set mismatch: want %v, got %v", want, names)
		}
	}

	// The per-verb tool names P14 shipped must NO LONGER be registered —
	// they are folded into the grouped tools' action/view enums.
	removed := []string{
		"a2a_inbox", "a2a_outbox", "a2a_show", "a2a_thread", "a2a_search", "a2a_contracts",
		"a2a_ack", "a2a_accept", "a2a_respond", "a2a_verify", "a2a_dispute", "a2a_note",
		"a2a_contract_new", "a2a_contract_publish",
	}
	for _, name := range removed {
		if _, ok := registry.Get(name); ok {
			t.Errorf("expected folded tool %q to be ABSENT (now an action/view of a grouped tool)", name)
		}
	}

	excluded := []string{"a2a_version", "a2a_init", "a2a_connect", "a2a_disconnect", "a2a_doctor", "a2a_template", "a2a_sync", "a2a_validate", "a2a_mcp"}
	for _, name := range excluded {
		if _, ok := registry.Get(name); ok {
			t.Errorf("expected tool %q to be ABSENT (CLI-only verb)", name)
		}
	}
}

func TestRawSchemaShape(t *testing.T) {
	t.Parallel()
	raw := rawSchema(map[string]propSpec{"ids": {"array", "the id(s) this call applies to"}}, "ids")
	if len(raw) == 0 {
		t.Fatal("expected a non-empty schema")
	}
}

// TestBuildRegistryWithOperationsCarriesSubmitResolver pins the seam that
// went missing once and was invisible to every unit test that owns it.
//
// SubmitDeps grew its own per-space ResolveSpace closure, wire.go installed
// it, and this constructor threw it away — it rebuilt SubmitDeps from a
// (staging dir, legality) pair rather than carrying the caller's own value.
// a2a_submit therefore went on refusing every multi-space write in a real
// session while tools_submit_test.go stayed green throughout, because those
// tests construct SubmitDeps directly and never travel through here. The
// defect lived exactly in the gap between the two.
//
// Anything the caller puts on SubmitDeps must survive registry assembly.
func TestBuildRegistryWithOperationsCarriesSubmitResolver(t *testing.T) {
	t.Parallel()

	staging := t.TempDir()
	writeStagedDraftForSpace(t, staging, "XQ-beta-20260721-reg1", "question", "space-b")

	var asked string
	submit := SubmitDeps{
		WriteDeps:  WriteDeps{OwnSystem: "beta", ReadFile: os.ReadFile},
		StagingDir: staging,
		ResolveSpace: func(spaceID string) (SubmitDeps, error) {
			asked = spaceID
			return SubmitDeps{}, errors.New("sentinel: the resolver reached the handler")
		},
	}

	r := BuildRegistryWithOperations(nil, WriteDeps{OwnSystem: "beta", ReadFile: os.ReadFile},
		submit, NewDeps{}, ContractToolOperations{}, DataToolDeps{})

	spec, ok := r.Get("a2a_submit")
	if !ok {
		t.Fatal("a2a_submit is not registered")
	}
	_, _, err := spec.Handler(context.Background(),
		json.RawMessage(`{"ids":["XQ-beta-20260721-reg1"]}`))
	if err == nil || !strings.Contains(err.Error(), "sentinel: the resolver reached the handler") {
		t.Fatalf("the registered a2a_submit handler did not use the caller's SubmitDeps.ResolveSpace; err = %v", err)
	}
	if asked != "space-b" {
		t.Fatalf("resolver asked for space %q, want the draft's own space-b", asked)
	}
}

// === agent-exchange-2026-08 spec 04: registry-wide schema/decode guards ===

// acSchemaTestRegistry builds a full production-shaped registry (same
// construction TestBuildRegistryExpectedToolCount already uses) purely to
// read every tool's own published InputSchema — none of the tests below
// invoke a handler through it, so degraded/minimal dependencies are fine.
func acSchemaTestRegistry(t *testing.T) *Registry {
	t.Helper()
	mirrorDir := t.TempDir()
	store := cache.NewStore("beta", t.TempDir(), nil, time.Now, 0)
	write := testWriteDeps(mirrorDir, &fakeFunnel{})
	legality := NewLegalityAdapter(mirrorDir, "beta", testManifest())
	newDeps := testNewDeps(t.TempDir())
	return BuildRegistry(store, write, mirrorDir, legality, newDeps)
}

// walkPublishedProperties recurses through node's own "properties" (and,
// for an array-typed property, its "items" own "properties") calling visit
// once per property found, keyed by its full dotted path from the tool
// name. Only a2a_work/a2a_notify's bespoke builders (tools_work.go/
// tools_notify.go) nest "properties" below the top level today —
// rawSchema/groupedSchema publish "object"/"array" properties bare, with
// no inner schema to recurse into — so this walker is a no-op past depth 1
// for every OTHER tool, and exercises the nested case for those two.
func walkPublishedProperties(node map[string]any, path string, visit func(path string, prop map[string]any)) {
	props, ok := node["properties"].(map[string]any)
	if !ok {
		return
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		propRaw, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		propPath := path + "." + name
		visit(propPath, propRaw)
		walkPublishedProperties(propRaw, propPath, visit)
		if items, ok := propRaw["items"].(map[string]any); ok {
			walkPublishedProperties(items, propPath+"[]", visit)
		}
	}
}

// TestEveryPublishedToolPropertyHasADescription is spec 04 AC 4's own
// registry-walking guard: it REDS the moment ANY registered tool's
// InputSchema publishes a property — at ANY nesting depth, a2a_work's own
// actor/waiting_on sub-objects included — with an empty or missing
// description.
func TestEveryPublishedToolPropertyHasADescription(t *testing.T) {
	t.Parallel()
	r := acSchemaTestRegistry(t)
	for _, spec := range r.List() {
		var schema map[string]any
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
			t.Fatalf("%s: unmarshal schema: %v", spec.Name, err)
		}
		walkPublishedProperties(schema, spec.Name, func(path string, prop map[string]any) {
			desc, _ := prop["description"].(string)
			if strings.TrimSpace(desc) == "" {
				t.Errorf("%s: published property has no description", path)
			}
		})
	}
}

// publishedTopLevelProperties returns tool's TOP-LEVEL published property
// name -> JSON Schema "type", read straight off the wire (InputSchema
// itself), not off any internal Go map — AC 3/AC 5 below must see EXACTLY
// what a real MCP client sees.
func publishedTopLevelProperties(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	out := make(map[string]string, len(schema.Properties))
	for name, p := range schema.Properties {
		out[name] = p.Type
	}
	return out
}

// jsonFieldNames reflects over v's exported struct fields and returns the
// set of json tag names decodeStrict would accept for that struct — the
// "honoured" half of AC 3's published ⊆ honoured check, read from the SAME
// struct decodeStrict decodes into, so a field added there is automatically
// honoured with nothing to keep in step by hand.
func jsonFieldNames(v any) map[string]bool {
	out := map[string]bool{}
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.PkgPath != "" { // unexported: never a JSON field
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// toolHonouredInputStructs is AC 3's own registry: for each grouped/
// registered tool, every Go input struct SOME one of its own handlers
// actually decodes (zero values only — jsonFieldNames reads field TAGS,
// never a decoded value). One row per DISTINCT decode-site struct, not per
// action: a2a_lifecycle's 15 generic verbs share ONE struct/decode site
// (tools_dispatch.go's own doc comment), so one LifecycleInput{} row covers
// all 15; a2a_exchange's four verbs decode four DIFFERENT structs, so four
// rows.
var toolHonouredInputStructs = map[string][]any{
	"a2a_read":      {InboxInput{}, OutboxInput{}, ShowInput{}, ThreadInput{}, SearchInput{}, ContractsInput{}},
	"a2a_new":       {NewInput{}},
	"a2a_submit":    {SubmitInput{}},
	"a2a_whatsnew":  {WhatsnewInput{}},
	"a2a_lifecycle": {LifecycleInput{}},
	"a2a_exchange":  {RespondInput{}, VerifyInput{}, DisputeInput{}, NoteInput{}},
	"a2a_contract": {
		ContractNewInput{}, ContractPublishInput{}, ContractPreflightInput{}, ContractMaterializeInput{},
		ContractCheckInput{}, ContractDeprecateInput{}, ContractRetireInput{}, ContractDiffInput{},
		ContractVerifyExportInput{}, ContractAdoptInput{}, ContractActivateInput{},
	},
	"a2a_data":   {DataInput{}},
	"a2a_work":   {WorkInput{}},
	"a2a_notify": {NotifyInput{}},
}

// TestPublishedPropertiesAreHonouredBySomeHandler is spec 04 AC 3's own
// guard: for every tool named in toolHonouredInputStructs, every property
// its OWN published schema advertises must be readable, by NAME, from at
// least one of that tool's own handler input structs — the exact
// silent-drop shape this epic's goal names (an agent reads the published
// schema, sends a field, the input struct has no such field, decode
// discards it without complaint).
//
// One direction only, deliberately (AC 3's own wording: "published ⊆
// honoured"): a field a handler honours but the schema does not YET
// publish is a real, separate gap (this package's spec 04 report says what
// this test's own author found there while fixing it), but it is not what
// this guard exists to catch, and a permanent honoured ⊆ published
// assertion would need its own registry of which fields are DELIBERATELY
// unpublished (generated_from_digest, spec 04) — scope this test does not
// carry.
func TestPublishedPropertiesAreHonouredBySomeHandler(t *testing.T) {
	t.Parallel()
	r := acSchemaTestRegistry(t)
	for tool, structs := range toolHonouredInputStructs {
		spec, ok := r.Get(tool)
		if !ok {
			t.Fatalf("tool %s is not registered", tool)
		}
		honoured := map[string]bool{}
		for _, s := range structs {
			for name := range jsonFieldNames(s) {
				honoured[name] = true
			}
		}
		for name := range publishedTopLevelProperties(t, spec.InputSchema) {
			if !honoured[name] {
				t.Errorf("%s: published property %q is not read by any handler for this tool", tool, name)
			}
		}
	}
}

// TestOneTypePerPublishedFieldNameAcrossTools is spec 04 AC 5's own guard:
// a field name published by more than one REGISTERED tool must publish the
// SAME JSON Schema type everywhere. The concrete defect it exists to catch:
// `refs` was published `array` by a2a_lifecycle and `string` by
// a2a_exchange before this package withdrew the latter (D4) — see this
// package's own spec 04 report for the mutation proof.
func TestOneTypePerPublishedFieldNameAcrossTools(t *testing.T) {
	t.Parallel()
	r := acSchemaTestRegistry(t)
	typeByField := map[string]string{}
	toolByField := map[string]string{}
	for _, spec := range r.List() {
		for name, typ := range publishedTopLevelProperties(t, spec.InputSchema) {
			if want, ok := typeByField[name]; ok {
				if want != typ {
					t.Errorf("field %q is published as %q by %s and %q by %s", name, want, toolByField[name], typ, spec.Name)
				}
				continue
			}
			typeByField[name] = typ
			toolByField[name] = spec.Name
		}
	}
}

// decodeSiteProbe is one (source label, constructed handler) row for
// TestAllDecodeSitesRefuseAnUnknownTopLevelKey below.
type decodeSiteProbe struct {
	name string
	call HandlerFunc
}

// TestAllDecodeSitesRefuseAnUnknownTopLevelKey is spec 04 AC 1/2's own
// proof, over every TYPED-STRUCT decode site this phase's own ground-truth
// grep found: 26 of the 28 `json.Unmarshal(args` sites now converted to
// decodeStrict here, plus the 2 that already called its predecessor shape
// before this phase (tools_data.go/tools_work.go's own two rows below) —
// 28 rows, not merely a grep claiming they call decodeStrict.
//
// D4's own count is 30 (28 + the pre-existing 2). The remaining 2 of the 28
// grep hits — tools_dispatch.go:59's newDispatch probe and tools.go's
// rejectDegradedLegacyContractWrites probe — are DELIBERATELY absent from
// this table, not missed: both decode into a shape with no unknown-field
// concept to refuse (a bare map, and a 1-field struct that forwards the
// FULL untouched args to next()), so routing them through decodeStrict
// would refuse every real call before the actual per-action handler — the
// one that owns this tool's real field set — ever ran. Refusal for the
// tools those two front is proven here anyway, one frame in, by every
// per-action handler row below; see tools_dispatch.go's and tools.go's own
// doc comments for the full reasoning, and this package's spec 04 report
// for the same point stated as a deviation.
//
// Each row calls its handler CONSTRUCTOR directly — bypassing Registry and
// newDispatch entirely — with args carrying ONLY a single, deliberately
// unrecognized top-level key; decodeStrict refuses it before any
// required-field validation runs, so no row needs a realistic, fully wired
// request to prove its own refusal.
//
// The four contract_p6.go rows (and the two tools_contract.go rows that
// gate on deps.Inspection) need NON-nil deps to reach their own decode at
// all — each checks "P6 service is not configured" FIRST — so this test
// reuses the exact fakes tools_contract_p6_test.go already defines
// (mcpP6PublicationFake/mcpP6MaterializeFake/mcpP6CheckFake/
// mcpP6InspectionFake) rather than inventing a third set.
func TestAllDecodeSitesRefuseAnUnknownTopLevelKey(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("beta", t.TempDir(), nil, time.Now, 0)
	publication := &mcpP6PublicationFake{}
	materialize := &mcpP6MaterializeFake{}
	check := &mcpP6CheckFake{}
	inspection := &mcpP6InspectionFake{}

	sites := []decodeSiteProbe{
		{"tools_read.go newInboxHandler", newInboxHandler(store)},
		{"tools_read.go newOutboxHandler", newOutboxHandler(store)},
		{"tools_read.go newShowHandler", newShowHandler(store)},
		{"tools_read.go newThreadHandler", newThreadHandler(store)},
		{"tools_read.go newSearchHandler", newSearchHandler(store)},
		{"tools_read.go newContractsHandler", newContractsHandler(store)},
		{"tools_new.go newNewHandler", newNewHandler(NewDeps{})},
		{"tools_submit.go newSubmitHandler", newSubmitHandler(SubmitDeps{})},
		{"tools_whatsnew.go newWhatsnewHandler", newWhatsnewHandler(
			func() ([]notes.ReleaseNotes, error) { return nil, nil },
			func() ([]notes.Change, error) { return nil, nil },
		)},
		{"tools_lifecycle.go newLifecycleHandler", newLifecycleHandler(LifecycleVerbTable[0], WriteDeps{})},
		{"tools_lifecycle.go newRespondHandler", newRespondHandler(WriteDeps{})},
		{"tools_lifecycle.go newVerifyHandler", newVerifyHandler(WriteDeps{})},
		{"tools_lifecycle.go newDisputeHandler", newDisputeHandler(WriteDeps{})},
		{"tools_lifecycle.go newNoteHandler", newNoteHandler(WriteDeps{})},
		{"tools_contract.go newContractNewHandler", newContractNewHandler(NewDeps{})},
		{"tools_contract.go newContractDeprecateHandler", newContractDeprecateHandler(ContractDeps{})},
		{"tools_contract.go newContractRetireHandler", newContractRetireHandler(ContractDeps{})},
		{"tools_contract.go newContractDiffHandler", newContractDiffHandler(ContractDeps{Inspection: inspection})},
		{"tools_contract.go newContractVerifyExportHandler", newContractVerifyExportHandler(ContractDeps{Inspection: inspection})},
		{"tools_contract.go newContractAdoptHandler", newContractAdoptHandler(ContractDeps{})},
		{"tools_contract.go newContractActivateHandler", newContractActivateHandler(ContractDeps{})},
		{"tools_contract_p6.go newContractPreflightHandler", newContractPreflightHandler(ContractDeps{Publication: publication})},
		{"tools_contract_p6.go newP6ContractPublishHandler", newContractPublishHandler(ContractDeps{Publication: publication})},
		{"tools_contract_p6.go newContractMaterializeHandler", newContractMaterializeHandler(ContractDeps{Materialize: materialize})},
		{"tools_contract_p6.go newContractCheckHandler", newContractCheckHandler(ContractDeps{Check: check})},
		{"tools_notify.go newNotifyHandler", newNotifyHandler(NotifyToolDeps{})},
		// The 2 sites that already called decodeStrict's predecessor shape
		// before this phase (D4's own count: 28 + 2 = 30) — included here so
		// this ONE test proves all 30, not 28 plus a grep claiming the other
		// 2 already passed elsewhere.
		{"tools_data.go newDataHandler", newDataHandler(DataToolDeps{})},
		{"tools_work.go newWorkHandler", newWorkHandler(unavailableWorkToolDeps())},
	}

	if got, want := len(sites), 28; got != want {
		t.Fatalf("decode-site table has %d rows, want exactly %d (D4's ground-truth 30, minus the 2 deliberately-excluded discriminator probes named in this test's own doc comment)", got, want)
	}

	for _, site := range sites {
		site := site
		t.Run(site.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := site.call(context.Background(), json.RawMessage(`{"__mcp_unknown_probe__":true}`))
			if err == nil {
				t.Fatalf("%s: expected an unknown-key refusal, got a nil error", site.name)
			}
			if !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("%s: error = %q, want it to name an unknown field (json.Decoder.DisallowUnknownFields)", site.name, err.Error())
			}
		})
	}
}
