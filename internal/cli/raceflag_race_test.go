//go:build race

package cli_test

// raceDetectorEnabled is true when the test binary is built with -race. Under
// the race detector, wall-clock timing is dominated by instrumentation
// overhead (and by CPU contention across the parallel `go test ./... -race`
// run), so absolute-latency perf assertions are unrepresentative and are
// measured-and-logged rather than hard-gated (see TestStatuslinePerf).
const raceDetectorEnabled = true
