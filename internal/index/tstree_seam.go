//go:build tstree

package index

import (
	"context"

	"github.com/FreePeak/LeanKG/internal/tstree"
)

// tsExtract returns definition elements via the tree-sitter tier for the
// given language, or nil when no grammar is bundled (caller falls back to
// regex). ctx bounds the parse.
func tsExtract(ctx context.Context, src []byte, lang string) ([]indexDef, error) {
	defs, err := tstree.Extract(ctx, src, lang)
	if err != nil {
		return nil, err
	}
	out := make([]indexDef, len(defs))
	for i, d := range defs {
		out[i] = indexDef{Kind: d.Kind, Name: d.Name, StartLine: d.StartLine, EndLine: d.EndLine, Owner: d.Parent}
	}
	return out, nil
}
