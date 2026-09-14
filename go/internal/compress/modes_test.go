package compress

import (
	"reflect"
	"testing"
)

func TestParseMode(t *testing.T) {
	tests := []struct {
		in   string
		want ReadMode
		ok   bool
	}{
		{"adaptive", ModeAdaptive, true},
		{"FULL", ModeFull, true},
		{"Map", ModeMap, true},
		{"signatures", ModeSignatures, true},
		{"diff", ModeDiff, true},
		{"aggressive", ModeAggressive, true},
		{"entropy", ModeEntropy, true},
		{"lines", ModeLines, true},
		{"bogus", ModeAdaptive, false},
		{"", ModeAdaptive, false},
	}
	for _, tt := range tests {
		got, ok := ParseMode(tt.in)
		if ok != tt.ok {
			t.Errorf("ParseMode(%q) ok = %v, want %v", tt.in, ok, tt.ok)
		}
		if ok && got != tt.want {
			t.Errorf("ParseMode(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestReadModeStringRoundTrip(t *testing.T) {
	modes := []ReadMode{ModeAdaptive, ModeFull, ModeMap, ModeSignatures, ModeDiff, ModeAggressive, ModeEntropy, ModeLines}
	for _, m := range modes {
		got, ok := ParseMode(m.String())
		if !ok || got != m {
			t.Errorf("round trip for %v failed: got %v ok=%v", m, got, ok)
		}
		if m.Description() == "" || m.EstimatedSavings() == "" {
			t.Errorf("mode %v has empty description/savings", m)
		}
	}
}

func TestSelectAdaptive(t *testing.T) {
	tests := []struct {
		path  string
		lines int
		want  ReadMode
	}{
		{"main.rs", 100, ModeMap},
		{"main.rs", 250, ModeSignatures},
		{"main.rs", 600, ModeMap},
		{"app.go", 100, ModeMap},
		{"app.ts", 250, ModeSignatures},
		{"app.py", 600, ModeMap},
		{"README.md", 600, ModeFull},
		{"data.json", 10, ModeFull},
		{"noext", 900, ModeFull},
	}
	for _, tt := range tests {
		if got := SelectAdaptive(tt.path, 0, tt.lines); got != tt.want {
			t.Errorf("SelectAdaptive(%q, %d) = %v, want %v", tt.path, tt.lines, got, tt.want)
		}
	}
}

func TestParseLinesRange(t *testing.T) {
	tests := []struct {
		in    string
		want  LinesRange
		valid bool
	}{
		{"10-20", LinesRange{Start: 10, End: 20}, true},
		{"5-5", LinesRange{Start: 5, End: 5}, true},
		{"20-10", LinesRange{}, false},
		{"invalid", LinesRange{}, false},
		{"10-", LinesRange{}, false},
		{"-5", LinesRange{}, false},
		{"0-5", LinesRange{}, false},
		{"1-2-3", LinesRange{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseLinesRange(tt.in)
		if ok != tt.valid {
			t.Errorf("ParseLinesRange(%q) ok = %v, want %v", tt.in, ok, tt.valid)
		}
		if ok && got != tt.want {
			t.Errorf("ParseLinesRange(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseLinesSpec(t *testing.T) {
	ranges := ParseLinesSpec("10-20,30-40,50-60")
	want := []LinesRange{{10, 20}, {30, 40}, {50, 60}}
	if !reflect.DeepEqual(ranges, want) {
		t.Fatalf("ParseLinesSpec = %+v, want %+v", ranges, want)
	}

	spec := ParseLinesSpec("1-2, bad, 5-9")
	want = []LinesRange{{1, 2}, {5, 9}}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("ParseLinesSpec with garbage = %+v, want %+v", spec, want)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens("hello world"); got != 2 {
		t.Errorf("EstimateTokens(hello world) = %d, want 2", got)
	}
	if got := EstimateTokens("fn foo()"); got != 2 {
		t.Errorf("EstimateTokens(fn foo()) = %d, want 2", got)
	}
	if got := EstimateTokensPrecise("fn main() {\n    println!(\"hello\");\n}"); got <= 0 {
		t.Errorf("EstimateTokensPrecise = %d, want > 0", got)
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\nb", []string{"a", "b"}},
		{"a\r\nb\r\n", []string{"a", "b"}},
		{"\n", []string{""}},
		{"a\n\n", []string{"a", ""}},
	}
	for _, tt := range tests {
		got := splitLines(tt.in)
		if len(got) != len(tt.want) {
			t.Fatalf("splitLines(%q) = %#v, want %#v", tt.in, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("splitLines(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}
