package index

// callOwner returns the index of the element owning a call site inside a file:
// the element named by the enclosing definition that covers the call line,
// else the innermost element covering it (a C function wrapping the call, or
// the only candidate). Returns -1 when no element covers the line — the
// grammar tier drops such calls, since the Go engine has no file element.
func callOwner(els []indexedElem, line int, caller string) int {
	owner, ownerSpan := -1, 0
	for i := range els {
		if els[i].start > line || els[i].end < line {
			continue
		}
		span := els[i].end - els[i].start
		if caller != "" && els[i].name == caller {
			return i
		}
		if owner == -1 || span < ownerSpan {
			owner, ownerSpan = i, span
		}
	}
	return owner
}
