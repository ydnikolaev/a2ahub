package localserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/operational"
)

const (
	// DefaultListen is the loopback address used when no listener is configured.
	DefaultListen = "127.0.0.1:8765"
	// DefaultRefresh is the default interval between snapshot refreshes.
	DefaultRefresh = 2 * time.Second
	// MinimumRefresh is the shortest permitted snapshot refresh interval.
	MinimumRefresh = 250 * time.Millisecond
	// MaximumRefresh is the longest permitted snapshot refresh interval.
	MaximumRefresh = time.Minute
	// MinimumSyncEvery is the shortest permitted fetch refresh interval.
	MinimumSyncEvery = 15 * time.Second
	// MaximumSyncEvery is the longest permitted fetch refresh interval.
	MaximumSyncEvery = 24 * time.Hour
	// DefaultSSEClients is the default number of concurrent SSE clients.
	DefaultSSEClients = 32
	// MaximumSSEClients is the maximum number of concurrent SSE clients.
	MaximumSSEClients = 256
	// DefaultSnapshotWriters is the default concurrent snapshot response limit.
	DefaultSnapshotWriters = 4
	// MaximumRetainedBodies bounds retained response bodies.
	MaximumRetainedBodies = 5
	// DefaultMaxShellBytes bounds the default dashboard document size.
	DefaultMaxShellBytes = 4 << 20
	// DefaultWriteDeadline bounds individual response writes.
	DefaultWriteDeadline = 2 * time.Second
	// DefaultKeepalive is the SSE idle keepalive interval.
	DefaultKeepalive = 15 * time.Second
	// DefaultMaxHeaderBytes bounds HTTP request headers.
	DefaultMaxHeaderBytes = 8 << 10
)

var (
	// ErrInvalidConfig reports invalid server configuration or lifecycle use.
	ErrInvalidConfig = errors.New("localserver: invalid configuration")
	// ErrSnapshotUnavailable reports a missing, invalid, or oversized snapshot.
	ErrSnapshotUnavailable = errors.New("localserver: snapshot unavailable")
	// ErrDegradedSnapshotRequired reports a sync error without explicit degradation.
	ErrDegradedSnapshotRequired = errors.New("localserver: sync error requires an explicit degraded snapshot")
	revisionPattern             = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Config bounds the loopback HTTP server and its refresh behavior.
type Config struct {
	Listen            string
	Refresh           time.Duration
	SyncEvery         time.Duration
	SSEKeepalive      time.Duration
	MaxSSEClients     int
	SnapshotWriters   int
	MaxSnapshotBytes  int
	MaxShellBytes     int
	WriteDeadline     time.Duration
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	ShutdownTimeout   time.Duration
}

// DefaultConfig returns the safe localserver configuration.
func DefaultConfig() Config {
	return Config{
		Listen: DefaultListen, Refresh: DefaultRefresh, SSEKeepalive: DefaultKeepalive,
		MaxSSEClients: DefaultSSEClients, SnapshotWriters: DefaultSnapshotWriters,
		MaxSnapshotBytes: operational.MaximumEncodedSnapshot, WriteDeadline: DefaultWriteDeadline,
		MaxShellBytes:     DefaultMaxShellBytes,
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: DefaultMaxHeaderBytes, ShutdownTimeout: 5 * time.Second,
	}
}

// ValidateConfig validates the public server configuration without binding a
// listener or starting goroutines. CLI flag handling uses it to reject unsafe
// addresses and out-of-range intervals before any process side effect.
func ValidateConfig(config Config) error {
	_, err := config.normalized()
	return err
}

func (c Config) normalized() (Config, error) {
	defaults := DefaultConfig()
	if c.Listen == "" {
		c.Listen = defaults.Listen
	}
	if c.Refresh == 0 {
		c.Refresh = defaults.Refresh
	}
	if c.SSEKeepalive == 0 {
		c.SSEKeepalive = defaults.SSEKeepalive
	}
	if c.MaxSSEClients == 0 {
		c.MaxSSEClients = defaults.MaxSSEClients
	}
	if c.SnapshotWriters == 0 {
		c.SnapshotWriters = defaults.SnapshotWriters
	}
	if c.MaxSnapshotBytes == 0 {
		c.MaxSnapshotBytes = defaults.MaxSnapshotBytes
	}
	if c.MaxShellBytes == 0 {
		c.MaxShellBytes = defaults.MaxShellBytes
	}
	if c.WriteDeadline == 0 {
		c.WriteDeadline = defaults.WriteDeadline
	}
	if c.ReadHeaderTimeout == 0 {
		c.ReadHeaderTimeout = defaults.ReadHeaderTimeout
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = defaults.IdleTimeout
	}
	if c.MaxHeaderBytes == 0 {
		c.MaxHeaderBytes = defaults.MaxHeaderBytes
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = defaults.ShutdownTimeout
	}
	if err := ValidateListenAddress(c.Listen); err != nil {
		return Config{}, err
	}
	if c.Refresh < MinimumRefresh || c.Refresh > MaximumRefresh ||
		(c.SyncEvery != 0 && (c.SyncEvery < MinimumSyncEvery || c.SyncEvery > MaximumSyncEvery)) ||
		c.SSEKeepalive <= 0 || c.MaxSSEClients < 1 || c.MaxSSEClients > MaximumSSEClients ||
		c.SnapshotWriters < 1 || c.SnapshotWriters > DefaultSnapshotWriters ||
		c.MaxSnapshotBytes < 1 || c.MaxSnapshotBytes > operational.MaximumEncodedSnapshot ||
		c.MaxShellBytes < 1 || c.MaxShellBytes > DefaultMaxShellBytes ||
		c.WriteDeadline <= 0 || c.ReadHeaderTimeout <= 0 || c.IdleTimeout <= 0 ||
		c.MaxHeaderBytes < 1024 || c.MaxHeaderBytes > DefaultMaxHeaderBytes || c.ShutdownTimeout <= 0 {
		return Config{}, ErrInvalidConfig
	}
	return c, nil
}

type snapshotGeneration struct {
	revision string
	body     []byte
	ctx      context.Context
	cancel   context.CancelFunc
}

type shellGeneration struct {
	body   []byte
	ctx    context.Context
	cancel context.CancelFunc
}

type snapshotStore struct {
	mu           sync.RWMutex
	current      *snapshotGeneration
	currentShell *shellGeneration
}

func (s *snapshotStore) get() *snapshotGeneration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *snapshotStore) shell() *shellGeneration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentShell
}

