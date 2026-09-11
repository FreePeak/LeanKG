//go:build tstree

package index

import (
	"github.com/FreePeak/LeanKG/go/internal/tstree"
)

// tsExtract returns definition elements via the tree-sitter tier for the
// given language, or nil when no grammar is bundled (caller falls back to
// regex).
func tsExtract(src []byte, lang string) ([]indexDef, error) {
	defs, err := tstree.Extract(src, lang)
	if err != nil {
		return nil, err
	}
	out := make([]indexDef, len(defs))
	for i, d := range defs {
		out[i] = indexDef{Kind: d.Kind, Name: d.Name, StartLine: d.StartLine, EndLine: d.EndLine}
	}
	return out, nil
}
