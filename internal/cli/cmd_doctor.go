// OP-218 basic doctor (spec 09 T1). This file's only package-level symbols
// are DoctorCommand + NewDoctorCommand plus its own uniquely-named,
// file-private helpers (doctor* prefix) — no shared helper, no package var,
// per this phase's plan Placement decision (avoids collision with P6/P7/P8's
// parallel verb files in this same package).
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/surface"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// DoctorCommand implements the basic (non-`--space`) `a2a doctor` verb: one
// line per check, exit 0 iff all pass. `--space` (the v2 admin host-drift
// diff, D-030) is rejected explicitly, never silently ignored.
//
// The check LIST lives in Run's `checks` slice and nowhere else. This comment
// used to enumerate "the five OP-218 checks" and the set had grown to ten —
// the same drift that made Synopsis() lie for two releases. The enumeration
// belongs somewhere it can be diffed against the code; here it can only rot.
type DoctorCommand struct {
	binaryVersion     string
	projectConfigPath string
	machineConfigPath string
	projectRoot       string
	h                 host.Host

	// TemplateFiles is the embedded space-template/ tree (spacetemplate.Files
	// — mirrors SpaceCommand.TemplateFiles' own role and doc). Exported and
	// left NIL by NewDoctorCommand ("nil means not wired", this package's DI
	// convention — see SpaceCommand's own six space-update-only fields): the
	// lead wires it post-construction in cmd/a2a (`cmd.TemplateFiles =
	// spacetemplate.Files`, the same shape update/init already use for
	// SkillFiles), because internal/cli must not import space-template
	// directly. The "space scaffolding current" check
	// (doctorCheckScaffoldingCurrent) reports "could not be checked" rather
	// than nil-panicking or silently skipping when this is unset.
	TemplateFiles fs.FS

	// The following are real-implementation-backed seams (rails DI):
	// NewDoctorCommand defaults every one of them to the real internal/space
	// (and stdlib) operation; tests override individual fields to drive each
	// check to both pass and fail without a live git remote or real
	// credentials.
	loadProjectConfig func(path string) (space.ProjectConfig, error)
	loadMachineConfig func(path string) (space.MachineConfig, error)
	resolveMirror     func(projectRoot string, ref space.Ref, machine space.MachineConfig) string
	cloneOrFetch      func(ctx context.Context, dir, repoURL string) error
	resolveCredential func(ctx context.Context, explicitEnvVar string, ref space.CredentialReference) (host.Credential, error)
	readFile          func(path string) ([]byte, error)
	lookupGit         func() error
	// cachePath resolves the spec 19 T3 update-check cache location for the
	// "versions" check's advisory half (doctorCheckVersions): defaults to
	// release.CachePath; tests override it to point at a seeded temp cache.
	cachePath func() (string, error)
}

// NewDoctorCommand constructs the basic doctor command. h is the host
// adapter (injected for parity with the rest of this package's DI
// convention; the CI-presence check's required-check-config half is NOT
// implemented against it today — see checkCIPresence's doc comment and this
// phase's reported deviation). binaryVersion is this build's own version
// stamp (§7.3, injected rather than read from a build var so tests control
// it). projectConfigPath/machineConfigPath are `.a2a/config.yaml` and
// `~/.config/a2a/config.yaml` (§7.4); projectRoot resolves each connected
// space's mirror directory (space.ResolveMirrorLocation) when a space's
// config entry does not carry an absolute mirror location.
func NewDoctorCommand(h host.Host, binaryVersion, projectConfigPath, machineConfigPath, projectRoot string) *DoctorCommand {
	return &DoctorCommand{
		binaryVersion:     binaryVersion,
		projectConfigPath: projectConfigPath,
		machineConfigPath: machineConfigPath,
		projectRoot:       projectRoot,
		h:                 h,
		loadProjectConfig: space.LoadProjectConfig,
		loadMachineConfig: space.LoadMachineConfig,
		resolveMirror:     space.ResolveMirrorLocation,
		cloneOrFetch:      space.CloneOrFetch,
		resolveCredential: space.ResolveCredential,
		readFile:          os.ReadFile,
		lookupGit:         func() error { _, err := exec.LookPath("git"); return err },
		cachePath:         release.CachePath,
	}
}

// Name implements cli.Command.
func (c *DoctorCommand) Name() string { return "doctor" }

// Synopsis implements cli.Command.
//
// Deliberately NOT an enumeration of the check names. It used to list five of
// them and the check set grew to nine, so the one artifact an agent is told is
// "generated from the binary, the source of truth for invocation syntax"
// (skill/a2ahub/reference/commands.md) described a doctor that had not existed
// for two releases. The `skill-drift` gate could not catch it either: it
// regenerates from this same string and byte-diffs, so a stale sentence here
// stays green forever. A summary cannot go stale that way; the enumeration lives
// where it can be checked against `checks` — troubleshooting.md's table.
func (c *DoctorCommand) Synopsis() string {
	return "run local health checks over every connected space (credentials, mirror access, identity, versions, CI, space scaffolding, auto-merge, CODEOWNERS, statusline, skill) — see troubleshooting.md for what each FAIL means"
}

