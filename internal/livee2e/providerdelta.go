package livee2e

// addedSince returns the members of after that were not present in before,
// preserving after's order and reporting a repeated value once per extra
// occurrence.
//
// A write-free assertion has to ask "did anything APPEAR?", never "is the set
// identical?". The live space runs with `delete_branch_on_merge: true`, so
// GitHub prunes a merged pull request's head branch asynchronously and a
// branch can vanish between two samples with no local action at all. Set
// equality reads that disappearance as a write and fails a scenario that did
// nothing wrong; only an addition is evidence that the command under test
// touched provider state.
func addedSince[T comparable](before, after []T) []T {
	remaining := make(map[T]int, len(before))
	for _, item := range before {
		remaining[item]++
	}
	added := make([]T, 0)
	for _, item := range after {
		if remaining[item] > 0 {
			remaining[item]--
			continue
		}
		added = append(added, item)
	}
	return added
}
