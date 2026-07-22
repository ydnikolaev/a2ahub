package mcp

// This file is internal/mcp's own projection of the FROZEN schemas/event/v1
// document shape (D-030) — built to match internal/cli's cmd_lifecycle.go
// (lifecycleEventDoc, ~L149-196) and cmd_submit.go (submitEventDoc)
// field-for-field, WITHOUT importing them (plan 14 Placement decisions,
// binding). The per-write-verb equivalence suite in cmd/a2a is the
// anti-drift gate that proves this struct's yaml.Marshal output is
// byte-identical to the CLI's own, modulo the event/artifact id.

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"gopkg.in/yaml.v3"
)

// eventDoc is this package's own event/v1 document decode+encode struct —
// field order and yaml tags mirror internal/cli's lifecycleEventDoc
// exactly (the equivalence suite's byte-identity depends on it).
type eventDoc struct {
	Schema     string     `yaml:"schema"`
	Event      string     `yaml:"event"`
	Space      string     `yaml:"space"`
	Subject    string     `yaml:"subject"`
	Transition string     `yaml:"transition"`
	State      string     `yaml:"state,omitempty"`
	Actor      eventActor `yaml:"actor"`
	At         string     `yaml:"at"`
	Note       string     `yaml:"note,omitempty"`
	Refs       []refEntry `yaml:"refs,omitempty"`
	ReasonCode string     `yaml:"reason_code,omitempty"`
	Version    string     `yaml:"version,omitempty"`
	Commit     string     `yaml:"commit,omitempty"`
	Digest     string     `yaml:"digest,omitempty"`
}

type eventActor struct {
	Kind   string `yaml:"kind"`
	Name   string `yaml:"name"`
	System string `yaml:"system"`
}

type refEntry struct {
	Ref string `yaml:"ref"`
}

// envelopeProbe is this package's own minimal envelope decode (mirrors
// internal/cli's lifecycleEnvelopeProbe).
type envelopeProbe struct {
	ID                string   `yaml:"id"`
	Space             string   `yaml:"space"`
	From              string   `yaml:"from"`
	To                any      `yaml:"to"`
	RequiredApprovers []string `yaml:"required_approvers"`
	Parent            string   `yaml:"parent"`
}

// prefixInfo maps a §3.3 id prefix to its fold.Kind.
var prefixInfo = map[string]fold.Kind{
	"XC": fold.KindContract,
	"XR": fold.KindRequirement,
	"XQ": fold.KindQuestion,
	"XW": fold.KindWorkRequest,
	"XD": fold.KindDecision,
	"XH": fold.KindHandoff,
	"XS": fold.KindResponse,
	"XA": fold.KindAnnouncement,
}

// artifactPath resolves parsed's committed space-relative path per §4.2's
// layout (mirrors internal/cli's lifecycleArtifactPath).
func artifactPath(parsed artifact.ID) (string, error) {
	switch parsed.Prefix {
	case "XC":
		layout, err := space.NewLayout(parsed.System)
		if err != nil {
			return "", err
		}
		return layout.ProvidesContract(parsed.Slug), nil
	case "XR":
		layout, err := space.NewLayout(parsed.System)
		if err != nil {
			return "", err
		}
		return layout.Requires(parsed.Raw), nil
	case "XD":
		return space.Decision(parsed.Raw), nil
	case "XQ", "XW", "XH", "XA", "XS":
		layout, err := space.NewLayout(parsed.System)
		if err != nil {
			return "", err
		}
		return layout.Exchange(parsed.Raw), nil
	default:
		return "", fmt.Errorf("mcp: unknown artifact id prefix %q", parsed.Prefix)
	}
}

// loadEnvelope reads and parses id's committed artifact file from
// mirrorDir.
func loadEnvelope(mirrorDir, id string) (fold.Envelope, envelopeProbe, error) {
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return fold.Envelope{}, envelopeProbe{}, fmt.Errorf("mcp: %s: %w", id, err)
	}
	kind, ok := prefixInfo[parsed.Prefix]
	if !ok {
		return fold.Envelope{}, envelopeProbe{}, fmt.Errorf("mcp: %s: unknown artifact id prefix %q", id, parsed.Prefix)
	}
	relPath, err := artifactPath(parsed)
	if err != nil {
		return fold.Envelope{}, envelopeProbe{}, err
	}
	raw, err := readBoundedFile(filepath.Join(mirrorDir, relPath), maxMirrorEventBytes)
	if err != nil {
		return fold.Envelope{}, envelopeProbe{}, fmt.Errorf("mcp: cannot read %s: %w", id, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return fold.Envelope{}, envelopeProbe{}, fmt.Errorf("mcp: %s: %w", id, err)
	}
	var probe envelopeProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return fold.Envelope{}, envelopeProbe{}, fmt.Errorf("mcp: %s: cannot decode envelope: %w", id, err)
	}
	env := fold.Envelope{
		ID: id, Kind: kind, From: probe.From,
		To: toStringSlice(probe.To), RequiredApprovers: probe.RequiredApprovers,
	}
	return env, probe, nil
}

