// OP-212 (contract lifecycle) + OP-213 (`contract verify-export`) + the
// `contract diff` slice of OP-221 (spec 08 T1). One `a2a contract <sub>`
// verb dispatching new/publish/deprecate/retire/diff/verify-export — the
// same uniform write funnel as cmd_lifecycle.go for every mutating
// sub-verb (auto-merge always on; publish/retire add an advisory PR
// marker only when G1/G2 apply, per this phase's plan Placement
// decisions); diff/verify-export are read-only, no funnel.
//
// This file's only package-level symbols are ContractCommand + its
// NewContractCommand constructor and file-private, uniquely-named helpers
// (contract* prefix) — no shared helper, no package var, per this phase's
// plan Placement decision. It freely reuses cmd_lifecycle.go's own
// file-private helpers (lifecycleLoadEnvelope, lifecycleCheckLegality,
// lifecycleReadAllEvents, lifecycleFoldEvents, lifecycleMembership,
// lifecycleEventDoc, lifecycleArtifactPath, lifecycleDeps, ...) — both
// files are this SAME phase's own output (never P7's), so this is reuse,
// not the cross-file duplication the plan's "disjoint files" rule guards
// against (that rule is about the PARALLEL SIBLING, P7).
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io/fs"
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

// contractEvaluateCandidate assembles the committed prior fold through the
// lifecycle package-local readers, then delegates legality, applicability and
// outcome to fold's sole writer-facing evaluator. It exists here because the
// contract writer must serialize the evaluator receipt as well as inspect the
// verdict; lifecycleCheckLegality intentionally exposes only the latter.
func contractEvaluateCandidate(mirrorDir string, manifest space.Manifest, id string, candidate fold.Event) (fold.CandidateEvaluation, fold.Envelope, error) {
	env, _, err := lifecycleLoadEnvelope(mirrorDir, id)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, err
	}
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return fold.CandidateEvaluation{}, env, err
	}
	membership := lifecycleMembership(manifest)
	prior := fold.NewResult(env.Kind)
	events := lifecycleFoldEvents(all, id)
	if len(events) > 0 {
		prior = fold.Fold(env.Kind, env, events, membership)
	}
	candidate.Subject = id
	return fold.EvaluateCandidate(env.Kind, prior, candidate, env, membership), env, nil
}

func contractReceiptState(evaluation fold.CandidateEvaluation) string {
	if !evaluation.Applicable {
		return ""
	}
	return string(evaluation.Outcome)
}

// xBindingProbe decodes envelope/v2/contract's `x_binding` field
// (specs/05-declared-nature.md, 2026-08-10 amendment): either the bare
// sentinel `none`, or the long form object requiring `artifact_class`,
// `compatibility_status`, `adoptable` and `runtime_pinnable` — the SAME
// value grammar as envelope/v2/work_request's `binding`. A *xBindingProbe
// that is nil means the field is absent entirely: undeclared, distinct
// from a declared `none` (P-1) — callers must check for nil before asking
// anything of the value.
type xBindingProbe struct {
	// Sentinel is set when the field was written as the literal scalar
	// `none` rather than the long-form object.
	Sentinel            bool
	ArtifactClass       string `yaml:"artifact_class"`
	CompatibilityStatus string `yaml:"compatibility_status"`
	Adoptable           *bool  `yaml:"adoptable"`
	RuntimePinnable     *bool  `yaml:"runtime_pinnable"`
}

// UnmarshalYAML distinguishes the two legal shapes x_binding's own schema
// oneOf's: a bare scalar (only "none" is schema-valid, but any other
// scalar is decoded harmlessly rather than erroring here — schema
// validation, not this probe, is what refuses it) or the long-form
// mapping.
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
// `adoptable: false`. Both are refused identically by `a2a contract
// adopt`: the schema's own T2 asymmetry already forces a
// `compatibility_status: none` long form to carry `adoptable: false`, so
// the sentinel and the long form never disagree on this fact. A nil x (the
// field is absent — undeclared) is adoptable, per P-1.
func (x *xBindingProbe) nonAdoptable() bool {
	if x == nil {
		return false
	}
	if x.Sentinel {
		return true
	}
	return x.Adoptable != nil && !*x.Adoptable
}

// contractDescriptorProbe is this file's own minimal decode of a
// contract's descriptor (contract.md) fields (§5.2.1's contract-only
// extensions) — a richer sibling of lifecycleEnvelopeProbe (which only
// carries the base envelope fields every OP-211 verb needs).
type contractDescriptorProbe struct {
	ID string `yaml:"id"`
	// Schema is the descriptor's own envelope generation. It selects the
	// publication profile together with the space's floor, so a shape check
	// that runs before the merge has to be able to read it.
	Schema string `yaml:"schema"`
	// Artifacts is a POINTER so its nil-ness distinguishes "no `artifacts:`
	// key at all" from "an empty inventory" — exactly the distinction
	// contract.BuildCandidateIntent makes when it selects declared-v2 versus
	// legacy-fixed-v1. A plain slice would collapse both to len 0 and make
	// the two refusals indistinguishable.
	Artifacts    *[]yaml.Node `yaml:"artifacts"`
	Space        string       `yaml:"space"`
	From         string       `yaml:"from"`
	To           []string     `yaml:"to"`
	Version      string       `yaml:"version"`
	CompatPolicy string       `yaml:"compat_policy"`
	SchemaFormat string       `yaml:"schema_format"`
	// XBinding is P5's declared-nature field (specs/05-declared-nature.md,
	// 2026-08-10 amendment): what this contract IS and whether it may be
	// adopted or pinned. A nil pointer means the field is absent —
	// `undeclared`, distinct from a declared `none` (P-1) — so this, too,
	// is a pointer rather than a value type.
	XBinding      *xBindingProbe `yaml:"x_binding"`
	GeneratedFrom struct {
		Tool         string `yaml:"tool"`
		SourceDigest string `yaml:"source_digest"`
	} `yaml:"generated_from"`
	// XOperational is P5's AC1 discharge half (specs/05-declared-nature.md,
	// 2026-08-10 amendment): the named operational items this contract
	// declares, whatever their state. A nil slice (the field absent) is
	// itself meaningful — `x_operational` carries no default, and this
	// probe never treats absence as any item being `ready` (P-1) — but
	// this file's only present use of it (`contract activate`'s
	// declared-item refusal, below) only asks "is this NAME present at
	// all", so a nil vs empty slice does not need its own pointer here the
	// way XBinding's undeclared-vs-none distinction does.
	XOperational []xOperationalItemProbe `yaml:"x_operational"`
	// Thread is the contract's own §3.8 thread id — spec 46 §T1 R2:
	// `contract deprecate`'s linked announcement is DERIVED from this
	// contract, so it inherits the SAME thread rather than minting or
	// leaving one unfilled.
	Thread string `yaml:"thread"`
}

// xOperationalItemProbe decodes one entry of envelope/v2/contract's
// `x_operational[]` (specs/05-declared-nature.md's AC1 discharge half) —
// {name, state, eta?}.
type xOperationalItemProbe struct {
	Name  string `yaml:"name"`
	State string `yaml:"state"`
	ETA   string `yaml:"eta"`
}

// contractReadDescriptor reads and parses a contract's committed
// descriptor file, returning its raw frontmatter (for in-place field
// edits), decoded probe, and mirror-relative directory (the contract's
// own <system>/provides/<slug>/ root, under which schema/ and fixtures/
// live).
func contractReadDescriptor(mirrorDir, id string) (fm artifact.Frontmatter, probe contractDescriptorProbe, relPath, relDir string, err error) {
	parsed, perr := artifact.ParseID(id)
	if perr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("cli: %s: %w", id, perr)
	}
	if parsed.Prefix != "XC" {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("cli: %s: not a contract id (XC-)", id)
	}
	layout, lerr := space.NewLayout(parsed.System)
	if lerr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", lerr
	}
	relPath = layout.ProvidesContract(parsed.Slug)
	relDir = path.Dir(relPath)
	raw, rerr := readBoundedFile(filepath.Join(mirrorDir, relPath), maxMirrorEventBytes)
	if rerr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("cli: cannot read %s: %w", id, rerr)
	}
	fm, ferr := artifact.ParseFrontmatter(raw)
	if ferr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("cli: %s: %w", id, ferr)
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("cli: %s: cannot decode descriptor: %w", id, err)
	}
	return fm, probe, relPath, relDir, nil
}

