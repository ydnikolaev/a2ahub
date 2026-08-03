package space

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
	embeddedschemas "github.com/ydnikolaev/a2ahub/schemas"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
	"gopkg.in/yaml.v3"
)

func TestWorkPreparerRendersEvaluatesValidatesAndFreezesInOrder(t *testing.T) {
	t.Parallel()

	var calls []string
	validator := newWorkPreparationValidator(t, &calls)
	fakeHost := host.NewFakeHost()
	funnel := &recordingWorkSubmissionPreparer{
		calls: &calls,
		inner: NewWriteFunnel(fakeHost, validator, version.OperationalConfidenceFloor),
	}
	preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{0x12}, 14), validator, funnel, nil)
	request := validWorkPreparationRequest(t, workreport.ModeImplementing)

	prepared, err := preparer.Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got, want := fmt.Sprint(calls), "[render context funnel schema-candidate schema-final]"; got != want {
		t.Fatalf("call order = %s, want %s", got, want)
	}
	if prepared.OperationKey != request.OperationKey || prepared.Action != request.Action ||
		prepared.SemanticSequence != request.SemanticSequence || prepared.LocalTarget != request.LocalTarget {
		t.Fatalf("prepared operation changed coordinator identity: %+v", prepared)
	}
	if prepared.ArtifactID == "" || prepared.EventID == "" || prepared.Prepared.Len() == 0 {
		t.Fatalf("prepared operation is incomplete: %+v", prepared)
	}

	journal := prepared.Prepared.Bytes()
	decoded, err := DecodePreparedSubmissionJournal(journal)
	if err != nil {
		t.Fatalf("DecodePreparedSubmissionJournal: %v", err)
	}
	files := preparedWorkFiles(t, decoded)
	if len(files) != 2 {
		t.Fatalf("prepared files = %d, want one artifact + one event", len(files))
	}
	artifactRaw := files[prepared.ArtifactID]
	eventRaw := files[prepared.EventID]
	if len(artifactRaw) == 0 || len(eventRaw) == 0 {
		t.Fatalf("prepared paths do not bind IDs: files=%v", sortedPreparedPaths(decoded))
	}
	assertCanonicalWorkArtifact(t, artifactRaw, request, prepared.ArtifactID)
	assertCanonicalWorkEvent(t, eventRaw, request, prepared.ArtifactID, prepared.EventID)
	if !bytes.Contains(eventRaw, []byte("produced_by:")) {
		t.Fatal("P4 final event bytes lack the producer stamp")
	}
	for _, raw := range files {
		if bytes.Contains(raw, []byte(request.OperationKey)) {
			t.Fatalf("operation key leaked into durable file bytes:\n%s", raw)
		}
	}
	if !bytes.Contains(journal, []byte(request.OperationKey)) {
		t.Fatal("operation key missing from local prepared journal metadata")
	}
	if bytes.Contains(journal, []byte(validWorkPreparationRuntime().RemoteURL)) {
		t.Fatal("raw remote/provider endpoint leaked into prepared journal")
	}
	if len(fakeHost.Pushes) != 0 || len(fakeHost.Opens) != 0 {
		t.Fatalf("side-effect-free preparation called host: pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
}

func TestWorkPreparerEveryModeWaitAndFinishedShape(t *testing.T) {
	t.Parallel()

	for _, mode := range []workreport.Mode{
		workreport.ModePlanning,
		workreport.ModeImplementing,
		workreport.ModeTesting,
		workreport.ModeReviewing,
		workreport.ModeWaiting,
		workreport.ModePaused,
		workreport.ModeFinished,
	} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			validator := newWorkPreparationValidator(t, nil)
			preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{byte(len(mode) + 1)}, 14), validator,
				NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor), nil)
			request := validWorkPreparationRequest(t, mode)
			switch mode {
			case workreport.ModeWaiting:
				request.Action = workreport.ActionWait
				request.WaitingOn = []workreport.WaitingOn{
					{Kind: workreport.WaitSystem, ID: "seomatrix", Summary: "Revised invalid fixtures"},
					{Kind: workreport.WaitTool, ID: "live-e2e"},
				}
			case workreport.ModePaused, workreport.ModeFinished:
				request.Action = workreport.ActionStop
				request.LocalTarget = workreport.TargetClosing
			}
			if mode == workreport.ModeFinished {
				request.ValidUntil = time.Time{}
			}
			request.OperationKey = workOperationKey(t, request.Identity.WorkID, request.SemanticSequence, request.Action)

			prepared, err := preparer.Prepare(context.Background(), request)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			decoded, err := DecodePreparedSubmissionJournal(prepared.Prepared.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			artifactRaw := preparedWorkFiles(t, decoded)[prepared.ArtifactID]
			doc := decodeWorkArtifact(t, artifactRaw)
			work := mappingValue(t, doc, "work")
			_, hasValidUntil := work["valid_until"]
			_, hasWaiting := work["waiting_on"]
			if hasValidUntil != (mode != workreport.ModeFinished) {
				t.Fatalf("mode %s valid_until presence = %v", mode, hasValidUntil)
			}
			if hasWaiting != (mode == workreport.ModeWaiting) {
				t.Fatalf("mode %s waiting_on presence = %v", mode, hasWaiting)
			}
		})
	}
}