func (s *snapshotStore) replace(next *snapshotGeneration, shell *shellGeneration) (changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.current
	previousShell := s.currentShell
	// A semantic revision controls SSE and cancellation, but response-only
	// observation fields are intentionally excluded from it. Publish the fresh
	// bytes behind the same weak ETag while sharing the existing cancellation
	// lifetime, so current readers are not aborted and a later semantic change
	// still cancels every reader from this revision.
	if previous != nil && previous.revision == next.revision {
		next.cancel()
		shell.cancel()
		next.ctx, next.cancel = previous.ctx, previous.cancel
		shell.ctx, shell.cancel = previousShell.ctx, previousShell.cancel
		s.current = next
		s.currentShell = shell
		return false
	}
	s.current = next
	s.currentShell = shell
	if previous != nil {
		previous.cancel()
	}
	if previousShell != nil {
		previousShell.cancel()
	}
	return true
}

func (s *snapshotStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		s.current.cancel()
	}
	if s.currentShell != nil {
		s.currentShell.cancel()
	}
}

// Server exposes a read-only local operational snapshot and dashboard.
type Server struct {
	config   Config
	reader   SnapshotReader
	syncer   FetchSyncer
	renderer ShellRenderer
	tickers  TickerFactory

	store         snapshotStore
	broker        *revisionBroker
	writerSlots   chan struct{}
	publicationMu sync.Mutex

	mu           sync.Mutex
	listenerHost string
	listenerPort string
	lastError    error
	serving      bool
}

// New creates a local server with explicit snapshot and rendering dependencies.
func New(config Config, reader SnapshotReader, syncer FetchSyncer, renderer ShellRenderer, tickers TickerFactory) (*Server, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	if reader == nil || renderer == nil || normalized.SyncEvery != 0 && syncer == nil {
		return nil, fmt.Errorf("%w: reader, renderer, and enabled syncer are required", ErrInvalidConfig)
	}
	if tickers == nil {
		tickers = realTickerFactory{}
	}
	host, port, _ := net.SplitHostPort(normalized.Listen)
	return &Server{
		config: normalized, reader: reader, syncer: syncer, renderer: renderer, tickers: tickers,
		broker: newRevisionBroker(normalized.MaxSSEClients), writerSlots: make(chan struct{}, normalized.SnapshotWriters),
		listenerHost: host, listenerPort: port,
	}, nil
}

// Handler returns the read-only HTTP handler.
func (s *Server) Handler() http.Handler { return s.routeHandler() }

// LastError returns the most recent background refresh or sync error.
func (s *Server) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastError
}

func (s *Server) recordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.lastError = err
	s.mu.Unlock()
}

