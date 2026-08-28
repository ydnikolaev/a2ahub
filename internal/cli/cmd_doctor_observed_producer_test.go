package cli

// cmd_doctor_observed_producer_test.go is spec 06's producer-direction half
// (answers-that-hold-2026-08, US-1/US-2): doctorObservedProducerRows is the
// THIRD call site of cache.FindObservedConsumers (AC-6), and this file is
// new rather than an addition to cmd_doctor_test.go's own "doctorUnadopted
// ConsumptionRows (defects-fix-2026-08 P6)" section — this phase's plan
// allowlists internal/cli/cmd_doctor.go but not cmd_doctor_test.go, which
// three sibling agents are editing concurrently this wave; a novel file
// name keeps this phase's own tests off that shared surface entirely.
//
// Fixtures reuse cmd_doctor_test.go's own docWriteFile/docWriteAcceptedDelivery
// helpers (same package, same on-disk shape internal/cache's registered_
// consumers_test.go fixtures already use) plus one addition this direction
// needs that the consumer direction does not: a space.yaml manifest
// (FindObservedConsumers reads manifest.Participants) and a
// <system>/provides/<slug>/contract.md descriptor naming the contract THIS
// project's own system provides.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// docWriteProducerSpace writes a minimal space.yaml manifest naming exactly
// the two participants every fixture in this file needs (axon, seomatrix),
// both active — the same participant pair registered_consumers_test.go's
// own cache-level fixtures already use for this exact contract id.
func docWriteProducerSpace(t *testing.T, mirror string) {
	t.Helper()
	docWriteFile(t, mirror, "space.yaml",
		"schema: space/v1\nspace: fixture-space\nmin_binary_version: 0.0.0\nparticipants:\n"+
			"  - system: axon\n    org: fixture\n    section: axon\n    owners: [axon-bot]\n    status: active\n    joined: \"2026-01-01\"\n"+
			"  - system: seomatrix\n    org: fixture\n    section: seomatrix\n    owners: [seo-bot]\n    status: active\n    joined: \"2026-01-01\"\n")
}

// docWriteContractDescriptor writes a minimal contract.md at the
// space.Layout.ProvidesContract path this phase's own doctorObservedProducerRows
// globs — the same path contractReadDescriptor (cmd_contract.go) already
// reads this shape from.
func docWriteContractDescriptor(t *testing.T, mirror, system, slug, id string) {
	t.Helper()
	docWriteFile(t, mirror, filepath.Join(system, "provides", slug, "contract.md"),
		"---\nschema: envelope/v1\nid: "+id+"\ntype: contract\ntitle: t\nspace: fixture-space\n"+
			"from: "+system+"\nto: []\nversion: 1.0.0\n---\nBody.\n")
}

