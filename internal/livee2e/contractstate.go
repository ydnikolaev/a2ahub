package livee2e

import (
	"encoding/json"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// rwContractState returns the lifecycle state `a2a contracts --json` reports
// for ONE contract.
//
// Selecting by id is the whole point. The listing carries every contract in
// every connected space, so a substring search for a state — which is what the
// rolling-window row used to do — is satisfied by any unrelated contract that
// happens to be in that state. The operational-confidence family publishes one
// earlier in the same run and never retires it, so the assertion could not
// fail and proved nothing about the rolling window it is named for.
func rwContractState(listing, contractID string) (string, error) {
	var contracts []cache.ContractInfo
	if err := json.Unmarshal([]byte(listing), &contracts); err != nil {
		return "", fmt.Errorf("decode contracts listing: %w", err)
	}
	for _, item := range contracts {
		if item.ID == contractID {
			return item.State, nil
		}
	}
	return "", fmt.Errorf("contract %s is absent from a listing of %d contract(s)", contractID, len(contracts))
}
