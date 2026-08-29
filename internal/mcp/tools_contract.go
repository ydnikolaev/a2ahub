package mcp

// a2a_contract_* (OP-212/OP-213/OP-221 3rd clause): mirrors internal/cli's
// cmd_contract.go ContractCommand sub-verbs exactly — new (thin delegate
// to a2a_new's own draft path), publish/deprecate/retire (funnel writers,
// G1/G2/§5.4 gate awareness unchanged), diff/verify-export (read-only, no
// funnel/event — the digest-tree comparison over the local mirror).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

// contractDescriptorProbe is this package's own minimal decode of a
// contract's descriptor (contract.md) fields (mirrors internal/cli's
// contractDescriptorProbe).
type contractDescriptorProbe struct {
	ID      string   `yaml:"id"`
	Space   string   `yaml:"space"`
	From    string   `yaml:"from"`
	To      []string `yaml:"to"`
	Version string   `yaml:"version"`
	// Thread is the contract's own §3.8 thread id — spec 46 §T1 R2: the
	// deprecation announcement is DERIVED from this contract and inherits
	// it. Mirrors internal/cli's contractDescriptorProbe field.
	Thread        string `yaml:"thread"`
	CompatPolicy  string `yaml:"compat_policy"`
	SchemaFormat  string `yaml:"schema_format"`
	GeneratedFrom struct {
		Tool         string `yaml:"tool"`
		SourceDigest string `yaml:"source_digest"`
	} `yaml:"generated_from"`
	// XOperational is P5's AC1 discharge half (specs/05-declared-nature.md,
	// 2026-08-10 amendment): the named operational items this contract
	// declares, whatever their state. A nil slice (the field absent) is
	// itself meaningful — this probe never treats absence as any item
	// being `ready` (P-1) — but this package's only present use of it
	// (`a2a_contract` action=activate's declared-item refusal, below) only
	// asks "is this NAME present at all". Mirrors internal/cli's own
	// contractDescriptorProbe field (ADR-001: internal/mcp never imports
	// internal/cli).
	XOperational []xOperationalItemProbe `yaml:"x_operational"`
	// XBinding is P5's AC2 counterweight, and it is on THIS probe because
	// leaving it off was a live hole rather than an omission. The T2
	// escape-hatch argument (threat-model.md) is that a contract declaring
	// itself non-adoptable costs its author every consumer — which only
	// holds if EVERY surface that can adopt refuses one. `a2a contract
	// adopt` refused; `a2a_contract` action=adopt did not, and it writes the
	// identical `consumes.yaml` dependency, which IS the pin. Found by the
	// 2026-08-10 coherence audit, hours after the CLI half shipped in its
	// own commit specifically so the hatch would never open alone.
	// Mirrors internal/cli's own field (ADR-001: no import).
	XBinding *xBindingProbe `yaml:"x_binding"`
}

// xBindingProbe decodes envelope/v2/contract's `x_binding` — either the
// bare sentinel `none`, or the long form object. A nil pointer means the
// field is ABSENT: undeclared, which is distinct from a declared `none`
// (P-1), so callers must check for nil before asking anything of it.
// Mirrors internal/cli's own xBindingProbe, including its UnmarshalYAML.
type xBindingProbe struct {
	Sentinel            bool
	ArtifactClass       string `yaml:"artifact_class"`
	CompatibilityStatus string `yaml:"compatibility_status"`
	Adoptable           *bool  `yaml:"adoptable"`
	RuntimePinnable     *bool  `yaml:"runtime_pinnable"`
}

// UnmarshalYAML distinguishes the two shapes x_binding's own schema oneOf's:
// a bare scalar (only "none" is schema-valid; any other scalar decodes
// harmlessly here because schema validation, not this probe, refuses it) or
// the long-form mapping.
func (x *xBindingProbe) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		if s == "none" {
			x.Sentinel = true
		}
		return nil
	}
	type alias xBindingProbe
	var a alias
	if err := node.Decode(&a); err != nil {
		return err
	}
	*x = xBindingProbe(a)
	return nil
}

// nonAdoptable reports whether x declares this contract unable to be
// adopted — the bare `none` sentinel, or the long form with
// `adoptable: false`. A nil x (undeclared) is adoptable, per P-1.
func (x *xBindingProbe) nonAdoptable() bool {
	if x == nil {
		return false
	}
	if x.Sentinel {
		return true
	}
	return x.Adoptable != nil && !*x.Adoptable
}

// xOperationalItemProbe decodes one entry of envelope/v2/contract's
// `x_operational[]` (specs/05-declared-nature.md's AC1 discharge half) —
// {name, state, eta?}. Mirrors internal/cli's own xOperationalItemProbe.
type xOperationalItemProbe struct {
	Name  string `yaml:"name"`
	State string `yaml:"state"`
	ETA   string `yaml:"eta"`
}

func contractReadDescriptor(mirrorDir, id string) (fm artifact.Frontmatter, probe contractDescriptorProbe, relPath, relDir string, err error) {
	parsed, perr := artifact.ParseID(id)
	if perr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("mcp: %s: %w", id, perr)
	}
	if parsed.Prefix != "XC" {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("mcp: %s: not a contract id (XC-)", id)
	}
	layout, lerr := space.NewLayout(parsed.System)
	if lerr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", lerr
	}
	relPath = layout.ProvidesContract(parsed.Slug)
	relDir = path.Dir(relPath)
	raw, rerr := readBoundedFile(filepath.Join(mirrorDir, relPath), maxMirrorEventBytes)
	if rerr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("mcp: cannot read %s: %w", id, rerr)
	}
	fm, ferr := artifact.ParseFrontmatter(raw)
	if ferr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("mcp: %s: %w", id, ferr)
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("mcp: %s: cannot decode descriptor: %w", id, err)
	}
	return fm, probe, relPath, relDir, nil
}

func contractAddFrontmatterFields(raw []byte, fields map[string]any) ([]byte, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		return nil, fmt.Errorf("mcp: cannot decode frontmatter: %w", err)
	}
	for k, v := range fields {
		doc[k] = v
	}
	newYAML, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("mcp: cannot encode frontmatter: %w", err)
	}
	return artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: newYAML, Body: fm.Body}), nil
}

// --- semver ---------------------------------------------------------------

type contractSemver [3]int

func contractParseSemver(s string) (contractSemver, error) {
	var out contractSemver
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("mcp: %q is not a valid semver (major.minor.patch)", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("mcp: %q is not a valid semver (major.minor.patch)", s)
		}
		out[i] = n
	}
	return out, nil
}

func (v contractSemver) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// contractCanonicalVersion mirrors internal/cli's own copy (ADR-001:
// internal/mcp never imports internal/cli) — see its doc comment for why
// this exists: fold.Result.Versions is a map[string]State keyed on the
// raw version string with no canonicalization of its own, so two
// spellings of one version must be reformatted to the identical string
// before either reaches fold.
func contractCanonicalVersion(v string) string {
	if parsed, err := contractParseSemver(v); err == nil {
		return parsed.String()
	}
	return v
}

func contractDeprecateSeed(contractID, version, sunset string) []byte {
	var buf bytes.Buffer
	buf.WriteString("contract=" + contractID + "\n")
	buf.WriteString("version=" + version + "\n")
	buf.WriteString("sunset=" + sunset + "\n")
	sum := sha256.Sum256(buf.Bytes())
	return sum[:]
}

// --- ContractDeps -----------------------------------------------------

// ContractDeps bundles WriteDeps for the contract family's funnel-writer
// sub-verbs (publish/deprecate/retire) and read-only sub-verbs
// (diff/verify-export). StagingDir is P37 Wave I's own addition: `publish`
// needs it to fold a staged schema/fixture edit into the version it is
// declaring (see newContractPublishHandler's own doc comment) — empty
// (the zero value, e.g. any existing test construction that does not set
// it) degrades to exactly the pre-wave behaviour, never a panic.
type ContractDeps struct {
	WriteDeps
	// StagingDir is retained only for source compatibility while cmd/a2a
	// moves to the per-request staging selector. Contract handlers never read
	// it and there is no transport-local staging overlay fallback.
	StagingDir  string
	Publication ContractPublicationOperations
	Materialize ContractMaterializeOperation
	Check       ContractCheckOperation
	Inspection  ContractInspectionOperations
}

