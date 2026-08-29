package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

// WorkStarter is the one-method consumer seam used by `work start`.
type WorkStarter interface {
	Start(context.Context, workreport.StartInput) (workreport.OperationResult, error)
}

// WorkProgressor is the semantic continuation seam. The coordinator owns all
// validation, preparation, lease CAS, and publication rules.
type WorkProgressor interface {
	Checkpoint(context.Context, workreport.CheckpointInput) (workreport.OperationResult, error)
	Wait(context.Context, workreport.WaitInput) (workreport.OperationResult, error)
	Stop(context.Context, workreport.StopInput) (workreport.OperationResult, error)
}

// WorkLocalOperator is deliberately separate from the semantic seams.
// Heartbeat and resume cannot accidentally acquire a publisher or sync hook.
type WorkLocalOperator interface {
	Heartbeat(context.Context, workreport.HeartbeatInput) (workreport.OperationResult, error)
	Resume(context.Context, workreport.ResumeInput) (workreport.OperationResult, error)
}

// WorkLocalReader resolves the exact local selection and lists local leases.
// Its implementation belongs beside the rooted lease store; the transport
// neither walks cache paths nor guesses ownership/expiry semantics.
type WorkLocalReader interface {
	ResolveWork(context.Context, string, string, string, string) (workreport.WorkIdentityInput, error)
	ListWork(context.Context, string, string, string, bool) ([]workreport.Lease, error)
}

// WorkActorFlags are the provider-neutral actor fields accepted by start.
// Session remains opaque data; the coordinator normalizes or generates it.
type WorkActorFlags struct {
	Kind    string
	Name    string
	Model   string
	Session string
}

// WorkActorResolver binds the existing CLI actor-resolution chain to work
// reporting without making this transport read environment or config itself.
type WorkActorResolver func(WorkActorFlags) (workreport.Actor, error)

// WorkCommandDeps are construction-time, configuration-trusted dependencies.
// ProjectID is a path-free digest; no project-root flag exists on this surface.
type WorkCommandDeps struct {
	Starter      WorkStarter
	Progressor   WorkProgressor
	Local        WorkLocalOperator
	Reader       WorkLocalReader
	ResolveActor WorkActorResolver
	ProjectID    string
	Space        string
}

// WorkCommand is the thin `a2a work <action>` transport.
type WorkCommand struct{ deps WorkCommandDeps }

// NewWorkCommand constructs the work command and refuses incomplete DI.
func NewWorkCommand(deps WorkCommandDeps) (*WorkCommand, error) {
	if deps.Starter == nil || deps.Progressor == nil || deps.Local == nil || deps.Reader == nil || deps.ResolveActor == nil {
		return nil, fmt.Errorf("cli: work starter, progressor, local operator, local reader and actor resolver are required")
	}
	if strings.TrimSpace(deps.ProjectID) == "" || strings.TrimSpace(deps.Space) == "" {
		return nil, fmt.Errorf("cli: work project id and space are required")
	}
	return &WorkCommand{deps: deps}, nil
}

// Name returns the command name.
func (c *WorkCommand) Name() string { return "work" }

// Synopsis returns a concise command description.
func (c *WorkCommand) Synopsis() string {
	return "report local and durable work: work <start|heartbeat|resume|checkpoint|wait|stop|status>"
}

// WorkSubcommands is the surface-enumeration SSOT used by lead-owned wiring.
func WorkSubcommands() []string {
	return []string{"start", "heartbeat", "resume", "checkpoint", "wait", "stop", "status"}
}

// Run dispatches the selected work subcommand.
func (c *WorkCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a work <start|heartbeat|resume|checkpoint|wait|stop|status> ...")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("work-loops"))
		return 2
	}
	if IsHelpArg(args[0]) {
		_, _ = fmt.Fprintln(stdio.Stdout, c.Synopsis())
		return 0
	}
	switch args[0] {
	case "start":
		return c.runStart(ctx, args[1:], stdio)
	case "heartbeat":
		return c.runHeartbeat(ctx, args[1:], stdio)
	case "resume":
		return c.runResume(ctx, args[1:], stdio)
	case "checkpoint":
		return c.runCheckpoint(ctx, args[1:], stdio)
	case "wait":
		return c.runWait(ctx, args[1:], stdio)
	case "stop":
		return c.runStop(ctx, args[1:], stdio)
	case "status":
		return c.runStatus(ctx, args[1:], stdio)
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "work: unknown subcommand %q\n", args[0])
		return 2
	}
}

