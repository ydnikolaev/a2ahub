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

.PHONY: check test check-validators ci-parity ci-parity-audit ci-parity-docker frozen-allowlist lane lane-run lane-declarations web-quality error-codes _print-repo-gates dashboard-template-drift dashboard-cards dashboard-derivation feature-lint epic-drift operational-confidence-guard event-writer-receipts contract-carried-set work-checkpoint-schema operational-projection-single-source localserver-readonly-routes skill-citations feedback-corpus spec-verify-refs feedback-sync view-vocabulary pendency-uniqueness notify-workflow notify-secrets error-codes loop-coverage human-gates loop-reachability prose-roster prose-coverage release-notes-freshness release-record roadmap-release-decisions provider-tier-deferral space-template-baseline space-template-baseline-check readme-lint classify-guard workflow-lint gosec-scope harness-check _harness-check coverage vulncheck release-preflight release-postflight projection release-check release-check-dry live-e2e live-e2e-evidence logic-e2e install

# ONE list, consumed by both `check` (the ceiling) and `check-validators` (the
# static lane). Two hand-kept copies of a gate list drift, and the drift is
# invisible: a copy quietly stops running a gate while still printing green.
#
# classify-guard + workflow-lint are PRODUCT gates (always run, committed public).
# feature-lint/epic-drift are PRIVATE harness gates: their scripts live under
# the mate-managed harness (scripts/check-feature-lint.sh, .agents/scripts/
# epic_docs_drift.sh) and are absent on a public checkout — each target below
# presence-gates itself so `make check` never hard-fails on their absence.
REPO_GATES := spec-verify-refs ci-parity-audit frozen-allowlist lane-declarations classify-guard workflow-lint gosec-scope readme-lint dashboard-cards dashboard-derivation feature-lint epic-drift operational-confidence-guard event-writer-receipts contract-carried-set work-checkpoint-schema operational-projection-single-source localserver-readonly-routes skill-citations feedback-corpus view-vocabulary dashboard-props card-content pendency-uniqueness notify-workflow notify-secrets error-codes loop-coverage human-gates loop-reachability prose-roster prose-coverage release-notes-freshness release-record roadmap-release-decisions provider-tier-deferral runner-economics space-template-baseline-check

_print-repo-gates:
	@echo "$(REPO_GATES)"

check-validators: ## Repo gates only; one shared CLI build feeds binary-backed static gates.
	@bash scripts/verify.sh validators

lane: ## PRINT the lane this working tree's changes can actually reach, with each phase's reason and measured median. Optional: LANE_FILES="a b c".
	@bash scripts/verify.sh lane

lane-run: ## RUN that derived lane. NOT the ceiling — a release still runs `make check` (spec 12 J5). Optional: LANE_FILES="a b c".
	@bash scripts/verify.sh lane-run

# The strict form, for CI. `lane-run` on an EMPTY changed set prints a friendly
# note and exits 0 — correct at a terminal, a false green in a job: a clean
# `actions/checkout` tree derives nothing, so the run reports success having
# executed zero gates, and nothing downstream can tell that apart from a real
# pass because the job appears in the run list, succeeded. This target refuses
# instead. Local use stays on `lane-run`; a workflow calls THIS one.
lane-run-strict: ## RUN the derived lane, refusing an empty or unresolvable input set. The CI form of `lane-run`.
	@bash scripts/verify.sh lane-run --require-nonempty

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
# Presence-gated locally on web/node_modules. The drift check runs inside this
# same web lane; CI invokes both only after its filtered job has installed Node.
# skill/** is declared because the SITE IS GENERATED FROM IT, and leaving it out
# was a false green that lasted a day. web/scripts/generate-content.mjs reads
# skill/a2ahub/docs-manifest.json and every doc body it names, so a skill edit
# changes this gate's verdict as directly as a web edit does. On 2026-08-09
# commit a09db83e grew skill/a2ahub/loops.md by sixteen lines and pushed
# docs/work-loops.html past its gzip budget; the lane selected nothing, the
# commit went green, and the breakage surfaced a day later inside an unrelated
# wave. This is exactly the hole check-convention.md predicts in its own words —
# "a non-Go edit that changes path meaning does NOT select it today" — and the
# fix it prescribes.
# lane-inputs:
#   web/**
#   ui/**
#   skill/**
#   internal/html/template.html
#   scripts/dashboard-template-drift.sh
web-quality: ## The web stack's own quality gate (npm). NOT part of `make check` — run when web/**, ui/** or skill/** changed.
	@if [ -d web/node_modules ]; then \
	  npm --prefix web run check:quality && bash scripts/dashboard-template-drift.sh; \
	else \
	  echo "web-quality: skip — web/node_modules absent (run 'npm --prefix web ci' first)."; \
	fi

