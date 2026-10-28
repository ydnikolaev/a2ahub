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
	const approvalCode = "POL-029"
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
		{
			// The approval rule's refusal is a violation like any other: the write
			// lacks an approval, which is the write's fault and not the checkout's.
			name: "exit 1 with only the approval code is a refusal, not an error", exit: 1,
			stdout:    report(ciReport{Mode: "v3-pr", Artifacts: []validateReport{artifactViolation("<gate-approval>", approvalCode)}}),
			wantCodes: []string{approvalCode},
		},
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

// --- the approval rule (spec 04 section T1.3; AC 9, AC 20) -----------------

const landGateApprovalTrailer = "A2A-Approved-Gates"

// landGateCandidate builds a two-commit space repo like landCheckCandidate, but
// with files on BOTH sides, so a test can put a contract at the tip (the base
// commit) and have the write change it. headMessage is the head commit's
// message as the host would hand it to the check.
func landGateCandidate(t *testing.T, baseFiles, headFiles map[string]string, headMessage string) host.LandCandidate {
	t.Helper()
	root := t.TempDir()
	ciTestGit(t, root, "init", "-q", "-b", "main")
	writeAll := func(files map[string]string) {
		for rel, content := range files {
			abs := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
		}
	}
	writeAll(baseFiles)
	ciTestGit(t, root, "add", "-A")
	ciTestGit(t, root, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(ciTestGit(t, root, "rev-parse", "HEAD"))
	writeAll(headFiles)
	ciTestGit(t, root, "add", "-A")
	ciTestGit(t, root, "commit", "-q", "-m", "head")
	head := strings.TrimSpace(ciTestGit(t, root, "rev-parse", "HEAD"))
	return host.LandCandidate{
		CheckoutDir: root, BaseBranch: "main", BaseSHA: base, MergeSHA: head, HeadSHA: head,
		HeadMessage: headMessage, Author: "ydnikolaev",
	}
}

func ciManifestWithGates(setting string) string {
	return strings.Replace(ciSpaceYAML, "min_binary_version: 0.1.0\n", "min_binary_version: 0.1.0\ngates: "+setting+"\n", 1)
}

// landContractFiles is a V3-clean contract at the given version.
func landContractFiles(version string) map[string]string {
	return map[string]string{
		ciContractDescriptorPath:     ciContractDescriptorAt(version, "json-schema-2020-12"),
		ciContractSchemaPath:         `{"type":"object"}`,
		ciContractFixturePath:        `{}`,
		ciContractInvalidFixturePath: `null`,
	}
}

func approvalTrailer(gates string) string {
	return "publish\n\n" + landGateApprovalTrailer + ": " + gates + "\n"
}

func landVerdict(t *testing.T, c host.LandCandidate) host.LandVerdict {
	t.Helper()
	verdict, err := NewLandCheck(ciEngine(t))(context.Background(), c)
	if err != nil {
		t.Fatalf("NewLandCheck: %v", err)
	}
	return verdict
}

func wantRefusedWith(t *testing.T, verdict host.LandVerdict, code, gate string) {
	t.Helper()
	if verdict.OK || !reflect.DeepEqual(verdict.Codes, []string{code}) {
		t.Fatalf("verdict = %+v, want a refusal carrying exactly %s", verdict, code)
	}
	if !strings.Contains(verdict.Detail, gate) {
		t.Fatalf("Detail = %q, want it to name gate %s", verdict.Detail, gate)
	}
}

func TestLandCheck_GateApproval_ConfirmRefusesAnUnapprovedFirstPublish(t *testing.T) {
	t.Parallel()
	c := landGateCandidate(t, map[string]string{"space.yaml": ciSpaceYAML}, landContractFiles("1.0.0"), "publish\n")
	wantRefusedWith(t, landVerdict(t, c), "POL-029", "G1")
}

func TestLandCheck_GateApproval_TrailerLetsItLand(t *testing.T) {
	t.Parallel()
	c := landGateCandidate(t, map[string]string{"space.yaml": ciSpaceYAML}, landContractFiles("1.0.0"), approvalTrailer("G1"))
	if verdict := landVerdict(t, c); !verdict.OK {
		t.Fatalf("verdict = %+v, want OK: the trailer approves G1", verdict)
	}
}

// A write that is not behind any gate needs no trailer, in a confirm space.
func TestLandCheck_GateApproval_UngatedWriteNeedsNoTrailer(t *testing.T) {
	t.Parallel()
	rel := "axon/exchanges/XQ-axon-20260730-h2k8.md"
	c := landGateCandidate(t, map[string]string{"space.yaml": ciSpaceYAML},
		map[string]string{rel: validQuestion("XQ-axon-20260730-h2k8", "axon", "seomatrix")}, "ask\n")
	if verdict := landVerdict(t, c); !verdict.OK {
		t.Fatalf("verdict = %+v, want OK", verdict)
	}
}

// The mode is the TIP's: a write that flips confirm to autonomous is still
// judged under confirm, and the manifest change is itself G4.
func TestLandCheck_GateApproval_TipManifestDecidesTheMode(t *testing.T) {
	t.Parallel()
	t.Run("a candidate that says autonomous is judged by the tip's confirm", func(t *testing.T) {
		t.Parallel()
		c := landGateCandidate(t, map[string]string{"space.yaml": ciManifestWithGates("confirm")},
			map[string]string{"space.yaml": ciManifestWithGates("autonomous")}, "flip\n")
		wantRefusedWith(t, landVerdict(t, c), "POL-029", "G4")
	})
	t.Run("a tip that says autonomous needs no approval for a gated write", func(t *testing.T) {
		t.Parallel()
		c := landGateCandidate(t, map[string]string{"space.yaml": ciManifestWithGates("autonomous")}, landContractFiles("1.0.0"), "publish\n")
		if verdict := landVerdict(t, c); !verdict.OK {
			t.Fatalf("verdict = %+v, want OK under an autonomous tip", verdict)
		}
	})
	t.Run("a candidate that says confirm does not tighten an autonomous tip", func(t *testing.T) {
		t.Parallel()
		c := landGateCandidate(t, map[string]string{"space.yaml": ciManifestWithGates("autonomous")},
			map[string]string{"space.yaml": ciManifestWithGates("confirm")}, "tighten\n")
		if verdict := landVerdict(t, c); !verdict.OK {
			t.Fatalf("verdict = %+v, want OK: the tip decides", verdict)
		}
	})
}

// AC 9: a retry after a rival published first. The tip already holds the
// contract at 1.0.0, so the retried write (1.0.0 -> 2.0.0) is a G2 where the
// approval it was prepared with covered G1 only. The land check recomputes
// against the tip and names the missing G2; the approved set that names G2
// lets it land.
func TestLandCheck_GateApproval_RetryAfterARivalNeedsTheNewGate(t *testing.T) {
	t.Parallel()
	base := map[string]string{"space.yaml": ciSpaceYAML}
	for rel, content := range landContractFiles("1.0.0") {
		base[rel] = content
	}
	head := landContractFiles("2.0.0")

	t.Run("G1 alone no longer covers it", func(t *testing.T) {
		t.Parallel()
		wantRefusedWith(t, landVerdict(t, landGateCandidate(t, base, head, approvalTrailer("G1"))), "POL-029", "G2")
	})
	t.Run("G2 covers it", func(t *testing.T) {
		t.Parallel()
		if verdict := landVerdict(t, landGateCandidate(t, base, head, approvalTrailer("G2"))); !verdict.OK {
			t.Fatalf("verdict = %+v, want OK", verdict)
		}
	})
	t.Run("a same-major bump over a published baseline is no gate", func(t *testing.T) {
		t.Parallel()
		minor := landContractFiles("1.1.0")
		if verdict := landVerdict(t, landGateCandidate(t, base, minor, "bump\n")); !verdict.OK {
			t.Fatalf("verdict = %+v, want OK: 1.0.0 -> 1.1.0 carries no gate", verdict)
		}
	})
}

// A descriptor at 0.0.0 is a submitted candidate, not a publication: adding one
// needs no gate, and publishing over one is the contract's first publish (G1),
// not a major bump over a baseline (G2).
func TestLandCheck_GateApproval_SubmittedCandidateIsNoPublication(t *testing.T) {
	t.Parallel()
	t.Run("submitting a new descriptor at 0.0.0 needs no approval", func(t *testing.T) {
		t.Parallel()
		c := landGateCandidate(t, map[string]string{"space.yaml": ciSpaceYAML}, landContractFiles("0.0.0"), "submit\n")
		if verdict := landVerdict(t, c); !verdict.OK {
			t.Fatalf("verdict = %+v, want OK: a 0.0.0 candidate publishes nothing", verdict)
		}
	})
	base := map[string]string{"space.yaml": ciSpaceYAML}
	for rel, content := range landContractFiles("0.0.0") {
		base[rel] = content
	}
	t.Run("publishing over a submitted candidate is G1", func(t *testing.T) {
		t.Parallel()
		wantRefusedWith(t, landVerdict(t, landGateCandidate(t, base, landContractFiles("1.0.0"), "publish\n")), "POL-029", "G1")
	})
	t.Run("G1 approves the first publish over a submitted candidate", func(t *testing.T) {
		t.Parallel()
		if verdict := landVerdict(t, landGateCandidate(t, base, landContractFiles("1.0.0"), approvalTrailer("G1"))); !verdict.OK {
			t.Fatalf("verdict = %+v, want OK: 0.0.0 is no baseline, so 1.0.0 is G1, not G2", verdict)
		}
	})
}

func TestLandCheck_GateApproval_TrailerParsingFailsClosed(t *testing.T) {
	t.Parallel()
	base := map[string]string{"space.yaml": ciSpaceYAML}
	head := landContractFiles("1.0.0")
	tests := []struct {
		name    string
		message string
	}{
		{name: "an unknown gate id approves nothing", message: approvalTrailer("G1,G5")},
		{name: "a lowercase id approves nothing", message: approvalTrailer("g1")},
		{name: "an approval in an earlier paragraph is not the trailer block", message: landGateApprovalTrailer + ": G1\n\nsigned\n"},
		{name: "no trailer", message: "publish\n"},
		{name: "the wrong token", message: "publish\n\nA2A-Approved-Gate: G1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantRefusedWith(t, landVerdict(t, landGateCandidate(t, base, head, tt.message)), "POL-029", "G1")
		})
	}
}

