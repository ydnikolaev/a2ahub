# ─────────────────────────────────────────────────────────────────────
# Root Makefile — repo-level tooling (make-ABI)
# ─────────────────────────────────────────────────────────────────────
#
# make check             THE CEILING — one outer runner owns cache/artifact,
#                         repo gates, then Go gates (gofmt/vet/lint/test).
# make check-validators  THE STATIC LANE — repo gates only, no tests. The inner
#                         loop when the diff is docs/scripts and no Go changed.
# make classify-guard    publish-boundary gate: no private (harness) path tracked.
# make workflow-lint     every GitHub Action `uses:` is SHA-pinned (product gate).
# make readme-lint       README stays compact, current, and linked to deeper docs.
# make feature-lint      docs/features/<slug>/ conforms to the canonical template
#                        (private harness gate — skips cleanly if absent).
# make epic-drift        an epic's committed docs match its reality
#                        (private harness gate — skips cleanly if absent).
# make skill-citations   every `a2a <verb>` / error code the shipped skill PROSE
#                        cites must exist (private harness gate — skips if absent).
# make harness-check     both harness gates' --teeth self-tests (the gates bite).
# make coverage          go test -race with the coveragepolicy SSOT floor (also run by `check`).
# make vulncheck         govulncheck ./... gated by .govulncheck-allow.txt (network; not in `check`).
# make live-e2e          THE LIVE TIER — the real binary against a real throwaway
#                         GitHub space (spec 36). Network + two credentials +
#                         immutable public candidate SHA + Actions latency:
#                         NEVER in `check`, never a merge gate.
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

.PHONY: check test check-validators _print-repo-gates dashboard-template-drift feature-lint epic-drift skill-citations release-notes-freshness readme-lint classify-guard workflow-lint gosec-scope harness-check _harness-check coverage vulncheck release-preflight live-e2e install

# ONE list, consumed by both `check` (the ceiling) and `check-validators` (the
# static lane). Two hand-kept copies of a gate list drift, and the drift is
# invisible: a copy quietly stops running a gate while still printing green.
#
# classify-guard + workflow-lint are PRODUCT gates (always run, committed public).
# feature-lint/epic-drift are PRIVATE harness gates: their scripts live under
# the mate-managed harness (scripts/check-feature-lint.sh, .agents/scripts/
# epic_docs_drift.sh) and are absent on a public checkout — each target below
# presence-gates itself so `make check` never hard-fails on their absence.
REPO_GATES := classify-guard workflow-lint gosec-scope readme-lint dashboard-template-drift feature-lint epic-drift skill-citations release-notes-freshness

_print-repo-gates:
	@echo "$(REPO_GATES)"

check-validators: ## Repo gates only; one shared CLI build feeds binary-backed static gates.
	@bash scripts/verify.sh validators

check: ## THE CEILING — project-owned cache + one CLI artifact + static and Go gates.
	@bash scripts/verify.sh full

test: ## Scoped race test through the owned environment. Optional: A2A_VERIFY_TEST_RUN=Regex A2A_VERIFY_TEST_COUNT=N.
	@test -n "$(PKG)" || { echo "test: set PKG, e.g. make test PKG=./internal/cache/..."; exit 2; }
	@bash scripts/verify.sh test $(PKG)

classify-guard: ## Publish-boundary gate: no private (harness) path is tracked, DENY↔.gitignore agree.
	@bash scripts/classify-guard.sh

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

coverage: ## Same one-artifact race/coverage path as `check`, without static/vet/lint phases.
	@bash scripts/verify.sh coverage

release-preflight: ## MUST pass before cutting a release tag: version free on the release remote + every space-template reusable pin resolves to a tag that carries the workflow. Needs network — NOT in `check`. Usage: make release-preflight VERSION=v0.6.0
	@test -n "$(VERSION)" || { echo "release-preflight: set VERSION, e.g. make release-preflight VERSION=v0.6.0"; exit 2; }
	@bash scripts/release-preflight.sh "$(VERSION)"

vulncheck: ## govulncheck ./... gated by .govulncheck-allow.txt (NEW called vuln reds; accepted stays green). Needs network — NOT in `check`.
	@command -v govulncheck >/dev/null 2>&1 || { echo "vulncheck: govulncheck missing — go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }
	@out=$$(govulncheck ./... 2>&1) || true; \
	found=$$(printf '%s\n' "$$out" | grep -oE 'GO-[0-9]{4}-[0-9]+' | sort -u); \
	new=""; for id in $$found; do grep -qxF "$$id" .govulncheck-allow.txt 2>/dev/null || new="$$new $$id"; done; \
	if [ -n "$$new" ]; then printf '%s\n' "$$out"; echo; echo "vulncheck: FAIL — NEW vulnerabilities (not in .govulncheck-allow.txt):$$new"; exit 1; fi; \
	if [ -n "$$found" ]; then echo "vulncheck: OK — only accepted vulns present:$$(printf '%s' "$$found" | tr '\n' ' ' | sed 's/^/ /')"; else echo "vulncheck: OK — no called vulnerabilities"; fi

live-e2e: ## THE LIVE TIER: the real binary against a real GitHub space (spec 36). Needs A2A_LIVE_E2E_{ORG,PROVISIONER_TOKEN,PARTICIPANT_TOKEN,CANDIDATE_SHA}; CANDIDATE_SHA is the immutable public release candidate used for workflow source and validator. NEVER in `check` or a merge gate. A narrowed A2A_LIVE_E2E_FAMILIES/A2A_LIVE_E2E_CELLS run always exits non-zero.
	@bash scripts/verify.sh live

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

epic-drift: ## An epic's committed docs (status.md stamp, receipts) must match its tracker (private harness gate, presence-gated).
	@if [ -f .agents/scripts/epic_docs_drift.sh ]; then \
	  bash .agents/scripts/epic_docs_drift.sh; \
	else \
	  echo "epic-drift: skip — .agents/scripts/epic_docs_drift.sh absent (public checkout)."; \
	fi

harness-check: ## Run the gates' --teeth self-tests (harness gates are private/presence-gated; release-preflight is public).
	@bash scripts/verify.sh harness

_harness-check:
	@bash scripts/verify.sh --teeth
	@bash scripts/check-gosec-scope.sh --teeth
	@bash scripts/release-preflight.sh --teeth
	@bash scripts/check-release-notes-freshness.sh --teeth
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
