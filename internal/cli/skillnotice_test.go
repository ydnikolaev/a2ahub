package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSkillNotice(t *testing.T) {
	t.Parallel()

	embedded := fstest.MapFS{
		"a2ahub/SKILL.md":         {Data: []byte("new manual")},
		"a2ahub/reference/ops.md": {Data: []byte("ops")},
	}
	same := map[string]string{"SKILL.md": "new manual", "reference/ops.md": "ops"}
	stale := map[string]string{"SKILL.md": "old manual", "reference/ops.md": "ops"}

	cases := []struct {
		name       string
		disk       map[string]string
		provenance string
		binary     string
		want       string
	}{
		{"nothing installed", nil, "", "0.27.1", ""},
		{"a tree a2ahub does not own", stale, "someone else's notes", "0.27.1", ""},
		{"an equal tree is silent", same, skillProvenance("0.27.1"), "0.27.1", ""},
		{"an old stamp over an equal tree owes nothing", same, skillProvenance("0.26.3"), "0.27.1", ""},
		{"an old stamp over a stale tree", stale, skillProvenance("0.26.3"), "0.27.1", "1 file(s)"},
		{"a local build's equal stamp over a stale tree", stale, skillProvenance("0.27.1"), "0.27.1", "1 file(s)"},
		{"a stamp newer than the binary is not this binary's to move", stale, skillProvenance("0.28.0"), "0.27.1", ""},
		{"a dev binary has no version to compare", stale, skillProvenance("0.27.1"), "dev", ""},
		{"a dev stamp has no version to compare", stale, skillProvenance("dev"), "0.27.1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for rel, body := range tc.disk {
				path := filepath.Join(root, ".a2ahub", "skill", filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.provenance != "" {
				path := filepath.Join(root, ".a2ahub", "skill", skillProvenanceFile)
				if err := os.WriteFile(path, []byte(tc.provenance), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := SkillNotice(embedded, root, ".a2ahub/skill", tc.binary)
			if tc.want == "" && got != "" {
				t.Fatalf("SkillNotice = %q, want silence", got)
			}
			if tc.want != "" && (!strings.Contains(got, tc.want) || !strings.Contains(got, "a2a skill install")) {
				t.Fatalf("SkillNotice = %q, want it to name %q and the remedy", got, tc.want)
			}
		})
	}
}

func TestWriteSkillAdvisory(t *testing.T) {
	t.Parallel()

	var silent bytes.Buffer
	WriteSkillAdvisory(IO{Stderr: &silent}, "", false)
	WriteSkillAdvisory(IO{Stderr: &silent}, "", true)
	if silent.Len() != 0 {
		t.Fatalf("no sentence wrote %q", silent.String())
	}

	var text bytes.Buffer
	WriteSkillAdvisory(IO{Stderr: &text}, "stale", false)
	if text.String() != "note: stale\n" {
		t.Fatalf("text = %q", text.String())
	}

	var out bytes.Buffer
	WriteSkillAdvisory(IO{Stderr: &out}, "stale", true)
	var decoded struct {
		SkillNotice string `json:"skill_notice"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded.SkillNotice != "stale" {
		t.Fatalf("json = %q, err %v", out.String(), err)
	}
}
