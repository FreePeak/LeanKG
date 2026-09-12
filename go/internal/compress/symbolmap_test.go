package compress

import (
	"strings"
	"testing"
)

func TestAnchorGenerator(t *testing.T) {
	gen := &AnchorGenerator{}
	if got := gen.Next(); got != "A" {
		t.Fatalf("first anchor = %q, want A", got)
	}
	if got := gen.Next(); got != "B" {
		t.Fatalf("second anchor = %q, want B", got)
	}

	gen2 := &AnchorGenerator{}
	for i := 0; i < 26; i++ {
		gen2.Next()
	}
	if got := gen2.Next(); got != "a" {
		t.Fatalf("anchor after A-Z = %q, want a", got)
	}
	for i := 0; i < 25; i++ {
		gen2.Next()
	}
	if got := gen2.Next(); got != "AA" {
		t.Fatalf("anchor after a-z = %q, want AA", got)
	}
	if got := gen2.Next(); got != "AB" {
		t.Fatalf("anchor after AA = %q, want AB", got)
	}
}

func TestSymbolMapApply(t *testing.T) {
	content := "fn my_long_function_name() {}"
	m := NewSymbolMap(content)
	anchor, ok := m.Register("my_long_function_name")
	if !ok || anchor != "A" {
		t.Fatalf("Register = %q, %v; want A, true", anchor, ok)
	}
	if got := m.Apply(content); got != "fn A() {}" {
		t.Errorf("Apply = %q, want %q", got, "fn A() {}")
	}
	if table := m.FormatTable(); !strings.Contains(table, "A=my_long_function_name") {
		t.Errorf("FormatTable = %q, want it to contain A=my_long_function_name", table)
	}
}

func TestSymbolMapCollisionPrevention(t *testing.T) {
	content := "let A = 0; let B = 1; let C = 2; let custom_long_var = 5;"
	m := NewSymbolMap(content)
	anchor, ok := m.Register("custom_long_var")
	if !ok || anchor != "D" {
		t.Fatalf("Register = %q, %v; want D, true (A, B, C are taken)", anchor, ok)
	}
}

func TestSymbolMapRegisterRules(t *testing.T) {
	m := NewSymbolMap("short")
	if _, ok := m.Register("tiny"); ok {
		t.Error("Register(tiny) should fail for identifiers shorter than 6 chars")
	}
	first, _ := m.Register("already_registered")
	second, ok := m.Register("already_registered")
	if !ok || first != second {
		t.Errorf("re-register = %q, %v; want the original anchor %q", second, ok, first)
	}
}

func TestSymbolMapEmpty(t *testing.T) {
	m := NewSymbolMap("x")
	if !m.IsEmpty() || m.Len() != 0 {
		t.Errorf("fresh map: IsEmpty=%v Len=%d, want true/0", m.IsEmpty(), m.Len())
	}
	if got := m.Apply("unchanged"); got != "unchanged" {
		t.Errorf("Apply on empty map = %q, want passthrough", got)
	}
	if got := m.FormatTable(); got != "" {
		t.Errorf("FormatTable on empty map = %q, want empty", got)
	}
}

func TestKeywordExclusion(t *testing.T) {
	if !isKeyword("continue", "rs") {
		t.Error("continue should be a Rust keyword")
	}
	if !isKeyword("interface", "go") {
		t.Error("interface should be a Go keyword")
	}
	if isKeyword("my_var", "rs") {
		t.Error("my_var should not be a keyword")
	}

	content := "fn continue() {} fn my_custom_ident() {}"
	idents := ExtractIdentifiers(content, "rs")
	for _, id := range idents {
		if id == "continue" {
			t.Errorf("extracted keywords should be excluded, got %v", idents)
		}
	}
}

func TestExtractIdentifiersSavingsOrder(t *testing.T) {
	// "repeated_long_identifier" is long and frequent; "rare_one" appears once
	// and is not worth mapping.
	content := strings.Repeat("repeated_long_identifier(repeated_long_identifier, repeated_long_identifier)\n", 8) + "rare_one\n"
	idents := ExtractIdentifiers(content, "go")
	if len(idents) == 0 {
		t.Fatal("expected at least one mappable identifier")
	}
	if idents[0] != "repeated_long_identifier" {
		t.Errorf("idents[0] = %q, want repeated_long_identifier", idents[0])
	}
	for _, id := range idents {
		if id == "rare_one" {
			t.Error("rare_one should not be worth mapping")
		}
	}
}

func TestShouldRegister(t *testing.T) {
	tests := []struct {
		ident string
		count int
		want  bool
	}{
		{"tiny", 100, false}, // below min length
		{"abcdef", 1, false}, // 1 occurrence: saving <= entry cost
		{"abcdef", 2, false}, // tokens(1) - 1 = 0 saving per use
		{"a_very_long_identifier_name", 5, true},
		{"a_very_long_identifier_name", 1, false},
	}
	for _, tt := range tests {
		if got := ShouldRegister(tt.ident, tt.count); got != tt.want {
			t.Errorf("ShouldRegister(%q, %d) = %v, want %v", tt.ident, tt.count, got, tt.want)
		}
	}
}
