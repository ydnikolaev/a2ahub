package space

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

// CarriedFinding is one membership verdict, carrying its REGISTRY CODE as
// data rather than as an internal/validate.Violation.
//
// That is a dependency decision, not a style one: internal/template imports
// internal/space (ContractSidecarsFromStaging is built on Layout), and
// internal/validate's own tests import internal/template, so an
// internal/space -> internal/validate edge closes a cycle that Go refuses at
// test-build time. The codes below are POL-013 and REF-014 — existing,
// registry-backed entries with `severity: reject` and `mode_scope: both`;
// nothing is minted here, and each caller copies these four fields into the
// Violation shape its own surface already speaks.
type CarriedFinding struct {
	// Code is the schemas/errors/v1/registry.yaml entry, verbatim.
	Code string
	// Class matches internal/validate's own Class vocabulary ("policy",
	// "referential"), as a plain string for the reason above.
	Class string
	// Path is the SPACE-relative path the finding is about.
	Path string
	// Message prints the FIX, not the symptom.
	Message string
	// Severity is always "reject" for both codes; carried explicitly so a
	// caller copies the registry's answer instead of deciding one.
	Severity string
}

// CarriedClass is what ONE space-relative path IS, decided once (spec 09
// §T1, fb-20260820-d1e370). Before this type existed, four path-family
// predicates — IsDataPackageReadmePath, isDataPackagePayloadPath,
// isBlobPayloadPath and IsContractBaselinePath — answered one question
// between them, and every reader answered it again in its own idiom: the
// submit validator by "is this a contract baseline path", `validate --ci`
// by "does it end in .md". The two disagreed about
// `<system>/provides/<slug>/artifacts/CHANGELOG.md`, so `a2a submit`
// carried a declared companion into a space whose own `--ci` then refused
// it with POL-002 — the gate submit exists to pre-empt, on a file the
// scaffold wrote, the schema mandates the location of, and the role
// vocabulary blesses.
//
// The declaration this consults is neither remote nor inferred: it is the
// descriptor's own `artifacts:` inventory, carried in the same commit, and
// it is not trusted either — ContractCarriedMembership (below) cross-checks
// it against the bytes actually carried, in BOTH directions.
type CarriedClass string

const (
	// CarriedNotAContractPath is anything outside a contract's own
	// directory. Whatever already judges it keeps judging it.
	CarriedNotAContractPath CarriedClass = "not-a-contract-path"
	// CarriedArtifact is an envelope draft — including the descriptor
	// contract.md itself. Judged by the full V1/V2 artifact classes,
	// POL-002 among them.
	CarriedArtifact CarriedClass = "artifact"
	// CarriedDeclaredCompanion is a path under <system>/provides/<slug>/
	// that the descriptor's inventory declares. Judged by the carried-set
	// rules only — NEVER by the artifact frontmatter policy.
	CarriedDeclaredCompanion CarriedClass = "declared-companion"
	// CarriedUndeclaredCompanion is a path under a contract's own directory
	// that its inventory does not declare. A space must never receive a
	// file nothing classifies, so this is itself a refusal at a proposed
	// write (submit, `--ci --mode=v3-pr`) — but not over an already-merged
	// publication (ADR-011; see ContractCarriedMembership's own note).
	CarriedUndeclaredCompanion CarriedClass = "undeclared-companion"
	// CarriedUnclassifiable means the descriptor at DescriptorPath is
	// absent or unparseable. Nothing separately judges the companion: the
	// descriptor's OWN violation is the verdict, and two red lines about
	// one broken descriptor is noise whose fix is one edit.
	CarriedUnclassifiable CarriedClass = "unclassifiable"
)

// IsCompanion reports whether a class is a contract COMPANION — a file
// carried beside a descriptor that is not an envelope artifact and must
// therefore never be fed to the artifact frontmatter policy (POL-002 and
// the rest of the V1/V2 artifact classes).
//
// `unclassifiable` is a companion for this purpose and deliberately so: the
// path is under a contract's own directory, so it is certainly not an
// envelope draft; what is unknown is only WHICH companion it is, and the
// answer to that is the descriptor's own violation.
//
// This is the predicate the two submit validators branch on. Before P9 they
// branched on IsContractBaselinePath directly, which is how one half of the
// binary came to hold a different opinion from the other about
// `<system>/provides/<slug>/artifacts/CHANGELOG.md`.
func (c CarriedClass) IsCompanion() bool {
	switch c {
	case CarriedDeclaredCompanion, CarriedUndeclaredCompanion, CarriedUnclassifiable:
		return true
	case CarriedArtifact, CarriedNotAContractPath:
		return false
	default:
		return false
	}
}