// contractAddFrontmatterFields decodes raw's frontmatter into a generic
// map, sets every key in fields (adding new keys, not just overwriting
// existing ones — unlike template.Render's own in-place applyFills), and
// re-serializes. Used where a canonical template carries a field only as
// a commented-out example (announcement.md's ack_requested/deprecates/
// valid_until) that a verb still needs to set for real.
func contractAddFrontmatterFields(raw []byte, fields map[string]any) ([]byte, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("cli: %w", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		return nil, fmt.Errorf("cli: cannot decode frontmatter: %w", err)
	}
	for k, v := range fields {
		doc[k] = v
	}
	newYAML, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("cli: cannot encode frontmatter: %w", err)
	}
	return artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: newYAML, Body: fm.Body}), nil
}

// --- semver (stdlib-only, own minimal copy — internal/space's own
// parseVersion/versionOlderThan are unexported to that package) ----------

type contractSemver [3]int

func contractParseSemver(s string) (contractSemver, error) {
	var out contractSemver
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("cli: %q is not a valid semver (major.minor.patch)", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("cli: %q is not a valid semver (major.minor.patch)", s)
		}
		out[i] = n
	}
	return out, nil
}

func (v contractSemver) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// contractCanonicalVersion reformats v through contractSemver, so that two
// spellings of the same version ("1.0.0" and "01.0.0") produce the
// IDENTICAL string — fold.Result.Versions is a map[string]State keyed on
// the raw version string with no canonicalization of its own (P4,
// 04-per-version-lifecycle.plan.md; internal/fold is off-limits here, so
// this is the caller's own half of keeping that map's keys consistent).
// `contract publish` already writes a canonical spelling unconditionally
// (newVersion.String(), always a parsed contractSemver) — the ONLY
// caller-introduced non-canonical spelling is an explicit `--version`
// flag passed straight to deprecate/retire (contractResolveVersionOrRefuse
// returns it VERBATIM, by its own doc comment). Fails open (returns v
// unchanged) on an unparseable v — never itself the refusal path;
// contractResolveVersionOrRefuse/the legality check downstream decide
// whether an unparseable version is legal.
func contractCanonicalVersion(v string) string {
	if parsed, err := contractParseSemver(v); err == nil {
		return parsed.String()
	}
	return v
}

// contractDeprecateSeed builds `contract deprecate`'s own canonical,
// content-derived seed (HIGH-1 fix-wave finding): a fixed-order join of
// the deprecated contract id, its deprecated version, and the sunset
// date — deliberately EXCLUDING `now` (MintExchangeIDAt's own known
// midnight-crossing limitation, spec 08 §11 amendment, is accepted
// separately) and EXCLUDING --successor (the migration target, not part
// of what THIS announcement itself commits to). Fed to MintExchangeIDAt
// IN PLACE OF c.deps.entropy for announcementID only — a retry with
// identical inputs reproduces the identical id, landing on the funnel's
// SAME deterministic branch (dedup) instead of authoring a duplicate
// announcement + PR.
func contractDeprecateSeed(contractID, version, sunset string) []byte {
	var buf bytes.Buffer
	buf.WriteString("contract=" + contractID + "\n")
	buf.WriteString("version=" + version + "\n")
	buf.WriteString("sunset=" + sunset + "\n")
	sum := sha256.Sum256(buf.Bytes())
	return sum[:]
}

// contractDiffTree renders added/removed/changed paths between two
// per-file digest maps (schema/**+fixtures/** only, both already scoped by
// the caller), plus F5/AC-975.1's own frontmatter field-change list
// (contract.md itself, populated by the caller via
// contractFrontmatterDiff — contractDiff below never touches it, since it
// only ever sees the file-digest maps).
// --- ContractCommand ------------------------------------------------------

// ContractCommand implements `a2a contract <new|publish|deprecate|retire|
// diff|verify-export>` (spec 08 T1).
type ContractCommand struct {
	newCmd      *NewCommand
	deps        lifecycleDeps
	publication ContractPublicationOperations
	materialize ContractMaterializeOperation
	check       ContractCheckOperation
	inspection  ContractInspectionOperations
}

// SetPendingMarker wires lifecycle/contract writes to the shared pending store.
func (c *ContractCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// NewContractCommand constructs the contract command. newCmd is P6's own
// `a2a new` command (reused verbatim for `contract new`'s delegation,
// never duplicated); funnel/manifest/resolveActor must not be nil/zero
// (rails anti-pattern #10).
func NewContractCommand(newCmd *NewCommand, funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *ContractCommand {
	return &ContractCommand{newCmd: newCmd, deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// SetClockForTest overrides this command's injected clock (test-only DI
// seam, rails anti-pattern #10: production always uses the constructor's
// own time.Now default). HIGH-1/LOW fix-wave finding: proving
// announcementID's determinism and contractSunsetPassed's date comparison
// both need a FIXED, reproducible `now` across multiple calls — a real
// wall-clock read would make either assertion flaky near a UTC-date
// boundary.
func (c *ContractCommand) SetClockForTest(now func() time.Time) {
	c.deps.now = now
}

// Name implements cli.Command.
func (c *ContractCommand) Name() string { return "contract" }

// Synopsis implements cli.Command.
func (c *ContractCommand) Synopsis() string {
	return "contract lifecycle and confidence: preflight | publish | materialize | check | deprecate | retire | diff | verify-export"
}

// Run implements cli.Command.
func (c *ContractCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract <new|preflight|publish|materialize|check|deprecate|retire|diff|verify-export|verify-published|adopt|activate> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	if IsHelpArg(sub) {
		_, _ = fmt.Fprintln(stdio.Stdout, "usage: a2a contract <new|preflight|publish|materialize|check|deprecate|retire|diff|verify-export|verify-published|adopt|activate> ...")
		for _, s := range ContractSubcommands() {
			_, _ = fmt.Fprintf(stdio.Stdout, "  %-14s %s\n", s.Name, s.Synopsis)
		}
		return 0
	}
	switch sub {
	case "new":
		return c.runNew(ctx, rest, stdio)
	case "preflight":
		return c.runPreflight(ctx, rest, stdio)
	case "publish":
		return c.runPublish(ctx, rest, stdio)
	case "materialize":
		return c.runMaterialize(ctx, rest, stdio)
	case "check":
		return c.runCheck(ctx, rest, stdio)
	case "deprecate":
		return c.runDeprecate(ctx, rest, stdio)
	case "retire":
		return c.runRetire(ctx, rest, stdio)
	case "diff":
		return c.runDiff(ctx, rest, stdio)
	case "verify-export":
		return c.runVerifyExport(ctx, rest, stdio)
	case "verify-published":
		return c.runVerifyPublished(ctx, rest, stdio)
	case "adopt":
		return c.runAdopt(ctx, rest, stdio)
	case "activate":
		return c.runActivate(ctx, rest, stdio)
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "contract: unknown subcommand %q\n", sub)
		return 2
	}
}

var _ Command = (*ContractCommand)(nil)

// ContractSubcommand describes one `a2a contract <sub>` sub-verb for
// external surface enumeration.
type ContractSubcommand struct {
	Name     string // e.g. "publish"
	Synopsis string
}

// ContractSubcommands is the SSOT list of the `a2a contract` family's
// sub-verbs for surface enumeration — the P14 CLI/MCP parity check and the
// P13 command-catalog projection both read it. The contract sub-verbs are
// dispatched by the bare switch in ContractCommand.Run (they are NOT
// registered as individual cli.Command values / buildCommands keys), so
// this list is their only machine-enumerable home. KEEP IN SYNC with that
// switch: a sub-verb added there without a row here (or vice versa) is
// exactly the drift the parity gate exists to catch.
func ContractSubcommands() []ContractSubcommand {
	return []ContractSubcommand{
		{Name: "new", Synopsis: "draft a new contract (alias for `a2a new contract --slug`)"},
		{Name: "preflight", Synopsis: "preview the exact immutable publication plan"},
		{Name: "publish", Synopsis: "publish a contract version (--version/--bump, --staging, --allow-empty-bump)"},
		{Name: "materialize", Synopsis: "materialize an exact historical contract version"},
		{Name: "check", Synopsis: "check a payload or the contract self-suite offline"},
		{Name: "deprecate", Synopsis: "deprecate a contract with a linked announcement (--sunset)"},
		{Name: "retire", Synopsis: "retire a contract (consumer-ack precondition, --override)"},
		{Name: "diff", Synopsis: "diff two contract versions (--json)"},
		{Name: "verify-export", Synopsis: "verify a local export's digest tree (--local, --json)"},
		{Name: "verify-published", Synopsis: "report whether every published contract this system provides still matches its code (--json, --local <id>=<path>)"},
		{Name: "adopt", Synopsis: "register this system as a consumer of a contract (writes consumes.yaml)"},
		{Name: "activate", Synopsis: "declare a published version's operational readiness (--version, --satisfies, event/v2 only)"},
	}
}

// runNew translates `contract new <slug>` into P6's `a2a new contract
// --slug <slug>` path (spec 08 T1: "thin alias... do not forward args
// verbatim; P6's NewCommand takes the slug as a flag").
//
// The positional `<slug>` is only ONE of three equivalent spellings this
// verb accepts: `--slug <slug>` and `--field slug=<slug>` both name the
// same value (the surface-consistency defect this fixes — `a2a new
// contract --slug foo` already took a flag, so `a2a contract new --slug
// foo` silently treating "--slug" itself as the literal slug, rather than
// erroring or honoring the flag, was the inconsistency). All three forms
// may be combined as long as they AGREE; if two disagree this is a usage
// error (contractResolveNewSlug), never a silent pick of one over the
// other.
func (c *ContractCommand) runNew(ctx context.Context, args []string, stdio IO) int {
	positional, viaFlag, viaField, rest, err := contractParseNewArgs(args)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract new: %v\n", err)
		return 2
	}
	slug, err := contractResolveNewSlug(positional, viaFlag, viaField)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract new: %v\n", err)
		return 2
	}
	if slug == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract new <slug> | --slug <slug> [--field k=v | k.nested=v]...")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-first-integration"))
		return 2
	}
	delegated := append([]string{"contract", "--slug", slug}, rest...)
	return c.newCmd.Run(ctx, delegated, stdio)
}

