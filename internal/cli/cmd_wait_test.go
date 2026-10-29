package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

type fakeWaitSource struct {
	res cache.WaitResult
	err error
	got cache.WaitRequest
	// deadline records whether the context handed to Wait carried one.
	deadline bool
	calls    int
	notify   []string
}

func (f *fakeWaitSource) Wait(ctx context.Context, req cache.WaitRequest) (cache.WaitResult, error) {
	f.calls++
	f.got = req
	_, f.deadline = ctx.Deadline()
	for _, line := range f.notify {
		req.Notify(line)
	}
	return f.res, f.err
}

func runWait(t *testing.T, src *fakeWaitSource, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = NewWaitCommand(src).Run(context.Background(), args, IO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb})
	return code, out.String(), errb.String()
}

func waitTestEvent() cache.WaitEvent {
	return cache.WaitEvent{
		Space: "sp1", Event: "01JEVENT", Actor: "seomatrix", Transition: "acknowledge",
		Subject: "XW-axon-1", Thread: "T-axon-1", Why: cache.WhyAuthored, At: "2026-10-29T10:00:00Z",
	}
}

func TestWait_PrintsEvent(t *testing.T) {
	t.Parallel()
	t.Run("text line", func(t *testing.T) {
		t.Parallel()
		src := &fakeWaitSource{res: cache.WaitResult{Events: []cache.WaitEvent{waitTestEvent()}}}
		code, stdout, stderr := runWait(t, src)
		if code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr)
		}
		want := "sp1\t01JEVENT\tseomatrix\tacknowledge\tXW-axon-1\tauthored\n"
		if stdout != want {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
		if src.got.Selector.Kind != cache.WaitMine {
			t.Fatalf("selector = %+v, want --mine by default", src.got.Selector)
		}
		if !src.deadline {
			t.Fatal("the wait ran without the --timeout deadline")
		}
	})
	t.Run("json", func(t *testing.T) {
		t.Parallel()
		src := &fakeWaitSource{res: cache.WaitResult{Events: []cache.WaitEvent{waitTestEvent()}}}
		code, stdout, _ := runWait(t, src, "--json", "--subject", "XW-axon-1")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		var got struct {
			Events     []map[string]string `json:"events"`
			TimedOut   bool                `json:"timed_out"`
			Unmeasured []string            `json:"unmeasured"`
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
		}
		if len(got.Events) != 1 || got.TimedOut || got.Unmeasured == nil || len(got.Unmeasured) != 0 {
			t.Fatalf("json = %+v, want one event, timed_out false, unmeasured []", got)
		}
		for k, want := range map[string]string{"space": "sp1", "event": "01JEVENT", "actor": "seomatrix", "transition": "acknowledge", "subject": "XW-axon-1", "thread": "T-axon-1", "why": "authored", "at": "2026-10-29T10:00:00Z"} {
			if got.Events[0][k] != want {
				t.Errorf("events[0].%s = %q, want %q", k, got.Events[0][k], want)
			}
		}
		if src.got.Selector != (cache.WaitSelector{Kind: cache.WaitSubject, ID: "XW-axon-1"}) {
			t.Fatalf("selector = %+v, want subject XW-axon-1", src.got.Selector)
		}
	})
	t.Run("space and thread are passed through", func(t *testing.T) {
		t.Parallel()
		src := &fakeWaitSource{res: cache.WaitResult{Events: []cache.WaitEvent{waitTestEvent()}}}
		runWait(t, src, "--space", "sp1", "--thread", "T-1")
		if len(src.got.Spaces) != 1 || src.got.Spaces[0] != "sp1" || src.got.Selector != (cache.WaitSelector{Kind: cache.WaitThread, ID: "T-1"}) {
			t.Fatalf("request = %+v", src.got)
		}
	})
}