// readAllEvents walks every <system>/events/<year>/<ulid>.yaml under
// mirrorDir — every participating system's own section.
func readAllEvents(mirrorDir string) ([]eventDoc, error) {
	matches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "events", "*", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("mcp: list committed events: %w", err)
	}
	out := make([]eventDoc, 0, len(matches))
	for _, m := range matches {
		raw, err := readBoundedFile(m, maxMirrorEventBytes)
		if err != nil {
			return nil, err
		}
		var ev eventDoc
		if err := yaml.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("mcp: decode committed event %s: %w", m, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

// foldEvents selects, from every committed event, the ones relevant to
// primaryID's own fold (mirrors internal/cli's lifecycleFoldEvents).
func foldEvents(all []eventDoc, primaryID string) []fold.Event {
	responseIDs := map[string]bool{}
	for _, ev := range all {
		if ev.Subject == primaryID && ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			responseIDs[ev.Refs[0].Ref] = true
		}
	}
	var out []fold.Event
	for _, ev := range all {
		if ev.Subject != primaryID && !responseIDs[ev.Subject] {
			continue
		}
		fe := fold.Event{
			ULID: ev.Event, Subject: ev.Subject, Transition: ev.Transition,
			ClaimedState: fold.State(ev.State),
			Actor:        fold.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
		}
		if ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			fe.ResponseID = ev.Refs[0].Ref
		}
		out = append(out, fe)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ULID < out[j].ULID })
	return out
}

// membership adapts a space.Manifest into a fold.MembershipView.
func membership(manifest space.Manifest) fold.MembershipView {
	return func(system string) fold.MembershipStatus {
		for _, p := range manifest.Participants {
			if p.System == system {
				if p.Status == "left" {
					return fold.MembershipLeft
				}
				return fold.MembershipMember
			}
		}
		return fold.MembershipUnknown
	}
}

// checkLegality is the generic (non-response-scoped) pre-write legality
// check every write tool except verify/dispute uses.
func checkLegality(mirrorDir string, manifest space.Manifest, id, transition string, actor fold.Actor) (fold.Verdict, fold.Envelope, error) {
	env, _, err := loadEnvelope(mirrorDir, id)
	if err != nil {
		return "", fold.Envelope{}, err
	}
	all, err := readAllEvents(mirrorDir)
	if err != nil {
		return "", env, err
	}
	events := foldEvents(all, id)
	memb := membership(manifest)

	var state fold.State
	if len(events) == 0 {
		state = fold.NewResult(env.Kind).State
	} else {
		state = fold.Fold(env.Kind, env, events, memb).State
	}
	actorStatus := memb(actor.System)
	return fold.CheckLegality(env.Kind, state, transition, env, actor, actorStatus), env, nil
}

// checkResponseLegality is the verify/dispute pre-write legality check.
func checkResponseLegality(mirrorDir string, manifest space.Manifest, responseID, transition string, actor fold.Actor) (fold.Verdict, fold.Envelope, string, fold.Result, error) {
	_, responseProbe, err := loadEnvelope(mirrorDir, responseID)
	if err != nil {
		return "", fold.Envelope{}, "", fold.Result{}, err
	}
	parentID := responseProbe.Parent
	if parentID == "" {
		return "", fold.Envelope{}, "", fold.Result{}, fmt.Errorf("mcp: response %s carries no `parent` link", responseID)
	}
	parentEnv, _, err := loadEnvelope(mirrorDir, parentID)
	if err != nil {
		return "", fold.Envelope{}, "", fold.Result{}, err
	}
	all, err := readAllEvents(mirrorDir)
	if err != nil {
		return "", parentEnv, parentID, fold.Result{}, err
	}
	events := foldEvents(all, parentID)
	memb := membership(manifest)
	result := fold.Fold(parentEnv.Kind, parentEnv, events, memb)

	substate := fold.State("")
	if result.Responses != nil {
		substate = result.Responses[responseID]
	}
	actorStatus := memb(actor.System)
	verdict := fold.CheckLegality(fold.KindResponse, substate, transition, parentEnv, actor, actorStatus)
	return verdict, parentEnv, parentID, result, nil
}