// contractParseNewArgs splits `contract new`'s raw args into: an optional
// leading positional slug (args[0], only when it does not itself look like
// a flag), an optional `--slug <value>`/`--slug=<value>` flag value, an
// optional `--field slug=<value>` value, and rest — every remaining token
// UNCHANGED and in order, forwarded verbatim to P6's NewCommand (whose own
// flag set owns --field/--body-file/--thread/--actor-*, never re-parsed
// here). Both single- and double-dash spellings are accepted for --slug/
// --field, matching Go's own flag package convention (it treats "-x" and
// "--x" identically).
func contractParseNewArgs(args []string) (positional, viaFlag, viaField string, rest []string, err error) {
	rest = make([]string, 0, len(args))
	i := 0
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional = args[0]
		i = 1
	}
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--slug" || a == "-slug":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("flag needs an argument: %s", a)
			}
			viaFlag = args[i+1]
			i++
		case strings.HasPrefix(a, "--slug="):
			viaFlag = strings.TrimPrefix(a, "--slug=")
		case strings.HasPrefix(a, "-slug="):
			viaFlag = strings.TrimPrefix(a, "-slug=")
		case a == "--field" || a == "-field":
			rest = append(rest, a)
			if i+1 < len(args) {
				if k, v, found := strings.Cut(args[i+1], "="); found && k == "slug" {
					viaField = v
				}
				rest = append(rest, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--field="):
			if k, v, found := strings.Cut(strings.TrimPrefix(a, "--field="), "="); found && k == "slug" {
				viaField = v
			}
			rest = append(rest, a)
		case strings.HasPrefix(a, "-field="):
			if k, v, found := strings.Cut(strings.TrimPrefix(a, "-field="), "="); found && k == "slug" {
				viaField = v
			}
			rest = append(rest, a)
		default:
			rest = append(rest, a)
		}
	}
	return positional, viaFlag, viaField, rest, nil
}

// contractResolveNewSlug merges the (up to three) slug spellings
// contractParseNewArgs found, in positional -> --slug -> --field slug=
// precedence order, ERRORING the moment two disagree (the defect this fixes
// is a silent, wrong pick — e.g. the positional literal "--slug" winning
// over what the user actually meant as a flag — so agreement is required,
// never assumed). Returns "" with a nil error when none of the three were
// given at all (the caller's own usage-error branch).
func contractResolveNewSlug(positional, viaFlag, viaField string) (string, error) {
	slug := positional
	if viaFlag != "" {
		if slug != "" && slug != viaFlag {
			return "", fmt.Errorf("conflicting slug: positional %q vs --slug %q", slug, viaFlag)
		}
		slug = viaFlag
	}
	if viaField != "" {
		if slug != "" && slug != viaField {
			return "", fmt.Errorf("conflicting slug: %q vs --field slug=%q", slug, viaField)
		}
		slug = viaField
	}
	return slug, nil
}

// runPublish implements `a2a contract publish <id> [--version <semver> |
// --bump major|minor|patch] [--generated-from-digest <hex>]`.
//
// (parseArgsAnyOrder, the any-order positional/flag lift every sub-verb
// below calls, moved to cli.go in Wave K — thirteen more commands across
// the package needed it, which made a single per-file copy the wrong
// shape; see its own doc comment there.)

func (c *ContractCommand) runPublish(ctx context.Context, args []string, stdio IO) int {
	if c.publication == nil {
		return contractServiceUnavailable(stdio, "publish")
	}
	return c.runP6Publish(ctx, args, stdio)
}

