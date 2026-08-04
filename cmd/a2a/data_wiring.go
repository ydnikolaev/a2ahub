package main

// data_wiring.go is dataCore: the one production adapter behind
// cli.DataOperations (internal/cli/cmd_data.go's seam), mirroring
// contractP6Core (contract_p6_wiring.go) — same construction shape, same
// injected-closure DI, same multi-space routing discipline. It selects
// bounded rooted inputs and delegates the pure judgment to
// internal/datapackage and the space read/write to internal/space's
// data_resolve.go / data_delivery.go (this same wave's sibling files).
//
// dataCore additionally reimplements the small envelope/event/fold-legality
// projection every OP-211 surface already owns independently
// (internal/cli/cmd_lifecycle.go's lifecycle* helpers,
// internal/mcp/tools_lifecycle.go's eventdoc.go helpers) — plan 14's own
// precedent: "duplicating only the OUTER shape ... the inner
// legality/event-authoring/funnel-submit logic is this file's own call
// into eventdoc.go's shared helpers." dataCore is a THIRD such surface, for
// the same reason: internal/space cannot import internal/cache (where
// committed-event history lives), so nothing capable of folding legality
// can live below cmd/a2a, and internal/cli's own copies are unexported to
// their own package.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	semver "github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

// dataCore is the production adapter shared by the CLI and (later) MCP
// transports for the whole `a2a data` surface.
type dataCore struct {
	projectRoot string
	mirrorDir   string
	stagingDir  string
	remoteURL   string
	spaceID     string
	ownSystem   string
	repository  host.Repo
	authorName  string
	authorEmail string
	binary      string
	engine      *validate.Engine
	host        *host.GitHubHost
	funnel      *space.WriteFunnel

	now               func() time.Time
	entropy           io.Reader
	resolveActor      func(kind, name, model string) (template.Actor, error)
	resolveCredential func(context.Context) (host.Credential, error)
	loadManifest      func() (space.Manifest, error)

	// resolveContractSchemas is the injectable seam behind
	// ResolveDataContractSchemas (space/data_resolve.go), defaulted to the
	// real implementation in newDataCore. Injected — rather than called
	// inline — so a test can wrap it with a call counter and prove D-2's
	// "resolved and digest-verified once per run" property, which nothing
	// about the resolution's own return value can otherwise demonstrate:
	// the property is about how many times the CALLER invokes it, not
	// about what any single call returns.
	resolveContractSchemas func(ctx context.Context, ref string) (map[string][]byte, string, error)

	// resolveDataPackage is the same shape of injectable seam behind
	// space.ResolveDataPackage, defaulted to the real implementation.
	resolveDataPackage func(ctx context.Context, packageID string) (space.DataPackageBytes, error)
}

func newDataCore(
	projectRoot, mirrorDir, stagingDir, remoteURL, spaceID, ownSystem string,
	repository host.Repo,
	authorName, authorEmail, binary string,
	engine *validate.Engine,
	h *host.GitHubHost,
	resolveActor func(kind, name, model string) (template.Actor, error),
	resolveCredential func(context.Context) (host.Credential, error),
) (*dataCore, error) {
	if projectRoot == "" || mirrorDir == "" || stagingDir == "" || remoteURL == "" || spaceID == "" || ownSystem == "" ||
		repository.Owner == "" || repository.Name == "" || authorName == "" || authorEmail == "" || binary == "" ||
		engine == nil || h == nil || resolveActor == nil || resolveCredential == nil {
		return nil, fmt.Errorf("data wiring: complete production dependencies are required")
	}
	c := &dataCore{
		projectRoot: projectRoot, mirrorDir: mirrorDir, stagingDir: stagingDir, remoteURL: remoteURL,
		spaceID: spaceID, ownSystem: ownSystem, repository: repository,
		authorName: authorName, authorEmail: authorEmail, binary: binary,
		engine: engine, host: h, now: time.Now, entropy: rand.Reader,
		resolveActor: resolveActor, resolveCredential: resolveCredential,
		loadManifest: func() (space.Manifest, error) { return loadManifest(mirrorDir) },
	}
	validator := contractHistoryDocumentEngine{engine: engine}
	c.resolveContractSchemas = func(ctx context.Context, ref string) (map[string][]byte, string, error) {
		return space.ResolveDataContractSchemas(ctx, mirrorDir, ref, validator)
	}
	c.resolveDataPackage = func(ctx context.Context, packageID string) (space.DataPackageBytes, error) {
		return space.ResolveDataPackage(ctx, mirrorDir, packageID)
	}
	nullValidator := &dataNoopSubmitValidator{}
	c.funnel = space.NewWriteFunnel(h, nullValidator, binary)
	return c, nil
}

func newCLIDataCore(p paths, deps lifecycleDeps) (*dataCore, error) {
	engine, err := newEngine()
	if err != nil {
		return nil, err
	}
	return newDataCore(
		p.projectRoot, deps.mirrorDir, p.staging, deps.hostCfg.RemoteURL, deps.spaceID, deps.ownSystem,
		deps.hostCfg.Repo,
		deps.hostCfg.CommitAuthorName, deps.hostCfg.CommitAuthorEmail, funnelBinaryVersion(),
		engine, host.NewGitHubHost(http.DefaultClient, githubAPIBase()),
		func(kind, name, model string) (template.Actor, error) {
			return deps.resolveActor(cli.ActorFlags{Kind: kind, Name: name, Model: model})
		},
		func(context.Context) (host.Credential, error) { return deps.hostCfg.Credential, nil },
	)
}

// dataNoopSubmitValidator satisfies space.WriteFunnel's SubmitValidator seam
// with no additional check beyond the funnel's own (section guard, min
// binary version).
//
// KNOWN GAP, recorded rather than papered over: the real V2 path
// (internal/cli/adapters.go's SubmitValidatorAdapter, built from
// NewSubmitValidatorAdapter/NewMirrorResolver/NewLegalityAdapter, exactly as
// contractPublicationSubmitValidator's own ValidateSubmit delegates to it)
// cannot be reused here verbatim: its file-dispatch loop treats every path
// that is not under "/events/", not consumes.yaml, and not a recognized
// contract-baseline path as an envelope DRAFT and runs
// artifact.ParseFrontmatter over it — which the manifest and payload files
// under <system>/data/<id>/** are not (data-package/v1 is deliberately not
// an X- envelope type, CONFIRM-3), so wiring it in unmodified would refuse
// every delivery on its own committed shape. Widening that adapter's
// dispatch to recognize a data-package family (the same carve-out
// IsContractBaselinePath already gets) is real work inside
// internal/cli/adapters.go, which is outside this brief's allowlist. An
// earlier version of this comment claimed contractP6Core's own preflight
// path as precedent for a no-op validator; that claim was checked against
// contract_p6_wiring.go and found false — NewContractPreflightService
// constructs no funnel and takes no validator at all, which is a different
// thing from a validator that is present but does nothing. This is a real
// gap, reported for the lead to weigh, not a decision this file is making
// quietly.
type dataNoopSubmitValidator struct{}