func TestWorkPreparerThreadSubjectOmitsArtifactRefs(t *testing.T) {
	t.Parallel()

	validator := newWorkPreparationValidator(t, nil)
	preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{0x31}, 14), validator,
		NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor), nil)
	request := validWorkPreparationRequest(t, workreport.ModePlanning)
	request.SubjectRef = request.Identity.Thread
	prepared, err := preparer.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePreparedSubmissionJournal(prepared.Prepared.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeWorkArtifact(t, preparedWorkFiles(t, decoded)[prepared.ArtifactID])
	if _, ok := doc["refs"]; ok {
		t.Fatal("thread-valued subject was serialized into artifact refs")
	}
}

func TestWorkPreparerHostileTextRemainsData(t *testing.T) {
	t.Parallel()

	validator := newWorkPreparationValidator(t, nil)
	preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{0x41}, 14), validator,
		NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor), nil)
	request := validWorkPreparationRequest(t, workreport.ModeWaiting)
	request.Action = workreport.ActionWait
	request.OperationKey = workOperationKey(t, request.Identity.WorkID, request.SemanticSequence, request.Action)
	request.Summary = "<script>alert(1)</script> `$(touch owned)` & keep # literal"
	request.WaitingOn = []workreport.WaitingOn{{Kind: workreport.WaitExternal, ID: "upstream:$(false)", Summary: "<b>not HTML</b>"}}

	prepared, err := preparer.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePreparedSubmissionJournal(prepared.Prepared.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeWorkArtifact(t, preparedWorkFiles(t, decoded)[prepared.ArtifactID])
	work := mappingValue(t, doc, "work")
	if work["summary"] != request.Summary {
		t.Fatalf("summary changed or executed: got %#v want %q", work["summary"], request.Summary)
	}
	waits, ok := work["waiting_on"].([]any)
	if !ok || len(waits) != 1 {
		t.Fatalf("waiting reason changed: %#v", work["waiting_on"])
	}
	wait, waitOK := waits[0].(map[string]any)
	if !waitOK || wait["summary"] != request.WaitingOn[0].Summary {
		t.Fatalf("waiting reason changed: %#v", work["waiting_on"])
	}
	if _, err := os.Stat("owned"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hostile text caused a filesystem effect: %v", err)
	}
	fm, err := artifact.ParseFrontmatter(preparedWorkFiles(t, decoded)[prepared.ArtifactID])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(fm.Body, []byte("<script>")) || bytes.Contains(fm.Body, []byte("$(touch")) {
		t.Fatalf("untrusted summary was copied into interpreted Markdown body: %s", fm.Body)
	}
}

