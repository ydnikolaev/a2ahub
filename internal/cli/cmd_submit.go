// OP-204 `a2a validate`, OP-205 `a2a submit`, OP-220 `a2a submit --batch`/
// `--drafts` (spec 06 T1/T1.1/T1.2). This file's only package-level
// symbols are ValidateCommand/SubmitCommand + their NewXCommand
// constructors, the submitFunnel seam interface, the submitFirstTransition
// table, and file-private, uniquely-named helpers (submit* prefix) — no
// shared helper, no package var beyond that lookup table, per this
// phase's plan Placement decision (avoids collision with P7/P8/P9's
// parallel verb files in this package).
package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// submitFirstTransition maps an envelope type to the §3.4 entry
// transition `a2a submit` emits (T1.1, quoted: "submit for exchanges,
// publish for standing/broadcast types, propose for decisions") —
// cross-checked against internal/fold/table.go's own draft-fromState rows
// for each kind, not re-derived from prose. Table-driven, no per-type
// branch (Future-proofing table, §9).
var submitFirstTransition = map[string]string{
	"contract":     fold.TPublish,
	"requirement":  fold.TPublish,
	"question":     fold.TSubmit,
	"work_request": fold.TSubmit,
	"decision":     fold.TPropose,
	"handoff":      fold.TSubmit,
	"response":     fold.TSubmit,
	"announcement": fold.TPublish,
}

// --- validate (OP-204) ---------------------------------------------------

// validateReport is one path's JSON report line — `a2a validate`'s
// machine-readable output shape (§7.2 OP-204: "V1/V2 checks, machine-
// readable (JSON) output"). This phase's `validate` verb runs V1
// (ValidateDraft) only — a standalone V2 preview would need a synthetic
// candidate event/LocalContext this verb has no legitimate way to
// construct outside of `submit`'s own flow; see this phase's Deviations
// report.
type validateReport struct {
	Path   string           `json:"path"`
	Result *validate.Result `json:"result,omitempty"`
	Error  string           `json:"error,omitempty"`
}

// ValidateCommand implements `a2a validate [path|--all]`: delegates to
// internal/validate (P3) for V1 (schema-class) checks; this phase adds no
// validation logic, only wires the CLI verb (§0.5 domain table).
type ValidateCommand struct {
	engine     *validate.Engine
	stagingDir string

	readFile func(path string) ([]byte, error)
	readDir  func(dir string) ([]os.DirEntry, error)
}

// NewValidateCommand constructs the validate command. engine must not be
// nil (rails anti-pattern #10). stagingDir is `.a2a/staging/`'s path,
// used by --all.
func NewValidateCommand(engine *validate.Engine, stagingDir string) *ValidateCommand {
	return &ValidateCommand{engine: engine, stagingDir: stagingDir, readFile: os.ReadFile, readDir: os.ReadDir}
}

// Name implements cli.Command.
func (c *ValidateCommand) Name() string { return "validate" }

// Synopsis implements cli.Command.
func (c *ValidateCommand) Synopsis() string {
	return "validate a draft (V1), JSON output: validate <path> | validate --all"
}

// Run implements cli.Command. Exit codes: 2 = usage; 1 = one or more
// paths invalid or unreadable; 0 = every checked path is V1-valid. JSON
// output is always written to stdout, even on a non-zero exit (rails:
// "JSON output modes stay machine-parseable on error").
func (c *ValidateCommand) Run(_ context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	all := fs.Bool("all", false, "validate every staged draft under .a2a/staging/")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var paths []string
	if *all {
		entries, err := c.readDir(c.stagingDir)
		if err != nil && !os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stdio.Stderr, "validate: cannot list %s: %v\n", c.stagingDir, err)
			return 1
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				paths = append(paths, filepath.Join(c.stagingDir, e.Name()))
			}
		}
	} else {
		if fs.NArg() != 1 {
			_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a validate <path> | a2a validate --all")
			return 2
		}
		paths = []string{fs.Arg(0)}
	}

	var reports []validateReport
	allValid := true
	for _, p := range paths {
		raw, err := c.readFile(p)
		if err != nil {
			allValid = false
			reports = append(reports, validateReport{Path: p, Error: err.Error()})
			continue
		}
		result, err := c.engine.ValidateDraft(validate.Draft{Path: p, Raw: raw})
		if err != nil {
			allValid = false
			reports = append(reports, validateReport{Path: p, Error: err.Error()})
			continue
		}
		if !result.Valid {
			allValid = false
		}
		reports = append(reports, validateReport{Path: p, Result: &result})
	}

	enc := json.NewEncoder(stdio.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(reports); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "validate: cannot encode JSON output: %v\n", err)
		return 1
	}
	if !allValid {
		return 1
	}
	return 0
}

