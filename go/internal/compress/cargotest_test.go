package compress

import (
	"math"
	"strings"
	"testing"
)

func TestCargoTestCompressEmpty(t *testing.T) {
	c := NewCargoTestCompressor()
	if got := c.Compress(""); !strings.Contains(got, "[TEST SUMMARY]") {
		t.Errorf("Compress(\"\") = %q, want the summary header", got)
	}
}

func TestCargoTestCompressPassed(t *testing.T) {
	c := NewCargoTestCompressor()
	output := `running 2 tests
test test_one ... ok
test test_two ... ok
test result: ok. 2 passed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s`
	result := c.Compress(output)
	if !strings.Contains(result, "ok. 2 passed") {
		t.Errorf("result = %q, want ok. 2 passed", result)
	}
	if strings.Contains(result, "FAIL") {
		t.Errorf("result = %q, want no failure line", result)
	}
}

func TestCargoTestCompressFailed(t *testing.T) {
	c := NewCargoTestCompressor()
	output := `running 3 tests
test test_one ... FAILED
test test_two ... ok
test test_three ... ok
test result: FAILED. 2 passed; 1 failed; 0 ignored`
	result := c.Compress(output)
	if !strings.Contains(result, "FAIL:") {
		t.Errorf("result = %q, want a failure summary", result)
	}
	if !strings.Contains(result, "test_one") {
		t.Errorf("result = %q, want the failing test name", result)
	}
}

func TestCargoTestCompressFailuresSection(t *testing.T) {
	c := NewCargoTestCompressor()
	output := `running 2 tests
test a ... FAILED
test b ... FAILED

failures:

---- a stdout ----
thread 'a' panicked

failures:
    a
    b

test result: FAILED. 0 passed; 2 failed; 0 ignored`
	result := c.Compress(output)
	if !strings.Contains(result, "- a") || !strings.Contains(result, "- b") {
		t.Errorf("result = %q, want both failures from the failures section", result)
	}
	if strings.Contains(result, "panicked") {
		t.Errorf("result = %q, want panic noise stripped", result)
	}
}

func TestCargoTestStripsNoise(t *testing.T) {
	c := NewCargoTestCompressor()
	output := `Compiling leankg v0.8.3
   Compiling leankg v0.8.3
warning: unused variable: ` + "`foo`" + `
  --> src/lib.rs:10:5
Finished dev [unoptimized] target(s)
running 1 test
test test_one ... ok
test result: ok. 1 passed; 0 ignored`
	result := c.Compress(output)
	for _, noise := range []string{"Compiling", "Finished", "warning:"} {
		if strings.Contains(result, noise) {
			t.Errorf("result should not contain %q:\n%s", noise, result)
		}
	}
}

func TestCargoTestMaxFailures(t *testing.T) {
	c := NewCargoTestCompressorWithMaxFailures(2)
	output := `running 5 tests
test a ... FAILED
test b ... FAILED
test c ... FAILED
test d ... FAILED
test e ... ok
test result: FAILED. 1 passed; 4 failed; 0 ignored`
	result := c.Compress(output)
	if got := strings.Count(result, "  - "); got != 2 {
		t.Errorf("expected 2 listed failures, got %d:\n%s", got, result)
	}
}

func TestCargoTestEstimateSavings(t *testing.T) {
	c := NewCargoTestCompressor()
	if got := c.EstimateSavings(strings.Repeat("x", 1000), strings.Repeat("x", 100)); math.Abs(got-90.0) > 0.1 {
		t.Errorf("EstimateSavings = %v, want ~90", got)
	}
}

func TestParseTestLine(t *testing.T) {
	c := NewCargoTestCompressor()
	tests := []struct {
		line   string
		name   string
		status TestStatus
		ok     bool
	}{
		{"test foo ... ok", "foo", TestPassed, true},
		{"test foo ... FAILED", "foo", TestFailed, true},
		{"test foo ... ignored", "foo", TestIgnored, true},
		{"test foo", "", 0, false},
		{"running 1 test", "", 0, false},
		{"test foo ... unknown", "", 0, false},
	}
	for _, tt := range tests {
		got, ok := c.parseTestLine(tt.line)
		if ok != tt.ok {
			t.Errorf("parseTestLine(%q) ok = %v, want %v", tt.line, ok, tt.ok)
			continue
		}
		if ok && (got.Name != tt.name || got.Status != tt.status) {
			t.Errorf("parseTestLine(%q) = %+v, want %s/%v", tt.line, got, tt.name, tt.status)
		}
	}
}

func TestParseTestResult(t *testing.T) {
	c := NewCargoTestCompressor()
	passed, failed, ignored, ok := c.parseTestResult("test result: FAILED. 2 passed; 1 failed; 3 ignored")
	if !ok || passed != 2 || failed != 1 || ignored != 3 {
		t.Errorf("parseTestResult = %d/%d/%d/%v, want 2/1/3/true", passed, failed, ignored, ok)
	}
	if _, _, _, ok := c.parseTestResult("no numbers here"); ok {
		t.Error("parseTestResult without counts should report false")
	}
}
