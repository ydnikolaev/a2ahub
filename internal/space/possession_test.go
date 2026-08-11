package space

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/operation"
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

// TestFunnelSubmitPossessionRefusesBeforeUnreachableHostIsConsulted is
// B29's own reported symptom: an unresolvable attachment must refuse with
// ErrAttachmentUnresolvable even when the host cannot be reached at all —
// not surface FindPRByHeadBranch's own raw transport error. FindPRByHeadBranch
// IS still called once (fresh-vs-replay cannot be decided without asking,
// and unconditionally skipping the call was verified to break replay
// idempotency — see
// TestFunnelSubmitReplayOfAlreadyMergedWriteIsNotBlockedByPossession); what
// changes is that ITS failure no longer wins over a possession refusal the
// draft itself already has.
// TestFunnelSubmitDeliveryPossessionRefusesBeforeUnreachableHostIsConsulted is
// the SECOND half of B29, and it exists because the first fix was incomplete in
// a way only a reader comparing two call sites would notice.
//
// B29's subject was checkSubmitAttachmentPossession sitting behind step 0's
// FindPRByHeadBranch call, so an offline author met a transport error instead of
// the refusal that names what is wrong with their draft. That was fixed by
// running possession as a diagnostic on the network call's OWN failure.
//
// checkSubmitResponseDeliveryPossession is the identical shape one artifact
// type over: a `respond --result delivered` whose handoff deliverable the space
// cannot resolve. Leaving it behind would have made the quality of the
// diagnostic depend on WHICH kind of unresolvable reference an author happened
// to write — an attachment gets a real refusal offline, a delivery ref gets a
// dial-tcp error — which is not a distinction any author could predict.
//
// It asserts the transport error is NOT leaked, for the same reason its sibling
// does: a test that only checked for the possession sentinel would pass on a
// wrapped error carrying both.
func TestFunnelSubmitDeliveryPossessionRefusesBeforeUnreachableHostIsConsulted(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	// A handoff committed to origin/main whose data deliverable names a
	// package that was never delivered — so the handoff itself RESOLVES and
	// its deliverable does not, which is the one shape this check refuses.
	repo := req.RepoDir
	handoffID := "XH-axon-20260811-hf22"
	commitHandoffFixture(t, repo, "axon", handoffID,
		deliveryPossessionHandoffBody(handoffID, "DP-axon-20260811-gn04", "data"))

	responseID := "XS-axon-20260808-resp1"
	req.Files = []FileWrite{
		{Path: l.Exchange(responseID), Content: []byte(deliveryPossessionResponseBody("delivered", handoffID))},
		{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRU"), Content: []byte("event: submit\n")},
	}

	transportErr := errors.New("host: FindPRByHeadBranch: github is failing transiently: dial tcp: connect: connection refused")
	findCalls := 0
	fake := host.NewFakeHost()
	fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
		findCalls++
		return nil, transportErr
	}
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	_, err = funnel.Submit(t.Context(), req)
	if !errors.Is(err, ErrHandoffDeliverableUnresolvable) {
		t.Fatalf("Submit error = %v, want ErrHandoffDeliverableUnresolvable (not the host's transport error)", err)
	}
	if errors.Is(err, transportErr) {
		t.Fatalf("Submit error = %v, leaked the host transport error instead of refusing on possession", err)
	}
	if findCalls != 1 {
		t.Fatalf("FindPRByHeadBranch was called %d times, want exactly 1 — possession runs as a diagnostic on ITS failure, not by skipping the call outright", findCalls)
	}
}

func TestFunnelSubmitPossessionRefusesBeforeUnreachableHostIsConsulted(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	// Nothing was ever delivered to the space under this id — exactly the
	// bare, unresolvable ref `a2a attach` mints (B29).
	packageID := "DP-axon-20260811-off1"
	digest := "sha256:" + strings.Repeat("a", 64)
	req.Files = []FileWrite{
		{Path: l.Exchange(req.ArtifactID), Content: []byte(possessionWorkRequestBody(packageID, digest))},
		{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRT"), Content: []byte("event: submit\n")},
	}

	transportErr := errors.New("host: FindPRByHeadBranch: github is failing transiently: dial tcp: connect: connection refused")
	findCalls := 0
	fake := host.NewFakeHost()
	fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
		findCalls++
		return nil, transportErr
	}
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	_, err = funnel.Submit(t.Context(), req)
	if !errors.Is(err, ErrAttachmentUnresolvable) {
		t.Fatalf("Submit error = %v, want ErrAttachmentUnresolvable (not the host's transport error)", err)
	}
	if errors.Is(err, transportErr) {
		t.Fatalf("Submit error = %v, leaked the host transport error instead of refusing on possession", err)
	}
	if findCalls != 1 {
		t.Fatalf("FindPRByHeadBranch was called %d times, want exactly 1 — possession runs as a diagnostic on ITS failure, not by skipping the call outright", findCalls)
	}
}

