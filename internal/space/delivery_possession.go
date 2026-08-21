package space

// P6 (agent-exchange-2026-08) spec 06 §11's 2026-08-10 "AC9 wire decision"
// (cross-referenced from spec 04 §11's own "AC9's wire decision is taken,
// and it lives in spec 06"): the SUBMIT half of AC9. A response cannot name
// a package directly — the wire is two hops, response --refs--> handoff
// --deliverables[].ref--> data package (docs/the-plan/plan/16-handoff-
// directive.md §16.1) — so this file resolves a response's REFERENCED
// handoff and refuses to submit the response when that handoff declares a
// `kind: data` deliverable the space cannot resolve.
//
// It sits BESIDE possession.go's CheckAttachmentPossession, not inside it:
// a different declaration (`refs` naming a handoff, not `attachments[]`),
// a different resolution path (a handoff artifact read by id, not a
// digest-only structural check), but the SAME funnel seat (funnel.go's
// submitPreparedRequest, after the idempotent short-circuit, before any
// commit/push git action) and the SAME "no local filesystem read" property
// possession.go's own doc comment names as its whole point: the handoff is
// read via contractGitReadPath against origin/main
// (contractAuthoritativeMainRef), never the author's working tree, and the
// deliverable's package is resolved via ResolveDataPackage — the exact same
// call CheckAttachmentPossession already uses.
//
// internal/space may import internal/datapackage for exactly ONE existing
// reason (deliverable.go's own doc comment) — but that reason is internal/
// cache's, not internal/space's: docs/decisions.md:34 pins internal/
// space's own allowed imports as artifact, schema, validate, host,
// workreport, contract, and internal/datapackage is not among them. So this
// file does NOT import internal/datapackage's DecodeDeliverables/
// Deliverable/DeliverableKindData; it carries its own ~15-line
// package-local probe instead, the SAME ADR-001-mirror idiom possession.go
// (Attachment mirrors datapackage.Attachment) and data_resolve.go
// (dataPackagePrefix mirrors datapackage.PackagePrefix) already use.
// DEVIATION from the brief's "which space can import freely": recorded in
// this wave's report for the lead to resolve — either ADR-001 gains
// datapackage on space's row, or a shared decoder needs a home space may
// import without exception.
//
// SINCE 2026-08-21 THE PREMISE ABOVE IS HALF TRUE, and the correction is
// this file's second half (judge-the-thing-2026-08 P1, spec 01). A response
// CAN now name a package directly — envelope/v2 response's `delivers[]` —
// and checkResponseDeliversPossession resolves every named package through
// ResolveDataPackage against the SAME origin/main ref, at the SAME funnel
// seat. That closes fb-20260808-d5740f, which the two-hop wire above could
// not: when the handoff is still inside the unmerged payload PR,
// resolveHandoffArtifact finds nothing and this file's own `if !found
// { continue }` residue says "out of scope" — so a delivery whose payload
// had not landed was announced and nothing refused it. A response that
// names its package turns that same unmerged-payload fact into the check.
//
// THE NARROWNESS RULE IS THE WHOLE RISK (spec 06 §11, 2026-08-10, twice):
// `result: delivered` is the ordinary result word for a plainly-answered
// work_request, driven by SIX call sites in the P11 conformance catalogue
// on exchanges with no data package anywhere near them. So this file must
// refuse ONLY when a response's own `refs[]` names an artifact that
// resolves to an actual handoff carrying a `kind: data` deliverable whose
// package does not resolve — never merely because `result: delivered` was
// used, never because a referenced id fails to resolve to a handoff at
// all (that is REF-017's territory, out of scope here by the brief's own
// instruction), and never because a handoff has no data-kind deliverable.
import (
	"context"
	"errors"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"gopkg.in/yaml.v3"
)

