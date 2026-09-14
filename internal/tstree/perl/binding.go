//go:build tstree

// Package perl provides the tree-sitter Perl grammar compiled from vendored C
// sources (github.com/tree-sitter-perl/tree-sitter-perl v2.0.0 — the org
// grammar published as the `ts-parser-perl` crate, MIT; see PARSER_SOURCE and
// LICENSE). This grammar replaces the ganezdragon/tree-sitter-perl one the
// Rust engine used, which parsed only 40.2% of a 8,342-file real-world corpus
// without an ERROR node against ts-parser-perl's 95.4% (issue #61).
//
// The published parser.c targets tree-sitter ABI 15; the smacker/go-tree-sitter
// runtime bundled here accepts only ABI 13-14 (api.h TREE_SITTER_LANGUAGE_VERSION
// 14), so parser.c + tree_sitter/{parser,array,alloc}.h are regenerated at ABI
// 14 from the untouched src/grammar.json (also vendored) with the command in
// PARSER_SOURCE. Every other file is upstream verbatim.
package perl

// #cgo CFLAGS: -I${SRCDIR}
// #include "tree_sitter/parser.h"
// extern const TSLanguage *tree_sitter_perl(void);
import "C"

import (
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for Perl.
func GetLanguage() *sitter.Language {
	ptr := unsafe.Pointer(C.tree_sitter_perl())
	return sitter.NewLanguage(ptr)
}
