package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// modPath stands in for the module path; the resolver matches import paths to
// directories by suffix, so the prefix only has to be a plausible module path.
const modPath = "example.com/m"

// newCrossTree opens an empty store and a fresh source directory. The caller
// writes files with putFiles and indexes with IndexDir, so one store can be
// indexed more than once (incremental tests).
func newCrossTree(t *testing.T) (store.Backend, string) {
	t.Helper()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(t.TempDir(), "s.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st, t.TempDir()
}

// putFiles writes files (relative path -> source) under dir, creating folders.
func putFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// indexTree runs one incremental index pass over dir.
func indexTree(t *testing.T, st store.Backend, dir string) {
	t.Helper()
	if _, err := IndexDir(context.Background(), st, dir); err != nil {
		t.Fatalf("IndexDir: %v", err)
	}
}

// targetsFrom lists the targets of src's outgoing edges of relType.
func targetsFrom(t *testing.T, st store.Backend, src, relType string) []string {
	t.Helper()
	rels, err := st.Outgoing(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rels {
		if r.RelType == relType {
			out = append(out, r.Target)
		}
	}
	return out
}

// TestGoQualifiedCallResolvesAcrossDirs: store.Open(...) in cmd/app reaches
// the Open that internal/store defines, at confidence 0.8 with the call line.
func TestGoQualifiedCallResolvesAcrossDirs(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, map[string]string{
		"internal/store/store.go": "package store\n\nfunc Open(p string) {}\n",
		"cmd/app/main.go":         "package main\n\nimport \"" + modPath + "/internal/store\"\n\nfunc run() {\n\tstore.Open(\"p\")\n}\n",
	})
	indexTree(t, st, dir)
	r, ok := edgeTo(t, st, "cmd/app/main.go::run", "internal/store/store.go::Open", "calls")
	if !ok {
		t.Fatalf("store.Open has no calls edge into internal/store; outgoing %v", targetsFrom(t, st, "cmd/app/main.go::run", "calls"))
	}
	if r.Confidence != 0.8 {
		t.Fatalf("confidence = %v, want 0.8", r.Confidence)
	}
	if line, _ := lineMeta(r); line != 6 {
		t.Fatalf("call-site line = %d, want 6", line)
	}
}

// TestGoRenamedImportAliasResolves: the same call through an import alias
// (st "…/internal/store") resolves to the package the alias names.
func TestGoRenamedImportAliasResolves(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, map[string]string{
		"internal/store/store.go": "package store\n\nfunc Open(p string) {}\n",
		"cmd/app/main.go":         "package main\n\nimport st \"" + modPath + "/internal/store\"\n\nfunc run() {\n\tst.Open(\"p\")\n}\n",
	})
	indexTree(t, st, dir)
	if _, ok := edgeTo(t, st, "cmd/app/main.go::run", "internal/store/store.go::Open", "calls"); !ok {
		t.Fatalf("aliased st.Open has no calls edge; outgoing %v", targetsFrom(t, st, "cmd/app/main.go::run", "calls"))
	}
}

// TestGoMethodByNameAcrossImportedPackage: srv.Handler() where srv is an
// internal/mcp value resolves to Server.Handler of an imported package, at
// confidence 0.4 with method-by-name and the call line.
func TestGoMethodByNameAcrossImportedPackage(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, map[string]string{
		"internal/mcp/server.go": "package mcp\n\ntype Server struct{}\n\nfunc (s *Server) Handler() {}\n",
		"cmd/app/main.go":        "package main\n\nimport \"" + modPath + "/internal/mcp\"\n\nfunc run(srv *mcp.Server) {\n\tsrv.Handler()\n}\n",
	})
	indexTree(t, st, dir)
	r, ok := edgeTo(t, st, "cmd/app/main.go::run", "internal/mcp/server.go::Server.Handler", "calls")
	if !ok {
		t.Fatalf("srv.Handler() has no calls edge to Server.Handler; outgoing %v", targetsFrom(t, st, "cmd/app/main.go::run", "calls"))
	}
	if r.Confidence != 0.4 {
		t.Fatalf("confidence = %v, want 0.4", r.Confidence)
	}
	if got, _ := r.Metadata["resolution"].(string); got != "method-by-name" {
		t.Fatalf("resolution = %q, want method-by-name", got)
	}
	if line, _ := lineMeta(r); line != 6 {
		t.Fatalf("call-site line = %d, want 6", line)
	}
}