// contractPublishedVersions returns every PRIOR publish event's version
// for id, sorted ascending (oldest first) — malformed/missing versions
// are skipped (a legality/schema concern this phase does not re-derive).
func contractPublishedVersions(all []lifecycleEventDoc, id string) []contractSemver {
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

// contractDistinctPublishedVersions dedupes contractPublishedVersions' own
// per-EVENT list into the distinct SET of versions ever published — a
// retry that lands the same publish event twice, or a republish of an
// identical version, must not multiply the version count. F4/AC-972.1
// refuses on "how many DISTINCT versions has this contract ever
// published", never "how many publish events exist". contractPublishedVersions
// already returns its slice sorted ascending, so dedup-by-adjacency
// preserves that order.
func contractDistinctPublishedVersions(all []lifecycleEventDoc, id string) []contractSemver {
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

// contractResolveVersionOrRefuse is F4/AC-972.1: `deprecate` and `retire`
// both used to default an omitted `--version` to the descriptor's CURRENT
// version — after a `--bump major` that is the NEW version, so the OLD one
// silently got no announcement at all (the finding this fixes). With
// exactly one distinct published version, defaulting to currentVersion is
// unambiguous and stays; with MORE than one and explicit == "", this
// REFUSES (a usage error — the operator must say which version) and lists
// every published version, oldest first, so the refusal is actionable
// rather than a bare "try again". Reuses contractPublishedVersions/
// contractDistinctPublishedVersions — no second event walker.
func contractResolveVersionOrRefuse(all []lifecycleEventDoc, id, explicit, currentVersion string) (string, error) {
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
	return "", fmt.Errorf("--version is required: %s has %d published versions (%s) — say which one", id, len(versions), strings.Join(strs, ", "))
}

// contractInferBumpKind classifies the jump used by validate --ci's legacy
// compatibility gate. Contract publication itself now delegates selection
// and compatibility semantics to the shared P6 planner.
//
// ONE classifier, read by BOTH enforcement layers — `contract publish`
// here and `validate --ci` in cmd_validate_ci.go (same package). Wave B
// briefly shipped two, and they disagreed on exactly one input: a
// component DOWNGRADE. Comparing with `>`, publishing 1.0.0 over a 2.0.0
// baseline falls through to "patch" and gets the strict fixture
// revalidation; comparing with `!=` it is "major" and is not checked at
// all. Nothing guards version monotonicity, so the input is reachable —
// and the two layers landed on opposite sides of it, with CI (the merge
// gate) on the fail-open side. That is precisely the divergence AC-970.2
// exists to prevent, arriving through the bump classifier rather than
// through the compat rule itself.
//
// `!=` is the answer kept: any change to a component means the version
// moved in that component, and a downgrade is the LEAST safe thing to
// quietly reclassify as a patch.
func contractInferBumpKind(baseline, newVersion contractSemver) string {
	switch {
	case newVersion[0] != baseline[0]:
		return "major"
	case newVersion[1] != baseline[1]:
		return "minor"
	default:
		return "patch"
	}
}

// runDeprecate implements `a2a contract deprecate <id> [--version
// <semver>] --successor <XC-id@version> --sunset <date>`: authors the
// deprecate event AND a linked deprecation announcement in the same PR
// (§5.4). F4/AC-972.1: `--version` may be omitted only while the contract
// has AT MOST one published version — with more than one it is REQUIRED
// (refused, exit 2, listing every published version) rather than silently
// defaulting to the descriptor's current version. A consequence worth
// stating (not a bug): an unscoped, whole-contract deprecate is therefore
// unreachable once a contract has published a second version —
// contractPriorMajorDeprecated's own `ev.Version == ""` branch still
// exists for events written before this phase, but this verb no longer
// produces one for any multi-version contract.
func (c *ContractCommand) runDeprecate(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("contract deprecate", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	version := fs.String("version", "", "version to deprecate — required once more than one version is published (F4/AC-972.1); defaults to the sole published version otherwise")
	successor := fs.String("successor", "", "successor XC-id@version (required)")
	sunset := fs.String("sunset", "", "sunset date, YYYY-MM-DD (required)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 || *successor == "" || *sunset == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract deprecate <id> [--version <semver>] --successor <XC-id@version> --sunset <date>")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-contract-change"))
		return 2
	}
	id := positional[0]

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract: %v\n", actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	// P4: deprecatedVersion must be resolved BEFORE the legality check
	// below — see runRetire's own comment on why the order matters once a
	// contract carries any recorded version.
	_, probe, _, _, err := contractReadDescriptor(c.deps.mirrorDir, id)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr,
			"contract deprecate: cannot read %s from the local mirror — run `a2a sync` first, or check the contract has been published: %v\n", id, err)
		return 1
	}
	allEvents, err := lifecycleReadAllEvents(c.deps.mirrorDir)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %v\n", err)
		return 1
	}
	deprecatedVersion, err := contractResolveVersionOrRefuse(allEvents, id, *version, probe.Version)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %v\n", err)
		return 2
	}

	// legalityVersion is "" (the legacy whole-subject path) unless this
	// contract has AT LEAST ONE prior publish event that itself carried a
	// `version` field. deprecatedVersion above is resolved for EVENT-
	// AUTHORING purposes (F4/AC-972.1, predates P4) and is always non-empty
	// once the contract has published at all — but the fold only tracks
	// per-version state (Result.Versions) for versions a publish EVENT
	// actually named. Passing deprecatedVersion unconditionally into the
	// legality check would force the version-aware branch
	// (contractVersionVerdict) even for a contract whose entire history is
	// version-less, where Versions[deprecatedVersion] can never be found —
	// refusing a deprecate that the legacy subject-state check correctly
	// allows. This is the caller-side half of "version-less folds
	// identically" (04-per-version-lifecycle.plan.md's cutover guarantee):
	// the guarantee is about the FOLD, but a caller that hands a version to
	// a version-blind history breaks it one layer up.
	legalityVersion := ""
	if len(contractPublishedVersions(allEvents, id)) > 0 {
		legalityVersion = contractCanonicalVersion(deprecatedVersion)
	}
	deprecateEvaluation, _, err := contractEvaluateCandidate(c.deps.mirrorDir, c.deps.manifest, id, fold.Event{
		Transition: fold.TDeprecate, Version: legalityVersion, Actor: actor,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %s: %v\n", id, err)
		return 1
	}
	if deprecateEvaluation.Verdict != fold.VerdictLegal {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %s\n", c.deps.refusalMessage(id, deprecateEvaluation.Verdict))
		return 1
	}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %v\n", err)
		return 1
	}

	deprecateEventID, err := artifact.MintULIDAt(now, c.deps.entropy)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: cannot mint event id: %v\n", err)
		return 1
	}
	// The COMMITTED event's own Version field is legalityVersion, not
	// deprecatedVersion: it must name EXACTLY what the pre-write legality
	// check above just verified against, or a later fold of this same
	// event disagrees with the check that admitted it — the identical
	// "one rule, two readings" failure fold.CheckCandidate's own doc
	// comment exists to prevent, one caller layer up. deprecatedVersion
	// (F4/AC-972.1, resolved above) still names the version everywhere
	// else in this commit (the announcement's `deprecates:` field, its
	// deterministic id seed) — those are audit-trail facts, independent
	// of whether THIS contract's fold is on the per-version path yet.
	deprecateEvent := lifecycleEventDoc{
		Schema: "event/v1", Event: deprecateEventID.String(), Space: probe.Space,
		Subject: id, Transition: fold.TDeprecate, State: contractReceiptState(deprecateEvaluation), Version: legalityVersion,
		Actor: eventActorFrom(resolved, actor.System),
		At:    now.UTC().Format(time.RFC3339),
		Refs:  []lifecycleRefEntry{{Ref: *successor}},
	}
	deprecateRaw, merr := yaml.Marshal(deprecateEvent)
	if merr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: cannot encode event: %v\n", merr)
		return 1
	}

	// HIGH-1 fix-wave finding: announcementID's random suffix is derived
	// from the deprecation's OWN content (contractDeprecateSeed — the
	// deprecated contract id, its deprecated version, and the sunset date;
	// deliberately EXCLUDING --successor, which names the migration target
	// but is not itself part of what THIS announcement commits to), never
	// c.deps.entropy — a retry with identical inputs reproduces the
	// identical id, landing on the SAME funnel branch (dedup) instead of
	// authoring a duplicate announcement + PR. deprecate is one-shot
	// (legality blocks a second deprecate on the same contract@version), so
	// this is not a multi-response concern the way respond's is — same
	// mechanism used for consistency. NOTE: MintExchangeIDAt still embeds
	// today's UTC date from `now`; a retry crossing midnight still mints a
	// different id (spec 08 §11 amendment — accepted, out of scope here).
	announcementSeed := contractDeprecateSeed(id, deprecatedVersion, *sunset)
	announcementID, err := artifact.MintExchangeIDAt("XA", c.deps.ownSystem, now, bytes.NewReader(announcementSeed))
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: cannot mint announcement id: %v\n", err)
		return 1
	}
	// F3/T4 (AC-971.1, AC-971.2): the announcement's addressees are the
	// registered-consumer set — the SAME cache.FindRegisteredConsumers
	// family the retire precondition reads (unscoped here; retire's own
	// read is major-scoped, Edge 1) — not the descriptor's own
	// authoring-time `to:`. Computed ONCE, here, and used directly: "who
	// blocks my retire" and "who was told" share one underlying query
	// instead of two that can drift apart (a system that only ever ran
	// `contract adopt` used to block retire forever while never being
	// addressed).
	to, err := contractDeprecateAddressees(c.deps.mirrorDir, id, probe.From, probe.To)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %v\n", err)
		return 1
	}

	announcementDraft, err := template.Render(template.Input{
		Type: "announcement", ID: announcementID, Actor: resolved, Created: now,
		Fields: map[string]string{
			"from":     c.deps.ownSystem,
			"category": "deprecation",
		},
	})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: render announcement failed: %v\n", err)
		return 1
	}
	// The canonical announcement.md template carries `ack_requested`/
	// `deprecates`/`valid_until` as COMMENTED-OUT example lines (not real
	// mapping keys) — template.Render's own applyFills only overwrites
	// EXISTING keys, it never adds one, so a Fields override for any of
	// these three would be silently dropped. This phase adds them as new
	// keys directly onto the rendered frontmatter's decoded map instead
	// (the same "decode map / mutate / re-encode" idiom `contract publish`
	// already uses for its own descriptor edit) — see this phase's
	// Deviations report.
	if probe.Thread == "" {
		// Same refusal, same reason as RespondCommand's (see its comment):
		// a source with no thread predates P46 and spec 46 carries no
		// legacy path. This path is the quieter of the two failure modes
		// and therefore the more dangerous — `contractAddFrontmatterFields`
		// marshals an empty Go string as `thread: ""`, which was
		// schema-valid before the field was patterned, so a deprecation
		// announcement would have SILENTLY published itself outside every
		// conversation.
		_, _ = fmt.Fprintf(stdio.Stderr,
			"contract deprecate: %s carries no thread, so its deprecation announcement has no conversation to join.\n"+
				"That contract predates thread propagation; reseed the space or republish it with this version.\n", id)
		return 1
	}
	announcementDraft, err = contractAddFrontmatterFields(announcementDraft, map[string]any{
		// space/title are the template's own PLACEHOLDERS and
		// template.Render fills neither, so every deprecation announcement
		// needs them set here regardless. `to` USED to be filled from
		// probe.To for the same reason (a literal placeholder was refused
		// by V2, REF-006, making deprecate impossible against a real
		// space) — it is now `to` computed above (F3), which falls back to
		// probe.To only when the registry has no registered consumers yet
		// (nobody has adopted this contract), preserving the REF-006 fix
		// for that case.
		"space":         probe.Space,
		"to":            to,
		"title":         fmt.Sprintf("Deprecating %s@%s (sunset %s)", id, deprecatedVersion, *sunset),
		"ack_requested": true,
		"deprecates":    id + "@" + deprecatedVersion,
		"valid_until":   *sunset,
		// spec 46 §T1 R2: this announcement is DERIVED from the contract
		// being deprecated — it inherits the CONTRACT's own thread, set
		// alongside space/title (the same "computed once, added onto the
		// rendered frontmatter" idiom this call already uses for the
		// template's other unfilled placeholders).
		"thread": probe.Thread,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %v\n", err)
		return 1
	}

	announcementPublishEventID, err := artifact.MintULIDAt(now, c.deps.entropy)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: cannot mint event id: %v\n", err)
		return 1
	}
	announcementEnv := fold.Envelope{
		ID: announcementID, Kind: fold.KindAnnouncement, From: c.deps.ownSystem, To: to,
	}
	announcementEvaluation := fold.EvaluateCandidate(
		fold.KindAnnouncement,
		fold.NewResult(fold.KindAnnouncement),
		fold.Event{Subject: announcementID, Transition: fold.TPublish, Actor: actor},
		announcementEnv,
		lifecycleMembership(c.deps.manifest),
	)
	if announcementEvaluation.Verdict != fold.VerdictLegal {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %s\n", c.deps.refusalMessage(announcementID, announcementEvaluation.Verdict))
		return 1
	}
	announcementPublishEvent := lifecycleEventDoc{
		Schema: "event/v1", Event: announcementPublishEventID.String(), Space: probe.Space,
		Subject: announcementID, Transition: fold.TPublish, State: contractReceiptState(announcementEvaluation),
		Actor: eventActorFrom(resolved, actor.System),
		At:    now.UTC().Format(time.RFC3339),
	}
	announcementPublishRaw, merr := yaml.Marshal(announcementPublishEvent)
	if merr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: cannot encode announcement publish event: %v\n", merr)
		return 1
	}

	files := []space.FileWrite{
		{Path: layout.EventFile(now.UTC().Format("2006"), deprecateEventID.String()), Content: deprecateRaw},
		{Path: layout.Exchange(announcementID), Content: announcementDraft},
		{Path: layout.EventFile(now.UTC().Format("2006"), announcementPublishEventID.String()), Content: announcementPublishRaw},
	}

	req := c.deps.buildRequest([]string{id, announcementID}, files, "contract-deprecate", false)
	req.OperationKey = operation.ContractDeprecate(
		c.deps.ownSystem, id, contractCanonicalVersion(deprecatedVersion), *successor, *sunset,
	)
	return c.deps.submit(ctx, req, "contract deprecate", []string{id, announcementID}, stdio)
}

