package livee2e

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStageRecorder(t *testing.T) {
	t.Parallel()

	t.Run("lines in call order with monotone offsets", func(t *testing.T) {
		t.Parallel()
		start := time.Unix(1000, 0)
		clock := start
		var buf bytes.Buffer
		r := newStageRecorder(start, func() time.Time { return clock }, &buf)

		r.Mark("setup", "start")
		clock = clock.Add(1500 * time.Millisecond)
		r.Mark("setup", "end")
		clock = clock.Add(2 * time.Second)
		r.Mark("family-matrix", "start")

		want := "logic-stage: setup start +0.0\n" +
			"logic-stage: setup end +1.5\n" +
			"logic-stage: family-matrix start +3.5\n"
		if got := buf.String(); got != want {
			t.Fatalf("lines:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("concurrent marks stay whole lines", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		r := newStageRecorder(time.Unix(0, 0), func() time.Time { return time.Unix(2, 0) }, &buf)
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r.Mark(fmt.Sprintf("group-%d", i), "start")
				r.Mark(fmt.Sprintf("group-%d", i), "end")
			}()
		}
		wg.Wait()
		lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
		if len(lines) != 64 {
			t.Fatalf("got %d lines, want 64", len(lines))
		}
		for _, l := range lines {
			if !strings.HasPrefix(l, "logic-stage: group-") || !strings.HasSuffix(l, " +2.0") {
				t.Fatalf("torn or malformed line %q", l)
			}
		}
	})

	t.Run("nil recorder writes nothing", func(t *testing.T) {
		t.Parallel()
		var r *stageRecorder
		r.Mark("setup", "start") // must not panic
	})
}

func TestSpawnCounter(t *testing.T) {
	t.Parallel()

	t.Run("buckets by first argument with total and mean", func(t *testing.T) {
		t.Parallel()
		var c spawnCounter
		c.Add([]string{"propose", "--x"}, 1*time.Second)
		c.Add([]string{"propose"}, 2*time.Second)
		c.Add([]string{"list"}, 500*time.Millisecond)
		c.Add(nil, 100*time.Millisecond)
		got := strings.Join(c.Lines(), "\n")
		want := strings.Join([]string{
			"logic-spawns: <none> n=1 total=0.1 mean=100",
			"logic-spawns: list n=1 total=0.5 mean=500",
			"logic-spawns: propose n=2 total=3.0 mean=1500",
		}, "\n")
		if got != want {
			t.Fatalf("lines:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("concurrent adds", func(t *testing.T) {
		t.Parallel()
		var c spawnCounter
		var wg sync.WaitGroup
		for g := 0; g < 16; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 100; i++ {
					c.Add([]string{"v"}, time.Millisecond)
				}
			}()
		}
		wg.Wait()
		lines := c.Lines()
		if len(lines) != 1 || !strings.Contains(lines[0], " n=1600 ") {
			t.Fatalf("lines = %v, want one line with n=1600", lines)
		}
	})

	t.Run("reset empties", func(t *testing.T) {
		t.Parallel()
		var c spawnCounter
		c.Add([]string{"v"}, time.Second)
		c.Reset()
		if got := c.Lines(); len(got) != 0 {
			t.Fatalf("after Reset: %v", got)
		}
	})
}
