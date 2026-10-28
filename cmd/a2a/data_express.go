package main

// data_express.go is the express data flow (local-space-2026-10 spec 05): the
// three composite verbs `a2a data request`, `a2a data deliver --for <XW>` and
// `a2a data verify --for <XW> [--record]`, and the one sequencer under them.
//
// The sequencer composes ONLY existing verbs. It runs the shipped lifecycle
// commands (ack, accept, respond, supersede, close, dispute) and the shipped
// submit command through their own constructors, and the shipped data core
// methods (pack, deliver, verify, fetch) — it adds no write path of its own.
// What it owns is the ORDER and the RESUME: which inner step is owed given
// what the refreshed mirror already says, and what to do with each step's
// WriteResult before starting the next.
//
// Placement (ADR-019): every lifecycle command and the submit command live in
// internal/cli and the data core lives here, so the one place able to hold
// both without a cycle is cmd/a2a — the same split data_wiring.go already
// uses. internal/mcp cannot import either, so the MCP twin gets its own copy
// of the result shapes (mcp.DataExpressOperations) and an adapter below.
//
// Sequencing is by WriteResult.State, never by a sleep:
//
//   - merged / already-merged: refresh the mirror and continue.
//   - pending-merge / already-open: wait for the PR through space.Awaiter
//     (Refresh = mirror fetch, Clear = pending-marker removal) bounded by the
//     request's timeout.
//   - needs-attention / outcome-unknown / a refusal / a timeout: stop, report
//     the furthest landed step, and return a non-nil error.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// Step outcomes reported in DataStep.Outcome.
const (
	expressLanded      = "landed"       // this invocation performed the step
	expressAlreadyDone = "already-done" // the mirror already shows the step; nothing was written
	expressStopped     = "stopped"      // the step started and could not finish
	expressRefused     = "refused"      // the inner verb refused before writing
)

// expressVerifyCriterionText is ac1 on every request this verb authors: the
// one criterion `data verify` can judge by itself.
const expressVerifyCriterionText = "The delivered package passes a2a data verify."

// expressSubmitter is the funnel seam the inner verbs write through. The
// production value is a validating *space.WriteFunnel; tests inject a
// scripted one to reach the pending / needs-attention branches.
type expressSubmitter interface {
	Submit(ctx context.Context, req space.SubmitRequest) (space.WriteResult, error)
}

// expressRecorder decorates a funnel to keep what it returned, so the
// sequencer can read each inner verb's WriteResult without parsing its stdout.
type expressRecorder struct {
	inner  expressSubmitter
	writes []space.WriteResult
}

func (r *expressRecorder) Submit(ctx context.Context, req space.SubmitRequest) (space.WriteResult, error) {
	result, err := r.inner.Submit(ctx, req)
	r.writes = append(r.writes, result)
	return result, err
}

// expressCommand is the part of a cli.Command the sequencer drives.
type expressCommand interface {
	Run(ctx context.Context, args []string, stdio cli.IO) int
}

// expressEnv is everything an inner command is constructed from, resolved
// fresh before each step so every step reads the mirror as the previous step
// left it.
type expressEnv struct {
	funnel   *expressRecorder
	manifest space.Manifest
	hostCfg  cli.SubmitHostConfig
	legality *cli.LegalityAdapter
	pending  cli.PendingMarker
}

// --- seams on the core ------------------------------------------------------

// refresh re-fetches the mirror so the next step reads what the last one
// landed. A test replaces refreshMirror.
func (c *dataCore) refresh(ctx context.Context) error {
	if c.refreshMirror != nil {
		return c.refreshMirror(ctx)
	}
	credential, err := c.resolveCredential(ctx)
	if err != nil {
		return err
	}
	return space.CloneOrFetch(ctx, c.mirrorDir, c.remoteURL, credential)
}

// submitterFor is the validating funnel the inner verbs write through: the
// same adapter chain resolveLifecycleDepsWithPolicy builds, so a step the
// express flow runs is judged exactly as the standalone verb would judge it.
// (dataCore's own c.funnel keeps its no-op validator for the package files, as
// data_wiring.go documents.)
func (c *dataCore) submitterFor(manifest space.Manifest) (expressSubmitter, error) {
	if c.expressFunnel != nil {
		return c.expressFunnel(manifest)
	}
	resolver := cli.NewMirrorResolver(c.mirrorDir, manifest)
	legality := cli.NewLegalityAdapter(c.mirrorDir, c.ownSystem, manifest)
	validator := cli.NewSubmitValidatorAdapter(c.engine, c.ownSystem, resolver, legality)
	return space.NewWriteFunnel(c.host, validator, c.binary), nil
}

func (c *dataCore) awaitPR(ctx context.Context, req space.AwaitRequest) (space.AwaitResult, error) {
	if c.awaitMerge != nil {
		return c.awaitMerge(ctx, req)
	}
	return space.NewAwaiter(c.host).Await(ctx, req)
}

func (c *dataCore) pendingMarker() cli.PendingMarker {
	if c.cacheDir == "" {
		return cli.NewNoopPendingMarker()
	}
	return cli.NewCacheBackedPendingMarker(c.cacheDir)
}

func (c *dataCore) clearMarkers(ids []string) error {
	if c.cacheDir == "" {
		return nil
	}
	for _, id := range ids {
		if err := cache.RemoveMarker(c.cacheDir, c.spaceID, id); err != nil {
			return err
		}
	}
	return nil
}

// --- the run -----------------------------------------------------------------

// expressRun is one composite invocation: its accumulating result and the
// knobs every inner step shares.
type expressRun struct {
	c       *dataCore
	actor   cli.ActorFlags
	timeout time.Duration
	result  cli.DataExpressResult
}

func (c *dataCore) newExpressRun(actor cli.ActorFlags, timeout time.Duration) *expressRun {
	if timeout <= 0 {
		timeout = cli.DefaultDataExpressTimeout
	}
	return &expressRun{c: c, actor: actor, timeout: timeout, result: cli.DataExpressResult{Steps: []cli.DataStep{}}}
}

func (r *expressRun) add(step cli.DataStep) { r.result.Steps = append(r.result.Steps, step) }

// fail names the furthest step in the space on a stopping error, so a caller
// (or an agent reading the message) knows where to resume from without reading
// the steps[] too. A step the mirror already showed done counts: it is as
// landed as one this invocation wrote.
func (r *expressRun) fail(err error) error {
	if err == nil {
		return nil
	}
	last := "none"
	for _, step := range r.result.Steps {
		if step.Outcome == expressLanded || step.Outcome == expressAlreadyDone {
			last = strings.TrimSpace(step.Step + " " + step.Subject)
		}
	}
	return fmt.Errorf("%w (last landed step: %s)", err, last)
}

func (r *expressRun) skip(step, subject, transition, note string) {
	r.add(cli.DataStep{Step: step, Subject: subject, Transition: transition, Outcome: expressAlreadyDone, Note: note})
}

