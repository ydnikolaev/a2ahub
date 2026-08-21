package space

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// deliveryPossessionHandoffBody renders a minimal handoff body carrying one
// deliverables[] entry — enough for artifact.ParseFrontmatter to find a
// frontmatter block and for handoffDeliverablesProbe to decode it. kind ==
// "" omits the deliverables key entirely (the "handoff with no deliverables
// at all" shape).
func deliveryPossessionHandoffBody(handoffID, deliverableRef, kind string) string {
	body := "---\n" +
		"id: " + handoffID + "\n" +
		"type: handoff\n"
	if kind != "" {
		body += "deliverables:\n" +
			"  - name: sample\n" +
			"    ref: " + deliverableRef + "\n" +
			"    kind: " + kind + "\n"
	}
	return body + "---\nbody\n"
}

// deliveryPossessionResponseBody renders a minimal response body — `result`
// plus, when refID is non-empty, a single refs[] entry naming it. Empty
// refID omits `refs` entirely (the ordinary plain-answer shape).
func deliveryPossessionResponseBody(result, refID string) string {
	body := "---\n" +
		"id: XS-axon-20260808-resp1\n" +
		"type: response\n" +
		"result: " + result + "\n"
	if refID != "" {
		body += "refs:\n  - {ref: " + refID + "}\n"
	}
	return body + "---\nbody\n"
}

// commitHandoffFixture commits body at layout.Exchange(handoffID) to
// origin/main — the same commit/push shape commitDataPackageFixture
// (data_resolve_test.go) already uses for a data package's own manifest.
func commitHandoffFixture(t *testing.T, repo, system, handoffID, body string) {
	t.Helper()
	layout, err := NewLayout(system)
	if err != nil {
		t.Fatal(err)
	}
	path := layout.Exchange(handoffID)
	writeContractCandidateFile(t, repo, path, body)
	contractTestGit(t, repo, "add", "--", path)
	contractTestGit(t, repo, "commit", "-q", "-m", "handoff "+handoffID)
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")
}

