package release

import "github.com/ydnikolaev/a2ahub/internal/version"

// Decision is Resolve's verdict (T1 step 1): what the update verb should do
// and what `--json`/doctor/the funnel hint report.
type Decision struct {
	// Current is the running binary's bare version.
	Current string
	// Latest is the newest usable release's bare version.
	Latest string
	// Floor is the max min_binary_version across connected spaces'
	// manifests, computed by the CLI (release never reads manifests) —
	// "" when running outside any connected space (no floor constraint).
	Floor string
	// FloorSpace names the space that set Floor, for the BelowFloor /
	// Required error messages. Meaningless when Floor == "".
	FloorSpace string
	// Commit is the latest release's target commitish (Release.Commit),
	// carried through for the "updated vX -> vY (<commit>)" report line.
	Commit string

	// UpToDate is true when Latest <= Current: nothing to do.
	UpToDate bool
	// Target is the version an update would move to: Latest when newer
	// than Current, else Current (a no-op target).
	Target string
	// BelowFloor is true when Latest < Floor: the fleet's floor demands a
	// version that is not published (operator misconfiguration, T1
	// step 1) — an error condition the verb surfaces, naming FloorSpace.
	BelowFloor bool
	// Required is true when Current < Floor: the running binary is
	// already stale enough that CC-085 refuses writes against
	// FloorSpace (T4 "required" grade).
	Required bool
}

// Resolve computes the update Decision (T1 step 1) from the current binary
// version, the latest known release, and the CLI-computed floor (empty
// Floor => no floor constraint, e.g. running outside a connected project).
// latestRel is threaded through only for its Commit/report-line value —
// Resolve does not otherwise inspect it. Fail-closed via
// internal/version.OlderThan: an unparseable Current or Latest returns a
// non-nil error; Floor is only compared when non-empty.
func Resolve(current, latest string, latestRel Release, floor, floorSpace string) (Decision, error) {
	d := Decision{
		Current:    current,
		Latest:     latest,
		Floor:      floor,
		FloorSpace: floorSpace,
		Commit:     latestRel.Commit,
	}

	newer, err := version.OlderThan(current, latest)
	if err != nil {
		return Decision{}, err
	}
	d.UpToDate = !newer
	if newer {
		d.Target = latest
	} else {
		d.Target = current
	}

	if floor != "" {
		belowFloor, err := version.OlderThan(latest, floor)
		if err != nil {
			return Decision{}, err
		}
		d.BelowFloor = belowFloor

		required, err := version.OlderThan(current, floor)
		if err != nil {
			return Decision{}, err
		}
		d.Required = required
	}

	return d, nil
}
