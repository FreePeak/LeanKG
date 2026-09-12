//go:build tstree

package tstree

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestGrammarCoverage pins the bundled grammar set. objc and dart arrive from
// the vendored grammars in this directory (smacker ships neither); markdown
// has no definition-kind grammar and must stay on the regex tier.
func TestGrammarCoverage(t *testing.T) {
	for _, lang := range []string{"go", "rust", "ts", "tsx", "js", "jsx", "py", "java", "kotlin", "swift", "objc", "dart"} {
		if Grammar(lang) == nil {
			t.Errorf("no grammar bundled for %s", lang)
		}
	}
	if Grammar("md") != nil {
		t.Error("markdown must not claim a tree-sitter grammar")
	}
}

const objcFixture = `#import <Foundation/Foundation.h>

@protocol Greeter
- (void)greet;
@end

@interface Person : NSObject <Greeter>
@property (nonatomic, copy) NSString *name;
- (void)sayHello;
@end

@implementation Person
- (void)sayHello {
    NSLog(@"Hi");
}
- (void)setName:(NSString *)name age:(int)age {
    _name = name;
}
@end

int main(int argc, char *argv[]) {
    return 0;
}

static NSString *describe(void) {
    return @"x";
}
`

// TestExtractObjC pins objective-c definitions: @protocol/@interface heads
// (the @implementation head folds into the class element), methods declared
// and defined — including the "setName:age:" selector form — and C functions
// whose names resolve through their function_declarator, including one whose
// return type is a typedef name (the declarator must win over the
// type_identifier child).
func TestExtractObjC(t *testing.T) {
	defs, err := Extract([]byte(objcFixture), "objc")
	if err != nil {
		t.Fatal(err)
	}
	// Person and sayHello are declared in the @interface and defined in the
	// @implementation; the widest span wins, so both point at the definition.
	want := []Def{
		{Kind: "type", Name: "Greeter", StartLine: 3, EndLine: 5},
		{Kind: "method", Name: "greet", StartLine: 4, EndLine: 4},
		{Kind: "type", Name: "Person", StartLine: 12, EndLine: 19},
		{Kind: "method", Name: "sayHello", StartLine: 13, EndLine: 15},
		{Kind: "method", Name: "setName:age:", StartLine: 16, EndLine: 18},
		{Kind: "function", Name: "main", StartLine: 21, EndLine: 23},
		{Kind: "function", Name: "describe", StartLine: 25, EndLine: 27},
	}
	assertDefs(t, defs, want)
}

const dartFixture = `import 'dart:math';

abstract class Animal {
  void speak();
}

class Dog extends Animal {
  @override
  void speak() => print('woof');
  int legs() { return 4; }
  Dog();
  Dog.named(int legs);
  factory Dog.puppy() => Dog();
  const Dog.other();
}

mixin Swimmer {
  void swim() {}
}

enum Color { red, green, blue }

extension AnimalX on Animal {
  String get tag => 'x';
}

int add(int a, int b) {
  return a + b;
}
`

// TestExtractDart pins dart definitions: classes, mixins, enums, extensions,
// top-level functions, methods, getters, constructors (default, named,
// factory and const — named after the constructor, without the class) and
// enum constants. Every constructor node maps to element type "constructor"
// like the Rust reference; enum constants map to "constant".
func TestExtractDart(t *testing.T) {
	defs, err := Extract([]byte(dartFixture), "dart")
	if err != nil {
		t.Fatal(err)
	}
	// Animal.speak (abstract member) and Dog.speak (override) both span one
	// line, so the first occurrence wins; legs/swim/tag/add keep their bodies.
	want := []Def{
		{Kind: "type", Name: "Animal", StartLine: 3, EndLine: 5},
		{Kind: "method", Name: "speak", StartLine: 4, EndLine: 4},
		{Kind: "type", Name: "Dog", StartLine: 7, EndLine: 15},
		{Kind: "method", Name: "legs", StartLine: 10, EndLine: 10},
		{Kind: "constructor", Name: "Dog", StartLine: 11, EndLine: 11},
		{Kind: "constructor", Name: "named", StartLine: 12, EndLine: 12},
		{Kind: "constructor", Name: "puppy", StartLine: 13, EndLine: 13},
		{Kind: "constructor", Name: "other", StartLine: 14, EndLine: 14},
		{Kind: "type", Name: "Swimmer", StartLine: 17, EndLine: 19},
		{Kind: "method", Name: "swim", StartLine: 18, EndLine: 18},
		{Kind: "type", Name: "Color", StartLine: 21, EndLine: 21},
		{Kind: "constant", Name: "red", StartLine: 21, EndLine: 21},
		{Kind: "constant", Name: "green", StartLine: 21, EndLine: 21},
		{Kind: "constant", Name: "blue", StartLine: 21, EndLine: 21},
		{Kind: "type", Name: "AnimalX", StartLine: 23, EndLine: 25},
		{Kind: "method", Name: "tag", StartLine: 24, EndLine: 24},
		{Kind: "function", Name: "add", StartLine: 27, EndLine: 29},
	}
	assertDefs(t, defs, want)
}

// assertDefs compares the extracted set as a whole, so a fixture that grows an
// unexpected definition (or loses one) fails loudly instead of silently
// passing a subset check.
func assertDefs(t *testing.T, got, want []Def) {
	t.Helper()
	byKey := map[string]Def{}
	for _, d := range got {
		byKey[d.Kind+" "+d.Name] = d
	}
	for _, w := range want {
		g, ok := byKey[w.Kind+" "+w.Name]
		if !ok {
			t.Errorf("missing def %s %q (got %s)", w.Kind, w.Name, formatDefs(got))
			continue
		}
		if g.StartLine != w.StartLine || g.EndLine != w.EndLine {
			t.Errorf("%s %q lines = %d..%d, want %d..%d", w.Kind, w.Name, g.StartLine, g.EndLine, w.StartLine, w.EndLine)
		}
		delete(byKey, w.Kind+" "+w.Name)
	}
	for _, extra := range byKey {
		t.Errorf("unexpected def %s %q at lines %d..%d", extra.Kind, extra.Name, extra.StartLine, extra.EndLine)
	}
}