// runRetire implements `a2a contract retire <id> [--version <semver>]
// [--override]`: calls internal/validate's retire-precondition policy
// hook (never re-derived here). F4/AC-972.1: `--version` may be omitted
// only while the contract has at most one published version — with more
// than one it is REQUIRED (refused, exit 2, listing every published
// version) rather than silently defaulting to the descriptor's current
// version.
func (c *ContractCommand) runRetire(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("contract retire", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	version := fs.String("version", "", "version to retire — required once more than one version is published (F4/AC-972.1); defaults to the sole published version otherwise")
	override := fs.Bool("override", false, "human-gated override (§5.4)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract retire <id> [--version <semver>] [--override]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-contract-change"))
		return 2
	}
	id := positional[0]

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract: %v\n", actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	// P4: retiredVersion must be resolved BEFORE the legality check below —
	// fold.CheckCandidate/contractVersionVerdict answers per-VERSION once a
	// contract has any recorded version (04-per-version-lifecycle.plan.md),
	// so a version-less check here would refuse a legal version-scoped
	// retire (the pre-P4 "whole subject" table has no (published, retire)
	// row at all, since a rolling window's other versions can still be
	// published while one line retires).
	_, probe, _, _, err := contractReadDescriptor(c.deps.mirrorDir, id)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr,
			"contract retire: cannot read %s from the local mirror — run `a2a sync` first, or check the contract has been published: %v\n", id, err)
		return 1
	}
	allEvents, err := lifecycleReadAllEvents(c.deps.mirrorDir)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %v\n", err)
		return 1
	}
	retiredVersion, err := contractResolveVersionOrRefuse(allEvents, id, *version, probe.Version)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %v\n", err)
		return 2
	}

	// legalityVersion: see runDeprecate's own comment on why this is ""
	// rather than retiredVersion when no prior publish event ever named a
	// version — the legacy, fully version-less path must stay
	// bit-identical to before this phase.
	legalityVersion := ""
	if len(contractPublishedVersions(allEvents, id)) > 0 {
		legalityVersion = contractCanonicalVersion(retiredVersion)
	}
	retireEvaluation, _, err := contractEvaluateCandidate(c.deps.mirrorDir, c.deps.manifest, id, fold.Event{
		Transition: fold.TRetire, Version: legalityVersion, Actor: actor,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %s: %v\n", id, err)
		return 1
	}
	if retireEvaluation.Verdict != fold.VerdictLegal {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %s\n", c.deps.refusalMessage(id, retireEvaluation.Verdict))
		return 1
	}

	// now is fetched ONCE, up front, and threaded through both the LOW
	// fix-wave finding's contractSunsetPassed(sunset, now) call (via
	// contractBuildRetirePrecondition) and the retire event's own
	// timestamp below — never a second, independently-drifting
	// c.deps.now() call.
	now := c.deps.now()

	precondition, err := contractBuildRetirePrecondition(c.deps.mirrorDir, c.deps.manifest, id, retiredVersion, *override, resolved.Kind == "human", now)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %v\n", err)
		return 1
	}
	// Observed consumption is NAMED before the gate is consulted, and on
	// both outcomes (spec 03-observed-consumption.md §8 criterion 1): the
	// producer is deciding either way, and the clean path — the one this
	// phase exists for — is precisely the path that used to say nothing.
	// It is printed, never returned: the line informs and refuses nothing
	// (§9), so it cannot sit in the violation channel. Silent when nothing
	// is observed, which is §8 criterion 4's floor.
	if notice := validate.ObservedConsumptionNotice(precondition); notice != "" {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %s: %s\n", id, notice)
	}
	violation, overridden := validate.CheckRetirePrecondition(precondition)
	if violation != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %s: refused: %s (%s)\n", id, violation.Message, violation.Code)
		return 1
	}

	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %v\n", err)
		return 1
	}
	eventID, err := artifact.MintULIDAt(now, c.deps.entropy)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: cannot mint event id: %v\n", err)
		return 1
	}
	note := ""
	if len(overridden) > 0 {
		note = "retired-unacked: " + strings.Join(overridden, ", ")
	}
	// The COMMITTED event's own Version field is legalityVersion, not
	// retiredVersion — see runDeprecate's own comment on why the two must
	// agree.
	ev := lifecycleEventDoc{
		Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
		Subject: id, Transition: fold.TRetire, State: contractReceiptState(retireEvaluation), Version: legalityVersion,
		Actor: eventActorFrom(resolved, actor.System),
		At:    now.UTC().Format(time.RFC3339),
		Note:  note,
	}
	raw, merr := yaml.Marshal(ev)
	if merr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: cannot encode event: %v\n", merr)
		return 1
	}
	files := []space.FileWrite{{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw}}

	gated := len(overridden) > 0 // mirrors G2: an advisory marker only when the override path was actually taken
	req := c.deps.buildRequest([]string{id}, files, "contract-retire", gated)
	return c.deps.submit(ctx, req, "contract retire", []string{id}, stdio)
}

