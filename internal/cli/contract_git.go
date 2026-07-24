// P37 wave A2 (spec 37 §2 T2's compat core needs raw bytes, not just
// digests). `contract diff` already resolves a version to a commit
// (contractResolveVersionSHA, cmd_contract.go) and digests its tree
// (contractDigestTreeAtSHA, cmd_contract.go) — this file gives the CLI one
// home for reading a PRIOR contract version's actual file CONTENT out of
// git history, since internal/space/layout.go's ProvidesContract/
// ProvidesSchemaDir/ProvidesFixturesValidDir shape has NO per-version
// directories: a prior version's files exist only in git history, nowhere
// on disk.
//
// This file's only package-level symbols are contractReadTreeAtSHA,
// contractPriorVersionFiles, and their file-private contractGit* helper —
// same file-private, uniquely-named-helper discipline cmd_contract.go
// documents for itself.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// contractGitBounded runs `git <args...>` in repoDir and returns its
// stdout, capped at max bytes — the SAME "LimitReader to max+1, error if
// the cap is hit" idiom this package's own readBoundedFile (adapters.go)
// already uses for on-disk artifact reads, adapted here to a subprocess
// pipe instead of an *os.File: neither a file living in git history nor a
// tree listing from a repo this code does not control the size of may be
// read unbounded into memory. Both of this file's git reads — the content
// and the listing that names it — go through here, so the bound its
// callers document is the bound they actually get.
//
// On a cap hit or any read error the subprocess is killed rather than
// waited on: with a pipe's bounded OS buffer, a git process still trying
// to write past what we stopped draining would otherwise block cmd.Wait()
// forever.
func contractGitBounded(ctx context.Context, repoDir string, max int64, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoDir}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	raw, rerr := io.ReadAll(io.LimitReader(stdout, max+1))
	if rerr != nil {
		_ = cmd.Process.Kill() // reason: abandon the subprocess, we are already returning its read error
		_ = cmd.Wait()         // reason: reap it post-kill; the wait error itself is moot once we're erroring on rerr
		return nil, rerr
	}
	if int64(len(raw)) > max {
		_ = cmd.Process.Kill() // reason: past the bound — stop draining and kill rather than risk Wait() blocking on a git process still trying to write into a pipe we stopped reading
		_ = cmd.Wait()         // reason: reap the killed process; its wait error is moot, we already have the bound-exceeded error to return
		return nil, fmt.Errorf("content exceeds %d byte read bound", max)
	}
	if werr := cmd.Wait(); werr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", werr, msg)
		}
		return nil, werr
	}
	return raw, nil
}

// contractReadTreeAtSHA returns the raw CONTENT of every file under the
// named subtrees of descriptorDir as of sha, keyed by its path RELATIVE TO
// descriptorDir (forward-slash) — e.g. "schema/main.schema.json",
// "fixtures/valid/ok.json". Note this differs from this function's own doc
// comment as originally briefed ("keyed by repo-relative path"): it is
// deliberately keyed the SAME way contractDigestTreeAtSHA already keys its
// own digest map (relative to descriptorDir, via filepath.Rel), not
// repo-relative — see this phase's Deviations report.
//
// This is contractDigestTreeAtSHA's (cmd_contract.go) raw-content sibling:
// SAME traversal (git ls-tree -r --name-only per subtree, then git show
// sha:path per file) and SAME tolerance — a subtree that does not exist at
// sha (an ls-tree with no matches, exit 0) is omitted, not an error.
//
// It deliberately does NOT inherit two of the sibling's quirks, because
// what is a cosmetic wrong answer for a digest is a SILENT PASS here.
// Both were found by this wave's early audit:
//
//   - The sibling splits ls-tree's output on whitespace, shredding any
//     tracked path containing a space into bogus tokens. There it costs a
//     wrong digest; here it would DROP fixtures, and a shrunk PriorFixtures
//     set walks straight into D-B's "the prior version published no
//     fixtures, proceed". This one reads NUL-terminated `-z` output, and
//     any listed path that resolves outside descriptorDir is an error
//     rather than a silent skip.
//   - The sibling folds ls-tree's error path into the same "treat as
//     absent" branch as a genuinely-missing subtree, so an unknown sha
//     yields an empty result and a nil error. Digests can absorb that;
//     compatibility cannot — an unreachable base would read as "nothing to
//     compute". The sha's shape and existence are both verified up front.
//
// Bounded: BOTH git reads — each file's content and the tree listing that
// names it — go through contractGitBounded at maxMirrorEventBytes
// (adapters.go, this package's own existing artifact-read ceiling, reused
// rather than duplicated). Exceeding it is a hard error naming the sha and
// the path, never a silent truncation.
//
// Deterministic: each subtree's file list is sorted before it is read, so
// which file (if any) trips the bound first does not depend on git's or
// the OS's own listing order.
func contractReadTreeAtSHA(ctx context.Context, repoDir, sha, descriptorDir string, subtrees []string) (map[string][]byte, error) {
	// Resolve the commit ONCE, up front, so that "no files here" and "no
	// such commit" can never answer the same way. Without this the two
	// causes are indistinguishable: `git ls-tree` exits non-zero for a
	// bogus sha exactly as it does for a genuinely-absent subtree, the loop
	// below folds both into its `continue`, and the caller receives an
	// empty map with a nil error. For the compat core that is a fail-OPEN:
	// D-B reads an empty PriorFixtures as "the prior version published no
	// fixtures — nothing computed, proceed", so an unreachable base would
	// wave a breaking change through while reporting a benign reason. The
	// callers that resolve their own sha (contractPriorVersionFiles, via
	// contractResolveVersionSHA) never needed this; `validate --ci`, which
	// is handed a `--base` it did not resolve, does.
	if !contractLooksLikeObjectID(sha) {
		return nil, fmt.Errorf("cli: %q is not a git object id", sha)
	}
	if err := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--verify", "--quiet", sha+"^{commit}").Run(); err != nil {
		return nil, fmt.Errorf("cli: %s is not a commit in %s: %w", sha, repoDir, err)
	}
	out := map[string][]byte{}
	for _, sub := range subtrees {
		dir := path.Join(descriptorDir, sub)
		// -z: NUL-terminated names. The digest sibling splits ls-tree's
		// output on whitespace, which shreds any tracked path containing a
		// space into bogus tokens. There it costs a wrong digest; here it
		// would DROP fixtures, and a shrunk PriorFixtures set walks
		// straight into D-B's "the prior version published no fixtures,
		// proceed" — the fail-open this whole function is careful about.
		lsOut, err := contractGitBounded(ctx, repoDir, maxMirrorEventBytes,
			"ls-tree", "-r", "-z", "--name-only", sha, "--", dir)
		if err != nil {
			continue // subtree absent at this SHA — treated as empty, not fatal (the sha itself is already verified above)
		}
		rels := strings.FieldsFunc(string(lsOut), func(r rune) bool { return r == 0 })
		sort.Strings(rels)
		for _, rel := range rels {
			relToDescriptorRoot, rerr := filepath.Rel(descriptorDir, rel)
			if rerr != nil {
				return nil, fmt.Errorf("cli: %s:%s: %w", sha, rel, rerr)
			}
			// filepath.Rel happily answers "../../.." for a path outside
			// descriptorDir and returns a nil error. ls-tree was scoped to
			// dir, so this cannot happen — which is exactly why it must be
			// an error rather than a silent skip if it ever does.
			if strings.HasPrefix(filepath.ToSlash(relToDescriptorRoot), "../") {
				return nil, fmt.Errorf("cli: %s: git listed %q, which is outside %s", sha, rel, descriptorDir)
			}
			content, cerr := contractGitBounded(ctx, repoDir, maxMirrorEventBytes, "show", sha+":"+rel)
			if cerr != nil {
				return nil, fmt.Errorf("cli: git show %s:%s: %w", sha, rel, cerr)
			}
			out[filepath.ToSlash(relToDescriptorRoot)] = content
		}
	}
	return out, nil
}