func (s *Server) publish(ctx context.Context, snapshot operational.Snapshot) error {
	if snapshot.SchemaVersion != operational.SchemaVersion || !revisionPattern.MatchString(snapshot.Revision) {
		return fmt.Errorf("%w: invalid operational snapshot", ErrSnapshotUnavailable)
	}
	body, err := operational.CanonicalJSON(snapshot)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSnapshotUnavailable, err)
	}
	if len(body) > s.config.MaxSnapshotBytes {
		return fmt.Errorf("%w: snapshot has %d bytes, maximum %d", ErrSnapshotUnavailable, len(body), s.config.MaxSnapshotBytes)
	}
	shell, err := s.renderer.Render(ctx, snapshot)
	if err != nil {
		return fmt.Errorf("localserver: render shell: %w", err)
	}
	if len(shell) > s.config.MaxShellBytes {
		return fmt.Errorf("%w: shell has %d bytes, maximum %d", ErrSnapshotUnavailable, len(shell), s.config.MaxShellBytes)
	}
	generationCtx, cancel := context.WithCancel(context.Background())
	generation := &snapshotGeneration{revision: snapshot.Revision, body: body, ctx: generationCtx, cancel: cancel}
	shellCtx, shellCancel := context.WithCancel(context.Background())
	shellBody := &shellGeneration{body: append([]byte(nil), shell...), ctx: shellCtx, cancel: shellCancel}
	if s.store.replace(generation, shellBody) {
		s.broker.publish(snapshot.Revision)
	}
	return nil
}

func (s *Server) refresh(ctx context.Context) error {
	s.publicationMu.Lock()
	defer s.publicationMu.Unlock()
	snapshot, err := s.reader.Snapshot(ctx)
	if err != nil {
		return err
	}
	return s.publish(ctx, snapshot)
}

func (s *Server) sync(ctx context.Context) error {
	s.publicationMu.Lock()
	defer s.publicationMu.Unlock()
	snapshot, syncErr := s.syncer.Sync(ctx)
	if syncErr != nil && (snapshot.Revision == "" || !snapshotHasDegradation(snapshot)) {
		return errors.Join(syncErr, ErrDegradedSnapshotRequired)
	}
	if snapshot.Revision == "" {
		return ErrSnapshotUnavailable
	}
	if err := s.publish(ctx, snapshot); err != nil {
		return errors.Join(syncErr, err)
	}
	return syncErr
}

func snapshotHasDegradation(snapshot operational.Snapshot) bool {
	if len(snapshot.Unavailable) > 0 {
		return true
	}
	for _, source := range snapshot.Sources {
		if source.Freshness == operational.SourceStale || source.Freshness == operational.SourceUnavailable ||
			source.Freshness == operational.SourceDegraded {
			return true
		}
	}
	return false
}

func (s *Server) pollLoop(ctx context.Context) {
	ticker := s.tickers.NewTicker(s.config.Refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			if err := s.refresh(ctx); err != nil {
				s.recordError(err)
			}
		}
	}
}

func (s *Server) syncLoop(ctx context.Context) {
	ticker := s.tickers.NewTicker(s.config.SyncEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			if err := s.sync(ctx); err != nil {
				s.recordError(err)
			}
		}
	}
}

// Serve publishes snapshots and serves the supplied loopback listener until cancellation.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if err := validateListener(listener); err != nil {
		return err
	}
	s.mu.Lock()
	if s.serving {
		s.mu.Unlock()
		return fmt.Errorf("%w: server already started", ErrInvalidConfig)
	}
	s.serving = true
	if tcp, ok := listener.Addr().(*net.TCPAddr); ok {
		s.listenerHost = tcp.IP.String()
		s.listenerPort = strconv.Itoa(tcp.Port)
	}
	s.mu.Unlock()
	if err := s.refresh(ctx); err != nil {
		s.mu.Lock()
		s.serving = false
		s.mu.Unlock()
		return fmt.Errorf("%w: %w", ErrSnapshotUnavailable, err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpServer := &http.Server{
		Handler: s.Handler(), ReadHeaderTimeout: s.config.ReadHeaderTimeout,
		IdleTimeout: s.config.IdleTimeout, MaxHeaderBytes: s.config.MaxHeaderBytes,
		WriteTimeout: 0,
	}
	var group sync.WaitGroup
	group.Add(1)
	go s.owned(&group, func() { s.pollLoop(runCtx) })
	if s.config.SyncEvery != 0 {
		group.Add(1)
		go s.owned(&group, func() { s.syncLoop(runCtx) })
	}
	serveErrors := make(chan error, 1)
	group.Add(1)
	go func() {
		defer group.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				serveErrors <- fmt.Errorf("localserver: serve panic: %v", recovered)
			}
		}()
		serveErrors <- httpServer.Serve(listener)
	}()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serveErrors:
	}
	cancel()
	s.broker.close()
	s.store.close()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	shutdownCancel()
	group.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	if shutdownErr != nil {
		return shutdownErr
	}
	return nil
}

func (s *Server) owned(group *sync.WaitGroup, run func()) {
	defer group.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			s.recordError(fmt.Errorf("localserver: owned goroutine panic: %v", recovered))
		}
	}()
	run()
}