lane-declarations: ## Every validation phase declares the inputs that can change its verdict, and reads only what it declared (P12).
	@bash scripts/check-lane-declarations.sh

classify-guard: ## Publish-boundary gate: no private (harness) path is tracked, DENY↔.gitignore agree.
	@bash scripts/classify-guard.sh

runner-economics: ## No job runs an expensive runner ungated, and no job's timeout-minutes is missing, under 1.5x its measured p99, or over the 60-minute cap (P13 AC4).
	@bash scripts/check-runner-economics.sh

# lane-inputs:
#   .github/workflows/**
#   go.mod
workflow-lint: ## Every GitHub Action `uses:` must be SHA-pinned (defeats tag-hijack; dependabot still bumps the pins), and no workflow pins a Go toolchain go.mod does not name.
	@bad=$$(grep -rnE 'uses: +[^ ]+@' .github/workflows 2>/dev/null | grep -vE '@[0-9a-f]{40}([ "#]|$$)' | grep -v 'uses: \./' || true); \
	if [ -n "$$bad" ]; then echo "workflow-lint: FAIL — unpinned action(s), pin to a full 40-hex SHA (# vX.Y.Z):"; echo "$$bad"; exit 1; fi; \
	echo "workflow-lint: all actions SHA-pinned."
	@grep -Fq "if: github.repository == 'ydnikolaev/a2ahub'" .github/workflows/classify-guard.yml || { echo "workflow-lint: FAIL — public classify backstop must skip the private source repository"; exit 1; }
	@grep -Eq '^  actions: read$$' .github/workflows/codeql.yml || { echo "workflow-lint: FAIL — CodeQL needs actions: read with restrictive workflow permissions"; exit 1; }
	@# A space repo has no go.mod, so the two REUSABLE workflows name the Go
	@# toolchain explicitly (`go-version: "X.Y.Z"`) while every repo-local job
	@# uses `go-version-file: go.mod`. That split is deliberate and it is also a
	@# drift seam with no reader: on 2026-08-18 `make release-preflight` refused
	@# v0.23.0 on six CALLED stdlib vulnerabilities, all fixed in go1.26.6, and
	@# bumping go.mod alone would have left every space validating and notifying
	@# on the vulnerable toolchain. Nothing would have said so.
	@gomod_go=$$(awk '$$1 == "go" && $$2 ~ /^[0-9]+\.[0-9]+\.[0-9]+$$/ { print $$2; exit }' go.mod); \
	test -n "$$gomod_go" || { echo "workflow-lint: FAIL — go.mod names no PATCH-level go directive (e.g. 'go 1.26.6'). A two-component 'go 1.27' is legal Go and this refuses it on purpose: the reusable workflows every space runs name a toolchain literally, and a govulncheck fix is a patch-level fact. Restore the patch component."; exit 1; }; \
	drift=$$(grep -rnE '^ *go-version: "[0-9]+\.[0-9]+\.[0-9]+"' .github/workflows 2>/dev/null | grep -vF "\"$$gomod_go\"" || true); \
	if [ -n "$$drift" ]; then \
	  echo "workflow-lint: FAIL — a workflow pins a Go toolchain go.mod does not name ($$gomod_go):"; \
	  echo "$$drift"; \
	  echo "  These are the REUSABLE workflows every space calls. A toolchain fix in go.mod reaches a space only through them."; \
	  exit 1; \
	fi; \
	echo "workflow-lint: every explicit go-version matches go.mod ($$gomod_go)."
	@test "$$(grep -Fc "if: github.repository == 'ydnikolaev/a2ahub'" .github/workflows/codeql.yml)" -eq 4 || { echo "workflow-lint: FAIL — all four CodeQL execution steps must remain public-repository-only"; exit 1; }
	@command -v actionlint >/dev/null 2>&1 || { echo "workflow-lint: FAIL — actionlint missing; install github.com/rhysd/actionlint/cmd/actionlint@v1.7.12"; exit 1; }
	@actionlint

