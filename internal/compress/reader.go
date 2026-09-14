package compress

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ReadResult is the outcome of a mode-based file read (Rust
// reader::ReadResult). LinesIncluded is -1 when the Rust Option was None.
type ReadResult struct {
	Path           string
	Mode           ReadMode
	Content        string
	Tokens         int
	TotalTokens    int
	SavingsPercent float64
	TotalLines     int
	OutputLines    int
	IsCached       bool
	LinesIncluded  int
}

// FileReader reads files through the compression modes with a session cache
// (Rust reader::FileReader).
type FileReader struct {
	entropy *EntropyAnalyzer
	cache   *SessionCache
}

// NewFileReader builds a reader sharing cache; a nil cache creates a private
// one.
func NewFileReader(cache *SessionCache) *FileReader {
	if cache == nil {
		cache = NewSessionCache()
	}
	return &FileReader{entropy: DefaultEntropyAnalyzer(), cache: cache}
}

// Cache exposes the session cache (shared by FileReader and callers).
func (r *FileReader) Cache() *SessionCache { return r.cache }

// Read compresses path in the given mode (Rust FileReader::read). Adaptive
// must be resolved first via SelectAdaptive. fresh bypasses the cache-hit
// pre-emption.
func (r *FileReader) Read(path string, mode ReadMode, linesSpec string, fresh bool) (ReadResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			r.cache.Invalidate(path)
		}
		return ReadResult{}, fmt.Errorf("Failed to read file %s: %w", path, err)
	}
	text := string(data)
	totalTokens := EstimateTokens(text)

	entry, isHit, oldContent, hasOld := r.cache.Store(path, text)
	fileRef := r.cache.GetFileRef(path)

	// Cache return pre-emption (Rust "Cache Return Pre-emption").
	if isHit && !fresh && mode != ModeDiff && mode != ModeLines {
		msg := fmt.Sprintf(
			"%s=%s cached %dt %dL\n[File unchanged in SessionCache. Use fresh=true to pull absolute text]",
			fileRef, filepath.Base(path), entry.ReadCount, entry.LineCount)
		return ReadResult{
			Path:           path,
			Mode:           mode,
			Content:        msg,
			Tokens:         EstimateTokens(msg),
			TotalTokens:    entry.OriginalTokens,
			SavingsPercent: 99.0, // cache hits are roughly 99% efficient
			TotalLines:     entry.LineCount,
			OutputLines:    2,
			IsCached:       true,
			LinesIncluded:  -1,
		}, nil
	}

	lines := splitLines(text)
	totalLines := len(lines)

	var res ReadResult
	switch mode {
	case ModeAdaptive:
		return ReadResult{}, errors.New("Adaptive mode should be resolved before calling read()")
	case ModeFull:
		res = readFull(path, text)
	case ModeMap:
		res = readMap(path, text, lines)
	case ModeSignatures:
		res = readSignatures(path, text, lines)
	case ModeDiff:
		return r.readDiff(path, fileRef, text, oldContent, hasOld), nil
	case ModeAggressive:
		res = readAggressive(text, lines)
	case ModeEntropy:
		res = r.readEntropy(text, lines)
	case ModeLines:
		res = readLines(lines, ParseLinesSpec(linesSpec))
	}

	tokens := EstimateTokens(res.Content)
	savingsPercent := 0.0
	if totalTokens > 0 && tokens <= totalTokens {
		savingsPercent = float64(totalTokens-tokens) / float64(totalTokens) * 100.0
	}
	res.Path = path
	res.Mode = mode
	res.Tokens = tokens
	res.TotalTokens = totalTokens
	res.SavingsPercent = savingsPercent
	res.TotalLines = totalLines
	res.OutputLines = len(splitLines(res.Content))
	res.IsCached = isHit
	return res, nil
}

