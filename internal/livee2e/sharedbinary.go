package livee2e

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// sharedBuildTimeout bounds the one shared `go build`. Assumed, not measured:
// a cold build of ./cmd/a2a is well under it, and a hung toolchain must not
// stall the whole tier for its 20m ceiling.
const sharedBuildTimeout = 5 * time.Minute

// onceBuilder builds a value once per test process into a process-level temp
// dir that no harness cleanup owns, and hands every caller the same result
// (path or error). The build func is injected so this untagged file never
// names a tagged symbol; the tagged harness file sets it in init().
type onceBuilder struct {
	build func(ctx context.Context, dir string) (string, error)

	once sync.Once
	dir  string
	path string
	err  error

	mu sync.Mutex // guards dir against a Close racing the build
}

// Get runs build exactly once and returns its path or its error to every
// caller. The build runs under its own context, not any caller's, so one
// cancelled test cannot poison the result for the rest.
func (b *onceBuilder) Get() (string, error) {
	b.once.Do(func() {
		if b.build == nil {
			b.err = fmt.Errorf("livee2e: onceBuilder: no build func set")
			return
		}
		dir, err := os.MkdirTemp("", "a2a-logic-bin-")
		if err != nil {
			b.err = fmt.Errorf("livee2e: onceBuilder: %w", err)
			return
		}
		b.mu.Lock()
		b.dir = dir
		b.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), sharedBuildTimeout)
		defer cancel()
		b.path, b.err = b.build(ctx, dir)
	})
	return b.path, b.err
}

// Close removes the process-level dir. It is idempotent and safe before any
// Get.
func (b *onceBuilder) Close() error {
	b.mu.Lock()
	dir := b.dir
	b.dir = ""
	b.mu.Unlock()
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("livee2e: onceBuilder: remove %s: %w", dir, err)
	}
	return nil
}

// sharedLogicBinary is the one `a2a` build per test process; the tagged
// harness file's init() sets its build func.
var sharedLogicBinary onceBuilder
