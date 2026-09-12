package lsp

import (
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

func newTestStore(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return dir, st
}

// TestEnrichMergesLSPSymbolsIntoElements is the acceptance test: a configured
// (fake) language server's document symbols merge into the regex-extracted
// elements — names/kinds/spans, signature detail, hover docs — and symbols
// the regex extractor missed become elements; the in-process typed resolve
// then upgrades the CALLS edge.
func TestEnrichMergesLSPSymbolsIntoElements(t *testing.T) {
	fakeServerDir(t, "rich")
	dir, st := newTestStore(t)
	writeFile(t, dir, "a.go", "package a\n\nfunc Handle(x int) error { return nil }\n")
	writeFile(t, dir, "b.go", "package b\n\nfunc Main() {}\n")
	writeFile(t, dir, "leankg.yaml",
		"indexer:\n  typed_resolve: go\nlsp:\n  servers:\n    go:\n      command: fake-lsp\n      args: [\"--mode=fake\"]\n")
	els := []store.Element{
		{QualifiedName: "a.go::Handle", ElementType: "function", Name: "Handle", FilePath: "a.go",
			LineStart: 3, LineEnd: 7, Language: "go", Content: "func Handle(x int) error {"},
		{QualifiedName: "a.go::Server", ElementType: "class", Name: "Server", FilePath: "a.go",
			LineStart: 9, LineEnd: 21, Language: "go", Content: "type Server struct {"},
		{QualifiedName: "a.go::Serve", ElementType: "method", Name: "Serve", FilePath: "a.go",
			LineStart: 11, LineEnd: 17, Language: "go", Content: "func (s *Server) Serve() {"},
		{QualifiedName: "a.go::Legacy", ElementType: "function", Name: "Legacy", FilePath: "a.go",
			LineStart: 24, LineEnd: 26, Language: "go", Content: "func Legacy() {}"},
		{QualifiedName: "b.go::Main", ElementType: "function", Name: "Main", FilePath: "b.go",
			LineStart: 1, LineEnd: 3, Language: "go", Content: "func Main() {}"},
	}
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRelationships([]store.Relationship{{
		Source: "b.go::Main", Target: "a.go::Handle", RelType: "calls", Confidence: 0.5,
	}}); err != nil {
		t.Fatal(err)
	}

	res, err := Enrich(st, dir, []string{"go"})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if !res.Enabled {
		t.Fatalf("Enabled = false (reason %q)", res.Reason)
	}
	if len(res.Languages) != 1 || res.Languages[0].Language != "go" {
		t.Fatalf("languages = %+v", res.Languages)
	}
	if got := res.Languages[0].Tier; got != TierConfigured {
		t.Errorf("tier = %q, want %q", got, TierConfigured)
	}
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"files", res.Files, 2},
		{"symbols", res.Symbols, 5},
		{"merged", res.Merged, 3},
		{"new_elements", res.NewElements, 2},
		{"signatures", res.Signatures, 3},
		{"calls_upgraded", res.CallsUpgraded, 1},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}

	// Enriched elements carry LSP metadata + signature docs.
	handle := mustFind(t, st, "a.go::Handle")
	if handle.Metadata["lsp_enriched"] != true {
		t.Errorf("Handle not marked enriched: %v", handle.Metadata)
	}
	if got := handle.Metadata["lsp_kind_name"]; got != "function" {
		t.Errorf("Handle kind_name = %v", got)
	}
	if got := handle.Metadata["lsp_signature"]; got != "func Handle(x int) error" {
		t.Errorf("Handle signature = %v", got)
	}
	if got := handle.Metadata["lsp_tier"]; got != TierConfigured {
		t.Errorf("Handle tier = %v", got)
	}
	server := mustFind(t, st, "a.go::Server")
	if got := server.Metadata["lsp_doc"]; got != "server hover documentation" {
		t.Errorf("Server doc = %v", got)
	}
	if got := server.Metadata["lsp_kind_name"]; got != "class" {
		t.Errorf("Server kind_name = %v", got)
	}
	serve := mustFind(t, st, "a.go::Serve")
	if got := serve.Metadata["lsp_kind_name"]; got != "method" {
		t.Errorf("Serve kind_name = %v", got)
	}
	for _, qn := range []string{"a.go::Legacy", "b.go::Main"} {
		if m := mustFind(t, st, qn).Metadata; m != nil && m["lsp_enriched"] == true {
			t.Errorf("%s should not be enriched: %v", qn, m)
		}
	}

	// Symbols the regex extractor missed became elements.
	cfg := mustFind(t, st, "a.go::Config")
	if cfg.ElementType != "struct" || cfg.LineStart != 31 || cfg.LineEnd != 35 {
		t.Errorf("Config element = %+v", cfg)
	}
	if got := cfg.Metadata["source"]; got != "lsp" {
		t.Errorf("Config source = %v", got)
	}
	if got := cfg.Language; got != "go" {
		t.Errorf("Config language = %q", got)
	}
	helper := mustFind(t, st, "b.go::Helper2")
	if helper.ElementType != "function" || helper.LineStart != 11 || helper.LineEnd != 13 {
		t.Errorf("Helper2 element = %+v", helper)
	}

	// The CALLS edge was upgraded by the in-process tier.
	rels, err := st.RelationshipsAll(10)
	if err != nil || len(rels) != 1 {
		t.Fatalf("relationships = %v (%v)", rels, err)
	}
	if got := rels[0].Metadata["resolution_method"]; got != "typed" {
		t.Errorf("resolution_method = %v", got)
	}
	if rels[0].Confidence != 0.98 {
		t.Errorf("confidence = %v, want 0.98", rels[0].Confidence)
	}

	// Empty want list = every language present in the index.
	res, err = Enrich(st, dir, nil)
	if err != nil {
		t.Fatalf("Enrich(all): %v", err)
	}
	if len(res.Languages) != 1 || res.Languages[0].Language != "go" {
		t.Errorf("languages = %+v", res.Languages)
	}
	if res.NewElements != 0 {
		t.Errorf("second pass created %d elements, want 0", res.NewElements)
	}
}