gosec-scope: ## G204/G304 stay live outside the exact reviewed path allowlist.
	@bash scripts/check-gosec-scope.sh

readme-lint: ## README stays compact, current, and exits to the canonical docs.
	@bash scripts/check-readme.sh

dashboard-template-drift: ## template.html equals design source; missing Node skips locally and refuses under CI.
	@bash scripts/dashboard-template-drift.sh

dashboard-cards: ## Exactly seven registered card kinds use openCard and the one Modal engine.
	@bash scripts/check-dashboard-cards.sh

dashboard-derivation: ## Dashboard components preserve carried order/facts and keep network in DashboardLive.
	@bash scripts/check-dashboard-derivation.sh

view-vocabulary: ## No component may classify by its own list of state names, or spell one the domain does not have.
	@bash scripts/check-view-vocabulary.sh

# PRIVATE HARNESS GATE — the claim lives HERE, on the tracked recipe, because
# the script is PRIVATE_ONLY and the publisher strips it. A claim written only
# in an absent file is not a claim: the lane derivation cannot settle a phase
# whose script is gone, so it REFUSES the whole lane, and public CI on `main`
# was red from the moment P1 of dashboard-ui-restoration-2026-08 landed. Same shape and same fix as feedback-lint /
# skill-citations / feedback-corpus below; the declaration is kept in the
# script too, for the tree that has it.
# lane-inputs:
#   web/design-source/**
dashboard-props: ## No component may apply ?? or || to a prop its own data-props declaration marks required (P1, dashboard-ui-restoration-2026-08; private harness gate, presence-gated).
	@if [ -f scripts/check-dashboard-props.sh ]; then \
	  bash scripts/check-dashboard-props.sh; \
	else \
	  echo "dashboard-props: skip — scripts/check-dashboard-props.sh absent (public checkout)."; \
	fi

# PRIVATE HARNESS GATE — the claim lives HERE, on the tracked recipe, because
# the script is PRIVATE_ONLY and the publisher strips it. A claim written only
# in an absent file is not a claim: the lane derivation cannot settle a phase
# whose script is gone, so it REFUSES the whole lane, and public CI on `main`
# was red from the moment P2 landed on 2026-08-13. Same shape and same fix as feedback-lint /
# skill-citations / feedback-corpus below; the declaration is kept in the
# script too, for the tree that has it.
# lane-inputs:
#   docs/features/archive/dashboard-coherence-2026-08/card-spec/**
#   web/design-source/**
card-content: ## Card facts respect Rule A (empty-state sentence ban) and Rule C (placement/presence completeness) (P2, dashboard-ui-restoration-2026-08; private harness gate, presence-gated).
	@if [ -f scripts/check-card-content.sh ]; then \
	  bash scripts/check-card-content.sh; \
	else \
	  echo "card-content: skip — scripts/check-card-content.sh absent (public checkout)."; \
	fi

