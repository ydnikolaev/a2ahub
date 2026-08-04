package livee2e

import "testing"

func TestRWContractStateSelectsTheRowItWasAskedFor(t *testing.T) {
	t.Parallel()
	// Two contracts, and the OTHER one is published — the exact shape that made
	// a substring search pass while saying nothing about the subject row.
	listing := `[
  {"space":"live","id":"XC-alpha-oc-p7-contract","provider":"alpha","version":"2.0.0","state":"published"},
  {"space":"live","id":"XC-alpha-rw-rolling-window","provider":"alpha","version":"1.1.0","state":"deprecated"}
]`
	state, err := rwContractState(listing, "XC-alpha-rw-rolling-window")
	if err != nil {
		t.Fatalf("rwContractState() error = %v", err)
	}
	if state != "deprecated" {
		t.Fatalf("state = %q, want the subject row's own state, not a sibling's", state)
	}
}

func TestRWContractStateRefusesAnAbsentOrUnreadableListing(t *testing.T) {
	t.Parallel()
	if _, err := rwContractState(`[{"id":"XC-alpha-other","state":"published"}]`, "XC-alpha-rw-rolling-window"); err == nil {
		t.Fatal("an absent contract was reported as a state")
	}
	if _, err := rwContractState("not json", "XC-alpha-rw-rolling-window"); err == nil {
		t.Fatal("an unreadable listing was reported as a state")
	}
	if _, err := rwContractState("[]", "XC-alpha-rw-rolling-window"); err == nil {
		t.Fatal("an empty listing was reported as a state")
	}
}
