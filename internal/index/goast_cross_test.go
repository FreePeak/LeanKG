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
// too many to name a target, so the call links to none of them. The call still
// links to the interface's own spec, which its static receiver type names.
func TestGoInterfaceDispatchCapsAtSixCandidates(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, implementerTree(7))
	indexTree(t, st, dir)
	got := targetsFrom(t, st, "internal/idx/use.go::use", "calls")
	if !slices.Equal(got, []string{"internal/idx/use.go::Replacer.Replace"}) {
		t.Fatalf("seven candidates: calls targets = %v, want only the Replacer.Replace spec", got)
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

	// Control: a receiver typed with an interface the caller's own package
	// declares links, which is the exception the rule allows.
	putFiles(t, dir, map[string]string{
		"internal/idx/closer.go": "package idx\n\ntype Closer interface {\n\tClose()\n}\n\n" +
			"func useCloser(c Closer) {\n\tc.Close()\n}\n",
	})
	indexTree(t, st, dir)
	if _, ok := edgeTo(t, st, "internal/idx/closer.go::useCloser", "internal/impl/a.go::A.Close", "calls"); !ok {
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

// TestGoInterfaceSpecsDoNotStarveCallEdges: a 600-method interface links each
// method spec to it with a contains edge. Those contains edges must not spend
// the per-file call ceiling, so a call in a later function still links.
func TestGoInterfaceSpecsDoNotStarveCallEdges(t *testing.T) {
	var b strings.Builder
	b.WriteString("package a\n\ntype Big interface {\n")
	for i := range 600 {
		fmt.Fprintf(&b, "\tM%d()\n", i)
	}
	b.WriteString("}\n\nfunc GatherAll() {}\n\nfunc Caller() {\n\tGatherAll()\n}\n")
	st := indexedGraph(t, map[string]string{"a.go": b.String()})
	if _, ok := edgeTo(t, st, "a.go::Caller", "a.go::GatherAll", "calls"); !ok {
		t.Fatal("contains edges of the interface specs cut the call edge Caller -> GatherAll")
	}
}

// precisionTree writes files and indexes them into a fresh store. The
// precision tests below read edges back by source and target.
func precisionTree(t *testing.T, files map[string]string) store.Backend {
	t.Helper()
	st, dir := newCrossTree(t)
	putFiles(t, dir, files)
	indexTree(t, st, dir)
	return st
}

// noEdgeTo fails when src has a calls or references edge to dst.
func noEdgeTo(t *testing.T, st store.Backend, src, dst string) {
	t.Helper()
	for _, rel := range []string{"calls", "references"} {
		if r, ok := edgeTo(t, st, src, dst, rel); ok {
			t.Fatalf("%s has a %s edge to %s (%+v), want none", src, rel, dst, r)
		}
	}
}

// TestGoFieldReadIsNotAnEdge: r.Mode reads a field of Result. Opt in the same
// package declares a method Mode, but a plain field read names no method.
func TestGoFieldReadIsNotAnEdge(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/res/res.go": "package res\n\ntype Result struct {\n\tMode int\n}\n\n" +
			"type Opt struct{}\n\nfunc (Opt) Mode() {}\n\n" +
			"func show(r Result) int {\n\treturn r.Mode\n}\n",
	})
	noEdgeTo(t, st, "internal/res/res.go::show", "internal/res/res.go::Opt.Mode")
}

// TestGoFieldReadInCallArgIsNotAnEdge: sink(daos[0].Name) passes a field value.
// Name is a field of Dao in the package, so the value is not a method value.
func TestGoFieldReadInCallArgIsNotAnEdge(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/dao/dao.go": "package dao\n\ntype Dao struct {\n\tName string\n}\n\n" +
			"type Opt struct{}\n\nfunc (Opt) Name() string { return \"\" }\n\n" +
			"func sink(v string) {}\n\nfunc names(daos []Dao) {\n\tsink(daos[0].Name)\n}\n",
	})
	noEdgeTo(t, st, "internal/dao/dao.go::names", "internal/dao/dao.go::Opt.Name")
}

// TestGoTestingReceiverIsNotLinked: t.Fatal on a *testing.T is not Logger.Fatal,
// the module method of the same package.
func TestGoTestingReceiverIsNotLinked(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/app/log.go":      "package app\n\ntype Logger struct{}\n\nfunc (Logger) Fatal(msg string) {}\n",
		"internal/app/app_test.go": "package app\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\tt.Fatal(\"x\")\n}\n",
	})
	noEdgeTo(t, st, "internal/app/app_test.go::TestX", "internal/app/log.go::Logger.Fatal")
}

