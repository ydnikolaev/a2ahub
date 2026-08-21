package feedback

import (
	"context"
	"fmt"
	"net/url"
	"os"
	gopath "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// Funnel is feedback's own narrow consumer-side seam over
// *space.WriteFunnel (rails ISP/DI) — spec §11 A6/A5: feedback reuses the
// SAME write funnel every other a2a write goes through
// (space.WriteFunnel.Submit already hardcodes branch a2a/<System>/
// <ArtifactID>, and its section guard already cleanly accepts an
// arbitrary System="feedback" + a feedback/inbox/<id>.yaml file path with
// no edits to internal/space) — never a second, direct host.Host path.
// *space.WriteFunnel satisfies this structurally; tests inject
// space.NewWriteFunnel(host.NewFakeHost(), nil, "...") or a hand-written
// fake.
type Funnel interface {
	Submit(ctx context.Context, req space.SubmitRequest) (space.WriteResult, error)
}

// SubmitConfig carries the write funnel's per-target host-facing config a
// SubmitRequest needs beyond the report content itself — mirrors
// internal/cli's own SubmitHostConfig shape (this package's own copy:
// internal/cli is a downstream consumer of internal/feedback, never the
// reverse). cmd/a2a resolves RemoteURL/Repo from the canonical a2ahub
// repo, or the `--repo`/`A2A_FEEDBACK_REPO` override (§T1, §11 A8).
type SubmitConfig struct {
	RemoteURL         string
	Repo              host.Repo
	BaseBranch        string // REQUIRED — Submit refuses an empty value (see below)
	Credential        host.Credential
	CommitAuthorName  string
	CommitAuthorEmail string
}

// SubmitResult is what Submit returns to its CLI caller.
type SubmitResult struct {
	ID              string                `json:"id"`
	InputPath       string                `json:"input_path"`
	Branch          string                `json:"branch,omitempty"`
	PRNumber        int                   `json:"pr_number,omitempty"`
	PRURL           string                `json:"pr_url,omitempty"`
	Stage           space.WriteStage      `json:"stage"`
	State           space.WriteState      `json:"state,omitempty"`
	RemainingAction space.RemainingAction `json:"remaining_action"`
	MergeMethod     host.MergeMethod      `json:"merge_method,omitempty"`
	Note            string                `json:"note,omitempty"`
	LedgerRecorded  bool                  `json:"ledger_recorded"`
	ErrorCode       string                `json:"error_code,omitempty"`
	Error           string                `json:"error,omitempty"`

	// AlreadyOpen is retained for one compatibility cycle. New callers branch
	// on State and RemainingAction and never reconstruct them from this bit.
	AlreadyOpen bool `json:"-"`
}

const (
	// ErrorCodeValidation reports local feedback validation failure.
	ErrorCodeValidation = "validation"
	// ErrorCodeBatchRefused reports rejection of the submitted feedback batch.
	ErrorCodeBatchRefused = "batch-validation-refused"
	// ErrorCodeSubmit reports failure to submit a validated report.
	ErrorCodeSubmit = "submit"
	// ErrorCodeSharedWrite reports failure to write through the shared funnel.
	ErrorCodeSharedWrite = "shared-write"
	// ErrorCodeLocalLedger reports failure to update the local acknowledgement ledger.
	ErrorCodeLocalLedger = "local-ledger"
)

// LedgerRecordError means the shared write reached a confirmed PR, but the
// machine-local acknowledgement ledger could not be updated. The submission
// result remains authoritative and is returned alongside this error.
type LedgerRecordError struct{ Err error }

func (e *LedgerRecordError) Error() string { return "feedback: record local ledger: " + e.Err.Error() }
func (e *LedgerRecordError) Unwrap() error { return e.Err }

// Submitter drives `a2a feedback submit <file>` (§T1, §11 A6): validate
// first (refuse red), resolve/refresh a local mirror clone of cfg's
// target repo (§11 A8: cached under <projectRoot>/.a2a/cache/
// feedback-repo/<slug>/ via space.CloneOrFetch — no space.yaml
// dependency), push the deterministic branch a2a/feedback/<id> carrying
// the ONE file feedback/inbox/<id>.yaml through the injected Funnel,
// append the consumer-local ledger row, and stay idempotent (an
// already-open/merged PR short-circuits inside WriteFunnel.Submit's own
// step 0 — this package never re-implements that check).
type Submitter struct {
	funnel      Funnel
	ledgerPath  string
	projectRoot string
	slug        string
	cfg         SubmitConfig

	now          func() time.Time
	readFile     func(path string) ([]byte, error)
	normalize    func(raw []byte) ([]byte, error)
	appendLedger func(path string, item LedgerItem) error
	cloneOrFetch func(ctx context.Context, dir, repoURL string, credential host.Credential) error
	mirrorDir    func(projectRoot, slug string) string
}

// NewSubmitter constructs a Submitter. funnel must not be nil (rails
// anti-pattern #10). ledgerPath is `.a2a/feedback/ledger.yaml`'s path;
// projectRoot/slug together resolve the local mirror cache dir (§11 A8);
// cfg is the fixed feedback-repo push/PR target.
func NewSubmitter(funnel Funnel, ledgerPath, projectRoot, slug string, cfg SubmitConfig) *Submitter {
	return &Submitter{
		funnel: funnel, ledgerPath: ledgerPath, projectRoot: projectRoot, slug: slug, cfg: cfg,
		now:          time.Now,
		readFile:     os.ReadFile,
		normalize:    normalizeRecord,
		appendLedger: AppendLedger,
		cloneOrFetch: space.CloneOrFetch,
		mirrorDir:    defaultMirrorDir,
	}
}

// defaultMirrorDir is §11 A8's own cache-path helper (no space.yaml
// dependency; mirrors ResolveMirrorLocation/cacheDirOf's naming idiom
// without importing either — internal/space's own helpers are keyed on a
// connected-space Ref this consumer doesn't have).
func defaultMirrorDir(projectRoot, slug string) string {
	return filepath.Join(projectRoot, ".a2a", "cache", "feedback-repo", slug)
}

// SetCloneOrFetchForTest overrides the injected mirror-refresh seam
// (test-only DI, rails anti-pattern #10 convention).
func (s *Submitter) SetCloneOrFetchForTest(f func(ctx context.Context, dir, repoURL string, credential host.Credential) error) {
	s.cloneOrFetch = f
}

// SetMirrorDirForTest overrides the injected mirror-dir resolver.
func (s *Submitter) SetMirrorDirForTest(f func(projectRoot, slug string) string) {
	s.mirrorDir = f
}

// SetClockForTest overrides the injected clock (ledger Filed timestamp).
func (s *Submitter) SetClockForTest(now func() time.Time) { s.now = now }

// SetReadFileForTest overrides the injected file reader.
func (s *Submitter) SetReadFileForTest(f func(path string) ([]byte, error)) { s.readFile = f }

// SetNormalizeForTest overrides the submit-time comment strip. It exists so
// AC #3's fail-closed half can be driven directly: a normalization that
// refuses must mean NO bytes and NO write, and the only honest way to assert
// that is to inject the refusal.
func (s *Submitter) SetNormalizeForTest(f func(raw []byte) ([]byte, error)) { s.normalize = f }

// SetAppendLedgerForTest overrides only the local acknowledgement boundary.
func (s *Submitter) SetAppendLedgerForTest(f func(path string, item LedgerItem) error) {
	s.appendLedger = f
}

type submitProbe struct {
	ID    string `yaml:"id"`
	Kind  string `yaml:"kind"`
	Title string `yaml:"title"`
}

// Submit reads path, validates it (refusing red — I5/§T1), and pushes it
// through the write funnel: branch a2a/feedback/<id>, single file
// feedback/inbox/<id>.yaml, PR titled feedback(<kind>): <title>. On
// success it appends (or, on an idempotent re-run, no-ops) the ledger
// row.
func (s *Submitter) Submit(ctx context.Context, path string) (SubmitResult, error) {
	const op = "Submit"

	raw, err := s.readFile(path)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("feedback: %s: %w", op, err)
	}

	// THE LIFECYCLE BOUNDARY, and the one place it can be enforced. A draft
	// is a form and may carry the skeleton's instructions; a submitted record
	// is evidence and may not. Every submit route — `a2a feedback submit
	// <file>`, `--all`, and any future caller, including an MCP tool if one is
	// ever added — funnels through this function, so the guarantee cannot be
	// reached around. Placing it in internal/cli would forfeit exactly that.
	//
	// VALIDATE THE BYTES YOU SEND: everything below reads `record`, never
	// `raw`. Today the two carry the same values by construction; a validator
	// that judged a different document from the one that ships would be this
	// epic's own defect, one layer down.
	record, err := s.normalize(raw)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("feedback: %s: %w", op, err)
	}
	if err := s.validateRaw(record); err != nil {
		return SubmitResult{}, err
	}

	var probe submitProbe
	if err := yaml.Unmarshal(record, &probe); err != nil {
		return SubmitResult{}, fmt.Errorf("feedback: %s: %w", op, err)
	}

	dir := s.mirrorDir(s.projectRoot, s.slug)
	if err := s.cloneOrFetch(ctx, dir, s.cfg.RemoteURL, s.cfg.Credential); err != nil {
		return SubmitResult{}, fmt.Errorf("feedback: %s: %w", op, err)
	}

	// No default. This package does not get to GUESS where a report lands.
	//
	// It defaulted to "main" until agent-ops-2026-07 P15, which is the branch
	// a release force-pushes wholesale — so a caller that simply forgot to set
	// this filed the reporter's record onto the one ref that could destroy it,
	// silently and successfully. The wiring (cmd/a2a's feedbackBaseBranch) is
	// the single place that knows the hub branch; a second copy here would be
	// a second source of truth for exactly the fact this phase moved.
	baseBranch := s.cfg.BaseBranch
	if baseBranch == "" {
		return SubmitResult{}, fmt.Errorf("feedback: %s: SubmitConfig.BaseBranch is empty; the caller must name the hub branch", op)
	}

	title := fmt.Sprintf("feedback(%s): %s", probe.Kind, probe.Title)
	req := space.SubmitRequest{
		RepoDir:           dir,
		System:            "feedback",
		Verb:              "submit",
		ArtifactID:        probe.ID,
		Files:             []space.FileWrite{{Path: gopath.Join("feedback", "inbox", probe.ID+".yaml"), Content: record}},
		CommitMessage:     title,
		CommitAuthorName:  s.cfg.CommitAuthorName,
		CommitAuthorEmail: s.cfg.CommitAuthorEmail,
		RemoteURL:         s.cfg.RemoteURL,
		Repo:              s.cfg.Repo,
		BaseBranch:        baseBranch,
		PRTitle:           title,
		Credential:        s.cfg.Credential,
		// Feedback's audience is the consumer agent, who has no write
		// access to the product repo — a 403 here is the NORMAL case, not
		// a credential fault, so this is the one write that routes through
		// the submitter's own fork (P28). A collaborator's submit never
		// reaches the fallback: their push succeeds.
		AllowForkFallback: true,
		// The deterministic feedback branch can outlive a failed OpenPR API
		// call. The funnel has already proved no PR owns it, so a retry may
		// replace only the exact remote SHA it observed.
		ReplaceOrphanBranch: true,
	}

	writeResult, writeErr := s.funnel.Submit(ctx, req)
	result := mapSubmitResult(path, probe.ID, writeResult)
	if writeErr != nil {
		result.ErrorCode = ErrorCodeSharedWrite
		result.Error = writeErr.Error()
	}

	// A decoded PR number and URL are the ledger boundary. A pushed branch or
	// an ambiguous provider outcome is not enough evidence to claim filing.
	if writeResult.PRNumber > 0 && writeResult.PRURL != "" {
		if ledgerErr := s.appendLedger(s.ledgerPath, LedgerItem{
			ID: probe.ID, Kind: probe.Kind, Title: probe.Title, PRURL: writeResult.PRURL,
			Filed: s.now().UTC().Format(time.RFC3339),
		}); ledgerErr != nil {
			result.ErrorCode = ErrorCodeLocalLedger
			result.Error = "local ledger was not recorded"
			return result, &LedgerRecordError{Err: ledgerErr}
		}
		result.LedgerRecorded = true
	}

	if writeErr != nil {
		return result, fmt.Errorf("feedback: %s: %w", op, writeErr)
	}
	return result, nil
}

