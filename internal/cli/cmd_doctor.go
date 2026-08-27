// OP-218 basic doctor (spec 09 T1). This file's only package-level symbols
// are DoctorCommand + NewDoctorCommand plus its own uniquely-named,
// file-private helpers (doctor* prefix) — no shared helper, no package var,
// per this phase's plan Placement decision (avoids collision with P6/P7/P8's
// parallel verb files in this same package).
//
// ONE deliberate exception, added by space-notify-2026-08 P6: the three
// notify-* checks below call notifyCheckRoutes/notifyCheckSecret/
// notifyCheckDelivery (cmd_notify_setup.go) rather than re-implementing
// them, because spec 06 T1 requires `notify verify` to report "the same
// facts doctor reports" — one function per fact, read by both callers,
// never two hand-synced copies.
package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/notification"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/surface"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
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
	skillDir          string

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

	// SkillFiles is the embedded skill/a2ahub/ tree (skill.Files, the same
	// embed `a2a skill install` and `a2a init` write from) — the authority
	// "skill manual current" walks against once a stamp names THIS binary's
	// own version (judge-the-thing-2026-08 P5). Exported and left NIL by
	// NewDoctorCommand, same DI convention as TemplateFiles: the lead wires
	// it post-construction in cmd/a2a (`cmd.SkillFiles = skill.Files`)
	// because internal/cli must not import the skill package directly. Left
	// unwired the row degrades to an advisory "this build wires no embedded
	// skill tree" rather than failing — a forgotten wire.go line must never
	// silently upgrade to a green PASS carrying the tree-verified note (§8
	// #10's own point).
	SkillFiles fs.FS

	// The following are real-implementation-backed seams (rails DI):
	// NewDoctorCommand defaults every one of them to the real internal/space
	// (and stdlib) operation; tests override individual fields to drive each
	// check to both pass and fail without a live git remote or real
	// credentials.
	loadProjectConfig func(path string) (space.ProjectConfig, error)
	loadMachineConfig func(path string) (space.MachineConfig, error)
	resolveMirror     func(projectRoot string, ref space.Ref, machine space.MachineConfig) string
	cloneOrFetch      func(ctx context.Context, dir, repoURL string, credential host.Credential) error
	resolveCredential func(ctx context.Context, explicitEnvVar string, ref space.CredentialReference) (host.Credential, error)
	readFile          func(path string) ([]byte, error)
	lookupGit         func() error
	// cachePath resolves the spec 19 T3 update-check cache location for the
	// "versions" check's advisory half (doctorCheckVersions): defaults to
	// release.CachePath; tests override it to point at a seeded temp cache.
	cachePath func() (string, error)

	// NotificationStatus is wired by cmd/a2a to the same controller as
	// `a2a notifications status`; doctor never grows a second platform probe.
	NotificationStatus func(context.Context, string) (notification.Status, error)

	// notifyAdapter/ghSecretList back the three P6 notify checks (spec 06
	// doctor table): notify-delivery reads workflow-run conclusions through
	// the SAME notifySetupAdapter `notify verify` uses (real-defaulted by
	// NewDoctorCommand); notify-secret shells to `gh secret list` — presence
	// only, never a value.
	notifyAdapter *notifySetupAdapter
	ghSecretList  notifyGHSecretListFunc

	// ParticipantAvatarStatus is wired by cmd/a2a from the avatar cache owner.
	// The CLI receives policy, not the cache path or on-disk format. The first
	// result says a validated local image exists; the second says foreground
	// sync supports this owner identifier at all.
	ParticipantAvatarStatus func(login string) (cached, supported bool)

	// ClassificationSummaryReader is P4's consumer-side seam for P1's cache
	// classification summary. Nil is an explicit UNVERIFIED diagnostic, never a
	// scan reimplementation or a false PASS.
	ClassificationSummaryReader DoctorClassificationSummaryReader
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
		// Memoised: several of doctor's checks (see credentialmemo.go's doc
		// comment) each resolve the same connected space's credential
		// independently inside their own loop, and a `cmd:` machine-config
		// reference resolves through a subprocess — without this wrapper
		// one `a2a doctor` run spawns that subprocess once per check per
		// space instead of once per space.
		resolveCredential: memoizeCredentialResolver(space.ResolveCredential),
		readFile:          os.ReadFile,
		lookupGit:         func() error { _, err := exec.LookPath("git"); return err },
		cachePath:         release.CachePath,
		skillDir:          skillDefaultDir,
		notifyAdapter:     newNotifySetupAdapter(http.DefaultClient, notifyGitHubAPIBase()),
		ghSecretList:      runGHSecretList,
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
	return "run local health checks over every connected space (credentials, mirror access, identity, participant avatars, versions, CI, space scaffolding, repository visibility, auto-merge, CODEOWNERS, notifications, statusline, skill) — see troubleshooting.md for what each FAIL means"
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
	if c.skillDir, err = space.NormalizeSkillDir(cfg.SkillDir); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "doctor: invalid skill_dir: %v\n", err)
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
		{"participant avatars", func() (bool, string) { return c.doctorCheckParticipantAvatars(cfg, machine) }},
		{"versions", func() (bool, string) { return c.doctorCheckVersions(cfg, machine) }},
		{"CI presence", func() (bool, string) { return c.doctorCheckCIPresence(cfg, machine) }},
		{"space scaffolding current", func() (bool, string) { return c.doctorCheckScaffoldingCurrent(ctx, cfg, machine) }},
		{"auto-merge enabled", func() (bool, string) { return c.doctorCheckAutoMerge(ctx, cfg, machine) }},
		{"stuck green PRs", func() (bool, string) { return c.doctorCheckStuckGreenPRs(ctx, cfg, machine) }},
		{"default branch healthy", func() (bool, string) { return c.doctorCheckDefaultBranchHealthy(ctx, cfg, machine) }},
		{"codeowners resolvable", func() (bool, string) { return c.doctorCheckCodeownersResolvable(ctx, cfg, machine) }},
		{"threads intact", func() (bool, string) { return c.doctorCheckThreadsIntact(cfg, machine) }},
		{"skipped mirror files", func() (bool, string) { return c.doctorCheckSkippedFiles(ctx, cfg, machine) }},
		{"notification components", func() (bool, string) { return c.doctorCheckNotificationComponents(ctx) }},
		{"statusline wiring", func() (bool, string) { return c.doctorCheckStatuslineWiring() }},
		{"skill discoverable", func() (bool, string) { return c.doctorCheckSkillDiscoverable() }},
		{"skill manual current", func() (bool, string) { return c.doctorCheckSkillManualCurrent() }},
		{"automation coverage", func() (bool, string) { return c.doctorCheckAutomationCoverage() }},
		{"notify-routes", func() (bool, string) { return c.doctorCheckNotifyRoutes(cfg, machine) }},
		{"notify-secret", func() (bool, string) { return c.doctorCheckNotifySecret(ctx, cfg, machine) }},
		{"notify-delivery", func() (bool, string) { return c.doctorCheckNotifyDelivery(ctx, cfg, machine) }},
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
	for _, row := range c.doctorVisibilityRows(ctx, cfg, machine) {
		// P4 §5.2: WARN and UNVERIFIED are advisory-on-success. They are
		// intentionally rendered as their own statuses and never change allOK.
		_, _ = fmt.Fprintf(stdio.Stdout, "repository visibility [%s]: %s: %s\n", row.SpaceID, row.Status, row.Detail)
	}
	for _, row := range c.doctorUnadoptedConsumptionRows(cfg, machine) {
		// Spec 06 T1: WARN, never FAIL — "consuming without adopting is a
		// real risk and not an error; the operator may have reasons." Same
		// advisory-on-success rule as the visibility rows just above: this
		// loop never touches allOK.
		_, _ = fmt.Fprintf(stdio.Stdout, "consumed contract [%s]: %s: %s\n", row.SpaceID, row.Status, row.Detail)
	}

	if !allOK {
		return 1
	}
	return 0
}

