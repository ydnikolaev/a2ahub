package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/go-foundation/units/adoption"
)

// adoptionCorpus is two releases, each with one change that obliges the
// reader and one that does not, plus a change authored ahead of 0.2.0.
func adoptionCorpus() []adoption.Release {
	owed := func(id string) adoption.Change {
		return adoption.Change{ID: id, Kind: "feat", Impact: "normal", Subject: id, Action: adoption.Action{Scope: "local", Why: "do it"}}
	}
	free := func(id string) adoption.Change {
		return adoption.Change{ID: id, Kind: "fix", Impact: "low", Subject: id, Action: adoption.Action{Scope: "none", Why: "nothing"}}
	}
	return []adoption.Release{
		{Version: "0.1.0", Released: "2026-01-01", Changes: []adoption.Change{owed("RN-1"), free("RN-2")}},
		{Version: "0.2.0", Released: "2026-02-01", Changes: []adoption.Change{owed("RN-3"), free("RN-4")}},
		{Version: "0.3.0", Released: adoption.Unreleased, Changes: []adoption.Change{owed("RN-5")}},
	}
}

// The binary is per machine and adoption is per repository, so the notice is
// the only thing that tells a repository the binary moved under it
// (D-043). Each row is one state of the repository's own clock, read through
// a2a's own policy and verbs.
func TestAdoptionNotice(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		through string
		ahead   []string
		binary  string
		want    []string
		banned  []string
	}{
		{name: "behind names the count, the baseline and both steps", through: "0.1.0", binary: "0.2.0",
			want: []string{"2 adoption step(s) pending since v0.1.0", "v0.2.0", "`a2a adapt`", "`a2a adapt --done`"}},
		{name: "never recorded says so rather than claiming a baseline", through: "", binary: "0.2.0",
			want: []string{"never recorded adoption", "3 step(s) since v0.1.0", "`a2a adapt`"}, banned: []string{"pending since"}},
		{name: "a change adopted ahead of its release is not owed again", through: "0.2.0", ahead: []string{"RN-5"}, binary: "0.2.0",
			want: nil},
		{name: "a change authored ahead and not yet adopted is owed", through: "0.2.0", binary: "0.2.0",
			want: []string{"1 adoption step(s) pending since v0.2.0"}},
		{name: "ahead of the binary names the version and the executable", through: "0.9.0", binary: "0.2.0",
			want: []string{"adapted through v0.9.0", "newer than this binary (v0.2.0 at /opt/a2a)", "update a2a", "PATH"}},
		{name: "a dev build cannot judge and stays silent", through: "0.1.0", binary: "dev", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := adoption.Notice(adoptionCorpus(), nil, adoption.Clock{Through: tc.through, Ahead: tc.ahead}, tc.binary, "/opt/a2a", notes.Policy(), notes.Verbs())
			if tc.want == nil {
				if got != "" {
					t.Fatalf("want silence, got %q", got)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("notice %q lacks %q", got, w)
				}
			}
			for _, b := range tc.banned {
				if strings.Contains(got, b) {
					t.Errorf("notice %q carries %q", got, b)
				}
			}
		})
	}
}

func TestWriteAdoptionAdvisory(t *testing.T) {
	t.Parallel()
	var human bytes.Buffer
	WriteAdoptionAdvisory(IO{Stderr: &human}, "owed", false)
	if human.String() != "note: owed\n" {
		t.Fatalf("human = %q", human.String())
	}
	var machine bytes.Buffer
	WriteAdoptionAdvisory(IO{Stderr: &machine}, "owed", true)
	var obj map[string]string
	if err := json.Unmarshal(machine.Bytes(), &obj); err != nil || obj["adoption_notice"] != "owed" {
		t.Fatalf("json = %q (%v)", machine.String(), err)
	}
	var none bytes.Buffer
	WriteAdoptionAdvisory(IO{Stderr: &none}, "", true)
	if none.Len() != 0 {
		t.Fatalf("an empty notice must write nothing, got %q", none.String())
	}
}
