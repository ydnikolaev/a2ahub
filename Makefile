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

.PHONY: site-check site-check-teeth flaky-scan check test check-validators ci-parity ci-parity-audit ci-parity-docker frozen-allowlist lane lane-run lane-declarations web-quality error-codes _print-repo-gates dashboard-template-drift dashboard-cards dashboard-derivation feature-lint epic-drift operational-confidence-guard event-writer-receipts contract-carried-set work-checkpoint-schema operational-projection-single-source localserver-readonly-routes skill-citations cross-surface-citations feedback-corpus spec-verify-refs feedback-sync view-vocabulary pendency-uniqueness notify-workflow notify-secrets notify-selector-coverage mcp-schema-decodable refusal-ratchet vocabulary-carriers deadcode-ceiling discard-ceiling release-note-detect render-ledger usage-workflow error-codes loop-coverage human-gates loop-reachability prose-roster prose-coverage release-notes-freshness release-record roadmap-release-decisions provider-tier-deferral unmeasured-reach space-template-baseline space-template-baseline-check readme-lint classify-guard workflow-lint gosec-scope harness-check _harness-check coverage vulncheck release-preflight release-postflight projection release-check release-check-dry live-e2e live-e2e-evidence logic-e2e install import-rule-coverage cross-layer-test-import-ceiling verdict-exit-mapping harness-roster plugin-manifests

# ONE list, consumed by both `check` (the ceiling) and `check-validators` (the
# static lane). Two hand-kept copies of a gate list drift, and the drift is
# invisible: a copy quietly stops running a gate while still printing green.
#
# classify-guard + workflow-lint are PRODUCT gates (always run, committed public).
# feature-lint/epic-drift are PRIVATE harness gates: their scripts live under
# the mate-managed harness (scripts/check-feature-lint.sh, .agents/scripts/
# epic_docs_drift.sh) and are absent on a public checkout — each target below
# presence-gates itself so `make check` never hard-fails on their absence.
REPO_GATES := spec-verify-refs ci-parity-audit frozen-allowlist lane-declarations classify-guard workflow-lint gosec-scope readme-lint dashboard-cards dashboard-derivation feature-lint epic-drift operational-confidence-guard event-writer-receipts contract-carried-set work-checkpoint-schema operational-projection-single-source localserver-readonly-routes skill-citations cross-surface-citations feedback-corpus view-vocabulary dashboard-props card-content pendency-uniqueness notify-workflow notify-secrets notify-selector-coverage mcp-schema-decodable refusal-ratchet vocabulary-carriers deadcode-ceiling discard-ceiling release-note-detect render-ledger usage-workflow error-codes loop-coverage human-gates loop-reachability prose-roster prose-coverage release-notes-freshness release-record roadmap-release-decisions provider-tier-deferral runner-economics space-template-baseline-check unmeasured-reach import-rule-coverage cross-layer-test-import-ceiling verdict-exit-mapping harness-roster plugin-manifests

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
# TWO WAYS THIS GATE USED TO NOT MEASURE, AND ONLY ONE OF THEM WAS RED
# (release-cost-2026-08 P3, and both are named in release.md step 8).
#
#   * ABSENT `web/node_modules` printed "skip" and EXITED ZERO. A release step
#     whose answer is "I could not run" answered "fine". That is the class the
#     UNMEASURED vocabulary exists for and the one a grep cannot find, because
#     a gate returning green leaves nothing to search for — this one was found
#     by reading the recipe while writing the gate that cannot see it.
#   * PLAYWRIGHT WITH NO BROWSER exits 1, byte-identical to a real a11y
#     failure. On 2026-08-22 that cost a round of "the site is broken" over
#     fourteen tests reporting `Executable doesn't exist`. The distinction is
#     recoverable from the output, so it is recovered rather than assumed.
#
# The rc is carried through a file rather than a pipeline status: this
# Makefile's shell is /bin/sh, which has no `pipefail`, and `if cmd | tee`
# reads tee's status — the same class of misread that reported a red web lane
# as green on 2026-08-05 and cost the site four releases.
# lane-inputs:
#   web/**
#   ui/**
#   skill/**
#   internal/html/template.html
#   scripts/dashboard-template-drift.sh
web-quality: ## The web stack's own quality gate (npm). NOT part of `make check` — run when web/**, ui/** or skill/** changed.
	@if [ ! -d web/node_modules ]; then \
	  bash scripts/lib/gate-lib.sh --unmeasured "web-quality: web/node_modules is absent, so NOTHING was measured — not a11y, not Lighthouse, not the dashboard template drift. This is NOT a pass. Run 'npm --prefix web ci' and try again."; \
	fi; \
	log=$$(mktemp); rcf=$$(mktemp); \
	{ npm --prefix web run check:quality; echo $$? >"$$rcf"; } 2>&1 | tee "$$log"; \
	rc=$$(cat "$$rcf"); rm -f "$$rcf"; \
	if [ "$$rc" -ne 0 ] && grep -qE "Executable doesn't exist|playwright install" "$$log"; then \
	  rm -f "$$log"; \
	  bash scripts/lib/gate-lib.sh --unmeasured "web-quality: Playwright has no browser installed, so the a11y suite DID NOT RUN — this exit code is about the machine, not about the site. Run 'npx --prefix web playwright install chromium' and re-run; only then is a red here a verdict."; \
	fi; \
	rm -f "$$log"; \
	[ "$$rc" -eq 0 ] || exit "$$rc"; \
	bash scripts/dashboard-template-drift.sh

