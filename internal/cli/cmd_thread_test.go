package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// threadResp builds a response-kind envelope map (mirrors internal/cache's
// own threadview_test.go twResponse — reimplemented here, file-private,
// since cachetest_helpers_test.go (this package's shared fixture file)
// carries no response-kind builder and is outside this wave's allowlist).
func threadResp(id, parent, from, to, thread string, created time.Time) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "response", "title": "response",
		"space": "fixture-space", "from": from, "to": []string{to}, "parent": parent,
		"result": "answered", "thread": thread,
		"actor":   map[string]any{"kind": "agent", "name": from + "-bot"},
		"created": created.UTC().Format(time.RFC3339), "priority": "p2", "blocking": false, "classification": "internal",
	}
}

func TestThreadCommand_JSONOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	item1 := cliWR("XW-axon-20260701-th1", "th1", "axon", []string{"seomatrix"}, "p2", false)
	item1["thread"] = "thread:axon-thread1"
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-th1.md", item1, "body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000020", cliEvt("XW-axon-20260701-th1", "submit", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewThreadCommand(store)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--json", "thread:axon-thread1"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var result cache.ThreadResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if result.Thread != "thread:axon-thread1" {
		t.Fatalf("thread = %q, want thread:axon-thread1", result.Thread)
	}
	if len(result.Transcript) != 2 {
		t.Fatalf("transcript = %+v, want 2 entries (1 artifact + 1 submit event)", result.Transcript)
	}

	// TEETH (advisor: "the flags/unresolved test must assert on raw
	// JSON"): decoding into ThreadResult cannot distinguish `null` from
	// `[]` (both unmarshal into a nil/empty slice indistinguishably for
	// this assertion's purposes) — assert the RAW bytes are exactly `[]`,
	// the contract spec 46 §T3 actually promises (non-nil empty arrays,
	// never omitted/null).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatalf("json.Unmarshal (raw): %v", err)
	}
	if got := strings.TrimSpace(string(raw["flags"])); got != "[]" {
		t.Fatalf("raw flags = %q, want exactly []", got)
	}
	if got := strings.TrimSpace(string(raw["unresolved"])); got != "[]" {
		t.Fatalf("raw unresolved = %q, want exactly []", got)
	}
}

// TestThreadCommand_FlagAfterPositional is Wave K's live-run 6 defect
// applied to `a2a thread`: `thread <thread-id> --json` used to refuse with
// a usage error (Go's flag package stops parsing at the first non-flag
// token).
//
// TEETH: reverting ThreadCommand.Run's parseArgsAnyOrder call
// (cmd_thread.go) back to a bare `fs.Parse(args)` reds this.
func TestThreadCommand_FlagAfterPositional(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	item1 := cliWR("XW-axon-20260701-th2", "th2", "axon", []string{"seomatrix"}, "p2", false)
	item1["thread"] = "thread:axon-thread2"
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-th2.md", item1, "body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000021", cliEvt("XW-axon-20260701-th2", "submit", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewThreadCommand(store)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"thread:axon-thread2", "--json"}, io)
	if code != 0 {
		t.Fatalf("thread <id> --json: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result cache.ThreadResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if result.Thread != "thread:axon-thread2" {
		t.Fatalf("thread = %q, want thread:axon-thread2", result.Thread)
	}
}

// TestThreadCommand_SpaceFlagAcceptedUnconditionally is the brief's own
// "--space accepted unconditionally (harmless when unambiguous)" clause:
// passing --space on a thread that resolves in exactly one connected
// space must not itself cause a refusal.
func TestThreadCommand_SpaceFlagAcceptedUnconditionally(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	item1 := cliWR("XW-axon-20260701-th3", "th3", "axon", []string{"seomatrix"}, "p2", false)
	item1["thread"] = "thread:axon-thread3"
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-th3.md", item1, "body")

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewThreadCommand(store)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"thread:axon-thread3", "--space", "sp1", "--json"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result cache.ThreadResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if result.Space != "sp1" {
		t.Fatalf("space = %q, want sp1", result.Space)
	}
}

func TestThreadCommand_UsageError(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, time.Now, 0)
	cmd := cli.NewThreadCommand(store)
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "usage: a2a thread") {
		t.Fatalf("stderr = %q, want a usage line", errOut.String())
	}
}

// TestThreadCommand_UnknownRefExitsOne is D6's own guard applied to the
// CLI surface: an unknown ref is exit 1, never a silent 0/empty success —
// covers both cache.ErrThreadNotFound (a `thread:...`-shaped ref matching
// nothing) and a wrapped cache.ErrNotFound (a bare id matching no
// artifact anywhere).
func TestThreadCommand_UnknownRefExitsOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon")
	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, time.Now, 0)
	cmd := cli.NewThreadCommand(store)

	for _, ref := range []string{"thread:no-such-thread", "XW-axon-no-such-artifact"} {
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{ref}, io)
		if code != 1 {
			t.Fatalf("ref %q: code = %d, want 1 (not 0, not silently empty)", ref, code)
		}
		if errOut.Len() == 0 {
			t.Fatalf("ref %q: expected an actionable error on stderr", ref)
		}
	}
}