func TestCheckResponseDeliveryPossession(t *testing.T) {
	t.Parallel()

	t.Run("no frontmatter is not a violation", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		if err := checkResponseDeliveryPossession(t.Context(), repo, []byte("no frontmatter here")); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	// This is the "made unconditional" mutation guard from the brief: a
	// plainly-answered `result: delivered` response with no refs[] at all
	// is the P11 catalogue's own six-call-site shape (spec 06 §11's
	// 2026-08-10 amendment) and must cost nothing.
	t.Run("delivered with no refs at all is not a violation", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		raw := []byte(deliveryPossessionResponseBody("delivered", ""))
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	t.Run("result other than delivered is not a violation, even naming an unresolvable handoff", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		handoffID := "XH-axon-20260808-h004"
		commitHandoffFixture(t, repo, "axon", handoffID, deliveryPossessionHandoffBody(handoffID, "DP-axon-20260808-gh02", "data"))
		raw := []byte(deliveryPossessionResponseBody("answered", handoffID))
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	t.Run("a non-response type carrying result:delivered and refs is not a violation", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		handoffID := "XH-axon-20260808-h005"
		commitHandoffFixture(t, repo, "axon", handoffID, deliveryPossessionHandoffBody(handoffID, "DP-axon-20260808-gh03", "data"))
		raw := []byte("---\nid: XW-axon-20260808-notresp\ntype: work_request\nresult: delivered\nrefs:\n  - {ref: " + handoffID + "}\n---\nbody\n")
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	t.Run("a ref that does not resolve to any handoff at all is out of scope (REF-017's territory)", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		raw := []byte(deliveryPossessionResponseBody("delivered", "XH-axon-20260808-ghs1"))
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	t.Run("a ref naming a non-handoff artifact is out of scope", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		raw := []byte(deliveryPossessionResponseBody("delivered", "XC-axon-somecontract"))
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	t.Run("a resolving handoff with no deliverables at all is not a violation", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		handoffID := "XH-axon-20260808-h006"
		commitHandoffFixture(t, repo, "axon", handoffID, deliveryPossessionHandoffBody(handoffID, "", ""))
		raw := []byte(deliveryPossessionResponseBody("delivered", handoffID))
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	t.Run("a resolving handoff with only a non-data deliverable is not a violation", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		handoffID := "XH-axon-20260808-h001"
		commitHandoffFixture(t, repo, "axon", handoffID, deliveryPossessionHandoffBody(handoffID, "some/code/path", "code"))
		raw := []byte(deliveryPossessionResponseBody("delivered", handoffID))
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	// The positive accept case: the data-kind deliverable's package IS
	// committed to origin/main, so ResolveDataPackage finds it.
	t.Run("a resolving handoff whose data deliverable resolves is not a violation", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		packageID := "DP-axon-20260808-pkg1"
		commitDataPackageFixture(t, repo, "axon", packageID, map[string]string{"manifest.json": `{"schema":"data-package/v1"}`})
		handoffID := "XH-axon-20260808-h002"
		commitHandoffFixture(t, repo, "axon", handoffID, deliveryPossessionHandoffBody(handoffID, packageID, "data"))
		raw := []byte(deliveryPossessionResponseBody("delivered", handoffID))
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	// THE positive refusal case (mutation discipline: this must red if
	// ErrHandoffDeliverableUnresolvable's branch is ever removed).
	t.Run("a resolving handoff whose data deliverable does not resolve is refused", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		handoffID := "XH-axon-20260808-h003"
		commitHandoffFixture(t, repo, "axon", handoffID, deliveryPossessionHandoffBody(handoffID, "DP-axon-20260808-gh04", "data"))
		raw := []byte(deliveryPossessionResponseBody("delivered", handoffID))
		err := checkResponseDeliveryPossession(t.Context(), repo, raw)
		if !errors.Is(err, ErrHandoffDeliverableUnresolvable) {
			t.Fatalf("checkResponseDeliveryPossession = %v, want ErrHandoffDeliverableUnresolvable", err)
		}
	})
}

func TestResolveHandoffArtifact(t *testing.T) {
	t.Parallel()

	t.Run("cancelled context surfaces as an error", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, ok, err := resolveHandoffArtifact(ctx, repo, "XH-axon-20260808-h001")
		if err == nil || ok {
			t.Fatalf("resolveHandoffArtifact = ok=%v err=%v, want ok=false and a context error", ok, err)
		}
	})

	t.Run("a non-XH id resolves to nothing, not an error", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		_, ok, err := resolveHandoffArtifact(t.Context(), repo, "XC-axon-somecontract")
		if err != nil || ok {
			t.Fatalf("resolveHandoffArtifact = ok=%v err=%v, want ok=false err=nil", ok, err)
		}
	})

	t.Run("an uncommitted handoff resolves to nothing, not an error", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		_, ok, err := resolveHandoffArtifact(t.Context(), repo, "XH-axon-20260808-ghs1")
		if err != nil || ok {
			t.Fatalf("resolveHandoffArtifact = ok=%v err=%v, want ok=false err=nil", ok, err)
		}
	})

	t.Run("a committed handoff resolves to its own committed bytes", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		handoffID := "XH-axon-20260808-h009"
		body := deliveryPossessionHandoffBody(handoffID, "DP-axon-20260808-x", "data")
		commitHandoffFixture(t, repo, "axon", handoffID, body)
		raw, ok, err := resolveHandoffArtifact(t.Context(), repo, handoffID)
		if err != nil || !ok {
			t.Fatalf("resolveHandoffArtifact = ok=%v err=%v, want ok=true err=nil", ok, err)
		}
		if string(raw) != body {
			t.Fatalf("raw = %q, want %q", raw, body)
		}
	})
}

// TestFunnelSubmitRefusesUnresolvedHandoffDeliverableBeforeAnyGitAction is
// AC9's submit-side proof, end to end: a response declaring `result:
// delivered` and referencing a handoff whose data-kind deliverable has no
// resolvable package is refused by funnel.Submit itself, before any push or
// PR-open — the same shape TestFunnelSubmitRefusesUnresolvedAttachmentBeforeAnyGitAction
// (possession_test.go) already proves for attachments[].
func TestFunnelSubmitRefusesUnresolvedHandoffDeliverableBeforeAnyGitAction(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	repo := fx.Clone("axon")
	handoffID := "XH-axon-20260808-e2e1"
	commitHandoffFixture(t, repo, "axon", handoffID, deliveryPossessionHandoffBody(handoffID, "DP-axon-20260808-msg1", "data"))

	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = []FileWrite{
		{Path: l.Exchange(req.ArtifactID), Content: []byte(deliveryPossessionResponseBody("delivered", handoffID))},
		{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRS"), Content: []byte("event: submit\n")},
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	_, err = funnel.Submit(t.Context(), req)
	if !errors.Is(err, ErrHandoffDeliverableUnresolvable) {
		t.Fatalf("Submit error = %v, want ErrHandoffDeliverableUnresolvable", err)
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("expected zero pushes/opens, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestFunnelSubmitAcceptsResolvedHandoffDeliverable is the positive case run
// end to end: the referenced handoff's data-kind deliverable package IS
// committed to origin/main, so submission succeeds.
func TestFunnelSubmitAcceptsResolvedHandoffDeliverable(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	repo := fx.Clone("axon")
	packageID := "DP-axon-20260808-e2pk"
	commitDataPackageFixture(t, repo, "axon", packageID, map[string]string{"manifest.json": `{"schema":"data-package/v1"}`})
	handoffID := "XH-axon-20260808-e2e2"
	commitHandoffFixture(t, repo, "axon", handoffID, deliveryPossessionHandoffBody(handoffID, packageID, "data"))

	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = []FileWrite{
		{Path: l.Exchange(req.ArtifactID), Content: []byte(deliveryPossessionResponseBody("delivered", handoffID))},
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

// TestFunnelSubmitPlainDeliveredWithNoRefsIsUnaffected is the "made
// unconditional" mutation guard, run through the real funnel: an ordinary
// plainly-answered `result: delivered` work_request response — the P11
// catalogue's own six-call-site shape (spec 06 §11's 2026-08-10 amendment)
// — carries no refs[] at all and must submit exactly as it does today. If
// this check were ever widened to fire on `result: delivered` alone, this
// test reds.
func TestFunnelSubmitPlainDeliveredWithNoRefsIsUnaffected(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = []FileWrite{
		{Path: l.Exchange(req.ArtifactID), Content: []byte(deliveryPossessionResponseBody("delivered", ""))},
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
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected exactly 1 push + 1 open, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}
}

// deliveryPossessionDeliversBody renders a minimal response body carrying a
// `delivers[]` array — judge-the-thing-2026-08 P1's own wire (envelope/v2
// response.schema.json). No packageIDs at all omits the key entirely, which
// is the ordinary answer shape deliveryPossessionResponseBody already
// renders and must stay indistinguishable from it.
func deliveryPossessionDeliversBody(result string, packageIDs ...string) string {
	body := "---\n" +
		"id: XS-axon-20260821-resp2\n" +
		"type: response\n" +
		"result: " + result + "\n"
	if len(packageIDs) > 0 {
		body += "delivers:\n"
		for _, id := range packageIDs {
			body += "  - " + id + "\n"
		}
	}
	return body + "---\nbody\n"
}

// TestCheckResponseDeliversPossession is P1's own half: the response NAMES
// the package it announces, so the SAME origin/main resolution that made
// atomicity impossible becomes the check — an unmerged payload PR means no
// package on main means the response is refused.
func TestCheckResponseDeliversPossession(t *testing.T) {
	t.Parallel()

	// THE incident (fb-20260808-d5740f): the payload PR has not merged, so
	// the named package is not on origin/main, and the response that
	// announces it must not land.
	t.Run("delivers naming a package absent from origin main is refused, naming the package", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		packageID := "DP-axon-20260821-dpk1"
		raw := []byte(deliveryPossessionDeliversBody("delivered", packageID))
		err := checkResponseDeliveryPossession(t.Context(), repo, raw)
		if !errors.Is(err, ErrResponseDeliversUnlanded) {
			t.Fatalf("checkResponseDeliveryPossession = %v, want ErrResponseDeliversUnlanded", err)
		}
		if !strings.Contains(err.Error(), packageID) {
			t.Fatalf("refusal %q does not name the package %q", err.Error(), packageID)
		}
		if !strings.Contains(err.Error(), "REF-024") {
			t.Fatalf("refusal %q does not name its registry code REF-024", err.Error())
		}
	})

	// The golden path: by the time the reference flow responds, the payload
	// is already merged, so the announcement resolves and costs nothing.
	t.Run("delivers naming a committed package is not a violation", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		packageID := "DP-axon-20260821-dpk2"
		commitDataPackageFixture(t, repo, "axon", packageID, map[string]string{"manifest.json": `{"schema":"data-package/v1"}`})
		raw := []byte(deliveryPossessionDeliversBody("delivered", packageID))
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	// The oracle (§8 criterion 2), at unit tier: `delivered` is the ordinary
	// result word for a plainly-answered work_request and the five/six
	// declared catalogue paths use it with no package anywhere near them.
	// A response that names NO package is exactly as unremarkable as it was
	// before this field existed.
	t.Run("delivers absent is untouched, even at result delivered", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		raw := []byte(deliveryPossessionDeliversBody("delivered"))
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})

	// The trigger is the FIELD's presence, not the result word: an author
	// who names a package they do not hold is just as unactionable at
	// `partial` as at `delivered`, and nothing plain-answered carries the
	// field at all.
	t.Run("delivers naming an absent package is refused at any result", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		raw := []byte(deliveryPossessionDeliversBody("partial", "DP-axon-20260821-dpk3"))
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); !errors.Is(err, ErrResponseDeliversUnlanded) {
			t.Fatalf("checkResponseDeliveryPossession = %v, want ErrResponseDeliversUnlanded", err)
		}
	})

	// §6's malformed-id edge case: ResolveDataPackage is the ONE authority
	// on "is this package here" (spec §5), so a malformed id refuses through
	// the same seat rather than through a second parser.
	t.Run("a malformed delivers id is refused through the same seat", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		raw := []byte(deliveryPossessionDeliversBody("delivered", "not-a-package-id"))
		err := checkResponseDeliveryPossession(t.Context(), repo, raw)
		if !errors.Is(err, ErrResponseDeliversUnlanded) {
			t.Fatalf("checkResponseDeliveryPossession = %v, want ErrResponseDeliversUnlanded", err)
		}
		if !errors.Is(err, ErrDataPackageInvalidReference) {
			t.Fatalf("refusal does not carry ErrDataPackageInvalidReference: %v", err)
		}
	})

	// A non-response artifact carrying the same key is not this check's
	// business — the same narrowness the refs[] half already holds.
	t.Run("a non-response type carrying delivers is not a violation", func(t *testing.T) {
		t.Parallel()
		repo := newContractHistoryRepo(t)
		raw := []byte("---\nid: XW-axon-20260821-notresp\ntype: work_request\nresult: delivered\ndelivers:\n  - DP-axon-20260821-dpk4\n---\nbody\n")
		if err := checkResponseDeliveryPossession(t.Context(), repo, raw); err != nil {
			t.Fatalf("checkResponseDeliveryPossession = %v, want nil", err)
		}
	})
}

// TestFunnelSubmitRefusesUnlandedDeliversBeforeAnyGitAction is the incident
// itself, run through the real funnel: the payload PR has not merged, the
// response announces the package anyway, and the write is refused before any
// commit/push git action (zero pushes, zero opens) — the second half of
// "a data delivery is one submit, the response is a second, independent PR".
func TestFunnelSubmitRefusesUnlandedDeliversBeforeAnyGitAction(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = []FileWrite{
		{Path: l.Exchange(req.ArtifactID), Content: []byte(deliveryPossessionDeliversBody("delivered", "DP-axon-20260821-fnp1"))},
		{Path: l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRS"), Content: []byte("event: submit\n")},
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	_, err = funnel.Submit(t.Context(), req)
	if !errors.Is(err, ErrResponseDeliversUnlanded) {
		t.Fatalf("Submit error = %v, want ErrResponseDeliversUnlanded", err)
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("expected zero pushes/opens, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestFunnelSubmitAcceptsLandedDelivers is §8 criterion 3: with the package
// already merged — which is what the shipped reference flow always has by
// the time it responds — the response submits exactly as it does today.
func TestFunnelSubmitAcceptsLandedDelivers(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	repo := fx.Clone("axon")
	packageID := "DP-axon-20260821-fnp2"
	commitDataPackageFixture(t, repo, "axon", packageID, map[string]string{"manifest.json": `{"schema":"data-package/v1"}`})

	req := newTestSubmitRequest(fx, "axon", l)
	req.Files = []FileWrite{
		{Path: l.Exchange(req.ArtifactID), Content: []byte(deliveryPossessionDeliversBody("delivered", packageID))},
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
