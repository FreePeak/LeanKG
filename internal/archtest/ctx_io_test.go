package archtest

import (
	"go/ast"
	"testing"
)

// ioBudget is the measured count of context-less I/O per directory and rule,
// in non-main, non-test packages. Only non-zero entries appear. Like the other
// budgets it only goes down, and an entry is removed when its count reaches 0.
var ioBudget = map[string]map[string]int{
	"internal/dashboard":            {"exec.Command": 1},
	"internal/doctor":               {"sql-no-ctx": 6},
	"internal/embed":                {"exec.Command": 1, "http.NewRequest": 1, "http.Client-without-Timeout": 2},
	"internal/embed/embedtest":      {"exec.Command": 1},
	"internal/gitobj/gittest":       {"exec.Command": 1},
	"internal/lsp":                  {"exec.Command": 1},
	"internal/memory":               {"sql-no-ctx": 32},
	"internal/sessionlink/opencode": {"sql-no-ctx": 4},
	"internal/sources":              {"http.Client-without-Timeout": 1},
	"internal/store":                {"sql-no-ctx": 278},
}

// ioRule is one context-less I/O pattern with its AST matcher.
type ioRule struct {
	name string
	hit  func(ast.Node) bool
}

// ioRules are the rules enforced by TestNoContextlessIO. Their names are the
// keys of ioBudget.
var ioRules = []ioRule{
	{"exec.Command", func(n ast.Node) bool {
		return isPkgCall(n, "exec", "Command")
	}},
	{"http.NewRequest", func(n ast.Node) bool {
		return isPkgCall(n, "http", "NewRequest")
	}},
	{"http.Get/Post/PostForm/Head", func(n ast.Node) bool {
		return isPkgCall(n, "http", "Get", "Post", "PostForm", "Head")
	}},
	{"http.Client-without-Timeout", clientWithoutTimeout},
	{"sql-no-ctx", isSQLWithoutCtx},
}

// TestNoContextlessIO counts context-less I/O calls per directory and rule and
// compares each count with ioBudget.
func TestNoContextlessIO(t *testing.T) {
	_, files := scanLibrary(t)
	known := map[string]bool{}
	for _, r := range ioRules {
		known[r.name] = true
		compareBudget(t, r.name, countByDir(files, r.hit), budgetForRule(r.name))
	}
	for dir, rules := range ioBudget {
		for rule := range rules {
			if !known[rule] {
				t.Errorf("ioBudget[%s] names unknown rule %q: fix the key in internal/archtest/ctx_io_test.go", dir, rule)
			}
		}
	}
}

// budgetForRule flattens ioBudget to directory -> count for one rule.
func budgetForRule(rule string) map[string]int {
	out := map[string]int{}
	for dir, rules := range ioBudget {
		if n, ok := rules[rule]; ok {
			out[dir] = n
		}
	}
	return out
}

// clientWithoutTimeout matches an http.Client composite literal (including
// &http.Client{}) that has no Timeout key.
func clientWithoutTimeout(n ast.Node) bool {
	lit, ok := n.(*ast.CompositeLit)
	if !ok || !isPkgType(lit.Type, "http", "Client") {
		return false
	}
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Timeout" {
				return false
			}
		}
	}
	return true
}

// isPkgType reports whether expr is the qualified type pkg.name.
func isPkgType(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// isSQLWithoutCtx flags database/sql calls that take no context. It is
// syntactic and receiver-agnostic, because go/parser has no types:
//
//   - Exec, Query, QueryRow and Prepare count when they have at least one
//     argument and the first is not ctx-shaped. A zero-argument Query (such as
//     r.URL.Query()) never counts.
//   - Begin counts only with zero arguments (tx.Begin()). Begin(ctx, ...) and
//     BeginTx never count.
//
// ctx-shaped means the identifier exactly named ctx, or a call to a selector
// named Context (r.Context(), cmd.Context()). Anything else in the first
// position counts, including a string literal and pgCtx. pgCtx is flagged on
// purpose so that each store call site is renamed to ctx when its role
// migrates.
//
// Known false positives: any non-SQL method named Exec, Query, QueryRow,
// Prepare or Begin whose first argument is not ctx-shaped (for example a
// one-argument Query(req) on a custom type), and a context held in a variable
// not named ctx (such as pgCtx, flagged on purpose). Known false negatives: SQL
// calls reached through a method value or a wrapper with another name (for
// example s.run("select")), and a ctx-shaped argument that is not a real context
// because it is a call named Context on a non-request type.
func isSQLWithoutCtx(n ast.Node) bool {
	_, sel, args, ok := asCall(n)
	if !ok {
		return false
	}
	switch sel {
	case "Begin":
		return len(args) == 0
	case "Exec", "Query", "QueryRow", "Prepare":
		return len(args) > 0 && !isCtxShaped(args[0])
	}
	return false
}

// isCtxShaped reports whether expr is a context argument: the identifier ctx,
// or a call whose function is a selector named Context.
func isCtxShaped(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "ctx"
	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "Context"
	}
	return false
}
