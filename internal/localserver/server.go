package localserver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
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
	// DefaultMaxShellBytes bounds the default dashboard document size.
	DefaultMaxShellBytes = 4 << 20
	// DefaultMaxDashboardBytes bounds the default canonical dashboard view model.
	DefaultMaxDashboardBytes = 2 << 20
	// MaximumDashboardBytes is the configurable hard maximum for the canonical dashboard view model.
	MaximumDashboardBytes = 4 << 20
	// MaximumRetainedBytes is the conservative retained-body ceiling: current
	// snapshot, shell and dashboard plus four superseded largest bodies.
	MaximumRetainedBytes = 88 << 20
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
	MaxDashboardBytes int
	WriteDeadline     time.Duration
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	ShutdownTimeout   time.Duration
	// ErrorLog receives ONE line when the background refresh starts failing
	// and one when it recovers — never one per tick, which at the minimum
	// refresh interval would be four lines a second. Optional; nil silences
	// the operator channel and leaves the response header as the only signal.
	ErrorLog io.Writer
}

// DefaultConfig returns the safe localserver configuration.
func DefaultConfig() Config {
	return Config{
		Listen: DefaultListen, Refresh: DefaultRefresh, SSEKeepalive: DefaultKeepalive,
		MaxSSEClients: DefaultSSEClients, SnapshotWriters: DefaultSnapshotWriters,
		MaxSnapshotBytes: operational.MaximumEncodedSnapshot, WriteDeadline: DefaultWriteDeadline,
		MaxShellBytes: DefaultMaxShellBytes, MaxDashboardBytes: DefaultMaxDashboardBytes,
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
	if c.MaxDashboardBytes == 0 {
		c.MaxDashboardBytes = defaults.MaxDashboardBytes
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
		c.MaxDashboardBytes < 1 || c.MaxDashboardBytes > MaximumDashboardBytes ||
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

type dashboardGeneration struct {
	revision           string
	contentFingerprint string
	shell              []byte
	viewModel          []byte
	viewModelDigest    string
	ctx                context.Context
	cancel             context.CancelFunc
}

type snapshotStore struct {
	mu               sync.RWMutex
	current          *snapshotGeneration
	currentDashboard *dashboardGeneration
	closed           bool
}

func (s *snapshotStore) get() *snapshotGeneration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *snapshotStore) dashboard() *dashboardGeneration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentDashboard
}

func (s *snapshotStore) replace(next *snapshotGeneration, dashboard *dashboardGeneration) (dashboardChanged bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		next.cancel()
		dashboard.cancel()
		return false
	}
	previous := s.current
	previousDashboard := s.currentDashboard
	// Snapshot observation fields may refresh behind the same revision. Preserve
	// that route's existing cancellation lifetime while replacing its exact body.
	if previous != nil && previous.revision == next.revision {
		next.cancel()
		next.ctx, next.cancel = previous.ctx, previous.cancel
	} else if previous != nil {
		previous.cancel()
	}
	s.current = next

	if previousDashboard != nil && previousDashboard.revision == dashboard.revision &&
		previousDashboard.contentFingerprint == dashboard.contentFingerprint {
		dashboard.cancel()
		return false
	}
	s.currentDashboard = dashboard
	if previousDashboard != nil {
		previousDashboard.cancel()
	}
	return true
}

func (s *snapshotStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.current != nil {
		s.current.cancel()
	}
	if s.currentDashboard != nil {
		s.currentDashboard.cancel()
	}
	s.current = nil
	s.currentDashboard = nil
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

// LastError reports the CURRENT background degradation: the error from the
// latest failed refresh or sync attempt, or nil once an attempt succeeds
// again. It is the same fact the stale-snapshot response header carries.
func (s *Server) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastError
}

// recordError marks the served generation as no longer the result of a
// successful attempt.
//
// A refresh loop that fails every tick used to be completely invisible: the
// keepalives kept flowing, /api/v1/snapshot kept answering 200, and the data
// silently stayed frozen at the last good generation for as long as the
// process ran. A consumer — an agent polling the dashboard most of all — had
// no way to tell a live snapshot from one that stopped updating an hour ago.
// The error was stored in a field nothing read.
func (s *Server) recordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	first := s.lastError == nil
	s.lastError = err
	log := s.config.ErrorLog
	s.mu.Unlock()
	if first && log != nil {
		_, _ = fmt.Fprintf(log, "a2a serve: background refresh is failing; served data is frozen at the last good snapshot: %v\n", err)
	}
}

// recordSuccess clears the degradation after an attempt that worked.
func (s *Server) recordSuccess() {
	s.mu.Lock()
	recovered := s.lastError != nil
	s.lastError = nil
	log := s.config.ErrorLog
	s.mu.Unlock()
	if recovered && log != nil {
		_, _ = fmt.Fprintln(log, "a2a serve: background refresh recovered; served data is current again")
	}
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
	// Render every successful poll: non-operational dashboard facts can change
	// independently from the operational revision.
	shell, viewModel, fingerprint, err := s.renderer.Render(ctx, snapshot)
	if err != nil {
		return fmt.Errorf("localserver: render dashboard: %w", err)
	}
	if len(shell) > s.config.MaxShellBytes {
		return fmt.Errorf("%w: shell has %d bytes, maximum %d", ErrSnapshotUnavailable, len(shell), s.config.MaxShellBytes)
	}
	if len(viewModel) > s.config.MaxDashboardBytes {
		return fmt.Errorf("%w: dashboard has %d bytes, maximum %d", ErrSnapshotUnavailable, len(viewModel), s.config.MaxDashboardBytes)
	}
	if fingerprint == "" {
		return fmt.Errorf("%w: dashboard content fingerprint is empty", ErrSnapshotUnavailable)
	}
	generationCtx, cancel := context.WithCancel(context.Background())
	generation := &snapshotGeneration{revision: snapshot.Revision, body: body, ctx: generationCtx, cancel: cancel}
	dashboardCtx, dashboardCancel := context.WithCancel(context.Background())
	digest := sha256Digest(viewModel)
	dashboard := &dashboardGeneration{
		revision: snapshot.Revision, contentFingerprint: fingerprint,
		shell: append([]byte(nil), shell...), viewModel: append([]byte(nil), viewModel...), viewModelDigest: digest,
		ctx: dashboardCtx, cancel: dashboardCancel,
	}
	if s.store.replace(generation, dashboard) {
		s.broker.publish(dashboardVersion{revision: snapshot.Revision, viewModelDigest: digest})
	}
	return nil
}

func sha256Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("sha256:%x", sum)
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
				continue
			}
			s.recordSuccess()
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
				continue
			}
			s.recordSuccess()
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
