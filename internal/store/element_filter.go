package store

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ElementFilter is the population a search admits, as data rather than a
// closure, so every engine can render it into its own WHERE clause before the
// LIMIT. The zero value admits every element. Every pattern must be non-empty.
type ElementFilter struct {
	ExcludeQNPrefixes   []string
	ExcludePathPrefixes []string
	ExcludePathSuffixes []string
	ExcludePathContains []string
}

// IsZero reports whether the filter excludes nothing.
func (f ElementFilter) IsZero() bool {
	return len(f.ExcludeQNPrefixes) == 0 && len(f.ExcludePathPrefixes) == 0 &&
		len(f.ExcludePathSuffixes) == 0 && len(f.ExcludePathContains) == 0
}

// Merge returns the filter that excludes whatever either filter excludes.
func (f ElementFilter) Merge(g ElementFilter) ElementFilter {
	return ElementFilter{
		ExcludeQNPrefixes:   append(append([]string(nil), f.ExcludeQNPrefixes...), g.ExcludeQNPrefixes...),
		ExcludePathPrefixes: append(append([]string(nil), f.ExcludePathPrefixes...), g.ExcludePathPrefixes...),
		ExcludePathSuffixes: append(append([]string(nil), f.ExcludePathSuffixes...), g.ExcludePathSuffixes...),
		ExcludePathContains: append(append([]string(nil), f.ExcludePathContains...), g.ExcludePathContains...),
	}
}

// Admits reports whether el survives the filter. It is the Go reading of the
// SQL that where renders; the parity test keeps the two in step.
func (f ElementFilter) Admits(el Element) bool {
	for _, p := range f.ExcludeQNPrefixes {
		if strings.HasPrefix(el.QualifiedName, p) {
			return false
		}
	}
	for _, p := range f.ExcludePathPrefixes {
		if strings.HasPrefix(el.FilePath, p) {
			return false
		}
	}
	for _, s := range f.ExcludePathSuffixes {
		if strings.HasSuffix(el.FilePath, s) {
			return false
		}
	}
	for _, c := range f.ExcludePathContains {
		if strings.Contains(el.FilePath, c) {
			return false
		}
	}
	return true
}

// sqlDialect is the part of the SQL that differs between the engines: the
// placeholder syntax and the substring-position function.
type sqlDialect struct {
	placeholder func(n int) string // n is the 1-based argument number
	instr       string             // instr(haystack, needle) or strpos(haystack, needle)
}

var (
	sqliteDialect = sqlDialect{placeholder: func(int) string { return "?" }, instr: "instr"}
	pgDialect     = sqlDialect{placeholder: func(n int) string { return fmt.Sprintf("$%d", n) }, instr: "strpos"}
)

// where renders f as a predicate over the qualified-name and file-path
// expressions, appending each bound value to args in placeholder order. It
// returns "" for a zero filter, so a caller can AND it in unconditionally.
// The predicates use only substr, length and instr/strpos, which both engines
// evaluate identically, so no LIKE escaping or case folding is involved.
func (f ElementFilter) where(d sqlDialect, qn, path string, args *[]any) string {
	if f.IsZero() {
		return ""
	}
	bind := func(v any) string {
		*args = append(*args, v)
		return d.placeholder(len(*args))
	}
	qn = "COALESCE(" + qn + ",'')"
	path = "COALESCE(" + path + ",'')"
	var excluded []string
	prefix := func(col, p string) {
		excluded = append(excluded, fmt.Sprintf("substr(%s, 1, %s) = %s", col, bind(utf8.RuneCountInString(p)), bind(p)))
	}
	for _, p := range f.ExcludeQNPrefixes {
		prefix(qn, p)
	}
	for _, p := range f.ExcludePathPrefixes {
		prefix(path, p)
	}
	for _, s := range f.ExcludePathSuffixes {
		n := utf8.RuneCountInString(s)
		excluded = append(excluded, fmt.Sprintf("substr(%s, length(%s) - %s + 1) = %s", path, path, bind(n), bind(s)))
	}
	for _, c := range f.ExcludePathContains {
		excluded = append(excluded, fmt.Sprintf("%s(%s, %s) > 0", d.instr, path, bind(c)))
	}
	return "NOT (" + strings.Join(excluded, " OR ") + ")"
}

// whereSQLite renders f for an SQLite statement; args keep the statement order.
func (f ElementFilter) whereSQLite(qn, path string, args *[]any) string {
	return f.where(sqliteDialect, qn, path, args)
}

// wherePG renders f for a PostgreSQL statement; args keep the $n numbering.
func (f ElementFilter) wherePG(qn, path string, args *[]any) string {
	return f.where(pgDialect, qn, path, args)
}

// andWhere joins a rendered filter onto an existing WHERE clause: "" stays "",
// anything else becomes " AND <predicate>".
func andWhere(pred string) string {
	if pred == "" {
		return ""
	}
	return " AND " + pred
}
