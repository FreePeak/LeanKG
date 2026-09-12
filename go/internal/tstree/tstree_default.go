//go:build !tstree

// Package tstree is the CGO tree-sitter tier, compiled only under the
// `tstree` build tag: the default (CGO-free) build keeps this package present
// but empty so the module still typechecks and `go test` can walk it.
//
// The extraction entry points (Extract, ExtractCalls) live in tstree.go and
// the indexer's seams fall back to the regex tier when the tier is absent.
package tstree
