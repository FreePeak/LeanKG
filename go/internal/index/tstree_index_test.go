//go:build tstree

package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// TestTreeSitterExtractionLanguages pins tree-sitter extraction for the
// bundled grammars: each sample produces definition elements via the ts path.
func TestTreeSitterExtractionLanguages(t *testing.T) {
	samples := map[string]struct {
		src  string
		want []string
	}{
		"go":    {src: "package p\n\nfunc Handler() {}\nfunc helper() {}\n", want: []string{"Handler", "helper"}},
		"rust":  {src: "fn helper() {}\nstruct Config {}\n", want: []string{"helper", "Config"}},
		"py":    {src: "def parse():\n    pass\n\nclass Repo:\n    pass\n", want: []string{"parse", "Repo"}},
		"swift": {src: "func hello() {}\nclass Widget {}\n", want: []string{"hello", "Widget"}},
		"objc":  {src: "#import <UIKit.h>\n\n@interface Greeter : NSObject\n@end\n\n@implementation Greeter\n@end\n\nint main(void) { return 0; }\n", want: []string{"Greeter", "main"}},
		"dart":  {src: "import 'dart:math';\n\nclass Repo {\n  int size() { return 1; }\n}\n\nint add(int a, int b) { return a + b; }\n", want: []string{"Repo", "size", "add"}},
	}
	for lang, tc := range samples {
		defs, err := tsExtract([]byte(tc.src), lang)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if len(defs) == 0 {
			t.Fatalf("%s: tree-sitter returned no defs — grammar missing or kinds drifted", lang)
		}
		got := map[string]bool{}
		for _, d := range defs {
			got[d.Name] = true
		}
		for _, w := range tc.want {
			if !got[w] {
				t.Errorf("%s: want def %q, got %v", lang, w, got)
			}
		}
	}
}

// TestTreeSitterNoGrammarFallsBack pins the per-language fallback: md has no
// bundled grammar, so tsExtract returns nil and the regex tier runs.
func TestTreeSitterNoGrammarFallsBack(t *testing.T) {
	for _, lang := range []string{"md"} {
		defs, err := tsExtract([]byte("class App {}\n"), lang)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if len(defs) != 0 {
			t.Fatalf("%s: expected no defs from the no-grammar tier, got %d", lang, len(defs))
		}
	}
}

// TestIndexDirWithRegistryLazyNested proves the lazy nested-repo contract
// end-to-end: a tree with go.mod at the root and pubspec.yaml in mobile/
// indexes BOTH languages through the registry-routed IndexDirWith.
func TestIndexDirWithRegistryLazyNested(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":               "module x\n",
		"main.go":              "package main\n\nfunc Handler() {}\n",
		"mobile/pubspec.yaml":  "name: app\n",
		"mobile/lib/main.dart": "void main() {}\nclass App {}\n",
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := langs.DefaultRegistry()
	roots, err := reg.Activate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) < 2 {
		t.Fatalf("nested roots must activate separately, got %v", roots)
	}
	if _, err := IndexDirWith(context.Background(), st, dir, reg); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.FindExact("Handler"); len(got) == 0 {
		t.Fatal("go element missing via the registry path")
	}
	if got, _ := st.FindExact("App"); len(got) == 0 {
		t.Fatal("dart element missing via the registry path")
	}
}
