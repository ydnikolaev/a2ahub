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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

// contractDescriptorProbe is this file's own minimal decode of a
// contract's descriptor (contract.md) fields (§5.2.1's contract-only
// extensions) — a richer sibling of lifecycleEnvelopeProbe (which only
// carries the base envelope fields every OP-211 verb needs).
type contractDescriptorProbe struct {
	ID            string   `yaml:"id"`
	Space         string   `yaml:"space"`
	From          string   `yaml:"from"`
	To            []string `yaml:"to"`
	Version       string   `yaml:"version"`
	CompatPolicy  string   `yaml:"compat_policy"`
	SchemaFormat  string   `yaml:"schema_format"`
	GeneratedFrom struct {
		Tool         string `yaml:"tool"`
		SourceDigest string `yaml:"source_digest"`
	} `yaml:"generated_from"`
	// Thread is the contract's own §3.8 thread id — spec 46 §T1 R2:
	// `contract deprecate`'s linked announcement is DERIVED from this
	// contract, so it inherits the SAME thread rather than minting or
	// leaving one unfilled.
	Thread string `yaml:"thread"`
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

func contractBump(prior contractSemver, kind string) contractSemver {
	switch kind {
	case "major":
		return contractSemver{prior[0] + 1, 0, 0}
	case "minor":
		return contractSemver{prior[0], prior[1] + 1, 0}
	case "patch":
		return contractSemver{prior[0], prior[1], prior[2] + 1}
	default:
		return prior
	}
}

// contractSelectBaseline is P4 wave 5's Edge 2 (04-per-version-lifecycle.md
// §4): the baseline `contract publish` compares against and diffs is
// max{v ∈ priorVersions : v < newVersion} — the highest PRIOR version
// strictly older than the one being published, NOT necessarily the
// globally-highest ever published (`priorVersions[len(priorVersions)-1]`,
// this file's own former rule, is only that rule's special case when
// newVersion IS the highest-ever publish). Publishing a maintenance 1.2
// while 2.0 is already published must compare against 1.1, not 2.0
// (AC-8) — the everyday act during a sunset window.
//
// The DECISION itself lives in internal/version.Baseline, shared with
// internal/mcp's own copy of this bridge (decision 8,
// 04-per-version-lifecycle.plan.md — "shared code moves DOWN into core,
// never ACROSS between the surfaces"); this is only the adapter between
// contractSemver's own numeric shape and that string-based rule. Every
// contractSemver.String() is always a well-formed dotted-integer version,
// so version.Baseline can only ever fail on an input this function itself
// produced wrong — never a caller-reachable path — and such a failure is
// treated the same as "not found" (the zero value, false) rather than
// propagated as an error no caller actually needs to handle.
func contractSelectBaseline(priorVersions []contractSemver, newVersion contractSemver) (contractSemver, bool) {
	published := make([]string, len(priorVersions))
	for i, v := range priorVersions {
		published[i] = v.String()
	}
	baseline, found, err := version.Baseline(published, newVersion.String())
	if err != nil || !found {
		return contractSemver{0, 0, 0}, false
	}
	parsed, perr := contractParseSemver(baseline)
	if perr != nil {
		return contractSemver{0, 0, 0}, false
	}
	return parsed, true
}

// --- digest tree (§5.7/D-029) — the ONE impl publish/diff/verify-export
// all call now lives in internal/artifact (artifact.DigestTreeFS /
// artifact.CombineDigestPairs, MED-5 fix-wave finding): the plan's own
// "internal/artifact multi-file digest helper" placement, no longer a
// file-private copy here. contractDigestSubtrees is this file's own
// schema/**+fixtures/** subtree list, threaded into every call site below
// (the artifact helper stays generic; the subtree choice is the caller's).

var contractDigestSubtrees = []string{"schema", "fixtures"}

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
type contractDiffTree struct {
	Added              []string `json:"added"`
	Removed            []string `json:"removed"`
	Changed            []string `json:"changed"`
	FrontmatterChanged []string `json:"frontmatter_changed"`
}

func contractDiff(a, b map[string]string) contractDiffTree {
	// Non-nil empty slices so `--json` emits `[]` (not `null`) for an empty
	// diff — a JSON consumer doing `.added.length` must not trip (go-auditor
	// P26 IN LOW).
	out := contractDiffTree{Added: []string{}, Removed: []string{}, Changed: []string{}}
	for p, da := range a {
		db, ok := b[p]
		if !ok {
			out.Removed = append(out.Removed, p)
		} else if da != db {
			out.Changed = append(out.Changed, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			out.Added = append(out.Added, p)
		}
	}
	sort.Strings(out.Added)
	sort.Strings(out.Removed)
	sort.Strings(out.Changed)
	return out
}

// --- version resolution via git history (contract diff only; §5.4a) ----
// Publish events do not carry a real commit SHA (see lifecycleEventDoc's
// own doc comment: the SHA is only known AFTER the funnel commits, i.e.
// after the event file already had to be authored) — this phase resolves
// a version to a commit by walking the descriptor path's own git log
// directly instead (read-only git plumbing, explicit argv, mirrors
// internal/space/mirror.go's own idiom; kept file-private here since
// internal/space is import-only to this phase's allowlist).

func contractResolveVersionSHA(ctx context.Context, repoDir, descriptorPath, version string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "log", "--format=%H", "--", descriptorPath).Output()
	if err != nil {
		return "", fmt.Errorf("cli: git log %s: %w", descriptorPath, err)
	}
	shas := strings.Fields(string(out))
	for _, sha := range shas {
		content, serr := exec.CommandContext(ctx, "git", "-C", repoDir, "show", sha+":"+descriptorPath).Output()
		if serr != nil {
			continue
		}
		fm, ferr := artifact.ParseFrontmatter(content)
		if ferr != nil {
			continue
		}
		var probe contractDescriptorProbe
		if yaml.Unmarshal(fm.YAML, &probe) == nil && probe.Version == version {
			return sha, nil
		}
	}
	return "", fmt.Errorf("cli: no commit found where %s has version %s", descriptorPath, version)
}

// contractDigestTreeAtSHA computes the §5.7 digest tree for descriptorDir
// (schema/**+fixtures/**) as it existed at sha.
func contractDigestTreeAtSHA(ctx context.Context, repoDir, sha, descriptorDir string) (map[string]string, error) {
	perFile := map[string]string{}
	for _, sub := range []string{"schema", "fixtures"} {
		dir := path.Join(descriptorDir, sub)
		out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "ls-tree", "-r", "--name-only", sha, "--", dir).Output()
		if err != nil {
			continue // subtree absent at this SHA — treated as empty, not fatal
		}
		for _, rel := range strings.Fields(string(out)) {
			content, serr := exec.CommandContext(ctx, "git", "-C", repoDir, "show", sha+":"+rel).Output()
			if serr != nil {
				return nil, fmt.Errorf("cli: git show %s:%s: %w", sha, rel, serr)
			}
			relToDescriptorRoot, rerr := filepath.Rel(descriptorDir, rel)
			if rerr != nil {
				return nil, rerr
			}
			perFile[filepath.ToSlash(relToDescriptorRoot)] = artifact.Digest(content)
		}
	}
	return perFile, nil
}

