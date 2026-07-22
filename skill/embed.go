// Package skill embeds the a2ahub expert-skill tree so the `a2a` binary can
// install it into a consumer repo (`a2a skill install`). The binary is the
// distribution vector (§8.8 "non-mate / Codex → product-repo release"): the
// embedded copy ships with every release and self-updates with the binary, and
// its generated reference files (reference/commands.md, reference/authoring/*)
// are the same drift-gated artifacts this repo commits — so the embedded tree
// is guaranteed in sync with the binary's own catalog.
package skill

import "embed"

// Files is the embedded a2ahub skill tree, rooted at "a2ahub/" (a2ahub/SKILL.md,
// a2ahub/loops.md, a2ahub/reference/**, …). Consumers walk it via io/fs.
//
//go:embed all:a2ahub
var Files embed.FS
