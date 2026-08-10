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

// FreshnessStatus is AC3's three-valued verdict about whether hubRoot's
// feedback/inbox/** agrees with the hub of record (own-loop 09 §8 AC3).
// FreshnessRefused is deliberately iota 0: a Freshness value nobody
// resolved (a zero value, a nil resolver) must fail CLOSED into a refusal
// rather than silently reading as "confirmed current" — the same
// fail-closed shape AC3 itself asks Triage to enforce (§11 2026-08-10).
type FreshnessStatus int

const (
	// FreshnessRefused: hubRoot is a git work tree and the hub-of-record
	// check either found drift, could not be completed, or found no hub
	// configured (AC3 cases (a)/(b)/(c)) — Triage must refuse, and must
	// never report "inbox clean".
	FreshnessRefused FreshnessStatus = iota
	// FreshnessCurrent: the hub of record was checked and hubRoot agrees
	// with it — "inbox clean" is reachable if the local listing is also
	// empty.
	FreshnessCurrent
	// FreshnessNotApplicable: hubRoot is not a git work tree, so there is
	// no hub of record to diverge from — "inbox clean" is reachable (AC3:
	// "Only a hubRoot that is NOT a git work tree yields not-applicable").
	FreshnessNotApplicable
)

// Freshness is the ALREADY-RESOLVED freshness verdict Triage takes as an
// input. It is computed at the CLI (cmd/a2a's runFeedback, the only place
// that may fetch, shell out, or reach a remote for this purpose) and passed
// in — Triage itself never reaches a remote (own-loop 09 §8 AC3, §11 wave D:
// "the judgment stays out of the domain and only the already-computed
// verdict crosses the seam").
type Freshness struct {
	// Status is the three-valued verdict.
	Status FreshnessStatus
	// Reason names the drift, the failure, or the missing configuration
	// when Status == FreshnessRefused. Empty means the verdict was never
	// resolved (a nil resolver, a forgotten wire-up) rather than an actual
	// checked-and-refused outcome; Triage's error names that explicitly
	// rather than printing a blank reason.
	Reason string
	// HubOfRecord names the hub Triage's refusal error should cite. Empty
	// means no hub was configured to check against (AC3 case (c)).
	HubOfRecord string
}

// freshnessRefusalError renders f's refusal as an error naming both the
// reason and the hub of record (AC3: triage "refuses — naming the drift and
// the hub of record").
func freshnessRefusalError(f Freshness) error {
	hub := f.HubOfRecord
	if hub == "" {
		hub = "(no hub of record configured)"
	}
	reason := f.Reason
	if reason == "" {
		reason = "freshness verdict was never resolved"
	}
	return fmt.Errorf("feedback: Triage: refusing — hub of record %s is not confirmed current: %s", hub, reason)
}