func TestWait_Timeout(t *testing.T) {
	t.Parallel()
	src := &fakeWaitSource{res: cache.WaitResult{TimedOut: true}}
	code, stdout, stderr := runWait(t, src, "--timeout", "1s")
	if code != 124 {
		t.Fatalf("exit = %d, want 124 (stderr %q)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want nothing on a timeout", stdout)
	}

	src = &fakeWaitSource{res: cache.WaitResult{TimedOut: true}}
	code, stdout, _ = runWait(t, src, "--timeout", "1s", "--json")
	var got struct {
		Events     []any    `json:"events"`
		TimedOut   bool     `json:"timed_out"`
		Unmeasured []string `json:"unmeasured"`
	}
	if code != 124 {
		t.Fatalf("--json exit = %d, want 124", code)
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if !got.TimedOut || got.Events == nil || got.Unmeasured == nil {
		t.Fatalf("json = %s, want timed_out true and [] (never null) for events and unmeasured", stdout)
	}
}

func TestWait_Unmeasured(t *testing.T) {
	t.Parallel()
	src := &fakeWaitSource{res: cache.WaitResult{TimedOut: true, Unmeasured: []string{"sp2"}}}
	code, stdout, stderr := runWait(t, src)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (a space could not be observed is not a quiet 124)", code)
	}
	if stdout != "" || !strings.Contains(stderr, "sp2") {
		t.Fatalf("stdout = %q stderr = %q, want nothing on stdout and the space named on stderr", stdout, stderr)
	}

	src = &fakeWaitSource{res: cache.WaitResult{TimedOut: true, Unmeasured: []string{"sp2"}}}
	_, stdout, _ = runWait(t, src, "--json")
	if !strings.Contains(stdout, `"unmeasured":["sp2"]`) {
		t.Fatalf("json = %s, want unmeasured to name sp2", stdout)
	}
}

func TestWait_Error(t *testing.T) {
	t.Parallel()
	src := &fakeWaitSource{err: errors.New("no mirror")}
	code, stdout, stderr := runWait(t, src)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "no mirror") {
		t.Fatalf("exit = %d stdout = %q stderr = %q, want 1, nothing on stdout, the error on stderr", code, stdout, stderr)
	}
	src = &fakeWaitSource{}
	if code, _, _ := runWait(t, src); code != 1 {
		t.Fatalf("a source that ends without events or a timeout: exit = %d, want 1", code)
	}
}

// TestWait_NoFreeText: D-014. A hostile value in a field never forges a
// second line or column, and the diagnostics pass through the same scrub.
func TestWait_NoFreeText(t *testing.T) {
	t.Parallel()
	hostile := waitTestEvent()
	hostile.Subject = "XW-1\nsp9\tFORGED\tline"
	src := &fakeWaitSource{
		res:    cache.WaitResult{Events: []cache.WaitEvent{hostile}},
		notify: []string{"undecodable event x.yaml\nFORGED: second line"},
	}
	code, stdout, stderr := runWait(t, src)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n"); len(lines) != 1 || strings.Count(lines[0], "\t") != 5 {
		t.Fatalf("stdout = %q, want exactly one line of six tab-separated fields", stdout)
	}
	if got := strings.Count(stderr, "\n"); got != 1 {
		t.Fatalf("stderr = %q, want a diagnostic confined to one line, got %d", stderr, got)
	}
}

func TestWait_Usage(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"two selectors":          {"--mine", "--subject", "XW-1"},
		"subject and thread":     {"--subject", "XW-1", "--thread", "T-1"},
		"timeout zero":           {"--timeout", "0"},
		"timeout negative":       {"--timeout", "-5s"},
		"empty subject":          {"--subject", ""},
		"positional argument":    {"XW-1"},
		"unknown flag":           {"--nope"},
		"unparseable timeout":    {"--timeout", "soon"},
		"mine twice is one flag": nil, // control: must NOT be a usage error
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := &fakeWaitSource{res: cache.WaitResult{TimedOut: true}}
			code, _, stderr := runWait(t, src, args...)
			if args == nil {
				if code == 2 {
					t.Fatalf("a plain `a2a wait` is a usage error: %q", stderr)
				}
				return
			}
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr %q)", code, stderr)
			}
			if src.calls != 0 {
				t.Fatalf("a usage error still ran the wait")
			}
		})
	}

	t.Run("refusal names the usage and the workflow", func(t *testing.T) {
		t.Parallel()
		_, _, stderr := runWait(t, &fakeWaitSource{}, "--mine", "--thread", "T-1")
		if !strings.Contains(stderr, "usage: a2a wait") || !strings.Contains(stderr, workflowLine("loop-watch")) {
			t.Fatalf("stderr = %q, want the usage line and %q", stderr, workflowLine("loop-watch"))
		}
	})
}

func TestWait_NameAndSynopsisNeedNoDeps(t *testing.T) {
	t.Parallel()
	c := NewWaitCommand(nil)
	if c.Name() != "wait" || !strings.Contains(c.Synopsis(), "wait") {
		t.Fatalf("name/synopsis = %q / %q", c.Name(), c.Synopsis())
	}
}
