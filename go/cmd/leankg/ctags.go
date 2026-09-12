package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cmdCtags renders the indexed elements as a readtags-compatible `tags` file.
// Ported from the Rust `leankg tags --format=ctags` verb
// (ctags_export.rs + cli::CLICommand::Tags).
func cmdCtags(args []string) {
	fs := flag.NewFlagSet("ctags", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	out := fs.String("out", "", "write the tags file to FILE instead of stdout (Rust's --output)")
	format := fs.String("format", "ctags", "export format (only ctags is wired, like the Rust TagsFormat enum)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *format != "ctags" {
		fmt.Fprintf(os.Stderr, "ctags: unknown format %q (valid: ctags)\n", *format)
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
	tags := renderTags(elements)
	if *out == "" {
		fmt.Print(tags)
		return
	}
	path := *out
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	if err := os.WriteFile(path, []byte(tags), 0o644); err != nil {
		fatalJSON(err)
	}
	// The Rust verb printed this note on stdout; keep stdout tags-only.
	fmt.Fprintf(os.Stderr, "wrote %d tags to %s\n", strings.Count(tags, "\n"), path)
}

// ctagsKindFor maps a LeanKG element type to its ctags kind shorthand
// (Rust ctags_export::kind_for). Unknown types fall back to "x".
func ctagsKindFor(elementType string) string {
	switch elementType {
	case "file", "module", "function":
		return "f"
	case "class", "interface", "constructor":
		return "c"
	case "struct":
		return "s"
	case "enum":
		return "g"
	case "method":
		return "m"
	case "property":
		return "p"
	case "var":
		return "v"
	case "document", "doc_section":
		return "d"
	}
	return "x"
}

// renderTags renders indexed elements as readtags-compatible tag lines
// (Rust ctags_export::render_tags):
//
//	{name}\t{file}\t{line};\t{pattern}"\tkind:{kind}\tlanguage:{lang}\telement:{type}
//
// Deterministic: sorted by (name, file, line, qualified_name). Rows whose
// name or path would corrupt the tab-delimited columns (empty, tab, newline)
// are dropped.
func renderTags(elements []store.Element) string {
	type row struct {
		name, file, kind, language, elementType, qualified, pattern string
		line                                                        int
	}
	rows := make([]row, 0, len(elements))
	for _, el := range elements {
		if el.Name == "" || el.FilePath == "" {
			continue
		}
		if strings.ContainsAny(el.Name, "\t\n") || strings.ContainsAny(el.FilePath, "\t\n") {
			continue
		}
		language := el.Language
		if language == "" {
			language = "unknown"
		}
		rows = append(rows, row{
			name:        el.Name,
			file:        normalizeTagPath(el.FilePath),
			line:        el.LineStart,
			kind:        ctagsKindFor(el.ElementType),
			language:    language,
			elementType: el.ElementType,
			qualified:   el.QualifiedName,
			pattern:     escapeTagPattern(el.Name),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.name != b.name {
			return a.name < b.name
		}
		if a.file != b.file {
			return a.file < b.file
		}
		if a.line != b.line {
			return a.line < b.line
		}
		return a.qualified < b.qualified
	})

	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%d;\t%s\"\tkind:%s\tlanguage:%s\telement:%s\n",
			r.name, r.file, r.line, r.pattern, r.kind, r.language, r.elementType)
	}
	return b.String()
}

// normalizeTagPath strips leading "./", "." and "/" runs, matching Rust's
// chained trim_start_matches (repo-relative tag paths).
func normalizeTagPath(p string) string {
	p = strings.TrimLeft(p, "./")
	return strings.TrimLeft(p, "/")
}

// escapeTagPattern is the Rust escape_pattern: ASCII letters/digits/_ stay
// literal, every other rune becomes its decimal codepoint escape; the whole
// pattern is anchored.
func escapeTagPattern(name string) string {
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, "\\%d", r)
	}
	b.WriteByte('$')
	return b.String()
}
