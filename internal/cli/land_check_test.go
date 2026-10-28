package cli

// Tests for NewLandCheck, the land check the local host runs before it moves a
// base branch. Like cmd_validate_ci_test.go these live in package cli: the
// claim under test is that the land check is the required check's own body
// (runValidateCI), so the tests drive both and compare them.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// landCheckCandidate builds a real two-commit checkout: base holds the manifest
// (when manifest is non-empty), the head commit adds files. It returns the
// candidate a host would hand the check: checked out at the head commit, with
// the base commit as BaseSHA.
func landCheckCandidate(t *testing.T, manifest string, files map[string]string, author string) host.LandCandidate {
	t.Helper()
	root := t.TempDir()
	ciTestGit(t, root, "init", "-q", "-b", "main")
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(root, "space.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatalf("write space.yaml: %v", err)
		}
	} else if err := os.WriteFile(filepath.Join(root, "README"), []byte("no manifest\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	ciTestGit(t, root, "add", "-A")
	ciTestGit(t, root, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(ciTestGit(t, root, "rev-parse", "HEAD"))
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	ciTestGit(t, root, "add", "-A")
	ciTestGit(t, root, "commit", "-q", "-m", "head")
	head := strings.TrimSpace(ciTestGit(t, root, "rev-parse", "HEAD"))
	return host.LandCandidate{
		CheckoutDir: root, BaseBranch: "main", BaseSHA: base, MergeSHA: head, HeadSHA: head, Author: author,
	}
}

// requiredCheckCodes runs the required check's own body over the same checkout
// and returns every violation code it reports, sorted and deduplicated.
func requiredCheckCodes(t *testing.T, c host.LandCandidate) (int, []string) {
	t.Helper()
	code, rep, _ := runCI(t, ciEngine(t), c.CheckoutDir, gitDiffNameOnly, "v3-pr", c.BaseSHA, c.Author)
	return code, reportCodes(rep)
}

// reportCodes lists every violation code a required-check report carries,
// sorted and deduplicated. It reads the report independently of the land
// check's own decoder, so the two can disagree.
func reportCodes(rep ciReport) []string {
	seen := map[string]bool{}
	var codes []string
	add := func(code string) {
		if code != "" && !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	for _, a := range rep.Artifacts {
		if a.Result != nil {
			for _, v := range a.Result.Violations {
				add(v.Code)
			}
		}
	}
	for _, d := range rep.DiffAuthz {
		add(d.Code)
	}
	sort.Strings(codes)
	return codes
}

func TestLandCheck_SameCodesAsValidateCI(t *testing.T) {
	t.Parallel()
	ownID := "XQ-axon-20260730-h2k8"
	otherID := "XQ-seomatrix-20260730-h2k8"
	ownPath := "axon/exchanges/" + ownID + ".md"
	otherPath := "seomatrix/exchanges/" + otherID + ".md"

	cases := []struct {
		name      string
		author    string // the write's author; empty means ydnikolaev, axon's owner
		files     map[string]string
		wantOK    bool
		wantCodes []string // codes the required check itself reports, anchored independently
	}{
		{
			name:   "own section, valid artifact lands",
			files:  map[string]string{ownPath: validQuestion(ownID, "axon", "seomatrix")},
			wantOK: true,
		},
		{
			name:      "an author mapped to no system is refused with AUTHZ-001",
			author:    "stranger",
			files:     map[string]string{ownPath: validQuestion(ownID, "axon", "seomatrix")},
			wantCodes: []string{"AUTHZ-001"},
		},
		{
			name:      "another system's section is refused with AUTHZ-001",
			files:     map[string]string{otherPath: validQuestion(otherID, "seomatrix", "axon")},
			wantCodes: []string{"AUTHZ-001"},
		},
		{
			name: "an artifact the schema rejects carries the schema code, and a cross-section write adds AUTHZ-001",
			files: map[string]string{
				ownPath:   strings.Replace(validQuestion(ownID, "axon", "seomatrix"), "priority: p2\n", "", 1),
				otherPath: validQuestion(otherID, "seomatrix", "axon"),
			},
			wantCodes: []string{"AUTHZ-001", "SCH-001"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			author := tc.author
			if author == "" {
				author = "ydnikolaev"
			}
			c := landCheckCandidate(t, ciSpaceYAML, tc.files, author)
			exit, wantCodes := requiredCheckCodes(t, c)
			if tc.wantOK != (exit == 0) {
				t.Fatalf("fixture drifted: runValidateCI exit = %d, wantOK = %v", exit, tc.wantOK)
			}
			if tc.wantCodes != nil && !reflect.DeepEqual(wantCodes, tc.wantCodes) {
				t.Fatalf("fixture drifted: runValidateCI codes = %v, want %v", wantCodes, tc.wantCodes)
			}

			verdict, err := NewLandCheck(ciEngine(t))(context.Background(), c)
			if err != nil {
				t.Fatalf("land check: %v", err)
			}
			if verdict.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v (verdict %+v)", verdict.OK, tc.wantOK, verdict)
			}
			got := append([]string(nil), verdict.Codes...)
			sort.Strings(got)
			if (len(got) != 0 || len(wantCodes) != 0) && !reflect.DeepEqual(got, wantCodes) {
				t.Fatalf("Codes = %v, want the required check's own codes %v", got, wantCodes)
			}
			if !tc.wantOK && verdict.Detail == "" {
				t.Fatalf("a refusal carries no Detail; the operator cannot see why: %+v", verdict)
			}
		})
	}

	t.Run("the refusal names the path and the code the operator must fix", func(t *testing.T) {
		t.Parallel()
		c := landCheckCandidate(t, ciSpaceYAML, map[string]string{otherPath: validQuestion(otherID, "seomatrix", "axon")}, "ydnikolaev")
		verdict, err := NewLandCheck(ciEngine(t))(context.Background(), c)
		if err != nil {
			t.Fatalf("land check: %v", err)
		}
		if !strings.Contains(verdict.Detail, "AUTHZ-001") || !strings.Contains(verdict.Detail, otherPath) {
			t.Fatalf("Detail = %q, want it to name AUTHZ-001 and %s", verdict.Detail, otherPath)
		}
	})
}

func TestLandCheck_ExitOneWithoutReportIsError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		make func(t *testing.T) host.LandCandidate
	}{
		{"missing space.yaml", func(t *testing.T) host.LandCandidate {
			return landCheckCandidate(t, "", map[string]string{"axon/note.txt": "x\n"}, "ydnikolaev")
		}},
		{"unparseable space.yaml", func(t *testing.T) host.LandCandidate {
			return landCheckCandidate(t, "schema: [unclosed\n", map[string]string{"axon/note.txt": "x\n"}, "ydnikolaev")
		}},
		{"git cannot diff against the base", func(t *testing.T) host.LandCandidate {
			c := landCheckCandidate(t, ciSpaceYAML, map[string]string{"axon/note.txt": "x\n"}, "ydnikolaev")
			c.BaseSHA = strings.Repeat("0", 40)
			return c
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := tc.make(t)
			// The premise of the test: the required check exits 1 and prints no
			// report on stdout for this checkout.
			var out, errBuf strings.Builder
			exit := runValidateCI(context.Background(), ciEngine(t), c.CheckoutDir, gitDiffNameOnly, "v3-pr", c.BaseSHA, c.Author, IO{Stdout: &out, Stderr: &errBuf})
			if exit != 1 || out.Len() != 0 {
				t.Fatalf("fixture drifted: runValidateCI exit = %d, stdout = %q, want exit 1 with empty stdout", exit, out.String())
			}

			verdict, err := NewLandCheck(ciEngine(t))(context.Background(), c)
			if err == nil {
				t.Fatalf("land check returned verdict %+v and no error; a check that could not judge must not pass or refuse", verdict)
			}
			if !reflect.DeepEqual(verdict, host.LandVerdict{}) {
				t.Fatalf("verdict = %+v alongside error %v, want the zero verdict", verdict, err)
			}
			if !strings.Contains(err.Error(), "could not judge") {
				t.Fatalf("error = %q, want it to say the check could not judge", err)
			}
		})
	}
}

