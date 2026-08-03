package host

import (
	"bytes"
	"context"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// RemoteTreeFile is one exact regular file read from an already-fetched Git
// commit. It is a host-mechanics value: callers own all domain interpretation.
type RemoteTreeFile struct {
	Mode string
	Raw  []byte
}

// ListRemoteRecoveryTreeFiles resolves every regular file below a closed set
// of Git-tree prefixes at one commit, then reads that exact bounded path set.
func (h *GitHubHost) ListRemoteRecoveryTreeFiles(ctx context.Context, repoDir, commitSHA string, prefixes []string) (map[string]RemoteTreeFile, error) {
	const op = "ListRemoteRecoveryTreeFiles"
	if strings.TrimSpace(repoDir) == "" || !validRecoveryGitOID(commitSHA) || len(prefixes) == 0 || len(prefixes) > 4 {
		return nil, recoveryRequestError(op)
	}
	wantedPrefixes := append([]string(nil), prefixes...)
	sort.Strings(wantedPrefixes)
	for index, prefix := range wantedPrefixes {
		if !validRemoteTreePath(prefix) || index > 0 && wantedPrefixes[index-1] == prefix {
			return nil, recoveryRequestError(op)
		}
	}
	args := []string{"-C", repoDir, "ls-tree", "-r", "-z", "--full-tree", commitSHA, "--"}
	args = append(args, wantedPrefixes...)
	listing := runRecoveryGit(ctx, maxRecoveryGitOutput, args...)
	if listing.err != nil || listing.exitCode != 0 {
		return nil, recoveryGitError(op, "list recovery tree", listing.err)
	}
	if len(listing.stdout) == 0 {
		return map[string]RemoteTreeFile{}, nil
	}
	if listing.stdout[len(listing.stdout)-1] != 0 {
		return nil, recoveryGitError(op, "recovery tree listing is incomplete", nil)
	}
	records := bytes.Split(listing.stdout[:len(listing.stdout)-1], []byte{0})
	if len(records) > maxRecoveryChangedPaths+2 {
		return nil, recoveryGitError(op, "recovery tree listing exceeds path bound", nil)
	}
	paths := make([]string, 0, len(records))
	for _, record := range records {
		metadata, name, ok := strings.Cut(string(record), "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 || fields[1] != "blob" ||
			(fields[0] != "100644" && fields[0] != "100755") || !validRecoveryGitOID(fields[2]) ||
			!validRemoteTreePath(name) || !insideAnyRemoteTreePrefix(name, wantedPrefixes) {
			return nil, recoveryGitError(op, "recovery tree listing contains an unsafe entry", nil)
		}
		paths = append(paths, name)
	}
	if len(paths) == 0 {
		return map[string]RemoteTreeFile{}, nil
	}
	return h.ReadRemoteRecoveryTreeFiles(ctx, repoDir, commitSHA, paths)
}

// ReadRemoteRecoveryTreeFiles reads a closed, caller-supplied path set from a
// verified commit without consulting a worktree or network loader.
func (h *GitHubHost) ReadRemoteRecoveryTreeFiles(ctx context.Context, repoDir, commitSHA string, paths []string) (map[string]RemoteTreeFile, error) {
	const op = "ReadRemoteRecoveryTreeFiles"
	if strings.TrimSpace(repoDir) == "" || !validRecoveryGitOID(commitSHA) || len(paths) == 0 || len(paths) > maxRecoveryChangedPaths+2 {
		return nil, recoveryRequestError(op)
	}
	wanted := append([]string(nil), paths...)
	sort.Strings(wanted)
	for index, name := range wanted {
		if !validRemoteTreePath(name) || index > 0 && wanted[index-1] == name {
			return nil, recoveryRequestError(op)
		}
	}
	resolved := runRecoveryGit(ctx, 256, "-C", repoDir, "rev-parse", "--verify", commitSHA+"^{commit}")
	if resolved.err != nil || resolved.exitCode != 0 || strings.TrimSpace(string(resolved.stdout)) != commitSHA {
		return nil, recoveryGitError(op, "resolve recovery commit", resolved.err)
	}
	result := make(map[string]RemoteTreeFile, len(wanted))
	total := 0
	for _, name := range wanted {
		entry := runRecoveryGit(ctx, maxRecoveryPathBytes+256,
			"-C", repoDir, "ls-tree", "-z", "--full-tree", commitSHA, "--", name)
		if entry.err != nil || entry.exitCode != 0 || len(entry.stdout) == 0 ||
			entry.stdout[len(entry.stdout)-1] != 0 || bytes.Count(entry.stdout, []byte{0}) != 1 {
			return nil, recoveryGitError(op, "read exact tree entry", entry.err)
		}
		record := string(entry.stdout[:len(entry.stdout)-1])
		metadata, observed, ok := strings.Cut(record, "\t")
		fields := strings.Fields(metadata)
		if !ok || observed != name || len(fields) != 3 || fields[1] != "blob" ||
			(fields[0] != "100644" && fields[0] != "100755") || !validRecoveryGitOID(fields[2]) {
			return nil, recoveryGitError(op, "tree entry is not an exact regular file", nil)
		}
		sizeResult := runRecoveryGit(ctx, 128, "-C", repoDir, "cat-file", "-s", fields[2])
		size, sizeErr := strconv.Atoi(strings.TrimSuffix(string(sizeResult.stdout), "\n"))
		if sizeResult.err != nil || sizeResult.exitCode != 0 || sizeErr != nil || size < 0 ||
			size > maxRecoveryBlobBytes || size > maxRecoveryAggregateBytes-total {
			return nil, recoveryGitError(op, "tree blob exceeds recovery bounds", sizeResult.err)
		}
		blob := runRecoveryGit(ctx, size, "-C", repoDir, "cat-file", "blob", fields[2])
		if blob.err != nil || blob.exitCode != 0 || len(blob.stdout) != size {
			return nil, recoveryGitError(op, "read exact tree blob", blob.err)
		}
		total += size
		result[name] = RemoteTreeFile{Mode: fields[0], Raw: append([]byte(nil), blob.stdout...)}
	}
	return result, nil
}

func validRemoteTreePath(name string) bool {
	return name != "" && len(name) <= maxRecoveryPathBytes && utf8.ValidString(name) &&
		!strings.ContainsAny(name, "\\\x00\r\n") && !strings.HasPrefix(name, "-") &&
		path.Clean(name) == name && name != "." && !strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "../")
}

func insideAnyRemoteTreePrefix(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if name == prefix || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}
