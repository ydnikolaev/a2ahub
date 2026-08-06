package lane

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// TelemetryPath is scripts/verify.sh's own telemetry stream — the one J4
// reads (ground truth 6). scripts/lib/gate-lib.sh writes a DIFFERENT
// stream, .claude/.telemetry/gates.jsonl (duration_s, not duration_ms),
// and is out of scope here.
const TelemetryPath = ".a2a/cache/verify/telemetry.jsonl"

// MinSamples is J4's stated floor: below this many recorded runs, a median
// is "confident, specific and wrong" (the rg-preflight incident
// scripts/check-operational-confidence.sh:11-31 names) rather than a real
// estimate, so Estimate reports "no estimate" instead of a number.
const MinSamples = 3

// telemetryRecord mirrors scripts/verify.sh's append_telemetry line shape:
// {"gate":"...","verdict":"pass|fail","duration_ms":123,"mode":"...","at":"..."}.
type telemetryRecord struct {
	Gate       string `json:"gate"`
	Verdict    string `json:"verdict"`
	DurationMS int64  `json:"duration_ms"`
	Mode       string `json:"mode"`
	At         string `json:"at"`
}

// Estimate is one phase's duration estimate, derived from telemetry.
type Estimate struct {
	MedianMS int64
	Samples  int
}

// Ok reports whether Estimate carries a real median (Samples >= MinSamples).
func (e Estimate) Ok() bool {
	return e.Samples >= MinSamples
}

func (e Estimate) String() string {
	if !e.Ok() {
		return fmt.Sprintf("no estimate (n=%d)", e.Samples)
	}
	return fmt.Sprintf("%dms (n=%d)", e.MedianMS, e.Samples)
}

// ReadTelemetry reads root's telemetry.jsonl and returns every recorded
// duration_ms, grouped by gate name. A missing or unreadable file is not
// an error — it is "no estimate" for every phase, which EstimateFor
// reports the same way whether the cause is an absent file or too few
// samples. A present-but-malformed line is skipped rather than aborting
// the whole read: one bad line recorded by a stale verify.sh must not
// blind every other gate's estimate.
func ReadTelemetry(root string) map[string][]int64 {
	raw, err := readRepoFile(root, TelemetryPath)
	if err != nil {
		return map[string][]int64{}
	}

	byGate := map[string][]int64{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec telemetryRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Gate == "" {
			continue
		}
		byGate[rec.Gate] = append(byGate[rec.Gate], rec.DurationMS)
	}
	return byGate
}

// EstimateFor computes phase's Estimate from a duration_ms sample set
// (as returned by ReadTelemetry). The median of an even-sized sample is
// the mean of the two central values, rounded to the nearest millisecond
// (rounding half away from zero) — the usual statistical convention.
func EstimateFor(samples []int64) Estimate {
	n := len(samples)
	if n == 0 {
		return Estimate{Samples: 0}
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var median int64
	if n%2 == 1 {
		median = sorted[n/2]
	} else {
		lo, hi := sorted[n/2-1], sorted[n/2]
		sum := lo + hi
		half := sum / 2
		if sum%2 != 0 {
			// round half away from zero; durations are non-negative so
			// this is always "round up".
			half++
		}
		median = half
	}
	return Estimate{MedianMS: median, Samples: n}
}
