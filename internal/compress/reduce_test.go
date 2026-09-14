package compress

import (
	"strings"
	"testing"
)

func TestReduceCommandOutput(t *testing.T) {
	c := New()
	output := `diff --git a/src/lib.rs b/src/lib.rs
index 1234567..abcdefg 100644
--- a/src/lib.rs
+++ b/src/lib.rs
@@ -10,7 +10,7 @@
 fn main() {
-    println!("old");
+    println!("new");
 }`
	res, err := c.Reduce(Request{Cmd: "git diff", Output: output, Mode: ModeFull})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !strings.Contains(res.Content, "[GIT DIFF SUMMARY]") {
		t.Errorf("content = %q, want the diff summary", res.Content)
	}
	if res.TotalTokens == 0 || res.Tokens == 0 {
		t.Errorf("token accounting missing: %+v", res)
	}
	if res.OutputLines == 0 || res.TotalLines == 0 {
		t.Errorf("line accounting missing: %+v", res)
	}
}

func TestReduceReaderPath(t *testing.T) {
	path := writeTemp(t, "reduce.rs", "pub fn execute() {}\nfn helper() {}\n")
	c := New()
	res, err := c.Reduce(Request{Path: path, Mode: ModeSignatures})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if res.Mode != ModeSignatures || !strings.Contains(res.Content, "λ+execute()") {
		t.Errorf("result = %+v, want signature output", res)
	}
	// Signature mode carries a small header, so tiny files may expand; the
	// reference then reports zero savings instead of a negative number.
	if res.TotalTokens != EstimateTokens("pub fn execute() {}\nfn helper() {}\n") {
		t.Errorf("total_tokens = %d, want the original token count", res.TotalTokens)
	}
	if res.Tokens > res.TotalTokens && res.SavingsPercent != 0 {
		t.Errorf("savings = %v, want 0 when the mode expands the file", res.SavingsPercent)
	}
}

func TestReduceResponsePath(t *testing.T) {
	c := New()
	res, err := c.Reduce(Request{
		Tool:     "search_code",
		Response: map[string]any{"elements": elementSlice(30)},
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if res.Response["total_matches"] != 30 {
		t.Errorf("shaped response = %+v, want total_matches 30", res.Response)
	}
	if res.Stats.OriginalTokens <= res.Stats.CompressedTokens {
		t.Errorf("stats = %+v, want a real saving", res.Stats)
	}
}

func TestReduceUnknownToolPassesThrough(t *testing.T) {
	c := New()
	response := map[string]any{"custom": "payload"}
	res, err := c.Reduce(Request{Tool: "not_a_tool", Response: response})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if res.Response["custom"] != "payload" {
		t.Errorf("unknown tool should pass through, got %+v", res.Response)
	}
}

func TestReduceEmptyRequest(t *testing.T) {
	c := New()
	if _, err := c.Reduce(Request{}); err == nil {
		t.Fatal("empty request must be rejected")
	}
}

func TestReduceMissingFile(t *testing.T) {
	c := New()
	if _, err := c.Reduce(Request{Path: "/nonexistent/leankg/nope.go"}); err == nil {
		t.Fatal("missing file must surface an error")
	}
}

func TestReduceReaderCacheIsShared(t *testing.T) {
	path := writeTemp(t, "shared.rs", "pub fn run() {}\n")
	c := New()
	if _, err := c.Reduce(Request{Path: path, Mode: ModeSignatures}); err != nil {
		t.Fatalf("first Reduce: %v", err)
	}
	second, err := c.Reduce(Request{Path: path, Mode: ModeSignatures})
	if err != nil {
		t.Fatalf("second Reduce: %v", err)
	}
	if !second.IsCached {
		t.Error("the compressor's reader cache should persist across Reduce calls")
	}
	if _, ok := c.SessionCache().Get(path); !ok {
		t.Error("SessionCache() should expose the shared cache")
	}
}

func TestLeanKGCompressorEstimateSavings(t *testing.T) {
	c := New()
	if got := c.EstimateSavings(strings.Repeat("x", 1000), strings.Repeat("x", 100)); got < 89.9 || got > 90.1 {
		t.Errorf("EstimateSavings = %v, want ~90", got)
	}
	if got := c.EstimateSavings("", ""); got != 0 {
		t.Errorf("EstimateSavings(empty) = %v, want 0", got)
	}
}