func mapSubmitResult(path, id string, result space.WriteResult) SubmitResult {
	return SubmitResult{
		ID:              id,
		InputPath:       path,
		Branch:          result.Branch,
		PRNumber:        result.PRNumber,
		PRURL:           result.PRURL,
		Stage:           result.Stage,
		State:           result.State,
		RemainingAction: result.RemainingAction,
		MergeMethod:     result.MergeMethod,
		Note:            result.Note,
		AlreadyOpen:     result.State == space.WriteStateAlreadyOpen || result.State == space.WriteStateAlreadyMerged,
	}
}

// ValidatePath performs every pre-write check Submit applies without cloning,
// committing, pushing, opening a PR, or appending the ledger. The CLI uses it
// to validate an entire batch before the first item writes.
func (s *Submitter) ValidatePath(path string) error {
	raw, err := s.readFile(path)
	if err != nil {
		return fmt.Errorf("feedback: Submit: %w", err)
	}
	// The same bytes Submit would send, so a batch pre-check and the write it
	// gates cannot reach different verdicts.
	record, err := s.normalize(raw)
	if err != nil {
		return fmt.Errorf("feedback: Submit: %w", err)
	}
	return s.validateRaw(record)
}

func (s *Submitter) validateRaw(raw []byte) error {
	report := Validate(raw, Options{})
	if !report.Valid {
		return &ValidationRefusedError{Violations: report.Violations}
	}
	if githubRemote(s.cfg.RemoteURL) && s.cfg.Credential.Token == "" {
		return fmt.Errorf(
			"feedback: Submit: GitHub credential is required before any write; log in with `gh auth login`, or set A2A_FEEDBACK_TOKEN (or GITHUB_TOKEN / GH_TOKEN)",
		)
	}
	return nil
}

// githubRemote distinguishes the production feedback target from local
// filesystem fixtures, which deliberately remain credential-free. cmd/a2a has
// already parsed the production repository before constructing Submitter; this
// check owns only the pre-write refusal boundary.
func githubRemote(remote string) bool {
	if strings.HasPrefix(remote, "git@github.com:") {
		return true
	}
	u, err := url.Parse(remote)
	return err == nil && strings.EqualFold(u.Hostname(), "github.com")
}

// ValidationRefusedError is returned when Submit refuses a red report
// (I5/§T1: "validate first, refuse red").
type ValidationRefusedError struct {
	Violations []Violation
}

func (e *ValidationRefusedError) Error() string {
	if len(e.Violations) == 0 {
		return "feedback: submit: refused: invalid report"
	}
	first := e.Violations[0]
	return fmt.Sprintf("feedback: submit: refused: %d violation(s), first: %s: %s", len(e.Violations), first.Code, first.Message)
}