// Run implements cli.Command. Exit codes: 2 = usage error (including the
// rejected `--space` flag); 1 = one or more checks failed, or the local
// config could not be loaded; 0 = every check passed.
func (c *DoctorCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	spaceFlag := fs.Bool("space", false, "admin host-drift diff (v2, not available in v1-min, D-030)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *spaceFlag {
		_, _ = fmt.Fprintln(stdio.Stderr, "doctor: --space: v1-min: not available")
		return 2
	}

	cfg, err := c.loadProjectConfig(c.projectConfigPath)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "doctor: cannot load project config %s: %v\n", c.projectConfigPath, err)
		return 1
	}
	machine, err := c.loadMachineConfig(c.machineConfigPath)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "doctor: cannot load machine config %s: %v\n", c.machineConfigPath, err)
		return 1
	}

	checks := []struct {
		name string
		run  func() (bool, string)
	}{
		{"credentials", func() (bool, string) { return c.doctorCheckCredentials(ctx, cfg, machine) }},
		{"space access", func() (bool, string) { return c.doctorCheckSpaceAccess(ctx, cfg, machine) }},
		{"space identity", func() (bool, string) { return c.doctorCheckSpaceIdentity(cfg, machine) }},
		{"versions", func() (bool, string) { return c.doctorCheckVersions(cfg, machine) }},
		{"CI presence", func() (bool, string) { return c.doctorCheckCIPresence(cfg, machine) }},
		{"space scaffolding current", func() (bool, string) { return c.doctorCheckScaffoldingCurrent(ctx, cfg, machine) }},
		{"auto-merge enabled", func() (bool, string) { return c.doctorCheckAutoMerge(ctx, cfg, machine) }},
		{"codeowners resolvable", func() (bool, string) { return c.doctorCheckCodeownersResolvable(ctx, cfg, machine) }},
		{"statusline wiring", func() (bool, string) { return c.doctorCheckStatuslineWiring() }},
		{"skill discoverable", func() (bool, string) { return c.doctorCheckSkillDiscoverable() }},
		{"skill manual current", func() (bool, string) { return c.doctorCheckSkillManualCurrent() }},
	}

	allOK := true
	for _, chk := range checks {
		ok, detail := chk.run()
		if ok {
			// detail is normally empty on PASS, EXCEPT "versions", which
			// carries the spec 19 T4 update-available advisory (already
			// prefixed " · " by doctorCheckVersions) even when the floor
			// comparison itself passes — AC #7 requires doctor to actually
			// REPORT that advisory, not just compute it.
			_, _ = fmt.Fprintf(stdio.Stdout, "%s: PASS%s\n", chk.name, detail)
			continue
		}
		allOK = false
		_, _ = fmt.Fprintf(stdio.Stdout, "%s: FAIL: %s\n", chk.name, detail)
	}

	if !allOK {
		return 1
	}
	return 0
}

// doctorCheckCredentials resolves a write credential for every connected
// space exactly the way a WRITE does (§7.4/§10.5): the explicit
// A2A_TOKEN_<SPACE_ID> override first, the machine-config reference
// second. Checking only the reference (as this did before) made doctor
// disagree with `a2a submit` in both directions — red on a perfectly
// working exported token, green on a reference that only submit's own
// precedence would have rejected. A space whose credential resolves
// through NEITHER path fails this check with an actionable per-space
// message naming what was checked.
//
// Deviation (see this phase's report): neither space.Manifest.Participant
// nor space.MachineConfig models a credential EXPIRY field today, so "not
// expired" (plan §9.3 "a2a doctor warns on approaching expiry") is not
// verifiable against any exported core API yet — this check covers
// present+readable only.
func (c *DoctorCommand) doctorCheckCredentials(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	ok := true
	var failures, advisories []string
	for _, ref := range cfg.Spaces {
		var parsedRef space.CredentialReference
		if raw, present := machine.Credentials[ref.ID]; present {
			if parsed, err := space.ParseCredentialReference(raw); err == nil {
				parsedRef = parsed
			}
		}
		cred, err := c.resolveCredential(ctx, space.CredentialEnvVar(ref.ID), parsedRef)
		if err != nil {
			ok = false
			failures = append(failures, fmt.Sprintf("%s: %v", ref.ID, err))
			continue
		}
		if note := c.doctorWorkflowScopeNote(ctx, ref.ID, cred); note != "" {
			advisories = append(advisories, note)
		}
	}
	if !ok {
		return false, strings.Join(failures, "; ")
	}
	// PASS-with-advisory (the `versions` precedent): a missing `workflow`
	// scope must NOT fail this check. No ordinary participant needs it — every
	// artifact write is confined to the caller's own section, and .github/ is
	// reachable only through `a2a space update`. Failing here would red the
	// whole fleet over a capability almost nobody uses.
	//
	// The " · " prefix is the renderer's convention for PASS-with-detail: the
	// check supplies it, exactly as doctorCheckVersions does.
	if len(advisories) == 0 {
		return true, ""
	}
	return true, " · " + strings.Join(advisories, "; ")
}