// contractDescriptorProbeAtSHA reads and decodes the contract's own
// descriptor (contract.md frontmatter) exactly as it existed at sha —
// F5/AC-975.1's own read. Reuses contractResolveVersionSHA's git-show +
// frontmatter-decode idiom (same read-only git plumbing, no new plumbing
// added): contractResolveVersionSHA already had to do exactly this read,
// once per candidate commit, to find sha in the first place — it just
// discarded the decoded probe once the version matched. This is the
// leftover kept, for a sha the caller already resolved.
func contractDescriptorProbeAtSHA(ctx context.Context, repoDir, descriptorPath, sha string) (contractDescriptorProbe, error) {
	content, err := exec.CommandContext(ctx, "git", "-C", repoDir, "show", sha+":"+descriptorPath).Output()
	if err != nil {
		return contractDescriptorProbe{}, fmt.Errorf("cli: git show %s:%s: %w", sha, descriptorPath, err)
	}
	fm, ferr := artifact.ParseFrontmatter(content)
	if ferr != nil {
		return contractDescriptorProbe{}, fmt.Errorf("cli: %s: %w", descriptorPath, ferr)
	}
	var probe contractDescriptorProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return contractDescriptorProbe{}, fmt.Errorf("cli: %s: cannot decode descriptor: %w", descriptorPath, err)
	}
	return probe, nil
}

