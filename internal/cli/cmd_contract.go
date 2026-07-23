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
	"flag"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// contractDescriptorProbe is this file's own minimal decode of a
// contract's descriptor (contract.md) fields (§5.2.1's contract-only
// extensions) — a richer sibling of lifecycleEnvelopeProbe (which only
// carries the base envelope fields every OP-211 verb needs).
type contractDescriptorProbe struct {
	ID            string `yaml:"id"`
	Space         string `yaml:"space"`
	From          string `yaml:"from"`
	Version       string `yaml:"version"`
	CompatPolicy  string `yaml:"compat_policy"`
	SchemaFormat  string `yaml:"schema_format"`
	GeneratedFrom struct {
		Tool         string `yaml:"tool"`
		SourceDigest string `yaml:"source_digest"`
	} `yaml:"generated_from"`
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
// the caller).
type contractDiffTree struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Changed []string `json:"changed"`
}

func contractDiff(a, b map[string]string) contractDiffTree {
	var out contractDiffTree
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
func NewContractCommand(newCmd *NewCommand, funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *ContractCommand {
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
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract <new|publish|deprecate|retire|diff|verify-export> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
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
	}
}

// runNew translates `contract new <slug>` into P6's `a2a new contract
// --slug <slug>` path (spec 08 T1: "thin alias... do not forward args
// verbatim; P6's NewCommand takes the slug as a flag").
func (c *ContractCommand) runNew(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract new <slug> [--field k=v]...")
		return 2
	}
	slug := args[0]
	delegated := append([]string{"contract", "--slug", slug}, args[1:]...)
	return c.newCmd.Run(ctx, delegated, stdio)
}

// runPublish implements `a2a contract publish <id> [--version <semver> |
// --bump major|minor|patch] [--generated-from-digest <hex>]`.
func (c *ContractCommand) runPublish(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("contract publish", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	version := fs.String("version", "", "explicit semver to publish")
	bump := fs.String("bump", "", "major|minor|patch (bump the prior published version)")
	generatedFromDigest := fs.String("generated-from-digest", "", "optional §5.3 generated_from.source_digest to record")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract publish <id> [--version <semver>|--bump major|minor|patch]")
		return 2
	}
	id := fs.Arg(0)
	if *version == "" && *bump == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "contract publish: one of --version or --bump is required")
		return 2
	}

	resolved := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	verdict, _, err := lifecycleCheckLegality(c.deps.mirrorDir, c.deps.manifest, id, fold.TPublish, actor)
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

	// G1: no PRIOR publish event at all for this contract.
	all, err := lifecycleReadAllEvents(c.deps.mirrorDir)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
		return 1
	}
	priorVersions := contractPublishedVersions(all, id)
	isFirstPublish := len(priorVersions) == 0

	baseline := contractSemver{0, 0, 0}
	if !isFirstPublish {
		baseline = priorVersions[len(priorVersions)-1]
	}

	var newVersion contractSemver
	if *version != "" {
		newVersion, err = contractParseSemver(*version)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
			return 2
		}
	} else {
		newVersion = contractBump(baseline, *bump)
	}

	// G2: a self-declared MAJOR bump on a non-first publish.
	isMajorBump := !isFirstPublish && newVersion[0] > baseline[0]
	gated := isFirstPublish || isMajorBump

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

	// §5.7/D-029 multi-file digest tree over the published schema/**+
	// fixtures/** — computed from the CURRENT working tree (the mirror
	// already carries this contract's schema/fixtures files; publish
	// itself never rewrites them, only the descriptor).
	digest, _, derr := artifact.DigestTreeFS(filepath.Join(c.deps.mirrorDir, relDir), contractDigestSubtrees)
	if derr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: cannot compute digest tree: %v\n", derr)
		return 1
	}

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