// doctorWorkflowScopeNote returns an advisory when the space's credential
// provably lacks the `workflow` scope — the capability `a2a space update`
// needs to rewrite the space's CI caller, and the one whose absence otherwise
// surfaces as a raw git push rejection midway through that command.
//
// Silence (a fine-grained PAT or App token, which do not advertise scopes) is
// reported as nothing at all: an advisory that fires on the most narrowly
// scoped credentials would train people to ignore it.
func (c *DoctorCommand) doctorWorkflowScopeNote(ctx context.Context, spaceID string, cred host.Credential) string {
	if c.h == nil || cred.Token == "" {
		return ""
	}
	scopes, reported, err := c.h.TokenScopes(ctx, cred)
	if err != nil || !reported {
		return ""
	}
	for _, s := range scopes {
		if s == "workflow" {
			return ""
		}
	}
	return fmt.Sprintf("%s: token has no `workflow` scope — fine for participating (submit/lifecycle/contract never touch .github/), "+
		"but `a2a space update` would be refused; grant it with `gh auth refresh -h github.com -s workflow` if you own this space", spaceID)
}

// doctorCheckSpaceAccess ensures every connected space's mirror clone is
// fetchable (space.CloneOrFetch clones on first use, fetches thereafter).
func (c *DoctorCommand) doctorCheckSpaceAccess(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	ok := true
	var failures []string
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		if err := c.cloneOrFetch(ctx, dir, ref.RepoURL); err != nil {
			ok = false
			failures = append(failures, fmt.Sprintf("%s: %v", ref.ID, err))
		}
	}
	return ok, strings.Join(failures, "; ")
}

// doctorCheckSpaceIdentity verifies that every connected space's
// CONFIGURED id equals the id the space itself declares in its space.yaml
// (`space:`). They can differ silently: `a2a init -space <url>` never
// clones, so it derives the id from the repo URL — a repo whose basename
// is not its space id (the documented `a2a` vs `getvisa` case) leaves
// .a2a/config.yaml naming a space that does not exist. Nothing caught it:
// doctor never compared the two, so it reported a healthy setup while the
// first `a2a submit` failed and told the operator to run `a2a connect` —
// the command they had already run. This check names the mismatch and the
// one-command fix.
func (c *DoctorCommand) doctorCheckSpaceIdentity(cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	ok := true
	var failures []string
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		raw, err := c.readFile(filepath.Join(dir, "space.yaml"))
		if err != nil {
			// The mirror is unreachable — "space access"/"versions" already
			// report that; do not double-fail on the same root cause.
			continue
		}
		manifest, err := space.ParseManifest(raw)
		if err != nil || manifest.Space == "" {
			continue // "versions" reports an unparseable manifest
		}
		if manifest.Space != ref.ID {
			ok = false
			failures = append(failures, fmt.Sprintf(
				"configured id %q but the space declares %q — run `a2a connect %s` to correct it",
				ref.ID, manifest.Space, ref.RepoURL))
		}
	}
	return ok, strings.Join(failures, "; ")
}

// doctorCheckVersions compares this build's binaryVersion against each
// connected space's space.yaml min_binary_version pin (§7.3, CC-085's
// sibling read-only check — the write funnel enforces the write-time
// refusal; this check only reports the mismatch). It reads space.yaml
// straight from the space's mirror working tree; a mirror the "space
// access" check could not reach also fails this check (a stale/absent
// mirror has nothing to compare against).
func (c *DoctorCommand) doctorCheckVersions(cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	ok := true
	var failures []string
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		raw, err := c.readFile(filepath.Join(dir, "space.yaml"))
		if err != nil {
			ok = false
			failures = append(failures, fmt.Sprintf("%s: cannot read space.yaml: %v", ref.ID, err))
			continue
		}
		manifest, err := space.ParseManifest(raw)
		if err != nil {
			ok = false
			failures = append(failures, fmt.Sprintf("%s: %v", ref.ID, err))
			continue
		}
		older, err := doctorVersionOlder(c.binaryVersion, manifest.MinBinaryVersion)
		if err != nil {
			ok = false
			failures = append(failures, fmt.Sprintf("%s: %v", ref.ID, err))
			continue
		}
		if older {
			ok = false
			failures = append(failures, fmt.Sprintf("%s: local binary %s is older than min_binary_version %s", ref.ID, c.binaryVersion, manifest.MinBinaryVersion))
		}
	}
	detail := strings.Join(failures, "; ")

	// spec 19 T4 doctor row: report upward (vs the latest KNOWN release, T3
	// cache-read only — a live fetch when the cache is empty is a deferred
	// follow-up, NOT this wave; see this phase's reported deviation) as an
	// ADVISORY appended to the detail string. This never flips the floor
	// comparison above: `ok` is untouched here, so the check still PASSES on
	// the advisory alone (only a floor violation FAILs it).
	if cp, err := c.cachePath(); err == nil {
		if latest, _ := release.ReadLatest(cp, time.Now(), cache.DefaultUpdateCheckTTL); latest != "" {
			if older, err := version.OlderThan(c.binaryVersion, latest); err == nil && older {
				detail += fmt.Sprintf(" · update available: v%s -> v%s — run a2a update", c.binaryVersion, latest)
			}
		}
	}

	return ok, detail
}

