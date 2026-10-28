package space

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// finalParagraphApprovalLines is a TEST-ONLY restatement of the scan P2's
// merge-commit trailer copy applies (internal/host's approvedGatesTrailers):
// the lines of the message's final paragraph that open with the approval
// trailer's token and a colon. It is not production code. Spec 04 §T1.3 asks
// for the host's own scan to be exported and shared with the land check; until
// that export lands (internal/host is outside this wave), a test that wants to
// ask "would the land copy see it" has to restate the rule, and it is one place
// to delete when the shared scan exists.
func finalParagraphApprovalLines(message string) []string {
	lines := strings.Split(message, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	var out []string
	for _, line := range lines[start:end] {
		if strings.HasPrefix(line, host.TrailerApprovedGates+":") {
			out = append(out, line)
		}
	}
	return out
}

const testOperationKey = "op-v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func approvalRequest(t *testing.T, fx *spacefixture.Fixture) SubmitRequest {
	t.Helper()
	layout, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", layout)
	req.MinBinaryVersion = "0.19.0"
	req.ArtifactIDs = []string{req.ArtifactID}
	return req
}

func prepareApproval(t *testing.T, req SubmitRequest) PreparedSubmission {
	t.Helper()
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
		context.Background(), req, testPreparationContext("0.19.0"),
	)
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	return prepared
}

func commitMessageOf(t *testing.T, fx *spacefixture.Fixture, branch string) string {
	t.Helper()
	message, err := runGitOutput(context.Background(), fx.Clone("axon"), nil, "log", "-1", "--format=%B", branch)
	if err != nil {
		t.Fatalf("git log %s: %v", branch, err)
	}
	return strings.TrimRight(message, "\n")
}

func submitPreparedApproval(t *testing.T, fx *spacefixture.Fixture, prepared PreparedSubmission) WriteResult {
	t.Helper()
	result, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").SubmitPrepared(
		context.Background(), prepared, SubmissionRuntime{
			RepoDir: fx.Clone("axon"), RemoteURL: fx.RemoteURL(), TargetIdentity: "acme/getvisa",
			CurrentSpaceFloor: "0.19.0",
		},
	)
	if err != nil {
		t.Fatalf("SubmitPrepared: %v", err)
	}
	return result
}

// TestApprovedGatesTrailerIsAppendedFromTheTypedField is spec 04 §T1.3's
// "Trailer" row: the funnel appends A2A-Approved-Gates, sorted and
// comma-separated, as the LAST PARAGRAPH of the commit that reaches git, so the
// land copy (final paragraph only) records it in main's history.
func TestApprovedGatesTrailerIsAppendedFromTheTypedField(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := approvalRequest(t, fx)
	req.ApprovedGates = []string{"G4", "G1", "G4"}
	prepared := prepareApproval(t, req)

	wantMessage := req.CommitMessage + "\n\nA2A-Approved-Gates: G1,G4"
	result := submitPreparedApproval(t, fx, prepared)
	got := commitMessageOf(t, fx, result.Branch)
	if got != wantMessage {
		t.Fatalf("committed message = %q, want %q", got, wantMessage)
	}
	if lines := finalParagraphApprovalLines(got); len(lines) != 1 || lines[0] != "A2A-Approved-Gates: G1,G4" {
		t.Fatalf("final-paragraph approval lines = %q, want exactly [A2A-Approved-Gates: G1,G4]", lines)
	}
}

