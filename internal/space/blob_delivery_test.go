package space

import (
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func newTestBlobWriteRequest(fx *spacefixture.Fixture, system, blobID string, payload map[string][]byte) BlobWriteRequest {
	return BlobWriteRequest{
		System:  system,
		BlobID:  blobID,
		Payload: payload,
		SubmitTemplate: SubmitRequest{
			RepoDir:           fx.Clone(system),
			CommitMessage:     "a2a(attach): " + blobID,
			CommitAuthorName:  "a2a-" + system,
			CommitAuthorEmail: "a2a-" + system + "@a2ahub.invalid",
			RemoteURL:         fx.RemoteURL(),
			Repo:              host.Repo{Owner: "acme", Name: "getvisa"},
			BaseBranch:        "main",
			PRTitle:           "Attach " + blobID,
			MinBinaryVersion:  "0.1.0",
		},
	}
}

// TestDeliverBlobSingleCommit is spec 10 §3 seam 2's own proof, mirroring
// TestDeliverDataPackageSingleCommit (data_delivery_test.go): the payload
// and its own digest sidecar land in exactly ONE commit, at the exact path
// blobDir/blobDigestPath name — the only reason ResolveBlob can find either
// one back.
func TestDeliverBlobSingleCommit(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	blobID := "BL-axon-20260811-ab12"
	payload := map[string][]byte{"review.md": []byte("bundle body\n")}
	req := newTestBlobWriteRequest(fx, "axon", blobID, payload)

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := DeliverBlob(t.Context(), req, funnel)
	if err != nil {
		t.Fatalf("DeliverBlob: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v", result.State, WriteStatePendingMerge)
	}
	wantBranch := BranchName("axon", "attach", blobDeliverOperationKey(blobID))
	if result.Branch != wantBranch {
		t.Fatalf("Branch = %q, want %q", result.Branch, wantBranch)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected exactly 1 push + 1 open, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}

	changed, err := runGitOutput(t.Context(), req.SubmitTemplate.RepoDir, nil, "diff", "--name-only", "main", result.Branch)
	if err != nil {
		t.Fatalf("diff --name-only: %v", err)
	}
	want := map[string]bool{
		"axon/blobs/" + blobID + "/review.md": true,
		"axon/blobs/" + blobID + "/blob.json": true,
	}
	got := map[string]bool{}
	for _, line := range splitLinesForTest(changed) {
		got[line] = true
	}
	if len(got) != len(want) {
		t.Fatalf("changed files = %v, want exactly %v", got, want)
	}
	for path := range want {
		if !got[path] {
			t.Fatalf("changed files %v missing %q", got, path)
		}
	}
}

// TestDeliverBlobIdempotentRerunRepairsSamePR mirrors
// TestDeliverDataPackageIdempotentRerunRepairsSamePR: OperationKey is a pure
// function of BlobID alone, so a re-run against the same staged bytes
// repairs the same PR rather than opening a second one.
func TestDeliverBlobIdempotentRerunRepairsSamePR(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	blobID := "BL-axon-20260811-cd34"
	payload := map[string][]byte{"review.md": []byte("bundle body\n")}
	req := newTestBlobWriteRequest(fx, "axon", blobID, payload)

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	first, err := DeliverBlob(t.Context(), req, funnel)
	if err != nil {
		t.Fatalf("first DeliverBlob: %v", err)
	}

	second, err := DeliverBlob(t.Context(), req, funnel)
	if err != nil {
		t.Fatalf("second (re-run) DeliverBlob: %v", err)
	}
	if second.State != WriteStateAlreadyOpen {
		t.Fatalf("second State = %v, want %v", second.State, WriteStateAlreadyOpen)
	}
	if second.PRNumber != first.PRNumber || second.Branch != first.Branch {
		t.Fatalf("second = %+v, want same branch/PR as first %+v", second, first)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected NO second push/open cycle (no second PR), got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestDeliverBlobRefusesMissingFields mirrors ErrDataDeliveryInvalid's own
// "every field is required" test shape.
func TestDeliverBlobRefusesMissingFields(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")

	base := newTestBlobWriteRequest(fx, "axon", "BL-axon-20260811-ef56", map[string][]byte{"a": []byte("x")})

	missingSystem := base
	missingSystem.System = ""
	if _, err := DeliverBlob(t.Context(), missingSystem, funnel); !errors.Is(err, ErrBlobDeliveryInvalid) {
		t.Fatalf("missing System error = %v, want ErrBlobDeliveryInvalid", err)
	}

	missingID := base
	missingID.BlobID = ""
	if _, err := DeliverBlob(t.Context(), missingID, funnel); !errors.Is(err, ErrBlobDeliveryInvalid) {
		t.Fatalf("missing BlobID error = %v, want ErrBlobDeliveryInvalid", err)
	}

	emptyPayload := base
	emptyPayload.Payload = nil
	if _, err := DeliverBlob(t.Context(), emptyPayload, funnel); !errors.Is(err, ErrBlobDeliveryInvalid) {
		t.Fatalf("empty Payload error = %v, want ErrBlobDeliveryInvalid", err)
	}

	if _, err := DeliverBlob(t.Context(), base, nil); !errors.Is(err, ErrBlobDeliveryInvalid) {
		t.Fatalf("nil funnel error = %v, want ErrBlobDeliveryInvalid", err)
	}
}

// TestDeliverBlobRefusesReservedSidecarName proves a payload entry that
// collides with blobDigestPath's own reserved name is refused rather than
// silently overwriting the digest record DeliverBlob is about to write
// beside it.
func TestDeliverBlobRefusesReservedSidecarName(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := newTestBlobWriteRequest(fx, "axon", "BL-axon-20260811-gh78", map[string][]byte{"blob.json": []byte(`{"forged":true}`)})
	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")

	if _, err := DeliverBlob(t.Context(), req, funnel); !errors.Is(err, ErrBlobDeliveryInvalid) {
		t.Fatalf("DeliverBlob(reserved sidecar name) = %v, want ErrBlobDeliveryInvalid", err)
	}
}

// TestDeliverBlobRefusesTooManyEntries is spec 10 AC4's write-side proof for
// the entry-count ceiling, driven with the REAL named constant
// (maxBlobEntries-1 is the payload budget — see its own doc comment) rather
// than a substituted bound: building a map of that many 1-byte entries and
// counting them costs nothing (in-memory, pre-funnel — funnel is nil here
// because this refusal must fire before the funnel is ever consulted).
func TestDeliverBlobRefusesTooManyEntries(t *testing.T) {
	t.Parallel()

	payload := make(map[string][]byte, maxBlobEntries)
	for i := 0; i < maxBlobEntries; i++ { // one MORE than the payload budget (maxBlobEntries-1)
		payload[blobEntryNameForTest(i)] = []byte("x")
	}
	req := BlobWriteRequest{System: "axon", BlobID: "BL-axon-20260811-jk90", Payload: payload}

	// A non-nil funnel that must never actually be consulted: the ceiling
	// refusal runs entirely locally, before the funnel is ever touched
	// (DeliverBlob's own doc comment) — a nil RepoDir/RemoteURL funnel call
	// would itself fail loudly if this ever regressed to check the ceiling
	// AFTER attempting a git action.
	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")

	_, err := DeliverBlob(t.Context(), req, funnel)
	if !errors.Is(err, ErrBlobTooManyEntries) {
		t.Fatalf("DeliverBlob(too many entries) = %v, want ErrBlobTooManyEntries", err)
	}
}

func blobEntryNameForTest(i int) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	if i < len(alphabet) {
		return string(alphabet[i])
	}
	return string(alphabet[i%len(alphabet)]) + blobEntryNameForTest(i/len(alphabet))
}

func TestCheckBlobPayloadCeilings(t *testing.T) {
	t.Parallel()

	bounds := blobCeilingBounds{maxEntries: 2, maxEntryBytes: 5, maxTotalBytes: 8}

	t.Run("within every bound is accepted", func(t *testing.T) {
		t.Parallel()
		payload := map[string][]byte{"a": []byte("abcd"), "b": []byte("ab")}
		if err := checkBlobPayloadCeilings(payload, bounds); err != nil {
			t.Fatalf("checkBlobPayloadCeilings = %v, want nil", err)
		}
	})

	t.Run("entry count over the bound is refused", func(t *testing.T) {
		t.Parallel()
		payload := map[string][]byte{"a": []byte("x"), "b": []byte("x"), "c": []byte("x")}
		if err := checkBlobPayloadCeilings(payload, bounds); !errors.Is(err, ErrBlobTooManyEntries) {
			t.Fatalf("checkBlobPayloadCeilings(3 entries, max 2) = %v, want ErrBlobTooManyEntries", err)
		}
	})

	t.Run("one entry over its own bound is refused, naming that entry", func(t *testing.T) {
		t.Parallel()
		payload := map[string][]byte{"a": []byte("abcdef")} // 6 bytes > maxEntryBytes(5)
		err := checkBlobPayloadCeilings(payload, bounds)
		if !errors.Is(err, ErrBlobEntryTooLarge) {
			t.Fatalf("checkBlobPayloadCeilings(oversized entry) = %v, want ErrBlobEntryTooLarge", err)
		}
	})

	t.Run("aggregate over the bound is refused even though no single entry is", func(t *testing.T) {
		t.Parallel()
		payload := map[string][]byte{"a": []byte("abcde"), "b": []byte("abcd")} // 5+4=9 > 8, neither alone > 5
		err := checkBlobPayloadCeilings(payload, bounds)
		if !errors.Is(err, ErrBlobTooLarge) {
			t.Fatalf("checkBlobPayloadCeilings(aggregate over) = %v, want ErrBlobTooLarge", err)
		}
	})
}

// TestDefaultBlobCeilingBoundsMatchTheNamedConstants is the wiring proof
// TestCheckBlobPayloadCeilings' tiny-bound tests rely on: DeliverBlob and
// ResolveBlob call checkBlobPayloadCeilings with defaultBlobCeilingBounds,
// and this asserts THAT value is exactly the three named, documented
// ceilings — not a drifted copy.
func TestDefaultBlobCeilingBoundsMatchTheNamedConstants(t *testing.T) {
	t.Parallel()

	want := blobCeilingBounds{
		maxEntries:    maxBlobEntries - 1,
		maxEntryBytes: maxBlobEntryBytes,
		maxTotalBytes: maxBlobPackageBytes,
	}
	if defaultBlobCeilingBounds != want {
		t.Fatalf("defaultBlobCeilingBounds = %+v, want %+v", defaultBlobCeilingBounds, want)
	}
}
