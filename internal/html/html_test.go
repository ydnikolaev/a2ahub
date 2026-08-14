package html

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

func TestNoteProjectionReachesThreadAndArtifactDetail(t *testing.T) {
	t.Parallel()
	note := "A committed note must remain readable."
	thread := toThreadView(cache.ThreadResult{
		Thread: "thread:test", Space: "space", Order: cache.ThreadOrderCommitted,
		Transcript: []cache.TranscriptEntry{{
			Kind: "event",
			Event: &cache.TranscriptEvent{
				ULID: "01NOTE", Subject: "XW-test", Transition: "note", Note: note,
				Actor: cache.TranscriptEventActor{Kind: "agent", Name: "agent", System: "axon"},
			},
		}},
	}, "axon")
	if got := thread.Transcript[0].Event.Note; got != note {
		t.Fatalf("thread note = %q, want %q", got, note)
	}

	detail, err := toArtifactDetail(cache.ShowResult{
		ID: "XW-test", Type: "work_request", Title: "Test", Body: "body",
		Events: []cache.EventSummary{{ULID: "01NOTE", Transition: "note", Note: note}},
	})
	if err != nil {
		t.Fatalf("toArtifactDetail: %v", err)
	}
	if got := detail.Events[0].Note; got != note {
		t.Fatalf("artifact note = %q, want %q", got, note)
	}
}

func TestCommittedWorkProjectionReachesThreadAndHistoryCollection(t *testing.T) {
	t.Parallel()
	reportedAt := time.Date(2026, 8, 4, 9, 30, 0, 0, time.UTC)
	thread := toThreadView(cache.ThreadResult{
		Thread: "thread:test", Space: "space", Order: cache.ThreadOrderCommitted,
		Transcript: []cache.TranscriptEntry{{
			Seq: 7, Kind: "artifact", At: reportedAt,
			Artifact: &cache.TranscriptArtifact{
				ID: "XA-test-status", Type: "announcement", From: "axon", Title: "Checkpoint",
				Work: &cache.TranscriptWorkReport{
					ArtifactID: "XA-test-status", WorkID: "work:01K20ABCDEFHJKMNPQRSTVWXYZ",
					SubjectRef: "XW-test", Mode: workreport.ModeImplementing, Summary: "Implementing the fix",
					Actor:      cache.TranscriptEventActor{Kind: "agent", Name: "codex", System: "axon"},
					ReportedAt: reportedAt, ValidUntil: reportedAt.Add(30 * time.Minute), CommitSequence: 7,
				},
			},
		}},
	}, "axon")
	if thread.Transcript[0].Artifact == nil || thread.Transcript[0].Artifact.Work == nil {
		t.Fatalf("thread lost committed work report: %+v", thread.Transcript)
	}
	reports := collectWorkReports([]ThreadView{thread})
	if len(reports) != 1 || reports[0].SubjectRef != "XW-test" || reports[0].ReportedAt != reportedAt.Format(time.RFC3339Nano) {
		t.Fatalf("collected work reports = %+v", reports)
	}
}

func TestMaxPriority(t *testing.T) {
	t.Parallel()
	if maxPriority("p2", "p1") != "p1" || maxPriority("p1", "p3") != "p1" || maxPriority("", "p3") != "p3" {
		t.Fatal("maxPriority ranking wrong")
	}
}

func TestHumanizeAge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{0, "just now"},
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{2 * 24 * time.Hour, "2d"},
		{3 * 7 * 24 * time.Hour, "3w"},
	}
	for _, c := range cases {
		if got := humanizeAge(now, now.Add(-c.ago)); got != c.want {
			t.Errorf("humanizeAge(-%s) = %q, want %q", c.ago, got, c.want)
		}
	}
	if humanizeAge(now, time.Time{}) != "" {
		t.Error("zero time should format empty")
	}
}

func TestSeverityOf(t *testing.T) {
	t.Parallel()
	if severityOf(cache.Item{Blocking: true}, false) != "blocking" {
		t.Error("blocking item")
	}
	if severityOf(cache.Item{Priority: "p1"}, false) != "blocking" {
		t.Error("p1 item")
	}
	if severityOf(cache.Item{}, true) != "blocking" {
		t.Error("gate-pending item")
	}
	if severityOf(cache.Item{SyncStale: true}, false) != "attention" {
		t.Error("stale item")
	}
	if severityOf(cache.Item{Priority: "p3"}, false) != "normal" {
		t.Error("normal item")
	}
}

func TestExchangeEdges(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	items := []cache.Item{
		{Space: "getvisa", From: "seomatrix", To: []string{"axon"}, Priority: "p2", LatestEventAt: now.Add(-5 * 24 * time.Hour)},
		{Space: "getvisa", From: "seomatrix", To: []string{"axon"}, Priority: "p1", Blocking: true, LatestEventAt: now.Add(-2 * time.Hour)},
	}
	edges := exchangeEdges(items, now)
	if len(edges) != 1 {
		t.Fatalf("want 1 aggregated edge, got %d", len(edges))
	}
	e := edges[0]
	if e.From != "seomatrix" || e.To != "axon" || e.Count != 2 || !e.Blocking || e.MaxPriority != "p1" {
		t.Fatalf("bad aggregation: %+v", e)
	}
	if e.MaxStale != "5d" { // oldest of the two
		t.Fatalf("MaxStale = %q, want 5d", e.MaxStale)
	}
}