// ErrHandoffDeliverableUnresolvable refuses a response about to be
// submitted with `result: delivered` that references (via `refs[]`) a
// handoff whose `kind: data` deliverable does not resolve through the
// space's own resolution path — the submit-side half of AC9 (spec 06
// §11's 2026-08-10 amendment; spec 04 §11's principle: "a reference an
// agent cannot resolve is worse than no reference").
var ErrHandoffDeliverableUnresolvable = errors.New("space: response's fulfilling handoff names a data deliverable that does not resolve through the space's own resolution path")

// ErrResponseDeliversUnlanded refuses a response that NAMES the data
// package it announces (envelope/v2 response `delivers[]`, judge-the-thing-
// 2026-08 P1) while that package does not resolve out of the space's own
// origin/main tree.
//
// This is the fix fb-20260808-d5740f asked for, and it works BECAUSE of the
// property that defeated the atomic alternative: a data delivery is ONE
// submit (DeliverDataPackage — payload + handoff + event) and the response
// is a SECOND, independent PR, so at response time an unmerged payload is
// exactly a package that does not resolve against contractAuthoritative
// MainRef. The golden path is untouched: the shipped reference flow
// responds only after consumer verification, by which point the payload is
// long merged.
//
// The message carries its registry code (REF-024, schemas/errors/v1/
// registry.yaml) because the funnel's refusal is what BOTH surfaces print
// verbatim — `lifecycleDeps.submit` (internal/cli/cmd_lifecycle.go) writes
// "%s: %v" straight to stderr, and internal/mcp's own error path does the
// same — so naming the code DOWN here (ADR-004) is what keeps the CLI and
// MCP from each owning a private copy of the mapping.
var ErrResponseDeliversUnlanded = errors.New("space: REF-024: this response names a data package that has not landed on the space's main branch — the delivery's own pull request must merge before the response announcing it")

// handoffPrefix is the §3.3 artifact-ID prefix for a handoff — mirrors
// artifact.ID.Prefix's expected value for the type Layout.Exchange stores
// every XQ/XW/XH/XA/XS document under (layout.go's own doc comment).
const handoffPrefix = "XH"

// responseDeliveryProbe reads only the two top-level keys this file needs
// out of a response's own frontmatter — `result` (to gate on `delivered`)
// and `refs` (to find a fulfilling handoff) — exactly the same
// probe-a-single-concern idiom possession.go's envelopeAttachmentsProbe
// and data_delivery.go's own probes already use.
type responseDeliveryProbe struct {
	Type   string                    `yaml:"type"`
	Result string                    `yaml:"result"`
	Refs   []responseDeliveryRefWire `yaml:"refs"`
	// Delivers is envelope/v2 response.schema.json's own `delivers[]` —
	// the packages this response ANNOUNCES as delivered. Its absence is
	// the ordinary answer shape and stays unremarkable (§T2).
	Delivers []string `yaml:"delivers"`
}

type responseDeliveryRefWire struct {
	Ref string `yaml:"ref"`
}

// handoffDeliverableWire is one handoff.schema.json deliverables[] entry —
// only the two fields this check reads (ref, kind); name is not this file's
// business (it never surfaces a human-facing message naming the deliverable
// beyond its ref, matching CheckAttachmentPossession's own error shape).
type handoffDeliverableWire struct {
	Ref  string `yaml:"ref"`
	Kind string `yaml:"kind"`
}

type handoffDeliverablesProbe struct {
	Deliverables []handoffDeliverableWire `yaml:"deliverables"`
}

// deliverableKindData mirrors datapackage.DeliverableKindData without
// importing internal/datapackage — see this file's own top-of-file
// ADR-001 note.
const deliverableKindData = "data"

// resultDelivered is response.schema.json's own `result` enum member this
// check gates on (v1 and v2 both: "answered|delivered|partial|cannot").
const resultDelivered = "delivered"

// declaredResponseDelivery best-effort decodes raw's frontmatter into
// responseDeliveryProbe. A file with no frontmatter, or frontmatter that
// does not decode, has nothing this check can act on — ok=false, never an
// error, mirroring decodeHandoffFulfills' (mirror.go, internal/cache) own
// best-effort convention for the identical class of "this file may not
// even be an envelope" uncertainty.
func declaredResponseDelivery(raw []byte) (responseDeliveryProbe, bool) {
	fm, hasFrontmatter := frontmatterOrNone(raw)
	if !hasFrontmatter {
		return responseDeliveryProbe{}, false
	}
	var probe responseDeliveryProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return responseDeliveryProbe{}, false
	}
	return probe, true
}

