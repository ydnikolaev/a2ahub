package space

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

const buildNoteSHA = "0123456789abcdef0123456789abcdef01234567"

func localTestBuild() version.Build { return version.NewBuild("0.19.0", buildNoteSHA, "local") }

func prepareWithBuild(t *testing.T, req SubmitRequest, build version.Build) PreparedSubmission {
	t.Helper()
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, build).PrepareSubmission(
		context.Background(), req, testPreparationContext("0.19.0"),
	)
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	return prepared
}

// TestBuildNoteSitsAboveTheTrailerParagraph is T4's layout: the note is its own
// paragraph between the caller's message and the funnel's trailers, so every
// parser still sees the final paragraph it saw before.
func TestBuildNoteSitsAboveTheTrailerParagraph(t *testing.T) {
	t.Parallel()

	note := "A2A-Built-By: a2a 0.19.0 (local, " + buildNoteSHA + ")"
	t.Run("local build, no approval", func(t *testing.T) {
		t.Parallel()
		fx := spacefixture.New(t, "axon")
		req := approvalRequest(t, fx)
		result := submitPreparedApproval(t, fx, prepareWithBuild(t, req, localTestBuild()))
		if got, want := commitMessageOf(t, fx, result.Branch), req.CommitMessage+"\n\n"+note; got != want {
			t.Fatalf("committed message = %q, want %q", got, want)
		}
	})
	t.Run("approval stays the final paragraph", func(t *testing.T) {
		t.Parallel()
		fx := spacefixture.New(t, "axon")
		req := approvalRequest(t, fx)
		req.ApprovedGates = []string{"G1"}
		result := submitPreparedApproval(t, fx, prepareWithBuild(t, req, localTestBuild()))
		got := commitMessageOf(t, fx, result.Branch)
		if want := req.CommitMessage + "\n\n" + note + "\n\nA2A-Approved-Gates: G1"; got != want {
			t.Fatalf("committed message = %q, want %q", got, want)
		}
		if lines := host.ApprovedGatesTrailers(got); len(lines) != 1 || lines[0] != "A2A-Approved-Gates: G1" {
			t.Fatalf("final-paragraph approval lines = %q", lines)
		}
	})
	t.Run("release build records its own stamp", func(t *testing.T) {
		t.Parallel()
		fx := spacefixture.New(t, "axon")
		req := approvalRequest(t, fx)
		build := version.NewBuild("0.19.0", buildNoteSHA, "")
		result := submitPreparedApproval(t, fx, prepareWithBuild(t, req, build))
		want := req.CommitMessage + "\n\nA2A-Built-By: a2a 0.19.0 (" + buildNoteSHA + ")"
		if got := commitMessageOf(t, fx, result.Branch); got != want {
			t.Fatalf("committed message = %q, want %q", got, want)
		}
	})
	t.Run("no caller message gets the default title", func(t *testing.T) {
		t.Parallel()
		fx := spacefixture.New(t, "axon")
		req := approvalRequest(t, fx)
		req.CommitMessage = ""
		result := submitPreparedApproval(t, fx, prepareWithBuild(t, req, localTestBuild()))
		want := defaultCommitMessage(req.ArtifactID) + "\n\n" + note
		if got := commitMessageOf(t, fx, result.Branch); got != want {
			t.Fatalf("committed message = %q, want %q", got, want)
		}
	})
	t.Run("a build with no known commit writes no note", func(t *testing.T) {
		t.Parallel()
		fx := spacefixture.New(t, "axon")
		req := approvalRequest(t, fx)
		result := submitPreparedApproval(t, fx, prepareWithBuild(t, req, testBuild("0.19.0")))
		if got := commitMessageOf(t, fx, result.Branch); got != req.CommitMessage {
			t.Fatalf("committed message = %q, want the caller's message untouched", got)
		}
	})
}

