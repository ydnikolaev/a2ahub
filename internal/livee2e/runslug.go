package livee2e

import "fmt"

// liveRunSlug scopes a standing artifact to the current destructive live-space
// generation. GitHub retains merged pull requests after ResetSpace rewrites
// main, and semantic operations intentionally use deterministic branches.
// Reusing the same standing ID would therefore make a new run look like a
// retry of a merged operation from the previous, now-erased generation.
//
// PRFloor is captured after reset and increases whenever a prior run opened a
// PR. If a run opened none, there is no semantic operation for the repeated
// floor to collide with.
func liveRunSlug(base string, prFloor int) string {
	return fmt.Sprintf("%s-run-%d", base, prFloor)
}