// TestFunnelSubmitReplayOfAlreadyMergedWriteIsNotBlockedByPossession is
// B29's own posed edge case, decided explicitly rather than assumed: a
// draft that ALREADY merged, whose attachment has since become unresolvable
// (the space's origin/main history moved past it — e.g. a retention sweep
// removed the delivered package after the artifact referencing it landed),
// must still report WriteStateAlreadyMerged on replay.
//
// The write already durably completed — GitHub's own PR state says
// "merged" — and step 0's short-circuit exists exactly so that fact is not
// re-litigated. Possession's job is to keep an UNRESOLVABLE draft from
// reaching a NEW commit/push/open cycle; it is not a re-audit of a
// completed one run every time the caller asks "is this done yet?". Making
// an idempotent replay depend on the CURRENT resolvability of bytes the
// replay is not about to write would regress exactly the guarantee step 0
// exists for. checkSubmitAttachmentPossession therefore only gates the
// FRESH-write path (funnel.go's hoisted call runs unconditionally ahead of
// step 0, but step 0's own short-circuit — reached once existing is known —
// still returns before any second possession check, and this test proves
// that reaching WriteStateAlreadyMerged never required the removed package
// to resolve).
func TestFunnelSubmitReplayOfAlreadyMergedWriteIsNotBlockedByPossession(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	repo := fx.Clone("axon")
	packageID := "DP-axon-20260811-mrg1"
	payload := map[string][]byte{"orders.json": []byte(`[{"id":1}]`)}
	digest := possessionRecomputedDigest(t, payload)
	files := map[string]string{"manifest.json": `{"schema":"data-package/v1"}`}
	for name, raw := range payload {
		files[name] = string(raw)
	}
	commitDataPackageFixture(t, repo, "axon", packageID, files)

	req := newTestSubmitRequest(fx, "axon", l)
	req.ArtifactIDs = []string{req.ArtifactID}
	req.OperationKey = operation.Respond("axon", "agent", "bot", []string{req.ArtifactID}, "answered", nil, nil, nil)
	req.Files = []FileWrite{
		{Path: l.Exchange(req.ArtifactID), Content: []byte(possessionWorkRequestBody(packageID, digest))},
		{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRU"), Content: []byte("event: submit\n")},
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	first, err := funnel.Submit(t.Context(), req)
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	if _, err := fake.MergePR(t.Context(), host.MergePRRequest{Repo: req.Repo, PRNumber: first.PRNumber}); err != nil {
		t.Fatalf("MergePR: %v", err)
	}

	// The space's own history moves on, from a SEPARATE clone (a retention
	// sweep, not this test's Submit path): the delivered package is removed
	// from origin/main by a later commit, and req.RepoDir fetches that new
	// state — exactly what an author's mirror would see after `a2a sync`.
	parent := t.TempDir()
	removalClone := filepath.Join(parent, "removal")
	contractTestGit(t, parent, "clone", "-q", fx.RemoteURL(), removalClone)
	contractTestGit(t, removalClone, "rm", "-r", "-q", "--", dataPackageDir("axon", packageID))
	contractTestGit(t, removalClone, "commit", "-q", "-m", "retention: remove "+packageID)
	contractTestGit(t, removalClone, "push", "-q", "origin", "main")
	contractTestGit(t, req.RepoDir, "fetch", "-q", "origin", "main")

	retry := req
	result, err := funnel.Submit(t.Context(), retry)
	if err != nil {
		t.Fatalf("replay Submit: %v", err)
	}
	if result.State != WriteStateAlreadyMerged {
		t.Fatalf("replay State = %v, want %v (idempotent report of a completed write must not depend on the CURRENT resolvability of possession bytes)", result.State, WriteStateAlreadyMerged)
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