var _ Command = (*ValidateCommand)(nil)

// --- submit (OP-205, OP-220) ----------------------------------------------

// submitFunnel is this file's own narrow consumer-side seam over
// *space.WriteFunnel (rails ISP/DI) — *space.WriteFunnel satisfies it
// structurally; tests inject a hand-written fake so submit is unit-
// testable without a live git remote or GitHub call.
type submitFunnel interface {
	Submit(ctx context.Context, req space.SubmitRequest) (space.WriteResult, error)
}

// submitEventActor is the event/v1 actor shape (kind/name/system —
// distinct from the base envelope actor block, which has no `system`).
type submitEventActor struct {
	Kind   string `yaml:"kind"`
	Name   string `yaml:"name"`
	System string `yaml:"system"`
}

// submitEventDoc is this file's own minimal event/v1 document builder —
// just the fields §5.2.2/event.schema.json requires for a fresh entry
// transition (schema, event, space, subject, transition, actor, at).
type submitEventDoc struct {
	Schema     string           `yaml:"schema"`
	Event      string           `yaml:"event"`
	Space      string           `yaml:"space"`
	Subject    string           `yaml:"subject"`
	Transition string           `yaml:"transition"`
	Actor      submitEventActor `yaml:"actor"`
	At         string           `yaml:"at"`
}

// SubmitHostConfig carries the write funnel's per-space host-facing
// config a SubmitRequest needs beyond the artifact content itself (§4.2
// D-002): the push/PR target and commit authorship. cmd/a2a resolves
// RemoteURL from the connected space's Ref.RepoURL, Credential via
// space.ResolveCredential (§7.4/§10.5), and Repo from the space's known
// GitHub owner/name, before constructing the submit command with this.
type SubmitHostConfig struct {
	// RemoteURL is the push target (`git push` destination); Repo is the
	// same space identified for the OpenPR/FindPRByHeadBranch host calls
	// (host.Repo's owner/name shape — distinct from RemoteURL).
	RemoteURL string
	Repo      host.Repo
	// BaseBranch is the PR's target branch — normatively "main" (§4.2); a
	// zero value defaults to "main" at Run time.
	BaseBranch string
	Credential host.Credential
	// CommitAuthorName/Email are the system's machine account (T1.1,
	// quoted: "Commit author = the system's machine account").
	CommitAuthorName  string
	CommitAuthorEmail string
}

// SubmitCommand implements `a2a submit <artifact>` / `a2a submit --batch
// <artifact...>` / `a2a submit --drafts` (§7.2 OP-205/OP-220, T1.1/T1.2
// quoted). Foreign-section refusal (AC-201.3) and the idempotent
// already-submitted check (AC-301.1) both run locally, BEFORE the write
// funnel is ever called — the funnel's own step 0 (FindPRByHeadBranch) is
// itself a host call, so relying on it to catch either would violate the
// "before any git/network call" requirement.
type SubmitCommand struct {
	funnel     submitFunnel
	legality   *LegalityAdapter
	pending    PendingMarker
	mirrorDir  string
	spaceID    string
	ownSystem  string
	stagingDir string
	hostCfg    SubmitHostConfig

	now      func() time.Time
	entropy  io.Reader
	readFile func(path string) ([]byte, error)
}

