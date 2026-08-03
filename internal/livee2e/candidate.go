package livee2e

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
)

const requiredCandidateFloor = "0.19.0"

var (
	// ErrCandidateSource marks a refusal at the exact public-candidate source
	// boundary. It is always returned before building or provider mutation.
	ErrCandidateSource = errors.New("livee2e: candidate source attestation failed")
	// ErrCandidateBinary marks a refusal after the local build but before any
	// live-space provisioning or provider mutation.
	ErrCandidateBinary = errors.New("livee2e: candidate binary attestation failed")
)

// CandidateExpectation is the publisher-bound input for one live run.
type CandidateExpectation struct {
	Root     string
	SHA      string
	Tree     string
	Tag      string
	Floor    string
	CheckLog string
}

// CandidateAttestation is the evidence established before ResetSpace may
// mutate the dedicated provider space.
type CandidateAttestation struct {
	SourceRoot     string
	SourceSHA      string
	SourceTree     string
	SourceDetached bool
	IndexClean     bool
	WorktreeClean  bool
	UntrackedClean bool
	IntendedTag    string
	TemplateFloor  string
	CheckLog       string
	BinarySHA256   string
	BuildRevision  string
	BuildModified  bool
	BinaryStamp    string
}

// candidateCheckAttestation parses the retained verification-checkout
// transcript into an attestation independent of the execution checkout. The
// transcript is the only durable input describing the checkout that ran
// make check, so every release-bearing fact has exact-one cardinality.
func candidateCheckAttestation(raw []byte, checkLog string, want CandidateExpectation) (CandidateAttestation, error) {
	values := map[string]string{
		"CHECKOUT_ROOT":     "",
		"CANDIDATE_SHA":     want.SHA,
		"CANDIDATE_TREE":    want.Tree,
		"CANDIDATE_TAG":     want.Tag,
		"CHECKOUT_DETACHED": "true",
		"INDEX_CLEAN":       "true",
		"WORKTREE_CLEAN":    "true",
		"UNTRACKED_CLEAN":   "true",
		"EXIT":              "0",
	}
	parsed := make(map[string]string, len(values))
	counts := make(map[string]int, len(values))
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if _, required := values[key]; !required {
			continue
		}
		counts[key]++
		parsed[key] = value
	}
	for key, expected := range values {
		if counts[key] != 1 {
			return CandidateAttestation{}, fmt.Errorf("retained check log must contain exactly one %s marker", key)
		}
		if expected != "" && parsed[key] != expected {
			return CandidateAttestation{}, fmt.Errorf("retained check log %s=%q, want %q", key, parsed[key], expected)
		}
	}
	root := parsed["CHECKOUT_ROOT"]
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return CandidateAttestation{}, fmt.Errorf("retained check log CHECKOUT_ROOT=%q must be an absolute clean path", root)
	}
	return CandidateAttestation{
		SourceRoot:     root,
		SourceSHA:      parsed["CANDIDATE_SHA"],
		SourceTree:     parsed["CANDIDATE_TREE"],
		SourceDetached: true,
		IndexClean:     true,
		WorktreeClean:  true,
		UntrackedClean: true,
		IntendedTag:    parsed["CANDIDATE_TAG"],
		TemplateFloor:  want.Floor,
		CheckLog:       checkLog,
	}, nil
}

func checkoutEvidenceID(root string) string {
	digest := sha256.Sum256([]byte(root))
	return fmt.Sprintf("checkout-%x", digest[:12])
}

type candidateCommandRunner func(context.Context, string, string, ...string) (string, error)

