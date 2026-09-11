package langs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fixture builds a tree with the given marker files at the given relative
// repo-root directories, plus source files for extension census.
func fixture(t *testing.T, markers map[string][]string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for root, mks := range markers {
		for _, mk := range mks {
			p := filepath.Join(dir, filepath.FromSlash(root), mk)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
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
	return dir
}

func TestActivateNestedRepos(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t,
		map[string][]string{
			".":                       {"go.mod"},
			"services/mobile-app":     {"pubspec.yaml"},
			"services/ios-app":        {"Package.swift"},
			"services/android-client": {"build.gradle.kts"},
			"vendor-lib":              {"Cargo.toml"},
		},
		map[string]string{
			"main.go":                                         "package main",
			"services/mobile-app/lib/main.dart":               "void main() {}",
			"services/ios-app/Sources/App.swift":              "let x = 1",
			"services/ios-app/Sources/bridge.h":               "",
			"services/android-client/app/src/MainActivity.kt": "class A",
			"services/android-client/app/src/Util.java":       "class U",
			"services/ts-frontend/src/index.ts":               "let a = 1",
			"vendor-lib/src/lib.rs":                           "fn a() {}",
		})

	roots, err := r.Activate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 5 {
		t.Fatalf("roots = %d, want 5: %v", len(roots), roots)
	}
	active := r.Active()
	want := []Language{Go, Rust, Java, Kotlin, Swift, Dart}
	if !reflect.DeepEqual(active, want) {
		t.Fatalf("active = %v, want %v", active, want)
	}
	// lazy: nothing beyond the detected slice is on
	if r.IsActive(Markdown) || r.IsActive(Python) || r.IsActive(ObjC) {
		t.Fatal("undetected languages must stay idle")
	}
	// extension ownership honors activation: .dart routes to Dart only when active
	if l, ok := r.ExtOwner(".dart"); !ok || l != Dart {
		t.Fatalf(".dart owner: %v %v", l, ok)
	}
	// header ownership: .h goes to Swift (only header-capable language active here)
	if l, ok := r.ExtOwner(".h"); !ok || l != Swift {
		t.Fatalf(".h owner: %v %v", l, ok)
	}
	// TS file outside any marker root still indexes only if TS got activated —
	// here no tsconfig/package.json exists at services/ts-frontend, so census
	// did not run (markers existed elsewhere): TS should NOT be active.
	if r.IsActive(TypeScript) {
		t.Fatalf("ts activated without a marker or census hit")
	}
}

func TestActivateMarkerlessCensus(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, nil, map[string]string{
		"app/main.py":  "def a(): pass",
		"app/util.py":  "def b(): pass",
		"web/index.js": "let x = 1",
	})
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	if !r.IsActive(Python) || !r.IsActive(JavaScript) {
		t.Fatalf("census must activate py+js, got %v", r.Active())
	}
	if r.IsActive(Go) {
		t.Fatal("go must stay idle in a py/js tree")
	}
}

func TestDeactivateReturnsToIdle(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, map[string][]string{".": {"go.mod"}}, map[string]string{"a.go": "package a"})
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	if !r.IsActive(Go) {
		t.Fatal("go should be active")
	}
	r.Deactivate()
	if len(r.Active()) != 0 {
		t.Fatalf("deactivate must return to idle, got %v", r.Active())
	}
}

func TestLookupAliases(t *testing.T) {
	r := NewRegistry()
	for name, want := range map[string]Language{
		"golang": Go, "txs": TSX, "flutter": Dart, "object-c": ObjC,
		"objectivec": ObjC, "swift": Swift, "kotlin": Kotlin, "kt": Kotlin,
		"python": Python, "GO": Go, "Rust": Rust,
	} {
		got, ok := r.Lookup(name)
		if !ok || got.Language != want {
			t.Fatalf("lookup %q = %v ok=%v, want %v", name, got.Language, ok, want)
		}
	}
	if _, ok := r.Lookup("cobol"); ok {
		t.Fatal("cobol must not resolve")
	}
}

func TestTiersCapabilityGated(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, map[string][]string{".": {"go.mod", "pubspec.yaml"}}, nil)
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	tiers := r.Tiers()
	// regex always on
	for _, l := range []Language{Go, Dart} {
		if len(tiers[l]) == 0 || tiers[l][0] != TierRegex {
			t.Fatalf("%s tiers: %v", l, tiers[l])
		}
	}
	// tree-sitter is present ONLY for bundled grammars under the tstree tag
	// (go yes, dart no grammar in the set), absent entirely in the default build.
	for _, tier := range tiers[Go] {
		if tier == TierTreeSitter && !TreeSitterEnabled(Go) {
			t.Fatalf("tree-sitter tier claimed while disabled: %v", tiers[Go])
		}
	}
	if !TreeSitterEnabled(Go) {
		for _, tier := range tiers[Go] {
			if tier == TierTreeSitter {
				t.Fatalf("tree-sitter must be absent in default build: %v", tiers[Go])
			}
		}
	} else {
		found := false
		for _, tier := range tiers[Go] {
			if tier == TierTreeSitter {
				found = true
			}
		}
		if !found {
			t.Fatalf("tstree build must report tree-sitter for go: %v", tiers[Go])
		}
	}
	// dart has no bundled grammar in either build
	for _, tier := range tiers[Dart] {
		if tier == TierTreeSitter {
			t.Fatalf("dart must not claim a tree-sitter tier: %v", tiers[Dart])
		}
	}
	// markdown has no AST tier even when active
	r2 := NewRegistry()
	d2 := fixture(t, nil, map[string]string{"README.md": "# x"})
	if _, err := r2.Activate(d2); err != nil {
		t.Fatal(err)
	}
	for _, tier := range r2.Tiers()[Markdown] {
		if tier == TierAstGrep || tier == TierTreeSitter {
			t.Fatalf("markdown must not gain AST tiers: %v", r2.Tiers()[Markdown])
		}
	}
}

func TestExtOwnerInactiveSkips(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, nil, map[string]string{"x.dart": "void main(){}"})
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	if !r.IsActive(Dart) {
		t.Fatal("census must activate dart")
	}
	r.Deactivate()
	if _, ok := r.ExtOwner(".dart"); ok {
		t.Fatal("inactive language must not own extensions")
	}
}

// TestAstGrepPresentIgnoresSgAlias pins the tier side of the Linux name
// collision: a bare `sg` on PATH is util-linux's set-group command there
// (upstream deprecated the alias), so it must not light up the ast-grep tier.
// Resolution goes through astgrep.New, which probes only `ast-grep`.
func TestAstGrepPresentIgnoresSgAlias(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sg"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if astGrepPresent() {
		t.Fatal("astGrepPresent = true with only util-linux sg on PATH, want false")
	}
}