// NewSubmitCommand constructs the submit command. funnel, legality and
// pending must not be nil (rails anti-pattern #10 — inject
// NewNoopPendingMarker() until P7 lands). mirrorDir is the connected
// space's local mirror clone working directory; spaceID identifies that
// space for the PendingMarker seam; ownSystem is this project's
// configured own system id (§7.4); stagingDir is `.a2a/staging/`'s path;
// hostCfg supplies the push/PR target and commit authorship a real
// submit needs (see SubmitHostConfig's own doc comment).
func NewSubmitCommand(funnel submitFunnel, legality *LegalityAdapter, pending PendingMarker, mirrorDir, spaceID, ownSystem, stagingDir string, hostCfg SubmitHostConfig) *SubmitCommand {
	return &SubmitCommand{
		funnel: funnel, legality: legality, pending: pending,
		mirrorDir: mirrorDir, spaceID: spaceID, ownSystem: ownSystem, stagingDir: stagingDir,
		hostCfg: hostCfg,
		now:     time.Now, entropy: rand.Reader, readFile: os.ReadFile,
	}
}

// Name implements cli.Command.
func (c *SubmitCommand) Name() string { return "submit" }

// Synopsis implements cli.Command.
func (c *SubmitCommand) Synopsis() string {
	return "validate (V2) and submit staged draft(s): submit <artifact> | submit --batch <artifact...> | submit --drafts"
}

type submitItem struct {
	path string
	raw  []byte
	env  submitEnvelopeProbe
}

// Run implements cli.Command. Exit codes: 2 = usage; 1 = refusal,
// validation failure, or a funnel/IO error; 0 = success (including the
// idempotent already-submitted no-op, whether whole or per-artifact).
func (c *SubmitCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	batch := fs.Bool("batch", false, "submit multiple staged artifacts as one commit + one PR (all-or-nothing)")
	drafts := fs.Bool("drafts", false, "submit every staged draft under .a2a/staging/ as one batch")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	targets, code := c.resolveTargets(*batch, *drafts, fs.Args(), stdio)
	if code >= 0 {
		return code
	}
	if len(targets) == 0 {
		_, _ = fmt.Fprintln(stdio.Stdout, "submit: nothing to submit")
		return 0
	}

	items, err := c.loadItems(targets)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "submit: %v\n", err)
		return 1
	}

	// AC-201.3: foreign-section refusal, BEFORE any git/network call,
	// all-or-nothing across the whole batch.
	for _, it := range items {
		if it.env.From != c.ownSystem {
			_, _ = fmt.Fprintf(stdio.Stderr, "submit: %s: refused (CC-002 foreign-section): artifact `from` %q does not match configured own system %q\n", it.path, it.env.From, c.ownSystem)
			return 1
		}
	}

	// AC-301.1: per-artifact idempotency — an already-submitted subject is
	// excluded from the new commit, never re-validated or re-committed.
	fresh, alreadyDone, err := c.partitionByHistory(items)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "submit: %v\n", err)
		return 1
	}
	for _, id := range alreadyDone {
		_, _ = fmt.Fprintf(stdio.Stdout, "submit: %s: already submitted\n", id)
	}
	if len(fresh) == 0 {
		return 0
	}

	req, ids, err := c.buildRequest(fresh)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "submit: %v\n", err)
		return 1
	}

	result, err := c.funnel.Submit(ctx, req)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "submit: %v\n", err)
		return 1
	}

	switch result.State {
	case space.WriteStateAlreadyOpen, space.WriteStateAlreadyMerged:
		_, _ = fmt.Fprintf(stdio.Stdout, "submit: already submitted (PR %s, %s)\n", result.PRURL, result.State)
		return 0
	default:
		if err := c.pending.MarkPending(ctx, c.spaceID, req.ArtifactID, result); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "submit: pending-merge marker failed: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "submit: opened PR %s for %s (%s)\n", result.PRURL, strings.Join(ids, ", "), result.State)
		return 0
	}
}

