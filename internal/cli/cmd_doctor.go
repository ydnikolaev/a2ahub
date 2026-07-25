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

// DoctorCommand implements the basic (non-`--space`) `a2a doctor` verb: the
// five OP-218 checks — credentials, space access, versions, CI presence,
// statusline wiring — one line per check, exit 0 iff all pass. `--space`
// (the v2 admin host-drift diff, D-030) is rejected explicitly, never
// silently ignored.
type DoctorCommand struct {
	binaryVersion     string
	projectConfigPath string
	machineConfigPath string
	projectRoot       string
	h                 host.Host

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
	return "run local health checks over every connected space (credentials, mirror access, identity, versions, CI, auto-merge, statusline, skill) — see troubleshooting.md for what each FAIL means"
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
		{"auto-merge enabled", func() (bool, string) { return c.doctorCheckAutoMerge(ctx, cfg, machine) }},
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
	if linked == 0 {
		return true, " · ADVISORY: skill installed but no agent surface links it — run 'a2a skill link'"
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
