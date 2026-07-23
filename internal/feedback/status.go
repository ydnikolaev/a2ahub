package feedback

import (
	"fmt"
	"io"
	"net/http"

	"gopkg.in/yaml.v3"
)

// StatusRow is one `a2a feedback status` line: the ledger's own facts
// plus the hub-side status/resolution resolved via the injected
// HubReader (§T1).
type StatusRow struct {
	ID         string
	Kind       string
	Title      string
	PRURL      string
	HubStatus  string // "unknown" on a hub-reader error (§T1: network errors degrade, exit 0)
	Resolution string
}

// HubReader resolves id's current committed hub-side content
// (feedback/inbox/<id>.yaml as it stands on the hub repo's default
// branch). Production reads it via a raw HTTP GET — no clone (§T1);
// tests inject a func reading a local fixture. A non-nil error degrades
// the corresponding StatusRow to HubStatus:"unknown", never a hard
// failure.
type HubReader func(id string) ([]byte, error)

type hubStatusProbe struct {
	Status     string `yaml:"status"`
	Resolution string `yaml:"resolution"`
}

// Status builds every ledger row's StatusRow. An empty ledger returns an
// empty, nil-error slice (the CLI layer prints the "no feedback filed"
// friendly message for that case, §T1).
func Status(ledgerPath string, reader HubReader) ([]StatusRow, error) {
	items, err := ReadLedger(ledgerPath)
	if err != nil {
		return nil, err
	}
	rows := make([]StatusRow, 0, len(items))
	for _, it := range items {
		row := StatusRow{ID: it.ID, Kind: it.Kind, Title: it.Title, PRURL: it.PRURL, HubStatus: "unknown"}
		raw, rerr := reader(it.ID)
		if rerr == nil {
			var probe hubStatusProbe
			if uerr := yaml.Unmarshal(raw, &probe); uerr == nil && probe.Status != "" {
				row.HubStatus = probe.Status
				row.Resolution = probe.Resolution
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// DefaultHubReader builds the production HubReader: a raw HTTPS GET of
// <rawBaseURL>/feedback/inbox/<id>.yaml (no clone, §T1). client lets
// callers/tests swap the transport (or point it at an httptest server)
// without a live network call.
func DefaultHubReader(client *http.Client, rawBaseURL string) HubReader {
	return func(id string) ([]byte, error) {
		url := rawBaseURL + "/feedback/inbox/" + id + ".yaml"
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("feedback: DefaultHubReader: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("feedback: DefaultHubReader: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("feedback: DefaultHubReader: %s: unexpected status %d", url, resp.StatusCode)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedbackBytes*2))
		if err != nil {
			return nil, fmt.Errorf("feedback: DefaultHubReader: %w", err)
		}
		return raw, nil
	}
}
