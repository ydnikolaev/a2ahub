// Package spacetemplate embeds the space-template/ tree so `a2a space init`
// (internal/cli's SpaceCommand, spec 33 §12) can scaffold a new space repo
// from it directly, without an operator hand-copy step. Mirrors
// skill/embed.go's own embed+DI pattern: this file is the one narrow
// exception to ADR-001's "space-template/ is data, no Go imports" rule — a
// thin embed-only shim, no logic.
package spacetemplate

import "embed"

// Files is the embedded space-template/ tree (BRANCH-PROTECTION.md,
// CODEOWNERS, README.md, space.yaml, decisions/.gitkeep, vendored/.gitkeep,
// .github/workflows/a2a-validate.yml, .github/dependabot.yml).
//
// `//go:embed` skips dot-prefixed entries unless the pattern uses the
// `all:` prefix, and `all:*` alone still will NOT match `.github` (a
// literal dot-directory) — it must be named explicitly, with its own
// `all:` prefix so the dotfiles beneath it (none today, but future-proof)
// and the directory itself survive. `decisions/` and `vendored/` carry
// only a `.gitkeep` each, so they need the same `all:` treatment or their
// sole file — and the now-empty directory — silently vanish from Files.
//
//go:embed all:.github BRANCH-PROTECTION.md CODEOWNERS README.md space.yaml all:decisions all:vendored
var Files embed.FS
