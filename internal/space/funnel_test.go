package space

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/fakegithub"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func newTestSubmitRequest(fx *spacefixture.Fixture, system string, l Layout) SubmitRequest {
	artifactID := "XQ-" + system + "-20260721-k3f9"
	return SubmitRequest{
		RepoDir:    fx.Clone(system),
		System:     system,
		Verb:       "submit",
		ArtifactID: artifactID,
		Files: []FileWrite{
			{Path: l.Exchange(artifactID), Content: []byte("---\nid: " + artifactID + "\n---\nbody\n")},
			{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRS"), Content: []byte("event: submit\n")},
		},
		CommitMessage:     "a2a(question): " + artifactID,
		CommitAuthorName:  "a2a-axon",
		CommitAuthorEmail: "a2a-axon@a2ahub.invalid",
		RemoteURL:         fx.RemoteURL(),
		Repo:              host.Repo{Owner: "acme", Name: "getvisa"},
		BaseBranch:        "main",
		PRTitle:           "a2a(question): " + artifactID,
		MinBinaryVersion:  "0.1.0",
	}
}

// TestFunnelSingleCommit is spec 05 §8 AC row 3: the write funnel produces
// exactly ONE commit containing the artifact file and its first lifecycle
// event before any push occurs.
func TestFunnelSingleCommit(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v", result.State, WriteStatePendingMerge)
	}
	if result.Branch != "a2a/axon/submit/"+req.ArtifactID {
		t.Fatalf("Branch = %q, want a2a/axon/%s", result.Branch, req.ArtifactID)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected exactly 1 push + 1 open, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}

	// Assert commit tree contents: exactly one commit ahead of main,
	// containing exactly the artifact file and its event.
	count, err := runGitOutput(context.Background(), req.RepoDir, nil, "rev-list", "--count", "main.."+result.Branch)
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if count != "1" {
		t.Fatalf("commits ahead of main = %s, want 1", count)
	}
	changed, err := runGitOutput(context.Background(), req.RepoDir, nil, "diff", "--name-only", "main", result.Branch)
	if err != nil {
		t.Fatalf("diff --name-only: %v", err)
	}
	files := strings.Fields(changed)
	if len(files) != 2 {
		t.Fatalf("changed files = %v, want exactly 2 (artifact + event)", files)
	}
}