// TestBuildNoteIsReservedToTheFunnel: a caller cannot write the note.
func TestBuildNoteIsReservedToTheFunnel(t *testing.T) {
	t.Parallel()

	for _, message := range []string{
		"a2a(question): X\n\nA2A-Built-By: a2a 9.9.9 (forged)",
		"A2A-Built-By: a2a 9.9.9 (forged)",
		"a2a(question): X\n\n   A2A-Built-By: a2a 9.9.9 (forged)",
	} {
		fx := spacefixture.New(t, "axon")
		req := approvalRequest(t, fx)
		req.CommitMessage = message
		_, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, localTestBuild()).PrepareSubmission(
			context.Background(), req, testPreparationContext("0.19.0"),
		)
		if !errors.Is(err, ErrPreparedInvalid) {
			t.Errorf("PrepareSubmission(%q) error = %v, want ErrPreparedInvalid", message, err)
		}
	}
}

// TestBuildNoteResumeWithAndWithoutIt: a journal prepared before the note
// existed resumes with its frozen message and no note, and a prepared
// submission that carries the note is accepted by the semantic check that
// peels it.
func TestBuildNoteResumeWithAndWithoutIt(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := approvalRequest(t, fx)
	old := prepareWithBuild(t, req, testBuild("0.19.0"))
	if strings.Contains(old.data.commitMessageBase, builtByTrailerName) {
		t.Fatalf("a build with no commit froze a note: %q", old.data.commitMessageBase)
	}
	if err := validatePreparedSemantics(old.data); err != nil {
		t.Fatalf("a message with no note must validate: %v", err)
	}
	fresh := prepareWithBuild(t, req, localTestBuild())
	if !strings.Contains(fresh.data.commitMessageBase, builtByTrailerName+": ") {
		t.Fatalf("a local build froze no note: %q", fresh.data.commitMessageBase)
	}
	if err := validatePreparedSemantics(fresh.data); err != nil {
		t.Fatalf("a message with the note must validate: %v", err)
	}
	// A forged second note is not peeled and stays visible to the reservation.
	forged := fresh.data
	forged.commitMessageBase += "\n\nA2A-Built-By: a2a 9.9.9 (forged)"
	if err := validatePreparedSemantics(forged); !errors.Is(err, ErrPreparedInvalid) {
		t.Fatalf("a forged extra note error = %v, want ErrPreparedInvalid", err)
	}
}

// oldRecoveryCommitMessageBase is a verbatim copy of the 0.27.0 binary's
// recoveryCommitMessageBase, kept as an oracle: it is the logic an already
// released binary runs over a message this funnel wrote.
func oldRecoveryCommitMessageBase(canonical string) (string, bool) {
	lines := strings.Split(canonical, "\n")
	const trailerCount = 7
	if len(lines) < trailerCount {
		return "", false
	}
	start := len(lines) - trailerCount
	if start == 0 {
		return "", true
	}
	if lines[start-1] == "" {
		return strings.Join(lines[:start-1], "\n"), true
	}
	base := strings.Join(lines[:start], "\n")
	if _, _, approved := splitApprovedGatesTrailer(base); approved {
		return base, true
	}
	return "", false
}

// TestBuildNoteLeavesRecoveryParsingUnchanged: a recovery commit a local build
// wrote is peeled to the same base by the current parser and by the oracle of
// the released one, with and without an approval.
func TestBuildNoteLeavesRecoveryParsingUnchanged(t *testing.T) {
	t.Parallel()

	for _, gates := range [][]string{nil, {"G1", "G2"}} {
		prepared := testRemoteRecoveryPreparedWithBuildAndGates(t, localTestBuild(), gates)
		message := prepared.data.commitMessage
		if !strings.Contains(message, "\n\n"+builtByTrailerName+": a2a 0.19.0 (local, "+buildNoteSHA+")") {
			t.Fatalf("gates %v: no note paragraph in %q", gates, message)
		}
		trailers, canonical, err := parseRemoteRecoveryTrailers(message)
		if err != nil || trailers.operationKey == "" {
			t.Fatalf("gates %v: parseRemoteRecoveryTrailers: %v (%+v)", gates, err, trailers)
		}
		base, ok := recoveryCommitMessageBase(canonical)
		if !ok || base != prepared.data.commitMessageBase {
			t.Fatalf("gates %v: recoveryCommitMessageBase = %q, %v; want %q", gates, base, ok, prepared.data.commitMessageBase)
		}
		oldBase, oldOK := oldRecoveryCommitMessageBase(canonical)
		if oldBase != base || oldOK != ok {
			t.Fatalf("gates %v: released parser = %q, %v; current = %q, %v", gates, oldBase, oldOK, base, ok)
		}
		if len(gates) > 0 {
			if lines := host.ApprovedGatesTrailers(message); len(lines) != 1 {
				t.Fatalf("gates %v: final-paragraph approval lines = %q", gates, lines)
			}
		}
	}
}