// doctorCheckCIPresence is a lightweight existence check (spec 09 T1: "not
// the full §9.3 host-drift diff"): does the space's default-branch mirror
// carry `.github/workflows/a2a-validate.yml`.
//
// Deviation (see this phase's report): the spec also asks this check to
// confirm "a required check named a2a-validate" is CONFIGURED on the host
// (GitHub branch-protection settings) — internal/host's Host interface
// (PushBranch/OpenPR/CheckStatus/ReviewStatus/FindPRByHeadBranch) exposes no
// primitive to read a repo's branch-protection/required-status-check
// configuration; CheckStatus/ReviewStatus are scoped to one PR, not the
// repo's protection settings. This check therefore covers the workflow-FILE
// half only; the required-check-config half needs a new Host primitive that
// is out of this phase's footprint (arguably `--space`'s own host-drift
// diff territory, itself v2/deferred per D-030).
func (c *DoctorCommand) doctorCheckCIPresence(cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	ok := true
	var failures []string
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		if _, err := c.readFile(filepath.Join(dir, ".github", "workflows", "a2a-validate.yml")); err != nil {
			ok = false
			failures = append(failures, fmt.Sprintf("%s: missing .github/workflows/a2a-validate.yml: %v", ref.ID, err))
		}
	}
	return ok, strings.Join(failures, "; ")
}

// doctorSpaceScaffoldingReader is the consumer-side capability (rails:
// consumer-side interfaces) doctorCheckScaffoldingCurrent's "who can fix it"
// note needs — host.GitHubHost.RepoPermissions satisfies it structurally.
// Declared here for the same reason doctorRepoSettingsReader is declared
// next to doctorCheckAutoMerge: a second consumer promotes it to host.go,
// not before.
type doctorSpaceScaffoldingReader interface {
	RepoPermissions(ctx context.Context, req host.RepoSettingsRequest) (host.RepoPermissions, error)
}