// contractFrontmatterDiff reports which of contractDescriptorProbe's own
// fields changed between a and b (F5/AC-975.1) — a change confined to
// contract.md, INCLUDING compat_policy itself, is exactly what
// contractDiff's schema/**+fixtures/** file digest cannot see. Each entry
// is "field: old -> new", sorted for determinism (a fixed non-nil empty
// slice, not nil, matching contractDiff's own P26 "`[]` not `null`"
// discipline for --json).
//
// Deliberately EXCLUDES two fields:
//   - `id` — identity, never differs between two versions of the SAME
//     descriptor path (both a and b were read from one contractResolveVersionSHA
//     match on this exact id).
//   - `version` — the diff's own two arguments (v1, v2) ALWAYS differ by
//     construction (runDiff already refuses v1 == v2); reporting it here
//     would fire on every single call and bury the compat_policy signal
//     this function exists for.
//
// Scoped to what contractDescriptorProbe itself decodes: a change to
// `title` or `category` (not part of the probe) is invisible here, same as
// it always was to every other reader of this probe. Spec §7.3 defers
// diffing contract.md's prose sections entirely.
func contractFrontmatterDiff(a, b contractDescriptorProbe) []string {
	out := []string{}
	add := func(field, oldV, newV string) {
		if oldV != newV {
			out = append(out, fmt.Sprintf("%s: %s -> %s", field, oldV, newV))
		}
	}
	add("space", a.Space, b.Space)
	add("from", a.From, b.From)
	if !slices.Equal(a.To, b.To) {
		out = append(out, fmt.Sprintf("to: %v -> %v", a.To, b.To))
	}
	add("compat_policy", a.CompatPolicy, b.CompatPolicy)
	add("schema_format", a.SchemaFormat, b.SchemaFormat)
	add("generated_from.tool", a.GeneratedFrom.Tool, b.GeneratedFrom.Tool)
	add("generated_from.source_digest", a.GeneratedFrom.SourceDigest, b.GeneratedFrom.SourceDigest)
	sort.Strings(out)
	return out
}

// --- ContractCommand ------------------------------------------------------

// ContractCommand implements `a2a contract <new|publish|deprecate|retire|
// diff|verify-export>` (spec 08 T1).
type ContractCommand struct {
	newCmd *NewCommand
	deps   lifecycleDeps
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
	return "contract lifecycle: new <slug> | publish <id> | deprecate <id> | retire <id> | diff <id> <v1> <v2> | verify-export --local <path> <id>[@version]"
}