func TestWorkPreparerContextAndEvaluatorRefusalsStopBeforeFunnel(t *testing.T) {
	t.Parallel()

	contextErr := errors.New("POL-015 contextual refusal")
	for _, test := range []struct {
		name       string
		membership fold.MembershipStatus
		validator  error
		want       error
	}{
		{name: "contextual", membership: fold.MembershipMember, validator: contextErr, want: contextErr},
		{name: "evaluator", membership: fold.MembershipUnknown, want: ErrWorkEvaluationRefused},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			validator := newWorkPreparationValidator(t, nil)
			validator.err = test.validator
			funnel := &recordingWorkSubmissionPreparer{inner: NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor)}
			runtime := validWorkPreparationRuntime()
			runtime.ReporterMembership = test.membership
			preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{0x51}, 14), validator, funnel, &runtime)
			_, err := preparer.Prepare(context.Background(), validWorkPreparationRequest(t, workreport.ModeImplementing))
			if !errors.Is(err, test.want) {
				t.Fatalf("Prepare error = %v, want errors.Is(_, %v)", err, test.want)
			}
			if funnel.callsCount != 0 {
				t.Fatalf("refused candidate reached funnel %d times", funnel.callsCount)
			}
		})
	}
}

func TestWorkPreparerBindsRuntimeAndRejectsMismatchedContextBeforeFunnel(t *testing.T) {
	t.Parallel()

	validator := newWorkPreparationValidator(t, nil)
	runtime := validWorkPreparationRuntime()
	runtime.Space = "other-space"
	funnel := &recordingWorkSubmissionPreparer{inner: NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor)}
	preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{0x61}, 14), validator, funnel, &runtime)
	if _, err := preparer.Prepare(context.Background(), validWorkPreparationRequest(t, workreport.ModeImplementing)); !errors.Is(err, ErrWorkPreparationScopeMismatch) {
		t.Fatalf("Prepare error = %v, want scope mismatch", err)
	}
	if funnel.callsCount != 0 || validator.contextCalls != 0 {
		t.Fatalf("mismatched runtime crossed boundary: funnel=%d validator=%d", funnel.callsCount, validator.contextCalls)
	}
}

func TestWorkPreparerRejectsUnboundBaseAndRemoteBeforeIDsOrBoundaries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		change func(*WorkPreparationRuntime)
		want   error
	}{
		{name: "empty base", change: func(runtime *WorkPreparationRuntime) { runtime.BaseCommitSHA = "" }, want: ErrWorkPreparationBaseInvalid},
		{name: "short base", change: func(runtime *WorkPreparationRuntime) { runtime.BaseCommitSHA = strings.Repeat("a", 39) }, want: ErrWorkPreparationBaseInvalid},
		{name: "non-hex base", change: func(runtime *WorkPreparationRuntime) { runtime.BaseCommitSHA = strings.Repeat("g", 40) }, want: ErrWorkPreparationBaseInvalid},
		{name: "empty remote", change: func(runtime *WorkPreparationRuntime) { runtime.RemoteURL = "" }, want: ErrWorkPreparationTargetInvalid},
		{name: "whitespace remote", change: func(runtime *WorkPreparationRuntime) { runtime.RemoteURL = " \t\n" }, want: ErrWorkPreparationTargetInvalid},
		{name: "credentialed remote", change: func(runtime *WorkPreparationRuntime) {
			runtime.RemoteURL = "https://token@github.com/acme/checkout-core.git"
		}, want: ErrWorkPreparationTargetInvalid},
		{name: "query remote", change: func(runtime *WorkPreparationRuntime) {
			runtime.RemoteURL = "https://github.com/acme/checkout-core.git?token=secret"
		}, want: ErrWorkPreparationTargetInvalid},
		{name: "wrong repository remote", change: func(runtime *WorkPreparationRuntime) {
			runtime.RemoteURL = "https://github.com/acme/other.git"
		}, want: ErrWorkPreparationTargetInvalid},
		{name: "unbound host remote", change: func(runtime *WorkPreparationRuntime) {
			runtime.RemoteURL = "https://git.example.test/acme/checkout-core.git"
		}, want: ErrWorkPreparationTargetInvalid},
		{name: "empty repository identity", change: func(runtime *WorkPreparationRuntime) {
			runtime.Repository = host.Repo{}
		}, want: ErrWorkPreparationTargetInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			entropy := &countingWorkEntropy{reader: bytes.NewReader(bytes.Repeat([]byte{0x62}, 14))}
			validator := newWorkPreparationValidator(t, &calls)
			funnel := &recordingWorkSubmissionPreparer{
				calls: &calls,
				inner: NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor),
			}
			runtime := validWorkPreparationRuntime()
			test.change(&runtime)
			preparer, err := NewWorkPreparer(entropy, &staticWorkPreparationRuntime{runtime: runtime},
				&shippedWorkAnnouncementRenderer{t: t, calls: &calls}, validator, funnel)
			if err != nil {
				t.Fatal(err)
			}

			_, err = preparer.Prepare(context.Background(), validWorkPreparationRequest(t, workreport.ModeImplementing))
			if !errors.Is(err, test.want) {
				t.Fatalf("Prepare error = %v, want errors.Is(_, %v)", err, test.want)
			}
			if entropy.reads != 0 || len(calls) != 0 || validator.contextCalls != 0 || funnel.callsCount != 0 {
				t.Fatalf("invalid runtime crossed preparation boundary: entropy=%d calls=%v context=%d funnel=%d",
					entropy.reads, calls, validator.contextCalls, funnel.callsCount)
			}
		})
	}
}

