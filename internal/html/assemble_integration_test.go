package html

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/avatar"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
	"gopkg.in/yaml.v3"
)

// TestAssemble_NodesAndContractEdges drives Assemble over a hand-built Store
// (a temp mirror with a manifest + a consumes.yaml) — deterministic, no
// network — and asserts the nodes, per-space health, and the derived
// contract-dependency edge.
func TestAssemble_NodesAndContractEdges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "axon"), 0o755); err != nil {
		t.Fatal(err)
	}
	consumes := "schema: consumes/v1\nsystem: axon\ndependencies:\n" +
		"  - contract: XC-seomatrix-feed-v1\n    major: 1\n    since: 2026-07-01\n"
	if err := os.WriteFile(filepath.Join(dir, "axon", "consumes.yaml"), []byte(consumes), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := space.Manifest{Space: "getvisa", Participants: []space.Participant{
		{System: "axon", Section: "axon/", Org: "r22d222", Status: "active", Owners: []string{"ydnikolaev"}},
		{System: "seomatrix", Section: "seomatrix/", Org: "r22d222", Status: "active", Owners: []string{"xpressmike"}},
	}, Schema: "space/v1", MinBinaryVersion: "0.12.0"}
	workflowDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := "jobs:\n  validate:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@v0.13.0\n"
	if err := os.WriteFile(filepath.Join(workflowDir, "a2a-validate.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	avatarCache, err := avatar.NewCache(cacheDir, func(context.Context, string, string, string) ([]byte, string, string, string, bool, error) {
		return []byte("png-avatar"), "image/png", "https://avatars.githubusercontent.com/u/1?s=64", `"v1"`, false, nil
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := avatarCache.Refresh(context.Background(), []string{"ydnikolaev"}); err != nil {
		t.Fatal(err)
	}
	store := cache.NewStore("axon", cacheDir,
		[]cache.SpaceMirror{{SpaceID: "getvisa", Dir: dir, RepoURL: "https://github.com/r22d222/a2a", Manifest: manifest}},
		time.Now, 0)

	data, err := Assemble(context.Background(), store, "", time.Now())
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(data.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2: %+v", len(data.Nodes), data.Nodes)
	}
	var axon *Node
	for i := range data.Nodes {
		if data.Nodes[i].System == "axon" {
			axon = &data.Nodes[i]
		}
	}
	if axon == nil || !axon.Self || axon.Org != "r22d222" {
		t.Fatalf("axon self node wrong: %+v", axon)
	}
	if !strings.HasPrefix(axon.Avatar, "data:image/png;base64,") {
		t.Fatalf("axon avatar = %q, want validated local data URI", axon.Avatar)
	}

	if len(data.Spaces) != 1 || data.Spaces[0].ParticipantCount != 2 || !data.Spaces[0].Readable {
		t.Fatalf("space health: %+v", data.Spaces)
	}
	if data.Spaces[0].SchemaVersion != "space/v1" ||
		data.Spaces[0].MinBinaryVersion != "0.12.0" ||
		data.Spaces[0].WorkflowVersion != "0.13.0" {
		t.Fatalf("space compatibility axes not assembled: %+v", data.Spaces[0])
	}
	if len(data.ReleaseNotes) == 0 {
		t.Fatal("embedded release-note corpus must be available to the HTML data model")
	}

	if len(data.ContractEdges) != 1 {
		t.Fatalf("contract edges = %d, want 1: %+v", len(data.ContractEdges), data.ContractEdges)
	}
	e := data.ContractEdges[0]
	if e.From != "axon" || e.To != "seomatrix" || e.Contract != "XC-seomatrix-feed-v1" ||
		e.PinnedMajor != 1 || e.Drift != "dangling" || e.Space != "getvisa" {
		t.Fatalf("contract edge wrong: %+v", e)
	}

	if data.Self != "axon" {
		t.Fatalf("self = %q, want axon", data.Self)
	}
}

func TestContractDependencyMappingCarriesDegradedScan(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, section := range []string{"atlas", "checkout"} {
		if err := os.MkdirAll(filepath.Join(dir, section), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "atlas", "consumes.yaml"), []byte(
		"schema: consumes/v1\nsystem: atlas\ndependencies:\n  - contract: XC-checkout-api\n    major: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checkout", "consumes.yaml"), []byte("not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := space.Manifest{Space: "space-sentinel", Participants: []space.Participant{
		{System: "atlas", Section: "atlas/", Status: "active"},
		{System: "checkout", Section: "checkout/", Status: "active"},
	}}
	store := cache.NewStore("atlas", t.TempDir(), []cache.SpaceMirror{{SpaceID: manifest.Space, Dir: dir, Manifest: manifest}}, time.Now, 0)
	data, err := Assemble(t.Context(), store, "atlas", time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(data.ContractEdges) != 1 || data.ContractEdges[0].From != "atlas" || data.ContractEdges[0].To != "checkout" ||
		data.ContractEdges[0].Space != manifest.Space || data.ContractEdges[0].Contract != "XC-checkout-api" ||
		data.ContractEdges[0].PinnedMajor != 1 || data.ContractEdges[0].Drift != cache.DependencyDriftDangling {
		t.Fatalf("mapped dependency edge = %+v", data.ContractEdges)
	}
	if data.Windows.ContractEdges != (operational.Window{Total: 1, Shown: 1}) {
		t.Fatalf("root dependency window = %+v", data.Windows.ContractEdges)
	}
	lines := data.Aggregates.LinesOffCurrent
	if lines.Complete || len(lines.Items) != 1 || lines.Window != (operational.Window{Total: 1, Shown: 1}) {
		t.Fatalf("aggregate dependency projection = %+v", lines)
	}
	if len(lines.Degradations) != 1 {
		t.Fatalf("dependency degradations = %+v, want one malformed registry", lines.Degradations)
	}
	degradation := lines.Degradations[0]
	if degradation.Space != manifest.Space || degradation.System != "checkout" || degradation.Path != "checkout/consumes.yaml" ||
		degradation.Code != cache.DependencyDegradationMalformedRegistry || strings.TrimSpace(degradation.Reason) == "" {
		t.Fatalf("mapped dependency degradation = %+v", degradation)
	}
}

func TestViewModelMappingOperationalSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	manifest := space.Manifest{Space: "space-sentinel", Participants: []space.Participant{}}
	store := cache.NewStore("atlas", t.TempDir(), []cache.SpaceMirror{{SpaceID: manifest.Space, Dir: dir, Manifest: manifest}}, func() time.Time { return now }, 0)
	validUntil := now.Add(time.Hour)
	snapshot := operational.Snapshot{
		SchemaVersion: operational.SchemaVersion, GeneratedAt: now, Revision: "sha256:" + strings.Repeat("a", 64),
		Sources: []operational.Source{{Kind: operational.SourceSpace, Space: manifest.Space, Revision: "revision-sentinel", Freshness: operational.SourceCurrent, SyncedAt: &now}},
		Timeline: []operational.TimelineRow{{
			Space: manifest.Space, Thread: "thread:sentinel", Title: "title-sentinel", Participants: []string{"atlas"},
			Protocol: operational.Protocol{WaitingOn: []string{}, BlockingBy: []string{}},
			Work: []operational.Work{{
				WorkID: "work:01ARZ3NDEKTSV4RRFFQ69G5FAV", SubjectRef: "XW-sentinel", Mode: workreport.ModeTesting,
				Summary: "summary-sentinel", Actor: operational.Actor{Kind: "agent", Name: "codex", System: "atlas", Session: "session-sentinel"},
				Freshness: operational.FreshnessLocalCurrent, Current: true, ValidUntil: &validUntil,
				Source: "local-lease", WaitingOn: []workreport.WaitingOn{},
			}},
			WorkWindow:        operational.Window{Total: 3, Shown: 1, Truncated: true},
			ConsistencyWindow: operational.Window{Total: 2, Shown: 1, Truncated: true},
		}},
		TimelineWindow: operational.Window{Total: 4, Shown: 1, Truncated: true},
		Unavailable:    []operational.Unavailable{},
	}
	got, err := AssembleWithOperational(t.Context(), store, "atlas", now, snapshot)
	if err != nil {
		t.Fatalf("AssembleWithOperational: %v", err)
	}
	if !reflect.DeepEqual(got.Operational, snapshot) {
		t.Fatalf("AssembleWithOperational dropped source field Operational\n got: %#v\nwant: %#v", got.Operational, snapshot)
	}
}

func TestViewModelMappingSpaceSynced(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(gitDir, "HEAD")
	if err := os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	syncedAt := now.Add(-2 * time.Minute)
	if err := os.Chtimes(head, syncedAt, syncedAt); err != nil {
		t.Fatal(err)
	}
	manifest := space.Manifest{Space: "space-sentinel", Participants: []space.Participant{}}
	store := cache.NewStore("atlas", t.TempDir(), []cache.SpaceMirror{{SpaceID: manifest.Space, Dir: dir, Manifest: manifest}}, func() time.Time { return now }, 5*time.Minute)
	data, err := Assemble(t.Context(), store, "atlas", now)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(data.Spaces) != 1 || !data.Spaces[0].Synced || data.Spaces[0].Stale || data.Spaces[0].SyncAge != "2m" {
		t.Fatalf("SpaceHealth dropped source field Synced or freshness facts: %+v", data.Spaces)
	}
}

func TestAssembleDeterministic(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	manifest := space.Manifest{Space: "space-sentinel", Participants: []space.Participant{}}
	store := cache.NewStore("atlas", t.TempDir(), []cache.SpaceMirror{{SpaceID: manifest.Space, Dir: t.TempDir(), Manifest: manifest}}, func() time.Time { return now }, 0)
	first, err := Assemble(t.Context(), store, "atlas", now)
	if err != nil {
		t.Fatalf("first Assemble: %v", err)
	}
	second, err := Assemble(t.Context(), store, "atlas", now)
	if err != nil {
		t.Fatalf("second Assemble: %v", err)
	}
	firstJSON, err := CanonicalViewModelJSON(first)
	if err != nil {
		t.Fatalf("first CanonicalViewModelJSON: %v", err)
	}
	secondJSON, err := CanonicalViewModelJSON(second)
	if err != nil {
		t.Fatalf("second CanonicalViewModelJSON: %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("two complete assemblies differ: first=%d bytes second=%d bytes", len(firstJSON), len(secondJSON))
	}
}

func TestSpaceWorkflowVersion_MixedRefsAreExplicit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	workflowDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "jobs:\n" +
		"  first:\n    uses: org/repo/.github/workflows/a2a-validate-reusable.yml@v0.12.0\n" +
		"  second:\n    uses: org/repo/.github/workflows/a2a-validate-reusable.yml@v0.13.0\n"
	if err := os.WriteFile(filepath.Join(workflowDir, "a2a-validate.yml"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	version, ref := space.WorkflowVersion(dir)
	if version != "mixed" ||
		ref != "org/repo/.github/workflows/a2a-validate-reusable.yml@v0.12.0, org/repo/.github/workflows/a2a-validate-reusable.yml@v0.13.0" {
		t.Fatalf("mixed workflow refs not explicit: version=%q ref=%q", version, ref)
	}
}

// writeArtifact writes a *.md artifact at dir/relPath with the given
// envelope/v1 fields (a plain map — this test's own minimal envelope
// authoring, mirroring internal/cache's own fixtureSpace.commitArtifact
// shape but WITHOUT git: buildIndex's own commitOrder degrades to
// OrderKnown=false / Order="declared" when no commit history is readable,
// which is exactly what this test wants — member order falls back to each
// envelope's own `created` field).
func writeArtifact(t *testing.T, dir, relPath string, fields map[string]any, body string) {
	t.Helper()
	raw, err := yaml.Marshal(fields)
	if err != nil {
		t.Fatalf("writeArtifact: marshal envelope: %v", err)
	}
	full := "---\n" + string(raw) + "---\n" + body
	fullPath := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAssemble_Threads is spec 46 §T6.1's own acceptance shape: a
// two-member thread (a work_request + its response, linked by the
// response's own `parent` field) appears in Data.Threads with both members
// and the `parent` DocLink between them, in transcript order; a newly opened
// one-member thread appears too, so the Threads page does not hide work until
// the first response arrives.
func TestAssemble_Threads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

	const hostileTitle = `<script>alert(1)</script> question`
	const threadA = "thread:axon-20260701-tt01"
	const parentID = "XW-axon-20260701-q1"
	const responseID = "XS-seomatrix-20260701-r1"
	writeArtifact(t, dir, "axon/exchanges/"+parentID+".md", map[string]any{
		"schema": "envelope/v1", "id": parentID, "type": "work_request", "title": hostileTitle,
		"space": "getvisa", "from": "axon", "to": []string{"seomatrix"}, "thread": threadA,
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": "2026-07-01T10:00:00Z", "priority": "p2", "blocking": false, "classification": "internal",
	}, "question body")
	writeArtifact(t, dir, "seomatrix/exchanges/"+responseID+".md", map[string]any{
		"schema": "envelope/v1", "id": responseID, "type": "response", "title": "response",
		"space": "getvisa", "from": "seomatrix", "to": []string{"axon"}, "parent": parentID,
		"result": "answered", "thread": threadA,
		"actor":   map[string]any{"kind": "agent", "name": "seomatrix-bot"},
		"created": "2026-07-01T10:05:00Z", "priority": "p2", "blocking": false, "classification": "internal",
	}, "response body")

	const threadB = "thread:axon-20260702-tt02"
	const soloID = "XW-axon-20260702-solo"
	writeArtifact(t, dir, "axon/exchanges/"+soloID+".md", map[string]any{
		"schema": "envelope/v1", "id": soloID, "type": "work_request", "title": "solo, no reply yet",
		"space": "getvisa", "from": "axon", "to": []string{"seomatrix"}, "thread": threadB,
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": "2026-07-02T09:00:00Z", "priority": "p2", "blocking": false, "classification": "internal",
	}, "solo body")

	manifest := space.Manifest{Space: "getvisa", Participants: []space.Participant{
		{System: "axon", Section: "axon/", Org: "r22d222", Status: "active", Owners: []string{"ydnikolaev"}},
		{System: "seomatrix", Section: "seomatrix/", Org: "r22d222", Status: "active", Owners: []string{"xpressmike"}},
	}}
	store := cache.NewStore("axon", t.TempDir(),
		[]cache.SpaceMirror{{SpaceID: "getvisa", Dir: dir, RepoURL: "https://github.com/r22d222/a2a", Manifest: manifest}},
		func() time.Time { return now }, 0)

	data, err := Assemble(context.Background(), store, "", now)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(data.Threads) != 2 {
		t.Fatalf("Threads = %d, want 2 (including the newly opened solo thread): %+v", len(data.Threads), data.Threads)
	}
	var th, soloThread Thread
	for _, candidate := range data.Threads {
		switch candidate.ID {
		case threadA:
			th = candidate
		case threadB:
			soloThread = candidate
		}
	}
	if th.ID != threadA || th.Space != "getvisa" || th.MemberCount != 2 {
		t.Fatalf("thread wrong: %+v", th)
	}
	if soloThread.ID != threadB || soloThread.MemberCount != 1 || soloThread.Opener.ID != soloID {
		t.Fatalf("solo thread wrong: %+v", soloThread)
	}
	if th.Opener.ID != parentID {
		t.Fatalf("opener = %+v, want the earlier-created work_request %s", th.Opener, parentID)
	}
	// The hostile title reaches the model VERBATIM here (Go struct field);
	// Render()'s own json.MarshalIndent escaping — asserted generically in
	// html_test.go's TestRender_SanitizesScriptBreakout — is what keeps it
	// from executing once injected into the page.
	if th.Opener.Title != hostileTitle {
		t.Fatalf("opener title = %q, want %q verbatim on the model", th.Opener.Title, hostileTitle)
	}
	if len(th.Members) != 2 {
		t.Fatalf("members = %d, want 2: %+v", len(th.Members), th.Members)
	}
	if th.Members[0].ID != parentID || th.Members[1].ID != responseID {
		t.Fatalf("members not in transcript order: %+v", th.Members)
	}
	// Whose move it is must never name self: YourMove already says that, and
	// repeating it turns "you and seomatrix" into "you, you and seomatrix".
	for _, who := range th.WaitingOthers {
		if who == "axon" {
			t.Fatalf("WaitingOthers names self: %v", th.WaitingOthers)
		}
	}
	if len(th.Links) != 1 {
		t.Fatalf("links = %d, want 1 (the response's own parent link): %+v", len(th.Links), th.Links)
	}
	link := th.Links[0]
	if link.From != responseID || link.To != parentID || link.Kind != "parent" {
		t.Fatalf("link wrong: %+v", link)
	}

	if len(data.ArtifactDetails) != 3 {
		t.Fatalf("ArtifactDetails = %d, want the 3 visible inbox/outbox records: %+v", len(data.ArtifactDetails), data.ArtifactDetails)
	}
	byDetailID := map[string]ArtifactDetail{}
	for _, detail := range data.ArtifactDetails {
		byDetailID[detail.ID] = detail
	}
	responseDetail, ok := byDetailID[responseID]
	if !ok {
		t.Fatalf("response detail %s missing: %+v", responseID, data.ArtifactDetails)
	}
	if responseDetail.Space != "getvisa" || responseDetail.Path != "seomatrix/exchanges/"+responseID+".md" || responseDetail.Body != "response body" || responseDetail.BodyHTML != "<p>response body</p>\n" ||
		responseDetail.Envelope["parent"] != parentID || responseDetail.SourceClass != "canonical" {
		t.Fatalf("response detail is not the canonical show projection: %+v", responseDetail)
	}
	parentDetail, ok := byDetailID[parentID]
	if !ok || parentDetail.Title != hostileTitle || parentDetail.Envelope["title"] != hostileTitle {
		t.Fatalf("hostile title was not preserved as untrusted detail data: %+v", parentDetail)
	}
	for _, fact := range data.Unavailable {
		if fact.ID == "artifact-detail-bodies" {
			t.Fatalf("fulfilled artifact detail capability still reported unavailable: %+v", fact)
		}
	}

	// The agent prompt's facts must reach the row for a lone document nobody has
	// replied to yet — exactly the case where a human beats their agent to it.
	byRowID := map[string]Item{}
	for _, row := range append(append([]Item{}, data.Inbox...), data.Outbox...) {
		byRowID[row.ID] = row
	}
	solo, ok := byRowID[soloID]
	if !ok {
		t.Fatalf("the solo work request is on no exchange row: %+v", data.Outbox)
	}
	// AMENDED 2026-08-07 — this assertion used to REQUIRE a prompt here, and
	// the moves it accepted were `cancel, withdraw, supersede, close, note`.
	//
	// That is spec 11 §8.1's defect written as a test. The solo work request is
	// SUBMITTED and unanswered: pendency puts the next move on seomatrix
	// (`acknowledge`), and nobody is waiting for axon to withdraw or supersede
	// anything. Offering those to an agent is exactly what the operator
	// reported — "the dashboard still said it was the operator's turn and
	// offered a prompt to hand to an agent. By the protocol nothing was pending
	// on the operator."
	//
	// `next_actions` still lists every move the protocol would ACCEPT — that is
	// not what changed. A PROMPT is an instruction, and it now appears only
	// where the relation names this system as owing the next move.
	if solo.Prompt != nil {
		t.Fatalf("%s is submitted and its next move belongs to seomatrix, yet it offers an agent prompt: %+v",
			soloID, solo.Prompt)
	}
	// ...and the rule must not have simply killed every prompt. The response
	// addressed TO us is one we owe a move on, so it keeps its facts.
	owed, ok := byRowID[responseID]
	if !ok {
		t.Fatalf("the incoming response is on no exchange row: %+v", data.Inbox)
	}
	if owed.Prompt == nil || len(owed.Prompt.Moves) == 0 {
		t.Fatalf("%s is addressed to us and pendency puts the move on us, yet it carries no prompt: %+v",
			responseID, owed)
	}
	if !strings.Contains(owed.Prompt.Loop, "§8.3") {
		t.Fatalf("%s came to us, so it belongs to the receive loop; got %q", responseID, owed.Prompt.Loop)
	}
	// The label and the creation sort key are two formattings of one instant, so a row
	// can never carry an age the exchange list cannot order it by. (These
	// artifacts are written without git history, so both are legitimately
	// empty here; what must not happen is one without the other.)
	for id, row := range byRowID {
		if (row.Age == "") != (row.CreatedAt == "") {
			t.Fatalf("%s: age=%q but CreatedAt=%q — the label and the sort key come from one instant", id, row.Age, row.CreatedAt)
		}
	}

	// Operational work is a separate overview read model. Injecting it must
	// not reinterpret the existing attention queue: identity, order, reasons,
	// and prompt moves are owned by the protocol projection above.
	if len(data.Inbox) == 0 {
		t.Fatal("fixture did not produce an attention queue")
	}
	validUntil := now.Add(time.Hour)
	operationalSnapshot := operational.Snapshot{
		SchemaVersion: operational.SchemaVersion, GeneratedAt: now, Revision: "sha256:" + strings.Repeat("a", 64),
		Sources: []operational.Source{},
		Timeline: []operational.TimelineRow{{
			Space: "getvisa", Thread: threadB, Title: "solo, no reply yet",
			Participants: []string{"axon", "seomatrix"}, Protocol: operational.Protocol{WaitingOn: []string{}, BlockingBy: []string{}},
			Work: []operational.Work{{
				WorkID: "work:01ARZ3NDEKTSV4RRFFQ69G5FAV", SubjectRef: soloID, Mode: workreport.ModeImplementing,
				Summary: "Implementing ingest", Actor: operational.Actor{Kind: "agent", Name: "codex", System: "axon", Session: "session-a"},
				Freshness: operational.FreshnessLocalCurrent, Current: true, ValidUntil: &validUntil,
				Source: "local-lease", WaitingOn: []workreport.WaitingOn{},
			}},
		}},
		Unavailable: []operational.Unavailable{},
	}
	injected, err := AssembleWithOperational(context.Background(), store, "", now, operationalSnapshot)
	if err != nil {
		t.Fatalf("AssembleWithOperational: %v", err)
	}
	if !reflect.DeepEqual(data.Inbox, injected.Inbox) {
		t.Fatalf("operational work changed attention queue\nbefore: %#v\n after: %#v", data.Inbox, injected.Inbox)
	}
	if !reflect.DeepEqual(injected.Operational, operationalSnapshot) {
		t.Fatalf("AssembleWithOperational dropped source field Operational\n got: %#v\nwant: %#v", injected.Operational, operationalSnapshot)
	}
}
