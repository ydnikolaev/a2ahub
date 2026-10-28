package feedback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	gopath "path"
	"path/filepath"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// The local feedback inbox (local-space-2026-10 spec 08, D-039).
//
// With no GitHub credential, or when the caller asks for it, a validated
// report is filed to `<data-root>/feedback/inbox/<id>.yaml` instead of the hub:
// a file in the hub's own format, byte for byte what delivery would send. No
// host, no funnel and no schema are involved; the route is a validated,
// atomic file write, and delivery to the hub stays a later Submit.

// errNoDataRoot is the refusal for an empty SubmitConfig.InboxDir.
var errNoDataRoot = errors.New("feedback: inbox: no data root: HOME is not set, so <data-root>/feedback/inbox cannot be placed; set HOME and re-run")

const (
	inboxNoteFiled   = "filed to the local inbox; delivery to the hub is a later submit"
	inboxNoteExists  = "already in the local inbox; delivery to the hub is a later submit"
	inboxTempPattern = ".incoming-*"
)

// ValidateInboxPath performs every pre-write check FileToInbox applies, without
// writing anything: the report checks, the final-byte validator the hub's
// funnel runs, and the data-root check. The CLI uses it to validate a whole
// batch before the first filing. It holds no credential guard: the inbox exists
// for the machine that has no credential.
func (s *Submitter) ValidateInboxPath(path string) error {
	_, _, err := s.inboxRecord(path)
	return err
}

// FileToInbox files the validated report at path to the local inbox and records
// it in the ledger. It never overwrites: the same bytes under the same id are
// reported as already filed, and different bytes under a filed id are refused
// with ErrorCodeInboxConflict. The inbox holds exactly the bytes a later Submit
// would send, because both read, normalize and validate through readValidRecord.
func (s *Submitter) FileToInbox(ctx context.Context, path string) (SubmitResult, error) {
	const op = "FileToInbox"
	if err := ctx.Err(); err != nil {
		return SubmitResult{}, fmt.Errorf("feedback: %s: %w", op, err)
	}

	id, record, err := s.inboxRecord(path)
	if err != nil {
		result := SubmitResult{InputPath: path}
		if errors.Is(err, errNoDataRoot) {
			result.ErrorCode = ErrorCodeInboxWrite
		}
		return result, err
	}
	var probe submitProbe
	if err := yaml.Unmarshal(record, &probe); err != nil {
		return SubmitResult{}, fmt.Errorf("feedback: %s: %w", op, err)
	}

	inboxDir, err := filepath.Abs(s.cfg.InboxDir)
	if err != nil {
		return inboxWriteFailure(path, id, fmt.Errorf("feedback: %s: %w", op, err))
	}
	final := filepath.Join(inboxDir, id+".yaml")
	result := SubmitResult{
		ID: id, InputPath: path, Inbox: final,
		Stage: space.WriteStageNone, RemainingAction: space.RemainingActionNone,
		Note: inboxNoteFiled,
	}

	existing, err := s.writeIfAbsent(inboxDir, final, record)
	if err != nil {
		return inboxWriteFailure(path, id, fmt.Errorf("feedback: %s: %w", op, err))
	}
	if existing != nil {
		if !bytes.Equal(existing, record) {
			result.Note = ""
			result.ErrorCode = ErrorCodeInboxConflict
			result.Error = fmt.Sprintf("%s is already in the local inbox as %s with different content", id, final)
			return result, fmt.Errorf(
				"feedback: %s: %s is already in the local inbox as %s with different content than %s; a changed body under a filed id is a different report: file it with a new `a2a feedback new`",
				op, id, final, path)
		}
		result.InboxExisting = true
		result.Note = inboxNoteExists
	}

	// The ledger comes after the link: a failed filing leaves no row, and a
	// crash between the two is healed by the next run, which finds the report
	// already filed and records the row.
	if ledgerErr := s.appendLedger(s.ledgerPath, LedgerItem{
		ID: id, Kind: probe.Kind, Title: probe.Title, Inbox: final,
		Filed: s.now().UTC().Format(time.RFC3339),
	}); ledgerErr != nil {
		result.ErrorCode = ErrorCodeLocalLedger
		result.Error = "local ledger was not recorded"
		return result, &LedgerRecordError{Err: ledgerErr}
	}
	result.LedgerRecorded = true
	return result, nil
}

// InboxConflictError is the hub route's refusal of a draft whose id is already
// filed to the local inbox, not yet delivered, with a different record than the
// draft's (spec 08 §11 "Delivery sends the filed bytes", US-6). It is the same
// verdict FileToInbox reaches on the inbox route, so one draft gets one answer
// whichever route a submit takes. The CLI reports it as ErrorCodeInboxConflict.
type InboxConflictError struct {
	// ID is the report id the draft and the filed copy share.
	ID string
	// Inbox is the filed copy's absolute path, as the ledger recorded it.
	Inbox string
	// Path is the draft that differs from it.
	Path string
}

