package compress

import (
	"strings"
	"testing"
)

func TestCategorizeLine(t *testing.T) {
	tests := []struct {
		line string
		want LineCategory
	}{
		{"use std::io;", CategoryImport},
		{"import os", CategoryImport},
		{"pub struct Foo {", CategoryTypeDefinition},
		{"export interface Shape {", CategoryTypeDefinition},
		{"fn main() {", CategoryFunctionSignature},
		{"async def run():", CategoryFunctionSignature},
		{"return Err(e);", CategoryErrorHandling},
		{"throw new Error(x)", CategoryErrorHandling},
		{"} ", CategoryClosingBrace},
		{"let x = 1;", CategoryLogic},
		{"", CategoryEmpty},
		{"   ", CategoryEmpty},
	}
	for _, tt := range tests {
		if got := CategorizeLine(tt.line); got != tt.want {
			t.Errorf("CategorizeLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestReorderForLCurveShortContentUnchanged(t *testing.T) {
	content := "a\nb\nc\nd\ne"
	if got := ReorderForLCurve(content, nil); got != content {
		t.Errorf("content with <= 5 lines must pass through, got:\n%s", got)
	}
}

func TestReorderPutsErrorsAndImportsFirst(t *testing.T) {
	content := "let x = 1;\nuse std::io;\n}\nreturn Err(e);\npub struct Foo {\nfn main() {"
	result := ReorderForLCurve(content, nil)
	lines := strings.Split(result, "\n")
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 6:\n%s", len(lines), result)
	}
	if !strings.Contains(lines[0], "Err") && !strings.Contains(lines[0], "use ") {
		t.Errorf("first line should be error handling or import, got: %q", lines[0])
	}
}

func TestReorderTaskKeywordsBoostRelevantLines(t *testing.T) {
	content := "fn unrelated() {\nlet x = 1;\n}\nfn validate_token() {\nlet y = 2;\n}"
	result := ReorderForLCurve(content, []string{"validate"})
	lines := strings.Split(result, "\n")
	validatePos, unrelatedPos := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "validate") {
			validatePos = i
		}
		if strings.Contains(l, "unrelated") {
			unrelatedPos = i
		}
	}
	if validatePos == -1 || unrelatedPos == -1 {
		t.Fatalf("both lines must survive reordering, got:\n%s", result)
	}
	if validatePos >= unrelatedPos {
		t.Errorf("validate line (%d) should precede unrelated line (%d):\n%s", validatePos, unrelatedPos, result)
	}
}

func TestReorderDropsBlankLines(t *testing.T) {
	content := "fn a() {\n\n\n\n\n\nlet x = 1;\n\n\nfn b() {\n\n}"
	result := ReorderForLCurve(content, nil)
	for _, l := range strings.Split(result, "\n") {
		if strings.TrimSpace(l) == "" {
			t.Errorf("interior blank lines should be dropped, got:\n%s", result)
		}
	}
}
