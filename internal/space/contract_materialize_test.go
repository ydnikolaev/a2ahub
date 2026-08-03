package space

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

func TestContractMaterializerWritesExactSnapshotAndRerunsWithoutWrites(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	materializer, err := NewContractMaterializer(project)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := materializer.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	snapshot := contractMaterializeSnapshot(t)
	result, err := materializer.Materialize(context.Background(), snapshot, "vendor/orders")
	if !contractMaterializeNoReplaceSupported {
		if !errors.Is(err, ErrContractAtomicNoReplaceUnsupported) || result.Outcome != "" {
			t.Fatalf("unsupported platform = %+v err=%v", result, err)
		}
		if entries, readErr := os.ReadDir(filepath.Join(project, "vendor")); readErr != nil || len(entries) != 0 {
			t.Fatalf("unsupported platform changed destination: entries=%v err=%v", entries, readErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if result.Outcome != ContractMaterialized || !result.Durable || result.ContractID != snapshot.ContractID ||
		result.Version != snapshot.Version || result.SourceCommit != snapshot.CommitSHA ||
		result.DigestProfile != snapshot.DigestProfile || result.AggregateDigest != snapshot.PublishedDigest ||
		result.AggregateVerification != snapshot.AggregateVerification ||
		result.DescriptorVerification != snapshot.DescriptorVerification || result.Destination != "vendor/orders" {
		t.Fatalf("result = %+v", result)
	}
	assertMaterializedSnapshot(t, filepath.Join(project, "vendor", "orders"), snapshot)

	var writes, renames atomic.Int32
	materializer.hooks.beforeWrite = func(string) error { writes.Add(1); return nil }
	materializer.hooks.beforeRename = func() error { renames.Add(1); return nil }
	again, err := materializer.Materialize(context.Background(), snapshot, "vendor/orders")
	if err != nil {
		t.Fatalf("idempotent Materialize: %v", err)
	}
	if again.Outcome != ContractAlreadyPresent || !again.Durable || writes.Load() != 0 || renames.Load() != 0 {
		t.Fatalf("rerun = %+v writes=%d renames=%d", again, writes.Load(), renames.Load())
	}
}

func TestContractMaterializerRefusesDivergenceWithoutChangingDestination(t *testing.T) {
	t.Parallel()

	for _, change := range []struct {
		name string
		fn   func(*testing.T, string)
	}{
		{name: "missing", fn: func(t *testing.T, destination string) {
			t.Helper()
			if err := os.Remove(filepath.Join(destination, "fixtures", "valid", "order.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "extra", fn: func(t *testing.T, destination string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(destination, "extra.txt"), []byte("extra"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "bytes", fn: func(t *testing.T, destination string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(destination, "schema", "order.json"), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", fn: func(t *testing.T, destination string) {
			t.Helper()
			name := filepath.Join(destination, "schema", "order.json")
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../contract.md", name); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			if err := os.Mkdir(filepath.Join(project, "vendor"), 0o755); err != nil {
				t.Fatal(err)
			}
			materializer, err := NewContractMaterializer(project)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = materializer.Close() }() // reason: the assertion error remains primary
			snapshot := contractMaterializeSnapshot(t)
			destination := filepath.Join(project, "vendor", "orders")
			if contractMaterializeNoReplaceSupported {
				if _, err := materializer.Materialize(context.Background(), snapshot, "vendor/orders"); err != nil {
					t.Fatal(err)
				}
			} else {
				writeSnapshotUnrooted(t, destination, snapshot)
			}
			change.fn(t, destination)
			before := filesystemProjection(t, destination)

			result, err := materializer.Materialize(context.Background(), snapshot, "vendor/orders")
			if !errors.Is(err, ErrContractDestinationDiverged) || result.Outcome != "" {
				t.Fatalf("Materialize = %+v err=%v, want destination divergence", result, err)
			}
			after := filesystemProjection(t, destination)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("divergent destination changed:\nbefore=%v\nafter=%v", before, after)
			}
		})
	}
}

func TestContractMaterializerRejectsUnsafeOrMissingDestinationWithZeroChanges(t *testing.T) {
	t.Parallel()

	for _, destination := range []string{"", ".", "/tmp/out", "../out", "vendor/../out", "vendor\\out", "vendor//out"} {
		destination := destination
		t.Run(strings.ReplaceAll(destination, "/", "_"), func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			if err := os.Mkdir(filepath.Join(project, "vendor"), 0o755); err != nil {
				t.Fatal(err)
			}
			materializer, err := NewContractMaterializer(project)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = materializer.Close() }() // reason: the assertion error remains primary
			before := filesystemProjection(t, project)
			if _, err := materializer.Materialize(context.Background(), contractMaterializeSnapshot(t), destination); !errors.Is(err, ErrContractUnsafePath) {
				t.Fatalf("Materialize(%q) error = %v, want unsafe path", destination, err)
			}
			if after := filesystemProjection(t, project); !reflect.DeepEqual(before, after) {
				t.Fatalf("unsafe destination changed project: before=%v after=%v", before, after)
			}
		})
	}

	project := t.TempDir()
	materializer, err := NewContractMaterializer(project)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = materializer.Close() }() // reason: the assertion error remains primary
	before := filesystemProjection(t, project)
	if _, err := materializer.Materialize(context.Background(), contractMaterializeSnapshot(t), "missing/nested/out"); !errors.Is(err, ErrContractDestinationParentMissing) {
		t.Fatalf("missing parent error = %v", err)
	}
	if after := filesystemProjection(t, project); !reflect.DeepEqual(before, after) {
		t.Fatalf("missing parent changed project: before=%v after=%v", before, after)
	}
}