// contractBuildRetirePrecondition resolves every fact
// validate.CheckRetirePrecondition needs (§5.4/D-022) from the local
// mirror: registered consumers (satisfied requirement ∪ consumes.yaml
// entry), the deprecation announcement's ack set + sunset + reminder
// count.
func contractBuildRetirePrecondition(mirrorDir string, manifest space.Manifest, contractID, contractVersion string, override, actorIsHuman bool, now time.Time) (validate.RetirePrecondition, error) {
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return validate.RetirePrecondition{}, err
	}

	// Find the deprecation announcement for this contract@version (its
	// `refs[0].ref` on the contract's own `deprecate` event names the
	// successor, not the announcement — the announcement instead is found
	// by its own `deprecates` field, read off the committed artifact).
	announcementID, sunset, err := contractFindDeprecationAnnouncement(mirrorDir, contractID, contractVersion)
	if err != nil {
		return validate.RetirePrecondition{}, err
	}

	membership := lifecycleMembership(manifest)
	var ackedSystems map[string]bool
	var reminderCount int
	if announcementID != "" {
		events := lifecycleFoldEvents(all, announcementID)
		result := fold.Fold(fold.KindAnnouncement, fold.Envelope{ID: announcementID, Kind: fold.KindAnnouncement}, events, membership)
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
	// registered-consumer scan is scoped to the MAJOR being retired — a
	// consumer registered on a different major must not block this line's
	// retire forever. contractVersion has already been resolved (never
	// "" here — see runRetire's own comment on why legalityVersion/
	// retiredVersion must be known before this call), so an unparseable
	// value here would be this function's own bug, not a caller-reachable
	// path; fail CLOSED (refuse, never silently unscoped) all the same.
	major, err := version.Major(contractVersion)
	if err != nil {
		return validate.RetirePrecondition{}, fmt.Errorf("cli: %s: %w", contractVersion, err)
	}
	consumerSystems, err := cache.FindRegisteredConsumersForMajor(mirrorDir, contractID, major)
	if err != nil {
		return validate.RetirePrecondition{}, err
	}

	consumers := make([]validate.RegisteredConsumer, 0, len(consumerSystems))
	for sys := range consumerSystems {
		left := membership(sys) == fold.MembershipLeft
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
// `observed` set — the systems this space's own committed, verify-passed
// deliveries show consuming contractID while neither half of the D-022
// declaration union names them (fb-20260820-0cb8c8).
//
// UNSCOPED by major, deliberately, where the declared half just above is
// scoped (Edge 1): a delivery's data-package manifest pins a full
// `id@version#digest`, but the fact being reported is "this system's
// artifacts show it consuming your contract and it registered nowhere",
// and a producer weighing a retire is worse served by silence about a
// neighbouring major than by one line naming a system it can ask.
//
// ALREADY-ACKED SYSTEMS ARE DROPPED (§6's own "observed system already
// acked" edge case). An unregistered system CAN acknowledge a deprecation
// broadcast, and acknowledging is one of the two exits §9 names — "seen,
// and I do not depend on this". A system that has taken an exit is not an
// open risk, and naming it anyway is the nagging US-5 exists to prevent.
// The filter lives HERE rather than on validate.ObservedConsumer, which
// deliberately carries no Acked field: the ack set is the deprecation
// thread's fact, already resolved by the caller above, and giving the type
// an Acked would be the first step toward counting observation in the
// un-acked set the gate reads.
//
// FAILS OPEN, and this is the one place in the retire path that does. An
// error here degrades to "nothing observed", never to a refused retire:
// this set gates nothing (§8 criterion 2), so letting an unreadable
// registry or an unwalkable mirror block a retire the DECLARED set already
// cleared would give observation exactly the veto §9 refuses it. The
// declared half keeps its fail-closed contract one call above, unchanged.
//
// COST, stated because a reviewer should know it: cache.FindObservedConsumers
// walks the mirror once per manifest participant. `contract retire` is a
// rare, interactive, human-gated command, and it pays this on every run,
// including the overwhelmingly common zero-observed one.
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

// contractSunsetPassed reports whether sunset (YYYY-MM-DD) is in the past
// relative to now — now is the CALLER's own injected clock (c.deps.now,
// LOW fix-wave finding), never a direct time.Now().UTC() call: every other
// wall-clock read in this file already goes through the DI seam, and a
// direct call here would be the one un-injectable exception (untestable
// without waiting on real wall-clock dates, anti-pattern #10).
func contractSunsetPassed(sunset string, now time.Time) bool {
	t, err := time.Parse("2006-01-02", sunset)
	if err != nil {
		return false
	}
	return now.UTC().After(t)
}

// contractFindDeprecationAnnouncement walks every committed announcement
// under the mirror looking for one whose `deprecates` field matches
// `<contractID>@<version>`, returning its id and `valid_until` (sunset).
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
// down to internal/cache, ONE home shared with internal/mcp, so "who
// blocks retire" and "who was told" stay one query rather than two that can
// silently disagree; that disagreement is exactly how F3's deadlock
// existed: a system that only ever ran `contract adopt` blocked the
// producer's retire forever while never being addressed by the
// deprecation). UNSCOPED by major, deliberately — Edge 1 (04-per-version-
// lifecycle.md §4) scopes the RETIRE gate only; a deprecation announcement
// still addresses every registered consumer regardless of major, unchanged
// from before this wave. Sorted (cache.FindRegisteredConsumers returns a
// map), deduped, and excludes the contract's OWN `from` system — a
// producer does not address itself.
//
// An EMPTY registered-consumer set (nobody has adopted this contract yet)
// falls back to fallback (the descriptor's own authoring-time `to:`):
// schemas/envelope/v1/base.schema.json's own `to` requires either
// `minItems: 1` or the literal `"all"`, so `to: []` is refused by V2 — the
// same REF-006 history runDeprecate's own doc comment records — and is not
// an option. A fallback that is ALSO empty/nil is refused with an
// actionable error rather than silently authoring an invalid `to: null`
// announcement.
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
		return nil, fmt.Errorf("cli: %s has no registered consumers and no fallback recipients (descriptor `to:` is empty) — nobody to address the deprecation to", contractID)
	}
	return fallback, nil
}

// runDiff implements `a2a contract diff <id> <v1> <v2> [--json]`.
func (c *ContractCommand) runDiff(ctx context.Context, args []string, stdio IO) int {
	if c.inspection == nil {
		return contractServiceUnavailable(stdio, "diff")
	}
	fs := flag.NewFlagSet("contract diff", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	jsonOut := fs.Bool("json", false, "JSON output")
	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 3 || positional[1] == positional[2] {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract diff <id> <v1> <v2> [--json]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-contract-change"))
		return 2
	}
	result, err := c.inspection.DiffContract(ctx, ContractDiffRequest{ID: positional[0], V1: positional[1], V2: positional[2]})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract diff: %v\n", err)
		return 1
	}
	if *jsonOut {
		return encodeContractJSON(stdio, result)
	}
	for _, value := range result.Added {
		_, _ = fmt.Fprintf(stdio.Stdout, "added   %s\n", value)
	}
	for _, value := range result.Removed {
		_, _ = fmt.Fprintf(stdio.Stdout, "removed %s\n", value)
	}
	for _, value := range result.Changed {
		_, _ = fmt.Fprintf(stdio.Stdout, "changed %s\n", value)
	}
	for _, value := range result.FrontmatterChanged {
		_, _ = fmt.Fprintf(stdio.Stdout, "frontmatter %s\n", value)
	}
	return 0
}