// Triage lists every status:new feedback/inbox/*.yaml item under hubRoot,
// with dedupe candidates drawn from the rest of the inbox (any status)
// and from feedback/backlog.yaml. It is read-only — verdict judgment and
// write-back is ApplyVerdicts (§T1: "mechanical part is the verb; the
// judgment... is the triage procedure in AGENTS.md").
//
// freshness is AC3's already-resolved hub-of-record verdict. Triage refuses
// — before reading a single feedback/inbox/*.yaml file — unless freshness is
// FreshnessCurrent or FreshnessNotApplicable; it never fetches or shells out
// itself (§8 AC3, §11 wave D: "resolved at the CLI... and passed in, never
// inside Triage").
func Triage(hubRoot string, freshness Freshness) (TriageReport, error) {
	if freshness.Status != FreshnessCurrent && freshness.Status != FreshnessNotApplicable {
		return TriageReport{}, freshnessRefusalError(freshness)
	}
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
		out, aerr := applyStatusResolution(raw, v.Status, v.Resolution)
		if aerr != nil {
			return result, fmt.Errorf("feedback: %s: %s: %w", op, item.Path, aerr)
		}
		// G703 fires here and not on the ReadFile above because the bytes
		// written are now derived from the bytes read at the same path, which
		// is the whole point of the change: the record is EDITED rather than
		// regenerated. The path itself is not attacker-influenced — it comes
		// from filepath.Glob over hubRoot/feedback/inbox, so its filename
		// component is enumerated by this package, never supplied by a
		// verdict. A verdict only ever names an `id`, and an id that matches
		// no enumerated record is skipped, never turned into a path.
		if werr := os.WriteFile(item.Path, out, 0o644); werr != nil { //nolint:gosec // reason: item.Path is enumerated by filepath.Glob under hubRoot's own feedback/inbox, never built from a verdict.
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

// resolutionWrapWidth is the target column count for a wrapped folded
// resolution body line, BEFORE the 4-space indent is added (§ own-loop 09
// wave C: "wrapped at ~76 columns and indented 4 spaces").
const resolutionWrapWidth = 76

// resolutionIndent is the fixed indentation applied to every body line of a
// rendered resolution block (folded or literal).
const resolutionIndent = "    "

// applyStatusResolution rewrites raw's top-level `status:` line and, when
// resolution is non-empty, its top-level `resolution:` section — leaving
// every other byte of the document exactly as it was (own-loop 09 §11,
// 2026-08-09: "ApplyVerdicts must edit only the two fields it owns and leave
// the record's other bytes untouched"). It fails closed: exactly one
// top-level `status:` line must exist, and the rendered result must
// yaml.Unmarshal back to the intended values, or no bytes are returned at
// all (the caller does not write on error).
func applyStatusResolution(raw []byte, status, resolution string) ([]byte, error) {
	hadTrailingNewline := len(raw) > 0 && raw[len(raw)-1] == '\n'
	text := string(raw)
	if hadTrailingNewline {
		text = text[:len(text)-1]
	}
	lines := strings.Split(text, "\n")

	statusIdx := -1
	statusCount := 0
	for i, ln := range lines {
		if isTopLevelKey(ln, "status") {
			statusCount++
			statusIdx = i
		}
	}
	if statusCount != 1 {
		return nil, fmt.Errorf("found %d top-level `status:` lines, want exactly 1", statusCount)
	}
	lines[statusIdx] = "status: " + status

	if resolution != "" {
		section := renderResolutionSection(resolution)

		resIdx := -1
		for i, ln := range lines {
			if isTopLevelKey(ln, "resolution") {
				resIdx = i
				break
			}
		}
		if resIdx == -1 {
			out := make([]string, 0, len(lines)+len(section))
			out = append(out, lines[:statusIdx]...)
			out = append(out, section...)
			out = append(out, lines[statusIdx:]...)
			lines = out
		} else {
			end := topLevelSectionEnd(lines, resIdx)
			out := make([]string, 0, len(lines)-(end-resIdx)+len(section))
			out = append(out, lines[:resIdx]...)
			out = append(out, section...)
			out = append(out, lines[end:]...)
			lines = out
		}
	}

	outText := strings.Join(lines, "\n")
	if hadTrailingNewline {
		outText += "\n"
	}
	outBytes := []byte(outText)

	// Fail-closed: an emitted block that does not round-trip to the intended
	// values (an unrepresentable shape — a trailing newline a literal block
	// strips, whitespace a folded block cannot preserve, ambiguous literal
	// indentation) is a refusal, never a silently reshaped record.
	var doc map[string]any
	if uerr := yaml.Unmarshal(outBytes, &doc); uerr != nil {
		return nil, fmt.Errorf("rendered YAML failed to parse: %w", uerr)
	}
	if got, _ := doc["status"].(string); got != status {
		return nil, fmt.Errorf("rendered status = %q, want %q", got, status)
	}
	if resolution != "" {
		if got, _ := doc["resolution"].(string); got != resolution {
			return nil, fmt.Errorf("rendered resolution did not round-trip to the verdict text")
		}
	}
	return outBytes, nil
}

// isTopLevelKey reports whether line is a column-0 mapping key named key —
// i.e. begins with "key:" and is therefore neither indented (a nested key)
// nor a different key sharing the prefix (e.g. "status" vs "statuses").
func isTopLevelKey(line, key string) bool {
	return strings.HasPrefix(line, key+":")
}

// topLevelSectionEnd returns the exclusive end index of the top-level
// section starting at lines[start]: that line plus every following line
// that is blank or starts with whitespace, which is how a folded (`>-`) or
// literal (`|-`) scalar's continuation lines stay attached to their key.
func topLevelSectionEnd(lines []string, start int) int {
	end := start + 1
	for end < len(lines) {
		ln := lines[end]
		if ln == "" || strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t") {
			end++
			continue
		}
		break
	}
	return end
}

// renderResolutionSection renders a top-level `resolution:` section
// (key line + indented body) for text: a literal block (`|-`) when text
// contains a newline, a folded block (`>-`) otherwise — UNLESS text's
// whitespace is not already single-spaced (a run of spaces, a tab, or
// leading/trailing space), which folding cannot represent without
// reshaping it, so that case also falls back to literal.
func renderResolutionSection(text string) []string {
	if strings.Contains(text, "\n") || !isFoldable(text) {
		return renderLiteralResolution(text)
	}
	return renderFoldedResolution(text)
}

// isFoldable reports whether text can be losslessly represented as a YAML
// folded scalar: no newline, and its whitespace is already exactly single
// spaces between words with no leading/trailing space or tabs — anything
// else, folding (which unfolds every line break to one space) would change.
func isFoldable(text string) bool {
	return text != "" && !strings.Contains(text, "\n") && strings.Join(strings.Fields(text), " ") == text
}

// renderFoldedResolution renders text as `resolution: >-` with its body
// wrapped at ~resolutionWrapWidth columns, split on whitespace only (never
// inside a token — a hand-written wrapper broke a path at a hyphen one
// commit before this fix, and folding then joins the halves with a space,
// producing a path that resolves nowhere) and indented resolutionIndent.
func renderFoldedResolution(text string) []string {
	out := make([]string, 0, 4)
	out = append(out, "resolution: >-")
	for _, ln := range wrapWords(text, resolutionWrapWidth) {
		out = append(out, resolutionIndent+ln)
	}
	return out
}

// wrapWords wraps words (split on whitespace) into lines of at most width
// columns, never splitting a word — a single word longer than width goes on
// its own line, unbroken. The accumulator always starts non-empty (the next
// unplaced word), so a first word alone longer than width can never flush an
// empty line ahead of it (never "emit a blank line inside the body").
func wrapWords(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, 4)
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	lines = append(lines, cur)
	return lines
}

// renderLiteralResolution renders text as `resolution: |-` with every line
// of text indented resolutionIndent, preserving interior whitespace and
// blank lines byte-for-byte (unlike folding, a literal block never rewrites
// its body — the fail-closed round-trip check in applyStatusResolution
// refuses any shape this cannot represent, e.g. a trailing newline or a
// value whose own first line already carries leading whitespace).
func renderLiteralResolution(text string) []string {
	out := make([]string, 0, 4)
	out = append(out, "resolution: |-")
	for _, ln := range strings.Split(text, "\n") {
		if ln == "" {
			out = append(out, "")
			continue
		}
		out = append(out, resolutionIndent+ln)
	}
	return out
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
