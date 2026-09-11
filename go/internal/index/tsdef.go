package index

// indexDef is the unified definition shape both regex and tree-sitter paths
// emit, so extractFileAs can merge from either tier identically.
type indexDef struct {
	Kind      string
	Name      string
	StartLine int
	EndLine   int
}