// doctorCheckParticipantAvatars is deliberately local-only and advisory.
// Foreground `a2a sync` owns network refresh and cache mutation; doctor only
// names active owners whose validated local image is absent. The dashboard
// therefore remains fully useful with monograms while an agent gets one
// concrete repair command and the human never has to manage cache files.
func (c *DoctorCommand) doctorCheckParticipantAvatars(cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	if c.ParticipantAvatarStatus == nil {
		return true, " · local participant avatar probe not wired"
	}
	missing := make(map[string][]string)
	unsupported := make(map[string]map[string]string)
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		raw, err := c.readFile(filepath.Join(dir, "space.yaml"))
		if err != nil {
			continue // space access and versions own unreadable-mirror failures
		}
		manifest, err := space.ParseManifest(raw)
		if err != nil {
			continue // versions owns malformed-manifest failures
		}
		owners := make(map[string]string)
		for _, participant := range manifest.Participants {
			if participant.Status != "active" {
				continue
			}
			for _, rawOwner := range participant.Owners {
				owner := strings.TrimSpace(rawOwner)
				if owner == "" {
					continue
				}
				key := strings.ToLower(owner)
				cached, supported := c.ParticipantAvatarStatus(owner)
				if cached {
					continue
				}
				if !supported {
					if unsupported[ref.ID] == nil {
						unsupported[ref.ID] = make(map[string]string)
					}
					if _, exists := unsupported[ref.ID][key]; !exists {
						unsupported[ref.ID][key] = owner
					}
					continue
				}
				if _, exists := owners[key]; !exists {
					owners[key] = owner
				}
			}
		}
		if len(owners) == 0 {
			continue
		}
		keys := make([]string, 0, len(owners))
		for key := range owners {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			missing[ref.ID] = append(missing[ref.ID], owners[key])
		}
	}
	if len(missing) == 0 && len(unsupported) == 0 {
		return true, ""
	}
	formatOwners := func(bySpace map[string][]string) string {
		spaceIDs := make([]string, 0, len(bySpace))
		for spaceID := range bySpace {
			spaceIDs = append(spaceIDs, spaceID)
		}
		sort.Strings(spaceIDs)
		parts := make([]string, 0, len(spaceIDs))
		for _, spaceID := range spaceIDs {
			owners := bySpace[spaceID]
			sort.Slice(owners, func(i, j int) bool { return strings.ToLower(owners[i]) < strings.ToLower(owners[j]) })
			parts = append(parts, fmt.Sprintf("%s: %s", spaceID, strings.Join(owners, ", ")))
		}
		return strings.Join(parts, "; ")
	}
	formatUnsupported := func(bySpace map[string]map[string]string) string {
		flat := make(map[string][]string, len(bySpace))
		for spaceID, owners := range bySpace {
			for _, owner := range owners {
				flat[spaceID] = append(flat[spaceID], owner)
			}
		}
		return formatOwners(flat)
	}
	var notes []string
	if len(missing) > 0 {
		notes = append(notes, "local participant avatars are missing ("+formatOwners(missing)+") — run `a2a sync`")
	}
	if len(unsupported) > 0 {
		notes = append(notes, "owner identifiers cannot be fetched from GitHub ("+formatUnsupported(unsupported)+") — correct them in space.yaml")
	}
	return true, " · " + strings.Join(notes, "; ") + "; initials remain available meanwhile"
}

func (c *DoctorCommand) doctorCheckNotificationComponents(ctx context.Context) (bool, string) {
	if c.NotificationStatus == nil {
		return true, " · notification probe not wired"
	}
	status, err := c.NotificationStatus(ctx, c.projectRoot)
	if err != nil {
		return false, err.Error()
	}
	if status.Project == nil || len(status.Project.Channels) == 0 {
		return true, " · notifications not enabled for this project"
	}
	healthByChannel := make(map[notification.Channel][]notification.ComponentHealth)
	for _, component := range status.Components {
		healthByChannel[component.Channel] = append(healthByChannel[component.Channel], component)
	}
	var failures []string
	for _, channel := range status.Project.Channels {
		components := healthByChannel[channel]
		healthy := false
		var detail string
		for _, component := range components {
			platformHealthy := component.Detail == "" && component.Installed && component.Handshake == "ok"
			if channel == notification.ChannelMacOS {
				platformHealthy = platformHealthy &&
					component.Permission == "authorized" && component.LoginItem == "enabled"
			}
			if component.Enabled != nil && !*component.Enabled {
				platformHealthy = false
			}
			if platformHealthy {
				healthy = true
				break
			}
			detail = fmt.Sprintf(
				"installed=%t handshake=%s permission=%s login_item=%s profile=%s detail=%s",
				component.Installed, component.Handshake, component.Permission,
				component.LoginItem, component.Profile, component.Detail,
			)
		}
		if !healthy {
			if detail == "" {
				detail = "no component health record"
			}
			failures = append(failures, fmt.Sprintf("%s: %s", channel, detail))
		}
	}
	if len(failures) > 0 {
		return false, strings.Join(failures, "; ")
	}
	return true, ""
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
		credential := mirrorCredential(ctx, c.resolveCredential, ref.ID, machine)
		err := c.cloneOrFetch(ctx, dir, ref.RepoURL, credential)
		if err == nil {
			continue
		}
		ok = false
		failure := fmt.Sprintf("%s: %v", ref.ID, err)
		if hint := c.doctorPrivateRepoHint(ctx, ref, credential, err); hint != "" {
			failure += " — " + hint
		}
		failures = append(failures, failure)
	}
	return ok, strings.Join(failures, "; ")
}

// gitRepositoryNotFound reports whether a failed git fetch/clone carries
// GitHub's not-found answer.
//
// It matches the SERVER's line ("remote: Repository not found.") rather than
// git's own "fatal: repository '<url>' not found", because git's is one of its
// translated die() strings and would stop matching under a non-C locale, while
// the remote: line is relayed verbatim from GitHub in English.
//
// The reason this string is worth naming at all: GitHub answers "does not
// exist" and "exists, and you may not know that" identically and on purpose,
// so the message alone can never tell an operator which one happened. That is
// exactly the question doctor is in a position to answer, because it can ask
// the API with a credential the git call did not have.
func gitRepositoryNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "repository not found")
}