// TestGoMethodValueIsReferenceWithLine: s.handle passed as a value (not
// called) is a references edge to the same-package method, with its line.
func TestGoMethodValueIsReferenceWithLine(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\ntype Server struct{}\n\n" +
		"func (s *Server) handle() {}\n\nfunc reg(f func()) {}\n\n" +
		"func (s *Server) Register() {\n\treg(s.handle)\n}\n"})
	r, ok := edgeTo(t, st, "a.go::Server.Register", "a.go::Server.handle", "references")
	if !ok {
		t.Fatalf("method value s.handle has no references edge; edge types %v", edgeTypes(t, st, "a.go::Server.Register", "a.go::Server.handle"))
	}
	if line, _ := lineMeta(r); line != 10 {
		t.Fatalf("reference line = %d, want 10", line)
	}
}

// implementerTree writes one interface Replacer in internal/idx (called by use)
// and n concrete implementers of Replace, each in its own package.
func implementerTree(n int) map[string]string {
	files := map[string]string{
		"internal/idx/use.go": "package idx\n\ntype Replacer interface {\n\tReplace(x int)\n}\n\n" +
			"func use(r Replacer) {\n\tr.Replace(1)\n}\n",
	}
	for i := range n {
		pkg := fmt.Sprintf("impl%d", i)
		files[fmt.Sprintf("internal/%s/a.go", pkg)] = "package " + pkg + "\n\ntype A struct{}\n\nfunc (A) Replace(x int) {}\n"
	}
	return files
}

// TestGoInterfaceDispatchToOneImplementer: r.Replace(1) on a local interface
// reaches the single implementer in a package idx does not import, at 0.3.
func TestGoInterfaceDispatchToOneImplementer(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, implementerTree(1))
	indexTree(t, st, dir)
	r, ok := edgeTo(t, st, "internal/idx/use.go::use", "internal/impl0/a.go::A.Replace", "calls")
	if !ok {
		t.Fatalf("interface call r.Replace has no edge to the implementer; outgoing %v", targetsFrom(t, st, "internal/idx/use.go::use", "calls"))
	}
	if r.Confidence != 0.3 {
		t.Fatalf("confidence = %v, want 0.3", r.Confidence)
	}
	if got, _ := r.Metadata["resolution"].(string); got != "interface-dispatch" {
		t.Fatalf("resolution = %q, want interface-dispatch", got)
	}
	if line, _ := lineMeta(r); line != 8 {
		t.Fatalf("call-site line = %d, want 8", line)
	}
}

// TestGoInterfaceDispatchCapsAtSixCandidates: seven implementers of Replace are
// too many to name a target, so the call links to none of them.
func TestGoInterfaceDispatchCapsAtSixCandidates(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, implementerTree(7))
	indexTree(t, st, dir)
	if got := targetsFrom(t, st, "internal/idx/use.go::use", "calls"); len(got) != 0 {
		t.Fatalf("seven candidates still linked: %v", got)
	}
}

// TestGoInterfaceDispatchSkipsStdlibCommonNames: Close is a stdlib-common name.
// Flusher declares it in another package, and the caller declares no Close
// interface of its own, so f.Close() links to no implementer.
func TestGoInterfaceDispatchSkipsStdlibCommonNames(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, map[string]string{
		"internal/other/io.go": "package other\n\ntype Flusher interface {\n\tClose()\n}\n",
		"internal/impl/a.go":   "package impl\n\ntype A struct{}\n\nfunc (A) Close() {}\n",
		"internal/idx/use.go": "package idx\n\nimport \"" + modPath + "/internal/other\"\n\n" +
			"func use(f other.Flusher) {\n\tf.Close()\n}\n",
	})
	indexTree(t, st, dir)
	if got := targetsFrom(t, st, "internal/idx/use.go::use", "calls"); len(got) != 0 {
		t.Fatalf("stdlib-common Close linked through interface dispatch: %v", got)
	}

	// Control: the same call links once the caller's own package declares a
	// Close interface, which is the exception the rule allows.
	putFiles(t, dir, map[string]string{
		"internal/idx/closer.go": "package idx\n\ntype Closer interface {\n\tClose()\n}\n",
	})
	indexTree(t, st, dir)
	if _, ok := edgeTo(t, st, "internal/idx/use.go::use", "internal/impl/a.go::A.Close", "calls"); !ok {
		t.Fatal("with a Close interface in the caller's package the call should link; it does not")
	}
}

