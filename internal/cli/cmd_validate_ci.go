// OP-204 `a2a validate --ci` (spec 17 T1/§8, plan 17 wave-10): the CI
// validation entrypoint. It runs against a SPACE-repo checkout (the CI
// cwd), loads the in-repo `./space.yaml` manifest, computes the changed
// (v3-pr) or all (v3-full-repo) `*.md` artifacts, and REUSES the existing
// V2 engine (validate.Engine.ValidateForSubmit) over each — mirroring the
// SubmitValidatorAdapter's LocalContext construction — plus a basic
// diff-authz check. ZERO new validation rules live here.
//
// This file is kept separate from cmd_submit.go's ValidateCommand.Run so
// the existing `validate <path>` / `validate --all` paths stay untouched;
// ValidateCommand.Run only delegates here when `--ci` is set.
//
// P37 wave B2 (spec 37 §2 T2/T3, AC-970.2) adds ONE more rule, still not a
// new validation RULE of its own: for every contract this PR touches, at
// merge, run the SAME `validate.CheckComputedCompatibility` core `contract
// publish` runs locally (internal/validate/compat.go), plus D-D's
// `validate.CheckContractPublishable` (POL-009). Both are exported,
// pure-input functions this file only CALLS — see
// TestValidateCIAndContractHaveNoSecondCompatCopy for the test that proves
// no second copy of either verdict exists in this package.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// gitChangedFilesFunc is the seam over `git diff --name-only` — the real
// implementation (gitDiffNameOnly) shells out; tests inject a fake so the
// CI path is unit-testable without a live git checkout.
type gitChangedFilesFunc func(ctx context.Context, root, base string) ([]string, error)

// ciReport is the machine-readable JSON shape `validate --ci` writes to
// stdout. It EXTENDS (does not fork) the existing per-artifact
// validateReport shape: Artifacts holds one validateReport per changed
// artifact (the same {path, result, error} the non-CI verb emits), plus
// the CI-specific top-level fields (mode, overall valid, diff-authz).
type ciReport struct {
	Mode      string             `json:"mode"`
	Valid     bool               `json:"valid"`
	Artifacts []validateReport   `json:"artifacts"`
	DiffAuthz []ciAuthzViolation `json:"diff_authz_violations,omitempty"`
}

// ciAuthzViolation is the stable machine shape for the CI-owned authorization
// boundary. AUTHZ-001 is intentionally outside the envelope validation-code
// registry: this verdict is about a PR diff, not an artifact's contents.
// Keeping a code, path and severity here lets GitHub surface an annotation and
// lets live evidence prove the intended refusal instead of accepting any red.
type ciAuthzViolation struct {
	Code       string `json:"code"`
	Path       string `json:"path,omitempty"`
	Severity   string `json:"severity"`
	ArtifactID string `json:"artifact_id,omitempty"`
	Author     string `json:"author"`
	CCRef      string `json:"cc_ref,omitempty"`
	Message    string `json:"message"`
}

