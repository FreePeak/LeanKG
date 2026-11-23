package index

import "testing"

// TestGoBlankImportKeepsImportsEdge: a blank import runs the package's init for
// its side effects, so it is an imports edge like a named import.
func TestGoBlankImportKeepsImportsEdge(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/drv/drv.go": "package drv\n\nfunc init() {}\n",
		"cmd/blank/main.go":   "package main\n\nimport _ \"" + modPath + "/internal/drv\"\n\nfunc main() {}\n",
	})
	if _, ok := importEdge(t, st, "cmd/blank", "internal/drv"); !ok {
		t.Fatalf("blank import has no imports edge to internal/drv")
	}
}

// TestGoDotImportKeepsImportsEdge: a dot import brings the package's exported
// names into scope, so the package still imports it.
func TestGoDotImportKeepsImportsEdge(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/dot/dot.go": "package dot\n\nfunc Open() {}\n",
		"cmd/dot/main.go":     "package main\n\nimport . \"" + modPath + "/internal/dot\"\n\nfunc run() {\n\tOpen()\n}\n",
	})
	if _, ok := importEdge(t, st, "cmd/dot", "internal/dot"); !ok {
		t.Fatalf("dot import has no imports edge to internal/dot")
	}
}

// TestGoDotImportCallIsACeiling pins the documented ceiling: an unqualified call
// through a dot import makes no call edge (crosspkg.go ceilings).
func TestGoDotImportCallIsACeiling(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/dot/dot.go": "package dot\n\nfunc Open() {}\n",
		"cmd/dot/main.go":     "package main\n\nimport . \"" + modPath + "/internal/dot\"\n\nfunc run() {\n\tOpen()\n}\n",
	})
	noEdgeTo(t, st, "cmd/dot/main.go::run", "internal/dot/dot.go::Open")
}
