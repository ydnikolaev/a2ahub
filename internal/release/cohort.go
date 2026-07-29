package release

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/version"
)

const (
	// CohortSchemaV1 is the first signed release-cohort wire contract.
	CohortSchemaV1 = "release-cohort/v1"
	// ReleaseNotesSchemaV1 is the only future-detail schema understood by a
	// P49-era binary. A future schema is intentionally not best-effort parsed.
	ReleaseNotesSchemaV1 = "release-notes/v1"
	// MacOSBundleID is the fixed identity required by P49 T4.1.
	MacOSBundleID = "io.a2ahub.notifier"
	// CohortAssetName is fetched and verified before an older binary trusts
	// any future component or release-note identity.
	CohortAssetName = "release-cohort.json"

	maxCohortBytes = 1 << 20
)

var (
	// ErrCohortMalformed means the signed manifest could not be decoded.
	ErrCohortMalformed = errors.New("release: malformed cohort manifest")
	// ErrCohortTooLarge rejects an unbounded manifest.
	ErrCohortTooLarge = errors.New("release: cohort manifest exceeds size limit")
	// ErrCohortInvalid rejects cross-field or identity violations.
	ErrCohortInvalid = errors.New("release: invalid cohort manifest")
	// ErrCohortVerification means manifest signature trust was not established.
	ErrCohortVerification = errors.New("release: cohort verification failed")
	// ErrCohortAssetInvalid rejects a component hash/signature mismatch.
	ErrCohortAssetInvalid = errors.New("release: cohort asset verification failed")
	// ErrProtocolIncompatible rejects a cohort outside the N/N-1 window.
	ErrProtocolIncompatible = errors.New("release: cohort protocol is incompatible")
)

// ProtocolRange is the inclusive protocol compatibility window carried by a
// cohort. V1 accepts the current protocol and at most its immediate predecessor.
type ProtocolRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// ComponentKind names the four independently verifiable parts of one release
// cohort. Multiple CLI components are allowed for platform/architecture
// variants; the other kinds occur exactly once.
type ComponentKind string

const (
	// ComponentCLI identifies a raw platform a2a binary.
	ComponentCLI ComponentKind = "cli"
	// ComponentMacOS identifies the signed native application archive.
	ComponentMacOS ComponentKind = "macos"
	// ComponentVSCode identifies the CLI-managed VSIX.
	ComponentVSCode ComponentKind = "vscode"
	// ComponentReleaseNotes identifies standalone future What’s New detail.
	ComponentReleaseNotes ComponentKind = "release-notes"
)

// CohortComponent binds an asset name and digest to the versions and platform
// identity that the installer must observe before it mutates an installation.
// Fields irrelevant to a kind must be empty, preventing identity ambiguity.
type CohortComponent struct {
	Kind            ComponentKind `json:"kind"`
	Name            string        `json:"name"`
	SHA256          string        `json:"sha256"`
	ProductVersion  string        `json:"product_version"`
	ProtocolVersion int           `json:"protocol_version"`
	BundleID        string        `json:"bundle_id,omitempty"`
	TeamID          string        `json:"team_id,omitempty"`
	Publisher       string        `json:"publisher,omitempty"`
	ExtensionID     string        `json:"extension_id,omitempty"`
	Schema          string        `json:"schema,omitempty"`
}

// CohortManifest is the bounded, signed release-cohort document. Component
// assets are separately signed and hashed; signing this manifest binds them as
// one coordinated product release.
type CohortManifest struct {
	Schema         string            `json:"schema"`
	ProductVersion string            `json:"product_version"`
	Protocol       ProtocolRange     `json:"protocol"`
	Components     []CohortComponent `json:"components"`
}

// Compatible reports whether protocol is inside this cohort's declared N/N-1
// overlap window.
func (m CohortManifest) Compatible(protocol int) bool {
	return protocol >= m.Protocol.Min && protocol <= m.Protocol.Max
}

// CheckCompatibility returns a stable sentinel when protocol is outside the
// signed window.
func (m CohortManifest) CheckCompatibility(protocol int) error {
	if !m.Compatible(protocol) {
		return fmt.Errorf("%w: local=%d cohort=%d..%d", ErrProtocolIncompatible, protocol, m.Protocol.Min, m.Protocol.Max)
	}
	return nil
}

// ParseCohort strictly decodes one size-bounded JSON manifest and validates
// every cross-field binding. Unknown fields and trailing JSON fail closed.
func ParseCohort(r io.Reader) (CohortManifest, error) {
	if r == nil {
		return CohortManifest{}, fmt.Errorf("%w: nil reader", ErrCohortMalformed)
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxCohortBytes+1))
	if err != nil {
		return CohortManifest{}, fmt.Errorf("%w: read: %w", ErrCohortMalformed, err)
	}
	if len(raw) > maxCohortBytes {
		return CohortManifest{}, ErrCohortTooLarge
	}

	var manifest CohortManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return CohortManifest{}, fmt.Errorf("%w: %w", ErrCohortMalformed, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return CohortManifest{}, fmt.Errorf("%w: trailing JSON", ErrCohortMalformed)
	}
	if err := validateCohort(manifest); err != nil {
		return CohortManifest{}, err
	}
	return manifest, nil
}