// doctorCheckScaffoldingCurrent is the tenth doctor row, closing the
// asymmetry `doctorCheckCIPresence` and `doctorCheckVersions` leave open:
// they check that a workflow FILE exists and that THIS BINARY is not too
// old for the space, but nothing checked the reverse — whether the SPACE's
// own scaffolding has fallen behind what this binary's embedded template
// would write. The live getvisa space proved the cost is real: its reusable
// -workflow caller sat pinned three releases behind its required check,
// silently validating with an old binary, and doctor was green throughout.
//
// It computes the SAME drift `a2a space update --dry-run` would show —
// spaceComputeUpdatePlanFor (cmd_space.go), the one function both this
// check and `space update`'s own spaceComputeUpdatePlan call, so this row
// can never disagree with what a real update run would do (mirrors
// space.IsInfrastructurePath's own single-owner precedent, spec 35 §9).
//
// Never a FAIL. A space that is behind still writes fine — the cost is a
// weaker CI gate, not a stoppage — and a red doctor on a working setup is
// the exact disease this repo's advisory-on-PASS precedent
// (doctorWorkflowScopeNote, doctorCheckAutoMerge's "unverified" outcome)
// exists to avoid. Three PASS shapes:
//   - in sync -> PASS, no note.
//   - behind -> PASS, naming the drift AND who can act on it, resolved by
//     what the credential can actually DO (doctorScaffoldingCanFix), never
//     from a config field: a client-space participant must not be told to
//     run a command the host will refuse, and a space admin must not be
//     nagged with a raw error.
//   - undecidable (no embedded template wired, an unparseable binary
//     version, or the plan itself could not be computed — no mirror, an
//     unreadable manifest) -> PASS, saying so plainly. The reason is a
//     short class, never the raw underlying error: a plan error can embed
//     an absolute mirror path, which is the same output-stability failure
//     mode the auto-merge row's own doc warns against for a raw API body.
func (c *DoctorCommand) doctorCheckScaffoldingCurrent(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	if c.TemplateFiles == nil {
		return true, " · scaffolding drift unverified: this build wires no embedded space template"
	}
	// The RUNNING BINARY's own clean version, exactly what spaceComputeUpdatePlan
	// would be given by a real `space update` run right now — never the
	// template's own baked-in version (spaceUpdateFloor's own reasoning: the
	// template is the floor SSOT, but the caller-ref substitution is pinned
	// to the binary that would actually perform the write). A dev build
	// ("dev", no ldflags) cannot be cleaned — that space's drift is reported
	// as undecidable rather than compared against a nonsense "@vdev" pin.
	cleanVersion, versionErr := spaceCleanVersion(c.binaryVersion)

	var notes []string
	for _, ref := range cfg.Spaces {
		if versionErr != nil {
			notes = append(notes, fmt.Sprintf("%s: scaffolding drift could not be checked (binary version %q is not a release version)", ref.ID, c.binaryVersion))
			continue
		}
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		plan, err := spaceComputeUpdatePlanFor(c.TemplateFiles, dir, ref.ID, cleanVersion, c.readFile)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: scaffolding drift could not be checked (mirror unreadable or manifest malformed)", ref.ID))
			continue
		}
		if len(plan.writes) == 0 {
			// In sync — spaceComputeUpdatePlanFor's own drift-only entries
			// (a customized seeded file, a floor pinned above the template's)
			// are intentional divergence, not "behind": `space update` would
			// write nothing here either.
			continue
		}

		var whoNote string
		switch canFix, known := c.doctorScaffoldingCanFix(ctx, ref, machine); {
		case !known:
			whoNote = "who can fix it could not be determined (repository permissions unreadable)"
		case canFix:
			whoNote = "run `a2a space update`"
		default:
			whoNote = "ask the space admin to run `a2a space update`"
		}
		notes = append(notes, fmt.Sprintf("%s: scaffolding is behind the current template (%s) — %s",
			ref.ID, strings.Join(plan.summary, "; "), whoNote))
	}
	if len(notes) == 0 {
		return true, ""
	}
	return true, " · " + strings.Join(notes, "; ")
}

// doctorScaffoldingCanFix reports whether the credential resolved for ref
// can push (or admin) the space's repository — the ONLY input
// doctorCheckScaffoldingCurrent's "who can fix it" note is allowed to use
// (this phase's brief §3). known=false means the question could not be
// answered (no repo-settings reader wired, an unparseable repo URL, no
// resolvable credential, or the read itself failed) — the caller must
// render the neutral form, never guess.
func (c *DoctorCommand) doctorScaffoldingCanFix(ctx context.Context, ref space.Ref, machine space.MachineConfig) (canFix, known bool) {
	reader, isReader := c.h.(doctorSpaceScaffoldingReader)
	if !isReader {
		return false, false
	}
	owner, name, err := doctorRepoOwnerName(ref.RepoURL)
	if err != nil {
		return false, false
	}
	var parsedRef space.CredentialReference
	if raw, present := machine.Credentials[ref.ID]; present {
		if parsed, perr := space.ParseCredentialReference(raw); perr == nil {
			parsedRef = parsed
		}
	}
	cred, err := c.resolveCredential(ctx, space.CredentialEnvVar(ref.ID), parsedRef)
	if err != nil {
		return false, false
	}
	perm, err := reader.RepoPermissions(ctx, host.RepoSettingsRequest{
		Repo:       host.Repo{Owner: owner, Name: name},
		Credential: cred,
	})
	if err != nil {
		return false, false
	}
	return perm.Push || perm.Admin, true
}

// doctorRepoSettingsReader is the consumer-side capability this check
// needs (rails: consumer-side interfaces) — host.GitHubHost.AutoMergeAllowed
// satisfies it structurally. It is declared HERE rather than next to
// host.Forker/host.AutoMerger in internal/host/host.go because that file is
// outside this phase's footprint (see this phase's reported deviation): if a
// second consumer ever needs this same read, promoting it to host.go is the
// natural next step.
type doctorRepoSettingsReader interface {
	AutoMergeAllowed(ctx context.Context, req host.RepoSettingsRequest) (bool, error)
}

