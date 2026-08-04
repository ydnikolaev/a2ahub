package livee2e

import (
	"bytes"
	"sync"
)

// syncBuffer collects a child process's combined output so it can be read
// while that process is still running.
//
// os/exec copies into a non-*os.File writer from goroutines it only reaps in
// Wait, so a caller that reads the buffer on a startup-failure path — before
// Wait — races the copier. The live tier is not run under -race, so such a race
// is never reported; it silently corrupts the one diagnostic available when the
// child fails to come up.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
