package index

// Round-trip tests for the second language-expansion wave
// (extract_langexp2.go): fixture file -> regex matcher -> the shared element
// pipeline (ranges, parents, qualify, content) -> expected element names and
// kinds. The pipeline mirrors extractFileAs's regex path; matchLangExp2 is
// reached in production through matchLangExp's default branch, which the
// delegation test below pins.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// extractLangExp2Fixture runs the full regex extraction pipeline on a fixture.
func extractLangExp2Fixture(t *testing.T, rel, lang string) fileElements {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
	ms, ok := matchLangExp2(lang, lines)
	if !ok {
		t.Fatalf("no matcher for language %q", lang)
	}
	// ends: same default branch as extractFileAs — body extends to the next
	// element start.
	ends := make([]int, len(ms))
	for i := range ms {
		ends[i] = len(lines)
		if i+1 < len(ms) {
			ends[i] = ms[i+1].line - 1
		}
		if ends[i] < ms[i].line {
			ends[i] = ms[i].line
		}
	}
	fe := fileElements{rel: rel}
	for i, m := range ms {
		fe.elements = append(fe.elements, indexedElem{
			name: m.name, etype: m.kind, lang: lang,
			start: m.line, end: ends[i], parent: -1, recv: m.recv,
		})
	}
	assignParents(fe.elements)
	qualify(fe)
	boundContent(fe.elements, lines)
	return fe
}

// gotKinds renders an extraction result as "kind:name" in source order.
func gotKinds(fe fileElements) []string {
	out := make([]string, 0, len(fe.elements))
	for _, e := range fe.elements {
		out = append(out, e.etype+":"+e.name)
	}
	return out
}