# PRIVATE HARNESS GATE — the claim lives HERE, on the tracked recipe, because
# the script is PRIVATE_ONLY and the publisher strips it. A claim written only
# in an absent file is not a claim: the lane derivation cannot settle a phase
# whose script is gone, so it REFUSES the whole lane, and public CI on `main`
# was red from the moment P7 landed. Same shape and same fix as feedback-lint /
# skill-citations / feedback-corpus below; the declaration is kept in the
# script too, for the tree that has it.
# lane-inputs:
#   .github/workflows/a2a-notify-reusable.yml
#   space-template/.github/workflows/**
notify-workflow: ## The notify workflow pair's security posture: no secrets: inherit, exact triggers, least-privilege permissions, no logic in the caller (P7; private harness gate, presence-gated).
	@if [ -f scripts/check-notify-workflow.sh ]; then \
	  bash scripts/check-notify-workflow.sh; \
	else \
	  echo "notify-workflow: skip — scripts/check-notify-workflow.sh absent (public checkout)."; \
	fi

# PRIVATE HARNESS GATE — the claim lives HERE, on the tracked recipe, because
# the script is PRIVATE_ONLY and the publisher strips it. A claim written only
# in an absent file is not a claim: the lane derivation cannot settle a phase
# whose script is gone, so it REFUSES the whole lane, and public CI on `main`
# was red from the moment P7 landed. Same shape and same fix as feedback-lint /
# skill-citations / feedback-corpus below; the declaration is kept in the
# script too, for the tree that has it.
# lane-inputs: ALWAYS
# lane-reason: invariant 1 walks the entire tracked tree for a credential shape;
#   no path glob narrows what it must re-check.
# lane-claims:
#   internal/sensitive/**
notify-secrets: ## No credential shape internal/sensitive knows appears in the tracked tree; the Telegram shape exists in exactly one place (P7; private harness gate, presence-gated).
	@if [ -f scripts/check-notify-secrets.sh ]; then \
	  bash scripts/check-notify-secrets.sh; \
	else \
	  echo "notify-secrets: skip — scripts/check-notify-secrets.sh absent (public checkout)."; \
	fi

error-codes: ## Every validation code carries prose, a production-path test and an ADR-011 mode scope — or a stated exemption (defects-fix-2026-08 P1).
	@bash scripts/check-error-codes.sh

pendency-uniqueness: ## Whose-move-is-it has ONE home; no surface may resolve its own verdict (P2 AC3/AC4).
	@bash scripts/check-pendency-uniqueness.sh

loop-coverage: ## Every (type x role x phase) loop cell is covered by a step or declared empty with a reason (P7).
	@bash scripts/check-loop-coverage.sh

human-gates: ## loops.md's human-gated verb roster equals the binary's human_gates exactly, and no step self-serves one (P7 AC4).
	@bash scripts/check-human-gates.sh

prose-roster: ## Every hand-maintained skill page is registered in all five rosters, and no generated page is (P13 AC6).
	@bash scripts/check-prose-roster.sh

# In REPO_GATES since the ledger went green. It was held out for exactly as long
# as it reded: a gate in the ceiling that cannot pass breaks every commit, and a
# gate with no recipe is what `check-lane-declarations` refuses — and did refuse,
# on this very script, which is the repo asserting this phase's own thesis
# against the phase.
prose-coverage: ## Every command, MCP tool and derived read-surface field the binary reports is taught or structurally declared (P13 AC5).
	@bash scripts/check-prose-coverage.sh

loop-reachability: ## Every Concepts/Reference/Authoring manifest page is reachable from loops.md alone, transitively (P7 AC5).
	@bash scripts/check-loop-reachability.sh

coverage: ## Same one-artifact race/coverage path as `check`, without static/vet/lint phases.
	@bash scripts/verify.sh coverage