// runValidateCI is the `--ci` path. Exit codes: 2 = usage (missing/unknown
// --mode, v3-pr without --base); 1 = any artifact violation, unreadable/
// malformed artifact, diff-authz violation, or a manifest/git error; 0 =
// all clean. JSON is written to stdout on any non-usage outcome.
//
// author is already resolved by the caller (the --author flag if given,
// else the config-layer-injected GITHUB_ACTOR) — this package never reads
// the environment itself (config & secrets rail).
func runValidateCI(ctx context.Context, engine *validate.Engine, root string, git gitChangedFilesFunc, mode, base, author string, stdio IO) int {
	switch mode {
	case "v3-pr":
		if base == "" {
			_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a validate --ci --mode=v3-pr requires --base <sha>")
			return 2
		}
	case "v3-full-repo":
		// --base is not consulted for a full-repo scan.
	case "":
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a validate --ci requires --mode=v3-pr|v3-full-repo")
		return 2
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a validate --ci: unknown mode %q (want v3-pr|v3-full-repo)\n", mode)
		return 2
	}

	raw, err := os.ReadFile(filepath.Join(root, "space.yaml"))
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "validate --ci: cannot read space.yaml: %v\n", err)
		return 1
	}
	manifest, err := space.ParseManifest(raw)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "validate --ci: %v\n", err)
		return 1
	}

	var changed []string
	if mode == "v3-pr" {
		changed, err = git(ctx, root, base)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "validate --ci: %v\n", err)
			return 1
		}
	} else {
		changed, err = walkArtifacts(root, manifest)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "validate --ci: walk repo: %v\n", err)
			return 1
		}
	}

	// Authority policy is evaluated BEFORE any path is classified through the
	// manifest. Schema defects remain changed-file-scoped for migration safety,
	// but an ambiguous ownership map is categorically different: it must never
	// be consulted to grant diff authorization, even when space.yaml predates
	// this PR.
	manifestPolicy, err := engine.ValidateManifestPolicy(raw)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "validate --ci: manifest policy: %v\n", err)
		return 1
	}
	manifestAuthorityValid := manifestPolicy.Valid

	// One pass over the changed set builds two lists:
	//   - artifacts: *.md files under a participant section — the V2 engine's
	//     input (schema/referential/authz/secret run per artifact).
	//   - authzPaths: EVERY changed path under a participant section, any
	//     extension — the diff-authz input. A PR editing another system's
	//     non-artifact files (consumes.yaml, events/*.yaml) must be authorized
	//     too, not just its *.md (else those edits are unguarded — the strict-L0
	//     gap this closes). Paths under NO section (root space.yaml, CODEOWNERS,
	//     .github/**) are deliberately excluded from author-diff-authz — they are
	//     space infrastructure governed by CODEOWNERS review + branch protection,
	//     and authorizing them here would red the space owner's own manifest edit.
	//
	// decisions/*.md is the one artifact location under NO section (§4.2's
	// multi-party space-level exception): it is validated like any other
	// artifact, but deliberately stays out of diff-authz — a decision is
	// authored by one system and approved by others, so "the PR author owns
	// this section" is the wrong question there (CODEOWNERS + the decision
	// flow's own approvals are). Before this, decisions were skipped by V3
	// entirely — invisible to the required check that gates the merge.
	//
	// consumes.yaml is the third list: a NON-artifact file (no envelope, no
	// id) that is nonetheless normative — it is the D-022 registry that
	// makes a system a registered consumer, and the retire-block check
	// reads it. Nothing validated it before, so a file the schema rejects
	// outright merged silently and simply registered nobody.
	//
	// contractIDs is the P37 wave B2 fourth bucket: every CONTRACT (by id)
	// this PR touches — a changed path that IS a contract descriptor, or
	// that lives under a descriptor's schema/**, fixtures/valid/** or
	// fixtures/invalid/** subtree (contractTouchedByPath). Deduped to ONE
	// entry per descriptor directory (contractDescPaths, seenContracts): a
	// PR changing five of a contract's schema files must produce ONE
	// compat verdict below, not five (AC-970.2's acceptance).
	var artifacts []string
	var authzPaths []string
	var consumes []string
	var events []string
	var contractIDs []string
	contractDescPaths := map[string]string{}
	seenContracts := map[string]bool{}
	for _, p := range changed {
		if id, descPath, ok := contractTouchedByPath(p); ok && !seenContracts[id] {
			seenContracts[id] = true
			contractIDs = append(contractIDs, id)
			contractDescPaths[id] = descPath
		}
		if _, ok := systemForPath(manifest, p); ok {
			authzPaths = append(authzPaths, p)
			switch {
			case strings.HasSuffix(p, ".md"):
				artifacts = append(artifacts, p)
			case isConsumesRegistry(p):
				consumes = append(consumes, p)
			case isEventDocument(p):
				events = append(events, p)
			}
			continue
		}
		if isSpaceLevelArtifact(p) {
			artifacts = append(artifacts, p)
		}
	}

	// One resolver over the whole checkout (its artifact index is built
	// once, lazily) — shared across every artifact's V2 run.
	resolver := NewMirrorResolver(root, manifest)

	report := ciReport{Mode: mode, Valid: true, Artifacts: []validateReport{}}
	if !manifestAuthorityValid && mode == "v3-pr" && len(changed) > 0 && !manifestChanged(changed) {
		policy := manifestPolicy
		report.Artifacts = append(report.Artifacts, validateReport{Path: "space.yaml", Result: &policy})
		report.Valid = false
	}
	for _, relPath := range artifacts {
		if rep, ok := validateCILinkageImmutable(ctx, root, base, relPath); rep != nil {
			report.Artifacts = append(report.Artifacts, *rep)
			if !ok {
				report.Valid = false
				continue
			}
		}
		rep, ok := validateCIArtifact(engine, root, relPath, manifest, resolver)
		if rep == nil {
			// Absent on disk (deleted in this PR) — nothing to validate.
			continue
		}
		report.Artifacts = append(report.Artifacts, *rep)
		if !ok {
			report.Valid = false
		}
	}
	for _, relPath := range consumes {
		rep, ok := validateCIConsumes(engine, root, relPath)
		if rep == nil {
			continue // deleted in this PR
		}
		report.Artifacts = append(report.Artifacts, *rep)
		if !ok {
			report.Valid = false
		}
	}

	// Every EVENT this change carries is validated against event/v1, and — once
	// the space's own floor has reached the release that introduced the field —
	// must also name the producer that wrote it.
	//
	// The SCHEMA half was missing entirely until 2026-07-26: a changed event
	// merged as long as it was parseable YAML, and the fold read it afterwards —
	// the same shape as the two holes closed alongside it. Wired only after every
	// event in both live spaces (46 of them) had been run through the schema by
	// hand and passed; a gate is not switched on to find out whether it reds.
	//
	// The PRODUCER half is the part that matters for a stale binary. See internal/space.StampProducer for why the FLOOR is the switch
	// and not the binary's own version: event/v1 is additionalProperties:false
	// and the validator is pinned BY THE SPACE, so a stamped event written to a
	// space whose validator predates the field would be refused outright.
	//
	// This is the only place an old binary can actually be stopped.
	// min_binary_version is checked inside the WRITER's binary, which binds a
	// binary that honours it and does nothing at all to one that does not —
	// before 0.9.0 that was every lifecycle and contract verb.
	changedEventPaths := make(map[string]bool, len(events))
	for _, relPath := range events {
		changedEventPaths[filepath.ToSlash(relPath)] = true
	}
	var lifecycleCandidates []ciLifecycleCandidate
	for _, relPath := range events {
		rep, ok := validateCIEvent(engine, root, relPath, manifest.MinBinaryVersion)
		if rep == nil {
			continue // deleted in this PR
		}
		report.Artifacts = append(report.Artifacts, *rep)
		if !ok {
			report.Valid = false
			continue
		}
		if mode == "v3-pr" {
			raw, err := readBoundedFile(filepath.Join(root, relPath), maxMirrorEventBytes)
			if err != nil {
				report.Artifacts[len(report.Artifacts)-1] = validateReport{Path: relPath, Error: err.Error()}
				report.Valid = false
				continue
			}
			var event lifecycleEventDoc
			if err := yaml.Unmarshal(raw, &event); err != nil {
				report.Artifacts[len(report.Artifacts)-1] = validateReport{Path: relPath, Error: err.Error()}
				report.Valid = false
				continue
			}
			lifecycleCandidates = append(lifecycleCandidates, ciLifecycleCandidate{
				path: relPath, event: event, report: len(report.Artifacts) - 1,
			})
		}
	}
	if len(lifecycleCandidates) > 0 {
		baseEvents, err := lifecycleReadBaseEvents(ctx, root, base, changedEventPaths)
		if err != nil {
			for _, candidate := range lifecycleCandidates {
				report.Artifacts[candidate.report] = validateReport{Path: candidate.path, Error: err.Error()}
			}
			report.Valid = false
		} else {
			checker := ciBaseLegalityChecker{root: root, manifest: manifest, baseEvents: baseEvents}
			for _, candidate := range lifecycleCandidates {
				result, err := engine.ValidateLifecycleCandidates(
					[]validate.CandidateEvent{lifecycleCandidate(candidate.event)},
					checker,
				)
				if err != nil {
					report.Artifacts[candidate.report] = validateReport{Path: candidate.path, Error: err.Error()}
					report.Valid = false
					continue
				}
				if result.Valid {
					continue
				}
				rep := &report.Artifacts[candidate.report]
				rep.Result.Violations = append(rep.Result.Violations, result.Violations...)
				rep.Result.Valid = false
				report.Valid = false
			}
		}
	}

	// The MANIFEST itself, when this change touches it. See
	// validate.ValidateManifest for the finding: the schema, the seam
	// (space.LoadManifest) and the adapter (cli.ManifestValidatorAdapter) all
	// existed and were wired to NOTHING, so `space.yaml` — the document that
	// decides who may write where, and the input diff-authz authorises every
	// PR against — could be merged in a shape the schema forbids.
	//
	// Gated on `space.yaml` being in the CHANGED set, deliberately. A space
	// whose manifest was merged before this check existed must not have every
	// unrelated artifact PR reddened by it: that would make an old manifest a
	// tripwire on writes nobody proposed, and the operator's only route out is
	// a manifest PR the gate itself is blocking. A change proposes the
	// manifest, or the manifest is not this PR's business.
	//
	// v3-full-repo validates it UNCONDITIONALLY, and the condition below has
	// to say so explicitly: in that mode `changed` comes from walkArtifacts,
	// which walks artifacts, so `space.yaml` is never in it. An audit of the
	// whole space that skips the file governing the space is not an audit —
	// and a full-repo scan has no merge to block, so there is no tripwire
	// concern to trade against.
	if mode == "v3-full-repo" || manifestChanged(changed) {
		rep, ok := validateCIManifest(engine, root)
		if rep != nil {
			report.Artifacts = append(report.Artifacts, *rep)
			if !ok {
				report.Valid = false
			}
		}
	}

	// Per-contract compat + publishability verdict (spec 37 §2 T2/T3,
	// AC-970.2) — v3-pr only. A full-repo scan (`--mode=v3-full-repo`) has
	// no single `--base` commit to diff a PRIOR version's fixtures out of
	// git history against, so it is not this check's scope; the merge gate
	// a raw `git push` cannot bypass is specifically the PR check.
	if mode == "v3-pr" {
		for _, id := range contractIDs {
			rep, ok := validateCIContract(ctx, root, base, id, contractDescPaths[id], stdio.Stderr)
			if rep == nil {
				continue // deleted in this PR
			}
			report.Artifacts = append(report.Artifacts, *rep)
			if !ok {
				report.Valid = false
			}
		}
	}

	// Diff-authz applies only to v3-pr (a full-repo scan has no single PR
	// author; GITHUB_ACTOR ⊆ every section can never hold across systems)
	// and only when there is at least one section-scoped changed path to
	// authorize — an empty changed set is a clean exit 0, never an
	// unmapped-author red. Gated on authzPaths (not artifacts): a PR touching
	// ONLY another system's non-artifact files must still be authorized.
	if mode == "v3-pr" && manifestAuthorityValid && len(authzPaths) > 0 {
		if authz := diffAuthz(manifest, author, authzPaths); len(authz) > 0 {
			report.DiffAuthz = authz
			report.Valid = false
		}
	}

	enc := json.NewEncoder(stdio.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "validate --ci: cannot encode JSON output: %v\n", err)
		return 1
	}
	if !report.Valid {
		return 1
	}
	return 0
}

