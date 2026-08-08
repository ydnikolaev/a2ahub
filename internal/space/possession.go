package space

// P4 (agent-exchange-2026-08) plan 04-possession, AC2: "an attachment is
// proven fetchable from the space's perspective at submit". Spec 04's
// principle, stated first: "a reference an agent cannot resolve is worse
// than no reference. Therefore: an unresolvable reference must be
// impossible to submit, not merely discouraged."
//
// This file is the submit-time liveness check. It resolves every declared
// attachments[] entry (schemas/envelope/v2/work_request.schema.json:78-114)
// on an artifact about to be committed THROUGH THE SPACE's OWN resolution
// path — ResolveDataPackage (data_resolve.go), which reads origin/main via
// contractGitResolveCommit/contractGitListTree, never the author's working
// tree — so "it works on my machine" cannot pass it by construction (plan
// "The gate"). funnel.go wires checkSubmitAttachmentPossession into
// submitPreparedRequest, after the idempotent short-circuit and before any
// commit/push git action.
//
// internal/space may not import internal/datapackage (ADR-001's table,
// recorded again at data_resolve.go's own top-of-file doc and
// conformance.go:273) — the plan's own re-anchor (Q2) confirms this rule
// does not fight the check: possession is "digest-versus-declaration only —
// a structural check over the body's own text that needs no bytes and
// therefore no new import." Attachment below is therefore a package-local
// mirror of datapackage.Attachment's two identity fields (Ref, Digest)
// rather than the imported type, exactly the way dataPackagePrefix already
// mirrors datapackage.PackagePrefix in this same file's neighbour.
import (
	"context"
	"errors"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"gopkg.in/yaml.v3"
)

var (
	// ErrAttachmentUnresolvable refuses an attachments[] entry whose ref does
	// not resolve through the space's own resolution path — the gitignored-
	// directory incident this phase exists to make impossible to submit
	// (spec 04 principle; AC2).
	ErrAttachmentUnresolvable = errors.New("space: attachment does not resolve through the space's own resolution path")

	// ErrAttachmentDigestMismatch refuses an attachments[] entry that DOES
	// resolve, but to different bytes than its declared digest — "a
	// reference that resolves to DIFFERENT bytes is worse than one that does
	// not resolve" (this brief's own instruction; spec 04 §6's "digest
	// mismatch: bytes that do not match their digest are a hard refusal on
	// both ends").
	ErrAttachmentDigestMismatch = errors.New("space: attachment resolves to bytes that do not match its declared digest")

	// ErrAttachmentDeclarationInvalid refuses a committed file whose
	// `attachments` key is present but not the schema's own shape (an array
	// of objects each carrying ref and digest) — refused before any
	// resolution is even attempted, exactly like every other malformed
	// declaration this package refuses before touching Git.
	ErrAttachmentDeclarationInvalid = errors.New("space: attachments declaration is malformed")
)

// Attachment is the minimal shape this file needs out of an
// attachments[] entry — mirrors datapackage.Attachment's Ref/Digest
// fields (internal/datapackage/attach.go) without importing that package.
// Attachment is part of the public package API.
type Attachment struct {
	Ref    string
	Digest string
}

// attachmentDeclarationWire is one attachments[] entry as it appears in an
// artifact's YAML frontmatter (work_request.schema.json:81-113's own
// property list) — only the two identity fields this check reads; role/
// conforms_to/verification/retention are none of this check's business.
type attachmentDeclarationWire struct {
	Ref    string `yaml:"ref"`
	Digest string `yaml:"digest"`
}

// envelopeAttachmentsProbe reads only the `attachments` top-level key out of
// an envelope's frontmatter — this file never decodes the rest of the
// document, which is exactly the discipline schema/corpus validation
// already owns.
type envelopeAttachmentsProbe struct {
	Attachments []attachmentDeclarationWire `yaml:"attachments"`
}

// frontmatterOrNone parses raw's frontmatter, reporting absence as a BOOL
// rather than as an error.
//
// The distinction is the point: a file with no `---` delimiters — an event,
// a manifest, a report — has nothing to declare, and that is not a failure
// to be swallowed. Returning (value, ok) says so in the signature instead of
// discarding an error the linter would rightly ask about.
func frontmatterOrNone(raw []byte) (artifact.Frontmatter, bool) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return artifact.Frontmatter{}, false
	}
	return fm, true
}