// declaredHandoffDeliverables best-effort decodes a handoff artifact's own
// deliverables[] — same convention as declaredResponseDelivery above.
func declaredHandoffDeliverables(raw []byte) []handoffDeliverableWire {
	fm, hasFrontmatter := frontmatterOrNone(raw)
	if !hasFrontmatter {
		return nil
	}
	var probe handoffDeliverablesProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return nil
	}
	return probe.Deliverables
}

// resolveHandoffArtifact reads one handoff's committed bytes out of
// mirrorDir's origin/main tree, by id — the by-id counterpart to
// ResolveDataPackage (data_resolve.go), reusing the SAME unexported git
// substrate (contractGitResolveCommit/contractGitReadPath,
// contract_git.go) work_history.go's ResolveWorkContinuation already reads
// a single committed artifact through.
//
// ok=false covers EVERY way a referenced id fails to resolve to a handoff
// at all: not an XH- id, no NewLayout for its system, the commit/path
// cannot be resolved, the file is missing, or it is an unsafe git entry.
// None of those become an error — spec 06 §11's own narrowness rule: "a
// referenced id that does not resolve to a handoff at all is OUT OF SCOPE,
// a dangling ref is REF-017's territory, not this rule's." Only ctx's own
// cancellation propagates as an error, since that is a caller-owned fault,
// not a fact about the referenced id.
//
// The lookup itself is a closure returning ([]byte, bool) with NO error, and
// that signature is the point rather than a style choice. Written flat, every
// one of its four failure branches reads `err != nil` and returns a nil error,
// which `nilerr` flags four times — correctly, in general: silently swallowing
// an error is usually a bug. Four suppressions would each have to re-argue why
// it is not one here. A signature that cannot express an error says it once, in
// the only place a reader looks, and cannot be got wrong later.
func resolveHandoffArtifact(ctx context.Context, mirrorDir, handoffID string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	lookup := func() ([]byte, bool) {
		parsed, err := artifact.ParseID(handoffID)
		if err != nil || parsed.Prefix != handoffPrefix {
			return nil, false
		}
		layout, err := NewLayout(parsed.System)
		if err != nil {
			return nil, false
		}
		commit, err := contractGitResolveCommit(ctx, mirrorDir, contractAuthoritativeMainRef)
		if err != nil {
			return nil, false
		}
		file, err := contractGitReadPath(ctx, mirrorDir, commit, layout.Exchange(handoffID), maxConfigBytes)
		if err != nil {
			return nil, false
		}
		return file.Raw, true
	}
	raw, ok := lookup()
	return raw, ok, nil
}

// checkHandoffDeliverablePossession resolves every kind:"data" deliverable
// handoffRaw declares through ResolveDataPackage — the space's own
// resolution path, exactly as CheckAttachmentPossession already uses for
// attachments[]. A handoff with no data-kind deliverable costs nothing and
// returns nil, matching the "an artifact declaring none behaves exactly as
// it does today" property possession.go's own CheckAttachmentPossession
// documents for attachments[].
func checkHandoffDeliverablePossession(ctx context.Context, mirrorDir, handoffID string, handoffRaw []byte) error {
	for _, d := range declaredHandoffDeliverables(handoffRaw) {
		if d.Kind != deliverableKindData {
			continue
		}
		if _, err := ResolveDataPackage(ctx, mirrorDir, d.Ref); err != nil {
			return fmt.Errorf("%w: handoff %q's deliverable %q: %w", ErrHandoffDeliverableUnresolvable, handoffID, d.Ref, err)
		}
	}
	return nil
}

