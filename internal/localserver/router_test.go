package localserver

import (
	"net/http"
	"strings"
	"testing"
)

func TestRouterExactRouteAndMethodInventory(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	tests := []struct {
		method string
		path   string
		status int
		allow  string
	}{
		{http.MethodGet, "/", 200, ""}, {http.MethodHead, "/", 200, ""},
		{http.MethodGet, "/api/v1/snapshot", 200, ""}, {http.MethodHead, "/api/v1/snapshot", 200, ""},
		{http.MethodPost, "/", 405, "GET, HEAD"}, {http.MethodPut, "/api/v1/snapshot", 405, "GET, HEAD"},
		{http.MethodPost, "/api/v1/events", 405, "GET"}, {http.MethodGet, "/api/v1/snapshot/", 404, ""},
		{http.MethodPost, "/unknown", 404, ""},
	}
	for _, test := range tests {
		test := test
		t.Run(test.method+"_"+test.path, func(t *testing.T) {
			t.Parallel()
			response := request(t, server, test.method, test.path, false)
			if response.status != test.status {
				t.Fatalf("status = %d, want %d; body=%s", response.status, test.status, response.body)
			}
			if response.header.Get("Allow") != test.allow {
				t.Fatalf("Allow = %q, want %q", response.header.Get("Allow"), test.allow)
			}
		})
	}
}

func TestRouterRejectsHostAndBodiesBeforeServingData(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	requestWithHost, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://evil.example/api/v1/snapshot", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	response := newResponseCapture()
	server.Handler().ServeHTTP(response, requestWithHost)
	if response.status != http.StatusMisdirectedRequest || strings.Contains(string(response.body), testRevision("sha256:one")) {
		t.Fatalf("host refusal status/body = %d %q", response.status, response.body)
	}
	bodyResponse := request(t, server, http.MethodGet, "/api/v1/snapshot", true)
	if bodyResponse.status != http.StatusRequestEntityTooLarge || bodyResponse.header.Get("Connection") != "close" {
		t.Fatalf("body refusal = %d headers=%v", bodyResponse.status, bodyResponse.header)
	}
}

func TestSnapshotWeakETagAndHeadContract(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	get := request(t, server, http.MethodGet, "/api/v1/snapshot", false)
	wantRevision := testRevision("sha256:one")
	wantETag := weakETag(wantRevision)
	if get.status != 200 || get.header.Get("ETag") != wantETag || !strings.Contains(string(get.body), `"revision":"`+wantRevision+`"`) {
		t.Fatalf("GET snapshot = status %d etag %q body %s", get.status, get.header.Get("ETag"), get.body)
	}
	head := request(t, server, http.MethodHead, "/api/v1/snapshot", false)
	if head.status != 200 || len(head.body) != 0 || head.header.Get("Content-Length") == "" {
		t.Fatalf("HEAD snapshot = status %d body %q headers %v", head.status, head.body, head.header)
	}
	conditionalRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/snapshot", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	conditionalRequest.Header.Set("If-None-Match", wantETag)
	conditional := newResponseCapture()
	server.Handler().ServeHTTP(deadlineResponseCapture{responseCapture: conditional}, conditionalRequest)
	if conditional.status != http.StatusNotModified || len(conditional.body) != 0 {
		t.Fatalf("conditional = %d %q", conditional.status, conditional.body)
	}
	strongRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/snapshot", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	strongRequest.Header.Set("If-None-Match", `"`+wantRevision+`"`)
	strong := newResponseCapture()
	server.Handler().ServeHTTP(deadlineResponseCapture{responseCapture: strong}, strongRequest)
	if strong.status != 200 {
		t.Fatalf("strong ETag was incorrectly accepted: %d", strong.status)
	}
}

func TestBodyRoutesRefuseWriterWithoutDeadlineSupport(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/snapshot", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	response := newResponseCapture()
	server.Handler().ServeHTTP(response, req)
	if response.status != http.StatusInternalServerError || server.LastError() != nil {
		t.Fatalf("unsupported bounded writer = status %d last_error=%v", response.status, server.LastError())
	}
}

func TestSecurityHeadersForbidRemoteResourcesAndCORS(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	response := request(t, server, http.MethodGet, "/", false)
	csp := response.header.Get("Content-Security-Policy")
	if response.status != 200 || !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Fatalf("CSP = %q", csp)
	}
	if response.header.Get("Access-Control-Allow-Origin") != "" || response.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe headers = %v", response.header)
	}
}

func TestSnapshotWriterBudgetAndSupersededCancellation(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	old := server.store.get()
	oldShell := server.store.shell()
	for range server.config.SnapshotWriters {
		server.writerSlots <- struct{}{}
	}
	response := request(t, server, http.MethodGet, "/api/v1/snapshot", false)
	if response.status != http.StatusServiceUnavailable || response.header.Get("Retry-After") == "" {
		t.Fatalf("writer limit response = %d %v", response.status, response.header)
	}
	rootResponse := request(t, server, http.MethodGet, "/", false)
	if rootResponse.status != http.StatusServiceUnavailable || rootResponse.header.Get("Retry-After") == "" {
		t.Fatalf("root writer limit response = %d %v", rootResponse.status, rootResponse.header)
	}
	for range server.config.SnapshotWriters {
		<-server.writerSlots
	}
	if err := server.publish(t.Context(), testSnapshot("sha256:two")); err != nil {
		t.Fatalf("publish() error = %v", err)
	}
	select {
	case <-old.ctx.Done():
	default:
		t.Fatal("superseded generation was not canceled")
	}
	select {
	case <-oldShell.ctx.Done():
	default:
		t.Fatal("superseded shell generation was not canceled")
	}
	if cap(server.writerSlots) != 4 || MaximumRetainedBodies*server.config.MaxSnapshotBytes > 80<<20 {
		t.Fatalf("retained-body budget drift: writers=%d bytes=%d", cap(server.writerSlots), MaximumRetainedBodies*server.config.MaxSnapshotBytes)
	}
	if MaximumRetainedBodies*server.config.MaxShellBytes > 20<<20 {
		t.Fatalf("retained-shell budget drift: bytes=%d", MaximumRetainedBodies*server.config.MaxShellBytes)
	}
}
