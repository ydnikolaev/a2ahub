package space

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// maxContractBatchDiscardBytes is how large a refused body may be before the
// reader stops draining it and restarts the process instead. Draining keeps the
// stream aligned for the next read; past this size, reading a hostile object
// through the pipe just to throw it away costs more than a new process.
const maxContractBatchDiscardBytes = 8 << 20

// contractBatchBufferBytes sizes the read buffer, and with it bounds one
// "<oid> <type> <size>" header line. The real line is under 100 bytes; one that
// fills the buffer is a stream that lost its framing, not a header.
const contractBatchBufferBytes = 4096

// contractGitBatchReader answers many object-addressed blob reads from ONE
// long-lived `git cat-file --batch` process. A history walk reads hundreds of
// small objects; one process each (ls-tree, then cat-file -s, then cat-file) is
// what made a publication-history read cost tens of thousands of spawns.
//
// It uses the same hardened argv prefix and scrubbed environment as
// contractGitBytes. The process is started lazily on the first read, so a walk
// that reads nothing spawns nothing, and it is bound to the context given at
// construction: cancelling it kills the process. There are no goroutines — git
// flushes after every object unless asked to buffer, so a request is written
// and its reply read synchronously. A reader belongs to one goroutine.
//
// Only fully resolved object ids are ever written to the process. A
// `<commit>:<path>` expression would also resolve, but it would say nothing
// about the entry's mode, and the mode (regular file versus symlink or
// submodule) is the safety verdict; that stays with ls-tree and `log --raw`.
type contractGitBatchReader struct {
	ctx          context.Context
	repoDir      string
	discardLimit int64

	proc *contractGitBatchProcess
	// err is sticky: once the stream has lost its framing or the process has
	// died, every later read reports the same cause rather than guessing.
	err error
}

type contractGitBatchProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *boundedCommandBuffer
}

func newContractGitBatchReader(ctx context.Context, repoDir string) *contractGitBatchReader {
	return &contractGitBatchReader{ctx: ctx, repoDir: repoDir, discardLimit: maxContractBatchDiscardBytes}
}