func validateCohort(manifest CohortManifest) error {
	if manifest.Schema != CohortSchemaV1 {
		return fmt.Errorf("%w: unsupported schema %q", ErrCohortInvalid, manifest.Schema)
	}
	if canonical, err := version.Canonical(manifest.ProductVersion); err != nil || canonical != manifest.ProductVersion {
		return fmt.Errorf("%w: product version %q", ErrCohortInvalid, manifest.ProductVersion)
	}
	if manifest.Protocol.Min < 1 || manifest.Protocol.Max < manifest.Protocol.Min || manifest.Protocol.Max-manifest.Protocol.Min > 1 {
		return fmt.Errorf("%w: protocol window %d..%d is not N/N-1", ErrCohortInvalid, manifest.Protocol.Min, manifest.Protocol.Max)
	}
	if len(manifest.Components) < 4 || len(manifest.Components) > 64 {
		return fmt.Errorf("%w: component count %d", ErrCohortInvalid, len(manifest.Components))
	}

	names := make(map[string]struct{}, len(manifest.Components))
	counts := make(map[ComponentKind]int, 4)
	for i, component := range manifest.Components {
		if err := validateCohortComponent(manifest, component); err != nil {
			return fmt.Errorf("%w: component %d: %w", ErrCohortInvalid, i, err)
		}
		if _, exists := names[component.Name]; exists {
			return fmt.Errorf("%w: duplicate asset name %q", ErrCohortInvalid, component.Name)
		}
		names[component.Name] = struct{}{}
		counts[component.Kind]++
	}
	if counts[ComponentCLI] < 1 || counts[ComponentMacOS] != 1 ||
		counts[ComponentVSCode] != 1 || counts[ComponentReleaseNotes] != 1 {
		return fmt.Errorf(
			"%w: need cli>=1, macos=1, vscode=1, release-notes=1",
			ErrCohortInvalid,
		)
	}
	return nil
}

func validateCohortComponent(manifest CohortManifest, component CohortComponent) error {
	if component.Name == "" || filepath.Base(component.Name) != component.Name ||
		component.Name == "." || strings.ContainsAny(component.Name, "\x00\r\n") {
		return fmt.Errorf("unsafe asset name %q", component.Name)
	}
	if len(component.SHA256) != 64 || !isHex(component.SHA256) {
		return fmt.Errorf("invalid sha256 for %q", component.Name)
	}
	if component.ProductVersion != manifest.ProductVersion {
		return fmt.Errorf("product version %q does not match cohort", component.ProductVersion)
	}
	if !manifest.Compatible(component.ProtocolVersion) {
		return fmt.Errorf("protocol %d is outside cohort window", component.ProtocolVersion)
	}

	switch component.Kind {
	case ComponentCLI:
		if component.BundleID != "" || component.TeamID != "" || component.Publisher != "" ||
			component.ExtensionID != "" || component.Schema != "" {
			return errors.New("cli carries unrelated identity fields")
		}
	case ComponentMacOS:
		if component.BundleID != MacOSBundleID || component.TeamID == "" {
			return errors.New("macos bundle/team identity is invalid")
		}
		if component.Publisher != "" || component.ExtensionID != "" || component.Schema != "" {
			return errors.New("macos carries unrelated identity fields")
		}
	case ComponentVSCode:
		if component.Publisher == "" || component.ExtensionID == "" ||
			!strings.HasPrefix(component.ExtensionID, component.Publisher+".") {
			return errors.New("vscode publisher/extension identity is invalid")
		}
		if component.BundleID != "" || component.TeamID != "" || component.Schema != "" {
			return errors.New("vscode carries unrelated identity fields")
		}
	case ComponentReleaseNotes:
		if component.Schema == "" {
			return errors.New("release notes schema is empty")
		}
		if component.BundleID != "" || component.TeamID != "" || component.Publisher != "" ||
			component.ExtensionID != "" {
			return errors.New("release notes carry unrelated identity fields")
		}
	default:
		return fmt.Errorf("unknown component kind %q", component.Kind)
	}
	return nil
}

// VerifiedCohort is an unforgeable-in-normal-use marker that the cohort file
// passed the existing signature/checksum verifier before its fields were used.
// Its zero value is rejected by staging/detail operations.
type VerifiedCohort struct {
	manifest CohortManifest
}

// Manifest returns a copy of the signed manifest. Its component slice is
// cloned so callers cannot mutate the verification token's internal value.
func (v VerifiedCohort) Manifest() CohortManifest {
	out := v.manifest
	out.Components = append([]CohortComponent(nil), v.manifest.Components...)
	return out
}

// VerifyCohort verifies the manifest through the package's existing Verifier
// seam, then parses it and binds its product version to the resolved release.
func VerifyCohort(ctx context.Context, path string, rel Release, verifier Verifier) (VerifiedCohort, error) {
	if verifier == nil {
		return VerifiedCohort{}, fmt.Errorf("%w: nil verifier", ErrCohortVerification)
	}
	if err := verifier.Verify(ctx, path, rel); err != nil {
		return VerifiedCohort{}, fmt.Errorf("%w: %w", ErrCohortVerification, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return VerifiedCohort{}, fmt.Errorf("%w: open manifest: %w", ErrCohortMalformed, err)
	}
	defer func() { _ = file.Close() }()
	manifest, err := ParseCohort(file)
	if err != nil {
		return VerifiedCohort{}, err
	}
	if rel.Version == "" || manifest.ProductVersion != rel.Version {
		return VerifiedCohort{}, fmt.Errorf(
			"%w: cohort product %q does not match release %q",
			ErrCohortInvalid,
			manifest.ProductVersion,
			rel.Version,
		)
	}
	return VerifiedCohort{manifest: manifest}, nil
}
