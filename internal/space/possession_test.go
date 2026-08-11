package space

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// possessionWorkRequestBody renders a minimal work_request/envelope-v2 body
// carrying one declared attachments[] entry — the schema's own required
// fields (work_request.schema.json:78-114) plus enough of the base envelope
// (id/type/schema) for artifact.ParseFrontmatter to find a frontmatter
// block. This file's checks never decode anything beyond `attachments`, so
// the rest of the body is deliberately minimal.
func possessionWorkRequestBody(ref, digest string) string {
	return "---\n" +
		"id: XW-axon-20260808-poss1\n" +
		"type: work_request\n" +
		"schema: envelope/v2\n" +
		"attachments:\n" +
		"  - ref: " + ref + "\n" +
		"    digest: " + digest + "\n" +
		"    verification: none\n" +
		"    retention: pinned\n" +
		"---\n" +
		"body\n"
}

// possessionRecomputedDigest is the test's own oracle for what
// recomputeAttachmentDigest would compute over payload — used both to
// declare a matching digest (the positive case) and, mutated by one byte,
// a non-matching one (the digest-mismatch case).
func possessionRecomputedDigest(t *testing.T, payload map[string][]byte) string {
	t.Helper()
	return recomputeAttachmentDigest(payload)
}

func TestCheckAttachmentPossessionRefusesLocalOnlyAttachment(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	packageID := "DP-axon-20260808-acx1"
	payload := map[string][]byte{"orders.json": []byte(`[{"id":1}]`)}
	digest := possessionRecomputedDigest(t, payload)

	// The gitignored-directory case, by name (spec 04 §6): the bytes —
	// manifest AND payload — exist ONLY on the local working tree, under
	// the exact directory ResolveDataPackage would look for them in, but
	// are never committed to origin/main. AC2's whole point is that this
	// must refuse. (manifest.json is written too, not just the payload, so
	// that a mutation resolving from the working tree instead of the space
	// would find a COMPLETE package there and actually flip this to a
	// pass — proving the refusal comes from "not on origin/main", not from
	// an incidentally-missing manifest.)
	writeContractCandidateFile(t, repo, dataPackageDir("axon", packageID)+"/manifest.json", `{"schema":"data-package/v1"}`)
	for name, raw := range payload {
		writeContractCandidateFile(t, repo, dataPackageDir("axon", packageID)+"/"+name, string(raw))
	}

	err := CheckAttachmentPossession(t.Context(), repo, []Attachment{{Ref: packageID, Digest: digest}})
	if !errors.Is(err, ErrAttachmentUnresolvable) {
		t.Fatalf("CheckAttachmentPossession = %v, want ErrAttachmentUnresolvable", err)
	}
}

func TestCheckAttachmentPossessionAcceptsAttachmentCommittedToTheSpace(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	packageID := "DP-axon-20260808-spc1"
	payload := map[string][]byte{"orders.json": []byte(`[{"id":1}]`)}
	digest := possessionRecomputedDigest(t, payload)

	files := map[string]string{"manifest.json": `{"schema":"data-package/v1"}`}
	for name, raw := range payload {
		files[name] = string(raw)
	}
	commitDataPackageFixture(t, repo, "axon", packageID, files)

	if err := CheckAttachmentPossession(t.Context(), repo, []Attachment{{Ref: packageID, Digest: digest}}); err != nil {
		t.Fatalf("CheckAttachmentPossession = %v, want nil", err)
	}
}

func TestCheckAttachmentPossessionRefusesDigestMismatch(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	packageID := "DP-axon-20260808-msx1"
	files := map[string]string{
		"manifest.json": `{"schema":"data-package/v1"}`,
		"orders.json":   `[{"id":1}]`,
	}
	commitDataPackageFixture(t, repo, "axon", packageID, files)

	// A digest that is well-formed but does not match the committed bytes —
	// "corrupt one byte" (spec 04 §6).
	wrongDigest := "sha256:" + strings.Repeat("0", 64)

	err := CheckAttachmentPossession(t.Context(), repo, []Attachment{{Ref: packageID, Digest: wrongDigest}})
	if !errors.Is(err, ErrAttachmentDigestMismatch) {
		t.Fatalf("CheckAttachmentPossession = %v, want ErrAttachmentDigestMismatch", err)
	}
}

// TestCheckAttachmentPossessionAcceptsBlobAttachmentCommittedToTheSpace is
// spec 10 §3 seam 5's own positive case: a `BL-` ref committed to the space
// (via DeliverBlob's own shape, commitBlobFixture — blob_resolve_test.go)
// resolves through ResolveBlob and possesses exactly like a `DP-` ref does.
func TestCheckAttachmentPossessionAcceptsBlobAttachmentCommittedToTheSpace(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	blobID := "BL-axon-20260811-psb1"
	payload := map[string][]byte{"review.md": []byte("bundle body\n")}
	commitBlobFixture(t, repo, "axon", blobID, payload)

	digest := possessionRecomputedDigest(t, payload)
	if err := CheckAttachmentPossession(t.Context(), repo, []Attachment{{Ref: blobID, Digest: digest}}); err != nil {
		t.Fatalf("CheckAttachmentPossession = %v, want nil", err)
	}
}

