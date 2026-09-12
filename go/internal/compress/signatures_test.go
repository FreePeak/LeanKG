package compress

import (
	"strings"
	"testing"
)

func TestExtractRustSignatures(t *testing.T) {
	code := `
pub struct User {}
pub async fn login(user: String) -> Result<(), Error> {
    println!("hi");
}
`
	sigs := ExtractSignatures(code, "rs")
	if len(sigs) != 2 {
		t.Fatalf("got %d signatures, want 2: %+v", len(sigs), sigs)
	}
	if sigs[0].Kind != "struct" || sigs[0].Name != "User" || !sigs[0].IsExported {
		t.Errorf("sig[0] = %+v, want exported struct User", sigs[0])
	}
	if sigs[1].Kind != "fn" || sigs[1].Name != "login" {
		t.Errorf("sig[1] = %+v, want fn login", sigs[1])
	}
	if got := sigs[1].ToTDD(); got != "~λ+login(user:s)→R" {
		t.Errorf("ToTDD() = %q, want %q", got, "~λ+login(user:s)→R")
	}
}

func TestExtractJavaSignatures(t *testing.T) {
	code := `
package com.test;
public class JavaApp {
    private void privateHelp(int count) {
        return;
    }
    public static String getApp() {
        return "App";
    }
}
`
	sigs := ExtractSignatures(code, "java")
	if len(sigs) != 3 {
		t.Fatalf("got %d signatures, want 3: %+v", len(sigs), sigs)
	}
	if sigs[0].Kind != "class" || sigs[0].Name != "JavaApp" {
		t.Errorf("sig[0] = %+v, want class JavaApp", sigs[0])
	}
	if sigs[1].Kind != "method" || sigs[1].Name != "privateHelp" || sigs[1].IsExported {
		t.Errorf("sig[1] = %+v, want private method privateHelp", sigs[1])
	}
	if sigs[1].ReturnType != "void" {
		t.Errorf("sig[1].ReturnType = %q, want void", sigs[1].ReturnType)
	}
	if sigs[2].Kind != "method" || sigs[2].Name != "getApp" || !sigs[2].IsExported {
		t.Errorf("sig[2] = %+v, want public method getApp", sigs[2])
	}
}

func TestExtractTSSignatures(t *testing.T) {
	code := `
// a comment with function fake() {
export async function fetchUser(id: string): Promise<User> {
  return null;
}
export class Repo {
}
interface Shape {
}
export type Id = string;
export const MAX_COUNT: number = 3;
const secretLocal = 1;
`
	sigs := ExtractSignatures(code, "ts")
	names := make([]string, 0, len(sigs))
	for _, s := range sigs {
		names = append(names, s.Kind+":"+s.Name)
	}
	want := []string{"fn:fetchUser", "class:Repo", "interface:Shape", "type:Id", "const:MAX_COUNT"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("signatures = %v, want %v", names, want)
	}
	if !sigs[0].IsAsync || !sigs[0].IsExported {
		t.Errorf("fetchUser flags = async:%v exported:%v, want true/true", sigs[0].IsAsync, sigs[0].IsExported)
	}
	if sigs[4].ReturnType != "number" || !sigs[4].IsExported {
		t.Errorf("MAX_COUNT = %+v, want exported const with type number", sigs[4])
	}
}

func TestExtractGoSignatures(t *testing.T) {
	code := `package main

type Server struct {
	name string
}

type Reader interface {
	Read() error
}

func NewServer() *Server {
	return nil
}

func (s *Server) Run(ctx context.Context) error {
	return nil
}

func helper() {}
`
	sigs := ExtractSignatures(code, "go")
	if len(sigs) != 5 {
		t.Fatalf("got %d signatures, want 5: %+v", len(sigs), sigs)
	}
	if sigs[0].Kind != "struct" || sigs[0].Name != "Server" || !sigs[0].IsExported {
		t.Errorf("sig[0] = %+v, want exported struct Server", sigs[0])
	}
	if sigs[1].Kind != "interface" || sigs[1].Name != "Reader" {
		t.Errorf("sig[1] = %+v, want interface Reader", sigs[1])
	}
	// The Rust reference keeps the space before "{" in the captured return
	// type ("*Server "), so the port does too.
	if sigs[2].Kind != "fn" || sigs[2].Name != "NewServer" || sigs[2].ReturnType != "*Server " {
		t.Errorf("sig[2] = %+v, want fn NewServer -> \"*Server \"", sigs[2])
	}
	if sigs[3].Kind != "method" || sigs[3].Name != "Run" || sigs[3].Indent != 2 {
		t.Errorf("sig[3] = %+v, want method Run with indent 2", sigs[3])
	}
	if sigs[3].ReturnType != "error " {
		t.Errorf("sig[3].ReturnType = %q, want \"error \"", sigs[3].ReturnType)
	}
	if sigs[4].IsExported {
		t.Errorf("sig[4] = %+v, want unexported helper", sigs[4])
	}
}

