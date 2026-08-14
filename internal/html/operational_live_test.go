package html

import (
	"bytes"
	"strings"
	"testing"
)

func TestDashboardLiveClientConsumesRevisionThenCompleteDashboard(t *testing.T) {
	t.Parallel()
	live := []byte(dashboardDesignSource(t, "DashboardLive.dc.html"))
	shipped := DefaultTemplate()
	rootEnd := strings.Index(string(shipped), `<script>window.A2A_DEMO=`)
	if rootEnd < 0 {
		t.Fatal("shipped dashboard has no root/resource boundary")
	}
	root := shipped[:rootEnd]
	const liveModelGuard = `if (!d || (d.meta && d.meta.schema && (d.meta.schema !== "a2a-design-demo/v4" || d.meta.synthetic !== true))) return false;`
	const strictDesignFixtureGuard = `if (!d || !d.meta || d.meta.schema !== "a2a-design-demo/v4" || d.meta.synthetic !== true) return false;`
	if !bytes.Contains(shipped, []byte(liveModelGuard)) {
		t.Fatalf("shipped dashboard rejects real live models with empty meta; missing relaxed guard %q", liveModelGuard)
	}
	if bytes.Contains(shipped, []byte(strictDesignFixtureGuard)) {
		t.Fatalf("shipped dashboard still contains design-fixture-only hydrate guard %q", strictDesignFixtureGuard)
	}
	for _, required := range [][]byte{
		[]byte(`new EventSource("/api/v1/events")`),
		[]byte(`fetch("/api/v1/dashboard", { method:"HEAD"`),
		[]byte(`fetch("/api/v1/dashboard", { method:"GET"`),
		[]byte(`const headers = { "If-None-Match":this.acceptedETag };`),
		[]byte(`response.status === 304`),
		[]byte(`this.completeCandidate(candidate)`),
		[]byte(`this.props.onHydrate(candidate) === true`),
		[]byte(`this.emitState("newer-available")`),
		[]byte(`window.location.protocol !== "http:"`),
		[]byte(`typeof this.props.onHydrate === "function"`),
	} {
		if !bytes.Contains(live, required) {
			t.Fatalf("DashboardLive lacks live dashboard contract %q", required)
		}
	}
	for _, required := range [][]byte{
		[]byte(`on-hydrate="{{ hydrateDashboard }}"`),
		[]byte(`hydrateDashboard: candidate => this.hydrate(candidate)`),
	} {
		if !bytes.Contains(root, required) {
			t.Fatalf("shipped dashboard root lacks live hydrate contract %q", required)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`/api/v1/snapshot`),
		[]byte(`current.operational = snapshot`),
		[]byte(`fetch("demo-data.json")`),
		[]byte(`this.props.onHydrate(snapshot)`),
		[]byte(`new WebSocket(`),
		[]byte(`method:"POST"`),
		[]byte(`method:"PUT"`),
		[]byte(`method:"PATCH"`),
	} {
		if bytes.Contains(live, forbidden) || bytes.Contains(root, forbidden) {
			t.Fatalf("dashboard live client contains forbidden surface %q", forbidden)
		}
	}
}