// TestMinBinaryVersionGuard is spec 05 §8 AC row 4: the write funnel
// refuses to write when the local binary version is older than
// space.yaml's min_binary_version, remains read-only, and warns (CC-085).
func TestMinBinaryVersionGuard(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	req.MinBinaryVersion = "2.0.0"

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "1.0.0") // older than the pin

	_, err = funnel.Submit(context.Background(), req)
	if !errors.Is(err, ErrStaleBinaryVersion) {
		t.Fatalf("Submit error = %v, want ErrStaleBinaryVersion", err)
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("expected zero git-host mutation on refusal, got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestFunnelStampShapedVersionFailsClosed guards the wiring-regression class
// the P11 smoke test surfaced (docs/backlog.md): a caller that hands the
// funnel the one-line version STAMP ("a2a x.y.z (sha)") instead of the BARE
// dotted version makes the CC-085 guard's parser fail — so EVERY write against
// a version-pinned space is refused with a parse error, even one the bare
// version would pass. Here the pin ("0.5.0") is OLDER than the encoded 1.0.0,
// so a bare "1.0.0" would clear the guard and proceed; the stamp shape instead
// fails closed (parse error, NOT ErrStaleBinaryVersion) with zero host
// mutation. This is the funnel-side contract that fixes the wiring bug depend
// on — the cmd/a2a side is guarded by TestFunnelBinaryVersionIsBare.
func TestFunnelStampShapedVersionFailsClosed(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	req.MinBinaryVersion = "0.5.0" // a bare "1.0.0" is NEWER → would clear the guard

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "a2a 1.0.0 (abcdef0)") // the STAMP, not bare

	_, err = funnel.Submit(context.Background(), req)
	if err == nil {
		t.Fatal("Submit with a stamp-shaped version: want a parse failure, got nil")
	}
	if errors.Is(err, ErrStaleBinaryVersion) {
		t.Fatalf("Submit error = %v; want an unparseable-version failure, not ErrStaleBinaryVersion "+
			"(the encoded 1.0.0 is newer than the 0.5.0 pin — this must fail on the STAMP shape, not staleness)", err)
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("expected zero git-host mutation on a fail-closed refusal, got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestFunnelIdempotentShortCircuit exercises the AC-301.1 idempotent
// re-run: a second Submit for the same artifact id finds the already-open
// PR via FindPRByHeadBranch and short-circuits — no second push/open.
func TestFunnelIdempotentShortCircuit(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	first, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	second, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("second (re-run) Submit: %v", err)
	}
	if second.State != WriteStateAlreadyOpen {
		t.Fatalf("second Submit State = %v, want %v", second.State, WriteStateAlreadyOpen)
	}
	if second.PRNumber != first.PRNumber || second.Branch != first.Branch {
		t.Fatalf("second Submit = %+v, want same branch/PR as first %+v", second, first)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected NO second push/open cycle, got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestFunnelWrongSectionRefusedBeforeGitAction covers the §6 test-matrix
// edge case: a file path outside the authoring system's section (and not
// under decisions/) is refused before any git action.
func TestFunnelWrongSectionRefusedBeforeGitAction(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	// Smuggle in a file under a DIFFERENT system's section.
	other, err := NewLayout("seomatrix")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req.Files = append(req.Files, FileWrite{Path: other.Exchange("XQ-seomatrix-20260721-abcd"), Content: []byte("x")})

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	_, err = funnel.Submit(context.Background(), req)
	if !errors.Is(err, ErrWrongSection) {
		t.Fatalf("Submit error = %v, want ErrWrongSection", err)
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("expected zero git-host mutation on section refusal, got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
	// No commit should have been created either.
	branch := "a2a/axon/submit/" + req.ArtifactID
	if _, err := runGitOutput(context.Background(), req.RepoDir, nil, "rev-parse", "--verify", branch); err == nil {
		t.Fatalf("expected branch %s to NOT exist after a wrong-section refusal", branch)
	}
}

func TestFunnelRefusesSymlinkDestinationBeforeAnyFileMutation(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	external := t.TempDir()
	link := filepath.Join(req.RepoDir, "axon", "exchanges", "escape")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir link parent: %v", err)
	}
	if err := os.Symlink(external, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	req.Files = []FileWrite{
		{Path: "axon/exchanges/safe-before-escape.md", Content: []byte("must not be written")},
		{Path: "axon/exchanges/escape/outside.md", Content: []byte("must not escape")},
	}

	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")
	_, err = funnel.Submit(context.Background(), req)
	if !errors.Is(err, ErrSymlinkWrite) {
		t.Fatalf("Submit error = %v, want ErrSymlinkWrite", err)
	}
	if _, err := os.Stat(filepath.Join(external, "outside.md")); !os.IsNotExist(err) {
		t.Fatalf("external destination was mutated, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(req.RepoDir, "axon", "exchanges", "safe-before-escape.md")); !os.IsNotExist(err) {
		t.Fatalf("safe file was partially written before refusal, stat error = %v", err)
	}
	staged, err := runGitOutput(context.Background(), req.RepoDir, nil, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatalf("git diff --cached: %v", err)
	}
	if staged != "" {
		t.Fatalf("refusal left staged mutations: %q", staged)
	}
}

// TestSectionOKRejectsTraversal is the security regression net for the
// wave-2 audit HIGH: a crafted path with `..` segments (or an absolute
// path, or any non-canonical form) must not pass the single-writer
// section guard even though a naive prefix check would accept it.
func TestSectionOKRejectsTraversal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		system, path string
		want         bool
	}{
		{"axon", "axon/exchanges/XQ-axon-20260721-abcd.md", true},
		{"axon", "decisions/D-001.md", true},
		{"axon", "axon", true},
		// Traversal / escape attempts — all must be rejected.
		{"axon", "axon/../seomatrix/evil.md", false},
		{"axon", "axon/../../etc/passwd", false},
		{"axon", "../axon/evil.md", false},
		{"axon", "/etc/passwd", false},
		{"axon", "axon/./sneaky/../../seomatrix/x.md", false},
		{"axon", "", false},
		{"axon", "seomatrix/x.md", false},
	}
	for _, tc := range cases {
		if got := sectionOK(tc.system, tc.path); got != tc.want {
			t.Errorf("sectionOK(%q, %q) = %v, want %v", tc.system, tc.path, got, tc.want)
		}
	}
}

// TestFunnelDecisionsExceptionAllowed confirms the decisions/ funnel-level
// exception: a file under decisions/ (multi-party, no single owning
// system) is NOT refused by the section guard.
func TestFunnelDecisionsExceptionAllowed(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = append(req.Files, FileWrite{Path: Decision("XD-space-20260721-abcd"), Content: []byte("x")})

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	if _, err := funnel.Submit(context.Background(), req); err != nil {
		t.Fatalf("Submit: %v", err)
	}
}

// fakeSubmitValidator is a hand-written test double for the
// SubmitValidator seam.
type fakeSubmitValidator struct {
	err error
}

func (f *fakeSubmitValidator) ValidateSubmit(_ context.Context, _ []FileWrite) error { return f.err }

func TestFunnelSubmitValidatorSeamInvoked(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	wantErr := errors.New("V2: envelope field missing")
	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, &fakeSubmitValidator{err: wantErr}, "0.1.0")

	_, err = funnel.Submit(context.Background(), req)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Submit error = %v, want wrapping %v", err, wantErr)
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("expected zero git-host mutation on validator refusal, got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
}

var _ SubmitValidator = (*fakeSubmitValidator)(nil)

// TestDirectGitNoHub is spec 05 §8 AC row 1 (AC-301.3/CC-042): given no
// hub configured, the mirror-clone + write-funnel round trip succeeds via
// direct git with zero hub configuration present anywhere in project or
// machine config.
func TestDirectGitNoHub(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")

	// Zero hub config anywhere: ProjectConfig/MachineConfig carry no hub
	// field at all (this package defines none) — connect + mirror clone +
	// submit round trip using ONLY git + the host adapter.
	proj := ProjectConfig{
		System: "axon",
		Spaces: []Ref{{ID: "getvisa", RepoURL: fx.RemoteURL()}},
	}
	machine := MachineConfig{}

	mirrorDir := ResolveMirrorLocation(t.TempDir(), proj.Spaces[0], machine)
	if err := CloneOrFetch(context.Background(), mirrorDir, proj.Spaces[0].RepoURL); err != nil {
		t.Fatalf("CloneOrFetch: %v", err)
	}

	l, err := NewLayout(proj.System)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	artifactID := "XQ-axon-20260721-zzzz"
	req := SubmitRequest{
		RepoDir:    mirrorDir,
		System:     proj.System,
		Verb:       "submit",
		ArtifactID: artifactID,
		Files: []FileWrite{
			{Path: l.Exchange(artifactID), Content: []byte("artifact")},
			{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRT"), Content: []byte("event")},
		},
		RemoteURL:  fx.RemoteURL(),
		Repo:       host.Repo{Owner: "acme", Name: "getvisa"},
		BaseBranch: "main",
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")
	result, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStatePendingMerge || result.PRNumber == 0 {
		t.Fatalf("result = %+v, want a pending-merge PR", result)
	}
}

// spaceInfraPaths is the fixed set spec 35 §2 T1 admits when
// SubmitRequest.AllowSpaceInfrastructure is set.
var spaceInfraPaths = []string{
	"space.yaml",
	"CODEOWNERS",
	"BRANCH-PROTECTION.md",
	".github/workflows/a2a-validate.yml",
}

// TestFunnelSpaceInfrastructureDefaultRefused covers spec 35 §2 T1: with
// AllowSpaceInfrastructure left at its zero value (false), every
// space-infrastructure path is refused exactly like any other
// out-of-section path — the existing behaviour is unchanged.
func TestFunnelSpaceInfrastructureDefaultRefused(t *testing.T) {
	t.Parallel()

	for _, path := range spaceInfraPaths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			fx := spacefixture.New(t, "axon")
			l, err := NewLayout("axon")
			if err != nil {
				t.Fatalf("NewLayout: %v", err)
			}
			req := newTestSubmitRequest(fx, "axon", l)
			req.Files = []FileWrite{{Path: path, Content: []byte("infra\n")}}
			// AllowSpaceInfrastructure left unset (false) — the default.

			fake := host.NewFakeHost()
			funnel := NewWriteFunnel(fake, nil, "0.1.0")

			_, err = funnel.Submit(context.Background(), req)
			if !errors.Is(err, ErrWrongSection) {
				t.Fatalf("Submit(%s) error = %v, want ErrWrongSection", path, err)
			}
			if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
				t.Fatalf("expected zero git-host mutation on section refusal, got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
			}
		})
	}
}

// TestFunnelSpaceInfrastructureAllowed covers spec 35 §2 T1: with
// AllowSpaceInfrastructure set, every fixed space-infrastructure path is
// admitted past the section guard and the write proceeds to completion.
func TestFunnelSpaceInfrastructureAllowed(t *testing.T) {
	t.Parallel()

	for _, path := range spaceInfraPaths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			fx := spacefixture.New(t, "axon")
			l, err := NewLayout("axon")
			if err != nil {
				t.Fatalf("NewLayout: %v", err)
			}
			req := newTestSubmitRequest(fx, "axon", l)
			req.Files = []FileWrite{{Path: path, Content: []byte("infra\n")}}
			req.AllowSpaceInfrastructure = true

			fake := host.NewFakeHost()
			funnel := NewWriteFunnel(fake, nil, "0.1.0")

			result, err := funnel.Submit(context.Background(), req)
			if err != nil {
				t.Fatalf("Submit(%s): %v", path, err)
			}
			if result.State != WriteStatePendingMerge {
				t.Fatalf("Submit(%s) State = %v, want %v", path, result.State, WriteStatePendingMerge)
			}
		})
	}
}

// TestFunnelSpaceInfrastructureDoesNotWidenSectionGuard confirms
// AllowSpaceInfrastructure only ADDS the fixed infra path set — it does
// not otherwise loosen the section guard: a foreign system's section path
// is still refused even with the flag set.
func TestFunnelSpaceInfrastructureDoesNotWidenSectionGuard(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	other, err := NewLayout("seomatrix")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req.Files = []FileWrite{{Path: other.Exchange("XQ-seomatrix-20260721-abcd"), Content: []byte("x")}}
	req.AllowSpaceInfrastructure = true

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	_, err = funnel.Submit(context.Background(), req)
	if !errors.Is(err, ErrWrongSection) {
		t.Fatalf("Submit error = %v, want ErrWrongSection", err)
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("expected zero git-host mutation on section refusal, got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestFunnelSpaceInfrastructureFlagDoesNotWeakenCleanliness is the security
// regression net for the infra route: AllowSpaceInfrastructure must not
// reopen the traversal/absolute-path holes sectionOK already closes. Every
// path here is refused whether or not it superficially resembles an infra
// path.
func TestFunnelSpaceInfrastructureFlagDoesNotWeakenCleanliness(t *testing.T) {
	t.Parallel()

	badPaths := []string{
		".github/../beta/evil.md", // collapses OUT of .github/ once cleaned
		"/etc/passwd",             // absolute
		".githubfoo/x",            // NOT anchored under .github/
	}
	for _, path := range badPaths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			fx := spacefixture.New(t, "axon")
			l, err := NewLayout("axon")
			if err != nil {
				t.Fatalf("NewLayout: %v", err)
			}
			req := newTestSubmitRequest(fx, "axon", l)
			req.Files = []FileWrite{{Path: path, Content: []byte("x")}}
			req.AllowSpaceInfrastructure = true

			fake := host.NewFakeHost()
			funnel := NewWriteFunnel(fake, nil, "0.1.0")

			_, err = funnel.Submit(context.Background(), req)
			if !errors.Is(err, ErrWrongSection) {
				t.Fatalf("Submit(%s) error = %v, want ErrWrongSection", path, err)
			}
			if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
				t.Fatalf("expected zero git-host mutation, got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
			}
		})
	}
}

// TestFunnelAxonCodeownersResolvesBySectionNotInfra documents the
// distinction the brief calls out: "axon/CODEOWNERS" is inside axon's OWN
// section, so sectionOK already admits it — it was always allowed, with
// no AllowSpaceInfrastructure involved. spaceInfraOK itself must NOT be
// the rule that admits this path (it only recognizes the repo-root
// CODEOWNERS, not one nested under a system's section).
func TestFunnelAxonCodeownersResolvesBySectionNotInfra(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = []FileWrite{{Path: "axon/CODEOWNERS", Content: []byte("x")}}
	// AllowSpaceInfrastructure intentionally left false: this write must
	// succeed on the section rule alone.

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	if _, err := funnel.Submit(context.Background(), req); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if spaceInfraOK("axon/CODEOWNERS") {
		t.Fatalf(`spaceInfraOK("axon/CODEOWNERS") = true, want false — only the section rule admits this path`)
	}
}

// TestSpaceInfraOKAnchored is the unit-level anchoring net for
// spaceInfraOK: every entry in the fixed infra set matches, and every
// near-miss (nested-under-a-section, prefix-but-not-directory,
// traversal, absolute) is rejected.
func TestSpaceInfraOKAnchored(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		want bool
	}{
		{"space.yaml", true},
		{"CODEOWNERS", true},
		{"BRANCH-PROTECTION.md", true},
		{".github/workflows/a2a-validate.yml", true},
		{".github/x", true},
		{"axon/CODEOWNERS", false},
		{"axon/space.yaml", false},
		{".githubfoo/x", false},
		{".github/../beta/evil.md", false},
		{"/etc/passwd", false},
		{"beta/anything.md", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := spaceInfraOK(tc.path); got != tc.want {
			t.Errorf("spaceInfraOK(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// The idempotent short-circuit must RE-ARM auto-merge on the PR it finds.
//
// OpenPR is not atomic on GitHub: creating the PR and arming auto-merge are
// two calls. So the interrupted run this short-circuit exists to recover from
// may have created a PR and died before arming it — and reporting "already
// submitted" over a PR that will never merge on its own is the silent stall
// the path is supposed to prevent, not cause.
//
// Observed live on 2026-07-24 (P36's matrix): a 504 on OpenPR left a PR open
// with a green required check and `auto_merge: null`, and every retry
// answered already-open.
func TestFunnelShortCircuitReArmsAutoMerge(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	first, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	if len(fake.AutoArms) != 0 {
		t.Fatalf("the create path armed via the repair capability (%d calls); OpenPR does it inline", len(fake.AutoArms))
	}

	if _, err := funnel.Submit(context.Background(), req); err != nil {
		t.Fatalf("second (re-run) Submit: %v", err)
	}
	if len(fake.AutoArms) != 1 {
		t.Fatalf("re-run armed auto-merge %d times, want exactly 1", len(fake.AutoArms))
	}
	if got := fake.AutoArms[0]; got.PRNumber != first.PRNumber || got.Repo != req.Repo {
		t.Fatalf("armed %+v, want PR %d on %+v", got, first.PRNumber, req.Repo)
	}
}

// A repair that cannot be performed must FAIL the retry, not be reported as a
// successful re-submit: "already submitted" over a PR whose auto-merge could
// not be confirmed is precisely the false reassurance this guards.
func TestFunnelShortCircuitFailsWhenReArmingFails(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")
	if _, err := funnel.Submit(context.Background(), req); err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	sentinel := errors.New("auto-merge not allowed on this repository")
	fake.EnableAutoMergeFunc = func(context.Context, host.EnableAutoMergeRequest) error { return sentinel }

	if _, err := funnel.Submit(context.Background(), req); !errors.Is(err, sentinel) {
		t.Fatalf("re-run error = %v, want the arming failure to surface", err)
	}
}

// --- WAVE M4: landing a PR GitHub declines to auto-merge because it is
// already clean (AC-1050.12/.13) --------------------------------------------

// openAlreadyCleanPR wires fake.OpenPRFunc to reproduce exactly what
// host.GitHubHost.OpenPR returns when GitHub's auto-merge arming call is
// refused because the PR is already mergeable: not armed, and the exact
// note text host.AutoMergeNoteIsAlreadyClean recognises (host.
// AutoMergeAlreadyCleanNote — reused here rather than a hand-typed literal,
// so this test cannot silently drift from what the real host produces).
func openAlreadyCleanPR(fake *host.FakeHost, prNumber int) {
	fake.OpenPRFunc = func(context.Context, host.OpenPRRequest) (host.PRInfo, error) {
		return host.PRInfo{
			Number: prNumber, URL: "https://example.invalid/pr/99", State: "open",
			AutoMergeArmed: false, AutoMergeNote: host.AutoMergeAlreadyCleanNote,
		}, nil
	}
}

// TestFunnelLandsAlreadyCleanPR is AC-1050.12: when GitHub declines to arm
// auto-merge because the PR is already mergeable, and CheckStatus confirms
// the required check reported PRESENT and EXPLICITLY green, the write LANDS
// the PR itself instead of reporting "pending-merge" over a PR nothing else
// will ever merge — the defect observed live as three stuck PRs on the
// getvisa space.
func TestFunnelLandsAlreadyCleanPR(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	fake := host.NewFakeHost()
	openAlreadyCleanPR(fake, 99)
	fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
		return host.CheckStatusResult{State: "completed", Conclusion: "success"}, nil
	}
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// WriteStateMerged, not WriteStateAlreadyMerged: THIS call opened the PR
	// and landed it, and `a2a submit` prints "already submitted" for
	// already-merged — which would be a lie about a first-time write. The two
	// states are separate for exactly that reason (lead split, see the enum's
	// own doc comment).
	if result.State != WriteStateMerged {
		t.Fatalf("State = %v, want %v — the write must land the PR, not report pending-merge, and must not claim it was ALREADY submitted", result.State, WriteStateMerged)
	}
	if result.AutoMergeNote != "" {
		t.Fatalf("AutoMergeNote = %q, want empty — the write landed, nothing is pending", result.AutoMergeNote)
	}
	if len(fake.MergePRs) != 1 {
		t.Fatalf("MergePR called %d time(s), want exactly 1", len(fake.MergePRs))
	}
	if got := fake.MergePRs[0]; got.PRNumber != 99 || got.Repo != req.Repo {
		t.Fatalf("MergePR request = %+v, want PR 99 on %+v", got, req.Repo)
	}
}

// TestFunnelNeverMergesAnUnreportedCheck is AC-1050.13's central safety
// guarantee, named to read as the guard it is: a PR whose required check has
// NEVER REPORTED — CheckStatus's own answer for "no run matched the head SHA
// yet", {State: "queued", Conclusion: "", Name: ""} — is never merged, even
// though GitHub's own auto-merge arming call already says the PR is
// otherwise clean. a2a must never merge something CI has not passed, and
// "never ran" is the sharpest instance of that: nothing green has been
// observed at all.
func TestFunnelNeverMergesAnUnreportedCheck(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	fake := host.NewFakeHost()
	openAlreadyCleanPR(fake, 99)
	fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
		// The pre-existing "no check matched" answer (see github.go's
		// CheckStatus, the not-ok branch of selectRequiredCheckRun) — the
		// required context has not reported at all.
		return host.CheckStatusResult{State: "queued"}, nil
	}
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v — an unreported check must never be merged", result.State, WriteStatePendingMerge)
	}
	if result.AutoMergeNote != host.AutoMergeAlreadyCleanNote {
		t.Fatalf("AutoMergeNote = %q, want the note preserved exactly as today", result.AutoMergeNote)
	}
	if len(fake.MergePRs) != 0 {
		t.Fatalf("MergePR called %d time(s); the guard must refuse to merge an unreported check", len(fake.MergePRs))
	}
}

// TestFunnelNeverMergesWithoutExplicitGreen rounds out AC-1050.13: pending,
// ambiguous, and failing all fall through to the same refusal an unreported
// check gets — none of them is "explicitly green", and the guard treats
// anything short of that identically (see tryLandCleanPR's doc).
func TestFunnelNeverMergesWithoutExplicitGreen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status host.CheckStatusResult
	}{
		{name: "pending (in progress)", status: host.CheckStatusResult{State: "in_progress"}},
		{
			name: "ambiguous (more than one compound run matched)",
			status: host.CheckStatusResult{
				State: "completed", Conclusion: "success",
				Ambiguous: []string{"a2a-validate / validate", "a2a-validate / validate"},
			},
		},
		{name: "failing", status: host.CheckStatusResult{State: "completed", Conclusion: "failure"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := spacefixture.New(t, "axon")
			l, err := NewLayout("axon")
			if err != nil {
				t.Fatalf("NewLayout: %v", err)
			}
			req := newTestSubmitRequest(fx, "axon", l)

			fake := host.NewFakeHost()
			openAlreadyCleanPR(fake, 99)
			fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
				return tc.status, nil
			}
			funnel := NewWriteFunnel(fake, nil, "0.1.0")

			result, err := funnel.Submit(context.Background(), req)
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			if result.State != WriteStatePendingMerge {
				t.Fatalf("State = %v, want %v", result.State, WriteStatePendingMerge)
			}
			if result.AutoMergeNote != host.AutoMergeAlreadyCleanNote {
				t.Fatalf("AutoMergeNote = %q, want the note preserved exactly as today", result.AutoMergeNote)
			}
			if len(fake.MergePRs) != 0 {
				t.Fatalf("MergePR called %d time(s); the guard must refuse to merge", len(fake.MergePRs))
			}
		})
	}
}

