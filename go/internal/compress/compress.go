// Package compress ports the LeanKG context-compression pipeline
// (Rust src/compress/*): RTK-style command-output compression, mode-based
// file-reader compression, and query-response shaping.
package compress

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// CharsPerToken is the rough characters-per-token estimate used across the
// package (Rust CHARS_PER_TOKEN).
const CharsPerToken = 4

// EstimateTokens approximates token count as byte length / CharsPerToken.
func EstimateTokens(text string) int {
	return len(text) / CharsPerToken
}

// EstimateTokensPrecise counts whitespace-delimited word starts plus one for
// a trailing non-whitespace character (bug-for-bug port of
// estimate_tokens_precise: the final word is counted twice).
func EstimateTokensPrecise(text string) int {
	tokenCount := 0
	inWhitespace := true
	lastWasWhitespace := true
	for _, c := range text {
		lastWasWhitespace = false
		if unicode.IsSpace(c) {
			inWhitespace = true
			lastWasWhitespace = true
			continue
		}
		if inWhitespace {
			tokenCount++
			inWhitespace = false
		}
	}
	if len(text) > 0 && !lastWasWhitespace {
		tokenCount++
	}
	return tokenCount
}

// LeanKGCompressor is the top-level entry point (Rust LeanKGCompressor): it
// routes command output to the specialized compressors and owns the
// file-reader and response-shaping sub-compressors used by Reduce.
type LeanKGCompressor struct {
	shell     *ShellCompressor
	cargoTest *CargoTestCompressor
	gitDiff   *GitDiffCompressor
	reader    *FileReader
	responses *ResponseCompressor
}

// New builds a LeanKGCompressor with a private session cache.
func New() *LeanKGCompressor {
	return &LeanKGCompressor{
		shell:     NewShellCompressor(),
		cargoTest: NewCargoTestCompressor(),
		gitDiff:   NewGitDiffCompressor(),
		reader:    NewFileReader(nil),
		responses: NewResponseCompressor(),
	}
}

// Compress compresses shell-command output, dispatching on the command
// string (Rust LeanKGCompressor::compress): cargo test output, git diff
// output (plain and --stat), then the generic per-category shell patterns.
func (c *LeanKGCompressor) Compress(cmd, output string) string {
	cmdLower := strings.ToLower(cmd)
	if strings.Contains(cmdLower, "cargo test") && !strings.Contains(cmdLower, "--no-run") {
		return c.cargoTest.Compress(output)
	}
	if strings.Contains(cmdLower, "git diff") && !strings.Contains(cmdLower, "--stat") {
		return c.gitDiff.Compress(output)
	}
	if strings.Contains(cmdLower, "git diff") && strings.Contains(cmdLower, "--stat") {
		return c.gitDiff.CompressStatOnly(output)
	}
	return c.shell.Compress(cmd, output)
}

// EstimateSavings returns the percentage token saving of compressed vs
// original.
func (c *LeanKGCompressor) EstimateSavings(original, compressed string) float64 {
	originalTokens := EstimateTokens(original)
	compressedTokens := EstimateTokens(compressed)
	if originalTokens == 0 {
		return 0.0
	}
	return float64(originalTokens-compressedTokens) / float64(originalTokens) * 100.0
}

// FileReader exposes the mode-based reader (shared session cache).
func (c *LeanKGCompressor) FileReader() *FileReader { return c.reader }

// Responses exposes the response shaper.
func (c *LeanKGCompressor) Responses() *ResponseCompressor { return c.responses }

// SessionCache exposes the reader's cache for callers that want to share one
// cache across compressor instances.
func (c *LeanKGCompressor) SessionCache() *SessionCache { return c.reader.cache }

// Request is one Reduce call. Exactly one path must be set:
//   - Path (reader-mode file compression),
//   - Cmd + Output (command-output compression),
//   - Tool + Response (query-response shaping).
type Request struct {
	// Reader path.
	Path      string
	Mode      ReadMode // zero value (Adaptive) is resolved via SelectAdaptive
	LinesSpec string
	Fresh     bool

	// Command-output path.
	Cmd    string
	Output string

	// Response-shaping path.
	Tool     string
	Response map[string]any
}

// Result is the outcome of a Reduce call. Content is set for the reader and
// command paths; Response/Stats for the response-shaping path.
type Result struct {
	Content        string
	Mode           ReadMode
	Tokens         int
	TotalTokens    int
	SavingsPercent float64
	TotalLines     int
	OutputLines    int
	IsCached       bool
	LinesIncluded  int // -1 = not applicable

	Response map[string]any
	Stats    CompressionStats
}

// Reduce dispatches one compression request to the right mode implementation
// and returns a unified result.
func (c *LeanKGCompressor) Reduce(req Request) (Result, error) {
	switch {
	case req.Path != "":
		mode := req.Mode
		if mode == ModeAdaptive {
			data, err := os.ReadFile(req.Path)
			if err != nil {
				return Result{}, fmt.Errorf("Failed to read file %s: %w", req.Path, err)
			}
			mode = SelectAdaptive(req.Path, len(data), len(splitLines(string(data))))
		}
		res, err := c.reader.Read(req.Path, mode, req.LinesSpec, req.Fresh)
		if err != nil {
			return Result{}, err
		}
		return Result{
			Content:        res.Content,
			Mode:           res.Mode,
			Tokens:         res.Tokens,
			TotalTokens:    res.TotalTokens,
			SavingsPercent: res.SavingsPercent,
			TotalLines:     res.TotalLines,
			OutputLines:    res.OutputLines,
			IsCached:       res.IsCached,
			LinesIncluded:  res.LinesIncluded,
		}, nil

	case req.Cmd != "":
		content := c.Compress(req.Cmd, req.Output)
		return Result{
			Content:        content,
			Mode:           ModeFull,
			Tokens:         EstimateTokens(content),
			TotalTokens:    EstimateTokens(req.Output),
			SavingsPercent: c.EstimateSavings(req.Output, content),
			TotalLines:     len(splitLines(req.Output)),
			OutputLines:    len(splitLines(content)),
			LinesIncluded:  -1,
		}, nil

	case req.Response != nil:
		shaped := c.responses.CompressByTool(req.Tool, req.Response)
		return Result{Response: shaped, Stats: c.responses.EstimateSavings(req.Response, shaped)}, nil

	default:
		return Result{}, errors.New("compress: empty request (set Path, Cmd, or Tool+Response)")
	}
}

// FormatReadResult renders a reader result into the ctx_read envelope
// (Rust mcp/handler.rs ctx_read header/footer).
func FormatReadResult(res ReadResult) string {
	header := fmt.Sprintf("%s [%dL] mode=%s", filepath.Base(res.Path), res.OutputLines, res.Mode.String())
	footer := fmt.Sprintf("---\noriginal: %d tokens | sent: %d tokens (%.1f%% saved)",
		res.TotalTokens, res.Tokens, res.SavingsPercent)
	return header + "\n" + res.Content + "\n" + footer
}

// splitLines splits on "\n" like Rust str::lines: a trailing newline does
// not produce a final empty element, and "\r" is trimmed from line ends.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := make([]string, 0, stringsCount(s, '\n')+1)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

func stringsCount(s string, c byte) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			n++
		}
	}
	return n
}
