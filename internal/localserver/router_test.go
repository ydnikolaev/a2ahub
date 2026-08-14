package localserver

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestDashboardRouteBytesEqualShellData(t *testing.T) {
	t.Parallel()

	const prefix = "window.A2A_DEMO="
	const suffix = ";window.A2A_DOCS="
	viewModel := []byte(`{"self":"atlas","inbox":[{"title":"\u003c/script\u003e\u003cimg src=x onerror=alert(1)\u003e"}]}`)
	shell := append([]byte("<script>"+prefix), viewModel...)
	shell = append(shell, []byte(suffix+"[];</script>")...)
	server, err := New(DefaultConfig(), &fakeReader{}, nil, fakeRenderer{shell: shell, viewModel: viewModel}, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := server.publish(t.Context(), testSnapshot("dashboard-parity")); err != nil {
		t.Fatalf("publish() error = %v", err)
	}

	page := request(t, server, http.MethodGet, "/", false)
	start := bytes.Index(page.body, []byte(prefix))
	if start < 0 {
		t.Fatal("shell has no DATA prefix")
	}
	start += len(prefix)
	end := bytes.Index(page.body[start:], []byte(suffix))
	if end < 0 {
		t.Fatal("shell has no DATA suffix")
	}
	want := page.body[start : start+end]
	if bytes.Contains(want, []byte("</script><img")) {
		t.Fatalf("shell DATA contains active hostile text: %s", want)
	}

	dashboard := request(t, server, http.MethodGet, "/api/v1/dashboard", false)
	if dashboard.status != http.StatusOK {
		t.Fatalf("dashboard status = %d, want 200; body=%s", dashboard.status, dashboard.body)
	}
	if !bytes.Equal(dashboard.body, want) {
		t.Fatalf("dashboard body differs from raw shell DATA\nroute: %s\nshell: %s", dashboard.body, want)
	}
}

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
		{http.MethodGet, "/api/v1/dashboard", 200, ""}, {http.MethodHead, "/api/v1/dashboard", 200, ""},
		{http.MethodPost, "/", 405, "GET, HEAD"}, {http.MethodPut, "/api/v1/snapshot", 405, "GET, HEAD"},
		{http.MethodPut, "/api/v1/dashboard", 405, "GET, HEAD"},
		{http.MethodPost, "/api/v1/events", 405, "GET"}, {http.MethodGet, "/api/v1/snapshot/", 404, ""},
		{http.MethodPost, "/unknown", 404, ""},
	}
	for _, test := range tests {
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

func TestDashboardWeakETagHeadConditionalAndStaleContract(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	generation := server.store.dashboard()
	get := request(t, server, http.MethodGet, "/api/v1/dashboard", false)
	wantETag := weakETag(generation.viewModelDigest)
	if get.status != http.StatusOK || get.header.Get("ETag") != wantETag || get.header.Get("Cache-Control") != "no-store" ||
		get.header.Get("Content-Type") != "application/json; charset=utf-8" || !bytes.Equal(get.body, generation.viewModel) {
		t.Fatalf("GET dashboard = status %d headers=%v body=%s", get.status, get.header, get.body)
	}
	head := request(t, server, http.MethodHead, "/api/v1/dashboard", false)
	if head.status != http.StatusOK || len(head.body) != 0 || head.header.Get("Content-Length") != fmt.Sprint(len(generation.viewModel)) || head.header.Get("ETag") != wantETag {
		t.Fatalf("HEAD dashboard = status %d body=%q headers=%v", head.status, head.body, head.header)
	}
	conditionalRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/dashboard", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	conditionalRequest.Header.Set("If-None-Match", wantETag)
	conditional := newResponseCapture()
	server.Handler().ServeHTTP(deadlineResponseCapture{responseCapture: conditional}, conditionalRequest)
	if conditional.status != http.StatusNotModified || len(conditional.body) != 0 {
		t.Fatalf("conditional dashboard = %d %q", conditional.status, conditional.body)
	}
	unknownRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/dashboard", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	unknownRequest.Header.Set("If-None-Match", weakETag(testRevision("unknown-dashboard")))
	unknown := newResponseCapture()
	server.Handler().ServeHTTP(deadlineResponseCapture{responseCapture: unknown}, unknownRequest)
	if unknown.status != http.StatusOK || !bytes.Equal(unknown.body, generation.viewModel) {
		t.Fatalf("unknown dashboard ETag = %d %q", unknown.status, unknown.body)
	}
	server.recordError(errors.New("refresh failed"))
	stale := request(t, server, http.MethodHead, "/api/v1/dashboard", false)
	if stale.header.Get(StaleHeader) == "" {
		t.Fatal("stale dashboard response omitted its transport degradation header")
	}
}

func TestHeadAndConditionalDashboardConsumeNoBodyWriterSlot(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	for range server.config.SnapshotWriters {
		server.writerSlots <- struct{}{}
	}
	defer func() {
		for range server.config.SnapshotWriters {
			<-server.writerSlots
		}
	}()
	head := request(t, server, http.MethodHead, "/api/v1/dashboard", false)
	if head.status != http.StatusOK {
		t.Fatalf("HEAD dashboard under exhausted writer pool = %d", head.status)
	}
	conditionalRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/dashboard", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	conditionalRequest.Header.Set("If-None-Match", weakETag(server.store.dashboard().viewModelDigest))
	conditional := newResponseCapture()
	server.Handler().ServeHTTP(deadlineResponseCapture{responseCapture: conditional}, conditionalRequest)
	if conditional.status != http.StatusNotModified {
		t.Fatalf("conditional dashboard under exhausted writer pool = %d", conditional.status)
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
	oldDashboard := server.store.dashboard()
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
	case <-oldDashboard.ctx.Done():
	default:
		t.Fatal("superseded dashboard generation was not canceled")
	}
	largest := max(server.config.MaxSnapshotBytes, server.config.MaxShellBytes, MaximumDashboardBytes)
	retained := server.config.MaxSnapshotBytes + server.config.MaxShellBytes + MaximumDashboardBytes + cap(server.writerSlots)*largest
	if cap(server.writerSlots) != DefaultSnapshotWriters || retained != MaximumRetainedBytes || retained > 88<<20 {
		t.Fatalf("retained-body budget drift: writers=%d bytes=%d", cap(server.writerSlots), retained)
	}
}

func TestMixedBodyRoutesShareOneFourWriterPool(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	paths := []string{"/", "/api/v1/snapshot", "/api/v1/dashboard", "/"}
	writers := make([]*staggeredSnapshotWriter, 0, len(paths))
	done := make([]<-chan struct{}, 0, len(paths))
	for _, path := range paths {
		writer := newStaggeredSnapshotWriter()
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost"+path, nil)
		if err != nil {
			t.Fatalf("NewRequest(%s) error = %v", path, err)
		}
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			server.Handler().ServeHTTP(writer, request)
		}()
		awaitSignal(t, writer.started, "mixed-route body writer did not start")
		writers = append(writers, writer)
		done = append(done, finished)
	}
	if len(server.writerSlots) != DefaultSnapshotWriters || cap(server.writerSlots) != DefaultSnapshotWriters {
		t.Fatalf("shared writer pool len/cap = %d/%d", len(server.writerSlots), cap(server.writerSlots))
	}
	excess := request(t, server, http.MethodGet, "/api/v1/dashboard", false)
	if excess.status != http.StatusServiceUnavailable || excess.header.Get("Retry-After") == "" {
		t.Fatalf("fifth mixed-route writer = status %d headers=%v", excess.status, excess.header)
	}
	for _, writer := range writers {
		close(writer.release)
	}
	for _, finished := range done {
		awaitSignal(t, finished, "mixed-route writer did not release")
	}
	if len(server.writerSlots) != 0 {
		t.Fatalf("shared writer slots leaked: %d", len(server.writerSlots))
	}
}