// validateCIArtifact runs the V2 engine over one changed artifact,
// mirroring SubmitValidatorAdapter's LocalContext construction (manifest-
// backed resolver + legality). It returns (nil, true) when the path is
// absent on disk (deleted in the PR — skipped, not a violation), else a
// filled validateReport and whether the artifact is clean.
//
// Lifecycle candidates are validated separately from artifacts below:
// event/v1 documents use the PR head's envelopes and the merge-base's event
// history, excluding every changed event path. Passing events here would fold
// the PR checkout's candidate into its own prior.
func validateCIArtifact(engine *validate.Engine, root, relPath string, manifest space.Manifest, resolver validate.Resolver) (*validateReport, bool) {
	raw, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true
		}
		return &validateReport{Path: relPath, Error: err.Error()}, false
	}

	// ownSystem is the system owning the section this artifact is filed
	// under — the CC-002 authz check then verifies the envelope's `from`
	// matches the section it lives in. The filter guarantees a match, so
	// ownSystem is never empty (which would trip ErrNoOwnSystem).
	//
	// A space-level artifact (decisions/, §4.2) has no owning section, so
	// its own id's <system> token — the DRAFTING system — stands in. That
	// keeps the engine's fail-closed OwnSystem contract satisfied without
	// weakening anything: `type: decision` is exempt from the from==section
	// check by §5.2 anyway, and any OTHER type dropped into decisions/ gets
	// compared against a system that is not its own section, so it reds
	// (REF-005) exactly as it should.
	ownSystem, ok := systemForPath(manifest, relPath)
	if !ok {
		ownSystem = spaceLevelOwnSystem(relPath)
	}
	legality := NewLegalityAdapter(root, ownSystem, manifest)

	result, err := engine.ValidateForSubmit(
		validate.Draft{Path: relPath, Raw: raw},
		nil, // empty candidate events — see the lifecycle-scope note above.
		validate.LocalContext{OwnSystem: ownSystem, Resolver: resolver, Legality: legality},
	)
	if err != nil {
		return &validateReport{Path: relPath, Error: err.Error()}, false
	}
	r := result
	return &validateReport{Path: relPath, Result: &r}, result.Valid
}