// hostWithoutMerger is a Host (plus the AutoMerger capability the repair
// path also needs) that does NOT satisfy the optional Merger capability —
// the GitLab/Gitea-profile case ADR-003 keeps possible, and the concrete
// "this build predates WAVE M4" case: today's pending-merge behaviour must
// be byte-for-byte unchanged when nothing offers a direct-merge capability.
type hostWithoutMerger struct{ inner *host.FakeHost }

func (h hostWithoutMerger) PushBranch(ctx context.Context, req host.PushBranchRequest) (host.PushBranchResult, error) {
	return h.inner.PushBranch(ctx, req)
}

func (h hostWithoutMerger) OpenPR(ctx context.Context, req host.OpenPRRequest) (host.PRInfo, error) {
	return h.inner.OpenPR(ctx, req)
}

func (h hostWithoutMerger) TokenScopes(ctx context.Context, cred host.Credential) ([]string, bool, error) {
	return h.inner.TokenScopes(ctx, cred)
}

func (h hostWithoutMerger) CheckStatus(ctx context.Context, req host.StatusRequest) (host.CheckStatusResult, error) {
	return h.inner.CheckStatus(ctx, req)
}

func (h hostWithoutMerger) ReviewStatus(ctx context.Context, req host.StatusRequest) (host.ReviewStatusResult, error) {
	return h.inner.ReviewStatus(ctx, req)
}