npm-audit: ## `npm audit` over every npm tree, failing on high/critical. Needs network — NOT in `check`, same reason as vulncheck.
	@for tree in web integrations/vscode; do \
	  if [ -f "$$tree/package.json" ]; then \
	    printf 'npm-audit: %s … ' "$$tree"; \
	    if npm --prefix "$$tree" audit --audit-level=high >/tmp/a2a-npm-audit.$$$$ 2>&1; then \
	      echo "OK"; \
	    else \
	      echo "FAIL"; cat /tmp/a2a-npm-audit.$$$$; rm -f /tmp/a2a-npm-audit.$$$$; \
	      echo "npm-audit: FAIL — $$tree has high or critical advisories. Fix with: npm --prefix $$tree audit fix --package-lock-only"; \
	      exit 1; \
	    fi; \
	    rm -f /tmp/a2a-npm-audit.$$$$; \
	  else \
	    echo "npm-audit: skip — $$tree/package.json absent."; \
	  fi; \
	done

# vulncheck is a PREREQUISITE, not a copy of its logic: the allowlist gating
# lives in that recipe and stays stated once. It is here because nothing else
# reached a release. govulncheck.yml fires on schedule, on pull_request over Go
# paths, and on dispatch — never on push to main, and private main has no PR
# flow, so release work never triggers it. Measured on 2026-08-12: v0.20.0 was
# cut with the check run only because a human noticed a Dependabot banner on an
# unrelated push. It was green, so nothing shipped broken; the process was the
# defect. This is the same seam as the site-deploy assertion below it — both
# need network, both answer "is it safe to cut", and this target is where that
# question already lives.
release-preflight: vulncheck npm-audit ## MUST pass before cutting a release tag: no NEW called vulnerabilities + version free on the release remote + every space-template reusable pin resolves to a tag that carries the workflow. Needs network — NOT in `check`. Usage: make release-preflight VERSION=v0.6.0
	@test -n "$(VERSION)" || { echo "release-preflight: set VERSION, e.g. make release-preflight VERSION=v0.6.0"; exit 2; }
	@bash scripts/check-roadmap-release-decisions.sh "$(VERSION)"
	@bash scripts/release-preflight.sh "$(VERSION)"

# lane-inputs: NEVER
# lane-reason: needs the network, a published tag and a live site — it verifies
#   an OUTCOME, not a tree, so no diff can select it. Release runbook Phase 3.
release-postflight: ## MUST run AFTER promoting and tagging: the tag on the remote, the Release's state/assets/body, pages.yml's run FOR THE PROMOTED COMMIT, the live site, and the template baseline. Usage: make release-postflight VERSION=v0.24.0
	@test -n "$(VERSION)" || { echo "release-postflight: set VERSION, e.g. make release-postflight VERSION=v0.24.0"; exit 2; }
	@bash scripts/release-postflight.sh "$(VERSION)"

# THE SHIP GATE, composed. Container-primary: the container runs the FULL suite
# (GNU userland, where every non-notifier CI job actually runs — and the TRACKED
# tree, because its entrypoint syncs and `git clean`s, while the host judges a
# working tree carrying generated, gitignored files). The host runs ONLY the
# macos-15 delta, derived from `runs-on:` rather than any kept list: the two
# Swift-notifier jobs that nothing covered before 2026-08-21.
#
# It records a receipt naming the SHA it judged, under .a2a/release-gate/;
# docs/runbooks/publish-to-public.sh refuses a publish without one.
#
# NEEDS A DOCKER DAEMON, and that is now a hard requirement for cutting a
# release. There is deliberately no flag to skip it — a bare flag becomes a
# permanent silent downgrade. An unreachable daemon reports UNMEASURED (exit 3),
# never a host-only pass.
# lane-inputs: NEVER
# lane-reason: it runs the ceiling inside a container and needs a Docker daemon;
#   a ship gate, never a commit gate — the same classification ci-parity-docker
#   already carries.
release-check: ## THE SHIP GATE — the full suite in the container, the macos-15 delta on the host, and a receipt naming the SHA judged.
	@bash scripts/ci-parity.sh --release

release-check-dry: ## Rehearse `release-check`: print its members, the derived macOS jobs and the receipt path; execute nothing, write nothing.
	@bash scripts/ci-parity.sh --release --dry-run