// TestGoStdlibReceiverIsNotLinked: rows.Close() on a *sql.Rows is not
// Store.Close of the same package.
func TestGoStdlibReceiverIsNotLinked(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/db/db.go": "package db\n\nimport \"database/sql\"\n\ntype Store struct{}\n\nfunc (Store) Close() {}\n\n" +
			"func drain(rows *sql.Rows) {\n\trows.Close()\n}\n",
	})
	noEdgeTo(t, st, "internal/db/db.go::drain", "internal/db/db.go::Store.Close")
}

// TestGoSameNameMethodsResolveByReceiverType: g.Fetch() on a *GitSource reaches
// GitSource.Fetch only, not GcsSource.Fetch of the same package.
func TestGoSameNameMethodsResolveByReceiverType(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/src/src.go": "package src\n\ntype GitSource struct {\n\turi string\n}\n\n" +
			"func (g *GitSource) Fetch() {}\n\nfunc (g *GitSource) read() string {\n\treturn g.uri\n}\n\n" +
			"func (g *GitSource) run() {\n\tg.Fetch()\n}\n\n" +
			"type GcsSource struct{}\n\nfunc (g *GcsSource) Fetch() {}\n\nfunc (g *GcsSource) uri() string { return \"\" }\n",
	})
	if _, ok := edgeTo(t, st, "internal/src/src.go::GitSource.run", "internal/src/src.go::GitSource.Fetch", "calls"); !ok {
		t.Fatalf("g.Fetch() has no edge to GitSource.Fetch; outgoing %v", targetsFrom(t, st, "internal/src/src.go::GitSource.run", "calls"))
	}
	if _, ok := edgeTo(t, st, "internal/src/src.go::GitSource.run", "internal/src/src.go::GcsSource.Fetch", "calls"); ok {
		t.Fatal("g.Fetch() on a *GitSource is linked to GcsSource.Fetch")
	}
	noEdgeTo(t, st, "internal/src/src.go::GitSource.read", "internal/src/src.go::GcsSource.uri")
}

// TestGoReceiverFromFieldAndConstructorResolves: h.src.Fetch() is typed through
// the struct field src, and g.Fetch() through the same-package constructor
// NewGit. Both reach GitSource.Fetch only.
func TestGoReceiverFromFieldAndConstructorResolves(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/src/src.go": "package src\n\ntype GitSource struct{}\n\nfunc (g *GitSource) Fetch() {}\n\n" +
			"type GcsSource struct{}\n\nfunc (g *GcsSource) Fetch() {}\n\n" +
			"type Holder struct {\n\tsrc *GitSource\n}\n\nfunc NewGit() *GitSource {\n\treturn &GitSource{}\n}\n\n" +
			"func (h *Holder) run() {\n\th.src.Fetch()\n\tg := NewGit()\n\tg.Fetch()\n}\n",
	})
	const run = "internal/src/src.go::Holder.run"
	if _, ok := edgeTo(t, st, run, "internal/src/src.go::GitSource.Fetch", "calls"); !ok {
		t.Fatalf("typed Fetch calls have no edge to GitSource.Fetch; outgoing %v", targetsFrom(t, st, run, "calls"))
	}
	if _, ok := edgeTo(t, st, run, "internal/src/src.go::GcsSource.Fetch", "calls"); ok {
		t.Fatal("typed Fetch calls are linked to GcsSource.Fetch")
	}
}

// TestGoInterfaceParamDispatchToTwoImplementers: a parameter typed with a local
// interface links its method to both implementers in unimported packages, at 0.3.
func TestGoInterfaceParamDispatchToTwoImplementers(t *testing.T) {
	st := precisionTree(t, implementerTree(2))
	for _, impl := range []string{"internal/impl0/a.go::A.Replace", "internal/impl1/a.go::A.Replace"} {
		r, ok := edgeTo(t, st, "internal/idx/use.go::use", impl, "calls")
		if !ok {
			t.Fatalf("no interface-dispatch edge to %s; outgoing %v", impl, targetsFrom(t, st, "internal/idx/use.go::use", "calls"))
		}
		if r.Confidence != 0.3 {
			t.Fatalf("%s confidence = %v, want 0.3", impl, r.Confidence)
		}
	}
}