func TestContractMaterializerFaultsCleanTemporaryTreeAndReportsDurability(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		fault func(*ContractMaterializer, error)
		want  error
	}{
		{name: "write", fault: func(materializer *ContractMaterializer, injected error) {
			materializer.hooks.beforeWrite = func(string) error { return injected }
		}, want: errContractMaterializeInjected},
		{name: "file fsync", fault: func(materializer *ContractMaterializer, injected error) {
			materializer.hooks.syncFile = func(string, *os.File) error { return injected }
		}, want: errContractMaterializeInjected},
		{name: "tree fsync", fault: func(materializer *ContractMaterializer, injected error) {
			materializer.hooks.syncDirectory = func(string, *os.File) error { return injected }
		}, want: errContractMaterializeInjected},
		{name: "rename", fault: func(materializer *ContractMaterializer, injected error) {
			materializer.hooks.beforeRename = func() error { return injected }
		}, want: errContractMaterializeInjected},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			if err := os.Mkdir(filepath.Join(project, "vendor"), 0o755); err != nil {
				t.Fatal(err)
			}
			materializer, err := NewContractMaterializer(project)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = materializer.Close() }() // reason: the assertion error remains primary
			injected := errContractMaterializeInjected
			test.fault(materializer, injected)
			if _, err := materializer.Materialize(context.Background(), contractMaterializeSnapshot(t), "vendor/orders"); !errors.Is(err, test.want) {
				t.Fatalf("fault error = %v, want %v", err, test.want)
			}
			entries, err := os.ReadDir(filepath.Join(project, "vendor"))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("fault left destination or temporary siblings: %v", entries)
			}
		})
	}

	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	materializer, err := NewContractMaterializer(project)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = materializer.Close() }() // reason: the assertion error remains primary
	materializer.hooks.syncParent = func(*os.File) error { return errContractMaterializeInjected }
	result, err := materializer.Materialize(context.Background(), contractMaterializeSnapshot(t), "vendor/orders")
	if !contractMaterializeNoReplaceSupported {
		if !errors.Is(err, ErrContractAtomicNoReplaceUnsupported) || result.Outcome != "" {
			t.Fatalf("unsupported platform = %+v err=%v", result, err)
		}
		return
	}
	if !errors.Is(err, ErrContractDestinationDurabilityUnknown) || result.Outcome != ContractMaterialized || result.Durable {
		t.Fatalf("parent fsync = %+v err=%v", result, err)
	}
	assertMaterializedSnapshot(t, filepath.Join(project, "vendor", "orders"), contractMaterializeSnapshot(t))
}

