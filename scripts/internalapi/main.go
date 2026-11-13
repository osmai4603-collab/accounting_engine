// Command internalapi reports exported identifiers whose public Go type
// mentions an internal package. It is used by check_public_api_purity.sh.
//
// Usage: go run ./scripts/internalapi <import-path>...
package main

import (
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: internalapi <import-path>...")
		os.Exit(2)
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "source", nil)
	violations := 0
	for _, path := range os.Args[1:] {
		pkg, err := imp.Import(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "internalapi: load %s: %v\n", path, err)
			os.Exit(2)
		}
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if !obj.Exported() {
				continue
			}
			var found []string
			checker := &checker{seen: map[types.Type]bool{}}
			checker.checkType(obj.Type(), name)
			if named, ok := obj.Type().(*types.Named); ok {
				for i := 0; i < named.NumMethods(); i++ {
					method := named.Method(i)
					if !method.Exported() {
						continue
					}
					checker.checkType(method.Type(), name+"."+method.Name())
				}
			}
			found = append(found, checker.found...)
			sort.Strings(found)
			for _, item := range found {
				fmt.Printf("%s: %s\n", path, item)
				violations++
			}
		}
	}
	if violations > 0 {
		fmt.Fprintf(os.Stderr, "internalapi: %d exported internal type exposure(s)\n", violations)
		os.Exit(1)
	}
}

type checker struct {
	seen  map[types.Type]bool
	found []string
}

func (c *checker) checkType(t types.Type, where string) {
	if t == nil || c.seen[t] {
		return
	}
	c.seen[t] = true
	switch t := t.(type) {
	case *types.Alias:
		c.report(t.Obj(), where)
		c.checkType(types.Unalias(t), where)
	case *types.Named:
		c.report(t.Obj(), where)
		c.checkType(t.Underlying(), where)
	case *types.Pointer:
		c.checkType(t.Elem(), where)
	case *types.Slice:
		c.checkType(t.Elem(), where)
	case *types.Array:
		c.checkType(t.Elem(), where)
	case *types.Map:
		c.checkType(t.Key(), where)
		c.checkType(t.Elem(), where)
	case *types.Chan:
		c.checkType(t.Elem(), where)
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			field := t.Field(i)
			if !field.Exported() {
				continue
			}
			c.checkType(field.Type(), where+"."+field.Name())
		}
	case *types.Interface:
		for i := 0; i < t.NumMethods(); i++ {
			c.checkType(t.Method(i).Type(), where+"."+t.Method(i).Name())
		}
	case *types.Signature:
		if t.TypeParams() != nil {
			for i := 0; i < t.TypeParams().Len(); i++ {
				c.checkType(t.TypeParams().At(i), where)
			}
		}
		c.checkTuple(t.Params(), where)
		c.checkTuple(t.Results(), where)
	case *types.TypeParam:
		c.checkType(t.Constraint(), where)
	case *types.Tuple:
		c.checkTuple(t, where)
	case *types.Union:
		for i := 0; i < t.Len(); i++ {
			c.checkType(t.Term(i).Type(), where)
		}
	}
}

func (c *checker) checkTuple(tuple *types.Tuple, where string) {
	if tuple == nil {
		return
	}
	for i := 0; i < tuple.Len(); i++ {
		c.checkType(tuple.At(i).Type(), where)
	}
}

func (c *checker) report(obj types.Object, where string) {
	if obj == nil || obj.Pkg() == nil {
		return
	}
	path := obj.Pkg().Path()
	if strings.Contains(path, "/internal/") || strings.HasSuffix(path, "/internal") {
		c.found = append(c.found, fmt.Sprintf("%s exposes %s (%s)", where, obj.Name(), path))
	}
}
