package livee2e

import (
	"fmt"
	"sort"
	"strings"
)

// EnvCells narrows a diagnostic run to exact declared matrix cells. Values
// are comma-separated `<scenario>[@<branch>]/<system>/<surface>` triples, for
// example:
//
//	contract-publish-deprecate-retire/A/cli
//	LE-OC-08@unsupported-provider/A/cli
//
// Like EnvFamilies, this can never produce a release verdict: every
// unselected declared cell remains not-run.
const EnvCells = "A2A_LIVE_E2E_CELLS"

func selectedCells(raw string, catalogue []Scenario) (map[cellKey]bool, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	known := make(map[cellKey]string)
	var legal []string
	for _, scenario := range catalogue {
		for _, system := range scenario.Systems {
			for _, surface := range scenario.Surfaces {
				key := cellKey{scenario: scenario.Name, branch: scenario.Branch, system: system, surface: surface}
				known[key] = scenario.Family
				legal = append(legal, renderCellKey(key))
			}
		}
	}
	sort.Strings(legal)

	out := map[cellKey]bool{}
	var unknown []string
	for _, part := range strings.Split(raw, ",") {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		pieces := strings.Split(token, "/")
		if len(pieces) != 3 {
			unknown = append(unknown, token)
			continue
		}
		scenario, branch, _ := strings.Cut(pieces[0], "@")
		key := cellKey{scenario: scenario, branch: branch, system: pieces[1], surface: Surface(pieces[2])}
		if _, ok := known[key]; !ok {
			unknown = append(unknown, token)
			continue
		}
		out[key] = true
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown matrix cell(s) %s; use exact scenario[@branch]/system/surface triples; declared cells are: %s",
			strings.Join(unknown, ", "), strings.Join(legal, ", "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("names no matrix cell (value was %q)", raw)
	}
	return out, nil
}

func renderCellKey(key cellKey) string {
	scenario := key.scenario
	if key.branch != "" {
		scenario += "@" + key.branch
	}
	return scenario + "/" + key.system + "/" + string(key.surface)
}
