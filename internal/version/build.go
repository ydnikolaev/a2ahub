package version

import "fmt"

// Channel values a Build reports.
const (
	ChannelRelease = "release"
	ChannelLocal   = "local"
	ChannelDev     = "dev"
)

// devVersion is the version a binary carries when no -X main.version was
// injected (a plain `go build`).
const devVersion = "dev"

// Build is the identity of the running binary, carried once so every surface
// that reports or depends on it agrees (D-040). Stdlib-only, like the rest of
// this package.
type Build struct {
	Version string // bare dotted version, or "dev"
	Commit  string // full sha; "" when unknown
	Channel string // ChannelRelease | ChannelLocal | ChannelDev
}

// IsDevBuild reports whether v is the un-stamped development version.
func IsDevBuild(v string) bool { return v == devVersion }

// NewBuild composes a Build. A "dev" version is always the dev channel; the
// channel var "local" (set only by `make deliver`) selects the local channel;
// anything else is a release.
func NewBuild(ver, commit, channelVar string) Build {
	ch := ChannelRelease
	switch {
	case IsDevBuild(ver):
		ch = ChannelDev
	case channelVar == ChannelLocal:
		ch = ChannelLocal
	}
	return Build{Version: ver, Commit: commit, Channel: ch}
}

// IsLocal reports whether this is a locally delivered build.
func (b Build) IsLocal() bool { return b.Channel == ChannelLocal }

// Stamp is the ONE composer of the stamp line. A release renders byte-for-byte
// as it always has: "a2a <v> (<sha>)".
func (b Build) Stamp() string {
	sha := b.Commit
	if sha == "" {
		sha = "unknown"
	}
	if b.IsLocal() {
		return fmt.Sprintf("a2a %s (local, %s)", b.Version, sha)
	}
	return fmt.Sprintf("a2a %s (%s)", b.Version, sha)
}