// bothRegionsTmpl is a minimal template carrying both injection regions, in
// file order (DATA before DOCS) — the real template's layout, so the DOCS
// index-recompute path is exercised.
const bothRegionsTmpl = "<script>const DATA = /*A2A_DATA_START*/{}/*A2A_DATA_END*/;</script>\n" +
	"<script>const DOCS = /*A2A_DOCS_START*/[]/*A2A_DOCS_END*/;</script>\n"

// TestToItem_CarriesDescription guards the D-001 plumbing: a cache.Item's
// Description (from the artifact body) reaches the model Item the dashboard
// renders. The mapper is otherwise field-copy, so one assertion suffices.
func TestToItem_CarriesDescription(t *testing.T) {
	t.Parallel()
	m := toItem(cache.Item{ID: "XQ-x", Description: "why this matters"}, time.Now(), "checkout", openItemIndex{})
	if m.Description != "why this matters" {
		t.Fatalf("Description = %q, want it carried through", m.Description)
	}
}

func TestRender_InjectsBothRegions(t *testing.T) {
	t.Parallel()
	docs := []DocSection{{ID: "x", Group: "Start", Title: "X", HTML: "<h2>hi</h2>"}}
	out, err := Render([]byte(bothRegionsTmpl), Data{Self: "axon"}, docs)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"self":"axon"`)) {
		t.Fatalf("DATA not injected:\n%s", out)
	}
	// DOCS is injected as a section object; its html is JSON-escaped like DATA
	// (the <script>-breakout protection is asserted in TestRender_Sanitizes...).
	if !bytes.Contains(out, []byte(`"id": "x"`)) || !bytes.Contains(out, []byte(`"html":`)) {
		t.Fatalf("DOCS section not injected:\n%s", out)
	}
	// NEITHER marker pair may survive — a stale DOCS offset (from not recomputing
	// after the DATA splice) would leave a marker or misplace the JSON.
	for _, m := range []string{"A2A_DATA_START", "A2A_DATA_END", "A2A_DOCS_START", "A2A_DOCS_END"} {
		if bytes.Contains(out, []byte(m)) {
			t.Fatalf("marker %s leaked into output:\n%s", m, out)
		}
	}
	if !bytes.Contains(out, []byte("const DOCS = [")) {
		t.Fatalf("DOCS global not well-formed:\n%s", out)
	}
}

func TestRender_MissingMarkers(t *testing.T) {
	t.Parallel()
	// Missing DATA markers entirely.
	if _, err := Render([]byte("no markers here"), Data{}, nil); err == nil {
		t.Fatal("want error on a template missing the DATA markers")
	}
	// DATA present but DOCS markers missing must also fail loud (half a page).
	dataOnly := "const DATA = /*A2A_DATA_START*/{}/*A2A_DATA_END*/;\n"
	if _, err := Render([]byte(dataOnly), Data{}, nil); err == nil {
		t.Fatal("want error on a template missing the DOCS markers")
	}
}

// TestRender_SanitizesScriptBreakout: a hostile title containing "</script>"
// must be escaped in the injected JSON so it cannot break out of the <script>
// block (json.Marshal escapes < as \u003c).
func TestRender_SanitizesScriptBreakout(t *testing.T) {
	t.Parallel()
	hostile := `</script><img src=x onerror=alert(1)>`
	out, err := Render([]byte(bothRegionsTmpl), Data{Inbox: []Item{{Title: hostile}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("</script><img")) {
		t.Fatalf("hostile title broke out of the script block:\n%s", out)
	}
	if !bytes.Contains(out, []byte(`</script>`)) {
		t.Fatalf("expected escaped < in the injected JSON:\n%s", out)
	}
}

// TestRender_SanitizesThreadTitle is TestRender_SanitizesScriptBreakout's own
// shape, for the newly-surfaced Thread.Opener/Thread.Members titles (spec 46
// §T6.1): a hostile title anywhere in a Thread must not break out of the
// <script> block either — same encoding/json escaping, exercised over
// the new struct and canonical compact encoder.
func TestRender_SanitizesThreadTitle(t *testing.T) {
	t.Parallel()
	hostile := `</script><img src=x onerror=alert(1)>`
	data := Data{Threads: []Thread{{
		ID: "thread:x", Opener: ThreadOpener{ID: "XW-x", Title: hostile},
		Members: []ThreadMember{{ID: "XW-x", Title: hostile}},
	}}}
	out, err := Render([]byte(bothRegionsTmpl), data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("</script><img")) {
		t.Fatalf("hostile thread title broke out of the script block:\n%s", out)
	}
	if !bytes.Contains(out, []byte(`</script>`)) {
		t.Fatalf("expected escaped < in the injected JSON:\n%s", out)
	}
}

// TestRender_MarkerInTitleIsInert guards the two-region injection against an
// untrusted title that contains the literal DOCS marker string: because both
// regions are located on the ORIGINAL template (never re-scanned over the
// DATA-spliced buffer), the marker-shaped title is inert data — the real DOCS
// content still lands, and the page is not corrupted.
func TestRender_MarkerInTitleIsInert(t *testing.T) {
	t.Parallel()
	hostile := "pwn" + docsStart + "INJECTED" + docsEnd
	docs := []DocSection{{ID: "real", Group: "Start", Title: "Real", HTML: "<h2>real</h2>"}}
	out, err := Render([]byte(bothRegionsTmpl), Data{Inbox: []Item{{Title: hostile}}}, docs)
	if err != nil {
		t.Fatal(err)
	}
	// The real DOCS section survives (not truncated by a false marker match).
	if !bytes.Contains(out, []byte(`"id": "real"`)) {
		t.Fatalf("real DOCS content lost to a marker-shaped title:\n%s", out)
	}
	// The hostile title is present as escaped JSON data, not as a live marker.
	if !bytes.Contains(out, []byte(`"title":`)) {
		t.Fatalf("hostile item not injected as data:\n%s", out)
	}
	// Exactly one DOCS global — no second, corrupt one from a false splice.
	if n := bytes.Count(out, []byte("const DOCS = [")); n != 1 {
		t.Fatalf("want exactly one DOCS global, got %d:\n%s", n, out)
	}
}

func TestDefaultTemplate_HasMarkers(t *testing.T) {
	t.Parallel()
	tmpl := DefaultTemplate()
	for _, m := range []string{dataStart, dataEnd, docsStart, docsEnd} {
		if !strings.Contains(string(tmpl), m) {
			t.Fatalf("embedded template.html is missing marker %s", m)
		}
	}
	// It must render without error over the REAL docs assembler (markers present
	// + valid, and the embedded skill tree renders).
	docs, err := Docs()
	if err != nil {
		t.Fatalf("Docs(): %v", err)
	}
	if _, err := Render(tmpl, Data{Self: "axon"}, docs); err != nil {
		t.Fatalf("Render over the embedded template failed: %v", err)
	}
}

func TestDefaultTemplate_CarriesApprovedV4ViewsWithoutRemoteLoads(t *testing.T) {
	t.Parallel()
	tmpl := dashboardTemplateCorpus(t)
	// Integrity is no longer a view: its three panels are a qualifier on the
	// freshness table and now live at the bottom of Spaces. Docs took its slot —
	// the embedded SSOT corpus gets its own route instead of hiding in Guide.
	for _, view := range []string{"Overview", "Work", "Threads", "Contracts", "Network", "Spaces", "Versions", "Docs", "Guide"} {
		if !strings.Contains(tmpl, `data-screen-label="`+view+`"`) {
			t.Errorf("embedded v4 dashboard is missing %s view", view)
		}
	}
	for _, pattern := range []string{`<script src="http`, `<link href="http`, `<img src="http`} {
		if strings.Contains(tmpl, pattern) {
			t.Errorf("self-contained dashboard has automatic remote load %q", pattern)
		}
	}
}