func (h hostWithoutMerger) FindPRByHeadBranch(ctx context.Context, req host.FindPRRequest) (*host.PRInfo, error) {
	return h.inner.FindPRByHeadBranch(ctx, req)
}

func (h hostWithoutMerger) EnableAutoMerge(ctx context.Context, req host.EnableAutoMergeRequest) error {
	return h.inner.EnableAutoMerge(ctx, req)
}

var (
	_ host.Host       = hostWithoutMerger{}
	_ host.AutoMerger = hostWithoutMerger{}
)

// TestFunnelKeepsPendingMergeWhenHostHasNoMerger is the optional-capability
// half: a host implementing neither Merger keeps today's exact behaviour —
// no panic, no failed type assertion, the same pending-merge note as before
// this wave existed.
func TestFunnelKeepsPendingMergeWhenHostHasNoMerger(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	fake := host.NewFakeHost()
	openAlreadyCleanPR(fake, 99)
	// CheckStatus defaults to green — deliberately irrelevant here: the
	// capability assertion must fail closed before CheckStatus is even
	// consulted.
	funnel := NewWriteFunnel(hostWithoutMerger{inner: fake}, nil, "0.1.0")

	result, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v — a host without Merger must keep today's behaviour", result.State, WriteStatePendingMerge)
	}
	if result.AutoMergeNote != host.AutoMergeAlreadyCleanNote {
		t.Fatalf("AutoMergeNote = %q, want preserved", result.AutoMergeNote)
	}
}