func TestWorkPreparerAcceptsAndNormalizesSHA256Base(t *testing.T) {
	t.Parallel()

	runtime := validWorkPreparationRuntime()
	runtime.BaseCommitSHA = strings.Repeat("AB", 32)
	validator := newWorkPreparationValidator(t, nil)
	preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{0x64}, 14), validator,
		NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor), &runtime)

	operation, err := preparer.Prepare(context.Background(), validWorkPreparationRequest(t, workreport.ModeImplementing))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	prepared, err := DecodePreparedSubmissionJournal(operation.Prepared.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Repeat("ab", 32); prepared.data.baseCommitSHA != want {
		t.Fatalf("journal base = %q, want normalized SHA-256 %q", prepared.data.baseCommitSHA, want)
	}
}

func TestWorkPreparerNormalizesAndFreezesBaseForReplayAfterBaseAdvances(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "atlas", "advancer")
	repo := fx.Clone("atlas")
	frozenBase := fx.HeadSHA(repo, "HEAD")
	runtime := validWorkPreparationRuntime()
	runtime.RemoteURL = fx.RemoteURL()
	runtime.BaseCommitSHA = strings.ToUpper(frozenBase)
	validator := newWorkPreparationValidator(t, nil)
	preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{0x63}, 14), validator,
		NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor), &runtime)

	operation, err := preparer.Prepare(context.Background(), validWorkPreparationRequest(t, workreport.ModeImplementing))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	prepared, err := DecodePreparedSubmissionJournal(operation.Prepared.Bytes())
	if err != nil {
		t.Fatalf("DecodePreparedSubmissionJournal: %v", err)
	}
	if prepared.data.baseCommitSHA != frozenBase {
		t.Fatalf("journal base = %q, want normalized frozen SHA %q", prepared.data.baseCommitSHA, frozenBase)
	}

	advanceRepo := fx.Clone("advancer")
	if err := os.WriteFile(filepath.Join(advanceRepo, "advancer", "docs", "later.md"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(context.Background(), advanceRepo, "add", "--all"); err != nil {
		t.Fatal(err)
	}
	identity := []string{
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@a2a.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@a2a.invalid",
	}
	if _, err := runGitOutput(context.Background(), advanceRepo, identity, "commit", "-m", "advance mutable base"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(context.Background(), advanceRepo, "push", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	advanced := fx.HeadSHA(advanceRepo, "HEAD")
	if advanced == frozenBase {
		t.Fatal("fixture base did not advance")
	}

	result, err := NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor).SubmitPrepared(
		context.Background(), prepared, SubmissionRuntime{
			RepoDir: repo, RemoteURL: fx.RemoteURL(), TargetIdentity: repositoryIdentity(runtime.Repository),
			CurrentSpaceFloor: runtime.MinimumBinaryVersion,
		},
	)
	if err != nil {
		t.Fatalf("SubmitPrepared after base advance: %v", err)
	}
	parent, err := runGitOutput(context.Background(), repo, nil, "rev-parse", result.CommitSHA+"^")
	if err != nil {
		t.Fatal(err)
	}
	if parent != frozenBase {
		t.Fatalf("replayed commit parent = %q, want frozen %q (not advanced %q)", parent, frozenBase, advanced)
	}
	if err := runGit(context.Background(), repo, "cat-file", "-e", result.CommitSHA+":advancer/docs/later.md"); err == nil {
		t.Fatal("replayed commit inherited bytes from the advanced mutable base")
	}
}

func TestWorkPreparerProducesStableFinalBytesFromFixedInputs(t *testing.T) {
	t.Parallel()

	request := validWorkPreparationRequest(t, workreport.ModeReviewing)
	prepare := func() workreport.PreparedOperation {
		validator := newWorkPreparationValidator(t, nil)
		preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{0x71}, 14), validator,
			NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor), nil)
		prepared, err := preparer.Prepare(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		return prepared
	}
	left, right := prepare(), prepare()
	if left.ArtifactID != right.ArtifactID || left.EventID != right.EventID || !bytes.Equal(left.Prepared.Bytes(), right.Prepared.Bytes()) {
		t.Fatalf("fixed preparation changed: left=%s/%s right=%s/%s bytesEqual=%v",
			left.ArtifactID, left.EventID, right.ArtifactID, right.EventID, bytes.Equal(left.Prepared.Bytes(), right.Prepared.Bytes()))
	}
}

