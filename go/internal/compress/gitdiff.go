package compress

import (
	"fmt"
	"regexp"
	"strings"
)

// GitDiffCompressor summarizes git diff output (Rust
// git_diff::GitDiffCompressor).
type GitDiffCompressor struct {
	statsRe *regexp.Regexp
	fileRe  *regexp.Regexp
	hunkRe  *regexp.Regexp
}

// NewGitDiffCompressor builds the compressor (Rust GitDiffCompressor::new).
func NewGitDiffCompressor() *GitDiffCompressor {
	return &GitDiffCompressor{
		statsRe: regexp.MustCompile(`^\s*(.+?)\s*\|\s*\d+\s*([+\-]+)$`),
		fileRe:  regexp.MustCompile(`^(diff --git|new file|deleted file|index|mode)`),
		hunkRe:  regexp.MustCompile(`^@@\s*-\d+(?:,\d+)?\s*\+\d+(?:,\d+)?\s*@@`),
	}
}

// Compress reduces a full diff to a summary (Rust
// GitDiffCompressor::compress).
func (c *GitDiffCompressor) Compress(output string) string {
	var result []string
	inDiff := false
	var statsLines []string
	fileChanges := 0
	insertions := 0
	deletions := 0

	for _, raw := range splitLines(output) {
		line := strings.TrimSpace(raw)

		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "diff --git") {
			inDiff = true
			continue
		}
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		if c.fileRe.MatchString(line) {
			fileChanges++
			continue
		}
		if m := c.statsRe.FindStringSubmatch(line); m != nil {
			insertions += strings.Count(m[2], "+")
			deletions += strings.Count(m[2], "-")
			statsLines = append(statsLines, m[1])
			continue
		}
		if c.hunkRe.MatchString(line) {
			continue
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			insertions++
			continue
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			deletions++
			continue
		}
		if inDiff && (strings.HasPrefix(line, " ") || len(line) < 3) {
			continue
		}
	}

	result = append(result, "[GIT DIFF SUMMARY]")
	result = append(result, fmt.Sprintf("%d file(s) changed, +%d insertions, -%d deletions",
		fileChanges, insertions, deletions))

	if len(statsLines) > 0 && len(statsLines) <= 20 {
		result = append(result, "Changed files:")
		for _, f := range statsLines {
			result = append(result, "  - "+f)
		}
	}

	return strings.Join(result, "\n")
}

// CompressStatOnly summarizes "--stat" output (Rust
// GitDiffCompressor::compress_stat_only).
func (c *GitDiffCompressor) CompressStatOnly(output string) string {
	var statsLines []string
	fileCount := 0

	for _, raw := range splitLines(output) {
		line := strings.TrimSpace(raw)
		if m := c.statsRe.FindStringSubmatch(line); m != nil {
			statsLines = append(statsLines, m[1])
			fileCount++
		}
	}

	if len(statsLines) == 0 {
		return "[GIT DIFF] No changes"
	}

	result := []string{fmt.Sprintf("%d file(s) changed:", fileCount)}
	limit := 20
	if len(statsLines) < limit {
		limit = len(statsLines)
	}
	for _, f := range statsLines[:limit] {
		result = append(result, "  "+f)
	}
	if len(statsLines) > 20 {
		result = append(result, fmt.Sprintf("  ... and %d more", len(statsLines)-20))
	}

	return strings.Join(result, "\n")
}

// EstimateSavings reports the token delta between two strings (Rust
// GitDiffCompressor::estimate_savings).
func (c *GitDiffCompressor) EstimateSavings(original, compressed string) float64 {
	originalTokens := len(original) / 4
	compressedTokens := len(compressed) / 4
	if originalTokens == 0 {
		return 0.0
	}
	return float64(originalTokens-compressedTokens) / float64(originalTokens) * 100.0
}
