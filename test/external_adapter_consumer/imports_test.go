package externaladapterconsumer

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	engineModule  = "github.com/osmai4603-collab/accounting_engine"
	ownModule     = engineModule + "/test/external_adapter_consumer"
	internalPath  = "internal/"
	memoryPackage = "storage/memory"
	postgresPath  = "storage/postgres"
)

// TestNoForbiddenImports proves that this project consumes the engine only
// through its public packages. Any internal package, bundled memory adapter or
// PostgreSQL adapter import fails the external-consumer gate.
func TestNoForbiddenImports(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "vendor" || entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if strings.Contains(importPath, internalPath) {
				return fmt.Errorf("%s imports internal package %q", path, importPath)
			}
			if strings.Contains(importPath, memoryPackage) {
				return fmt.Errorf("%s imports the bundled memory adapter %q", path, importPath)
			}
			if strings.Contains(importPath, postgresPath) {
				return fmt.Errorf("%s imports the PostgreSQL adapter %q", path, importPath)
			}
			if importPath == ownModule || strings.HasPrefix(importPath, ownModule+"/") {
				continue
			}
			if importPath == engineModule || strings.HasPrefix(importPath, engineModule+"/") {
				if !strings.HasPrefix(importPath, engineModule+"/pkg/") {
					return fmt.Errorf("%s imports %q outside the public pkg/ surface", path, importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestReplaceIsLocalOnly documents the module-hygiene contract: the replace
// directive is only for local pre-release verification and points at the
// repository root. The published check in
// scripts/check_external_adapter_consumer.sh --published rebuilds this module
// without the directive.
func TestReplaceIsLocalOnly(t *testing.T) {
	content, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	expected := "replace " + engineModule + " => ../.."
	var replaces []string
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "replace ") {
			replaces = append(replaces, trimmed)
		}
	}
	if len(replaces) == 0 {
		// Published-mode verification deliberately removes the directive.
		t.Log("no replace directive: published-module verification mode")
		return
	}
	if len(replaces) != 1 || replaces[0] != expected {
		t.Fatalf("go.mod replace directives = %v, want exactly %q", replaces, expected)
	}
}
