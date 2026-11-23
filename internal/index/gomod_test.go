package index

import (
	"slices"
	"testing"

	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
)

// TestParseGoModForms: the module line (quoted, with a comment), a single-line
// local replace, a block of replaces where a version-target replace and a target
// outside the root are dropped, and the directives that are not read.
func TestParseGoModForms(t *testing.T) {
	src := `module "example.com/m" // the root module
go 1.25

require (
	example.com/dep v1.2.3
)

replace example.com/shared v1.0.0 => ./shared

replace (
	example.com/gen => ./tools/gen
	example.com/fork => example.com/upstream v1.2.3
	example.com/escape => ../../../escape
)
`
	got := parseGoMod([]byte(src), "svc")
	want := goModules{
		{path: "example.com/m", dir: "svc"},
		{path: "example.com/shared", dir: "svc/shared"},
		{path: "example.com/gen", dir: "svc/tools/gen"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("parseGoMod = %v, want %v", got, want)
	}
}

// TestGoModStdlibImportDoesNotLinkRepoDir: net/http names the standard library,
// so the repo's top-level http/ is not what it imports.
func TestGoModStdlibImportDoesNotLinkRepoDir(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"go.mod":       "module example.com/m\n",
		"http/http.go": "package http\n\nfunc Get(u string) {}\n",
		"app/app.go":   "package app\n\nimport \"net/http\"\n\nfunc Run() {\n\thttp.Get(\"u\")\n}\n",
	})
	if _, ok := importEdge(t, st, "app", "http"); ok {
		t.Fatalf("net/http linked to the repo's http/ directory")
	}
	noEdgeTo(t, st, "app/app.go::Run", "http/http.go::Get")
}

// TestGoModExternalModuleSuffixDoesNotLink: github.com/acme/operator/api/v1 is
// another module's api/v1, not the repo's api/v1.
func TestGoModExternalModuleSuffixDoesNotLink(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"go.mod":          "module example.com/m\n",
		"api/v1/types.go": "package v1\n\ntype Spec struct{}\n",
		"ctl/ctl.go":      "package ctl\n\nimport \"github.com/acme/operator/api/v1\"\n\nvar _ v1.Spec\n",
	})
	if _, ok := importEdge(t, st, "ctl", "api/v1"); ok {
		t.Fatalf("github.com/acme/operator/api/v1 linked to the repo's api/v1")
	}
}

// TestGoModForeignPathDoesNotLink: example.com/other/internal/store is not this
// module, though its trailing path is the repo's internal/store.
func TestGoModForeignPathDoesNotLink(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"go.mod":                  "module example.com/m\n",
		"internal/store/store.go": "package store\n\nfunc Open() {}\n",
		"app/app.go":              "package app\n\nimport \"example.com/other/internal/store\"\n\nfunc Run() {\n\tstore.Open()\n}\n",
	})
	if _, ok := importEdge(t, st, "app", "internal/store"); ok {
		t.Fatalf("example.com/other/internal/store linked to the repo's internal/store")
	}
	noEdgeTo(t, st, "app/app.go::Run", "internal/store/store.go::Open")
}

// TestGoModModulePathDiffersFromDir: the module svc declares example.com/svc-core,
// so its package svc/store is imported under that path.
func TestGoModModulePathDiffersFromDir(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"svc/go.mod":         "module example.com/svc-core\n",
		"svc/store/store.go": "package store\n\nfunc Open() {}\n",
		"cmd/app/main.go":    "package main\n\nimport \"example.com/svc-core/store\"\n\nfunc run() {\n\tstore.Open()\n}\n",
	})
	if _, ok := importEdge(t, st, "cmd/app", "svc/store"); !ok {
		t.Fatalf("import of example.com/svc-core/store has no imports edge to svc/store")
	}
	if _, ok := edgeTo(t, st, "cmd/app/main.go::run", "svc/store/store.go::Open", "calls"); !ok {
		t.Fatalf("call of store.Open has no edge to svc/store/store.go::Open")
	}
}

// TestGoModNestedModuleOwnsItsTree: tools is a nested module with its own path.
// The root module's path for tools/internal/store links nothing, because the
// nested module owns that directory, while the nested path and the root's
// internal/store do link.
func TestGoModNestedModuleOwnsItsTree(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"go.mod":                        "module example.com/m\n",
		"tools/go.mod":                  "module example.com/tools\n",
		"internal/store/store.go":       "package store\n\nfunc Open() {}\n",
		"tools/internal/store/store.go": "package store\n\nfunc Open() {}\n",
		"app/root.go":                   "package app\n\nimport \"example.com/m/internal/store\"\n",
		"app/tools.go":                  "package app\n\nimport \"example.com/tools/internal/store\"\n",
		"lint/lint.go":                  "package lint\n\nimport \"example.com/m/tools/internal/store\"\n",
	})
	if got := importEnds(t, st, "app", true); !slices.Equal(got, []string{"internal/store", "tools/internal/store"}) {
		t.Fatalf("imports of app = %v, want the root's internal/store and the nested tools/internal/store", got)
	}
	if _, ok := importEdge(t, st, "lint", "tools/internal/store"); ok {
		t.Fatalf("root path example.com/m/tools/internal/store linked into the nested module's directory")
	}
}

// TestGoModLocalReplaceMapsModule: a block replace maps example.com/shared to the
// local directory shared/, and the import links there.
func TestGoModLocalReplaceMapsModule(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"go.mod":           "module example.com/m\n\nreplace (\n\texample.com/shared v1.0.0 => ./shared\n)\n",
		"shared/shared.go": "package shared\n\nfunc Name() string { return \"s\" }\n",
		"app/app.go":       "package app\n\nimport \"example.com/shared\"\n\nvar _ = shared.Name\n",
	})
	if _, ok := importEdge(t, st, "app", "shared"); !ok {
		t.Fatalf("import of the replaced module has no imports edge to shared/")
	}
}

// TestGoModNoGoModKeepsSuffixFallback: with no go.mod under the run, imports
// resolve by longest suffix, as they did before module boundaries.
func TestGoModNoGoModKeepsSuffixFallback(t *testing.T) {
	st := precisionTree(t, map[string]string{
		"internal/store/store.go": "package store\n\nfunc Open() {}\n",
		"app/app.go":              "package app\n\nimport \"example.com/other/internal/store\"\n\nfunc Run() {\n\tstore.Open()\n}\n",
	})
	if _, ok := importEdge(t, st, "app", "internal/store"); !ok {
		t.Fatalf("no go.mod: suffix fallback no longer links internal/store")
	}
}

// TestGoModReadFromHEADInGitSnapshot: in git-snapshot mode the go.mod is read
// from its HEAD blob, so the stdlib import does not link the repo's http/.
func TestGoModReadFromHEADInGitSnapshot(t *testing.T) {
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	gittest.WriteFile(t, root, "go.mod", "module example.com/m\n")
	gittest.WriteFile(t, root, "http/http.go", "package http\n\nfunc Get(u string) {}\n")
	gittest.WriteFile(t, root, "app/app.go", "package app\n\nimport \"net/http\"\n\nfunc Run() {\n\thttp.Get(\"u\")\n}\n")
	gittest.Git(t, root, "add", "-A")
	gittest.Git(t, root, "commit", "-q", "-m", "fixture")

	st := gitIndexStore(t)
	gitIndexRun(t, st, root)
	if _, ok := importEdge(t, st, "app", "http"); ok {
		t.Fatalf("git snapshot: net/http linked to the repo's http/ directory")
	}
}
