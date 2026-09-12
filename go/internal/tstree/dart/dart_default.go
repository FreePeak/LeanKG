//go:build !tstree

// Package dart holds the vendored tree-sitter Dart grammar, compiled only
// under the `tstree` build tag: the default (CGO-free) build keeps this
// package present but empty so the module still typechecks and `go test` can
// walk it.
package dart
