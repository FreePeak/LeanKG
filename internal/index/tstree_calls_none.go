//go:build !tstree

package index

import "context"

// tsAttributeCalls is the no-op seam for the CGO-free build: no tree-sitter
// tier, so files carry no grammar-derived call seeds and the identifier
// heuristic in relationships() is the only call source.
func tsAttributeCalls(_ context.Context, _ []byte, _ string, _ []indexedElem) error { return nil }
