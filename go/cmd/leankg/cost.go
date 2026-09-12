package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Cost estimation constants, ported verbatim from the Rust
// cost_estimate::ModelRate::default() — the LOCOMO / scc heuristic for source
// code: ~13 tokens per source line of code in context, plus a mid-size model
// reply budget per affected file. Ceiling: a counting heuristic, not a
// provider price table; callers multiply by their own rate.
const (
	costModelName        = "locomo-default"
	costTokensPerSLOC    = 13
	costOutTokensPerFile = 256
)

// fileCost is one file's cost line (Rust FileCost).
type fileCost struct {
	File      string `json:"file"`
	Lines     int    `json:"lines"`
	SLOC      int    `json:"sloc"`
	Bytes     int    `json:"bytes"`
	InTokens  int    `json:"in_tokens"`
	OutTokens int    `json:"out_tokens"`
}

// costEstimate is the aggregate estimate (Rust CostEstimate).
type costEstimate struct {
	Model      string     `json:"model"`
	Files      []fileCost `json:"files"`
	TotalLines int        `json:"total_lines"`
	TotalSLOC  int        `json:"total_sloc"`
	TotalBytes int        `json:"total_bytes"`
	InTokens   int        `json:"in_tokens"`
	OutTokens  int        `json:"out_tokens"`
}

// cmdCost estimates the token cost of reasoning over a project's indexed
// source volume. Ported from the Rust `leankg cost` verb
// (cost_estimate.rs + cli::CLICommand::Cost); the project's indexed file
// census replaces Rust's --file/--files selectors, so the verb needs no graph
// scan and stays a pure read.
func cmdCost(args []string) {
	fs := flag.NewFlagSet("cost", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	format := fs.String("format", "text", "output format: text | json")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	dir := resolveProjectDir(*project)
	engine, err := openEngine(dir, store.RO)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()

	elements, err := engine.Store().Elements()
	if err != nil {
		fatalJSON(err)
	}
	est := estimateCost(filesForElements(elements), dir)

	switch *format {
	case "json":
		printJSON(est)
	case "text":
		// LOCOMO: in = prompt (source context), out = reply budget.
		fmt.Printf("%d files, %d sloc, %d bytes — est. in %d tokens / out %d tokens\n",
			len(est.Files), est.TotalSLOC, est.TotalBytes, est.InTokens, est.OutTokens)
		for _, f := range est.Files {
			fmt.Printf("  %s  (%d sloc, in %d / out %d)\n", f.File, f.SLOC, f.InTokens, f.OutTokens)
		}
	default:
		fmt.Fprintf(os.Stderr, "cost: unknown format %q (valid: text|json)\n", *format)
		os.Exit(2)
	}
}

// filesForElements collects the distinct source files behind indexed elements
// (Rust cost_estimate::files_for_elements): empty paths dropped, leading "./"
// stripped, sorted.
func filesForElements(elements []store.Element) []string {
	seen := map[string]struct{}{}
	for _, el := range elements {
		if el.FilePath == "" {
			continue
		}
		seen[normalizeCostPath(el.FilePath)] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// normalizeCostPath is the Rust trim_start_matches("./") on file paths.
func normalizeCostPath(p string) string {
	for len(p) >= 2 && p[0] == '.' && p[1] == '/' {
		p = p[2:]
	}
	return p
}

// countSLOC counts source lines in a byte buffer (Rust count_sloc): blank-only
// lines and lines made solely of `{ } /` and whitespace do not count.
func countSLOC(b []byte) int {
	sloc := 0
	for _, line := range bytes.Split(b, []byte("\n")) {
		trimmed := bytes.TrimLeft(line, " \t\r")
		if len(trimmed) == 0 {
			continue
		}
		codeOnly := true
		for _, c := range trimmed {
			switch c {
			case '{', '}', '/', ' ', '\t', '\r':
			default:
				codeOnly = false
			}
			if !codeOnly {
				break
			}
		}
		if codeOnly {
			continue
		}
		sloc++
	}
	return sloc
}

// estimateCost prices a file set against baseDir with the default model rate
// (Rust estimate). Files missing on disk — generated or unindexed — are
// skipped and reported by absence from Files.
func estimateCost(files []string, baseDir string) costEstimate {
	out := costEstimate{Model: costModelName, Files: make([]fileCost, 0, len(files))}
	for _, f := range files {
		path := f
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, f)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := bytes.Count(raw, []byte("\n"))
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			lines++
		}
		sloc := countSLOC(raw)
		inTokens := sloc * costTokensPerSLOC
		out.TotalLines += lines
		out.TotalSLOC += sloc
		out.TotalBytes += len(raw)
		out.InTokens += inTokens
		out.OutTokens += costOutTokensPerFile
		out.Files = append(out.Files, fileCost{
			File:      f,
			Lines:     lines,
			SLOC:      sloc,
			Bytes:     len(raw),
			InTokens:  inTokens,
			OutTokens: costOutTokensPerFile,
		})
	}
	return out
}