func (c *WorkCommand) runStart(ctx context.Context, args []string, stdio IO) int {
	fs := newWorkFlagSet("work start", stdio)
	thread := fs.String("thread", "", "durable thread id")
	subject := fs.String("subject-ref", "", "thread/artifact/contract being worked on")
	mode := fs.String("mode", "", "planning|implementing|testing|reviewing")
	summary := fs.String("summary", "", "plain-text semantic progress summary")
	ttl := fs.Duration("ttl", 0, "machine-local lease ttl")
	validFor := fs.Duration("report-valid-for", 0, "durable report validity")
	recipients := fs.String("to", "", "comma-separated recipients")
	classification := fs.String("classification", "", "checkpoint classification")
	actorKind := fs.String("actor-kind", "", "explicit actor.kind override")
	actorName := fs.String("actor-name", "", "explicit actor.name override")
	actorModel := fs.String("actor-model", "", "explicit actor.model override")
	session := fs.String("session", "", "opaque provider session; generated when omitted")
	jsonOut := fs.Bool("json", false, "emit structured two-axis result")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*thread) == "" || strings.TrimSpace(*subject) == "" || strings.TrimSpace(*summary) == "" || !workActiveMode(*mode) {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a work start --thread <id> --subject-ref <ref> --mode <planning|implementing|testing|reviewing> --summary <text> [--ttl <duration>] [--report-valid-for <duration>] [--to <systems>] [--classification <class>] [--actor-* ...] [--session <id>] [--json]")
		return 2
	}
	actor, err := c.deps.ResolveActor(WorkActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel, Session: *session})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "work start: %v\n", err)
		return 1
	}
	result, callErr := c.deps.Starter.Start(ctx, workreport.StartInput{
		ProjectID: c.deps.ProjectID, Space: c.deps.Space, Thread: *thread, Actor: actor,
		SubjectRef: *subject, Mode: workreport.Mode(*mode), Summary: *summary,
		TTL: *ttl, ReportValidFor: *validFor, Recipients: workRecipients(*recipients), Classification: *classification,
	})
	return renderWorkOperation(stdio, "work start", result, callErr, *jsonOut)
}

func (c *WorkCommand) runHeartbeat(ctx context.Context, args []string, stdio IO) int {
	fs := newWorkFlagSet("work heartbeat", stdio)
	workID := fs.String("work-id", "", "work stream id; omit only for exact local selection")
	session := fs.String("session", "", "opaque session; omit only for exact local selection")
	ttl := fs.Duration("ttl", 0, "machine-local lease ttl")
	jsonOut := fs.Bool("json", false, "emit structured two-axis result")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a work heartbeat [--work-id <id>] [--session <id>] [--ttl <duration>] [--json]")
		return 2
	}
	identity, err := c.resolveWork(ctx, *workID, *session)
	if err != nil {
		return renderWorkOperation(stdio, "work heartbeat", workSelectionFailure(*workID, *session), err, *jsonOut)
	}
	result, callErr := c.deps.Local.Heartbeat(ctx, workreport.HeartbeatInput{Work: identity, TTL: *ttl})
	return renderWorkOperation(stdio, "work heartbeat", result, callErr, *jsonOut)
}

func (c *WorkCommand) runResume(ctx context.Context, args []string, stdio IO) int {
	fs := newWorkFlagSet("work resume", stdio)
	workID := fs.String("work-id", "", "work stream id; omit only for exact local selection")
	session := fs.String("session", "", "opaque session; omit only for exact local selection")
	jsonOut := fs.Bool("json", false, "emit structured two-axis result")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a work resume [--work-id <id>] [--session <id>] [--json]")
		return 2
	}
	identity, err := c.resolveWork(ctx, *workID, *session)
	if err != nil {
		return renderWorkOperation(stdio, "work resume", workSelectionFailure(*workID, *session), err, *jsonOut)
	}
	result, callErr := c.deps.Local.Resume(ctx, workreport.ResumeInput{Work: identity})
	return renderWorkOperation(stdio, "work resume", result, callErr, *jsonOut)
}

