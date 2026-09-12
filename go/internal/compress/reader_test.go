package compress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestReadFullAppliesSymbolMap(t *testing.T) {
	content := strings.Repeat("validateToken(parseToken(encodePayload(value)))\n", 6)
	path := writeTemp(t, "sample.go", content)

	reader := NewFileReader(nil)
	res, err := reader.Read(path, ModeFull, "", false)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Mode != ModeFull {
		t.Errorf("mode = %v, want full", res.Mode)
	}
	if !strings.Contains(res.Content, "[MAP]:") {
		t.Fatalf("expected a symbol map table in full mode output:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "validateToken") {
		t.Errorf("map table should list the original identifiers:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "validateToken(") {
		t.Errorf("identifiers in the body should be replaced by anchors:\n%s", res.Content)
	}
	if res.Tokens >= res.TotalTokens {
		t.Errorf("compression should reduce tokens: %d vs %d", res.Tokens, res.TotalTokens)
	}
	if res.LinesIncluded != 6 || res.TotalLines != 6 {
		t.Errorf("lines = %d/%d, want 6/6", res.LinesIncluded, res.TotalLines)
	}
}

func TestReadFullWithoutMappableIdentifiers(t *testing.T) {
	content := "package x\n\nvar y = 1\n"
	path := writeTemp(t, "tiny.go", content)
	reader := NewFileReader(nil)
	res, err := reader.Read(path, ModeFull, "", false)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Content != content {
		t.Errorf("content = %q, want the file verbatim", res.Content)
	}
}

func TestReadMap(t *testing.T) {
	content := "use std::io;\npub fn main() {}"
	path := writeTemp(t, "test.rs", content)
	reader := NewFileReader(nil)

	res, err := reader.Read(path, ModeMap, "", false)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Mode != ModeMap {
		t.Errorf("mode = %v, want map", res.Mode)
	}
	if !strings.Contains(res.Content, "deps: L1: use std::io;") {
		t.Errorf("map output missing deps line:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "fn main()") {
		t.Errorf("map output missing API signature:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "exports: L2: pub fn main() {}") {
		t.Errorf("map output missing exports line:\n%s", res.Content)
	}
	if res.LinesIncluded != 1 {
		t.Errorf("lines_included = %d, want 1 signature", res.LinesIncluded)
	}
}

func TestReadSignatures(t *testing.T) {
	content := "pub fn execute() {}\nfn helper() {}\npub struct Point { x: i32 }"
	path := writeTemp(t, "test.rs", content)
	reader := NewFileReader(nil)

	res, err := reader.Read(path, ModeSignatures, "", false)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Mode != ModeSignatures {
		t.Errorf("mode = %v, want signatures", res.Mode)
	}
	if res.TotalLines != 3 {
		t.Errorf("total_lines = %d, want 3", res.TotalLines)
	}
	if res.LinesIncluded != 3 {
		t.Errorf("lines_included = %d, want 3", res.LinesIncluded)
	}
	if !strings.Contains(res.Content, "λ+execute()") {
		t.Errorf("signatures output missing λ+execute():\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "§+Point") {
		t.Errorf("signatures output missing §+Point:\n%s", res.Content)
	}
}

func TestReadLines(t *testing.T) {
	content := "l1\nl2\nl3\nl4\nl5\n"
	path := writeTemp(t, "lines.txt", content)
	reader := NewFileReader(nil)

	res, err := reader.Read(path, ModeLines, "2-3,5-5", false)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Content != "l2\nl3\nl5" {
		t.Errorf("content = %q, want the requested ranges", res.Content)
	}
	if res.LinesIncluded != 3 || res.TotalLines != 5 {
		t.Errorf("lines = %d/%d, want 3/5", res.LinesIncluded, res.TotalLines)
	}
}

func TestReadLinesWithoutSpec(t *testing.T) {
	path := writeTemp(t, "lines.txt", "a\nb\n")
	reader := NewFileReader(nil)
	res, err := reader.Read(path, ModeLines, "", false)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Content != "" {
		t.Errorf("content = %q, want empty without a range spec", res.Content)
	}
}

func TestReadAggressive(t *testing.T) {
	content := "// leading comment\nlet x = 1; // trailing\n\n/* block */\n"
	path := writeTemp(t, "agg.go", content)
	reader := NewFileReader(nil)

	res, err := reader.Read(path, ModeAggressive, "", false)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Content != "let x = 1;" {
		t.Errorf("content = %q, want the single stripped code line", res.Content)
	}
}

func TestReadEntropy(t *testing.T) {
	content := "let x = 1;\n\n// a very low entropy string\naaaaaaaaaaaaa\npub fn run() {}"
	path := writeTemp(t, "ent.go", content)
	reader := NewFileReader(nil)

	res, err := reader.Read(path, ModeEntropy, "", false)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Mode != ModeEntropy {
		t.Errorf("mode = %v, want entropy", res.Mode)
	}
	if !strings.Contains(res.Content, "run()") {
		t.Errorf("structural line must survive:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "aaaaaaaaaaaaa") {
		t.Errorf("low entropy line must be filtered:\n%s", res.Content)
	}
}

func TestReadCachePreemption(t *testing.T) {
	content := "pub fn main() {}\n"
	path := writeTemp(t, "cached.rs", content)
	reader := NewFileReader(nil)

	if _, err := reader.Read(path, ModeSignatures, "", false); err != nil {
		t.Fatalf("first Read: %v", err)
	}
	res, err := reader.Read(path, ModeSignatures, "", false)
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if !res.IsCached {
		t.Error("second read should be a cache hit")
	}
	if !strings.Contains(res.Content, "File unchanged in SessionCache") {
		t.Errorf("cached read content = %q, want the cache notice", res.Content)
	}
	if res.SavingsPercent != 99.0 || res.OutputLines != 2 || res.LinesIncluded != -1 {
		t.Errorf("cached result = %+v, want 99%%, 2 lines, no lines_included", res)
	}

	fresh, err := reader.Read(path, ModeSignatures, "", true)
	if err != nil {
		t.Fatalf("fresh Read: %v", err)
	}
	if strings.Contains(fresh.Content, "File unchanged in SessionCache") {
		t.Errorf("fresh=true must bypass the cache notice:\n%s", fresh.Content)
	}
	if !strings.Contains(fresh.Content, "λ+main()") {
		t.Errorf("fresh read should return signatures:\n%s", fresh.Content)
	}
}

func TestReadDiff(t *testing.T) {
	path := writeTemp(t, "diff.rs", "line1\nline2\nline3\n")

	reader := NewFileReader(nil)
	first, err := reader.Read(path, ModeDiff, "", false)
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}
	if !strings.Contains(first.Content, "[New in Cache => Showing Full 3L]") {
		t.Errorf("first diff read = %q, want the full-content notice", first.Content)
	}
	if first.IsCached || first.TotalLines != 3 {
		t.Errorf("first diff result = %+v, want not cached / 3 lines", first)
	}

	if err := os.WriteFile(path, []byte("line1\nCHANGED\nline3\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	second, err := reader.Read(path, ModeDiff, "", false)
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	for _, want := range []string{"[auto-delta]", "@@ -1,3 +1,3 @@", "-line2", "+CHANGED", " line1"} {
		if !strings.Contains(second.Content, want) {
			t.Errorf("delta output missing %q:\n%s", want, second.Content)
		}
	}
	if second.IsCached {
		t.Error("a changed diff read is not a cache hit")
	}
}

func TestReadDiffSkipsPreemptionOnRepeatedReads(t *testing.T) {
	path := writeTemp(t, "same.rs", "a\nb\n")
	reader := NewFileReader(nil)
	for i := 0; i < 3; i++ {
		res, err := reader.Read(path, ModeDiff, "", false)
		if err != nil {
			t.Fatalf("Read %d: %v", i, err)
		}
		if strings.Contains(res.Content, "File unchanged in SessionCache") {
			t.Errorf("diff mode must not use the cache pre-emption notice:\n%s", res.Content)
		}
	}
}

func TestReadAdaptiveIsRejected(t *testing.T) {
	path := writeTemp(t, "adaptive.rs", "fn a() {}\n")
	reader := NewFileReader(nil)
	if _, err := reader.Read(path, ModeAdaptive, "", false); err == nil {
		t.Fatal("Adaptive must be resolved before Read")
	}
}

func TestReadMissingFileInvalidatesCache(t *testing.T) {
	path := writeTemp(t, "gone.rs", "fn a() {}\n")
	reader := NewFileReader(nil)
	if _, err := reader.Read(path, ModeFull, "", false); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, ok := reader.Cache().Get(path); !ok {
		t.Fatal("file should be cached after a successful read")
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := reader.Read(path, ModeFull, "", false); err == nil {
		t.Fatal("reading a missing file must fail")
	}
	if _, ok := reader.Cache().Get(path); ok {
		t.Error("a missing file must be evicted from the session cache")
	}
}

func TestReadAdaptiveResolutionThroughReduce(t *testing.T) {
	path := writeTemp(t, "resolve.go", strings.Repeat("func f() {}\n", 300))
	c := New()
	res, err := c.Reduce(Request{Path: path})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if res.Mode != ModeSignatures {
		t.Errorf("mode = %v, want signatures (>200 lines of code)", res.Mode)
	}
}

func TestFormatReadResult(t *testing.T) {
	res := ReadResult{
		Path:           "/tmp/x/main.rs",
		Mode:           ModeSignatures,
		Content:        "λ+run()",
		Tokens:         5,
		TotalTokens:    100,
		SavingsPercent: 95.0,
		OutputLines:    3,
	}
	got := FormatReadResult(res)
	want := "main.rs [3L] mode=signatures\nλ+run()\n---\noriginal: 100 tokens | sent: 5 tokens (95.0% saved)"
	if got != want {
		t.Errorf("FormatReadResult =\n%q\nwant\n%q", got, want)
	}
}

func TestImportExportHelpers(t *testing.T) {
	for _, line := range []string{"import foo from 'bar'", "use std::collections", "require('./module')", "#include <stdio.h>"} {
		if !isImportLine(line) {
			t.Errorf("isImportLine(%q) = false, want true", line)
		}
	}
	if isImportLine("let x = 1") {
		t.Error("isImportLine(let x = 1) should be false")
	}
	for _, line := range []string{"pub fn main()", "export const x", "module.exports = y"} {
		if !isExportLine(line) {
			t.Errorf("isExportLine(%q) = false, want true", line)
		}
	}
}

func TestRemoveSyntaxNoise(t *testing.T) {
	tests := []struct{ in, want string }{
		{"let x = 1; // trailing comment", "let x = 1;"},
		{"# heading", "# heading"},
		{"## 42 heading", "## 42 heading"},
		{"x = #42", "x ="},
		{`let s = "// not a comment";`, `let s = "// not a comment";`},
		{"    indented code   ", "indented code"},
	}
	for _, tt := range tests {
		if got := removeSyntaxNoise(tt.in); got != tt.want {
			t.Errorf("removeSyntaxNoise(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
