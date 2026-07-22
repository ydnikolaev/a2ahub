# ─────────────────────────────────────────────────────────────────────
# Root Makefile — repo-level tooling (make-ABI)
# ─────────────────────────────────────────────────────────────────────
#
# make check             THE CEILING — repo gates + Go gates (gofmt/vet/test),
#                         the latter only if go.mod exists yet.
# make check-validators  THE STATIC LANE — repo gates only, no tests. The inner
#                         loop when the diff is docs/scripts and no Go changed.
# make classify-guard    publish-boundary gate: no private (harness) path tracked.
# make feature-lint      docs/features/<slug>/ conforms to the canonical template
#                        (private harness gate — skips cleanly if absent).
# make epic-drift        an epic's committed docs match its reality
#                        (private harness gate — skips cleanly if absent).
# make harness-check     both harness gates' --teeth self-tests (the gates bite).
#
# Recipes are POSIX sh — no bashisms — even though the gate scripts they call
# are bash (invoked explicitly via `bash`, never relying on $(SHELL)).

.PHONY: check check-validators feature-lint epic-drift classify-guard harness-check

# ONE list, consumed by both `check` (the ceiling) and `check-validators` (the
# static lane). Two hand-kept copies of a gate list drift, and the drift is
# invisible: a copy quietly stops running a gate while still printing green.
#
# classify-guard is a PRODUCT gate (always runs, script is committed public).
# feature-lint/epic-drift are PRIVATE harness gates: their scripts live under
# the mate-managed harness (scripts/check-feature-lint.sh, .agents/scripts/
# epic_docs_drift.sh) and are absent on a public checkout — each target below
# presence-gates itself so `make check` never hard-fails on their absence.
REPO_GATES := classify-guard feature-lint epic-drift

check-validators: $(REPO_GATES) ## Repo gates only, no tests, no build — the static lane.
	@echo "check-validators: repo gates green (classify-guard, feature-lint, epic-drift). No tests ran."

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
	  go test ./... -race -count=1 && \
	  echo "check: repo gates + Go gates green."; \
	else \
	  echo "check: no go.mod yet — repo gates only (Go gates skipped)."; \
	fi

classify-guard: ## Publish-boundary gate: no private (harness) path is tracked, DENY↔.gitignore agree.
	@bash scripts/classify-guard.sh

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

harness-check: ## Run both gates' --teeth self-tests (proves the gates actually bite).
	@bash scripts/check-feature-lint.sh --teeth
	@bash .agents/scripts/epic_docs_drift.sh --teeth