func formatDefs(defs []Def) string {
	var parts []string
	for _, d := range defs {
		parts = append(parts, d.Kind+" "+d.Name+"@"+strconv.Itoa(d.StartLine)+"-"+strconv.Itoa(d.EndLine))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

const objcJunkFixture = `@interface Widget (Extras)
@end

@interface Widget : NSObject
@property (nonatomic) NSInteger count;
@end

@implementation Widget
- (void)run {
    void (^block)(void) = ^{
        NSLog(@"x");
    };
    block();
}
@end

typedef struct { int x; } Pair;
enum Kind { A, B };
`

const dartJunkFixture = `final topVar = 3;

class Factory {
  Factory();
  Factory.named(int x);
  factory Factory.create() => Factory();
  static const int limit = 5;
  final Function closer = () => 1;
}

void main() {
  var f = (int x) => x * 2;
  [1, 2].map((e) => e + 1);
  int inner() => 7;
  print(inner());
}
`

// TestExtractIgnoresNonDefinitions pins that nodes which merely reference or
// annotate a definition never become elements: objc property type annotations
// and blocks, the category head that folds into its class, dart fields, static
// members and anonymous closures. Dart constructors are definitions (see
// TestExtractDart), but the class name, default arguments and redirecting
// targets around them are not.
func TestExtractIgnoresNonDefinitions(t *testing.T) {
	objcDefs, err := Extract([]byte(objcJunkFixture), "objc")
	if err != nil {
		t.Fatal(err)
	}
	assertDefs(t, objcDefs, []Def{
		{Kind: "type", Name: "Widget", StartLine: 8, EndLine: 15},
		{Kind: "method", Name: "run", StartLine: 9, EndLine: 14},
	})

	dartDefs, err := Extract([]byte(dartJunkFixture), "dart")
	if err != nil {
		t.Fatal(err)
	}
	assertDefs(t, dartDefs, []Def{
		{Kind: "type", Name: "Factory", StartLine: 3, EndLine: 9},
		{Kind: "constructor", Name: "Factory", StartLine: 4, EndLine: 4},
		{Kind: "constructor", Name: "named", StartLine: 5, EndLine: 5},
		{Kind: "constructor", Name: "create", StartLine: 6, EndLine: 6},
		{Kind: "function", Name: "main", StartLine: 11, EndLine: 16},
		{Kind: "function", Name: "inner", StartLine: 14, EndLine: 14},
	})
}

const objcCallFixture = `@implementation Greeter
- (void)sayHello {
    [self setup];
    [logger log:@"hi" level:1];
    [[Foo alloc] init];
    [bar];
}
- (void)setup {}
- (void)log:(NSString *)msg level:(int)lvl {}
@end
`

// TestExtractCallsObjC pins the message-send call walk: one call per
// message_expression, the selector as the target (keywords joined with ":")
// and the enclosing method as the caller. A bare receiver ([bar]) has no
// selector and yields no call.
func TestExtractCallsObjC(t *testing.T) {
	calls, err := ExtractCalls([]byte(objcCallFixture), "objc")
	if err != nil {
		t.Fatal(err)
	}
	want := []Call{
		{Caller: "sayHello", Callee: "setup", Line: 3},
		{Caller: "sayHello", Callee: "log:level:", Line: 4},
		{Caller: "sayHello", Callee: "alloc", Line: 5},
		{Caller: "sayHello", Callee: "init", Line: 5},
	}
	// The walk is pre-order, so a nested send is visited before its receiver;
	// compare as a set.
	sort.Slice(calls, func(i, j int) bool {
		if calls[i].Line != calls[j].Line {
			return calls[i].Line < calls[j].Line
		}
		return calls[i].Callee < calls[j].Callee
	})
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i, w := range want {
		if calls[i] != w {
			t.Errorf("call[%d] = %+v, want %+v", i, calls[i], w)
		}
	}
}

// TestExtractCallsOtherLanguages pins that only the languages with a call
// walk produce calls; everything else returns nil so the caller keeps its
// non-grammar behavior.
func TestExtractCallsOtherLanguages(t *testing.T) {
	for _, lang := range []string{"go", "dart", "swift", "py"} {
		calls, err := ExtractCalls([]byte("func main() { helper() }\n"), lang)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if len(calls) != 0 {
			t.Errorf("%s: calls = %v, want none", lang, calls)
		}
	}
}

// TestExtractDartConstructorParent pins Def.Parent: a constructor's name is
// the constructor, not the class, so the enclosing definition name is what
// lets the indexer qualify it ("<file>::Box.named") even when the class body
// sits on a single line and line-based parent detection finds no parent.
func TestExtractDartConstructorParent(t *testing.T) {
	defs, err := Extract([]byte("class Box { Box(); Box.named(); }\n"), "dart")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range defs {
		got[d.Kind+" "+d.Name] = d.Parent
	}
	want := map[string]string{"type Box": "", "constructor Box": "Box", "constructor named": "Box"}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("missing def %q, got %v", k, got)
			continue
		}
		if g != w {
			t.Errorf("parent of %q = %q, want %q", k, g, w)
		}
	}
}
