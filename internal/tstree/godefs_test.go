//go:build tstree

package tstree

import "testing"

// TestExtractGoKeepsTypesAndMethodOwners pins the Go definitions the grammar
// tier must produce.
//
// Two defects made the Go grammar tier produce a DIFFERENT graph than the
// regex tier for the same file — measured on this repository's own sources on
// 2026-10-20, the tstree tier indexed 260 Go files into 33 types and 933
// methods where the regex tier found 533 types and 1005 methods:
//
//  1. The dedupe key is kind+name, so two types' methods with the SAME name
//     collapsed into one: `A.String` and `B.String` became a single `String`.
//     A Go method's identity is its receiver plus its name, so the key needs
//     the owner.
//
//  2. A Go type declaration is `type_declaration > type_spec > name`, and the
//     receiver lives in the method's `receiver` field. Neither reached the
//     extractor, so types were dropped and methods lost their owner — a
//     method's qualified name became `<file>::String` instead of
//     `<file>::A.String`.
//
// The consequence was not a missing tier but a WRONG graph: the same
// repository indexed under `-tags tstree` answered different qualified names
// than the shipped CGO-free build, and same-named methods on different types
// were indistinguishable.
func TestExtractGoKeepsTypesAndMethodOwners(t *testing.T) {
	src := []byte(`package p

type A struct{ x int }

func (a *A) String() string { return "a" }

type B struct{ y int }

func (b *B) String() string { return "b" }
`)
	defs, err := Extract(src, "go")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	byKey := map[string]Def{}
	for _, d := range defs {
		byKey[d.Kind+"|"+d.Parent+"|"+d.Name] = d
	}
	for _, want := range []struct{ kind, owner, name string }{
		{"type", "", "A"},
		{"type", "", "B"},
		{"method", "A", "String"},
		{"method", "B", "String"},
	} {
		key := want.kind + "|" + want.owner + "|" + want.name
		if _, ok := byKey[key]; !ok {
			t.Errorf("missing definition %s (kind=%q owner=%q name=%q); got %v", key, want.kind, want.owner, want.name, defs)
		}
	}
	// The two String methods must be DISTINCT definitions: the dedupe key
	// carries the owner, so one cannot swallow the other.
	if byKey["method|A|String"].StartLine == byKey["method|B|String"].StartLine {
		t.Errorf("A.String and B.String collapsed to one definition at line %d", byKey["method|A|String"].StartLine)
	}
}
