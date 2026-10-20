package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name string, size int) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineFileReadSumsDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", 400) // 100 tokens
	writeFile(t, dir, "sub/b.go", 800)
	// a.go named twice must be counted once.
	tokens, method := Baseline(dir, "query", OutcomeOK, []string{"a.go", "sub/b.go", "a.go"})
	if method != BaselineFileRead || tokens != 300 {
		t.Fatalf("Baseline = (%d, %q), want (300, file_read)", tokens, method)
	}
}

func TestBaselineOutsideProjectIsNotCounted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", 400)
	outside := t.TempDir()
	writeFile(t, outside, "secret.go", 4000)
	tokens, method := Baseline(dir, "query", OutcomeOK,
		[]string{"a.go", filepath.Join(outside, "secret.go"), "../" + filepath.Base(outside) + "/secret.go"})
	if method != BaselineFileRead || tokens != 100 {
		t.Fatalf("Baseline = (%d, %q), want (100, file_read): files outside projectDir must not count", tokens, method)
	}
}

func TestBaselineNoneForNonSavingCalls(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", 400)
	files := []string{"a.go"}
	cases := []struct {
		name, tool, outcome string
	}{
		{"status", "status", OutcomeOK},
		{"import", "import", OutcomeOK},
		{"zero_hit", "query", OutcomeZeroHit},
		{"cold", "query", OutcomeCold},
		{"refused", "query", OutcomeRefused},
		{"timeout", "query", OutcomeTimeout},
		{"error", "query", OutcomeErrorPrefix + "LEANKG_ERROR_UNKNOWN_ACTION"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens, method := Baseline(dir, tc.tool, tc.outcome, files)
			if tokens != 0 || method != BaselineNone {
				t.Fatalf("Baseline = (%d, %q), want (0, none)", tokens, method)
			}
		})
	}
}

func TestBaselineNoneWithoutReadableFiles(t *testing.T) {
	dir := t.TempDir()
	tokens, method := Baseline(dir, "query", OutcomeOK, []string{"missing.go"})
	if tokens != 0 || method != BaselineNone {
		t.Fatalf("Baseline = (%d, %q), want (0, none) for files that are not on disk", tokens, method)
	}
	if tokens, method := Baseline(dir, "query", OutcomeOK, nil); tokens != 0 || method != BaselineNone {
		t.Fatalf("Baseline with no hit files = (%d, %q), want (0, none)", tokens, method)
	}
}

func TestSavedClampsAtZero(t *testing.T) {
	if got := Saved(100, 30); got != 70 {
		t.Errorf("Saved(100,30) = %d, want 70", got)
	}
	if got := Saved(30, 100); got != 0 {
		t.Errorf("Saved(30,100) = %d, want 0 (negative delta clamps)", got)
	}
	if got := Saved(0, 0); got != 0 {
		t.Errorf("Saved(0,0) = %d, want 0", got)
	}
}

// An error or zero-hit call has baseline 0, so it can never report savings.
func TestErrorCallsHaveNoSavings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", 4000)
	base, _ := Baseline(dir, "query", OutcomeErrorPrefix+"FIXTURE", []string{"a.go"})
	if got := Saved(base, 10); got != 0 {
		t.Fatalf("error call saved %d tokens, want 0", got)
	}
}