// TestGoCrossPackageEdgesSurviveIncrementalRun: after the target package changes,
// the unchanged caller's edge into it is still there (its outgoing edges are not
// touched). Removing the target and re-indexing drops the edge through the orphan
// sweep, so no edge names a symbol that no longer exists.
func TestGoCrossPackageEdgesSurviveIncrementalRun(t *testing.T) {
	st, dir := newCrossTree(t)
	const target = "internal/store/store.go::Open"
	const caller = "cmd/app/main.go::run"
	putFiles(t, dir, map[string]string{
		"internal/store/store.go": "package store\n\nfunc Open(p string) {}\n",
		"cmd/app/main.go":         "package main\n\nimport \"" + modPath + "/internal/store\"\n\nfunc run() {\n\tstore.Open(\"p\")\n}\n",
	})
	indexTree(t, st, dir)

	putFiles(t, dir, map[string]string{"internal/store/store.go": "package store\n\n// Open opens a store.\nfunc Open(p string) {}\n\nfunc Extra() {}\n"})
	indexTree(t, st, dir)
	if _, ok := edgeTo(t, st, caller, target, "calls"); !ok {
		t.Fatalf("edge from unchanged cmd/app lost after internal/store changed; outgoing %v", targetsFrom(t, st, caller, "calls"))
	}

	putFiles(t, dir, map[string]string{"internal/store/store.go": "package store\n\nfunc Extra() {}\n"})
	indexTree(t, st, dir)
	if got := targetsFrom(t, st, caller, "calls"); slices.Contains(got, target) {
		t.Fatalf("dangling edge to the removed Open survived the sweep: %v", got)
	}
}

// TestGoCommentAndStringMentionsResolveNothing: a comment and a string literal
// spell store.Open, but neither is a use, so no edge is written.
func TestGoCommentAndStringMentionsResolveNothing(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, map[string]string{
		"internal/store/store.go": "package store\n\nfunc Open(p string) {}\n",
		"cmd/app/main.go": "package main\n\nimport \"" + modPath + "/internal/store\"\n\n" +
			"// store.Open is the entry point.\nfunc run() {\n\t_ = \"store.Open\"\n}\n",
	})
	indexTree(t, st, dir)
	if got := targetsFrom(t, st, "cmd/app/main.go::run", "calls"); len(got) != 0 {
		t.Fatalf("comment or string mention produced calls edges: %v", got)
	}
	if got := strings.Join(targetsFrom(t, st, "cmd/app/main.go::run", "references"), ","); got != "" {
		t.Fatalf("comment or string mention produced references edges: %s", got)
	}
}

// TestGoAstSeedsAreNotCutByPerElementCeiling: a function with more call sites
// than the word-heuristic ceiling (maxEdgesPerElement) still links its last
// call. The AST seeds are exact use sites, so the ceiling does not apply to them.
func TestGoAstSeedsAreNotCutByPerElementCeiling(t *testing.T) {
	const calls = maxEdgesPerElement + 5
	var b strings.Builder
	b.WriteString("package a\n\ntype T struct{}\n\n")
	for i := range calls {
		fmt.Fprintf(&b, "func (T) M%d() {}\n\n", i)
	}
	b.WriteString("func Caller(t T) {\n")
	for i := range calls {
		fmt.Fprintf(&b, "\tt.M%d()\n", i)
	}
	b.WriteString("}\n")
	st := indexedGraph(t, map[string]string{"a.go": b.String()})
	last := fmt.Sprintf("a.go::T.M%d", calls-1)
	if _, ok := edgeTo(t, st, "a.go::Caller", last, "calls"); !ok {
		t.Fatalf("call %d of %d lost to the per-element ceiling", calls, calls)
	}
}
