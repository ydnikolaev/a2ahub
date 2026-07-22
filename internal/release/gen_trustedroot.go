//go:build ignore

// gen_trustedroot.go regenerates the frozen Sigstore trusted root that
// KeylessCosignVerifier embeds (cosign.go: //go:embed trusted_root.json).
//
// It fetches the `trusted_root.json` TUF TARGET from the Sigstore public-good
// instance and writes it verbatim. tuf.DefaultOptions() pins the public-good
// TUF bootstrap root, so the fetched target is TUF-VERIFIED — a tampered CDN
// cannot substitute a hostile trusted root here. The embedded copy is what
// makes verify-time signature checking fully OFFLINE (no TUF/network at
// `a2a update`); the cost is that WE own its rotation.
//
// Rotate (rare — only when Sigstore rotates Fulcio/Rekor/CT keys):
//
//	go run ./internal/release/gen_trustedroot.go
//
// then commit the updated internal/release/trusted_root.json and re-run the
// keyless verifier tests against a freshly-signed asset.
//
//go:generate go run gen_trustedroot.go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sigstore/sigstore-go/pkg/tuf"
)

func main() {
	client, err := tuf.New(tuf.DefaultOptions())
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen_trustedroot: TUF client: %v\n", err)
		os.Exit(1)
	}
	raw, err := client.GetTarget("trusted_root.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen_trustedroot: GetTarget(trusted_root.json): %v\n", err)
		os.Exit(1)
	}

	// Write beside this generator (internal/release/), regardless of cwd.
	out := "internal/release/trusted_root.json"
	if wd, _ := os.Getwd(); filepath.Base(wd) == "release" {
		out = "trusted_root.json"
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gen_trustedroot: write %s: %v\n", out, err)
		os.Exit(1)
	}
	fmt.Printf("gen_trustedroot: wrote %s (%d bytes, TUF-verified)\n", out, len(raw))
}