// TestApprovedGatesTrailerStaysInTheFinalParagraphWithOtherFunnelTrailers: the
// funnel appends its own paragraphs after the base (the operation-key trailer at
// commit time, the recovery block at preparation). An approval trailer left as
// a paragraph of its own would be pushed out of the final paragraph by either,
// the land copy would silently drop it, and the land check would refuse a write
// that was approved.
func TestApprovedGatesTrailerStaysInTheFinalParagraphWithOtherFunnelTrailers(t *testing.T) {
	t.Parallel()

	t.Run("operation key and no recovery record", func(t *testing.T) {
		t.Parallel()
		fx := spacefixture.New(t, "axon")
		req := approvalRequest(t, fx)
		req.OperationKey = testOperationKey
		req.ApprovedGates = []string{"G1"}
		prepared := prepareApproval(t, req)

		result := submitPreparedApproval(t, fx, prepared)
		got := commitMessageOf(t, fx, result.Branch)
		want := req.CommitMessage + "\n\nA2A-Approved-Gates: G1\nA2A-Operation-Key: " + testOperationKey
		if got != want {
			t.Fatalf("committed message = %q, want %q", got, want)
		}
		if lines := finalParagraphApprovalLines(got); len(lines) != 1 {
			t.Fatalf("final-paragraph approval lines = %q, want the approval trailer in the final paragraph", lines)
		}
	})

	t.Run("recovery record", func(t *testing.T) {
		t.Parallel()
		prepared := testRemoteRecoveryPreparedWithGates(t, []string{"G2", "G1"})
		message := prepared.data.commitMessage
		lines := finalParagraphApprovalLines(message)
		if len(lines) != 1 || lines[0] != "A2A-Approved-Gates: G1,G2" {
			t.Fatalf("final-paragraph approval lines = %q, want [A2A-Approved-Gates: G1,G2] in %q", lines, message)
		}
		if !strings.HasSuffix(message, "A2A-Recovery-v1: "+recoveryV1Of(t, prepared)) {
			t.Fatalf("the recovery block is no longer the tail of %q: remote recovery parses it from the end", message)
		}
		if err := prepared.verify(); err != nil {
			t.Fatalf("verify: %v", err)
		}
	})
}

func recoveryV1Of(t *testing.T, prepared PreparedSubmission) string {
	t.Helper()
	record, err := DecodeRecoveryV1(prepared.data.recoveryJSON)
	if err != nil {
		t.Fatalf("DecodeRecoveryV1: %v", err)
	}
	encoded, _, err := EncodeRecoveryV1Trailer(record)
	if err != nil {
		t.Fatalf("EncodeRecoveryV1Trailer: %v", err)
	}
	return encoded
}

func testRemoteRecoveryPreparedWithGates(t *testing.T, gates []string) PreparedSubmission {
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
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
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

// TestNoApprovedGatesLeavesTheCommitMessageAndDigestUntouched: a write with no
// approval produces exactly today's commit message and today's prepared digest.
func TestNoApprovedGatesLeavesTheCommitMessageAndDigestUntouched(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := approvalRequest(t, fx)
	baseline := prepareApproval(t, req)

	for name, gates := range map[string][]string{"nil": nil, "empty": {}} {
		withField := req
		withField.ApprovedGates = gates
		prepared := prepareApproval(t, withField)
		if prepared.Digest() != baseline.Digest() {
			t.Errorf("%s ApprovedGates: digest = %s, want today's %s", name, prepared.Digest(), baseline.Digest())
		}
		if prepared.data.commitMessageBase != req.CommitMessage || prepared.data.commitMessage != req.CommitMessage {
			t.Errorf("%s ApprovedGates: message base/full = %q/%q, want %q", name, prepared.data.commitMessageBase, prepared.data.commitMessage, req.CommitMessage)
		}
	}

	result := submitPreparedApproval(t, fx, baseline)
	if got := commitMessageOf(t, fx, result.Branch); got != req.CommitMessage {
		t.Fatalf("committed message = %q, want today's %q", got, req.CommitMessage)
	}
	if lines := finalParagraphApprovalLines(commitMessageOf(t, fx, result.Branch)); len(lines) != 0 {
		t.Fatalf("approval lines = %q, want none when nothing was approved", lines)
	}
}

// TestApprovedGatesTrailerIsReservedToTheFunnel is spec 04 AC 11, first half:
// caller-supplied message text that carries the trailer is refused with
// ErrPreparedInvalid, wherever it sits and whether or not the typed field is
// also set.
func TestApprovedGatesTrailerIsReservedToTheFunnel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
		gates   []string
	}{
		{"final paragraph", "a2a(question): X\n\nA2A-Approved-Gates: G1", nil},
		{"mid message", "a2a(question): X\n\nA2A-Approved-Gates: G1\n\nmore body", nil},
		{"subject line", "A2A-Approved-Gates: G1", nil},
		{"indented", "a2a(question): X\n\n   A2A-Approved-Gates: G1", nil},
		{"alongside the typed field", "a2a(question): X\n\nA2A-Approved-Gates: G4", []string{"G1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fx := spacefixture.New(t, "axon")
			req := approvalRequest(t, fx)
			req.CommitMessage = tt.message
			req.ApprovedGates = tt.gates
			_, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
				context.Background(), req, testPreparationContext("0.19.0"),
			)
			if !errors.Is(err, ErrPreparedInvalid) {
				t.Fatalf("PrepareSubmission(%q) error = %v, want ErrPreparedInvalid", tt.message, err)
			}
		})
	}
}

