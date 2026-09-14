package compress

import (
	"math"
	"sort"
	"strings"
)

// LineCategory classifies a line for LITM L-curve reordering (Rust
// litm::LineCategory).
type LineCategory uint8

const (
	// CategoryErrorHandling: error returns, raises, panics.
	CategoryErrorHandling LineCategory = iota
	// CategoryImport: use/import/require lines.
	CategoryImport
	// CategoryTypeDefinition: struct/class/interface/enum declarations.
	CategoryTypeDefinition
	// CategoryFunctionSignature: fn/def/func declarations.
	CategoryFunctionSignature
	// CategoryLogic: everything else.
	CategoryLogic
	// CategoryClosingBrace: bare closing brackets.
	CategoryClosingBrace
	// CategoryEmpty: blank lines.
	CategoryEmpty
)

// CategorizedLine pairs a line with its category and original index (Rust
// litm::CategorizedLine).
type CategorizedLine struct {
	Line          string
	Category      LineCategory
	OriginalIndex int
}

// CategorizeLine classifies one line (Rust litm::categorize_line).
func CategorizeLine(line string) LineCategory {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return CategoryEmpty
	}
	if isErrorHandling(trimmed) {
		return CategoryErrorHandling
	}
	if isImport(trimmed) {
		return CategoryImport
	}
	if isTypeDef(trimmed) {
		return CategoryTypeDefinition
	}
	if isFnSignature(trimmed) {
		return CategoryFunctionSignature
	}
	if isClosing(trimmed) {
		return CategoryClosingBrace
	}
	return CategoryLogic
}

// ReorderForLCurve sorts lines by category priority, task-keyword boosts,
// and the LITM U-curve weight favoring file start/end (Rust
// litm::reorder_for_lcurve).
func ReorderForLCurve(content string, taskKeywords []string) string {
	lines := splitLines(content)
	if len(lines) <= 5 {
		return content
	}

	categorized := make([]CategorizedLine, len(lines))
	for i, line := range lines {
		categorized[i] = CategorizedLine{Line: line, Category: CategorizeLine(line), OriginalIndex: i}
	}

	kwLower := make([]string, len(taskKeywords))
	for i, k := range taskKeywords {
		kwLower[i] = strings.ToLower(k)
	}

	type scored struct {
		cl    CategorizedLine
		score float64
	}
	scores := make([]scored, 0, len(categorized))
	n := float64(len(lines))
	if n < 1 {
		n = 1
	}
	for _, cl := range categorized {
		base := categoryPriority(cl.Category)
		var kwBoost float64
		if len(kwLower) > 0 {
			lineLower := strings.ToLower(cl.Line)
			for _, kw := range kwLower {
				if strings.Contains(lineLower, kw) {
					kwBoost += 0.5
				}
			}
		}
		origPos := float64(cl.OriginalIndex) / n
		// LITM U-Curve generation: high weight at start and end of file,
		// middle ignored. [0,1] -> 1.0 (start), 0.0 (middle), 1.0 (end).
		lCurveWeight := math.Abs(origPos-0.5) * 2.0
		scores = append(scores, scored{cl, base + kwBoost + lCurveWeight*0.2})
	}

	// Sort by score descending (stable, matching Rust sort_by).
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].score > scores[j].score })

	var out []string
	for _, s := range scores {
		if s.cl.Category != CategoryEmpty || s.cl.OriginalIndex == 0 {
			out = append(out, s.cl.Line)
		}
	}
	return strings.Join(out, "\n")
}

func categoryPriority(cat LineCategory) float64 {
	switch cat {
	case CategoryErrorHandling:
		return 5.0
	case CategoryImport:
		return 4.0
	case CategoryTypeDefinition:
		return 3.5
	case CategoryFunctionSignature:
		return 3.0
	case CategoryLogic:
		return 1.0
	case CategoryClosingBrace:
		return 0.2
	case CategoryEmpty:
		return 0.1
	}
	return 0.0
}

func isErrorHandling(line string) bool {
	return strings.HasPrefix(line, "return Err(") ||
		strings.HasPrefix(line, "Err(") ||
		strings.HasPrefix(line, "bail!(") ||
		strings.Contains(line, ".map_err(") ||
		strings.HasPrefix(line, "raise ") ||
		strings.HasPrefix(line, "throw ") ||
		strings.HasPrefix(line, "catch ") ||
		strings.HasPrefix(line, "except ") ||
		strings.HasPrefix(line, "panic!(") ||
		strings.Contains(line, "Error::")
}

func isImport(line string) bool {
	if strings.HasPrefix(line, "use ") || strings.HasPrefix(line, "import ") ||
		strings.HasPrefix(line, "from ") || strings.HasPrefix(line, "#include") ||
		strings.HasPrefix(line, "require(") {
		return true
	}
	return strings.HasPrefix(line, "const ") && strings.Contains(line, "require(")
}

func isTypeDef(line string) bool {
	starters := []string{
		"struct ", "pub struct ", "enum ", "pub enum ", "trait ", "pub trait ",
		"type ", "pub type ", "interface ", "export interface ", "class ",
		"export class ", "typedef ", "data ",
	}
	for _, s := range starters {
		if strings.HasPrefix(line, s) {
			return true
		}
	}
	return false
}

func isFnSignature(line string) bool {
	starters := []string{
		"fn ", "pub fn ", "async fn ", "pub async fn ", "function ",
		"export function ", "async function ", "def ", "async def ",
		"func ", "pub(crate) fn ", "pub(super) fn ",
	}
	for _, s := range starters {
		if strings.HasPrefix(line, s) {
			return true
		}
	}
	return false
}

func isClosing(line string) bool {
	switch line {
	case "}", "};", ");", "});", ")", "})":
		return true
	}
	return false
}
