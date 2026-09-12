package compress

import (
	"math"
	"strings"
	"testing"
)

func TestShannonEntropy(t *testing.T) {
	a := DefaultEntropyAnalyzer()
	if got := a.ShannonEntropy(""); got != 0 {
		t.Errorf("entropy(\"\") = %v, want 0", got)
	}
	// Single repeated character: p=1 -> entropy 0.
	if got := a.ShannonEntropy("aaaa"); got != 0 {
		t.Errorf("entropy(aaaa) = %v, want 0", got)
	}
	// Two equally likely characters: 1 bit.
	if got := a.ShannonEntropy("ab"); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("entropy(ab) = %v, want 1", got)
	}
}

func TestNormalizedEntropy(t *testing.T) {
	a := DefaultEntropyAnalyzer()
	if got := a.NormalizedEntropy(""); got != 0 {
		t.Errorf("normalized(\"\") = %v, want 0", got)
	}
	// A single char has no divisibility: len <= 1 returns raw entropy.
	if got := a.NormalizedEntropy("a"); got != 0 {
		t.Errorf("normalized(a) = %v, want 0", got)
	}
	// Two distinct chars is maximally entropic for its length.
	if got := a.NormalizedEntropy("ab"); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("normalized(ab) = %v, want 1", got)
	}
}

func TestKolmogorovProxy(t *testing.T) {
	text := strings.Repeat("a", 1000)
	k := KolmogorovProxy(text)
	if k >= 100 {
		t.Errorf("KolmogorovProxy(1000 a's) = %d, want < 100", k)
	}
	if KolmogorovProxy("") != 0 {
		t.Error("KolmogorovProxy(\"\") should be 0")
	}
	if CompressibilityClassOf(text) != ClassHigh {
		t.Error("1000 repeated a's should classify as High")
	}
	if CompressibilityClassOf("") != ClassLow {
		t.Error("empty text should classify as Low")
	}
}

func TestCompressibilityClassBuckets(t *testing.T) {
	randomish := "q7Zm3xKp9Lw2Vn5Rt8Yb1Cd4Fg6Hj0Uk" + "eSiOaP T,.;:/@#"
	if CompressibilityClassOf(randomish) != ClassLow {
		t.Errorf("dense text classified %v, want Low", CompressibilityClassOf(randomish))
	}
}

func TestLineEntropies(t *testing.T) {
	a := DefaultEntropyAnalyzer()
	got := a.LineEntropies([]string{"aaaa", "ab ab"})
	if len(got) != 2 {
		t.Fatalf("got %d entropies, want 2", len(got))
	}
	if got[0] != 0 {
		t.Errorf("entropy of aaaa = %v, want 0", got[0])
	}
	if got[1] <= 0 {
		t.Errorf("entropy of varied line = %v, want > 0", got[1])
	}
}

func TestFilterLowEntropyLines(t *testing.T) {
	a := DefaultEntropyAnalyzer()
	lines := []string{
		"let x = 1;",
		"",
		"// a very low entropy comment",
		"aaaaaaaaaaaaa",
		"",
		"",
		"pub fn run() {",
		"}",
	}
	filtered := a.FilterLowEntropyLines(lines, 0.3)
	joined := strings.Join(filtered, "\n")
	if !strings.Contains(joined, "run()") {
		t.Errorf("structural bounds must survive, got:\n%s", joined)
	}
	if strings.Contains(joined, "aaaaaaaaaaaaa") {
		t.Errorf("repetitive line should be filtered, got:\n%s", joined)
	}
	// A run of consecutive blank lines collapses to one; blanks separated by
	// content each survive (the reference resets last_was_empty on every
	// non-empty line, including ones later dropped by entropy).
	prevBlank := false
	for _, l := range filtered {
		blank := strings.TrimSpace(l) == ""
		if blank && prevBlank {
			t.Errorf("consecutive blank lines must collapse, got:\n%s", joined)
		}
		prevBlank = blank
	}
}

func TestJaccardSimilarity(t *testing.T) {
	tests := []struct {
		s1, s2 []string
		want   float64
	}{
		{nil, nil, 1.0},
		{[]string{"a"}, nil, 0.0},
		{nil, []string{"a"}, 0.0},
		{[]string{"a", "b"}, []string{"a", "b"}, 1.0},
		{[]string{"a", "b"}, []string{"b", "c"}, 1.0 / 3.0},
		{[]string{"a"}, []string{"b"}, 0.0},
	}
	for _, tt := range tests {
		if got := JaccardSimilarity(tt.s1, tt.s2); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("JaccardSimilarity(%v, %v) = %v, want %v", tt.s1, tt.s2, got, tt.want)
		}
	}
}
