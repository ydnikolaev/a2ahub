package cache

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestProjectContractDependenciesDriftAndDeterminism(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeDependencyRegistry(t, dir, "checkout", `schema: consumes/v1
system: checkout
dependencies:
  - contract: XC-provider-behind
    major: 1
  - contract: XC-provider-current
    major: 1
  - contract: XC-provider-dangling
    major: 1
  - contract: XC-provider-deprecated
    major: 1
  - contract: XC-provider-missing
    major: 1
`)
	mirrors := []SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: space.Manifest{Participants: []space.Participant{
		{System: "checkout", Section: "checkout", Status: "active"},
		{System: "provider", Section: "provider", Status: "active"},
	}}}}
	contracts := []ContractInfo{
		{Space: "sp1", ID: "XC-provider-current", Provider: "provider", Version: "1.4.0", State: "published", Description: "current contract",
			Versions: []ContractVersion{{Version: "1.4.0", State: "published"}}},
		{Space: "sp1", ID: "XC-provider-behind", Provider: "provider", Version: "2.1.0", State: "published",
			Versions: []ContractVersion{{Version: "1.9.0", State: "published"}, {Version: "2.1.0", State: "published"}}},
		{Space: "sp1", ID: "XC-provider-deprecated", Provider: "provider", Version: "1.5.0", State: "deprecated",
			Versions: []ContractVersion{{Version: "1.5.0", State: "deprecated", Sunset: "2026-09-01", Successor: "XC-provider-next"}}},
		{Space: "sp1", ID: "XC-provider-missing", Provider: "provider", Version: "2.0.0", State: "published",
			Versions: []ContractVersion{{Version: "2.0.0", State: "published"}}},
	}

	first := projectContractDependencies(mirrors, contracts)
	second := projectContractDependencies(mirrors, contracts)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same inputs produced different dependency projections\nfirst: %#v\nsecond: %#v", first, second)
	}
	if !first.Complete || len(first.Degradations) != 0 {
		t.Fatalf("complete readable scan = %+v, want Complete with no degradation", first)
	}
	if len(first.Edges) != 5 {
		t.Fatalf("Edges = %+v, want 5", first.Edges)
	}
	wantDrift := map[string]string{
		"XC-provider-behind":     DependencyDriftBehind,
		"XC-provider-current":    DependencyDriftCurrent,
		"XC-provider-dangling":   DependencyDriftDangling,
		"XC-provider-deprecated": DependencyDriftDeprecated,
		"XC-provider-missing":    DependencyDriftMissing,
	}
	for i, edge := range first.Edges {
		if i > 0 && first.Edges[i-1].Contract > edge.Contract {
			t.Fatalf("edges are not deterministic by contract: %+v", first.Edges)
		}
		if edge.Drift != wantDrift[edge.Contract] {
			t.Errorf("%s drift = %q, want %q", edge.Contract, edge.Drift, wantDrift[edge.Contract])
		}
	}
	behind := dependencyEdgeByContract(t, first.Edges, "XC-provider-behind")
	if behind.PinnedVersion != "1.9.0" || behind.ProviderVersion != "2.1.0" ||
		!reflect.DeepEqual(behind.AvailableMajors, []int{1, 2}) {
		t.Fatalf("behind edge lost rolling-window facts: %+v", behind)
	}
	deprecated := dependencyEdgeByContract(t, first.Edges, "XC-provider-deprecated")
	if deprecated.PinnedState != "deprecated" || deprecated.Sunset != "2026-09-01" || deprecated.Successor != "XC-provider-next" {
		t.Fatalf("deprecated edge lost pinned-line facts: %+v", deprecated)
	}
	missing := dependencyEdgeByContract(t, first.Edges, "XC-provider-missing")
	if missing.PinnedState != "missing" || !reflect.DeepEqual(missing.AvailableMajors, []int{2}) {
		t.Fatalf("missing-major edge = %+v", missing)
	}
	current := dependencyEdgeByContract(t, first.Edges, "XC-provider-current")
	if current.Description != "current contract" {
		t.Fatalf("current edge Description = %q", current.Description)
	}
}

