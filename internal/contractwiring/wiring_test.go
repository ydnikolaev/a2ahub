package contractwiring

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// stubRecoveryHost satisfies Host for construction-only tests: it embeds
// *host.FakeHost (which already implements host.Host and host.AutoMerger)
// and adds no-op stand-ins for the four recovery-reader methods FakeHost
// does not implement — sufficient for exercising NewPublicationService's
// assembly, since none of the constructors it calls perform I/O eagerly.
type stubRecoveryHost struct{ *host.FakeHost }

func (stubRecoveryHost) ListContractPublishHeads(context.Context, string, string, string, host.Credential) (host.RemoteHeadListing, error) {
	return host.RemoteHeadListing{}, nil
}

func (stubRecoveryHost) ReadRemoteRecoveryCommit(context.Context, string, string, host.Repo, string, host.Credential) (host.Repo, string, string, string, string, string, string, map[string]host.RemoteRecoveryChange, bool, error) {
	return host.Repo{}, "", "", "", "", "", "", nil, false, nil
}

func (stubRecoveryHost) ReadRemoteRecoveryTreeFiles(context.Context, string, string, []string) (map[string]host.RemoteTreeFile, error) {
	return nil, nil
}

func (stubRecoveryHost) ListRemoteRecoveryTreeFiles(context.Context, string, string, []string) (map[string]host.RemoteTreeFile, error) {
	return nil, nil
}

type stubManifestValidator struct{}

func (stubManifestValidator) ValidateManifest(context.Context, []byte) error { return nil }

type stubHistoryValidator struct{}

func (stubHistoryValidator) ValidateHistoricalContractDocuments(context.Context, space.ContractHistoryDocuments) error {
	return nil
}

func validPublicationDependencies(t *testing.T) PublicationDependencies {
	t.Helper()
	return PublicationDependencies{
		MirrorDir: t.TempDir(), RemoteURL: "file:///dev/null", SpaceID: "test", OwnSystem: "axon",
		Binary: "0.19.0", Repository: host.Repo{Owner: "fixture", Name: "space"}, Credential: host.Credential{Token: "t"},
		Host: stubRecoveryHost{FakeHost: host.NewFakeHost()}, Engine: newTestEngine(t),
		ManifestValidator: stubManifestValidator{}, HistoryValidator: stubHistoryValidator{}, Compatibility: validate.ContractCompatibilityAdapter{},
		Actor: template.Actor{Kind: "agent", Name: "tester"}, Now: time.Now, Entropy: bytes.NewReader(make([]byte, 16)),
		LoadManifest: func() (space.Manifest, error) { return space.Manifest{}, nil },
		ValidateSubmitFiles: func(context.Context, space.Manifest, []space.FileWrite) error {
			return nil
		},
	}
}

// TestNewPublicationServiceAssemblesFromCompleteDependencies is the
// constructive proof that NewPublicationService performs the FULL
// production assembly (repository, event builder, submit validator, funnel,
// recovery, service) without erroring given a complete dependency set —
// none of the constructors it calls perform I/O eagerly, so this needs no
// real git mirror.
func TestNewPublicationServiceAssemblesFromCompleteDependencies(t *testing.T) {
	t.Parallel()
	service, err := NewPublicationService(validPublicationDependencies(t))
	if err != nil || service == nil {
		t.Fatalf("NewPublicationService(complete deps) = (%v, %v), want a non-nil service and nil error", service, err)
	}
}

