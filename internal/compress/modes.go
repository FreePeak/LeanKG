package compress

import (
	"strconv"
	"strings"
)

// ReadMode is a file-read compression mode (Rust modes::ReadMode).
type ReadMode uint8

const (
	// ModeAdaptive auto-selects the best mode based on file type/size/cache.
	ModeAdaptive ReadMode = iota
	// ModeFull returns complete file content (cached re-reads are ~13 tokens).
	ModeFull
	// ModeMap returns dependencies + exports + API signatures (~85-95% savings).
	ModeMap
	// ModeSignatures returns function/class signatures only (~90-95% savings).
	ModeSignatures
	// ModeDiff returns only changed hunks via Myers diff.
	ModeDiff
	// ModeAggressive strips syntax and boilerplate (~60-70% savings).
	ModeAggressive
	// ModeEntropy filters by Shannon entropy for repetitive patterns (~70-80% savings).
	ModeEntropy
	// ModeLines returns specific line ranges (proportional savings).
	ModeLines
)

// ParseMode maps a mode string (case-insensitive) to a ReadMode; ok is false
// for unknown names (Rust ReadMode::from_str).
func ParseMode(s string) (ReadMode, bool) {
	switch strings.ToLower(s) {
	case "adaptive":
		return ModeAdaptive, true
	case "full":
		return ModeFull, true
	case "map":
		return ModeMap, true
	case "signatures":
		return ModeSignatures, true
	case "diff":
		return ModeDiff, true
	case "aggressive":
		return ModeAggressive, true
	case "entropy":
		return ModeEntropy, true
	case "lines":
		return ModeLines, true
	default:
		return ModeAdaptive, false
	}
}

// String returns the lowercase mode name (Rust ReadMode::fmt).
func (m ReadMode) String() string {
	switch m {
	case ModeAdaptive:
		return "adaptive"
	case ModeFull:
		return "full"
	case ModeMap:
		return "map"
	case ModeSignatures:
		return "signatures"
	case ModeDiff:
		return "diff"
	case ModeAggressive:
		return "aggressive"
	case ModeEntropy:
		return "entropy"
	case ModeLines:
		return "lines"
	default:
		return "unknown"
	}
}

// Description returns the human-readable description (Rust ReadMode::description).
func (m ReadMode) Description() string {
	switch m {
	case ModeAdaptive:
		return "Auto-select best mode based on file type/size/cache"
	case ModeFull:
		return "Complete file content (cached re-reads ≈ 13 tokens)"
	case ModeMap:
		return "Dependencies + exports + API signatures (~85-95% savings)"
	case ModeSignatures:
		return "Function/class signatures only (~90-95% savings)"
	case ModeDiff:
		return "Only changed hunks via Myers diff"
	case ModeAggressive:
		return "Syntax-stripped, removes boilerplate (~60-70% savings)"
	case ModeEntropy:
		return "Shannon entropy filtered for repetitive patterns (~70-80% savings)"
	case ModeLines:
		return "Specific line ranges (proportional savings)"
	default:
		return "unknown"
	}
}

// EstimatedSavings returns the advertised savings range (Rust
// ReadMode::estimated_savings).
func (m ReadMode) EstimatedSavings() string {
	switch m {
	case ModeAdaptive:
		return "~75-95% (auto-selected)"
	case ModeFull:
		return "~0% cached"
	case ModeMap:
		return "~85-95%"
	case ModeSignatures:
		return "~90-95%"
	case ModeDiff:
		return "proportional"
	case ModeAggressive:
		return "~60-70%"
	case ModeEntropy:
		return "~70-80%"
	case ModeLines:
		return "proportional"
	default:
		return "unknown"
	}
}

// SelectAdaptive picks a mode for code files: >500 lines => map,
// >200 lines => signatures, else map; non-code files => full (Rust
// ReadMode::select_adaptive).
func SelectAdaptive(filePath string, _fileSize, lines int) ReadMode {
	ext := ""
	if i := strings.LastIndex(filePath, "."); i >= 0 {
		ext = strings.ToLower(filePath[i+1:])
	}
	switch ext {
	case "rs", "go", "ts", "js", "py", "java", "c", "cpp", "h":
		if lines > 500 {
			return ModeMap
		}
		if lines > 200 {
			return ModeSignatures
		}
		return ModeMap
	default:
		return ModeFull
	}
}

// LinesRange is an inclusive 1-based line range (Rust modes::LinesRange).
type LinesRange struct {
	Start int
	End   int
}

// ParseLinesRange parses "N-M" (start <= end required).
func ParseLinesRange(s string) (LinesRange, bool) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return LinesRange{}, false
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || start > end || start <= 0 || end <= 0 {
		return LinesRange{}, false
	}
	return LinesRange{Start: start, End: end}, true
}

// ParseLinesSpec parses a comma-separated "10-20,30-40" spec, skipping
// invalid entries (Rust modes::parse_lines_spec).
func ParseLinesSpec(spec string) []LinesRange {
	var ranges []LinesRange
	for _, part := range strings.Split(spec, ",") {
		if r, ok := ParseLinesRange(strings.TrimSpace(part)); ok {
			ranges = append(ranges, r)
		}
	}
	return ranges
}