// env resolves the per-step dependencies: manifest, base branch, credential
// and a recording validating funnel.
func (r *expressRun) env(ctx context.Context) (expressEnv, error) {
	c := r.c
	manifest, err := c.loadManifest()
	if err != nil {
		return expressEnv{}, fmt.Errorf("load space manifest: %w", err)
	}
	base, err := c.resolveDataBaseBranch(ctx)
	if err != nil {
		return expressEnv{}, fmt.Errorf("resolve base branch: %w", err)
	}
	credential, err := c.resolveCredential(ctx)
	if err != nil {
		return expressEnv{}, err
	}
	inner, err := c.submitterFor(manifest)
	if err != nil {
		return expressEnv{}, err
	}
	return expressEnv{
		funnel:   &expressRecorder{inner: inner},
		manifest: manifest,
		hostCfg: cli.SubmitHostConfig{
			RemoteURL: c.remoteURL, Repo: c.repository, BaseBranch: base, Credential: credential,
			CommitAuthorName: c.authorName, CommitAuthorEmail: c.authorEmail,
		},
		legality: cli.NewLegalityAdapter(c.mirrorDir, c.ownSystem, manifest),
		pending:  c.pendingMarker(),
	}, nil
}

// The shipped verbs parse with parseArgsAnyOrder: positionals LEADING the
// arguments are lifted, then the flag package stops at the first positional it
// meets. So an argv is either "<positionals> <flags...>" or "<flags...>
// <positional>"; mixing flags, a positional, then more flags reads the trailing
// flags as extra positionals. The helpers below build the two shapes.

// idThenFlags is "<id> <flags...> <actor flags...>", for a verb whose only
// positional leads.
func (r *expressRun) idThenFlags(id string, flags ...string) []string {
	return append(append([]string{id}, flags...), r.actorFlags()...)
}

// flagsThenID is "<flags...> <actor flags...> <id>", for a verb whose flags
// already precede its positional.
func (r *expressRun) flagsThenID(id string, flags ...string) []string {
	return append(append(append([]string{}, flags...), r.actorFlags()...), id)
}

func (r *expressRun) actorFlags() []string {
	var out []string
	if r.actor.Kind != "" {
		out = append(out, "--actor-kind", r.actor.Kind)
	}
	if r.actor.Name != "" {
		out = append(out, "--actor-name", r.actor.Name)
	}
	if r.actor.Model != "" {
		out = append(out, "--actor-model", r.actor.Model)
	}
	return out
}

// lifecycleCommand constructs one shipped lifecycle verb over env.
func (r *expressRun) lifecycleCommand(verb string, env expressEnv) (expressCommand, error) {
	c := r.c
	resolve := func(flags cli.ActorFlags) (template.Actor, error) {
		return c.resolveActor(flags.Kind, flags.Name, flags.Model)
	}
	type pendingSetter interface{ SetPendingMarker(cli.PendingMarker) }
	var cmd expressCommand
	switch verb {
	case "ack":
		cmd = cli.NewAckCommand(env.funnel, c.mirrorDir, c.spaceID, c.ownSystem, env.manifest, env.hostCfg, resolve)
	case "accept":
		cmd = cli.NewAcceptCommand(env.funnel, c.mirrorDir, c.spaceID, c.ownSystem, env.manifest, env.hostCfg, resolve)
	case "close":
		cmd = cli.NewCloseCommand(env.funnel, c.mirrorDir, c.spaceID, c.ownSystem, env.manifest, env.hostCfg, resolve)
	case "supersede":
		cmd = cli.NewSupersedeCommand(env.funnel, c.mirrorDir, c.spaceID, c.ownSystem, env.manifest, env.hostCfg, resolve)
	case "respond":
		cmd = cli.NewRespondCommand(env.funnel, c.mirrorDir, c.spaceID, c.ownSystem, env.manifest, env.hostCfg, resolve)
	case "dispute":
		cmd = cli.NewDisputeCommand(env.funnel, c.mirrorDir, c.spaceID, c.ownSystem, env.manifest, env.hostCfg, resolve)
	default:
		return nil, fmt.Errorf("data: express flow has no inner verb %q", verb)
	}
	if setter, ok := cmd.(pendingSetter); ok {
		setter.SetPendingMarker(env.pending)
	}
	return cmd, nil
}

