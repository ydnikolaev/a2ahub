//go:build !race

package cli_test

// raceDetectorEnabled is false in a normal (non-race) test build, where the
// statusline warm-render perf budget (AC-601.2, <100ms) is a real, enforced
// gate. See raceflag_race_test.go for the -race counterpart.
const raceDetectorEnabled = false