// TestFunnelLandsAlreadyCleanPR_RealHostReachesMain drives host.GitHubHost
// (not FakeHost) against testkit/fakegithub's real HTTP+git plumbing — the
// through-the-host proof for AC-1050.12 that a submit whose auto-merge
// arming is refused because the PR is already clean still ends with the
// artifact actually merged into main, not merely "reported merged".
func TestFunnelLandsAlreadyCleanPR_RealHostReachesMain(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	gh := fakegithub.New(t, fx.RemoteURL())
	gh.AutoMergeAlreadyClean = true // GitHub declines to arm: the PR is already mergeable.

	h := host.NewGitHubHost(nil, gh.URL)
	funnel := NewWriteFunnel(h, nil, "0.1.0")

	result, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStateMerged {
		t.Fatalf("State = %v, want %v (this call did the whole thing — see the enum's doc)", result.State, WriteStateMerged)
	}
	if result.AutoMergeNote != "" {
		t.Fatalf("AutoMergeNote = %q, want empty — the write landed", result.AutoMergeNote)
	}

	ctx := context.Background()
	if err := runGit(ctx, req.RepoDir, "fetch", "origin"); err != nil {
		t.Fatalf("fetch origin: %v", err)
	}
	if err := runGit(ctx, req.RepoDir, "merge-base", "--is-ancestor", result.CommitSHA, "origin/main"); err != nil {
		t.Fatalf("commit %s is not an ancestor of origin/main after landing: %v", result.CommitSHA, err)
	}
}

// TestFunnelShortCircuitLandsAlreadyCleanPR_RealHostReachesMain is the
// repair-path sibling: the idempotent-retry short-circuit finds a PR the
// FIRST submit opened and armed successfully but that never merged (the
// interrupted-run scenario TestFunnelShortCircuitReArmsAutoMerge already
// covers), and by the time of the retry GitHub reports the PR as already
// clean — the repair path must land it directly too, through the SAME
// tryLandCleanPR helper as the create path (so the two cannot drift).
func TestFunnelShortCircuitLandsAlreadyCleanPR_RealHostReachesMain(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	gh := fakegithub.New(t, fx.RemoteURL())
	gh.AutoMerge = false // first OpenPR arms auto-merge successfully but nothing merges yet.

	h := host.NewGitHubHost(nil, gh.URL)
	funnel := NewWriteFunnel(h, nil, "0.1.0")

	first, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	if first.State != WriteStatePendingMerge {
		t.Fatalf("first State = %v, want %v", first.State, WriteStatePendingMerge)
	}

	// By the time of the retry, GitHub would decline re-arming because
	// nothing is left to wait for — the repair path must land it directly.
	gh.AutoMergeAlreadyClean = true

	second, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("second Submit: %v", err)
	}
	if second.State != WriteStateAlreadyMerged {
		t.Fatalf("second State = %v, want %v", second.State, WriteStateAlreadyMerged)
	}

	ctx := context.Background()
	if err := runGit(ctx, req.RepoDir, "fetch", "origin"); err != nil {
		t.Fatalf("fetch origin: %v", err)
	}
	if err := runGit(ctx, req.RepoDir, "merge-base", "--is-ancestor", first.CommitSHA, "origin/main"); err != nil {
		t.Fatalf("commit %s is not an ancestor of origin/main after landing: %v", first.CommitSHA, err)
	}
}

// --- AcquireMirrorLock regression: the live-e2e concurrent-writes-no-lost-
// write row (system B's branch carried system A's artifacts), reduced to a
// hermetic test against a local bare origin ------------------------------

// concurrentMirrorWriteRequest builds a SubmitRequest for system that
// shares repoDir with every other system's own request — the live-e2e
// defect's exact shape: mirror_root puts every project's clone of a space
// in ONE directory, so two systems on one machine share the same mirror
// working tree.
func concurrentMirrorWriteRequest(repoDir, system, eventULID string, l Layout) SubmitRequest {
	artifactID := "XQ-" + system + "-20260721-conc"
	return SubmitRequest{
		RepoDir:    repoDir,
		System:     system,
		Verb:       "submit",
		ArtifactID: artifactID,
		Files: []FileWrite{
			{Path: l.Exchange(artifactID), Content: []byte("---\nid: " + artifactID + "\n---\nbody\n")},
			{Path: l.EventFile("2026", eventULID), Content: []byte("event: submit\n")},
		},
		CommitMessage: "a2a(question): " + artifactID,
		RemoteURL:     repoDir,
		Repo:          host.Repo{Owner: "acme", Name: "getvisa"},
		BaseBranch:    "main",
	}
}

// runConcurrentMirrorWrites drives two Submit calls — one per system —
// concurrently against ONE shared mirror directory, and returns each
// result/error indexed the same way the caller's requests were: [0]=alpha,
// [1]=bravo.
func runConcurrentMirrorWrites(t *testing.T, funnel *WriteFunnel, reqA, reqB SubmitRequest) (results [2]WriteResult, errs [2]error) {
	t.Helper()

	var wg sync.WaitGroup
	start := make(chan struct{})
	reqs := [2]SubmitRequest{reqA, reqB}
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = funnel.Submit(context.Background(), reqs[i])
		}(i)
	}
	close(start)
	wg.Wait()
	return results, errs
}

// assertBranchOnlyContainsOwnFiles fails the test if branch's diff against
// main touches any path outside prefix — the exact live-e2e assertion
// ("changed path is outside the author's section") reduced to a direct
// git-tree check.
func assertBranchOnlyContainsOwnFiles(t *testing.T, repoDir, branch, prefix string) {
	t.Helper()

	changed, err := runGitOutput(context.Background(), repoDir, nil, "diff", "--name-only", "main", branch)
	if err != nil {
		t.Fatalf("diff --name-only main %s: %v", branch, err)
	}
	files := strings.Fields(changed)
	if len(files) == 0 {
		t.Fatalf("branch %s: diff against main is empty, want at least this system's own files", branch)
	}
	for _, f := range files {
		if !strings.HasPrefix(f, prefix) {
			t.Errorf("branch %s carries %q, outside the author's own section %q — a crossed write", branch, f, prefix)
		}
	}
}

// TestFunnelConcurrentSubmitsOneMirrorNoCrossedWrite is the hermetic
// regression for the live-e2e matrix's `concurrent-writes-no-lost-write`
// row: two systems (alpha, bravo) sharing ONE mirror directory (exactly
// what mirror_root produces on one machine running two systems) each
// Submit concurrently, and each system's resulting branch must carry ONLY
// that system's own files — never the other's.
//
// This is the funnel's own commitAndPush span (checkout/write/add/commit,
// then push) racing on the SAME git index and working tree: without
// AcquireMirrorLock, one system's `git add` stages both systems' files (the
// other's WriteFile landed on disk first), or one system's plain `git
// commit` (no pathspec — see commitOne) sweeps whatever the other staged in
// the shared index, and a branch ends up carrying the wrong author's paths
// — observed live as `"path": ".../events/...", "message": "changed path is
// outside the author's section"`.
func TestFunnelConcurrentSubmitsOneMirrorNoCrossedWrite(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "alpha", "bravo")
	shared := filepath.Join(t.TempDir(), "shared-mirror")
	if err := CloneOrFetch(context.Background(), shared, fx.RemoteURL()); err != nil {
		t.Fatalf("seed shared mirror: %v", err)
	}

	la, err := NewLayout("alpha")
	if err != nil {
		t.Fatalf("NewLayout(alpha): %v", err)
	}
	lb, err := NewLayout("bravo")
	if err != nil {
		t.Fatalf("NewLayout(bravo): %v", err)
	}
	reqA := concurrentMirrorWriteRequest(shared, "alpha", "01J8QYK2Z3ABCDEFGHJKMNPQRA", la)
	reqB := concurrentMirrorWriteRequest(shared, "bravo", "01J8QYK2Z3ABCDEFGHJKMNPQRB", lb)

	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")
	results, errs := runConcurrentMirrorWrites(t, funnel, reqA, reqB)

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Submit[%d]: %v", i, err)
		}
	}

	assertBranchOnlyContainsOwnFiles(t, shared, results[0].Branch, "alpha/")
	assertBranchOnlyContainsOwnFiles(t, shared, results[1].Branch, "bravo/")
}