// runInner drives one inner command to completion: it builds the command,
// runs it with its output captured (the express result carries steps, not the
// inner verbs' prose), and settles the write it made. A nil build means the
// shipped submit command.
func (r *expressRun) runInner(ctx context.Context, step, subject, transition string, build func(expressEnv) (expressCommand, error), argv []string) error {
	env, err := r.env(ctx)
	if err != nil {
		r.add(cli.DataStep{Step: step, Subject: subject, Transition: transition, Outcome: expressRefused, Note: err.Error()})
		return fmt.Errorf("data: %s %s: %w", step, subject, err)
	}
	cmd, err := build(env)
	if err != nil {
		r.add(cli.DataStep{Step: step, Subject: subject, Transition: transition, Outcome: expressRefused, Note: err.Error()})
		return err
	}
	var stdout, stderr bytes.Buffer
	code := cmd.Run(ctx, argv, cli.IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		note := strings.TrimSpace(stderr.String())
		if note == "" {
			note = strings.TrimSpace(stdout.String())
		}
		outcome := expressRefused
		var write *space.WriteResult
		if n := len(env.funnel.writes); n > 0 {
			last := env.funnel.writes[n-1]
			write = &last
			outcome = expressStopped
		}
		r.add(cli.DataStep{Step: step, Subject: subject, Transition: transition, Outcome: outcome, Write: write, Note: note})
		return fmt.Errorf("data: %s %s: %s", step, subject, firstLine(note))
	}
	if len(env.funnel.writes) == 0 {
		// Exit 0 with no funnel call: the verb found its work already done.
		r.skip(step, subject, transition, firstLine(strings.TrimSpace(stdout.String())))
		return nil
	}
	return r.settle(ctx, cli.DataStep{Step: step, Subject: subject, Transition: transition}, env.funnel.writes[len(env.funnel.writes)-1], []string{subject})
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// settle applies the sequencing rule to one step's write.
func (r *expressRun) settle(ctx context.Context, step cli.DataStep, write space.WriteResult, markerIDs []string) error {
	step.Write = &write
	ids := markerIDs
	if len(write.ArtifactIDs) > 0 {
		ids = write.ArtifactIDs
	}
	switch write.State {
	case space.WriteStateMerged, space.WriteStateAlreadyMerged:
		step.Outcome = expressLanded
		if write.State == space.WriteStateAlreadyMerged {
			step.Outcome = expressAlreadyDone
		}
		if err := r.c.clearMarkers(ids); err != nil {
			step.Outcome, step.Note = expressStopped, err.Error()
			r.add(step)
			return fmt.Errorf("data: %s %s: clear pending marker: %w", step.Step, step.Subject, err)
		}
		if err := r.c.refresh(ctx); err != nil {
			step.Outcome, step.Note = expressStopped, "the write merged but the mirror refresh failed: "+err.Error()
			r.add(step)
			return fmt.Errorf("data: %s %s: refresh mirror: %w", step.Step, step.Subject, err)
		}
		r.add(step)
		return nil
	case space.WriteStatePendingMerge, space.WriteStateAlreadyOpen:
		credential, err := r.c.resolveCredential(ctx)
		if err == nil {
			waitCtx, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()
			_, err = r.c.awaitPR(waitCtx, space.AwaitRequest{
				Branch: write.Branch, Repo: r.c.repository, Credential: credential,
				Refresh: r.c.refresh,
				Clear:   func() error { return r.c.clearMarkers(ids) },
			})
			if errors.Is(err, context.DeadlineExceeded) {
				err = fmt.Errorf("PR %s was not merged within %s: %w", write.PRURL, r.timeout, err)
			}
		}
		if err != nil {
			step.Outcome, step.Note = expressStopped, err.Error()
			r.add(step)
			return fmt.Errorf("data: %s %s: %w", step.Step, step.Subject, err)
		}
		step.Outcome = expressLanded
		r.add(step)
		return nil
	default:
		step.Outcome = expressStopped
		step.Note = strings.TrimSpace(fmt.Sprintf("write ended %s at %s: %s", write.State, write.Stage, write.Note))
		r.add(step)
		return fmt.Errorf("data: %s %s: %s (PR %s)", step.Step, step.Subject, step.Note, write.PRURL)
	}
}

// settleCore settles a write made by a data core method (deliver, verify):
// those write through c.funnel directly, so the pending marker the lifecycle
// commands set themselves is set here.
func (r *expressRun) settleCore(ctx context.Context, step cli.DataStep, write *space.WriteResult, callErr error, markerIDs []string) error {
	if write == nil || write.State == "" {
		if callErr == nil {
			callErr = errors.New("the write returned no outcome")
		}
		step.Outcome, step.Note = expressRefused, callErr.Error()
		r.add(step)
		return fmt.Errorf("data: %s %s: %w", step.Step, step.Subject, callErr)
	}
	// A write that came back with an error never landed a step, whatever state
	// it reports beside it: a closed-unmerged PR is reported already-open WITH an
	// error, and the standalone verb exits 1 on it. Stop; do not mark it pending
	// or wait on it.
	if callErr != nil {
		step.Write = write
		step.Outcome, step.Note = expressStopped, callErr.Error()
		r.add(step)
		return fmt.Errorf("data: %s %s: %w", step.Step, step.Subject, callErr)
	}
	switch write.State {
	case space.WriteStatePendingMerge, space.WriteStateAlreadyOpen:
		ids := markerIDs
		if len(write.ArtifactIDs) > 0 {
			ids = write.ArtifactIDs
		}
		for _, id := range ids {
			if err := r.c.pendingMarker().MarkPending(ctx, r.c.spaceID, id, *write); err != nil {
				step.Outcome, step.Note = expressStopped, "pending-merge marker failed: "+err.Error()
				r.add(step)
				return fmt.Errorf("data: %s %s: %w", step.Step, step.Subject, err)
			}
		}
	}
	return r.settle(ctx, step, *write, markerIDs)
}

// --- reading the mirror -------------------------------------------------------

// expressEvent is one committed event with the refs the fold needs to attach
// response-scoped events to their parent.
type expressEvent struct {
	Event      string         `yaml:"event"`
	Subject    string         `yaml:"subject"`
	Transition string         `yaml:"transition"`
	State      string         `yaml:"state"`
	Actor      dataEventActor `yaml:"actor"`
	Refs       []struct {
		Ref string `yaml:"ref"`
	} `yaml:"refs"`
}

func (c *dataCore) expressReadEvents() ([]expressEvent, error) {
	matches, err := filepath.Glob(filepath.Join(c.mirrorDir, "*", "events", "*", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("data: list committed events: %w", err)
	}
	out := make([]expressEvent, 0, len(matches))
	for _, m := range matches {
		raw, err := os.ReadFile(m) //nolint:gosec // reason: m comes from filepath.Glob against the mirror's own committed tree
		if err != nil {
			return nil, fmt.Errorf("data: read committed event %s: %w", m, err)
		}
		var ev expressEvent
		if err := yaml.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("data: decode committed event %s: %w", m, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

// expressFoldEvents selects the events of primaryID's own fold: its own, plus
// those whose subject is a response attached to it by a respond event (verify
// and dispute target the response, not the parent).
func expressFoldEvents(all []expressEvent, primaryID string) []fold.Event {
	responses := map[string]bool{}
	for _, ev := range all {
		if ev.Subject == primaryID && ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			responses[ev.Refs[0].Ref] = true
		}
	}
	var out []fold.Event
	for _, ev := range all {
		if ev.Subject != primaryID && !responses[ev.Subject] {
			continue
		}
		fe := fold.Event{
			ULID: ev.Event, Subject: ev.Subject, Transition: ev.Transition, ClaimedState: fold.State(ev.State),
			Actor: fold.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
		}
		if ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			fe.ResponseID = ev.Refs[0].Ref
		}
		out = append(out, fe)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ULID < out[j].ULID })
	return out
}

// expressDoc is the slice of an exchange envelope the flow reads.
type expressDoc struct {
	ID           string `yaml:"id"`
	Space        string `yaml:"space"`
	From         string `yaml:"from"`
	To           any    `yaml:"to"`
	Thread       string `yaml:"thread"`
	Parent       string `yaml:"parent"`
	Fulfills     []string
	Delivers     []string `yaml:"delivers"`
	Deliverables []struct {
		Ref  string `yaml:"ref"`
		Kind string `yaml:"kind"`
	} `yaml:"deliverables"`
	Refs []struct {
		Ref string `yaml:"ref"`
	} `yaml:"refs"`
	AcceptanceCriteria []any `yaml:"acceptance_criteria"`
}

func (c *dataCore) expressLoadDoc(id string) (expressDoc, error) {
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return expressDoc{}, fmt.Errorf("data: %s: %w", id, err)
	}
	rel, err := dataArtifactPath(parsed)
	if err != nil {
		return expressDoc{}, err
	}
	return expressReadDoc(filepath.Join(c.mirrorDir, rel))
}

func expressReadDoc(path string) (expressDoc, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // reason: path is derived from the mirror's own layout or glob
	if err != nil {
		return expressDoc{}, fmt.Errorf("data: cannot read %s: %w", path, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return expressDoc{}, fmt.Errorf("data: %s: %w", path, err)
	}
	var doc expressDoc
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		return expressDoc{}, fmt.Errorf("data: %s: cannot decode envelope: %w", path, err)
	}
	var fulfills struct {
		Fulfills any `yaml:"fulfills"`
	}
	if err := yaml.Unmarshal(fm.YAML, &fulfills); err != nil {
		return expressDoc{}, fmt.Errorf("data: %s: cannot decode fulfills: %w", path, err)
	}
	doc.Fulfills = dataToStrings(fulfills.Fulfills)
	return doc, nil
}

// criterionID is the declared id of acceptance criterion i, or "" for the
// bare-string (envelope/v1) form.
func (d expressDoc) criterionID(i int) string {
	if i < 0 || i >= len(d.AcceptanceCriteria) {
		return ""
	}
	if m, ok := d.AcceptanceCriteria[i].(map[string]any); ok {
		if id, ok := m["id"].(string); ok {
			return id
		}
	}
	return ""
}

type expressHandoff struct {
	id        string
	packageID string
	state     fold.State
	first     string // the first event ULID: the order handoffs were submitted in
}

type expressResponse struct {
	id       string
	delivers []string
	refs     []string
	state    fold.State
}

// expressSnapshot is the flow's whole view of one work_request, read from the
// mirror as it stands.
type expressSnapshot struct {
	id        string
	doc       expressDoc
	state     fold.State
	handoffs  []expressHandoff // oldest first
	responses []expressResponse
	floor     string // the space's min_binary_version
}

func (s expressSnapshot) producer() string {
	if to := dataToStrings(s.doc.To); len(to) > 0 {
		return to[0]
	}
	return ""
}

// active is the newest handoff that has not been superseded.
func (s expressSnapshot) active() *expressHandoff {
	for i := len(s.handoffs) - 1; i >= 0; i-- {
		if s.handoffs[i].state != fold.StateSuperseded {
			return &s.handoffs[i]
		}
	}
	return nil
}

// responseFor is the response announcing h: the one that delivers its package,
// or failing that references it.
func (s expressSnapshot) responseFor(h expressHandoff) *expressResponse {
	for i := len(s.responses) - 1; i >= 0; i-- {
		for _, d := range s.responses[i].delivers {
			if d == h.packageID {
				return &s.responses[i]
			}
		}
	}
	for i := len(s.responses) - 1; i >= 0; i-- {
		for _, ref := range s.responses[i].refs {
			if ref == h.id {
				return &s.responses[i]
			}
		}
	}
	return nil
}

func (c *dataCore) expressSnapshot(wrID string) (expressSnapshot, error) {
	parsed, err := artifact.ParseID(wrID)
	if err != nil || parsed.Prefix != "XW" {
		return expressSnapshot{}, fmt.Errorf("data: --for wants a work_request id (XW-...), got %q", wrID)
	}
	wrEnv, _, err := c.loadEnvelope(wrID)
	if err != nil {
		return expressSnapshot{}, err
	}
	doc, err := c.expressLoadDoc(wrID)
	if err != nil {
		return expressSnapshot{}, err
	}
	manifest, err := c.loadManifest()
	if err != nil {
		return expressSnapshot{}, fmt.Errorf("data: load space manifest: %w", err)
	}
	membership := dataMembership(manifest)
	all, err := c.expressReadEvents()
	if err != nil {
		return expressSnapshot{}, err
	}
	wrResult := fold.Fold(wrEnv.Kind, wrEnv, expressFoldEvents(all, wrID), membership)
	snap := expressSnapshot{id: wrID, doc: doc, state: wrResult.State, floor: manifest.MinBinaryVersion}

	exchanges, err := filepath.Glob(filepath.Join(c.mirrorDir, "*", "exchanges", "*.md"))
	if err != nil {
		return expressSnapshot{}, fmt.Errorf("data: list exchanges: %w", err)
	}
	for _, path := range exchanges {
		base := filepath.Base(path)
		switch {
		case strings.HasPrefix(base, "XH-"):
			// A handoff the flow cannot read is not "a handoff of another request":
			// skipping it would let a re-run deliver a second package over it.
			h, err := expressReadDoc(path)
			if err != nil {
				return expressSnapshot{}, err
			}
			if !containsString(h.Fulfills, wrID) {
				continue
			}
			env := fold.Envelope{ID: h.ID, Kind: fold.KindHandoff, From: h.From, To: dataToStrings(h.To)}
			events := expressFoldEvents(all, h.ID)
			handoff := expressHandoff{id: h.ID, state: fold.Fold(fold.KindHandoff, env, events, membership).State}
			if len(events) > 0 {
				handoff.first = events[0].ULID
			}
			for _, d := range h.Deliverables {
				if d.Kind == "data" {
					handoff.packageID = d.Ref
				}
			}
			snap.handoffs = append(snap.handoffs, handoff)
		case strings.HasPrefix(base, "XS-"):
			resp, err := expressReadDoc(path)
			if err != nil {
				return expressSnapshot{}, err
			}
			if resp.Parent != wrID {
				continue
			}
			r := expressResponse{id: resp.ID, delivers: resp.Delivers, state: wrResult.Responses[resp.ID]}
			for _, ref := range resp.Refs {
				r.refs = append(r.refs, ref.Ref)
			}
			snap.responses = append(snap.responses, r)
		}
	}
	sort.Slice(snap.handoffs, func(i, j int) bool { return snap.handoffs[i].first < snap.handoffs[j].first })
	return snap, nil
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// expressContractRef finds the one XC@version a request pins. A request that
// pins none, or several, needs the caller to name it.
func expressContractRef(doc expressDoc, explicit string) (string, error) {
	var pinned []string
	for _, ref := range doc.Refs {
		if strings.HasPrefix(ref.Ref, "XC-") && strings.Contains(ref.Ref, "@") {
			pinned = append(pinned, ref.Ref)
		}
	}
	if explicit != "" {
		return explicit, nil
	}
	if len(pinned) != 1 {
		return "", fmt.Errorf("data: %s pins %d contract ref(s) (%s): pass --contract <XC-id>@<version> to choose", doc.ID, len(pinned), strings.Join(pinned, ", "))
	}
	return pinned[0], nil
}

// --- data request --------------------------------------------------------------

// expressRequestEntropy derives the request's id entropy from its content, so
// repeating the identical request on the same day yields the SAME XW id: the
// second run finds it already landed and writes nothing. The same trade
// dataReportEntropy makes for report ids.
func expressRequestEntropy(parts ...string) io.Reader {
	sum := sha256.Sum256([]byte("a2ahub-data-request-id-v1\x00" + strings.Join(parts, "\x00")))
	return bytes.NewReader(sum[:])
}

func (c *dataCore) request(ctx context.Context, req cli.DataRequestRequest) (cli.DataExpressResult, error) {
	r := c.newExpressRun(req.Actor, req.Timeout)
	err := r.fail(r.request(ctx, req))
	return r.result, err
}

func (r *expressRun) request(ctx context.Context, req cli.DataRequestRequest) error {
	c := r.c
	if err := c.refresh(ctx); err != nil {
		return fmt.Errorf("data: request: refresh mirror: %w", err)
	}
	manifest, err := c.loadManifest()
	if err != nil {
		return fmt.Errorf("data: request: load space manifest: %w", err)
	}
	if !dataParticipant(manifest, req.Producer) {
		return fmt.Errorf("data: request: %q is not a participant of this space", req.Producer)
	}
	if req.Producer == c.ownSystem {
		return fmt.Errorf("data: request: --producer is this system (%s); a request is addressed to another system", c.ownSystem)
	}
	_, pinned, err := c.resolveContractSchemas(ctx, req.Contract)
	if err != nil {
		return err
	}
	contractRef, _, _ := strings.Cut(pinned, "#")

	now := c.now()
	title := req.Title
	if title == "" {
		title = "Data conforming to " + contractRef
	}
	neededBy := req.NeededBy
	if neededBy == "" {
		neededBy = now.UTC().AddDate(0, 0, 2).Format("2006-01-02")
	} else if _, perr := time.Parse("2006-01-02", neededBy); perr != nil {
		return fmt.Errorf("data: request: --needed-by must be YYYY-MM-DD: %w", perr)
	}
	criteria := append([]string{expressVerifyCriterionText}, req.AcceptanceCriteria...)

	entropy := expressRequestEntropy(append([]string{
		c.ownSystem, req.Producer, contractRef, title, req.InterimBehavior, neededBy, now.UTC().Format("20060102"),
	}, criteria...)...)
	id, err := artifact.MintExchangeIDAt("XW", c.ownSystem, now, entropy)
	if err != nil {
		return fmt.Errorf("data: request: mint id: %w", err)
	}
	thread, err := artifact.MintThreadIDAt(c.ownSystem, now, entropy)
	if err != nil {
		return fmt.Errorf("data: request: mint thread id: %w", err)
	}
	r.result.WorkRequest = id
	actor, err := c.resolveActor(req.Actor.Kind, req.Actor.Name, req.Actor.Model)
	if err != nil {
		return err
	}
	draft, err := template.RenderNew(template.Input{
		Type: "work_request", ID: id, Actor: actor, Created: now,
		Fields: map[string]string{
			"from": c.ownSystem, "to": req.Producer, "space": c.spaceID, "thread": thread,
			"title": title, "category": "data", "priority": "p3", "blocking": "false",
			"interim_behavior": req.InterimBehavior, "needed_by": neededBy,
			"expected_response.shape": "A delivered data package conforming to " + contractRef + ".",
		},
		Body:               []byte("Please deliver data conforming to " + contractRef + ".\n\nAcceptance evidence: the delivered package passes `a2a data verify`.\n"),
		AcceptanceCriteria: criteria,
	}, validate.IsJSONSchemaFormat)
	if err != nil {
		return fmt.Errorf("data: request: render the work_request: %w", err)
	}
	draft, err = expressPinContract(draft, contractRef)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.stagingDir, 0o755); err != nil {
		return fmt.Errorf("data: request: create staging directory: %w", err)
	}
	path := filepath.Join(c.stagingDir, id+".md")
	if err := os.WriteFile(path, draft, 0o644); err != nil {
		return fmt.Errorf("data: request: write %s: %w", path, err)
	}

	submit := func(env expressEnv) (expressCommand, error) {
		return cli.NewSubmitCommand(env.funnel, env.legality, env.pending, c.mirrorDir, c.spaceID, c.ownSystem, c.stagingDir, env.hostCfg), nil
	}
	if err := r.runInner(ctx, "submit", id, fold.TSubmit, submit, []string{path}); err != nil {
		return err
	}
	r.result.Next = fmt.Sprintf("%s runs: a2a data deliver --for %s --from <dir> --profile <synthetic|sanitized> --format <json|ndjson>", req.Producer, id)
	return nil
}

func dataParticipant(manifest space.Manifest, system string) bool {
	for _, p := range manifest.Participants {
		if p.System == system && p.Status != fold.MembershipLeft {
			return true
		}
	}
	return false
}

// expressPinContract adds the live refs block the template ships commented
// out: Fields cannot express an array-typed key, so the pin is spliced in
// before the frontmatter's closing fence.
func expressPinContract(draft []byte, contractRef string) ([]byte, error) {
	text := string(draft)
	const fence = "\n---\n"
	open := strings.Index(text, "---\n")
	if open != 0 {
		return nil, errors.New("data: request: the rendered draft has no frontmatter")
	}
	closeAt := strings.Index(text[open+4:], fence)
	if closeAt < 0 {
		return nil, errors.New("data: request: the rendered draft has no closing frontmatter fence")
	}
	at := open + 4 + closeAt + 1
	return []byte(text[:at] + "refs:\n  - ref: " + contractRef + "\n" + text[at:]), nil
}

// --- data deliver --for ----------------------------------------------------------

func (c *dataCore) deliverFor(ctx context.Context, req cli.DataDeliverForRequest) (cli.DataExpressResult, error) {
	r := c.newExpressRun(req.Actor, req.Timeout)
	r.result.WorkRequest = req.For
	err := r.fail(r.deliverFor(ctx, req))
	return r.result, err
}

func (r *expressRun) deliverNext(producer, wrID string) string {
	return fmt.Sprintf("%s runs: a2a data deliver --for %s --from <dir> --profile <synthetic|sanitized> --format <json|ndjson>", producer, wrID)
}

func (r *expressRun) deliverFor(ctx context.Context, req cli.DataDeliverForRequest) error {
	c := r.c
	if err := c.refresh(ctx); err != nil {
		return fmt.Errorf("data: deliver --for: refresh mirror: %w", err)
	}
	snap, err := c.expressSnapshot(req.For)
	if err != nil {
		return err
	}
	if !containsString(dataToStrings(snap.doc.To), c.ownSystem) {
		return fmt.Errorf("data: deliver --for: %s is addressed to %v, not to this system (%s)", req.For, dataToStrings(snap.doc.To), c.ownSystem)
	}
	switch snap.state {
	case fold.StateClosed:
		r.result.Handoff, r.result.Package = "", ""
		if h := snap.active(); h != nil {
			r.result.Handoff, r.result.Package = h.id, h.packageID
		}
		return nil // nothing owed: the consumer already closed it
	case fold.StateSubmitted, fold.StateAcknowledged, fold.StateAccepted, fold.StateInProgress, fold.StateResponded:
	default:
		return fmt.Errorf("data: deliver --for: %s is %s; there is nothing to deliver against", req.For, snap.state)
	}
	contractRef, err := expressContractRef(snap.doc, req.Contract)
	if err != nil {
		return err
	}

	active := snap.active()
	retry := active != nil && active.state == fold.StateRejected

	// Acknowledge and accept: owed only while the request has not got that far.
	if snap.state == fold.StateSubmitted {
		if err := r.runInner(ctx, "ack", req.For, fold.TAcknowledge, r.verbBuilder("ack"), r.idThenFlags(req.For)); err != nil {
			return err
		}
	} else {
		r.skip("ack", req.For, fold.TAcknowledge, "")
	}
	if snap.state == fold.StateSubmitted || snap.state == fold.StateAcknowledged {
		if err := r.runInner(ctx, "accept", req.For, fold.TAccept, r.verbBuilder("accept"), r.idThenFlags(req.For)); err != nil {
			return err
		}
	} else {
		r.skip("accept", req.For, fold.TAccept, "")
	}

	var handoffID, packageID string
	if active != nil && !retry {
		handoffID, packageID = active.id, active.packageID
		r.skip("pack", packageID, "", "")
		r.skip("deliver", handoffID, fold.TSubmit, "")
		// A run that died after this handoff landed and before the one it
		// replaced was superseded leaves that one rejected and unsuperseded, which
		// pendency keeps in the producer's inbox as an obligation. Finish it
		// here, before the respond (spec 05 §8 row 6).
		for _, old := range snap.handoffs {
			if old.id != active.id && old.state == fold.StateRejected {
				if err := r.supersedeWith(ctx, old.id, handoffID); err != nil {
					r.result.Handoff, r.result.Package = handoffID, packageID
					return err
				}
			}
		}
	} else {
		supersedes := ""
		if retry {
			supersedes = active.packageID
		}
		document, root, err := r.packOrResume(ctx, req, snap, contractRef, supersedes)
		if err != nil {
			return err
		}
		packageID = document.ID
		r.result.Package = packageID

		delivered, deliverErr := c.deliver(ctx, cli.DataDeliverRequest{
			StagingRoot: root, Fulfills: req.For, ExpectPack: document.AggregateDigest, Actor: r.actor,
		})
		// The handoff the delivery's PR carries, which is not necessarily the id
		// deliver just minted: see deliveredHandoffID.
		handoffID = deliveredHandoffID(delivered)
		step := cli.DataStep{Step: "deliver", Subject: handoffID, Transition: fold.TSubmit}
		if err := r.settleCore(ctx, step, delivered.Write, deliverErr, []string{handoffID}); err != nil {
			r.result.Handoff = handoffID
			return err
		}
		if retry {
			if err := r.supersedeWith(ctx, active.id, handoffID); err != nil {
				r.result.Handoff = handoffID
				return err
			}
		}
	}
	r.result.Handoff, r.result.Package = handoffID, packageID

	// Respond, unless the mirror already shows a response announcing this handoff.
	fresh, err := c.expressSnapshot(req.For)
	if err != nil {
		return err
	}
	if existing := fresh.responseFor(expressHandoff{id: handoffID, packageID: packageID}); existing != nil {
		r.result.Response = existing.id
		r.skip("respond", existing.id, fold.TRespond, "")
	} else {
		argv := r.flagsThenID(req.For, "--result", "delivered", "--ref", handoffID, "--delivers", packageID)
		if err := r.runInner(ctx, "respond", req.For, fold.TRespond, r.verbBuilder("respond"), argv); err != nil {
			return err
		}
		if after, err := c.expressSnapshot(req.For); err == nil {
			if resp := after.responseFor(expressHandoff{id: handoffID, packageID: packageID}); resp != nil {
				r.result.Response = resp.id
			}
		}
	}
	r.result.Next = fmt.Sprintf("%s runs: a2a data verify --for %s --record", dataFirst(dataToStrings(snap.doc.From), snap.doc.From), req.For)
	return nil
}

// supersedeWith runs the shipped supersede verb on a rejected handoff, naming
// the one that replaces it.
func (r *expressRun) supersedeWith(ctx context.Context, rejectedID, replacementID string) error {
	argv := r.idThenFlags(rejectedID, "--refs", replacementID)
	return r.runInner(ctx, "supersede", rejectedID, fold.TSupersede, r.verbBuilder("supersede"), argv)
}

// deliveredHandoffID is the handoff a delivery's write actually carries.
// dataCore.deliver mints a fresh XH id on every call, but the write is keyed on
// the package alone, so a re-run while the first delivery's PR is still open
// converges on that PR and the id deliver minted is never committed. The write's
// ArtifactIDs name the ids present on the PR (space.WriteResult), so the
// supersede and the respond must take the handoff from there.
func deliveredHandoffID(delivered cli.DataResult) string {
	if delivered.Write != nil {
		for _, id := range delivered.Write.ArtifactIDs {
			if strings.HasPrefix(id, "XH-") {
				return id
			}
		}
	}
	return delivered.HandoffID
}

func dataFirst(list []string, fallback any) string {
	if len(list) > 0 {
		return list[0]
	}
	if s, ok := fallback.(string); ok {
		return s
	}
	return "the requester"
}

func (r *expressRun) verbBuilder(verb string) func(expressEnv) (expressCommand, error) {
	return func(env expressEnv) (expressCommand, error) { return r.lifecycleCommand(verb, env) }
}

// packOrResume returns the package to deliver: an already-staged, not yet
// delivered one for this request when --from still holds the same bytes, else
// a fresh pack. A staged package over DIFFERENT bytes, or more than one staged
// candidate, is refused rather than guessed at: delivering the wrong package
// is the one error this flow cannot undo.
func (r *expressRun) packOrResume(ctx context.Context, req cli.DataDeliverForRequest, snap expressSnapshot, contractRef, supersedes string) (datapackage.Document, string, error) {
	c := r.c
	id, canonical, _, err := splitPinnedContractRef(contractRef)
	if err != nil {
		return datapackage.Document{}, "", err
	}
	pinnedPrefix := id + "@" + canonical
	staged, err := c.expressStagedCandidates(snap.doc.Thread, pinnedPrefix)
	if err != nil {
		return datapackage.Document{}, "", err
	}
	switch len(staged) {
	case 0:
	case 1:
		bytesAggregate, err := c.expressAggregateOf(ctx, req.From)
		if err != nil {
			return datapackage.Document{}, "", err
		}
		if staged[0].document.AggregateDigest != bytesAggregate {
			return datapackage.Document{}, "", fmt.Errorf(
				"data: deliver --for: %s holds a staged, undelivered package %s whose bytes differ from --from %s; deliver it as it is (a2a data deliver %s --fulfills %s) or remove it, then re-run",
				c.stagingDir, staged[0].document.ID, req.From, staged[0].root, req.For)
		}
		r.add(cli.DataStep{Step: "pack", Subject: staged[0].document.ID, Outcome: expressAlreadyDone, Note: "re-delivering the staged package at " + staged[0].root})
		return staged[0].document, staged[0].root, nil
	default:
		roots := make([]string, 0, len(staged))
		for _, s := range staged {
			roots = append(roots, s.root)
		}
		return datapackage.Document{}, "", fmt.Errorf("data: deliver --for: %d staged, undelivered packages match %s: %s; remove all but the one to deliver", len(staged), req.For, strings.Join(roots, ", "))
	}

	packed, err := c.pack(ctx, cli.DataPackRequest{
		ContractRef: contractRef, From: req.From, Profile: req.Profile, Format: req.Format,
		Expires: req.Expires, Fulfills: req.For, Supersedes: supersedes, MaxAttempts: req.MaxAttempts,
	})
	if err != nil {
		r.add(cli.DataStep{Step: "pack", Outcome: expressRefused, Note: err.Error()})
		return datapackage.Document{}, "", err
	}
	r.add(cli.DataStep{Step: "pack", Subject: packed.Manifest.ID, Outcome: expressLanded})
	return *packed.Manifest, packed.StagingRoot, nil
}

type expressStagedPackage struct {
	root     string
	document datapackage.Document
}

// expressStagedCandidates lists the staged packages for this thread and
// contract that no committed handoff references yet.
func (c *dataCore) expressStagedCandidates(thread, pinnedPrefix string) ([]expressStagedPackage, error) {
	manifests, err := filepath.Glob(filepath.Join(c.stagingDir, "data", "*", stagedManifestName))
	if err != nil {
		return nil, fmt.Errorf("data: list staged packages: %w", err)
	}
	var out []expressStagedPackage
	for _, path := range manifests {
		raw, err := os.ReadFile(path) //nolint:gosec // reason: path comes from filepath.Glob against the staging directory
		if err != nil {
			return nil, fmt.Errorf("data: read staged manifest %s: %w", path, err)
		}
		var document datapackage.Document
		if err := json.Unmarshal(raw, &document); err != nil {
			return nil, fmt.Errorf("data: decode staged manifest %s: %w", path, err)
		}
		if document.Thread != thread || !strings.HasPrefix(document.Contract, pinnedPrefix) {
			continue
		}
		delivered, err := c.expressPackageDelivered(document.ID)
		if err != nil {
			return nil, err
		}
		if delivered {
			continue
		}
		out = append(out, expressStagedPackage{root: filepath.Dir(path), document: document})
	}
	return out, nil
}

// expressPackageDelivered reports whether a committed handoff carries the
// package. Unlike findHandoffForPackage, which answers "no handoff" for a handoff
// it could not read, a handoff that cannot be read or decoded is an error here:
// "not delivered" must mean every handoff was read and none carries the package.
func (c *dataCore) expressPackageDelivered(packageID string) (bool, error) {
	matches, err := filepath.Glob(filepath.Join(c.mirrorDir, "*", "exchanges", "XH-*.md"))
	if err != nil {
		return false, fmt.Errorf("data: list committed handoffs: %w", err)
	}
	for _, path := range matches {
		h, err := expressReadDoc(path)
		if err != nil {
			return false, err
		}
		for _, d := range h.Deliverables {
			if d.Kind == "data" && d.Ref == packageID {
				return true, nil
			}
		}
	}
	return false, nil
}

// expressAggregateOf is the aggregate digest of the bytes under dir, computed
// the way pack computes the manifest's: each file is hashed on its own and the
// aggregate is combined from those per-file digests (datapackage.BuildEntrySet,
// entryset.go: `AggregateDigest: artifact.CombineDigestPairs(perFile)`), so the
// comparison never depends on a declared aggregate being believed first.
func (c *dataCore) expressAggregateOf(ctx context.Context, dir string) (string, error) {
	files, err := datapackage.WalkLocalDirectory(ctx, dir, datapackage.DefaultBounds())
	if err != nil {
		return "", fmt.Errorf("data: read --from %s: %w", dir, err)
	}
	set, err := datapackage.BuildEntrySet(files)
	if err != nil {
		return "", fmt.Errorf("data: digest --from %s: %w", dir, err)
	}
	return set.AggregateDigest, nil
}

// --- data verify --for ------------------------------------------------------------

func (c *dataCore) verifyFor(ctx context.Context, req cli.DataVerifyForRequest) (cli.DataExpressResult, error) {
	r := c.newExpressRun(req.Actor, req.Timeout)
	r.result.WorkRequest = req.For
	err := r.fail(r.verifyFor(ctx, req))
	return r.result, err
}

func (r *expressRun) verifyFor(ctx context.Context, req cli.DataVerifyForRequest) error {
	c := r.c
	if err := c.refresh(ctx); err != nil {
		return fmt.Errorf("data: verify --for: refresh mirror: %w", err)
	}
	snap, err := c.expressSnapshot(req.For)
	if err != nil {
		return err
	}
	if snap.doc.From != c.ownSystem {
		return fmt.Errorf("data: verify --for: %s was requested by %s, not by this system (%s)", req.For, snap.doc.From, c.ownSystem)
	}
	next := r.deliverNext(snap.producer(), req.For)
	h := snap.active()
	if h == nil {
		r.result.Next = next
		return nil // nothing delivered yet
	}
	r.result.Handoff, r.result.Package = h.id, h.packageID
	response := snap.responseFor(*h)
	if h.state == fold.StateRejected && response != nil && response.state == fold.StateDisputed {
		r.result.Response = response.id
		r.result.Next = next
		return nil // already judged and disputed; the producer owes a new attempt
	}
	if response != nil {
		r.result.Response = response.id
	}

	if req.To != "" {
		fetched, err := c.fetch(ctx, cli.DataFetchRequest{PackageID: h.packageID, Destination: req.To})
		if err != nil {
			r.add(cli.DataStep{Step: "fetch", Subject: h.packageID, Outcome: expressRefused, Note: err.Error()})
			return err
		}
		r.add(cli.DataStep{Step: "fetch", Subject: h.packageID, Outcome: expressLanded, Note: fetched.Outcome})
	}

	judged, err := c.verify(ctx, cli.DataVerifyRequest{PackageID: h.packageID, Actor: r.actor})
	if err != nil {
		r.add(cli.DataStep{Step: "verify", Subject: h.id, Outcome: expressRefused, Note: err.Error()})
		return err
	}
	report := judged.Report
	r.result.Report = report
	if !req.Record {
		return nil
	}
	pass := datapackage.VerifyPass(*report) == nil

	// Once the handoff is accepted or rejected the verdict is on record, and the
	// close or dispute below must follow it. A fresh verification that now
	// disagrees is refused, not obeyed: it would close over a rejected handoff or
	// dispute an accepted one.
	if (h.state == fold.StateAccepted && !pass) || (h.state == fold.StateRejected && pass) {
		fresh := "fail"
		if pass {
			fresh = "pass"
		}
		return fmt.Errorf("data: verify --for: handoff %s is recorded %s, but %s now verifies to %s: the verdict changed between runs, so nothing is closed or disputed; re-check the package and the contract pin",
			h.id, h.state, h.packageID, fresh)
	}

	// Everything that can be refused is refused BEFORE the first write.
	var closeArgs []string
	if pass {
		if snap.state != fold.StateResponded && snap.state != fold.StateClosed {
			return fmt.Errorf("data: verify --for: %s is %s, not responded: the producer's respond has not landed, so there is nothing to close yet", req.For, snap.state)
		}
		closeArgs, err = expressCloseArgs(snap, req.Verdicts)
		if err != nil {
			return err
		}
	} else if response == nil {
		return fmt.Errorf("data: verify --for: no response announcing %s on %s: the producer's respond has not landed, so there is nothing to dispute", h.id, req.For)
	}

	if h.state == fold.StateSubmitted {
		argv := r.idThenFlags(h.id)
		if err := r.runInner(ctx, "ack", h.id, fold.TAcknowledge, r.verbBuilder("ack"), argv); err != nil {
			return err
		}
	} else {
		r.skip("ack", h.id, fold.TAcknowledge, "")
	}

	if h.state == fold.StateSubmitted || h.state == fold.StateAcknowledged {
		recorded, recordErr := c.verify(ctx, cli.DataVerifyRequest{PackageID: h.packageID, Record: true, Actor: r.actor})
		transition := fold.TVerifyFail
		if pass {
			transition = fold.TVerifyPass
		}
		step := cli.DataStep{Step: "verify", Subject: h.id, Transition: transition}
		if recorded.Report != nil {
			r.result.Report = recorded.Report
		}
		if err := r.settleCore(ctx, step, recorded.Write, recordErr, []string{h.id}); err != nil {
			return err
		}
	} else {
		r.skip("verify", h.id, "", "")
	}

	if pass {
		if snap.state == fold.StateClosed {
			r.skip("close", req.For, fold.TClose, "")
			return nil
		}
		argv := r.idThenFlags(req.For, closeArgs...)
		return r.runInner(ctx, "close", req.For, fold.TClose, r.verbBuilder("close"), argv)
	}

	reason := req.Reason
	if reason == "" {
		reason = expressDisputeReason(h.packageID, r.result.Report)
	}
	argv := r.flagsThenID(response.id, "--reason", reason)
	if err := r.runInner(ctx, "dispute", response.id, fold.TDispute, r.verbBuilder("dispute"), argv); err != nil {
		return err
	}
	r.result.Next = next
	return nil
}

// expressCloseArgs builds close's --verdict arguments. At the event/v2 floor a
// pass closes with the one verdict the verification itself proves — criterion
// 0, "the delivered package passes a2a data verify", met by the producer —
// unless the caller supplied that criterion's verdict, which then wins. Every
// OTHER criterion needs the caller's own verdict: this flow cannot judge it,
// and inventing one is the failure close's verdicts exist to prevent. Below
// the floor close carries no verdicts, and a caller's --verdict is refused
// before anything is written.
func expressCloseArgs(snap expressSnapshot, callerVerdicts []string) ([]string, error) {
	if cli.LifecycleEventSchema(snap.floor) != "event/v2" {
		if len(callerVerdicts) > 0 {
			return nil, fmt.Errorf("data: verify --for: --verdict needs a space at the event/v2 floor; this space's min_binary_version %q is below it", snap.floor)
		}
		return nil, nil
	}
	criteria := len(snap.doc.AcceptanceCriteria)
	covered := map[int]bool{}
	for _, token := range callerVerdicts {
		key, _, _ := strings.Cut(token, ":")
		if idx, err := strconv.Atoi(key); err == nil {
			covered[idx] = true
			continue
		}
		for i := 0; i < criteria; i++ {
			if snap.doc.criterionID(i) == key {
				covered[i] = true
			}
		}
	}
	args := make([]string, 0, 2*(len(callerVerdicts)+1))
	for _, token := range callerVerdicts {
		args = append(args, "--verdict", token)
	}
	for i := 1; i < criteria; i++ {
		if !covered[i] {
			label := snap.doc.criterionID(i)
			if label == "" {
				label = strconv.Itoa(i)
			}
			return nil, fmt.Errorf("data: verify --for: acceptance criterion %s of %s has no verdict: pass --verdict %s:<met|unmet|not_warranted|not_exercised>:<cause_owner> (data verify can judge only criterion 0)", label, snap.id, label)
		}
	}
	if criteria > 0 && !covered[0] {
		key := snap.doc.criterionID(0)
		if key == "" {
			key = "0"
		}
		args = append(args, "--verdict", key+":met:"+snap.producer())
	}
	return args, nil
}

// expressDisputeReason names what failed, from the report's own findings.
func expressDisputeReason(packageID string, report *datapackage.Report) string {
	if report == nil {
		return "package " + packageID + " failed verification"
	}
	var failing []string
	for _, check := range report.Checks {
		if check.Status != datapackage.CheckPass {
			failing = append(failing, strings.TrimSpace(fmt.Sprintf("%s %s", check.ID, check.Path)))
		}
	}
	if len(failing) > 3 {
		failing = append(failing[:3], fmt.Sprintf("and %d more", len(failing)-3))
	}
	return fmt.Sprintf("package %s failed verification %s: %s", packageID, report.ID, strings.Join(failing, "; "))
}

// --- adapters ------------------------------------------------------------------------

func (a cliDataAdapter) Request(ctx context.Context, req cli.DataRequestRequest) (cli.DataExpressResult, error) {
	return a.core.request(ctx, req)
}

func (a cliDataAdapter) DeliverFor(ctx context.Context, req cli.DataDeliverForRequest) (cli.DataExpressResult, error) {
	return a.core.deliverFor(ctx, req)
}

func (a cliDataAdapter) VerifyFor(ctx context.Context, req cli.DataVerifyForRequest) (cli.DataExpressResult, error) {
	return a.core.verifyFor(ctx, req)
}

var _ cli.DataExpressOperations = cliDataAdapter{}

func actorFlagsOf(in mcp.ActorInput) cli.ActorFlags {
	return cli.ActorFlags{Kind: in.Kind, Name: in.Name, Model: in.Model}
}

func (a mcpDataAdapter) Request(ctx context.Context, req mcp.DataRequestRequest) (mcp.DataExpressResult, error) {
	result, err := a.core.request(ctx, cli.DataRequestRequest{
		Producer: req.Producer, Contract: req.Contract, Title: req.Title, NeededBy: req.NeededBy,
		InterimBehavior: req.InterimBehavior, AcceptanceCriteria: req.AcceptanceCriteria,
		Timeout: req.Timeout, Actor: actorFlagsOf(req.Actor),
	})
	return mcpExpressResultOf(result), err
}

func (a mcpDataAdapter) DeliverFor(ctx context.Context, req mcp.DataDeliverForRequest) (mcp.DataExpressResult, error) {
	expires := req.Expires
	if expires == 0 {
		expires = cli.DefaultDataPackExpiry
	}
	result, err := a.core.deliverFor(ctx, cli.DataDeliverForRequest{
		For: req.For, From: req.From, Profile: req.Profile, Format: req.Format, Contract: req.Contract,
		Expires: expires, MaxAttempts: req.MaxAttempts, Timeout: req.Timeout, Actor: actorFlagsOf(req.Actor),
	})
	return mcpExpressResultOf(result), err
}

func (a mcpDataAdapter) VerifyFor(ctx context.Context, req mcp.DataVerifyForRequest) (mcp.DataExpressResult, error) {
	result, err := a.core.verifyFor(ctx, cli.DataVerifyForRequest{
		For: req.For, Record: req.Record, To: req.To, Verdicts: req.Verdicts, Reason: req.Reason,
		Timeout: req.Timeout, Actor: actorFlagsOf(req.Actor),
	})
	return mcpExpressResultOf(result), err
}

// mcpExpressResultOf translates the cli result to the mcp one — the identical
// field set on both sides of the ADR-001 boundary, copied field for field.
func mcpExpressResultOf(result cli.DataExpressResult) mcp.DataExpressResult {
	steps := make([]mcp.DataStep, 0, len(result.Steps))
	for _, s := range result.Steps {
		steps = append(steps, mcp.DataStep{Step: s.Step, Subject: s.Subject, Transition: s.Transition, Outcome: s.Outcome, Write: s.Write, Note: s.Note})
	}
	return mcp.DataExpressResult{
		WorkRequest: result.WorkRequest, Handoff: result.Handoff, Package: result.Package, Response: result.Response,
		Report: result.Report, Steps: steps, Next: result.Next, Error: result.Error,
	}
}

func (r mcpDataRouter) Request(ctx context.Context, req mcp.DataRequestRequest) (mcp.DataExpressResult, error) {
	adapter, err := r.adapter(req.Space)
	if err != nil {
		return mcp.DataExpressResult{}, err
	}
	return adapter.Request(ctx, req)
}

func (r mcpDataRouter) DeliverFor(ctx context.Context, req mcp.DataDeliverForRequest) (mcp.DataExpressResult, error) {
	adapter, err := r.adapter(req.Space)
	if err != nil {
		return mcp.DataExpressResult{}, err
	}
	return adapter.DeliverFor(ctx, req)
}

func (r mcpDataRouter) VerifyFor(ctx context.Context, req mcp.DataVerifyForRequest) (mcp.DataExpressResult, error) {
	adapter, err := r.adapter(req.Space)
	if err != nil {
		return mcp.DataExpressResult{}, err
	}
	return adapter.VerifyFor(ctx, req)
}

var _ mcp.DataExpressOperations = mcpDataAdapter{}
var _ mcp.DataExpressOperations = mcpDataRouter{}