// Carried is ClassifyCarried's answer for one path: the class, the contract
// that owns it, and — for a declared companion — the inventory entry that
// classified it, so a caller can report the role without a second
// descriptor read.
type Carried struct {
	Path           string
	Class          CarriedClass
	ContractID     string
	DescriptorPath string
	// Entry is the descriptor's own inventory row. Zero unless Class is
	// CarriedDeclaredCompanion AND the descriptor carries an inventory (an
	// envelope/v1 descriptor has none — see the legacy floor below).
	Entry contract.Entry
	// Rel is the contract-root-relative path (`artifacts/CHANGELOG.md`),
	// the form the descriptor's inventory and internal/contract's whole
	// carried-set domain speak. Empty for CarriedNotAContractPath.
	Rel string
}

// ClassifyCarried is THE one function that decides what a carried path is.
// Every reader consults this and only this: internal/cli/adapters.go and
// internal/mcp/adapters.go's submit validators, both of `validate --ci`'s
// discovery sites, and `validate --all`. Adding a FIFTH path-family
// predicate beside the four named above is what this exists to make
// unnecessary.
//
// It takes a space-relative path and the descriptor bytes for that path's
// contract; the CALLER owns the I/O, the same detached-value discipline
// contract.CandidateSnapshot uses — the submit surfaces read the descriptor
// out of the batch they are about to write, `--ci` reads it from the
// checkout, and neither can observe different bytes mid-evaluation.
// descriptorRaw may be nil: that is the unclassifiable case, not an error.
//
// # The legacy floor, stated so it is not discovered
//
// An envelope/v1 contract descriptor carries no `artifacts:` inventory at
// all. Where the inventory does not exist, this falls back to the PATH
// shape IsContractBaselinePath already computes — a companion is a
// companion — i.e. exactly today's behaviour, no better and no worse. The
// inventory is consulted where the grammar has one; the path shape is the
// floor where it does not. Without this, every contract authored before
// contract-set-v2 would start being refused by a rule its own grammar
// predates.
func ClassifyCarried(p string, descriptorRaw []byte) Carried {
	id, descriptorPath, ok := ContractForPath(p)
	if !ok {
		return Carried{Path: p, Class: CarriedNotAContractPath}
	}
	out := Carried{Path: p, ContractID: id, DescriptorPath: descriptorPath}
	if p == descriptorPath {
		out.Class = CarriedArtifact
		return out
	}
	out.Rel = strings.TrimPrefix(p, path.Dir(descriptorPath)+"/")

	if len(descriptorRaw) == 0 {
		out.Class = CarriedUnclassifiable
		return out
	}
	descriptor, err := contract.ParseDescriptor(descriptorRaw)
	if err != nil {
		out.Class = CarriedUnclassifiable
		return out
	}
	if len(descriptor.Artifacts) == 0 {
		// The legacy floor — see this function's own doc comment.
		out.Class = CarriedDeclaredCompanion
		return out
	}
	for _, entry := range descriptor.Artifacts {
		if entry.Path == out.Rel {
			out.Class = CarriedDeclaredCompanion
			out.Entry = entry
			return out
		}
	}
	out.Class = CarriedUndeclaredCompanion
	return out
}

// PresenceFromDir answers, for a mirror checked out at dir, whether the
// contract rooted at descriptorPath already holds rel. It is the "thing" half
// of ContractCarriedMembership's REF-014 direction.
//
// It exists because the first version of that rule compared the descriptor's
// inventory against THIS BATCH alone, and a re-publication legitimately carries
// only what changed: on 2026-08-21 `TestContractLifecyclePublishRoundTrip`
// published 1.0.0 with its scaffolded companions, merged them to main, and then
// failed publishing 1.1.0 because the unchanged companions were not re-carried.
// The refusal's own message said "no such file was found under the contract's
// own directory" — describing a disk read the code never performed. A message
// asserting a check that was not made is the defect this epic is named after,
// introduced by the epic itself, and the fix is to make the code do what the
// message already said.
func PresenceFromDir(dir string) func(descriptorPath, rel string) bool {
	if dir == "" {
		return nil
	}
	return func(descriptorPath, rel string) bool {
		if descriptorPath == "" || rel == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(dir, filepath.Dir(descriptorPath), filepath.FromSlash(rel)))
		return err == nil
	}
}