// TestFunnelSequentialSubmitsOneMirrorNoInheritedFiles isolates the SECOND,
// independent defect the live-e2e symptom turned out to have: even with NO
// race at all — two Submits run one after the other, no goroutines, nothing
// for AcquireMirrorLock to serialize — a shared mirror used to produce a
// crossed branch, because commitOne's `git checkout -B branch` had no start
// point and forked from ambient HEAD (left standing on the FIRST system's
// own ephemeral branch, per checkoutRemoteHead's own doc: "commitOne checks
// it out and never leaves"). This fails without commitOne's explicit
// `origin/<base>` start point — proved directly, before the fix, via this
// exact sequential repro, no lock involved — and it PASSES on the lock
// alone reverted (there is nothing to contend here): only the start-point
// fix closes it. AcquireMirrorLock's own regression is
// TestFunnelConcurrentSubmitsOneMirrorNoCrossedWrite, above; the two tests
// are deliberately independent so a future regression in either fix fails
// the right one, not "locking is broken" for a start-point defect or vice
// versa.
func TestFunnelSequentialSubmitsOneMirrorNoInheritedFiles(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "alpha", "bravo")
	shared := filepath.Join(t.TempDir(), "shared-mirror")
	if err := CloneOrFetch(context.Background(), shared, fx.RemoteURL()); err != nil {
		t.Fatalf("seed shared mirror: %v", err)
	}

	la, err := NewLayout("alpha")
	if err != nil {
		t.Fatalf("NewLayout(alpha): %v", err)
	}
	lb, err := NewLayout("bravo")
	if err != nil {
		t.Fatalf("NewLayout(bravo): %v", err)
	}
	reqA := concurrentMirrorWriteRequest(shared, "alpha", "01J8QYK2Z3ABCDEFGHJKMNPQSA", la)
	reqB := concurrentMirrorWriteRequest(shared, "bravo", "01J8QYK2Z3ABCDEFGHJKMNPQSB", lb)

	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")

	if _, err := funnel.Submit(context.Background(), reqA); err != nil {
		t.Fatalf("Submit(alpha): %v", err)
	}
	resultB, err := funnel.Submit(context.Background(), reqB)
	if err != nil {
		t.Fatalf("Submit(bravo): %v", err)
	}

	assertBranchOnlyContainsOwnFiles(t, shared, resultB.Branch, "bravo/")
}

