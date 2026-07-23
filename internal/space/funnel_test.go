package space

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
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