// TestNewPublicationServiceRejectsIncompleteDependencies covers
// PublicationDependencies.validate's every required field, one omission at
// a time — the constructor rejection paths.
func TestNewPublicationServiceRejectsIncompleteDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*PublicationDependencies)
	}{
		{name: "missing mirror dir", mutate: func(d *PublicationDependencies) { d.MirrorDir = "" }},
		{name: "missing remote url", mutate: func(d *PublicationDependencies) { d.RemoteURL = "" }},
		{name: "missing space id", mutate: func(d *PublicationDependencies) { d.SpaceID = "" }},
		{name: "missing own system", mutate: func(d *PublicationDependencies) { d.OwnSystem = "" }},
		{name: "missing binary", mutate: func(d *PublicationDependencies) { d.Binary = "" }},
		{name: "missing repository owner", mutate: func(d *PublicationDependencies) { d.Repository.Owner = "" }},
		{name: "missing repository name", mutate: func(d *PublicationDependencies) { d.Repository.Name = "" }},
		{name: "nil host", mutate: func(d *PublicationDependencies) { d.Host = nil }},
		{name: "nil engine", mutate: func(d *PublicationDependencies) { d.Engine = nil }},
		{name: "nil manifest validator", mutate: func(d *PublicationDependencies) { d.ManifestValidator = nil }},
		{name: "nil history validator", mutate: func(d *PublicationDependencies) { d.HistoryValidator = nil }},
		{name: "nil compatibility", mutate: func(d *PublicationDependencies) { d.Compatibility = nil }},
		{name: "nil now", mutate: func(d *PublicationDependencies) { d.Now = nil }},
		{name: "nil entropy", mutate: func(d *PublicationDependencies) { d.Entropy = nil }},
		{name: "nil load manifest", mutate: func(d *PublicationDependencies) { d.LoadManifest = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deps := validPublicationDependencies(t)
			test.mutate(&deps)
			service, err := NewPublicationService(deps)
			if err == nil || service != nil {
				t.Fatalf("NewPublicationService(%s) = (%v, %v), want (nil, non-nil error)", test.name, service, err)
			}
		})
	}
}

// TestPublicationEventLookupErrorShapes covers
// contractPublicationSubmitValidator.publicationEvent's three failure
// shapes: no evaluated event on the builder, the event path missing from
// the submitted files, and the event path duplicated across them.
func TestPublicationEventLookupErrorShapes(t *testing.T) {
	t.Parallel()

	t.Run("no evaluated event", func(t *testing.T) {
		t.Parallel()
		v := &contractPublicationSubmitValidator{events: &contractPublicationEventBuilder{}}
		if _, err := v.publicationEvent(nil); err == nil {
			t.Fatal("publicationEvent with no evaluated event = nil error, want an error")
		}
	})

	t.Run("event missing from files", func(t *testing.T) {
		t.Parallel()
		v := &contractPublicationSubmitValidator{events: &contractPublicationEventBuilder{eventPath: "axon/events/2026/x.yaml"}}
		if _, err := v.publicationEvent([]space.FileWrite{{Path: "other/path.yaml", Content: []byte("x")}}); err == nil {
			t.Fatal("publicationEvent with the event path absent = nil error, want an error")
		}
	})

	t.Run("event path duplicated", func(t *testing.T) {
		t.Parallel()
		path := "axon/events/2026/x.yaml"
		v := &contractPublicationSubmitValidator{events: &contractPublicationEventBuilder{eventPath: path}}
		files := []space.FileWrite{{Path: path, Content: []byte("a")}, {Path: path, Content: []byte("b")}}
		if _, err := v.publicationEvent(files); err == nil {
			t.Fatal("publicationEvent with a duplicated event path = nil error, want an error")
		}
	})

	t.Run("event present exactly once", func(t *testing.T) {
		t.Parallel()
		path := "axon/events/2026/x.yaml"
		v := &contractPublicationSubmitValidator{events: &contractPublicationEventBuilder{eventPath: path}}
		raw, err := v.publicationEvent([]space.FileWrite{{Path: path, Content: []byte("payload")}})
		if err != nil || string(raw) != "payload" {
			t.Fatalf("publicationEvent = (%q, %v), want (\"payload\", nil)", raw, err)
		}
	})
}

