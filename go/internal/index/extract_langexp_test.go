package index

// Round-trip tests for the expanded language extractors (extract_langexp.go):
// fixture file → regex matcher → the shared element pipeline (ends, parents,
// qualify, content) → expected element names and kinds. The pipeline mirrors
// extractFileAs's regex path because the dispatch switch in extract.go is
// integrator-owned; matchLangExp is the exact function Main wires into it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// extractLangExpFixture runs the full regex extraction pipeline on a fixture.
func extractLangExpFixture(t *testing.T, rel, lang string) fileElements {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
	ms, ok := matchLangExp(lang, lines)
	if !ok {
		t.Fatalf("no matcher for language %q", lang)
	}
	// ends: same default branch as extractFileAs — body extends to the next
	// element start.
	ends := make([]int, len(ms))
	for i := range ms {
		ends[i] = len(lines)
		if i+1 < len(ms) {
			ends[i] = ms[i+1].line - 1
		}
		if ends[i] < ms[i].line {
			ends[i] = ms[i].line
		}
	}
	fe := fileElements{rel: rel}
	for i, m := range ms {
		fe.elements = append(fe.elements, indexedElem{
			name: m.name, etype: m.kind, lang: lang,
			start: m.line, end: ends[i], parent: -1, recv: m.recv,
		})
	}
	assignParents(fe.elements)
	qualify(fe)
	boundContent(fe.elements, lines)
	return fe
}

// TestExtractLangExpRoundTrip pins fixture → extract → expected elements for
// every expanded language. Each entry lists the full set of "kind:name"
// elements in source order (kind "import" covers module/path imports per the
// Rust reference's per-language import extraction).
func TestExtractLangExpRoundTrip(t *testing.T) {
	tests := []struct {
		lang, fixture string
		want          []string
	}{
		{"c", "testdata/langexp/c/a.c", []string{
			"import:stdio.h", "import:util.h",
			"type:Point", "type:Color",
			"function:add", "function:scale", "type:PointT",
		}},
		{"c", "testdata/langexp/c/types.h", []string{
			"type:Vec", "type:Blob", "type:ul",
		}},
		{"cpp", "testdata/langexp/cpp/shape.cpp", []string{
			"import:vector", "import:shape.h", "import:std",
			"class:Shape", "class:Circle", "import:geo::Circle", "function:area_of",
		}},
		{"cpp", "testdata/langexp/cpp/geom.hpp", []string{
			"import:string", "class:Rect", "type:uint32",
		}},
		{"csharp", "testdata/langexp/csharp/user.cs", []string{
			"import:System", "import:System.Collections.Generic", "import:System.Math",
			"class:UserService", "method:Find", "method:Save", "class:IRepository",
		}},
		{"csharp", "testdata/langexp/csharp/repo.cs", []string{
			"class:Customer", "class:Point", "class:Kind",
		}},
		{"php", "testdata/langexp/php/service.php", []string{
			"import:vendor/autoload.php", "import:App\\Models\\User",
			"import:App\\Repositories\\UserRepository",
			"class:UserService", "method:find", "method:hydrate", "function:helper",
		}},
		{"php", "testdata/langexp/php/legacy.php", []string{
			"class:Greetable", "class:Greets", "class:Greeter",
			// Ceiling: class-internal trait use reads as an import in the
			// regex tier (see matchPHP).
			"import:Greets", "method:greet",
		}},
		{"ruby", "testdata/langexp/ruby/invoice.rb", []string{
			"import:json", "class:App", "class:Invoice",
			"method:initialize", "method:total_with_tax", "method:build",
		}},
		{"ruby", "testdata/langexp/ruby/billing.rb", []string{
			"import:invoice", "class:App", "class:Billing", "method:charge",
		}},
		{"scala", "testdata/langexp/scala/geometry.scala", []string{
			"import:scala.collection.mutable",
			"class:Point", "class:Shape", "method:area",
			"class:Geometry", "method:distance",
		}},
		{"scala", "testdata/langexp/scala/counter.scala", []string{
			"class:Counter", "method:inc",
		}},
		{"perl", "testdata/langexp/perl/worker.pm", []string{
			"class:Worker", "import:strict", "import:warnings",
			"function:new", "function:run",
		}},
		{"perl", "testdata/langexp/perl/tool.pl", []string{
			"import:Data::Dumper", "import:Exporter",
			"function:main", "class:Util", "function:helper",
		}},
		{"lua", "testdata/langexp/lua/config.lua", []string{
			"import:json", "function:M.setup", "method:get", "function:helper",
		}},
		{"lua", "testdata/langexp/lua/util.lua", []string{
			"import:strutil", "function:trim", "function:export_all",
		}},
		{"haskell", "testdata/langexp/haskell/util.hs", []string{
			"import:Data.List", "type:Shape", "class:Drawable",
			"function:draw", "function:area",
		}},
		{"haskell", "testdata/langexp/haskell/more.hs", []string{
			"import:Data.Map", "type:Id", "type:Alias",
			"function:lookupKey", "function:fast",
		}},
		{"elixir", "testdata/langexp/elixir/worker.ex", []string{
			"class:MyApp.Worker", "import:Logger", "import:MyApp.Repo",
			"function:start_link", "function:handle",
		}},
		{"elixir", "testdata/langexp/elixir/math.ex", []string{
			"class:Math", "function:square", "function:twice", "function:hidden",
			"class:Describable", "function:describe",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			fe := extractLangExpFixture(t, tt.fixture, tt.lang)
			got := make([]string, 0, len(fe.elements))
			for _, e := range fe.elements {
				got = append(got, e.etype+":"+e.name)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("elements mismatch:\n got %v\nwant %v", got, tt.want)
			}
			for _, e := range fe.elements {
				if e.content == "" {
					t.Fatalf("element %s:%s has empty content", e.etype, e.name)
				}
				if e.qn == "" {
					t.Fatalf("element %s:%s has empty qualified name", e.etype, e.name)
				}
			}
		})
	}
}

// TestExtractLangExpQualifiedNames pins the qualified-name contract where it
// carries receiver/type information: Lua colon methods qualify via their
// receiver, and module-qualified function names keep their path.
func TestExtractLangExpQualifiedNames(t *testing.T) {
	fe := extractLangExpFixture(t, "testdata/langexp/lua/config.lua", "lua")
	want := map[string]string{
		"M.setup": "testdata/langexp/lua/config.lua::M.setup",
		"get":     "testdata/langexp/lua/config.lua::M.get",
	}
	for _, e := range fe.elements {
		if want[e.name] != "" && e.qn != want[e.name] {
			t.Fatalf("%s qn = %q, want %q", e.name, e.qn, want[e.name])
		}
	}
}

// TestMatchLangExpRejectsUnknown verifies the dispatcher only claims the
// expanded set — built-in languages must fall through to extract.go's switch.
func TestMatchLangExpRejectsUnknown(t *testing.T) {
	for _, lang := range []string{"go", "rust", "py", "ts", "md", "java", "kotlin", "swift", "objc", "dart", ""} {
		if _, ok := matchLangExp(lang, []string{"x"}); ok {
			t.Fatalf("matchLangExp(%q) must not claim a built-in language", lang)
		}
	}
}