func TestDefaultTemplate_ExplanatoryPopoversShareHoverBehavior(t *testing.T) {
	t.Parallel()
	tmpl := string(DefaultTemplate())

	for _, name := range []string{"HintDot", "StatusPill", "TypeBadge", "FreshnessDot"} {
		marker := `window.__resourceBlobs["./` + name + `.dc.html"]`
		start := strings.Index(tmpl, marker)
		if start < 0 {
			t.Errorf("dashboard has no %s resource", name)
			continue
		}
		stop := strings.Index(tmpl[start+1:], `window.__resourceBlobs["./`)
		if stop < 0 {
			stop = len(tmpl) - start - 1
		}
		resource := tmpl[start : start+1+stop]
		if !strings.Contains(resource, `onMouseEnter`) || !strings.Contains(resource, `role=\"tooltip\"`) {
			t.Errorf("%s does not expose its explanation on hover through the shared tooltip grammar", name)
		}
	}
}

func TestDefaultTemplate_DetailHierarchyAndGuideStayConsistent(t *testing.T) {
	t.Parallel()
	tmpl := dashboardTemplateCorpus(t)

	for _, want := range []string{
		`--font-detail-title: 28px`,
		`class="a2a-detail-title"`,
		`data-guide-primary`,
		`data-guide-feedback`,
		`data-guide-feedback-robot`,
		`right:-64px; bottom:-82px`,
		`<dc-import name="EmptyState" title="{{ feedbackTitle }}" text="{{ feedbackText }}" size="panel"`,
		`data-guide-status-card="true"`,
		`data-guide-reading-card="true"`,
		`data-guide-reference`,
		`Как устроен a2ahub`,
		`Есть идея? Давайте сделаем a2ahub лучше`,
		`a2a feedback validate`,
		`Это статус документа, а не отметка о релизе.`,
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("dashboard is missing shared detail/guide contract %q", want)
		}
	}
	for _, removed := range []string{
		`Команды за этими экранами`,
		`Commands behind these screens`,
		`Как это работает без ручной диспетчеризации`,
		`How this works without manual dispatch`,
	} {
		if strings.Contains(tmpl, removed) {
			t.Errorf("dashboard still carries removed guide copy %q", removed)
		}
	}

	badge, err := os.ReadFile("../../web/design-source/TypeBadge.dc.html")
	if err != nil {
		t.Fatalf("read generated TypeBadge: %v", err)
	}
	if !bytes.Contains(badge, []byte(`{{ prefix }}</span> {{ word }}`)) {
		t.Error("type badges do not put the protocol code before the human label")
	}
}

