package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// maxDigestTreeFileBytes bounds every leaf file read while building a §5.7
// multi-file digest tree — this package's own copy of the repo-wide bounded-
// read idiom (each package owns its own cap constant; see e.g.
// internal/cli/adapters.go's maxMirrorEventBytes, internal/cache/mirror.go's
// maxCacheReadBytes, internal/space/config.go's maxConfigBytes — all 1 MiB,
// none shared across package boundaries).
const maxDigestTreeFileBytes = 1 << 20 // 1 MiB

// DigestTreeFS computes the spec 08 §5.7/D-029 multi-file digest tree over
// root's subtrees (e.g. []string{"schema", "fixtures"} — a contract's own
// contract.md is excluded by construction, simply by not being under any
// named subtree) as currently present on the local filesystem. It returns
// the combined digest (CombineDigestPairs) alongside the per-file digest
// map (root-relative, slash-separated paths) for callers that also need
// leaf-level detail (contract diff's own added/removed/changed view).
//
// This is the ONE impl `contract publish`/`contract diff`/`contract
// verify-export` all call (spec 08 §5/§5.7, D-029: "one digest tree impl,
// reused" — MED-5 fix-wave finding) — internal/cli's cmd_contract.go no
// longer carries its own file-private copy. subtrees is the caller's own
// argument (not hardcoded here) so this stays generic for any future
// consumer (P12's axon-CI, per D-029) that may need a different subtree
// list.
func DigestTreeFS(root string, subtrees []string) (digest string, perFile map[string]string, err error) {
	perFile = map[string]string{}
	for _, sub := range subtrees {
		dir := filepath.Join(root, sub)
		info, statErr := os.Stat(dir)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return "", nil, statErr
		}
		if !info.IsDir() {
			continue
		}
		walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				return nil
			}
			raw, rerr := readBoundedDigestFile(p)
			if rerr != nil {
				return rerr
			}
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil {
				return relErr
			}
			perFile[filepath.ToSlash(rel)] = Digest(raw)
			return nil
		})
		if walkErr != nil {
			return "", nil, walkErr
		}
	}
	return CombineDigestPairs(perFile), perFile, nil
}

// CombineDigestPairs is §5.7's exact algorithm: "SHA-256 over the sorted
// list of (contract-root-relative-path, sha256(file-bytes)) pairs".
func CombineDigestPairs(perFile map[string]string) string {
	paths := make([]string, 0, len(perFile))
	for p := range perFile {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write([]byte(perFile[p]))
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// readBoundedDigestFile reads path with the same LimitReader-plus-explicit-
// cap-check idiom every other package's own bounded read uses (see this
// file's maxDigestTreeFileBytes doc comment) — never an unbounded
// os.ReadFile on a leaf that could, in principle, be attacker- or
// mistake-sized.
func readBoundedDigestFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // reason: read-only fd, close error is not actionable here

	raw, err := io.ReadAll(io.LimitReader(f, maxDigestTreeFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxDigestTreeFileBytes {
		return nil, fmt.Errorf("artifact: %s exceeds %d byte read bound", path, maxDigestTreeFileBytes)
	}
	return raw, nil
}
