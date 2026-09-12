//go:build tstree

// Package tstree is the CGO tree-sitter tier (build tag `tstree`). The
// default build excludes it; `go build -tags tstree` compiles the bundled
// grammars (smacker/go-tree-sitter vendors the C sources, plus the vendored
// objc and dart grammars in this directory) and the indexer uses tree-sitter
// extraction for the bundled languages.
//
// Coverage: go, rust, ts, tsx, js/jsx, py, java, kotlin and swift come from
// smacker/go-tree-sitter; objective-c and dart from the vendored grammars in
// this directory. Every other registry language (markdown included) has no
// grammar here and stays on the regex tier.
package tstree

import (
	"strings"
	"sync"

	"github.com/FreePeak/LeanKG/go/internal/tstree/dart"
	tsobjc "github.com/FreePeak/LeanKG/go/internal/tstree/objc"
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
	case "objc":
		return tsobjc.GetLanguage()
	case "dart":
		return dart.GetLanguage()
	}
	return nil // md
}

// Def is one top-level definition found by the tree-sitter pass.
type Def struct {
	Kind      string // element_type: function, method, type, constructor, constant
	Name      string
	StartLine int // 1-based
	EndLine   int // 1-based
	// Parent is the name of the nearest enclosing definition, empty at file
	// scope. The indexer uses it as the owning type of a member whose name
	// carries no type (dart constructors), so the member's qualified name does
	// not depend on line-based parent detection.
	Parent string
}

// kindSpec maps an AST node Type() to the LeanKG element type. A node kind
// may appear for several grammars; the dedupe is enforced by the map itself.
var kindSpec = map[string]string{
	"function_declaration":       "function", // go, ts, kotlin
	"method_declaration":         "method",   // go, java
	"type_declaration":           "type",     // go
	"function_item":              "function", // rust
	"struct_item":                "type",
	"union_item":                 "type",
	"enum_item":                  "type",
	"trait_item":                 "type",
	"impl_item":                  "impl",
	"mod_item":                   "module",
	"class_declaration":          "type", // ts/js/java/kotlin
	"class_interface":            "type", // objc @interface (also categories)
	"class_implementation":       "type", // objc @implementation (dedupes with @interface)
	"method_definition":          "method",
	"type_alias_declaration":     "type",
	"interface_declaration":      "type",
	"enum_declaration":           "type",
	"abstract_class_declaration": "type",
	"function_definition":        "function", // py, objc (C functions)
	"class_definition":           "type",     // py, dart
	"lambda_expression":          "function", // dart top-level/local functions (body span)
	"class_member_definition":    "method",   // dart methods and getters (body span)
	"mixin_declaration":          "type",     // dart
	"extension_declaration":      "type",     // dart
	// dart constructors and enum values (Rust reference: the generic
	// EntityExtractor maps every constructor node to element_type
	// "constructor"; enum constants are value members like properties are).
	// A constructor is a class member, so qualify() prefixes it with the
	// enclosing type ("<file>::Factory.named") exactly like a method.
	"constructor_signature":                     "constructor",
	"constant_constructor_signature":            "constructor",
	"factory_constructor_signature":             "constructor",
	"redirecting_factory_constructor_signature": "constructor",
	"enum_constant":                             "constant",
	"constructor_declaration":                   "constructor", // java, kotlin
	"annotation_type_declaration":               "type",
	"object_declaration":                        "type",     // kotlin
	"func_declaration":                          "function", // swift
	"struct_declaration":                        "type",
	"protocol_declaration":                      "type", // swift, objc
	"actor_declaration":                         "type",
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
	var walk func(n *sitter.Node, parentType, parentName string, depth int)
	const limit = 64
	walk = func(n *sitter.Node, parentType, parentName string, depth int) {
		if n == nil || depth > limit {
			return
		}
		if et, ok := kindSpec[n.Type()]; ok && !suppressed(parentType, n.Type()) {
			if name := nameOf(n, src); name != "" {
				out = append(out, Def{
					Kind:      et,
					Name:      name,
					StartLine: int(n.StartPoint().Row) + 1,
					EndLine:   int(n.EndPoint().Row) + 1,
					Parent:    parentName,
				})
				parentName = name
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i), n.Type(), parentName, depth+1)
		}
	}
	walk(root, "", "", 0)
	// Dedupe by kind+name, keeping the widest span: objc and dart declare a
	// member in one place (@interface, abstract member) and define it in
	// another (@implementation, override), and only the definition carries the
	// real end line.
	at := map[string]int{}
	var uniq []Def
	for _, d := range out {
		key := d.Kind + "\x1f" + d.Name
		i, ok := at[key]
		if !ok {
			at[key] = len(uniq)
			uniq = append(uniq, d)
			continue
		}
		if d.EndLine-d.StartLine > uniq[i].EndLine-uniq[i].StartLine {
			uniq[i] = d
		}
	}
	return uniq, nil
}

// suppressed hides node kinds a grammar reuses for a non-definition: in the
// objc grammar a property's type annotation is a struct_declaration
// (@property NSString *name parses as property_declaration > struct_declaration),
// which is a type reference, not a definition.
func suppressed(parentType, nodeType string) bool {
	return parentType == "property_declaration" && nodeType == "struct_declaration"
}

