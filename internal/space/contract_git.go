package space

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

const maxContractGitListingBytes = 1 << 20

type contractGitTreeEntry struct {
	Mode string
	Kind string
	OID  string
	Path string
}

// contractGitBytes is the bounded read-only Git execution seam for contract
// collection. It deliberately does not use the general plumbing helper:
// historical verification must not inherit ambient GIT_DIR, alternate object
// stores, replacement objects, or user/system Git configuration.
func contractGitBytes(ctx context.Context, repoDir string, limit int64, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if repoDir == "" || limit < 0 {
		return nil, fmt.Errorf("space: invalid bounded git contract read")
	}
	// Ask the shared buffer for one byte beyond this operation's ceiling.
	// That makes the bound observable even on a platform where os/exec does
	// not propagate the writer's overflow error after the process exits.
	// A render asks the same object-addressed question four times out of five
	// (measured: 2 197 invocations, 481 distinct). When a caller has installed
	// a memo for the life of ONE read-only operation, answer from it rather
	// than paying another ~6 ms process spawn. Nothing that has not installed
	// one is affected; see contract_git_memo.go for what is and is not eligible.
	memo := contractGitMemoFrom(ctx)
	memoKey := ""
	if memo != nil && contractGitMemoizable(args) {
		memoKey = contractGitMemoKeyFor(repoDir, limit, args)
		if raw, ok := memo.get(memoKey); ok {
			return raw, nil
		}
	}
	bufferLimit := limit + 1
	gitArgs := append([]string{"--no-replace-objects", "--literal-pathspecs"}, args...)
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append([]string{"git"}, gitArgs...)
	cmd.Dir = repoDir
	cmd.Env = contractGitEnvironment()
	stdout := &boundedCommandBuffer{remaining: bufferLimit}
	stderr := &boundedCommandBuffer{remaining: maxTreeCommitMetadataBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, errTreeCommitOutputLimit) {
			return nil, fmt.Errorf("%w: git %s output exceeds %d bytes", ErrContractBoundedResult, firstContractGitArg(args), limit)
		}
		return nil, fmt.Errorf("git %v: %w: %s", gitArgs, err, stderr.String())
	}
	raw := append([]byte(nil), stdout.Bytes()...)
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: git %s output exceeds %d bytes", ErrContractBoundedResult, firstContractGitArg(args), limit)
	}
	if memoKey != "" {
		memo.put(memoKey, raw)
	}
	return raw, nil
}

func contractGitEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+5)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment,
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"LC_ALL=C",
	)
}

func firstContractGitArg(args []string) string {
	if len(args) == 0 {
		return "command"
	}
	return args[0]
}

func contractGitResolveCommit(ctx context.Context, repoDir, ref string) (string, error) {
	if ref == "" || strings.ContainsAny(ref, "\x00\r\n") || strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("%w: invalid commit reference", ErrContractObjectUnavailable)
	}
	raw, err := contractGitBytes(ctx, repoDir, maxTreeCommitMetadataBytes,
		"rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("%w: commit %q is unavailable; run a2a sync: %w", ErrContractObjectUnavailable, ref, err)
	}
	oid := strings.TrimSpace(string(raw))
	if !contractGitObjectID(oid) || strings.ContainsRune(oid, '\n') {
		return "", fmt.Errorf("%w: commit %q did not resolve to one object", ErrContractObjectUnavailable, ref)
	}
	return strings.ToLower(oid), nil
}

func contractGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}

func contractGitListTree(ctx context.Context, repoDir, commit string, recursive bool, paths ...string) ([]contractGitTreeEntry, error) {
	args := []string{"ls-tree", "-z", "--full-tree"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, commit, "--")
	args = append(args, paths...)
	raw, err := contractGitBytes(ctx, repoDir, maxContractGitListingBytes, args...)
	if err != nil {
		return nil, err
	}
	return parseContractGitTree(raw)
}

func parseContractGitTree(raw []byte) ([]contractGitTreeEntry, error) {
	if len(raw) == 0 {
		return []contractGitTreeEntry{}, nil
	}
	if raw[len(raw)-1] != 0 {
		return nil, fmt.Errorf("space: malformed non-NUL-terminated git tree listing")
	}
	records := bytes.Split(raw[:len(raw)-1], []byte{0})
	entries := make([]contractGitTreeEntry, 0, len(records))
	for _, record := range records {
		metadata, name, found := bytes.Cut(record, []byte{'\t'})
		fields := strings.Fields(string(metadata))
		if !found || len(fields) != 3 || len(name) == 0 || bytes.ContainsRune(name, '\x00') || !contractGitObjectID(fields[2]) {
			return nil, fmt.Errorf("space: malformed git tree record")
		}
		entries = append(entries, contractGitTreeEntry{
			Mode: fields[0], Kind: fields[1], OID: strings.ToLower(fields[2]), Path: string(name),
		})
	}
	return entries, nil
}

