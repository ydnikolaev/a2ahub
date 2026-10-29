package space

import "github.com/ydnikolaev/a2ahub/internal/version"

// testBuild is a release-channel build with no commit, so the write funnel
// records no build note and exact-message assertions keep their bytes.
func testBuild(v string) version.Build { return version.NewBuild(v, "", "") }
