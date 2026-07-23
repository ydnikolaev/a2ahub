package feedback

import "testing"

func TestLoadCodes(t *testing.T) {
	t.Parallel()
	table, err := LoadCodes()
	if err != nil {
		t.Fatalf("LoadCodes: %v", err)
	}
	want := []string{
		CodeSchemaStructural, CodeMissingBugEvidence, CodeChecksGateFalse,
		CodeOversize, CodeStatusNotNew, CodeSecretDetected,
		CodeFilenameMismatch, CodePathNotUnderInbox,
	}
	for _, code := range want {
		if !table.Has(code) {
			t.Errorf("codes.yaml does not have entry for %s", code)
		}
	}
	if len(table.Codes()) != len(want) {
		t.Errorf("Codes() = %v, want %d entries", table.Codes(), len(want))
	}
}