func contractGitExactTreeEntry(ctx context.Context, repoDir, commit, name string) (contractGitTreeEntry, bool, error) {
	entries, err := contractGitListTree(ctx, repoDir, commit, false, name)
	if err != nil {
		return contractGitTreeEntry{}, false, err
	}
	for _, entry := range entries {
		if entry.Path == name {
			return entry, true, nil
		}
	}
	return contractGitTreeEntry{}, false, nil
}

func contractGitReadBlob(ctx context.Context, repoDir string, entry contractGitTreeEntry, maximum int) ([]byte, error) {
	if entry.Kind != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") {
		return nil, fmt.Errorf("%w: %q has git mode/type %s/%s", ErrContractUnsafeEntry, entry.Path, entry.Mode, entry.Kind)
	}
	sizeRaw, err := contractGitBytes(ctx, repoDir, maxTreeCommitMetadataBytes, "cat-file", "-s", entry.OID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: blob for %q is unavailable; run a2a sync: %w", ErrContractObjectUnavailable, entry.Path, err)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeRaw)), 10, 64)
	if err != nil || size < 0 {
		return nil, fmt.Errorf("%w: invalid blob size for %q", ErrContractObjectUnavailable, entry.Path)
	}
	if size > int64(maximum) {
		return nil, fmt.Errorf("%w: %q exceeds %d bytes", ErrContractBoundedResult, entry.Path, maximum)
	}
	raw, err := contractGitBytes(ctx, repoDir, int64(maximum)+1, "cat-file", "blob", entry.OID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: blob for %q is unavailable; run a2a sync: %w", ErrContractObjectUnavailable, entry.Path, err)
	}
	if int64(len(raw)) != size {
		return nil, fmt.Errorf("%w: blob size for %q changed while reading", ErrContractObjectUnavailable, entry.Path)
	}
	return raw, nil
}

func contractGitReadPath(ctx context.Context, repoDir, commit, name string, maximum int) (ContractSnapshotFile, error) {
	entry, found, err := contractGitExactTreeEntry(ctx, repoDir, commit, name)
	if err != nil {
		return ContractSnapshotFile{}, err
	}
	if !found {
		return ContractSnapshotFile{}, fmt.Errorf("%w: historical file %q is missing", ErrContractHistoryInvalid, name)
	}
	kind := contractGitCandidateKind(entry)
	if kind != contract.CandidateRegular {
		return ContractSnapshotFile{}, fmt.Errorf("%w: %q has git mode/type %s/%s", ErrContractUnsafeEntry, name, entry.Mode, entry.Kind)
	}
	raw, err := contractGitReadBlob(ctx, repoDir, entry, maximum)
	if err != nil {
		return ContractSnapshotFile{}, err
	}
	return ContractSnapshotFile{
		Path: name, Kind: kind, Mode: entry.Mode, Source: ContractCandidateSourceMirror,
		ObjectID: entry.OID, Raw: raw,
	}, nil
}

func contractGitCandidateKind(entry contractGitTreeEntry) contract.CandidateKind {
	switch {
	case entry.Kind == "blob" && (entry.Mode == "100644" || entry.Mode == "100755"):
		return contract.CandidateRegular
	case entry.Kind == "blob" && entry.Mode == "120000":
		return contract.CandidateSymlink
	case entry.Kind == "commit" && entry.Mode == "160000":
		return contract.CandidateSubmodule
	default:
		return contract.CandidateOther
	}
}

func contractGitTreeObjectID(ctx context.Context, repoDir, commit, name string) (string, error) {
	entry, found, err := contractGitExactTreeEntry(ctx, repoDir, commit, name)
	if err != nil {
		return "", err
	}
	if !found || entry.Kind != "tree" || entry.Mode != "040000" {
		return "", fmt.Errorf("%w: contract root %q is not a Git tree", ErrContractHistoryInvalid, name)
	}
	return entry.OID, nil
}
