package html

import "github.com/ydnikolaev/a2ahub/internal/cache"

// This file projects internal/cache's Delivery read model (spec 05a AC-7:
// package id, attempt number, verdict, failing entry/rule, supersede
// chain — thread-side, under the handoff that carries it, plan decision
// 2) into the dashboard's own render vocabulary. It is deliberately NOT
// wired into Data/Thread/ThreadView here: model.go, assemble.go and
// dashboard_renderer.go are not in this wave's allowlist, so a later wave
// composes ProjectDeliveries' output into the page. This file's own test
// is fixture-driven for exactly that reason — it proves the projection
// end to end without needing the composition it will eventually sit
// inside.
//
// JSON keys are camelCase, matching model.go's own documented convention
// ("consumed by the page's client JS"), unlike internal/cache's
// snake_case CLI-facing shape.

// DeliveryResolution says whether a delivery's data-package/v1 manifest
// could be resolved — the render-layer mirror of cache.DeliveryStatus.
type DeliveryResolution string

const (
	DeliveryResolved   DeliveryResolution = "resolved"
	DeliveryUnresolved DeliveryResolution = "unresolved"
)

// DeliveryVerdict is the dashboard's own closed rendering vocabulary for a
// Delivery's verdict — one non-empty string per state, so "not yet
// verified" can never be mistaken for "passed" by a template that merely
// checks truthiness. This project has already shipped exactly that
// defect once (a missing report rounded down to a settled state); every
// branch here is named for that reason.
type DeliveryVerdict string

const (
	DeliveryVerdictPassed     DeliveryVerdict = "passed"
	DeliveryVerdictFailed     DeliveryVerdict = "failed"
	DeliveryVerdictErrored    DeliveryVerdict = "errored"
	DeliveryVerdictUnverified DeliveryVerdict = "unverified"
)

// DeliveryFailure is one non-passing check, projected for render.
type DeliveryFailure struct {
	EntryPath string `json:"entryPath"`
	Rule      string `json:"rule"`
	Record    *int64 `json:"record,omitempty"`
}

// DeliveryChainEntry is one attempt in a delivery's supersede chain,
// oldest first.
type DeliveryChainEntry struct {
	PackageID  string             `json:"packageId"`
	Attempt    int                `json:"attempt,omitempty"`
	Resolution DeliveryResolution `json:"resolution"`
	Verdict    DeliveryVerdict    `json:"verdict,omitempty"`
	ReportID   string             `json:"reportId,omitempty"`
}

// Delivery is one handoff deliverable of kind "data", projected for
// render under its handoff.
//
// NOTE on the id-prefix badge: the dashboard's TypeBadge convention
// (web/src/components/TypeBadge.astro) is keyed on a closed X-prefix set
// and lives outside this wave's allowlist (it is not Go at all). `DP-`
// and `VR-` — this delivery's own PackageID/ReportID prefixes — are
// neither an X- exchange type nor in that closed set, so they will
// render as "unknown" there until a web-allowlisted change extends it
// deliberately. Recorded here, and in this wave's own deviations report,
// per the top-level brief's explicit instruction not to paper over it.
type Delivery struct {
	HandoffID string `json:"handoffId"`
	Name      string `json:"name"`
	Ref       string `json:"ref"`

	Resolution  DeliveryResolution `json:"resolution"`
	Unavailable string             `json:"unavailable,omitempty"`

	PackageID  string `json:"packageId,omitempty"`
	Attempt    int    `json:"attempt,omitempty"`
	Supersedes string `json:"supersedes,omitempty"`

	Verdict  DeliveryVerdict `json:"verdict,omitempty"`
	ReportID string          `json:"reportId,omitempty"`

	Failures []DeliveryFailure `json:"failures,omitempty"`

	// Chain is always non-nil (an empty array, never omitted or null),
	// mirroring cache.Delivery.Chain's own guarantee.
	Chain []DeliveryChainEntry `json:"chain"`
}

// ProjectDeliveries converts cache's read model into the dashboard's own
// render vocabulary. It never re-derives a verdict from checks — that
// derivation already happened once, in datapackage.NewReport, and
// cache.ResolveDelivery only read it; this function only ever translates
// an already-resolved cache.DeliveryVerdictStatus into its render-layer
// spelling.
func ProjectDeliveries(deliveries []cache.Delivery) []Delivery {
	out := make([]Delivery, 0, len(deliveries))
	for _, d := range deliveries {
		out = append(out, projectDelivery(d))
	}
	return out
}

func projectDelivery(d cache.Delivery) Delivery {
	pd := Delivery{
		HandoffID: d.HandoffID, Name: d.Name, Ref: d.Ref,
		PackageID: d.PackageID, Attempt: d.Attempt, Supersedes: d.Supersedes,
		ReportID: d.ReportID,
		Chain:    make([]DeliveryChainEntry, 0, len(d.Chain)),
	}

	if d.Status == cache.DeliveryUnavailable {
		pd.Resolution = DeliveryUnresolved
		pd.Unavailable = d.UnavailableReason
		return pd
	}
	pd.Resolution = DeliveryResolved
	pd.Verdict = projectVerdict(d.Verdict)

	for _, f := range d.Failures {
		pd.Failures = append(pd.Failures, DeliveryFailure{EntryPath: f.EntryPath, Rule: f.Rule, Record: f.Record})
	}
	for _, c := range d.Chain {
		pd.Chain = append(pd.Chain, projectChainEntry(c))
	}
	return pd
}

func projectChainEntry(c cache.DeliveryChainEntry) DeliveryChainEntry {
	entry := DeliveryChainEntry{PackageID: c.PackageID, Attempt: c.Attempt, ReportID: c.ReportID}
	if c.Status == cache.DeliveryUnavailable {
		entry.Resolution = DeliveryUnresolved
		return entry
	}
	entry.Resolution = DeliveryResolved
	entry.Verdict = projectVerdict(c.Verdict)
	return entry
}

// projectVerdict is the ONLY place a cache.DeliveryVerdictStatus becomes a
// DeliveryVerdict. Every known value has its own explicit case; an
// unrecognized value renders as "unknown:<value>" rather than silently
// falling back to the empty string (which a template would then be free
// to treat as "not failed", i.e. passing) — visibly wrong beats silently
// wrong. See this file's own seeded-red receipt on the
// DeliveryVerdictUnverified branch specifically, since that is the one
// this project has already gotten wrong once.
func projectVerdict(v cache.DeliveryVerdictStatus) DeliveryVerdict {
	switch v {
	case cache.DeliveryVerdictPass:
		return DeliveryVerdictPassed
	case cache.DeliveryVerdictFail:
		return DeliveryVerdictFailed
	case cache.DeliveryVerdictError:
		return DeliveryVerdictErrored
	case cache.DeliveryVerdictUnverified:
		return DeliveryVerdictUnverified
	default:
		return DeliveryVerdict("unknown:" + string(v))
	}
}
