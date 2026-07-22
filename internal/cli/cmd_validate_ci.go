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
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
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

// ciAuthzViolation is a diff-authz finding — deliberately NOT a
// validate.Violation: diff-authz is not one of the engine's registry
// classes ({SCH,REF,LFC,POL}), so it carries no registry Code (fabricating
// one would corrupt the registry-code invariant). CC-097 (unmapped author)
// is reported in the corner-case CCRef field instead.
type ciAuthzViolation struct {
	Path    string `json:"path,omitempty"`
	Author  string `json:"author"`
	CCRef   string `json:"cc_ref,omitempty"`
	Message string `json:"message"`
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

	// Filter to *.md files located under a participant section — skip
	// space.yaml, CODEOWNERS, .github, and any non-artifact file.
	var artifacts []string
	for _, p := range changed {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		if _, ok := systemForPath(manifest, p); ok {
			artifacts = append(artifacts, p)
		}
	}

	// One resolver over the whole checkout (its artifact index is built
	// once, lazily) — shared across every artifact's V2 run.
	resolver := NewMirrorResolver(root, manifest)

	report := ciReport{Mode: mode, Valid: true, Artifacts: []validateReport{}}
	for _, relPath := range artifacts {
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

	// Diff-authz applies only to v3-pr (a full-repo scan has no single PR
	// author; GITHUB_ACTOR ⊆ every section can never hold across systems)
	// and only when there is at least one changed artifact to authorize —
	// an empty changed set is a clean exit 0, never an unmapped-author red.
	if mode == "v3-pr" && len(artifacts) > 0 {
		if authz := diffAuthz(manifest, author, artifacts); len(authz) > 0 {
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
// LIFECYCLE SCOPE: candidate events are deliberately empty. In a PR
// checkout the changed event file is already committed on the branch, so
// LegalityAdapter.committedEvents (which reads <root>/<system>/events/**)
// would fold that same event into history AND receive it again as a
// candidate — double-counting it and reding the legal entry transition as
// illegal. Reconstructing the base-ref state or excluding the candidate is
// out of this slice (the adapter and engine are read-only reuse). Empty
// events means checkLifecycle is a no-op: lifecycle is NOT exercised here
// (not faked-pass). Schema + referential + authz + policy(secret) ARE.
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
	ownSystem, _ := systemForPath(manifest, relPath)
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

// diffAuthz enforces that every changed artifact path is under the PR
// author's section (§8 diff-authz). An author not mapped to any system is
// a CC-097 violation; a changed path outside the author's section is a
// diff-authz violation.
func diffAuthz(manifest space.Manifest, author string, artifacts []string) []ciAuthzViolation {
	authorSystem, ok := manifest.SystemForLogin(author)
	if !ok {
		return []ciAuthzViolation{{
			Author:  author,
			CCRef:   "CC-097",
			Message: fmt.Sprintf("PR author %q is not mapped to any system in space.yaml", author),
		}}
	}
	var out []ciAuthzViolation
	for _, relPath := range artifacts {
		sys, _ := systemForPath(manifest, relPath)
		if sys != authorSystem {
			out = append(out, ciAuthzViolation{
				Path:    relPath,
				Author:  author,
				Message: fmt.Sprintf("changed path is outside the author's section (author system %q, path in system %q)", authorSystem, sys),
			})
		}
	}
	return out
}

// systemForPath resolves a space-relative path to the system whose
// participant section contains it, per the manifest's `section` entries
// (e.g. "axon/"). Returns ("", false) for a path under no section.
func systemForPath(manifest space.Manifest, p string) (string, bool) {
	for _, part := range manifest.Participants {
		sec := strings.TrimSuffix(part.Section, "/")
		if sec == "" {
			continue
		}
		if p == sec || strings.HasPrefix(p, sec+"/") {
			return part.System, true
		}
	}
	return "", false
}

// walkArtifacts collects every `*.md` file under a participant section in
// the checkout (v3-full-repo scope). The bare `.git` object store is
// skipped (it holds no artifacts and grows with history).
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
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if _, ok := systemForPath(manifest, rel); ok {
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
