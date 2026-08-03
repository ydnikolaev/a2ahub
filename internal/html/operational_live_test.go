package html

import (
	"bytes"
	"testing"
)

func TestDashboardLiveClientConsumesRevisionThenBoundedSnapshot(t *testing.T) {
	t.Parallel()
	template := DefaultTemplate()
	for _, required := range [][]byte{
		[]byte(`new EventSource("/api/v1/events")`),
		[]byte(`fetch("/api/v1/snapshot"`),
		[]byte(`headers["If-None-Match"]`),
		[]byte(`window.location.protocol !== "http:"`),
		[]byte(`current.operational = snapshot`),
	} {
		if !bytes.Contains(template, required) {
			t.Fatalf("dashboard template lacks live operational contract %q", required)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`new WebSocket(`),
		[]byte(`method:"POST"`),
		[]byte(`method:"PUT"`),
		[]byte(`method:"PATCH"`),
	} {
		if bytes.Contains(template, forbidden) {
			t.Fatalf("dashboard live client contains forbidden surface %q", forbidden)
		}
	}
}