// doctorPrivateRepoHint turns GitHub's ambiguous 404 into the sentence the
// operator can act on, or returns "" when it has nothing certain to add.
//
// This exists because of a real half-hour of confusion: after a space was made
// private, `a2a doctor` printed `space access: FAIL: ... Repository not found`
// and, four rows later, `repository visibility [getvisa]: PASS`. Both were
// accurate. The visibility row reads the API, which is authenticated; the
// access row runs git, which was not. Doctor already knew the repository
// existed and was private, and still relayed a 404 that reads as "your URL is
// wrong". Every check it needs is already wired here, so the only thing
// missing was asking.
//
// It reports only what it verified. A repository the API confirms is public
// gets no hint at all: the 404 then means something else (a typo'd URL, a
// deleted repo) and inventing a privacy explanation would send the operator
// down the wrong path.
func (c *DoctorCommand) doctorPrivateRepoHint(
	ctx context.Context,
	ref space.Ref,
	credential host.Credential,
	err error,
) string {
	if !gitRepositoryNotFound(err) {
		return ""
	}
	if credential.Token == "" {
		// Nothing was authenticated — neither the fetch that just failed nor
		// the API probe that would classify it. That absence IS the finding:
		// an unauthenticated fetch of a private repository produces precisely
		// this error, so name the possibility and the fix without asserting it.
		return fmt.Sprintf("no credential resolved for %s, so the mirror fetch ran unauthenticated; "+
			"GitHub answers an unauthenticated fetch of a PRIVATE repository with exactly this 404 — "+
			"if the space is private, set %s, or add `%s: cmd:gh auth token` under `credentials:` in %s "+
			"(reuses your existing `gh` login — nothing new to mint)",
			ref.ID, space.CredentialEnvVar(ref.ID), ref.ID, c.machineConfigPath)
	}
	reader, canRead := c.h.(host.RepoVisibilityReader)
	if !canRead {
		return ""
	}
	owner, name, parseErr := doctorRepoOwnerName(ref.RepoURL)
	if parseErr != nil {
		return ""
	}
	visibility, visibilityErr := reader.RepoVisibility(ctx, host.RepoSettingsRequest{
		Repo: host.Repo{Owner: owner, Name: name}, Credential: credential,
	})
	if visibilityErr != nil {
		return ""
	}
	switch visibility {
	case host.RepositoryVisibilityPrivate, host.RepositoryVisibilityInternal:
		return fmt.Sprintf("the repository EXISTS and is %s (the API says so, with this same credential) — "+
			"git could not read it, so the credential this space resolves lacks read access to it; "+
			"check that the token is authorized for the owning org and has repository read scope",
			visibility)
	default:
		// Public and reachable by the API, yet git got a 404: the repository
		// is not the problem, so stay silent rather than mis-explain the URL.
		return ""
	}
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
	if c.binaryVersion == "dev" {
		return true, " · unreleased local build (dev); released-version floor comparison skipped"
	}
	ok := true
	var failures []string
	mirrorDirs := map[string]string{}
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		mirrorDirs[ref.ID] = dir
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
	latest := ""
	if cp, err := c.cachePath(); err == nil {
		latest, _ = release.ReadLatest(cp, time.Now(), cache.DefaultUpdateCheckTTL)
	}
	if latest == "" {
		// THE UNMEASURED CASE, and it is the one that hurts. When no check has
		// ever run, EVERY surface is silent — this row, the advisory on every
		// verb, the MCP note. Silence then reads as "you are current", which is
		// a claim nobody made. It is named here because `doctor` is what an
		// agent runs when told to check its setup.
		detail += " · no release check has ever run on this machine, so nothing here can say whether a newer a2a exists — run a2a update --check"
	} else if older, err := version.OlderThan(c.binaryVersion, latest); err == nil && older {
		detail += fmt.Sprintf(" · update available: v%s -> v%s — run a2a update", c.binaryVersion, latest)
	}

	// THE SECOND AXIS, which no check has ever reported. A space pins an a2ahub
	// template tag in its committed workflows, and that pin moves only when
	// somebody runs `a2a space update`. A space can satisfy its own write floor
	// while pinning a template several releases old — precisely how axon's floor
	// sat at 0.23.0 while 0.24.0 shipped, letting an agent write from a stale
	// binary legitimately and invisibly.
	//
	// Advisory, never a failure: whether to update the space is the space
	// owner's decision, and refusing a writer for the owner's call punishes the
	// wrong party.
	if latest != "" {
		for _, ref := range cfg.Spaces {
			pin, _ := space.WorkflowVersion(mirrorDirs[ref.ID])
			if pin == "" || pin == "mixed" {
				continue
			}
			if older, err := version.OlderThan(pin, latest); err == nil && older {
				detail += fmt.Sprintf(" · %s pins the a2ahub template at v%s while v%s is published — run a2a space update", ref.ID, pin, latest)
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

// doctorCheckNotifyRoutes is P6's first notify check (spec 06 doctor
// table): every route in each connected space's mirrored space.yaml
// validates and names a known participant. A space with no routes at all
// is silently green — adopting notifications costs nothing (space-
// template's own a2a-notify.yml header states the identical philosophy).
func (c *DoctorCommand) doctorCheckNotifyRoutes(cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	ok := true
	var failures []string
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		raw, err := c.readFile(filepath.Join(dir, "space.yaml"))
		if err != nil {
			continue // no mirror yet: doctorCheckSpaceAccess already reports this
		}
		manifest, err := space.ParseManifest(raw)
		if err != nil {
			ok = false
			failures = append(failures, fmt.Sprintf("%s: cannot parse space.yaml: %v", ref.ID, err))
			continue
		}
		if routesOK, detail := notifyCheckRoutes(manifest); !routesOK {
			ok = false
			failures = append(failures, ref.ID+": "+detail)
		}
	}
	return ok, strings.Join(failures, "; ")
}

// doctorCheckNotifySecret is P6's second notify check: every secret a
// route names exists on the space repo — presence via `gh secret list`,
// NEVER the value.
func (c *DoctorCommand) doctorCheckNotifySecret(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 || c.ghSecretList == nil {
		return true, ""
	}
	ok := true
	var failures []string
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		raw, err := c.readFile(filepath.Join(dir, "space.yaml"))
		if err != nil {
			continue
		}
		manifest, err := space.ParseManifest(raw)
		if err != nil || len(manifest.NotificationRoutes) == 0 {
			continue
		}
		owner, name, err := doctorRepoOwnerName(ref.RepoURL)
		if err != nil {
			ok = false
			failures = append(failures, ref.ID+": cannot resolve repo owner/name: "+err.Error())
			continue
		}
		if secretOK, detail := notifyCheckSecret(ctx, c.ghSecretList, owner+"/"+name, manifest); !secretOK {
			ok = false
			failures = append(failures, ref.ID+": "+detail)
		}
	}
	return ok, strings.Join(failures, "; ")
}

// doctorCheckNotifyDelivery is P6's third notify check: the most recent
// a2a-notify run on the connected space's default branch concluded green.
// Uses the SAME space write credential doctorCheckStuckGreenPRs/
// doctorCheckDefaultBranchHealthy already resolve — a read-only use of it,
// never a second credential source.
func (c *DoctorCommand) doctorCheckNotifyDelivery(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 || c.notifyAdapter == nil {
		return true, ""
	}
	ok := true
	var failures []string
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		raw, err := c.readFile(filepath.Join(dir, "space.yaml"))
		if err != nil {
			continue
		}
		manifest, err := space.ParseManifest(raw)
		if err != nil || len(manifest.NotificationRoutes) == 0 {
			continue // no route configured: nothing to check delivery for
		}
		owner, name, err := doctorRepoOwnerName(ref.RepoURL)
		if err != nil {
			ok = false
			failures = append(failures, ref.ID+": cannot resolve repo owner/name: "+err.Error())
			continue
		}
		cred, err := c.doctorResolveSpaceCredential(ctx, ref, machine)
		if err != nil {
			ok = false
			failures = append(failures, ref.ID+": cannot resolve a credential: "+err.Error())
			continue
		}
		if deliveryOK, detail := notifyCheckDelivery(ctx, c.notifyAdapter, cred.Token, owner, name); !deliveryOK {
			ok = false
			failures = append(failures, ref.ID+": "+detail)
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

// doctorResolveSpaceCredential resolves ref's write credential the way every
// network-backed doctor check needs it: an optional machine-config credential
// reference is parsed (a malformed one is treated as absent rather than a
// hard error — the read this feeds is advisory-on-failure everywhere it is
// used, see doctorCheckStuckGreenPRs/doctorCheckDefaultBranchHealthy) and
// handed to c.resolveCredential. This is the byte-identical block those two
// checks shared before this extraction; doctorCheckAutoMerge and
// doctorCheckCodeownersResolvable keep their own copies untouched — folding
// them in is out of this change's scope.
func (c *DoctorCommand) doctorResolveSpaceCredential(ctx context.Context, ref space.Ref, machine space.MachineConfig) (host.Credential, error) {
	var parsedRef space.CredentialReference
	if raw, present := machine.Credentials[ref.ID]; present {
		if parsed, err := space.ParseCredentialReference(raw); err == nil {
			parsedRef = parsed
		}
	}
	return c.resolveCredential(ctx, space.CredentialEnvVar(ref.ID), parsedRef)
}

func (c *DoctorCommand) doctorCheckStuckGreenPRs(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	lister, ok := c.h.(host.OpenPRLister)
	if !ok {
		return true, " · stuck-green state unverified: this build wires no open-PR reader"
	}

	var stuck, unverifiable []string
	for _, ref := range cfg.Spaces {
		owner, name, err := doctorRepoOwnerName(ref.RepoURL)
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}
		credential, err := c.doctorResolveSpaceCredential(ctx, ref, machine)
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}
		repo := host.Repo{Owner: owner, Name: name}
		prs, err := lister.ListOpenPRs(ctx, host.ListOpenPRsRequest{Repo: repo, Credential: credential})
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}
		for _, pr := range prs {
			if pr.AutoMergeArmed {
				continue
			}
			status, statusErr := c.h.CheckStatus(ctx, host.StatusRequest{
				Repo: repo, PRNumber: pr.Number, Credential: credential,
			})
			if statusErr != nil {
				unverifiable = append(unverifiable, fmt.Sprintf("%s#%d", ref.ID, pr.Number))
				continue
			}
			if status.State != "completed" || status.Conclusion != "success" || len(status.Ambiguous) != 0 {
				continue
			}
			url := pr.URL
			if url == "" {
				url = fmt.Sprintf("PR #%d", pr.Number)
			}
			stuck = append(stuck, fmt.Sprintf(
				"%s %s is open with a green required check but auto-merge is not armed — run `gh pr merge %d --auto --repo %s/%s`; doctor will never merge it itself",
				ref.ID, url, pr.Number, owner, name,
			))
		}
	}
	sort.Strings(stuck)
	sort.Strings(unverifiable)
	if len(stuck) > 0 {
		return false, strings.Join(stuck, "; ")
	}
	if len(unverifiable) > 0 {
		return true, " · stuck-green state unverified for " + strings.Join(unverifiable, ", ")
	}
	return true, ""
}

// doctorCheckDefaultBranchHealthy is R4c (fb-20260812-ee6dcd; ADR-011
// Consequences: "a validator whose own verdict nothing surfaces has stopped
// being a gate and become a log entry"). `a2a doctor` used to answer
// fourteen PASS on a space whose default branch had been red for four
// hours: the post-merge required-check verdict existed (GitHub computed it)
// but nothing doctor read ever looked at it, because every other check is
// scoped to an open PR and a red default branch has none. This check reads
// that verdict directly, over host.RefStatusReader — the same required-check
// read doctorCheckStuckGreenPRs performs, merely scoped to a branch instead
// of a PR (see RefStatusReader's doc comment in internal/host/host.go).
//
// The branch itself is DERIVED per space (no-silent-yes-2026-08 P2b), from
// c.resolveMirror(...)'s own refs/remotes/origin/HEAD via
// space.ResolveBaseBranch — never the hardcoded "main" this check used to
// probe unconditionally, which for a master-default space silently checked
// a ref that could never match ("default branch healthy" would then have
// stayed permanently UNVERIFIABLE, and said nothing about it). When the
// branch cannot be derived (mirror never synced, or the remote genuinely
// publishes no HEAD) this check falls back to probing "main" exactly as it
// always did, rather than refusing to check anything — this is a READ-ONLY
// advisory over a GitHub Checks-API ref, never a push, so ResolveBaseBranch's
// own doc comment licenses exactly this caller to keep the old best-effort
// guess (see mirror.go's remoteHeadBranch, the same convention for mirror
// hygiene's own read-only checkout). The discoverability note below is
// honest about which of the two happened: a derived branch is named
// outright, an undetermined one says so instead of asserting "main" as fact.
//
// This IS ALSO the epic's own discoverability instrument (AC-2): every
// connected space's branch (derived, or the best-effort guess) is named in
// the returned detail regardless of outcome, so a joining team reads the
// answer BEFORE its first submit — including when this build wires no
// RefStatusReader at all (the trap: that capability gate used to return
// before any per-space work ran, which would have made the note invisible on
// exactly the build this discoverability instrument most needs to reach).
//
// Outcomes, matching doctorCheckAutoMerge/doctorCheckStuckGreenPRs' shape:
//   - zero connected spaces -> PASS, empty detail (vacuously nothing to check).
//   - host wires no RefStatusReader -> PASS with an advisory: a missing
//     capability is not a red (doctorCheckAutoMerge's own doc comment states
//     the same reasoning; a gate that reds on something that is not broken
//     teaches people to stop reading it).
//   - per space: an unparsable repo URL, a credential-resolution failure, or
//     the READ itself failing -> UNVERIFIABLE, never FAIL. An unanswerable
//     question is not evidence the branch is broken.
//   - no required check run matched the ref at all -> UNVERIFIABLE — there is
//     no verdict to compare against, the same "no check" case CheckStatus
//     already reports for a PR with none.
//   - the matched run's State != "completed" (queued/in_progress) -> PASS
//     with an advisory: a run in flight has reached no verdict yet, and must
//     never redden doctor while it is still running.
//   - State == "completed": Conclusion success/neutral/skipped is healthy;
//     failure/timed_out/cancelled/action_required is FAIL, naming the space,
//     the branch, the conclusion, and — when GitHub reported one —
//     status.URL, so the ONE row whose entire job is "go look at the failing
//     run" ends in a link, not a description of one. Absent URL degrades to
//     the message this check has always printed, never a bare/empty link.
//   - any other completed Conclusion (GitHub's check-run vocabulary is not
//     closed) -> UNVERIFIABLE rather than guessed either way.
func (c *DoctorCommand) doctorCheckDefaultBranchHealthy(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	reader, isReader := c.h.(host.RefStatusReader)

	var unhealthy, pending, unverifiable, branchNotes []string
	for _, ref := range cfg.Spaces {
		branch, branchErr := space.ResolveBaseBranch(ctx, c.resolveMirror(c.projectRoot, ref, machine))
		if branchErr != nil {
			branch = "main"
			branchNotes = append(branchNotes, ref.ID+`: base branch could not be determined (checked "main")`)
		} else {
			branchNotes = append(branchNotes, ref.ID+": "+branch)
		}

		if !isReader {
			continue
		}
		owner, name, err := doctorRepoOwnerName(ref.RepoURL)
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}
		credential, err := c.doctorResolveSpaceCredential(ctx, ref, machine)
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}
		status, err := reader.RefCheckStatus(ctx, host.RefStatusRequest{
			Repo: host.Repo{Owner: owner, Name: name}, Ref: branch, Credential: credential,
		})
		if err != nil {
			unverifiable = append(unverifiable, ref.ID)
			continue
		}
		if status.Name == "" {
			// No required check run matched this ref — nothing to judge,
			// which is distinct from a run that judged and concluded.
			unverifiable = append(unverifiable, ref.ID)
			continue
		}
		if status.State != "completed" {
			pending = append(pending, ref.ID)
			continue
		}
		switch status.Conclusion {
		case "success", "neutral", "skipped":
			// healthy — no entry in any list.
		case "failure", "timed_out", "cancelled", "action_required":
			line := fmt.Sprintf(
				"%s: default branch %q's required check (%s) concluded %q",
				ref.ID, branch, status.Name, status.Conclusion,
			)
			// status.URL is empty whenever GitHub's check-runs response
			// carried none (or the run predates html_url in the fixture) —
			// the message stays exactly what it was before URL existed
			// rather than appending a bare/empty link.
			if status.URL != "" {
				line += " — " + status.URL
			}
			unhealthy = append(unhealthy, line)
		default:
			// An unrecognised completed conclusion is not evidence of
			// brokenness — GitHub's check-run vocabulary is not closed.
			unverifiable = append(unverifiable, ref.ID)
		}
	}
	sort.Strings(unhealthy)
	sort.Strings(pending)
	sort.Strings(unverifiable)
	sort.Strings(branchNotes)
	var branchSuffix string
	if len(branchNotes) > 0 {
		branchSuffix = " · base branch: " + strings.Join(branchNotes, "; ")
	}
	if len(unhealthy) > 0 {
		// A space whose default branch is genuinely red is the failure, even
		// if a sibling space could not be read — the actionable half wins,
		// same precedence doctorCheckAutoMerge/doctorCheckStuckGreenPRs use.
		return false, strings.Join(unhealthy, "; ") + branchSuffix
	}
	if !isReader {
		return true, " · default-branch health unverified: this build wires no ref status reader" + branchSuffix
	}
	var advisories []string
	if len(pending) > 0 {
		advisories = append(advisories, "default-branch check still running for "+strings.Join(pending, ", "))
	}
	if len(unverifiable) > 0 {
		advisories = append(advisories, "default-branch health unverified for "+strings.Join(unverifiable, ", "))
	}
	if len(advisories) > 0 {
		return true, " · " + strings.Join(advisories, "; ") + branchSuffix
	}
	if branchSuffix != "" {
		return true, branchSuffix
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
	if _, err := os.Stat(filepath.Join(c.projectRoot, filepath.FromSlash(c.skillDir), "SKILL.md")); err != nil {
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

// doctorSkillManualStampPattern captures whatever the stamp actually says, digit
// or not — so a note can report the CAUSE ("a2a dev") instead of the useless
// "version unknown" that covered every non-numeric value alike.
//
// Anchored on the OPENING PAREN, unlike its numeric sibling. Without it the match
// is greedy from the first "a2a " in PROVENANCE.md's own prose ("Written by `a2a
// skill install` / `a2a init` (a2a dev).") and captures the whole sentence. The
// sibling gets away with a looser anchor only because its required leading digit
// happens to constrain it — caught by this row's own new test on its first run,
// which is the argument for having written the test before trusting the regexp.
var doctorSkillManualStampPattern = regexp.MustCompile(`\(a2a ([^)]+)\)`)

// doctorCheckSkillManualCurrent is P31 wave 5's out-of-band-update catch,
// closed by judge-the-thing-2026-08 P5: reading only PROVENANCE.md's version
// STAMP against the binary version answers a question about a NOTE attached
// to the tree, never about the tree itself, and every branch used to return
// `true` — the row could not fail at all. `refreshInstalledSkill` used to
// write the OLD binary's embedded tree stamped with the NEW version in one
// call (fixed by P4, `internal/cli/cmd_update.go`); an interrupted update, a
// binary swapped by hand, a downgrade, or a hand-edit all reach the same
// disagreeing state, and this row is the only thing that ever looks at the
// TREE to catch it.
//
// The walk is decisive ONLY when the stamp names THIS binary's own version
// (see "The equality rows 3 and 4 stand on" in spec 05 — `skillProvenance`
// and this check's own stamp regexp share one package-level `version` value
// passed at both call sites in cmd/a2a/wire.go, §8 #14 pins the standing
// dependency): a stamp naming a DIFFERENT version names a tree this binary
// does not have, so any disk/embed difference there is the expected
// consequence of comparing against the wrong referent, never evidence of a
// contradiction. `dev` is the one deliberate exception — the stamp says
// nothing by construction, so the walk is the ONLY signal that exists and it
// always runs, but it can still never FAIL (there is no claim to
// contradict; see spec 05 §0 "Row 8 is the fork this spec takes
// deliberately").
//
// `skillTargetState` (cmd_skill.go) decides ownership FIRST: a non-empty,
// unowned directory (no a2ahub provenance marker) is someone else's, and
// this check has no standing to judge a tree it never installed — it PASSes
// without reading it, let alone walking it.
func (c *DoctorCommand) doctorCheckSkillManualCurrent() (bool, string) {
	target := filepath.Join(c.projectRoot, filepath.FromSlash(c.skillDir))
	nonEmpty, owned, err := skillTargetState(target)
	if err != nil || !nonEmpty {
		return true, " · no skill installed"
	}
	if !owned {
		return true, " · skill directory is not an a2ahub-managed install — not judged"
	}

	data, err := os.ReadFile(filepath.Join(target, skillProvenanceFile)) //nolint:gosec // reason: target is the resolved skill-install directory this binary itself computes from project config, never caller-supplied input.
	if err != nil {
		return true, " · no skill installed"
	}

	// "version unknown" used to be the answer to THREE different situations, and
	// it named neither the cause nor a remedy. It also fires constantly in normal
	// development: a dev build stamps `(a2a dev)`, and the version pattern
	// requires a leading digit, so every locally-installed skill reported
	// "version unknown" with nothing to say why.
	//
	// Read from real doctor output on 2026-07-26, the same way the skill-link
	// advisory's dead remedy was: a note that names neither cause nor action is
	// the mildest form of the same defect. Naming a remedy here would be the
	// louder form of it — `a2a skill install` from a dev build re-stamps `dev`
	// and changes nothing.
	stamp := doctorSkillManualStampPattern.FindStringSubmatch(string(data))
	match := doctorSkillManualVersionPattern.FindStringSubmatch(string(data))
	if match == nil {
		if len(stamp) > 1 && stamp[1] == "dev" {
			return true, c.doctorSkillManualDevNote(target)
		}
		if len(stamp) > 1 {
			return true, fmt.Sprintf(" · skill installed, but its version stamp %q is not a release "+
				"version — `a2a skill install` from a released binary re-stamps it", stamp[1])
		}
		return true, " · skill installed, but PROVENANCE.md carries no version stamp (hand-edited, or " +
			"written by a foreign tool) — `a2a skill install` rewrites it"
	}
	manualVersion := match[1]

	older, err := version.OlderThan(manualVersion, c.binaryVersion)
	if err != nil {
		// The MANUAL parsed as a version and the comparison still failed, which
		// means this BINARY's own version is the unreadable one — a dev build.
		// Saying "version unknown" here blamed the skill for the binary.
		return true, fmt.Sprintf(" · skill manual is v%s; this binary's version (%q) is not comparable, "+
			"so drift cannot be checked — expected on a development build", manualVersion, c.binaryVersion)
	}
	if older {
		return true, fmt.Sprintf(
			" · skill manual is v%s, binary is v%s — run 'a2a skill install'",
			manualVersion, c.binaryVersion)
	}
	// Row 6 (spec 05 §0): a manual stamped NEWER than this binary used to
	// collapse into "skill manual current", which read a future release's
	// manual as current. `older` false does not mean equal — check the other
	// direction before treating the stamp as this binary's own.
	if newer, newerErr := version.OlderThan(c.binaryVersion, manualVersion); newerErr == nil && newer {
		return true, fmt.Sprintf(
			" · skill manual is v%s, newer than this binary (v%s) — 'a2a skill install' would write an OLDER manual",
			manualVersion, c.binaryVersion)
	}

	// Equal — the stamp names THIS binary, so a disk/embed difference is
	// real evidence, not an artifact of comparing against the wrong release.
	return c.doctorSkillManualWalk(target, manualVersion)
}

// doctorSkillManualDevNote answers row 8 (spec 05 §0): a `dev` stamp names no
// version to compare, so the tree walk is the ONLY signal that exists and it
// always runs — but it can never FAIL (there is no claim to contradict).
func (c *DoctorCommand) doctorSkillManualDevNote(target string) string {
	inSync := " · skill installed by a development build (a2a dev), so there is no version to " +
		"compare — expected when running from source"
	if c.SkillFiles == nil {
		return inSync
	}
	res, err := doctorSkillTreeCompare(c.SkillFiles, target)
	if err != nil || res.undecidable != "" || !res.hasDiff() {
		return inSync
	}
	return fmt.Sprintf(" · skill installed by a development build (a2a dev); its tree differs from this "+
		"binary's embedded tree in %d files (%s) — 'a2a skill install' rewrites them",
		res.diffCount(), doctorSkillTreeNames(res, 5))
}

// doctorSkillManualWalk answers rows 3/4/10 (spec 05 §0): the stamp names
// THIS binary, so the embedded tree is the authority the disk install is
// judged against. c.SkillFiles == nil is undecidable-not-a-guess (the
// TemplateFiles/doctorCheckScaffoldingCurrent precedent) — a forgotten
// cmd/a2a wiring line must degrade to a named advisory, never to a silent
// PASS carrying the tree-verified note (§8 #10).
func (c *DoctorCommand) doctorSkillManualWalk(target, manualVersion string) (bool, string) {
	if c.SkillFiles == nil {
		return true, " · skill tree unverified: this build wires no embedded skill tree"
	}
	res, err := doctorSkillTreeCompare(c.SkillFiles, target)
	if err != nil {
		// The embed side failed to walk, which should not happen against a
		// compiled-in tree — degrade to the stamp-only wording rather than
		// surfacing a raw error (doctorCheckScaffoldingCurrent's own
		// discipline: a class, never the raw message).
		return true, fmt.Sprintf(" · skill manual current (v%s)", manualVersion)
	}
	if res.undecidable != "" {
		return true, fmt.Sprintf(" · skill manual current (v%s) — tree could not be fully verified: "+
			"%s could not be read", manualVersion, res.undecidable)
	}
	if !res.hasDiff() {
		return true, fmt.Sprintf(" · skill manual current (v%s, %d files verified against this binary's own tree)",
			manualVersion, res.total)
	}
	return false, doctorSkillTreeFailLine(manualVersion, res)
}

// doctorSkillTreeResult is doctorSkillTreeCompare's verdict: three
// difference classes (spec 05 "What is compared, exactly" — reported
// separately because they are three different defects), each sorted
// lexicographically within itself (map iteration order would flap and break
// output stability, spec 05 §8 #9). total is the count of files the embed
// side carries — note text only, never an assertion target (§8 #10: a 36th
// skill file must never red the stable-substring assertion). undecidable
// carries the relative path of a disk file the walk could not read
// (permissions, a dangling symlink) — the walk stops there rather than
// guessing a classification for it.
type doctorSkillTreeResult struct {
	total       int
	missing     []string // present in the embed, absent on disk
	modified    []string // present in both, bytes differ
	unexpected  []string // present on disk, absent from the embed
	undecidable string
}

func (r doctorSkillTreeResult) hasDiff() bool {
	return len(r.missing) > 0 || len(r.modified) > 0 || len(r.unexpected) > 0
}

func (r doctorSkillTreeResult) diffCount() int {
	return len(r.missing) + len(r.modified) + len(r.unexpected)
}

// doctorSkillTreeReadDisk reads path, distinguishing "genuinely absent"
// (Lstat itself says nothing is there) from "present but unreadable"
// (permission denied, or a symlink whose target does not resolve) — the
// latter is undecidable, never silently reclassified as missing.
func doctorSkillTreeReadDisk(path string) (data []byte, undecidable bool) {
	if _, err := os.Lstat(path); err != nil {
		return nil, false // absent — the caller's missing/unexpected bookkeeping handles this
	}
	data, err := os.ReadFile(path) //nolint:gosec // reason: path is produced by filepath.WalkDir over the resolved skill-install directory, never caller-supplied input.
	if err != nil {
		return nil, true
	}
	return data, false
}

// doctorSkillTreeCompare walks c.SkillFiles the same way writeSkillTree does
// (fs.WalkDir(files, "a2ahub") + the "a2ahub/" prefix strip, spec 05 §5 anti-
// duplication — check and installer are proven to agree by this round-trip,
// never by extracting a shared function) and diffs it against target, the
// disk-side skill install. The one exemption is skillProvenanceFile at
// target's OWN root — an exact string match against the existing constant,
// never a suffix or basename match, so `reference/PROVENANCE.md` is NOT
// exempt (spec 05 "The exclusion rule, and why it cannot widen"). The
// returned error is reserved for the embed side failing to walk at all
// (should not happen against a compiled-in tree); a disk-side read failure
// is reported through result.undecidable instead, never as an error.
func doctorSkillTreeCompare(files fs.FS, target string) (doctorSkillTreeResult, error) {
	var res doctorSkillTreeResult

	diskFiles := map[string]bool{}
	walkErr := filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			rel, relErr := filepath.Rel(target, p)
			if relErr != nil {
				rel = p
			}
			res.undecidable = filepath.ToSlash(rel)
			return filepath.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(target, p)
		if relErr != nil {
			// This function's own contract, four lines up: a disk-side
			// failure surfaces through res.undecidable, NEVER silently.
			// Returning nil here dropped the file from diskFiles, so a file
			// the walk found but could not place vanished from the
			// comparison — and "unexpected" is precisely the verdict this
			// check exists to produce. Same handling as the walk-error
			// branch above.
			res.undecidable = filepath.ToSlash(p)
			return filepath.SkipAll
		}
		rel = filepath.ToSlash(rel)
		if rel == skillProvenanceFile {
			return nil
		}
		diskFiles[rel] = true
		return nil
	})
	if res.undecidable != "" {
		return res, nil
	}
	if walkErr != nil {
		return res, walkErr
	}

	walkErr = fs.WalkDir(files, "a2ahub", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "a2ahub"), "/")
		res.total++
		embedData, readErr := fs.ReadFile(files, p)
		if readErr != nil {
			return readErr
		}
		if !diskFiles[rel] {
			res.missing = append(res.missing, rel)
			return nil
		}
		delete(diskFiles, rel)
		diskData, undecidable := doctorSkillTreeReadDisk(filepath.Join(target, filepath.FromSlash(rel)))
		if undecidable {
			res.undecidable = rel
			return fs.SkipAll
		}
		if !bytes.Equal(embedData, diskData) {
			res.modified = append(res.modified, rel)
		}
		return nil
	})
	if res.undecidable != "" {
		return doctorSkillTreeResult{total: res.total, undecidable: res.undecidable}, nil
	}
	if walkErr != nil {
		return res, walkErr
	}

	for rel := range diskFiles {
		res.unexpected = append(res.unexpected, rel)
	}
	sort.Strings(res.missing)
	sort.Strings(res.modified)
	sort.Strings(res.unexpected)
	return res, nil
}

