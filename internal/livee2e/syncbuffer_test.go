package livee2e

import (
	"strings"
	"sync"
	"testing"
)

func TestSyncBufferIsReadableWhileBeingWritten(t *testing.T) {
	t.Parallel()
	// Under -race this fails outright on an unguarded buffer, which is the
	// point: the live tier that uses it never runs with -race.
	buffer := &syncBuffer{}
	var writers sync.WaitGroup
	for range 8 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for range 32 {
				if _, err := buffer.Write([]byte("serve: line\n")); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}()
	}
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 32 {
				_ = buffer.String()
			}
		}()
	}
	writers.Wait()
	readers.Wait()
	if got := strings.Count(buffer.String(), "serve: line"); got != 8*32 {
		t.Fatalf("collected %d lines, want %d", got, 8*32)
	}
}
