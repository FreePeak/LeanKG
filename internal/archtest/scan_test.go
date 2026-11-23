package archtest

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// skipDirs are directory names that are never scanned, at any depth.
var skipDirs = map[string]bool{
	".worktrees": true, ".git": true, "node_modules": true, "testdata": true,
	"vendor": true, "ui-v2": true, "ui-dashboard": true, ".idea": true,
}

// sourceFile is one non-test, non-generated Go file of the repository.
type sourceFile struct {
	Rel     string            // repo-relative slash path
	Dir     string            // repo-relative slash directory, "." for the root
	Pkg     string            // package clause name
	Imports map[string]string // local name -> import path, from the import block
	AST     *ast.File
}

// nodeHit reports whether node n of file f is one of the patterns a rule
// counts. The file is passed so a matcher can resolve import aliases.
type nodeHit func(f *sourceFile, n ast.Node) bool

// newSourceFile wraps a parsed file. Each import is keyed by the name it is
// used under: its explicit alias, or else the last element of its path (the
// package name for every stdlib package the rules use). Blank and dot imports
// bind no name.
func newSourceFile(rel string, f *ast.File) sourceFile {
	imports := map[string]string{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name != "_" && name != "." {
			imports[name] = path
		}
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	return sourceFile{Rel: rel, Dir: dir, Pkg: f.Name.Name, Imports: imports, AST: f}
}

// parseFixture parses an in-memory Go source as a repo file named fixture.go,
// for rule tests that must prove a pattern is caught or not.
func parseFixture(t *testing.T, src string) *sourceFile {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	sf := newSourceFile("fixture.go", f)
	return &sf
}

// scanLibrary parses every non-test, non-generated .go file under the
// repository root, drops package main, and returns the rest sorted by Rel.
func scanLibrary(t *testing.T) (*token.FileSet, []sourceFile) {
	return scanSources(t, func(rel, pkg string) bool {
		return pkg != "main" && !strings.HasSuffix(rel, "_test.go")
	})
}

// scanSources parses every non-generated .go file under the repository root
// that keep accepts (by repo-relative path and package clause), sorted by Rel.
// The top-level examples directory is a separate module and is skipped.
func scanSources(t *testing.T, keep func(rel, pkg string) bool) (*token.FileSet, []sourceFile) {
	t.Helper()
	root := repoRoot(t)
	fset := token.NewFileSet()
	var files []sourceFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (skipDirs[d.Name()] || rel == "examples") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(src[:min(len(src), 1024)], []byte("Code generated")) {
			return nil
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if keep(rel, f.Name.Name) {
			files = append(files, newSourceFile(rel, f))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository: %v", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return fset, files
}

// repoRoot returns the directory holding go.mod, found by walking up from the
// package directory (the test file's directory under go test). Nothing is
// hardcoded, so the scan works in any checkout or worktree.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test package directory")
		}
		dir = parent
	}
}

// countByDir counts the nodes of every file for which hit reports true,
// keyed by repo-relative directory.
func countByDir(files []sourceFile, hit nodeHit) map[string]int {
	counts := map[string]int{}
	for i := range files {
		f := &files[i]
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if hit(f, n) {
				counts[f.Dir]++
			}
			return true
		})
	}
	return counts
}

// asCall splits a call of the form recv.Sel(args...). recv is the receiver
// identifier name, or "" when the receiver is not a plain identifier.
func asCall(n ast.Node) (recv, sel string, args []ast.Expr, ok bool) {
	call, isCall := n.(*ast.CallExpr)
	if !isCall {
		return "", "", nil, false
	}
	sx, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel {
		return "", "", nil, false
	}
	if id, isIdent := sx.X.(*ast.Ident); isIdent {
		recv = id.Name
	}
	return recv, sx.Sel.Name, call.Args, true
}

// isPkgCall reports whether n is a call pkgPath.Sel(...) for one of sels. The
// receiver must be an identifier that f imports as pkgPath, so an alias
// (import h "net/http") matches and a local variable that happens to share the
// package's name does not.
func isPkgCall(f *sourceFile, n ast.Node, pkgPath string, sels ...string) bool {
	recv, sel, _, ok := asCall(n)
	if !ok || f.Imports[recv] != pkgPath {
		return false
	}
	return slices.Contains(sels, sel)
}

// isQualified reports whether expr is the qualified identifier pkgPath.name,
// used as a type (http.Client) or a value (http.DefaultClient).
func isQualified(f *sourceFile, expr ast.Expr, pkgPath, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && f.Imports[id.Name] == pkgPath
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

// compareBudget applies the two-way ratchet to counts keyed by directory.
// Above budget, below budget and unbudgeted non-zero counts all fail.
func compareBudget(t *testing.T, rule string, counts, budget map[string]int) {
	t.Helper()
	dirs := map[string]bool{}
	for d := range counts {
		dirs[d] = true
	}
	for d := range budget {
		dirs[d] = true
	}
	keys := make([]string, 0, len(dirs))
	for d := range dirs {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	for _, dir := range keys {
		got, want := counts[dir], budget[dir]
		switch {
		case got > want:
			t.Errorf("new %s in %s: thread a ctx instead (budget %d, found %d)", rule, dir, want, got)
		case got < want:
			t.Errorf("lower the budget for %s to %d in internal/archtest (rule %s)", dir, got, rule)
		}
	}
}