// ContractCarriedMembership judges ONE proposed write's contract paths
// against the inventories carried in the same batch, in BOTH directions:
//
//	S-3  a carried path the inventory does not declare  -> POL-013
//	S-4  an inventory entry whose file is not carried   -> REF-014
//
// Both codes already exist, are registry-backed, carry severity `reject`
// and `mode_scope: both` with stated reasons; this phase mints none. The
// class decision is ClassifyCarried's alone — this function only compares
// the two sets it produces and dresses each disagreement in the code the
// registry already assigns it.
//
// It is deliberately the WHOLE of what submit judges about a companion.
// The rest of the carried-set core (role/media/`conforms_to`/exactness/
// bounds/digest, contract.BuildCarriedSet) needs a publication context
// submit does not have — no publication event, no digest profile, no
// expected digest — and validate.ValidateContractCarriedSet's own
// proposed-mode floor check demands `event/v2` + `contract-set-v2`, which
// a first `a2a submit` cannot supply and must not fabricate. Those rules
// keep running where they already run: `a2a contract preflight`,
// `a2a contract publish`, and the merge gate. Recorded in spec 09 §11.
//
// It is a PROPOSED-WRITE rule. `--ci --mode=v3-full-repo` deliberately does
// not call it: there the path is already merged and part of an immutable
// publication, and a new refusal over already-merged content would demand
// edits to a publication ADR-011 D3/D4 makes immutable. Which files a
// caller feeds a rule is a caller decision, not a change to the rule's own
// `mode_scope`.
//
// The returned order is deterministic (code, then path) and independent of
// the batch's own file order.
// presentInSpace may be nil, and nil means UNKNOWN rather than absent: the
// REF-014 direction (a declared entry nothing carries) then does not fire at
// all, because a caller that cannot see the space cannot tell a missing file
// from an unchanged one. POL-013 (a carried file nothing declares) needs no
// space knowledge and fires either way, so a caller without a space still gets
// the half it can answer honestly.
func ContractCarriedMembership(files []FileWrite, presentInSpace func(descriptorPath, rel string) bool) []CarriedFinding {
	descriptors := batchDescriptors(files)

	// carriedRel[descriptorPath][rel] — what the batch actually carries for
	// each contract, in the contract-root-relative form the inventory
	// speaks.
	carriedRel := map[string]map[string]bool{}
	var findings []CarriedFinding
	for _, carried := range ClassifyCarriedBatch(files) {
		switch carried.Class {
		case CarriedDeclaredCompanion:
			if carriedRel[carried.DescriptorPath] == nil {
				carriedRel[carried.DescriptorPath] = map[string]bool{}
			}
			carriedRel[carried.DescriptorPath][carried.Rel] = true
		case CarriedUndeclaredCompanion:
			if carriedRel[carried.DescriptorPath] == nil {
				carriedRel[carried.DescriptorPath] = map[string]bool{}
			}
			carriedRel[carried.DescriptorPath][carried.Rel] = true
			findings = append(findings, UndeclaredCompanionFinding(carried))
		case CarriedArtifact, CarriedUnclassifiable, CarriedNotAContractPath:
			// An artifact is judged by the artifact classes; an
			// unclassifiable companion's verdict is its descriptor's own.
		}
	}

	for descriptorPath, raw := range descriptors {
		descriptor, err := contract.ParseDescriptor(raw)
		if err != nil || len(descriptor.Artifacts) == 0 {
			// Unparseable: the descriptor's own violation is the verdict.
			// No inventory (envelope/v1): nothing to compare against.
			continue
		}
		for _, entry := range descriptor.Artifacts {
			if entry.Path == "" || carriedRel[descriptorPath][entry.Path] {
				continue
			}
			if presentInSpace == nil || presentInSpace(descriptorPath, entry.Path) {
				// Already in the space (a re-publication carrying only what
				// changed), or a caller that cannot see the space. Either
				// way this entry names a file the space HAS or may have, and
				// refusing it would refuse the ordinary republish.
				continue
			}
			findings = append(findings, CarriedFinding{
				Code:     "REF-014",
				Class:    "referential",
				Path:     path.Join(path.Dir(descriptorPath), entry.Path),
				Message:  missingCompanionMessage(descriptorPath, entry),
				Severity: "reject",
			})
		}
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Code != findings[j].Code {
			return findings[i].Code < findings[j].Code
		}
		return findings[i].Path < findings[j].Path
	})
	return findings
}

