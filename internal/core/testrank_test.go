package core

import "testing"

func TestIsTestPath(t *testing.T) {
	for p, want := range map[string]bool{
		"internal/core/core_test.go": true, "src/app.test.ts": true, "web/x.spec.jsx": true,
		"tests/test_api.py": true, "pkg/foo_test.py": true, "src/__tests__/a.js": true,
		"app/FooTest.java": true, "spec/models/user_spec.rb": true,
		"internal/core/core.go": false, "src/latest.ts": false, "contest/entry.go": false,
	} {
		if got := isTestPath(p); got != want {
			t.Errorf("isTestPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestDemoteTestHits(t *testing.T) {
	h := func(fp string) map[string]any { return map[string]any{"file_path": fp} }
	hits := []map[string]any{h("a_test.go"), h("a.go"), h("b_test.go"), h("b.go"), h("c.go")}
	got := demoteTestHits(hits, 4, true, false)
	want := []string{"a.go", "a_test.go", "b.go", "b_test.go"} // a_test 0→2, b_test 2→4 ties c.go and keeps its earlier order
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i]["file_path"] != w {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	for q, want := range map[string]bool{
		"which tests cover the parser":    true,
		"where is TestParse defined":      true,
		"where is the parser implemented": false,
		"make the parser testable":        false,
		"the testament of the lexer":      false,
	} {
		if got := mentionsTests(q); got != want {
			t.Errorf("mentionsTests(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestDemoteDocHits(t *testing.T) {
	doc := map[string]any{"element_type": "doc", "file_path": "CLAUDE.md"}
	mdFile := map[string]any{"element_type": "function", "file_path": "README.mdx"}
	code := map[string]any{"element_type": "function", "file_path": "a.go"}
	hits := []map[string]any{doc, code, mdFile} // doc 0+2, code 1, README.mdx 2+2

	got := demoteTestHits(hits, 3, false, true)
	if got[0]["file_path"] != "a.go" {
		t.Fatalf("doc demotion must put the code hit first, got %v", got)
	}
	if kept := demoteTestHits(hits, 3, false, false); kept[0]["file_path"] != "CLAUDE.md" {
		t.Fatalf("without the doc flag the order must be unchanged, got %v", kept)
	}
	for q, want := range map[string]bool{
		"where is the readme parser":  true,
		"update CLAUDE.md for it":     true,
		"document the parser":         false,
		"where is the parser defined": false,
	} {
		if got := mentionsDocs(q); got != want {
			t.Errorf("mentionsDocs(%q) = %v, want %v", q, got, want)
		}
	}
}