// ContractNewInput is a2a_contract_new's structured input: a thin delegate
// onto a2a_new's own draft path with type="contract" (mirrors
// internal/cli's runNew -> P6 NewCommand delegation).
type ContractNewInput struct {
	// Action is a2a_contract's own discriminator — never read here; it
	// exists purely so this struct stays decodable under decodeStrict's
	// DisallowUnknownFields when newDispatch forwards the full raw args,
	// discriminator included (tools_dispatch.go's own doc comment).
	Action string            `json:"action,omitempty"`
	Slug   string            `json:"slug"`
	Fields map[string]string `json:"fields,omitempty"`
	Body   string            `json:"body,omitempty"`
	Thread string            `json:"thread,omitempty"`
	Actor  ActorInput        `json:"actor,omitempty"`
}

func newContractNewHandler(newDeps NewDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractNewInput
		if err := decodeStrict(args, &in, "contract new", 0); err != nil {
			return nil, "", err
		}
		if in.Slug == "" {
			return nil, "", fmt.Errorf("contract new: slug is required")
		}
		inner := NewInput{
			Items:  []NewItem{{Type: "contract", Fields: in.Fields, Body: in.Body, Slug: in.Slug, Actor: in.Actor}},
			Thread: in.Thread,
		}
		raw, merr := json.Marshal(inner)
		if merr != nil {
			return nil, "", fmt.Errorf("contract new: %w", merr)
		}
		return newNewHandler(newDeps)(ctx, raw)
	}
}

// contractPublishedVersions returns every PRIOR publish event's version
// for id, sorted ascending.
func contractPublishedVersions(all []eventDoc, id string) []contractSemver {
	var out []contractSemver
	for _, ev := range all {
		if ev.Subject != id || ev.Transition != fold.TPublish || ev.Version == "" {
			continue
		}
		v, err := contractParseSemver(ev.Version)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		for k := 0; k < 3; k++ {
			if out[i][k] != out[j][k] {
				return out[i][k] < out[j][k]
			}
		}
		return false
	})
	return out
}

// ContractPublishInput is a2a_contract_publish's structured input.
type ContractPublishInput struct {
	// Action is a2a_contract's own discriminator — never read here; see
	// ContractNewInput's identical field for why it exists.
	Action              string `json:"action,omitempty"`
	Space               string `json:"space,omitempty"`
	ID                  string `json:"id"`
	Version             string `json:"version,omitempty"`
	Bump                string `json:"bump,omitempty"`
	Staging             string `json:"staging,omitempty"`
	ExpectPlan          string `json:"expect_plan,omitempty"`
	GeneratedFromDigest string `json:"generated_from_digest,omitempty"`
	// AllowEmptyBump is the MCP twin of the CLI's `--allow-empty-bump` (P2
	// AC-3/4, T1: "an agent that can only reach the MCP surface must not
	// meet a refusal it has no way to satisfy"). Decoded here because
	// newP6ContractPublishHandler (internal/mcp/tools_contract_p6.go)
	// decodeStrict()s into THIS struct; a schema property with no matching
	// field here is a hard decode refusal for every caller who sends it
	// (DisallowUnknownFields), which is exactly what
	// scripts/check-mcp-schema-decodable.sh exists to catch.
	AllowEmptyBump bool       `json:"allow_empty_bump,omitempty"`
	Actor          ActorInput `json:"actor,omitempty"`
}

func newContractPublishHandler(deps ContractDeps) HandlerFunc {
	if deps.Publication == nil {
		return func(context.Context, json.RawMessage) (any, string, error) {
			return nil, "", fmt.Errorf("contract publish: P6 service is not configured")
		}
	}
	return newP6ContractPublishHandler(deps)
}

// contractDistinctPublishedVersions dedupes contractPublishedVersions' own
// per-EVENT list into the distinct SET of versions ever published (mirrors
// internal/cli's own copy — ADR-001: internal/mcp never imports
// internal/cli). contractPublishedVersions already returns its slice sorted
// ascending, so dedup-by-adjacency preserves that order.
func contractDistinctPublishedVersions(all []eventDoc, id string) []contractSemver {
	sorted := contractPublishedVersions(all, id)
	out := make([]contractSemver, 0, len(sorted))
	for i, v := range sorted {
		if i > 0 && v == sorted[i-1] {
			continue
		}
		out = append(out, v)
	}
	return out
}

// contractResolveVersionOrRefuse is F4/AC-972.1 on the MCP surface: `deprecate`
// and `retire` both used to default an omitted version to the descriptor's
// CURRENT version — after a `bump: major` publish that is the NEW version, so
// the OLD one silently got no announcement at all. With exactly one distinct
// published version, defaulting to currentVersion is unambiguous and stays;
// with MORE than one and explicit == "", this REFUSES with an error listing
// every published version, oldest first, so the caller can retry with an
// explicit version (mirrors internal/cli's own copy — ADR-001: internal/mcp
// never imports internal/cli). The MCP surface returns errors rather than
// exit codes, so unlike the CLI's exit-2 usage error this is just the
// returned error.
func contractResolveVersionOrRefuse(all []eventDoc, id, explicit, currentVersion string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	versions := contractDistinctPublishedVersions(all, id)
	if len(versions) <= 1 {
		return currentVersion, nil
	}
	strs := make([]string, len(versions))
	for i, v := range versions {
		strs[i] = v.String()
	}
	return "", fmt.Errorf("mcp: version is required: %s has %d published versions (%s) — say which one", id, len(versions), strings.Join(strs, ", "))
}

// ContractDeprecateInput is a2a_contract_deprecate's structured input.
type ContractDeprecateInput struct {
	// Action is a2a_contract's own discriminator — never read here; see
	// ContractNewInput's identical field for why it exists.
	Action    string     `json:"action,omitempty"`
	Space     string     `json:"space,omitempty"`
	ID        string     `json:"id"`
	Version   string     `json:"version,omitempty"`
	Successor string     `json:"successor"`
	Sunset    string     `json:"sunset"`
	Actor     ActorInput `json:"actor,omitempty"`
}

func newContractDeprecateHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractDeprecateInput
		if err := decodeStrict(args, &in, "contract deprecate", 0); err != nil {
			return nil, "", err
		}
		if in.ID == "" || in.Successor == "" || in.Sunset == "" {
			return nil, "", fmt.Errorf("contract deprecate: id, successor and sunset are required")
		}

		// Resolve the target space BEFORE the first deps.MirrorDir/deps.Manifest
		// read below — a single-space session returns deps unchanged
		// (resolveWriteSpace's own contract); shadowed locally so the resolved
		// space never leaks into a later call through this same constructed
		// handler.
		resolvedWrite, err := resolveWriteSpace(deps.WriteDeps, in.Space, nil)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}
		deps := deps
		deps.WriteDeps = resolvedWrite

		resolved, actorErr := deps.ResolveActor(in.Actor)
		if actorErr != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", actorErr)
		}
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		// P4: deprecatedVersion must be resolved BEFORE the legality check
		// below — see internal/cli's own runDeprecate comment on why the
		// order matters once a contract carries any recorded version.
		_, probe, _, _, err := contractReadDescriptor(deps.MirrorDir, in.ID)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot read %s from the local mirror — run `a2a sync` first, or check the contract has been published: %w", in.ID, err)
		}
		allEvents, err := readAllEvents(deps.MirrorDir)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}
		deprecatedVersion, err := contractResolveVersionOrRefuse(allEvents, in.ID, in.Version, probe.Version)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}

		// legalityVersion: see internal/cli's own runDeprecate comment —
		// "" (the legacy whole-subject path) unless this contract has at
		// least one prior publish event that itself carried a `version`
		// field; deprecatedVersion is always non-empty once the contract
		// has published at all (F4/AC-972.1, predates P4), but a
		// version-less history's Result.Versions can never contain it.
		legalityVersion := ""
		if len(contractPublishedVersions(allEvents, in.ID)) > 0 {
			legalityVersion = contractCanonicalVersion(deprecatedVersion)
		}
		deprecateEvaluation, _, _, err := evaluateCandidate(deps.MirrorDir, deps.Manifest, in.ID, fold.Event{
			Transition: fold.TDeprecate, Version: legalityVersion, Actor: actor,
		}, nil)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %s: %w", in.ID, err)
		}
		if deprecateEvaluation.Verdict != fold.VerdictLegal {
			return nil, "", fmt.Errorf("contract deprecate: %w", verdictError(in.ID, deprecateEvaluation.Verdict))
		}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}

		deprecateEventID, err := artifact.MintULIDAt(now, deps.Entropy)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot mint event id: %w", err)
		}
		// The COMMITTED event's own Version field is legalityVersion, not
		// deprecatedVersion — see internal/cli's own runDeprecate comment
		// on why the two must agree (fold.CheckCandidate's own "one rule,
		// never a second reading" invariant, one caller layer up).
		deprecateEvent := eventDoc{
			Schema: "event/v1", Event: deprecateEventID.String(), Space: probe.Space,
			Subject: in.ID, Transition: fold.TDeprecate, State: eventReceiptState(deprecateEvaluation), Version: legalityVersion,
			Actor: eventActorFrom(resolved, actor.System),
			At:    now.UTC().Format(time.RFC3339),
			Refs:  []refEntry{{Ref: in.Successor}},
		}
		deprecateRaw, merr := yaml.Marshal(deprecateEvent)
		if merr != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot encode event: %w", merr)
		}

		announcementSeed := contractDeprecateSeed(in.ID, deprecatedVersion, in.Sunset)
		announcementID, err := artifact.MintExchangeIDAt("XA", deps.OwnSystem, now, bytes.NewReader(announcementSeed))
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot mint announcement id: %w", err)
		}
		announcementDraft, err := template.Render(template.Input{
			Type: "announcement", ID: announcementID, Actor: resolved, Created: now,
			Fields: map[string]string{
				"from":     deps.OwnSystem,
				"category": "deprecation",
			},
		})
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: render announcement failed: %w", err)
		}
		// F3/T4 (AC-971.1, AC-971.2): the announcement's addressees are the
		// registered-consumer set — the SAME cache.FindRegisteredConsumers
		// family the retire precondition reads (unscoped here; retire's own
		// read is major-scoped, Edge 1) — not the descriptor's own
		// authoring-time `to:`. "Who blocks my retire" and "who was told"
		// share one underlying query instead of two that can drift apart
		// (mirrors internal/cli's own copy — ADR-001: internal/mcp never
		// imports internal/cli).
		to, err := contractDeprecateAddressees(deps.MirrorDir, in.ID, probe.From, probe.To)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}

		// Spec 46 §T1 R2 + the threadless refusal, mirroring internal/cli's
		// ContractDeprecateCommand exactly (ADR-001: this package may never
		// import internal/cli, so the rule is written twice and
		// cmd/a2a/mcp_equivalence_test.go is what keeps the copies honest —
		// it went red the moment only the CLI half propagated, which is the
		// parity suite doing precisely its job).
		if probe.Thread == "" {
			return nil, "", fmt.Errorf(
				"contract deprecate: %s carries no thread, so its deprecation announcement has no conversation to join; "+
					"that contract predates thread propagation — reseed the space or republish it with this version", in.ID)
		}

		announcementDraft, err = contractAddFrontmatterFields(announcementDraft, map[string]any{
			// space/to/title are the template's own PLACEHOLDERS and
			// template.Render fills none of them, so every deprecation
			// announcement was authored with a literal `to:
			// [<recipient-system>]` and refused by V2 (REF-006). `to` used
			// to be filled from probe.To for the same reason — it is now
			// `to` computed above (F3), which falls back to probe.To only
			// when the registry has no registered consumers yet. Mirrors
			// internal/cli's copy (ADR-001's deliberate duplication).
			"space":         probe.Space,
			"to":            to,
			"title":         fmt.Sprintf("Deprecating %s@%s (sunset %s)", in.ID, deprecatedVersion, in.Sunset),
			"ack_requested": true,
			"deprecates":    in.ID + "@" + deprecatedVersion,
			"valid_until":   in.Sunset,
			"thread":        probe.Thread,
		})
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}

		announcementPublishEventID, err := artifact.MintULIDAt(now, deps.Entropy)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot mint event id: %w", err)
		}
		announcementEnv := fold.Envelope{
			ID: announcementID, Kind: fold.KindAnnouncement, From: deps.OwnSystem, To: to,
		}
		announcementEvaluation := fold.EvaluateCandidate(
			fold.KindAnnouncement,
			fold.NewResult(fold.KindAnnouncement),
			fold.Event{Subject: announcementID, Transition: fold.TPublish, Actor: actor},
			announcementEnv,
			membership(deps.Manifest),
		)
		if announcementEvaluation.Verdict != fold.VerdictLegal {
			return nil, "", fmt.Errorf("contract deprecate: %w", verdictError(announcementID, announcementEvaluation.Verdict))
		}
		announcementPublishEvent := eventDoc{
			Schema: "event/v1", Event: announcementPublishEventID.String(), Space: probe.Space,
			Subject: announcementID, Transition: fold.TPublish, State: eventReceiptState(announcementEvaluation),
			Actor: eventActorFrom(resolved, actor.System),
			At:    now.UTC().Format(time.RFC3339),
		}
		announcementPublishRaw, merr := yaml.Marshal(announcementPublishEvent)
		if merr != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot encode announcement publish event: %w", merr)
		}

		files := []space.FileWrite{
			{Path: layout.EventFile(now.UTC().Format("2006"), deprecateEventID.String()), Content: deprecateRaw},
			{Path: layout.Exchange(announcementID), Content: announcementDraft},
			{Path: layout.EventFile(now.UTC().Format("2006"), announcementPublishEventID.String()), Content: announcementPublishRaw},
		}

		req := deps.buildRequest([]string{in.ID, announcementID}, files, "contract-deprecate", false)
		req.OperationKey = operation.ContractDeprecate(
			deps.OwnSystem, in.ID, contractCanonicalVersion(deprecatedVersion), in.Successor, in.Sunset,
		)
		result, err := deps.submit(ctx, req, "contract deprecate", []string{in.ID, announcementID})
		return result, "", err
	}
}

// ContractRetireInput is a2a_contract_retire's structured input.
type ContractRetireInput struct {
	// Action is a2a_contract's own discriminator — never read here; see
	// ContractNewInput's identical field for why it exists.
	Action   string     `json:"action,omitempty"`
	Space    string     `json:"space,omitempty"`
	ID       string     `json:"id"`
	Version  string     `json:"version,omitempty"`
	Override bool       `json:"override,omitempty"`
	Actor    ActorInput `json:"actor,omitempty"`
}

func newContractRetireHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractRetireInput
		if err := decodeStrict(args, &in, "contract retire", 0); err != nil {
			return nil, "", err
		}
		if in.ID == "" {
			return nil, "", fmt.Errorf("contract retire: id is required")
		}

		// Resolve the target space BEFORE the first deps.MirrorDir/deps.Manifest
		// read below — see the deprecate handler's identical comment.
		resolvedWrite, err := resolveWriteSpace(deps.WriteDeps, in.Space, nil)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %w", err)
		}
		deps := deps
		deps.WriteDeps = resolvedWrite

		resolved, actorErr := deps.ResolveActor(in.Actor)
		if actorErr != nil {
			return nil, "", fmt.Errorf("contract retire: %w", actorErr)
		}
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		// P4: retiredVersion must be resolved BEFORE the legality check
		// below — see internal/cli's own runRetire comment on why the
		// order matters once a contract carries any recorded version.
		_, probe, _, _, err := contractReadDescriptor(deps.MirrorDir, in.ID)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: cannot read %s from the local mirror — run `a2a sync` first, or check the contract has been published: %w", in.ID, err)
		}
		allEvents, err := readAllEvents(deps.MirrorDir)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %w", err)
		}
		retiredVersion, err := contractResolveVersionOrRefuse(allEvents, in.ID, in.Version, probe.Version)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %w", err)
		}

		// legalityVersion: see internal/cli's own runRetire comment — ""
		// unless this contract has at least one prior publish event that
		// itself carried a `version` field.
		legalityVersion := ""
		if len(contractPublishedVersions(allEvents, in.ID)) > 0 {
			legalityVersion = contractCanonicalVersion(retiredVersion)
		}
		retireEvaluation, _, _, err := evaluateCandidate(deps.MirrorDir, deps.Manifest, in.ID, fold.Event{
			Transition: fold.TRetire, Version: legalityVersion, Actor: actor,
		}, nil)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %s: %w", in.ID, err)
		}
		if retireEvaluation.Verdict != fold.VerdictLegal {
			return nil, "", fmt.Errorf("contract retire: %w", verdictError(in.ID, retireEvaluation.Verdict))
		}

		now := deps.Now()

		precondition, err := contractBuildRetirePrecondition(deps.MirrorDir, deps.Manifest, in.ID, retiredVersion, in.Override, resolved.Kind == "human", now)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %w", err)
		}
		violation, overridden := validate.CheckRetirePrecondition(precondition)
		if violation != nil {
			return nil, "", fmt.Errorf("contract retire: %s: refused: %s (%s)", in.ID, violation.Message, violation.Code)
		}

		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %w", err)
		}
		eventID, err := artifact.MintULIDAt(now, deps.Entropy)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: cannot mint event id: %w", err)
		}
		note := ""
		if len(overridden) > 0 {
			note = "retired-unacked: " + strings.Join(overridden, ", ")
		}
		// The COMMITTED event's own Version field is legalityVersion, not
		// retiredVersion — see the deprecate handler's own comment above.
		ev := eventDoc{
			Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
			Subject: in.ID, Transition: fold.TRetire, State: eventReceiptState(retireEvaluation), Version: legalityVersion,
			Actor: eventActorFrom(resolved, actor.System),
			At:    now.UTC().Format(time.RFC3339),
			Note:  note,
		}
		raw, merr := yaml.Marshal(ev)
		if merr != nil {
			return nil, "", fmt.Errorf("contract retire: cannot encode event: %w", merr)
		}
		files := []space.FileWrite{{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw}}

		gated := len(overridden) > 0
		req := deps.buildRequest([]string{in.ID}, files, "contract-retire", gated)
		result, err := deps.submit(ctx, req, "contract retire", []string{in.ID})
		return contractRetireWithObserved(result, precondition), "", err
	}
}

// contractRetireResult is the write funnel's own submitResult widened with
// spec 03-observed-consumption.md's observed-consumer naming — §8 row 7
// (the epic's AC5): internal/cli prints that line to stderr, and this
// surface has no stderr, so it travels as structured data instead. Same
// fact, same words, the door-appropriate channel.
//
// submitResult is EMBEDDED, so every §7.7 field a write tool already
// returns marshals at exactly the same place it did before; this adds one
// optional key and removes none. `body` was the other candidate and is
// wrong for it: HandlerFunc's own contract reserves that return for "the
// artifact body verbatim", and a write tool has none.
type contractRetireResult struct {
	submitResult
	// ObservedNotice is validate.ObservedConsumptionNotice's one line —
	// the SAME renderer internal/cli calls, never a second formatting of
	// the same facts. Omitted entirely when nothing is observed (§8
	// criterion 4: a plain retire's result shape is unchanged).
	ObservedNotice string `json:"observed_consumption,omitempty"`
	// ObservedConsumers is the machine half for an agent that would rather
	// branch than parse prose — this IS an agent-to-agent surface.
	ObservedConsumers []validate.ObservedConsumer `json:"observed_consumers,omitempty"`
}

// contractRetireWithObserved widens a retire's submit result with the
// observed-consumption facts, and is a no-op in both the cases that matter:
// nothing observed (the overwhelmingly common path), and a funnel result
// this function cannot recognise.
//
// The type assertion is deliberately non-fatal. WriteDeps.submit returns a
// submitResult on its PARTIAL-WRITE path too — result non-nil ALONGSIDE a
// non-nil error — and that is exactly the case where a caller most needs
// the PR/stage facts, so a failed assertion returns the original result
// untouched rather than dropping it for the sake of an advisory.
func contractRetireWithObserved(result any, precondition validate.RetirePrecondition) any {
	notice := validate.ObservedConsumptionNotice(precondition)
	if notice == "" {
		return result
	}
	sr, ok := result.(submitResult)
	if !ok {
		return result
	}
	return contractRetireResult{
		submitResult:      sr,
		ObservedNotice:    notice,
		ObservedConsumers: precondition.Observed,
	}
}

func contractBuildRetirePrecondition(mirrorDir string, manifest space.Manifest, contractID, contractVersion string, override, actorIsHuman bool, now time.Time) (validate.RetirePrecondition, error) {
	all, err := readAllEvents(mirrorDir)
	if err != nil {
		return validate.RetirePrecondition{}, err
	}

	announcementID, sunset, err := contractFindDeprecationAnnouncement(mirrorDir, contractID, contractVersion)
	if err != nil {
		return validate.RetirePrecondition{}, err
	}

	memb := membership(manifest)
	var ackedSystems map[string]bool
	var reminderCount int
	if announcementID != "" {
		events := foldEvents(all, announcementID)
		result := fold.Fold(fold.KindAnnouncement, fold.Envelope{ID: announcementID, Kind: fold.KindAnnouncement}, events, memb)
		ackedSystems = map[string]bool{}
		for _, s := range result.AckedRecipients() {
			ackedSystems[s] = true
		}
		for _, ev := range all {
			if ev.Subject == announcementID && ev.Transition == fold.TNote {
				reminderCount++
			}
		}
	}

	// Edge 1 (04-per-version-lifecycle.md §4, AC-9): the retire gate's
	// registered-consumer scan is scoped to the MAJOR being retired — see
	// internal/cli's own contractBuildRetirePrecondition for the full
	// rationale (ADR-001: mirrored here, never imported). contractVersion
	// has already been resolved (never "" at this call site), so an
	// unparseable value here would be this function's own bug; fail CLOSED
	// all the same.
	major, err := version.Major(contractVersion)
	if err != nil {
		return validate.RetirePrecondition{}, fmt.Errorf("mcp: %s: %w", contractVersion, err)
	}
	consumerSystems, err := cache.FindRegisteredConsumersForMajor(mirrorDir, contractID, major)
	if err != nil {
		return validate.RetirePrecondition{}, err
	}

	consumers := make([]validate.RegisteredConsumer, 0, len(consumerSystems))
	for sys := range consumerSystems {
		left := memb(sys) == fold.MembershipLeft
		consumers = append(consumers, validate.RegisteredConsumer{System: sys, Acked: ackedSystems[sys], Left: left})
	}
	sort.Slice(consumers, func(i, j int) bool { return consumers[i].System < consumers[j].System })

	return validate.RetirePrecondition{
		Consumers:    consumers,
		SunsetPassed: sunset != "" && contractSunsetPassed(sunset, now),
		HasReminder:  reminderCount > 0,
		ActorIsHuman: actorIsHuman,
		Override:     override,
		Observed:     contractObservedConsumers(mirrorDir, manifest, contractID, ackedSystems),
	}, nil
}