// TestInvalidApprovedGatesAreRefused: only G1-G4 are approvable. G5 has no
// override on any host, and an id the trailer's grammar cannot carry must not
// reach a commit message.
func TestInvalidApprovedGatesAreRefused(t *testing.T) {
	t.Parallel()

	for _, gates := range [][]string{{"G5"}, {"G1", "G9"}, {""}, {"g1"}, {"G1,G2"}, {"G1\nA2A-Operation-Key: forged"}} {
		fx := spacefixture.New(t, "axon")
		req := approvalRequest(t, fx)
		req.ApprovedGates = gates
		_, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
			context.Background(), req, testPreparationContext("0.19.0"),
		)
		if !errors.Is(err, ErrPreparedInvalid) {
			t.Errorf("PrepareSubmission(ApprovedGates=%q) error = %v, want ErrPreparedInvalid", gates, err)
		}
	}
}

// TestApprovedGatesTrailerSurvivesResume is spec 04 AC 11, second half: a write
// prepared with an approval, journalled, decoded and submitted by a restarted
// process carries the same trailer it was prepared with, and a journal edited
// to forge or strip the approval is refused rather than submitted.
func TestApprovedGatesTrailerSurvivesResume(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := approvalRequest(t, fx)
	req.ApprovedGates = []string{"G3", "G1"}
	prepared := prepareApproval(t, req)

	raw, err := EncodePreparedSubmissionJournal(prepared)
	if err != nil {
		t.Fatalf("EncodePreparedSubmissionJournal: %v", err)
	}
	if !strings.Contains(string(raw), `A2A-Approved-Gates: G1,G3`) {
		t.Fatalf("journal does not carry the approval in its message base: %s", raw)
	}
	decoded, err := DecodePreparedSubmissionJournal(raw)
	if err != nil {
		t.Fatalf("DecodePreparedSubmissionJournal: %v", err)
	}
	if decoded.Digest() != prepared.Digest() {
		t.Fatalf("decoded digest = %s, want %s", decoded.Digest(), prepared.Digest())
	}
	result := submitPreparedApproval(t, fx, decoded)
	got := commitMessageOf(t, fx, result.Branch)
	if want := req.CommitMessage + "\n\nA2A-Approved-Gates: G1,G3"; got != want {
		t.Fatalf("resumed committed message = %q, want %q", got, want)
	}

	// The digest covers the message base: stripping or rewriting the approval
	// in a persisted journal cannot go unnoticed.
	for name, tamper := range map[string]string{
		"approval widened":  strings.Replace(string(raw), "A2A-Approved-Gates: G1,G3", "A2A-Approved-Gates: G1,G2,G3", 1),
		"approval stripped": strings.Replace(string(raw), `\n\nA2A-Approved-Gates: G1,G3`, "", 1),
	} {
		if tamper == string(raw) {
			t.Fatalf("%s: the tamper did not change the journal", name)
		}
		if _, err := DecodePreparedSubmissionJournal([]byte(tamper)); err == nil {
			t.Errorf("%s: DecodePreparedSubmissionJournal accepted a journal whose approval was edited", name)
		}
	}
}

// TestApprovedGatesDefaultTitleWhenTheCallerSuppliesNoMessage: a request with
// no commit message gets the funnel's default title, and the trailer rides
// under it rather than standing alone as the whole message.
func TestApprovedGatesDefaultTitleWhenTheCallerSuppliesNoMessage(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := approvalRequest(t, fx)
	req.CommitMessage = ""
	req.ApprovedGates = []string{"G1"}
	prepared := prepareApproval(t, req)

	result := submitPreparedApproval(t, fx, prepared)
	want := "a2a: submit " + req.ArtifactID + "\n\nA2A-Approved-Gates: G1"
	if got := commitMessageOf(t, fx, result.Branch); got != want {
		t.Fatalf("committed message = %q, want %q", got, want)
	}
}