func (c *WorkCommand) runCheckpoint(ctx context.Context, args []string, stdio IO) int {
	fs := newWorkFlagSet("work checkpoint", stdio)
	workID, session := workSelectionFlags(fs)
	mode := fs.String("mode", "", "planning|implementing|testing|reviewing")
	summary := fs.String("summary", "", "plain-text semantic progress summary")
	subject := fs.String("subject-ref", "", "optional changed subject")
	ttl := fs.Duration("ttl", 0, "machine-local lease ttl")
	validFor := fs.Duration("report-valid-for", 0, "durable report validity")
	jsonOut := fs.Bool("json", false, "emit structured two-axis result")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*summary) == "" || !workActiveMode(*mode) {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a work checkpoint [--work-id <id>] [--session <id>] --mode <planning|implementing|testing|reviewing> --summary <text> [--subject-ref <ref>] [--ttl <duration>] [--report-valid-for <duration>] [--json]")
		return 2
	}
	identity, err := c.resolveWork(ctx, *workID, *session)
	if err != nil {
		return renderWorkOperation(stdio, "work checkpoint", workSelectionFailure(*workID, *session), err, *jsonOut)
	}
	result, callErr := c.deps.Progressor.Checkpoint(ctx, workreport.CheckpointInput{
		Work: identity, SubjectRef: *subject, Mode: workreport.Mode(*mode), Summary: *summary,
		TTL: *ttl, ReportValidFor: *validFor,
	})
	return renderWorkOperation(stdio, "work checkpoint", result, callErr, *jsonOut)
}

func (c *WorkCommand) runWait(ctx context.Context, args []string, stdio IO) int {
	fs := newWorkFlagSet("work wait", stdio)
	workID, session := workSelectionFlags(fs)
	summary := fs.String("summary", "", "plain-text reason for waiting")
	var waiting workWaitingFlags
	fs.Var(&waiting, "waiting-on", "typed dependency kind:id[:summary] (repeatable)")
	ttl := fs.Duration("ttl", 0, "machine-local lease ttl")
	validFor := fs.Duration("report-valid-for", 0, "durable report validity")
	jsonOut := fs.Bool("json", false, "emit structured two-axis result")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*summary) == "" || len(waiting.values) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a work wait [--work-id <id>] [--session <id>] --summary <text> --waiting-on <kind:id[:summary]>... [--ttl <duration>] [--report-valid-for <duration>] [--json]")
		return 2
	}
	identity, err := c.resolveWork(ctx, *workID, *session)
	if err != nil {
		return renderWorkOperation(stdio, "work wait", workSelectionFailure(*workID, *session), err, *jsonOut)
	}
	result, callErr := c.deps.Progressor.Wait(ctx, workreport.WaitInput{
		Work: identity, Summary: *summary, WaitingOn: waiting.values,
		TTL: *ttl, ReportValidFor: *validFor,
	})
	return renderWorkOperation(stdio, "work wait", result, callErr, *jsonOut)
}

func (c *WorkCommand) runStop(ctx context.Context, args []string, stdio IO) int {
	fs := newWorkFlagSet("work stop", stdio)
	workID, session := workSelectionFlags(fs)
	resultMode := fs.String("result", "", "finished|paused")
	summary := fs.String("summary", "", "plain-text terminal or pause summary")
	validFor := fs.Duration("report-valid-for", 0, "paused report validity; forbidden for finished")
	jsonOut := fs.Bool("json", false, "emit structured two-axis result")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*summary) == "" || (*resultMode != string(workreport.ModeFinished) && *resultMode != string(workreport.ModePaused)) {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a work stop [--work-id <id>] [--session <id>] --result <finished|paused> --summary <text> [--report-valid-for <duration>] [--json]")
		return 2
	}
	identity, err := c.resolveWork(ctx, *workID, *session)
	if err != nil {
		return renderWorkOperation(stdio, "work stop", workSelectionFailure(*workID, *session), err, *jsonOut)
	}
	result, callErr := c.deps.Progressor.Stop(ctx, workreport.StopInput{
		Work: identity, Result: workreport.Mode(*resultMode), Summary: *summary,
		ReportValidFor: *validFor,
	})
	return renderWorkOperation(stdio, "work stop", result, callErr, *jsonOut)
}

func (c *WorkCommand) runStatus(ctx context.Context, args []string, stdio IO) int {
	fs := newWorkFlagSet("work status", stdio)
	workID := fs.String("work-id", "", "optional work stream filter")
	includeExpired := fs.Bool("include-expired", false, "include locally expired leases")
	jsonOut := fs.Bool("json", false, "emit structured local status")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a work status [--work-id <id>] [--include-expired] [--json]")
		return 2
	}
	leases, err := c.deps.Reader.ListWork(ctx, c.deps.ProjectID, c.deps.Space, *workID, *includeExpired)
	output := workStatusFrom(leases)
	if *jsonOut {
		if encodeErr := json.NewEncoder(stdio.Stdout).Encode(output); encodeErr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "work status: %v\n", encodeErr)
			return 1
		}
	} else {
		renderWorkStatusHuman(stdio, output)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "work status: %v\n", err)
		return 1
	}
	return 0
}