// runDeprecate implements `a2a contract deprecate <id> [--version
// <semver>] --successor <XC-id@version> --sunset <date>`: authors the
// deprecate event AND a linked deprecation announcement in the same PR
// (§5.4).
func (c *ContractCommand) runDeprecate(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("contract deprecate", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	version := fs.String("version", "", "version scope (omit = whole-contract)")
	successor := fs.String("successor", "", "successor XC-id@version (required)")
	sunset := fs.String("sunset", "", "sunset date, YYYY-MM-DD (required)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *successor == "" || *sunset == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract deprecate <id> --successor <XC-id@version> --sunset <date>")
		return 2
	}
	id := fs.Arg(0)

	resolved := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	verdict, _, err := lifecycleCheckLegality(c.deps.mirrorDir, c.deps.manifest, id, fold.TDeprecate, actor)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %s: %v\n", id, err)
		return 1
	}
	if verdict != fold.VerdictLegal {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %s\n", verdictRefusalMessage(id, verdict))
		return 1
	}
	_, probe, _, _, err := contractReadDescriptor(c.deps.mirrorDir, id)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract deprecate: %v\n", err)
		return 1
	}
	deprecatedVersion := *version
	if deprecatedVersion == "" {
		deprecatedVersion = probe.Version
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
	deprecateEvent := lifecycleEventDoc{
		Schema: "event/v1", Event: deprecateEventID.String(), Space: probe.Space,
		Subject: id, Transition: fold.TDeprecate, Version: deprecatedVersion,
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
	announcementDraft, err = contractAddFrontmatterFields(announcementDraft, map[string]any{
		"ack_requested": true,
		"deprecates":    id + "@" + deprecatedVersion,
		"valid_until":   *sunset,
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
// hook (never re-derived here).
func (c *ContractCommand) runRetire(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("contract retire", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	version := fs.String("version", "", "version scope")
	override := fs.Bool("override", false, "human-gated override (§5.4)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract retire <id> [--version <semver>] [--override]")
		return 2
	}
	id := fs.Arg(0)

	resolved := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	verdict, _, err := lifecycleCheckLegality(c.deps.mirrorDir, c.deps.manifest, id, fold.TRetire, actor)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %s: %v\n", id, err)
		return 1
	}
	if verdict != fold.VerdictLegal {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %s\n", verdictRefusalMessage(id, verdict))
		return 1
	}
	_, probe, _, _, err := contractReadDescriptor(c.deps.mirrorDir, id)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract retire: %v\n", err)
		return 1
	}
	retiredVersion := *version
	if retiredVersion == "" {
		retiredVersion = probe.Version
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
	ev := lifecycleEventDoc{
		Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
		Subject: id, Transition: fold.TRetire, Version: retiredVersion,
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
func contractBuildRetirePrecondition(mirrorDir string, manifest space.Manifest, contractID, version string, override, actorIsHuman bool, now time.Time) (validate.RetirePrecondition, error) {
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return validate.RetirePrecondition{}, err
	}

	// Find the deprecation announcement for this contract@version (its
	// `refs[0].ref` on the contract's own `deprecate` event names the
	// successor, not the announcement — the announcement instead is found
	// by its own `deprecates` field, read off the committed artifact).
	announcementID, sunset, err := contractFindDeprecationAnnouncement(mirrorDir, contractID, version)
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

	consumerSystems, err := contractFindRegisteredConsumers(mirrorDir, contractID)
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

// contractFindRegisteredConsumers is §5.2.3/D-022's union: every system
// with a `satisfied` requirement whose `target_contract` names
// contractID, OR a `consumes.yaml` entry naming it.
func contractFindRegisteredConsumers(mirrorDir, contractID string) (map[string]bool, error) {
	out := map[string]bool{}

	reqMatches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "requires", "XR-*.md"))
	if err != nil {
		return nil, err
	}
	for _, m := range reqMatches {
		raw, rerr := readBoundedFile(m, maxMirrorEventBytes)
		if rerr != nil {
			return nil, rerr
		}
		fm, ferr := artifact.ParseFrontmatter(raw)
		if ferr != nil {
			continue
		}
		var probe struct {
			ID             string `yaml:"id"`
			From           string `yaml:"from"`
			TargetContract string `yaml:"target_contract"`
		}
		if yaml.Unmarshal(fm.YAML, &probe) != nil || probe.TargetContract != contractID {
			continue
		}
		// Determine the requirement's OWN folded state directly (no
		// membership needed — Fold's own zero-events fallback / table
		// lookup is membership-agnostic for reading state, only
		// authorization checks consult membership, which this read-only
		// resolution does not need).
		all, aerr := lifecycleReadAllEvents(mirrorDir)
		if aerr != nil {
			return nil, aerr
		}
		events := lifecycleFoldEvents(all, probe.ID)
		var state fold.State
		if len(events) == 0 {
			state = fold.NewResult(fold.KindRequirement).State
		} else {
			state = fold.Fold(fold.KindRequirement, fold.Envelope{ID: probe.ID, Kind: fold.KindRequirement, From: probe.From}, events, func(string) fold.MembershipStatus { return fold.MembershipMember }).State
		}
		if state == fold.StateSatisfied {
			out[probe.From] = true
		}
	}

	consumesMatches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "consumes.yaml"))
	if err != nil {
		return nil, err
	}
	for _, m := range consumesMatches {
		raw, rerr := readBoundedFile(m, maxMirrorEventBytes)
		if rerr != nil {
			return nil, rerr
		}
		var doc struct {
			System       string `yaml:"system"`
			Dependencies []struct {
				Contract string `yaml:"contract"`
			} `yaml:"dependencies"`
		}
		if yaml.Unmarshal(raw, &doc) != nil {
			continue
		}
		for _, d := range doc.Dependencies {
			if d.Contract == contractID {
				out[doc.System] = true
			}
		}
	}
	return out, nil
}

// runDiff implements `a2a contract diff <id> <v1> <v2> [--json]`.
func (c *ContractCommand) runDiff(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("contract diff", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 3 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract diff <id> <v1> <v2> [--json]")
		return 2
	}
	id, v1, v2 := fs.Arg(0), fs.Arg(1), fs.Arg(2)
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
	return 0
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