func (e *InboxConflictError) Error() string {
	return fmt.Sprintf(
		"%s is already in the local inbox as %s with different content than %s; a changed body under a filed id is a different report: file it with a new `a2a feedback new`",
		e.ID, e.Inbox, e.Path)
}

// checkFiledCopy keeps one id to one body on the hub route. When the ledger
// says record's id was filed to the local inbox and has no PR yet, record must
// be byte-for-byte the filed copy, otherwise this is an *InboxConflictError.
// Byte equality makes "deliver the draft" and "deliver the filed copy" the same
// bytes, so delivery always sends what was filed, and `--all` (which hands this
// the filed copy itself) passes trivially. Both sides are normalized records, so
// a draft that differs only by its skeleton comments is not a conflict.
//
// A row with a PR takes the idempotent re-run path unchanged, and an id with no
// row is an ordinary first submit. A row whose inbox copy is gone is NOT a
// conflict: `submit --all` refuses that row and tells the agent to submit the
// draft explicitly, so this explicit route has to accept the draft.
func (s *Submitter) checkFiledCopy(op, path string, record []byte) error {
	var probe submitProbe
	if err := yaml.Unmarshal(record, &probe); err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	row, err := FindLedgerItem(s.ledgerPath, probe.ID)
	if err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	if row == nil || row.PRURL != "" || row.Inbox == "" {
		return nil
	}
	filed, err := readBounded(filepath.Dir(row.Inbox), filepath.Base(row.Inbox), maxFeedbackBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("feedback: %s: read the filed copy of %s: %w", op, probe.ID, err)
	}
	if !bytes.Equal(filed, record) {
		return &InboxConflictError{ID: probe.ID, Inbox: row.Inbox, Path: path}
	}
	return nil
}

func inboxWriteFailure(path, id string, err error) (SubmitResult, error) {
	return SubmitResult{ID: id, InputPath: path, ErrorCode: ErrorCodeInboxWrite, Error: err.Error()}, err
}

// inboxRecord is the shared front half of ValidateInboxPath and FileToInbox:
// the normalized, validated record, its id, the hub funnel's final-byte check
// on the path the hub would store it at, and the data-root check.
func (s *Submitter) inboxRecord(path string) (id string, record []byte, err error) {
	const op = "FileToInbox"
	record, err = s.readValidRecord(op, path)
	if err != nil {
		return "", nil, err
	}
	var probe submitProbe
	if err := yaml.Unmarshal(record, &probe); err != nil {
		return "", nil, fmt.Errorf("feedback: %s: %w", op, err)
	}
	// The same check the hub's funnel and intake run, on the same bytes and the
	// same path, so a file the inbox accepts is a file `a2a feedback validate
	// --ci` accepts. It also refuses an id that is not a plain file name before
	// that id is joined onto a directory below.
	hubPath := gopath.Join("feedback", "inbox", probe.ID+".yaml")
	if err := (FinalSubmitValidator{}).ValidateSubmit(context.Background(), []space.FileWrite{{Path: hubPath, Content: record}}); err != nil {
		return "", nil, err
	}
	if s.cfg.InboxDir == "" {
		return "", nil, errNoDataRoot
	}
	return probe.ID, record, nil
}

// writeIfAbsent files record at final atomically and without overwriting: the
// bytes go to a private temp file in the inbox directory, and os.Link then
// creates the final name only if it does not exist (os.Rename would replace
// silently). It returns the existing file's bytes when final was already
// present, nil when this call created it. The temp file is removed whatever the
// outcome, so a failed filing leaves nothing behind.
func (s *Submitter) writeIfAbsent(dir, final string, record []byte) (existing []byte, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, inboxTempPattern)
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // reason: best-effort cleanup of our own temp file; the outcome is already decided

	if _, err := tmp.Write(record); err != nil {
		_ = tmp.Close() // reason: the write error is the one worth reporting
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close() // reason: the sync error is the one worth reporting
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	linkErr := s.link(tmpPath, final)
	if linkErr == nil {
		return nil, nil
	}
	if !errors.Is(linkErr, fs.ErrExist) {
		return nil, linkErr
	}
	existing, err = readBounded(dir, filepath.Base(final), maxFeedbackBytes)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		existing = []byte{}
	}
	return existing, nil
}

// readBounded reads at most limit+1 bytes of the file called name directly
// inside dir (os.OpenInRoot confines the open to dir), so a file larger than
// limit is returned oversize, and therefore unequal to any valid record, rather
// than truncated into a lookalike or read whole.
func readBounded(dir, name string, limit int64) ([]byte, error) {
	f, err := os.OpenInRoot(dir, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // reason: read-only handle; nothing to flush
	return io.ReadAll(io.LimitReader(f, limit+1))
}
