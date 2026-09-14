package compress

import (
	"math"
	"strings"
	"testing"
)

func TestGitDiffCompressEmpty(t *testing.T) {
	c := NewGitDiffCompressor()
	result := c.Compress("")
	if !strings.Contains(result, "[GIT DIFF SUMMARY]") {
		t.Errorf("result = %q, want the diff summary header", result)
	}
	if !strings.Contains(result, "0 file(s) changed") {
		t.Errorf("result = %q, want zero files", result)
	}
}

func TestGitDiffCompress(t *testing.T) {
	c := NewGitDiffCompressor()
	output := `diff --git a/src/lib.rs b/src/lib.rs
index 1234567..abcdefg 100644
--- a/src/lib.rs
+++ b/src/lib.rs
@@ -10,7 +10,7 @@
 fn main() {
-    println!("old");
+    println!("new");
 }`
	result := c.Compress(output)
	if !strings.Contains(result, "[GIT DIFF SUMMARY]") {
		t.Errorf("result = %q, want the diff summary header", result)
	}
	if !strings.Contains(result, "1 file(s) changed") {
		t.Errorf("result = %q, want 1 changed file", result)
	}
	if !strings.Contains(result, "+1 insertions, -1 deletions") {
		t.Errorf("result = %q, want +1/-1 counts", result)
	}
}

func TestGitDiffCompressStat(t *testing.T) {
	c := NewGitDiffCompressor()
	output := ` src/lib.rs | 5 +++--
 src/main.rs | 2 ++
 2 files changed, 3 insertions(+), 2 deletions(-)`
	result := c.CompressStatOnly(output)
	if !strings.Contains(result, "2 file(s) changed") {
		t.Errorf("result = %q, want 2 files", result)
	}
	if !strings.Contains(result, "src/lib.rs") || !strings.Contains(result, "src/main.rs") {
		t.Errorf("result = %q, want both file names", result)
	}
}

func TestGitDiffCompressStatNoChanges(t *testing.T) {
	c := NewGitDiffCompressor()
	if got := c.CompressStatOnly("nothing here"); got != "[GIT DIFF] No changes" {
		t.Errorf("CompressStatOnly = %q, want the no-changes marker", got)
	}
}

func TestGitDiffCompressStatTruncates(t *testing.T) {
	c := NewGitDiffCompressor()
	var b strings.Builder
	for i := 0; i < 25; i++ {
		b.WriteString(" src/file" + string(rune('a'+i%26)) + ".rs | 2 +-\n")
	}
	result := c.CompressStatOnly(b.String())
	if !strings.Contains(result, "25 file(s) changed") {
		t.Errorf("result should count all 25 files:\n%s", result)
	}
	if !strings.Contains(result, "... and 5 more") {
		t.Errorf("result should truncate past 20 files:\n%s", result)
	}
}

func TestGitDiffEstimateSavings(t *testing.T) {
	c := NewGitDiffCompressor()
	original := strings.Repeat("x", 1000)
	compressed := strings.Repeat("x", 100)
	if got := c.EstimateSavings(original, compressed); math.Abs(got-90.0) > 0.1 {
		t.Errorf("EstimateSavings = %v, want ~90", got)
	}
	if got := c.EstimateSavings("", ""); got != 0 {
		t.Errorf("EstimateSavings(empty) = %v, want 0", got)
	}
}

func TestLeanKGCompressorRouting(t *testing.T) {
	c := New()
	cargoOut := "running 2 tests\ntest test_one ... ok\ntest test_two ... ok\ntest result: ok. 2 passed; 0 ignored"
	if got := c.Compress("cargo test", cargoOut); !strings.Contains(got, "ok. 2 passed") {
		t.Errorf("cargo test routing failed: %q", got)
	}
	if got := c.Compress("cargo test --no-run", cargoOut); strings.Contains(got, "[TEST SUMMARY]") {
		t.Errorf("--no-run must not use the test compressor: %q", got)
	}

	diffOut := "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
	if got := c.Compress("git diff", diffOut); !strings.Contains(got, "[GIT DIFF SUMMARY]") {
		t.Errorf("git diff routing failed: %q", got)
	}
	statOut := " src/lib.rs | 5 +++--\n 1 file changed, 3 insertions(+), 2 deletions(-)"
	if got := c.Compress("git diff --stat", statOut); !strings.Contains(got, "1 file(s) changed") {
		t.Errorf("git diff --stat routing failed: %q", got)
	}
}