func newWorkFlagSet(name string, stdio IO) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	return fs
}

func workSelectionFlags(fs *flag.FlagSet) (*string, *string) {
	return fs.String("work-id", "", "work stream id; omit only for exact local selection"),
		fs.String("session", "", "opaque session; omit only for exact local selection")
}

func workActiveMode(value string) bool {
	switch workreport.Mode(value) {
	case workreport.ModePlanning, workreport.ModeImplementing, workreport.ModeTesting, workreport.ModeReviewing:
		return true
	default:
		return false
	}
}

func workRecipients(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

type workWaitingFlags struct{ values []workreport.WaitingOn }

func (f *workWaitingFlags) String() string { return "" }

func (f *workWaitingFlags) Set(raw string) error {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return errors.New("waiting-on must be kind:id[:summary]")
	}
	kind := workreport.WaitKind(parts[0])
	if !kind.Valid() {
		return fmt.Errorf("waiting-on kind must be system|human|tool|timer|external")
	}
	wait := workreport.WaitingOn{Kind: kind, ID: parts[1]}
	if len(parts) == 3 {
		wait.Summary = parts[2]
	}
	f.values = append(f.values, wait)
	return nil
}

type workOperationOutput struct {
	WorkID           string            `json:"work_id,omitempty"`
	Session          string            `json:"session,omitempty"`
	OperationKey     string            `json:"operation_key,omitempty"`
	Action           workreport.Action `json:"action,omitempty"`
	SemanticSequence uint64            `json:"semantic_sequence,omitempty"`
	Local            workLocalOutput   `json:"local"`
	Shared           workSharedOutput  `json:"shared"`
}

type workLocalOutput struct {
	State     workreport.LocalState `json:"state"`
	ErrorCode string                `json:"error_code,omitempty"`
	ExpiresAt *time.Time            `json:"expires_at,omitempty"`
}

type workSharedOutput struct {
	Attempted   bool                   `json:"attempted"`
	WriteResult json.RawMessage        `json:"write_result,omitempty"`
	Convergence workreport.Convergence `json:"convergence,omitempty"`
	ErrorCode   string                 `json:"error_code,omitempty"`
}

func workOperationFrom(result workreport.OperationResult) workOperationOutput {
	local := workLocalOutput{State: result.LocalState, ErrorCode: result.LocalErrorCode}
	if !result.ExpiresAt.IsZero() {
		expires := result.ExpiresAt.UTC()
		local.ExpiresAt = &expires
	}
	shared := workSharedOutput{
		Attempted: result.Shared.Attempted, Convergence: result.Shared.Convergence, ErrorCode: result.Shared.ErrorCode,
	}
	if raw := result.Shared.WriteResult.Bytes(); len(raw) > 0 {
		shared.WriteResult = json.RawMessage(raw)
	}
	return workOperationOutput{
		WorkID: result.WorkID, Session: result.Session, OperationKey: result.OperationKey, Action: result.Action,
		SemanticSequence: result.SemanticSequence, Local: local, Shared: shared,
	}
}

func workSelectionFailure(workID, session string) workreport.OperationResult {
	return workreport.OperationResult{
		WorkID: workID, Session: workreport.NormalizeSession(session), LocalState: workreport.LocalUnchanged,
	}
}

func (c *WorkCommand) resolveWork(ctx context.Context, workID, session string) (workreport.WorkIdentityInput, error) {
	if (workID == "") != (session == "") {
		return workreport.WorkIdentityInput{}, workreport.ErrLeaseNotOwned
	}
	return c.deps.Reader.ResolveWork(ctx, c.deps.ProjectID, c.deps.Space, workID, session)
}