// TestFunnelReleasesMirrorLockOnPushErrorPath is the brief's own "release
// on error paths too" requirement: a Submit refused mid-commitAndPush
// (here, a push refusal with no fork fallback configured — the same shape
// TestFunnelWithoutForkFallbackReportsTheRefusal already covers
// behaviourally) must not leave the mirror's advisory lock file behind. A
// refused write that leaks the lock would poison every later Submit
// against the same mirror with ErrMirrorLocked until mirrorLockStaleAfter
// elapses — this is what proves it does not.
func TestFunnelReleasesMirrorLockOnPushErrorPath(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	// AllowForkFallback left false: the push refusal below is reported as
	// an error directly from inside commitAndPush's locked span, rather
	// than routed through the fork fallback.

	fake := host.NewFakeHost()
	forbidPushTo(fake, fx.RemoteURL())
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	if _, err := funnel.Submit(context.Background(), req); !errors.Is(err, host.ErrPushRejected) {
		t.Fatalf("Submit error = %v, want ErrPushRejected", err)
	}

	gitDir, err := resolveGitDir(req.RepoDir)
	if err != nil {
		t.Fatalf("resolveGitDir: %v", err)
	}
	lockPath := filepath.Join(gitDir, mirrorLockFileName)
	if _, statErr := os.Stat(lockPath); !os.IsNotExist(statErr) {
		t.Fatalf("mirror lock file stat = %v, want IsNotExist — a refused write must not leak the lock", statErr)
	}

	// And the mirror must not now be poisoned for a later, different write:
	// a fresh Submit on the same mirror must acquire the lock immediately,
	// not wait out mirrorLockWaitBudget. A NEW fake host (without the push
	// refusal wired in) so this second write actually succeeds — the
	// refusal above only exists to reach the error path under test.
	req2 := newTestSubmitRequest(fx, "axon", l)
	req2.ArtifactID = "XQ-axon-20260721-second"
	req2.Verb = "ack"
	req2.Files = []FileWrite{{Path: l.Exchange(req2.ArtifactID), Content: []byte("---\nid: " + req2.ArtifactID + "\n---\n")}}
	funnel2 := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")
	start := time.Now()
	if _, err := funnel2.Submit(context.Background(), req2); err != nil {
		t.Fatalf("Submit after a refused prior write: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= mirrorLockWaitBudget {
		t.Fatalf("second Submit took %s, want well under the wait budget %s (i.e. the lock was not leaked)",
			elapsed, mirrorLockWaitBudget)
	}
}

// TestFunnelLeavesTheMirrorOnBaseNotOnTheWriteBranch is the regression for
// the defect that made `a2a mcp` validate transitions against a state the
// space never agreed to.
//
// commitOne checks out the ephemeral write branch and, before this fix,
// never left. Only checkoutRemoteHead moved the tree back, and its single
// production caller is CloneOrFetch — which the CLI runs per invocation
// (so it was never bitten) and which an MCP server runs ONCE, at
// construction. A long-lived process's second write therefore folded its
// legality over a working tree standing on the first write's UNMERGED
// branch.
//
// The assertion is deliberately about the TREE, not about HEAD's name: a
// reader in this product globs files off the mirror directory
// (<system>/events/**), it does not consult refs. So the property that
// actually matters is "the files a reader would see are the space's, not
// the in-flight write's".
func TestFunnelLeavesTheMirrorOnBaseNotOnTheWriteBranch(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "alpha", "bravo")
	shared := filepath.Join(t.TempDir(), "shared-mirror")
	if err := CloneOrFetch(context.Background(), shared, fx.RemoteURL()); err != nil {
		t.Fatalf("seed shared mirror: %v", err)
	}

	la, err := NewLayout("alpha")
	if err != nil {
		t.Fatalf("NewLayout(alpha): %v", err)
	}
	req := concurrentMirrorWriteRequest(shared, "alpha", "01J8QYK2Z3ABCDEFGHJKMNPQSA", la)

	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")
	res, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// The written artifact must NOT be visible in the mirror's working
	// tree: it lives on an unmerged branch, and a reader of this mirror is
	// reading the space.
	written := filepath.Join(shared, filepath.FromSlash(la.Exchange("XQ-alpha-20260721-conc")))
	if _, statErr := os.Stat(written); statErr == nil {
		head, _ := runGitOutput(context.Background(), shared, nil, "rev-parse", "--abbrev-ref", "HEAD")
		t.Fatalf("the mirror still shows the unmerged write at %s (HEAD=%s, branch=%s) — "+
			"a long-lived reader would fold a transition over a state the space never agreed to",
			written, strings.TrimSpace(head), res.Branch)
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("stat %s: %v", written, statErr)
	}

	// And the tree is on base, not merely emptied of that one file.
	head, err := runGitOutput(context.Background(), shared, nil, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if got := strings.TrimSpace(head); got != "main" {
		t.Fatalf("mirror left on %q, want the base branch %q", got, "main")
	}

	// The write itself still happened — a "restore" that quietly discarded
	// the commit would satisfy every assertion above.
	if res.Branch == "" || res.CommitSHA == "" {
		t.Fatalf("Submit returned no branch/sha: %+v", res)
	}
	if err := runGit(context.Background(), shared, "merge-base", "--is-ancestor", res.CommitSHA, res.Branch); err != nil {
		t.Fatalf("the write's commit %s is not on its own branch %s after the restore: %v",
			res.CommitSHA, res.Branch, err)
	}
}

// TestBranchNameGrammarIsProtocol pins the rendered shape of the funnel's
// deterministic branch.
//
// This is not a change-detector for its own sake. BranchName is the
// funnel's IDEMPOTENCY KEY: step 0 looks up an existing PR by head branch,
// so two binaries that render the branch differently do not recognise each
// other's in-flight writes. That is not hypothetical — the grammar changed
// from a2a/<system>/<id> to a2a/<system>/<verb>/<id> in 0.4.0, and a live
// space still carries TWO open pull requests for ONE `contract publish` of
// one artifact, identical content, because a binary from each side of that
// change wrote to the space.
//
// So this test's job is to make the next such change deliberate. If you are
// here because it went red, read the failure message: the change is a
// breaking protocol change and needs min_binary_version raised in the SAME
// release, or in-flight artifacts fork a duplicate PR on upgrade.
func TestBranchNameGrammarIsProtocol(t *testing.T) {
	t.Parallel()

	const want = "a2a/axon/submit/XQ-axon-20260726-abcd"
	got := BranchName("axon", "submit", "XQ-axon-20260726-abcd")
	if got != want {
		t.Fatalf("BranchName grammar changed: got %q, want %q\n\n"+
			"This string is the write funnel's idempotency key (Submit's step 0 looks up an "+
			"existing PR BY HEAD BRANCH). Changing it means a binary on the old grammar and a "+
			"binary on the new one cannot see each other's in-flight writes, so every artifact "+
			"with an open PR forks a SECOND pull request the moment someone upgrades — exactly "+
			"what the 0.4.0 change did to a live space. If this change is intended, raise "+
			"min_binary_version in the same release so the old grammar cannot still be writing.", got, want)
	}
}

func TestValidateBranchSegmentsRefusesWhatWouldNotRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                     string
		system, verb, artifactID string
		wantErr                  bool
	}{
		{"the ordinary case", "axon", "submit", "XQ-axon-20260726-abcd", false},
		{"a composite batch id", "axon", "submit", "XQ-a+XQ-b", false},
		{"a contract id with an @version", "axon", "contract-publish", "XC-axon-widget", false},
		{"a hyphenated system", "my-system", "submit", "XQ-a", false},

		{"a slash in the artifact id nests the ref", "axon", "submit", "XQ-a/b", true},
		{"a slash in the system", "ax/on", "submit", "XQ-a", true},
		{"a slash in the verb", "axon", "sub/mit", "XQ-a", true},
		{"an empty system", "", "submit", "XQ-a", true},
		{"an empty artifact id", "axon", "submit", "", true},
		{"whitespace git refuses", "axon", "submit", "XQ a", true},
		{"a caret git refuses", "axon", "submit", "XQ^a", true},
		{"a colon git refuses", "axon", "submit", "XQ:a", true},
		{"a .lock suffix git reserves", "axon", "submit", "XQ-a.lock", true},
		{"a double dot git refuses", "axon", "submit", "XQ..a", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateBranchSegments(tc.system, tc.verb, tc.artifactID)
			if tc.wantErr && err == nil {
				t.Fatalf("validateBranchSegments(%q, %q, %q) = nil, want a refusal — "+
					"BranchName would render %q", tc.system, tc.verb, tc.artifactID,
					BranchName(tc.system, tc.verb, tc.artifactID))
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateBranchSegments(%q, %q, %q) = %v, want nil — "+
					"refusing a legal id would block a real write", tc.system, tc.verb, tc.artifactID, err)
			}
			if tc.wantErr && !errors.Is(err, ErrInvalidBranchSegment) {
				t.Fatalf("error = %v, want ErrInvalidBranchSegment so a caller can recognise the class", err)
			}
		})
	}
}

// TestSubmitRefusesANestingArtifactIDBeforeAnyGitAction proves the guard
// runs at the funnel, ahead of step 0 and ahead of any commit — a refusal
// that arrives after a commit exists is a refusal that left a mess.
func TestSubmitRefusesANestingArtifactIDBeforeAnyGitAction(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	mirror := filepath.Join(t.TempDir(), "mirror")
	if err := CloneOrFetch(context.Background(), mirror, fx.RemoteURL()); err != nil {
		t.Fatalf("seed clone: %v", err)
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := concurrentMirrorWriteRequest(mirror, "axon", "01J8QYK2Z3ABCDEFGHJKMNPQSA", l)
	req.ArtifactID = "XQ-axon/nested"

	if _, err := funnel.Submit(context.Background(), req); !errors.Is(err, ErrInvalidBranchSegment) {
		t.Fatalf("Submit error = %v, want ErrInvalidBranchSegment", err)
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("refused write still reached the host: %d pushes, %d opens", len(fake.Pushes), len(fake.Opens))
	}
}

// assertBranchCarriesExactlyTheseFiles is the artifact-level assertion the
// section-prefix helper cannot express.
//
// assertBranchOnlyContainsOwnFiles compares each changed path against the
// author's SECTION prefix, which is the right check when two DIFFERENT
// systems share a mirror. It is structurally blind to the same-system case:
// with one system, both writers' paths share the prefix, so a branch
// carrying the other write's artifact passes every assertion. Naming the
// exact expected file set closes that.
func assertBranchCarriesExactlyTheseFiles(t *testing.T, repoDir, branch string, want []string) {
	t.Helper()

	changed, err := runGitOutput(context.Background(), repoDir, nil, "diff", "--name-only", "main", branch)
	if err != nil {
		t.Fatalf("diff --name-only main %s: %v", branch, err)
	}
	got := strings.Fields(changed)
	sort.Strings(got)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)

	if len(got) != len(wantSorted) {
		t.Fatalf("branch %s carries %v, want exactly %v — a same-system crossed write is invisible to a "+
			"section-prefix check, which is why this asserts the file SET", branch, got, wantSorted)
	}
	for i := range got {
		if got[i] != wantSorted[i] {
			t.Fatalf("branch %s carries %v, want exactly %v", branch, got, wantSorted)
		}
	}
}