// wave2Fixtures pins fixture -> extract -> expected elements for every
// language of the second expansion wave. Kind "import" covers module/path
// imports; table/view/procedure are the SQL element types (Rust sql.rs DDL
// vocabulary); class/type cover containers and declared types.
var wave2Fixtures = []struct {
	lang, fixture string
	want          []string
}{
	{"crystal", "testdata/langexp2/crystal/user.cr", []string{
		"import:json", "class:User", "method:initialize", "method:greet",
	}},
	{"crystal", "testdata/langexp2/crystal/util.cr", []string{
		"import:yaml", "class:Utils", "method:double",
		"class:Point", "class:Color", "method:helper",
	}},
	{"cuda", "testdata/langexp2/cuda/kernel.cu", []string{
		// The __global__ prototype on line 13 ends in ';' and is skipped;
		// the <<<...>>> launch inside launch() is not a definition.
		"import:cuda_runtime.h", "type:Dim", "type:real",
		"function:square", "function:kernel", "function:launch",
	}},
	{"cuda", "testdata/langexp2/cuda/math.cuh", []string{
		"import:math.h", "function:fast_norm", "type:Mode",
	}},
	{"cypher", "testdata/langexp2/cypher/schema.cyp", []string{
		"type:Person", "type:KNOWS", "type:Company", "type:WORKS_AT",
	}},
	{"cypher", "testdata/langexp2/cypher/queries.cyp", []string{
		"type:Person", "type:Company", "type:KNOWS", "type:Employee",
		"type:WORKS_AT",
	}},
	{"elm", "testdata/langexp2/elm/Counter.elm", []string{
		"class:Counter", "import:Html", "type:Model", "type:Msg",
		"function:double", "function:update", "function:view",
	}},
	{"elm", "testdata/langexp2/elm/Geometry.elm", []string{
		"class:Geometry", "import:Html.Attributes", "type:Shape",
		"function:area", "function:scale",
	}},
	{"erlang", "testdata/langexp2/erlang/math.erl", []string{
		"class:math", "function:double", "function:add", "function:factorial",
	}},
	{"erlang", "testdata/langexp2/erlang/worker.erl", []string{
		"class:worker", "import:worker.hrl", "import:lists",
		"function:start", "function:loop", "function:handle",
	}},
	{"fsharp", "testdata/langexp2/fsharp/Math.fs", []string{
		"class:Math", "import:System", "function:double", "function:add",
		"type:Point", "function:distance", // `let origin = ...` is a value
	}},
	{"fsharp", "testdata/langexp2/fsharp/Shapes.fs", []string{
		"class:Geometry", "import:System.Drawing", "type:Shape",
		"function:area", "function:scale",
	}},
	{"glsl", "testdata/langexp2/glsl/shader.vert", []string{
		"function:main",
	}},
	{"glsl", "testdata/langexp2/glsl/bright.frag", []string{
		"function:luminance", "function:main",
	}},
	{"hlsl", "testdata/langexp2/hlsl/shader.hlsl", []string{
		"class:Light", "function:main_ps", // cbuffer block is not an element
	}},
	{"hlsl", "testdata/langexp2/hlsl/lighting.fxh", []string{
		"import:common.hlsli", "class:Material", "function:tint", "function:shade",
	}},
	{"nim", "testdata/langexp2/nim/math.nim", []string{
		"import:std/strutils", "function:double", "function:add",
		"type:Point", "function:distance",
	}},
	{"nim", "testdata/langexp2/nim/seqs.nim", []string{
		"import:std", "import:algorithm", "function:pairs",
		"function:twice", "function:topOr",
	}},
	{"ocaml", "testdata/langexp2/ocaml/math.ml", []string{
		"import:List", "function:double", "function:add",
		"class:Math", "class:counter", "method:inc", "method:get",
	}},
	{"ocaml", "testdata/langexp2/ocaml/shapes.mli", []string{
		"type:shape", "class:Printer", "class:shape_view",
		"method:describe", "method:render", "function:area",
	}},
	{"powershell", "testdata/langexp2/powershell/User.ps1", []string{
		"import:System.Text", "function:Get-User", "function:Set-User",
		"function:Test-Helper",
	}},
	{"powershell", "testdata/langexp2/powershell/Repo.psm1", []string{
		"import:./Logging.psm1", "class:Repository", "function:Save-Item",
	}},
	{"qsharp", "testdata/langexp2/qsharp/bell.qs", []string{
		"class:Microsoft.Quantum.Samples", "import:Microsoft.Quantum.Intrinsic",
		"import:Microsoft.Quantum.Measurement", "function:BellPair",
		"function:EntanglePair",
	}},
	{"qsharp", "testdata/langexp2/qsharp/Grover.qs", []string{
		"class:Quantum.Search", "import:Microsoft.Quantum.Canon",
		"function:IndexOfMarked", "function:ApplyOracle",
	}},
	{"solidity", "testdata/langexp2/solidity/Counter.sol", []string{
		"import:./Helper.sol", "class:Counter", "method:increment",
		"method:getCount",
	}},
	{"solidity", "testdata/langexp2/solidity/Escrow.sol", []string{
		"import:./lib/SafeMath.sol", "class:IReceiver", "method:receiveFunds",
		"class:Fees", "method:compute", "type:Deal", "type:State",
		"class:Escrow", "method:constructor", "method:release",
	}},
	{"sql", "testdata/langexp2/sql/pgsql.sql", []string{
		"function:calc_total", "function:update_status", // public.-qualified
	}},
	{"sql", "testdata/langexp2/sql/tsql.sql", []string{
		"table:Users", "procedure:GetUser", "function:Age", "view:ActiveUsers",
	}},
	{"sql", "testdata/langexp2/sql/plsql.pls", []string{
		// Package spec + body collapse: employee_pkg, get_name, update_salary.
		"class:employee_pkg", "function:get_name", "procedure:update_salary",
	}},
	{"systemverilog", "testdata/langexp2/systemverilog/packet.sv", []string{
		"class:packet", "function:get_length", "function:set_length",
	}},
	{"systemverilog", "testdata/langexp2/systemverilog/alu.sv", []string{
		"class:alu", "function:bitwise_and", "function:reset",
		"class:base_driver", // `pure virtual task drive()` is a prototype
	}},
	{"verilog", "testdata/langexp2/verilog/counter.v", []string{
		"class:counter",
	}},
	{"verilog", "testdata/langexp2/verilog/shift.v", []string{
		"class:shift_register", "function:mask", // legacy no-port-list function
	}},
	{"zig", "testdata/langexp2/zig/math.zig", []string{
		"import:std", "function:add", "type:Point", "function:add works",
	}},
	{"zig", "testdata/langexp2/zig/queue.zig", []string{
		"import:std", "type:Queue", "function:init", "function:push",
		"type:Kind", "function:queue push",
	}},
}

func TestExtractLangExp2RoundTrip(t *testing.T) {
	for _, tt := range wave2Fixtures {
		t.Run(tt.lang+"/"+filepath.Base(tt.fixture), func(t *testing.T) {
			got := gotKinds(extractLangExp2Fixture(t, tt.fixture, tt.lang))
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Fatalf("elements = %v, want %v", got, tt.want)
			}
		})
	}
}

