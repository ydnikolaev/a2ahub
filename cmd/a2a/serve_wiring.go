package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/html"
	"github.com/ydnikolaev/a2ahub/internal/localserver"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/operationalsource"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

type operationalWallClock struct{}

func (operationalWallClock) Now() time.Time { return time.Now() }

type operationalPendingDecoder struct{}

func (operationalPendingDecoder) DecodePending(raw []byte) (operationalsource.PendingResult, error) {
	result, err := space.DecodeWriteResultJournal(raw)
	if err != nil {
		return operationalsource.PendingResult{}, err
	}
	return operationalsource.PendingResult{
		Stage: string(result.Stage), State: string(result.State), RemainingAction: string(result.RemainingAction),
	}, nil
}

type operationalRuntime struct {
	source           *operationalsource.Source
	renderer         *html.DashboardRenderer
	historyValidator space.ContractHistoryDocumentValidator
	leases           *space.WorkLeaseStore
}

func (runtime *operationalRuntime) Close() error {
	if runtime == nil || runtime.leases == nil {
		return nil
	}
	return runtime.leases.Close()
}

func buildOperationalRuntime(p paths, store *cache.Store) (*operationalRuntime, error) {
	return buildOperationalRuntimeWithClock(p, store, operationalWallClock{})
}

func buildOperationalRuntimeWithClock(p paths, store *cache.Store, clock operational.Clock) (*operationalRuntime, error) {
	if store == nil {
		return nil, fmt.Errorf("operational runtime: cache store is required")
	}
	if clock == nil {
		return nil, fmt.Errorf("operational runtime: clock is required")
	}
	cacheRoot := cacheDirOf(p)
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return nil, fmt.Errorf("operational runtime: create cache root: %w", err)
	}
	leases, err := space.NewWorkLeaseStore(cacheRoot, space.WithWorkLeaseClock(clock))
	if err != nil {
		return nil, fmt.Errorf("operational runtime: open work lease store: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = leases.Close() // reason: preserve the construction error
		}
	}()
	projectID, err := space.CanonicalProjectID(p.projectRoot)
	if err != nil {
		return nil, err
	}
	spaces := make([]string, 0, len(store.SpaceMirrors()))
	for _, mirror := range store.SpaceMirrors() {
		spaces = append(spaces, mirror.SpaceID)
	}
	fetcher, err := operationalsource.NewCacheFetcher(store)
	if err != nil {
		return nil, err
	}
	source, err := operationalsource.New(operationalsource.Config{
		ProjectID: projectID, Spaces: spaces, Limits: operational.DefaultLimits(),
	}, store, leases, operationalPendingDecoder{}, fetcher, clock)
	if err != nil {
		return nil, err
	}
	engine, err := newEngine()
	if err != nil {
		return nil, fmt.Errorf("operational runtime: load validation engine: %w", err)
	}
	historyValidator := contractHistoryDocumentEngine{engine: engine}
	renderer, err := html.NewDashboardRendererWithContractHistory(store, store.OwnSystem(), historyValidator)
	if err != nil {
		return nil, err
	}
	closeOnError = false
	return &operationalRuntime{source: source, renderer: renderer, historyValidator: historyValidator, leases: leases}, nil
}

type productionServeLauncher struct {
	source   *operationalsource.Source
	renderer *html.DashboardRenderer
	opener   func(string) error
	warn     func(error)
	errorLog io.Writer
}

func (launcher productionServeLauncher) Serve(ctx context.Context, options cli.ServeOptions) error {
	// The operator channel for a refresh loop that starts failing. Without it
	// a serve whose background refresh dies keeps answering with data frozen
	// at the last good snapshot and says nothing to anyone.
	options.Config.ErrorLog = launcher.errorLog
	server, err := localserver.New(options.Config, launcher.source, launcher.source, launcher.renderer, nil)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", options.Config.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", options.Config.Listen, err)
	}
	defer func() {
		_ = listener.Close() // reason: server owns normal close; this covers pre-serve failures
	}()
	if options.Open && launcher.opener != nil {
		if err := launcher.opener("http://" + listener.Addr().String() + "/"); err != nil && launcher.warn != nil {
			launcher.warn(fmt.Errorf("open browser: %w", err))
		}
	}
	return server.Serve(ctx, listener)
}

func runServe(args []string, stdout, stderr io.Writer) int {
	p, err := resolvePaths()
	if err != nil {
		return fail(stderr, err)
	}
	store, err := buildStore(context.Background(), p)
	if err != nil {
		return failf(stderr, "a2a serve: %v", err)
	}
	runtimeState, err := buildOperationalRuntime(p, store)
	if err != nil {
		return failf(stderr, "a2a serve: %v", err)
	}
	defer func() {
		if err := runtimeState.Close(); err != nil {
			_, _ = fmt.Fprintf(stderr, "a2a serve: close local work store: %v\n", err)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	launcher := productionServeLauncher{
		source: runtimeState.source, renderer: runtimeState.renderer, opener: openLocalDashboard,
		warn: func(err error) { _, _ = fmt.Fprintf(stderr, "a2a serve: %v\n", err) },
	}
	return cli.NewServeCommand(launcher).Run(ctx, args, stdio(stdout, stderr))
}

func openLocalDashboard(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		//nolint:gosec // target is a locally generated loopback URL, passed as data to a fixed executable.
		command = exec.Command("open", target)
	case "windows":
		//nolint:gosec // target is a locally generated loopback URL, passed as data to a fixed executable.
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		//nolint:gosec // target is a locally generated loopback URL, passed as data to a fixed executable.
		command = exec.Command("xdg-open", target)
	}
	return command.Start()
}
