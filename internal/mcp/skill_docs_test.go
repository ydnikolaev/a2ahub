package mcp

import (
	"os"
	"strings"
	"testing"
)

func TestSkillDescribesMCPPreCallFreshness(t *testing.T) {
	t.Parallel()
	paths := []string{
		"../../skill/a2ahub/SKILL.md",
		"../../skill/a2ahub/troubleshooting.md",
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := string(raw)
		for _, stale := range []string{
			"MCP read tools do not refresh a stale mirror",
			"built at startup and never refreshed",
			"restart the MCP session",
		} {
			if strings.Contains(doc, stale) {
				t.Errorf("%s still carries the closed MCP freshness limit %q", path, stale)
			}
		}
		if !strings.Contains(doc, "pre-call mirror refresh failed") {
			t.Errorf("%s does not name the server's honest last-good-view warning", path)
		}
	}
}
