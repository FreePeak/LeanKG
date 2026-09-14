//go:build tstree

package index

import (
	"github.com/FreePeak/LeanKG/go/internal/tstree"
)

// tsAttributeCalls records the grammar-derived call targets of one file on the
// elements that own each call site. Only objective-c has a call walk: the
// message sends of a file become "calls" seeds (the selector) on the enclosing
// method (or on the innermost element whose span covers the call when the
// call sits in a C function body). relationships() resolves those seeds
// through the same package name map as the identifier heuristic.
//
// Ceiling: a message send outside every element (file scope, or a receiver
// with no selector such as [bar]) seeds nothing — the Rust reference
// attributed such calls to the file path, which the Go engine has no element
// for, so no edge is possible without inventing one.
func tsAttributeCalls(src []byte, lang string, els []indexedElem) {
	calls, err := tstree.ExtractCalls(src, lang)
	if err != nil || len(calls) == 0 {
		return
	}
	for _, c := range calls {
		if i := callOwner(els, c.Line, c.Caller); i >= 0 {
			els[i].calls = append(els[i].calls, c.Callee)
		}
	}
}
