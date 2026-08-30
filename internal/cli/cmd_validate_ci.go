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
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/internal/workcheckpoint"
	"github.com/ydnikolaev/a2ahub/schemas"
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
		// P4 (answers-that-hold-2026-08, spec 04 §T1/AC-1): the one site this
		// phase migrates through NewRefusal. Before this, "cannot read
		// space.yaml: <os error>" read exactly like "this command does not
		// exist" (spec 04 US-1) — nothing told a participant-repo caller
		// where it stood or what to run instead. The three parts below are
		// the precondition, the caller's own state, and the synced mirror
		// path for EVERY connected space (spec 04 §6: "a repo with two
		// connected spaces must name both").
		refusal, rerr := NewRefusal(
			fmt.Sprintf("validate --ci: read this checkout's own space manifest at %s", filepath.Join(root, "space.yaml")),
			fmt.Sprintf("this cwd (%s) is a participant project, not a space checkout — %v", root, err),
			validateCINoManifestNextStep(participantConnectedSpaceMirrors(root)),
		)
		if rerr != nil {
			// Unreachable in practice: validateCINoManifestNextStep never
			// returns an empty string (see its own doc comment). Kept as a
			// structural safety net, not a trusted-forever assumption — and
			// deliberately printing a fixed message rather than rerr itself,
			// so this branch is never mistaken for a new raw err-to-stderr
			// site by scripts/check-refusal-ratchet.sh's own sink scan.
			_, _ = fmt.Fprintln(stdio.Stderr, "validate --ci: internal error building the missing-manifest refusal (empty next step) — this is a bug in cmd_validate_ci.go, file one")
			return 1
		}
		_, _ = fmt.Fprintln(stdio.Stderr, refusal)
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
	// fixtures/invalid/** subtree (space.ContractForPath). Deduped to ONE
	// entry per descriptor directory (contractDescPaths, seenContracts): a
	// PR changing five of a contract's schema files must produce ONE
	// compat verdict below, not five (AC-970.2's acceptance).
	var artifacts []string
	var authzPaths []string
	var consumes []string
	var events []string
	var contractIDs []string
	var undeclared []space.Carried
	contractDescPaths := map[string]string{}
	seenContracts := map[string]bool{}
	descriptors := newCIDescriptorReader(root)
	for _, p := range changed {
		if id, descPath, ok := space.ContractForPath(p); ok && !seenContracts[id] {
			seenContracts[id] = true
			contractIDs = append(contractIDs, id)
			contractDescPaths[id] = descPath
		}
		if _, ok := systemForPath(manifest, p); ok {
			authzPaths = append(authzPaths, p)
			// A data package's own payload (space.DataPackageForPath's
			// "<system>/data/<DP-id>/..." grammar) is sealed by
			// manifest.json's per-entry sha256 and was never an envelope
			// draft, a consumes.yaml registry, or an event document — so it
			// is excluded from EVERY classification below, not merely the
			// ".md" one (spec 04 §11, amendment 2026-08-09, AC8). A blob's
			// own payload (space.BlobForPath's "<system>/blobs/<BL-id>/..."
			// grammar) is the same carve-out for the same reason (P10 spec
			// 10 wave B): sealed by its own digest sidecar, never an
			// envelope draft either. authzPaths still carries both: each is
			// a sectioned write and diff-authz must still see it, even
			// though nothing here validates its content.
			if isDataPackagePayloadPath(p) || isBlobPayloadPath(p) {
				continue
			}
			// P9 (spec 09, fb-20260820-d1e370): a contract's own companion
			// files are classified by the descriptor's `artifacts:`
			// inventory — the answer space.ContractForPath already computed
			// THREE LINES ABOVE, at :182, and which this switch used to
			// discard in favour of "does it end in .md". That discard is the
			// incident: a declared `artifacts/CHANGELOG.md` was refused with
			// POL-002 by the very gate `a2a submit` exists to pre-empt.
			//
			// The descriptor is read from the CHECKOUT, never from `changed`
			// — a PR touching only the companion leaves contract.md
			// unchanged, and looking for it in the diff would classify the
			// incident's own file as unclassifiable and exempt it by
			// accident rather than on purpose.
			//
			// This one loop serves BOTH of spec 09 §T1's discovery sites:
			// v3-full-repo's walkArtifacts output flows through here too
			// (see :122), so the classifier is consulted once for both
			// rather than twice in two shapes (§11).
			carried, unmeasuredDescriptor := descriptors.classify(p)
			if unmeasuredDescriptor {
				// computed-not-listed-2026-08 P6 AC-6/US-4: an unreadable
				// descriptor (EACCES, EISDIR, a bad symlink, an I/O error —
				// never a genuinely absent one, which stays the cheap
				// CarriedUnclassifiable path below) is SeverityUnmeasured
				// (D9's own third state, reused rather than a second name
				// minted for it) — printed as an honest non-answer, the
				// same idiom this file already uses at :1256/:1287
				// ("computed nothing, say why"), never silently treated as
				// a clean companion. It must never flip report.Valid to
				// false (validate/result.go:70): a rule that could not be
				// checked is not a rule that failed.
				_, _ = fmt.Fprintf(stdio.Stderr,
					"validate --ci: contract descriptor for %s could not be read — unmeasured, not classified as clean\n", p)
				continue
			}
			switch carried.Class {
			case space.CarriedDeclaredCompanion, space.CarriedUnclassifiable:
				// Judged by the carried-set rules, never by the artifact
				// frontmatter policy. `unclassifiable` means the descriptor
				// itself is absent or unparseable: its OWN violation is the
				// verdict, and two red lines about one broken descriptor is
				// noise whose fix is one edit.
				continue
			case space.CarriedUndeclaredCompanion:
				// A proposed write must never land a file nothing
				// classifies (US-3). Already-merged content is exempt:
				// v3-full-repo judges an immutable publication, and a new
				// refusal there would demand edits to it (US-7, ADR-011
				// D3/D4). POL-013's own `mode_scope: both` is unchanged —
				// which files a caller feeds a rule is a caller decision.
				if mode == "v3-pr" {
					undeclared = append(undeclared, carried)
				}
				continue
			case space.CarriedArtifact, space.CarriedNotAContractPath:
				// An envelope draft (the descriptor included) or a path no
				// contract owns — the classification below is unchanged.
			}
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
	// P9 S-3 at the merge gate: a proposed write carrying a file under a
	// contract's own directory that the descriptor's inventory does not
	// declare. Reported as its own line, with the code the registry already
	// assigns it — nothing is minted here, and the message is
	// internal/space's, so submit and `--ci` name the same fix in the same
	// words.
	for _, carried := range undeclared {
		result := validate.Result{
			Valid:           false,
			InvocationPoint: validate.V3,
			ArtifactID:      carried.ContractID,
			Violations:      carriedFindingViolations([]space.CarriedFinding{space.UndeclaredCompanionFinding(carried)}),
		}
		report.Artifacts = append(report.Artifacts, validateReport{Path: carried.Path, Result: &result})
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
		rep, ok := validateCIArtifact(ctx, engine, root, relPath, mode, base, events, manifest, resolver)
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
	// supersedeLinks collects CC-024/CC-025's own graph input — every
	// supersede event's {successor, predecessor} pair — but ONLY in
	// v3-full-repo mode. See the CheckSupersessionGraph call below (after
	// this loop) for why v3-pr must never build or check this graph: a PR
	// diff carries a partial event set, and a partial supersession graph
	// would report a fork or cycle that does not exist repo-wide.
	var supersedeLinks []validate.SupersedeLink
	for _, relPath := range events {
		rep, ok := validateCIEvent(engine, resolver, root, relPath, manifest.MinBinaryVersion, mode)
		if rep == nil {
			continue // deleted in this PR
		}
		report.Artifacts = append(report.Artifacts, *rep)
		if !ok {
			report.Valid = false
			continue
		}
		if mode == "v3-full-repo" {
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
			// Only a `supersede` transition carries a supersession link.
			// event.Event is the event's OWN ULID (schemas/event/v1's
			// `event` field), not the verb — the verb is `transition`
			// (fold.TSupersede) — so matching on Event would match
			// nothing and silently produce an empty graph forever.
			if event.Transition == fold.TSupersede {
				for _, ref := range event.Refs {
					successor := supersedeBareID(ref.Ref)
					if successor == "" {
						continue
					}
					supersedeLinks = append(supersedeLinks, validate.SupersedeLink{
						Successor:   successor,
						Predecessor: supersedeBareID(event.Subject),
					})
				}
			}
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
	// CC-024/CC-025 (spec 03 §3.8, plan 12): the supersession graph check
	// runs ONCE, over the whole repo's supersede-event links, and ONLY in
	// v3-full-repo. Deliberately not run in v3-pr: a PR's changed-event set
	// is a partial slice of the repo's supersede events, so evaluating the
	// graph from it could report a fork or a cycle neither of which exists
	// once the whole graph is considered — worse than not checking at all,
	// because it would refuse a legal PR. The full-repo scan is exactly
	// this check's own scope, same as validateCIManifest's own
	// mode=="v3-full-repo" gate a few lines below.
	if mode == "v3-full-repo" {
		if violations := validate.CheckSupersessionGraph(supersedeLinks); len(violations) > 0 {
			report.Artifacts = append(report.Artifacts, validateReport{
				Path: "<supersession-graph>",
				Result: &validate.Result{
					Valid:           false,
					InvocationPoint: validate.V3,
					Violations:      violations,
				},
			})
			report.Valid = false
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
			rep, ok := validateCIContract(ctx, root, base, id, contractDescPaths[id], manifest.MinBinaryVersion, stdio.Stderr)
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

// ---------------------------------------------------------------------
// P4 (answers-that-hold-2026-08, spec 04): the missing-manifest refusal's
// own "what to run instead" half — every connected space's synced mirror,
// read from THIS checkout's own .a2a/config.yaml (space.ProjectConfig), the
// same file `a2a doctor`/`a2a sync` already read (cmd_doctor.go's own
// projectConfigPath field). Deliberately self-contained: it reads the
// FIXED, documented convention paths (space.ProjectConfig at
// "<root>/.a2a/config.yaml", space.MachineConfig at
// "~/.config/a2a/config.yaml" — cmd/a2a/wire.go's own `paths` struct
// literal, not a second convention) rather than adding injected path
// fields to ValidateCommand, which would require wiring changes in
// cmd_submit.go and cmd/a2a/wire.go this phase's allowlist does not grant
// (reported as a deviation: AC-2's `--space <id>` CLI flag needs exactly
// that wiring and is UNMET by this phase for the same reason).
// ---------------------------------------------------------------------

// connectedSpaceMirror names one connected space's own synced mirror
// checkout, resolved the same way space.ResolveMirrorLocation already
// resolves it for `a2a sync`/`a2a doctor`.
type connectedSpaceMirror struct {
	ID        string
	MirrorDir string
}

// participantConnectedSpaceMirrors reads root's own .a2a/config.yaml (a
// participant PROJECT's connected-space list — never a space checkout's
// own concept) and resolves every entry's synced mirror directory. Returns
// nil when the file is absent, unparseable, or names no space — all three
// mean "nothing to redirect to", handled by validateCINoManifestNextStep's
// own fallback text, never a second error path here.
func participantConnectedSpaceMirrors(root string) []connectedSpaceMirror {
	cfg, err := space.LoadProjectConfig(filepath.Join(root, ".a2a", "config.yaml"))
	if err != nil || len(cfg.Spaces) == 0 {
		return nil
	}
	machine := loadMachineConfigBestEffort()
	mirrors := make([]connectedSpaceMirror, 0, len(cfg.Spaces))
	for _, ref := range cfg.Spaces {
		mirrors = append(mirrors, connectedSpaceMirror{
			ID:        ref.ID,
			MirrorDir: space.ResolveMirrorLocation(root, ref, machine),
		})
	}
	return mirrors
}

// loadMachineConfigBestEffort reads ~/.config/a2a/config.yaml — the exact
// path cmd/a2a/wire.go's own `paths{}` literal builds
// (filepath.Join(home, ".config", "a2a", "config.yaml")), not a second
// convention. Best-effort: a machine with no machine config (or one this
// process cannot read) still gets a correct project-relative mirror path
// from space.ResolveMirrorLocation's own zero-value fallback — the same
// fallback a real connect/sync without a configured mirror_root already
// relies on.
func loadMachineConfigBestEffort() space.MachineConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return space.MachineConfig{}
	}
	machine, err := space.LoadMachineConfig(filepath.Join(home, ".config", "a2a", "config.yaml"))
	if err != nil {
		return space.MachineConfig{}
	}
	return machine
}

// validateCINoManifestNextStep is the missing-manifest refusal's third part.
// It NEVER returns an empty string — NewRefusal's own construction check
// depends on that — so the zero-connected-spaces case still names a
// concrete next action (connect one) rather than a bare "no spaces found".
func validateCINoManifestNextStep(mirrors []connectedSpaceMirror) string {
	if len(mirrors) == 0 {
		return "this checkout has no connected space to redirect to (no .a2a/config.yaml, or it names none) — " +
			"run `a2a init <space-repo-url>` to connect one, then re-run `a2a validate --ci` inside that space's own synced mirror"
	}
	parts := make([]string, 0, len(mirrors))
	for _, m := range mirrors {
		parts = append(parts, fmt.Sprintf("space %q: run `a2a validate --ci` inside %s", m.ID, m.MirrorDir))
	}
	return "run `a2a validate --ci` inside the connected space's own synced mirror instead of this participant checkout: " + strings.Join(parts, "; ")
}

// resolveConnectedSpace resolves id against refs (a participant project's
// own .a2a/config.yaml Spaces list — space.ProjectConfig.Spaces). An
// unknown id refuses NAMING THE IDS THAT ARE CONNECTED (spec 04 AC-3),
// never a bare "not found": an agent choosing between spaces needs the
// legal set in the SAME message that told it its choice was wrong.
func resolveConnectedSpace(id string, refs []space.Ref) (space.Ref, error) {
	var known []string
	for _, ref := range refs {
		if ref.ID == id {
			return ref, nil
		}
		known = append(known, ref.ID)
	}
	if len(known) == 0 {
		return space.Ref{}, fmt.Errorf("--space %q: this checkout has no connected space at all (empty .a2a/config.yaml Spaces) — run `a2a init <space-repo-url>` first", id)
	}
	return space.Ref{}, fmt.Errorf("--space %q: not a connected space here — the connected space(s) are: %s", id, strings.Join(known, ", "))
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
func validateCIArtifact(ctx context.Context, engine *validate.Engine, root, relPath, mode, base string, eventPaths []string, manifest space.Manifest, resolver validate.Resolver) (*validateReport, bool) {
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
	// ADR-011 decision 3 (fb-20260812-e6d189): REF-017 stays a reject in
	// v3-pr (the write gate, where refusing still prevents a merge) but
	// must return NO VERDICT in v3-full-repo (the post-merge audit),
	// because a work_request merged before REF-017 existed has no
	// in-protocol repair — committed history is immutable and no verb
	// retracts a closed exchange. POL-017 is DELIBERATELY NOT suppressed:
	// it is a warning (never flips Valid), so the full-repo audit still
	// surfaces the smell without refusing anything immutable. See
	// internal/validate/possession.go's own doc comment for the other
	// half of this cross-reference.
	//
	// This MUST run before InvocationPoint is stamped and before the
	// result.Valid check that gates the contextual work-checkpoint call
	// below: a suppressed REF-017 must not go on suppressing that
	// contextual check too — without recomputing Valid here first, a
	// REF-017-only violation set would leave Valid false and the
	// contextual check would never run, silently hiding an unrelated
	// contextual defect behind a verdict this mode no longer stands
	// behind.
	if mode == "v3-full-repo" {
		// ADR-011 D3, DERIVED rather than restated: every code whose
		// registry row declares `mode_scope: v3-pr` is suppressed here.
		// A rule that can only be satisfied by editing an immutable
		// artifact must not judge immutable history, and WHICH codes those
		// are is a fact the registry already carries.
		//
		// This replaced three hardcoded SuppressingCode literals. Spec 01's
		// AC5 asked for exactly this after the FIRST one; by the end of the
		// epic there were three, which is the copy-paste that AC predicted
		// and the whole argument for a declaration over a literal.
		for _, code := range validateCIWriteGateOnlyCodes() {
			result = result.SuppressingCode(code)
		}
	}
	result.InvocationPoint = validate.V3
	// V3 preserves V2's fail-closed order: the existing generic submit policy
	// owns the outer envelope first; contextual work rules only see a generic-
	// valid candidate. This avoids replacing stable generic violation codes with
	// parser/history errors from a malformed work-shaped document.
	if result.Valid {
		if contextual, applies, contextualErr := validateCIWorkCheckpoint(ctx, engine, root, relPath, mode, base, eventPaths, ownSystem, raw); contextualErr != nil {
			return &validateReport{Path: relPath, Error: contextualErr.Error()}, false
		} else if applies && !contextual.Valid {
			result.Violations = append(result.Violations, contextual.Violations...)
			result.Valid = false
		}
	}
	r := result
	return &validateReport{Path: relPath, Result: &r}, result.Valid
}

type ciWorkEnvelope struct {
	ID       string `yaml:"id"`
	Type     string `yaml:"type"`
	Space    string `yaml:"space"`
	From     string `yaml:"from"`
	Category string `yaml:"category"`
	Actor    struct {
		Kind    string `yaml:"kind"`
		Name    string `yaml:"name"`
		Model   string `yaml:"model"`
		Session string `yaml:"session"`
	} `yaml:"actor"`
	Work *struct{} `yaml:"work"`
}

type ciWorkEvent struct {
	Event      string `yaml:"event"`
	Space      string `yaml:"space"`
	Subject    string `yaml:"subject"`
	Transition string `yaml:"transition"`
	State      string `yaml:"state"`
	Actor      struct {
		Kind    string `yaml:"kind"`
		Name    string `yaml:"name"`
		System  string `yaml:"system"`
		Model   string `yaml:"model"`
		Session string `yaml:"session"`
	} `yaml:"actor"`
}

func validateCIWorkCheckpoint(ctx context.Context, engine *validate.Engine, root, relPath, mode, base string, eventPaths []string, ownSystem string, raw []byte) (validate.Result, bool, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return validate.Result{}, false, nil
	}
	var envelope ciWorkEnvelope
	if err := yaml.Unmarshal(fm.YAML, &envelope); err != nil || envelope.Work == nil || envelope.Type != "announcement" || envelope.Category != "status" {
		return validate.Result{}, false, nil
	}
	var eventRaw []byte
	var eventPath, eventID string
	var pairedEvent ciWorkEvent
	for _, candidatePath := range eventPaths {
		candidateRaw, readErr := readBoundedFile(filepath.Join(root, candidatePath), maxMirrorEventBytes)
		if readErr != nil {
			continue
		}
		var candidate ciWorkEvent
		if yaml.Unmarshal(candidateRaw, &candidate) == nil && candidate.Subject == envelope.ID &&
			candidate.Space == envelope.Space && candidate.Transition == "publish" && candidate.State == "published" {
			if eventRaw != nil {
				return validate.Result{}, true, fmt.Errorf("work checkpoint %s has multiple publish/published candidate events", envelope.ID)
			}
			eventRaw, eventPath, eventID, pairedEvent = candidateRaw, candidatePath, candidate.Event, candidate
		}
	}
	if eventRaw == nil {
		return validate.Result{}, true, fmt.Errorf("work checkpoint %s has no exact publish/published candidate event", envelope.ID)
	}
	if pairedEvent.Actor.System != envelope.From || pairedEvent.Actor.Kind != envelope.Actor.Kind || pairedEvent.Actor.Name != envelope.Actor.Name ||
		pairedEvent.Actor.Model != envelope.Actor.Model || pairedEvent.Actor.Session != envelope.Actor.Session {
		return validate.Result{}, true, fmt.Errorf("work checkpoint %s event actor does not match the checkpoint actor", envelope.ID)
	}
	artifactCommit, err := workCheckpointIntroductionCommit(ctx, root, relPath)
	if err != nil {
		return validate.Result{}, true, err
	}
	eventCommit, err := workCheckpointIntroductionCommit(ctx, root, eventPath)
	if err != nil {
		return validate.Result{}, true, err
	}
	if artifactCommit != eventCommit {
		return validate.Result{}, true, fmt.Errorf("work checkpoint %s artifact and event were not introduced in one first-parent commit", envelope.ID)
	}
	authorityRef := base
	if mode == "v3-full-repo" {
		// A full-repo audit evaluates each immutable record at its own atomic
		// introduction point. Reusing a later touch would let future history
		// masquerade as the candidate's prior state.
		authorityRef = artifactCommit
	}
	facts, err := space.NewWorkCheckpointRepositoryFactsAt(root, ownSystem, authorityRef, "HEAD", nil)
	if err != nil {
		return validate.Result{}, true, err
	}
	validator, err := workcheckpoint.NewContextual(engine, facts, ownSystem)
	if err != nil {
		return validate.Result{}, true, err
	}
	candidate := space.WorkCheckpointValidation{
		InvocationPoint: string(validate.V3), Space: envelope.Space, ArtifactID: envelope.ID, EventID: eventID,
		ArtifactPath: relPath, EventPath: eventPath, Artifact: raw, Event: eventRaw,
	}
	if err := validator.ValidateWorkCheckpoint(ctx, candidate); err != nil {
		var violations *workcheckpoint.ViolationError
		if errors.As(err, &violations) {
			return validate.Result{InvocationPoint: validate.V3, ArtifactID: envelope.ID, Valid: false, Violations: violations.Violations}, true, nil
		}
		return validate.Result{}, true, err
	}
	return validate.Result{InvocationPoint: validate.V3, ArtifactID: envelope.ID, Valid: true, Violations: []validate.Violation{}}, true, nil
}

func workCheckpointIntroductionCommit(ctx context.Context, root, relPath string) (string, error) {
	raw, err := contractGitBounded(ctx, root, 128, "log", "--first-parent", "--diff-filter=A", "-1", "--format=%H", "HEAD", "--", relPath)
	if err != nil {
		return "", fmt.Errorf("resolve work checkpoint introduction for %s: %w", relPath, err)
	}
	commit := strings.TrimSpace(string(raw))
	if commit == "" || strings.ContainsAny(commit, " \t\r\n") {
		return "", fmt.Errorf("resolve work checkpoint introduction for %s: no exact first-parent commit", relPath)
	}
	return commit, nil
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
		if part.Status != fold.MembershipMember {
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

// supersedeBareID strips a ref's optional `@version` and/or `#digest` pin
// (envelope/v1 §5.7's own ref grammar; internal/validate's parseRef is the
// package-private twin of this logic) down to the bare artifact id.
// validate.CheckSupersessionGraph compares SupersedeLink ids by exact
// string equality, so a supersede event's `subject` (always bare in
// practice — see cmd_lifecycle.go's supersede row, which writes `id`
// straight from the batch's own positional argument) and its `refs[].ref`
// (free text, since supersede's own --refs flag applies no grammar of its
// own beyond RequireRefs) must both be normalized here before either one
// becomes a graph node — an unpinned subject and a pinned ref naming the
// same artifact must land on the same node, or a real fork/cycle silently
// splits into two disconnected, clean-looking edges.
func supersedeBareID(ref string) string {
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		ref = ref[:i]
	}
	return ref
}

// validateCIEvent validates one changed event: its event/v1 schema, and — once
// the space's floor has reached the release that introduced the field — that it
// names the producer that wrote it. The verdict comes from the engine
// (validate.ValidateEventWithContext); this file adds ZERO validation rules of
// its own, the same discipline the compat and publishability checks follow.
//
// The resolver is passed rather than omitted, and that is the whole reason this
// signature changed. REF-019 (an out-of-range `verdicts[].index` on a verify or
// close event) lives in the engine and can only fire when a Resolver is offered:
// without one it degrades to "cannot check", silently. Wave 25B wired the rule
// and reached no production caller, which is this epic's own recurring defect —
// a rule that is true and inert. `validate --ci` is the merge-time path a space
// actually pins, exactly as P6 already recorded for REF-018, so it is the caller
// that has to offer it.
//
// mode carries defects-fix-2026-08 P4's own D3 scoping (ADR-011): REF-023
// (a verify/close event whose verdicts[] does not name every criterion the
// parent declares) is suppressed at v3-full-repo, the same seat
// validateCIArtifact already uses for REF-017/POL-022 — a merged verify/close
// event is immutable and no verb rewrites its verdicts[], so a post-merge
// audit against it could only punish, never repair. getvisa's own two
// `verdicts: []` closes (over 7 and 8 declared criteria) are exactly that
// population.
func validateCIEvent(engine *validate.Engine, resolver validate.Resolver, root, relPath, spaceFloor, mode string) (*validateReport, bool) {
	raw, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true // deleted in this PR
		}
		return &validateReport{Path: relPath, Error: err.Error()}, false
	}
	result, err := engine.ValidateEventWithContext(raw, spaceFloor, validate.EventContext{Resolver: resolver})
	if err != nil {
		return &validateReport{Path: relPath, Error: err.Error()}, false
	}
	if mode == "v3-full-repo" {
		for _, code := range validateCIWriteGateOnlyCodes() {
			result = result.SuppressingCode(code)
		}
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

// isDataPackagePayloadPath reports whether p sits inside one `a2a data pack`
// package's own directory (space.DataPackageForPath's own
// "<system>/data/<DP-id>/..." grammar) — spec 04 §11's 2026-08-09 AC8
// amendment. Both artifact-discovery sites in this file (the changed-file
// loop above and walkArtifacts below) ask it exactly as they already ask
// space.ContractForPath for a contract's own schema/fixtures/companion
// subtree: a package's payload — including the packed README.md the real
// incident failed on — is sealed by manifest.json's per-entry sha256
// (datapackage.BuildEntrySet) and was never an envelope draft, so POL-002's
// frontmatter policy does not apply to it. This is deliberately narrower
// than "skip this whole system section": a genuinely malformed artifact
// filed anywhere else under the same system still reaches artifact
// discovery and still reds.
func isDataPackagePayloadPath(p string) bool {
	_, _, ok := space.DataPackageForPath(p)
	return ok
}

// isBlobPayloadPath is isDataPackagePayloadPath's own sibling for a blob's
// own directory (space.BlobForPath's "<system>/blobs/<BL-id>/..." grammar) —
// P10 (agent-exchange-2026-08) spec 10 wave A's own handoff, closed here:
// `a2a attach` (wave B) now lands its payload bytes in the space the same
// structural way `a2a data pack`/`deliver` already do, and a blob payload's
// own frontmatter-bearing .md file must not be walked as an envelope draft
// any more than a data package's own README.md is — the exact defect spec
// 04 §11's AC8 amendment already fixed once for `DP-` packages. Both
// artifact-discovery sites in this file (the changed-file loop above and
// walkArtifacts below) ask it exactly beside isDataPackagePayloadPath, and
// it is deliberately narrower than "skip this whole system section": a
// genuinely malformed artifact filed anywhere else under the same system
// still reaches artifact discovery and still reds.
func isBlobPayloadPath(p string) bool {
	_, _, ok := space.BlobForPath(p)
	return ok
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
func validateCIContract(ctx context.Context, root, base, id, descriptorPath, spaceFloor string, stderr io.Writer) (*validateReport, bool) {
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
	// The descriptor's SHAPE is checked first and from the space's own floor:
	// a contract whose shape the publication planner will refuse can never be
	// published no matter what its schemas and fixtures say, so saying that
	// here — before the merge — is the whole point (see
	// validate.CheckContractDescriptorShape for the report that added it).
	if v := validate.CheckContractDescriptorShape(validate.DescriptorShapeInput{
		SpaceMinBinaryVersion: spaceFloor,
		ContractID:            id,
		DescriptorSchema:      probe.Schema,
		DeclaresArtifacts:     probe.Artifacts != nil,
		SchemaFormat:          probe.SchemaFormat,
	}); v != nil {
		violations = append(violations, *v)
	}
	if v := validate.CheckContractPublishable(validate.PublishableInput{
		SchemaFormat:    probe.SchemaFormat,
		ContractID:      id,
		Schemas:         len(schemasNew),
		ValidFixtures:   len(fixturesValidNew),
		InvalidFixtures: len(fixturesInvalidNew),
		// P5 US-1's relaxation (specs/05-declared-nature.md, 2026-08-10
		// amendment): resolved from the descriptor's own `x_binding` here,
		// at the caller — validate.CheckContractPublishable reads no
		// schema and no descriptor itself.
		DeclaresNoCompatibilityClaim: probe.XBinding.declaresNoCompatibilityClaim(),
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

// ciDescriptorReader is the checkout-side half of space.ClassifyCarried's
// "caller owns the I/O" discipline: it resolves the descriptor bytes for a
// path's contract, ONCE per contract directory, from the working tree.
//
// A PR that changes five of a contract's companions reads contract.md once;
// a descriptor that is absent on disk is cached as nil, which
// space.ClassifyCarried reads as `unclassifiable` rather than as an error —
// the read failing IS the answer, and re-attempting it per path would turn
// one missing file into N stat calls.
type ciDescriptorReader struct {
	root string
	seen map[string][]byte
}

func newCIDescriptorReader(root string) *ciDescriptorReader {
	return &ciDescriptorReader{root: root, seen: map[string][]byte{}}
}

// classify is the ONE call site's-eye view of space.ClassifyCarried: it
// supplies the descriptor bytes and decides nothing itself.
//
// unmeasured reports true when the descriptor could not be read for a
// reason OTHER than genuine absence (computed-not-listed-2026-08 P6 AC-6,
// US-4): an fs.ErrNotExist keeps today's cheap, correct behaviour — a
// deleted-in-this-PR descriptor is not a failure, and space.ClassifyCarried
// already reads a nil/absent descriptor as CarriedUnclassifiable ("the
// descriptor's OWN violation is the verdict"). An EACCES, EISDIR, a bad
// symlink or an I/O error is different in kind: it says nothing about
// whether the descriptor is valid, so it is neither cached into r.seen (a
// transient failure must not silence every companion under this contract
// for the REST of the run, which an unconditional cache would do) nor
// classified as though it were a measured absence. The caller decides how
// to surface `unmeasured` — this method's own job stops at not lying about
// what it could not read.
func (r *ciDescriptorReader) classify(p string) (carried space.Carried, unmeasured bool) {
	_, descriptorPath, ok := space.ContractForPath(p)
	if !ok {
		return space.ClassifyCarried(p, nil), false
	}
	raw, cached := r.seen[descriptorPath]
	if !cached {
		read, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(descriptorPath)))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return space.Carried{Path: p, DescriptorPath: descriptorPath}, true
		}
		raw = read
		r.seen[descriptorPath] = raw
	}
	return space.ClassifyCarried(p, raw), false
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
// (v3-full-repo scope): `*.md`, event documents and `consumes.yaml` under a
// participant section, plus the space-level decisions/ artifacts, which
// belong to no section. Events are part of the walk both for their own V3
// verdict and so a committed work checkpoint can bind its exact paired
// publish event. The bare `.git` object store is skipped (it holds no
// working-tree documents and grows with history).
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
		if !strings.HasSuffix(path, ".md") && !isConsumesRegistry(path) && !isEventDocument(filepath.ToSlash(path)) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		// A data package's own payload is excluded here for the same reason
		// it is excluded from every classification in the changed-file loop
		// above, not merely the ".md" one (spec 04 §11, AC8): none of *.md,
		// consumes.yaml or an event document means what it normally means
		// once it is a package's own sealed entry. A blob's own payload
		// (P10 spec 10 wave B) is the same carve-out for the same reason.
		if isDataPackagePayloadPath(filepath.ToSlash(rel)) || isBlobPayloadPath(filepath.ToSlash(rel)) {
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

// declaresNoCompatibilityClaim reports whether x makes NO compatibility
// claim at all — the bare `none` sentinel, or the long form with
// `compatibility_status: "none"`. This is the caller-resolved fact
// validate.PublishableInput's DeclaresNoCompatibilityClaim wants: NARROWER
// than nonAdoptable, deliberately — a long form declaring
// `adoptable: false` while still naming a real compatibility_status (e.g.
// strict-semver) IS making a checkable claim, and relaxing §5.3's
// schema-plus-fixtures requirement for it would let an unverified claim
// through, widening T2's escape hatch rather than policing it (spec 05,
// 2026-08-10 amendment).
func (x *xBindingProbe) declaresNoCompatibilityClaim() bool {
	if x == nil {
		return false
	}
	return x.Sentinel || x.CompatibilityStatus == "none"
}

// validateCIWriteGateOnlyCodes is ADR-011 D3's declared set, read from the
// embedded error registry once. A code earns membership by declaring
// `mode_scope: v3-pr` on its own registry row — never by being named here.
func registryBytes() []byte {
	raw, err := schemas.FS.ReadFile("errors/v1/registry.yaml")
	if err != nil {
		return nil
	}
	return raw
}

func validateCIWriteGateOnlyCodes() []string {
	registry, err := schema.LoadRegistry(registryBytes())
	if err != nil {
		// Fail CLOSED to suppressing nothing: a registry this binary cannot
		// read is a build-time defect, and silently widening a post-merge
		// audit is safer than silently narrowing it.
		return nil
	}
	return registry.CodesScopedToWriteGate()
}
