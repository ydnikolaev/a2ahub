.PHONY: check check-validators readme-lint feature-lint classify-guard

REPO_GATES := classify-guard readme-lint feature-lint

_print-repo-gates:
	@echo "$(REPO_GATES)"

check-validators:
	@bash scripts/verify.sh validators

classify-guard: ## Publish-boundary gate.
	@bash scripts/classify-guard.sh

readme-lint: ## README stays compact.
	@bash scripts/check-readme.sh

feature-lint: ## Private harness gate, presence-gated.
	@if [ -f scripts/check-feature-lint.sh ]; then \
	  bash scripts/check-feature-lint.sh; \
	else \
	  echo "feature-lint: skip — scripts/check-feature-lint.sh absent (public checkout)."; \
	fi