func TestDefaultTemplate_MapTooltipsEscapeTheSVGAndPanelsContainText(t *testing.T) {
	t.Parallel()
	mapSource, err := os.ReadFile("../../web/design-source/NetworkMap.dc.html")
	if err != nil {
		t.Fatalf("read generated NetworkMap: %v", err)
	}
	for _, want := range []string{
		`role="tooltip"`,
		`position:fixed; z-index:1200`,
		`pointer-events:none;`,
		`pointer-events:stroke;`,
		`this.relTip(r)`,
		`panelReason`,
		`overflow-wrap:anywhere`,
		`word-break:break-word`,
	} {
		if !bytes.Contains(mapSource, []byte(want)) {
			t.Errorf("map is missing tooltip/overflow contract %q", want)
		}
	}
	if bytes.Contains(mapSource, []byte(`this.spaceTip(sp, panelReason)`)) {
		t.Error("network map still reconstructs a local space-tooltip status instead of presenting the carried reason")
	}
	mapView := dashboardDesignSource(t, "MapView.dc.html")
	for _, want := range []string{
		`actions.cardID("cver"`,
		`actions.cardAccent("dependency-line"`,
		`actions.openCard("cver"`,
	} {
		if !strings.Contains(mapView, want) {
			t.Errorf("map is missing canonical contract-version card route %q", want)
		}
	}
}

func TestDefaultTemplate_ArtifactPreviewAndDeadlineHierarchy(t *testing.T) {
	t.Parallel()
	tmpl := dashboardTemplateCorpus(t)
	detail := dashboardDesignSource(t, "ArtifactDetail.dc.html")
	modal := dashboardDesignSource(t, "Modal.dc.html")

	for _, required := range []string{
		`data-artifact-markdown`,
		`data-artifact-metadata`,
		`data-artifact-footer`,
		`data-artifact-author`,
		`hydrateMarkdown`,
		`bodyFadeStyle`,
		`bodyExpanded`,
		`metadataExpanded`,
		`Метаданные`,
		`История событий`,
		`FreshnessDot`,
		`ActorEventLine`,
		`authorProfileURL`,
		`authorAvatar`,
		`calloutParts`,
		`SectionHeading`,
		`data-artifact-section`,
		`data-work-report-history`,
	} {
		if !strings.Contains(detail, required) {
			t.Errorf("ArtifactDetail is missing %q", required)
		}
	}
	for _, forbidden := range []string{`role="dialog"`, `aria-modal="true"`, `position:fixed; inset:0`, `this.props.onClose`, `this.onOutside`, `lockBody()`, `unlockBody()`} {
		if strings.Contains(detail, forbidden) {
			t.Errorf("ArtifactDetail still owns modal mechanics %q", forbidden)
		}
	}
	for _, required := range []string{`data-card-modal`, `role="dialog"`, `aria-modal="true"`, `lockBody()`, `trapCardTab(ev)`, `cardGoneText`} {
		if !strings.Contains(modal, required) {
			t.Errorf("canonical Modal is missing lifecycle contract %q", required)
		}
	}
	for _, removed := range []string{
		`Главное о документе`,
		`Тело документа`,
		`Все поля frontmatter`,
	} {
		if strings.Contains(detail, removed) {
			t.Errorf("ArtifactDetail still renders removed duplicate heading %q", removed)
		}
	}
	if !strings.Contains(tmpl, "Что до ответа делаем мы") {
		t.Error("dashboard does not separate the sender's interim work under its own label")
	}
	if !strings.Contains(tmpl, "Локальная копия спейса актуальна. Последняя синхронизация:") {
		t.Error("dashboard does not explain the compact current-snapshot indicator")
	}
	if !strings.Contains(tmpl, `const primary = [];`) {
		t.Error("dashboard essentials still duplicate author, priority, or blocking from the header")
	}
	if strings.Contains(tmpl, "А пока:") {
		t.Error("ArtifactDetail still joins the sender's interim work with the requested response under 'А пока'")
	}
	if strings.Contains(detail, `>{{ body }}</`) {
		t.Error("ArtifactDetail still renders authored Markdown as one raw text block")
	}
	if !strings.Contains(detail, `bodyOpen || !bodyHasMore`) {
		t.Error("short document preview has no bottom padding when the accordion is absent")
	}
	for _, removed := range []string{`Документ целиком`, `DocumentModal`, `factsLabel`, ` · запись · `, ` record · `, `sourceLine`, `Открыть тред`, `Начало документа`} {
		if strings.Contains(detail, removed) {
			t.Errorf("ArtifactDetail still carries removed legacy UI %q", removed)
		}
	}
	if strings.Contains(tmpl, `window.__resourceBlobs["./DocumentModal.dc.html"]`) {
		t.Error("removed DocumentModal is still embedded in the generated dashboard")
	}
	for _, required := range []string{
		`actions.cardID("cver"`,
		`actions.cardAccent("dependency-line"`,
		`actions.openCard("cver"`,
	} {
		if !strings.Contains(tmpl, required) {
			t.Errorf("map canonical contract-version card route is missing %q", required)
		}
	}
	for _, removed := range []string{`mapDocumentOpen`, `mapDocumentDetail`, `closeMapDocument`} {
		if strings.Contains(tmpl, removed) {
			t.Errorf("map still carries retired parallel detail overlay %q", removed)
		}
	}
	if strings.Contains(tmpl, `Открыть в разделе «Работа»`) {
		t.Error("map document navigation still names the removed Work section")
	}
	if strings.Contains(tmpl, `this.setState({ sel: null });\n        this.props.onOpenDocument`) {
		t.Error("opening a map document still discards the underlying system popover")
	}
	deadline := strings.Index(detail, "A declared date is the first consequence under the title")
	preview := strings.Index(detail, "Artifact Markdown is untrusted data")
	if deadline < 0 || preview < 0 || deadline > preview {
		t.Fatalf("deadline callout must precede the document preview: deadline=%d preview=%d", deadline, preview)
	}
}

