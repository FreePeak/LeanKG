package index

import "testing"

// The TS/JS function matcher must require a word boundary after the keyword.
// Without it, any identifier that merely starts with "function" is matched and
// the tail becomes a phantom element (name "ality" from "functionality:"),
// which then gets embedded, indexed and ranked like a real declaration.
func TestMatchTSFunctionKeywordNeedsWordBoundary(t *testing.T) {
	lines := []string{
		"      functionality: `tool choice type: ${toolChoice.type}`,",
		"  functionDefs.push(x)",
		"  functionName = 'x'",
		"export async function prepareTools(tools) {",
		"function* gen() {}",
	}
	ms := matchTS(lines)

	var names []string
	for _, m := range ms {
		names = append(names, m.name)
	}
	want := []string{"prepareTools", "gen"}
	if len(names) != len(want) {
		t.Fatalf("matchTS matched %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("matchTS matched %v, want %v", names, want)
		}
	}
}
