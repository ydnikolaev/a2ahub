package livee2e

// NewLocalHostSeam builds the seam the LOCAL tier uses (plan D-1/D-5): apiRoot
// is testkit/fakegithub's own Server.URL, and BOTH the git remote and the
// human-facing web root are the local bare origin's own filesystem path —
// there is no browser-facing host to distinguish the two from on this tier,
// unlike NewLiveHostSeam's org+repo pair (hostseam.go), which derives three
// DIFFERENT strings from one GitHub identity.
//
// UNTAGGED for the same reason hostseam.go itself is: the RULE — how a local
// seam's three strings derive from an API root and an origin path — is pure
// and has no network dependency, so it belongs where the plain `go test
// ./internal/livee2e/` suite (the one `make check` runs) covers it, not
// behind the livee2e build tag where only a logic or live run would ever
// exercise it.
func NewLocalHostSeam(apiRoot, originDir string) HostSeam {
	return HostSeam{
		APIRoot:   apiRoot,
		RemoteURL: originDir,
		WebRoot:   originDir,
	}
}