// contractObservedConsumers resolves spec 03-observed-consumption.md's
// `observed` set — see internal/cli's own contractObservedConsumers for the
// full rationale (ADR-001: mirrored here, never imported), including why it
// is unscoped by major where the declared half above is scoped, and why
// this is the one call in the retire path that fails OPEN: the set gates
// nothing (§8 criterion 2), so an unreadable mirror must never block a
// retire the declared set already cleared.
//
// §8 row 7 is the reason this exists on this surface at all:
// `a2a_contract_retire` is a door onto the same rule, and a rule only the
// CLI can express is the B22 shape the epic's AC5 exists to prevent — which
// includes the already-acked filter below: a rule that nags on one door and
// not the other is the same defect wearing different clothes.
func contractObservedConsumers(mirrorDir string, manifest space.Manifest, contractID string, acked map[string]bool) []validate.ObservedConsumer {
	observed, err := cache.FindObservedConsumers(mirrorDir, contractID, manifest)
	if err != nil {
		return nil
	}
	out := make([]validate.ObservedConsumer, 0, len(observed))
	for _, o := range observed {
		if acked[o.System] {
			continue
		}
		out = append(out, validate.ObservedConsumer{System: o.System, Version: o.Version, Packages: o.Packages})
	}
	return out
}

func contractSunsetPassed(sunset string, now time.Time) bool {
	t, err := time.Parse("2006-01-02", sunset)
	if err != nil {
		return false
	}
	return now.UTC().After(t)
}

func contractFindDeprecationAnnouncement(mirrorDir, contractID, version string) (id, sunset string, err error) {
	matches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "exchanges", "XA-*.md"))
	if err != nil {
		return "", "", err
	}
	want := contractID + "@" + version
	for _, m := range matches {
		raw, rerr := readBoundedFile(m, maxMirrorEventBytes)
		if rerr != nil {
			return "", "", rerr
		}
		fm, ferr := artifact.ParseFrontmatter(raw)
		if ferr != nil {
			continue
		}
		var probe struct {
			ID         string `yaml:"id"`
			Deprecates string `yaml:"deprecates"`
			ValidUntil string `yaml:"valid_until"`
		}
		if yaml.Unmarshal(fm.YAML, &probe) == nil && probe.Deprecates == want {
			return probe.ID, probe.ValidUntil, nil
		}
	}
	return "", "", nil
}

// contractDeprecateAddressees is F3/T4 (AC-971.1, AC-971.2): who a
// deprecation announcement is addressed to. Computed from the SAME D-022
// registered-consumer query the retire precondition reads
// (cache.FindRegisteredConsumers — P4 wave 5 decision 8: this scan moved
// down to internal/cache, ONE home shared with internal/cli, so "who
// blocks retire" and "who was told" stay one query rather than two that can
// silently disagree). UNSCOPED by major, deliberately — Edge 1
// (04-per-version-lifecycle.md §4) scopes the RETIRE gate only; a
// deprecation announcement still addresses every registered consumer
// regardless of major, unchanged from before this wave. Sorted
// (cache.FindRegisteredConsumers returns a map), deduped, and excludes the
// contract's OWN `from` system — a producer does not address itself.
//
// An EMPTY registered-consumer set (nobody has adopted this contract yet)
// falls back to fallback (the descriptor's own authoring-time `to:`):
// schemas/envelope/v1/base.schema.json's own `to` requires either
// `minItems: 1` or the literal `"all"`, so `to: []` is refused by V2
// (REF-006), and is not an option. A fallback that is ALSO empty/nil is
// refused with an actionable error rather than silently authoring an
// invalid `to: null` announcement. Mirrors internal/cli's own copy —
// ADR-001: internal/mcp never imports internal/cli.
func contractDeprecateAddressees(mirrorDir, contractID, from string, fallback []string) ([]string, error) {
	consumers, err := cache.FindRegisteredConsumers(mirrorDir, contractID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(consumers))
	for sys := range consumers {
		if sys == from {
			continue
		}
		out = append(out, sys)
	}
	sort.Strings(out)
	if len(out) > 0 {
		return out, nil
	}
	if len(fallback) == 0 {
		return nil, fmt.Errorf("mcp: %s has no registered consumers and no fallback recipients (descriptor `to:` is empty) — nobody to address the deprecation to", contractID)
	}
	return fallback, nil
}

// ContractDiffInput is a2a_contract_diff's structured (read-only) input.
type ContractDiffInput struct {
	// Action is a2a_contract's own discriminator — never read here; see
	// ContractNewInput's identical field for why it exists. NOT part of
	// ContractDiffRequest below — this struct is built into that one
	// FIELD BY FIELD (not by a bare struct conversion) specifically so
	// this input struct's own shape can carry a field ContractDiffRequest
	// does not.
	Action string `json:"action,omitempty"`
	Space  string `json:"space,omitempty"`
	ID     string `json:"id"`
	V1     string `json:"v1"`
	V2     string `json:"v2"`
}

func newContractDiffHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		if deps.Inspection == nil {
			return nil, "", fmt.Errorf("contract diff: P6 service is not configured")
		}
		var in ContractDiffInput
		if err := decodeStrict(args, &in, "contract diff", 0); err != nil {
			return nil, "", err
		}
		if in.ID == "" || in.V1 == "" || in.V2 == "" || in.V1 == in.V2 {
			return nil, "", fmt.Errorf("contract diff: distinct id, v1 and v2 are required")
		}
		result, err := deps.Inspection.DiffContract(ctx, ContractDiffRequest{Space: in.Space, ID: in.ID, V1: in.V1, V2: in.V2})
		if err != nil {
			return nil, "", fmt.Errorf("contract diff: %w", err)
		}
		return result, "", nil
	}
}

// ContractVerifyExportInput is a2a_contract_verify_export's structured
// (read-only) input.
type ContractVerifyExportInput struct {
	// Action is a2a_contract's own discriminator — never read here; see
	// ContractDiffInput's identical field/comment for why it exists and
	// why ContractVerifyExportRequest below is built field by field rather
	// than by a bare struct conversion.
	Action string `json:"action,omitempty"`
	Space  string `json:"space,omitempty"`
	Local  string `json:"local"`
	Ref    string `json:"ref"`
}

func newContractVerifyExportHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		if deps.Inspection == nil {
			return nil, "", fmt.Errorf("contract verify-export: P6 service is not configured")
		}
		var in ContractVerifyExportInput
		if err := decodeStrict(args, &in, "contract verify-export", 0); err != nil {
			return nil, "", err
		}
		if in.Local == "" || in.Ref == "" {
			return nil, "", fmt.Errorf("contract verify-export: local and ref are required")
		}
		result, err := deps.Inspection.VerifyContractExport(ctx, ContractVerifyExportRequest{Space: in.Space, Local: in.Local, Ref: in.Ref})
		if err != nil {
			return nil, "", fmt.Errorf("contract verify-export: %w", err)
		}
		return result, "", nil
	}
}

// ContractVerifyPublishedRow is one provided contract's verify-published
// outcome — mirrors internal/cli's own ContractVerifyPublishedRow (ADR-001:
// internal/mcp never imports internal/cli). Status is never a fourth
// Verdict (spec 07 §11's own correction, tracking `no-silent-yes-2026-08`
// D9): matched/drifted/not-published-yet are row-local vocabulary, and
// "unmeasured" is validate.SeverityUnmeasured carried BY VALUE.
type ContractVerifyPublishedRow struct {
	ID string `json:"id"`
	// Version is the version RESOLVED from the published descriptor
	// (US-2/AC-2) — empty exactly when Status is "not-published-yet".
	Version string `json:"version,omitempty"`
	Status  string `json:"status"`
	// Local is the project-relative subject this row was checked against —
	// empty when no override was given for this id (Status is then
	// "unmeasured", never a silent skip).
	Local string `json:"local,omitempty"`
	// Detail carries the reason behind an "unmeasured" row.
	Detail string `json:"detail,omitempty"`
}

