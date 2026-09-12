package compress

import (
	"bytes"
	"compress/gzip"
	"math"
	"strings"
)

// CompressibilityClass classifies how repetitive a text is (Rust
// entropy::CompressibilityClass).
type CompressibilityClass uint8

const (
	// ClassHigh: highly compressible (kolmogorov ratio < 0.3).
	ClassHigh CompressibilityClass = iota
	// ClassMedium: moderately compressible (ratio < 0.6).
	ClassMedium
	// ClassLow: dense (ratio >= 0.6).
	ClassLow
)

// EntropyAnalyzer filters low-entropy content (Rust entropy::EntropyAnalyzer).
type EntropyAnalyzer struct {
	jaccardThreshold float64
}

// NewEntropyAnalyzer builds an analyzer with the given jaccard threshold.
func NewEntropyAnalyzer(jaccardThreshold float64) *EntropyAnalyzer {
	return &EntropyAnalyzer{jaccardThreshold: jaccardThreshold}
}

// DefaultEntropyAnalyzer builds the default analyzer (threshold 0.7).
func DefaultEntropyAnalyzer() *EntropyAnalyzer {
	return NewEntropyAnalyzer(0.7)
}

// ShannonEntropy computes the per-character Shannon entropy. Faithful port
// note: the denominator is the BYTE length while counts are per rune,
// exactly as in the Rust reference.
func (a *EntropyAnalyzer) ShannonEntropy(text string) float64 {
	if text == "" {
		return 0.0
	}
	charCounts := map[rune]int{}
	for _, c := range text {
		charCounts[c]++
	}
	length := float64(len(text))
	var entropy float64
	for _, count := range charCounts {
		p := float64(count) / length
		if p > 0 {
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

// NormalizedEntropy divides Shannon entropy by log2(length).
func (a *EntropyAnalyzer) NormalizedEntropy(text string) float64 {
	entropy := a.ShannonEntropy(text)
	length := len(text)
	if length <= 1 {
		return entropy
	}
	maxEntropy := math.Log2(float64(length))
	if maxEntropy > 0 {
		return entropy / maxEntropy
	}
	return 0.0
}

// KolmogorovProxy approximates Kolmogorov complexity with gzip compressed
// size (Rust uses flate2; Go stdlib compress/gzip covers it).
func KolmogorovProxy(text string) int {
	if text == "" {
		return 0
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write([]byte(text))
	if err := w.Close(); err != nil {
		return len(text) // fallback
	}
	return buf.Len()
}

// CompressibilityClassOf buckets a text by its kolmogorov ratio (Rust
// EntropyAnalyzer::compressibility_class).
func CompressibilityClassOf(text string) CompressibilityClass {
	bytesLen := len(text)
	if bytesLen == 0 {
		return ClassLow
	}
	ratio := float64(KolmogorovProxy(text)) / float64(bytesLen)
	if ratio < 0.3 {
		return ClassHigh
	}
	if ratio < 0.6 {
		return ClassMedium
	}
	return ClassLow
}

// LineEntropies returns normalized entropy per line.
func (a *EntropyAnalyzer) LineEntropies(lines []string) []float64 {
	out := make([]float64, len(lines))
	for i, line := range lines {
		out[i] = a.NormalizedEntropy(line)
	}
	return out
}

// FilterLowEntropyLines drops repetitive lines, keeping structural bounds
// and collapsing blank runs (Rust EntropyAnalyzer::filter_low_entropy_lines).
func (a *EntropyAnalyzer) FilterLowEntropyLines(lines []string, threshold float64) []string {
	var filtered []string
	lastWasEmpty := false

	// Fast paths for very uncompressible files.
	class := CompressibilityClassOf(strings.Join(lines, "\n"))
	dynamicThreshold := threshold
	switch class {
	case ClassHigh:
		dynamicThreshold = threshold * 1.5 // aggressive prune if highly repetitive
	case ClassMedium:
		dynamicThreshold = threshold
	case ClassLow:
		dynamicThreshold = threshold * 0.5 // gentle if already dense
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			if !lastWasEmpty {
				filtered = append(filtered, line)
				lastWasEmpty = true
			}
			continue
		}
		lastWasEmpty = false

		// Always keep structural bounds.
		if strings.HasPrefix(t, "fn ") || strings.HasPrefix(t, "class ") ||
			strings.HasPrefix(t, "pub ") || strings.HasSuffix(t, "{") || t == "}" {
			filtered = append(filtered, line)
			continue
		}

		if a.NormalizedEntropy(line) >= dynamicThreshold {
			filtered = append(filtered, line)
		}
	}
	return filtered
}

// JaccardSimilarity computes |A∩B| / |A∪B| over string sets (Rust
// entropy::jaccard_similarity).
func JaccardSimilarity(set1, set2 []string) float64 {
	if len(set1) == 0 && len(set2) == 0 {
		return 1.0
	}
	if len(set1) == 0 || len(set2) == 0 {
		return 0.0
	}
	s1 := make(map[string]struct{}, len(set1))
	for _, v := range set1 {
		s1[v] = struct{}{}
	}
	s2 := make(map[string]struct{}, len(set2))
	for _, v := range set2 {
		s2[v] = struct{}{}
	}
	intersection := 0
	for v := range s1 {
		if _, ok := s2[v]; ok {
			intersection++
		}
	}
	union := len(s1) + len(s2) - intersection
	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}
