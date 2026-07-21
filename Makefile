# ─────────────────────────────────────────────────────────────────────
# Root Makefile — repo-level tooling (make-ABI)
# ─────────────────────────────────────────────────────────────────────
#
# make check             THE CEILING — repo gates + Go gates (gofmt/vet/test),
#                         the latter only if go.mod exists yet.
# make check-validators  THE STATIC LANE — repo gates only, no tests. The inner
#                         loop when the diff is docs/scripts and no Go changed.
# make feature-lint      docs/features/<slug>/ conforms to the canonical template.
# make epic-drift        an epic's committed docs match its reality.
# make harness-check     both gates' --teeth self-tests (the gates themselves work).
#
# Recipes are POSIX sh — no bashisms — even though the gate scripts they call
# are bash (invoked explicitly via `bash`, never relying on $(SHELL)).

.PHONY: check check-validators feature-lint epic-drift harness-check

# ONE list, consumed by both `check` (the ceiling) and `check-validators` (the
# static lane). Two hand-kept copies of a gate list drift, and the drift is
# invisible: a copy quietly stops running a gate while still printing green.
REPO_GATES := feature-lint epic-drift

check-validators: $(REPO_GATES) ## Repo gates only, no tests, no build — the static lane.
	@echo "check-validators: repo gates green (feature-lint, epic-drift). No tests ran."

check: $(REPO_GATES) ## THE CEILING — repo gates, plus Go gates once go.mod exists.
	@if [ -f go.mod ]; then \
	  echo "check: go.mod found — running Go gates (gofmt -l, go vet, go test -race)"; \
	  unformatted=$$(gofmt -l .); \
	  if [ -n "$$unformatted" ]; then \
	    echo "check: gofmt -l found unformatted file(s):"; \
	    echo "$$unformatted"; \
	    exit 1; \
	  fi; \
	  go vet ./... && \
	  go test ./... -race -count=1 && \
	  echo "check: repo gates + Go gates (gofmt, vet, test) green."; \
	else \
	  echo "check: no go.mod yet — repo gates only (Go gates skipped)."; \
	fi

feature-lint: ## Validate docs/features/<slug>/ against the canonical template.
	@bash scripts/check-feature-lint.sh

epic-drift: ## An epic's committed docs (status.md stamp, receipts) must match its tracker.
	@bash .agents/scripts/epic_docs_drift.sh

harness-check: ## Run both gates' --teeth self-tests (proves the gates actually bite).
	@bash scripts/check-feature-lint.sh --teeth
	@bash .agents/scripts/epic_docs_drift.sh --teeth