// diffAuthz enforces that every section-scoped changed path — any extension,
// not just *.md artifacts — is under the PR author's section (§8 diff-authz).
// An author not mapped to any system is a CC-097 violation; a changed path
// under another system's section is a diff-authz violation. Callers pass only
// paths that already resolve to a section (systemForPath ok); paths under no
// section are space infrastructure, out of author-diff-authz scope.
func diffAuthz(manifest space.Manifest, author string, paths []string) []ciAuthzViolation {
	authorSystem, ok := manifest.SystemForLogin(author)
	if !ok {
		return []ciAuthzViolation{{
			Code:     "AUTHZ-001",
			Severity: "reject",
			Author:   author,
			CCRef:    "CC-097",
			Message:  fmt.Sprintf("PR author %q is not mapped to any system in space.yaml", author),
		}}
	}
	var out []ciAuthzViolation
	for _, relPath := range paths {
		sys, _ := systemForPath(manifest, relPath)
		if sys != authorSystem {
			out = append(out, ciAuthzViolation{
				Code:       "AUTHZ-001",
				Path:       relPath,
				Severity:   "reject",
				ArtifactID: artifactIDFromPath(relPath),
				Author:     author,
				Message:    fmt.Sprintf("changed path is outside the author's section (author system %q, path in system %q)", authorSystem, sys),
			})
		}
	}
	return out
}

func artifactIDFromPath(relPath string) string {
	if filepath.Ext(relPath) != ".md" {
		return ""
	}
	candidate := strings.TrimSuffix(filepath.Base(relPath), ".md")
	if _, err := artifact.ParseID(candidate); err != nil {
		return ""
	}
	return candidate
}

