package cli

import (
	"reflect"
	"testing"
)

// The doctor scan and the mirror walk read the same rule: a companion file of a
// contract is not an unverified classification gap, and a malformed artifact or
// a malformed contract descriptor still is.
func TestDoctorFilterDropsContractCompanionsOnly(t *testing.T) {
	t.Parallel()
	const descriptor = "axon/provides/export/contract.md"
	const malformed = "axon/exchanges/XQ-axon-bad.md"
	paths, count := doctorFilterDataPackageReadmeSkips([]string{
		"axon/provides/export/artifacts/CHANGELOG.md",
		"axon/provides/export/schema/notes.md",
		"axon/provides/export/fixtures/valid/case.md",
		descriptor,
		malformed,
	}, 5)
	if want := []string{descriptor, malformed}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}