func TestWorkPreparerRefusesOperationKeyInDurableText(t *testing.T) {
	t.Parallel()

	validator := newWorkPreparationValidator(t, nil)
	funnel := &recordingWorkSubmissionPreparer{inner: NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor)}
	preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{0x72}, 14), validator, funnel, nil)
	request := validWorkPreparationRequest(t, workreport.ModeTesting)
	request.Summary = request.OperationKey
	if _, err := preparer.Prepare(context.Background(), request); !errors.Is(err, ErrWorkOperationKeyLeak) {
		t.Fatalf("Prepare error = %v, want operation-key leak refusal", err)
	}
	if funnel.callsCount != 1 {
		t.Fatalf("funnel calls = %d, want one side-effect-free freeze before final byte scan", funnel.callsCount)
	}
}

func TestWorkPreparerEnforcesPreparedJournalBound(t *testing.T) {
	t.Parallel()

	validator := newWorkPreparationValidator(t, nil)
	runtime := validWorkPreparationRuntime()
	runtime.CommitAuthorName = strings.Repeat("x", 25*1024)
	funnel := NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor)
	preparer := newTestWorkPreparer(t, bytes.Repeat([]byte{0x73}, 14), validator, funnel, &runtime)
	if _, err := preparer.Prepare(context.Background(), validWorkPreparationRequest(t, workreport.ModePlanning)); !errors.Is(err, ErrPreparedJournalTooLarge) {
		t.Fatalf("Prepare error = %v, want prepared journal bound", err)
	}
}

func TestWorkPreparerRequiresDependencies(t *testing.T) {
	t.Parallel()

	validator := newWorkPreparationValidator(t, nil)
	runtime := &staticWorkPreparationRuntime{runtime: validWorkPreparationRuntime()}
	renderer := &shippedWorkAnnouncementRenderer{t: t}
	funnel := NewWriteFunnel(host.NewFakeHost(), validator, version.OperationalConfidenceFloor)
	for _, test := range []struct {
		name      string
		entropy   io.Reader
		runtime   WorkPreparationRuntimeProvider
		renderer  WorkAnnouncementRenderer
		validator WorkCheckpointValidator
		funnel    WorkSubmissionPreparer
	}{
		{name: "entropy", runtime: runtime, renderer: renderer, validator: validator, funnel: funnel},
		{name: "runtime", entropy: bytes.NewReader(nil), renderer: renderer, validator: validator, funnel: funnel},
		{name: "renderer", entropy: bytes.NewReader(nil), runtime: runtime, validator: validator, funnel: funnel},
		{name: "validator", entropy: bytes.NewReader(nil), runtime: runtime, renderer: renderer, funnel: funnel},
		{name: "funnel", entropy: bytes.NewReader(nil), runtime: runtime, renderer: renderer, validator: validator},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewWorkPreparer(test.entropy, test.runtime, test.renderer, test.validator, test.funnel); err == nil {
				t.Fatalf("NewWorkPreparer accepted missing %s", test.name)
			}
		})
	}
}

type workPreparationValidator struct {
	t            *testing.T
	calls        *[]string
	err          error
	contextCalls int
	corpus       *schema.Corpus
}

func newWorkPreparationValidator(t *testing.T, calls *[]string) *workPreparationValidator {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	return &workPreparationValidator{t: t, calls: calls, corpus: corpus}
}

