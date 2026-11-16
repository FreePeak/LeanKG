//go:build !tstree

package index

import "context"

// tsExtract is the no-op seam for the CGO-free build: no tree-sitter tier,
// so callers fall back to regex extraction.
func tsExtract(_ context.Context, _ []byte, _ string) ([]indexDef, error) {
	return nil, nil
}
