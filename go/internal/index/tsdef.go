package index

// indexDef is the unified definition shape both regex and tree-sitter paths
// emit, so extractFileAs can merge from either tier identically.
type indexDef struct {
	Kind      string
	Name      string
	StartLine int
	EndLine   int
	// Owner is the name of the nearest enclosing definition (empty at file
	// scope); a member whose name carries no owning type (dart constructors)
	// uses it as that type.
	Owner string
}
