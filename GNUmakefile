# Host queue shim, owned by a2ahub. Adapted from devkit's template/GNUmakefile (heavy-run doctrine rules 2 and 11,
# ~/Developer/_ops/doctrines/heavy-run.md); a2ahub has no .devkit/, so the sizes and the heavy list live here.
#
# GNU make reads this before Makefile. At MAKELEVEL 0 a command-line goal is classed by name: a heavy goal with the
# host queue present, no HEAVY_RUN_SLOT and no -n/-q/-t queues the whole invocation once through heavy_run (Makefile
# is not read here: the inner make reads it inside the hold); light and unclassed goals run Makefile at
# HEAVY_RUN_CORES=2 GOMAXPROCS=2; dev goals run it untouched. A sub-make only reads Makefile: it inherits its parent's
# decision. Without the host queue (CI, the parity container, a public checkout) this file is a plain
# `include Makefile` and changes nothing. No `$(shell)`. Rules go in Makefile, never here.
#
# DIFFERENCES FROM THE TEMPLATE, on purpose:
#   - `check` is HEAVY here: it is the ~15 min offline ceiling (scripts/verify.sh full), not a light lint.
#   - the docker grant applies to a2ahub's container goals, not to a goal named `check-docker`.
#   - web-quality declares its own, smaller cost: one browser, one worker.
#   - `--project a2ahub`, so a run from a clone or worktree is estimated with the repo's own history.
#   - nothing is exported when the queue is absent.
# Typed bare, always: `make check`. Never under lockf, never under a second wrapper.
ifeq ($(MAKELEVEL),0)
kit_comma := ,
kit_cores := $(or $(KIT_QUEUE_CORES),6)
kit_cores_docker := $(or $(KIT_QUEUE_CORES_DOCKER),8)
kit_cores_web := $(or $(KIT_QUEUE_CORES_WEB),3)
# Every goal over ~3 min or over 2 cores. scripts/tests/heavy_queue_shim_test.sh reads this line and proves each name
# queues and is a real Makefile target.
a2a_heavy_targets := check test coverage flaky-scan lane-run lane-run-strict harness-check ci-parity ci-parity-docker release-check release-preflight projection logic-e2e live-e2e web-quality site-check vulncheck
a2a_docker_targets := ci-parity-docker release-check
a2a_web_targets := web-quality
kit_list := $(a2a_heavy_targets) $(subst $(kit_comma), ,$(KIT_HEAVY_TARGETS))
kit_dev_names := dev-up dev-down dev-status run serve
kit_light := $(filter-out $(kit_list),check lint fmt vet)
kit_heavy := $(filter-out $(kit_light) $(kit_dev_names),$(filter check-% %-check test test-% e2e% %-gate build build-% \
  %-build image image-% %-image $(kit_list),$(MAKECMDGOALS)))
kit_dev := $(filter $(kit_dev_names),$(MAKECMDGOALS))
ifneq ($(kit_dev),)
ifneq ($(kit_heavy),)
$(error $(firstword $(kit_dev)) is a dev goal and $(firstword $(kit_heavy)) is heavy: run them apart)
endif
endif
ifneq ($(filter $(a2a_docker_targets),$(kit_heavy)),)
kit_cores := $(kit_cores_docker)
else ifeq ($(filter-out $(a2a_web_targets),$(kit_heavy)),)
kit_cores := $(if $(kit_heavy),$(kit_cores_web),$(kit_cores))
endif
kit_dry := $(findstring n,$(firstword -$(MAKEFLAGS)))$(findstring q,$(firstword -$(MAKEFLAGS)))$(findstring t,$(firstword -$(MAKEFLAGS)))
KIT_HEAVY_RUN := $(wildcard $(or $(DEVKIT_HOST),$(HOME)/Developer/_ops)/scripts/heavy_run.py)
kit_outer := $(if $(kit_heavy),$(if $(HEAVY_RUN_SLOT)$(kit_dry),,$(KIT_HEAVY_RUN)))
endif

ifneq ($(kit_outer),)
kit_first := $(firstword $(MAKECMDGOALS))
.PHONY: $(MAKECMDGOALS)
$(kit_first):
	+@python3 $(KIT_HEAVY_RUN) run --project a2ahub --label $(firstword $(kit_heavy)) --cores $(kit_cores) -- $(MAKE) --no-print-directory $(MAKECMDGOALS)
$(filter-out $(kit_first),$(MAKECMDGOALS)): $(kit_first) ;
else
ifneq ($(KIT_HEAVY_RUN),)
ifeq ($(MAKELEVEL)$(HEAVY_RUN_SLOT)$(kit_dev),0)
export HEAVY_RUN_CORES := $(if $(kit_heavy),$(kit_cores),2)
export GOMAXPROCS := $(HEAVY_RUN_CORES)
endif
endif
include Makefile
endif
