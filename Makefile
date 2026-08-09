# ─────────────────────────────────────────────────────────────────────
# Root Makefile — repo-level tooling (make-ABI)
# ─────────────────────────────────────────────────────────────────────
#
# make check             THE CEILING — one outer runner owns cache/artifact,
#                         repo gates, then Go gates (gofmt/vet/lint/test).
# make lane              PRINT the lane this diff can reach — every phase whose
#                         declared inputs intersect it, with the reason it was
#                         selected and its measured median. Derived, never
#                         looked up in a table (spec 12).
# make lane-run          RUN that lane. NOT the ceiling: `make check` stays the
#                         release gate, and this makes the cheap lane CORRECT,
#                         not the expensive one optional.
# make check-validators  THE STATIC LANE — repo gates only, no tests. The inner
#                         loop when the diff is docs/scripts and no Go changed.
# make classify-guard    publish-boundary gate: no private (harness) path tracked.
# make workflow-lint     every GitHub Action `uses:` is SHA-pinned (product gate).
# make readme-lint       README stays compact, current, and linked to deeper docs.
# make feature-lint      docs/features/<slug>/ conforms to the canonical template
#                        (private harness gate — skips cleanly if absent).
# make epic-drift        an epic's committed docs match its reality
#                        (private harness gate — skips cleanly if absent).
# make operational-confidence-guard
#                        P0 stable IDs/history/DAG/v1-bytes/package-boundary ratchet.
# make skill-citations   every `a2a <verb>` / error code the shipped skill PROSE
#                        cites must exist (private harness gate — skips if absent).
# make harness-check     both harness gates' --teeth self-tests (the gates bite).
# make coverage          go test -race with the coveragepolicy SSOT floor (also run by `check`).
# make vulncheck         govulncheck ./... gated by .govulncheck-allow.txt (network; not in `check`).
# make live-e2e          THE LIVE TIER — the real binary against a real throwaway
#                         GitHub space (spec 36). Network + two credentials +
#                         immutable public candidate SHA + Actions latency:
#                         NEVER in `check`, never a merge gate.
# make logic-e2e          THE LOGIC LANE inner loop — TestLogicMatrix and its
#                         three siblings (spec 09), local bare git repo + an
#                         in-process host stand-in, no credentials, no
#                         network, no candidate. `make check` already reaches
#                         these same entry points; this target is the fast
#                         narrower path for iterating on one scenario.
# make install           put a dev `a2a` on your PATH that always runs THIS source tree.
#
# Recipes are POSIX sh — no bashisms — even though the gate scripts they call
# are bash (invoked explicitly via `bash`, never relying on $(SHELL)).
#
# `check` also vets the `livee2e`-TAGGED tree, which `go vet ./...` does not
# see: the live tier's scenarios and its runner sit behind //go:build livee2e,
# so a type error there compiles nowhere in `check` and surfaces only when
# somebody starts a 2-hour live run. Type-checking it costs a second. Note
# what this is NOT: vet type-checks the tagged tree, it does not RUN it —
# `make live-e2e` is still the only thing that touches a real GitHub space.

.PHONY: check test check-validators lane lane-run lane-declarations web-quality _print-repo-gates dashboard-template-drift feature-lint epic-drift operational-confidence-guard event-writer-receipts contract-carried-set work-checkpoint-schema operational-projection-single-source localserver-readonly-routes skill-citations view-vocabulary pendency-uniqueness loop-coverage release-notes-freshness roadmap-release-decisions provider-tier-deferral space-template-baseline space-template-baseline-check readme-lint classify-guard workflow-lint gosec-scope harness-check _harness-check coverage vulncheck release-preflight live-e2e live-e2e-evidence logic-e2e install

# ONE list, consumed by both `check` (the ceiling) and `check-validators` (the
# static lane). Two hand-kept copies of a gate list drift, and the drift is
# invisible: a copy quietly stops running a gate while still printing green.
#
# classify-guard + workflow-lint are PRODUCT gates (always run, committed public).
# feature-lint/epic-drift are PRIVATE harness gates: their scripts live under
# the mate-managed harness (scripts/check-feature-lint.sh, .agents/scripts/
# epic_docs_drift.sh) and are absent on a public checkout — each target below
# presence-gates itself so `make check` never hard-fails on their absence.
REPO_GATES := lane-declarations classify-guard workflow-lint gosec-scope readme-lint dashboard-template-drift feature-lint epic-drift operational-confidence-guard event-writer-receipts contract-carried-set work-checkpoint-schema operational-projection-single-source localserver-readonly-routes skill-citations view-vocabulary pendency-uniqueness loop-coverage release-notes-freshness roadmap-release-decisions provider-tier-deferral space-template-baseline-check