func TestDecodeLandCheckRun(t *testing.T) {
	t.Parallel()
	report := func(r ciReport) []byte {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	artifactViolation := func(path string, codes ...string) validateReport {
		res := validate.Result{Valid: false}
		for _, code := range codes {
			res.Violations = append(res.Violations, validate.Violation{Code: code, Path: "title", Message: "bad " + code})
		}
		return validateReport{Path: path, Result: &res}
	}
	authz := ciAuthzViolation{Code: "AUTHZ-001", Path: "seomatrix/x.md", Severity: "reject", Author: "a", Message: "outside"}

	cases := []struct {
		name      string
		exit      int
		stdout    []byte
		stderr    string
		wantOK    bool
		wantCodes []string
		wantErr   string // substring; empty means no error
	}{
		{name: "exit 0 with a report is OK", exit: 0, stdout: report(ciReport{Mode: "v3-pr", Valid: true, Artifacts: []validateReport{}}), wantOK: true},
		{name: "exit 0 with nothing decodable is an error", exit: 0, stdout: []byte("not json"), wantErr: "could not judge"},
		{name: "exit 0 with empty stdout is an error", exit: 0, wantErr: "could not judge"},
		{
			name: "exit 1 with violations lists their codes in order, once each", exit: 1,
			stdout: report(ciReport{Mode: "v3-pr", Artifacts: []validateReport{
				artifactViolation("a.md", "SCH-007", "REF-001"), artifactViolation("b.md", "SCH-007"),
			}, DiffAuthz: []ciAuthzViolation{authz}}),
			wantCodes: []string{"SCH-007", "REF-001", "AUTHZ-001"},
		},
		{name: "exit 1 with only a diff-authz violation", exit: 1, stdout: report(ciReport{Mode: "v3-pr", DiffAuthz: []ciAuthzViolation{authz}}), wantCodes: []string{"AUTHZ-001"}},
		{name: "exit 1 with a decodable report and zero violations is an error", exit: 1, stdout: report(ciReport{Mode: "v3-pr", Artifacts: []validateReport{}}), wantErr: "could not judge"},
		{name: "exit 1 with nothing on stdout is an error carrying stderr", exit: 1, stderr: "validate --ci: boom\n", wantErr: "validate --ci: boom"},
		{name: "exit 1 with undecodable stdout is an error", exit: 1, stdout: []byte("{"), wantErr: "could not judge"},
		{name: "exit 2 (usage) is an error even with a report", exit: 2, stdout: report(ciReport{Mode: "v3-pr", DiffAuthz: []ciAuthzViolation{authz}}), stderr: "usage: x", wantErr: "usage: x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			verdict, err := decodeLandCheckRun(tc.exit, tc.stdout, tc.stderr)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one containing %q (verdict %+v)", err, tc.wantErr, verdict)
				}
				if !reflect.DeepEqual(verdict, host.LandVerdict{}) {
					t.Fatalf("verdict = %+v with an error, want zero", verdict)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want none", err)
			}
			if verdict.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v", verdict.OK, tc.wantOK)
			}
			if (len(verdict.Codes) != 0 || len(tc.wantCodes) != 0) && !reflect.DeepEqual(verdict.Codes, tc.wantCodes) {
				t.Fatalf("Codes = %v, want %v", verdict.Codes, tc.wantCodes)
			}
			if !tc.wantOK && verdict.Detail == "" {
				t.Fatalf("a refusal has no Detail")
			}
		})
	}

	t.Run("Detail is bounded", func(t *testing.T) {
		t.Parallel()
		var arts []validateReport
		for i := 0; i < 400; i++ {
			arts = append(arts, artifactViolation("dir/"+strings.Repeat("p", 60)+".md", "SCH-007"))
		}
		verdict, err := decodeLandCheckRun(1, report(ciReport{Mode: "v3-pr", Artifacts: arts}), "")
		if err != nil {
			t.Fatal(err)
		}
		if len(verdict.Detail) > landCheckDetailLimit+64 {
			t.Fatalf("Detail is %d bytes, want it bounded near %d", len(verdict.Detail), landCheckDetailLimit)
		}
	})
}
