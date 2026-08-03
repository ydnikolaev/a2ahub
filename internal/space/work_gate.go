package space

import (
	"context"
	"errors"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/version"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

var (
	// ErrWorkFeatureFloorInactive is the typed pre-mutation refusal used when
	// an authoritative manifest has not activated envelope/v2 work authoring.
	// ErrWorkFeatureFloorInactive is part of the public package API.
	ErrWorkFeatureFloorInactive = errors.New("space: work feature floor is inactive")
	// ErrWorkAuthoringScopeMismatch refuses a resolver result for another
	// project or space instead of treating it as authority for this command.
	// ErrWorkAuthoringScopeMismatch is part of the public package API.
	ErrWorkAuthoringScopeMismatch = errors.New("space: work authoring scope does not match authoritative manifest")
	// ErrWorkManifestFloorInvalid fails closed when the authoritative floor is
	// absent or not the manifest's canonical dotted version shape.
	// ErrWorkManifestFloorInvalid is part of the public package API.
	ErrWorkManifestFloorInvalid = errors.New("space: authoritative work manifest floor is invalid")
)

// WorkManifestFloor is the complete authoritative binding needed to activate
// work authoring. It intentionally has no producer metadata.
// WorkManifestFloor is part of the public package API.
type WorkManifestFloor struct {
	ProjectID            string
	Space                string
	MinimumBinaryVersion string
}

// WorkManifestFloorResolver resolves the currently configured project's
// already-validated manifest. The adapter is injected at cmd/a2a and owns any
// I/O; the gate itself is pure apart from this one read.
// WorkManifestFloorResolver is part of the public package API.
type WorkManifestFloorResolver interface {
	ResolveWorkManifestFloor(context.Context) (WorkManifestFloor, error)
}

// WorkAuthoringGate implements workreport.WorkAuthoringGate from authoritative
// manifest state.
// WorkAuthoringGate is part of the public package API.
type WorkAuthoringGate struct {
	resolver WorkManifestFloorResolver
}

// NewWorkAuthoringGate is part of the public package API.
func NewWorkAuthoringGate(resolver WorkManifestFloorResolver) (*WorkAuthoringGate, error) {
	if resolver == nil {
		return nil, fmt.Errorf("space: work manifest floor resolver is required")
	}
	return &WorkAuthoringGate{resolver: resolver}, nil
}

// AuthorizeWork is part of the public package API.
func (g *WorkAuthoringGate) AuthorizeWork(ctx context.Context, scope workreport.WorkAuthoringScope) error {
	binding, err := g.resolver.ResolveWorkManifestFloor(ctx)
	if err != nil {
		return fmt.Errorf("space: resolve authoritative work manifest floor: %w", err)
	}
	if scope.ProjectID == "" || scope.Space == "" || binding.ProjectID != scope.ProjectID || binding.Space != scope.Space {
		return fmt.Errorf("%w: requested project/space is not the resolved manifest", ErrWorkAuthoringScopeMismatch)
	}
	canonical, err := version.Canonical(binding.MinimumBinaryVersion)
	if err != nil || canonical != binding.MinimumBinaryVersion {
		return fmt.Errorf("%w: %q", ErrWorkManifestFloorInvalid, binding.MinimumBinaryVersion)
	}
	older, err := version.OlderThan(binding.MinimumBinaryVersion, version.OperationalConfidenceFloor)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrWorkManifestFloorInvalid, binding.MinimumBinaryVersion)
	}
	if older {
		return fmt.Errorf("%w: manifest floor %s is below %s", ErrWorkFeatureFloorInactive, binding.MinimumBinaryVersion, version.OperationalConfidenceFloor)
	}
	return nil
}