// systemForPath resolves a space-relative path to the system whose
// participant section contains it, per the manifest's `section` entries
// (e.g. "axon/"). Returns ("", false) for a path under no section.
func systemForPath(manifest space.Manifest, p string) (string, bool) {
	system := ""
	for _, part := range manifest.Participants {
		if part.Status != "active" {
			continue
		}
		sec := strings.TrimSuffix(part.Section, "/")
		if sec == "" {
			continue
		}
		if p == sec || strings.HasPrefix(p, sec+"/") {
			if system != "" && system != part.System {
				return "", false
			}
			system = part.System
		}
	}
	return system, system != ""
}

// validateCIConsumes runs the consumes/v1 schema check over one
// <system>/consumes.yaml (§5.2.3 / D-022). Same report shape as an
// artifact — the file is normative even though it carries no envelope.
func validateCIConsumes(engine *validate.Engine, root, relPath string) (*validateReport, bool) {
	raw, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true
		}
		return &validateReport{Path: relPath, Error: err.Error()}, false
	}
	result, err := engine.ValidateConsumes(raw)
	if err != nil {
		return &validateReport{Path: relPath, Error: err.Error()}, false
	}
	r := result
	return &validateReport{Path: relPath, Result: &r}, result.Valid
}

// isEventDocument reports whether a space-relative path is an event document —
// §4.2's `<system>/events/<year>/<ulid>.yaml`. Matched on the path segment
// rather than the extension alone: a `.yaml` elsewhere in a section (a
// consumes.yaml, a docs/ file) is not an event.
func isEventDocument(p string) bool {
	return strings.Contains(p, "/events/") && strings.HasSuffix(p, ".yaml")
}

// validateCIEvent validates one changed event: its event/v1 schema, and — once
// the space's floor has reached the release that introduced the field — that it
// names the producer that wrote it. The verdict comes from the engine
// (validate.ValidateEvent); this file adds ZERO validation rules of its own, the
// same discipline the compat and publishability checks follow.
func validateCIEvent(engine *validate.Engine, root, relPath, spaceFloor string) (*validateReport, bool) {
	raw, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true // deleted in this PR
		}
		return &validateReport{Path: relPath, Error: err.Error()}, false
	}
	result, err := engine.ValidateEvent(raw, spaceFloor)
	if err != nil {
		return &validateReport{Path: relPath, Error: err.Error()}, false
	}
	r := result
	return &validateReport{Path: relPath, Result: &r}, result.Valid
}

// manifestChanged reports whether this change set touches the space manifest.
//
// Matched at the repo ROOT only. `space.yaml` is a fixed, single-instance path
// (§4.2); a file of the same name nested inside a system's section is that
// system's business and is not the space's trust document, so matching on the
// basename would let an unrelated file drag the manifest check in — and, worse,
// would validate the ROOT manifest while reporting a path the author never
// touched.
func manifestChanged(changed []string) bool {
	for _, p := range changed {
		if p == "space.yaml" {
			return true
		}
	}
	return false
}

// validateCIManifest runs the manifest schema over the checkout's own
// space.yaml. Mirrors validateCIConsumes: same report shape, and an absent file
// is not a finding here — runValidateCI has already failed on an unreadable
// manifest long before this point, so the only way to reach this with no file
// is a deletion, which the diff-authz/CODEOWNERS half is what governs.
func validateCIManifest(engine *validate.Engine, root string) (*validateReport, bool) {
	const relPath = "space.yaml"
	raw, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true
		}
		return &validateReport{Path: relPath, Error: err.Error()}, false
	}
	result, err := engine.ValidateManifest(raw)
	if err != nil {
		return &validateReport{Path: relPath, Error: err.Error()}, false
	}
	r := result
	return &validateReport{Path: relPath, Result: &r}, result.Valid
}

// isConsumesRegistry reports whether a space-relative path is a system's
// consumes.yaml — the §4.2 name is fixed, one per section.
func isConsumesRegistry(p string) bool {
	return filepath.Base(p) == "consumes.yaml"
}

// contractTouchedByPath reports whether repo-relative path p is a
// contract's own descriptor file, or lives under that descriptor's
// schema/**, fixtures/valid/** or fixtures/invalid/** subtree
// (internal/space/layout.go's Layout.ProvidesContract/ProvidesSchemaDir/
// ProvidesFixturesValidDir/ProvidesFixturesInvalidDir — the §4.2
// <system>/provides/<slug>/{contract.md,schema/,fixtures/{valid,invalid}/}
// shape). Returns the contract's id and the descriptor's own repo-relative
// path.
//
// The id is reconstructed directly from the path, not looked up: a
// contract id's own grammar (artifact.ParseID) makes its <rest> token
// (the third `-`-separated segment) identical to its slug verbatim, so
// "XC-"+system+"-"+slug IS the id — the same identity ProvidesContractPath
// (internal/artifact/id.go) renders the path from, just run in reverse.
func contractTouchedByPath(p string) (id, descriptorPath string, ok bool) {
	parts := strings.Split(p, "/")
	if len(parts) < 4 || parts[1] != "provides" {
		return "", "", false
	}
	system, slug := parts[0], parts[2]
	switch {
	case len(parts) == 4 && parts[3] == "contract.md":
	case len(parts) >= 5 && parts[3] == "schema":
	case len(parts) >= 6 && parts[3] == "fixtures" && (parts[4] == "valid" || parts[4] == "invalid"):
	default:
		return "", "", false
	}
	descriptorPath = strings.Join(parts[:3], "/") + "/contract.md"
	return "XC-" + system + "-" + slug, descriptorPath, true
}