func TestContractMaterializerRechecksConcurrentWinnerAndSymlinkRaces(t *testing.T) {
	t.Parallel()

	for _, identical := range []bool{true, false} {
		identical := identical
		t.Run(map[bool]string{true: "identical", false: "divergent"}[identical], func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			parentPath := filepath.Join(project, "vendor")
			if err := os.Mkdir(parentPath, 0o755); err != nil {
				t.Fatal(err)
			}
			materializer, err := NewContractMaterializer(project)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = materializer.Close() }() // reason: the assertion error remains primary
			snapshot := contractMaterializeSnapshot(t)
			materializer.hooks.beforeRename = func() error {
				winner := filepath.Join(parentPath, "orders")
				if identical {
					writeSnapshotUnrooted(t, winner, snapshot)
				} else if err := os.Mkdir(winner, 0o700); err != nil {
					t.Fatal(err)
				} else if err := os.WriteFile(filepath.Join(winner, "wrong"), []byte("wrong"), 0o600); err != nil {
					t.Fatal(err)
				}
				return nil
			}
			result, err := materializer.Materialize(context.Background(), snapshot, "vendor/orders")
			if identical {
				if err != nil || result.Outcome != ContractAlreadyPresent {
					t.Fatalf("identical winner = %+v err=%v", result, err)
				}
				assertMaterializedSnapshot(t, filepath.Join(parentPath, "orders"), snapshot)
			} else if !errors.Is(err, ErrContractDestinationDiverged) {
				t.Fatalf("divergent winner = %+v err=%v", result, err)
			}
			entries, err := os.ReadDir(parentPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "orders" {
				t.Fatalf("concurrent winner left temporary siblings: %v", entries)
			}
		})
	}

	project := t.TempDir()
	outside := t.TempDir()
	parentPath := filepath.Join(project, "vendor")
	if err := os.Mkdir(parentPath, 0o755); err != nil {
		t.Fatal(err)
	}
	materializer, err := NewContractMaterializer(project)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = materializer.Close() }() // reason: the assertion error remains primary
	materializer.hooks.afterParentComponentLstat = func(component string) {
		if component != "vendor" {
			return
		}
		moved := filepath.Join(project, "vendor-real")
		if err := os.Rename(parentPath, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, parentPath); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := materializer.Materialize(context.Background(), contractMaterializeSnapshot(t), "vendor/orders"); !errors.Is(err, ErrContractUnsafePath) {
		t.Fatalf("parent swap error = %v", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("parent symlink race escaped root: entries=%v err=%v", entries, err)
	}
}

func TestContractMaterializerNoReplaceClosesPostCheckRace(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		winner func(*testing.T, string, string)
	}{
		{name: "empty directory", winner: func(t *testing.T, destination, _ string) {
			t.Helper()
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", winner: func(t *testing.T, destination, outside string) {
			t.Helper()
			if err := os.Symlink(outside, destination); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			outside := t.TempDir()
			parent := filepath.Join(project, "vendor")
			destination := filepath.Join(parent, "orders")
			if err := os.Mkdir(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			materializer, err := NewContractMaterializer(project)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = materializer.Close() }() // reason: the assertion error remains primary
			materializer.hooks.afterDestinationRecheck = func() {
				test.winner(t, destination, outside)
			}

			if _, err := materializer.Materialize(context.Background(), contractMaterializeSnapshot(t), "vendor/orders"); !errors.Is(err, ErrContractDestinationDiverged) {
				t.Fatalf("post-check winner error = %v, want divergence", err)
			}
			info, err := os.Lstat(destination)
			if err != nil {
				t.Fatal(err)
			}
			switch test.name {
			case "empty directory":
				entries, err := os.ReadDir(destination)
				if err != nil || !info.IsDir() || len(entries) != 0 {
					t.Fatalf("empty winner was overwritten: info=%v entries=%v err=%v", info.Mode(), entries, err)
				}
			case "symlink":
				target, err := os.Readlink(destination)
				if err != nil || info.Mode()&os.ModeSymlink == 0 || target != outside {
					t.Fatalf("symlink winner was overwritten: mode=%v target=%q err=%v", info.Mode(), target, err)
				}
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
				t.Fatalf("post-check race wrote outside: entries=%v err=%v", entries, err)
			}
			parentEntries, err := os.ReadDir(parent)
			if err != nil || len(parentEntries) != 1 || parentEntries[0].Name() != "orders" {
				t.Fatalf("post-check race left temporary siblings: entries=%v err=%v", parentEntries, err)
			}
		})
	}
}

