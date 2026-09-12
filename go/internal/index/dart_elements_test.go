package index

import (
	"strconv"
	"strings"
	"testing"
)

const dartElementsFixture = `class Factory {
  Factory();
  Factory.named(int x);
  factory Factory.create() => Factory();
  const Factory.zero();
  static const int limit = 5;
}

enum Color { red, green, blue }

enum Status {
  ok(1),
  bad(2);
  const Status(this.code);
  final int code;
}

void main() {
  setState(() {});
}
`

// TestMatchDartConstructorsAndEnumValues pins the regex tier's constructor and
// enum-value extraction (the tier the CGO-free build runs): constructors are
// named after the constructor, not the class, and both constructors and enum
// values carry their owning type in recv so the qualified name cannot collide
// with the class or enum element.
func TestMatchDartConstructorsAndEnumValues(t *testing.T) {
	lines := strings.Split(dartElementsFixture, "\n")
	var ctors, constants, others []string
	for _, m := range matchDart(lines) {
		label := m.name + "=" + m.kind
		switch m.kind {
		case "constructor":
			ctors = append(ctors, label+"@"+strconv.Itoa(m.line)+"/"+m.recv)
		case "constant":
			constants = append(constants, label+"@"+strconv.Itoa(m.line)+"/"+m.recv)
		default:
			others = append(others, label)
		}
	}
	wantCtors := []string{
		"Factory=constructor@2/Factory",
		"named=constructor@3/Factory",
		"create=constructor@4/Factory",
		"zero=constructor@5/Factory",
	}
	assertStrings(t, "constructors", ctors, wantCtors)
	// Values of the one-line and the multi-line form; the members after the
	// ";" terminator (an enhanced enum's const constructor, a field) are not
	// values — enhanced-enum bodies are also beyond the vendored grammar.
	wantConstants := []string{
		"red=constant@9/Color", "green=constant@9/Color", "blue=constant@9/Color",
		"ok=constant@12/Status", "bad=constant@13/Status",
	}
	assertStrings(t, "enum values", constants, wantConstants)
	for _, want := range []string{"Factory=class", "Color=class", "Status=class", "main=function"} {
		if !containsString(others, want) {
			t.Errorf("%s missing, got %v", want, others)
		}
	}
}

// TestQualifyDartConstructorNames pins the constructor and enum-value qualified
// names: the type prefix comes from recv, so it does not depend on the
// declaring element's heuristic end line (the regex tier cuts a class at its
// first member otherwise). Two elements named "Factory" must keep distinct qns,
// or the constructor's upsert replaces the class element's row.
func TestQualifyDartConstructorNames(t *testing.T) {
	fe := fileElements{rel: "lib/factory.dart", elements: []indexedElem{
		{name: "Factory", etype: "class", lang: "dart", start: 1, end: 1, parent: -1},
		{name: "Factory", etype: "constructor", lang: "dart", start: 2, end: 2,
			parent: -1, recv: "Factory"},
		{name: "named", etype: "constructor", lang: "dart", start: 3, end: 3,
			parent: -1, recv: "Factory"},
		{name: "red", etype: "constant", lang: "dart", start: 9, end: 9, parent: -1, recv: "Color"},
	}}
	qualify(fe)
	want := []string{
		"lib/factory.dart::Factory",         // the class element
		"lib/factory.dart::Factory.Factory", // default constructor
		"lib/factory.dart::Factory.named",
		"lib/factory.dart::Color.red",
	}
	for i, w := range want {
		if got := fe.elements[i].qn; got != w {
			t.Errorf("qn(%s %s) = %q, want %q", fe.elements[i].etype, fe.elements[i].name, got, w)
		}
	}
	if fe.elements[1].qn == fe.elements[0].qn {
		t.Fatalf("default constructor shares the class qn %q: the class row would be replaced", fe.elements[1].qn)
	}
}

func assertStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
