package lane

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// Load scans root for every `lane-inputs:` declaration under D-1's four
// homes: a gate script's header (scripts/*.sh, .agents/scripts/*.sh), the
// Makefile (a block above a target with no separate backing script, e.g.
// workflow-lint), scripts/verify.sh (a block above a wrapping function or
// a bare run_phase statement), and a Go package's doc.go.
//
// Every parse or resolution failure is aggregated (errors.Join) rather
// than stopping at the first — one Load call reports every problem in the
// tree, the same discipline Reconcile and Coverage apply to Refusal.
func Load(root string) ([]Declaration, error) {
	var errs []error
	var decls []Declaration

	makePhases, makeDoc, err := corpusFromMakefile(root)
	if err != nil {
		return nil, err
	}
	scriptPhase := scriptToPhase(makeDoc, makePhases)

	verifyPath := filepath.Join(root, "scripts", "verify.sh")

	scriptDecls, scriptErrs := loadScriptHeaders(root, scriptPhase, verifyPath)
	decls = append(decls, scriptDecls...)
	errs = append(errs, scriptErrs...)

	makefileDecls, makefileErrs := loadMakefileTargetDeclarations(root, makeDoc)
	decls = append(decls, makefileDecls...)
	errs = append(errs, makefileErrs...)

	verifyDecls, verifyErrs := loadVerifyDeclarations(root, verifyPath)
	decls = append(decls, verifyDecls...)
	errs = append(errs, verifyErrs...)

	docGoDecls, docGoErrs := loadDocGoDeclarations(root)
	decls = append(decls, docGoDecls...)
	errs = append(errs, docGoErrs...)

	if err := validateAll(decls); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return nil, joinErrors(errs)
	}
	return decls, nil
}

// validateAll catches ALWAYS/NEVER declarations that reused another
// declaration's Phase name — an ambiguity findLaneBlocks cannot see
// because it works one file at a time.
func validateAll(decls []Declaration) error {
	byPhase := map[string]string{}
	var errs []error
	for _, d := range decls {
		if prev, ok := byPhase[d.Phase]; ok {
			errs = append(errs, fmt.Errorf("phase %q is declared twice: %s and %s", d.Phase, prev, d.Source))
			continue
		}
		byPhase[d.Phase] = d.Source
	}
	if len(errs) > 0 {
		return joinErrors(errs)
	}
	return nil
}

// loadScriptHeaders scans every scripts/*.sh and .agents/scripts/*.sh file
// (top level only — scripts/tests/** and scripts/lib/** are test helpers
// and libraries, not gates) for a header lane-inputs block, resolving its
// Phase via scriptPhase (D-2's corpus-restricted reverse map). verify.sh
// is excluded — it carries many blocks, one per wrapped/bare phase, and is
// handled by loadVerifyDeclarations instead.
func loadScriptHeaders(root string, scriptPhase map[string]string, verifyPath string) ([]Declaration, []error) {
	var decls []Declaration
	var errs []error

	var candidates []string
	for _, dir := range []string{"scripts", ".agents/scripts"} {
		matches, _ := filepath.Glob(filepath.Join(root, dir, "*.sh"))
		candidates = append(candidates, matches...)
	}

	for _, path := range candidates {
		if path == verifyPath {
			continue
		}
		raw, err := readRepoFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", path, err))
			continue
		}
		relPath := toRepoRelative(root, path)
		blocks, err := findLaneBlocks(strings.Split(string(raw), "\n"), "#")
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", relPath, err))
			continue
		}
		if len(blocks) == 0 {
			continue
		}
		if len(blocks) > 1 {
			errs = append(errs, fmt.Errorf("%s: %d lane-inputs blocks in one script header, want exactly one gate per script", relPath, len(blocks)))
			continue
		}
		b := blocks[0]
		phase, ok := scriptPhase[relPath]
		if !ok {
			errs = append(errs, fmt.Errorf("%s:%d: declares lane-inputs but no corpus phase's Makefile recipe invokes this script directly (or that phase left the corpus)", relPath, b.StartLine))
			continue
		}
		decls = append(decls, declarationFromBlock(b, phase, relPath))
	}
	return decls, errs
}

// loadMakefileTargetDeclarations scans the Makefile itself for a block that
// sits directly above a target definition line — the home for a corpus
// phase with no separate backing script (workflow-lint's inline shell).
func loadMakefileTargetDeclarations(_ string, doc *makefileDoc) ([]Declaration, []error) {
	var decls []Declaration
	var errs []error

	blocks, err := findLaneBlocks(doc.Lines, "#")
	if err != nil {
		return nil, []error{fmt.Errorf("in Makefile: %w", err)}
	}
	for _, b := range blocks {
		m := targetHeaderRe.FindStringSubmatch(b.Following)
		if m == nil {
			errs = append(errs, fmt.Errorf("in Makefile:%d: lane-inputs block does not sit directly above a target definition (found %q)", b.StartLine, b.Following))
			continue
		}
		decls = append(decls, declarationFromBlock(b, m[1], "Makefile"))
	}
	return decls, errs
}