func (v *workPreparationValidator) ValidateWorkCheckpoint(_ context.Context, candidate WorkCheckpointValidation) error {
	v.contextCalls++
	v.record("context")
	if candidate.ArtifactID == "" || candidate.EventID == "" || len(candidate.Artifact) == 0 || len(candidate.Event) == 0 {
		v.t.Fatalf("context validator received incomplete candidate: %+v", candidate)
	}
	if bytes.Contains(candidate.Event, []byte("produced_by:")) {
		v.t.Fatal("context validator received a pre-stamped event")
	}
	if !candidate.ProducerStampPending {
		v.t.Fatal("pre-funnel context validation did not declare the producer-stamp phase")
	}
	event := decodeYAMLObject(v.t, candidate.Event)
	if event["state"] != "published" {
		v.t.Fatalf("context validator ran before canonical evaluation receipt: %#v", event)
	}
	v.validateSchema(candidate.Artifact, candidate.Event)
	return v.err
}

func (v *workPreparationValidator) ValidateSubmitCandidate(_ context.Context, files []FileWrite) error {
	v.record("schema-candidate")
	artifactRaw, eventRaw := splitWorkFiles(v.t, files)
	if bytes.Contains(eventRaw, []byte("produced_by:")) {
		v.t.Fatal("P4 candidate validator received producer metadata")
	}
	v.validateSchema(artifactRaw, eventRaw)
	return nil
}

func (v *workPreparationValidator) ValidateSubmit(_ context.Context, files []FileWrite) error {
	v.record("schema-final")
	artifactRaw, eventRaw := splitWorkFiles(v.t, files)
	if !bytes.Contains(eventRaw, []byte("produced_by:")) {
		v.t.Fatal("P4 final validator did not receive producer-stamped event")
	}
	v.validateSchema(artifactRaw, eventRaw)
	return nil
}

func (v *workPreparationValidator) record(call string) {
	if v.calls != nil {
		*v.calls = append(*v.calls, call)
	}
}

func (v *workPreparationValidator) validateSchema(artifactRaw, eventRaw []byte) {
	doc := decodeWorkArtifact(v.t, artifactRaw)
	violations, err := v.corpus.ValidateEnvelope("announcement", "envelope/v2", doc)
	if err != nil || len(violations) != 0 {
		v.t.Fatalf("announcement/v2 schema: violations=%+v err=%v\n%s", violations, err, artifactRaw)
	}
	event := decodeYAMLObject(v.t, eventRaw)
	violations, err = v.corpus.ValidateEvent("event/v1", event)
	if err != nil || len(violations) != 0 {
		v.t.Fatalf("event/v1 schema: violations=%+v err=%v\n%s", violations, err, eventRaw)
	}
}

type staticWorkPreparationRuntime struct {
	runtime WorkPreparationRuntime
	err     error
	calls   int
}

type shippedWorkAnnouncementRenderer struct {
	t     *testing.T
	calls *[]string
}

func (r *shippedWorkAnnouncementRenderer) RenderWorkAnnouncement(input WorkAnnouncementRenderInput) ([]byte, error) {
	if r.calls != nil {
		*r.calls = append(*r.calls, "render")
	}
	if input.Type != "announcement" || input.EnvelopeSchema != "envelope/v2" {
		return nil, fmt.Errorf("renderer received %s %s", input.EnvelopeSchema, input.Type)
	}
	raw, err := embeddedschemas.FS.ReadFile("templates/v2/announcement.md")
	if err != nil {
		return nil, err
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := yaml.Unmarshal(fm.YAML, &document); err != nil {
		return nil, err
	}
	document["id"] = input.ID
	document["created"] = input.Created.UTC().Format(time.RFC3339)
	document["actor"] = map[string]any{
		"kind": input.Actor.Kind, "name": input.Actor.Name, "model": input.Actor.Model,
	}
	for key, encoded := range input.Fields {
		if key == "actor.session" {
			document["actor"].(map[string]any)["session"] = encoded
			continue
		}
		var value any
		if err := yaml.Unmarshal([]byte(encoded), &value); err != nil {
			return nil, err
		}
		document[key] = value
	}
	yamlRaw, err := yaml.Marshal(document)
	if err != nil {
		return nil, err
	}
	return artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: yamlRaw, Body: bytes.Clone(input.Body)}), nil
}