func TestContractMaterializerLeafSwapNeverFollowsOutsideRoot(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(project, "vendor")
	destination := filepath.Join(parent, "orders")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot := contractMaterializeSnapshot(t)
	writeSnapshotUnrooted(t, destination, snapshot)
	materializer, err := NewContractMaterializer(project)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = materializer.Close() }() // reason: the assertion error remains primary
	var swapped atomic.Bool
	materializer.hooks.afterCompareLeafLstat = func(relative string) {
		if relative != "schema/order.json" || !swapped.CompareAndSwap(false, true) {
			return
		}
		leaf := filepath.Join(destination, filepath.FromSlash(relative))
		if err := os.Remove(leaf); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, leaf); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := materializer.Materialize(context.Background(), snapshot, "vendor/orders"); !errors.Is(err, ErrContractUnsafePath) {
		t.Fatalf("leaf swap error = %v, want unsafe-path refusal", err)
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "outside\n" {
		t.Fatalf("leaf swap changed outside file: raw=%q err=%v", raw, err)
	}
}

var errContractMaterializeInjected = errors.New("injected materialize fault")

func contractMaterializeSnapshot(t *testing.T) HistoricalSnapshot {
	t.Helper()
	descriptorRaw := []byte("---\nschema_format: json-schema-2020-12\nartifacts:\n" +
		"  - {path: schema/order.json, role: schema, normative: true, media_type: application/schema+json}\n" +
		"  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.json}\n" +
		"  - {path: fixtures/invalid/order.json, role: invalid-fixture, normative: false, media_type: application/json, conforms_to: schema/order.json}\n---\nOrders contract.\n")
	descriptor, err := contract.ParseDescriptor(descriptorRaw)
	if err != nil {
		t.Fatal(err)
	}
	files := []ContractSnapshotFile{
		{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Mode: "100644", Raw: descriptorRaw},
		{Path: "fixtures/invalid/order.json", Kind: contract.CandidateRegular, Mode: "100644", Raw: []byte(`{}`)},
		{Path: "fixtures/valid/order.json", Kind: contract.CandidateRegular, Mode: "100644", Raw: []byte(`{"id":"1"}`)},
		{Path: "schema/order.json", Kind: contract.CandidateRegular, Mode: "100644", Raw: []byte(`{"type":"object","required":["id"]}`)},
	}
	core := contract.CandidateSnapshot{Descriptor: contract.CandidateFile{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Raw: descriptorRaw}}
	for _, file := range files[1:] {
		core.Files = append(core.Files, contract.CandidateFile{Path: file.Path, Kind: file.Kind, Raw: file.Raw})
	}
	set, issues := contract.ValidateCarriedSet(contract.ProfileContractSetV2, descriptor, core)
	if len(issues) != 0 {
		t.Fatalf("fixture carried set: %+v", issues)
	}
	return HistoricalSnapshot{
		ContractID: "XC-axon-orders", Version: "1.2.3", CommitSHA: strings.Repeat("a", 40),
		DigestProfile: contract.ProfileContractSetV2, PublishedDigest: set.AggregateDigest,
		AggregateVerification: ContractVerificationEventDigest, DescriptorVerification: ContractVerificationEventDigest,
		Descriptor: descriptor, CarriedSet: set, Files: files,
	}
}

func assertMaterializedSnapshot(t *testing.T, destination string, snapshot HistoricalSnapshot) {
	t.Helper()
	want := make(map[string][]byte, len(snapshot.Files))
	for _, file := range snapshot.Files {
		want[file.Path] = file.Raw
	}
	got := make(map[string][]byte)
	err := filepath.WalkDir(destination, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(destination, name)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		got[filepath.ToSlash(relative)] = raw
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("materialized bytes = %v, want %v", got, want)
	}
}

func writeSnapshotUnrooted(t *testing.T, destination string, snapshot HistoricalSnapshot) {
	t.Helper()
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range snapshot.Files {
		name := filepath.Join(destination, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, file.Raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func filesystemProjection(t *testing.T, root string) []string {
	t.Helper()
	var projection []string
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := filepath.ToSlash(relative) + ":" + info.Mode().String()
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			value += ":" + string(bytes.ReplaceAll(raw, []byte("\n"), []byte("\\n")))
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(name)
			if err != nil {
				return err
			}
			value += ":" + target
		}
		projection = append(projection, value)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(projection)
	return projection
}
