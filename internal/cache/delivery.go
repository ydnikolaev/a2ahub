package cache

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"gopkg.in/yaml.v3"
)

// This file projects one handoff's data-kind deliverables (spec 05a AC-7,
// plan decision 2: thread-side only, under the handoff that carries them)
// against the data-package/v1 manifest and verification-report/v1 documents
// they name. It is new resolution, not a wiring change: nothing in this
// package or internal/html referenced deliverables[] before this wave
// (verified by grep), and this file is deliberately NOT called from
// threadview.go, mirror.go or decode.go — none of those are in this
// wave's allowlist, so a later wave composes this into ThreadResult/Data.
//
// Reuse, not reimplementation (spec §5, the top-level brief's own
// instruction): the manifest and report shapes are datapackage.Document
// and datapackage.Report, decoded exactly once by internal/datapackage
// (W1) and never re-typed here. datapackage.Report.Result() is the ONLY
// place a verdict is derived from checks[]; this file only ever READS that
// derivation, never recomputes it from Checks itself.

// DeliverableKindData is handoff.schema.json's deliverables[].kind value
// this package resolves. The other four kind values (code, contract,
// config, doc) carry no data-package/verification-report chain and are
// left to their own renderer — DecodeDeliverables returns all of them,
// and it is ResolveDeliveries' job, not the decoder's, to filter.
const DeliverableKindData = "data"

// Deliverable is one handoff.schema.json deliverables[] entry
// ({name, ref, kind}), decoded directly from the raw envelope bytes. This
// package's own minimal decode (decode.go's own documented ISP idiom,
// repeated here rather than added to decode.go, which this wave's
// allowlist does not include): deliverables[] has exactly one consumer,
// this file.
type Deliverable struct {
	Name string `yaml:"name" json:"name"`
	Ref  string `yaml:"ref" json:"ref"`
	Kind string `yaml:"kind" json:"kind"`
}

// deliverablesProbe is DecodeDeliverables' own frontmatter decode target —
// the handoff envelope carries other fields (verification,
// acceptance_criteria, fulfills, ...) that this file has no use for, so
// only deliverables[] is named.
type deliverablesProbe struct {
	Deliverables []Deliverable `yaml:"deliverables"`
}

// DecodeDeliverables decodes a handoff artifact's raw `.md` bytes (the same
// bytes foldedArtifact.Raw / rawArtifact.Raw carries) into its
// deliverables[] array. raw must be frontmatter-shaped
// (artifact.ParseFrontmatter's own contract); a non-handoff document
// simply decodes to an empty slice, since deliverables is not a field it
// carries.
func DecodeDeliverables(raw []byte) ([]Deliverable, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("cache: DecodeDeliverables: %w", err)
	}
	var probe deliverablesProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return nil, fmt.Errorf("cache: DecodeDeliverables: %w", err)
	}
	return probe.Deliverables, nil
}

// DeliveryStatus says whether a deliverable's data-package/v1 manifest
// could be resolved at all — mirrors ContractVersionDetailStatus's own
// rule (internal/html/model.go): "unavailable is an honest result, never
// an empty available package." A delivery whose manifest cannot be
// resolved renders as DeliveryUnavailable, never as a zero-value Delivery
// that silently drops out of a rendered list — see the top-level brief's
// own "guard the failure path" requirement, naming
// ContractVersionDetail's blanking resolver as the shape of defect NOT to
// repeat here.
type DeliveryStatus string

const (
	// DeliveryAvailable means the manifest named by the deliverable's ref
	// (or a chain link's supersedes) was resolved.
	DeliveryAvailable DeliveryStatus = "available"
	// DeliveryUnavailable preserves that a manifest could not be resolved,
	// and why — never a blanked-out payload.
	DeliveryUnavailable DeliveryStatus = "unavailable"
)

// DeliveryVerdictStatus is a resolved delivery's own closed verdict
// vocabulary — one non-empty value per state, so "not yet verified" can
// never collapse into the Go zero value and be mistaken for "passed" by a
// caller that merely checks truthiness. This project has already shipped
// exactly that defect once (a missing report rounded down to a settled
// state); DeliveryVerdictUnverified exists so it cannot happen again here.
//
// Deliberately four members, not the brief's three (pass/fail/"not yet
// verified"): verification-report/v1's own `result` is pass|fail|error
// (datapackage.Result), and collapsing `error` into `fail` would misname a
// checker crash as a content violation — spec 05a §T2.3 fidelity, recorded
// as a deviation in this wave's report.
type DeliveryVerdictStatus string

// The four verdict states a delivery can be in. `unverified` is a state of
// its own and never a synonym for anything else: a delivery nobody has
// judged yet must not read as one that passed.
const (
	DeliveryVerdictPass       DeliveryVerdictStatus = "pass"
	DeliveryVerdictFail       DeliveryVerdictStatus = "fail"
	DeliveryVerdictError      DeliveryVerdictStatus = "error"
	DeliveryVerdictUnverified DeliveryVerdictStatus = "unverified"
)