func (r *staticWorkPreparationRuntime) ResolveWorkPreparation(context.Context) (WorkPreparationRuntime, error) {
	r.calls++
	return r.runtime, r.err
}

type recordingWorkSubmissionPreparer struct {
	inner      WorkSubmissionPreparer
	calls      *[]string
	callsCount int
}

type countingWorkEntropy struct {
	reader io.Reader
	reads  int
}

func (r *countingWorkEntropy) Read(p []byte) (int, error) {
	r.reads++
	return r.reader.Read(p)
}

func (p *recordingWorkSubmissionPreparer) PrepareSubmission(
	ctx context.Context,
	candidate SubmitRequest,
	preparation PreparationContext,
) (PreparedSubmission, error) {
	p.callsCount++
	if p.calls != nil {
		*p.calls = append(*p.calls, "funnel")
	}
	return p.inner.PrepareSubmission(ctx, candidate, preparation)
}

func newTestWorkPreparer(
	t *testing.T,
	entropy []byte,
	validator WorkCheckpointValidator,
	funnel WorkSubmissionPreparer,
	runtimeOverride *WorkPreparationRuntime,
) *WorkPreparer {
	t.Helper()
	runtime := validWorkPreparationRuntime()
	if runtimeOverride != nil {
		runtime = *runtimeOverride
	}
	renderer := &shippedWorkAnnouncementRenderer{t: t}
	if validatorWithCalls, ok := validator.(*workPreparationValidator); ok {
		renderer.calls = validatorWithCalls.calls
	}
	preparer, err := NewWorkPreparer(bytes.NewReader(entropy), &staticWorkPreparationRuntime{runtime: runtime}, renderer, validator, funnel)
	if err != nil {
		t.Fatalf("NewWorkPreparer: %v", err)
	}
	return preparer
}

func validWorkPreparationRuntime() WorkPreparationRuntime {
	return WorkPreparationRuntime{
		ProjectID:            "sha256:" + strings.Repeat("a", 64),
		Space:                "checkout-core",
		MinimumBinaryVersion: version.OperationalConfidenceFloor,
		Repository:           host.Repo{Owner: "acme", Name: "checkout-core"},
		RemoteURL:            "https://github.com/acme/checkout-core.git",
		BaseBranch:           "main",
		BaseCommitSHA:        strings.Repeat("a", 40),
		ReporterMembership:   fold.MembershipMember,
		CommitAuthorName:     "a2a work",
		CommitAuthorEmail:    "a2a-work@localhost",
	}
}

func validWorkPreparationRequest(t *testing.T, mode workreport.Mode) workreport.PreparationRequest {
	t.Helper()
	identity := workIdentityFixture("sha256:" + strings.Repeat("a", 64))
	reported := time.Date(2026, 8, 3, 10, 15, 0, 0, time.UTC)
	request := workreport.PreparationRequest{
		Identity: identity, Action: workreport.ActionCheckpoint,
		SemanticSequence: 2, LocalTarget: workreport.TargetActive,
		SubjectRef: "XW-atlas-20260803-abcd", Mode: mode,
		Summary: "Implementing the accepted contract", ReportedAt: reported,
		ValidUntil: reported.Add(24 * time.Hour), Recipients: []string{"seomatrix"}, Classification: "internal",
	}
	request.OperationKey = workOperationKey(t, identity.WorkID, request.SemanticSequence, request.Action)
	return request
}

func workOperationKey(t *testing.T, workID string, sequence uint64, action workreport.Action) string {
	t.Helper()
	key, err := operation.Work(workID, int64(sequence), string(action))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func preparedWorkFiles(t *testing.T, prepared PreparedSubmission) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	for _, mutation := range prepared.Mutations() {
		base := mutation.Path[strings.LastIndex(mutation.Path, "/")+1:]
		id := strings.TrimSuffix(strings.TrimSuffix(base, ".md"), ".yaml")
		out[id] = append([]byte(nil), mutation.Bytes...)
	}
	return out
}

func sortedPreparedPaths(prepared PreparedSubmission) []string {
	mutations := prepared.Mutations()
	paths := make([]string, len(mutations))
	for i, mutation := range mutations {
		paths[i] = mutation.Path
	}
	return paths
}

