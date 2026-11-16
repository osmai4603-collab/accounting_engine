package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const postingImport = "github.com/osmai4603-collab/accounting_engine/internal/posting"

var compositionRoots = map[string]bool{
	"internal/legacyengine/engine.go":            true,
	"internal/sdkapplication/posting_factory.go": true,
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fail(err)
	}

	fset := token.NewFileSet()
	violations := 0
	err = filepath.WalkDir(root, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if file != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") || strings.Contains(file, string(filepath.Separator)+"scripts"+string(filepath.Separator)) {
			return nil
		}

		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		tree, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			return err
		}
		aliases := map[string]bool{}
		for _, spec := range tree.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || importPath != postingImport {
				continue
			}
			alias := path.Base(importPath)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			if alias == "." && !compositionRoots[rel] {
				position := fset.Position(spec.Pos())
				fmt.Fprintf(os.Stderr, "VIOLATION: %s:%d dot-imports the Posting Engine, bypassing construction checks\n", position.Filename, position.Line)
				violations++
				continue
			}
			if alias != "." && alias != "_" {
				aliases[alias] = true
			}
		}
		engineTypes := map[string]bool{}
		ast.Inspect(tree, func(node ast.Node) bool {
			spec, ok := node.(*ast.TypeSpec)
			if ok && isPostingEngineType(spec.Type, aliases, nil) {
				engineTypes[spec.Name.Name] = true
			}
			return true
		})
		if compositionRoots[rel] {
			return nil
		}

		ast.Inspect(tree, func(node ast.Node) bool {
			var reason string
			switch current := node.(type) {
			case *ast.SelectorExpr:
				if base, ok := current.X.(*ast.Ident); ok && aliases[base.Name] {
					if current.Sel.Name == "NewEngine" || current.Sel.Name == "NewConfiguredEngine" {
						reason = "calls or references a Posting Engine constructor"
					}
				}
			case *ast.CompositeLit:
				if isPostingEngineType(current.Type, aliases, engineTypes) {
					reason = "constructs a Posting Engine literal"
				}
			case *ast.CallExpr:
				if fn, ok := current.Fun.(*ast.Ident); ok && fn.Name == "new" && len(current.Args) == 1 && isPostingEngineType(current.Args[0], aliases, engineTypes) {
					reason = "allocates a Posting Engine with new"
				}
			}
			if reason != "" {
				position := fset.Position(node.Pos())
				fmt.Fprintf(os.Stderr, "VIOLATION: %s:%d %s outside a composition root\n", position.Filename, position.Line, reason)
				violations++
			}
			return true
		})
		return nil
	})
	if err != nil {
		fail(err)
	}
	if violations > 0 {
		os.Exit(1)
	}
	fmt.Println("Posting Engine construction boundary: passed")
}

func isPostingEngineType(expression ast.Expr, aliases, engineTypes map[string]bool) bool {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		return engineTypes[identifier.Name]
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Engine" {
		return false
	}
	base, ok := selector.X.(*ast.Ident)
	return ok && aliases[base.Name]
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
