package space

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// batchFixture is a repository with a small blob, a large blob, and a tree, and
// the tree entries that name them.
type batchFixture struct {
	repo  string
	small contractGitTreeEntry
	large contractGitTreeEntry
	tree  contractGitTreeEntry
}

func newBatchFixture(t *testing.T) batchFixture {
	t.Helper()
	repo := newContractHistoryRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "dir"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"small.txt":   "small body\n",
		"large.txt":   strings.Repeat("0123456789", 100),
		"dir/leaf.md": "leaf\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	contractTestGit(t, repo, "add", ".")
	contractTestGit(t, repo, "commit", "-q", "-m", "fixture")
	entry := func(name, kind, mode string) contractGitTreeEntry {
		oid := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD:"+name))
		return contractGitTreeEntry{Path: name, Kind: kind, Mode: mode, OID: oid}
	}
	return batchFixture{
		repo:  repo,
		small: entry("small.txt", "blob", "100644"),
		large: entry("large.txt", "blob", "100644"),
		tree:  entry("dir", "tree", "040000"),
	}
}

func TestContractGitBatchReaderReadsBlobsFromOneProcess(t *testing.T) {
	t.Parallel()
	fx := newBatchFixture(t)
	ctx, spawns := withContractGitSpawnCounter(t.Context())
	reader := newContractGitBatchReader(ctx, fx.repo)
	if got := spawns.Load(); got != 0 {
		t.Fatalf("spawns before the first read = %d, want 0 (lazy start)", got)
	}
	for round := 0; round < 3; round++ {
		small, err := reader.readBlob(ctx, fx.small, 1024)
		if err != nil || string(small) != "small body\n" {
			t.Fatalf("round %d small = %q, %v; want %q", round, small, err, "small body\n")
		}
		large, err := reader.readBlob(ctx, fx.large, 1024)
		if err != nil || len(large) != 1000 {
			t.Fatalf("round %d large = %d bytes, %v; want 1000 bytes", round, len(large), err)
		}
	}
	if got := spawns.Load(); got != 1 {
		t.Fatalf("spawns for six reads = %d, want 1", got)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
}

func TestContractGitBatchReaderCloseWithoutReadSpawnsNothing(t *testing.T) {
	t.Parallel()
	ctx, spawns := withContractGitSpawnCounter(t.Context())
	reader := newContractGitBatchReader(ctx, t.TempDir())
	if err := reader.Close(); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
	if got := spawns.Load(); got != 0 {
		t.Fatalf("spawns = %d, want 0", got)
	}
}

// An oversize blob is refused from its header, and the refusal must leave the
// stream aligned: the very next read on the same process has to work.
func TestContractGitBatchReaderOversizeIsRefusedAndStreamStaysAligned(t *testing.T) {
	t.Parallel()
	fx := newBatchFixture(t)
	ctx, spawns := withContractGitSpawnCounter(t.Context())
	reader := newContractGitBatchReader(ctx, fx.repo)
	defer func() { _ = reader.Close() }() // reason: the test judges the reads; a close failure after them adds no verdict

	if _, err := reader.readBlob(ctx, fx.large, 10); !errors.Is(err, ErrContractBoundedResult) {
		t.Fatalf("oversize read = %v, want ErrContractBoundedResult", err)
	}
	small, err := reader.readBlob(ctx, fx.small, 1024)
	if err != nil || string(small) != "small body\n" {
		t.Fatalf("read after oversize = %q, %v; want %q", small, err, "small body\n")
	}
	if got := spawns.Load(); got != 1 {
		t.Fatalf("spawns = %d, want 1: the refusal must drain, not restart", got)
	}
}

// Past discardLimit a refused body is abandoned with its process rather than
// drained; the next read must transparently start a fresh one.
func TestContractGitBatchReaderAbandonsHugeRefusedBodyAndRecovers(t *testing.T) {
	t.Parallel()
	fx := newBatchFixture(t)
	ctx, spawns := withContractGitSpawnCounter(t.Context())
	reader := newContractGitBatchReader(ctx, fx.repo)
	reader.discardLimit = 100
	defer func() { _ = reader.Close() }() // reason: the test judges the reads; a close failure after them adds no verdict

	if _, err := reader.readBlob(ctx, fx.large, 10); !errors.Is(err, ErrContractBoundedResult) {
		t.Fatalf("oversize read = %v, want ErrContractBoundedResult", err)
	}
	small, err := reader.readBlob(ctx, fx.small, 1024)
	if err != nil || string(small) != "small body\n" {
		t.Fatalf("read after abandon = %q, %v; want %q", small, err, "small body\n")
	}
	if got := spawns.Load(); got != 2 {
		t.Fatalf("spawns = %d, want 2 (restart after abandoning the huge body)", got)
	}
}

func TestContractGitBatchReaderMissingObjectIsUnavailableAndNotSticky(t *testing.T) {
	t.Parallel()
	fx := newBatchFixture(t)
	reader := newContractGitBatchReader(t.Context(), fx.repo)
	defer func() { _ = reader.Close() }() // reason: the test judges the reads; a close failure after them adds no verdict

	missing := contractGitTreeEntry{Path: "gone.txt", Kind: "blob", Mode: "100644", OID: strings.Repeat("a", 40)}
	if _, err := reader.readBlob(t.Context(), missing, 1024); !errors.Is(err, ErrContractObjectUnavailable) {
		t.Fatalf("missing object = %v, want ErrContractObjectUnavailable", err)
	}
	small, err := reader.readBlob(t.Context(), fx.small, 1024)
	if err != nil || string(small) != "small body\n" {
		t.Fatalf("read after a missing object = %q, %v; want %q", small, err, "small body\n")
	}
}

func TestContractGitBatchReaderNonBlobObjectIsUnavailableAndNextReadWorks(t *testing.T) {
	t.Parallel()
	fx := newBatchFixture(t)
	reader := newContractGitBatchReader(t.Context(), fx.repo)
	defer func() { _ = reader.Close() }() // reason: the test judges the reads; a close failure after them adds no verdict

	// A tree id presented as a blob entry: the header type is what refuses it.
	lying := contractGitTreeEntry{Path: "dir", Kind: "blob", Mode: "100644", OID: fx.tree.OID}
	if _, err := reader.readBlob(t.Context(), lying, 1024); !errors.Is(err, ErrContractObjectUnavailable) {
		t.Fatalf("tree read as blob = %v, want ErrContractObjectUnavailable", err)
	}
	small, err := reader.readBlob(t.Context(), fx.small, 1024)
	if err != nil || string(small) != "small body\n" {
		t.Fatalf("read after a non-blob = %q, %v; want %q", small, err, "small body\n")
	}
}

func TestContractGitBatchReaderRefusesUnsafeEntriesAndBadIDsWithoutSpawning(t *testing.T) {
	t.Parallel()
	fx := newBatchFixture(t)
	ctx, spawns := withContractGitSpawnCounter(t.Context())
	reader := newContractGitBatchReader(ctx, fx.repo)
	defer func() { _ = reader.Close() }() // reason: the test judges the reads; a close failure after them adds no verdict

	symlink := contractGitTreeEntry{Path: "link", Kind: "blob", Mode: "120000", OID: fx.small.OID}
	if _, err := reader.readBlob(ctx, symlink, 1024); !errors.Is(err, ErrContractUnsafeEntry) {
		t.Fatalf("symlink = %v, want ErrContractUnsafeEntry", err)
	}
	// An id that is not a full object id must never reach the process: it could
	// be a ref or a "<commit>:<path>" expression.
	for _, oid := range []string{"HEAD", "HEAD:small.txt", "", fx.small.OID + "\nHEAD"} {
		bad := contractGitTreeEntry{Path: "x", Kind: "blob", Mode: "100644", OID: oid}
		if _, err := reader.readBlob(ctx, bad, 1024); !errors.Is(err, ErrContractObjectUnavailable) {
			t.Fatalf("oid %q = %v, want ErrContractObjectUnavailable", oid, err)
		}
	}
	if got := spawns.Load(); got != 0 {
		t.Fatalf("spawns = %d, want 0: refusals happen before the process is needed", got)
	}
}

func TestContractGitBatchReaderCancelKillsTheProcess(t *testing.T) {
	t.Parallel()
	fx := newBatchFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader := newContractGitBatchReader(ctx, fx.repo)

	if _, err := reader.readBlob(ctx, fx.small, 1024); err != nil {
		t.Fatalf("first read: %v", err)
	}
	cancel()
	// With no request outstanding an idle cat-file never closes its stdout, so
	// this read returns io.EOF only if cancellation killed the process.
	if _, err := reader.proc.stdout.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("idle stdout after cancel = %v, want io.EOF (process killed)", err)
	}
	if _, err := reader.readBlob(ctx, fx.small, 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancel = %v, want context.Canceled", err)
	}
	// Close reports the cancellation rather than a git failure.
	if err := reader.Close(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close after cancel = %v, want context.Canceled", err)
	}
}