// resolveTargets computes the target draft paths from flags/args. A
// non-negative second return means "stop and return this exit code"
// (usage error); -1 means "targets is the answer, continue".
func (c *SubmitCommand) resolveTargets(batch, allDrafts bool, args []string, stdio IO) ([]string, int) {
	switch {
	case allDrafts:
		entries, err := os.ReadDir(c.stagingDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, -1
			}
			_, _ = fmt.Fprintf(stdio.Stderr, "submit: cannot list %s: %v\n", c.stagingDir, err)
			return nil, 1
		}
		var targets []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				targets = append(targets, filepath.Join(c.stagingDir, e.Name()))
			}
		}
		return targets, -1
	case batch:
		if len(args) == 0 {
			_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a submit --batch <artifact...>")
			return nil, 2
		}
		targets := make([]string, 0, len(args))
		for _, a := range args {
			targets = append(targets, c.resolveTarget(a))
		}
		return targets, -1
	default:
		if len(args) != 1 {
			_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a submit <artifact> | a2a submit --batch <artifact...> | a2a submit --drafts")
			return nil, 2
		}
		return []string{c.resolveTarget(args[0])}, -1
	}
}

// resolveTarget accepts either a staged-draft path or a bare artifact id
// (resolved to <stagingDir>/<id>.md), per §7.2 OP-205's own Input column.
func (c *SubmitCommand) resolveTarget(a string) string {
	if strings.Contains(a, "/") || strings.HasSuffix(a, ".md") {
		return a
	}
	return filepath.Join(c.stagingDir, a+".md")
}

func (c *SubmitCommand) loadItems(targets []string) ([]submitItem, error) {
	items := make([]submitItem, 0, len(targets))
	for _, path := range targets {
		raw, err := c.readFile(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", path, err)
		}
		fm, err := artifact.ParseFrontmatter(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var probe submitEnvelopeProbe
		if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
			return nil, fmt.Errorf("%s: cannot decode envelope: %w", path, err)
		}
		items = append(items, submitItem{path: path, raw: raw, env: probe})
	}
	return items, nil
}

func (c *SubmitCommand) partitionByHistory(items []submitItem) (fresh []submitItem, alreadyDone []string, err error) {
	for _, it := range items {
		has, herr := c.legality.HasCommittedHistory(it.env.ID)
		if herr != nil {
			return nil, nil, fmt.Errorf("%s: cannot check committed history: %w", it.path, herr)
		}
		if has {
			alreadyDone = append(alreadyDone, it.env.ID)
			continue
		}
		fresh = append(fresh, it)
	}
	return fresh, alreadyDone, nil
}

