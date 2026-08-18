.PHONY: check phase-a phase-b

REPO_GATES := phase-a phase-b

phase-a: ## invokes the shared multi-phase corpus script, mode a.
	@bash scripts/verify.sh a

phase-b: ## invokes the shared multi-phase corpus script, mode b.
	@bash scripts/verify.sh b