// readFull returns the file content, optionally symbol-map compressed when
// that saves at least 5% (Rust FileReader::read_full).
func readFull(path, content string) ReadResult {
	ext := fileExt(path)
	finalContent := content

	symMap := NewSymbolMap(content)
	for _, ident := range ExtractIdentifiers(content, ext) {
		symMap.Register(ident)
	}
	if symMap.Len() >= 3 {
		table := symMap.FormatTable()
		compressed := symMap.Apply(content)
		origTokens := EstimateTokens(content)
		newTokens := EstimateTokens(compressed) + EstimateTokens(table)
		netSavings := origTokens - newTokens
		if netSavings < 0 {
			netSavings = 0
		}
		if origTokens > 0 && netSavings*100/origTokens >= 5 {
			finalContent = compressed + table
		}
	}

	totalLines := len(splitLines(content))
	return ReadResult{
		Mode:          ModeFull,
		Content:       finalContent,
		TotalLines:    totalLines,
		OutputLines:   len(splitLines(finalContent)),
		LinesIncluded: totalLines,
	}
}

// readMap returns dependencies + exports + API signatures (Rust
// FileReader::read_map).
func readMap(path, content string, lines []string) ReadResult {
	var resultLines []string
	var imports, exports []string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isImportLine(trimmed) {
			imports = append(imports, fmt.Sprintf("L%d: %s", i+1, trimmed))
		}
		if isExportLine(trimmed) {
			exports = append(exports, fmt.Sprintf("L%d: %s", i+1, trimmed))
		}
	}

	resultLines = append(resultLines, fmt.Sprintf("# %s [%dL]", filepath.Base(path), len(lines)))
	resultLines = append(resultLines, "")

	if len(imports) > 0 {
		resultLines = append(resultLines, "deps: "+strings.Join(imports, ", "))
	}
	if len(exports) > 0 {
		resultLines = append(resultLines, "exports: "+strings.Join(exports, ", "))
	}

	sigs := ExtractSignatures(content, fileExt(path))

	resultLines = append(resultLines, "")
	resultLines = append(resultLines, "API:")
	for _, sig := range sigs {
		resultLines = append(resultLines, "  "+sig.ToCompact())
	}

	mapOutput := strings.Join(resultLines, "\n")
	mapOptimal := ReorderForLCurve(mapOutput, nil)

	return ReadResult{
		Mode:          ModeMap,
		Content:       mapOptimal,
		TotalLines:    len(lines),
		OutputLines:   len(resultLines),
		LinesIncluded: len(sigs),
	}
}

// readSignatures returns TDD-style signatures (Rust
// FileReader::read_signatures).
func readSignatures(path, content string, lines []string) ReadResult {
	sigs := ExtractSignatures(content, fileExt(path))

	out := []string{fmt.Sprintf("%s [%dL]\nsignatures: %d\n", filepath.Base(path), len(lines), len(sigs))}
	for _, sig := range sigs {
		out = append(out, sig.ToTDD())
	}

	return ReadResult{
		Mode:          ModeSignatures,
		Content:       strings.Join(out, "\n"),
		TotalLines:    len(lines),
		OutputLines:   len(out),
		LinesIncluded: len(sigs),
	}
}

// readDiff returns the delta against the previous read, or the full content
// when there is no previous version (Rust FileReader::read_diff).
func (r *FileReader) readDiff(path, fileRef, currentContent, oldContent string, hasOld bool) ReadResult {
	shortName := filepath.Base(path)
	lines := splitLines(currentContent)
	totalLines := len(lines)

	if !hasOld {
		// First time in the cache: emit the full file instead of a diff.
		return ReadResult{
			Path: path,
			Mode: ModeDiff,
			Content: fmt.Sprintf("%s=%s [New in Cache => Showing Full %dL]\n%s",
				fileRef, shortName, totalLines, currentContent),
			Tokens:        EstimateTokens(currentContent),
			TotalTokens:   EstimateTokens(currentContent),
			TotalLines:    totalLines,
			OutputLines:   totalLines,
			LinesIncluded: totalLines,
		}
	}

	if oldContent == currentContent {
		msg := fmt.Sprintf("%s=%s [No changes since last read]", fileRef, shortName)
		return ReadResult{
			Path:           path,
			Mode:           ModeDiff,
			Content:        msg,
			Tokens:         EstimateTokens(msg),
			TotalTokens:    EstimateTokens(currentContent),
			SavingsPercent: 99.0,
			TotalLines:     totalLines,
			OutputLines:    1,
			IsCached:       true,
			LinesIncluded:  -1,
		}
	}

	unified := UnifiedDiff(oldContent, currentContent, 3)
	msg := fmt.Sprintf("%s=%s [auto-delta] ∆%dL\n%s", fileRef, shortName, totalLines, unified)

	outputLines := len(splitLines(unified))
	tokens := EstimateTokens(msg)
	totalTokens := EstimateTokens(currentContent)
	savingsPercent := 0.0
	if totalTokens > 0 && tokens <= totalTokens {
		savingsPercent = float64(totalTokens-tokens) / float64(totalTokens) * 100.0
	}

	return ReadResult{
		Path:           path,
		Mode:           ModeDiff,
		Content:        msg,
		Tokens:         tokens,
		TotalTokens:    totalTokens,
		SavingsPercent: savingsPercent,
		TotalLines:     totalLines,
		OutputLines:    outputLines,
		LinesIncluded:  -1,
	}
}