// TestSplitApprovedGatesTrailerOnlyPeelsTheFunnelsOwnForm: the semantic check
// on a frozen message base peels the trailer the funnel wrote and nothing else,
// so a forged, reordered or mid-message line stays in the text the reservation
// then refuses.
func TestSplitApprovedGatesTrailerOnlyPeelsTheFunnelsOwnForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		message   string
		wantBody  string
		wantGates []string
		wantOK    bool
	}{
		{"canonical", "title\n\nA2A-Approved-Gates: G1,G4", "title", []string{"G1", "G4"}, true},
		{"multi paragraph body", "title\n\nbody\n\nA2A-Approved-Gates: G3", "title\n\nbody", []string{"G3"}, true},
		{"no trailer", "title\n\nbody", "title\n\nbody", nil, false},
		{"unsorted list", "title\n\nA2A-Approved-Gates: G4,G1", "title\n\nA2A-Approved-Gates: G4,G1", nil, false},
		{"repeated gate", "title\n\nA2A-Approved-Gates: G1,G1", "title\n\nA2A-Approved-Gates: G1,G1", nil, false},
		{"G5", "title\n\nA2A-Approved-Gates: G5", "title\n\nA2A-Approved-Gates: G5", nil, false},
		{"empty list", "title\n\nA2A-Approved-Gates: ", "title\n\nA2A-Approved-Gates: ", nil, false},
		{"spaced list", "title\n\nA2A-Approved-Gates: G1, G2", "title\n\nA2A-Approved-Gates: G1, G2", nil, false},
		{"not the last paragraph", "title\n\nA2A-Approved-Gates: G1\n\nmore", "title\n\nA2A-Approved-Gates: G1\n\nmore", nil, false},
		{"joined to another trailer", "title\n\nA2A-Approved-Gates: G1\nOther: x", "title\n\nA2A-Approved-Gates: G1\nOther: x", nil, false},
		{"no blank line before it", "title\nA2A-Approved-Gates: G1", "title\nA2A-Approved-Gates: G1", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, gates, ok := splitApprovedGatesTrailer(tt.message)
			if body != tt.wantBody || ok != tt.wantOK || strings.Join(gates, ",") != strings.Join(tt.wantGates, ",") {
				t.Fatalf("splitApprovedGatesTrailer(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tt.message, body, gates, ok, tt.wantBody, tt.wantGates, tt.wantOK)
			}
		})
	}
}

// TestFrozenMessageBaseWithAForgedTrailerIsRefused: validatePreparedSemantics
// runs on every verify and every resume. The funnel's own trailer passes; a
// trailer in any other shape or place is caller-supplied text by another name
// and is refused.
func TestFrozenMessageBaseWithAForgedTrailerIsRefused(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	prepared := prepareApproval(t, approvalRequest(t, fx))
	title := prepared.data.commitMessageBase

	tests := []struct {
		name    string
		base    string
		wantErr bool
	}{
		{"the funnel's own form", title + "\n\nA2A-Approved-Gates: G1,G2", false},
		{"unsorted", title + "\n\nA2A-Approved-Gates: G2,G1", true},
		{"mid message", title + "\n\nA2A-Approved-Gates: G1\n\nbody", true},
		{"G5", title + "\n\nA2A-Approved-Gates: G5", true},
		{"two trailer lines", title + "\n\nA2A-Approved-Gates: G1\nA2A-Approved-Gates: G2", true},
	}
	for _, tt := range tests {
		data := prepared.data
		data.commitMessageBase = tt.base
		err := validatePreparedSemantics(data)
		if tt.wantErr != errors.Is(err, ErrPreparedInvalid) || (!tt.wantErr && err != nil) {
			t.Errorf("%s: validatePreparedSemantics error = %v, wantErr=%v (ErrPreparedInvalid)", tt.name, err, tt.wantErr)
		}
	}
}