func TestContractGitBatchReaderCancelledBeforeStartSpawnsNothing(t *testing.T) {
	t.Parallel()
	fx := newBatchFixture(t)
	base, cancel := context.WithCancel(t.Context())
	ctx, spawns := withContractGitSpawnCounter(base)
	cancel()
	reader := newContractGitBatchReader(ctx, fx.repo)
	if _, err := reader.readBlob(ctx, fx.small, 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("read on a cancelled context = %v, want context.Canceled", err)
	}
	if got := spawns.Load(); got != 0 {
		t.Fatalf("spawns = %d, want 0", got)
	}
}

func TestContractGitBatchReaderReadPathMatchesContractGitReadPath(t *testing.T) {
	t.Parallel()
	fx := newBatchFixture(t)
	reader := newContractGitBatchReader(t.Context(), fx.repo)
	defer func() { _ = reader.Close() }() // reason: the test judges the reads; a close failure after them adds no verdict

	want, err := contractGitReadPath(t.Context(), fx.repo, "HEAD", "dir/leaf.md", 1024)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.readPath(t.Context(), "HEAD", "dir/leaf.md", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want.Path || got.Mode != want.Mode || string(got.Raw) != string(want.Raw) {
		t.Fatalf("readPath = %+v, want %+v", got, want)
	}
	if _, err := reader.readPath(t.Context(), "HEAD", "dir/absent.md", 1024); !errors.Is(err, ErrContractHistoryInvalid) {
		t.Fatalf("readPath of an absent file = %v, want ErrContractHistoryInvalid", err)
	}
}
