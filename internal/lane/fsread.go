package lane

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// fsread.go is internal/lane's single filesystem-read seam and single
// subprocess-launch seam. Every os.ReadFile/os.Open call and the package's
// one exec.Command call in the LINTED package live HERE, and nowhere else
// — so a gosec G304 ("file included via variable") or G204 ("subprocess
// launched with variable") finding lands on exactly this file, and the
// .golangci.yml allowlist (an exact-file list, not a package-wide
// suppression — see the comment above the G204/G304 rules there) needs
// exactly one internal/lane entry instead of six. (lanecheck.go's own
// os.Open is a separate case: `//go:build ignore` + `package main` keeps it
// out of `go build`/lint entirely, so it never reaches gosec to find — it
// is deliberately not funneled here, not an oversight.)
//
// Do not add a new os.ReadFile, os.Open, or exec.Command call anywhere else
// in this package. Route it through readRepoFile or gitLsFiles below, or
// add a narrowly-named third helper here if neither fits — never leave the
// raw call at the new site.

// readRepoFile reads a file rooted at root, optionally joined with further
// path components (readRepoFile(root, "scripts", "verify.sh")), or a
// single already-resolved path (readRepoFile(path)). Callers that compute
// their own full path (filepath.Glob, filepath.WalkDir matches) pass it as
// the sole argument; callers that know root plus a fixed relative address
// pass the pieces separately.
func readRepoFile(root string, rel ...string) ([]byte, error) {
	path := filepath.Join(append([]string{root}, rel...)...)
	return os.ReadFile(path)
}

// gitLsFiles runs `git ls-files` in root and returns every tracked path,
// repo-relative with "/" separators. Fixed argv, resolved via exec.Command's
// normal PATH lookup of the "git" binary — never a shell, never
// attacker-controlled arguments beyond root itself.
func gitLsFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git -C %s ls-files: %w", root, err)
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}
