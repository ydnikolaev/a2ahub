# ─────────────────────────────────────────────────────────────────────
# Root Makefile — repo-level tooling (make-ABI)
# ─────────────────────────────────────────────────────────────────────
#
# make check             THE CEILING — repo gates + Go gates (gofmt/vet/test),
#                         the latter only if go.mod exists yet.
# make check-validators  THE STATIC LANE — repo gates only, no tests. The inner
#                         loop when the diff is docs/scripts and no Go changed.
# make classify-guard    publish-boundary gate: no private (harness) path tracked.
# make workflow-lint     every GitHub Action `uses:` is SHA-pinned (product gate).
# make feature-lint      docs/features/<slug>/ conforms to the canonical template
#                        (private harness gate — skips cleanly if absent).
# make epic-drift        an epic's committed docs match its reality
#                        (private harness gate — skips cleanly if absent).
# make harness-check     both harness gates' --teeth self-tests (the gates bite).
# make coverage          go test -race with the coveragepolicy SSOT floor (also run by `check`).
# make vulncheck         govulncheck ./... gated by .govulncheck-allow.txt (network; not in `check`).
# make install           put a dev `a2a` on your PATH that always runs THIS source tree.
#
# Recipes are POSIX sh — no bashisms — even though the gate scripts they call
# are bash (invoked explicitly via `bash`, never relying on $(SHELL)).

.PHONY: check check-validators feature-lint epic-drift classify-guard workflow-lint harness-check coverage vulncheck install

# ONE list, consumed by both `check` (the ceiling) and `check-validators` (the
# static lane). Two hand-kept copies of a gate list drift, and the drift is
# invisible: a copy quietly stops running a gate while still printing green.
#
# classify-guard + workflow-lint are PRODUCT gates (always run, committed public).
# feature-lint/epic-drift are PRIVATE harness gates: their scripts live under
# the mate-managed harness (scripts/check-feature-lint.sh, .agents/scripts/
# epic_docs_drift.sh) and are absent on a public checkout — each target below
# presence-gates itself so `make check` never hard-fails on their absence.
REPO_GATES := classify-guard workflow-lint feature-lint epic-drift

check-validators: $(REPO_GATES) ## Repo gates only, no tests, no build — the static lane.
	@echo "check-validators: repo gates green (classify-guard, workflow-lint, feature-lint, epic-drift). No tests ran."

check: $(REPO_GATES) ## THE CEILING — repo gates, plus Go gates once go.mod exists.
	@if [ -f go.mod ]; then \
	  echo "check: go.mod found — running Go gates (gofmt -l, go vet, lint, go test -race)"; \
	  unformatted=$$(gofmt -l .); \
	  if [ -n "$$unformatted" ]; then \
	    echo "check: gofmt -l found unformatted file(s):"; \
	    echo "$$unformatted"; \
	    exit 1; \
	  fi; \
	  go vet ./... || exit 1; \
	  if [ -f .golangci.yml ]; then \
	    if command -v golangci-lint >/dev/null 2>&1; then \
	      golangci-lint run ./... || exit 1; \
	    else \
	      echo "check: FAIL — .golangci.yml exists but golangci-lint is not installed."; \
	      echo "       A configured lint gate that silently skips is a hole, not a gate."; \
	      exit 1; \
	    fi; \
	  fi; \
	  go test ./... -race -covermode=atomic -coverprofile=coverage.out -count=1 || exit 1; \
	  go run internal/coveragepolicy/covercheck.go coverage.out || exit 1; \
	  echo "check: repo gates + Go gates green (coverage floor met)."; \
	else \
	  echo "check: no go.mod yet — repo gates only (Go gates skipped)."; \
	fi

classify-guard: ## Publish-boundary gate: no private (harness) path is tracked, DENY↔.gitignore agree.
	@bash scripts/classify-guard.sh

workflow-lint: ## Every GitHub Action `uses:` must be SHA-pinned (defeats tag-hijack; dependabot still bumps the pins).
	@bad=$$(grep -rnE 'uses: +[^ ]+@' .github/workflows 2>/dev/null | grep -vE '@[0-9a-f]{40}([ "#]|$$)' | grep -v 'uses: \./' || true); \
	if [ -n "$$bad" ]; then echo "workflow-lint: FAIL — unpinned action(s), pin to a full 40-hex SHA (# vX.Y.Z):"; echo "$$bad"; exit 1; fi; \
	echo "workflow-lint: all actions SHA-pinned."
	@command -v actionlint >/dev/null 2>&1 && actionlint || echo "workflow-lint: actionlint not installed locally (CI runs it) — go install github.com/rhysd/actionlint/cmd/actionlint@latest"

coverage: ## go test -race with coverage, gated by the coveragepolicy SSOT floor (same code path as `check`).
	@if [ ! -f go.mod ]; then echo "coverage: no go.mod — skipped."; exit 0; fi
	go test ./... -race -covermode=atomic -coverprofile=coverage.out -count=1
	@go run internal/coveragepolicy/covercheck.go coverage.out

vulncheck: ## govulncheck ./... gated by .govulncheck-allow.txt (NEW called vuln reds; accepted stays green). Needs network — NOT in `check`.
	@command -v govulncheck >/dev/null 2>&1 || { echo "vulncheck: govulncheck missing — go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }
	@out=$$(govulncheck ./... 2>&1) || true; \
	found=$$(printf '%s\n' "$$out" | grep -oE 'GO-[0-9]{4}-[0-9]+' | sort -u); \
	new=""; for id in $$found; do grep -qxF "$$id" .govulncheck-allow.txt 2>/dev/null || new="$$new $$id"; done; \
	if [ -n "$$new" ]; then printf '%s\n' "$$out"; echo; echo "vulncheck: FAIL — NEW vulnerabilities (not in .govulncheck-allow.txt):$$new"; exit 1; fi; \
	if [ -n "$$found" ]; then echo "vulncheck: OK — only accepted vulns present:$$(printf '%s' "$$found" | tr '\n' ' ' | sed 's/^/ /')"; else echo "vulncheck: OK — no called vulnerabilities"; fi

install: ## Put a dev `a2a` on your PATH that always runs THIS source tree (rebuilds when changed).
	@sh scripts/dev-install.sh

feature-lint: ## Validate docs/features/<slug>/ against the canonical template (private harness gate, presence-gated).
	@if [ -f scripts/check-feature-lint.sh ]; then \
	  bash scripts/check-feature-lint.sh; \
	else \
	  echo "feature-lint: skip — scripts/check-feature-lint.sh absent (public checkout)."; \
	fi

epic-drift: ## An epic's committed docs (status.md stamp, receipts) must match its tracker (private harness gate, presence-gated).
	@if [ -f .agents/scripts/epic_docs_drift.sh ]; then \
	  bash .agents/scripts/epic_docs_drift.sh; \
	else \
	  echo "epic-drift: skip — .agents/scripts/epic_docs_drift.sh absent (public checkout)."; \
	fi

harness-check: ## Run both harness gates' --teeth self-tests (private-only; presence-gated).
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
