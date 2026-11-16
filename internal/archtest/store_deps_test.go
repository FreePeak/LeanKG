package archtest

import (
	"go/ast"
	"slices"
	"sort"
	"testing"
)

// narrowedStorePackages are the consumers whose store dependency is narrowed
// to the roles or methods they call (plan CX-37, section 3.3.1). A declaration
// here must not name store.Backend in a function signature or a struct field.
var narrowedStorePackages = []string{
	"internal/graph",
	"internal/annot",
	"internal/githistory",
	"internal/summarize",
	"internal/export",
	"internal/metrics",
}

// backendNamingAllowlist holds the declarations of narrowedStorePackages that
// still name store.Backend. It starts with the offenders found when CX-37
// began and must end empty: an entry that no longer names Backend fails the
// test until it is removed, so the list can only shrink.
var backendNamingAllowlist = map[string]bool{}

// TestNarrowedStorePackagesDoNotNameBackend fails when a declaration in one of
// the narrowed packages takes or stores store.Backend.
func TestNarrowedStorePackagesDoNotNameBackend(t *testing.T) {
	_, files := scanLibrary(t)

	scanned := map[string]bool{}
	offenders := map[string]bool{}
	for _, f := range files {
		if !slices.Contains(narrowedStorePackages, f.Dir) {
			continue
		}
		scanned[f.Dir] = true
		for _, decl := range f.AST.Decls {
			for _, key := range backendDeclKeys(f.Rel, decl) {
				offenders[key] = true
			}
		}
	}
	for _, dir := range narrowedStorePackages {
		if !scanned[dir] {
			t.Fatalf("%s: no source files scanned; the package list is stale", dir)
		}
	}

	var newOffenders []string
	for key := range offenders {
		if !backendNamingAllowlist[key] {
			newOffenders = append(newOffenders, key)
		}
	}
	sort.Strings(newOffenders)
	for _, key := range newOffenders {
		t.Errorf("%s names store.Backend: depend on the store role or method set it calls (plan 3.3.1)", key)
	}

	var stale []string
	for key := range backendNamingAllowlist {
		if !offenders[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("backendNamingAllowlist lists %s, which no longer names store.Backend: remove it from internal/archtest/store_deps_test.go", key)
	}
}

// backendDeclKeys returns one key per signature or struct field of decl that
// names store.Backend. A key is "file:Func", "file:Recv.Method" or
// "file:Type.field".
func backendDeclKeys(rel string, decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if (d.Recv != nil && namesBackend(d.Recv)) || namesBackend(d.Type) {
			return []string{rel + ":" + funcName(d)}
		}
	case *ast.GenDecl:
		var keys []string
		for _, spec := range d.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				if !namesBackend(field.Type) {
					continue
				}
				for _, name := range field.Names {
					keys = append(keys, rel+":"+ts.Name.Name+"."+name.Name)
				}
			}
		}
		return keys
	}
	return nil
}

// namesBackend reports whether the syntax under n refers to store.Backend.
func namesBackend(n ast.Node) bool {
	hit := false
	ast.Inspect(n, func(x ast.Node) bool {
		if sel, ok := x.(*ast.SelectorExpr); ok && sel.Sel.Name == "Backend" {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "store" {
				hit = true
			}
		}
		return !hit
	})
	return hit
}

// funcName returns "Func" for a function and "Recv.Method" for a method.
func funcName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}
	recv := d.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if id, ok := recv.(*ast.Ident); ok {
		return id.Name + "." + d.Name.Name
	}
	return d.Name.Name
}
