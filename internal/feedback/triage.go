package feedback

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The three feedback/** paths triage reads/writes, relative to the hub
// repo root triage runs from (§T1: "run from the hub repo root").
const (
	feedbackInboxDir    = "feedback/inbox"
	feedbackBacklogPath = "feedback/backlog.yaml"
	feedbackDigestPath  = "feedback/digest.md"
)

type inboxProbe struct {
	ID       string `yaml:"id"`
	Kind     string `yaml:"kind"`
	Severity string `yaml:"severity"`
	Title    string `yaml:"title"`
	Status   string `yaml:"status"`
}

// TriageItem is one feedback/inbox/*.yaml entry.
type TriageItem struct {
	Path     string
	ID       string
	Kind     string
	Severity string
	Title    string
	Status   string
}

// DedupeCandidate names another report/backlog row a fresh item looks
// like a duplicate of — a candidate for the operator-agent's judgment,
// never auto-merged (§T1: "the judgment... is the triage procedure").
type DedupeCandidate struct {
	ID     string
	Title  string
	Source string // "inbox" | "backlog"
}

// TriageEntry pairs one status:new inbox item with its dedupe candidates.
type TriageEntry struct {
	Item       TriageItem
	Candidates []DedupeCandidate
}

// TriageReport is `a2a feedback triage`'s read-only listing result.
type TriageReport struct {
	Entries []TriageEntry
}

// Clean reports whether triage found nothing new to review (§T1:
// "nothing new -> inbox clean").
func (r TriageReport) Clean() bool { return len(r.Entries) == 0 }

// Triage lists every status:new feedback/inbox/*.yaml item under hubRoot,
// with dedupe candidates drawn from the rest of the inbox (any status)
// and from feedback/backlog.yaml. It is read-only — verdict judgment and
// write-back is ApplyVerdicts (§T1: "mechanical part is the verb; the
// judgment... is the triage procedure in AGENTS.md").
func Triage(hubRoot string) (TriageReport, error) {
	all, err := readInboxItems(hubRoot)
	if err != nil {
		return TriageReport{}, err
	}
	backlog, err := readBacklogDoc(hubRoot)
	if err != nil {
		return TriageReport{}, err
	}

	var entries []TriageEntry
	for _, item := range all {
		if item.Status != "new" {
			continue
		}
		var candidates []DedupeCandidate
		for _, other := range all {
			if other.ID == item.ID {
				continue
			}
			if other.Kind == item.Kind && titleSimilar(other.Title, item.Title) {
				candidates = append(candidates, DedupeCandidate{ID: other.ID, Title: other.Title, Source: "inbox"})
			}
		}
		for _, b := range backlog.Items {
			if b.Kind == item.Kind && titleSimilar(b.Title, item.Title) {
				candidates = append(candidates, DedupeCandidate{ID: b.ID, Title: b.Title, Source: "backlog"})
			}
		}
		entries = append(entries, TriageEntry{Item: item, Candidates: candidates})
	}
	return TriageReport{Entries: entries}, nil
}

func readInboxItems(hubRoot string) ([]TriageItem, error) {
	const op = "readInboxItems"
	dir := filepath.Join(hubRoot, filepath.FromSlash(feedbackInboxDir))
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("feedback: %s: %w", op, err)
	}
	sort.Strings(matches)
	items := make([]TriageItem, 0, len(matches))
	for _, m := range matches {
		raw, rerr := os.ReadFile(m)
		if rerr != nil {
			return nil, fmt.Errorf("feedback: %s: %w", op, rerr)
		}
		var probe inboxProbe
		if uerr := yaml.Unmarshal(raw, &probe); uerr != nil {
			return nil, fmt.Errorf("feedback: %s: %s: %w", op, m, uerr)
		}
		items = append(items, TriageItem{
			Path: m, ID: probe.ID, Kind: probe.Kind, Severity: probe.Severity,
			Title: probe.Title, Status: probe.Status,
		})
	}
	return items, nil
}

// titleSimilar is triage's dedupe heuristic: same-kind items whose titles
// share at least half their significant (>=4-char) words are flagged as
// candidates — a list only, never an auto-merge.
func titleSimilar(a, b string) bool {
	wa := significantWords(a)
	wb := significantWords(b)
	if len(wa) == 0 || len(wb) == 0 {
		return false
	}
	shared := 0
	for w := range wa {
		if wb[w] {
			shared++
		}
	}
	smaller := len(wa)
	if len(wb) < smaller {
		smaller = len(wb)
	}
	return smaller > 0 && float64(shared)/float64(smaller) >= 0.5
}

func significantWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, `.,;:!?"'`)
		if len(w) >= 4 {
			out[w] = true
		}
	}
	return out
}

// BacklogItem is one feedback/backlog.yaml row (backlog.schema.json).
type BacklogItem struct {
	ID       string   `yaml:"id"`
	Kind     string   `yaml:"kind"`
	Severity string   `yaml:"severity"`
	Title    string   `yaml:"title"`
	Verdict  string   `yaml:"verdict"`
	Route    string   `yaml:"route"`
	Refs     []string `yaml:"refs"`
	Date     string   `yaml:"date"`
}

// BacklogDoc is feedback/backlog.yaml's whole shape.
type BacklogDoc struct {
	Backlog  string        `yaml:"backlog"`
	WipLimit int           `yaml:"wip_limit"`
	Items    []BacklogItem `yaml:"items"`
}

const defaultWipLimit = 16

func readBacklogDoc(hubRoot string) (BacklogDoc, error) {
	const op = "readBacklogDoc"
	path := filepath.Join(hubRoot, filepath.FromSlash(feedbackBacklogPath))
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return BacklogDoc{Backlog: "v1", WipLimit: defaultWipLimit}, nil
		}
		return BacklogDoc{}, fmt.Errorf("feedback: %s: %w", op, err)
	}
	var doc BacklogDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return BacklogDoc{}, fmt.Errorf("feedback: %s: %s: %w", op, path, err)
	}
	return doc, nil
}

func writeBacklogDoc(hubRoot string, doc BacklogDoc) error {
	const op = "writeBacklogDoc"
	path := filepath.Join(hubRoot, filepath.FromSlash(feedbackBacklogPath))
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	return nil
}

// Verdict is the operator-agent's judgment for one inbox item (§T1:
// "after operator-agent judgment writes verdicts"). Status is the new
// hub-side status (accepted/rejected/duplicate/needs-info/shipped);
// Route/Refs only matter when Status == "accepted" (routed into
// feedback/backlog.yaml).
type Verdict struct {
	ID         string   `yaml:"id"`
	Status     string   `yaml:"status"`
	Resolution string   `yaml:"resolution"`
	Route      string   `yaml:"route"`
	Refs       []string `yaml:"refs"`
}

type verdictsFile struct {
	Verdicts []Verdict `yaml:"verdicts"`
}

// ParseVerdicts decodes a verdicts YAML file (`a2a feedback triage
// --apply <file>`'s input — the operator-agent's own written judgment)
// into the Verdict slice ApplyVerdicts consumes.
func ParseVerdicts(raw []byte) ([]Verdict, error) {
	var doc verdictsFile
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("feedback: ParseVerdicts: %w", err)
	}
	return doc.Verdicts, nil
}

// ApplyResult reports what ApplyVerdicts actually did, per id.
type ApplyResult struct {
	Applied     []string
	Skipped     []string // already-triaged (idempotent no-op) or unknown id
	WipLimitHit []string // accepted verdict refused: feedback/backlog.yaml is at wip_limit
}

// validVerdictStatuses is the closed set a triage verdict may assign — the
// hub-side outcomes of feedback.schema.json's `status` enum minus "new" (a
// verdict never un-triages an item). Guarding against this set BEFORE any
// write keeps a typo'd verdict (e.g. "accepetd") from silently corrupting a
// hub-committed feedback artifact and being reported as "applied" (go-auditor
// P25 IN MED).
var validVerdictStatuses = map[string]bool{
	"accepted": true, "rejected": true, "duplicate": true, "needs-info": true, "shipped": true,
}

// ApplyVerdicts mutates each named inbox item's status/resolution
// in-file, routes ACCEPTED verdicts into feedback/backlog.yaml
// (schema-shaped append, respecting BacklogDoc.WipLimit — the brake
// against graveyarding), and appends one dated feedback/digest.md entry
// summarizing every verdict actually applied. It is idempotent: an item
// whose status is no longer "new" (already triaged by a prior run) is
// skipped, never re-mutated or re-digested — a re-run over an
// already-triaged inbox with no new verdicts to apply changes nothing.
func ApplyVerdicts(hubRoot string, verdicts []Verdict, now time.Time) (ApplyResult, error) {
	const op = "ApplyVerdicts"

	// Fail-closed BEFORE mutating any inbox file: reject the whole apply if any
	// verdict carries a status outside the closed enum, so a garbage verdict
	// never partially rewrites the inbox/backlog/digest.
	for _, v := range verdicts {
		if !validVerdictStatuses[v.Status] {
			return ApplyResult{}, fmt.Errorf("feedback: %s: verdict for %q has invalid status %q (want one of accepted/rejected/duplicate/needs-info/shipped)", op, v.ID, v.Status)
		}
	}

	items, err := readInboxItems(hubRoot)
	if err != nil {
		return ApplyResult{}, err
	}
	byID := make(map[string]TriageItem, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}

	backlog, err := readBacklogDoc(hubRoot)
	if err != nil {
		return ApplyResult{}, err
	}
	wipLimit := backlog.WipLimit
	if wipLimit <= 0 {
		wipLimit = defaultWipLimit
	}

	var result ApplyResult
	var digestLines []string
	backlogChanged := false

	for _, v := range verdicts {
		item, ok := byID[v.ID]
		if !ok || item.Status != "new" {
			result.Skipped = append(result.Skipped, v.ID)
			continue
		}
		if v.Status == "accepted" && len(backlog.Items) >= wipLimit {
			result.WipLimitHit = append(result.WipLimitHit, v.ID)
			continue
		}

		raw, rerr := os.ReadFile(item.Path)
		if rerr != nil {
			return result, fmt.Errorf("feedback: %s: %w", op, rerr)
		}
		var doc map[string]any
		if uerr := yaml.Unmarshal(raw, &doc); uerr != nil {
			return result, fmt.Errorf("feedback: %s: %s: %w", op, item.Path, uerr)
		}
		doc["status"] = v.Status
		if v.Resolution != "" {
			doc["resolution"] = v.Resolution
		}
		out, merr := yaml.Marshal(doc)
		if merr != nil {
			return result, fmt.Errorf("feedback: %s: %w", op, merr)
		}
		if werr := os.WriteFile(item.Path, out, 0o644); werr != nil {
			return result, fmt.Errorf("feedback: %s: %w", op, werr)
		}

		if v.Status == "accepted" {
			backlog.Items = append(backlog.Items, BacklogItem{
				ID: item.ID, Kind: item.Kind, Severity: item.Severity, Title: item.Title,
				Verdict: "accepted", Route: v.Route, Refs: v.Refs,
				Date: now.UTC().Format("2006-01-02"),
			})
			backlogChanged = true
		}

		result.Applied = append(result.Applied, v.ID)
		digestLines = append(digestLines, fmt.Sprintf("- %s (%s -> %s): %s", item.ID, item.Kind, v.Status, item.Title))
	}

	if backlogChanged {
		if err := writeBacklogDoc(hubRoot, backlog); err != nil {
			return result, err
		}
	}
	if len(digestLines) > 0 {
		if err := appendDigest(hubRoot, now, digestLines); err != nil {
			return result, err
		}
	}
	return result, nil
}

func appendDigest(hubRoot string, now time.Time, lines []string) error {
	const op = "appendDigest"
	path := filepath.Join(hubRoot, filepath.FromSlash(feedbackDigestPath))
	entry := fmt.Sprintf("\n## %s Triage\n\n%s\n", now.UTC().Format("2006-01-02"), strings.Join(lines, "\n"))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	return nil
}