// wave2Examples pins the extraction result for the REAL sample files under
// examples/<lang>-api/ — the acceptance list for the language-expansion
// wave. Skipped only when the examples tree is not part of the checkout.
var wave2Examples = []struct {
	lang, path string
	want       []string
}{
	{"crystal", "examples/crystal-api/user.cr", []string{
		"import:json", "class:User", "method:initialize", "method:greet",
		"class:Utils", "method:double",
	}},
	{"cuda", "examples/cuda-api/kernel.cu", []string{
		"function:vec_add", "function:square", "function:launch",
	}},
	{"cypher", "examples/cypher-api/graph.cyp", []string{
		"type:Person", "type:KNOWS", "type:Company", "type:WORKS_AT",
	}},
	{"elm", "examples/elm-api/Counter.elm", []string{
		"class:Counter", "import:Html", "type:Model", "type:Msg",
		"function:double", "function:update", "function:view",
	}},
	{"erlang", "examples/erlang-api/math.erl", []string{
		"class:math", "function:double", "function:add", "function:factorial",
	}},
	{"fsharp", "examples/fsharp-api/Math.fs", []string{
		"class:Math", "function:double", "function:add", "type:Point",
		"function:distance",
	}},
	{"glsl", "examples/glsl-api/shader.vert", []string{"function:main"}},
	{"hlsl", "examples/hlsl-api/shader.hlsl", []string{
		"class:Light", "function:main_ps",
	}},
	{"nim", "examples/nim-api/math.nim", []string{
		"import:std/strutils", "function:double", "function:add",
		"type:Point", "function:distance",
	}},
	{"ocaml", "examples/ocaml-api/math.ml", []string{
		"import:List", "function:double", "function:add", "class:Math",
		"class:counter", "method:inc", "method:get",
	}},
	{"powershell", "examples/powershell-api/User.ps1", []string{
		"function:Get-User", "function:Set-User", "function:Test-Helper",
	}},
	{"qsharp", "examples/qsharp-api/bell.qs", []string{
		"class:Microsoft.Quantum.Samples", "import:Microsoft.Quantum.Intrinsic",
		"import:Microsoft.Quantum.Measurement", "function:BellPair",
		"function:EntanglePair",
	}},
	{"solidity", "examples/solidity-api/Counter.sol", []string{
		"import:./Helper.sol", "class:Counter", "method:increment",
		"method:getCount",
	}},
	{"sql", "examples/pgsql-api/fn.sql", []string{
		"function:calc_total", "function:update_status",
	}},
	{"sql", "examples/tsql-api/users.sql", []string{
		"procedure:GetUser", "function:Age", "view:ActiveUsers",
	}},
	{"sql", "examples/plsql-api/pkg.pls", []string{
		"class:employee_pkg", "function:get_name", "procedure:update_salary",
	}},
	{"systemverilog", "examples/systemverilog-api/packet.sv", []string{
		"class:packet", "function:get_length", "function:set_length",
	}},
	{"verilog", "examples/verilog-api/counter.v", []string{"class:counter"}},
	{"zig", "examples/zig-api/math.zig", []string{
		"import:std", "function:add", "type:Point", "function:add works",
	}},
}

func TestExtractLangExp2Examples(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "examples")); err != nil {
		t.Skipf("examples/ tree unavailable: %v", err)
	}
	for _, tt := range wave2Examples {
		t.Run(tt.lang+"/"+filepath.Base(tt.path), func(t *testing.T) {
			full := filepath.Join(root, filepath.FromSlash(tt.path))
			if _, err := os.Stat(full); err != nil {
				t.Skipf("%s unavailable: %v", tt.path, err)
			}
			got := gotKinds(extractLangExp2Fixture(t, full, tt.lang))
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Fatalf("elements = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMatchLangExpReachesWave2 pins the delegation contract: matchLangExp is
// the function extract.go's switch default calls, so wave-2 ids must reach
// their matchers through it, wave-1 ids keep their own matchers, and
// built-in languages stay unclaimed.
func TestMatchLangExpReachesWave2(t *testing.T) {
	cases := map[string]string{
		"crystal":       "class A",
		"cuda":          "__global__ void k() {",
		"cypher":        "MATCH (n:Person)",
		"elm":           "module Main exposing (main)",
		"erlang":        "f() -> ok.",
		"fsharp":        "let f x = x",
		"glsl":          "void main() {",
		"hlsl":          "struct S {",
		"nim":           "proc f() = discard",
		"ocaml":         "let f x = x",
		"sql":           "CREATE TABLE t (id INT);",
		"powershell":    "function Get-Item {",
		"qsharp":        "operation Op() : Unit {",
		"solidity":      "contract C {",
		"systemverilog": "class packet;",
		"verilog":       "module counter;",
		"zig":           "pub fn main() void {",
	}
	for lang, line := range cases {
		ms, ok := matchLangExp(lang, []string{line})
		if !ok {
			t.Fatalf("matchLangExp(%q) did not reach the wave-2 matcher", lang)
		}
		if len(ms) == 0 {
			t.Fatalf("matchLangExp(%q) on %q extracted nothing", lang, line)
		}
	}
	// Wave-1 ids still route to their own matchers.
	for _, lang := range []string{"c", "cpp", "csharp", "php", "ruby", "scala", "perl", "lua", "haskell", "elixir"} {
		if _, ok := matchLangExp(lang, []string{"x"}); !ok {
			t.Fatalf("matchLangExp(%q) must still claim the wave-1 set", lang)
		}
	}
	// Built-ins and unknown tags fall through untouched.
	for _, lang := range []string{"go", "rust", "py", "ts", "tsx", "js", "jsx", "md",
		"java", "kotlin", "swift", "objc", "dart", "cobol", ""} {
		if _, ok := matchLangExp(lang, []string{"x"}); ok {
			t.Fatalf("matchLangExp(%q) must not claim a built-in or unknown language", lang)
		}
		if _, ok := matchLangExp2(lang, []string{"x"}); ok {
			t.Fatalf("matchLangExp2(%q) must not claim a language it does not own", lang)
		}
	}
}
