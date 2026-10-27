package host

import (
	"fmt"
	"path/filepath"
	"strings"
)

// locatorClass is the address class of a space URL. It is deliberately
// unexported: code outside this package cannot read it, so it cannot branch on
// which provider a space uses. A caller that needs to know what a host can do
// asks Host.Capabilities, never the locator.
type locatorClass int

const (
	// classURL is every address that is not a filesystem path: https://,
	// http://, ssh://, scp-style git@host:o/r, and bare owner/name slices.
	classURL locatorClass = iota
	// classPath is an absolute filesystem path or a file:// URL.
	classPath
)

// Locator is the result of classifying one space URL. Raw is the string as
// configured; Repo is its owner/name identity. The address class and the
// absolute path are unexported on purpose (see locatorClass).
type Locator struct {
	Raw  string
	Repo Repo

	class locatorClass
	// path is the absolute filesystem path of a path-class locator, with any
	// file:// scheme stripped; empty for the URL class.
	path string
}

// ParseLocator classifies raw and extracts its owner/name identity. It is
// pure: no I/O and no environment.
//
// Identity is always the legacy last-two-segments slice, so a bare path still
// parses to a non-empty owner/name pair (the wire tier depends on it, see
// docs/validator-backlog.md). It is deliberately NOT guarded on a github.com
// prefix: a GitHub Enterprise URL or a local-path fixture origin parses too.
// Only the class is new.
func ParseLocator(raw string) (Locator, error) {
	s := strings.TrimSuffix(raw, ".git")
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "git@github.com:")
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
		return Locator{}, fmt.Errorf("cannot parse owner/name from repo URL %q", raw)
	}
	loc := Locator{
		Raw:  raw,
		Repo: Repo{Owner: parts[len(parts)-2], Name: parts[len(parts)-1]},
	}
	if p, ok := localPath(raw); ok {
		loc.class = classPath
		loc.path = p
	}
	return loc, nil
}

// localPath reports whether raw addresses the local filesystem and returns the
// path: an absolute path as-is, or a file:// URL with its scheme (and any host
// part) removed.
func localPath(raw string) (string, bool) {
	if rest, ok := strings.CutPrefix(raw, "file://"); ok {
		// file://<host>/<path>: the host part, when present, ends at the
		// first slash; file:///<path> has an empty host.
		if i := strings.Index(rest, "/"); i >= 0 {
			return rest[i:], true
		}
		return "", false
	}
	if filepath.IsAbs(raw) {
		return raw, true
	}
	return "", false
}