// doctorSkillTreeFailLine renders row 4 (spec 05 "The failure line"): a
// single bounded, deterministic line naming the contradicted stamp, all
// three class counts (including zeros, so the shape is stable), up to five
// sorted class-labelled paths then "(+N more)", and a remedy that says
// plainly that it overwrites local edits (spec 05 "The deliberate hand-
// edit": the remedy destroys the edit, so the reader learns the real shape
// of what they did).
func doctorSkillTreeFailLine(stamp string, res doctorSkillTreeResult) string {
	lead := fmt.Sprintf("skill tree does not match this binary (stamp says v%s)", stamp)
	counts := fmt.Sprintf("%d missing, %d modified, %d unexpected", len(res.missing), len(res.modified), len(res.unexpected))

	type labeled struct{ class, path string }
	var all []labeled
	for _, p := range res.missing {
		all = append(all, labeled{"missing", p})
	}
	for _, p := range res.modified {
		all = append(all, labeled{"modified", p})
	}
	for _, p := range res.unexpected {
		all = append(all, labeled{"unexpected", p})
	}
	more := 0
	if len(all) > 5 {
		more = len(all) - 5
		all = all[:5]
	}
	named := make([]string, 0, len(all))
	for _, l := range all {
		named = append(named, l.class+": "+l.path)
	}
	detail := strings.Join(named, "; ")
	if more > 0 {
		detail += fmt.Sprintf(" (+%d more)", more)
	}
	return fmt.Sprintf("%s: %s (%s) — run 'a2a skill install' (this OVERWRITES local edits)", lead, counts, detail)
}

