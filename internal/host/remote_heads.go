package host

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxContractPublishRemoteHeads bounds a publication-head namespace listing.
const MaxContractPublishRemoteHeads = 256

// RemoteHead is one exact remote branch identity returned by an exhaustive
// namespace listing. SHA is the object ID reported by the remote, not a local
// tracking ref.
type RemoteHead struct {
	Branch string
	SHA    string
}

// RemoteHeadListing distinguishes a proved-empty/exhausted namespace from a
// bounded prefix. Callers must not interpret Exhaustive=false as absence.
type RemoteHeadListing struct {
	Heads      []RemoteHead
	Observed   int
	Exhaustive bool
}

// ListContractPublishHeads lists the complete deterministic publication-head
// namespace up to the protocol hard maximum. namespacePrefix is the branch
// prefix without refs/heads/ and must end in "op-v1-". A 257th head returns a
// non-exhaustive listing rather than silently truncating to absence.
func (h *GitHubHost) ListContractPublishHeads(
	ctx context.Context,
	repoDir string,
	remoteURL string,
	namespacePrefix string,
	credential Credential,
) (RemoteHeadListing, error) {
	const op = "ListContractPublishHeads"
	if repoDir == "" || remoteURL == "" || strings.HasPrefix(remoteURL, "-") ||
		!validContractPublishNamespace(namespacePrefix) {
		return RemoteHeadListing{}, recoveryRequestError(op)
	}
	if checked := runRecoveryGit(ctx, 1024, "check-ref-format", "refs/heads/"+namespacePrefix+strings.Repeat("0", 64)); checked.err != nil || checked.exitCode != 0 {
		return RemoteHeadListing{}, recoveryRequestError(op)
	}

	authArgs := GitAuthArgs(credential)
	args := append([]string{"-C", repoDir}, authArgs...)
	args = append(args, "ls-remote", "--heads", remoteURL, "refs/heads/"+namespacePrefix+"*")
	result := runRecoveryGit(ctx, maxRecoveryGitOutput, args...)
	if result.err != nil || result.exitCode != 0 {
		return RemoteHeadListing{}, recoveryGitError(op, "list remote publication heads", result.err)
	}
	listing, err := parseContractPublishHeadListing(result.stdout, namespacePrefix, MaxContractPublishRemoteHeads)
	if err != nil {
		return RemoteHeadListing{}, recoveryGitError(op, "invalid remote publication-head listing", err)
	}
	return listing, nil
}

func validContractPublishNamespace(prefix string) bool {
	if !utf8.ValidString(prefix) || len(prefix) > maxRecoveryPathBytes ||
		!strings.HasPrefix(prefix, "a2a/") ||
		!strings.HasSuffix(prefix, "/contract-publish/op-v1-") ||
		strings.ContainsAny(prefix, "*?[\\\x00\r\n\t ") {
		return false
	}
	parts := strings.Split(prefix, "/")
	return len(parts) == 4 && parts[0] == "a2a" && parts[1] != "" &&
		parts[2] == "contract-publish" && parts[3] == "op-v1-"
}

func parseContractPublishHeadListing(raw []byte, namespacePrefix string, limit int) (RemoteHeadListing, error) {
	if limit != MaxContractPublishRemoteHeads || !validContractPublishNamespace(namespacePrefix) ||
		!utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return RemoteHeadListing{}, fmt.Errorf("invalid publication-head listing input")
	}
	if len(raw) == 0 {
		return RemoteHeadListing{Heads: []RemoteHead{}, Exhaustive: true}, nil
	}
	if raw[len(raw)-1] != '\n' {
		return RemoteHeadListing{}, fmt.Errorf("publication-head listing is incomplete")
	}

	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	heads := make([]RemoteHead, 0, min(len(lines), limit))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || !validRecoveryGitOID(fields[0]) ||
			!strings.HasPrefix(fields[1], "refs/heads/"+namespacePrefix) {
			return RemoteHeadListing{}, fmt.Errorf("publication-head listing contains a malformed row")
		}
		branch := strings.TrimPrefix(fields[1], "refs/heads/")
		suffix := strings.TrimPrefix(branch, namespacePrefix)
		if len(suffix) != 64 || !lowerHex(suffix) {
			return RemoteHeadListing{}, fmt.Errorf("publication-head listing contains a noncanonical operation key")
		}
		if _, duplicate := seen[branch]; duplicate {
			return RemoteHeadListing{}, fmt.Errorf("publication-head listing repeats a branch")
		}
		seen[branch] = struct{}{}
		if len(heads) < limit {
			heads = append(heads, RemoteHead{Branch: branch, SHA: fields[0]})
		}
	}
	sort.Slice(heads, func(i, j int) bool { return heads[i].Branch < heads[j].Branch })
	return RemoteHeadListing{Heads: heads, Observed: len(lines), Exhaustive: len(lines) <= limit}, nil
}

func lowerHex(value string) bool {
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