func TestExtractPythonSignatures(t *testing.T) {
	code := `class Engine:
    def __init__(self):
        pass

    async def run(self, task: str) -> bool:
        return True

def _private_helper(x: int) -> None:
    pass
`
	sigs := ExtractSignatures(code, "py")
	if len(sigs) != 4 {
		t.Fatalf("got %d signatures, want 4: %+v", len(sigs), sigs)
	}
	if sigs[0].Kind != "class" || sigs[0].Name != "Engine" || !sigs[0].IsExported {
		t.Errorf("sig[0] = %+v, want exported class Engine", sigs[0])
	}
	if sigs[1].Kind != "method" || sigs[1].Name != "__init__" || sigs[1].IsExported {
		t.Errorf("sig[1] = %+v, want private method __init__", sigs[1])
	}
	if sigs[2].Kind != "method" || !sigs[2].IsAsync || sigs[2].ReturnType != "bool" {
		t.Errorf("sig[2] = %+v, want async method run -> bool", sigs[2])
	}
	if sigs[3].IsExported {
		t.Errorf("sig[3] = %+v, want private _private_helper", sigs[3])
	}
}

func TestExtractGenericSignatures(t *testing.T) {
	// "module" is one of the generic class keywords in the reference.
	code := `# a comment
module Widget
  public class Thing {
  }
  private fn build(x) {
  }
`
	sigs := ExtractSignatures(code, "rb")
	if len(sigs) != 3 {
		t.Fatalf("got %d signatures, want 3: %+v", len(sigs), sigs)
	}
	if sigs[0].Kind != "type" || sigs[0].Name != "Widget" {
		t.Errorf("sig[0] = %+v, want type Widget", sigs[0])
	}
	if sigs[1].Kind != "type" || sigs[1].Name != "Thing" {
		t.Errorf("sig[1] = %+v, want type Thing", sigs[1])
	}
	if sigs[2].Kind != "fn" || sigs[2].Name != "build" {
		t.Errorf("sig[2] = %+v, want fn build", sigs[2])
	}
}

func TestSignatureToCompact(t *testing.T) {
	tests := []struct {
		sig  Signature
		want string
	}{
		{Signature{Kind: "fn", Name: "login", Params: "user:s", ReturnType: "R", IsAsync: true, IsExported: true}, "fn async ⊛ login(user:s) → R"},
		{Signature{Kind: "method", Name: "run", Params: "", Indent: 2}, "  fn run()"},
		{Signature{Kind: "class", Name: "Repo", IsExported: true}, "cl ⊛ Repo"},
		{Signature{Kind: "struct", Name: "User"}, "cl User"},
		{Signature{Kind: "interface", Name: "Shape"}, "if Shape"},
		{Signature{Kind: "trait", Name: "Reader"}, "if Reader"},
		{Signature{Kind: "type", Name: "Id"}, "ty Id"},
		{Signature{Kind: "enum", Name: "Color"}, "en Color"},
		{Signature{Kind: "const", Name: "MAX", ReturnType: "number"}, "val MAX:number"},
	}
	for _, tt := range tests {
		if got := tt.sig.ToCompact(); got != tt.want {
			t.Errorf("ToCompact(%+v) = %q, want %q", tt.sig, got, tt.want)
		}
	}
}

func TestSignatureToTDD(t *testing.T) {
	tests := []struct {
		sig  Signature
		want string
	}{
		{Signature{Kind: "fn", Name: "run", IsExported: true}, "λ+run()"},
		{Signature{Kind: "fn", Name: "hidden"}, "λ-hidden()"},
		{Signature{Kind: "method", Name: "step", IsExported: true, Indent: 2}, " λ+step()"},
		{Signature{Kind: "class", Name: "Repo", IsExported: true}, "§+Repo"},
		{Signature{Kind: "interface", Name: "Shape"}, "∂-Shape"},
		{Signature{Kind: "type", Name: "Id"}, "τ-Id"},
		{Signature{Kind: "enum", Name: "Color"}, "ε-Color"},
		{Signature{Kind: "const", Name: "MAX", ReturnType: "number"}, "ν-MAX:n"},
	}
	for _, tt := range tests {
		if got := tt.sig.ToTDD(); got != tt.want {
			t.Errorf("ToTDD(%+v) = %q, want %q", tt.sig, got, tt.want)
		}
	}
}

func TestCompactParamsAndTypes(t *testing.T) {
	params := compactParams("user: String, count: usize, flag: boolean, m: HashMap<String, u32>, bare")
	want := "user:s, count:n, flag:b, m:HashMap<String, u32>, bare"
	if params != want {
		t.Errorf("compactParams = %q, want %q", params, want)
	}
	if got := compactParams(""); got != "" {
		t.Errorf("compactParams(\"\") = %q, want empty", got)
	}

	tests := []struct{ in, want string }{
		{"String", "s"},
		{"bool", "b"},
		{"i64", "n"},
		{"void", "∅"},
		{"()", "∅"},
		{"Vec<String>", "[s]"},
		{"Array<number>", "[n]"},
		{"Option<String>", "?s"},
		{"Maybe<bool>", "?b"},
		{"Result<(), Error>", "R"},
		{"impl Display", "Display"},
		// trim_start_matches removes every repeated prefix, so the outer
		// Vec< still collapses to a single element type.
		{"Vec<Vec<String>>", "[s]"},
		{"Option<Vec<String>>", "?[s]"},
		{"CustomType", "CustomType"},
	}
	for _, tt := range tests {
		if got := compactType(tt.in); got != tt.want {
			t.Errorf("compactType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTDDParams(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"user: String", "user:s"},
		{"&self", "&self"},
		{"self", "⊕"},
		{"name: &str, count: usize", "name:s,count:n"},
		{"item: &Item", "item:&Item"},
		{"x int", "x int"},
	}
	for _, tt := range tests {
		if got := tddParams(tt.in); got != tt.want {
			t.Errorf("tddParams(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
