package lsp

import (
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func elem(qn, etype, name, file, lang, parent string) store.Element {
	return store.Element{
		QualifiedName: qn, ElementType: etype, Name: name, FilePath: file,
		LineStart: 1, LineEnd: 2, Language: lang, ParentQualified: parent,
	}
}

// Rust parity: type_registry tests.
func TestTypeRegistryBuildsCrossFileIndex(t *testing.T) {
	reg := NewTypeRegistry([]store.Element{
		elem("a.go::Helper", "function", "Helper", "pkg/a.go", "go", ""),
		elem("b.go::Main", "function", "Main", "pkg/b.go", "go", ""),
	})
	if reg.Len() != 2 {
		t.Fatalf("len = %d, want 2", reg.Len())
	}
	hit, ok := reg.LookupInModule("pkg", "Helper")
	if !ok || hit.QualifiedName != "a.go::Helper" {
		t.Errorf("LookupInModule(pkg, Helper) = %+v ok=%v", hit, ok)
	}
	if _, ok := reg.LookupUniqueName("Helper"); !ok {
		t.Error("LookupUniqueName(Helper) not ok")
	}
	if m, ok := reg.ModuleForFile("pkg/a.go"); !ok || m != "pkg" {
		t.Errorf("ModuleForFile = %q ok=%v", m, ok)
	}
}

func TestTypeRegistryIndexesTypeMethods(t *testing.T) {
	reg := NewTypeRegistry([]store.Element{
		elem("svc.ts::UserService", "class", "UserService", "src/svc.ts", "typescript", ""),
		elem("src/svc.ts::UserService::save", "method", "save", "src/svc.ts", "typescript", "src/svc.ts::UserService"),
	})
	m, ok := reg.LookupTypeMethod("UserService", "save")
	if !ok || m.QualifiedName != "src/svc.ts::UserService::save" {
		t.Errorf("LookupTypeMethod = %+v ok=%v", m, ok)
	}
}

func TestTypeRegistryAmbiguousNameIsNotUnique(t *testing.T) {
	reg := NewTypeRegistry([]store.Element{
		elem("a.go::Run", "function", "Run", "pkg1/a.go", "go", ""),
		elem("b.go::Run", "function", "Run", "pkg2/b.go", "go", ""),
	})
	if _, ok := reg.LookupUniqueName("Run"); ok {
		t.Error("LookupUniqueName(Run) = ok, want ambiguous")
	}
	if len(reg.Candidates("Run")) != 2 {
		t.Errorf("candidates = %d, want 2", len(reg.Candidates("Run")))
	}
}

// Rust parity: hybrid resolve tier tests.
func TestResolveCallTiers(t *testing.T) {
	reg := NewTypeRegistry([]store.Element{
		elem("pkg/a.go::Helper", "function", "Helper", "pkg/a.go", "go", ""),
		elem("pkg/b.go::Main", "function", "Main", "pkg/b.go", "go", ""),
		elem("svc.ts::UserService", "class", "UserService", "src/svc.ts", "typescript", ""),
		elem("src/svc.ts::UserService::save", "method", "save", "src/svc.ts", "typescript", "src/svc.ts::UserService"),
		elem("only.go::Unique", "function", "Unique", "only.go", "go", ""),
	})

	hit, ok := ResolveCall(reg, "pkg/b.go", "Helper", "")
	if !ok || hit.QualifiedName != "pkg/a.go::Helper" || hit.Confidence < 0.95 {
		t.Errorf("cross-file module hit = %+v ok=%v", hit, ok)
	}
	hit, ok = ResolveCall(reg, "src/app.ts", "format", "")
	if ok {
		t.Errorf("unregistered callee resolved to %+v", hit)
	}
	hit, ok = ResolveCall(reg, "src/app.ts", "save", "UserService")
	if !ok || hit.QualifiedName != "src/svc.ts::UserService::save" || hit.Confidence != 0.97 {
		t.Errorf("receiver type-method hit = %+v ok=%v", hit, ok)
	}
	hit, ok = ResolveCall(reg, "other/dir/x.go", "Unique", "")
	if !ok || hit.QualifiedName != "only.go::Unique" || hit.Confidence != 0.92 {
		t.Errorf("unique-name hit = %+v ok=%v", hit, ok)
	}
	if _, ok := ResolveCall(NewTypeRegistry(nil), "x.go", "missing", ""); ok {
		t.Error("empty registry resolved a callee (must never spawn)")
	}
}

func TestCallerFileFromQN(t *testing.T) {
	for in, want := range map[string]string{
		"pkg/b.go::Main":                "pkg/b.go",
		"src/svc.ts::UserService::save": "src/svc.ts",
		"plain":                         "plain",
	} {
		if got := CallerFileFromQN(in); got != want {
			t.Errorf("CallerFileFromQN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLanguageFromFile(t *testing.T) {
	for in, want := range map[string]string{
		"a.go":     "go",
		"b.ts":     "typescript",
		"c.tsx":    "typescript",
		"d.js":     "typescript",
		"S.swift":  "swift",
		"m.m":      "objc",
		"m.mm":     "objc",
		"h.h":      "objc",
		"app.py":   "",
		"app.rs":   "",
		"notes.md": "",
	} {
		got, _ := LanguageFromFile(in)
		if got != want {
			t.Errorf("LanguageFromFile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBareCallee(t *testing.T) {
	for in, want := range map[string]string{
		"__unresolved__Helper": "Helper",
		"pkg/a.go::Helper":     "Helper",
		"a.go::Type.method":    "method",
		"bare":                 "bare",
	} {
		if got := BareCallee(in); got != want {
			t.Errorf("BareCallee(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTypedResolveEnabledTable(t *testing.T) {
	for _, lang := range []string{"go", "typescript", "python", "rust", "ruby", "csharp", "swift", "objc"} {
		if !TypedResolveEnabled("all", lang) {
			t.Errorf("typed_resolve=all should enable %s", lang)
		}
		if TypedResolveEnabled("off", lang) {
			t.Errorf("typed_resolve=off should disable %s", lang)
		}
		if TypedResolveEnabled("", lang) {
			t.Errorf("typed_resolve='' should disable %s", lang)
		}
	}
	if !TypedResolveEnabled("go,ts", "go") || !TypedResolveEnabled("go,ts", "typescript") {
		t.Error("CSV should enable listed languages")
	}
	if TypedResolveEnabled("go,ts", "python") {
		t.Error("CSV should not enable unlisted languages")
	}
	if !TypedResolveEnabled("py", "python") {
		t.Error("alias py should enable python")
	}
	if !TypedResolveEnabled("yes", "go") || !TypedResolveEnabled("true", "go") {
		t.Error("yes/true should enable everything")
	}
}

func TestApplyTypedResolveUpgradesUnresolvedCalls(t *testing.T) {
	reg := NewTypeRegistry([]store.Element{
		elem("pkg/a.go::Helper", "function", "Helper", "pkg/a.go", "go", ""),
		elem("pkg/b.go::Main", "function", "Main", "pkg/b.go", "go", ""),
	})
	rels := []store.Relationship{{
		Source: "pkg/b.go::Main", Target: "__unresolved__Helper",
		RelType: "calls", Confidence: 0.5,
		Metadata: map[string]any{"resolution_method": "unresolved"},
	}}
	if n := ApplyTypedResolve(rels, reg, "go,ts"); n != 1 {
		t.Fatalf("upgraded = %d, want 1", n)
	}
	if rels[0].Target != "pkg/a.go::Helper" {
		t.Errorf("target = %q, want pkg/a.go::Helper", rels[0].Target)
	}
	if m, _ := rels[0].Metadata["resolution_method"].(string); m != "typed" {
		t.Errorf("resolution_method = %v", rels[0].Metadata["resolution_method"])
	}
	if v, _ := rels[0].Metadata["is_resolved"].(bool); !v {
		t.Error("is_resolved not set")
	}
	if m, _ := rels[0].Metadata["hybrid_tier"].(string); m != "in_process" {
		t.Errorf("hybrid_tier = %v", rels[0].Metadata["hybrid_tier"])
	}
	if rels[0].Confidence != 0.98 {
		t.Errorf("confidence = %v, want 0.98 (cross-file module hit)", rels[0].Confidence)
	}
}

func TestApplyTypedResolveOffSkipsUpgrade(t *testing.T) {
	reg := NewTypeRegistry([]store.Element{
		elem("pkg/a.go::Helper", "function", "Helper", "pkg/a.go", "go", ""),
	})
	rels := []store.Relationship{{
		Source: "pkg/b.go::Main", Target: "__unresolved__Helper",
		RelType: "calls", Confidence: 0.5,
	}}
	if n := ApplyTypedResolve(rels, reg, "off"); n != 0 {
		t.Errorf("off upgraded %d edges, want 0", n)
	}
}

func TestApplyTypedResolveSwift(t *testing.T) {
	reg := NewTypeRegistry([]store.Element{
		elem("App/Session.swift::authenticate", "method", "authenticate", "App/Session.swift", "swift", ""),
		elem("App/Session.swift::start", "method", "start", "App/Session.swift", "swift", ""),
	})
	rels := []store.Relationship{{
		Source: "App/Session.swift::start", Target: "authenticate",
		RelType: "calls", Confidence: 0.7,
		Metadata: map[string]any{"resolution_method": "name"},
	}}
	if n := ApplyTypedResolve(rels, reg, "swift,objc"); n != 1 {
		t.Fatalf("upgraded = %d, want 1", n)
	}
	if m, _ := rels[0].Metadata["resolution_method"].(string); m != "typed" {
		t.Errorf("resolution_method = %v", rels[0].Metadata["resolution_method"])
	}
}

func TestApplyTypedResolveSkipsAlreadyTypedAndOtherEdges(t *testing.T) {
	reg := NewTypeRegistry([]store.Element{
		elem("pkg/a.go::Helper", "function", "Helper", "pkg/a.go", "go", ""),
	})
	rels := []store.Relationship{
		{Source: "pkg/b.go::Main", Target: "pkg/a.go::Helper", RelType: "calls", Confidence: 0.5,
			Metadata: map[string]any{"resolution_method": "typed"}},
		{Source: "pkg/b.go::Main", Target: "pkg/a.go::Helper", RelType: "contains", Confidence: 1.0},
	}
	if n := ApplyTypedResolve(rels, reg, "go"); n != 0 {
		t.Errorf("upgraded = %d, want 0 (typed and contains skipped)", n)
	}
}

// Store round-trip: the enrichment pass uses this path post-index.
func TestHybridResolveStoreRoundTrip(t *testing.T) {
	dir, st := newTestStore(t)
	writeFile(t, dir, "leankg.yaml", "lsp:\n  servers:\n    go:\n      command: not-installed-lsp\n")
	els := []store.Element{
		elem("pkg/a.go::Helper", "function", "Helper", "pkg/a.go", "go", ""),
		elem("pkg/b.go::Main", "function", "Main", "pkg/b.go", "go", ""),
	}
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRelationships([]store.Relationship{{
		Source: "pkg/b.go::Main", Target: "pkg/a.go::Helper", RelType: "calls", Confidence: 0.5,
	}}); err != nil {
		t.Fatal(err)
	}
	n, err := HybridResolve(st, "go")
	if err != nil || n != 1 {
		t.Fatalf("HybridResolve = %d, %v; want 1, nil", n, err)
	}
	rels, err := st.RelationshipsAll(10)
	if err != nil || len(rels) != 1 {
		t.Fatalf("reload rels: %d, %v", len(rels), err)
	}
	if m, _ := rels[0].Metadata["resolution_method"].(string); m != "typed" {
		t.Errorf("persisted resolution_method = %v", rels[0].Metadata["resolution_method"])
	}
	if rels[0].Confidence != 0.98 {
		t.Errorf("persisted confidence = %v", rels[0].Confidence)
	}
	// Second run: already-typed edges are not rewritten.
	if n, err = HybridResolve(st, "go"); err != nil || n != 0 {
		t.Errorf("second HybridResolve = %d, %v; want 0, nil", n, err)
	}
}