projection: ## Judge the SHIPPED suite against a faithful public projection (release-loop-2026-08 P3, presence-gated). Not in `check`: it RUNS the ceiling, so membership would make the ceiling run the ceiling.
	@if [ -f scripts/check-projection.sh ]; then \
	  bash scripts/check-projection.sh; \
	else \
	  echo "projection: skip — scripts/check-projection.sh absent (public checkout); there is nothing to project inside a projection."; \
	fi

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

# The claim lives HERE, on the tracked recipe, and not in the gate script's own
# header — because that script is UNTRACKED (`scripts/*` is ignored behind a
# per-file negation list) and it must stay that way: it greps `docs/features`
# and `docs/backlog.md`, both of which the publisher STRIPS, so it cannot
# function in a public tree. Its recipe is presence-gated for exactly that
# reason.
#
# A claim written in an absent file is not a claim. Declaring these paths in
# the script meant they were claimed on the machine that has the harness and
# unclaimed everywhere else, so `lane-declarations` reds in CI, in a public
# checkout, and in the filtered release candidate while being green locally.
# It has been reding private CI on `main` since a562c5a6 (2026-08-09) moved
# them out of scripts/lib/lane-ungated.txt, and nothing in the release flow
# looked: release-preflight checks only pages.yml. Measured: move the script
# aside, `make lane-declarations` exits 2 with 23 unclaimed paths; move it
# back, exit 0.
#
# This is the `web-quality` shape directly above — a presence-gated recipe
# whose declaration lives on the recipe, where it survives whatever the guard
# is guarding against. The paths declared are the gate's own inputs that
# OUTLIVE the publish filter; the ones it also reads under `docs/` are claimed
# by feature-lint and are stripped from any tree where this file is absent.
#
# The FULL input set moves, not just the paths that survive the filter. Keeping
# only the surviving two here would leave the gate under-declared: it shells out
# to the built `a2a feedback validate`, so a Go change must select it, and
# declaring less than it reads is a false green in the other direction.
# lane-reason: the Go tree and module files are declared because half 1 and
#   half 2 shell out to the built `a2a feedback validate` — a change to the
#   validator changes this gate's verdict. `docs/features/**` and
#   `docs/backlog.md` are declared because check 3's tracker-row branch
#   resolves a referent by `git grep` over them: deleting the row a
#   resolution names flips this gate red, which is the whole point of the
#   referent rule. That branch can only ADD a way to resolve, so the
#   imprecision runs toward selecting the gate too often, never toward a
#   false green.
# lane-inputs:
#   feedback/inbox/**
#   feedback/backlog.yaml
#   releasenotes/**
#   docs/features/**
#   docs/backlog.md
#   **/*.go
#   go.mod
#   go.sum
feedback-corpus: ## A feedback record cannot be corrupted into the corpus, and a release cannot claim to close a report the corpus calls unread (P9; private harness gate, presence-gated).
	@if [ -f scripts/check-feedback-corpus.sh ]; then \
	  bash scripts/check-feedback-corpus.sh; \
	else \
	  echo "feedback-corpus: skip — scripts/check-feedback-corpus.sh absent (public checkout)."; \
	fi

spec-verify-refs: ## Every spec's §8 "How to verify" citation must resolve to a real path or test function (P11 wave B AC1; private harness gate, presence-gated). IN REPO_GATES since 2026-08-11, once wave B's own two findings were resolved — a gate joining the ceiling while its findings are live reds the tree for work nobody did.
	@if [ -f scripts/check-spec-verify-refs.sh ]; then \
	  bash scripts/check-spec-verify-refs.sh; \
	else \
	  echo "spec-verify-refs: skip — scripts/check-spec-verify-refs.sh absent (public checkout)."; \
	fi

feedback-sync: ## Pull the hub of record's feedback/inbox/** into this tree (P9 wave B; git-level, stages only).
	@if [ -f docs/runbooks/feedback-sync.sh ]; then \
	  bash docs/runbooks/feedback-sync.sh; \
	else \
	  echo "feedback-sync: skip — docs/runbooks/feedback-sync.sh absent (public checkout)."; \
	fi