// doctorCheckAutoMerge is WAVE M2's ninth basic-doctor row (spec 45 §T1,
// AC-1050.5): GitHub's `allow_auto_merge` repo setting is OFF by default on a
// freshly created repository — exactly what `a2a space init` produces — and
// neither `a2a doctor` nor `space init`'s own residual-steps output named it
// before this row. Left off, the funnel opens a PR and arms auto-merge on
// every `a2a submit`, and nothing ever merges it: the publisher gets a
// stderr warning (host.PRInfo.AutoMergeNote) and the consumer sees nothing
// at all. This already happened on the live getvisa space (this phase's
// evidence audit).
//
// Three outcomes, not two, and the third is NOT a failure (lead correction,
// 2026-07-25 — the wave shipped it as one):
//   - auto-merge ON  -> PASS.
//   - auto-merge OFF -> FAIL, naming the setting, how to turn it on, and why
//     it matters. This is the one genuinely broken state.
//   - the READ itself failed (no credential, no permission to read repo
//     metadata, no network, an unwired host) -> **PASS with an advisory note**,
//     never a silent PASS and never a FAIL.
//
// Why the third is advisory: a red gate that fires on something that is not
// broken teaches people to stop reading the gate — the disease P40 exists to
// treat. This repo already settled the same question the same way once, for
// `doctorWorkflowScopeNote`: "an advisory that fires on the most narrowly
// scoped credentials would train people to ignore it". A fine-grained token
// without `Repository metadata: read` cannot answer this question, and that is
// a legitimate, common, working setup — it must not make `a2a doctor` exit 1.
// The note is what keeps it from being a false PASS.
//
// The note text is deliberately TERSE and deterministic — it names the class
// and the fix, and does NOT interpolate the raw API error. A doctor line that
// embeds a multi-line 401 body is unstable output (it broke the e2e doctor
// script) and, worse, buries the actionable sentence.
func (c *DoctorCommand) doctorCheckAutoMerge(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	// unverifiable collects the spaces this check could not answer for; broken
	// collects the ones it answered NO for. They are kept apart because only
	// the second is a failure — see this function's doc comment.
	var unverifiable, broken []string

	reader, isReader := c.h.(doctorRepoSettingsReader)
	if !isReader {
		return true, " · auto-merge unverified: this build wires no GitHub repo-settings reader"
	}

	for _, ref := range cfg.Spaces {
		owner, name, err := doctorRepoOwnerName(ref.RepoURL)
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}

		var parsedRef space.CredentialReference
		if raw, present := machine.Credentials[ref.ID]; present {
			if parsed, perr := space.ParseCredentialReference(raw); perr == nil {
				parsedRef = parsed
			}
		}
		cred, err := c.resolveCredential(ctx, space.CredentialEnvVar(ref.ID), parsedRef)
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}

		allowed, err := reader.AutoMergeAllowed(ctx, host.RepoSettingsRequest{
			Repo:       host.Repo{Owner: owner, Name: name},
			Credential: cred,
		})
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}
		if !allowed {
			broken = append(broken, fmt.Sprintf(
				"%s: auto-merge is disabled on this repository — enable Settings -> General -> \"Allow auto-merge\"; "+
					"a2a submit opens a PR and arms auto-merge, so with this off every write stalls behind a PR nothing will merge",
				ref.ID))
		}
	}

	if len(broken) > 0 {
		// A space that is genuinely misconfigured is the failure, even if a
		// sibling space could not be read — the actionable half wins.
		return false, strings.Join(broken, "; ")
	}
	if len(unverifiable) > 0 {
		return true, fmt.Sprintf(" · auto-merge unverified for %s: the credential cannot read this repo's settings "+
			"(a fine-grained token needs \"Repository metadata: read\")", strings.Join(unverifiable, ", "))
	}
	return true, ""
}

// doctorCodeownersReader is the consumer-side capability the CODEOWNERS row
// needs — host.GitHubHost.CodeownersErrors satisfies it structurally. Declared
// here for the same reason doctorRepoSettingsReader is: a second consumer
// promotes it to host.go, not before.
type doctorCodeownersReader interface {
	CodeownersErrors(ctx context.Context, req host.RepoSettingsRequest) ([]host.CodeownersError, error)
}

