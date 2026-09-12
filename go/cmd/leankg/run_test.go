package main

import (
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/compress"
)

// TestRunCommandCapturesStdout pins the happy path: argv execution, stdout
// captured verbatim, no exit code.
func TestRunCommandCapturesStdout(t *testing.T) {
	out, code, err := runCommand([]string{"echo", "hello"}, false)
	if err != nil {
		t.Fatalf("runCommand: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if out != "hello\n" {
		t.Fatalf("stdout = %q, want %q", out, "hello\n")
	}
}

// TestRunCommandCompressesStdout pins the --compress wiring: the same argv
// string the Rust runner built ("program args...") is the compressor's
// dispatch key, and the compressed body is genuinely shorter.
func TestRunCommandCompressesStdout(t *testing.T) {
	script := `for i in $(seq 1 80); do echo "line $i"; done`
	argv := []string{"sh", "-c", script}

	raw, code, err := runCommand(argv, false)
	if err != nil || code != 0 {
		t.Fatalf("raw run: code=%d err=%v", code, err)
	}
	compressed, code, err := runCommand(argv, true)
	if err != nil || code != 0 {
		t.Fatalf("compressed run: code=%d err=%v", code, err)
	}
	if want := compress.New().Compress(strings.Join(argv, " "), raw); compressed != want {
		t.Fatalf("compressed stdout = %q, want the compressor output %q", compressed, want)
	}
	if len(compressed) >= len(raw) {
		t.Fatalf("compression did not shrink output: raw=%d compressed=%d", len(raw), len(compressed))
	}
}

// TestRunCommandFailureCarriesExitCodeAndStderr is the acceptance guard: a
// non-zero child exit must surface as an error naming the exit code, with the
// child's stderr attached and stdout still captured.
func TestRunCommandFailureCarriesExitCodeAndStderr(t *testing.T) {
	out, code, err := runCommand([]string{"sh", "-c", "echo partial; echo boom >&2; exit 3"}, false)
	if err == nil {
		t.Fatal("non-zero child must be an error")
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want the child's 3", code)
	}
	if !strings.Contains(err.Error(), "failed with exit code 3") {
		t.Fatalf("error %q must name the exit code", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %q must carry the child's stderr", err)
	}
	if !strings.Contains(out, "partial") {
		t.Fatalf("stdout %q must still be captured on failure", out)
	}
}

// TestRunCommandMissingBinary covers the never-started case (Rust's
// "Failed to execute" path).
func TestRunCommandMissingBinary(t *testing.T) {
	_, code, err := runCommand([]string{"leankg-no-such-binary-1f2e3d"}, false)
	if err == nil {
		t.Fatal("missing binary must be an error")
	}
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(err.Error(), "failed to execute") {
		t.Fatalf("error %q must be the execute-failure message", err)
	}
}

// TestRunCLIExitAndStreamSplit drives the real binary: the exit code
// propagates, stdout stays stdout, stderr carries the failure report — and a
// successful command's stderr is dropped (Rust ShellRunner::run parity).
func TestRunCLIExitAndStreamSplit(t *testing.T) {
	stdout, stderr, code := runCLI(t, "run", "--", "sh", "-c", "echo out; echo err >&2; exit 7")
	if code != 7 {
		t.Fatalf("exit = %d, want 7 (stderr: %s)", code, stderr)
	}
	// Rust parity: a failing command reports exit code + stderr and discards
	// the partial stdout it captured.
	if stdout != "" {
		t.Fatalf("stdout on failure = %q, want empty", stdout)
	}
	for _, want := range []string{"err", "failed with exit code 7"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr %q missing %q", stderr, want)
		}
	}

	stdout, stderr, code = runCLI(t, "run", "--", "sh", "-c", "echo ok; echo warn >&2")
	if code != 0 {
		t.Fatalf("success exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "ok\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "ok\n")
	}
	if stderr != "" {
		t.Fatalf("successful command's stderr must not leak: %q", stderr)
	}

	stdout, stderr, code = runCLI(t, "run", "--compress", "--", "echo", "hi")
	if code != 0 || !strings.Contains(stdout, "hi") {
		t.Fatalf("--compress run: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	_, stderr, code = runCLI(t, "run")
	if code != 2 {
		t.Fatalf("missing command exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "no command provided") {
		t.Fatalf("missing command stderr = %q", stderr)
	}
}
