package feedback

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ydnikolaev/a2ahub/schemas"
	"gopkg.in/yaml.v3"
)

// ValidKinds mirrors feedback.schema.json's closed `kind` enum (§2.2).
var ValidKinds = map[string]bool{
	"bug": true, "feature": true, "docs": true, "friction": true, "protocol": true,
}

// Drafter materializes `a2a feedback new <kind> [--title]` (§T1, §11 A4):
// its own go:embed YAML template (schemas/feedback/v1/template.yaml),
// NOT internal/template's envelope Markdown Render.
type Drafter struct {
	draftsDir string // e.g. ".a2a/feedback" — the staging convention (§T1)
	version   string // bare a2a_version (ground-truth seam: NOT versionStamp())

	now          func() time.Time
	entropy      io.Reader
	readTemplate func() ([]byte, error)
	mkdirAll     func(path string, perm os.FileMode) error
	writeFile    func(path string, data []byte, perm os.FileMode) error
}

// NewDrafter constructs a Drafter. draftsDir is `.a2a/feedback`'s path
// (repo-root-relative or absolute, caller's choice); version is the bare
// `a2a` binary version to auto-fill into context.a2a_version.
func NewDrafter(draftsDir, version string) *Drafter {
	return &Drafter{
		draftsDir:    draftsDir,
		version:      version,
		now:          time.Now,
		entropy:      rand.Reader,
		readTemplate: defaultReadTemplate,
		mkdirAll:     os.MkdirAll,
		writeFile:    os.WriteFile,
	}
}

func defaultReadTemplate() ([]byte, error) {
	return schemas.FS.ReadFile("feedback/v1/template.yaml")
}

// SetClockForTest overrides the injected clock (test-only DI seam, rails
// anti-pattern #10: production always uses NewDrafter's own time.Now).
func (d *Drafter) SetClockForTest(now func() time.Time) { d.now = now }

// SetEntropyForTest overrides the injected entropy source.
func (d *Drafter) SetEntropyForTest(entropy io.Reader) { d.entropy = entropy }

// SetReadTemplateForTest overrides the injected template reader.
func (d *Drafter) SetReadTemplateForTest(f func() ([]byte, error)) { d.readTemplate = f }

// Draft mints a fresh id, fills the embedded template's id/kind/title/
// context.a2a_version/context.os_arch (checks.* and status stay the
// template's own all-false/"new" defaults — I5: the agent flips each gate
// consciously, never bulk-set), writes it to
// <draftsDir>/<id>.yaml, and returns that path.
func (d *Drafter) Draft(kind, title string) (string, error) {
	const op = "Draft"
	if !ValidKinds[kind] {
		return "", fmt.Errorf("feedback: %s: unknown kind %q", op, kind)
	}

	raw, err := d.readTemplate()
	if err != nil {
		return "", fmt.Errorf("feedback: %s: %w", op, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("feedback: %s: %w", op, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("feedback: %s: malformed template", op)
	}

	id, err := MintFeedbackID(d.now(), d.entropy)
	if err != nil {
		return "", fmt.Errorf("feedback: %s: %w", op, err)
	}
	osArch := runtime.GOOS + "/" + runtime.GOARCH
	applyDraftFills(doc.Content[0], id, kind, title, d.version, osArch)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", fmt.Errorf("feedback: %s: %w", op, err)
	}

	if err := d.mkdirAll(d.draftsDir, 0o755); err != nil {
		return "", fmt.Errorf("feedback: %s: %w", op, err)
	}
	path := filepath.Join(d.draftsDir, id+".yaml")
	if err := d.writeFile(path, out, 0o644); err != nil {
		return "", fmt.Errorf("feedback: %s: %w", op, err)
	}
	return path, nil
}

// applyDraftFills walks the template's top-level mapping in place, filling
// id/kind/title/context.a2a_version/context.os_arch — every other field
// (severity, summary, evidence, checks, status) is left at the template's
// own already-schema-shaped placeholder/default.
func applyDraftFills(mapping *yaml.Node, id, kind, title, version, osArch string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		val := mapping.Content[i+1]
		switch key.Value {
		case "id":
			setScalar(val, id)
		case "kind":
			setScalar(val, kind)
		case "title":
			if title != "" {
				setScalar(val, title)
			}
		case "context":
			fillContext(val, version, osArch)
		}
	}
}

func fillContext(node *yaml.Node, version, osArch string) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		val := node.Content[i+1]
		switch key.Value {
		case "a2a_version":
			setScalar(val, version)
		case "os_arch":
			setScalar(val, osArch)
		}
	}
}

// setScalar overwrites node's value, clearing its style/tag so the
// encoder re-infers the correct emission form for the new content (same
// idiom as internal/template.setScalar — kept as its own copy: A4 pins
// feedback's template materialization as its OWN path, not a reuse of
// internal/template's envelope Render).
func setScalar(node *yaml.Node, value string) {
	node.Value = value
	node.Style = 0
	node.Tag = ""
}
