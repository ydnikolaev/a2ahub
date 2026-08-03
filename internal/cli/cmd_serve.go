package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/localserver"
)

// ServeOptions is the complete thin-transport request for the production
// loopback server launcher.
type ServeOptions struct {
	Config localserver.Config
	Open   bool
}

// ServeLauncher runs the configured loopback server.
type ServeLauncher interface {
	Serve(context.Context, ServeOptions) error
}

// ServeCommand is the thin CLI transport for the loopback server.
type ServeCommand struct{ launcher ServeLauncher }

// NewServeCommand constructs a ServeCommand using the supplied launcher.
func NewServeCommand(launcher ServeLauncher) *ServeCommand {
	return &ServeCommand{launcher: launcher}
}

// Name returns the command name.
func (c *ServeCommand) Name() string { return "serve" }

// Synopsis returns a concise command description.
func (c *ServeCommand) Synopsis() string {
	return "serve the local operational dashboard and snapshot API on loopback"
}

// Run parses command arguments and starts the loopback server.
func (c *ServeCommand) Run(ctx context.Context, args []string, stdio IO) int {
	defaults := localserver.DefaultConfig()
	fs := flag.NewFlagSet(c.Name(), flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	listen := fs.String("listen", defaults.Listen, "loopback listen address")
	refresh := fs.Duration("refresh", defaults.Refresh, "local snapshot refresh interval")
	syncEvery := fs.Duration("sync-every", defaults.SyncEvery, "opt-in fetch-only sync interval (0 disables network)")
	open := fs.Bool("open", false, "open the local dashboard after a successful bind")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a serve [--listen 127.0.0.1:8765] [--refresh 2s] [--sync-every 0|duration] [--open=false]")
		return 2
	}
	config := defaults
	config.Listen = *listen
	config.Refresh = *refresh
	config.SyncEvery = *syncEvery
	if err := localserver.ValidateConfig(config); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a serve: %v\n", err)
		return 2
	}
	if c.launcher == nil {
		_, _ = fmt.Fprintln(stdio.Stderr, "a2a serve: local server is not configured")
		return 1
	}
	if err := c.launcher.Serve(ctx, ServeOptions{Config: config, Open: *open}); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a serve: %v\n", err)
		return 1
	}
	return 0
}

var _ Command = (*ServeCommand)(nil)