// TestFunnelConcurrentSubmitsSameSystemNoCrossedWrite covers the shape no
// tier covered: TWO WRITES FROM THE SAME SYSTEM against one shared mirror.
//
// One agent doing two things at once — a submit and a lifecycle transition,
// two lifecycle verbs on different artifacts — is at least as likely as two
// systems on one machine, and it is strictly harder to detect: the existing
// hermetic and live assertions both key on the author's section prefix, and
// with a single system both writers' paths share it. A branch carrying the
// other write's artifact would have passed every check this package had.
func TestFunnelConcurrentSubmitsSameSystemNoCrossedWrite(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "alpha", "bravo")
	shared := filepath.Join(t.TempDir(), "shared-mirror")
	if err := CloneOrFetch(context.Background(), shared, fx.RemoteURL()); err != nil {
		t.Fatalf("seed shared mirror: %v", err)
	}

	l, err := NewLayout("alpha")
	if err != nil {
		t.Fatalf("NewLayout(alpha): %v", err)
	}

	// Same system, DIFFERENT artifacts — so the two writes are legitimately
	// distinct and each branch has an exact, checkable file set.
	req1 := sameSystemWriteRequest(shared, "alpha", "XQ-alpha-20260726-one", "01J8QYK2Z3ABCDEFGHJKMNPQS1", l)
	req2 := sameSystemWriteRequest(shared, "alpha", "XQ-alpha-20260726-two", "01J8QYK2Z3ABCDEFGHJKMNPQS2", l)

	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")
	results, errs := runConcurrentMirrorWrites(t, funnel, req1, req2)

	for i, err := range errs {
		if err != nil && !errors.Is(err, ErrMirrorLocked) {
			t.Fatalf("Submit[%d]: %v — want success or a named lock refusal, never a raw git failure", i, err)
		}
	}
	if errs[0] != nil && errs[1] != nil {
		t.Fatal("both same-system writes were refused — the lock is serialising nothing through")
	}

	for i, req := range []SubmitRequest{req1, req2} {
		if errs[i] != nil {
			continue // legitimately refused; a re-run is the caller's move
		}
		want := make([]string, 0, len(req.Files))
		for _, f := range req.Files {
			want = append(want, f.Path)
		}
		assertBranchCarriesExactlyTheseFiles(t, shared, results[i].Branch, want)
	}
}

// sameSystemWriteRequest builds a write for `system` on a NAMED artifact —
// concurrentMirrorWriteRequest derives its id from the system, so it cannot
// express two distinct artifacts owned by one system.
func sameSystemWriteRequest(repoDir, system, artifactID, eventULID string, l Layout) SubmitRequest {
	return SubmitRequest{
		RepoDir:    repoDir,
		System:     system,
		Verb:       "submit",
		ArtifactID: artifactID,
		Files: []FileWrite{
			{Path: l.Exchange(artifactID), Content: []byte("---\nid: " + artifactID + "\n---\nbody\n")},
			{Path: l.EventFile("2026", eventULID), Content: []byte("event: submit\n")},
		},
		CommitMessage: "a2a(question): " + artifactID,
		RemoteURL:     repoDir,
		Repo:          host.Repo{Owner: "acme", Name: "getvisa"},
		BaseBranch:    "main",
	}
}

// TestFunnelWriteConcurrentWithReadRefreshLeavesBothIntact covers the
// shape that this package's own fix created and then never tested.
//
// TestCloneOrFetchConcurrentWritersNoLostWrite races refresh against
// refresh. Once CloneOrFetch began taking the mirror lock, the interesting
// race became refresh against WRITE — a reader's `reset --hard` landing
// between a writer's WriteFile and its `git add` is precisely the crossed
// write the lock exists to prevent, arriving from the reader's side. That
// mixed shape was covered nowhere.
func TestFunnelWriteConcurrentWithReadRefreshLeavesBothIntact(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "alpha", "bravo")
	shared := filepath.Join(t.TempDir(), "shared-mirror")
	if err := CloneOrFetch(context.Background(), shared, fx.RemoteURL()); err != nil {
		t.Fatalf("seed shared mirror: %v", err)
	}

	l, err := NewLayout("alpha")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := sameSystemWriteRequest(shared, "alpha", "XQ-alpha-20260726-race", "01J8QYK2Z3ABCDEFGHJKMNPQS3", l)
	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")

	const readers = 6
	var wg sync.WaitGroup
	start := make(chan struct{})

	var writeRes WriteResult
	var writeErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		writeRes, writeErr = funnel.Submit(context.Background(), req)
	}()

	readErrs := make([]error, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			readErrs[i] = CloneOrFetch(context.Background(), shared, fx.RemoteURL())
		}(i)
	}
	close(start)
	wg.Wait()

	// A reader may legitimately be refused (bounded wait). It must never
	// fail in git's own words — that is the unserialised collision.
	for i, err := range readErrs {
		if err != nil && !errors.Is(err, ErrMirrorLocked) {
			t.Errorf("refresh[%d] during a write: %v — want success or a named ErrMirrorLocked refusal", i, err)
		}
	}

	// The write either completes intact or is refused by name. What it must
	// NOT do is land a branch missing files a reader's reset swept away.
	switch {
	case writeErr == nil:
		want := make([]string, 0, len(req.Files))
		for _, f := range req.Files {
			want = append(want, f.Path)
		}
		assertBranchCarriesExactlyTheseFiles(t, shared, writeRes.Branch, want)
	case errors.Is(writeErr, ErrMirrorLocked):
		// Refused under contention: correct, and the caller re-runs.
	default:
		t.Fatalf("Submit during concurrent refreshes: %v — want success or ErrMirrorLocked", writeErr)
	}

	// And the mirror is still usable by the next caller either way.
	if err := CloneOrFetch(context.Background(), shared, fx.RemoteURL()); err != nil {
		t.Fatalf("mirror unusable after the race: %v", err)
	}
}

// TestFunnelThreeSystemsOneMirrorNoCrossedWrite is the operator's
// "two now, more later" made checkable. Two writers can collide in exactly
// one pairing; three can collide in three, and a lock that serialises a
// pair by luck rather than by design shows it here first.
func TestFunnelThreeSystemsOneMirrorNoCrossedWrite(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "alpha", "bravo", "gamma")
	shared := filepath.Join(t.TempDir(), "shared-mirror")
	if err := CloneOrFetch(context.Background(), shared, fx.RemoteURL()); err != nil {
		t.Fatalf("seed shared mirror: %v", err)
	}

	systems := []string{"alpha", "bravo", "gamma"}
	reqs := make([]SubmitRequest, len(systems))
	for i, sys := range systems {
		l, err := NewLayout(sys)
		if err != nil {
			t.Fatalf("NewLayout(%s): %v", sys, err)
		}
		reqs[i] = sameSystemWriteRequest(shared, sys,
			"XQ-"+sys+"-20260726-tri", "01J8QYK2Z3ABCDEFGHJKMNPQT"+string(rune('1'+i)), l)
	}

	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]WriteResult, len(reqs))
	errs := make([]error, len(reqs))
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = funnel.Submit(context.Background(), reqs[i])
		}(i)
	}
	close(start)
	wg.Wait()

	landed := 0
	for i, err := range errs {
		switch {
		case err == nil:
			landed++
			assertBranchOnlyContainsOwnFiles(t, shared, results[i].Branch, systems[i]+"/")
		case errors.Is(err, ErrMirrorLocked):
		default:
			t.Errorf("Submit[%s]: %v — want success or a named lock refusal", systems[i], err)
		}
	}
	if landed == 0 {
		t.Fatal("all three systems were refused — the lock is serialising nothing through")
	}
}
