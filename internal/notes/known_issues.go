package notes

import (
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"
)

// currentKnownIssuesPath is deliberately singular: current limitations have
// one add/remove surface rather than one copy per release.
const currentKnownIssuesPath = "current/known-issues.yaml"

type currentKnownIssuesDocument struct {
	Schema string   `yaml:"schema"`
	Issues []Change `yaml:"issues"`
}

// LoadCurrentKnownIssues reads the standing known-issues document. Release
// history stays version-keyed; this list is current state and is therefore
// queried independently of any --since range.
func LoadCurrentKnownIssues(fsys fs.FS) ([]Change, error) {
	const op = "LoadCurrentKnownIssues"

	raw, err := fs.ReadFile(fsys, currentKnownIssuesPath)
	if err != nil {
		return nil, &Error{Op: op, Input: currentKnownIssuesPath, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
	}
	var doc currentKnownIssuesDocument
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, &Error{Op: op, Input: currentKnownIssuesPath, Err: ErrKnownIssuesInvalid}
	}
	return append([]Change(nil), doc.Issues...), nil
}

// AttachCurrentKnownIssues adds standing limitations to the newest selected
// release without changing the established []ReleaseNotes machine contract.
// If the strict version range is empty, the newest embedded release is used as
// a metadata carrier containing only the standing issues.
func AttachCurrentKnownIssues(selected, all []ReleaseNotes, issues []Change) []ReleaseNotes {
	if len(issues) == 0 {
		return selected
	}

	out := append([]ReleaseNotes(nil), selected...)
	if len(out) == 0 {
		if len(all) == 0 {
			return out
		}
		carrier := all[len(all)-1]
		carrier.Changes = nil
		out = append(out, carrier)
	}

	last := len(out) - 1
	out[last].Changes = append([]Change(nil), out[last].Changes...)
	seen := make(map[string]struct{}, len(out[last].Changes))
	for _, change := range out[last].Changes {
		seen[change.ID] = struct{}{}
	}
	for _, issue := range issues {
		if _, duplicate := seen[issue.ID]; duplicate {
			continue
		}
		out[last].Changes = append(out[last].Changes, issue)
		seen[issue.ID] = struct{}{}
	}
	return out
}