// isContractBaselinePath reports whether p is one of a contract's own
// schema/** or fixtures/** files — the D-D baseline `submit` now carries
// alongside contract.md — as opposed to the descriptor itself.
//
// Expressed on top of contractTouchedByPath so the funnel's admission and
// the CI's classification cannot drift into disagreeing about which paths
// belong to a contract. That divergence is P35's scar and AC-970.2's whole
// subject; two hand-written path predicates for one shape is how it starts.
func isContractBaselinePath(p string) bool {
	_, descriptorPath, ok := contractTouchedByPath(p)
	return ok && p != descriptorPath
}

// contractWorkingTreeFiles reads every file under
// root/descriptorDir/sub (sub e.g. "schema" or "fixtures/valid"), keyed
// the SAME way contractPriorVersionFiles/contractReadTreeAtSHA
// (contract_git.go) key their own maps: relative to descriptorDir,
// forward-slash, sub-PREFIXED (e.g. "schema/main.schema.json") — so a map
// this returns drops straight into validate.CompatInput.NewSchemas without
// re-keying. A missing directory is empty, not an error: a contract
// publishing no schema/** (or no fixtures/valid/**) at all is exactly what
// validate.CheckContractPublishable (POL-009) exists to catch, not this
// helper's concern.
func contractWorkingTreeFiles(root, descriptorDir, sub string) (map[string][]byte, error) {
	out := map[string][]byte{}
	base := filepath.Join(root, filepath.FromSlash(descriptorDir))
	dir := filepath.Join(base, filepath.FromSlash(sub))
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if errors.Is(werr, fs.ErrNotExist) {
				return nil
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		raw, rerr := readBoundedFile(p, maxMirrorEventBytes)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// contractFixturesValidOnly keeps only the fixtures/valid/** half of a
// contractReadTreeAtSHA(..., []string{"fixtures"}) result — mirroring the
// same fixtures/invalid/**-exclusion contractPriorVersionFiles
// (contract_git.go) applies for its own two-subtree callers: an invalid
// fixture failing the new schema is the fixture doing its job, not a
// compatibility break, and must never reach validate.CompatInput.
func contractFixturesValidOnly(tree map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range tree {
		if k == "fixtures/valid" || strings.HasPrefix(k, "fixtures/valid/") {
			out[k] = v
		}
	}
	return out
}

// validateCIContract is spec 37 §2 T2/T3's merge-side compat +
// publishability verdict for ONE contract this PR touched (AC-970.2): the
// SAME two exported checks `contract publish` runs locally
// (validate.CheckContractPublishable, unconditional — D-D/POL-009 — and,
// gated on validate.IsJSONSchemaFormat, validate.CheckComputedCompatibility
// — §5.4b/POL-007/POL-008), never a local re-implementation of either.
// Returns (nil, true) when the descriptor is absent on disk at HEAD
// (deleted in this PR — nothing to check), else a filled validateReport
// (Path is always the descriptor's own repo-relative path, AC-970.1) and
// whether the contract is clean.
//
// Ordering matters for one thing: when the format is JSON-Schema, the
// PRIOR version's fixtures are read via contractReadTreeAtSHA BEFORE the
// prior descriptor is read via a single `git show`. contractReadTreeAtSHA
// verifies `base` resolves to a real commit up front and fails loudly if
// not; reading the descriptor FIRST and treating any git-show failure
// (unreachable base OR a genuinely absent descriptor) as "first publish,
// nothing to check" would silently wave a breaking change through for a
// bogus `--base` — the exact fail-open wave A closed for
// contractReadTreeAtSHA itself (its own doc comment), reopened one layer
// up if this function got the order backwards.
func validateCIContract(ctx context.Context, root, base, id, descriptorPath string, stderr io.Writer) (*validateReport, bool) {
	_, probe, relPath, relDir, err := contractReadDescriptor(root, id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, true
		}
		return &validateReport{Path: descriptorPath, Error: fmt.Sprintf("contract %s: %v", id, err)}, false
	}

	schemasNew, err := contractWorkingTreeFiles(root, relDir, "schema")
	if err != nil {
		return &validateReport{Path: relPath, Error: fmt.Sprintf("contract %s: cannot read schema/**: %v", id, err)}, false
	}
	fixturesValidNew, err := contractWorkingTreeFiles(root, relDir, "fixtures/valid")
	if err != nil {
		return &validateReport{Path: relPath, Error: fmt.Sprintf("contract %s: cannot read fixtures/valid/**: %v", id, err)}, false
	}
	fixturesInvalidNew, err := contractWorkingTreeFiles(root, relDir, "fixtures/invalid")
	if err != nil {
		return &validateReport{Path: relPath, Error: fmt.Sprintf("contract %s: cannot read fixtures/invalid/**: %v", id, err)}, false
	}

	var violations []validate.Violation
	if v := validate.CheckContractPublishable(validate.PublishableInput{
		SchemaFormat:    probe.SchemaFormat,
		ContractID:      id,
		Schemas:         len(schemasNew),
		ValidFixtures:   len(fixturesValidNew),
		InvalidFixtures: len(fixturesInvalidNew),
	}); v != nil {
		violations = append(violations, *v)
	}

	if validate.IsJSONSchemaFormat(probe.SchemaFormat) {
		// PRIOR fixtures FIRST — see this function's own doc comment for
		// why the order is load-bearing.
		priorTree, terr := contractReadTreeAtSHA(ctx, root, base, relDir, []string{"fixtures"})
		if terr != nil {
			return &validateReport{Path: relPath, Error: fmt.Sprintf("contract %s: cannot read prior fixtures at base %s: %v", id, base, terr)}, false
		}

		priorRaw, gerr := contractGitBounded(ctx, root, maxMirrorEventBytes, "show", base+":"+relPath)
		switch {
		case gerr != nil:
			// base itself already resolved (contractReadTreeAtSHA above
			// would have errored otherwise) — so a failing `git show
			// base:relPath` means the descriptor genuinely does not exist
			// at base: a FIRST publish in this PR. D-B: nothing to check,
			// and say so (never silence — "a CI log that says nothing
			// reads as it passed").
			_, _ = fmt.Fprintf(stderr, "validate --ci: contract %s: no descriptor found at base %s — first publish in this PR, computed compatibility not checked\n", id, base)
		default:
			priorFM, ferr := artifact.ParseFrontmatter(priorRaw)
			var priorProbe contractDescriptorProbe
			if ferr == nil {
				ferr = yaml.Unmarshal(priorFM.YAML, &priorProbe)
			}
			if ferr != nil {
				return &validateReport{Path: relPath, Error: fmt.Sprintf("contract %s: prior descriptor at base %s is not parseable: %v", id, base, ferr)}, false
			}
			priorSemver, perr := contractParseSemver(priorProbe.Version)
			if perr != nil {
				return &validateReport{Path: relPath, Error: fmt.Sprintf("contract %s: prior version %q at base %s is not valid semver: %v", id, priorProbe.Version, base, perr)}, false
			}
			newSemver, nerr := contractParseSemver(probe.Version)
			if nerr != nil {
				return &validateReport{Path: relPath, Error: fmt.Sprintf("contract %s: version %q is not valid semver: %v", id, probe.Version, nerr)}, false
			}

			result := validate.CheckComputedCompatibility(validate.CompatInput{
				DeclaredBump:  contractInferBumpKind(priorSemver, newSemver),
				PriorVersion:  priorProbe.Version,
				NewVersion:    probe.Version,
				NewSchemas:    schemasNew,
				PriorFixtures: contractFixturesValidOnly(priorTree),
			})
			if !result.Computed {
				// Computed==false with no Violation (major bump, or a
				// prior version that published no fixtures) is an honest
				// non-answer, not a pass — printed explicitly so a clean
				// exit-0 CI log still says WHY nothing was checked.
				_, _ = fmt.Fprintf(stderr, "validate --ci: contract %s: %s\n", id, result.Reason)
			}
			if result.Violation != nil {
				violations = append(violations, *result.Violation)
			}
		}
	}

	valid := true
	for _, v := range violations {
		if v.Severity != validate.SeverityWarning {
			valid = false
			break
		}
	}
	if violations == nil {
		violations = []validate.Violation{}
	}
	res := validate.Result{Valid: valid, ArtifactID: id, InvocationPoint: validate.V2, Violations: violations}
	return &validateReport{Path: relPath, Result: &res}, valid
}

// spaceLevelArtifactDir is the one §4.2 directory that holds artifacts
// belonging to no single system's section (decisions are multi-party).
const spaceLevelArtifactDir = "decisions"

// isSpaceLevelArtifact reports whether a space-relative path is an
// artifact filed at the space level rather than inside a participant
// section — today exactly decisions/*.md (§4.2).
func isSpaceLevelArtifact(p string) bool {
	return strings.HasPrefix(p, spaceLevelArtifactDir+"/") && strings.HasSuffix(p, ".md")
}

// spaceLevelOwnSystem derives the OwnSystem a space-level artifact is
// validated under: its own id's <system> token (the drafting system, §3.3),
// read from the filename stem. A stem that is not a parseable id falls back
// to the directory name — a value no participant can equal, so the artifact
// fails closed instead of being waved through (the stem itself is separately
// reported by CC-003 via the placement guard).
func spaceLevelOwnSystem(relPath string) string {
	stem := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
	if id, err := artifact.ParseID(stem); err == nil {
		return id.System
	}
	return spaceLevelArtifactDir
}

// walkArtifacts collects every validatable file in the checkout
// (v3-full-repo scope): `*.md` under a participant section, each section's
// `consumes.yaml`, and the space-level decisions/ artifacts, which belong
// to no section. The bare `.git` object store is skipped (it holds no
// artifacts and grows with history).
func walkArtifacts(root string, manifest space.Manifest) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") && !isConsumesRegistry(path) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if _, ok := systemForPath(manifest, rel); ok || isSpaceLevelArtifact(rel) {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// gitDiffNameOnly is the real gitChangedFilesFunc: it shells out to
// `git -C <root> diff --name-only --diff-filter=ACMR <base>...HEAD`. The
// three-dot range is the PR-diff semantic (changes on HEAD since the merge
// base with <base>); --diff-filter=ACMR excludes deletions (a deleted
// path is gone on disk and must not red the gate on an ENOENT read). A git
// failure is returned loudly, with the captured stderr.
func gitDiffNameOnly(ctx context.Context, root, base string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "diff", "--name-only", "--diff-filter=ACMR", base+"...HEAD")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git diff --name-only %s...HEAD failed: %w", base, err)
		}
		return nil, fmt.Errorf("git diff --name-only %s...HEAD failed: %w: %s", base, err, msg)
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// linkageProbe decodes the two envelope fields whose value, once merged, is
// the record itself: which conversation an artifact belongs to and which
// artifact it answers.
type linkageProbe struct {
	Thread string `yaml:"thread"`
	Parent string `yaml:"parent"`
}