// readAggressive strips comments and boilerplate (Rust
// FileReader::read_aggressive).
func readAggressive(_content string, lines []string) ReadResult {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if isNoiseLine(strings.TrimSpace(line)) {
			continue
		}
		filtered = append(filtered, removeSyntaxNoise(line))
	}
	return ReadResult{
		Mode:          ModeAggressive,
		Content:       strings.Join(filtered, "\n"),
		TotalLines:    len(lines),
		OutputLines:   len(filtered),
		LinesIncluded: len(filtered),
	}
}

// readEntropy drops low-entropy lines (Rust FileReader::read_entropy).
func (r *FileReader) readEntropy(_content string, lines []string) ReadResult {
	filtered := r.entropy.FilterLowEntropyLines(lines, 0.3)
	return ReadResult{
		Mode:          ModeEntropy,
		Content:       strings.Join(filtered, "\n"),
		TotalLines:    len(lines),
		OutputLines:   len(filtered),
		LinesIncluded: len(filtered),
	}
}

// readLines returns the requested 1-based inclusive ranges (Rust
// FileReader::read_lines).
func readLines(lines []string, ranges []LinesRange) ReadResult {
	var selected []string
	for _, rg := range ranges {
		start := rg.Start - 1
		if start < 0 {
			start = 0
		}
		end := rg.End
		if end > len(lines) {
			end = len(lines)
		}
		for i := start; i < end; i++ {
			selected = append(selected, lines[i])
		}
	}
	return ReadResult{
		Mode:          ModeLines,
		Content:       strings.Join(selected, "\n"),
		TotalLines:    len(lines),
		OutputLines:   len(selected),
		LinesIncluded: len(selected),
	}
}

func fileExt(path string) string {
	return strings.TrimPrefix(filepath.Ext(path), ".")
}

func isImportLine(line string) bool {
	imports := []string{
		"import ", "use ", "require(", "from ", "#include", "use crate::", "use self::",
	}
	for _, p := range imports {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func isExportLine(line string) bool {
	return strings.HasPrefix(line, "pub ") ||
		strings.HasPrefix(line, "export ") ||
		strings.HasPrefix(line, "module.exports")
}

func isNoiseLine(line string) bool {
	if line == "" {
		return true
	}
	noise := []string{"// ", "/* ", "*/", "# ", "##", "---", "***", "<!--", "-->", "```", `"""`}
	for _, p := range noise {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// removeSyntaxNoise drops line comments and markdown heading markers outside
// strings (Rust reader::remove_syntax_noise).
func removeSyntaxNoise(line string) string {
	var b strings.Builder
	inString := false
	runes := []rune(line)

	for i := 0; i < len(runes); {
		c := runes[i]
		if c == '"' || c == '\'' {
			inString = !inString
			b.WriteRune(c)
			i++
			continue
		}
		if inString {
			b.WriteRune(c)
			i++
			continue
		}
		if c == '/' && i+1 < len(runes) && runes[i+1] == '/' {
			break
		}
		if c == '#' && i+1 < len(runes) && unicode.IsDigit(runes[i+1]) {
			i++
			for i < len(runes) && unicode.IsDigit(runes[i]) {
				i++
			}
			continue
		}
		b.WriteRune(c)
		i++
	}

	return strings.TrimSpace(b.String())
}
