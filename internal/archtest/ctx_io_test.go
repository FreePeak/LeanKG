package archtest

import (
	"go/ast"
	"testing"
)

// ioBudget is the measured count of context-less I/O per directory and rule,
// in non-main, non-test packages. Only non-zero entries appear. Like the other
// budgets it only goes down, and an entry is removed when its count reaches 0.
var ioBudget = map[string]map[string]int{
	// Deliberate: a nil federation.Options.Client falls back to http.DefaultClient (no timeout), see federation.go:119-122.
	"internal/federation": {"http.DefaultClient": 1},
}

// ioRule is one context-less I/O pattern with its AST matcher.
type ioRule struct {
	name string
	hit  nodeHit
}

// ioRules are the rules enforced by TestNoContextlessIO. Their names are the
// keys of ioBudget.
var ioRules = []ioRule{
	{"exec.Command", func(f *sourceFile, n ast.Node) bool {
		return isPkgCall(f, n, "os/exec", "Command")
	}},
	{"http.NewRequest", func(f *sourceFile, n ast.Node) bool {
		return isPkgCall(f, n, "net/http", "NewRequest")
	}},
	{"http.Get/Post/PostForm/Head", func(f *sourceFile, n ast.Node) bool {
		return isPkgCall(f, n, "net/http", "Get", "Post", "PostForm", "Head")
	}},
	{"http.Client-without-Timeout", clientWithoutTimeout},
	{"http.DefaultClient", isDefaultClient},
	{"sql-no-ctx", func(_ *sourceFile, n ast.Node) bool {
		return isSQLWithoutCtx(n)
	}},
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

// clientWithoutTimeout matches an http.Client that carries no Timeout. It
// covers two shapes: a composite literal (including &http.Client{}) with no
// Timeout key, and new(http.Client). http.DefaultClient has its own rule
// (isDefaultClient). The package name is resolved through the file's imports,
// so an alias is caught and an unrelated identifier named http is not.
func clientWithoutTimeout(f *sourceFile, n ast.Node) bool {
	switch e := n.(type) {
	case *ast.CompositeLit:
		return isQualified(f, e.Type, "net/http", "Client") && !hasTimeoutKey(e)
	case *ast.CallExpr:
		id, ok := e.Fun.(*ast.Ident)
		return ok && id.Name == "new" && len(e.Args) == 1 && isQualified(f, e.Args[0], "net/http", "Client")
	}
	return false
}

// hasTimeoutKey reports whether a composite literal sets a Timeout field.
func hasTimeoutKey(lit *ast.CompositeLit) bool {
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Timeout" {
				return true
			}
		}
	}
	return false
}

// isDefaultClient matches a reference to http.DefaultClient, which has no
// timeout. It has its own rule, so it is counted once and not as a timeout-less
// http.Client literal.
func isDefaultClient(f *sourceFile, n ast.Node) bool {
	e, ok := n.(ast.Expr)
	return ok && isQualified(f, e, "net/http", "DefaultClient")
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
// position counts, including a string literal or a context held in a stored
// variable. Each store call site is renamed to ctx when its role migrates.
//
// Known false positives: any non-SQL method named Exec, Query, QueryRow,
// Prepare or Begin whose first argument is not ctx-shaped (for example a
// one-argument Query(req) on a custom type), and a context held in a variable
// not named ctx (flagged on purpose). Known false negatives: SQL
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

// TestContextlessIORuleFixtures proves each matcher catches the shapes it
// claims to, and ignores look-alikes that a file which never imports the
// package cannot use. Each case is an in-memory file counted by one rule.
func TestContextlessIORuleFixtures(t *testing.T) {
	cases := []struct {
		name string
		rule string
		src  string
		want int
	}{
		{"client literal without timeout", "http.Client-without-Timeout",
			`package p; import "net/http"; var c = &http.Client{}`, 1},
		{"client literal with timeout", "http.Client-without-Timeout",
			`package p; import "net/http"; var c = http.Client{Timeout: 5}`, 0},
		{"new(http.Client)", "http.Client-without-Timeout",
			`package p; import "net/http"; var c = new(http.Client)`, 1},
		{"http.DefaultClient value", "http.DefaultClient",
			`package p; import "net/http"; var c = http.DefaultClient`, 1},
		{"http.DefaultClient.Do call counts once", "http.DefaultClient",
			`package p; import "net/http"; func f() { http.DefaultClient.Do(nil) }`, 1},
		{"aliased client literal", "http.Client-without-Timeout",
			`package p; import h "net/http"; var c = &h.Client{}`, 1},
		{"aliased new(client)", "http.Client-without-Timeout",
			`package p; import h "net/http"; var c = new(h.Client)`, 1},
		{"aliased DefaultClient", "http.DefaultClient",
			`package p; import h "net/http"; var c = h.DefaultClient`, 1},
		{"http name without the import", "http.DefaultClient",
			`package p; var c = http.DefaultClient`, 0},
		{"aliased http.Get", "http.Get/Post/PostForm/Head",
			`package p; import h "net/http"; func f() { h.Get("u") }`, 1},
		{"http.Get without the import", "http.Get/Post/PostForm/Head",
			`package p; func f() { http.Get("u") }`, 0},
		{"aliased exec.Command", "exec.Command",
			`package p; import x "os/exec"; func f() { x.Command("ls") }`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := parseFixture(t, tc.src)
			got := countByDir([]sourceFile{*f}, ruleHit(t, tc.rule))["."]
			if got != tc.want {
				t.Fatalf("%s: %s counted %d, want %d", tc.src, tc.rule, got, tc.want)
			}
		})
	}
}

// ruleHit returns the matcher of the named ioRules entry.
func ruleHit(t *testing.T, name string) nodeHit {
	t.Helper()
	for _, r := range ioRules {
		if r.name == name {
			return r.hit
		}
	}
	t.Fatalf("no ioRules entry named %q", name)
	return nil
}
