package lane

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFixture1(t *testing.T) {
	decls, err := Load("testdata/fixture1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byPhase := map[string]Declaration{}
	for _, d := range decls {
		byPhase[d.Phase] = d
	}

	rl, ok := byPhase["readme-lint"]
	if !ok {
		t.Fatalf("no declaration for readme-lint, got %v", byPhase)
	}
	if rl.Kind != KindScoped || len(rl.Inputs) != 1 || rl.Inputs[0] != "README.md" {
		t.Errorf("readme-lint = %+v", rl)
	}
	if rl.Source != "scripts/check-readme.sh:4" {
		t.Errorf("readme-lint Source = %q", rl.Source)
	}

	cg, ok := byPhase["classify-guard"]
	if !ok {
		t.Fatalf("no declaration for classify-guard")
	}
	if cg.Kind != KindAlways || cg.Reason == "" {
		t.Errorf("classify-guard = %+v", cg)
	}

	bc, ok := byPhase["build-cli"]
	if !ok {
		t.Fatalf("no declaration for build-cli")
	}
	if bc.Kind != KindScoped || len(bc.Inputs) != 3 {
		t.Errorf("build-cli = %+v", bc)
	}
	if bc.Source != "scripts/verify.sh:18" {
		t.Errorf("build-cli Source = %q", bc.Source)
	}

	vet, ok := byPhase["vet"]
	if !ok {
		t.Fatalf("no declaration for vet")
	}
	if vet.Kind != KindScoped || len(vet.Inputs) != 3 {
		t.Errorf("vet = %+v", vet)
	}

	if _, ok := byPhase["gofmt"]; ok {
		t.Errorf("gofmt has no lane-inputs block in the fixture, should not be in decls")
	}
}

func TestLoadRejectsUnresolvableScriptHeader(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile", "REPO_GATES := classify-guard\n\nclassify-guard:\n\t@bash scripts/classify-guard.sh\n")
	writeFixture(t, dir, "scripts/classify-guard.sh", "#!/usr/bin/env bash\nset -euo pipefail\n")
	writeFixture(t, dir, "scripts/verify.sh", "#!/usr/bin/env bash\nset -euo pipefail\n")
	writeFixture(t, dir, "scripts/orphan.sh", "#!/usr/bin/env bash\n# lane-inputs:\n#   README.md\nset -euo pipefail\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected an error: orphan.sh declares lane-inputs but backs no corpus phase")
	}
}

func TestLoadRejectsDuplicatePhaseDeclaration(t *testing.T) {
	dir := t.TempDir()
	// A lane-inputs block directly above the classify-guard TARGET, on top
	// of the one the script header ALREADY carries — the same phase name
	// declared twice, which is exactly the ambiguity validateAll exists to
	// catch (findLaneBlocks works one file at a time and cannot see it).
	writeFixture(t, dir, "Makefile", "REPO_GATES := classify-guard\n\n# lane-inputs: ALWAYS\n# lane-reason: duplicate on purpose, to prove the ambiguity is caught\nclassify-guard:\n\t@bash scripts/classify-guard.sh\n")
	writeFixture(t, dir, "scripts/classify-guard.sh", "#!/usr/bin/env bash\n# lane-inputs: ALWAYS\n# lane-reason: reads git ls-files over the whole tracked set\nset -euo pipefail\n")
	writeFixture(t, dir, "scripts/verify.sh", "#!/usr/bin/env bash\nset -euo pipefail\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected an error: classify-guard is declared twice (Makefile target block AND script header)")
	}
}

func writeFixture(t *testing.T, root, relPath, content string) {
	t.Helper()
	path := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