func TestLandApprovedGates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, message string
		want          []string
	}{
		{name: "one line", message: approvalTrailer("G1,G3"), want: []string{"G1", "G3"}},
		{name: "padding inside the list is not an id and approves nothing", message: approvalTrailer("G1, G2"), want: nil},
		{name: "two lines union", message: "x\n\n" + landGateApprovalTrailer + ": G1\n" + landGateApprovalTrailer + ": G4\n", want: []string{"G1", "G4"}},
		{name: "a bad line does not poison a good one", message: "x\n\n" + landGateApprovalTrailer + ": G1,G9\n" + landGateApprovalTrailer + ": G2\n", want: []string{"G2"}},
		{name: "no trailer", message: "x\n", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := landApprovedGates(tt.message); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("landApprovedGates(%q) = %v, want %v", tt.message, got, tt.want)
			}
		})
	}
}

// G3 and G4 are recomputed from the diff: an approve or reject event is G3, any
// other transition is not, and a space.yaml change is G4. Driven at
// landRequiredGates so the event need not be a whole valid decision flow.
func TestLandRequiredGates_EventsAndManifest(t *testing.T) {
	t.Parallel()
	event := func(transition string) string {
		return "schema: event/v1\nevent: 01ARZ3NDEKTSV4RRFFQ69G5FAV\nspace: getvisa\nsubject: XD-axon-20260730-h2k8\ntransition: " + transition + "\n"
	}
	const eventPath = "axon/events/2026/01ARZ3NDEKTSV4RRFFQ69G5FAV.yaml"
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{name: "approve is G3", files: map[string]string{eventPath: event("approve")}, want: []string{"G3"}},
		{name: "reject is G3", files: map[string]string{eventPath: event("reject")}, want: []string{"G3"}},
		{name: "ack is no gate", files: map[string]string{eventPath: event("ack")}},
		{name: "a manifest change is G4", files: map[string]string{"space.yaml": ciManifestWithGates("confirm")}, want: []string{"G4"}},
		{name: "both", files: map[string]string{eventPath: event("approve"), "space.yaml": ciManifestWithGates("confirm")}, want: []string{"G3", "G4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := landGateCandidate(t, map[string]string{"space.yaml": ciSpaceYAML}, tt.files, "x\n")
			got, err := landRequiredGates(context.Background(), c)
			if err != nil {
				t.Fatalf("landRequiredGates: %v", err)
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("landRequiredGates = %v, want %v", got, tt.want)
			}
		})
	}
}