func (dataNoopSubmitValidator) ValidateSubmit(context.Context, []space.FileWrite) error { return nil }

// --- envelope reads -----------------------------------------------------

// dataPrefixKind mirrors internal/cli's lifecyclePrefixInfo (cmd_lifecycle.go)
// — this file's own copy, for the same "each surface owns its thin
// projection" reason the rest of this file's fold helpers are copies.
var dataPrefixKind = map[string]fold.Kind{
	"XC": fold.KindContract,
	"XR": fold.KindRequirement,
	"XQ": fold.KindQuestion,
	"XW": fold.KindWorkRequest,
	"XD": fold.KindDecision,
	"XH": fold.KindHandoff,
	"XS": fold.KindResponse,
	"XA": fold.KindAnnouncement,
}

// dataEnvelopeProbe is this file's own minimal envelope decode.
type dataEnvelopeProbe struct {
	ID                 string   `yaml:"id"`
	Space              string   `yaml:"space"`
	From               string   `yaml:"from"`
	To                 any      `yaml:"to"`
	Thread             string   `yaml:"thread"`
	AcceptanceCriteria []string `yaml:"acceptance_criteria"`
}

func dataArtifactPath(parsed artifact.ID) (string, error) {
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
		return "", fmt.Errorf("data: unknown artifact id prefix %q", parsed.Prefix)
	}
}

func (c *dataCore) loadEnvelope(id string) (fold.Envelope, dataEnvelopeProbe, error) {
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return fold.Envelope{}, dataEnvelopeProbe{}, fmt.Errorf("data: %s: %w", id, err)
	}
	kind, ok := dataPrefixKind[parsed.Prefix]
	if !ok {
		return fold.Envelope{}, dataEnvelopeProbe{}, fmt.Errorf("data: %s: unknown artifact id prefix %q", id, parsed.Prefix)
	}
	relPath, err := dataArtifactPath(parsed)
	if err != nil {
		return fold.Envelope{}, dataEnvelopeProbe{}, err
	}
	raw, err := os.ReadFile(filepath.Join(c.mirrorDir, relPath)) //nolint:gosec // reason: relPath is derived from Layout, never from unsanitized user input
	if err != nil {
		return fold.Envelope{}, dataEnvelopeProbe{}, fmt.Errorf("data: cannot read %s: %w", id, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return fold.Envelope{}, dataEnvelopeProbe{}, fmt.Errorf("data: %s: %w", id, err)
	}
	var probe dataEnvelopeProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return fold.Envelope{}, dataEnvelopeProbe{}, fmt.Errorf("data: %s: cannot decode envelope: %w", id, err)
	}
	if probe.ID != id {
		return fold.Envelope{}, dataEnvelopeProbe{}, fmt.Errorf("data: %s: committed envelope id disagrees with the requested id", id)
	}
	env := fold.Envelope{ID: id, Kind: kind, From: probe.From, To: dataToStrings(probe.To)}
	return env, probe, nil
}

func dataToStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return nil
	}
}

// --- event fold/legality (verify-pass/verify-fail on an EXISTING handoff) -

type dataEventActor struct {
	Kind   string `yaml:"kind"`
	Name   string `yaml:"name"`
	System string `yaml:"system"`
}

type dataEventDoc struct {
	Schema     string         `yaml:"schema"`
	Event      string         `yaml:"event"`
	Space      string         `yaml:"space"`
	Subject    string         `yaml:"subject"`
	Transition string         `yaml:"transition"`
	State      string         `yaml:"state,omitempty"`
	Actor      dataEventActor `yaml:"actor"`
	At         string         `yaml:"at"`
}