func TestDefaultTemplate_WorkReportsUseDurableHistoryAndCollapseTechnicalPublish(t *testing.T) {
	t.Parallel()
	tmpl := dashboardDesignSource(t, "ExchangeView.dc.html")
	for _, required := range []string{
		`data-work-report-list`,
		`const workEvents = (d.workReports || []).filter`,
		`report.space === a.space && (!a.thread || report.thread === a.thread) && (report.artifact_id === a.id || subjectID === a.id)`,
		`const carriesWorkReport = (d.workReports || []).some`,
		`filter(ev => !(carriesWorkReport && ev.transition === "publish"))`,
		`const workReportRows = workReports.map(report =>`,
		`actions.openCard("work-report", id, accent)`,
		`workReportsHint:ru ? "в перенесённом порядке" : "in carried order"`,
	} {
		if !strings.Contains(tmpl, required) {
			t.Errorf("dashboard work-report history is missing %q", required)
		}
	}
}

func TestDashboardDesignSource_WorkReportAndThreadSelectionSemantics(t *testing.T) {
	t.Parallel()
	exchange := dashboardDesignSource(t, "ExchangeView.dc.html")
	threads := dashboardDesignSource(t, "ThreadsView.dc.html")
	for _, required := range []string{
		`report.artifact_id === a.id || subjectID === a.id`,
		`const workReportRows = workReports.map(report =>`,
		`const carried = aggregate ? (aggregate.items || []) : collections[tab];`,
	} {
		if !strings.Contains(exchange, required) {
			t.Errorf("exchange carried-order contract is missing %q", required)
		}
	}
	for _, required := range []string{
		`v.space === threadSpace && v.thread === threadSel`,
		`threadSpace === t.space && threadSel === t.id`,
		`data-thread-space="{{ t.space }}"`,
		`actions.cardID("thread", { space:t.space, thread:t.id })`,
		`actions.openCard("thread", id, "")`,
	} {
		if !strings.Contains(threads, required) {
			t.Errorf("dashboard release semantic contract is missing %q", required)
		}
	}
	if strings.Contains(exchange, `.sort(`) || strings.Contains(threads, `.sort(`) {
		t.Fatal("dashboard exchange/thread views must preserve carried order instead of sorting in the browser")
	}
}