func TestSupersededDashboardReaderCancelsAndReleasesSharedSlot(t *testing.T) {
	t.Parallel()
	renderer := &countingRenderer{
		shell: []byte("first shell"), viewModel: bytes.Repeat([]byte("x"), 2*(32<<10)), fingerprint: "content:first",
	}
	server, err := New(DefaultConfig(), &fakeReader{}, nil, renderer, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot := testSnapshot("dashboard-reader")
	if err := server.publish(t.Context(), snapshot); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	previous := server.store.dashboard()
	writer := newStaggeredSnapshotWriter()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/dashboard", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Handler().ServeHTTP(writer, request)
	}()
	awaitSignal(t, writer.started, "dashboard reader did not start")
	if len(server.writerSlots) != 1 {
		t.Fatalf("dashboard reader slots = %d, want 1", len(server.writerSlots))
	}
	renderer.set([]byte("second shell"), []byte(`{"semantic":2}`), "content:second")
	if err := server.publish(t.Context(), snapshot); err != nil {
		t.Fatalf("replacement publish: %v", err)
	}
	select {
	case <-previous.ctx.Done():
	default:
		t.Fatal("replacement did not cancel the superseded dashboard generation")
	}
	close(writer.release)
	awaitSignal(t, done, "canceled dashboard reader did not stop")
	if len(server.writerSlots) != 0 {
		t.Fatalf("canceled dashboard reader leaked %d shared writer slots", len(server.writerSlots))
	}
}