// TestGoUnknownReceiverLinksNothing: get().Frobnicate() has a receiver of unknown
// type. Frobnicate is the one method of that name in the module, but an unknown
// receiver links to nothing (the unique-name rule was measured below target and
// dropped).
func TestGoUnknownReceiverLinksNothing(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/geo/geo.go": "package geo\n\ntype Shape struct{}\n\nfunc (Shape) Frobnicate() {}\n",
		"internal/app/run.go": "package app\n\nfunc run(get func() int) {\n\tget().Frobnicate()\n}\n",
	})
	noEdgeTo(t, st, "internal/app/run.go::run", "internal/geo/geo.go::Shape.Frobnicate")
}

// TestGoUnknownReceiverTwoCandidatesNoEdge: Frobnicate is a method of two types in
// two packages, so an unknown receiver links to neither.
func TestGoUnknownReceiverTwoCandidatesNoEdge(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/geo/geo.go":   "package geo\n\ntype Shape struct{}\n\nfunc (Shape) Frobnicate() {}\n",
		"internal/mesh/mesh.go": "package mesh\n\ntype Net struct{}\n\nfunc (Net) Frobnicate() {}\n",
		"internal/app/run.go":   "package app\n\nfunc run(get func() int) {\n\tget().Frobnicate()\n}\n",
	})
	noEdgeTo(t, st, "internal/app/run.go::run", "internal/geo/geo.go::Shape.Frobnicate")
	noEdgeTo(t, st, "internal/app/run.go::run", "internal/mesh/mesh.go::Net.Frobnicate")
}

// TestGoMethodValueNamedLikeFieldNoEdge: register(c.Mode) passes the field Mode of
// the typed Config. Config has no method Mode, so the value links nowhere, even
// though Opt in the package declares one.
func TestGoMethodValueNamedLikeFieldNoEdge(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/cfg/cfg.go": "package cfg\n\ntype Config struct {\n\tMode int\n}\n\n" +
			"type Opt struct{}\n\nfunc (Opt) Mode() {}\n\nfunc register(f any) {}\n\n" +
			"func load(c Config) {\n\tregister(c.Mode)\n}\n",
	})
	noEdgeTo(t, st, "internal/cfg/cfg.go::load", "internal/cfg/cfg.go::Opt.Mode")
}

// TestGoMethodValueArgumentReferences: a method value passed as a call argument
// from a typed receiver is a references edge with its line. Store.Use is the
// same-package method, and the typed receiver s makes it exact.
func TestGoMethodValueArgumentReferences(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"a.go": "package a\n\ntype Server struct{}\n\nfunc (s *Server) handle() {}\n\n" +
			"func reg(f func()) {}\n\nfunc (s *Server) Register() {\n\treg(s.handle)\n}\n",
	})
	r, ok := edgeTo(t, st, "a.go::Server.Register", "a.go::Server.handle", "references")
	if !ok {
		t.Fatalf("method value has no references edge; outgoing %v", targetsFrom(t, st, "a.go::Server.Register", "references"))
	}
	if line, _ := lineMeta(r); line != 10 {
		t.Fatalf("reference line = %d, want 10", line)
	}
}

// TestGoInterfaceFromUnchangedPackageDispatchesIncrementally: the interface lives
// in a package the second run does not re-extract, so its method set comes from
// the KV record the first run wrote. The caller's call still dispatches.
func TestGoInterfaceFromUnchangedPackageDispatchesIncrementally(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, map[string]string{
		"internal/store/store.go": "package store\n\ntype Store interface {\n\tReplace(x int)\n}\n",
		"internal/impl/a.go":      "package impl\n\ntype A struct{}\n\nfunc (A) Replace(x int) {}\n",
		"internal/idx/use.go":     "package idx\n\nimport \"" + modPath + "/internal/store\"\n\nfunc use(s store.Store) {\n\ts.Replace(1)\n}\n",
	})
	indexTree(t, st, dir)

	putFiles(t, dir, map[string]string{"internal/idx/use.go": "package idx\n\nimport \"" + modPath + "/internal/store\"\n\n// use replaces.\nfunc use(s store.Store) {\n\ts.Replace(2)\n}\n"})
	indexTree(t, st, dir)
	r, ok := edgeTo(t, st, "internal/idx/use.go::use", "internal/impl/a.go::A.Replace", "calls")
	if !ok {
		t.Fatalf("interface from an unchanged package lost its dispatch edge; outgoing %v", targetsFrom(t, st, "internal/idx/use.go::use", "calls"))
	}
	if got, _ := r.Metadata["resolution"].(string); got != "interface-dispatch" {
		t.Fatalf("resolution = %q, want interface-dispatch", got)
	}
}