func (r *contractGitBatchReader) start() error {
	if r.err != nil {
		return r.err
	}
	if r.proc != nil {
		return nil
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if r.repoDir == "" {
		return fmt.Errorf("space: invalid bounded git contract read")
	}
	cmd := contractGitCommand(r.ctx, r.repoDir, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("%w: cannot open git cat-file --batch: %w", ErrContractObjectUnavailable, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%w: cannot open git cat-file --batch: %w", ErrContractObjectUnavailable, err)
	}
	stderr := &boundedCommandBuffer{remaining: maxTreeCommitMetadataBytes}
	cmd.Stderr = stderr
	countContractGitSpawn(r.ctx)
	if err := cmd.Start(); err != nil {
		if ctxErr := r.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: cannot start git cat-file --batch: %w", ErrContractObjectUnavailable, err)
	}
	r.proc = &contractGitBatchProcess{
		cmd: cmd, stdin: stdin, stderr: stderr,
		stdout: bufio.NewReaderSize(stdout, contractBatchBufferBytes),
	}
	return nil
}

// fail records why the stream can no longer be trusted and stops the process.
// A cancelled context is reported as itself, never as a git failure.
func (r *contractGitBatchReader) fail(cause error) error {
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		r.err = ctxErr
	} else {
		stderr := ""
		if r.proc != nil {
			stderr = strings.TrimSpace(r.proc.stderr.String())
		}
		r.err = fmt.Errorf("%w: git cat-file --batch failed: %w: %s", ErrContractObjectUnavailable, cause, stderr)
	}
	r.stop()
	return r.err
}

// stop ends the current process without judging it. Used when the stream is
// already condemned or deliberately abandoned (see discardLimit).
func (r *contractGitBatchReader) stop() {
	proc := r.proc
	if proc == nil {
		return
	}
	r.proc = nil
	_ = proc.stdin.Close() // reason: the process is being killed next; a close failure cannot add information
	if proc.cmd.Process != nil {
		_ = proc.cmd.Process.Kill() // reason: it may already have exited, which is the outcome wanted
	}
	_ = proc.cmd.Wait() // reason: the exit status of a process we just killed carries no verdict
}

// Close ends the process the orderly way — closing stdin tells cat-file there
// are no more requests — and reports a non-zero exit. It is safe on a reader
// that never started, and safe to call twice. A cancelled context is reported
// as itself.
func (r *contractGitBatchReader) Close() error {
	if r == nil || r.proc == nil {
		return nil
	}
	proc := r.proc
	r.proc = nil
	_ = proc.stdin.Close() // reason: a failure here surfaces through Wait, which is checked below
	waitErr := proc.cmd.Wait()
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if waitErr != nil && r.err == nil {
		return fmt.Errorf("%w: git cat-file --batch exited abnormally: %w: %s",
			ErrContractObjectUnavailable, waitErr, strings.TrimSpace(proc.stderr.String()))
	}
	return nil
}

// closeContractGitBatchReader is the deferred form of Close: a close failure
// is surfaced as the operation's error unless the operation already has one.
func closeContractGitBatchReader(reader *contractGitBatchReader, err *error) {
	if closeErr := reader.Close(); closeErr != nil && *err == nil {
		*err = closeErr
	}
}

// readBlob returns the bytes of entry's blob, refusing anything over maximum.
// It reaches the same verdicts contractGitReadBlob does, for the same inputs:
// a non-regular entry is ErrContractUnsafeEntry, an oversize blob is
// ErrContractBoundedResult, and a missing or non-blob object is
// ErrContractObjectUnavailable. The size is taken from the batch header and
// checked BEFORE the body is read, so a hostile object is never buffered.
func (r *contractGitBatchReader) readBlob(ctx context.Context, entry contractGitTreeEntry, maximum int) ([]byte, error) {
	if entry.Kind != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") {
		return nil, fmt.Errorf("%w: %q has git mode/type %s/%s", ErrContractUnsafeEntry, entry.Path, entry.Mode, entry.Kind)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !contractGitObjectID(entry.OID) {
		return nil, fmt.Errorf("%w: blob for %q has no valid object id", ErrContractObjectUnavailable, entry.Path)
	}
	if err := r.start(); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(r.proc.stdin, entry.OID+"\n"); err != nil {
		return nil, r.fail(err)
	}
	line, err := r.proc.stdout.ReadSlice('\n')
	if err != nil {
		return nil, r.fail(err)
	}
	fields := strings.Fields(string(line))
	if len(fields) == 2 && fields[1] == "missing" && strings.EqualFold(fields[0], entry.OID) {
		return nil, fmt.Errorf("%w: blob for %q is unavailable; run a2a sync: object %s is missing", ErrContractObjectUnavailable, entry.Path, entry.OID)
	}
	if len(fields) != 3 || !strings.EqualFold(fields[0], entry.OID) {
		return nil, r.fail(fmt.Errorf("unexpected batch header %q for %s", strings.TrimSpace(string(line)), entry.OID))
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return nil, r.fail(fmt.Errorf("invalid blob size in batch header %q", strings.TrimSpace(string(line))))
	}
	if fields[1] != "blob" {
		if err := r.skipBody(size); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: object for %q is a %s, not a blob", ErrContractObjectUnavailable, entry.Path, fields[1])
	}
	if size > int64(maximum) {
		if err := r.skipBody(size); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %q exceeds %d bytes", ErrContractBoundedResult, entry.Path, maximum)
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(r.proc.stdout, raw); err != nil {
		return nil, r.fail(err)
	}
	if terminator, err := r.proc.stdout.ReadByte(); err != nil || terminator != '\n' {
		if err == nil {
			err = fmt.Errorf("blob body for %s is not newline-terminated", entry.OID)
		}
		return nil, r.fail(err)
	}
	return raw, nil
}

// skipBody consumes a body the caller refused: the size bytes plus the newline
// that terminates every batch reply, so the NEXT header starts where it should.
// A body too large to be worth draining abandons the process instead; the next
// read starts a fresh one.
func (r *contractGitBatchReader) skipBody(size int64) error {
	if size >= r.discardLimit {
		r.stop()
		return nil
	}
	if _, err := io.CopyN(io.Discard, r.proc.stdout, size+1); err != nil {
		return r.fail(err)
	}
	return nil
}

// readPath is contractGitReadPath over the batch process: the tree entry still
// comes from ls-tree (it carries the mode), the bytes come from the reader.
func (r *contractGitBatchReader) readPath(ctx context.Context, commit, name string, maximum int) (ContractSnapshotFile, error) {
	return contractGitReadPathWith(ctx, r.repoDir, commit, name, func(entry contractGitTreeEntry) ([]byte, error) {
		return r.readBlob(ctx, entry, maximum)
	})
}