_print-repo-gates:
	@echo "$(REPO_GATES)"

check-validators: ## Repo gates only; one shared CLI build feeds binary-backed static gates.
	@bash scripts/verify.sh validators

lane: ## PRINT the lane this working tree's changes can actually reach, with each phase's reason and measured median. Optional: LANE_FILES="a b c".
	@bash scripts/verify.sh lane

lane-run: ## RUN that derived lane. NOT the ceiling — a release still runs `make check` (spec 12 J5). Optional: LANE_FILES="a b c".
	@bash scripts/verify.sh lane-run

# The cost line is the point, not decoration. This is the SHIP/RELEASE gate;
# ordinary commits run the derived `make lane-run`, which refuses an unclaimed
# path instead of silently skipping it. The ceiling was once made the gate for
# every commit and recreated the exact docs-only waste P12 built derivation to
# remove. The convention carries the cadence at the point of use.
check: ## THE CEILING — project-owned cache + one CLI artifact + static and Go gates.
	@echo "check: THE CEILING (~15 min) — the ship/release gate. Ordinary commit? Run \`make lane-run\`."
	@bash scripts/verify.sh full

test: ## Scoped race test through the owned environment. Optional: A2A_VERIFY_TEST_RUN=Regex A2A_VERIFY_TEST_COUNT=N.
	@test -n "$(PKG)" || { echo "test: set PKG, e.g. make test PKG=./internal/cache/..."; exit 2; }
	@bash scripts/verify.sh test $(PKG)

# The web lane. Deliberately NOT in REPO_GATES: `make check` stays byte-identical
# (spec 12 J5) and the npm suite keeps its own entry. It exists as a target so the
# lane the repo already HAS gets one declared home the derivation can find —
# without it, deleting check-convention.md's hand-maintained table would make this
# lane vanish silently on the repo's second-largest tracked tree.
# Presence-gated on web/node_modules exactly as dashboard-template-drift is.
# lane-inputs:
#   web/**
#   ui/**
web-quality: ## The web stack's own quality gate (npm). NOT part of `make check` — run when web/** or ui/** changed.
	@if [ -d web/node_modules ]; then \
	  npm --prefix web run check:quality; \
	else \
	  echo "web-quality: skip — web/node_modules absent (run 'npm --prefix web ci' first)."; \
	fi

lane-declarations: ## Every validation phase declares the inputs that can change its verdict, and reads only what it declared (P12).
	@bash scripts/check-lane-declarations.sh

classify-guard: ## Publish-boundary gate: no private (harness) path is tracked, DENY↔.gitignore agree.
	@bash scripts/classify-guard.sh

# lane-inputs:
#   .github/workflows/**
workflow-lint: ## Every GitHub Action `uses:` must be SHA-pinned (defeats tag-hijack; dependabot still bumps the pins).
	@bad=$$(grep -rnE 'uses: +[^ ]+@' .github/workflows 2>/dev/null | grep -vE '@[0-9a-f]{40}([ "#]|$$)' | grep -v 'uses: \./' || true); \
	if [ -n "$$bad" ]; then echo "workflow-lint: FAIL — unpinned action(s), pin to a full 40-hex SHA (# vX.Y.Z):"; echo "$$bad"; exit 1; fi; \
	echo "workflow-lint: all actions SHA-pinned."
	@grep -Fq "if: github.repository == 'ydnikolaev/a2ahub'" .github/workflows/classify-guard.yml || { echo "workflow-lint: FAIL — public classify backstop must skip the private source repository"; exit 1; }
	@grep -Eq '^  actions: read$$' .github/workflows/codeql.yml || { echo "workflow-lint: FAIL — CodeQL needs actions: read with restrictive workflow permissions"; exit 1; }
	@test "$$(grep -Fc "if: github.repository == 'ydnikolaev/a2ahub'" .github/workflows/codeql.yml)" -eq 4 || { echo "workflow-lint: FAIL — all four CodeQL execution steps must remain public-repository-only"; exit 1; }
	@command -v actionlint >/dev/null 2>&1 || { echo "workflow-lint: FAIL — actionlint missing; install github.com/rhysd/actionlint/cmd/actionlint@v1.7.12"; exit 1; }
	@actionlint

gosec-scope: ## G204/G304 stay live outside the exact reviewed path allowlist.
	@bash scripts/check-gosec-scope.sh

