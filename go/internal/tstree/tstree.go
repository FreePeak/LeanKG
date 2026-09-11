//go:build tstree

// Package tstree is the CGO tree-sitter tier (build tag `tstree`). The
// default build excludes it; `go build -tags tstree` compiles the bundled
// grammars (smacker/go-tree-sitter vendors the C sources) and the indexer
// uses tree-sitter extraction for the bundled languages.
//
// Coverage of the 13 default languages: go, rust, ts, tsx, js/jsx, py, java,
// kotlin, swift have grammars; objective-c, dart and markdown do NOT exist in
// this bundled set — those three stay on the regex tier (documented gap).
package tstree

import (
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	tsswift "github.com/smacker/go-tree-sitter/swift"
	tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	typescript "github.com/smacker/go-tree-sitter/typescript/typescript"
)

// Grammar returns the tree-sitter language for a registry language id, or nil
// when no grammar is bundled (regex tier only).
func Grammar(lang string) *sitter.Language {
	switch lang {
	case "go":
		return golang.GetLanguage()
	case "rust":
		return rust.GetLanguage()
	case "ts":
		return typescript.GetLanguage()
	case "tsx":
		return tsx.GetLanguage()
	case "js", "jsx":
		return javascript.GetLanguage()
	case "py":
		return python.GetLanguage()
	case "java":
		return java.GetLanguage()
	case "kotlin":
		return kotlin.GetLanguage()
	case "swift":
		return tsswift.GetLanguage()
	}
	return nil // objc, dart, md
}

// Def is one top-level definition found by the tree-sitter pass.
type Def struct {
	Kind      string // element_type: function, method, type, impl, module
	Name      string
	StartLine int // 1-based
	EndLine   int // 1-based
}

// kindSpec maps an AST node Type() to the LeanKG element type. A node kind
// may appear for several grammars; the dedupe is enforced by the map itself.
var kindSpec = map[string]string{
	"function_declaration":        "function", // go, ts, kotlin
	"method_declaration":          "method",   // go, java
	"type_declaration":            "type",     // go
	"function_item":               "function", // rust
	"struct_item":                 "type",
	"union_item":                  "type",
	"enum_item":                   "type",
	"trait_item":                  "type",
	"impl_item":                   "impl",
	"mod_item":                    "module",
	"class_declaration":           "type", // ts/js/java/kotlin
	"method_definition":           "method",
	"type_alias_declaration":      "type",
	"interface_declaration":       "type",
	"enum_declaration":            "type",
	"abstract_class_declaration":  "type",
	"function_definition":         "function", // py
	"class_definition":            "type",     // py
	"record_declaration":          "type",     // java
	"constructor_declaration":     "method",
	"annotation_type_declaration": "type",
	"object_declaration":          "type",     // kotlin
	"func_declaration":            "function", // swift
	"struct_declaration":          "type",
	"protocol_declaration":        "type",
	"actor_declaration":           "type",
}

var (
	parserMu sync.Mutex
	parsers  = map[string]*sitter.Parser{}
)

func parserFor(lang string) *sitter.Parser {
	parserMu.Lock()
	defer parserMu.Unlock()
	if p, ok := parsers[lang]; ok {
		return p
	}
	g := Grammar(lang)
	var p *sitter.Parser
	if g != nil {
		p = sitter.NewParser()
		p.SetLanguage(g)
	}
	parsers[lang] = p
	return p
}

// Extract parses src as lang and returns definition elements. Languages with
// no bundled grammar return nil, nil — the caller falls back to regex.
func Extract(src []byte, lang string) ([]Def, error) {
	p := parserFor(lang)
	if p == nil {
		return nil, nil
	}
	tree := p.Parse(nil, src)
	if tree == nil {
		return nil, nil
	}
	root := tree.RootNode()
	var out []Def
	var walk func(n *sitter.Node, depth int)
	const limit = 64
	walk = func(n *sitter.Node, depth int) {
		if n == nil || depth > limit {
			return
		}
		if et, ok := kindSpec[n.Type()]; ok {
			if name := nameOf(n, src); name != "" {
				out = append(out, Def{
					Kind:      et,
					Name:      name,
					StartLine: int(n.StartPoint().Row) + 1,
					EndLine:   int(n.EndPoint().Row) + 1,
				})
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i), depth+1)
		}
	}
	walk(root, 0)

	// Materialize the tree bytes before the tree goes out of scope: nameOf
	// extracts from src (the raw bytes), which outlives the tree, so this is
	// safe even though sitter Node pointers are tree-lifetime-bound.
	_ = root
	seen := map[string]bool{}
	var uniq []Def
	for _, d := range out {
		key := d.Kind + "\x1f" + d.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		uniq = append(uniq, d)
	}
	return uniq, nil
}

// nameOf extracts the definition name: prefer the named "name" field, else
// the first identifier-like child.
func nameOf(n *sitter.Node, src []byte) string {
	if name := n.ChildByFieldName("name"); name != nil {
		return strings.TrimSpace(name.Content(src))
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if strings.Contains(c.Type(), "identifier") {
			return strings.TrimSpace(c.Content(src))
		}
	}
	return ""
}
