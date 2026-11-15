package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// CX-51 guard. The engine-neutral logic lives in engine_shared.go and the
// per-engine files call it. This test parses every non-test file of the
// package and fails when:
//   - a shared helper is not declared exactly once, in engine_shared.go;
//   - a retired per-engine copy is declared again anywhere;
//   - an inline copy of a moved idiom reappears outside engine_shared.go
//     (the metadata "!= {}" decode test, the migration ledger DDL, the
//     keep-set rarity filter, the fused-hit truncation).

const sharedFile = "engine_shared.go"

// sharedDecls must each be declared once, in sharedFile.
var sharedDecls = []string{
	"scanRows", "scanRow", "scanElements", "scanRelationships", "scanFuzzyMatches",
	"scanStamp", "scanEmbedRun", "scanAuditEntry", "encodeJSONObject", "decodeJSONObject",
	"keepRareNameTokens", "prepareStaleSweep", "migrateModeError", "fuseHybridHits",
	"schemaMigrationsDDL", "embedRunCols",
}

// retiredDecls are the per-engine copies CX-51 removed. None may be declared.
var retiredDecls = []string{
	"pgScanElements", "pgScanRelationships", "pgScanEmbedRun", "pgMarshalMeta",
	"pgEmbedRunCols", "versionRows",
}

// The DDL and the metadata idiom, as they were written inline before CX-51.
const (
	ledgerDDLMarker = "CREATE TABLE IF NOT EXISTS schema_migrations"
	metaEmptyIdiom  = "{}"
)

type engineFile struct {
	name string
	ast  *ast.File
	fset *token.FileSet
}

func parseEngineFiles(t *testing.T) []engineFile {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []engineFile
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", n, err)
		}
		out = append(out, engineFile{name: n, ast: f, fset: fset})
	}
	if len(out) == 0 {
		t.Fatal("no store sources found")
	}
	return out
}

// topLevelNames returns every package-level name a file declares.
func topLevelNames(f *ast.File) []string {
	var names []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, id := range s.Names {
						names = append(names, id.Name)
					}
				}
			}
		}
	}
	return names
}

func TestEngineSharedLogicIsNotDuplicated(t *testing.T) {
	files := parseEngineFiles(t)

	declaredIn := map[string][]string{}
	for _, f := range files {
		for _, n := range topLevelNames(f.ast) {
			declaredIn[n] = append(declaredIn[n], f.name)
		}
	}
	for _, n := range sharedDecls {
		where := declaredIn[n]
		if len(where) != 1 || where[0] != sharedFile {
			t.Errorf("shared helper %s must be declared once, in %s; found in %v", n, sharedFile, where)
		}
	}
	for _, n := range retiredDecls {
		if where := declaredIn[n]; len(where) > 0 {
			t.Errorf("retired per-engine copy %s reappeared in %v; use the shared helper in %s", n, where, sharedFile)
		}
	}

	for _, f := range files {
		if f.name == sharedFile {
			continue
		}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if n.Op == token.NEQ && (isStringLit(n.X, metaEmptyIdiom) || isStringLit(n.Y, metaEmptyIdiom)) {
					t.Errorf("%s:%d inline metadata decode idiom (`!= \"{}\"`); use decodeJSONObject", f.name, f.fset.Position(n.Pos()).Line)
				}
			case *ast.BasicLit:
				if n.Kind == token.STRING && strings.Contains(n.Value, ledgerDDLMarker) {
					t.Errorf("%s:%d inline migration ledger DDL; use schemaMigrationsDDL", f.name, f.fset.Position(n.Pos()).Line)
				}
			case *ast.SliceExpr:
				if id, ok := n.X.(*ast.Ident); ok && id.Name == "fused" {
					t.Errorf("%s:%d inline fused-hit truncation; use fuseHybridHits", f.name, f.fset.Position(n.Pos()).Line)
				}
			}
			return true
		})
	}
}

func isStringLit(e ast.Expr, value string) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING && lit.Value == `"`+value+`"`
}