// TestGoTypeParameterIsNotAPackageName: Item is a type parameter of Wrap, so its
// uses are not the package-level function Item, and Wrap links to nothing.
func TestGoTypeParameterIsNotAPackageName(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\nfunc Item() {}\n\n" +
		"func Wrap[Item any](x Item) Item {\n\treturn x\n}\n"})
	if _, ok := edgeTo(t, st, "a.go::Wrap", "a.go::Item", "references"); ok {
		t.Fatal("type parameter Item links to the package function Item")
	}
}

// TestGoInterfaceEmbeddedMethodDispatches: Store embeds IndexWriter from another
// package and does not declare Replace itself. s.Replace() on a Store still
// dispatches to the module's implementer of Replace, at 0.3.
func TestGoInterfaceEmbeddedMethodDispatches(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/store/roles.go": "package store\n\ntype IndexWriter interface {\n\tReplace(x int)\n}\n",
		"internal/store/impl.go":  "package store\n\ntype DB struct{}\n\nfunc (DB) Replace(x int) {}\n\nfunc (DB) Flush() {}\n",
		"internal/idx/use.go": "package idx\n\nimport \"" + modPath + "/internal/store\"\n\n" +
			"type Store interface {\n\tstore.IndexWriter\n\tFlush()\n}\n\nfunc use(s Store) {\n\ts.Replace(1)\n}\n",
	})
	r, ok := edgeTo(t, st, "internal/idx/use.go::use", "internal/store/impl.go::DB.Replace", "calls")
	if !ok {
		t.Fatalf("embedded Replace has no dispatch edge; outgoing %v", targetsFrom(t, st, "internal/idx/use.go::use", "calls"))
	}
	if r.Confidence != 0.3 {
		t.Fatalf("confidence = %v, want 0.3", r.Confidence)
	}
}

// TestGoInterfaceDispatchNeedsEveryMethod: Partial has Replace but not Flush, so
// it does not implement Store and a call s.Replace() on a Store does not reach
// it. Full implements both methods and is linked.
func TestGoInterfaceDispatchNeedsEveryMethod(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/store/iface.go": "package store\n\ntype Store interface {\n\tReplace(x int)\n\tFlush()\n}\n",
		"internal/impl/a.go":      "package impl\n\ntype Full struct{}\n\nfunc (Full) Replace(x int) {}\n\nfunc (Full) Flush() {}\n",
		"internal/impl/b.go":      "package impl\n\ntype Partial struct{}\n\nfunc (Partial) Replace(x int) {}\n",
		"internal/idx/use.go": "package idx\n\nimport \"" + modPath + "/internal/store\"\n\n" +
			"func use(s store.Store) {\n\ts.Replace(1)\n}\n",
	})
	const src = "internal/idx/use.go::use"
	if _, ok := edgeTo(t, st, src, "internal/impl/a.go::Full.Replace", "calls"); !ok {
		t.Fatalf("Full implements Store but has no dispatch edge; outgoing %v", targetsFrom(t, st, src, "calls"))
	}
	noEdgeTo(t, st, src, "internal/impl/b.go::Partial.Replace")
}

// TestGoMultiValueConstructorResultIsTyped: g is the first result of NewPair, a
// same-package function with two results, so g.Fetch() reaches GitSource.Fetch
// only, not GcsSource.Fetch.
func TestGoMultiValueConstructorResultIsTyped(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/src/src.go": "package src\n\ntype GitSource struct{}\n\nfunc (g *GitSource) Fetch() {}\n\n" +
			"type GcsSource struct{}\n\nfunc (g *GcsSource) Fetch() {}\n\n" +
			"func NewPair() (*GitSource, *GcsSource, error) {\n\treturn nil, nil, nil\n}\n\n" +
			"func run() {\n\tg, _, _ := NewPair()\n\tg.Fetch()\n}\n",
	})
	const run = "internal/src/src.go::run"
	if _, ok := edgeTo(t, st, run, "internal/src/src.go::GitSource.Fetch", "calls"); !ok {
		t.Fatalf("first result of NewPair has no edge to GitSource.Fetch; outgoing %v", targetsFrom(t, st, run, "calls"))
	}
	noEdgeTo(t, st, run, "internal/src/src.go::GcsSource.Fetch")
}

