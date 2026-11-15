package core

// Coverage guard for the action goldens (CX-42). core dispatches on string
// switches (query, importAction, memoryWrite, ...), not on a registry, so the
// vocabulary is read from the source: every case literal in a dispatch switch
// is a key, and each key must be covered by an actionCases entry. A new action
// or sub-command added to core without a golden fails TestActionVocabulary.
// CX-43 replaces the switches with a registry; this guard then reads the
// registry instead and its root table is the only thing to change.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// vocabRoots names the core functions whose switches are the action
// vocabulary, with the key path each one hangs off. Keys read
// "<root>/<action>[/<sub-command>]", e.g. "query/memory/view".
var vocabRoots = map[string]string{
	"query":             "query",
	"graphAction":       "query",
	"importAction":      "import",
	"memoryWrite":       "import/memory",
	"sessionWrite":      "import/session",
	"MemoryRead":        "query/memory",
	"SessionMemoryRead": "query/memory",
	"SessionRead":       "query/session",
	"lspQuery":          "query/lsp",
	"ResolveEnvelope":   "tool",
}

// vocabTags are the switch subjects that carry vocabulary: the action, the
// import command, and the sub-command variables the handlers use.
var vocabTags = map[string]bool{
	"req.Action": true, "req.Command": true, "cmd": true, "command": true, "tool": true,
}

// envGatedKeys are vocabulary keys whose positive path needs an external
// program (a language server for lsp workspace/document). Their refusals are
// pinned by the error goldens; the positive answer is not reproducible in a
// hermetic test.
var envGatedKeys = map[string]bool{
	"query/lsp/workspace": true,
	"query/lsp/document":  true,
	"query/lsp/(empty)":   true,
}

// auxKeys are golden keys that name a refusal or a non-switch answer rather
// than a switch case: an unknown action, a missing action, the engine status
// call, and the session read's no-command listing (the default branch).
var auxKeys = map[string]bool{
	"query":                 true,
	"import":                true,
	"tool":                  true,
	"status":                true,
	"query/session/(empty)": true,
}

// extractVocabulary parses the non-test Go files in dir and returns every
// vocabulary key found under vocabRoots.
func extractVocabulary(t *testing.T, dir string) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		parsed = append(parsed, f)
	}
	// Package-level string constants: ResolveEnvelope switches on ToolImport
	// and friends, so a case can name a constant instead of a literal.
	consts := map[string]string{}
	for _, f := range parsed {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if v, ok := stringLit(vs.Values[i], nil); ok {
							consts[name.Name] = v
						}
					}
				}
			}
		}
	}
	keys := map[string]bool{}
	for _, f := range parsed {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			root, ok := vocabRoots[fd.Name.Name]
			if !ok {
				continue
			}
			collectVocab(fd.Body, root, consts, keys)
		}
	}
	return keys
}

// collectVocab walks n. A vocabulary switch adds one key per case literal and
// hands its clause bodies the key of the action it is in, so nested
// sub-command switches get the full path. Other switches are walked through.
func collectVocab(n ast.Node, prefix string, consts map[string]string, keys map[string]bool) {
	ast.Inspect(n, func(x ast.Node) bool {
		sw, ok := x.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		tag := switchTag(sw)
		if sw.Tag == nil || !vocabTags[tag] {
			return true
		}
		isAction := tag == "req.Action"
		for _, stmt := range sw.Body.List {
			cc, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			child := prefix
			for i, e := range cc.List {
				lit, ok := stringLit(e, consts)
				if !ok {
					continue
				}
				key := prefix + "/" + displayLit(lit)
				keys[key] = true
				if isAction && i == 0 {
					child = key
				}
			}
			for _, s := range cc.Body {
				collectVocab(s, child, consts, keys)
			}
		}
		return false
	})
}

func switchTag(sw *ast.SwitchStmt) string {
	switch v := sw.Tag.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		if id, ok := v.X.(*ast.Ident); ok {
			return id.Name + "." + v.Sel.Name
		}
	}
	return ""
}

// stringLit returns the value of a string literal, or of an identifier naming
// a package-level string constant.
func stringLit(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.Ident:
		s, ok := consts[v.Name]
		return s, ok
	}
	return "", false
}

func displayLit(s string) string {
	if s == "" {
		return "(empty)"
	}
	return s
}

// TestActionVocabulary fails when core's dispatch switches name an action or
// sub-command with no golden, and when a golden names one core no longer
// dispatches on (a stale case to delete with its golden file).
func TestActionVocabulary(t *testing.T) {
	vocab := extractVocabulary(t, coreDir)
	// A guard that finds nothing passes vacuously; the real vocabulary is far
	// larger than this floor.
	if len(vocab) < 50 {
		t.Fatalf("vocabulary extraction found %d keys, want at least 50: %v", len(vocab), sortedKeys(vocab))
	}

	t.Logf("core dispatches on %d vocabulary keys: %v", len(vocab), sortedKeys(vocab))

	covered := map[string]bool{}
	for _, c := range actionCases {
		for _, k := range c.covers() {
			covered[k] = true
		}
	}

	var missing []string
	for k := range vocab {
		if !covered[k] && !envGatedKeys[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("core dispatches on actions with no golden: %v\nadd a case to actionCases in actions_golden_test.go, then run with -update", missing)
	}

	for _, c := range actionCases {
		for _, k := range c.covers() {
			if !vocab[k] && !auxKeys[k] {
				t.Errorf("case %s covers %q, which core no longer dispatches on; delete the case and its golden file", c.name, k)
			}
		}
	}
}

// TestVocabularyGuardSeesNewAction proves the guard reads a switch it has not
// been told about: a synthetic core file adds one action and the extractor
// reports it. It keeps the guard honest if the switch shapes above change.
func TestVocabularyGuardSeesNewAction(t *testing.T) {
	dir := t.TempDir()
	src := `package core

func (e *Engine) query(req QueryRequest) {
	switch req.Action {
	case "exact", "element":
	case "brand-new-action":
		switch cmd := argStr(req.Args, "command"); cmd {
		case "sub-one":
		}
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "core.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	vocab := extractVocabulary(t, dir)
	for _, want := range []string{"query/brand-new-action", "query/brand-new-action/sub-one", "query/exact"} {
		if !vocab[want] {
			t.Fatalf("extractor missed %q; got %v", want, sortedKeys(vocab))
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