// TestCheckAttachmentPossessionRefusesLocalOnlyBlobAttachment is the `BL-`
// counterpart to TestCheckAttachmentPossessionRefusesLocalOnlyAttachment
// above: a COMPLETE, resolvable-looking blob (payload + its own digest
// sidecar) that exists only on the author's working tree, never committed
// to origin/main, must still refuse — proving the `BL-` dispatch branch
// reads through the space's own resolution path exactly like the `DP-`
// branch does, not the filesystem.
func TestCheckAttachmentPossessionRefusesLocalOnlyBlobAttachment(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	blobID := "BL-axon-20260811-psb2"
	payload := map[string][]byte{"review.md": []byte("local only\n")}
	digest := possessionRecomputedDigest(t, payload)

	root := blobDir("axon", blobID)
	writeContractCandidateFile(t, repo, root+"/review.md", string(payload["review.md"]))
	sidecar, err := json.Marshal(blobDigestSidecar{ID: blobID, Digest: digest})
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	writeContractCandidateFile(t, repo, blobDigestPath("axon", blobID), string(sidecar))

	err = CheckAttachmentPossession(t.Context(), repo, []Attachment{{Ref: blobID, Digest: digest}})
	if !errors.Is(err, ErrAttachmentUnresolvable) {
		t.Fatalf("CheckAttachmentPossession = %v, want ErrAttachmentUnresolvable", err)
	}
}

// TestCheckAttachmentPossessionRefusesBlobDigestMismatch is the `BL-`
// counterpart to TestCheckAttachmentPossessionRefusesDigestMismatch: the
// blob resolves (its own sidecar agrees with its own bytes), but the
// ARTIFACT's declared attachments[].digest names something else — the
// question this check answers is different from ResolveBlob's own
// self-verification (blob_resolve_test.go's
// TestResolveBlobRefusesDigestMismatch), and neither substitutes for the
// other.
func TestCheckAttachmentPossessionRefusesBlobDigestMismatch(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	blobID := "BL-axon-20260811-psb3"
	payload := map[string][]byte{"review.md": []byte("bundle body\n")}
	commitBlobFixture(t, repo, "axon", blobID, payload)

	wrongDigest := "sha256:" + strings.Repeat("0", 64)
	err := CheckAttachmentPossession(t.Context(), repo, []Attachment{{Ref: blobID, Digest: wrongDigest}})
	if !errors.Is(err, ErrAttachmentDigestMismatch) {
		t.Fatalf("CheckAttachmentPossession = %v, want ErrAttachmentDigestMismatch", err)
	}
}

func TestCheckAttachmentPossessionEmptySetIsNoop(t *testing.T) {
	t.Parallel()

	// No mirror activity at all is required to answer "nothing to check":
	// an empty mirrorDir would fail any real ResolveDataPackage call, so a
	// nil error here proves the loop body never ran.
	if err := CheckAttachmentPossession(t.Context(), "", nil); err != nil {
		t.Fatalf("CheckAttachmentPossession(nil) = %v, want nil", err)
	}
}

func TestDeclaredAttachmentsParsing(t *testing.T) {
	t.Parallel()

	t.Run("no frontmatter is not a violation", func(t *testing.T) {
		t.Parallel()
		got, err := declaredAttachments([]byte(`{"schema":"event/v2"}`))
		if err != nil || got != nil {
			t.Fatalf("declaredAttachments = %v, %v; want nil, nil", got, err)
		}
	})

	t.Run("frontmatter with no attachments key is not a violation", func(t *testing.T) {
		t.Parallel()
		got, err := declaredAttachments([]byte("---\nid: XW-axon-20260808-none1\n---\nbody\n"))
		if err != nil || got != nil {
			t.Fatalf("declaredAttachments = %v, %v; want nil, nil", got, err)
		}
	})

	t.Run("well-formed attachments parses ref and digest", func(t *testing.T) {
		t.Parallel()
		got, err := declaredAttachments([]byte(possessionWorkRequestBody("DP-axon-20260808-abcd", "sha256:"+strings.Repeat("a", 64))))
		if err != nil {
			t.Fatalf("declaredAttachments error: %v", err)
		}
		if len(got) != 1 || got[0].Ref != "DP-axon-20260808-abcd" || got[0].Digest != "sha256:"+strings.Repeat("a", 64) {
			t.Fatalf("declaredAttachments = %v", got)
		}
	})

	t.Run("malformed attachments is refused", func(t *testing.T) {
		t.Parallel()
		_, err := declaredAttachments([]byte("---\nid: XW-axon-20260808-bad1\nattachments: not-an-array\n---\nbody\n"))
		if !errors.Is(err, ErrAttachmentDeclarationInvalid) {
			t.Fatalf("declaredAttachments error = %v, want ErrAttachmentDeclarationInvalid", err)
		}
	})

	t.Run("entry missing digest is refused", func(t *testing.T) {
		t.Parallel()
		_, err := declaredAttachments([]byte("---\nid: XW-axon-20260808-bad2\nattachments:\n  - ref: DP-axon-20260808-abcd\n---\nbody\n"))
		if !errors.Is(err, ErrAttachmentDeclarationInvalid) {
			t.Fatalf("declaredAttachments error = %v, want ErrAttachmentDeclarationInvalid", err)
		}
	})
}

