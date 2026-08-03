package localserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	if request.Method == http.MethodGet {
		select {
		case s.writerSlots <- struct{}{}:
			defer func() { <-s.writerSlots }()
		default:
			writer.Header().Set("Retry-After", "1")
			http.Error(writer, "response writer limit reached", http.StatusServiceUnavailable)
			return
		}
	}
	shell := s.store.shell()
	if shell == nil {
		http.Error(writer, "snapshot unavailable", http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Length", fmt.Sprint(len(shell.body)))
	if request.Method == http.MethodHead {
		writer.WriteHeader(http.StatusOK)
		return
	}
	if err := requireBoundedWriter(writer, s.config.WriteDeadline); err != nil {
		writer.Header().Del("Content-Length")
		http.Error(writer, "bounded response writes unsupported", http.StatusInternalServerError)
		return
	}
	if err := withWriteDeadline(writer, s.config.WriteDeadline, func() error {
		return writeBody(writer, shell.ctx, shell.body)
	}); err != nil && !errors.Is(err, context.Canceled) {
		s.recordError(err)
	}
}

func (s *Server) handleSnapshot(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, "GET, HEAD")
		return
	}
	if request.Method == http.MethodGet {
		select {
		case s.writerSlots <- struct{}{}:
			defer func() { <-s.writerSlots }()
		default:
			writer.Header().Set("Retry-After", "1")
			http.Error(writer, "snapshot writer limit reached", http.StatusServiceUnavailable)
			return
		}
	}
	generation := s.store.get()
	if generation == nil {
		http.Error(writer, "snapshot unavailable", http.StatusServiceUnavailable)
		return
	}
	etag := weakETag(generation.revision)
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Cache-Control", "no-store")
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
	generation := s.store.get()
	lastSent := request.Header.Get("Last-Event-ID")
	if generation != nil && lastSent != generation.revision {
		if err := s.writeRevision(writer, flusher, generation.revision); err != nil {
			return
		}
		lastSent = generation.revision
	}
	keepalive := s.tickers.NewTicker(s.config.SSEKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case revision, open := <-revisions:
			if !open {
				return
			}
			if revision == lastSent {
				continue
			}
			if err := s.writeRevision(writer, flusher, revision); err != nil {
				return
			}
			lastSent = revision
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

func (s *Server) writeRevision(writer http.ResponseWriter, flusher http.Flusher, revision string) error {
	payload, err := json.Marshal(struct {
		Revision string `json:"revision"`
	}{Revision: revision})
	if err != nil {
		return err
	}
	return withWriteDeadline(writer, s.config.WriteDeadline, func() error {
		if _, err := fmt.Fprintf(writer, "event: revision\nid: %s\ndata: %s\n\n", revision, payload); err != nil {
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
