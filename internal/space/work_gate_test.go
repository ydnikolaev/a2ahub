package space

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/version"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

func TestWorkAuthoringGateUsesAuthoritativeManifestFloor(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		floor   string
		wantErr error
	}{
		{name: "below floor", floor: "0.18.9", wantErr: ErrWorkFeatureFloorInactive},
		{name: "equal floor", floor: version.OperationalConfidenceFloor},
		{name: "higher floor", floor: "0.20.0"},
		{name: "malformed floor", floor: "latest", wantErr: ErrWorkManifestFloorInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resolver := &workFloorResolver{binding: WorkManifestFloor{
				ProjectID: "sha256:" + strings.Repeat("a", 64), Space: "checkout-core", MinimumBinaryVersion: test.floor,
			}}
			gate, err := NewWorkAuthoringGate(resolver)
			if err != nil {
				t.Fatal(err)
			}
			err = gate.AuthorizeWork(context.Background(), workreport.WorkAuthoringScope{
				ProjectID: resolver.binding.ProjectID, Space: resolver.binding.Space,
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("AuthorizeWork error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
			if resolver.calls != 1 {
				t.Fatalf("resolver calls = %d, want 1", resolver.calls)
			}
		})
	}
}

func TestWorkAuthoringGateRequiresExactProjectAndSpaceBinding(t *testing.T) {
	t.Parallel()

	binding := WorkManifestFloor{
		ProjectID: "sha256:" + strings.Repeat("a", 64),
		Space:     "checkout-core", MinimumBinaryVersion: version.OperationalConfidenceFloor,
	}
	for _, scope := range []workreport.WorkAuthoringScope{
		{ProjectID: "sha256:" + strings.Repeat("b", 64), Space: binding.Space},
		{ProjectID: binding.ProjectID, Space: "other-space"},
	} {
		resolver := &workFloorResolver{binding: binding}
		gate, err := NewWorkAuthoringGate(resolver)
		if err != nil {
			t.Fatal(err)
		}
		if err := gate.AuthorizeWork(context.Background(), scope); !errors.Is(err, ErrWorkAuthoringScopeMismatch) {
			t.Fatalf("AuthorizeWork(%+v) error = %v, want scope mismatch", scope, err)
		}
	}
}

func TestWorkAuthoringGateFailsClosedOnResolverErrorAndRequiresDependency(t *testing.T) {
	t.Parallel()

	if _, err := NewWorkAuthoringGate(nil); err == nil {
		t.Fatal("NewWorkAuthoringGate(nil) succeeded")
	}
	want := errors.New("manifest unavailable")
	gate, err := NewWorkAuthoringGate(&workFloorResolver{err: want})
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.AuthorizeWork(context.Background(), workreport.WorkAuthoringScope{}); !errors.Is(err, want) {
		t.Fatalf("AuthorizeWork error = %v, want resolver error", err)
	}
}

type workFloorResolver struct {
	binding WorkManifestFloor
	err     error
	calls   int
}

func (r *workFloorResolver) ResolveWorkManifestFloor(context.Context) (WorkManifestFloor, error) {
	r.calls++
	return r.binding, r.err
}