// DeliveryFailure is one non-passing check from the delivery's own
// verification-report/v1, naming the failing entry and the rule that
// failed it (spec 05a AC-7's own "the failing entry path and the rule that
// failed"). Record is the 1-based ndjson line (T2.6's own "aimed, not
// vague" requirement) — nil for a json entry or a violation that carries
// none.
type DeliveryFailure struct {
	EntryPath string `json:"entry_path"`
	Rule      string `json:"rule"`
	Record    *int64 `json:"record,omitempty"`
}

// DeliveryChainEntry is one attempt in a delivery's supersede chain,
// oldest first — "the supersede chain back to the first attempt, so a
// reader can see what was tried" (top-level brief). Status/Verdict follow
// the same guard as Delivery itself: an entry whose own manifest could not
// be resolved (a dangling `supersedes`) still appears, marked
// DeliveryUnavailable, rather than truncating the chain silently.
type DeliveryChainEntry struct {
	PackageID string                `json:"package_id"`
	Attempt   int                   `json:"attempt,omitempty"`
	Status    DeliveryStatus        `json:"status"`
	Verdict   DeliveryVerdictStatus `json:"verdict,omitempty"`
	ReportID  string                `json:"report_id,omitempty"`
}

// Delivery is one handoff deliverable of kind "data", resolved against its
// data-package/v1 manifest and (if any) verification-report/v1 — spec 05a
// AC-7, scoped thread-side under the handoff that carries it (plan
// decision 2; the per-contract-version delivery table is deferred to P8).
type Delivery struct {
	HandoffID string `json:"handoff_id"`
	Name      string `json:"name"`
	Ref       string `json:"ref"`

	Status            DeliveryStatus `json:"status"`
	UnavailableReason string         `json:"unavailable_reason,omitempty"`

	PackageID  string `json:"package_id,omitempty"`
	Attempt    int    `json:"attempt,omitempty"`
	Supersedes string `json:"supersedes,omitempty"`

	Verdict  DeliveryVerdictStatus `json:"verdict,omitempty"`
	ReportID string                `json:"report_id,omitempty"`

	// Failures is populated only when Verdict is fail or error — every
	// non-passing check in the report, not merely the first, so the next
	// attempt is aimed at everything that is wrong, not just whichever
	// check happened to run first.
	Failures []DeliveryFailure `json:"failures,omitempty"`

	// Chain is always non-nil (an empty array, never omitted or null) —
	// see ResolveDelivery's own doc comment for why an unresolved delivery
	// still carries one.
	Chain []DeliveryChainEntry `json:"chain"`
}

// PackageResolver resolves a data-package/v1 manifest and a
// verification-report/v1 by the identifiers a handoff's deliverable ref
// and a manifest's own `supersedes` field carry. Absence is signaled by
// ok=false, never an error: an unresolved package is a FACT this package
// renders as DeliveryUnavailable/DeliveryVerdictUnverified, not a Go error
// a caller must interpret.
//
// Deliberately NOT a filesystem walk over a space mirror: where `deliver`
// (a parallel, not-yet-landed wave) writes a package's manifest.json and a
// report's report.json inside the space tree is not yet decided, and
// inventing a directory convention here would create a second source of
// truth that wave would then have to either match or break. A later wave
// supplies the real, filesystem-backed implementation once that layout is
// fixed; this package accepts it as an injected seam instead.
type PackageResolver interface {
	// ResolvePackage returns the data-package/v1 manifest ref names (a
	// deliverable's own ref, or a prior manifest's supersedes), and
	// whether it was found.
	ResolvePackage(ref string) (datapackage.Document, bool)
	// ResolveReport returns the verification-report/v1 verifying
	// packageID, and whether one exists at all. false means "not yet
	// verified", never "passed" — see DeliveryVerdictUnverified.
	ResolveReport(packageID string) (datapackage.Report, bool)
}

// maxDeliveryChainLength bounds ResolveDelivery's supersede-chain walk
// (rails: bounded loops everywhere). A pilot-scale loop's real chains are a
// handful of attempts long; this only exists to turn a corrupted or
// cyclic `supersedes` pointer into a bounded, reported degradation instead
// of a hang.
const maxDeliveryChainLength = 1000

// ResolveDeliveries decodes handoffID's deliverables[] from raw and
// resolves every kind:"data" entry against resolver, in deliverables[]
// order. Every OTHER kind is silently excluded — this function's whole
// job is the data-package/verification-report projection; a "code" or
// "doc" deliverable has no manifest to resolve and belongs to a different
// renderer.
func ResolveDeliveries(handoffID string, raw []byte, resolver PackageResolver) ([]Delivery, error) {
	deliverables, err := DecodeDeliverables(raw)
	if err != nil {
		return nil, err
	}
	out := make([]Delivery, 0, len(deliverables))
	for _, d := range deliverables {
		if d.Kind != DeliverableKindData {
			continue
		}
		out = append(out, ResolveDelivery(handoffID, d, resolver))
	}
	return out, nil
}

