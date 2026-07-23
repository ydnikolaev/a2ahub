package feedback

import (
	"context"
	"fmt"
	"os"
	gopath "path"
	"path/filepath"
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
	BaseBranch        string // defaults to "main" at Submit time
	Credential        host.Credential
	CommitAuthorName  string
	CommitAuthorEmail string
}

// SubmitResult is what Submit returns to its CLI caller.
type SubmitResult struct {
	ID          string
	PRURL       string
	Branch      string
	AlreadyOpen bool
}

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
	cloneOrFetch func(ctx context.Context, dir, repoURL string) error
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
func (s *Submitter) SetCloneOrFetchForTest(f func(ctx context.Context, dir, repoURL string) error) {
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

	report := Validate(raw, Options{})
	if !report.Valid {
		return SubmitResult{}, &ValidationRefusedError{Violations: report.Violations}
	}

	var probe submitProbe
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return SubmitResult{}, fmt.Errorf("feedback: %s: %w", op, err)
	}

	dir := s.mirrorDir(s.projectRoot, s.slug)
	if err := s.cloneOrFetch(ctx, dir, s.cfg.RemoteURL); err != nil {
		return SubmitResult{}, fmt.Errorf("feedback: %s: %w", op, err)
	}

	baseBranch := s.cfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	title := fmt.Sprintf("feedback(%s): %s", probe.Kind, probe.Title)
	req := space.SubmitRequest{
		RepoDir:           dir,
		System:            "feedback",
		ArtifactID:        probe.ID,
		Files:             []space.FileWrite{{Path: gopath.Join("feedback", "inbox", probe.ID+".yaml"), Content: raw}},
		CommitMessage:     title,
		CommitAuthorName:  s.cfg.CommitAuthorName,
		CommitAuthorEmail: s.cfg.CommitAuthorEmail,
		RemoteURL:         s.cfg.RemoteURL,
		Repo:              s.cfg.Repo,
		BaseBranch:        baseBranch,
		PRTitle:           title,
		Credential:        s.cfg.Credential,
	}

	result, err := s.funnel.Submit(ctx, req)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("feedback: %s: %w", op, err)
	}

	already := result.State == space.WriteStateAlreadyOpen || result.State == space.WriteStateAlreadyMerged
	if err := AppendLedger(s.ledgerPath, LedgerItem{
		ID: probe.ID, Kind: probe.Kind, Title: probe.Title, PRURL: result.PRURL,
		Filed: s.now().UTC().Format(time.RFC3339),
	}); err != nil {
		return SubmitResult{}, fmt.Errorf("feedback: %s: %w", op, err)
	}

	return SubmitResult{ID: probe.ID, PRURL: result.PRURL, Branch: result.Branch, AlreadyOpen: already}, nil
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