// doctorSkillTreeNames renders up to limit sorted, unlabelled paths across
// all three difference classes for the row 8 (`dev`) advisory, which never
// FAILs and so never needs the class labels the failure line uses to name a
// contradiction — dev has none to name.
func doctorSkillTreeNames(res doctorSkillTreeResult, limit int) string {
	names := make([]string, 0, res.diffCount())
	names = append(names, res.missing...)
	names = append(names, res.modified...)
	names = append(names, res.unexpected...)
	sort.Strings(names)
	more := 0
	if len(names) > limit {
		more = len(names) - limit
		names = names[:limit]
	}
	out := strings.Join(names, ", ")
	if more > 0 {
		out += fmt.Sprintf(" (+%d more)", more)
	}
	return out
}

// doctorCheckAutomationCoverage names the two automations
// skill/a2ahub/onboarding.md §8.6 ("Making the loop run without a human")
// recommends — a harness session-start hook and a scheduled `a2a-poll.yml`
// workflow in the PARTICIPANT's own repository — and says plainly that
// doctor cannot observe either one, and why, rather than staying silent
// about them.
//
// This is deliberately NOT a detection check (this phase's brief; D-021,
// docs/the-plan/plan/07-client.md:140, 14-us-ac.md:143/:163: integration is
// "advisory, never invasive… every user owns their statusline/harness
// config"). Reading a harness's own settings file (e.g. Claude Code's
// .claude/settings.json) would also be provider-specific and silent on
// every other harness — the loud-specific-WRONG shape
// doctorCheckSkillDiscoverable's own history already warns against (a
// remedy that only works for one surface trains readers to distrust the
// row). The poll workflow lives in the PARTICIPANT's repository, which is
// never a tree doctor is pointed at: cfg.Spaces names the SPACE mirrors
// doctor clones, not the participant's own working copy, so there is no
// config surface here to read even if reading harness config were in
// scope.
//
// Always PASS, with an advisory note — never a FAIL and never a bare PASS
// with nothing said. A FAIL would misclaim doctor observed a broken state
// it structurally cannot see; a bare PASS would be silent about something
// onboarding.md tells every new participant to set up, which is the exact
// gap this check exists to close.
func (c *DoctorCommand) doctorCheckAutomationCoverage() (bool, string) {
	return true, " · doctor cannot see whether either automation onboarding.md §8.6 " +
		"recommends is set up: (1) a harness session-start hook running `a2a sync && " +
		"a2a inbox --actionable` — that is your harness's own config (D-021: a2a " +
		"never reads or writes it), or (2) a scheduled a2a-poll.yml workflow in " +
		"the PARTICIPANT's own repository (not the space, so it is outside every " +
		"path doctor reads) — check both by hand; see onboarding.md §8.6"
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

// doctorCheckThreadsIntact is spec 46 §T2's advisory row: it counts the
// artifacts in every connected space whose thread linkage cannot be trusted.
//
// Three populations, each a different failure:
//   - NO thread at all — an artifact committed before threads were minted.
//     Every read verb still shows it, but it belongs to no conversation and
//     `a2a respond` will REFUSE to reply to it, so the operator needs to know
//     before an agent hits that wall mid-exchange.
//   - a thread whose `<system>` token names no participant of that space — the
//     signature of a value typed by hand or copied from another space.
//   - a thread with exactly ONE member that is not the space's own newest
//     work. Harmless in isolation (every fresh artifact starts as a thread of
//     one) and therefore reported as a count only, never as a name: it is the
//     residual same-system same-day rand4 typo's only visible trace.
//
// It PASSES with an advisory rather than failing, deliberately. Pre-thread
// artifacts cannot be repaired in place — git is the archive and artifacts
// never move (§3.8) — so a failing row would be a permanent red on any space
// that predates this release, and a doctor row that can never go green teaches
// people to stop reading doctor.
func (c *DoctorCommand) doctorCheckThreadsIntact(cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	var threadless, foreign int
	var perSpace []string

	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		manifestRaw, err := c.readFile(filepath.Join(dir, "space.yaml"))
		if err != nil {
			// "space access" already reports an unreachable mirror; do not
			// double-report the same root cause.
			continue
		}
		manifest, merr := space.ParseManifest(manifestRaw)
		if merr != nil {
			continue
		}
		members := map[string]bool{}
		for _, p := range manifest.Participants {
			members[p.System] = true
		}

		spaceThreadless, spaceForeign := 0, 0
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
			// A walk error on one entry is skipped rather than aborting the
			// whole space: an advisory row that gives up on the first
			// unreadable directory would report zero problems for a space
			// full of them, which is the opposite of what it is for. The
			// unreadable-mirror case is reported by "space access".
			if werr != nil {
				return nil //nolint:nilerr // reason: per-entry walk errors are deliberately skipped, see above
			}
			if d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			thread, ok := c.doctorReadArtifactThread(path)
			if !ok {
				// An unreadable or undecodable file is not this row's
				// business — it is the silent-drop condition already filed
				// in docs/backlog.md, and reporting it here as a thread
				// problem would name the wrong cause.
				return nil
			}
			if thread == "" {
				spaceThreadless++
				return nil
			}
			parsed, perr := artifact.ParseThreadID(thread)
			if perr != nil || !members[parsed.System] {
				spaceForeign++
			}
			return nil
		})

		threadless += spaceThreadless
		foreign += spaceForeign
		if spaceThreadless > 0 || spaceForeign > 0 {
			perSpace = append(perSpace, fmt.Sprintf("%s: %d without a thread, %d with a thread minted outside the space",
				ref.ID, spaceThreadless, spaceForeign))
		}
	}

	if threadless == 0 && foreign == 0 {
		return true, ""
	}
	advisory := " · " + strings.Join(perSpace, "; ")
	if threadless > 0 {
		advisory += " — those artifacts predate thread propagation: `a2a respond` refuses to reply to them, and they cannot be repaired in place (git is the archive). Reseeding the space is the only fix."
	}
	return true, advisory
}