func attestCandidateSourceWith(
	ctx context.Context,
	want CandidateExpectation,
	run candidateCommandRunner,
	readFile func(string) ([]byte, error),
	evalSymlinks func(string) (string, error),
) (CandidateAttestation, error) {
	fail := func(format string, args ...any) (CandidateAttestation, error) {
		return CandidateAttestation{}, fmt.Errorf("%w: %s", ErrCandidateSource, fmt.Sprintf(format, args...))
	}

	if !filepath.IsAbs(want.Root) {
		return fail("execution root is not absolute: %q", want.Root)
	}
	wantRoot, err := evalSymlinks(filepath.Clean(want.Root))
	if err != nil {
		return fail("resolve execution root %q: %v", want.Root, err)
	}
	repoRoot, err := run(ctx, want.Root, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return fail("resolve git module root: %v", err)
	}
	gotRoot, err := evalSymlinks(filepath.Clean(repoRoot))
	if err != nil {
		return fail("resolve reported git root %q: %v", repoRoot, err)
	}
	if gotRoot != wantRoot {
		return fail("execution root %q is not the git module root %q", wantRoot, gotRoot)
	}

	module, err := readFile(filepath.Join(wantRoot, "go.mod"))
	if err != nil {
		return fail("read candidate go.mod: %v", err)
	}
	if !regexp.MustCompile(`(?m)^module github\.com/ydnikolaev/a2ahub\s*$`).Match(module) {
		return fail("go.mod does not declare module github.com/ydnikolaev/a2ahub")
	}

	head, err := run(ctx, wantRoot, "git", "rev-parse", "HEAD")
	if err != nil {
		return fail("resolve HEAD: %v", err)
	}
	if head != want.SHA {
		return fail("HEAD=%s, want candidate %s", head, want.SHA)
	}
	tree, err := run(ctx, wantRoot, "git", "rev-parse", "HEAD^{tree}")
	if err != nil {
		return fail("resolve HEAD tree: %v", err)
	}
	if tree != want.Tree {
		return fail("HEAD tree=%s, want publisher tree %s", tree, want.Tree)
	}
	branch, err := run(ctx, wantRoot, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fail("inspect detached HEAD: %v", err)
	}
	if branch != "HEAD" {
		return fail("candidate checkout is attached to %q, want detached HEAD", branch)
	}

	staged, err := run(ctx, wantRoot, "git", "diff", "--cached", "--name-only", "--no-ext-diff")
	if err != nil {
		return fail("inspect staged changes: %v", err)
	}
	if staged != "" {
		return fail("candidate index is dirty: %s", oneLine(staged))
	}
	worktree, err := run(ctx, wantRoot, "git", "diff", "--name-only", "--no-ext-diff")
	if err != nil {
		return fail("inspect worktree changes: %v", err)
	}
	if worktree != "" {
		return fail("candidate worktree is dirty: %s", oneLine(worktree))
	}
	status, err := run(ctx, wantRoot, "git", "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return fail("inspect candidate status: %v", err)
	}
	if status != "" {
		return fail("candidate status is not clean (including untracked files): %s", oneLine(status))
	}

	manifest, err := readFile(filepath.Join(wantRoot, "space-template", "space.yaml"))
	if err != nil {
		return fail("read candidate embedded space template: %v", err)
	}
	floor, err := candidateTemplateFloor(manifest)
	if err != nil {
		return fail("parse candidate embedded space template: %v", err)
	}
	if want.Floor != requiredCandidateFloor || floor != want.Floor {
		return fail("embedded min_binary_version=%s, expectation=%s, required=%s", floor, want.Floor, requiredCandidateFloor)
	}

	workflow, err := readFile(filepath.Join(wantRoot, "space-template", ".github", "workflows", "a2a-validate.yml"))
	if err != nil {
		return fail("read candidate embedded workflow: %v", err)
	}
	if err := candidateWorkflowRefs(workflow, want.Tag); err != nil {
		return fail("embedded workflow refs: %v", err)
	}

	checkLog, err := readFile(want.CheckLog)
	if err != nil {
		return fail("read retained candidate check log: %v", err)
	}
	verification, err := candidateCheckAttestation(checkLog, want.CheckLog, want)
	if err != nil {
		return fail("parse retained candidate check log: %v", err)
	}
	if verification.SourceRoot == wantRoot {
		return fail("verification and execution checkout roots are identical: %s", wantRoot)
	}

	return CandidateAttestation{
		SourceRoot:     wantRoot,
		SourceSHA:      head,
		SourceTree:     tree,
		SourceDetached: true,
		IndexClean:     true,
		WorktreeClean:  true,
		UntrackedClean: true,
		IntendedTag:    want.Tag,
		TemplateFloor:  floor,
		CheckLog:       want.CheckLog,
	}, nil
}

