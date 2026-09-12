package index

import "testing"

// TestRelationshipsGrammarCallSeeds pins the emission of grammar-derived call
// edges: the seed selector resolves through the package name map at
// confidence 0.7, it wins over the identifier heuristic's 0.5 row for the same
// pair (one edge per (source, target), grammar first), and an unresolvable
// selector yields nothing.
func TestRelationshipsGrammarCallSeeds(t *testing.T) {
	els := []indexedElem{
		{qn: "Greeter.m::Greeter.sayHello", etype: "method", name: "sayHello", parent: -1,
			start: 2, end: 5, content: "[self setup];", calls: []string{"setup", "log:level:", "missing"}},
		{qn: "Greeter.m::Greeter.setup", etype: "method", name: "setup", start: 6, end: 6, parent: -1},
		{qn: "Greeter.m::Greeter.log:level:", etype: "method", name: "log:level:", start: 7, end: 7, parent: -1},
		{qn: "Greeter.m::Greeter", etype: "type", name: "Greeter", start: 1, end: 8, parent: -1},
	}
	names := map[string][]string{}
	for _, e := range els {
		names[e.name] = append(names[e.name], e.qn)
	}
	rels := relationships(els, names)
	got := map[string]float64{}
	for _, r := range rels {
		if r.Source != "Greeter.m::Greeter.sayHello" || r.RelType != "calls" {
			continue
		}
		got[r.Target] = r.Confidence
	}
	if c, ok := got["Greeter.m::Greeter.setup"]; !ok || c != 0.7 {
		t.Errorf("setup edge = (present %v, confidence %v), want 0.7", ok, c)
	}
	if c, ok := got["Greeter.m::Greeter.log:level:"]; !ok || c != 0.7 {
		t.Errorf("log:level: edge = (present %v, confidence %v), want 0.7", ok, c)
	}
	if _, ok := got["Greeter.m::Greeter.missing"]; ok {
		t.Error("unresolvable selector produced an edge")
	}
	for _, r := range rels {
		if r.Target == "Greeter.m::Greeter.setup" && r.Confidence == 0.5 {
			t.Error("identifier heuristic duplicated the grammar edge at 0.5")
		}
	}
}

// TestRelationshipsCallSeedShortNames pins that the grammar path ignores the
// minCallNameLen noise filter: objc's "init:" selector is an exact call site
// even though the word heuristic would drop a 4-character identifier.
func TestRelationshipsCallSeedShortNames(t *testing.T) {
	els := []indexedElem{
		{qn: "f.m::Box.build", etype: "method", name: "build", start: 2, end: 4, parent: -1,
			calls: []string{"init:"}},
		{qn: "f.m::Box.init:", etype: "method", name: "init:", start: 5, end: 5, parent: -1},
	}
	names := map[string][]string{"init:": {"f.m::Box.init:"}}
	rels := relationships(els, names)
	var found bool
	for _, r := range rels {
		if r.Source == "f.m::Box.build" && r.Target == "f.m::Box.init:" {
			found = true
			if r.RelType != "calls" {
				t.Fatalf("edge rel_type = %q, want calls", r.RelType)
			}
		}
	}
	if !found {
		t.Fatalf("short selector edge missing, got %v", rels)
	}
}

// TestCallOwner pins caller attribution for a call site: the element named by
// the enclosing method wins over another element that also covers the line
// (the class), a file-scope call falls back to the innermost covering element,
// and a call outside every element has no owner.
func TestCallOwner(t *testing.T) {
	els := []indexedElem{
		{qn: "g.m::Greeter", etype: "type", name: "Greeter", start: 1, end: 8, parent: -1},
		{qn: "g.m::Greeter.sayHello", etype: "method", name: "sayHello", start: 2, end: 5},
		{qn: "g.m::Greeter.setup", etype: "method", name: "setup", start: 6, end: 6, parent: -1},
	}
	if got := callOwner(els, 3, "sayHello"); got != 1 {
		t.Errorf("callOwner(line 3, sayHello) = %d, want 1", got)
	}
	if got := callOwner(els, 6, ""); got != 2 {
		t.Errorf("callOwner(line 6, file scope) = %d, want 2 (innermost covering)", got)
	}
	if got := callOwner(els, 20, ""); got != -1 {
		t.Errorf("callOwner(line 20) = %d, want -1", got)
	}
}
