package space

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowVersion reads the immutable reusable-workflow ref already committed
// in a space mirror — the tag a space PINS, which is a separate compatibility
// axis from space.yaml's min_binary_version. A space can satisfy its own write
// floor while pinning a template several releases old; that is exactly how a
// space's floor sits still while releases go past it.
//
// It lives here rather than in internal/html because two surfaces now need the
// same fact and neither may own it: the dashboard renders it, and `a2a version`
// / `a2a doctor` report it. ADR-004 — a shared concern moves DOWN into a
// package both importers already have, never ACROSS. internal/html's own
// import-set decision record already names `space` as the home for "read-only
// mirror/history helpers".
//
// Malformed or absent workflows degrade to EMPTY facts, never to a guess: a
// caller must label the axis unavailable rather than infer it from the binary
// or the template. A space whose jobs disagree returns the literal "mixed"
// with every ref joined — the split-pair shape the publisher's own projection
// gate exists to refuse, and a reader that silently picked the first one would
// hide it.
func WorkflowVersion(dir string) (version, ref string) {
	raw, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "a2a-validate.yml")) //nolint:gosec // reason: dir is a space mirror path this process already cloned and owns; the filename is a constant, so the only variable part is the mirror root the caller resolved.
	if err != nil {
		return "", ""
	}
	var workflow struct {
		Jobs map[string]struct {
			Uses string `yaml:"uses"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		return "", ""
	}
	var refs []string
	for _, job := range workflow.Jobs {
		if strings.Contains(job.Uses, "a2a-validate-reusable.yml@") {
			refs = append(refs, job.Uses)
		}
	}
	if len(refs) == 0 {
		return "", ""
	}
	sort.Strings(refs)
	ref = refs[0]
	for _, candidate := range refs[1:] {
		if candidate != ref {
			return "mixed", strings.Join(refs, ", ")
		}
	}
	at := strings.LastIndexByte(ref, '@')
	if at < 0 || at == len(ref)-1 {
		return "", ref
	}
	version = strings.TrimPrefix(ref[at+1:], "v")
	return version, ref
}
