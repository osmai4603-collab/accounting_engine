package standards

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSPIHasNoLegacyOrInternalImports enforces the P8-01 boundary: the public
// standards contract must stay implementation-neutral. Hosts and standard
// modules may only depend on pkg/spi (and pkg/api) from this package.
func TestSPIHasNoLegacyOrInternalImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read standards package directory: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			path := strings.Trim(spec.Path.Value, `"`)
			if strings.Contains(path, "/pkg/spi/legacy") || strings.Contains(path, "/internal/") {
				t.Errorf("%s imports forbidden package %q", name, path)
			}
		}
	}
}