func renderWorkOperation(stdio IO, label string, result workreport.OperationResult, callErr error, jsonOut bool) int {
	output := workOperationFrom(result)
	var renderErr error
	if jsonOut {
		renderErr = json.NewEncoder(stdio.Stdout).Encode(output)
	} else {
		_, renderErr = fmt.Fprintf(stdio.Stdout, "work_id=%s session=%s operation_key=%s action=%s semantic_sequence=%d\nlocal state=%s error_code=%s",
			output.WorkID, output.Session, output.OperationKey, output.Action, output.SemanticSequence, output.Local.State, output.Local.ErrorCode)
		if renderErr == nil && output.Local.ExpiresAt != nil {
			_, renderErr = fmt.Fprintf(stdio.Stdout, " expires_at=%s", output.Local.ExpiresAt.Format(time.RFC3339))
		}
		if renderErr == nil {
			_, renderErr = fmt.Fprintln(stdio.Stdout)
		}
		if renderErr == nil {
			if !output.Shared.Attempted {
				_, renderErr = fmt.Fprintln(stdio.Stdout, "shared attempted=false")
			} else {
				_, renderErr = fmt.Fprintf(stdio.Stdout, "shared attempted=true convergence=%s error_code=%s write_result=%s\n",
					output.Shared.Convergence, output.Shared.ErrorCode, output.Shared.WriteResult)
			}
		}
	}
	if renderErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: render result: %v\n", label, renderErr)
		return 1
	}
	if callErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", label, callErr)
		return 1
	}
	return 0
}

type workStatusOutput struct {
	Items []workStatusItem `json:"items"`
}

type workStatusItem struct {
	WorkID            string                 `json:"work_id"`
	Session           string                 `json:"session"`
	Thread            string                 `json:"thread"`
	SubjectRef        string                 `json:"subject_ref"`
	Actor             workreport.Actor       `json:"actor"`
	Mode              workreport.Mode        `json:"mode"`
	Summary           string                 `json:"summary"`
	WaitingOn         []workreport.WaitingOn `json:"waiting_on"`
	StartedAt         time.Time              `json:"started_at"`
	RenewedAt         time.Time              `json:"renewed_at"`
	ExpiresAt         time.Time              `json:"expires_at"`
	HeartbeatSequence uint64                 `json:"heartbeat_sequence"`
	SemanticSequence  uint64                 `json:"semantic_sequence"`
	Closing           bool                   `json:"closing"`
	Pending           *workPendingStatus     `json:"pending,omitempty"`
}

type workPendingStatus struct {
	OperationKey     string            `json:"operation_key"`
	Action           workreport.Action `json:"action"`
	SemanticSequence uint64            `json:"semantic_sequence"`
	Shared           workStatusShared  `json:"shared"`
	LastErrorCode    string            `json:"last_error_code,omitempty"`
}

type workStatusShared struct {
	Attempted   bool                   `json:"attempted"`
	Convergence workreport.Convergence `json:"convergence,omitempty"`
	ErrorCode   string                 `json:"error_code,omitempty"`
}

func workStatusFrom(leases []workreport.Lease) workStatusOutput {
	items := make([]workStatusItem, 0, len(leases))
	for _, lease := range leases {
		item := workStatusItem{
			WorkID: lease.Identity.WorkID, Session: lease.Identity.Actor.Session, Thread: lease.Identity.Thread,
			SubjectRef: lease.SubjectRef, Actor: lease.Identity.Actor, Mode: lease.Mode, Summary: lease.Summary,
			WaitingOn: append([]workreport.WaitingOn(nil), lease.WaitingOn...), StartedAt: lease.StartedAt.UTC(),
			RenewedAt: lease.RenewedAt.UTC(), ExpiresAt: lease.ExpiresAt.UTC(), HeartbeatSequence: lease.HeartbeatSequence,
			SemanticSequence: lease.SemanticSequence, Closing: lease.Closing,
		}
		if item.WaitingOn == nil {
			item.WaitingOn = []workreport.WaitingOn{}
		}
		if lease.Pending != nil {
			item.Pending = &workPendingStatus{
				OperationKey: lease.Pending.OperationKey, Action: lease.Pending.Action,
				SemanticSequence: lease.Pending.SemanticSequence, LastErrorCode: lease.Pending.LastErrorCode,
				Shared: workStatusShared{
					Attempted: lease.Pending.Shared.Attempted, Convergence: lease.Pending.Shared.Convergence,
					ErrorCode: lease.Pending.Shared.ErrorCode,
				},
			}
		}
		items = append(items, item)
	}
	return workStatusOutput{Items: items}
}

func renderWorkStatusHuman(stdio IO, output workStatusOutput) {
	if len(output.Items) == 0 {
		_, _ = fmt.Fprintln(stdio.Stdout, "work status: no local reports; current activity is unknown")
		return
	}
	for _, item := range output.Items {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s %s mode=%s thread=%s expires_at=%s closing=%t summary=%s\n",
			item.WorkID, item.Session, item.Mode, item.Thread, item.ExpiresAt.Format(time.RFC3339), item.Closing, item.Summary)
	}
}

var _ Command = (*WorkCommand)(nil)