// TestGoExternalReceiversAreNotLinked: a value from a stdlib call
// (strings.NewReplacer) or a type assertion to a stdlib type (*exec.ExitError)
// has an external type, so Replace and ExitCode do not reach the module's
// methods of the same names.
func TestGoExternalReceiversAreNotLinked(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/memory/memory.go": "package memory\n\nimport (\n\t\"os/exec\"\n\t\"strings\"\n)\n\ntype Memory struct{}\n\nfunc (m *Memory) Replace(s string) string { return s }\n\n" +
			"type Report struct{}\n\nfunc (r Report) ExitCode() int { return 0 }\n\n" +
			"func clean(s string) string {\n\treturn strings.NewReplacer(\"a\", \"b\").Replace(s)\n}\n\n" +
			"func code(err error) int {\n\tee, ok := err.(*exec.ExitError)\n\tif !ok {\n\t\treturn 1\n\t}\n\treturn ee.ExitCode()\n}\n",
	})
	noEdgeTo(t, st, "internal/memory/memory.go::clean", "internal/memory/memory.go::Memory.Replace")
	noEdgeTo(t, st, "internal/memory/memory.go::code", "internal/memory/memory.go::Report.ExitCode")
}

// TestGoConstructorFromImportedPackageTypesReceiver: srv := mcp.New() is typed by
// the result of mcp.New (*Server), read from the constructor's package, so
// srv.HTTPHandler() reaches Server.HTTPHandler and not Other.HTTPHandler. The
// second run changes only the caller, so mcp is read from its KV record.
func TestGoConstructorFromImportedPackageTypesReceiver(t *testing.T) {
	st, dir := newCrossTree(t)
	putFiles(t, dir, map[string]string{
		"internal/mcp/server.go":  "package mcp\n\ntype Server struct{}\n\nfunc (s *Server) HTTPHandler() {}\n\nfunc New() *Server {\n\treturn &Server{}\n}\n",
		"internal/other/other.go": "package other\n\ntype Other struct{}\n\nfunc (Other) HTTPHandler() {}\n",
		"cmd/app/main.go":         "package main\n\nimport \"" + modPath + "/internal/mcp\"\n\nfunc run() {\n\tsrv := mcp.New()\n\tsrv.HTTPHandler()\n}\n",
	})
	indexTree(t, st, dir)
	const src, server, other = "cmd/app/main.go::run", "internal/mcp/server.go::Server.HTTPHandler", "internal/other/other.go::Other.HTTPHandler"
	r, ok := edgeTo(t, st, src, server, "calls")
	if !ok {
		t.Fatalf("srv typed by mcp.New has no edge to Server.HTTPHandler; outgoing %v", targetsFrom(t, st, src, "calls"))
	}
	if got, _ := r.Metadata["resolution"].(string); got != "method-by-name" {
		t.Fatalf("resolution = %q, want method-by-name", got)
	}
	noEdgeTo(t, st, src, other)

	putFiles(t, dir, map[string]string{"cmd/app/main.go": "package main\n\nimport \"" + modPath + "/internal/mcp\"\n\n// run serves.\nfunc run() {\n\tsrv := mcp.New()\n\tsrv.HTTPHandler()\n}\n"})
	indexTree(t, st, dir)
	if _, ok := edgeTo(t, st, src, server, "calls"); !ok {
		t.Fatal("after an incremental run the constructor type is lost")
	}
	noEdgeTo(t, st, src, other)
}

// anonUseTree is implementerTree(n) with internal/idx/use.go replaced by the
// given source, so the caller's receiver is typed by an inline interface literal.
func anonUseTree(t *testing.T, n int, use string) store.Backend {
	t.Helper()
	files := implementerTree(n)
	files["internal/idx/use.go"] = use
	return precisionTree(t, files)
}