// Run implements cli.Command.
func (c *ContractCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract <new|publish|deprecate|retire|diff|verify-export|adopt> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	if IsHelpArg(sub) {
		_, _ = fmt.Fprintln(stdio.Stdout, "usage: a2a contract <new|publish|deprecate|retire|diff|verify-export|adopt> ...")
		for _, s := range ContractSubcommands() {
			_, _ = fmt.Fprintf(stdio.Stdout, "  %-14s %s\n", s.Name, s.Synopsis)
		}
		return 0
	}
	switch sub {
	case "new":
		return c.runNew(ctx, rest, stdio)
	case "publish":
		return c.runPublish(ctx, rest, stdio)
	case "deprecate":
		return c.runDeprecate(ctx, rest, stdio)
	case "retire":
		return c.runRetire(ctx, rest, stdio)
	case "diff":
		return c.runDiff(ctx, rest, stdio)
	case "verify-export":
		return c.runVerifyExport(ctx, rest, stdio)
	case "adopt":
		return c.runAdopt(ctx, rest, stdio)
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
		{Name: "publish", Synopsis: "publish a contract version (--version/--bump, digest tree)"},
		{Name: "deprecate", Synopsis: "deprecate a contract with a linked announcement (--sunset)"},
		{Name: "retire", Synopsis: "retire a contract (consumer-ack precondition, --override)"},
		{Name: "diff", Synopsis: "diff two contract versions (--json)"},
		{Name: "verify-export", Synopsis: "verify a local export's digest tree (--local)"},
		{Name: "adopt", Synopsis: "register this system as a consumer of a contract (writes consumes.yaml)"},
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

// contractReadWorkingTreeFiles reads every regular file under
// filepath.Join(root, sub) as it currently sits in the working tree, keyed
// "<sub>/<path-relative-to-root>" (forward-slash) — the SAME keying
// contractPriorVersionFiles/contractReadTreeAtSHA use for the prior
// version's git-history reads (both "schema/<name>" and
// "fixtures/valid/<name>", relative to the descriptor's own directory), so
// validate.CheckComputedCompatibility's fixture->schema mapping sees
// identical shapes on both sides of a comparison, and so a POL-007/POL-008
// refusal names a path that actually exists in the repo (AC-970.1). A
// missing directory is not an error — an empty map (nothing published
// under that subtree yet), which is exactly what D-D's own "count, don't
// assume" check needs to see. Bounded: every leaf read goes through
// readBoundedFile at maxMirrorEventBytes, this package's own existing
// artifact-read ceiling.
func contractReadWorkingTreeFiles(root, sub string) (map[string][]byte, error) {
	dir := filepath.Join(root, sub)
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]byte{}, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return map[string][]byte{}, nil
	}
	out := map[string][]byte{}
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		raw, rerr := readBoundedFile(p, maxMirrorEventBytes)
		if rerr != nil {
			return rerr
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out[filepath.ToSlash(rel)] = raw
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// contractStagingOverlay merges landed (this contract's schema/**+
// fixtures/** files as contractReadWorkingTreeFiles reads them off the
// mirror's OWN working tree) with whatever staged carries for this SAME
// contract (template.ContractSidecarsFromStaging's own space-relative
// Path, e.g. "<system>/provides/<slug>/schema/<name>", re-rooted here to
// relDir's own vocabulary: "schema/<name>") — PER FILE, never per
// directory: a staging dir holding only a new schema must leave the
// landed fixture in place, not fall back to "staging exists for this
// contract, ignore the landed tree entirely" (that would silently DROP a
// fixture nobody re-staged).
//
// Absence in staging means "unchanged", never "removed" — deliberately
// the opposite of the more obvious reading (a caller might expect
// "staging is now authoritative, anything landed but not restaged is
// gone"). That reading would make publishing a schema-only edit silently
// retire the contract's own fixtures the moment nobody bothered to
// re-stage them untouched — staged content only ever ADDS or REPLACES a
// file, it never deletes one relative to what publish would otherwise
// carry. A staging dir holding nothing for this contract (nil newCmd, an
// empty staging dir, or a contract nobody re-staged) therefore returns
// landed byte-for-byte unmodified — the no-regression case (P37 Wave I
// §3): a contract authored before the scaffold existed keeps publishing
// exactly as before this wave.
func contractStagingOverlay(landed map[string][]byte, staged []space.FileWrite, relDir string) map[string][]byte {
	out := make(map[string][]byte, len(landed))
	for k, v := range landed {
		out[k] = v
	}
	prefix := relDir + "/"
	for _, sc := range staged {
		if !strings.HasPrefix(sc.Path, prefix) {
			continue // defensive: a sidecar outside this contract's own directory never overlays it
		}
		out[strings.TrimPrefix(sc.Path, prefix)] = sc.Content
	}
	return out
}

func (c *ContractCommand) runPublish(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("contract publish", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	version := fs.String("version", "", "explicit semver to publish")
	bump := fs.String("bump", "", "major|minor|patch (bump the prior published version)")
	generatedFromDigest := fs.String("generated-from-digest", "", "optional §5.3 generated_from.source_digest to record")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract publish <id> [--version <semver>|--bump major|minor|patch]")
		return 2
	}
	id := positional[0]
	if *version == "" && *bump == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "contract publish: one of --version or --bump is required")
		return 2
	}

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract: %v\n", actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	// P4: newVersion must be resolved BEFORE the legality check below —
	// once a contract carries any recorded version, fold.CheckCandidate/
	// contractVersionVerdict refuses a version-LESS publish outright
	// (04-per-version-lifecycle.plan.md), so the version this commit is
	// about to mint must be known first, not deferred to the old G1 block
	// further down. This needs only the event history + the --version/
	// --bump flags, not the descriptor, so hoisting it here is cheap.
	all, err := lifecycleReadAllEvents(c.deps.mirrorDir)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
		return 1
	}
	priorVersions := contractPublishedVersions(all, id)
	isFirstPublish := len(priorVersions) == 0

	// bumpSource is the globally-highest published version — `--bump` must
	// choose it BEFORE newVersion is known (Edge 2's own documented
	// consequence, 04-per-version-lifecycle.md §4), so a maintenance-line
	// publish uses explicit `--version` instead (F4 already trained
	// operators to do this for deprecate/retire).
	bumpSource := contractSemver{0, 0, 0}
	if !isFirstPublish {
		bumpSource = priorVersions[len(priorVersions)-1]
	}

	var newVersion contractSemver
	if *version != "" {
		newVersion, err = contractParseSemver(*version)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
			return 2
		}
	} else {
		newVersion = contractBump(bumpSource, *bump)
	}

	// baseline is Edge 2's own rule (max{v ∈ priorVersions : v < newVersion})
	// — NOT bumpSource above once newVersion names a maintenance line
	// (AC-8: publish 1.2 while 2.0 is published compares against 1.1). The
	// two agree whenever newVersion came from --bump (bumpSource is by
	// construction the highest prior version strictly less than its own
	// bump), so this never regresses the ordinary sequential-publish case.
	//
	// hasBaseline is NOT `!isFirstPublish`. Prior versions can exist with
	// none of them older than newVersion — publishing 1.0.0 on a contract
	// whose only published version is 2.0.0, i.e. opening a lower line while
	// a higher one is live. The fold permits it (a version key that has
	// never been minted), and the old globally-highest rule could not
	// produce this case because it always returned a real prior version.
	// Discarding `found` here let a zero-value 0.0.0 flow into
	// contractPriorVersionFiles, which git-searches for a descriptor version
	// that was never published and aborts the publish with a confusing
	// "no commit found" error. There IS no baseline in that case, so this
	// publish is treated exactly as a first publish is: nothing to compute
	// compatibility against, and gated for human review.
	baseline, hasBaseline := contractSelectBaseline(priorVersions, newVersion)

	verdict, _, err := lifecycleCheckLegality(c.deps.mirrorDir, c.deps.manifest, id, fold.TPublish, newVersion.String(), actor)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %s: %v\n", id, err)
		return 1
	}
	if verdict != fold.VerdictLegal {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %s\n", verdictRefusalMessage(id, verdict))
		return 1
	}

	fm, probe, relPath, relDir, err := contractReadDescriptor(c.deps.mirrorDir, id)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
		return 1
	}

	// P37 Wave I: `contract publish` carries the schema/fixtures change it
	// is declaring a version for. internal/space/mirror.go's
	// checkoutRemoteHead hard-resets the mirror working tree to
	// origin/<branch> on EVERY `a2a` invocation (a mirror is a cache — that
	// reset is correct and out of scope here), so by the time this verb
	// runs the mirror can never carry an author's own schema/fixture edit;
	// only .a2a/staging/ (never touched by the reset) still can. Without
	// this overlay the OLD and NEW sides of every check below always read
	// the SAME just-reset bytes and can never disagree — which is exactly
	// how §5.4b's computed compatibility check shipped unreachable (P37
	// live-tier finding fb-20260725; see this wave's own doc comment at
	// the top of this file's package).
	//
	// landedAll is this contract's schema/**+fixtures/** as the mirror's
	// OWN working tree currently holds it — the SAME two keying prefixes
	// ("schema/…", "fixtures/…") artifact.DigestTreeFS itself walks, kept
	// as ONE map so contractStagingOverlay can override it per file.
	workDir := filepath.Join(c.deps.mirrorDir, relDir)
	landedSchema, err := contractReadWorkingTreeFiles(workDir, "schema")
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
		return 1
	}
	landedFixtures, err := contractReadWorkingTreeFiles(workDir, "fixtures")
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
		return 1
	}
	landedAll := make(map[string][]byte, len(landedSchema)+len(landedFixtures))
	for k, v := range landedSchema {
		landedAll[k] = v
	}
	for k, v := range landedFixtures {
		landedAll[k] = v
	}

	// c.newCmd is P6's own `a2a new` command, reused here ONLY for its
	// StagingDir() accessor — it is nil in many existing tests/call sites
	// that predate this wave, and a nil newCmd (or a newCmd whose staging
	// dir holds nothing for THIS contract) must degrade to exactly today's
	// behaviour: overlayAll below then equals landedAll, unmodified.
	var stagedSidecars []space.FileWrite
	if c.newCmd != nil {
		if stagingDir := c.newCmd.StagingDir(); stagingDir != "" {
			parsed, perr := artifact.ParseID(id)
			if perr != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", perr)
				return 1
			}
			stagedSidecars, err = template.ContractSidecarsFromStaging(stagingDir, parsed.System, parsed.Slug)
			if err != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
				return 1
			}
		}
	}
	overlayAll := contractStagingOverlay(landedAll, stagedSidecars, relDir)

	// D-D/POL-009: a JSON-Schema contract must publish an actual baseline
	// (schema/** + fixtures/valid/**) before publish records a version
	// anything else will trust as compat-checkable. This runs on EVERY
	// publish, including the first — CheckContractPublishable itself no-ops
	// for a non-JSON-Schema schema_format (validate.IsJSONSchemaFormat), so
	// calling it unconditionally here is always safe. newSchemas is also
	// F1's own NewSchemas input below (derived once from overlayAll, used
	// twice) — both counted from the OVERLAY, never the landed tree alone,
	// so POL-009 sees the version this commit is actually about to carry.
	newSchemas := map[string][]byte{}
	newFixturesValid := map[string][]byte{}
	for k, v := range overlayAll {
		switch {
		case k == "schema" || strings.HasPrefix(k, "schema/"):
			newSchemas[k] = v
		case k == "fixtures/valid" || strings.HasPrefix(k, "fixtures/valid/"):
			newFixturesValid[k] = v
		}
	}
	if violation := validate.CheckContractPublishable(validate.PublishableInput{
		SchemaFormat:  probe.SchemaFormat,
		ContractID:    id,
		Schemas:       len(newSchemas),
		ValidFixtures: len(newFixturesValid),
	}); violation != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %s: refused: %s (%s)\n", id, violation.Message, violation.Code)
		return 1
	}

	// G1/newVersion resolution moved above the legality check (P4) — see
	// this function's own comment there. all/priorVersions/isFirstPublish/
	// baseline/newVersion are already in scope.

	// G2: a self-declared MAJOR bump on a non-first publish.
	isMajorBump := hasBaseline && newVersion[0] > baseline[0]
	gated := !hasBaseline || isMajorBump

	// F1/POL-007/POL-008 (D-010, §5.4b): computed compatibility. Only makes
	// sense once there IS a prior version to compare against, and only for
	// a JSON-Schema dialect (validate.IsJSONSchemaFormat) — for
	// openapi-3.x/proto3/other, §5.4b hands deep compatibility to the
	// owner's own CI, so this engine has nothing to compute. A MAJOR bump
	// is still handed to the core (not special-cased out here): it answers
	// Computed:false with a Reason by design (D-B), and printing that
	// Reason the SAME way a minor/patch's "nothing computed" prints is
	// AC-970.3 — a caller-side special case would just re-derive the same
	// sentence the core already owns.
	if hasBaseline && validate.IsJSONSchemaFormat(probe.SchemaFormat) {
		declaredBump := *bump
		if declaredBump == "" {
			// --version was used instead of --bump: classify the jump from
			// baseline -> newVersion using the SAME major > minor > patch
			// precedence contractBump's own kind vocabulary uses, rather
			// than leaving DeclaredBump empty (which the core would not
			// recognize as "major" and so would treat as a checkable
			// minor/patch bump regardless of the operator's real intent).
			declaredBump = contractInferBumpKind(baseline, newVersion)
		}
		_, priorFixturesValid, perr := contractPriorVersionFiles(ctx, c.deps.mirrorDir, relPath, relDir, baseline.String())
		if perr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", perr)
			return 1
		}
		compat := validate.CheckComputedCompatibility(validate.CompatInput{
			DeclaredBump:  declaredBump,
			PriorVersion:  baseline.String(),
			NewVersion:    newVersion.String(),
			NewSchemas:    newSchemas,
			PriorFixtures: priorFixturesValid,
		})
		switch {
		case compat.Violation != nil:
			// compat.Violation.Message already NAMES the offending
			// fixture(s) (AC-970.1) — POL-007 joins compat.Failures'
			// fixture paths into its own Message, POL-008 names the one
			// fixture/schema that broke the baseline; neither needs
			// re-deriving here.
			_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %s: refused: %s (%s)\n", id, compat.Violation.Message, compat.Violation.Code)
			return 1
		case !compat.Computed:
			// D-B: "checked and compatible" must be visibly different from
			// "nothing was computed" — print the core's own Reason sentence
			// and CONTINUE (this is not a refusal).
			_, _ = fmt.Fprintln(stdio.Stdout, compat.Reason)
		default:
			_, _ = fmt.Fprintf(stdio.Stdout, "contract publish: %s: computed compatibility confirmed (%s -> %s)\n", id, baseline.String(), newVersion.String())
		}
	}

	// F2 (D-010 atomicity) is DELIBERATELY NOT ENFORCED HERE, and the
	// reason is a defect this phase found rather than a corner it cut.
	//
	// D-A specified a precondition: a major bump is refused unless the prior
	// major is already deprecated, so the deprecation always lands first and
	// no window opens in which a new major is published while the old one is
	// still undeclared. That rule was built, and it turned out to forbid the
	// very sequence it exists to allow.
	//
	// internal/fold models a contract's lifecycle per SUBJECT, not per
	// VERSION — fold.Event carries no version field at all — so a single
	// `deprecate` puts the WHOLE contract in StateDeprecated, and
	// contractRows() (internal/fold/table.go) has no (Deprecated, publish)
	// row. Deprecate the prior major first and the publish that was supposed
	// to follow is refused as an illegal transition (LFC-001), before any
	// check here runs. Publish first and the same wall arrives one version
	// later. Either way a contract can never publish a successor once
	// anything has been deprecated.
	//
	// That is a pre-existing product defect, not P37's: docs/the-plan/plan/
	// 05-schemas.md:118 describes a major-publish-after-deprecation flow the
	// state machine forbids, and P36's live row never hit it because it
	// publishes exactly once. Enforcing F2 on top of it would brick major
	// publishes outright, so the enforcement is withdrawn (operator decision
	// 2026-07-25) and the fold's per-version lifecycle is filed as its own
	// phase. Spec 37 §11 records this; F2 stays open there rather than
	// counting as shipped.

	now := c.deps.now()
	eventID, err := artifact.MintULIDAt(now, c.deps.entropy)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: cannot mint event id: %v\n", err)
		return 1
	}

	// Update the descriptor's own `version` (and generated_from, if
	// given) in place — decode/mutate/re-encode the frontmatter map,
	// never hand-editing YAML text (rails: no ad-hoc text surgery on a
	// structured document).
	var doc map[string]any
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
		return 1
	}
	doc["version"] = newVersion.String()
	if *generatedFromDigest != "" {
		gf, _ := doc["generated_from"].(map[string]any)
		if gf == nil {
			gf = map[string]any{"tool": probe.GeneratedFrom.Tool}
		}
		gf["source_digest"] = *generatedFromDigest
		doc["generated_from"] = gf
	}
	newYAML, err := yaml.Marshal(doc)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
		return 1
	}
	newRaw := artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: newYAML, Body: fm.Body})

	files := []space.FileWrite{{Path: relPath, Content: newRaw}}
	// Carry EVERY staged sidecar into this commit verbatim — never diffed
	// against the landed tree first (deterministic beats clever; an
	// identical file is simply a no-op once git sees it).
	files = append(files, stagedSidecars...)

	// §5.7/D-029 multi-file digest tree over the published schema/**+
	// fixtures/** — computed from overlayAll (P37 Wave I), not the mirror
	// tree alone: the recorded digest must describe what THIS COMMIT
	// CARRIES, not merely what the mirror happened to hold before staging
	// was folded in. Uses artifact.Digest + artifact.CombineDigestPairs
	// DIRECTLY — the exact two calls artifact.DigestTreeFS itself makes per
	// file — rather than approximating them, so this can never silently
	// diverge from DigestTreeFS's own answer over the same bytes (contract
	// verify-export/§5.7 both depend on that). When staging carries
	// nothing for this contract, overlayAll IS landedAll unmodified and
	// this reproduces DigestTreeFS's answer byte for byte (no-regression).
	perFileDigest := make(map[string]string, len(overlayAll))
	for k, v := range overlayAll {
		perFileDigest[k] = artifact.Digest(v)
	}
	digest := artifact.CombineDigestPairs(perFileDigest)

	ev := lifecycleEventDoc{
		Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
		Subject: id, Transition: fold.TPublish,
		Actor:   lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
		At:      now.UTC().Format(time.RFC3339),
		Version: newVersion.String(), Digest: digest,
	}
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
		return 1
	}
	raw, merr := yaml.Marshal(ev)
	if merr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: cannot encode event: %v\n", merr)
		return 1
	}
	files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})

	req := c.deps.buildRequest([]string{id}, files, "contract-publish", gated)
	return c.deps.submit(ctx, req, "contract publish", []string{id}, stdio)
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