func TestDefaultTemplate_HumanFacingDashboardLabelsStayActionable(t *testing.T) {
	t.Parallel()
	tmpl := dashboardTemplateCorpus(t)

	for _, want := range []string{
		`последняя проверка`,
		`минимальная версия для записи`,
		`последний релиз`,
		`showInstalledLabel: !!tooling.updateAvailable`,
		`const toText = (it.to || []).join`,
		`href:tooling.releaseURL || ""`,
		`SegmentedFilter`,
		`data-segmented-filter=\"true\"`,
		`summaryRows`,
		`ExpandIcon`,
		`DisclosureIcon`,
		`ProseDisclosure`,
		`data-section-heading`,
		`a2a-fade-scroll`,
		`a2a-release-meta-row`,
		`border-radius:var(--radius-chip)`,
		`minmax(0,54%)`,
		`a2a-sticky-rail`,
		`Условные обозначения`,
		`Как пользоваться`,
		`участники спейса «`,
		`const A2A_GLOSSARY`,
		`globalThis.A2A_GLOSSARY`,
		`data-status-key`,
		`data-type-badge`,
		`onMouseEnter`,
		`statusGuide: A2A_STATUS_GUIDE`,
		`artifactTypes: A2A_TYPE_GUIDE`,
		`Просьба другой системе выдать конкретный результат`,
		`Run a2a sync`,
		`cleanPath(doc.src)`,
		`d.id === raw.slice(1)`,
		`https://github.com/ydnikolaev/a2ahub/blob/main/`,
		`Узнать свою версию`,
		`доступно без интернета`,
		`count: releasesAll.reduce`,
		`actionMatches(c, s.verAction)`,
		`kindMatches(c, s.verKind)`,
		`fix:"Исправление"`,
		`ru ? "Нужно действие"`,
		`operational.timeline`,
		`Где мы сейчас`,
		`a2a-overview-command-grid`,
		`a2a-overview-command-heading`,
		`a2a-overview-operational-heading`,
		`grid-template-areas:"operational kpis" "operational inventory" "operational attention"`,
		`a2a-overview-snapshot`,
		`--space-grid: 18px`,
		`--font-section-title: 22px`,
		`--overview-columns:minmax(0,3fr) minmax(320px,2fr)`,
		`gap:var(--space-grid)`,
		`a2a-overview-section-title`,
		`a2a-overview-section-subtitle`,
		`a2a-overview-operational-scroll`,
		`a2a-overview-operational-list`,
		`a2a-overview-process-card`,
		`background:var(--teal-tint)`,
		`a2a-overview-attention`,
		`position:sticky; top:84px`,
		`data-overview-attention="true"`,
		`Open in Exchange:`,
		`Данные в снимке`,
		`Требуют вашего внимания`,
		`const inventoryRows = [`,
		`railOffset: 34`,
		`Ждём от`,
		`Блокирует`,
		`globalThis.A2A_VOCABULARY_RESOLVER`,
		`data-operational-process="true"`,
		`data-operational-windows="true"`,
		`data-thread-open-items`,
		`data-work-report-fact="subject"`,
		`data-card-fact="contract:dependencies"`,
		`data-card-fact="cver:documents"`,
		`data-card-modal`,
		`data-card-gone`,
		`on-hydrate="{{ hydrateDashboard }}"`,
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("dashboard is missing human-facing copy/behavior %q", want)
		}
	}
	for _, removed := range []string{
		`LinkDetailRow`,
		`data-link-detail`,
		`TRANSITION_PAST`,
		`mapDocumentOpen`,
		`mapDocumentDetail`,
		`selectedSpaceSource.freshness`,
		`Number(right.commit_sequence || 0) - Number(left.commit_sequence || 0)`,
		`порядок из коммитов · надёжно`,
		`все выпуски, вшитые в этот бинарник, тот же источник, что и на сайте`,
		`показано изменений:`,
		`kindLabel: ru ? "Тип"`,
		`actionLabel: ru ? "Действие"`,
		`Во всю ширину ⤢`,
		`Что у вас стоит`,
		`Исправления безопасности выходят только для последней версии.`,
		`вшито в этот файл`,
		`⌄`,
		`background:var(--green); flex:0 0 9px;`,
		`overviewLayoutTuner`,
		`overviewRatio`,
		`a2a-overview-operational-list::before`,
		`a2a-overview-process::before`,
	} {
		if strings.Contains(tmpl, removed) {
			t.Errorf("dashboard still carries noisy or ambiguous copy %q", removed)
		}
	}
	threadTitle := strings.Index(tmpl, `>{{ tvHeading }}`)
	threadParticipants := strings.Index(tmpl, `{{ tvParticipantsAria }}`)
	if threadTitle < 0 || threadParticipants < 0 || threadTitle > threadParticipants {
		t.Error("canonical thread heading is not rendered before its participant facts")
	}
}