// contract.PublicationGateFor assumes well-formed versions and indexes past a
// short one, so the land check validates both before calling it.
func TestLandPublicationGate_RefusesMalformedVersionsInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	good := []struct {
		hasBaseline      bool
		baseline, target string
		want             string
	}{
		{false, "", "1.0.0", "G1"},
		{true, "1.0.0", "2.0.0", "G2"},
		{true, "1.0.0", "1.2.0", "none"},
	}
	for _, g := range good {
		got, err := landPublicationGate(g.hasBaseline, g.baseline, g.target)
		if err != nil || string(got) != g.want {
			t.Fatalf("landPublicationGate(%v, %q, %q) = (%q, %v), want (%q, nil)", g.hasBaseline, g.baseline, g.target, got, err, g.want)
		}
	}
	bad := []struct {
		hasBaseline      bool
		baseline, target string
	}{
		{false, "", "1.0"},
		{true, "1.0", "2.0.0"},
		{true, "1.0.0", "2"},
		{true, "1.0.0", ""},
		{true, "", "1.0.0"},
		{true, "1.x.0", "2.0.0"},
	}
	for _, b := range bad {
		if got, err := landPublicationGate(b.hasBaseline, b.baseline, b.target); err == nil {
			t.Fatalf("landPublicationGate(%v, %q, %q) = %q, want an error", b.hasBaseline, b.baseline, b.target, got)
		}
	}
}
