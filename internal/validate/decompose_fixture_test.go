package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"gopkg.in/yaml.v3"
)

func TestDecomposeFixturesAreOneThreeIntentThread(t *testing.T) {
	t.Parallel()
	const thread = "thread:axon-20260729-d3c0"
	fixtures := map[string]string{
		"XA-axon-20260729-d3c1.md": "announcement",
		"XQ-axon-20260729-d3c2.md": "question",
		"XW-axon-20260729-d3c3.md": "work_request",
	}
	seenTitles := map[string]bool{}
	for name, wantType := range fixtures {
		raw, err := os.ReadFile(filepath.Join("../../schemas/envelope/v1/fixtures/valid", name))
		if err != nil {
			t.Fatal(err)
		}
		fm, err := artifact.ParseFrontmatter(raw)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			ID     string `yaml:"id"`
			Type   string `yaml:"type"`
			Title  string `yaml:"title"`
			Thread string `yaml:"thread"`
		}
		if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.ID+".md" != name || doc.Type != wantType || doc.Thread != thread {
			t.Errorf("%s decoded as %+v, want type=%s thread=%s", name, doc, wantType, thread)
		}
		if doc.Title == "" || seenTitles[doc.Title] {
			t.Errorf("%s does not carry a distinct single intent title: %q", name, doc.Title)
		}
		seenTitles[doc.Title] = true
	}
}
