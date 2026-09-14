// Quality analysis over store.Backend: the oversized-function scan
// (Rust GraphEngine::find_oversized_functions — the `quality` CLI verb and the
// find_large_functions analysis). Kept beside the traversal verbs because the
// Rust implementation lived on GraphEngine; the scan itself is a pure filter
// over the element table, no traversal.
package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// FunctionElementType is the element type the quality scan counts. Methods,
// classes and the like are excluded, matching the Rust predicate
// `element_type = "function"`.
const FunctionElementType = "function"

// lineSpan is the Rust line count: line_end - line_start + 1.
func lineSpan(e store.Element) int { return e.LineEnd - e.LineStart + 1 }

// OversizedFunctions returns every function element spanning at least
// minLines lines, longest first. lang != "" restricts the scan to that
// language (Rust find_oversized_functions_by_lang). Ties break by qualified
// name so the result is deterministic — the Rust sort was stable over an
// unspecified query order.
func OversizedFunctions(st store.Backend, minLines int, lang string) ([]store.Element, error) {
	els, err := st.Elements()
	if err != nil {
		return nil, err
	}
	out := make([]store.Element, 0, 16)
	for _, e := range els {
		if e.ElementType != FunctionElementType {
			continue
		}
		if lang != "" && e.Language != lang {
			continue
		}
		if lineSpan(e) < minLines {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := lineSpan(out[i]), lineSpan(out[j])
		if li != lj {
			return li > lj
		}
		return out[i].QualifiedName < out[j].QualifiedName
	})
	return out, nil
}

// RenderOversized renders the Rust `quality` verb output verbatim:
//
//	Found N oversized function(s) (>=50 lines):
//	  - name (123 lines, file.go:45)
//
// or "No functions found with >= 50 lines" when the scan is empty.
func RenderOversized(els []store.Element, minLines int) string {
	if len(els) == 0 {
		return fmt.Sprintf("No functions found with >= %d lines\n", minLines)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d oversized function(s) (>=%d lines):\n", len(els), minLines)
	for _, e := range els {
		fmt.Fprintf(&b, "  - %s (%d lines, %s:%d)\n", e.Name, lineSpan(e), e.FilePath, e.LineStart)
	}
	return b.String()
}
