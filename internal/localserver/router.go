package localserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrBoundedWriterUnsupported reports a ResponseWriter without deadline support.
var ErrBoundedWriterUnsupported = errors.New("localserver: response writer does not support bounded writes")

func (s *Server) routeHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		securityHeaders(writer.Header())
		s.mu.Lock()
		host := s.listenerHost
		port := s.listenerPort
		s.mu.Unlock()
		if err := validateRequestHost(request.Host, host, port); err != nil {
			http.Error(writer, "invalid Host", http.StatusMisdirectedRequest)
			return
		}
		if request.Body != nil && request.Body != http.NoBody || request.ContentLength > 0 || len(request.TransferEncoding) > 0 {
			writer.Header().Set("Connection", "close")
			http.Error(writer, "request bodies are not accepted", http.StatusRequestEntityTooLarge)
			return
		}
		switch request.URL.Path {
		case "/":
			s.handleRoot(writer, request)
		case "/api/v1/snapshot":
			s.handleSnapshot(writer, request)
		case "/api/v1/dashboard":
			s.handleDashboard(writer, request)
		case "/api/v1/events":
			s.handleEvents(writer, request)
		default:
			http.NotFound(writer, request)
		}
	})
}

func (s *Server) handleRoot(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, "GET, HEAD")
		return
	}
	s.setStaleHeader(writer)
	dashboard := s.store.dashboard()
	if dashboard == nil {
		http.Error(writer, "snapshot unavailable", http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	if request.Method == http.MethodHead {
		writer.Header().Set("Content-Length", fmt.Sprint(len(dashboard.shell)))
		writer.WriteHeader(http.StatusOK)
		return
	}
	if !s.acquireBodyWriter(writer) {
		return
	}
	defer func() { <-s.writerSlots }()
	if err := requireBoundedWriter(writer, s.config.WriteDeadline); err != nil {
		writer.Header().Del("Content-Length")
		http.Error(writer, "bounded response writes unsupported", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Length", fmt.Sprint(len(dashboard.shell)))
	if err := withWriteDeadline(writer, s.config.WriteDeadline, func() error {
		return writeBody(writer, dashboard.ctx, dashboard.shell)
	}); err != nil && !errors.Is(err, context.Canceled) {
		s.recordError(err)
	}
}

func (s *Server) handleSnapshot(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, "GET, HEAD")
		return
	}
	generation := s.store.get()
	if generation == nil {
		http.Error(writer, "snapshot unavailable", http.StatusServiceUnavailable)
		return
	}
	etag := weakETag(generation.revision)
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Cache-Control", "no-store")
	s.setStaleHeader(writer)
	if request.Header.Get("If-None-Match") == etag {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if request.Method == http.MethodHead {
		writer.Header().Set("Content-Length", fmt.Sprint(len(generation.body)))
		writer.WriteHeader(http.StatusOK)
		return
	}
	if !s.acquireBodyWriter(writer) {
		return
	}
	defer func() { <-s.writerSlots }()
	if err := requireBoundedWriter(writer, s.config.WriteDeadline); err != nil {
		writer.Header().Del("Content-Length")
		http.Error(writer, "bounded response writes unsupported", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Length", fmt.Sprint(len(generation.body)))
	if err := withWriteDeadline(writer, s.config.WriteDeadline, func() error {
		return writeBody(writer, generation.ctx, generation.body)
	}); err != nil && !errors.Is(err, context.Canceled) {
		s.recordError(err)
	}
}

func (s *Server) handleDashboard(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, "GET, HEAD")
		return
	}
	generation := s.store.dashboard()
	if generation == nil {
		http.Error(writer, "snapshot unavailable", http.StatusServiceUnavailable)
		return
	}
	etag := weakETag(generation.viewModelDigest)
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Cache-Control", "no-store")
	s.setStaleHeader(writer)
	if request.Header.Get("If-None-Match") == etag {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if request.Method == http.MethodHead {
		writer.Header().Set("Content-Length", fmt.Sprint(len(generation.viewModel)))
		writer.WriteHeader(http.StatusOK)
		return
	}
	if !s.acquireBodyWriter(writer) {
		return
	}
	defer func() { <-s.writerSlots }()
	if err := requireBoundedWriter(writer, s.config.WriteDeadline); err != nil {
		writer.Header().Del("Content-Length")
		http.Error(writer, "bounded response writes unsupported", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Length", fmt.Sprint(len(generation.viewModel)))
	if err := withWriteDeadline(writer, s.config.WriteDeadline, func() error {
		return writeBody(writer, generation.ctx, generation.viewModel)
	}); err != nil && !errors.Is(err, context.Canceled) {
		s.recordError(err)
	}
}

func (s *Server) acquireBodyWriter(writer http.ResponseWriter) bool {
	select {
	case s.writerSlots <- struct{}{}:
		return true
	default:
		writer.Header().Set("Retry-After", "1")
		http.Error(writer, "response writer limit reached", http.StatusServiceUnavailable)
		return false
	}
}

func writeBody(writer http.ResponseWriter, ctx context.Context, body []byte) error {
	const chunk = 32 << 10
	for offset := 0; offset < len(body); {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		end := min(offset+chunk, len(body))
		written, err := writer.Write(body[offset:end])
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		offset += written
	}
	return nil
}

func (s *Server) handleEvents(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if err := requireBoundedWriter(writer, s.config.WriteDeadline); err != nil {
		http.Error(writer, "bounded streaming writes unsupported", http.StatusInternalServerError)
		return
	}
	clientID, revisions, err := s.broker.register()
	if err != nil {
		writer.Header().Set("Retry-After", "1")
		http.Error(writer, "SSE client limit reached", http.StatusServiceUnavailable)
		return
	}
	defer s.broker.deregister(clientID)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	generation := s.store.dashboard()
	lastSent := request.Header.Get("Last-Event-ID")
	if generation != nil && lastSent != generation.viewModelDigest {
		version := dashboardVersion{revision: generation.revision, viewModelDigest: generation.viewModelDigest}
		if err := s.writeRevision(writer, flusher, version); err != nil {
			return
		}
		lastSent = generation.viewModelDigest
	}
	keepalive := s.tickers.NewTicker(s.config.SSEKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case version, open := <-revisions:
			if !open {
				return
			}
			if version.viewModelDigest == lastSent {
				continue
			}
			if err := s.writeRevision(writer, flusher, version); err != nil {
				return
			}
			lastSent = version.viewModelDigest
		case <-keepalive.C():
			if err := withWriteDeadline(writer, s.config.WriteDeadline, func() error {
				if _, err := writer.Write([]byte(": keepalive\n\n")); err != nil {
					return err
				}
				flusher.Flush()
				return nil
			}); err != nil {
				return
			}
		}
	}
}

func (s *Server) writeRevision(writer http.ResponseWriter, flusher http.Flusher, version dashboardVersion) error {
	payload, err := json.Marshal(struct {
		Revision  string `json:"revision"`
		ViewModel string `json:"viewModel"`
	}{Revision: version.revision, ViewModel: version.viewModelDigest})
	if err != nil {
		return err
	}
	return withWriteDeadline(writer, s.config.WriteDeadline, func() error {
		if _, err := fmt.Fprintf(writer, "event: revision\nid: %s\ndata: %s\n\n", version.viewModelDigest, payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
}

func methodNotAllowed(writer http.ResponseWriter, allow string) {
	writer.Header().Set("Allow", allow)
	http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
}

func weakETag(revision string) string { return `W/"` + revision + `"` }

func requireBoundedWriter(writer http.ResponseWriter, duration time.Duration) error {
	controller := http.NewResponseController(writer)
	if err := controller.SetWriteDeadline(time.Now().Add(duration)); err != nil {
		return errors.Join(ErrBoundedWriterUnsupported, err)
	}
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		return errors.Join(ErrBoundedWriterUnsupported, err)
	}
	return nil
}

func withWriteDeadline(writer http.ResponseWriter, duration time.Duration, write func() error) error {
	controller := http.NewResponseController(writer)
	if err := controller.SetWriteDeadline(time.Now().Add(duration)); err != nil {
		return errors.Join(ErrBoundedWriterUnsupported, err)
	}
	writeErr := write()
	clearErr := controller.SetWriteDeadline(time.Time{})
	if clearErr != nil {
		clearErr = errors.Join(ErrBoundedWriterUnsupported, clearErr)
	}
	return errors.Join(writeErr, clearErr)
}

// StaleHeader names the response header that says this body is NOT the result
// of a successful refresh.
//
// A header, deliberately, and not a field in the snapshot: the snapshot is the
// domain's document, and degradation inside it is a domain fact that only the
// source may assert (see SnapshotReader/FetchSyncer). "My own last refresh
// attempt failed" is the transport's own fact about its own cache, so it
// travels beside the body, on the very response whose freshness is in doubt,
// and the frozen read-only route inventory is untouched.
const StaleHeader = "X-A2A-Stale"

// maxStaleHeaderBytes bounds an error rendered into a response header.
const maxStaleHeaderBytes = 512

func (s *Server) setStaleHeader(writer http.ResponseWriter) {
	err := s.LastError()
	if err == nil {
		return
	}
	writer.Header().Set(StaleHeader, headerSafe(err.Error()))
}

// headerSafe reduces an arbitrary error to something legal in a header value:
// bounded, single-line, and printable ASCII.
func headerSafe(value string) string {
	if len(value) > maxStaleHeaderBytes {
		value = value[:maxStaleHeaderBytes]
	}
	var out strings.Builder
	out.Grow(len(value))
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			out.WriteByte(' ')
			continue
		}
		out.WriteRune(r)
	}
	return strings.TrimSpace(out.String())
}
