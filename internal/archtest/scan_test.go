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
	Rel string // repo-relative slash path
	Dir string // repo-relative slash directory, "." for the root
	Pkg string // package clause name
	AST *ast.File
}

// scanLibrary parses every non-test, non-generated .go file under the
// repository root, drops package main, and returns the rest sorted by Rel.
// The top-level examples directory is a separate module and is skipped.
func scanLibrary(t *testing.T) (*token.FileSet, []sourceFile) {
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
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
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
		if f.Name.Name != "main" {
			files = append(files, sourceFile{Rel: rel, Dir: filepath.ToSlash(filepath.Dir(rel)), Pkg: f.Name.Name, AST: f})
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
func countByDir(files []sourceFile, hit func(ast.Node) bool) map[string]int {
	counts := map[string]int{}
	for _, f := range files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if hit(n) {
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

// isPkgCall reports whether n is pkg.Sel(...) for one of sels.
func isPkgCall(n ast.Node, pkg string, sels ...string) bool {
	recv, sel, _, ok := asCall(n)
	if !ok || recv != pkg {
		return false
	}
	return slices.Contains(sels, sel)
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