// declaredAttachments extracts the attachments[] entries declared in one
// committed file's frontmatter. A file with no frontmatter at all (an
// event/manifest/report document, none of which carry `---` delimiters) is
// not a possession violation — it simply has nothing to declare, and
// returns (nil, nil). A file WITH frontmatter but no `attachments` key, or
// an empty one, also returns (nil, nil). Only a present-but-malformed
// `attachments` key (not an array of ref/digest objects) is refused.
func declaredAttachments(raw []byte) ([]Attachment, error) {
	fm, hasFrontmatter := frontmatterOrNone(raw)
	if !hasFrontmatter {
		return nil, nil
	}
	var probe envelopeAttachmentsProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAttachmentDeclarationInvalid, err)
	}
	if len(probe.Attachments) == 0 {
		return nil, nil
	}
	out := make([]Attachment, 0, len(probe.Attachments))
	for _, entry := range probe.Attachments {
		if entry.Ref == "" || entry.Digest == "" {
			return nil, fmt.Errorf("%w: an attachments[] entry is missing ref or digest", ErrAttachmentDeclarationInvalid)
		}
		out = append(out, Attachment(entry))
	}
	return out, nil
}

// recomputeAttachmentDigest reproduces, over the payload ResolveDataPackage
// actually read from the space, the same two minting algorithms
// internal/datapackage's attach.go core uses (this file cannot import that
// package, ADR-001, so it reproduces rather than calls):
//
//   - a single payload entry: that one blob's own artifact.Digest, mirroring
//     NewAttachmentFromBytes (attach.go: "Ref is ... the SAME value as
//     Digest").
//   - more than one payload entry: the aggregate
//     artifact.CombineDigestPairs over each entry's own artifact.Digest,
//     mirroring NewAttachmentFromDirectory / BuildEntrySet's identical
//     per-file-digest-then-aggregate shape (entryset.go).
//
// A package resolved with zero payload entries (manifest.json only) can
// never match a declared digest through either branch — an attachment
// naming bytes that were never delivered is exactly the unresolvable
// reference this phase exists to refuse, not a special case to detect
// separately.
func recomputeAttachmentDigest(payload map[string][]byte) string {
	if len(payload) == 1 {
		for _, raw := range payload {
			return artifact.Digest(raw)
		}
	}
	perFile := make(map[string]string, len(payload))
	for path, raw := range payload {
		perFile[path] = artifact.Digest(raw)
	}
	return artifact.CombineDigestPairs(perFile)
}

// CheckAttachmentPossession resolves every attachment THROUGH THE SPACE's
// own resolution path — ResolveDataPackage, reading origin/main out of
// mirrorDir exactly as data delivery/fetch/verify already do — and refuses
// the whole set at the first one that either does not resolve there
// (ErrAttachmentUnresolvable) or resolves to bytes that do not match its
// declared digest (ErrAttachmentDigestMismatch). It performs no local
// filesystem read outside mirrorDir's own Git object store: there is no
// path here that could ever answer "yes" because a file happens to exist on
// the author's working tree or in some other local directory, which is the
// AC2/by-construction property this whole file exists for.
//
// An empty attachments has nothing to check and returns nil immediately,
// so an artifact declaring none behaves exactly as it does today.
// CheckAttachmentPossession is part of the public package API.
func CheckAttachmentPossession(ctx context.Context, mirrorDir string, attachments []Attachment) error {
	for _, a := range attachments {
		pkg, err := ResolveDataPackage(ctx, mirrorDir, a.Ref)
		if err != nil {
			return fmt.Errorf("%w: %q: %w", ErrAttachmentUnresolvable, a.Ref, err)
		}
		if got := recomputeAttachmentDigest(pkg.Payload); got != a.Digest {
			return fmt.Errorf("%w: attachment %q declared %s, the space resolves it to %s",
				ErrAttachmentDigestMismatch, a.Ref, a.Digest, got)
		}
	}
	return nil
}

// checkSubmitAttachmentPossession is submitPreparedRequest's own entry
// point (funnel.go): it scans every file about to be committed for a
// declared attachments[] block and, if any exist, proves the whole set
// through CheckAttachmentPossession before any git action mutates
// req.RepoDir. Files carrying no frontmatter, or frontmatter with no
// (or an empty) `attachments` key, contribute nothing — the common case,
// and the one that must cost nothing (a submitted artifact declaring NO
// attachments behaves exactly as it does today).
func checkSubmitAttachmentPossession(ctx context.Context, mirrorDir string, files []FileWrite) error {
	var attachments []Attachment
	for _, file := range files {
		declared, err := declaredAttachments(file.Content)
		if err != nil {
			return fmt.Errorf("%s: %w", file.Path, err)
		}
		attachments = append(attachments, declared...)
	}
	if len(attachments) == 0 {
		return nil
	}
	return CheckAttachmentPossession(ctx, mirrorDir, attachments)
}