// TestThreadCommand_AmbiguityRecoveryPerSpace is spec 46 §T4: a thread
// present in two connected spaces refuses (exit 1) with a RECOVERY
// block — one rerun command per space, built from the RESOLVED thread id
// (never the caller's own ref, which may be a member-artifact id valid in
// only ONE of the ambiguous spaces). The ref used here IS such an id
// (XW-axon-sp1 exists only in sp1) — this is the exact trap: a rerun line
// built from the ref instead of the resolved thread id would fail for
// every space but the one the id came from.
func TestThreadCommand_AmbiguityRecoveryPerSpace(t *testing.T) {
	t.Parallel()
	threadID := "thread:axon-amb1"
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	dir1 := t.TempDir()
	manifest1 := cliWriteManifest(t, dir1, "axon")
	item1 := cliWR("XW-axon-sp1", "q1", "axon", []string{}, "p2", false)
	item1["thread"] = threadID
	cliWriteArtifact(t, dir1, "axon/exchanges/XW-axon-sp1.md", item1, "body")
	cliWriteEvent(t, dir1, "axon", "01HFX00000000000000000040", cliEvt("XW-axon-sp1", "submit", "axon", base))

	dir2 := t.TempDir()
	manifest2 := cliWriteManifest(t, dir2, "axon")
	item2 := cliWR("XW-axon-sp2", "q2", "axon", []string{}, "p2", false)
	item2["thread"] = threadID
	cliWriteArtifact(t, dir2, "axon/exchanges/XW-axon-sp2.md", item2, "body")
	cliWriteEvent(t, dir2, "axon", "01HFX00000000000000000041", cliEvt("XW-axon-sp2", "submit", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{
		{SpaceID: "sp1", Dir: dir1, Manifest: manifest1},
		{SpaceID: "sp2", Dir: dir2, Manifest: manifest2},
	}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewThreadCommand(store)

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"XW-axon-sp1"}, io)
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%s", code, errOut.String())
	}
	stderr := errOut.String()

	// Extract every "a2a thread ..." rerun line the recovery block
	// printed and run each one for real — the acceptance criterion in
	// spirit ("a weak agent must be able to copy one line and proceed").
	var rerunLines []string
	for _, line := range strings.Split(stderr, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "a2a thread ") {
			rerunLines = append(rerunLines, trimmed)
		}
	}
	if len(rerunLines) != 2 {
		t.Fatalf("stderr = %q, want exactly 2 rerun lines (one per space), got %d", stderr, len(rerunLines))
	}
	for _, line := range rerunLines {
		fields := strings.Fields(line)
		// fields: a2a thread <thread-id> --space <space-id>
		if len(fields) != 5 || fields[3] != "--space" {
			t.Fatalf("rerun line %q does not have the expected shape", line)
		}
		if fields[2] != threadID {
			t.Fatalf("rerun line %q uses %q as the thread arg, want the RESOLVED thread id %q (not the caller's own artifact-id ref)", line, fields[2], threadID)
		}
		reIO, reOut, reErr := newIO()
		reCode := cmd.Run(context.Background(), fields[2:], reIO)
		if reCode != 0 {
			t.Fatalf("rerun %q: code = %d, want 0; stdout=%s stderr=%s", line, reCode, reOut.String(), reErr.String())
		}
	}

	// The negative half of the same trap: the ORIGINAL ref (a member
	// artifact id, valid only in sp1) combined with the OTHER space's
	// --space must NOT resolve — ThreadView's resolveLoop honors --space
	// during resolution too (threadview.go), so "XW-axon-sp1" is simply
	// not found in sp2's own index. This is exactly why
	// threadPrintAmbiguity builds its rerun lines from the resolved
	// thread id, never the caller's ref.
	badIO, _, badErr := newIO()
	badCode := cmd.Run(context.Background(), []string{"XW-axon-sp1", "--space", "sp2"}, badIO)
	if badCode != 1 {
		t.Fatalf("XW-axon-sp1 --space sp2: code = %d, want 1 (ref only resolves in its own space); stderr=%s", badCode, badErr.String())
	}
}

// TestThreadCommand_DeclaredOrderNoticeInText is §T3's "degradation is
// designed, not silent": this package's fixture helpers write a bare
// tree with no git history (cachetest_helpers_test.go's own doc comment),
// so every CLI-level thread here renders Order == declared — the text
// output must say so.
func TestThreadCommand_DeclaredOrderNoticeInText(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	item1 := cliWR("XW-axon-20260701-th4", "th4", "axon", []string{"seomatrix"}, "p2", false)
	item1["thread"] = "thread:axon-thread4"
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-th4.md", item1, "body")

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewThreadCommand(store)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"thread:axon-thread4"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "DECLARED") {
		t.Fatalf("stdout = %q, want a declared-order degradation notice", out.String())
	}
}

// TestThreadCommand_OpenBlockAndUnresolvedLine exercises the text
// renderer's OPEN block and the trailing "!! N unresolved / N flags"
// line: a response whose `parent` does not resolve within this thread's
// own rendered member set (REF-009-adjacent fork — §T3 "rendered, never
// dropped") is both an open item (draft, no events) AND an unresolved
// fact.
func TestThreadCommand_OpenBlockAndUnresolvedLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	resp := threadResp("XS-seomatrix-orphan", "XW-axon-missing-parent", "seomatrix", "axon", "thread:axon-thread5", base)
	cliWriteArtifact(t, dir, "seomatrix/exchanges/XS-seomatrix-orphan.md", resp, "resp body")

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewThreadCommand(store)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"thread:axon-thread5"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	stdout := out.String()
	if !strings.Contains(stdout, "OPEN") {
		t.Fatalf("stdout = %q, want an OPEN block", stdout)
	}
	if !strings.Contains(stdout, "XS-seomatrix-orphan") {
		t.Fatalf("stdout = %q, want the open response listed", stdout)
	}
	if !strings.Contains(stdout, "!! 1 unresolved / 0 flags") {
		t.Fatalf("stdout = %q, want a trailing \"!! 1 unresolved / 0 flags\" line", stdout)
	}
}