// doctorCheckCodeownersResolvable turns a hand-verification into a gate.
//
// An unknown CODEOWNERS owner is IGNORED by GitHub, never rejected. So a file
// naming a team nobody created looks like it gates `/space.yaml` and gates
// nothing — and code-owner review is the ENTIRE mechanism behind the G4 safety
// argument (BRANCH-PROTECTION.md), which makes an inert CODEOWNERS an ungated
// trust root with nothing at merge time to say so.
//
// This shipped twice as a documentation problem before becoming a check. First
// the template's placeholder was `@REPLACE_WITH_ORG/space-admins` with an
// instruction to replace the org — so the natural edit kept a team name nobody
// creates. Then it became `@REPLACE_WITH_ORG/REPLACE_WITH_TEAM_OR_LOGIN` with
// an instruction to replace BOTH halves — and replacing both halves literally
// produces `@your-org/your-login`, which is still a team reference, still to a
// team that does not exist. Two rounds of clearer prose, the same inert file.
//
// The template also told the operator that GitHub's CODEOWNERS view was "the
// only feedback you will get". That was wrong in a useful direction: GitHub
// answers the same question through an API, with line numbers — verified on a
// real repo 2026-07-26, where both shapes came back as "Unknown owner". Once
// a thing is machine-readable, asking a human to check it by eye is a choice,
// and the wrong one.
//
// Three outcomes, matching doctorCheckAutoMerge's shape exactly rather than
// inventing a second reporting convention:
//
//   - every owner resolves -> PASS.
//   - GitHub reports errors -> FAIL, quoting its own line and suggestion.
//   - the READ failed (no credential, no permission, no network, an unwired
//     host) -> PASS with an advisory. A red gate that fires on something that
//     is not broken teaches people to stop reading the gate.
func (c *DoctorCommand) doctorCheckCodeownersResolvable(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	reader, isReader := c.h.(doctorCodeownersReader)
	if !isReader {
		return true, " · CODEOWNERS unverified: this build wires no GitHub CODEOWNERS reader"
	}

	var unverifiable, broken []string
	for _, ref := range cfg.Spaces {
		owner, name, err := doctorRepoOwnerName(ref.RepoURL)
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}

		var parsedRef space.CredentialReference
		if raw, present := machine.Credentials[ref.ID]; present {
			if parsed, perr := space.ParseCredentialReference(raw); perr == nil {
				parsedRef = parsed
			}
		}
		cred, err := c.resolveCredential(ctx, space.CredentialEnvVar(ref.ID), parsedRef)
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}

		errs, err := reader.CodeownersErrors(ctx, host.RepoSettingsRequest{
			Repo:       host.Repo{Owner: owner, Name: name},
			Credential: cred,
		})
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}
		if len(errs) == 0 {
			continue
		}
		// One finding per SPACE, listing its lines — not one per line. Driven
		// against a real space, three bad lines produced three copies of the
		// same paragraph of explanation, which buries the part that differs
		// (the line numbers) under the part that does not.
		//
		// GitHub's own suggestion is quoted rather than paraphrased: it names
		// all three conditions an owner must satisfy (exists, is publicly
		// visible, has write access), and a paraphrase would drop whichever one
		// the reader's case actually is.
		lines := make([]string, 0, len(errs))
		for _, e := range errs {
			lines = append(lines, fmt.Sprintf("line %d (%s): %s", e.Line, e.Kind, e.Suggestion))
		}
		broken = append(broken, fmt.Sprintf("%s: %s — GitHub IGNORES an owner it cannot resolve, "+
			"so this file gates nothing it appears to gate, and code-owner review is the only thing "+
			"standing behind space.yaml. Individual logins avoid all three conditions above",
			ref.ID, strings.Join(lines, "; ")))
	}

	if len(broken) > 0 {
		return false, strings.Join(broken, "; ")
	}
	if len(unverifiable) > 0 {
		return true, fmt.Sprintf(" · CODEOWNERS unverified for %s: the credential cannot read this repo's "+
			"CODEOWNERS errors (a fine-grained token needs \"Repository metadata: read\")",
			strings.Join(unverifiable, ", "))
	}
	return true, ""
}

// doctorRepoOwnerName extracts owner/name from a GitHub remote URL
// (https://github.com/<owner>/<name>[.git] or git@github.com:<owner>/<name>).
//
// This duplicates cmd/a2a/wire.go's parseGitHubRepo byte for byte (see this
// phase's reported deviation): wire.go lives in package main and is
// lead-reserved, so it cannot be imported from internal/cli. Deliberately
// NOT guarded on a "looks like github.com" prefix — it takes the last two
// path segments regardless of host, so a local-path fixture origin (the
// fakegithub-backed tests, and any future non-GitHub host) still parses an
// owner/name pair rather than being silently skipped.
func doctorRepoOwnerName(url string) (owner, name string, err error) {
	s := strings.TrimSuffix(url, ".git")
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "git@github.com:")
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
		return "", "", fmt.Errorf("cannot parse owner/name from repo URL %q", url)
	}
	return parts[len(parts)-2], parts[len(parts)-1], nil
}

// doctorCheckStatuslineWiring is a presence check only (spec 09 T1: "not
// statusline's own rendering logic"). The constructor DI list for basic
// doctor (this phase's binding Placement decision) names no statusline
// dependency at all, and no `a2a statusline` command exists in this
// package's footprint yet (P7, a different wave) — so this check reads
// §7.5's own fallback-refresh mechanism instead: when no hub is configured,
// a stale statusline cache refresh falls back to `git fetch`. This check
// therefore verifies that fallback's prerequisite, the `git` binary, is on
// PATH.
//
// Deviation (flagged prominently in this phase's report, not buried): this
// is this phase's weakest-founded interpretation of the five OP-218 checks
// — it is an assumption to reconcile with the lead/P7, not a verified
// requirement.
func (c *DoctorCommand) doctorCheckStatuslineWiring() (bool, string) {
	if err := c.lookupGit(); err != nil {
		return false, fmt.Sprintf("git-fallback statusline refresh unavailable: %v", err)
	}
	return true, ""
}