// TestGoAnonymousInterfaceParamDispatchToTwoImplementers: r is a parameter typed
// by the inline literal interface{ Replace(x int) }, not by a named interface. The
// call on line 4 links to both implementers in unimported packages, at 0.3, the
// same as a named local interface.
func TestGoAnonymousInterfaceParamDispatchToTwoImplementers(t *testing.T) {
	st := anonUseTree(t, 2, "package idx\n\nfunc use(r interface{ Replace(x int) }) {\n\tr.Replace(1)\n}\n")
	for _, impl := range []string{"internal/impl0/a.go::A.Replace", "internal/impl1/a.go::A.Replace"} {
		r, ok := edgeTo(t, st, "internal/idx/use.go::use", impl, "calls")
		if !ok {
			t.Fatalf("no interface-dispatch edge to %s; outgoing %v", impl, targetsFrom(t, st, "internal/idx/use.go::use", "calls"))
		}
		if r.Confidence != 0.3 {
			t.Fatalf("%s confidence = %v, want 0.3", impl, r.Confidence)
		}
		if got, _ := r.Metadata["resolution"].(string); got != "interface-dispatch" {
			t.Fatalf("%s resolution = %q, want interface-dispatch", impl, got)
		}
		if line, _ := lineMeta(r); line != 4 {
			t.Fatalf("%s call-site line = %d, want 4", impl, line)
		}
	}
}

// TestGoAnonymousInterfaceCommonOnlyLinksNothing: interface{ Close() error } is
// a literal whose whole method set is stdlib-common, so r.Close() links to no
// implementer, even though the module has one Close method outside the caller's
// package. A literal gets no same-package exemption for common names.
func TestGoAnonymousInterfaceCommonOnlyLinksNothing(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/impl/a.go":  "package impl\n\ntype A struct{}\n\nfunc (A) Close() error { return nil }\n",
		"internal/idx/use.go": "package idx\n\nfunc use(r interface{ Close() error }) {\n\t_ = r.Close()\n}\n",
	})
	if got := targetsFrom(t, st, "internal/idx/use.go::use", "calls"); len(got) != 0 {
		t.Fatalf("stdlib-common Close through a literal linked: %v", got)
	}
}

// TestGoAnonymousInterfaceIgnoresStdlibEmbed: the literal embeds io.Reader and
// declares Replace. Only the module-visible spec name Replace counts, so the call
// links to the implementer of Replace (which has no Read), and the stdlib embed
// neither adds a method nor blocks the link.
func TestGoAnonymousInterfaceIgnoresStdlibEmbed(t *testing.T) {
	st := anonUseTree(t, 1, "package idx\n\nimport \"io\"\n\n"+
		"func use(r interface {\n\tio.Reader\n\tReplace(x int)\n}) {\n\tr.Replace(1)\n}\n")
	r, ok := edgeTo(t, st, "internal/idx/use.go::use", "internal/impl0/a.go::A.Replace", "calls")
	if !ok {
		t.Fatalf("Replace through a literal with a stdlib embed has no edge; outgoing %v", targetsFrom(t, st, "internal/idx/use.go::use", "calls"))
	}
	if r.Confidence != 0.3 {
		t.Fatalf("confidence = %v, want 0.3", r.Confidence)
	}
}

// TestGoAnonymousInterfaceInEveryDeclarationForm: the literal type is read from a
// var, a struct field, and a constructor result as well as a parameter. Each call
// of Replace on it links to the implementer, at 0.3.
func TestGoAnonymousInterfaceInEveryDeclarationForm(t *testing.T) {
	st := anonUseTree(t, 1, "package idx\n\n"+
		"type holder struct {\n\tr interface{ Replace(x int) }\n}\n\n"+
		"func makeR() interface{ Replace(x int) } { return nil }\n\n"+
		"func viaVar() {\n\tvar r interface{ Replace(x int) }\n\tr.Replace(1)\n}\n\n"+
		"func viaField(h holder) {\n\th.r.Replace(1)\n}\n\n"+
		"func viaResult() {\n\tmakeR().Replace(1)\n}\n")
	for _, fn := range []string{"viaVar", "viaField", "viaResult"} {
		if _, ok := edgeTo(t, st, "internal/idx/use.go::"+fn, "internal/impl0/a.go::A.Replace", "calls"); !ok {
			t.Fatalf("%s: literal-typed Replace has no dispatch edge; outgoing %v", fn, targetsFrom(t, st, "internal/idx/use.go::"+fn, "calls"))
		}
	}
}