// ContractVerifyPublishedResult is the full aggregate report (AC-8's
// --json shape). Total is the run's own DENOMINATOR (US-3/AC-3).
type ContractVerifyPublishedResult struct {
	System string                       `json:"system"`
	Space  string                       `json:"space"`
	Total  int                          `json:"total"`
	Rows   []ContractVerifyPublishedRow `json:"rows"`
}

// ContractVerifyPublishedInput is a2a_contract action=verify-published's
// structured (read-only, aggregate) input. LocalSubjects mirrors
// internal/cli's repeatable `--local <id>=<path>` flag as a map — MCP has no
// repeatable-flag idiom, so one JSON object carries every per-contract
// override.
//
// The JSON key is `local_subjects`, NOT `local`, and the difference is
// load-bearing rather than cosmetic. a2a_contract is a GROUPED tool:
// groupedSchema (tools.go) publishes ONE properties map for every action, so
// two actions cannot disagree about a name's type. `local` is already
// published as a STRING for action=verify-export ("the local export path to
// verify"). Decoding the same key as an object here would advertise a shape
// this action cannot honour — an agent reads the schema, sends the string it
// was promised, and is refused for obeying it. That is exactly the class
// this epic's P12 exists to end, and P12's own gate does NOT catch it:
// check-mcp-schema-decodable.sh compares NAMES (is every declared property
// decoded?), and `local` is both declared and decoded, so the collision is
// invisible to it. Filed in docs/validator-backlog.md.
type ContractVerifyPublishedInput struct {
	// Action is a2a_contract's own discriminator — never read here; see
	// ContractNewInput's identical field for why it exists.
	Action        string            `json:"action,omitempty"`
	Space         string            `json:"space,omitempty"`
	LocalSubjects map[string]string `json:"local_subjects,omitempty"`
}

// newContractVerifyPublishedHandler AGGREGATES P2's comparison — every
// row's verdict comes from deps.Inspection.VerifyContractExport, the SAME
// digest logic `contract verify-export`/action=verify-export already run —
// it is never re-implemented here (spec 07: "the record it comes from is a
// consumer who wrote 137 lines of bash to substitute for it").
//
// Space selection reuses resolveWriteSpace (the SAME per-request `space`
// resolution deprecate/retire/adopt/activate already use) to find THIS
// space's own mirror directory for enumeration; deps.Inspection's own
// per-space routing (its Space field) is independent — mirrors
// internal/cli's own single-space `runVerifyPublished` limitation exactly:
// this handler covers ONE resolved space per call. See this phase's
// Deviations report for the multi-space (every connected space in one
// call) wiring gap on both surfaces.
func newContractVerifyPublishedHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		if deps.Inspection == nil {
			return nil, "", fmt.Errorf("contract verify-published: P6 service is not configured")
		}
		var in ContractVerifyPublishedInput
		if err := decodeStrict(args, &in, "contract verify-published", 0); err != nil {
			return nil, "", err
		}

		resolvedWrite, err := resolveWriteSpace(deps.WriteDeps, in.Space, nil)
		if err != nil {
			return nil, "", fmt.Errorf("contract verify-published: %w", err)
		}

		if _, statErr := os.Stat(resolvedWrite.MirrorDir); statErr != nil {
			return nil, "", fmt.Errorf("contract verify-published: read this space's synced mirror at %s — run `a2a sync` first: %w", resolvedWrite.MirrorDir, statErr)
		}
		if _, headErr := space.ResolveContractPublicationCandidateCommit(ctx, resolvedWrite.MirrorDir); headErr != nil {
			return nil, "", fmt.Errorf("contract verify-published: resolve this space's mirror HEAD — run `a2a sync` to refresh it (it looks stale or was never synced): %w", headErr)
		}

		rows, err := contractVerifyPublishedRows(ctx, resolvedWrite.OwnSystem, resolvedWrite.MirrorDir, deps.Inspection, in.Space, in.LocalSubjects)
		if err != nil {
			return nil, "", fmt.Errorf("contract verify-published: %w", err)
		}
		return ContractVerifyPublishedResult{
			System: resolvedWrite.OwnSystem, Space: resolvedWrite.SpaceID,
			Total: len(rows), Rows: rows,
		}, "", nil
	}
}

// contractVerifyPublishedRows enumerates ownSystem's `provides/*` tree in
// mirrorDir and builds one row per contract descriptor found there — see
// internal/cli's own contractVerifyPublishedRowsFor for the full rationale
// (ADR-001: mirrored here, never imported). A descriptor with no recorded
// `version:` is "not-published-yet" and is never passed to
// VerifyContractExport, which has no version to compare against.
func contractVerifyPublishedRows(ctx context.Context, ownSystem, mirrorDir string, inspection ContractInspectionOperations, spaceID string, overrides map[string]string) ([]ContractVerifyPublishedRow, error) {
	layout, err := space.NewLayout(ownSystem)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(mirrorDir, ownSystem, "provides"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Nothing provided yet — a legitimate zero-row state (US-3).
			return nil, nil
		}
		return nil, err
	}
	slugs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(mirrorDir, layout.ProvidesContract(entry.Name()))); statErr != nil {
			continue // no contract.md under this slug — not a provided contract
		}
		slugs = append(slugs, entry.Name())
	}
	sort.Strings(slugs)

	rows := make([]ContractVerifyPublishedRow, 0, len(slugs))
	for _, slug := range slugs {
		id, _, ok := space.ContractForPath(layout.ProvidesContract(slug))
		if !ok {
			continue
		}
		_, probe, _, _, derr := contractReadDescriptor(mirrorDir, id)
		if derr != nil {
			return nil, fmt.Errorf("%s: %w", id, derr)
		}

		row := ContractVerifyPublishedRow{ID: id}
		if probe.Version == "" {
			row.Status = "not-published-yet"
			rows = append(rows, row)
			continue
		}

		version := contractCanonicalVersion(probe.Version)
		row.Version = version

		local, hasLocal := overrides[id]
		if !hasLocal || local == "" {
			row.Status = string(validate.SeverityUnmeasured)
			row.Detail = "no local subject given for " + id + " — set local[\"" + id + "\"]"
			rows = append(rows, row)
			continue
		}
		row.Local = local

		verify, verifyErr := inspection.VerifyContractExport(ctx, ContractVerifyExportRequest{Space: spaceID, Local: local, Ref: id + "@" + version})
		if verifyErr != nil {
			row.Status = string(validate.SeverityUnmeasured)
			row.Detail = verifyErr.Error()
			rows = append(rows, row)
			continue
		}
		// verify.Outcome already carries the D9-mapped three-outcome
		// vocabulary (contract.ExportVerification), passed through
		// verbatim — never reclassified here.
		row.Status = verify.Outcome
		rows = append(rows, row)
	}
	return rows, nil
}

// ContractAdoptInput is a2a_contract action=adopt's structured input —
// the MCP twin of `a2a contract adopt` (internal/cli's runAdopt).
type ContractAdoptInput struct {
	// Action is a2a_contract's own discriminator — never read here; see
	// ContractNewInput's identical field for why it exists.
	Action string `json:"action,omitempty"`
	Space  string `json:"space,omitempty"`
	ID     string `json:"id"`
	Major  int    `json:"major,omitempty"`
	Note   string `json:"note,omitempty"`
}