func TestDoctorObservedProducerRows_NamesConsumerAndVersion(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	docWriteProducerSpace(t, mirror)
	docWriteContractDescriptor(t, mirror, "seomatrix", "regime-corpus", "XC-seomatrix-regime-corpus")
	docWriteFile(t, mirror, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")
	docWriteAcceptedDelivery(t, mirror, "aaaa", "p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111", true)

	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cfg := space.ProjectConfig{System: "seomatrix", Spaces: []space.Ref{{ID: "getvisa"}}}

	rows := cmd.doctorObservedProducerRows(cfg, space.MachineConfig{})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want exactly 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.SpaceID != "getvisa" {
		t.Fatalf("SpaceID = %q, want getvisa", row.SpaceID)
	}
	if row.Status != doctorVisibilityWARN {
		t.Fatalf("Status = %q, want WARN — observed consumption must never fail doctor by itself (AC-7)", row.Status)
	}
	for _, want := range []string{"axon", "XC-seomatrix-regime-corpus", "1.0.0", "1 package"} {
		if !strings.Contains(row.Detail, want) {
			t.Fatalf("Detail = %q, want it to contain %q", row.Detail, want)
		}
	}
}

// TestDoctorObservedProducerRows_SilentWhenDeclared is US-3's producer-side
// mirror: a system that DECLARES the dependency in its own consumes.yaml
// must never be named, even though its deliveries still pin the contract.
func TestDoctorObservedProducerRows_SilentWhenDeclared(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	docWriteProducerSpace(t, mirror)
	docWriteContractDescriptor(t, mirror, "seomatrix", "regime-corpus", "XC-seomatrix-regime-corpus")
	docWriteFile(t, mirror, "axon/consumes.yaml",
		"schema: consumes/v1\nsystem: axon\ndependencies:\n  - contract: XC-seomatrix-regime-corpus\n    major: 1\n    since: \"2026-01-01\"\n")
	docWriteAcceptedDelivery(t, mirror, "aaaa", "p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111", true)

	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cfg := space.ProjectConfig{System: "seomatrix", Spaces: []space.Ref{{ID: "getvisa"}}}

	rows := cmd.doctorObservedProducerRows(cfg, space.MachineConfig{})
	if len(rows) != 0 {
		t.Fatalf("got %+v, want no rows — axon DECLARED this contract, so it is never observed-and-undeclared", rows)
	}
}

// TestDoctorObservedProducerRows_DifferentVersionsReportedDistinctly is §8
// criterion 5 at this row's own layer: a consumer pinning two versions of
// one contract must produce TWO WARN rows, one per version, never one
// collapsed line.
func TestDoctorObservedProducerRows_DifferentVersionsReportedDistinctly(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	docWriteProducerSpace(t, mirror)
	docWriteContractDescriptor(t, mirror, "seomatrix", "regime-corpus", "XC-seomatrix-regime-corpus")
	docWriteFile(t, mirror, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")
	docWriteAcceptedDelivery(t, mirror, "aaaa", "p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111", true)
	docWriteAcceptedDelivery(t, mirror, "bbbb", "n2yp", "XC-seomatrix-regime-corpus@2.0.0#bbb222", true)

	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cfg := space.ProjectConfig{System: "seomatrix", Spaces: []space.Ref{{ID: "getvisa"}}}

	rows := cmd.doctorObservedProducerRows(cfg, space.MachineConfig{})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want exactly 2 (one per version), never collapsed: %+v", len(rows), rows)
	}
	joined := rows[0].Detail + " | " + rows[1].Detail
	if !strings.Contains(joined, "1.0.0") || !strings.Contains(joined, "2.0.0") {
		t.Fatalf("rows = %+v, want both versions named", rows)
	}
}

// TestDoctorObservedProducerRows_UnreadableParticipantRegistryExcludesRatherThanFails
// is §8 criterion 8 at the CALLER layer: cache.FindObservedConsumers fails
// closed on ANY unparseable consumes.yaml in the mirror (its own declared-
// consumer half), and this function's contract is to degrade that to
// "nothing observed for this contract" rather than panicking, erroring the
// whole doctor run, or reporting a guess.
func TestDoctorObservedProducerRows_UnreadableParticipantRegistryExcludesRatherThanFails(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	docWriteProducerSpace(t, mirror)
	docWriteContractDescriptor(t, mirror, "seomatrix", "regime-corpus", "XC-seomatrix-regime-corpus")
	docWriteFile(t, mirror, "axon/consumes.yaml", "consumes: []\n") // placeholder shape, refused by parseConsumesStrict
	docWriteAcceptedDelivery(t, mirror, "aaaa", "p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111", true)

	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cfg := space.ProjectConfig{System: "seomatrix", Spaces: []space.Ref{{ID: "getvisa"}}}

	rows := cmd.doctorObservedProducerRows(cfg, space.MachineConfig{})
	if len(rows) != 0 {
		t.Fatalf("got %+v, want no rows — an unreadable participant registry must be an UNDERCOUNT, never a false advisory", rows)
	}
}

func TestDoctorObservedProducerRows_NoOwnSystemConfiguredReturnsNil(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	rows := cmd.doctorObservedProducerRows(space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}, space.MachineConfig{})
	if rows != nil {
		t.Fatalf("got %+v, want nil — no configured system id means nothing can be resolved as \"mine\"", rows)
	}
}

func TestDoctorObservedProducerRows_NoOwnContractsProducesNoRows(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	docWriteProducerSpace(t, mirror)
	// No contract.md under seomatrix/provides/** at all.
	docWriteFile(t, mirror, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cfg := space.ProjectConfig{System: "seomatrix", Spaces: []space.Ref{{ID: "getvisa"}}}

	rows := cmd.doctorObservedProducerRows(cfg, space.MachineConfig{})
	if len(rows) != 0 {
		t.Fatalf("got %+v, want no rows — this system provides no contracts in this mirror", rows)
	}
}

// --- Phase 06's own AC-2 regression on the CONSUMER direction ---
//
// TestDoctorUnadoptedConsumptionRows_TwoVersionsCollapseToOneRow is AC-2:
// splitting cache.FindUnadoptedConsumption's own rows by (contract, version)
// for the producer direction above must NOT change this row's own output.
// Two deliveries pinning the SAME contract at DIFFERENT versions must still
// collapse into ONE row with the SUMMED count — byte-identical to what this
// function printed before the version field existed.
//
// TEETH: stop summing totals per ContractID in doctorUnadoptedConsumptionRows
// (print one row per (contract, version) bucket instead) and this reds with
// 2 rows instead of 1.
func TestDoctorUnadoptedConsumptionRows_TwoVersionsCollapseToOneRow(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	docWriteFile(t, mirror, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")
	docWriteAcceptedDelivery(t, mirror, "aaaa", "p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111", true)
	docWriteAcceptedDelivery(t, mirror, "bbbb", "n2yp", "XC-seomatrix-regime-corpus@2.0.0#bbb222", true)

	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cfg := space.ProjectConfig{System: "axon", Spaces: []space.Ref{{ID: "getvisa"}}}

	rows := cmd.doctorUnadoptedConsumptionRows(cfg, space.MachineConfig{})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want exactly 1 (versions collapse on the consumer direction, AC-2): %+v", len(rows), rows)
	}
	if !strings.Contains(rows[0].Detail, "2 verify-passed deliveries conform to XC-seomatrix-regime-corpus") {
		t.Fatalf("Detail = %q, want the summed count across both versions", rows[0].Detail)
	}
}