var candidateFloorPattern = regexp.MustCompile(`(?m)^min_binary_version:\s*"?([0-9]+\.[0-9]+\.[0-9]+)"?(?:\s|$)`)

func candidateTemplateFloor(raw []byte) (string, error) {
	matches := candidateFloorPattern.FindAllSubmatch(raw, -1)
	if len(matches) != 1 {
		return "", fmt.Errorf("min_binary_version cardinality=%d, want 1", len(matches))
	}
	return string(matches[0][1]), nil
}

var candidateWorkflowRefPattern = regexp.MustCompile(`(?m)^[ \t]*uses:[ \t]+ydnikolaev/a2ahub/\.github/workflows/a2a-validate-reusable\.yml@([^[:space:]#]+)[ \t]*(?:#.*)?$`)

func candidateWorkflowRefs(raw []byte, tag string) error {
	matches := candidateWorkflowRefPattern.FindAllSubmatch(raw, -1)
	if len(matches) != 2 {
		return fmt.Errorf("release-ref cardinality=%d, want 2", len(matches))
	}
	for _, match := range matches {
		if string(match[1]) != tag {
			return fmt.Errorf("found %s, want both refs at %s", match[1], tag)
		}
	}
	return nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func attestCandidateBinaryWith(
	ctx context.Context,
	bin string,
	want CandidateExpectation,
	source CandidateAttestation,
	run candidateCommandRunner,
	readBuildInfo func(string) (*debug.BuildInfo, error),
) (CandidateAttestation, error) {
	fail := func(format string, args ...any) (CandidateAttestation, error) {
		return CandidateAttestation{}, fmt.Errorf("%w: %s", ErrCandidateBinary, fmt.Sprintf(format, args...))
	}

	digest, err := fileSHA256(bin)
	if err != nil {
		return fail("hash binary: %v", err)
	}
	info, err := readBuildInfo(bin)
	if err != nil {
		return fail("read Go build info: %v", err)
	}
	revision, revisionCount := buildSetting(info.Settings, "vcs.revision")
	if revisionCount != 1 || revision != want.SHA {
		return fail("vcs.revision=%q, want %s", revision, want.SHA)
	}
	modified, modifiedCount := buildSetting(info.Settings, "vcs.modified")
	if modifiedCount != 1 || modified != "false" {
		return fail("vcs.modified=%q, want false", modified)
	}
	stamp, err := run(ctx, want.Root, bin, "version")
	if err != nil {
		return fail("read binary version stamp: %v", err)
	}
	wantStamp := fmt.Sprintf("a2a %s (%s)", want.Floor, want.SHA)
	if stamp != wantStamp {
		return fail("binary stamp=%q, want %q", stamp, wantStamp)
	}

	source.BinarySHA256 = digest
	source.BuildRevision = revision
	source.BuildModified = false
	source.BinaryStamp = stamp
	return source, nil
}

func buildSetting(settings []debug.BuildSetting, key string) (string, int) {
	var value string
	count := 0
	for _, setting := range settings {
		if setting.Key == key {
			value = setting.Value
			count++
		}
	}
	return value, count
}

func fileSHA256(path string) (digest string, retErr error) {
	// #nosec G304 -- path is the just-built binary selected by the live harness; build-info and SHA attest it before use.
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("close %q: %w", path, closeErr)
		}
	}()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
