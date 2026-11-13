// Command apisnapshot writes a deterministic snapshot of the exported Go API of
// the supplied packages. The snapshot is used to detect unannounced breaking
// changes in CI.
//
// Usage: go run ./scripts/apisnapshot <import-path>...
package main

import (
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"sort"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: apisnapshot <import-path>...")
		os.Exit(2)
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "source", nil)
	var lines []string
	qualifier := func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	}
	for _, path := range os.Args[1:] {
		pkg, err := imp.Import(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "apisnapshot: load %s: %v\n", path, err)
			os.Exit(2)
		}
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if !obj.Exported() {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s %s %s", path, obj.Name(), types.ObjectString(obj, qualifier)))
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}
			switch underlying := named.Underlying().(type) {
			case *types.Struct:
				for i := 0; i < underlying.NumFields(); i++ {
					field := underlying.Field(i)
					lines = append(lines, fmt.Sprintf("%s %s.%s %s", path, obj.Name(), field.Name(), types.TypeString(field.Type(), qualifier)))
				}
			case *types.Interface:
				for i := 0; i < underlying.NumMethods(); i++ {
					method := underlying.Method(i)
					lines = append(lines, fmt.Sprintf("%s %s.%s %s", path, obj.Name(), method.Name(), types.TypeString(method.Type(), qualifier)))
				}
			}
			for i := 0; i < named.NumMethods(); i++ {
				method := named.Method(i)
				if !method.Exported() {
					continue
				}
				lines = append(lines, fmt.Sprintf("%s %s.%s %s", path, obj.Name(), method.Name(), types.TypeString(method.Type(), qualifier)))
			}
		}
	}
	sort.Strings(lines)
	for _, line := range lines {
		fmt.Println(line)
	}
}