// TestValidateSubmitCandidateAcceptsExactUnstampedEventAndRejectsDrift
// covers ValidateSubmitCandidate's own two outcomes: it accepts the exact
// bytes BuildContractPublicationEvent produced (validated against "0.0.0" —
// the candidate stage runs before any producer-stamp floor is known), and
// rejects the same event once a single byte has drifted from what the
// builder evaluated, guarding against a candidate submitted with a
// different event than the one fold.EvaluateCandidate actually scored.
func TestValidateSubmitCandidateAcceptsExactUnstampedEventAndRejectsDrift(t *testing.T) {
	t.Parallel()

	plan := testContractPublicationPlan(t, contract.ContractPublicationFloor, true)
	engine := newTestEngine(t)
	builder := &contractPublicationEventBuilder{
		mirrorDir: t.TempDir(), spaceID: "test", ownSystem: "axon",
		actor: template.Actor{Kind: "agent", Name: "publisher", Model: "codex"}, engine: engine,
		now:     func() time.Time { return time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC) },
		entropy: bytes.NewReader(make([]byte, 16)),
		loadManifest: func() (space.Manifest, error) {
			return space.Manifest{Participants: []space.Participant{{System: "axon", Status: "active"}}}, nil
		},
	}
	write, err := builder.BuildContractPublicationEvent(t.Context(), plan)
	if err != nil {
		t.Fatalf("BuildContractPublicationEvent: %v", err)
	}
	validator := &contractPublicationSubmitValidator{events: builder, engine: engine}

	if err := validator.ValidateSubmitCandidate(t.Context(), []space.FileWrite{write}); err != nil {
		t.Fatalf("ValidateSubmitCandidate(exact event) = %v, want nil", err)
	}

	mutated := append(append([]byte(nil), write.Content...), '\n')
	drifted := []space.FileWrite{{Path: write.Path, Content: mutated}}
	if err := validator.ValidateSubmitCandidate(t.Context(), drifted); err == nil {
		t.Fatal("ValidateSubmitCandidate with a mutated event = nil error, want a rejection")
	}
}

// TestContractPublicationToStrings covers every case
// contractPublicationToStrings switches on: a bare string, a []any of
// strings (dropping non-string elements), an already-typed []string, and
// an unrecognised shape.
func TestContractPublicationToStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  []string
	}{
		{name: "bare string", value: "beta", want: []string{"beta"}},
		{name: "any slice of strings", value: []any{"beta", "gamma"}, want: []string{"beta", "gamma"}},
		{name: "any slice drops non-strings", value: []any{"beta", 7, "gamma"}, want: []string{"beta", "gamma"}},
		{name: "typed string slice", value: []string{"beta", "gamma"}, want: []string{"beta", "gamma"}},
		{name: "unrecognised shape", value: 42, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := contractPublicationToStrings(test.value)
			if !stringSlicesEqual(got, test.want) {
				t.Fatalf("contractPublicationToStrings(%#v) = %#v, want %#v", test.value, got, test.want)
			}
		})
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestContractPublicationMembership covers every fold.MembershipStatus
// contractPublicationMembership's returned view can report: an active
// participant, one that has left, and a system absent from the manifest
// entirely.
func TestContractPublicationMembership(t *testing.T) {
	t.Parallel()

	manifest := space.Manifest{Participants: []space.Participant{
		{System: "beta", Status: "active"},
		{System: "gamma", Status: "left"},
	}}
	view := contractPublicationMembership(manifest)

	if got := view("beta"); got != fold.MembershipMember {
		t.Fatalf("view(beta) = %v, want %v", got, fold.MembershipMember)
	}
	if got := view("gamma"); got != fold.MembershipLeft {
		t.Fatalf("view(gamma) = %v, want %v", got, fold.MembershipLeft)
	}
	if got := view("delta"); got != fold.MembershipUnknown {
		t.Fatalf("view(delta) = %v, want %v", got, fold.MembershipUnknown)
	}
}

func TestFreezePublicationCandidateRejectsUnownedID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		contractID string
	}{
		{name: "malformed id", contractID: "not-an-id"},
		{name: "wrong prefix", contractID: "XN-axon-orders"},
		{name: "wrong owning system", contractID: "XC-beta-orders"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, source, err := FreezePublicationCandidate(context.Background(), "axon", "", t.TempDir(), test.contractID, "")
			if err == nil {
				t.Fatalf("FreezePublicationCandidate(%q) = nil error, want a rejection", test.contractID)
			}
			if source != (contract.CandidateSource{}) {
				t.Fatalf("FreezePublicationCandidate(%q) source = %#v, want the zero value", test.contractID, source)
			}
			if !strings.Contains(err.Error(), test.contractID) {
				t.Fatalf("FreezePublicationCandidate(%q) error = %q, want it to name the rejected id", test.contractID, err.Error())
			}
		})
	}
}
