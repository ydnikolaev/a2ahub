package html

import (
	"bufio"
	"bytes"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const repositoryImportPrefix = "github.com/ydnikolaev/a2ahub/"

func TestImportBoundaryMatchesADR001(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate import boundary test source")
	}
	packageDir := filepath.Dir(filename)
	packageName := "internal/" + filepath.Base(packageDir)

	pkg, err := build.Default.ImportDir(packageDir, 0)
	if err != nil {
		t.Fatalf("inspect production imports for %s: %v", packageName, err)
	}
	decisions, err := os.ReadFile(filepath.Join(packageDir, "..", "..", "docs", "decisions.md"))
	if err != nil {
		t.Fatalf("read ADR-001 for %s: %v", packageName, err)
	}
	expected, err := importsFromADR001(decisions, packageName)
	if err != nil {
		t.Fatal(err)
	}

	actual := make([]string, 0, len(pkg.Imports))
	for _, importPath := range pkg.Imports {
		relative, found := strings.CutPrefix(importPath, repositoryImportPrefix)
		if !found {
			continue
		}
		actual = append(actual, strings.TrimPrefix(relative, "internal/"))
	}

	missing, extra := importSetDifference(expected, actual)
	if len(missing) != 0 || len(extra) != 0 {
		t.Errorf("%s import boundary drift: missing production imports %v; extra production imports %v", packageName, missing, extra)
	}
}

func importsFromADR001(decisions []byte, packageName string) ([]string, error) {
	prefix := "| `" + packageName + "/` |"
	scanner := bufio.NewScanner(bytes.NewReader(decisions))
	inADR001 := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## ADR-001 ") {
			inADR001 = true
			continue
		}
		if inADR001 && strings.HasPrefix(line, "## ADR-") {
			break
		}
		if !inADR001 || !strings.HasPrefix(line, prefix) {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			return nil, fmt.Errorf("parse ADR-001 row for %s: want a three-column table row", packageName)
		}
		quoted := strings.Split(cells[3], "`")
		imports := make([]string, 0, len(quoted)/2)
		for i := 1; i < len(quoted); i += 2 {
			imports = append(imports, quoted[i])
		}
		return imports, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan ADR-001 for %s: %w", packageName, err)
	}
	return nil, fmt.Errorf("find ADR-001 row for %s", packageName)
}

func importSetDifference(expected, actual []string) (missing, extra []string) {
	expectedSet := make(map[string]struct{}, len(expected))
	actualSet := make(map[string]struct{}, len(actual))
	for _, importPath := range expected {
		expectedSet[importPath] = struct{}{}
	}
	for _, importPath := range actual {
		actualSet[importPath] = struct{}{}
	}
	for importPath := range expectedSet {
		if _, found := actualSet[importPath]; !found {
			missing = append(missing, importPath)
		}
	}
	for importPath := range actualSet {
		if _, found := expectedSet[importPath]; !found {
			extra = append(extra, importPath)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
