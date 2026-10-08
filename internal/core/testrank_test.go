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
	got := demoteTestHits(hits, 4)
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