release-notes-freshness: ## User-visible product commits cannot outrun the newest authored release notes.
	@bash scripts/check-release-notes-freshness.sh

release-record: ## The release notes, the git tags and the a2a-ref default cannot disagree (B12).
	@bash scripts/check-release-record.sh

frozen-allowlist: ## A phase PLAN's allowlist may not grant a byte-frozen schemas/published-v1.sha256 path (rules-that-reach-2026-08 P2).
	@bash scripts/check-frozen-allowlist.sh

roadmap-release-decisions: ## Every feature in the newest release notes is explicitly included in or omitted from Shipped now.
	@bash scripts/check-roadmap-release-decisions.sh

provider-tier-deferral: ## A 3rd consecutive logic-proven, provider-deferred release without an intervening live-e2e run refuses to ship.
	@bash scripts/check-provider-tier-deferral.sh

space-template-baseline-check: ## The space template's write floor and its workflow pins name one published release (release runbook Phase 4 step 15).
	@bash scripts/bump-space-template.sh --check

space-template-baseline: ## Move the space template's floor AND workflow pins to the release just tagged, from one derived version. Refuses an unpublished tag.
	@bash scripts/bump-space-template.sh

# The claim lives HERE, on the tracked recipe, for the same reason the
# feedback-corpus block above gives at length: this gate is untracked AND
# stripped, so a claim written inside it is invisible to CI, to a public
# checkout and to the release candidate. Only the opaque-read note stays
# with the script, since the constructs it describes are there.
# lane-inputs: ALWAYS
# lane-reason: check A runs `git log --no-merges --format=...` with NO pathspec and judges commit SUBJECT LINES, not the files a commit touched — any commit anywhere in history can flip the verdict regardless of which paths it changed
# lane-claims:
#   docs/status.md
#   docs/features/**/tracker.yaml
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

# The gates' own teeth are reachable from a diff, and until 2026-08-12 they
# were not. check-convention.md said "`make lane` selects it for you"; it did
# not, because this recipe carried no declaration at all — and `make check` does
# not run them either (the ceiling is `verify.sh full`; this is `verify.sh
# harness`, a different mode). So a change to a gate's OWN logic selected
# nothing that would exercise it, and the only thing standing between a broken
# gate and a green tree was somebody remembering. Found by editing
# feedback-sync.sh and noticing the derived lane did not name this target.
#
# Same shape as web-quality's block above, and the same fix. The declaration is
# the SCRIPTS, because what this target reads is their --teeth entrypoints —
# not the corpora they judge, which each gate declares for itself.
# lane-inputs:
#   scripts/**/*.sh
#   docs/runbooks/*.sh
#   .agents/scripts/*.sh
ci-parity: ## Run exactly what CI runs, locally, in CI's order (the thing `make check` alone does NOT do).
	@bash scripts/ci-parity.sh --run

ci-parity-audit: ## Refuse when a CI command is executed by no local step and carries no written excuse.
	@bash scripts/ci-parity.sh --audit

ci-parity-docker: ## The same suite under ubuntu + GNU userland, where every non-notifier CI job actually runs.
	@bash scripts/ci-parity-docker.sh

harness-check: ## Run the gates' --teeth self-tests (harness gates are private/presence-gated; release-preflight is public).
	@bash scripts/verify.sh harness