// runAdopt implements `a2a contract adopt <XC-id> [--major N] [--note
// <text>]`: register this system as a CONSUMER of another system's
// contract by writing the dependency into `<own-system>/consumes.yaml`
// (§5.2.3, D-022) and submitting it through the same write funnel every
// other mutation goes through.
//
// This is the registry the retire-block precondition (§5.4) and the
// dashboard's dependency edges read, and D-022 is explicit that ONLY the
// space-visible file counts — "project-local config is never
// authoritative". Until this verb existed there was no way to write it:
// `loops.md` §8.2 step 7 told agents "the binary writes your
// consumes.yaml" and nothing did, while `a2a submit` accepts only
// envelope artifacts. A system therefore could not become a registered
// consumer at all, so a producer's retire never had anyone to wait for.
func (c *ContractCommand) runAdopt(ctx context.Context, args []string, stdio IO) int {
	// Both argument orders work, via the shared parseArgsAnyOrder — this
	// verb carried the only copy of that lift until its three siblings were
	// found to be missing it.
	fs := flag.NewFlagSet("contract adopt", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	major := fs.Int("major", 0, "pinned major version to build against (default: the contract's currently published major)")
	note := fs.String("note", "", "optional free-text rationale recorded with the dependency")

	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract adopt <XC-id> [--major <n>] [--note <text>]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-first-integration"))
		return 2
	}
	id := positional[0]

	parsed, err := artifact.ParseID(id)
	if err != nil || parsed.Prefix != "XC" {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract adopt: %s: not a contract id (XC-<system>-<slug>)\n", id)
		return 1
	}
	if parsed.System == c.deps.ownSystem {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract adopt: %s is this system's OWN contract — the registry records what you consume from OTHERS (§5.2.3)\n", id)
		return 1
	}

	// P5 US-1's counterweight (specs/05-declared-nature.md, 2026-08-10
	// amendment): a contract whose descriptor declares itself non-adoptable
	// — the `x_binding: none` sentinel, or the long form with
	// `adoptable: false` — refuses `a2a contract adopt` outright, so
	// nobody pins it. This runs BEFORE --major resolution and regardless
	// of whether --major was passed explicitly, so passing --major cannot
	// route around the refusal. A read failure here (contract not yet
	// synced) is NOT this check's error to report — that is
	// contractPublishedMajor's existing, actionable "run `a2a sync`
	// first" message below, so a read failure simply skips this check
	// rather than shadowing it with a different one.
	if _, adoptProbe, _, _, rerr := contractReadDescriptor(c.deps.mirrorDir, id); rerr == nil {
		if adoptProbe.XBinding.nonAdoptable() {
			_, _ = fmt.Fprintf(stdio.Stderr, "contract adopt: %s declares itself non-adoptable (x_binding) — nobody may pin it\n", id)
			return 1
		}
	}

	pinned := *major
	if pinned == 0 {
		pinned, err = contractPublishedMajor(c.deps.mirrorDir, id)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "contract adopt: %v (pass --major <n> to pin explicitly)\n", err)
			return 1
		}
	}

	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract adopt: %v\n", err)
		return 1
	}
	relPath := layout.ConsumesYAML()

	registry, err := contractLoadConsumes(c.deps.mirrorDir, relPath, c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract adopt: %v\n", err)
		return 1
	}

	updated, changed := contractUpsertDependency(registry, space.Dependency{
		Contract: id, Major: pinned,
		Since: c.deps.now().UTC().Format("2006-01-02"),
		Note:  *note,
	})
	if !changed {
		_, _ = fmt.Fprintf(stdio.Stdout, "contract adopt: %s already registered at major %d\n", id, pinned)
		return 0
	}

	raw, err := yaml.Marshal(updated)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract adopt: cannot encode %s: %v\n", relPath, err)
		return 1
	}

	// The funnel's own V2 pass validates the file against consumes/v1 (the
	// SAME check the space's V3 runs) — this verb never re-implements it.
	req := c.deps.buildRequest([]string{id}, []space.FileWrite{{Path: relPath, Content: raw}}, "contract-adopt", false)
	return c.deps.submit(ctx, req, "contract adopt", []string{id}, stdio)
}

// contractActivationEntry is `a2a contract activate`'s own `activation`
// object (specs/05-declared-nature.md's AC1 discharge half, 2026-08-10
// amendment) — schemas/event/v2/event.schema.json's `{version, status,
// satisfies[], note?}`. `Status` carries no CLI flag: this verb always
// writes the literal `live`, the schema's own enum has no other member yet
// (schemas/fill-classes.yaml classifies it TOOL for exactly this reason).
type contractActivationEntry struct {
	Version   string   `yaml:"version"`
	Status    string   `yaml:"status"`
	Satisfies []string `yaml:"satisfies"`
	Note      string   `yaml:"note,omitempty"`
}

// contractActivateEventDoc is `contract activate`'s own event/v2 wire shape
// — a FILE-LOCAL copy of cmd_lifecycle.go's lifecycleEventDoc (that file is
// off-limits to this wave) carrying only the fields an `activate` event
// needs, plus `activation`. Both structs marshal the shared fields
// identically because both mirror schemas/event/v2/event.schema.json's own
// property names — the same "define the shape where the writer needs it"
// precedent this file already follows for xBindingProbe/
// contractDescriptorProbe, rather than widening a struct in a file this
// brief does not grant.
type contractActivateEventDoc struct {
	Schema     string                  `yaml:"schema"`
	Event      string                  `yaml:"event"`
	Space      string                  `yaml:"space"`
	Subject    string                  `yaml:"subject"`
	Transition string                  `yaml:"transition"`
	Actor      lifecycleEventActor     `yaml:"actor"`
	At         string                  `yaml:"at"`
	Note       string                  `yaml:"note,omitempty"`
	Activation contractActivationEntry `yaml:"activation"`
}

// runActivate implements `a2a contract activate <XC-id> --version <semver>
// --satisfies <item> [--satisfies <item>...] [--note <text>]` — P5's AC1
// discharge half (specs/05-declared-nature.md's 2026-08-10 amendments):
// the producer's own act that moves a published version's `x_operational[]`
// items toward `ready`, authored as an `activate` event/v2 event (never a
// descriptor edit — a descriptor is immutable after publication).
//
// Two refusals this wave's brief asks to be decided and justified, both
// enforced here:
//
//  1. Below contract.ContractPublicationFloor, this verb refuses outright
//     rather than silently authoring event/v1: `activation` and the
//     `activate` transition exist ONLY on event/v2 (unlike verify/close,
//     which fall back to event/v1 without `verdicts[]`), so there is no
//     legal event/v1 shape to fall back to — the funnel's own V2 pass
//     would reject it regardless, and refusing here names the real
//     condition instead of a schema error far from this command.
//  2. `--satisfies` may only name an item already present in the
//     descriptor's own `x_operational[]` (regardless of its current
//     state) — activating an item the producer never declared, even as
//     `absent`, would let a producer route around ever declaring the
//     field at all, which is exactly the P-1 discipline this phase's own
//     entry point exists to hold.
//
// This verb does NOT run the fold.EvaluateCandidate legality gate every
// other OP-211 writer in this file does: internal/fold's transition table
// (off-limits to this wave) has no `activate` row, and `activation` is
// deliberately NOT a contract lifecycle state transition (draft/published/
// deprecated/retired) — it is a side fact about a published version's
// operational readiness, the same reasoning `contract adopt` above already
// applies to its own consumes.yaml write (also un-evaluated by fold).
// contractPublishedVersions is still consulted directly, though: activating
// a version this contract never actually published is refused, unevaluated
// legality notwithstanding.
func (c *ContractCommand) runActivate(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("contract activate", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	targetVersion := fs.String("version", "", "published version this activation covers (required)")
	note := fs.String("note", "", "optional free-text context")
	var satisfiesFlags newStringList
	fs.Var(&satisfiesFlags, "satisfies", "x_operational[] item name this activation makes ready (repeatable; must already be declared on the descriptor, any state)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)

	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 || *targetVersion == "" || len(satisfiesFlags) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract activate <XC-id> --version <semver> --satisfies <item> [--satisfies <item>...] [--note <text>]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-first-integration"))
		return 2
	}
	id := positional[0]

	parsed, err := artifact.ParseID(id)
	if err != nil || parsed.Prefix != "XC" {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract activate: %s: not a contract id (XC-<system>-<slug>)\n", id)
		return 1
	}
	// Ownership: this wave's own brief frames activate as "the producer's
	// act" — only the system that OWNS the contract (embedded in its own
	// id, §3.3) may declare its operational readiness. Unlike `adopt`
	// (which refuses the opposite direction — targeting one's OWN
	// contract), this write is never legality-checked through fold (see
	// this function's doc comment), so nothing else in this path would
	// stop a system from authoring an activation event about a contract
	// it does not own.
	if parsed.System != c.deps.ownSystem {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract activate: %s is not owned by this system (%s) — only the producer may declare its own operational readiness\n", id, c.deps.ownSystem)
		return 1
	}

	// Refusal 1 — the floor. lifecycleEventSchema mirrors internal/contract/
	// publication_plan.go's own authoring-floor split, the SAME reuse
	// `verify --verdict` already makes of it.
	eventSchema := lifecycleEventSchema(c.deps.manifest.MinBinaryVersion)
	if eventSchema != "event/v2" {
		_, _ = fmt.Fprintf(stdio.Stderr,
			"contract activate: requires this space's min_binary_version to be at or above %s (event/v2, `activation` has no event/v1 shape); this space's floor is %q\n",
			contract.ContractPublicationFloor, c.deps.manifest.MinBinaryVersion)
		return 1
	}

	_, probe, _, _, err := contractReadDescriptor(c.deps.mirrorDir, id)
	if err != nil {
		// Name the CONDITION, not the file that happened to be missing.
		// contractReadDescriptor's own error is a raw open() failure carrying
		// an absolute cache path, which tells an operator nothing they can
		// act on — and `runAdopt` two hundred lines up already says this
		// well for the identical condition ("run `a2a sync` first"). Found
		// 2026-08-11 by running the verb against an unsynced mirror.
		_, _ = fmt.Fprintf(stdio.Stderr,
			"contract activate: cannot read %s from the local mirror — run `a2a sync` first, or check the contract has been published: %v\n", id, err)
		return 1
	}

	allEvents, err := lifecycleReadAllEvents(c.deps.mirrorDir)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract activate: %v\n", err)
		return 1
	}
	canonicalVersion := contractCanonicalVersion(*targetVersion)
	published := false
	for _, v := range contractPublishedVersions(allEvents, id) {
		if v.String() == canonicalVersion {
			published = true
			break
		}
	}
	if !published {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract activate: %s@%s has not been published — activation names a published version's operational readiness, it is not a way to publish one\n", id, *targetVersion)
		return 1
	}

	// Refusal 2 — an undeclared item. Regardless of the item's current
	// state (ready or absent), its NAME must already be present in
	// x_operational[] — see this function's own doc comment.
	declared := map[string]bool{}
	for _, item := range probe.XOperational {
		declared[item.Name] = true
	}
	satisfies := append([]string(nil), []string(satisfiesFlags)...)
	for _, name := range satisfies {
		if !declared[name] {
			_, _ = fmt.Fprintf(stdio.Stderr, "contract activate: %q is not a named item in %s's x_operational[] — declare it there first (even as `state: absent`) before activating it\n", name, id)
			return 1
		}
	}

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract activate: %v\n", actorErr)
		return 1
	}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract activate: %v\n", err)
		return 1
	}
	eventID, err := artifact.MintULIDAt(now, c.deps.entropy)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract activate: cannot mint event id: %v\n", err)
		return 1
	}

	ev := contractActivateEventDoc{
		Schema: eventSchema, Event: eventID.String(), Space: probe.Space,
		Subject: id, Transition: "activate",
		Actor: eventActorFrom(resolved, c.deps.ownSystem),
		At:    now.UTC().Format(time.RFC3339),
		Note:  *note,
		Activation: contractActivationEntry{
			Version: canonicalVersion, Status: "live", Satisfies: satisfies,
		},
	}
	raw, merr := yaml.Marshal(ev)
	if merr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract activate: cannot encode event: %v\n", merr)
		return 1
	}
	files := []space.FileWrite{{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw}}

	req := c.deps.buildRequest([]string{id}, files, "contract-activate", false)
	req.OperationKey = operation.ContractActivate(c.deps.ownSystem, id, canonicalVersion, satisfies, *note)
	return c.deps.submit(ctx, req, "contract activate", []string{id}, stdio)
}