// nameOf extracts the definition name: dart constructors resolve through
// dartConstructorName, everything else prefers the named "name" field, else
// the first identifier-like child.
func nameOf(n *sitter.Node, src []byte) string {
	switch n.Type() {
	case "constructor_signature", "constant_constructor_signature",
		"factory_constructor_signature", "redirecting_factory_constructor_signature":
		return dartConstructorName(n, src)
	}
	if name := n.ChildByFieldName("name"); name != nil {
		return strings.TrimSpace(name.Content(src))
	}
	return firstIdentifierLike(n, src)
}

// dartConstructorName resolves the constructor's own member name, without the
// class: "Factory.named" yields "named", a default "Factory()" yields
// "Factory", "const Factory.zero()" and "factory Factory.create()" yield
// "zero"/"create". The grammar fields the class name as "name" (and, for named
// constructors, the dot as well), so the plain name-field lookup would return
// the class name for every form.
func dartConstructorName(n *sitter.Node, src []byte) string {
	// constant_constructor_signature wraps the dotted name in a qualified
	// node ("Factory.zero") instead of fielding identifiers.
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(i); c.Type() == "qualified" {
			return lastIdentifier(c, src)
		}
	}
	last := ""
	for i := 0; i < int(n.NamedChildCount()); i++ {
		switch c := n.NamedChild(i); c.Type() {
		case "identifier":
			last = strings.TrimSpace(c.Content(src))
		case "formal_parameter_list":
			// Everything after the parameters belongs to the body or to a
			// redirecting-factory target ("= Other.parse").
			return last
		}
	}
	return last
}

// lastIdentifier returns the text of the last identifier in a subtree.
func lastIdentifier(n *sitter.Node, src []byte) string {
	last := ""
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() == "identifier" {
			last = strings.TrimSpace(c.Content(src))
			continue
		}
		if id := lastIdentifier(c, src); id != "" {
			last = id
		}
	}
	return last
}

// firstIdentifierLike resolves names for grammars without a "name" field:
// objc protocol/class heads carry the name as their first identifier child;
// an objc selector method (- (void)setName:(NSString *)n age:(int)a) carries
// the selector parts as identifier children interleaved with method_parameter
// children, which join to "setName:age:" (the Rust selector format).
//
// The declarator is resolved before the identifier scan: a C function whose
// return type is a typedef name (static NSString *describe(void)) has a
// type_identifier child, which contains "identifier" and would otherwise win
// the scan over the function_declarator holding the function's own name.
func firstIdentifierLike(n *sitter.Node, src []byte) string {
	children := make([]*sitter.Node, 0, n.NamedChildCount())
	for i := 0; i < int(n.NamedChildCount()); i++ {
		children = append(children, n.NamedChild(i))
	}
	if name := objcSelector(children, src); name != "" {
		return name
	}
	for _, c := range children {
		if strings.Contains(c.Type(), "declarator") {
			if name := firstIdentifierLike(c, src); name != "" {
				return name
			}
		}
	}
	for _, c := range children {
		if strings.Contains(c.Type(), "identifier") {
			return strings.TrimSpace(c.Content(src))
		}
	}
	if name := signatureName(children, src); name != "" {
		return name
	}
	return ""
}

// objcSelector joins the identifier parts of an objc method node. A method
// with parameters is a selector ("setName:age:"); one without is a plain name
// ("sayHello"). method_type only appears in the objc grammar.
func objcSelector(children []*sitter.Node, src []byte) string {
	if !hasChildType(children, "method_type") {
		return ""
	}
	var sel strings.Builder
	ids, params := 0, 0
	for _, c := range children {
		switch c.Type() {
		case "identifier":
			if ids > 0 {
				sel.WriteString(":")
			}
			sel.WriteString(strings.TrimSpace(c.Content(src)))
			ids++
		case "method_parameter":
			params++
		}
	}
	if ids == 0 {
		return ""
	}
	if params == 0 {
		return sel.String()
	}
	return sel.String() + ":"
}

// signatureName resolves dart definitions, whose body-carrying containers
// (lambda_expression for functions, class_member_definition for members) hold
// the name in a nested signature node. The container node types must be
// dart-only shapes so no other grammar reaching nameOf can misresolve.
func signatureName(children []*sitter.Node, src []byte) string {
	for _, c := range children {
		switch c.Type() {
		// "declaration" is the dart wrapper around an abstract member's
		// function_signature; it never resolves on its own (a field's
		// declaration holds an initialized_identifier_list, not a signature).
		// constructor_signature and factory_constructor_signature are absent on
		// purpose: their name field is the class name, which would emit a
		// second element colliding with the class itself.
		case "method_signature", "function_signature", "getter_signature",
			"setter_signature", "declaration":
			if name := c.ChildByFieldName("name"); name != nil {
				return strings.TrimSpace(name.Content(src))
			}
			inner := make([]*sitter.Node, 0, c.NamedChildCount())
			for i := 0; i < int(c.NamedChildCount()); i++ {
				inner = append(inner, c.NamedChild(i))
			}
			if name := signatureName(inner, src); name != "" {
				return name
			}
		}
	}
	return ""
}

func hasChildType(children []*sitter.Node, t string) bool {
	for _, c := range children {
		if c.Type() == t {
			return true
		}
	}
	return false
}