func TestDashboardDesignSource_OperationalRowsKeepEvidenceLayersSeparate(t *testing.T) {
	t.Parallel()
	source := dashboardDesignSource(t, "Overview.dc.html")

	heading := strings.Index(source, `<div class="a2a-overview-command-heading">`)
	snapshot := strings.Index(source, `<div class="a2a-overview-snapshot">`)
	grid := strings.Index(source, `<div class="a2a-overview-command-grid"`)
	operationalSection := strings.Index(source, `<section class="a2a-overview-operational"`)
	if heading < 0 || snapshot < 0 || grid < 0 || operationalSection < 0 || heading >= snapshot || snapshot >= grid || grid >= operationalSection {
		t.Fatalf("heading and snapshot must share the aligned row above the widget grid: heading=%d snapshot=%d grid=%d section=%d", heading, snapshot, grid, operationalSection)
	}
	if strings.Contains(source[heading:grid], `a2a-overview-command-meta`) {
		t.Fatal("snapshot must not be separated from the Overview heading by a standalone meta row")
	}

	layers := []string{
		`data-operational-process-identity`,
		`data-operational-protocol`,
		`data-operational-participants`,
		`data-operational-latest-milestone`,
		`data-operational-work`,
		`data-operational-waits-checkpoint`,
		`data-operational-shared-pending`,
		`data-operational-consistency`,
		`data-operational-source`,
		`data-operational-windows`,
	}
	previous := -1
	for _, layer := range layers {
		position := strings.Index(source, layer)
		if position < 0 || position <= previous {
			t.Fatalf("operational fact layer %q is missing or out of carried reading order: position=%d previous=%d", layer, position, previous)
		}
		previous = position
	}

	for _, required := range []string{
		`const operationalSource = cardMode`,
		`const operationalRows = operationalSource.map(row => this.processRow(row, data, operational, ru, actions, card));`,
		`const milestoneResult = this.vocabulary(data, "transition", latest.transition, locale);`,
		`const workWindowText = windowSentence(row.work_window, ru);`,
		`const consistencyWindowText = windowSentence(row.consistency_window, ru);`,
		`const needYou = aggregates.needYou || { items:[], window:{} };`,
		`const timelineWindow = operational.timeline_window || {};`,
		`const timelineBound = windowSentence(timelineWindow, ru);`,
		`<span style="position:absolute; top:14px; right:14px;"><dc-import name="HintDot"`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("operational source is missing render contract %q", required)
		}
	}
	for _, forbidden := range []string{
		`const operationalActive =`,
		`.sort(`,
		`latestOwner || String(latestActor.name`,
		`critical path`,
		`exact next verb`,
		`overviewLayoutTuner`,
		`overviewRatio`,
		`a2a-overview-operational-list::before`,
		`a2a-overview-process::before`,
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("operational source still carries forbidden inference %q", forbidden)
		}
	}
}

func TestDashboardDesignSource_SelectedSpaceOperationalEmptyStatesUseSnapshotEvidence(t *testing.T) {
	t.Parallel()
	source := dashboardDesignSource(t, "Overview.dc.html")

	for _, required := range []string{
		`data-overview-operational-empty="true"`,
		`<dc-import name="EmptyState" title="{{ operationalEmptyTitle }}" text="{{ operationalEmptyText }}" size="panel"`,
		`operational.timeline_window || {}`,
		`const timelineBound = windowSentence(timelineWindow, ru);`,
		`This snapshot contains no operational rows.`,
		`This snapshot has no process for the carried identity.`,
		`The carried set is complete and empty.`,
		`The server reports a non-zero total but admitted no rows into this bounded snapshot.`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("selected-space empty-state contract is missing %q", required)
		}
	}
	for _, retired := range []string{`selectedSpaceSource`, `operationalEmptyKind`, `selectedSpaceUnavailable`, `readable-no-rows`, `no-source`} {
		if strings.Contains(source, retired) {
			t.Errorf("Overview still reconstructs a local selected-space empty-state classifier %q", retired)
		}
	}
}

func TestDefaultTemplate_ThreadCardsShareRhythmAndOpenByTitle(t *testing.T) {
	t.Parallel()
	tmpl := dashboardDesignSource(t, "ThreadsView.dc.html")

	for _, want := range []string{
		`data-thread-space="{{ t.space }}"`,
		`data-thread-id="{{ t.id }}"`,
		`onClick="{{ o.openDocument }}"`,
		`{{ o.title }}`,
		`const title = artifact.title || notRecorded;`,
		`border-radius:var(--radius-card)`,
		`padding:var(--padding-list-card-block) var(--padding-list-card-inline)`,
		`title: t.opener && t.opener.title ? t.opener.title : notRecorded`,
		`reasonSentence:localizedSentence(o.reasonSentence, ru)`,
		`collectionWindowSentence(tv && tv.windows && tv.windows.transcript, ru)`,
		`collectionWindowSentence(tv && tv.windows && tv.windows.openItems, ru)`,
		`actions.cardID("thread", { space:t.space, thread:t.id })`,
		`actions.openCard("thread", id, "")`,
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("thread cards are missing shared rhythm/title interaction %q", want)
		}
	}
	if strings.Contains(tmpl, `>{{ claimedLabel }}`) || strings.Contains(tmpl, `>{{ e.claimLabel }}`) {
		t.Fatal("matching claimed_state still renders as a duplicate timeline status")
	}
}