// doctorReadArtifactThread reads one artifact's `thread` field, reporting
// whether the file could be decoded at all. A bool rather than an error
// because every caller's response to "undecodable" is identical (skip it —
// that condition has its own backlog row), and returning an error the caller
// always discards is the nilerr trap this package has fallen into twice.
func (c *DoctorCommand) doctorReadArtifactThread(path string) (thread string, ok bool) {
	raw, err := c.readFile(path)
	if err != nil {
		return "", false
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return "", false
	}
	var probe struct {
		ID     string `yaml:"id"`
		Thread string `yaml:"thread"`
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil || probe.ID == "" {
		return "", false
	}
	return probe.Thread, true
}

// doctorCheckSkippedFiles is stage 2 of the defect filed 2026-07-26: the
// read model (internal/cache) was already best-effort BY DESIGN — one
// malformed artifact/event file must never blind the whole space to every
// other document in it — but the file it dropped was silently
// indistinguishable from one that simply did not exist. `a2a search`,
// `a2a inbox` and `a2a thread` all showed one FEWER row than the space
// actually held, with no word anywhere, and an agent running those verbs
// never runs `a2a doctor` on its own — so stage 1 (internal/cache/
// skipped.go) computes the fact and stage 2's OTHER half wires it to
// stderr on the read verbs themselves (skipadvisory.go); this row is doctor's
// own half, for the operator who runs doctor without ever hitting a skipped
// file through a read verb.
//
// Modeled on doctorCheckThreadsIntact (same file, immediately above): a
// per-connected-space best-effort scan that PASSES with an advisory rather
// than failing. But the REASON is different from that row's, and it matters:
// a pre-thread artifact is unrepairable in place because git is the archive
// (§3.8, doctorCheckThreadsIntact's own doc comment); a skipped file here
// may equally be UNREADABLE not because it is broken but because it belongs
// to a COUNTERPARTY's own section of a shared space — this project's own
// diff-authz (space.IsInfrastructurePath's single-owner rule) can leave it
// structurally unable to fix another participant's malformed document. A
// doctor row that is permanently red on someone else's file is a row nobody
// reads (the same disease doctorCheckAutoMerge's own doc comment names for
// an unverifiable read). The advisory names every skipped path and its
// reason so the operator can tell "not mine to fix" from "I broke this."
//
// This builds a throwaway *cache.Store over the resolved mirrors rather than
// re-deriving the walk doctorCheckThreadsIntact hand-rolls: internal/cache/
// skipped.go's AllSkippedFiles is the SSOT for "which files were skipped and
// why" (the fixed SkipReason* vocabulary), and re-deriving that vocabulary
// here would be exactly the second-copy risk this package's own anti-dup
// rule warns against. cache.Store.index (AllSkippedFiles' own callee) is a
// pure read over each mirror's working tree — no cursor file, no write —
// so an empty ownSystem/cacheDir and time.Now are safe placeholders here;
// none of them is read by AllSkippedFiles' own call path.
func (c *DoctorCommand) doctorCheckSkippedFiles(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) (bool, string) {
	if len(cfg.Spaces) == 0 {
		return true, ""
	}
	mirrors := make([]cache.SpaceMirror, 0, len(cfg.Spaces))
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		raw, err := c.readFile(filepath.Join(dir, "space.yaml"))
		if err != nil {
			// "space access" already reports an unreachable mirror; do not
			// double-report the same root cause.
			continue
		}
		manifest, merr := space.ParseManifest(raw)
		if merr != nil {
			// "versions" already reports an unparseable manifest.
			continue
		}
		mirrors = append(mirrors, cache.SpaceMirror{SpaceID: ref.ID, Dir: dir, Manifest: manifest})
	}
	if len(mirrors) == 0 {
		return true, ""
	}

	store := cache.NewStore("", "", mirrors, time.Now, 0)
	bySpace, err := store.AllSkippedFiles(ctx)
	if err != nil {
		// Never a FAIL for a read-side failure this check cannot itself
		// repair — see this function's own doc comment.
		return true, " · skipped-file check could not run: " + err.Error()
	}

	spaceIDs := make([]string, 0, len(bySpace))
	for id := range bySpace {
		spaceIDs = append(spaceIDs, id)
	}
	sort.Strings(spaceIDs)

	var notes []string
	for _, id := range spaceIDs {
		files := bySpace[id]
		if len(files) == 0 {
			continue
		}
		parts := make([]string, 0, len(files))
		for _, f := range files {
			parts = append(parts, fmt.Sprintf("%s (%s)", f.Path, f.Reason))
		}
		notes = append(notes, fmt.Sprintf("%s: %s", id, strings.Join(parts, ", ")))
	}
	if len(notes) == 0 {
		return true, ""
	}
	return true, " · mirror files could not be decoded and are missing from every read verb's output — " +
		strings.Join(notes, "; ") +
		" — fixable only by whoever owns that file's section; a counterparty's document may be outside this project's diff-authz"
}