func testRemoteRecoveryPreparedWithBuildAndGates(t *testing.T, build version.Build, gates []string) PreparedSubmission {
	t.Helper()
	req := SubmitRequest{
		System: "atlas", Verb: "contract-publish", ArtifactID: "XC-atlas-demo",
		ArtifactIDs: []string{"XC-atlas-demo"}, OperationKey: "op-v1-" + strings.Repeat("a", 64),
		Mutations: []Mutation{{
			Path: "atlas/provides/demo/schema/contract.json", Operation: MutationWrite,
			Bytes: []byte("{\"type\":\"object\"}\n"),
		}},
		CommitMessage:    "a2a(contract): publish XC-atlas-demo@2.0.0",
		CommitAuthorName: "a2a-atlas", CommitAuthorEmail: "a2a-atlas@a2ahub.invalid",
		RemoteURL: "https://github.com/acme/space.git",
		Repo:      host.Repo{Owner: "acme", Name: "space"}, BaseBranch: "main",
		PRTitle: "Publish XC-atlas-demo@2.0.0", PRBody: "Publish XC-atlas-demo@2.0.0",
		ApprovedGates: gates,
	}
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, build).PrepareSubmission(
		context.Background(), req, PreparationContext{
			TargetIdentity: "acme/space", ObservedSpaceFloor: "0.19.0", FeatureFloor: "0.19.0",
			SchemaFloor: "0.19.0", ProfileFloor: "0.19.0", ProducerCompatibility: "0.19.0",
			BaseCommitSHA: strings.Repeat("f", 40),
			Recovery: &RecoveryV1{
				CandidateIntentDigest: "sha256:" + strings.Repeat("c", 64),
				IntentKey:             "op-v1-" + strings.Repeat("b", 64), PlanDigest: testRecoveryPlanDigest(),
				Target: "XC-atlas-demo@2.0.0", VersionSelector: "auto:major",
			},
		},
	)
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	return prepared
}

// TestLocalBuildKeepsTheCanonicalVersionForEveryFloorAndStamp: the channel
// never reaches the version the funnel compares floors with or stamps into
// event data (produced_by is closed, AGENTS.md anti-pattern 21), so a local
// build's events validate under the pinned release validator (T3).
func TestLocalBuildKeepsTheCanonicalVersionForEveryFloorAndStamp(t *testing.T) {
	t.Parallel()

	f := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, localTestBuild())
	if f.binaryVersion != "0.19.0" {
		t.Fatalf("funnel binaryVersion = %q, want the bare canonical 0.19.0", f.binaryVersion)
	}
	if canonical, err := version.Canonical(f.binaryVersion); err != nil || canonical != f.binaryVersion {
		t.Fatalf("binaryVersion %q is not canonical (%q, %v)", f.binaryVersion, canonical, err)
	}
}

// TestLocalBuildWritesTheCanonicalVersionIntoEventBytes checks the bytes a
// local build stages, not only the funnel's field: produced_by is closed and a
// released validator refuses anything but a bare X.Y.Z, so neither the channel
// nor the commit may reach event data (D-040).
func TestLocalBuildWritesTheCanonicalVersionIntoEventBytes(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	prepared := prepareWithBuild(t, approvalRequest(t, fx), localTestBuild())
	stamped := 0
	for _, mutation := range prepared.Mutations() {
		body := string(mutation.Bytes)
		if !strings.Contains(body, "produced_by") {
			continue
		}
		stamped++
		if strings.Contains(body, buildNoteSHA) || strings.Contains(body, "local") {
			t.Fatalf("%s carries the build stamp in event data:\n%s", mutation.Path, body)
		}
		if !strings.Contains(body, "0.19.0") {
			t.Fatalf("%s does not carry the canonical version 0.19.0:\n%s", mutation.Path, body)
		}
	}
	if stamped == 0 {
		t.Fatal("the request staged no produced_by, so the test judged nothing")
	}
}