// contractPublishedMajor reads the contract descriptor committed in the
// mirror and returns its published major. A contract that is not in the
// mirror (never synced, or never published) is an actionable error, not a
// silent default — pinning to the wrong major is exactly the mistake this
// registry exists to prevent.
func contractPublishedMajor(mirrorDir, id string) (int, error) {
	_, probe, _, _, err := contractReadDescriptor(mirrorDir, id)
	if err != nil {
		return 0, fmt.Errorf("cannot read %s from the local mirror — run `a2a sync` first: %w", id, err)
	}
	if probe.Version == "" {
		return 0, fmt.Errorf("%s carries no published version yet", id)
	}
	v, err := contractParseSemver(probe.Version)
	if err != nil {
		return 0, fmt.Errorf("%s has an unparseable version %q: %w", id, probe.Version, err)
	}
	return v[0], nil
}

// contractLoadConsumes reads this system's committed consumes.yaml, or
// returns a fresh, schema-shaped empty registry when the system has never
// registered a dependency.
func contractLoadConsumes(mirrorDir, relPath, ownSystem string) (space.Consumes, error) {
	raw, err := readBoundedFile(filepath.Join(mirrorDir, relPath), maxMirrorEventBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Never registered a dependency before — a fresh registry, not an
		// error. Any OTHER read failure IS an error: silently starting from
		// empty would drop the system's existing dependencies on the next
		// write.
		return space.Consumes{Schema: "consumes/v1", System: ownSystem, Dependencies: []space.Dependency{}}, nil
	case err != nil:
		return space.Consumes{}, fmt.Errorf("cannot read %s: %w", relPath, err)
	}
	registry, perr := space.ParseConsumes(raw)
	if perr != nil {
		return space.Consumes{}, fmt.Errorf("cannot parse %s: %w", relPath, perr)
	}
	// A committed file whose header drifted (or a hand-written stub) is
	// repaired to the schema's shape rather than propagated — the write
	// still goes through V2/V3, so a file this cannot fix reds there.
	if registry.Schema == "" {
		registry.Schema = "consumes/v1"
	}
	if registry.System == "" {
		registry.System = ownSystem
	}
	return registry, nil
}

// contractUpsertDependency adds or updates dep in registry, keeping the
// dependency list sorted by contract id (a stable file makes the diff a
// reviewer sees the actual change). It reports changed=false when the
// registry already carries exactly this contract at this major — the
// idempotent re-run, which must not open a second PR.
func contractUpsertDependency(registry space.Consumes, dep space.Dependency) (space.Consumes, bool) {
	for i, existing := range registry.Dependencies {
		if existing.Contract != dep.Contract {
			continue
		}
		if existing.Major == dep.Major && (dep.Note == "" || existing.Note == dep.Note) {
			return registry, false
		}
		// A new major is a new dependency in substance, so it gets
		// today's date; editing only the note must NOT rewrite `since`
		// (it records when the dependency was declared, not when the row
		// was last touched).
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

// runVerifyExport implements `a2a contract verify-export --local <path>
// <id>[@version] [--json]` (AC-1001.1; answers-that-hold-2026-08 P3 US-3).
//
// Branches on result.Outcome's closed three-value vocabulary
// (matched/drifted/unmeasured — contract.ExportVerification, D9-mapped at
// the cmd/a2a render boundary), never on the retained Matches bool: Matches
// collapses drifted and unmeasured into the same "false", which used to
// print the SAME digest-mismatch message on the SAME non-zero exit for a
// run that had nothing to compare against at all (spec P2's own AC-6,
// carried here as this phase's AC-9 — D9: SeverityUnmeasured alone must
// never flip a result to failing, exactly as
// cmd_contract_verify_published.go's contractVerifyPublishedRun already
// treats "unmeasured" as non-failing).
//
// A descriptor-only difference used to print ZERO lines on a non-zero exit
// (fb-20260827-bc1f13): the `frontmatter <field>` loop is copied verbatim
// from runDiff's own fourth loop, printed for BOTH non-matched outcomes —
// a byte-level export diff can exist even when Outcome is "unmeasured"
// (no generated_from.source_digest was asserted to compare against, which
// is a distinct question from "do the exported bytes differ").
func (c *ContractCommand) runVerifyExport(ctx context.Context, args []string, stdio IO) int {
	if c.inspection == nil {
		return contractServiceUnavailable(stdio, "verify-export")
	}
	fs := flag.NewFlagSet("contract verify-export", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	local := fs.String("local", "", "project-relative export path")
	asJSON := fs.Bool("json", false, "emit JSON")
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if *local == "" || len(positionals) != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract verify-export --local <project-relative-path> <id>[@version] [--json]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-contract-change"))
		return 2
	}
	result, err := c.inspection.VerifyContractExport(ctx, ContractVerifyExportRequest{Local: *local, Ref: positionals[0]})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract verify-export: %v\n", err)
		return 1
	}
	if *asJSON {
		return encodeContractJSON(stdio, result)
	}
	if result.Outcome == "matched" {
		_, _ = fmt.Fprintf(stdio.Stdout, "contract verify-export: %s matches (%s)\n", result.ID, result.LocalDigest)
		return 0
	}
	for _, value := range result.Diff.Added {
		_, _ = fmt.Fprintf(stdio.Stdout, "added   %s\n", value)
	}
	for _, value := range result.Diff.Removed {
		_, _ = fmt.Fprintf(stdio.Stdout, "removed %s\n", value)
	}
	for _, value := range result.Diff.Changed {
		_, _ = fmt.Fprintf(stdio.Stdout, "changed %s\n", value)
	}
	for _, value := range result.Diff.FrontmatterChanged {
		_, _ = fmt.Fprintf(stdio.Stdout, "frontmatter %s\n", value)
	}
	if result.Outcome == "unmeasured" {
		_, _ = fmt.Fprintf(stdio.Stdout, "contract verify-export: %s: no generated_from.source_digest asserted — nothing to compare (local=%s)\n", result.ID, result.LocalDigest)
		return 0
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "contract verify-export: digest mismatch: local=%s want=%s\n", result.LocalDigest, result.WantDigest)
	return 1
}