// validateCILinkageImmutable is REF-011 (spec 46 §T2): `thread` and `parent`
// may not CHANGE on an artifact that already exists at the merge base.
//
// Why this is a separate gate rather than another envelope rule: every other
// check in this file asks "is this document well-formed", which a single
// version of the document can answer. This one asks "is this document the same
// document", which only a comparison can. The anti-fork code REF-009 keys on
// `parent`, so it sees a reply that joins the wrong conversation — it cannot
// see an artifact silently RE-PARENTED in a later PR, because after the edit
// the document is perfectly consistent with itself. In a permanent
// cross-company record whose whole pitch is that history does not move, an
// artifact quietly changing which negotiation it belongs to is the failure
// that matters most and is hardest to notice by reading.
//
// It reuses the mechanism `validate --ci`'s contract-compatibility half already
// runs on every PR (`git show <base>:<path>`, contractGitBounded) rather than
// introducing a second way to read the base tree.
//
// Returns (nil, true) when there is nothing to compare: no base (full-repo
// mode), the file is absent at base (created in this PR), or either side is
// unparseable — an unparseable document is already the schema class's problem
// and reporting it twice would just be noise.
func validateCILinkageImmutable(ctx context.Context, root, base, relPath string) (*validateReport, bool) {
	if base == "" {
		return nil, true
	}
	priorRaw, err := contractGitBounded(ctx, root, maxMirrorEventBytes, "show", base+":"+relPath)
	if err != nil {
		// Not at base = new in this PR. Nothing is being changed.
		return nil, true
	}
	headRaw, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		return nil, true
	}
	prior, ok1 := decodeLinkage(priorRaw)
	head, ok2 := decodeLinkage(headRaw)
	if !ok1 || !ok2 {
		return nil, true
	}

	var violations []validate.Violation
	if prior.Thread != head.Thread {
		violations = append(violations, validate.Violation{
			Code:     "REF-011",
			Class:    validate.ClassReferential,
			Path:     "thread",
			Message:  fmt.Sprintf("thread changed after merge: %q at base, %q here — a committed artifact may not move to another conversation", prior.Thread, head.Thread),
			Severity: validate.SeverityReject,
		})
	}
	if prior.Parent != head.Parent {
		violations = append(violations, validate.Violation{
			Code:     "REF-011",
			Class:    validate.ClassReferential,
			Path:     "parent",
			Message:  fmt.Sprintf("parent changed after merge: %q at base, %q here — a committed artifact may not be re-parented", prior.Parent, head.Parent),
			Severity: validate.SeverityReject,
		})
	}
	if len(violations) == 0 {
		return nil, true
	}
	return &validateReport{Path: relPath, Result: &validate.Result{
		Valid:           false,
		InvocationPoint: validate.V2,
		Violations:      violations,
	}}, false
}

// decodeLinkage returns the two linkage fields and whether the document could
// be decoded at all. A bool rather than an error because every caller's
// response to "unparseable" is the same — leave it to the schema class — and a
// returned error here would be a nilerr trap.
func decodeLinkage(raw []byte) (linkageProbe, bool) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return linkageProbe{}, false
	}
	var probe linkageProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return linkageProbe{}, false
	}
	return probe, true
}