readme-lint: ## README stays compact, current, and exits to the canonical docs.
	@bash scripts/check-readme.sh

dashboard-template-drift: ## internal/html/template.html must equal a fresh build of web/design-source (skips without web/node_modules).
	@bash scripts/dashboard-template-drift.sh

view-vocabulary: ## No component may classify by its own list of state names, or spell one the domain does not have.
	@bash scripts/check-view-vocabulary.sh

pendency-uniqueness: ## Whose-move-is-it has ONE home; no surface may resolve its own verdict (P2 AC3/AC4).
	@bash scripts/check-pendency-uniqueness.sh

loop-coverage: ## Every (type x role x phase) loop cell is covered by a step or declared empty with a reason (P7).
	@bash scripts/check-loop-coverage.sh

coverage: ## Same one-artifact race/coverage path as `check`, without static/vet/lint phases.
	@bash scripts/verify.sh coverage

release-preflight: ## MUST pass before cutting a release tag: version free on the release remote + every space-template reusable pin resolves to a tag that carries the workflow. Needs network — NOT in `check`. Usage: make release-preflight VERSION=v0.6.0
	@test -n "$(VERSION)" || { echo "release-preflight: set VERSION, e.g. make release-preflight VERSION=v0.6.0"; exit 2; }
	@bash scripts/check-roadmap-release-decisions.sh "$(VERSION)"
	@bash scripts/release-preflight.sh "$(VERSION)"

