//go:build tstree

package tstree

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// Call is one call site found by the tree-sitter tier.
type Call struct {
	// Caller is the name of the definition that encloses the call site: for
	// objective-c the selector of the enclosing method_definition
	// ("sayHello", "setName:age:"). Empty at file scope (a C function body
	// resolved through its own element, or a call outside every definition).
	Caller string
	// Callee is the call target as spelled at the call site: for
	// objective-c the message selector ("setup", "log:level:").
	Callee string
	// Line is the 1-based line of the call site.
	Line int
}

// ExtractCalls returns the call sites of the languages whose grammar exposes
// a call node. objective-c message sends ([recv sel:arg]) yield one call per
// selector; languages without a call walk return nil, nil so the caller keeps
// its non-grammar behavior.
func ExtractCalls(src []byte, lang string) ([]Call, error) {
	if lang != "objc" {
		return nil, nil
	}
	p := parserFor(lang)
	if p == nil {
		return nil, nil
	}
	tree := p.Parse(nil, src)
	if tree == nil {
		return nil, nil
	}
	return objcMessageCalls(tree.RootNode(), src), nil
}

// objcMessageCalls walks a parsed objc tree in source order and returns one
// call per message_expression, attributed to the enclosing method_definition
// selector (Rust reference: indexer/objc.rs extract_objc_message_calls).
func objcMessageCalls(root *sitter.Node, src []byte) []Call {
	var out []Call
	var walk func(n *sitter.Node, caller string)
	walk = func(n *sitter.Node, caller string) {
		if n == nil {
			return
		}
		if n.Type() == "method_definition" {
			// Reuses the element-naming path, so callers and elements spell a
			// selector identically ("setName:age:").
			if sel := firstIdentifierLike(n, src); sel != "" {
				caller = sel
			}
		}
		if n.Type() == "message_expression" {
			if sel := objcMessageSelector(n, src); sel != "" {
				out = append(out, Call{
					Caller: caller,
					Callee: sel,
					Line:   int(n.StartPoint().Row) + 1,
				})
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i), caller)
		}
	}
	walk(root, "")
	return out
}

// objcMessageSelector joins the selector parts of a message_expression:
// [recv sel] and [recv sel:arg other:arg2]. The first non-bracket child is the
// receiver and is skipped; for a keyword message each identifier followed by
// ":" contributes "name:" and its argument expression is skipped. Returns ""
// when the node carries no selector (the grammar parses a bare [recv] into an
// empty method identifier).
func objcMessageSelector(n *sitter.Node, src []byte) string {
	children := make([]*sitter.Node, 0, n.ChildCount())
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c.Type() == "[" || c.Type() == "]" {
			continue
		}
		children = append(children, c)
	}
	if len(children) == 0 {
		return ""
	}
	var sel strings.Builder
	parts := 0
	for i := 1; i < len(children); {
		c := children[i]
		if c.Type() != "identifier" {
			i++
			continue
		}
		name := strings.TrimSpace(c.Content(src))
		if i+1 < len(children) && children[i+1].Type() == ":" {
			sel.WriteString(name)
			sel.WriteString(":")
			parts++
			i += 2
			// Skip this keyword's argument expression(s).
			for i < len(children) && children[i].Type() != "identifier" && children[i].Type() != ":" {
				i++
			}
			continue
		}
		if parts == 0 {
			sel.WriteString(name)
			break
		}
		i++
	}
	return sel.String()
}
