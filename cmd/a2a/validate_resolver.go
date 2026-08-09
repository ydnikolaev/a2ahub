package main

// validate_resolver.go RETIRED, 2026-08-09 — P6's REF-018 fix moved from a
// per-call-site wrapper to the resolver TYPE itself.
//
// This file used to define mirrorResolverWithCriteria: a cmd/a2a-private
// wrapper around cli.MirrorResolver that type-asserted
// validate.ParentCriteriaCounter onto it, applied at only the two write
// paths wire.go's runSubmit/resolveLifecycleDepsWithPolicy constructed
// (newMirrorResolverWithCriteria). Every OTHER site that built a bare
// cli.NewMirrorResolver — contract_p6_wiring.go, work_wiring.go, and the
// merge-time path internal/cli/cmd_validate_ci.go pins — silently lost
// REF-018 counting, because criteria-counting was a property of WHO
// remembered to wrap the resolver, not of the resolver itself.
//
// The 2026-08-09 readiness audit (row 50) resolved that into a decision:
// AcceptanceCriteriaCount now lives on cli.MirrorResolver directly
// (internal/cli/adapters.go), guarded by a compile-time
// `var _ validate.ParentCriteriaCounter = (*cli.MirrorResolver)(nil)`
// assertion there. Every cli.NewMirrorResolver call site — including the
// three this file's wrapper never reached — now gets the capability by
// construction. wire.go's two former newMirrorResolverWithCriteria calls
// are now plain cli.NewMirrorResolver calls, identical to every other
// production site.
//
// findMirrorArtifactPath (this file's other former export, a second
// filepath.WalkDir keyed on a filename literally matching `<id>.md`) was
// NOT ported to internal/cli — it was eliminated. AcceptanceCriteriaCount
// resolves parentID's path from the resolver's OWN already-built index
// (ensureIndex) instead, keyed on the frontmatter `id:` field
// cache.BuildArtifactIndex already decoded, because
// internal/cli/adapters_test.go's TestMirrorResolverAdapterCarriesNoWalk
// forbids this package from ever regaining a second directory walk (AC-1.3,
// spec 01-resolver-one-home.md) — a rail cmd/a2a carries no equivalent of,
// which is exactly why the walk was safe to keep here originally but wrong
// to keep once the capability moved onto the shared resolver type. The
// index lookup is not a pure behavioural copy of the walk it replaced — see
// this wave's own Deviations report for the differences (identity by `id:`
// rather than filename, vendored/README/infrastructure exclusions,
// snapshot-at-first-use via sync.Once).
//
// This package's own gate for the fix — proving wire.go's real
// cli.NewMirrorResolver construction fires REF-018 through the real engine
// — lives in validate_resolver_test.go, unchanged in shape, updated to
// construct the resolver the same way wire.go now does.