// doctorCheckSkillDiscoverable is P32's AC-918.2 check: an installed skill
// that no agent surface can see is a correct file in a place nothing looks
// (spec 32 §1). Never a FAIL — a consumer who has not installed the skill,
// or has installed but not yet linked it, is not this check's concern (it
// only reports what it finds, an advisory on PASS, matching
// doctorCheckVersions's own advisory-on-PASS convention).
func (c *DoctorCommand) doctorCheckSkillDiscoverable() (bool, string) {
	if _, err := os.Stat(filepath.Join(c.projectRoot, skillDefaultDir, "SKILL.md")); err != nil {
		return true, " · no a2ahub skill installed"
	}

	detected := surface.Detect(c.projectRoot)
	linked := 0
	for _, s := range detected {
		if _, err := os.Lstat(filepath.Join(c.projectRoot, s.SkillsHome, "a2ahub")); err == nil {
			linked++
		}
	}
	// THREE states, not two. This used to collapse the last two, and the
	// collapsed message named a remedy that cannot work: it said to run
	// `a2a skill link`, which in a project with no agent surface answers
	// "no known agent surface detected (.claude/ or .codex/) — nothing to
	// link" and leaves the advisory repeating verbatim on the next doctor.
	//
	// Found on 2026-07-26 by following the advice and watching nothing change.
	// Loud, specific and unactionable is the same family as the validation gate
	// that named the wrong author: it sends the reader somewhere that cannot
	// help, and the reader's next move is to distrust the check.
	if len(detected) == 0 {
		return true, " · skill installed; this project shows no agent surface (.claude/ or .codex/), " +
			"so there is nothing to link — expected for a project no agent drives"
	}
	if linked == 0 {
		ids := make([]string, 0, len(detected))
		for _, s := range detected {
			ids = append(ids, s.ID)
		}
		return true, fmt.Sprintf(" · ADVISORY: skill installed but not linked from this project's "+
			"agent surface(s) (%s) — run 'a2a skill link'", strings.Join(ids, ", "))
	}
	return true, fmt.Sprintf(" · skill installed and linked (%d surface(s))", linked)
}

// doctorSkillManualVersionPattern parses the version stamp skillProvenance
// (cmd_skill.go) writes into PROVENANCE.md's prose: "... (a2a <version>).".
// A tolerant match — this check never fails doctor on an unrecognized
// provenance shape (a hand-edited or foreign-format file), it just reports
// "version unknown".
var doctorSkillManualVersionPattern = regexp.MustCompile(`a2a ([0-9][^)]*)\)`)

// doctorCheckSkillManualCurrent is P31 wave 5's out-of-band-update catch: an
// `a2a update` that swapped the binary but was interrupted before (or never
// reached, e.g. a manually-copied binary replacing the installed one)
// refreshing the installed skill leaves the manual stamped to an OLDER
// version than the binary now running. Never a hard FAIL — a stale manual is
// a nudge (advisory on PASS), matching doctorCheckSkillDiscoverable's own
// advisory-on-PASS convention; an absent install is simply not this check's
// concern (nothing to compare).
func (c *DoctorCommand) doctorCheckSkillManualCurrent() (bool, string) {
	data, err := os.ReadFile(filepath.Join(c.projectRoot, skillDefaultDir, skillProvenanceFile))
	if err != nil {
		return true, " · no skill installed"
	}

	match := doctorSkillManualVersionPattern.FindStringSubmatch(string(data))
	if match == nil {
		return true, " · skill installed (version unknown)"
	}
	manualVersion := match[1]

	older, err := version.OlderThan(manualVersion, c.binaryVersion)
	if err != nil {
		return true, " · skill installed (version unknown)"
	}
	if older {
		return true, fmt.Sprintf(
			" · skill manual is v%s, binary is v%s — run 'a2a skill install'",
			manualVersion, c.binaryVersion)
	}
	return true, fmt.Sprintf(" · skill manual current (v%s)", manualVersion)
}

// doctorVersionOlder reports whether binaryVersion is strictly older than
// minVersion (dotted major.minor.patch, per schemas/manifest/v1/
// space.schema.json's min_binary_version pattern). A thin wrapper over the
// SSOT comparator internal/version.OlderThan (spec 19 §7 anti-dup — the same
// pattern internal/space.versionOlderThan already uses): it remaps the
// leaf's own sentinel back to this file's errDoctorInvalidVersion so
// existing callers/tests keep observing that error.
func doctorVersionOlder(binaryVersion, minVersion string) (bool, error) {
	older, err := version.OlderThan(binaryVersion, minVersion)
	if err != nil {
		return false, errDoctorInvalidVersion
	}
	return older, nil
}

// errDoctorInvalidVersion is returned by doctorVersionOlder for an
// unparseable version string.
var errDoctorInvalidVersion = errors.New("doctor: invalid version string")

var _ Command = (*DoctorCommand)(nil)