// ResolveDelivery resolves one data-kind deliverable. It ALWAYS returns a
// Delivery — an unresolvable manifest still produces one, marked
// DeliveryUnavailable, rather than a caller-visible nil/omission: "a
// delivery whose manifest cannot be resolved must render as 'unresolved',
// never vanish" is the top-level brief's own guard, named after
// ContractVersionDetail's unavailable-branch that blanks its whole
// payload — this function's unavailable branch fills in every field it
// legitimately can (HandoffID, Name, Ref, the reason) instead of
// following that precedent.
func ResolveDelivery(handoffID string, d Deliverable, resolver PackageResolver) Delivery {
	del := Delivery{HandoffID: handoffID, Name: d.Name, Ref: d.Ref, Chain: []DeliveryChainEntry{}}

	doc, ok := resolver.ResolvePackage(d.Ref)
	if !ok {
		del.Status = DeliveryUnavailable
		del.UnavailableReason = fmt.Sprintf("data package %q referenced by handoff %q's deliverable %q could not be resolved", d.Ref, handoffID, d.Name)
		return del
	}
	del.Status = DeliveryAvailable
	del.PackageID = doc.ID
	del.Attempt = doc.Attempt
	del.Supersedes = doc.Supersedes

	if report, hasReport := resolver.ResolveReport(doc.ID); hasReport {
		del.ReportID = report.ID
		del.Verdict = verdictFor(report)
		if del.Verdict != DeliveryVerdictPass {
			del.Failures = failuresFor(report)
		}
	} else {
		// Absent is unknown, never good (top-level brief's own words) —
		// see DeliveryVerdictStatus's doc comment for why this is a named
		// constant and not the zero value.
		del.Verdict = DeliveryVerdictUnverified
	}

	del.Chain = deliveryChain(doc, resolver)
	return del
}

// deliveryChain walks doc.Supersedes back to the first attempt, resolving
// each attempt's own verdict along the way, then reverses the result so it
// reads oldest-first — "the supersede chain back to the first attempt, so
// a reader can see what was tried."
func deliveryChain(doc datapackage.Document, resolver PackageResolver) []DeliveryChainEntry {
	chain := make([]DeliveryChainEntry, 0, 4)
	visited := map[string]bool{}
	current := doc
	for i := 0; i < maxDeliveryChainLength; i++ {
		if visited[current.ID] {
			// A cyclic or self-referencing supersedes chain must not hang
			// this walk; stop rather than loop forever.
			break
		}
		visited[current.ID] = true

		entry := DeliveryChainEntry{PackageID: current.ID, Attempt: current.Attempt, Status: DeliveryAvailable}
		if report, hasReport := resolver.ResolveReport(current.ID); hasReport {
			entry.ReportID = report.ID
			entry.Verdict = verdictFor(report)
		} else {
			entry.Verdict = DeliveryVerdictUnverified
		}
		chain = append(chain, entry)

		if current.Supersedes == "" {
			break
		}
		prev, ok := resolver.ResolvePackage(current.Supersedes)
		if !ok {
			// The chain does not simply stop at a dangling pointer — it
			// says so, the same "unresolved, never vanished" guard
			// ResolveDelivery applies at the top level.
			chain = append(chain, DeliveryChainEntry{PackageID: current.Supersedes, Status: DeliveryUnavailable})
			break
		}
		current = prev
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// verdictFor is the only place a datapackage.Report's own Result() becomes
// a DeliveryVerdictStatus — it reads the derivation datapackage.NewReport
// already performed (§T2.3: "derived from checks[], never authored
// directly") and never recomputes it from Checks itself.
func verdictFor(report datapackage.Report) DeliveryVerdictStatus {
	switch report.Result() {
	case datapackage.ResultPass:
		return DeliveryVerdictPass
	case datapackage.ResultError:
		return DeliveryVerdictError
	default: // datapackage.ResultFail
		return DeliveryVerdictFail
	}
}

// failuresFor names every non-passing check in report — not just the
// first — path, rule (the check's own stable id) and, for an ndjson
// entry's violation, its record number.
func failuresFor(report datapackage.Report) []DeliveryFailure {
	var out []DeliveryFailure
	for _, c := range report.Checks {
		if c.Status == datapackage.CheckPass {
			continue
		}
		if len(c.Violations) == 0 {
			out = append(out, DeliveryFailure{EntryPath: c.Path, Rule: c.ID})
			continue
		}
		for _, v := range c.Violations {
			out = append(out, DeliveryFailure{EntryPath: c.Path, Rule: c.ID, Record: v.Record})
		}
	}
	return out
}