// ClassifyCarriedBatch classifies every path in ONE proposed write, with
// each descriptor resolved from the batch itself — the submit-side twin of
// `--ci`'s checkout-side descriptor read, and the call both write surfaces
// make so neither grows its own copy of "find the descriptor for this
// path" (ADR-004: a shared concern moves DOWN, never ACROSS).
//
// The answers come back in the caller's own file order, one per input path.
func ClassifyCarriedBatch(files []FileWrite) []Carried {
	descriptors := batchDescriptors(files)
	out := make([]Carried, 0, len(files))
	for _, f := range files {
		var raw []byte
		if _, descriptorPath, ok := ContractForPath(f.Path); ok {
			raw = descriptors[descriptorPath]
		}
		out = append(out, ClassifyCarried(f.Path, raw))
	}
	return out
}

// batchDescriptors indexes the contract descriptors a proposed write
// carries, by their space-relative path. `a2a submit` always carries
// contract.md alongside a contract's sidecars (cmd_submit.go's buildRequest
// appends the descriptor first, then template.ContractSidecarsFromStaging's
// output), so a companion in the batch whose descriptor is NOT is a batch
// that never came from this tool — and is classified `unclassifiable`
// rather than waved through.
func batchDescriptors(files []FileWrite) map[string][]byte {
	descriptors := map[string][]byte{}
	for _, f := range files {
		if _, descriptorPath, ok := ContractForPath(f.Path); ok && f.Path == descriptorPath {
			descriptors[descriptorPath] = f.Content
		}
	}
	return descriptors
}

// UndeclaredCompanionFinding is S-3's single refusal, shared by every
// proposed-write reader — `a2a submit` on both surfaces and
// `validate --ci --mode=v3-pr` — so the local verb and the merge gate name
// the same fix in the same words. POL-013 already exists, is
// registry-backed, carries `severity: reject` and `mode_scope: both` with a
// stated reason; this phase mints nothing.
//
// It is meaningful only for a CarriedUndeclaredCompanion; any other class is
// a caller bug, and the message says so rather than fabricating a verdict.
func UndeclaredCompanionFinding(carried Carried) CarriedFinding {
	return CarriedFinding{
		Code:     "POL-013",
		Class:    "policy",
		Path:     carried.Path,
		Message:  undeclaredCompanionMessage(carried),
		Severity: "reject",
	}
}

// undeclaredCompanionMessage prints the FIX, not the symptom
// (scripts/classify-guard.sh's own house rule): the path, the descriptor it
// belongs in, and the role vocabulary as the next move — never "unknown
// file".
func undeclaredCompanionMessage(carried Carried) string {
	if carried.Class != CarriedUndeclaredCompanion {
		return fmt.Sprintf("%s is classified %q, not an undeclared companion", carried.Path, carried.Class)
	}
	return fmt.Sprintf(
		"carried file %s is not declared in %s — add it to that descriptor's `artifacts:` inventory with a role from the contract-set-v2 vocabulary (schema, valid-fixture, invalid-fixture, errors, vocabulary, limits, changelog, example, other) and its media_type, or remove the file",
		carried.Rel, carried.DescriptorPath)
}

// missingCompanionMessage is S-3's mirror: the descriptor must not name a
// file the space never receives.
func missingCompanionMessage(descriptorPath string, entry contract.Entry) string {
	return fmt.Sprintf(
		"%s declares %s (role %s) but no such file was found under the contract's own directory — stage the file at that path, or drop the entry from the `artifacts:` inventory",
		descriptorPath, entry.Path, entry.Role)
}