func TestTimelineArtifactCardOpensDocumentFromWholeCard(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("../../web/design-source/TimelineArtifactCard.dc.html")
	if err != nil {
		t.Fatalf("read TimelineArtifactCard: %v", err)
	}
	card := string(source)
	for _, want := range []string{
		`data-timeline-artifact-card`,
		`onClick="{{ openDocument }}"`,
		`aria-label="{{ openLabel }}"`,
		`Открыть документ: `,
		`width:100%`,
		`cursor:pointer`,
	} {
		if !strings.Contains(card, want) {
			t.Errorf("timeline artifact card is missing whole-card interaction %q", want)
		}
	}
	if got := strings.Count(card, `<button`); got != 1 {
		t.Fatalf("timeline artifact card has %d buttons, want only the whole-card button", got)
	}
	for _, removed := range []string{`Открыть в разделе «Обмен»`, `exchangeLabel`, `openExchange`, `documentLabel`} {
		if strings.Contains(card, removed) {
			t.Errorf("timeline artifact card still carries removed action %q", removed)
		}
	}
}

func TestDefaultTemplate_ContractRiskNamesActorsVersionsAndSuccessor(t *testing.T) {
	t.Parallel()
	tmpl := dashboardDesignSource(t, "ContractsView.dc.html")
	for _, want := range []string{
		`data-card-fact="contract:consumers"`,
		`data-card-fact="contract:dependencies"`,
		`{{ line.consumer }}`,
		`{{ line.cardText }}`,
		`{{ line.successor }}`,
		`edge.pinnedVersion`,
		`edge.providerVersion`,
		`resolve("dependency-drift", edge.drift)`,
		`actions.cardID("cver", { space, id:target.id, version:target.version })`,
		`actions.openCard("cver", id, targetAccent || "")`,
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("contract risk/version UI is missing %q", want)
		}
	}
	for _, removed := range []string{`cdRiskTitle`, `cdRiskText`, `openContractRef`} {
		if strings.Contains(tmpl, removed) {
			t.Errorf("contract risk UI still carries the unstructured field %q", removed)
		}
	}
}

func TestDefaultTemplate_ContractVersionPackageIsSelectableAndTextOnly(t *testing.T) {
	t.Parallel()
	tmpl := dashboardDesignSource(t, "ContractsView.dc.html") + dashboardDesignSource(t, "Modal.dc.html")
	for _, want := range []string{
		`contractVersionSwitcher`,
		`shortLabel`,
		`Contract overview`,
		`Обзор контракта`,
		`Version package`,
		`Комплект версии`,
		`data-contract-file-key`,
		`{{ document.preview }}`,
		`Historical package unavailable`,
		`Исторический комплект недоступен`,
		`const detail = version.detail || null;`,
		`const documents = detail ? (detail.documents || []).map((document, index, rows) =>`,
		`document.shownBytes + " bytes of " + document.totalBytes`,
		`data-contract-version`,
		`data-contract-documents-omitted`,
		`cdVersionDocumentsOmittedText`,
		`totalDocumentCount`,
		`omittedDocumentCount`,
		`const documentsWindowText = detail && detail.omittedDocumentCount > 0`,
		`collectionBounds({ shown, total, truncated:true }, ru)`,
		`actions.sourceURL(contract.space, document.path, revision)`,
		`Открыть на GitHub`,
		`toggleCdVersionProvenance`,
		`DisclosureIcon`,
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("contract version package UI is missing %q", want)
		}
	}
	if strings.Contains(tmpl, `contractFileHTML`) {
		t.Fatal("contract version file preview gained an HTML projection; carried bytes must remain text-only")
	}
}

func TestDefaultTemplate_HasBilingualShellWithEnglishDefault(t *testing.T) {
	t.Parallel()
	tmpl := dashboardTemplateCorpus(t)
	for _, want := range []string{`<html lang="en">`, `a2a-locale`, `>EN</button>`, `>RU</button>`, `Interface language`, `Язык интерфейса`} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("embedded dashboard is missing bilingual-shell marker %q", want)
		}
	}
}

func TestDefaultTemplate_HasNoUnresolvedNodeEnvironmentReference(t *testing.T) {
	t.Parallel()
	tmpl := string(DefaultTemplate())
	if strings.Contains(tmpl, "process.env.NODE_ENV") {
		t.Fatal("self-contained browser runtime contains an unresolved Node.js environment reference")
	}
}

func TestDefaultTemplate_HydratesOnlyTheEmbeddedDashboard(t *testing.T) {
	t.Parallel()
	tmpl := string(DefaultTemplate())
	if !strings.Contains(tmpl, `this.hydrate(window.A2A_DEMO)`) {
		t.Fatal("self-contained dashboard must adopt embedded data through the single hydrate path")
	}
	if strings.Contains(tmpl, `fetch("demo-data.json")`) || strings.Contains(tmpl, `fetch(\"demo-data.json\")`) {
		t.Fatal("self-contained dashboard must not fall back to a sibling demo fixture")
	}
}

func TestSharedTokensMatchUIContract(t *testing.T) {
	t.Parallel()
	want, err := os.ReadFile("../../ui/tokens.css")
	if err != nil {
		t.Fatalf("read ui token SSOT: %v", err)
	}
	if !bytes.Equal(sharedTokens, want) {
		t.Fatal("internal/html/tokens.css drifted from ui/tokens.css; regenerate the projection")
	}
}