// contractInferBumpKind classifies the jump from baseline to newVersion
// using contractBump's own major/minor/patch vocabulary. It is F1's
// fallback for a publish that gave --version rather than --bump, so
// DeclaredBump is never left empty (which the compat core would not
// recognize as "major" and so would compat-check regardless of what the
// operator actually meant to declare).
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
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %v\n", err)
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
	verdict, _, err := lifecycleCheckLegality(c.deps.mirrorDir, c.deps.manifest, id, fold.TDeprecate, legalityVersion, actor)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %s: %v\n", id, err)
		return 1
	}
	if verdict != fold.VerdictLegal {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %s\n", verdictRefusalMessage(id, verdict))
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
		Subject: id, Transition: fold.TDeprecate, Version: legalityVersion,
		Actor: lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
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
	announcementPublishEvent := lifecycleEventDoc{
		Schema: "event/v1", Event: announcementPublishEventID.String(), Space: probe.Space,
		Subject: announcementID, Transition: fold.TPublish,
		Actor: lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
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
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %v\n", err)
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
	verdict, _, err := lifecycleCheckLegality(c.deps.mirrorDir, c.deps.manifest, id, fold.TRetire, legalityVersion, actor)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %s: %v\n", id, err)
		return 1
	}
	if verdict != fold.VerdictLegal {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %s\n", verdictRefusalMessage(id, verdict))
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
		Subject: id, Transition: fold.TRetire, Version: legalityVersion,
		Actor: lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
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
	}, nil
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
	fs := flag.NewFlagSet("contract diff", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	jsonOut := fs.Bool("json", false, "JSON output")
	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 3 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract diff <id> <v1> <v2> [--json]")
		return 2
	}
	id, v1, v2 := positional[0], positional[1], positional[2]
	if v1 == v2 {
		_, _ = fmt.Fprintln(stdio.Stderr, "contract diff: v1 and v2 are the same version")
		return 1
	}

	_, _, relPath, relDir, err := contractReadDescriptor(c.deps.mirrorDir, id)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract diff: %v\n", err)
		return 1
	}

	sha1, err := contractResolveVersionSHA(ctx, c.deps.mirrorDir, relPath, v1)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract diff: %s: %v\n", v1, err)
		return 1
	}
	sha2, err := contractResolveVersionSHA(ctx, c.deps.mirrorDir, relPath, v2)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract diff: %s: %v\n", v2, err)
		return 1
	}

	tree1, err := contractDigestTreeAtSHA(ctx, c.deps.mirrorDir, sha1, relDir)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract diff: %v\n", err)
		return 1
	}
	tree2, err := contractDigestTreeAtSHA(ctx, c.deps.mirrorDir, sha2, relDir)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract diff: %v\n", err)
		return 1
	}

	delta := contractDiff(tree1, tree2)

	// F5/AC-975.1: contractDiff above only ever sees the schema/**+
	// fixtures/** file digests, so a change confined to contract.md itself
	// — including compat_policy — is invisible to it. contractResolveVersionSHA
	// already resolved sha1/sha2 above; reading the descriptor probe at
	// each is the same git-show read that function performed internally to
	// find them.
	probe1, err := contractDescriptorProbeAtSHA(ctx, c.deps.mirrorDir, relPath, sha1)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract diff: %v\n", err)
		return 1
	}
	probe2, err := contractDescriptorProbeAtSHA(ctx, c.deps.mirrorDir, relPath, sha2)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract diff: %v\n", err)
		return 1
	}
	delta.FrontmatterChanged = contractFrontmatterDiff(probe1, probe2)

	if *jsonOut {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(delta)
		return 0
	}
	for _, p := range delta.Added {
		_, _ = fmt.Fprintf(stdio.Stdout, "added   %s\n", p)
	}
	for _, p := range delta.Removed {
		_, _ = fmt.Fprintf(stdio.Stdout, "removed %s\n", p)
	}
	for _, p := range delta.Changed {
		_, _ = fmt.Fprintf(stdio.Stdout, "changed %s\n", p)
	}
	for _, f := range delta.FrontmatterChanged {
		_, _ = fmt.Fprintf(stdio.Stdout, "frontmatter %s\n", f)
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
// <id>[@version]` (AC-1001.1).
func (c *ContractCommand) runVerifyExport(_ context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("contract verify-export", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	local := fs.String("local", "", "local export path to compare against the committed digest (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *local == "" || fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract verify-export --local <path> <id>[@version]")
		return 2
	}
	ref := fs.Arg(0)
	id, version, _ := splitRefGrammar(ref)

	_, probe, _, relDir, err := contractReadDescriptor(c.deps.mirrorDir, id)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract verify-export: %v\n", err)
		return 1
	}

	var wantDigest string
	if version != "" {
		all, aerr := lifecycleReadAllEvents(c.deps.mirrorDir)
		if aerr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "contract verify-export: %v\n", aerr)
			return 1
		}
		for _, ev := range all {
			if ev.Subject == id && ev.Transition == fold.TPublish && ev.Version == version {
				wantDigest = ev.Digest
			}
		}
		if wantDigest == "" {
			_, _ = fmt.Fprintf(stdio.Stderr, "contract verify-export: no recorded digest for %s@%s\n", id, version)
			return 1
		}
	} else {
		wantDigest = probe.GeneratedFrom.SourceDigest
		if wantDigest == "" {
			_, _ = fmt.Fprintf(stdio.Stderr, "contract verify-export: %s has no generated_from.source_digest recorded\n", id)
			return 1
		}
	}

	localDigest, localPerFile, err := artifact.DigestTreeFS(*local, contractDigestSubtrees)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract verify-export: %v\n", err)
		return 1
	}
	if localDigest == wantDigest {
		_, _ = fmt.Fprintf(stdio.Stdout, "contract verify-export: %s matches (%s)\n", id, localDigest)
		return 0
	}

	_, spacePerFile, serr := artifact.DigestTreeFS(filepath.Join(c.deps.mirrorDir, relDir), contractDigestSubtrees)
	if serr == nil {
		delta := contractDiff(spacePerFile, localPerFile)
		for _, p := range delta.Added {
			_, _ = fmt.Fprintf(stdio.Stdout, "added   %s\n", p)
		}
		for _, p := range delta.Removed {
			_, _ = fmt.Fprintf(stdio.Stdout, "removed %s\n", p)
		}
		for _, p := range delta.Changed {
			_, _ = fmt.Fprintf(stdio.Stdout, "changed %s\n", p)
		}
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "contract verify-export: digest mismatch: local=%s want=%s\n", localDigest, wantDigest)
	return 1
}