// buildRequest assembles the ONE-commit SubmitRequest (D-026: the
// artifact file + its first lifecycle event, for every fresh item) and
// returns the sorted artifact ids included, for the caller's own
// messages.
//
// Batch branch key: SubmitRequest.ArtifactID names the deterministic
// branch a2a/<system>/<id>; the core API is single-artifact-shaped, so
// for a batch this phase joins every included artifact id with "+"
// (sorted, deterministic) — this phase's own convention, not defined by
// any core package; see this phase's Deviations report.
func (c *SubmitCommand) buildRequest(fresh []submitItem) (space.SubmitRequest, []string, error) {
	layout, err := space.NewLayout(c.ownSystem)
	if err != nil {
		return space.SubmitRequest{}, nil, err
	}

	now := c.now()
	var files []space.FileWrite
	var ids []string
	for _, it := range fresh {
		sectionPath, err := submitSectionPath(layout, it.env.Type, it.env.ID)
		if err != nil {
			return space.SubmitRequest{}, nil, fmt.Errorf("%s: %w", it.path, err)
		}
		files = append(files, space.FileWrite{Path: sectionPath, Content: it.raw})

		transition, ok := submitFirstTransition[it.env.Type]
		if !ok {
			return space.SubmitRequest{}, nil, fmt.Errorf("%s: unknown envelope type %q", it.path, it.env.Type)
		}
		eventID, err := artifact.MintULIDAt(now, c.entropy)
		if err != nil {
			return space.SubmitRequest{}, nil, fmt.Errorf("cannot mint event id: %w", err)
		}
		eventDoc := submitEventDoc{
			Schema:     "event/v1",
			Event:      eventID.String(),
			Space:      c.spaceID,
			Subject:    it.env.ID,
			Transition: transition,
			Actor:      submitEventActor{Kind: it.env.Actor.Kind, Name: it.env.Actor.Name, System: c.ownSystem},
			At:         now.UTC().Format(time.RFC3339),
		}
		eventRaw, err := yaml.Marshal(eventDoc)
		if err != nil {
			return space.SubmitRequest{}, nil, fmt.Errorf("cannot encode event for %s: %w", it.env.ID, err)
		}
		eventPath := layout.EventFile(now.UTC().Format("2006"), eventID.String())
		files = append(files, space.FileWrite{Path: eventPath, Content: eventRaw})
		ids = append(ids, it.env.ID)
	}
	sort.Strings(ids)

	commitMsg := fmt.Sprintf("a2a(%s): %s", fresh[0].env.Type, fresh[0].env.ID)
	if len(fresh) > 1 {
		commitMsg = fmt.Sprintf("a2a(batch): %s", strings.Join(ids, ", "))
	}

	// CC-085's min_binary_version guard (funnel.go step 1b) only fires
	// when SubmitRequest.MinBinaryVersion is non-empty — silently leaving
	// it empty would silently disarm that guard for every submit. The
	// connected space's own space.yaml (already fetched into the mirror
	// by `a2a connect`/`a2a sync`) is the pin's source of truth; read it
	// straight from the mirror's working tree root, same as
	// DoctorCommand's own "versions" check does.
	minBinaryVersion, err := c.readMinBinaryVersion()
	if err != nil {
		return space.SubmitRequest{}, nil, fmt.Errorf("cannot read space.yaml min_binary_version pin: %w", err)
	}

	baseBranch := c.hostCfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	return space.SubmitRequest{
		RepoDir:           c.mirrorDir,
		System:            c.ownSystem,
		ArtifactID:        strings.Join(ids, "+"),
		Files:             files,
		CommitMessage:     commitMsg,
		CommitAuthorName:  c.hostCfg.CommitAuthorName,
		CommitAuthorEmail: c.hostCfg.CommitAuthorEmail,
		RemoteURL:         c.hostCfg.RemoteURL,
		Repo:              c.hostCfg.Repo,
		BaseBranch:        baseBranch,
		PRTitle:           commitMsg,
		Credential:        c.hostCfg.Credential,
		MinBinaryVersion:  minBinaryVersion,
	}, ids, nil
}

// readMinBinaryVersion reads and structurally parses <mirrorDir>/space.yaml
// for its min_binary_version pin (CC-085). A connected space's mirror
// always carries a space.yaml at its root (§4.2); a read/parse failure is
// therefore a real error, not silently treated as "no pin".
//
// This reads only the one scalar field it needs via its own minimal
// decode struct, rather than the full space.ParseManifest/Manifest
// shape: space.Manifest.Participants is a typed []Participant, and not
// every space.yaml in the wild is guaranteed to already satisfy that
// exact shape at read time (a permissive read here only ever wants
// min_binary_version) — the same "each layer owns its own minimal
// projection" idiom used throughout this codebase (see mirrorEvent's own
// doc comment in adapters.go).
func (c *SubmitCommand) readMinBinaryVersion() (string, error) {
	raw, err := c.readFile(filepath.Join(c.mirrorDir, "space.yaml"))
	if err != nil {
		return "", err
	}
	var probe struct {
		MinBinaryVersion string `yaml:"min_binary_version"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return "", fmt.Errorf("space.yaml is not valid yaml: %w", err)
	}
	return probe.MinBinaryVersion, nil
}

// submitSectionPath resolves envType/id's committed space-relative path
// per §4.2's layout (internal/space/layout.go). Contract's provides/
// <slug>/contract.md fixed filename is a known layout quirk this phase
// does not attempt to work around — see this phase's Deviations report.
func submitSectionPath(layout space.Layout, envType, id string) (string, error) {
	switch envType {
	case "contract":
		parsed, err := artifact.ParseID(id)
		if err != nil {
			return "", err
		}
		return layout.ProvidesContract(parsed.Slug), nil
	case "requirement":
		return layout.Requires(id), nil
	case "decision":
		return space.Decision(id), nil
	case "question", "work_request", "handoff", "response", "announcement":
		return layout.Exchange(id), nil
	default:
		return "", fmt.Errorf("unknown envelope type %q", envType)
	}
}

var _ Command = (*SubmitCommand)(nil)