// TestFunnelSubmitRefusesUnresolvedAttachmentBeforeAnyGitAction is AC2's
// end-to-end proof: an artifact declaring an attachment resolvable only on
// the author's own working tree (never on the space) is refused by
// funnel.Submit itself, before any push or PR-open — no host call is made.
//
// This is also the test that catches prepared.submitRequest's own
// Files-vs-Mutations split (prepared.go:412-424 sets Mutations only): a
// check that read req.Files alone would pass every direct
// submitPreparedRequest-level test yet never run through the real
// funnel.Submit path, exactly because req.Files is nil there.
func TestFunnelSubmitRefusesUnresolvedAttachmentBeforeAnyGitAction(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	packageID := "DP-axon-20260808-e2e1"
	payload := map[string][]byte{"orders.json": []byte(`[{"id":1}]`)}
	digest := possessionRecomputedDigest(t, payload)
	// The package is COMPLETE (manifest + payload) but only on the local
	// working tree (req.RepoDir) — never pushed to origin/main. Writing a
	// full, resolvable-looking package here (rather than an arbitrary
	// nonexistent ref) is what makes "resolve from the working tree
	// instead of the space" an actual mutation of this test: with that
	// mutation, this exact fixture WOULD be found and WOULD pass.
	writeContractCandidateFile(t, req.RepoDir, dataPackageDir("axon", packageID)+"/manifest.json", `{"schema":"data-package/v1"}`)
	for name, raw := range payload {
		writeContractCandidateFile(t, req.RepoDir, dataPackageDir("axon", packageID)+"/"+name, string(raw))
	}
	req.Files = []FileWrite{
		{Path: l.Exchange(req.ArtifactID), Content: []byte(possessionWorkRequestBody(packageID, digest))},
		{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRS"), Content: []byte("event: submit\n")},
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	_, err = funnel.Submit(t.Context(), req)
	if !errors.Is(err, ErrAttachmentUnresolvable) {
		t.Fatalf("Submit error = %v, want ErrAttachmentUnresolvable", err)
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("expected zero pushes/opens, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestFunnelSubmitAcceptsAttachmentCommittedToTheSpace is AC2's positive
// case run end to end: the SAME package is delivered to origin/main first
// (mirroring what `a2a data deliver` commits — dataPackageDir/manifest.json
// + payload, data_delivery.go), and a work_request declaring it as an
// attachment then submits successfully.
func TestFunnelSubmitAcceptsAttachmentCommittedToTheSpace(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	repo := fx.Clone("axon")
	packageID := "DP-axon-20260808-e2e2"
	payload := map[string][]byte{"orders.json": []byte(`[{"id":1}]`)}
	digest := possessionRecomputedDigest(t, payload)
	files := map[string]string{"manifest.json": `{"schema":"data-package/v1"}`}
	for name, raw := range payload {
		files[name] = string(raw)
	}
	commitDataPackageFixture(t, repo, "axon", packageID, files)

	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = []FileWrite{
		{Path: l.Exchange(req.ArtifactID), Content: []byte(possessionWorkRequestBody(packageID, digest))},
		{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRS"), Content: []byte("event: submit\n")},
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := funnel.Submit(t.Context(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v", result.State, WriteStatePendingMerge)
	}
}

// TestFunnelSubmitWithNoAttachmentsIsUnaffected is this brief's own
// constraint: "a submitted artifact declaring NO attachments must behave
// exactly as it does today." Same shape as TestFunnelSingleCommit
// (funnel_test.go), asserted again here so this file's own change is
// self-certifying rather than resting on a neighbour test staying green.
func TestFunnelSubmitWithNoAttachmentsIsUnaffected(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := funnel.Submit(t.Context(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v", result.State, WriteStatePendingMerge)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected exactly 1 push + 1 open, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}
}

func TestRecomputeAttachmentDigestMatchesSingleAndAggregateShapes(t *testing.T) {
	t.Parallel()

	single := map[string][]byte{"a.json": []byte("one")}
	if got, want := recomputeAttachmentDigest(single), artifact.Digest([]byte("one")); got != want {
		t.Fatalf("single-entry digest = %q, want %q", got, want)
	}

	multi := map[string][]byte{"a.json": []byte("one"), "b.json": []byte("two")}
	perFile := map[string]string{"a.json": artifact.Digest([]byte("one")), "b.json": artifact.Digest([]byte("two"))}
	if got, want := recomputeAttachmentDigest(multi), artifact.CombineDigestPairs(perFile); got != want {
		t.Fatalf("multi-entry digest = %q, want %q", got, want)
	}
}