// newContractAdoptHandler registers this system as a CONSUMER of another
// system's contract: it writes the dependency into <own-system>/
// consumes.yaml (§5.2.3, D-022 — the space-visible registry a producer's
// retire has to wait for) and submits it through the same write funnel as
// every other mutation. Mirrors internal/cli's runAdopt exactly; the
// duplication is ADR-001's (internal/mcp never imports internal/cli).
func newContractAdoptHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractAdoptInput
		if err := decodeStrict(args, &in, "contract adopt", 0); err != nil {
			return nil, "", err
		}
		parsed, perr := artifact.ParseID(in.ID)
		if perr != nil || parsed.Prefix != "XC" {
			return nil, "", fmt.Errorf("contract adopt: %q is not a contract id (XC-<system>-<slug>)", in.ID)
		}
		if parsed.System == deps.OwnSystem {
			return nil, "", fmt.Errorf("contract adopt: %s is this system's OWN contract — the registry records what you consume from OTHERS (§5.2.3)", in.ID)
		}

		// Resolve the target space BEFORE the first deps.MirrorDir read below
		// — see the deprecate handler's identical comment.
		resolvedWrite, err := resolveWriteSpace(deps.WriteDeps, in.Space, nil)
		if err != nil {
			return nil, "", fmt.Errorf("contract adopt: %w", err)
		}
		deps := deps
		deps.WriteDeps = resolvedWrite

		// The descriptor is read UNCONDITIONALLY now, not only when major
		// is 0, because the adoptability refusal below must not be skippable
		// by passing an explicit major. That is exactly how this surface came
		// to be missing it: the only descriptor read sat inside the
		// major-resolution branch, so a caller supplying `major` never
		// touched the contract's own declaration.
		//
		// A read failure does NOT refuse here — it skips the check and falls
		// through to the existing, actionable "sync first" message below when
		// the major actually needs resolving, mirroring internal/cli's
		// runAdopt so the two surfaces fail the same way on an unsynced
		// mirror.
		_, adoptProbe, _, _, readErr := contractReadDescriptor(deps.MirrorDir, in.ID)
		if readErr == nil && adoptProbe.XBinding.nonAdoptable() {
			return nil, "", fmt.Errorf("contract adopt: %s declares itself non-adoptable (x_binding) — nobody may pin it", in.ID)
		}

		major := in.Major
		if major == 0 {
			if readErr != nil {
				return nil, "", fmt.Errorf("contract adopt: cannot read %s from the local mirror — sync first, or pass major: %w", in.ID, readErr)
			}
			v, verr := contractParseSemver(adoptProbe.Version)
			if verr != nil {
				return nil, "", fmt.Errorf("contract adopt: %s has no usable published version (%q): %w", in.ID, adoptProbe.Version, verr)
			}
			major = v[0]
		}

		layout, lerr := space.NewLayout(deps.OwnSystem)
		if lerr != nil {
			return nil, "", fmt.Errorf("contract adopt: %w", lerr)
		}
		relPath := layout.ConsumesYAML()

		registry := space.Consumes{Schema: "consumes/v1", System: deps.OwnSystem, Dependencies: []space.Dependency{}}
		if raw, rerr := readBoundedFile(filepath.Join(deps.MirrorDir, relPath), maxMirrorEventBytes); rerr == nil {
			parsedRegistry, cerr := space.ParseConsumes(raw)
			if cerr != nil {
				return nil, "", fmt.Errorf("contract adopt: cannot parse %s: %w", relPath, cerr)
			}
			registry = parsedRegistry
			if registry.Schema == "" {
				registry.Schema = "consumes/v1"
			}
			if registry.System == "" {
				registry.System = deps.OwnSystem
			}
		}

		updated, changed := contractUpsertDependency(registry, space.Dependency{
			Contract: in.ID, Major: major,
			Since: deps.Now().UTC().Format("2006-01-02"),
			Note:  in.Note,
		})
		if !changed {
			return contractAdoptResult{ID: in.ID, Major: major, AlreadyRegistered: true}, "", nil
		}

		raw, merr := yaml.Marshal(updated)
		if merr != nil {
			return nil, "", fmt.Errorf("contract adopt: cannot encode %s: %w", relPath, merr)
		}

		req := deps.buildRequest([]string{in.ID}, []space.FileWrite{{Path: relPath, Content: raw}}, "contract-adopt", false)
		result, err := deps.submit(ctx, req, "contract adopt", []string{in.ID})
		return result, "", err
	}
}

// contractAdoptResult is the idempotent-no-op shape (a real write returns
// the funnel's own submit result, like every other write handler).
type contractAdoptResult struct {
	ID                string `json:"id"`
	Major             int    `json:"major"`
	AlreadyRegistered bool   `json:"already_registered"`
}

// contractUpsertDependency adds or updates dep in registry, keeping the
// list sorted by contract id, and reports changed=false for the
// idempotent re-run (same contract, same major).
func contractUpsertDependency(registry space.Consumes, dep space.Dependency) (space.Consumes, bool) {
	for i, existing := range registry.Dependencies {
		if existing.Contract != dep.Contract {
			continue
		}
		if existing.Major == dep.Major && (dep.Note == "" || existing.Note == dep.Note) {
			return registry, false
		}
		// `since` records when the dependency was DECLARED: only a real
		// major change restarts it (mirrors internal/cli's own copy).
		if registry.Dependencies[i].Major != dep.Major {
			registry.Dependencies[i].Major = dep.Major
			registry.Dependencies[i].Since = dep.Since
		}
		if dep.Note != "" {
			registry.Dependencies[i].Note = dep.Note
		}
		return registry, true
	}
	registry.Dependencies = append(registry.Dependencies, dep)
	sort.Slice(registry.Dependencies, func(i, j int) bool {
		return registry.Dependencies[i].Contract < registry.Dependencies[j].Contract
	})
	return registry, true
}

// --- contract activate (P5 AC1, specs/05-declared-nature.md's 2026-08-10
// amendments) ---------------------------------------------------------

// contractActivationEntry is `a2a_contract` action=activate's own
// `activation` object — schemas/event/v2/event.schema.json's own
// `{version, status, satisfies[], note?}`. `Status` carries no input field:
// this verb always writes the literal `live`, the schema's own enum has no
// other member yet. Mirrors internal/cli's contractActivationEntry exactly
// (ADR-001: internal/mcp never imports internal/cli).
type contractActivationEntry struct {
	Version   string   `yaml:"version"`
	Status    string   `yaml:"status"`
	Satisfies []string `yaml:"satisfies"`
	Note      string   `yaml:"note,omitempty"`
}

// contractActivateEventDoc is `a2a_contract` action=activate's own event/v2
// wire shape — a FILE-LOCAL copy of eventdoc.go's eventDoc carrying only the
// fields an `activate` event needs, plus `activation`. Mirrors internal/cli's
// contractActivateEventDoc exactly.
type contractActivateEventDoc struct {
	Schema     string                  `yaml:"schema"`
	Event      string                  `yaml:"event"`
	Space      string                  `yaml:"space"`
	Subject    string                  `yaml:"subject"`
	Transition string                  `yaml:"transition"`
	Actor      eventActor              `yaml:"actor"`
	At         string                  `yaml:"at"`
	Note       string                  `yaml:"note,omitempty"`
	Activation contractActivationEntry `yaml:"activation"`
}

// contractActivateEventSchema mirrors internal/cli's cmd_lifecycle.go
// lifecycleEventSchema (ADR-001: internal/mcp never imports internal/cli):
// a space whose `min_binary_version` (floor) is at or above
// contract.ContractPublicationFloor authors event/v2; below it, event/v1.
// An unparseable or absent floor fails CLOSED to event/v1, the same
// conservative direction version.OlderThan's own doc comment names.
//
// CORRECTED 2026-08-20 (one-answer-2026-08 P1, spec 01 §8 row 8). This
// comment used to say "no general event/v2 authoring path exists here yet
// ... tools_lifecycle.go does not", and that sentence was the reason
// `verify`/`close` on this surface authored event/v1 unconditionally while
// internal/cli authored event/v2 above the floor — an MCP-driven agent
// could not record a per-criterion judgement at all, so REF-023 (the
// completeness rule) never fired on anything it wrote. tools_lifecycle.go
// now HAS its own floor selector (`lifecycleEventSchema`, named after
// internal/cli's own function for the analogous job) and its own
// `verdicts` input, so the sentence is false and is corrected rather than
// left to drift.
//
// `activate` still carries its own selector rather than sharing one: it is
// the only verb here with no event/v1 shape to fall back to at all (see
// newContractActivateHandler's own doc comment), so its floor check is a
// hard precondition where the lifecycle verbs' is a choice of generation.
// Two selectors, one constant — `contract.ContractPublicationFloor` is read
// by both and by internal/cli, so the three cannot disagree about where the
// line is.
func contractActivateEventSchema(floor string) string {
	belowFloor, err := version.OlderThan(floor, contract.ContractPublicationFloor)
	if err != nil || belowFloor {
		return "event/v1"
	}
	return "event/v2"
}

