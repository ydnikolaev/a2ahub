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

func TestRender_Injects(t *testing.T) {
	t.Parallel()
	tmpl := []byte("const DATA = /*A2A_DATA_START*/{}/*A2A_DATA_END*/;\n")
	out, err := Render(tmpl, Data{Self: "axon"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"self": "axon"`)) {
		t.Fatalf("real data not injected:\n%s", out)
	}
	if bytes.Contains(out, []byte("A2A_DATA_START")) {
		t.Fatal("marker leaked into output")
	}
}

func TestRender_MissingMarkers(t *testing.T) {
	t.Parallel()
	if _, err := Render([]byte("no markers here"), Data{}); err == nil {
		t.Fatal("want error on a template missing the DATA markers")
	}
}

// TestRender_SanitizesScriptBreakout: a hostile title containing "</script>"
// must be escaped in the injected JSON so it cannot break out of the <script>
// block (json.Marshal escapes < as <).
func TestRender_SanitizesScriptBreakout(t *testing.T) {
	t.Parallel()
	tmpl := []byte("<script>const DATA = /*A2A_DATA_START*/{}/*A2A_DATA_END*/;</script>")
	hostile := `</script><img src=x onerror=alert(1)>`
	out, err := Render(tmpl, Data{Inbox: []Item{{Title: hostile}}})
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

func TestDefaultTemplate_HasMarkers(t *testing.T) {
	t.Parallel()
	tmpl := DefaultTemplate()
	if !strings.Contains(string(tmpl), dataStart) || !strings.Contains(string(tmpl), dataEnd) {
		t.Fatal("embedded template.html is missing the DATA markers")
	}
	// It must render without error (markers present + valid).
	if _, err := Render(tmpl, Data{Self: "axon"}); err != nil {
		t.Fatalf("Render over the embedded template failed: %v", err)
	}
}