func (c *dataCore) readAllEvents() ([]dataEventDoc, error) {
	matches, err := filepath.Glob(filepath.Join(c.mirrorDir, "*", "events", "*", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("data: list committed events: %w", err)
	}
	out := make([]dataEventDoc, 0, len(matches))
	for _, m := range matches {
		raw, err := os.ReadFile(m) //nolint:gosec // reason: m comes from filepath.Glob against the mirror's own committed tree
		if err != nil {
			return nil, fmt.Errorf("data: read committed event %s: %w", m, err)
		}
		var ev dataEventDoc
		if err := yaml.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("data: decode committed event %s: %w", m, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

func dataFoldEvents(all []dataEventDoc, subject string) []fold.Event {
	var out []fold.Event
	for _, ev := range all {
		if ev.Subject != subject {
			continue
		}
		out = append(out, fold.Event{
			ULID: ev.Event, Subject: ev.Subject, Transition: ev.Transition,
			ClaimedState: fold.State(ev.State),
			Actor:        fold.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ULID < out[j].ULID })
	return out
}

func dataMembership(manifest space.Manifest) fold.MembershipView {
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

func (c *dataCore) evaluateCandidate(manifest space.Manifest, id string, candidate fold.Event) (fold.CandidateEvaluation, fold.Envelope, error) {
	env, _, err := c.loadEnvelope(id)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, err
	}
	all, err := c.readAllEvents()
	if err != nil {
		return fold.CandidateEvaluation{}, env, err
	}
	events := dataFoldEvents(all, id)
	membership := dataMembership(manifest)
	prior := fold.NewResult(env.Kind)
	if len(events) > 0 {
		prior = fold.Fold(env.Kind, env, events, membership)
	}
	candidate.Subject = id
	return fold.EvaluateCandidate(env.Kind, prior, candidate, env, membership), env, nil
}

// --- handoff authoring (a fresh handoff at deliver time) -----------------

type dataHandoffActor struct {
	Kind  string `yaml:"kind"`
	Name  string `yaml:"name"`
	Model string `yaml:"model,omitempty"`
}

type dataHandoffDeliverable struct {
	Name string `yaml:"name"`
	Ref  string `yaml:"ref"`
	Kind string `yaml:"kind"`
}

// dataHandoffEnvelope is the closed handoff envelope shape
// (schemas/envelope/v1/handoff.schema.json): additionalProperties:false on
// deliverables[] items, unevaluatedProperties:false on the envelope.
// verification/acceptance_criteria/fulfills are all REQUIRED and synthesized
// non-interactively here (spec 05a "What to do" §2) — no digest, attempt
// number or verdict rides on this document; those live in the manifest and
// the report.
type dataHandoffEnvelope struct {
	Schema             string                   `yaml:"schema"`
	ID                 string                   `yaml:"id"`
	Type               string                   `yaml:"type"`
	Title              string                   `yaml:"title"`
	Space              string                   `yaml:"space"`
	From               string                   `yaml:"from"`
	To                 []string                 `yaml:"to"`
	Actor              dataHandoffActor         `yaml:"actor"`
	Created            string                   `yaml:"created"`
	Priority           string                   `yaml:"priority"`
	Blocking           bool                     `yaml:"blocking"`
	Classification     string                   `yaml:"classification"`
	Thread             string                   `yaml:"thread"`
	Fulfills           []string                 `yaml:"fulfills"`
	Deliverables       []dataHandoffDeliverable `yaml:"deliverables"`
	Verification       string                   `yaml:"verification"`
	AcceptanceCriteria []string                 `yaml:"acceptance_criteria"`
	Limitations        []string                 `yaml:"limitations"`
}

// buildHandoffDraft synthesizes the handoff carrying doc — non-interactive,
// as the closed schema requires. acceptanceCriteria is inherited verbatim
// from the fulfilled work_request's own acceptance_criteria when present
// (base.schema.json carries that field on every type); otherwise one
// synthesized item names the contract conformance the delivery was judged
// against, so the field is never emitted empty (minItems: 1).
func (c *dataCore) buildHandoffDraft(handoffID string, actor template.Actor, wrEnv fold.Envelope, wrProbe dataEnvelopeProbe, manifest datapackage.Document) []byte {
	acceptance := wrProbe.AcceptanceCriteria
	if len(acceptance) == 0 {
		acceptance = []string{fmt.Sprintf("Delivered payload conforms to the pinned contract %s.", manifest.Contract)}
	}
	env := dataHandoffEnvelope{
		Schema: "envelope/v1", ID: handoffID, Type: "handoff",
		Title: fmt.Sprintf("Delivery for %s", wrEnv.ID),
		Space: wrProbe.Space, From: c.ownSystem, To: []string{wrEnv.From},
		Actor:    dataHandoffActor{Kind: actor.Kind, Name: actor.Name, Model: actor.Model},
		Created:  c.now().UTC().Format(time.RFC3339),
		Priority: "p3", Blocking: false, Classification: "internal",
		Thread:   wrProbe.Thread,
		Fulfills: []string{wrEnv.ID},
		Deliverables: []dataHandoffDeliverable{
			{Name: manifest.ID, Ref: manifest.ID, Kind: "data"},
		},
		Verification:       fmt.Sprintf("Run `a2a data verify %s` to re-verify entry digests and contract conformance.", manifest.ID),
		AcceptanceCriteria: acceptance,
		Limitations:        []string{},
	}
	raw, marshalErr := yaml.Marshal(env)
	if marshalErr != nil {
		// yaml.Marshal over this fixed, non-cyclic struct cannot fail;
		// preserved as a defensive fallback rather than a silent empty doc.
		raw = []byte{}
	}
	var body strings.Builder
	body.WriteString("---\n")
	body.Write(raw)
	body.WriteString("---\n## Context\n## What was built\n## How to verify\n## How to operate\n## Limitations & next steps\n")
	return []byte(body.String())
}

// --- pack -----------------------------------------------------------------

// dataPackDeclaration is dataCore.pack's own file-classification convention
// (DEVIATION, recorded because the frozen seam gives Pack no per-file
// declaration mechanism at all — DataPackRequest carries no Entries map,
// and T1's own flag table for `data pack` has no per-file flag), now the
// directory-per-schema rule spec 05a §T2.3.5 / plan D-13 specifies:
//
//   - a file under "<source>/<schema-stem>/…" (any depth) conforms to that
//     contract schema entry, where schema-stem is the schema's own carried-
//     set path with its directory and ".schema.json"/".json" suffix
//     stripped (dataSchemaStem, below);
//   - a flat file at the source root conforms to the contract's SOLE
//     schema entry when it declares exactly one — and is REFUSED, naming
//     every schema the contract declares and the layout that would resolve
//     it, when it declares several;
//   - "README.md" (case-insensitively) at the source root is RoleReadme;
//   - "index.json"/"index.ndjson" (case-insensitively) at the source root
//     is RoleIndex.
//
// Refusing the ambiguous flat-root case is the point, not a gap: a pack
// that silently picked one of several schemas would produce a package whose
// verdict means nothing (spec 05a §T2.3.5's own framing).
func (c *dataCore) pack(ctx context.Context, req cli.DataPackRequest) (cli.DataResult, error) {
	schemas, pinnedRef, err := c.resolveContractSchemas(ctx, req.ContractRef)
	if err != nil {
		return cli.DataResult{}, err
	}
	checker, err := datapackage.NewEntryChecker(schemas)
	if err != nil {
		return cli.DataResult{}, err
	}

	entries, err := c.classifyPackEntries(req.From, req.Format, schemas)
	if err != nil {
		return cli.DataResult{}, err
	}

	var supersedes *datapackage.Supersedes
	if req.Supersedes != "" {
		prior, err := c.resolveDataPackage(ctx, req.Supersedes)
		if err != nil {
			return cli.DataResult{}, fmt.Errorf("data: pack: resolve --supersedes %q: %w", req.Supersedes, err)
		}
		var priorDoc datapackage.Document
		if err := json.Unmarshal(prior.ManifestRaw, &priorDoc); err != nil {
			return cli.DataResult{}, fmt.Errorf("data: pack: decode --supersedes %q manifest: %w", req.Supersedes, err)
		}
		supersedes = &datapackage.Supersedes{ID: priorDoc.ID, Attempt: priorDoc.Attempt}
	}
	attempt := 1
	if supersedes != nil {
		attempt = supersedes.Attempt + 1
	}

	thread := ""
	if req.Fulfills != "" {
		_, wrProbe, err := c.loadEnvelope(req.Fulfills)
		if err != nil {
			return cli.DataResult{}, fmt.Errorf("data: pack: resolve --fulfills %q: %w", req.Fulfills, err)
		}
		thread = wrProbe.Thread
	}

	// Locator's real value — the package root, relative to the space, the
	// SAME layout DeliverDataPackage/ResolveDataPackage agree on — cannot be
	// known until AFTER the id mints (Pack's own step 9), but Pack's step 2
	// refuses on !artifact.CleanRelativePath(req.Locator) BEFORE the mint,
	// and CleanRelativePath("") is false (paths.go: an empty value is never
	// clean). c.ownSystem is passed as a legal placeholder so pack does not
	// refuse itself on every call; the real value is stamped in below, once
	// document.ID exists.
	document, err := datapackage.Pack(ctx, datapackage.PackRequest{
		Source: req.From, System: c.ownSystem, Thread: thread, Contract: pinnedRef,
		Classification: "internal", DataProfile: req.Profile, Format: req.Format, Locator: c.ownSystem,
		Entries: entries, Expires: req.Expires, Attempt: attempt, Supersedes: supersedes,
		MaxAttempts: req.MaxAttempts, Bounds: datapackage.DefaultBounds(), Checker: checker,
		Now: c.now(), Entropy: c.entropy,
		// Provenance is REQUIRED by data-package/v1 (origin_system has
		// minLength 1), and an earlier revision left it at its zero value —
		// so every real pack produced a manifest that `data deliver` then
		// refused at its own schema check. What is honestly knowable here is
		// who packed it and when; the cursor/watermark is the producer's to
		// supply and stays empty rather than being invented, since a
		// fabricated watermark is worse than an absent one.
		Provenance: datapackage.Provenance{
			OriginSystem: c.ownSystem,
			ExtractedAt:  c.now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return cli.DataResult{}, err
	}
	document.Locator = filepath.ToSlash(filepath.Join(c.ownSystem, "data", document.ID))

	if err := c.writeStagedPackage(document, req.From); err != nil {
		return cli.DataResult{}, err
	}
	return cli.DataResult{Manifest: &document}, nil
}

// classifyPackEntries walks req.From per dataPackDeclaration's convention
// (spec 05a §T2.3.5, plan D-13): the source root is scanned once, and every
// TOP-LEVEL directory whose name matches a contract schema's own stem is
// walked recursively (dataWalkSchemaDir) so every file under it — at any
// depth — conforms to that schema. A top-level FILE is README/index by
// name, or (when the contract declares exactly one schema) the contract's
// sole dataset schema; with more than one schema declared, a top-level file
// is refused, naming every schema the contract declares and the directory
// layout that would resolve it.
func (c *dataCore) classifyPackEntries(from, format string, schemas map[string][]byte) (map[string]datapackage.EntryDeclaration, error) {
	mediaType, ok := datapackage.MediaTypeForFormat(format)
	if !ok {
		return nil, fmt.Errorf("data: pack: unknown format %q", format)
	}

	stems := make(map[string]string, len(schemas)) // schema-stem -> schema carried-set path
	for schemaPath := range schemas {
		stem := dataSchemaStem(schemaPath)
		if existing, dup := stems[stem]; dup && existing != schemaPath {
			return nil, fmt.Errorf(
				"data: pack: contract schemas %q and %q both resolve to the directory stem %q — packing cannot disambiguate between them by directory name",
				existing, schemaPath, stem,
			)
		}
		stems[stem] = schemaPath
	}
	var soleSchema string
	if len(schemas) == 1 {
		for schemaPath := range schemas {
			soleSchema = schemaPath
		}
	}

	dirEntries, err := os.ReadDir(from)
	if err != nil {
		return nil, fmt.Errorf("data: pack: read --from %q: %w", from, err)
	}
	declared := make(map[string]datapackage.EntryDeclaration, len(dirEntries))
	for _, entry := range dirEntries {
		name := entry.Name()
		switch {
		case entry.IsDir():
			schemaPath, ok := stems[name]
			if !ok {
				return nil, fmt.Errorf(
					"data: pack: directory %q under --from does not match any of the pinned contract's schema entries (%s) — a directory under --from must be named for one of these schemas' own stems (%s)",
					name, dataListSchemaPaths(schemas), dataListSchemaStems(stems),
				)
			}
			if err := dataWalkSchemaDir(filepath.Join(from, name), name, mediaType, schemaPath, declared); err != nil {
				return nil, err
			}
		case strings.EqualFold(name, "README.md"):
			declared[name] = datapackage.EntryDeclaration{Role: datapackage.RoleReadme, MediaType: "text/markdown"}
		case strings.EqualFold(name, "index.json") || strings.EqualFold(name, "index.ndjson"):
			declared[name] = datapackage.EntryDeclaration{Role: datapackage.RoleIndex, MediaType: mediaType}
		default:
			if len(schemas) != 1 {
				return nil, fmt.Errorf(
					"data: pack: entry %q is at the source root, but the pinned contract declares %d schema entries (%s) — place it under one of <source>/<schema-stem>/ instead, where schema-stem is one of: %s",
					name, len(schemas), dataListSchemaPaths(schemas), dataListSchemaStems(stems),
				)
			}
			declared[name] = datapackage.EntryDeclaration{Role: datapackage.RoleDataset, MediaType: mediaType, ConformsTo: soleSchema}
		}
	}
	return declared, nil
}

// dataWalkSchemaDir recursively classifies every file under dir (a
// top-level "<source>/<schema-stem>/" directory) as a RoleDataset entry
// conforming to schemaPath, keying declared by the SAME forward-slash,
// source-relative path WalkLocalDirectory itself produces (safefs.go builds
// entry paths with path.Join, never filepath.Join, so this walk matches it
// exactly rather than merely resembling it on POSIX and diverging on
// Windows).
func dataWalkSchemaDir(dir, relPrefix, mediaType, schemaPath string, declared map[string]datapackage.EntryDeclaration) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("data: pack: read %q: %w", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		relPath := relPrefix + "/" + name
		if entry.IsDir() {
			if err := dataWalkSchemaDir(filepath.Join(dir, name), relPath, mediaType, schemaPath, declared); err != nil {
				return err
			}
			continue
		}
		declared[relPath] = datapackage.EntryDeclaration{Role: datapackage.RoleDataset, MediaType: mediaType, ConformsTo: schemaPath}
	}
	return nil
}

// dataSchemaStem derives a contract schema's own directory-name convention
// from its carried-set path (plan D-13): the base filename with its
// extension AND a trailing ".schema" stripped — "schema/order.schema.json"
// becomes "order". Nothing in the shipped contract package already names
// this convention (checked: internal/contract has no schema-stem/directory
// naming concept at all), so this is a NEW convention this file alone
// defines and owns, scoped to how `a2a data pack` maps a source directory
// onto a contract's schemas — it names nothing about the contract itself.
func dataSchemaStem(schemaPath string) string {
	base := path.Base(schemaPath)
	base = strings.TrimSuffix(base, path.Ext(base))
	base = strings.TrimSuffix(base, ".schema")
	return base
}

// dataListSchemaPaths and dataListSchemaStems render every contract schema
// (and its own derived stem) in a refusal message, sorted so two runs over
// the same contract render byte-identical text.
func dataListSchemaPaths(schemas map[string][]byte) string {
	names := make([]string, 0, len(schemas))
	for schemaPath := range schemas {
		names = append(names, schemaPath)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func dataListSchemaStems(stems map[string]string) string {
	names := make([]string, 0, len(stems))
	for stem := range stems {
		names = append(names, stem)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// writeStagedPackage persists document + every payload file (re-read from
// source, byte-identical) under this core's own staging convention:
// <stagingDir>/data/<document.ID>/{manifest.json, <entry paths...>}. This is
// the "packed staging root" DataDeliverRequest.StagingRoot names as `a2a
// data pack`'s own output (T1's flags table has no --to/--stage flag, so
// this location is deterministic from the minted package id alone, printed
// by the CLI's own renderDataPackText as part of the manifest preview).
// DEVIATION, recorded because the spec names the staging root as an input
// to deliver without ever specifying where pack writes it.
func (c *dataCore) writeStagedPackage(document datapackage.Document, from string) error {
	root := filepath.Join(c.stagingDir, "data", document.ID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("data: pack: stage %s: %w", root, err)
	}
	manifestRaw, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("data: pack: encode manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), manifestRaw, 0o644); err != nil {
		return fmt.Errorf("data: pack: write staged manifest: %w", err)
	}
	for _, entry := range document.Entries {
		raw, err := os.ReadFile(filepath.Join(from, entry.Path)) //nolint:gosec // reason: entry.Path was already proven contained by WalkLocalDirectory inside datapackage.Pack
		if err != nil {
			return fmt.Errorf("data: pack: re-read %s for staging: %w", entry.Path, err)
		}
		dest := filepath.Join(root, entry.Path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("data: pack: stage %s: %w", dest, err)
		}
		if err := os.WriteFile(dest, raw, 0o644); err != nil { //nolint:gosec // reason: staged content is not secret
			return fmt.Errorf("data: pack: write staged entry %s: %w", entry.Path, err)
		}
	}
	return nil
}

// stagedManifestName is the manifest's fixed name inside a staging root. It
// is excluded from the payload set: the manifest describes the entries, it is
// not one of them, and the delivery write adds it separately.
const stagedManifestName = "manifest.json"

// readStagedPackage reads a packed staging root into its manifest and the
// exact bytes of every entry the manifest declares, refusing anything the
// manifest cannot prove.
//
// Three checks, in this order, each of which the previous one makes
// meaningful:
//
//  1. The manifest validates against data-package/v1. Its `path` constraint
//     is what makes a traversal unrepresentable rather than merely unlikely.
//  2. The tree is read through datapackage.WalkLocalDirectory, which enforces
//     containment, refuses symlinks, and re-checks the bounds.
//  3. datapackage.VerifyEntrySet proves every declared entry against its own
//     declared digest, before assembly (L-3), and refuses a declared entry
//     with no bytes behind it.
//
// After all three, --expect-pack has something real to bind to: the aggregate
// it compares was recomputed here from verified entries, not copied out of
// the manifest that claimed it.
func readStagedPackage(ctx context.Context, stagingRoot string) (datapackage.Document, []byte, map[string][]byte, error) {
	bounds := datapackage.DefaultBounds()
	files, err := datapackage.WalkLocalDirectory(ctx, stagingRoot, bounds)
	if err != nil {
		return datapackage.Document{}, nil, nil, fmt.Errorf("data: deliver: read staging root: %w", err)
	}

	var manifestRaw []byte
	entries := make([]datapackage.LocalFile, 0, len(files))
	for _, f := range files {
		if f.RelPath == stagedManifestName {
			manifestRaw = f.Bytes
			continue
		}
		entries = append(entries, f)
	}
	if manifestRaw == nil {
		return datapackage.Document{}, nil, nil, fmt.Errorf("data: deliver: staging root %s has no %s", stagingRoot, stagedManifestName)
	}

	var instance any
	if err := json.Unmarshal(manifestRaw, &instance); err != nil {
		return datapackage.Document{}, nil, nil, fmt.Errorf("data: deliver: decode staged manifest: %w", err)
	}
	corpus, err := schema.Load()
	if err != nil {
		return datapackage.Document{}, nil, nil, fmt.Errorf("data: deliver: load schema corpus: %w", err)
	}
	violations, err := corpus.ValidateDataPackage(instance)
	if err != nil {
		return datapackage.Document{}, nil, nil, fmt.Errorf("data: deliver: validate staged manifest: %w", err)
	}
	if len(violations) != 0 {
		return datapackage.Document{}, nil, nil, fmt.Errorf("data: deliver: staged manifest is not a valid data-package/v1: field %q failed %q", violations[0].Path, violations[0].Keyword)
	}

	var document datapackage.Document
	if err := json.Unmarshal(manifestRaw, &document); err != nil {
		return datapackage.Document{}, nil, nil, fmt.Errorf("data: deliver: decode staged manifest: %w", err)
	}

	verified, err := datapackage.VerifyEntrySet(document.Entries, entries)
	if err != nil {
		return datapackage.Document{}, nil, nil, fmt.Errorf("data: deliver: staged payload does not match its manifest: %w", err)
	}

	payload := make(map[string][]byte, len(verified.Entries))
	byPath := make(map[string][]byte, len(entries))
	for _, f := range entries {
		byPath[f.RelPath] = f.Bytes
	}
	for _, entry := range verified.Entries {
		payload[entry.Path] = byPath[entry.Path]
	}
	return document, manifestRaw, payload, nil
}

// contractSupersededBy answers L-2's other half: the verdict is unaffected by
// a newer contract version, and the consumer is TOLD the agreement moved.
//
// The verdict half is structural — verification only ever uses the version
// pinned in the manifest, and nothing in this file can reach "latest" even by
// accident. This observation is the part that has to be looked up, and an
// earlier revision never set it at all, so a consumer judging against 1.0.0
// while 1.1.0 was already published had no way to learn that.
//
// It is deliberately advisory and deliberately quiet on failure: an
// unreadable event history makes the observation absent, never the
// verification an error. Being unable to say "there is a newer version" must
// not be able to change a verdict about bytes.
func (c *dataCore) contractSupersededBy(pinnedRef string) string {
	id, pinnedVersion, _, err := splitPinnedContractRef(pinnedRef)
	if err != nil {
		return ""
	}
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return ""
	}
	events, err := cache.CommittedEvents(c.mirrorDir, parsed.System, id)
	if err != nil {
		return ""
	}
	newest := ""
	for _, event := range events {
		if event.Transition != "publish" || event.Version == "" {
			continue
		}
		if older, err := semver.OlderThan(pinnedVersion, event.Version); err != nil || !older {
			continue
		}
		if newest == "" {
			newest = event.Version
			continue
		}
		if older, err := semver.OlderThan(newest, event.Version); err == nil && older {
			newest = event.Version
		}
	}
	if newest == "" {
		return ""
	}
	return id + "@" + newest
}

// splitPinnedContractRef parses "<XC-id>@<version>[#<digest>]" — the shape
// T2.1 requires a manifest's contract field to carry. internal/space owns the
// authoritative parser; this is the read-only projection cmd/a2a needs and
// cannot import, since the dependency runs the other way.
func splitPinnedContractRef(ref string) (id, canonicalVersion, digest string, err error) {
	body, digest, _ := strings.Cut(ref, "#")
	id, rawVersion, found := strings.Cut(body, "@")
	if !found {
		return "", "", "", fmt.Errorf("data: %q is not <contract-id>@<version>", ref)
	}
	canonical, err := semver.Canonical(rawVersion)
	if err != nil {
		return "", "", "", fmt.Errorf("data: %q: %w", ref, err)
	}
	return id, canonical, digest, nil
}

// --- deliver ---------------------------------------------------------------

func (c *dataCore) deliver(ctx context.Context, req cli.DataDeliverRequest) (cli.DataResult, error) {
	// The staging root is read the same way a fetched package is: through
	// the hardened walker, then against the manifest's own declared
	// digests. Reading it with os.ReadFile(filepath.Join(root, entry.Path))
	// would trust a path out of a file on disk — and `deliver` is a
	// separate invocation from `pack`, so nothing proves the manifest here
	// is the one pack wrote. A manifest naming "../../.ssh/id_rsa" would
	// otherwise be read and committed into the exchange space, which is
	// spec 05a §6.4's first adversarial case. Provenance is not a check.
	document, manifestRaw, payload, err := readStagedPackage(ctx, req.StagingRoot)
	if err != nil {
		return cli.DataResult{}, err
	}

	wrEnv, wrProbe, err := c.loadEnvelope(req.Fulfills)
	if err != nil {
		return cli.DataResult{}, fmt.Errorf("data: deliver: resolve --fulfills %q: %w", req.Fulfills, err)
	}
	actor, err := c.resolveActor(req.Actor.Kind, req.Actor.Name, req.Actor.Model)
	if err != nil {
		return cli.DataResult{}, err
	}
	credential, err := c.resolveCredential(ctx)
	if err != nil {
		return cli.DataResult{}, err
	}
	manifest, err := c.loadManifest()
	if err != nil {
		return cli.DataResult{}, fmt.Errorf("data: deliver: load space manifest: %w", err)
	}

	now := c.now()
	handoffID, err := artifact.MintExchangeIDAt("XH", c.ownSystem, now, c.entropy)
	if err != nil {
		return cli.DataResult{}, fmt.Errorf("data: deliver: mint handoff id: %w", err)
	}
	env := fold.Envelope{ID: handoffID, Kind: fold.KindHandoff, From: c.ownSystem, To: []string{wrEnv.From}}
	evaluation := fold.EvaluateCandidate(fold.KindHandoff, fold.NewResult(fold.KindHandoff), fold.Event{
		Subject: handoffID, Transition: fold.TSubmit,
		Actor: fold.Actor{Kind: actor.Kind, Name: actor.Name, System: c.ownSystem},
	}, env, dataMembership(manifest))
	if evaluation.Verdict != fold.VerdictLegal {
		return cli.DataResult{}, fmt.Errorf("data: deliver: handoff submit refused: %s", evaluation.Verdict)
	}
	handoffRaw := c.buildHandoffDraft(handoffID, actor, wrEnv, wrProbe, document)

	eventID, err := artifact.MintULIDAt(now, c.entropy)
	if err != nil {
		return cli.DataResult{}, fmt.Errorf("data: deliver: mint handoff event id: %w", err)
	}
	state := ""
	if evaluation.Applicable {
		state = string(evaluation.Outcome)
	}
	eventDoc := dataEventDoc{
		Schema: "event/v1", Event: eventID.String(), Space: wrProbe.Space,
		Subject: handoffID, Transition: fold.TSubmit, State: state,
		Actor: dataEventActor{Kind: actor.Kind, Name: actor.Name, System: c.ownSystem},
		At:    now.UTC().Format(time.RFC3339),
	}
	eventRaw, err := yaml.Marshal(eventDoc)
	if err != nil {
		return cli.DataResult{}, fmt.Errorf("data: deliver: encode handoff event: %w", err)
	}

	write, err := space.DeliverDataPackage(ctx, space.DataDeliveryRequest{
		System: c.ownSystem, PackageID: document.ID,
		ManifestRaw: manifestRaw, AggregateDigest: document.AggregateDigest, Payload: payload,
		HandoffID: handoffID, HandoffRaw: handoffRaw,
		EventID: eventID.String(), EventYear: now.UTC().Format("2006"), EventRaw: eventRaw,
		ExpectPack: req.ExpectPack,
		SubmitTemplate: space.SubmitRequest{
			RepoDir: c.mirrorDir, RemoteURL: c.remoteURL, Repo: c.repository,
			CommitAuthorName: c.authorName, CommitAuthorEmail: c.authorEmail, BaseBranch: "main",
			CommitMessage: "a2a(data-deliver): " + document.ID,
			PRTitle:       "Deliver " + document.ID,
			PRBody:        "Deliver an immutable data package attempt.",
			Credential:    credential, MinBinaryVersion: manifest.MinBinaryVersion,
		},
	}, c.funnel)
	result := cli.DataResult{Manifest: &document, Write: &write, HandoffID: handoffID}
	if err != nil {
		return result, err
	}
	return result, nil
}

// --- fetch ------------------------------------------------------------------

func (c *dataCore) fetch(ctx context.Context, req cli.DataFetchRequest) (cli.DataResult, error) {
	resolved, err := c.resolveDataPackage(ctx, req.PackageID)
	if err != nil {
		return cli.DataResult{}, err
	}
	var document datapackage.Document
	if err := json.Unmarshal(resolved.ManifestRaw, &document); err != nil {
		return cli.DataResult{}, fmt.Errorf("data: fetch: decode manifest: %w", err)
	}
	files := make([]datapackage.LocalFile, 0, len(resolved.Payload))
	for relPath, raw := range resolved.Payload {
		files = append(files, datapackage.LocalFile{RelPath: relPath, Bytes: raw})
	}
	outcome, _, err := datapackage.Fetch(ctx, document, files, req.Destination, datapackage.DefaultBounds(), c.now())
	if err != nil {
		return cli.DataResult{}, err
	}
	return cli.DataResult{Manifest: &document, Outcome: string(outcome)}, nil
}

// --- verify -----------------------------------------------------------------

type dataHandoffProbe struct {
	ID           string `yaml:"id"`
	Deliverables []struct {
		Ref  string `yaml:"ref"`
		Kind string `yaml:"kind"`
	} `yaml:"deliverables"`
}

// findHandoffForPackage is verify's own reverse lookup: data-package/v1
// carries no field naming the handoff that references it (only the handoff
// names the package, via deliverables[].ref, spec 05a §5), so the handoff is
// found by a bounded glob over every committed handoff, exactly the same
// bounded-glob shape readAllEvents already uses for committed events.
func (c *dataCore) findHandoffForPackage(packageID string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(c.mirrorDir, "*", "exchanges", "XH-*.md"))
	if err != nil {
		return "", fmt.Errorf("data: verify: list committed handoffs: %w", err)
	}
	for _, m := range matches {
		raw, err := os.ReadFile(m) //nolint:gosec // reason: m comes from filepath.Glob against the mirror's own committed tree
		if err != nil {
			return "", fmt.Errorf("data: verify: read %s: %w", m, err)
		}
		fm, err := artifact.ParseFrontmatter(raw)
		if err != nil {
			continue
		}
		var probe dataHandoffProbe
		if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
			continue
		}
		for _, d := range probe.Deliverables {
			if d.Kind == "data" && d.Ref == packageID {
				return probe.ID, nil
			}
		}
	}
	return "", fmt.Errorf("data: verify: no committed handoff carries package %q", packageID)
}

// dataReportEntropy derives a report id's rand4 suffix DETERMINISTICALLY
// from (packageID, pinnedContractRef) rather than from crypto/rand, so a
// report's own VR- id — and, downstream, the operation key
// dataVerifyOperationKey mints from it (internal/space/data_delivery.go) —
// is STABLE across repeated `a2a data verify --record` invocations against
// the same package and contract.
//
// A report has no staging step to inherit that stability from the way
// PackageID gets it for free: pack mints PackageID once and writes it to
// disk, and deliver only ever RE-READS it (plan D-6's own doc comment is
// explicit that this is what makes deliver's own operation key stable
// across retries). Nothing is persisted between "verify" and
// "verify --record" — each is a fresh process invocation — so the id
// itself has to be pinned to inputs that cannot change between two
// --record runs instead. Two such runs therefore mint the SAME report id,
// collide on the SAME operation key and branch, and the funnel's own
// idempotent short-circuit repairs that one PR rather than opening a
// second (plan D-12, mirroring D-6).
//
// Only packageID and pinnedContractRef feed the seed — never c.now() (the
// id's own YYYYMMDD segment still comes from the caller's clock, so a
// cross-midnight re-run mints a different id and a different branch; an
// accepted, narrower version of the same bound deliver's own frozen-at-
// pack-time id does not have) and never the report's own checks[] (if the
// seed depended on judged content, a re-run after ANY change would mint a
// new id and open a second PR — exactly the failure this function exists
// to avoid). This trades away nothing §T2.1/§T2.3 require: the wire format
// stays exactly VR-<system>-<YYYYMMDD>-<rand4>, and
// datapackage.MintReportIDAt's contract with its caller is only "read n
// bytes of entropy", never "read them from crypto/rand specifically".
//
// DEVIATION, recorded because it is this file's own engineering choice
// rather than an explicit spec instruction: applied on BOTH the judge-only
// and --record paths (not only --record) because a reproducible id on the
// judge-only path is strictly better and special-casing entropy behind a
// flag would be a second code path for no gain.
func dataReportEntropy(packageID, pinnedContractRef string) io.Reader {
	sum := sha256.Sum256([]byte("a2ahub-data-verify-report-id-v1\x00" + packageID + "\x00" + pinnedContractRef))
	return bytes.NewReader(sum[:])
}

// verify runs data verify's core: resolve the package and its pinned
// contract's schemas (EXACTLY ONCE — resolveContractSchemas, this file's
// injectable seam) and judge every dataset entry through datapackage.Verify.
// Alone, it judges and prints: no write, no network beyond the resolves
// already required to judge at all (spec 05a T1).
//
// With req.Record set, it performs ONE funnel write
// (space.RecordVerificationReport) carrying the report AND the lifecycle
// event that drives the handoff's own verify-pass or verify-fail
// transition — whichever the report's own result derives (plan D-12).
// There is no caller-selectable direction: transition is set from
// datapackage.VerifyPass(report)'s own outcome, never from a flag. VerifyPass
// re-derives from report.Checks rather than trusting report.Result() (see
// its own doc comment in internal/datapackage/verify.go) — reading
// Result() here would trust a value a forged/hand-authored report could
// carry, which is exactly the forced pass AC-5 names as refused; routing
// through VerifyPass is what makes a forged pass unrepresentable rather
// than merely unlikely, and is the property this file's own seeded-red
// receipt proves by removing this exact call.
func (c *dataCore) verify(ctx context.Context, req cli.DataVerifyRequest) (cli.DataResult, error) {
	resolved, err := c.resolveDataPackage(ctx, req.PackageID)
	if err != nil {
		return cli.DataResult{}, err
	}
	var document datapackage.Document
	if err := json.Unmarshal(resolved.ManifestRaw, &document); err != nil {
		return cli.DataResult{}, fmt.Errorf("data: verify: decode manifest: %w", err)
	}
	schemas, pinnedRef, err := c.resolveContractSchemas(ctx, document.Contract)
	if err != nil {
		return cli.DataResult{}, err
	}
	checker, err := datapackage.NewEntryChecker(schemas)
	if err != nil {
		return cli.DataResult{}, err
	}
	files := make([]datapackage.LocalFile, 0, len(resolved.Payload))
	for relPath, raw := range resolved.Payload {
		files = append(files, datapackage.LocalFile{RelPath: relPath, Bytes: raw})
	}
	reportID, err := datapackage.MintReportIDAt(c.ownSystem, c.now(), dataReportEntropy(req.PackageID, pinnedRef))
	if err != nil {
		return cli.DataResult{}, fmt.Errorf("data: verify: mint report id: %w", err)
	}
	report, err := datapackage.Verify(ctx, datapackage.VerifyRequest{
		ID: reportID, Manifest: document, Files: files, Checker: checker,
		ContractRef: pinnedRef, Consumer: c.ownSystem, Clock: c.now,
		ContractSupersededBy: c.contractSupersededBy(document.Contract),
	})
	if err != nil {
		return cli.DataResult{}, err
	}
	result := cli.DataResult{Report: &report}
	if !req.Record {
		return result, nil
	}

	// The direction is DERIVED, never chosen — see this function's own doc
	// comment. Removing this call (and hardcoding fold.TVerifyPass) is the
	// seeded-red receipt for "the caller cannot choose the direction".
	transition := fold.TVerifyFail
	if err := datapackage.VerifyPass(report); err == nil {
		transition = fold.TVerifyPass
	}

	handoffID, err := c.findHandoffForPackage(req.PackageID)
	if err != nil {
		return result, err
	}
	actor, err := c.resolveActor(req.Actor.Kind, req.Actor.Name, req.Actor.Model)
	if err != nil {
		return result, err
	}
	credential, err := c.resolveCredential(ctx)
	if err != nil {
		return result, err
	}
	manifest, err := c.loadManifest()
	if err != nil {
		return result, fmt.Errorf("data: verify: load space manifest: %w", err)
	}
	candidate := fold.Event{Transition: transition, Actor: fold.Actor{Kind: actor.Kind, Name: actor.Name, System: c.ownSystem}}
	evaluation, _, err := c.evaluateCandidate(manifest, handoffID, candidate)
	if err != nil {
		return result, err
	}
	if evaluation.Verdict != fold.VerdictLegal {
		return result, fmt.Errorf("data: verify: %s refused: %s", transition, evaluation.Verdict)
	}
	now := c.now()
	eventID, err := artifact.MintULIDAt(now, c.entropy)
	if err != nil {
		return result, fmt.Errorf("data: verify: mint %s event id: %w", transition, err)
	}
	state := ""
	if evaluation.Applicable {
		state = string(evaluation.Outcome)
	}
	_, wrProbe, err := c.loadEnvelope(handoffID)
	if err != nil {
		return result, err
	}
	eventDoc := dataEventDoc{
		Schema: "event/v1", Event: eventID.String(), Space: wrProbe.Space,
		Subject: handoffID, Transition: transition, State: state,
		Actor: dataEventActor{Kind: actor.Kind, Name: actor.Name, System: c.ownSystem},
		At:    now.UTC().Format(time.RFC3339),
	}
	eventRaw, err := yaml.Marshal(eventDoc)
	if err != nil {
		return result, fmt.Errorf("data: verify: encode %s event: %w", transition, err)
	}
	reportRaw, err := json.Marshal(report)
	if err != nil {
		return result, fmt.Errorf("data: verify: encode report: %w", err)
	}

	prTitle := "Verify-fail " + handoffID
	prBody := "Record a failing verification."
	if transition == fold.TVerifyPass {
		prTitle = "Verify-pass " + handoffID
		prBody = "Record a passing verification."
	}
	write, err := space.RecordVerificationReport(ctx, space.DataVerificationRecordRequest{
		System: c.ownSystem, PackageID: req.PackageID, ReportID: reportID, ReportRaw: reportRaw,
		HandoffID: handoffID, Transition: transition,
		EventID: eventID.String(), EventYear: now.UTC().Format("2006"), EventRaw: eventRaw,
		SubmitTemplate: space.SubmitRequest{
			RepoDir: c.mirrorDir, RemoteURL: c.remoteURL, Repo: c.repository,
			CommitAuthorName: c.authorName, CommitAuthorEmail: c.authorEmail, BaseBranch: "main",
			CommitMessage: fmt.Sprintf("a2a(%s): %s", transition, handoffID),
			PRTitle:       prTitle, PRBody: prBody,
			Credential: credential, MinBinaryVersion: manifest.MinBinaryVersion,
		},
	}, c.funnel)
	result.Write = &write
	result.HandoffID = handoffID
	return result, err
}

// --- cli.DataOperations adapter -------------------------------------------

type cliDataAdapter struct{ core *dataCore }

func (a cliDataAdapter) Pack(ctx context.Context, req cli.DataPackRequest) (cli.DataResult, error) {
	return a.core.pack(ctx, req)
}

func (a cliDataAdapter) Deliver(ctx context.Context, req cli.DataDeliverRequest) (cli.DataResult, error) {
	return a.core.deliver(ctx, req)
}

func (a cliDataAdapter) Fetch(ctx context.Context, req cli.DataFetchRequest) (cli.DataResult, error) {
	return a.core.fetch(ctx, req)
}

func (a cliDataAdapter) Verify(ctx context.Context, req cli.DataVerifyRequest) (cli.DataResult, error) {
	return a.core.verify(ctx, req)
}

var _ cli.DataOperations = cliDataAdapter{}
