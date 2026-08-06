package lane

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var denyDirsRe = regexp.MustCompile(`^DENY_DIRS=\(\s*(.*?)\s*\)`)

// denyDirs reads scripts/classify-guard.sh's DENY_DIRS array — READ, never
// re-listed: a hardcoded second copy is exactly the defect class this
// phase exists to remove (ground truth 5 / D-3).
func denyDirs(root string) ([]string, error) {
	raw, err := readRepoFile(root, "scripts", "classify-guard.sh")
	if err != nil {
		return nil, fmt.Errorf("read scripts/classify-guard.sh: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if m := denyDirsRe.FindStringSubmatch(line); m != nil {
			return strings.Fields(m[1]), nil
		}
	}
	return nil, fmt.Errorf("scripts/classify-guard.sh: no DENY_DIRS=( … ) array found")
}

// Universe returns the coverage universe: `git ls-files` union a walk of
// the private harness roots named by classify-guard.sh's DENY_DIRS array,
// each SKIPPED (not refused) when absent — a public checkout does not
// carry them by design.
func Universe(root string) ([]string, error) {
	tracked, err := gitLsFiles(root)
	if err != nil {
		return nil, err
	}
	dirs, err := denyDirs(root)
	if err != nil {
		return nil, err
	}

	set := map[string]bool{}
	for _, p := range tracked {
		set[p] = true
	}
	for _, dir := range dirs {
		present, walked, err := walkPresentRoot(root, dir)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		for _, p := range walked {
			set[p] = true
		}
	}

	universe := make([]string, 0, len(set))
	for p := range set {
		universe = append(universe, p)
	}
	sort.Strings(universe)
	return universe, nil
}

// walkPresentRoot walks root/dir (a DENY_DIRS entry) and returns every
// regular file found, repo-relative with "/" separators. It reports
// present=false, with no error, when the directory does not exist at all.
func walkPresentRoot(root, dir string) (present bool, files []string, err error) {
	full := filepath.Join(root, dir)
	info, statErr := os.Stat(full)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("stat %s: %w", full, statErr)
	}
	if !info.IsDir() {
		return false, nil, fmt.Errorf("%s exists but is not a directory", full)
	}
	err = filepath.Walk(full, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fi.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return false, nil, err
	}
	return true, files, nil
}