func TestProjectContractDependenciesAbsentRegistryIsNormal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	projection := projectContractDependencies([]SpaceMirror{{
		SpaceID: "sp1", Dir: dir,
		Manifest: space.Manifest{Participants: []space.Participant{{System: "checkout", Section: "checkout"}}},
	}}, nil)
	if !projection.Complete || len(projection.Edges) != 0 || len(projection.Degradations) != 0 {
		t.Fatalf("absent optional consumes.yaml = %+v, want complete empty projection", projection)
	}
	if projection.Edges == nil || projection.Degradations == nil {
		t.Fatalf("empty projection must use non-nil fresh slices: %+v", projection)
	}
}

func TestProjectContractDependenciesReportsMalformedUnreadableAndIncomplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		section  string
		prepare  func(*testing.T, string)
		wantCode string
	}{
		{
			name: "malformed", section: "checkout",
			prepare: func(t *testing.T, dir string) {
				writeDependencyRegistry(t, dir, "checkout", "schema: [\n")
			},
			wantCode: DependencyDegradationMalformedRegistry,
		},
		{
			name: "oversized", section: "checkout",
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				path := filepath.Join(dir, "checkout", "consumes.yaml")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir registry: %v", err)
				}
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxCacheReadBytes+1), 0o644); err != nil {
					t.Fatalf("write oversized registry: %v", err)
				}
			},
			wantCode: DependencyDegradationUnreadableRegistry,
		},
		{
			name: "system_mismatch", section: "checkout",
			prepare: func(t *testing.T, dir string) {
				writeDependencyRegistry(t, dir, "checkout", "schema: consumes/v1\nsystem: somebody-else\ndependencies: []\n")
			},
			wantCode: DependencyDegradationIncompleteRegistry,
		},
		{
			name: "invalid_dependency", section: "checkout",
			prepare: func(t *testing.T, dir string) {
				writeDependencyRegistry(t, dir, "checkout", "schema: consumes/v1\nsystem: checkout\ndependencies:\n  - contract: ''\n    major: 0\n")
			},
			wantCode: DependencyDegradationIncompleteDependency,
		},
		{
			name: "unsafe_section", section: "../outside",
			prepare:  func(t *testing.T, dir string) {},
			wantCode: DependencyDegradationIncompleteRegistry,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tc.prepare(t, dir)
			projection := projectContractDependencies([]SpaceMirror{{
				SpaceID: "sp1", Dir: dir,
				Manifest: space.Manifest{Participants: []space.Participant{{System: "checkout", Section: tc.section}}},
			}}, nil)
			if projection.Complete {
				t.Fatalf("projection.Complete = true for %s input", tc.name)
			}
			if len(projection.Edges) != 0 || len(projection.Degradations) != 1 {
				t.Fatalf("projection = %+v, want no edges and one named degradation", projection)
			}
			if got := projection.Degradations[0]; got.Code != tc.wantCode || got.Space != "sp1" || got.System != "checkout" || got.Reason == "" {
				t.Fatalf("degradation = %+v, want code=%q plus space/system/reason", got, tc.wantCode)
			}
		})
	}
}

func TestStoreContractDependenciesUsesMirrorProjection(t *testing.T) {
	t.Parallel()

	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	fxWriteConsumes(t, fx, "axon", "XC-seomatrix-missing", 1)
	store := newThreadStore(t, fx, "sp1")

	projection, err := store.ContractDependencies(context.Background())
	if err != nil {
		t.Fatalf("ContractDependencies: %v", err)
	}
	if !projection.Complete || len(projection.Degradations) != 0 || len(projection.Edges) != 1 {
		t.Fatalf("ContractDependencies = %+v, want one complete dangling edge", projection)
	}
	if edge := projection.Edges[0]; edge.Contract != "XC-seomatrix-missing" || edge.Drift != DependencyDriftDangling {
		t.Fatalf("edge = %+v, want missing contract dangling", edge)
	}
}

func writeDependencyRegistry(t *testing.T, dir, section, content string) {
	t.Helper()
	path := filepath.Join(dir, section, "consumes.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir registry: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}

func dependencyEdgeByContract(t *testing.T, edges []ContractDependencyEdge, contractID string) ContractDependencyEdge {
	t.Helper()
	for _, edge := range edges {
		if edge.Contract == contractID {
			return edge
		}
	}
	t.Fatalf("edge %s not found: %+v", contractID, edges)
	return ContractDependencyEdge{}
}