// doctorUnadoptedConsumptionRow names one contract this project's own
// system verify-passed deliveries against without ever listing in its own
// consumes.yaml (defects-fix-2026-08 P6, spec 06, docs/inbox/defects/07).
// It reuses visibility.go's own doctorVisibilityStatus vocabulary (WARN/
// UNVERIFIED — same package, no import needed) rather than inventing a
// second one for the same shape of advisory-on-success row.
type doctorUnadoptedConsumptionRow struct {
	SpaceID string
	doctorVisibilityDecision
}

// doctorUnadoptedConsumptionRows computes spec 06's own advisory over every
// connected space: cache.FindUnadoptedConsumption (registered_consumers.go)
// is the read-only scan — FindRegisteredConsumers' own opposite direction,
// deliveries this system ACCEPTED vs contracts it DECLARED — and this
// function only turns that fact into one printable row per space, exactly
// the shape doctorVisibilityRows already takes one file over. Deliberately
// NOT folded into that function nor into visibility.go itself (off this
// phase's allowlist): a second, independent advisory computation, printed
// by its own loop in Run.
//
// cfg.System is this project's own configured identity (space.ProjectConfig's
// own field — the exact value cmd/a2a's wire.go already passes as
// ownSystem to cache.NewStore for every OTHER read verb this binary ships;
// this is the first time doctor itself needs it). An unconfigured system
// cannot resolve "mine" at all, so this returns no rows rather than
// guessing.
//
// This is WARN-only and never touches Run's own allOK (spec 06 T1:
// "consuming without adopting is a real risk and not an error; the
// operator may have reasons") — Run's own loop prints these rows entirely
// outside the `checks` slice, the same separation doctorVisibilityRows
// already established for its own advisory-on-success rows.
func (c *DoctorCommand) doctorUnadoptedConsumptionRows(cfg space.ProjectConfig, machine space.MachineConfig) []doctorUnadoptedConsumptionRow {
	if cfg.System == "" {
		return nil
	}
	refs := append([]space.Ref(nil), cfg.Spaces...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })

	var rows []doctorUnadoptedConsumptionRow
	for _, ref := range refs {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		found, skip, err := cache.FindUnadoptedConsumption(dir, cfg.System)
		if err != nil {
			// A read-side failure this check cannot itself repair — same
			// non-reporting rule doctorCheckSkippedFiles' own doc comment
			// gives for a scan that cannot run at all; "space access"/
			// "versions" already report an unreachable/unparseable mirror.
			continue
		}
		if skip != nil {
			rows = append(rows, doctorUnadoptedConsumptionRow{
				SpaceID: ref.ID,
				doctorVisibilityDecision: doctorVisibilityDecision{
					Status: doctorVisibilityUNVERIFIED,
					Detail: fmt.Sprintf("own consumes.yaml (%s) could not be read as a real consumes/v1 registry (%s) — unadopted-consumption advisory skipped", skip.Path, skip.Reason),
				},
			})
			continue
		}
		for _, u := range found {
			// word/verb agree together — "1 ... delivery conforms" vs
			// "2 ... deliveries conform" — a single %s for the noun alone
			// (delivery/deliveries) left the verb permanently singular
			// ("2 verify-passed deliveries conforms"), the one line of
			// this row's format string count==1's own test could not
			// exercise.
			word, verb := "delivery", "conforms"
			if u.Count != 1 {
				word, verb = "deliveries", "conform"
			}
			rows = append(rows, doctorUnadoptedConsumptionRow{
				SpaceID: ref.ID,
				doctorVisibilityDecision: doctorVisibilityDecision{
					Status: doctorVisibilityWARN,
					Detail: fmt.Sprintf("%d verify-passed %s %s to %s, which is not in this system's own consumes.yaml — run `a2a contract adopt %s`",
						u.Count, word, verb, u.ContractID, u.ContractID),
				},
			})
		}
	}
	return rows
}