func TestEnrichWithoutServerIsNoOp(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // nothing on PATH: no catalog server resolves
	dir, st := newTestStore(t)
	if err := st.UpsertElements([]store.Element{elem(
		"main.rs::boot", "function", "boot", "main.rs", "rust", "",
	)}); err != nil {
		t.Fatal(err)
	}

	res, err := Enrich(st, dir, []string{"rust"})
	if err != nil {
		t.Fatalf("Enrich err = %v, want nil (graceful absence)", err)
	}
	if res.Enabled {
		t.Error("Enabled = true with no server available")
	}
	if res.Reason == "" {
		t.Error("Reason empty for a no-op pass")
	}
	if len(res.Languages) != 1 || res.Languages[0].Reason == "" {
		t.Errorf("languages = %+v, want a per-language reason", res.Languages)
	}
	if m := mustFind(t, st, "main.rs::boot").Metadata; m != nil && m["lsp_enriched"] == true {
		t.Errorf("element mutated by a no-op pass: %v", m)
	}
}

// TestEnrichTypedResolveOffByDefault pins Rust parity: without an
// indexer.typed_resolve setting the in-process tier leaves CALLS edges alone,
// even though the LSP merge still runs.
func TestEnrichTypedResolveOffByDefault(t *testing.T) {
	fakeServerDir(t, "rich")
	dir, st := newTestStore(t)
	writeFile(t, dir, "a.go", "package a\n\nfunc Handle(x int) error { return nil }\n")
	writeFile(t, dir, "leankg.yaml",
		"lsp:\n  servers:\n    go:\n      command: fake-lsp\n")
	if err := st.UpsertElements([]store.Element{
		{QualifiedName: "a.go::Handle", ElementType: "function", Name: "Handle", FilePath: "a.go",
			LineStart: 3, LineEnd: 7, Language: "go", Content: "func Handle(x int) error {"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRelationships([]store.Relationship{{
		Source: "a.go::Handle", Target: "a.go::Handle", RelType: "calls", Confidence: 0.5,
	}}); err != nil {
		t.Fatal(err)
	}

	res, err := Enrich(st, dir, []string{"go"})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if res.Merged == 0 {
		t.Errorf("LSP merge did not run: %+v", res)
	}
	if res.CallsUpgraded != 0 {
		t.Errorf("CallsUpgraded = %d, want 0 with typed_resolve unset", res.CallsUpgraded)
	}
	rels, err := st.RelationshipsAll(10)
	if err != nil || len(rels) != 1 {
		t.Fatalf("relationships = %v (%v)", rels, err)
	}
	if _, ok := rels[0].Metadata["resolution_method"]; ok {
		t.Errorf("edge mutated with typed_resolve off: %v", rels[0].Metadata)
	}
}

// TestEnrichAcceptsRegistryActiveLanguages mirrors the integration snippet
// the indexer will use: Active() language tags are passed straight through.
func TestEnrichAcceptsRegistryActiveLanguages(t *testing.T) {
	fakeServerDir(t, "rich")
	dir, st := newTestStore(t)
	writeFile(t, dir, "go.mod", "module scratch\n")
	writeFile(t, dir, "a.go", "package scratch\n\nfunc Handle(x int) error { return nil }\n")
	writeFile(t, dir, "leankg.yaml",
		"lsp:\n  servers:\n    go:\n      command: fake-lsp\n")
	if err := st.UpsertElements([]store.Element{
		{QualifiedName: "a.go::Handle", ElementType: "function", Name: "Handle", FilePath: "a.go",
			LineStart: 3, LineEnd: 7, Language: "go", Content: "func Handle(x int) error {"},
	}); err != nil {
		t.Fatal(err)
	}

	reg := langs.NewRegistry()
	if _, err := reg.Activate(dir); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	var active []string
	for _, l := range reg.Active() {
		active = append(active, string(l))
	}
	res, err := Enrich(st, dir, active)
	if err != nil {
		t.Fatalf("Enrich(%v): %v", active, err)
	}
	if len(res.Languages) != 1 || res.Languages[0].Language != "go" || res.Merged == 0 {
		t.Errorf("res = %+v (active=%v)", res, active)
	}
}

func TestEnrichConfiguredButMissingServerIsNoOp(t *testing.T) {
	fakeServerDir(t, "rich")
	dir, st := newTestStore(t)
	writeFile(t, dir, "leankg.yaml",
		"lsp:\n  servers:\n    go:\n      command: not-a-real-lsp-binary\n")
	if err := st.UpsertElements([]store.Element{elem(
		"main.go::main", "function", "main", "main.go", "go", "",
	)}); err != nil {
		t.Fatal(err)
	}

	res, err := Enrich(st, dir, []string{"go"})
	if err != nil {
		t.Fatalf("Enrich err = %v, want nil", err)
	}
	if res.Enabled || res.Reason == "" {
		t.Errorf("Enabled=%v reason=%q, want a no-op with a reason", res.Enabled, res.Reason)
	}
	if len(res.Languages) != 1 || res.Languages[0].Tier != TierConfigured {
		t.Errorf("languages = %+v, want configured tier recorded", res.Languages)
	}
}

func mustFind(t *testing.T, st store.Backend, qn string) store.Element {
	t.Helper()
	els, err := st.FindExact(qn)
	if err != nil {
		t.Fatalf("FindExact(%s): %v", qn, err)
	}
	for _, e := range els {
		if e.QualifiedName == qn {
			return e
		}
	}
	t.Fatalf("element %s not found", qn)
	return store.Element{}
}

// TestEnrichExpandedLanguageWithoutServerIsGraceful: expansion tags whose
// language has no server in the Rust LSP registry (csharp, perl, cuda, ...)
// report a per-language reason and never fail the pass.
func TestEnrichExpandedLanguageWithoutServerIsGraceful(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir, st := newTestStore(t)
	if err := st.UpsertElements([]store.Element{
		elem("Program.cs::Main", "function", "Main", "Program.cs", "csharp", ""),
	}); err != nil {
		t.Fatal(err)
	}

	res, err := Enrich(st, dir, []string{"csharp"})
	if err != nil {
		t.Fatalf("Enrich err = %v, want nil", err)
	}
	if res.Enabled {
		t.Error("Enabled = true for a language with no catalog server")
	}
	if len(res.Languages) != 1 || res.Languages[0].Language != "csharp" || res.Languages[0].Reason == "" {
		t.Errorf("languages = %+v, want csharp with a reason", res.Languages)
	}
	if m := mustFind(t, st, "Program.cs::Main").Metadata; m != nil && m["lsp_enriched"] == true {
		t.Errorf("element mutated: %v", m)
	}
}