# THE TEETH ASSERT WHAT A GATE SAYS, SO THEY PIN HOW IT SAYS IT.
#
# gate-lib switches format on GITHUB_ACTIONS: `::error::`/`::warning::` on
# STDOUT under Actions, coloured `FAIL`/`WARN` on STDERR otherwise. Teeth that
# grep for the message therefore passed on every laptop and failed on the
# runner — `check-spec-verify-refs` T10 did exactly that on 2026-08-20, and it
# was found only after a push, because nothing local ran the teeth in
# annotation mode.
#
# Pinned empty here rather than fixed in that one script: a self-test asserts
# CONTENT, and the annotation format is presentation owned by CI. One line
# makes all 32 tooth scripts format-stable instead of leaving the next one to
# rediscover this. The annotation path itself is then exercised by exactly one
# dedicated tooth in scripts/verify.sh — without it, pinning here would mean
# nothing checks that `::error::` is emitted correctly at all.
# ONE GUARD, DERIVED — not seven hand-written copies plus the ones nobody wrote.
#
# Seven of these invocations used to carry their own `if [ -f ... ]` block and
# the rest did not, because the guard was added one script at a time, each time
# after that script's absence had already broken a public checkout.
# check-dashboard-props.sh and check-card-content.sh are both on the
# publisher's STRIP list and never got one: v0.24.0's THIRD candidate died in
# CI with `Error 127` on the first of them, on a tree whose own `make check`
# was green — because `candidate.sh --check-only` runs `make check` and CI runs
# `make check` AND this target.
#
# An ABSENT tooth is announced and skipped. A PRESENT tooth that fails still
# fails. Adding a script to either list below inherits the guard, so the next
# private tooth cannot repeat this — the list names WHAT to run, never how to
# survive its own absence.
HARNESS_TEETH := \
  scripts/lib/gate-lib.sh \
  scripts/check-projection.sh \
  scripts/release-postflight.sh \
  scripts/verify.sh \
  scripts/check-frozen-allowlist.sh \
  scripts/check-release-record.sh \
  scripts/ci-changes.sh \
  scripts/check-runner-economics.sh \
  scripts/feedback-intake-policy.sh \
  scripts/check-gosec-scope.sh \
  scripts/release-preflight.sh \
  scripts/check-dashboard-cards.sh \
  scripts/check-dashboard-derivation.sh \
  scripts/check-view-vocabulary.sh \
  scripts/check-dashboard-props.sh \
  scripts/check-card-content.sh \
  scripts/check-pendency-uniqueness.sh \
  scripts/check-notify-workflow.sh \
  scripts/check-notify-secrets.sh \
  scripts/check-release-notes-freshness.sh \
  scripts/check-roadmap-release-decisions.sh \
  scripts/check-provider-tier-deferral.sh \
  scripts/bump-space-template.sh \
  scripts/check-readme.sh \
  scripts/dashboard-template-drift.sh \
  docs/runbooks/publish-to-public.sh \
  docs/runbooks/feedback-sync.sh \
  scripts/check-feedback-corpus.sh \
  scripts/check-spec-verify-refs.sh \
  scripts/check-feature-lint.sh \
  .agents/scripts/epic_docs_drift.sh \
  scripts/check-skill-citations.sh \
  scripts/check-operational-confidence.sh \
  scripts/check-error-codes.sh \
  scripts/check-lane-declarations.sh \
  scripts/check-prose-roster.sh

HARNESS_TESTS := \
  scripts/tests/check_event_writer_receipts_test.sh \
  scripts/tests/check_contract_carried_set_test.sh \
  scripts/tests/check_work_checkpoint_schema_test.sh \
  scripts/tests/check_operational_projection_single_source_test.sh \
  scripts/tests/check_localserver_readonly_routes_test.sh \
  scripts/tests/check_live_e2e_evidence_test.sh \
  scripts/tests/check_human_gates_test.sh \
  scripts/tests/check_loop_reachability_test.sh \
  scripts/tests/check_loop_coverage_test.sh \
  scripts/tests/classify_guard_test.sh

_harness-check: export GITHUB_ACTIONS :=
_harness-check:
	@for s in $(HARNESS_TEETH); do \
	  if [ -f "$$s" ]; then \
	    bash "$$s" --teeth || exit 1; \
	  else \
	    echo "harness-check: skip — $$s absent (public checkout)."; \
	  fi; \
	done
	@for s in $(HARNESS_TESTS); do \
	  if [ -f "$$s" ]; then \
	    bash "$$s" || exit 1; \
	  else \
	    echo "harness-check: skip — $$s absent (public checkout)."; \
	  fi; \
	done