func splitWorkFiles(t *testing.T, files []FileWrite) ([]byte, []byte) {
	t.Helper()
	var artifactRaw, eventRaw []byte
	for _, file := range files {
		switch {
		case strings.HasSuffix(file.Path, ".md"):
			artifactRaw = file.Content
		case strings.HasSuffix(file.Path, ".yaml"):
			eventRaw = file.Content
		}
	}
	if len(artifactRaw) == 0 || len(eventRaw) == 0 || len(files) != 2 {
		t.Fatalf("work files = %+v, want exactly one md and one yaml", files)
	}
	return artifactRaw, eventRaw
}

func assertCanonicalWorkArtifact(t *testing.T, raw []byte, request workreport.PreparationRequest, artifactID string) {
	t.Helper()
	doc := decodeWorkArtifact(t, raw)
	for key, want := range map[string]any{
		"schema": "envelope/v2", "id": artifactID, "type": "announcement", "space": request.Identity.Space,
		"from": request.Identity.Actor.System, "category": "status", "blocking": false,
		"ack_requested": false, "classification": request.Classification, "thread": request.Identity.Thread,
	} {
		if got := doc[key]; got != want {
			t.Fatalf("artifact %s = %#v, want %#v\n%s", key, got, want, raw)
		}
	}
	actor := mappingValue(t, doc, "actor")
	for key, want := range map[string]any{
		"kind": request.Identity.Actor.Kind, "name": request.Identity.Actor.Name,
		"model": request.Identity.Actor.Model, "session": request.Identity.Actor.Session,
	} {
		if actor[key] != want {
			t.Fatalf("artifact actor.%s = %#v, want %#v", key, actor[key], want)
		}
	}
	work := mappingValue(t, doc, "work")
	for key, want := range map[string]any{
		"id": request.Identity.WorkID, "semantic_sequence": int(request.SemanticSequence),
		"mode": string(request.Mode), "subject_ref": request.SubjectRef, "summary": request.Summary,
		"reported_at": request.ReportedAt.UTC().Format(time.RFC3339),
		"valid_until": request.ValidUntil.UTC().Format(time.RFC3339),
	} {
		if !reflect.DeepEqual(work[key], want) {
			t.Fatalf("artifact work.%s = %#v, want %#v\n%s", key, work[key], want, raw)
		}
	}
	refs, ok := doc["refs"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("artifact refs = %#v, want subject ref", doc["refs"])
	}
	ref, ok := refs[0].(map[string]any)
	if !ok || ref["ref"] != request.SubjectRef {
		t.Fatalf("artifact refs = %#v, want subject ref", doc["refs"])
	}
}

func assertCanonicalWorkEvent(t *testing.T, raw []byte, request workreport.PreparationRequest, artifactID, eventID string) {
	t.Helper()
	event := decodeYAMLObject(t, raw)
	for key, want := range map[string]any{
		"schema": "event/v1", "event": eventID, "space": request.Identity.Space, "subject": artifactID,
		"transition": "publish", "state": "published", "at": request.ReportedAt.UTC().Format(time.RFC3339),
	} {
		if event[key] != want {
			t.Fatalf("event %s = %#v, want %#v\n%s", key, event[key], want, raw)
		}
	}
	actor := mappingValue(t, event, "actor")
	for key, want := range map[string]any{
		"kind": request.Identity.Actor.Kind, "name": request.Identity.Actor.Name,
		"system": request.Identity.Actor.System, "model": request.Identity.Actor.Model,
		"session": request.Identity.Actor.Session,
	} {
		if actor[key] != want {
			t.Fatalf("event actor.%s = %#v, want %#v", key, actor[key], want)
		}
	}
}

func decodeWorkArtifact(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v\n%s", err, raw)
	}
	return decodeYAMLObject(t, fm.YAML)
}

func decodeYAMLObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var object map[string]any
	if err := yaml.Unmarshal(raw, &object); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\n%s", err, raw)
	}
	return object
}

func mappingValue(t *testing.T, object any, key string) map[string]any {
	t.Helper()
	mapping, ok := object.(map[string]any)
	if !ok {
		t.Fatalf("%s parent = %T, want mapping", key, object)
	}
	value, ok := mapping[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %T, want mapping", key, mapping[key])
	}
	return value
}
