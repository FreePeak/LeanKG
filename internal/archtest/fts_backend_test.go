package archtest

import (
	"go/ast"
	"testing"
)

// TestNoFTSBackendRemains enforces CX-15: the FTSBackend capability interface
// is gone. Its keyword arm is now the Searcher contract (FindFuzzy returns the
// arm) and its knowledge search is KnowledgeEntriesSearch, so no identifier
// named FTSBackend may remain in library code.
func TestNoFTSBackendRemains(t *testing.T) {
	fset, files := scanLibrary(t)
	for _, f := range files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == "FTSBackend" {
				t.Errorf("%s: FTSBackend reference remains", fset.Position(id.Pos()))
			}
			return true
		})
	}
}