vulncheck: ## govulncheck ./... gated by .govulncheck-allow.txt (NEW called vuln reds; accepted stays green). Needs network — NOT in `check`.
	@command -v govulncheck >/dev/null 2>&1 || { echo "vulncheck: govulncheck missing — go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }
	@out=$$(govulncheck ./... 2>&1) || true; \
	found=$$(printf '%s\n' "$$out" | grep -oE 'GO-[0-9]{4}-[0-9]+' | sort -u); \
	new=""; for id in $$found; do grep -qxF "$$id" .govulncheck-allow.txt 2>/dev/null || new="$$new $$id"; done; \
	if [ -n "$$new" ]; then printf '%s\n' "$$out"; echo; echo "vulncheck: FAIL — NEW vulnerabilities (not in .govulncheck-allow.txt):$$new"; exit 1; fi; \
	if [ -n "$$found" ]; then echo "vulncheck: OK — only accepted vulns present:$$(printf '%s' "$$found" | tr '\n' ' ' | sed 's/^/ /')"; else echo "vulncheck: OK — no called vulnerabilities"; fi

live-e2e: ## THE LIVE TIER: exact public candidate against real GitHub. Launch via docs/runbooks/live-e2e/candidate.sh; requires explicit candidate root/SHA/tree/tag/floor/check-log plus two identities. NEVER in `check` or a merge gate. Narrowed family/cell runs always exit non-zero.
	@bash scripts/verify.sh live

live-e2e-evidence: ## VG-OC-24 release gate. Usage: make live-e2e-evidence EVIDENCE=path/to/evidence.json
	@test -n "$(EVIDENCE)" || { echo "live-e2e-evidence: set EVIDENCE to the exact candidate's manifest"; exit 2; }
	@bash scripts/check_live_e2e_evidence.sh "$(EVIDENCE)"

logic-e2e: ## THE LOGIC LANE inner loop: TestLogicMatrix + its 3 siblings (spec 09) — no credentials, no network, no candidate. Also runs inside `make check`; use this to iterate on one scenario without the whole ceiling.
	@bash scripts/verify.sh logic-e2e

install: ## Put a dev `a2a` on your PATH that always runs THIS source tree (rebuilds when changed).
	@sh scripts/dev-install.sh

feature-lint: ## Validate docs/features/<slug>/ against the canonical template (private harness gate, presence-gated).
	@if [ -f scripts/check-feature-lint.sh ]; then \
	  bash scripts/check-feature-lint.sh; \
	else \
	  echo "feature-lint: skip — scripts/check-feature-lint.sh absent (public checkout)."; \
	fi

skill-citations: ## Every `a2a <verb>` and error code the shipped skill PROSE cites must exist (P39 AC-993.1; private harness gate, presence-gated).
	@if [ -f scripts/check-skill-citations.sh ]; then \
	  bash scripts/check-skill-citations.sh; \
	else \
	  echo "skill-citations: skip — scripts/check-skill-citations.sh absent (public checkout)."; \
	fi

release-notes-freshness: ## User-visible product commits cannot outrun the newest authored release notes.
	@bash scripts/check-release-notes-freshness.sh

roadmap-release-decisions: ## Every feature in the newest release notes is explicitly included in or omitted from Shipped now.
	@bash scripts/check-roadmap-release-decisions.sh

provider-tier-deferral: ## A 3rd consecutive logic-proven, provider-deferred release without an intervening live-e2e run refuses to ship.
	@bash scripts/check-provider-tier-deferral.sh

space-template-baseline-check: ## The space template's write floor and its workflow pins name one published release (release runbook Phase 4 step 15).
	@bash scripts/bump-space-template.sh --check

space-template-baseline: ## Move the space template's floor AND workflow pins to the release just tagged, from one derived version. Refuses an unpublished tag.
	@bash scripts/bump-space-template.sh

epic-drift: ## An epic's committed docs (status.md stamp, receipts) must match its tracker (private harness gate, presence-gated).
	@if [ -f .agents/scripts/epic_docs_drift.sh ]; then \
	  bash .agents/scripts/epic_docs_drift.sh; \
	else \
	  echo "epic-drift: skip — .agents/scripts/epic_docs_drift.sh absent (public checkout)."; \
	fi

operational-confidence-guard: ## P0 IDs/history/DAG/v1-byte/boundary invariants.
	@bash scripts/check-operational-confidence.sh

event-writer-receipts: ## First-party event.state is evaluator-owned across CLI and MCP writers.
	@bash scripts/check_event_writer_receipts.sh

contract-carried-set: ## Contract v2 roles/profiles and digest builder remain one closed corpus.
	@bash scripts/check_contract_carried_set.sh

work-checkpoint-schema: ## Work schema/template/core/validator/docs share one closed mode/wait vocabulary.
	@bash scripts/check_work_checkpoint_schema.sh

operational-projection-single-source: ## Static HTML and the local server consume one operational snapshot.
	@bash scripts/check_operational_projection_single_source.sh

localserver-readonly-routes: ## Local HTTP exposes only the frozen read-only route inventory.
	@bash scripts/check_localserver_readonly_routes.sh

harness-check: ## Run the gates' --teeth self-tests (harness gates are private/presence-gated; release-preflight is public).
	@bash scripts/verify.sh harness

_harness-check:
	@bash scripts/verify.sh --teeth
	@bash scripts/check-gosec-scope.sh --teeth
	@bash scripts/release-preflight.sh --teeth
	@bash scripts/check-view-vocabulary.sh --teeth
	@bash scripts/check-pendency-uniqueness.sh --teeth
	@bash scripts/check-release-notes-freshness.sh --teeth
	@bash scripts/check-roadmap-release-decisions.sh --teeth
	@bash scripts/check-provider-tier-deferral.sh --teeth
	@bash scripts/bump-space-template.sh --teeth
	@bash scripts/check-readme.sh --teeth
	@bash scripts/dashboard-template-drift.sh --teeth
	@if [ -f docs/runbooks/publish-to-public.sh ]; then \
	  bash docs/runbooks/publish-to-public.sh --teeth; \
	else \
	  echo "harness-check: skip — private publish runbook absent (public checkout)."; \
	fi
	@if [ -f scripts/check-feature-lint.sh ]; then \
	  bash scripts/check-feature-lint.sh --teeth; \
	else \
	  echo "harness-check: skip — scripts/check-feature-lint.sh absent (public checkout)."; \
	fi
	@if [ -f .agents/scripts/epic_docs_drift.sh ]; then \
	  bash .agents/scripts/epic_docs_drift.sh --teeth; \
	else \
	  echo "harness-check: skip — .agents/scripts/epic_docs_drift.sh absent (public checkout)."; \
	fi
	@if [ -f scripts/check-skill-citations.sh ]; then \
	  bash scripts/check-skill-citations.sh --teeth; \
	else \
		echo "harness-check: skip — scripts/check-skill-citations.sh absent (public checkout)."; \
	fi
	@bash scripts/check-operational-confidence.sh --teeth
	@bash scripts/check-lane-declarations.sh --teeth
	@bash scripts/tests/check_event_writer_receipts_test.sh
	@bash scripts/tests/check_contract_carried_set_test.sh
	@bash scripts/tests/check_work_checkpoint_schema_test.sh
	@bash scripts/tests/check_operational_projection_single_source_test.sh
	@bash scripts/tests/check_localserver_readonly_routes_test.sh
	@bash scripts/tests/check_live_e2e_evidence_test.sh
