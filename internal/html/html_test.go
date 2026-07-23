package html

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

func TestProviderOf(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"XC-seomatrix-feed-v1": "seomatrix",
		"XC-axon-ingest":       "axon",
		"nodash":               "",
		"XC-":                  "",
	}
	for in, want := range cases {
		if got := providerOf(in); got != want {
			t.Errorf("providerOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDriftOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state           string
		pinned          int
		providerVersion string
		want            string
	}{
		{"published", 1, "1.2.0", "current"},
		{"published", 1, "2.0.0", "behind"},
		{"deprecated", 1, "1.0.0", "deprecated"},
		{"retired", 1, "3.0.0", "retired"},
		{"published", 2, "1.0.0", "current"}, // provider not ahead
	}
	for _, c := range cases {
		if got := driftOf(c.state, c.pinned, c.providerVersion); got != c.want {
			t.Errorf("driftOf(%q,%d,%q) = %q, want %q", c.state, c.pinned, c.providerVersion, got, c.want)
		}
	}
}

func TestMajorOf(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{"2.1.0": 2, "10.0.0": 10, "1": 1, "": 0, "vX": 0} {
		if got := majorOf(in); got != want {
			t.Errorf("majorOf(%q) = %d, want %d", in, got, want)
		}
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
	m := toItem(cache.Item{ID: "XQ-x", Description: "why this matters"}, time.Now())
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
	if !bytes.Contains(out, []byte(`"self": "axon"`)) {
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
// block (json.Marshal escapes < as <).
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