// ContractActivateInput is a2a_contract's action=activate structured input.
type ContractActivateInput struct {
	// Action is a2a_contract's own discriminator — never read here; see
	// ContractNewInput's identical field for why it exists.
	Action    string     `json:"action,omitempty"`
	Space     string     `json:"space,omitempty"`
	ID        string     `json:"id"`
	Version   string     `json:"version"`
	Satisfies []string   `json:"satisfies"`
	Note      string     `json:"note,omitempty"`
	Actor     ActorInput `json:"actor,omitempty"`
}

// newContractActivateHandler mirrors internal/cli's runActivate exactly
// (ADR-001: internal/mcp never imports internal/cli) — P5's AC1 discharge
// half (specs/05-declared-nature.md's 2026-08-10 amendments): the
// producer's own act that moves a published version's `x_operational[]`
// items toward `ready`, authored as an `activate` event/v2 event (never a
// descriptor edit — a descriptor is immutable after publication). Like
// `adopt` above, this write is never legality-checked through
// fold.EvaluateCandidate: `activation` is a side fact about a published
// version's operational readiness, not a §3.4 lifecycle state transition,
// so internal/fold's transition table has no `activate` row — runActivate's
// own doc comment gives the full reasoning.
//
// Four refusals, all enforced here in the SAME order as internal/cli's own
// (none may silently fall away between the two surfaces — this wave's own
// brief):
//
//  1. The target contract must be owned by THIS system (embedded in its
//     own id, §3.3) — only the producer may declare its own operational
//     readiness. Checked first because, unlike `adopt` (which refuses the
//     opposite direction), this write is never legality-checked through
//     fold, so nothing else in this path would otherwise stop a system
//     from authoring an activation event about a contract it does not own.
//  2. Below contract.ContractPublicationFloor this verb refuses outright:
//     `activation` has no event/v1 shape at all, so there is no legal
//     fallback the way verify/close have one.
//  3. The named version must actually have been published — activation
//     names a published version's readiness, it is not a way to publish
//     one.
//  4. `satisfies` may only name an item already declared in the
//     descriptor's own `x_operational[]` (any state) — activating an
//     undeclared item would let a producer route around ever declaring
//     the field at all (the exact P-1 failure this phase exists to
//     close).
func newContractActivateHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractActivateInput
		if err := decodeStrict(args, &in, "contract activate", 0); err != nil {
			return nil, "", err
		}
		if in.ID == "" || in.Version == "" || len(in.Satisfies) == 0 {
			return nil, "", fmt.Errorf("contract activate: id, version and satisfies (at least one item) are required")
		}

		parsed, perr := artifact.ParseID(in.ID)
		if perr != nil || parsed.Prefix != "XC" {
			return nil, "", fmt.Errorf("contract activate: %q is not a contract id (XC-<system>-<slug>)", in.ID)
		}
		// Refusal 1 — ownership.
		if parsed.System != deps.OwnSystem {
			return nil, "", fmt.Errorf("contract activate: %s is not owned by this system (%s) — only the producer may declare its own operational readiness", in.ID, deps.OwnSystem)
		}

		// Resolve the target space BEFORE the floor read just below (Refusal
		// 2 reads deps.Manifest.MinBinaryVersion, and that floor is per-space)
		// — see the deprecate handler's identical comment.
		resolvedWrite, err := resolveWriteSpace(deps.WriteDeps, in.Space, nil)
		if err != nil {
			return nil, "", fmt.Errorf("contract activate: %w", err)
		}
		deps := deps
		deps.WriteDeps = resolvedWrite

		// Refusal 2 — the floor.
		eventSchema := contractActivateEventSchema(deps.Manifest.MinBinaryVersion)
		if eventSchema != "event/v2" {
			return nil, "", fmt.Errorf(
				"contract activate: requires this space's min_binary_version to be at or above %s (event/v2, `activation` has no event/v1 shape); this space's floor is %q",
				contract.ContractPublicationFloor, deps.Manifest.MinBinaryVersion)
		}

		// Name the CONDITION, not the file that happened to be missing —
		// contractReadDescriptor's own error is a raw open() failure carrying
		// an absolute cache path, which tells an operator nothing they can
		// act on. Mirrors internal/cli's runActivate fix for the identical
		// gap (epic-backlog B31; ADR-001's duplication had left this surface
		// behind).
		_, probe, _, _, err := contractReadDescriptor(deps.MirrorDir, in.ID)
		if err != nil {
			return nil, "", fmt.Errorf(
				"contract activate: cannot read %s from the local mirror — run `a2a sync` first, or check the contract has been published: %w", in.ID, err)
		}

		allEvents, err := readAllEvents(deps.MirrorDir)
		if err != nil {
			return nil, "", fmt.Errorf("contract activate: %w", err)
		}
		canonicalVersion := contractCanonicalVersion(in.Version)
		published := false
		for _, v := range contractPublishedVersions(allEvents, in.ID) {
			if v.String() == canonicalVersion {
				published = true
				break
			}
		}
		// Refusal 3 — must be a published version.
		if !published {
			return nil, "", fmt.Errorf("contract activate: %s@%s has not been published — activation names a published version's operational readiness, it is not a way to publish one", in.ID, in.Version)
		}

		// Refusal 4 — an undeclared item. Regardless of the item's current
		// state (ready or absent), its NAME must already be present in
		// x_operational[] — see this function's own doc comment.
		declared := map[string]bool{}
		for _, item := range probe.XOperational {
			declared[item.Name] = true
		}
		satisfies := append([]string(nil), in.Satisfies...)
		for _, name := range satisfies {
			if !declared[name] {
				return nil, "", fmt.Errorf("contract activate: %q is not a named item in %s's x_operational[] — declare it there first (even as `state: absent`) before activating it", name, in.ID)
			}
		}

		resolved, actorErr := deps.ResolveActor(in.Actor)
		if actorErr != nil {
			return nil, "", fmt.Errorf("contract activate: %w", actorErr)
		}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("contract activate: %w", err)
		}
		eventID, err := artifact.MintULIDAt(now, deps.Entropy)
		if err != nil {
			return nil, "", fmt.Errorf("contract activate: cannot mint event id: %w", err)
		}

		ev := contractActivateEventDoc{
			Schema: eventSchema, Event: eventID.String(), Space: probe.Space,
			Subject: in.ID, Transition: "activate",
			Actor: eventActorFrom(resolved, deps.OwnSystem),
			At:    now.UTC().Format(time.RFC3339),
			Note:  in.Note,
			Activation: contractActivationEntry{
				Version: canonicalVersion, Status: "live", Satisfies: satisfies,
			},
		}
		raw, merr := yaml.Marshal(ev)
		if merr != nil {
			return nil, "", fmt.Errorf("contract activate: cannot encode event: %w", merr)
		}
		files := []space.FileWrite{{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw}}

		req := deps.buildRequest([]string{in.ID}, files, "contract-activate", false)
		req.OperationKey = operation.ContractActivate(deps.OwnSystem, in.ID, canonicalVersion, satisfies, in.Note)
		result, err := deps.submit(ctx, req, "contract activate", []string{in.ID})
		return result, "", err
	}
}
