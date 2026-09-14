//go:build tstree

// Package objc provides the tree-sitter Objective-C grammar compiled from
// vendored C sources (github.com/tree-sitter-grammars/tree-sitter-objc
// v3.0.2, MIT — see LICENSE; parser.c + tree_sitter/parser.h verbatim).
// This is the exact grammar version the Rust engine pinned (Cargo.lock
// tree-sitter-objc 3.0.2), and the newest whose ABI (LANGUAGE_VERSION 14)
// matches the tree-sitter runtime bundled by smacker/go-tree-sitter.
package objc

// #include "tree_sitter/parser.h"
// extern const TSLanguage *tree_sitter_objc(void);
import "C"

import (
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for Objective-C.
func GetLanguage() *sitter.Language {
	ptr := unsafe.Pointer(C.tree_sitter_objc())
	return sitter.NewLanguage(ptr)
}
