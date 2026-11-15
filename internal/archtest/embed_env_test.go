package archtest

import (
	"go/ast"
	"go/token"
	"strings"
	"testing"
)

// TestEmbedEnvStaysInCmd is the CX-41 guardrail: the embed provider is
// configured by an explicit embed.Config that cmd/ fills from LEANKG_EMBED_*.
// No code under internal/embed (except the test-only embedtest helper) reads
// the environment, and no code outside cmd/ reads an LEANKG_EMBED_* variable.
func TestEmbedEnvStaysInCmd(t *testing.T) {
	fset, files := scanLibrary(t)
	var bad []string
	for _, f := range files {
		inEmbed := strings.HasPrefix(f.Rel, "internal/embed/") && !strings.HasPrefix(f.Rel, "internal/embed/embedtest/")
		inCmd := strings.HasPrefix(f.Rel, "cmd/")
		ast.Inspect(f.AST, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isEnvRead(call) {
				return true
			}
			pos := fset.Position(call.Pos())
			switch {
			case inEmbed:
				bad = append(bad, pos.String()+": environment read under internal/embed")
			case !inCmd && embedVarArg(call):
				bad = append(bad, pos.String()+": LEANKG_EMBED_* read outside cmd/")
			}
			return true
		})
	}
	if len(bad) > 0 {
		t.Fatalf("embed environment reads outside cmd/:\n%s", strings.Join(bad, "\n"))
	}
}

// isEnvRead matches os.Getenv and os.LookupEnv calls.
func isEnvRead(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "os" && (sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv")
}

// embedVarArg reports whether the call names an LEANKG_EMBED_ variable literally.
func embedVarArg(call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	return ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "LEANKG_EMBED_")
}