lane-declarations: ## Every validation phase declares the inputs that can change its verdict, and reads only what it declared (P12).
	@bash scripts/check-lane-declarations.sh

classify-guard: ## Publish-boundary gate: no private (harness) path is tracked, DENY↔.gitignore agree.
	@bash scripts/classify-guard.sh

runner-economics: ## No job runs an expensive runner ungated, and no job's timeout-minutes is missing, under 1.5x its measured p99, or over the 60-minute cap (P13 AC4).
	@bash scripts/check-runner-economics.sh

# lane-inputs:
#   .github/workflows/**
#   go.mod
#   scripts/**
workflow-lint: ## Every GitHub Action `uses:` must be SHA-pinned (defeats tag-hijack; dependabot still bumps the pins), no workflow pins a Go toolchain go.mod does not name, and a script a workflow RUNS by bare path is executable.
	@bad=$$(grep -rnE 'uses: +[^ ]+@' .github/workflows 2>/dev/null | grep -vE '@[0-9a-f]{40}([ "#]|$$)' | grep -v 'uses: \./' || true); \
	if [ -n "$$bad" ]; then echo "workflow-lint: FAIL — unpinned action(s), pin to a full 40-hex SHA (# vX.Y.Z):"; echo "$$bad"; exit 1; fi; \
	echo "workflow-lint: all actions SHA-pinned."
	@grep -Fq "if: github.repository == 'ydnikolaev/a2ahub'" .github/workflows/classify-guard.yml || { echo "workflow-lint: FAIL — public classify backstop must skip the private source repository"; exit 1; }
	@# A script a workflow RUNS BY BARE PATH must be executable IN THE INDEX.
	@# scripts/build-mcpb.sh was tracked 100644 while release.yml called it
	@# bare. The cohort job exited 126 "Permission denied" — on the TAG, after
	@# v0.26.0 was already immutable — so that release shipped with no
	@# release-cohort.json, no release-notes asset and no .mcpb bundle, which
	@# was the epic's own deliverable. Nothing local runs a tag-only job, so
	@# the MODE is the only part of it that is checkable before the tag, and
	@# checking it is a grep. An interpreter-prefixed call is excluded (a call
	@# through bash, sh, or the dot builtin needs no bit) and so is a YAML
	@# path-filter list entry, which invokes nothing.
	@nx=$$(grep -rhE '(^|[^-])(\./)?scripts/[A-Za-z0-9_./-]+\.sh' .github/workflows/*.yml 2>/dev/null \
	  | sed -E 's/^[[:space:]]*//' | grep -vE '^- ' \
	  | grep -oE '(bash|sh|source|\.)?[[:space:]]*(\./)?scripts/[A-Za-z0-9_./-]+\.sh' \
	  | grep -vE '^(bash|sh|source|\.)[[:space:]]' \
	  | grep -oE 'scripts/[A-Za-z0-9_./-]+\.sh' | sort -u \
	  | while read -r f; do \
	      m=$$(git ls-files -s "$$f" 2>/dev/null | awk '{print $$1}'); \
	      [ -n "$$m" ] || continue; \
	      [ "$$m" = "100755" ] || echo "$$f (tracked $$m)"; \
	    done); \
	if [ -n "$$nx" ]; then echo "workflow-lint: FAIL — a workflow runs these tracked scripts by bare path, but they are not executable (100755). A bare-path call exits 126 on the runner:"; echo "$$nx"; exit 1; fi; \
	echo "workflow-lint: every script a workflow runs by bare path is executable."

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
# AN ABSENT TOOL IS NEVER A FINDING (release-cost-2026-08 P3). This said FAIL,
# which claims the workflows were linted and found wanting; what actually
# happened is that nothing was linted. Same rule the gate next door already
# followed, in the gate that lints the workflows CI runs.
	@command -v actionlint >/dev/null 2>&1 || \
	  bash scripts/lib/gate-lib.sh --unmeasured "workflow-lint: actionlint is not on PATH, so THE WORKFLOWS WERE NOT LINTED. Nothing was found wrong with them. Install it: go install github.com/rhysd/actionlint/cmd/actionlint@v1.7.12"
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

notify-selector-coverage: ## Every fold.Kind and fold.State is selectable by a notify route, or carries a written reason; no second kind/state vocabulary exists in internal/spacenotify (answers-that-hold-2026-08 P11, ACs 11-13).
	@bash scripts/check-notify-selector-coverage.sh

mcp-schema-decodable: ## Every property a published MCP tool schema declares maps to a field the tool's input struct decodes (answers-that-hold-2026-08 P12).
	@bash scripts/check-mcp-schema-decodable.sh

refusal-ratchet: ## No NEW raw err-to-stderr site appears in internal/cli outside the three-part refusal constructor; the per-file budget may only shrink (answers-that-hold-2026-08 P4, ACs 6-8).
	@bash scripts/check-refusal-ratchet.sh

vocabulary-carriers: ## A struct field named for a schema-closed vocabulary and typed plain string reds; the carrier ceiling may only fall (computed-not-listed-2026-08 P3).
	@bash scripts/check-vocabulary-carriers.sh

verdict-exit-mapping: ## Every JSON-carrying verb declares its verdict->exit-code mapping, and the declaration agrees with the verb (computed-not-listed-2026-08 P4).
	@bash scripts/check-verdict-exit-mapping.sh

deadcode-ceiling: ## The unreachable-symbol count is a seeded ceiling that may only fall (computed-not-listed-2026-08 P5).
	@bash scripts/check-deadcode-ceiling.sh

discard-ceiling: ## The undocumented error-discard count is a seeded ceiling that may only fall (computed-not-listed-2026-08 P8).
	@bash scripts/check-discard-ceiling.sh

import-rule-coverage: ## depguard's two structural blind spots — an ADR-001 row with no rule, an allow entry no file uses (computed-not-listed-2026-08 P2; private harness gate, presence-gated — it reads ADR-001's table in docs/, which the publisher removes).
	@if [ -f scripts/check-import-rule-coverage.sh ]; then \
	  bash scripts/check-import-rule-coverage.sh; \
	else \
	  echo "import-rule-coverage: skip — scripts/check-import-rule-coverage.sh absent (public checkout)."; \
	fi

cross-layer-test-import-ceiling: ## The `!$$test` depguard exemption's reach is a seeded ceiling that may only fall (computed-not-listed-2026-08 P2; private harness gate, presence-gated — it sources the gate above).
	@if [ -f scripts/check-cross-layer-test-import-ceiling.sh ]; then \
	  bash scripts/check-cross-layer-test-import-ceiling.sh; \
	else \
	  echo "cross-layer-test-import-ceiling: skip — scripts/check-cross-layer-test-import-ceiling.sh absent (public checkout)."; \
	fi

harness-roster: ## Every script that dispatches on --teeth is a HARNESS_TEETH member or carries a written excuse (computed-not-listed-2026-08 P9; private harness gate, presence-gated — it audits PRIVATE harness scripts, most of which the publisher strips).
	@if [ -f scripts/check-harness-roster.sh ]; then \
	  bash scripts/check-harness-roster.sh; \
	else \
	  echo "harness-roster: skip — scripts/check-harness-roster.sh absent (public checkout)."; \
	fi

release-note-detect: ## A new release-note change declaring scope local|space carries a detect:, or a counted exemption row; the frozen historical ceiling may only shrink (answers-that-hold-2026-08 P13, ACs 13-14).
	@bash scripts/check-release-note-detect.sh

usage-workflow: ## Every verb with a workflow page names its topic on a bare invocation, and every named topic resolves (answers-that-hold-2026-08 P8).
	@bash scripts/check-usage-workflow.sh

# The lane-inputs declaration for this phase lives in the gate script's own
# header, where it travels with the script into a public checkout. One phase,
# one declaration -- `lane-declarations` refuses two by name.
plugin-manifests: ## Every plugin manifest's launch line, description, skill tree and credential guidance agrees with the binary rather than with another manifest (built-not-listed-2026-08 P6).
	@bash scripts/check-plugin-manifests.sh

render-ledger: ## Every field the contract read surfaces compute is declared human-rendered or json-only with a structural reason (answers-that-hold-2026-08 P3, spec 03 §T1).
	@bash scripts/check-render-ledger.sh

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

# THE MECHANISM THIS REPOSITORY HAD NONE OF UNTIL 2026-08-22. The suite ran
# once per gate; nothing anywhere asked whether a test agrees with itself. Two
# timing-sensitive tests reached a tagged release and were both found by
# accident on CI afterwards. `ci-parity`/`ci-parity-docker` could not have
# caught either — they answer "is the environment the same?", correctly, and
# no amount of environment fidelity answers "does this test always pass?".
#
# lane-inputs: NEVER
# lane-reason: it runs the whole Go suite N times; a ship-lane and on-demand
#   instrument, never a commit gate — the classification ci-parity-docker and
#   release-check already carry.
flaky-scan: ## Run the Go suite N times (FLAKY_COUNT, default 3) and refuse a test that disagrees with itself.
	@bash scripts/check-flaky-tests.sh

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

# THE TWO UNMEASURED PATHS BELOW GO THROUGH gate-lib, and until 2026-08-23 they
# did not (release-cost-2026-08 P3). Both were wrong, in opposite directions:
#
#   * a MISSING govulncheck binary exited 1 — a could-not-measure reported as a
#     measured failure, which is the one rule gate-lib's own header states. It
#     matters more here than almost anywhere: govulncheck.yml's previous step is
#     `go install golang.org/x/vuln/cmd/govulncheck@latest`, UNPINNED, so an
#     install that fails produces a vulnerability-shaped red.
#   * the incomplete-scan path printed a careful, correct sentence with a plain
#     `echo`, so under GITHUB_ACTIONS there was no `::error::` annotation and
#     the run showed `Process completed with exit code 2` and nothing else. A
#     reader seeing a red `govulncheck` on a release push could not tell a
#     network hiccup from a called CVE without opening the log — the 2026-08-22
#     gitleaks incident, in the gate next door.
#
# `gate_unmeasured` emits `::error::UNMEASURED: …` on stdout under
# GITHUB_ACTIONS and plain text otherwise, which is the only channel that
# survives: a step has two outcomes and make collapses exit 3 into its own 2.
vulncheck: ## govulncheck ./... gated by .govulncheck-allow.txt (NEW called vuln reds; accepted stays green). Needs network — NOT in `check`.
	@command -v govulncheck >/dev/null 2>&1 || \
	  bash scripts/lib/gate-lib.sh --unmeasured "vulncheck: govulncheck is not on PATH, so NO SCAN RAN. This is a fact about the runner, not about the code — it is NOT a clean scan and NOT a finding. Install it: go install golang.org/x/vuln/cmd/govulncheck@latest"
	@out=$$(govulncheck ./... 2>&1); rc=$$?; \
	found=$$(printf '%s\n' "$$out" | grep -oE 'GO-[0-9]{4}-[0-9]+' | sort -u); \
	if [ "$$rc" -ne 0 ] && [ -z "$$found" ]; then \
	  printf '%s\n' "$$out"; echo; \
	  bash scripts/lib/gate-lib.sh --unmeasured "vulncheck: govulncheck exited $$rc and reported no GO-id at all, so THE SCAN DID NOT COMPLETE (network, module resolution, toolchain) and nothing was judged. This is NOT a clean scan and NOT a called vulnerability; re-run the job."; \
	fi; \
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

cross-surface-citations: ## ADR-019 (2026-08-27) detection half: a comment in one surface may not cite the other surface's symbol by name — the duplicated-RULE shape no `var _` assertion can catch (presence-gated).
	@if [ -f scripts/check-cross-surface-citations.sh ]; then \
	  bash scripts/check-cross-surface-citations.sh; \
	else \
	  echo "cross-surface-citations: skip — scripts/check-cross-surface-citations.sh absent (public checkout)."; \
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

# NO PRESENCE GATE, deliberately: this script is PUBLIC (classify-guard
# ALLOW_FILES), because the class it polices — a red job whose check never ran —
# is a public-CI concern and the workflows it reads ship. If it is ever made
# private, it needs the `[ -f ]` guard every private gate above carries.
unmeasured-reach: ## A gate that could not measure must SAY SO where a reader looks (release-cost-2026-08 P3).
	@bash scripts/check-unmeasured-reach.sh


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

# Until 2026-08-21 this recipe silently owned `harness-check`'s declaration.
# The three `ci-parity*` recipes were inserted BETWEEN that comment block and
# the target it described, and the extractor reads POSITION — so the block whose
# prose says "what this target reads is their --teeth entrypoints" came to
# select the full local CI suite on every shell-script commit. Nobody ever paid
# for it, because `verify.sh lane-run` was silently dropping any derived phase
# absent from its hand-written order: two defects that concealed each other.
#
# A comment block is attached by ADJACENCY, so inserting a target above another
# target's declaration silently reassigns it. That is a hazard of the form, not
# of this Makefile, and it argues for every recipe carrying its own.
# lane-inputs: NEVER
# lane-reason: it runs the WHOLE of what CI runs — the ceiling, the teeth, the
#   strict lane, the web suites, the browser contract and both gitleaks scans,
#   in CI's order. A ship gate, never a commit gate, and a PHASE of
#   `make release-check` rather than a step anyone sequences: the same
#   classification `ci-parity-docker` and `release-check` already carry.
ci-parity: ## Run exactly what CI runs, locally, in CI's order (the thing `make check` alone does NOT do).
	@bash scripts/ci-parity.sh --run

ci-parity-audit: ## Refuse when a CI command is executed by no local step and carries no written excuse.
	@bash scripts/ci-parity.sh --audit

ci-parity-docker: ## The same suite under ubuntu + GNU userland, where every non-notifier CI job actually runs.
	@bash scripts/ci-parity-docker.sh

# lane-inputs: NEVER
# lane-reason: it fetches `public/main` to define the overlay it judges, so it
#   needs the network and a configured public remote — no diff may select it.
#   It is the SITE PUBLISH's own gate, reached by its own target and by
#   `publish-to-public.sh --site`, never by a commit lane.
#   PRESENCE-GATED, and the guard is not decoration: this script is
#   PRIVATE_ONLY and the publisher strips it, so `make check` inside a public
#   projection reaches a declared phase whose script is gone and the lane
#   derivation's honesty pass REFUSES THE WHOLE LANE rather than that one gate.
#   Found by `make projection` inside the v0.25.6 ship gate — the first release
#   cut after this gate was written, and the first time a projection had ever
#   judged a tree containing it. Same shape and same fix as card-content /
#   feedback-corpus / skill-citations above.
site-check: ## THE SITE GATE — the derived lane over the overlay diff, gitleaks, and the site built inside the overlay. Writes .a2a/site-gate/<sha>.json. Private harness gate, presence-gated.
	@if [ -f scripts/site-check.sh ]; then \
	  bash scripts/site-check.sh; \
	else \
	  echo "site-check: skip — scripts/site-check.sh absent (public checkout)."; \
	fi

site-check-teeth: ## site-check's own self-tests. Private harness gate, presence-gated.
	@if [ -f scripts/site-check.sh ]; then \
	  bash scripts/site-check.sh --teeth; \
	else \
	  echo "site-check-teeth: skip — scripts/site-check.sh absent (public checkout)."; \
	fi

# The gates' own teeth are reachable from a diff, and until 2026-08-12 they
# were not. check-convention.md said "`make lane` selects it for you"; it did
# not, because this recipe carried no declaration at all — and `make check` does
# not run them either (the ceiling is `verify.sh full`; this is `verify.sh
# harness`, a different mode). So a change to a gate's OWN logic selected
# nothing that would exercise it, and the only thing standing between a broken
# gate and a green tree was somebody remembering. Found by editing
# feedback-sync.sh and noticing the derived lane did not name this target.
#
# THE DECLARATION FOR THIS LIVES IN scripts/verify.sh's `harness` mode, on the
# `harness-teeth` phase this target invokes — not here. A copy here would be a
# SECOND derivable phase doing the identical work: `make lane --plan` on a
# shell-script change would select `harness-check` AND `harness-teeth` and pay
# the ~173 s teeth twice. Measured on 2026-08-22, the moment this block was
# moved back onto its own target after years above the wrong one.
#
# So the history above is kept for its lesson and the declaration is not
# duplicated: a target that merely dispatches to a declared phase declares
# nothing of its own.
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
#
# STAYS A HAND-TYPED LIST — never derived the way HARNESS_TESTS above now is
# (computed-not-listed-2026-08 P9 spec 11 §2). A derived membership predicate
# here can be wrong in the direction that produces a FALSE GREEN: a script
# that does not actually dispatch on --teeth still exits 0 when run as
# `bash script --teeth` (it just ignores the flag and runs its ordinary
# check), so a vacuous tooth is indistinguishable from a passing one —
# over-inclusion here is a lie, where under-inclusion is only a measurable
# gap. So this list is instead GATED: `make harness-roster` (scripts/check-
# harness-roster.sh) computes the same "does this script dispatch on
# --teeth" universe this comment describes by hand and refuses when a
# dispatching script is absent from here with no written excuse — the
# `ci-parity-audit` shape (a set difference, named), never a stored count.
#
# Fourteen entries below were added in the SAME commit that shipped that
# gate, all measured (never copied from a stale doc) and all individually
# confirmed to pass their own `bash <script> --teeth`: the twelve
# scripts/check-cross-layer-test-import-ceiling.sh through
# scripts/ci-parity.sh, plus docs/runbooks/feedback-carry.sh (found only by
# re-deriving the universe rather than trusting spec 11 §1's own table, per
# that spec's §11 amendment) and scripts/check-harness-roster.sh itself —
# the new gate found its own script unrostered on its first real run.
HARNESS_TEETH := \
  scripts/site-check.sh \
  scripts/check-plugin-manifests.sh \
  scripts/check-verdict-exit-mapping.sh \
  scripts/check-flaky-tests.sh \
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
  scripts/check-unmeasured-reach.sh \
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
  scripts/check-cross-surface-citations.sh \
  scripts/check-operational-confidence.sh \
  scripts/check-error-codes.sh \
  scripts/check-lane-declarations.sh \
  scripts/check-notify-selector-coverage.sh \
  scripts/check-mcp-schema-decodable.sh \
  scripts/check-refusal-ratchet.sh \
  scripts/check-release-note-detect.sh \
  scripts/check-prose-roster.sh \
  scripts/check-cross-layer-test-import-ceiling.sh \
  scripts/check-deadcode-ceiling.sh \
  scripts/check-discard-ceiling.sh \
  scripts/check-human-gates.sh \
  scripts/check-import-rule-coverage.sh \
  scripts/check-loop-coverage.sh \
  scripts/check-loop-reachability.sh \
  scripts/check-prose-coverage.sh \
  scripts/check-render-ledger.sh \
  scripts/check-usage-workflow.sh \
  scripts/check-vocabulary-carriers.sh \
  scripts/ci-parity.sh \
  docs/runbooks/feedback-carry.sh \
  scripts/check-harness-roster.sh \
  scripts/check-mcp-publish-preconditions.sh

# DERIVED, not hand-typed (computed-not-listed-2026-08 P9, AC-1). Every file
# under scripts/tests/*_test.sh is a teeth test BY CONSTRUCTION — it is what
# the directory is for, it takes no argument, and it is run as `bash "$$s"`
# below. A wildcard cannot over-include the way a hand-typed HARNESS_TEETH
# entry could (see that variable's own header for why THAT one stays a
# list): there is no shape a file could take under this directory that would
# make deriving membership from its mere presence wrong. Three files were
# found missing from the old hand-typed roster the day this was written —
# check_render_ledger_test.sh, check_skill_citations_test.sh,
# check_usage_workflow_test.sh — and a fourth was added by hand to BOTH
# rosters on the same day this spec was filed about exactly that
# (check_verdict_exit_mapping_test.sh, spec 11 §11's amendment). This line
# is what makes the next one unnecessary.
HARNESS_TESTS := $(sort $(wildcard scripts/tests/*_test.sh))

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