// contractLooksLikeObjectID reports whether s is a plain hex object id.
// It is a cheap end-of-options stand-in: every sha reaching this file is
// either resolved by contractResolveVersionSHA or is `validate --ci`'s
// `--base`, which arrives from outside. A leading dash would be read by
// git as a flag rather than a revision, and refusing the shape up front is
// clearer than relying on rev-parse to fail closed on every spelling.
func contractLooksLikeObjectID(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// contractPriorVersionFiles resolves version to its commit — via
// contractResolveVersionSHA (cmd_contract.go), this package's own
// version->commit primitive, never re-derived here — and returns that
// commit's schema/** and fixtures/valid/** content, split into two
// separate maps.
//
// ONE call answers for ONE version. validate.CheckComputedCompatibility
// (spec 37 §2 T2) needs the PRIOR version's fixtures and the NEW version's
// schemas — two different commits — so a caller comparing versions calls
// this twice and keeps a different half of each result: `fixturesValid`
// from the prior version, `schemas` from the new one. (At publish time the
// new version's schemas are simply the working tree's, so only the prior
// call is needed.) Reading this as a single call that fills both
// CompatInput fields would compare a version against itself and pass
// everything.
//
// Both returned maps are keyed the SAME way contractReadTreeAtSHA itself
// keys its combined tree — relative to descriptorDir, i.e. "schema/"- and
// "fixtures/valid/"-PREFIXED (e.g. schemas["schema/main.schema.json"],
// fixturesValid["fixtures/valid/ok.json"]), NOT re-keyed to strip the
// prefix. This is deliberate: D-E's fixture->schema mapping rule (the
// phase plan) is stated in this same prefixed vocabulary
// (fixtures/valid/<stem>.json -> schema/<stem>.schema.json), and
// AC-970.1 requires a refusal to NAME THE FIXTURE — a stripped key would
// name a path that does not exist anywhere in the repo. A caller that
// wants bare stems can strip the prefix itself; this function cannot
// un-strip it once dropped.
//
// fixtures/invalid/** is deliberately EXCLUDED from fixturesValid: an
// invalid fixture failing the NEW schema is the fixture doing its job,
// not a compatibility break — feeding it to the compat core would
// silently invert the check.
func contractPriorVersionFiles(ctx context.Context, repoDir, descriptorPath, descriptorDir, version string) (schemas, fixturesValid map[string][]byte, err error) {
	sha, err := contractResolveVersionSHA(ctx, repoDir, descriptorPath, version)
	if err != nil {
		return nil, nil, err
	}
	tree, err := contractReadTreeAtSHA(ctx, repoDir, sha, descriptorDir, []string{"schema", "fixtures"})
	if err != nil {
		return nil, nil, err
	}
	schemas = map[string][]byte{}
	fixturesValid = map[string][]byte{}
	for relPath, content := range tree {
		switch {
		case relPath == "schema" || strings.HasPrefix(relPath, "schema/"):
			schemas[relPath] = content
		case relPath == "fixtures/valid" || strings.HasPrefix(relPath, "fixtures/valid/"):
			fixturesValid[relPath] = content
		default:
			// fixtures/invalid/** and anything else under fixtures/ that is
			// not fixtures/valid/** is intentionally dropped — see doc
			// comment above.
		}
	}
	return schemas, fixturesValid, nil
}
