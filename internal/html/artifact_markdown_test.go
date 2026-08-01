package html

import (
	"strings"
	"testing"
)

func TestRenderArtifactMarkdown_GFMWithoutExecutableContent(t *testing.T) {
	t.Parallel()
	source := "# Heading\n\n**bold** and ~~old~~; applying from <{residence}>\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n" +
		"<script>alert('raw')</script>\n\n[x](javascript:alert(1))\n\n![pixel](https://evil.test/pixel.png)\n"

	got, err := renderArtifactMarkdown(source)
	if err != nil {
		t.Fatalf("renderArtifactMarkdown: %v", err)
	}
	for _, want := range []string{"<h1>Heading</h1>", "<strong>bold</strong>", "<del>old</del>", "&lt;{residence}&gt;", "&lt;script&gt;", "<table>", "artifact-image-alt", "pixel"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered Markdown is missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"<script", "javascript:", "<img", "evil.test"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("rendered Markdown contains unsafe %q:\n%s", forbidden, got)
		}
	}
}