// checkResponseDeliversPossession resolves every package a response NAMES
// in `delivers[]` through ResolveDataPackage — the same origin/main
// resolution `a2a data fetch` itself uses (data_resolve.go), so the refusal
// and the fetch agree by construction rather than by two implementations of
// "is this package here" agreeing by luck.
//
// An empty/absent delivers[] costs one length check, matching the "an
// artifact declaring none behaves exactly as it does today" property
// CheckAttachmentPossession documents for attachments[].
//
// A MALFORMED id refuses here too, wrapped rather than re-parsed:
// ResolveDataPackage answers ErrDataPackageInvalidReference for an id that
// is not DP-shaped, and routing it through the same seat keeps
// ParsePackageID's grammar the one authority (spec §T2). This is NOT the
// refs[] half's "a ref that resolves to no handoff is out of scope" rule:
// a `refs[]` entry may legitimately name any artifact kind, while a
// `delivers[]` entry has exactly one meaning and an id that cannot be one
// is a claim the space can never satisfy.
func checkResponseDeliversPossession(ctx context.Context, mirrorDir string, delivers []string) error {
	for _, packageID := range delivers {
		if _, err := ResolveDataPackage(ctx, mirrorDir, packageID); err != nil {
			return fmt.Errorf("%w: %q: %w", ErrResponseDeliversUnlanded, packageID, err)
		}
	}
	return nil
}

// checkResponseDeliveryPossession is the per-file entry point: raw is one
// file about to be committed. It costs nothing unless raw decodes as a
// response with `result: delivered` naming at least one `refs[]` entry —
// the narrow gate spec 06 §11 requires so this check cannot fire on any of
// the P11 catalogue's six plain-answer `delivered` call sites.
func checkResponseDeliveryPossession(ctx context.Context, mirrorDir string, raw []byte) error {
	probe, ok := declaredResponseDelivery(raw)
	if !ok || probe.Type != "response" {
		return nil
	}
	// The `delivers[]` half runs FIRST and on its own trigger — the field's
	// PRESENCE, never the result word. That split is the whole point of
	// minting a field instead of reusing refs[] (spec §T2): `result:
	// delivered` is the ordinary answer word and SIX declared catalogue
	// paths (seven call sites) use it with no package anywhere near them,
	// so a check keyed on it reds them (2026-08-10, and the check was
	// narrowed for exactly that); a check
	// keyed on `delivers` cannot reach a document that does not carry it.
	// Not gated on `delivered` either: an author naming a package they do
	// not hold is exactly as unactionable at `partial`, and no plain answer
	// carries the field at all, so the narrowness is not bought by it.
	if err := checkResponseDeliversPossession(ctx, mirrorDir, probe.Delivers); err != nil {
		return err
	}
	if probe.Result != resultDelivered || len(probe.Refs) == 0 {
		return nil
	}
	for _, ref := range probe.Refs {
		handoffRaw, found, err := resolveHandoffArtifact(ctx, mirrorDir, ref.Ref)
		if err != nil {
			return err
		}
		if !found {
			// No referenced handoff (this ref is not a handoff at all, or
			// does not resolve to one) -> nothing to check for THIS ref.
			// Not a refusal (spec 06 §11: out of scope, REF-017's territory).
			continue
		}
		if err := checkHandoffDeliverablePossession(ctx, mirrorDir, ref.Ref, handoffRaw); err != nil {
			return err
		}
	}
	return nil
}

// checkSubmitResponseDeliveryPossession is submitPreparedRequest's own
// entry point (funnel.go), the sibling call checkSubmitAttachmentPossession
// already is: it scans every file about to be committed and refuses the
// whole write at the first response whose fulfilling handoff names an
// unresolvable data-kind deliverable. A batch containing no such response
// costs one no-op scan per file, exactly like checkSubmitAttachmentPossession
// does for a batch declaring no attachments.
func checkSubmitResponseDeliveryPossession(ctx context.Context, mirrorDir string, files []FileWrite) error {
	for _, file := range files {
		if err := checkResponseDeliveryPossession(ctx, mirrorDir, file.Content); err != nil {
			return fmt.Errorf("%s: %w", file.Path, err)
		}
	}
	return nil
}