// loadVerifyDeclarations scans scripts/verify.sh for a block above a
// wrapping function (resolved via funcToPhase) or directly above a bare
// run_phase statement (vet, coverage-policy, harness-teeth — D-1).
func loadVerifyDeclarations(root, verifyPath string) ([]Declaration, []error) {
	raw, err := readRepoFile(verifyPath)
	if err != nil {
		return nil, []error{fmt.Errorf("read scripts/verify.sh: %w", err)}
	}
	lines := strings.Split(string(raw), "\n")

	_, funcToPhase, err := corpusFromVerify(root)
	if err != nil {
		return nil, []error{err}
	}

	blocks, err := findLaneBlocks(lines, "#")
	if err != nil {
		return nil, []error{fmt.Errorf("scripts/verify.sh: %w", err)}
	}

	var decls []Declaration
	var errs []error
	for _, b := range blocks {
		if m := funcOpenRe.FindStringSubmatch(b.Following); m != nil {
			phase, ok := funcToPhase[m[1]]
			if !ok {
				errs = append(errs, fmt.Errorf("scripts/verify.sh:%d: precedes function %s(), which backs no top-level run_phase call site", b.StartLine, m[1]))
				continue
			}
			decls = append(decls, declarationFromBlock(b, phase, "scripts/verify.sh"))
			continue
		}
		if m := runPhaseBareRe.FindStringSubmatch(b.Following); m != nil {
			nameTok := strings.Trim(m[1], `"`)
			if strings.HasPrefix(m[1], "$") || strings.HasPrefix(m[1], `"$`) {
				errs = append(errs, fmt.Errorf("scripts/verify.sh:%d: precedes a variable run_phase call (%q), which is never a phase name", b.StartLine, m[1]))
				continue
			}
			decls = append(decls, declarationFromBlock(b, nameTok, "scripts/verify.sh"))
			continue
		}
		errs = append(errs, fmt.Errorf("scripts/verify.sh:%d: lane-inputs block does not sit above a function definition or a run_phase statement (found %q)", b.StartLine, b.Following))
	}
	return decls, errs
}

// loadDocGoDeclarations scans every doc.go for a `// lane-inputs:` block
// (D-4 — a Go package declaring its non-Go inputs). The derived phase name
// is "go-test-scoped:./<pkg-dir>/..." and is exempt from corpus membership
// by construction: the package directory it names already exists, because
// that is where the doc.go scanned lives.
func loadDocGoDeclarations(root string) ([]Declaration, []error) {
	var decls []Declaration
	var errs []error

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// `.a2a` is the LOCAL CACHE, not source, and pruning it is what
			// keeps this walk asking about the repository rather than about
			// the machine. `a2a`'s own feedback reader clones the public
			// repository into .a2a/cache/feedback-repo/<slug>/ — a complete
			// second copy of this tree, doc.go files and all. Unpruned, this
			// walk derived a real declaration for
			// `go-test-scoped:./.a2a/cache/feedback-repo/ydnikolaev-a2ahub/internal/e2e/...`
			// and the lane then ran `go test` inside a clone, which fails to
			// set up and reds a tree whose own packages are green.
			//
			// This is the SEVENTH instance of one class in this repository —
			// a check that walks the FILESYSTEM answering a different
			// question from one that walks the TRACKED SET — and the first
			// one in Go rather than in a shell gate. The same prune, with
			// the same reason, is already in internal/e2e/writefloor_test.go
			// and testkit/gitfixture/hygiene_test.go.
			switch d.Name() {
			case ".git", ".a2a", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "doc.go" {
			return nil
		}
		raw, rerr := readRepoFile(path)
		if rerr != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", path, rerr))
			return nil
		}
		relPath := toRepoRelative(root, path)
		blocks, berr := findLaneBlocks(strings.Split(string(raw), "\n"), "//")
		if berr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", relPath, berr))
			return nil
		}
		if len(blocks) == 0 {
			return nil
		}
		if len(blocks) > 1 {
			errs = append(errs, fmt.Errorf("%s: %d lane-inputs blocks in one doc.go, want exactly one", relPath, len(blocks)))
			return nil
		}
		b := blocks[0]
		if !strings.HasPrefix(b.Following, "package ") {
			errs = append(errs, fmt.Errorf("%s:%d: lane-inputs block does not sit directly above the package clause (found %q)", relPath, b.StartLine, b.Following))
			return nil
		}
		pkgDir := filepath.ToSlash(filepath.Dir(relPath))
		phase := fmt.Sprintf("go-test-scoped:./%s/...", pkgDir)
		decls = append(decls, declarationFromBlock(b, phase, relPath))
		return nil
	})
	if walkErr != nil {
		// A discarded walk error is a silently-partial declaration set —
		// exactly the failure mode this package refuses to produce
		// anywhere else.
		errs = append(errs, fmt.Errorf("walk %s for doc.go: %w", root, walkErr))
	}
	return decls, errs
}

func declarationFromBlock(b rawBlock, phase, relPath string) Declaration {
	return Declaration{
		Phase:  phase,
		Kind:   b.Kind,
		Inputs: b.Inputs,
		Reason: b.Reason,
		Claims: b.Claims,
		Tier:   b.Tier,
		Source: fmt.Sprintf("%s:%d", relPath, b.StartLine),
	}
}

func toRepoRelative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
