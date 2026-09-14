//go:build tstree

// Package dart provides the tree-sitter Dart grammar compiled from vendored
// C sources (crates.io tree-sitter-dart 0.0.4, repo
// github.com/nielsenko/tree-sitter-dart, MIT; parser.c + scanner.c +
// tree_sitter/parser.h verbatim).
//
// The Rust engine pinned tree-sitter-dart 0.1.0, whose parser targets
// tree-sitter ABI 15. The smacker/go-tree-sitter runtime only loads ABI 14
// (tree-sitter 0.22-era), so this package vendors 0.0.4 — the newest release
// of the same grammar family that loads. Node kind names differ slightly
// (class_definition vs class_declaration etc.); tstree maps them to the same
// element types, so indexed output is equivalent.
package dart

// #cgo CFLAGS: -I${SRCDIR}
// #include <tree_sitter/parser.h>
// extern const TSLanguage *tree_sitter_dart(void);
import "C"

import (
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for Dart.
func GetLanguage() *sitter.Language {
	ptr := unsafe.Pointer(C.tree_sitter_dart())
	return sitter.NewLanguage(ptr)
}
