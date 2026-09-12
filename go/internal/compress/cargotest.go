package compress

import (
	"fmt"
	"strings"
)

// TestStatus is a test outcome (Rust cargo_test::TestStatus).
type TestStatus uint8

const (
	// TestPassed: the test passed.
	TestPassed TestStatus = iota
	// TestFailed: the test failed.
	TestFailed
	// TestIgnored: the test was ignored.
	TestIgnored
)

// TestResult is one parsed test line (Rust cargo_test::TestResult).
type TestResult struct {
	Name       string
	Status     TestStatus
	DurationMs *uint64
}

// CargoTestCompressor summarizes cargo test output (Rust
// cargo_test::CargoTestCompressor).
type CargoTestCompressor struct {
	maxFailures int
}

// NewCargoTestCompressor builds a compressor capping failures at 10 (Rust
// CargoTestCompressor::new).
func NewCargoTestCompressor() *CargoTestCompressor {
	return &CargoTestCompressor{maxFailures: 10}
}

// NewCargoTestCompressorWithMaxFailures caps reported failures at max.
func NewCargoTestCompressorWithMaxFailures(max int) *CargoTestCompressor {
	return &CargoTestCompressor{maxFailures: max}
}

// Compress reduces cargo test output to a summary (Rust
// CargoTestCompressor::compress).
func (c *CargoTestCompressor) Compress(output string) string {
	var result []string
	passedCount := 0
	ignoredCount := 0
	var failedTests []string
	inFailures := false

	for _, raw := range splitLines(output) {
		line := strings.TrimSpace(raw)

		if strings.HasPrefix(line, "test result:") {
			if passed, _, ignored, ok := c.parseTestResult(line); ok {
				passedCount = passed
				ignoredCount = ignored
			}
			continue
		}

		if strings.HasPrefix(line, "failures:") {
			inFailures = true
			continue
		}

		if inFailures {
			if line == "" {
				inFailures = false
				continue
			}
			if len(failedTests) < c.maxFailures {
				if name, ok := c.extractFailedTest(line); ok {
					failedTests = append(failedTests, name)
				}
			}
			continue
		}

		if c.isNoiseLine(line) {
			continue
		}

		if strings.HasPrefix(line, "running ") || strings.HasPrefix(line, "test ") {
			if strings.Contains(line, " ... ") {
				if test, ok := c.parseTestLine(line); ok {
					switch test.Status {
					case TestPassed:
						passedCount++
					case TestFailed:
						if len(failedTests) < c.maxFailures {
							failedTests = append(failedTests, test.Name)
						}
					case TestIgnored:
						ignoredCount++
					}
				}
			}
			continue
		}
	}

	result = append(result, "[TEST SUMMARY]")

	if len(failedTests) == 0 {
		result = append(result, fmt.Sprintf("ok. %d passed; %d ignored", passedCount, ignoredCount))
	} else {
		result = append(result, fmt.Sprintf("FAIL: %d/%d failed", len(failedTests), passedCount+len(failedTests)))
		result = append(result, "Failures:")
		for i, test := range failedTests {
			if i >= c.maxFailures {
				result = append(result, fmt.Sprintf("  ... and %d more", len(failedTests)-c.maxFailures))
				break
			}
			result = append(result, "  - "+test)
		}
	}

	return strings.Join(result, "\n")
}

// parseTestResult parses "N passed; M failed; K ignored" (Rust
// CargoTestCompressor::parse_test_result).
func (c *CargoTestCompressor) parseTestResult(line string) (passed, failed, ignored int, ok bool) {
	for _, part := range strings.Split(line, ";") {
		part = strings.TrimSpace(part)
		switch {
		case strings.Contains(part, "passed"):
			if n, ok := extractNumber(part); ok {
				passed = n
			}
		case strings.Contains(part, "failed"):
			if n, ok := extractNumber(part); ok {
				failed = n
			}
		case strings.Contains(part, "ignored"):
			if n, ok := extractNumber(part); ok {
				ignored = n
			}
		}
	}
	return passed, failed, ignored, passed > 0 || failed > 0 || ignored > 0
}

// parseTestLine parses "test NAME ... ok|FAILED|ignored" (Rust
// CargoTestCompressor::parse_test_line).
func (c *CargoTestCompressor) parseTestLine(line string) (TestResult, bool) {
	parts := strings.Split(line, " ... ")
	if len(parts) != 2 {
		return TestResult{}, false
	}
	name := strings.TrimSpace(parts[0])
	if !strings.HasPrefix(name, "test ") {
		return TestResult{}, false
	}
	name = strings.TrimPrefix(name, "test ")

	statusStr := strings.TrimSpace(parts[1])
	var status TestStatus
	switch {
	case strings.HasPrefix(statusStr, "ok"):
		status = TestPassed
	case strings.HasPrefix(statusStr, "FAILED"):
		status = TestFailed
	case strings.HasPrefix(statusStr, "ignored"):
		status = TestIgnored
	default:
		return TestResult{}, false
	}
	return TestResult{Name: name, Status: status}, true
}

// extractFailedTest parses an entry of the "failures:" section (Rust
// CargoTestCompressor::extract_failed_test).
func (c *CargoTestCompressor) extractFailedTest(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "test ") {
		return "", false
	}
	return strings.TrimPrefix(line, "test "), true
}

// isNoiseLine reports build chatter that never belongs in a test summary
// (Rust CargoTestCompressor::is_noise_line).
func (c *CargoTestCompressor) isNoiseLine(line string) bool {
	return strings.HasPrefix(line, "Compiling") ||
		strings.HasPrefix(line, "Finished") ||
		strings.HasPrefix(line, "Running") ||
		strings.Contains(line, "warning: unused") ||
		strings.Contains(line, "warning: field ") ||
		strings.Contains(line, "warning: method ") ||
		strings.HasPrefix(line, "   Compiling") ||
		strings.HasPrefix(line, "   Finished") ||
		strings.Contains(line, "note: ") ||
		strings.HasPrefix(line, "     Running") ||
		line == ""
}

// extractNumber parses the digits embedded in a summary fragment (Rust
// CargoTestCompressor::extract_number).
func extractNumber(part string) (int, bool) {
	var digits strings.Builder
	for _, c := range part {
		if c >= '0' && c <= '9' {
			digits.WriteRune(c)
		}
	}
	if digits.Len() == 0 {
		return 0, false
	}
	n := 0
	for _, c := range digits.String() {
		n = n*10 + int(c-'0')
	}
	return n, true
}

// EstimateSavings reports the token delta between two strings (Rust
// CargoTestCompressor::estimate_savings).
func (c *CargoTestCompressor) EstimateSavings(original, compressed string) float64 {
	originalTokens := len(original) / 4
	compressedTokens := len(compressed) / 4
	if originalTokens == 0 {
		return 0.0
	}
	return float64(originalTokens-compressedTokens) / float64(originalTokens) * 100.0
}