// resolveResponseID resolves verify's own `<response-id|parent-id>`
// ambiguity.
func resolveResponseID(mirrorDir string, target, refsHint string) (string, error) {
	parsed, err := artifact.ParseID(target)
	if err == nil && parsed.Prefix == "XS" {
		return target, nil
	}
	if refsHint != "" {
		return refsHint, nil
	}
	all, err := readAllEvents(mirrorDir)
	if err != nil {
		return "", err
	}
	var candidates []string
	seen := map[string]bool{}
	for _, ev := range all {
		if ev.Subject == target && ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			rid := ev.Refs[0].Ref
			if !seen[rid] {
				seen[rid] = true
				candidates = append(candidates, rid)
			}
		}
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("mcp: %s has no attached response", target)
	case 1:
		return candidates[0], nil
	default:
		sort.Strings(candidates)
		return "", fmt.Errorf("mcp: %s has multiple responses (%s) — disambiguate with refs", target, strings.Join(candidates, ", "))
	}
}

// verdictError renders a registry-code-carrying refusal error for a
// non-legal fold.Verdict (mirrors internal/cli's verdictRefusalMessage).
func verdictError(id string, verdict fold.Verdict) error {
	switch verdict {
	case fold.VerdictIllegalTransition:
		return fmt.Errorf("%s: refused: illegal transition for the current folded state (LFC-001)", id)
	case fold.VerdictUnauthorizedActor:
		return fmt.Errorf("%s: refused: actor is not authorized for this transition (LFC-002)", id)
	default:
		return fmt.Errorf("%s: refused: unknown verdict %v", id, verdict)
	}
}

// --- shared write-verb plumbing (constructor DI) -------------------------

// Funnel is the consumer-side seam over *space.WriteFunnel (rails ISP/DI,
// mirrors internal/cli's lifecycleFunnel/submitFunnel) — tests inject a
// spy that records the SubmitRequest for the cmd/a2a equivalence suite.
type Funnel interface {
	Submit(ctx context.Context, req space.SubmitRequest) (space.WriteResult, error)
}

// SubmitHostConfig carries the write funnel's per-space host-facing
// config a SubmitRequest needs beyond the artifact content itself —
// mirrors internal/cli's SubmitHostConfig field-for-field.
type SubmitHostConfig struct {
	RemoteURL         string
	Repo              host.Repo
	BaseBranch        string
	Credential        host.Credential
	CommitAuthorName  string
	CommitAuthorEmail string
}

// WriteDeps is the common constructor-injected dependency set every write
// tool handler needs (mirrors internal/cli's lifecycleDeps).
type WriteDeps struct {
	Funnel       Funnel
	MirrorDir    string
	SpaceID      string
	OwnSystem    string
	Manifest     space.Manifest
	HostCfg      SubmitHostConfig
	ResolveActor func(ActorInput) template.Actor

	Now      func() time.Time
	Entropy  io.Reader
	ReadFile func(path string) ([]byte, error)
}

// buildRequest assembles a space.SubmitRequest for a batch of ids +
// files, mirroring internal/cli's lifecycleDeps.buildRequest exactly
// (commit message convention, branch/PR body shape).
func (d WriteDeps) buildRequest(ids []string, files []space.FileWrite, verb string, gated bool) space.SubmitRequest {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	commitMsg := fmt.Sprintf("a2a(%s): %s", verb, strings.Join(sorted, ", "))
	baseBranch := d.HostCfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	var prBody string
	if gated {
		prBody = fmt.Sprintf("ADVISORY GATE: %s requires an approving CODEOWNERS review before auto-merge (§3.7 G3).", verb)
	}
	return space.SubmitRequest{
		RepoDir: d.MirrorDir, System: d.OwnSystem,
		ArtifactID: strings.Join(sorted, "+"), Files: files,
		CommitMessage: commitMsg, CommitAuthorName: d.HostCfg.CommitAuthorName, CommitAuthorEmail: d.HostCfg.CommitAuthorEmail,
		RemoteURL: d.HostCfg.RemoteURL, Repo: d.HostCfg.Repo, BaseBranch: baseBranch,
		PRTitle: commitMsg, PRBody: prBody, Credential: d.HostCfg.Credential,
	}
}

// submitResult is the structured StructuredContent shape every write
// tool's handler returns on success — the §7.7 "structured returns"
// contract for the write side.
type submitResult struct {
	Verb   string   `json:"verb"`
	IDs    []string `json:"ids"`
	Branch string   `json:"branch"`
	PRURL  string   `json:"pr_url"`
	State  string   `json:"state"`
}

// submit runs req through d.Funnel and shapes the result/error the same
// way every write tool handler needs.
func (d WriteDeps) submit(ctx context.Context, req space.SubmitRequest, verb string, ids []string) (any, error) {
	result, err := d.Funnel.Submit(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", verb, err)
	}
	return submitResult{
		Verb: verb, IDs: ids, Branch: result.Branch, PRURL: result.PRURL, State: string(result.State),
	}, nil
}
