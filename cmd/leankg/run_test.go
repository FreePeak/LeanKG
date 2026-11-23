package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/cliexit"
	"github.com/FreePeak/LeanKG/internal/compress"
)

// TestRunCommandCapturesStdout pins the happy path: argv execution, stdout
// captured verbatim, no exit code.
func TestRunCommandCapturesStdout(t *testing.T) {
	out, code, err := runCommand(context.Background(), []string{"echo", "hello"}, false)
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

	raw, code, err := runCommand(context.Background(), argv, false)
	if err != nil || code != 0 {
		t.Fatalf("raw run: code=%d err=%v", code, err)
	}
	compressed, code, err := runCommand(context.Background(), argv, true)
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
	out, code, err := runCommand(context.Background(), []string{"sh", "-c", "echo partial; echo boom >&2; exit 3"}, false)
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
	_, code, err := runCommand(context.Background(), []string{"leankg-no-such-binary-1f2e3d"}, false)
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

// TestRunCommandCancelStopsChild pins the cancellation contract: a cancelled
// ctx kills the child and the run reports context.Canceled (exit 130 through
// cliexit.Finish), instead of waiting for the child to finish.
func TestRunCommandCancelStopsChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(200*time.Millisecond, cancel) // the SIGINT/SIGTERM path
	start := time.Now()
	_, _, err := runCommand(ctx, []string{"sleep", "5"}, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run error = %v, want one wrapping context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancelled run took %v, the child was not stopped", elapsed)
	}
}

// TestRunVerbCancelExitsWith130 pins the exit status a cancelled `run` reports
// through the same cliexit.Finish main uses.
func TestRunVerbCancelExitsWith130(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cmdRun(ctx, []string{"--", "sleep", "5"})
	if got := cliexit.Finish(err, io.Discard); got != 130 {
		t.Fatalf("cancelled run exit = %d, want 130 (err: %v)", got, err)
	}
}

// TestRunCommandCancelReturnsWhenGrandchildHoldsStdout pins the bound on a
// cancel: the child's grandchild inherits stdout, so the pipe stays open after
// the child is killed. The run must still return promptly instead of waiting
// for the grandchild to finish.
func TestRunCommandCancelReturnsWhenGrandchildHoldsStdout(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	t.Cleanup(func() { killPidFile(t, pidFile) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	argv := []string{"sh", "-c", `sleep 30 & echo $! >"$1"; wait`, "sh", pidFile}
	done := make(chan error, 1)
	go func() {
		_, _, err := runCommand(ctx, argv, false)
		done <- err
	}()
	if !waitForFile(pidFile, 5*time.Second) {
		t.Fatal("the grandchild never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled run error = %v, want one wrapping context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled run still waits on the grandchild that holds stdout")
	}
}

// TestRunCommandChildExitWithGrandchildHoldingStdout pins the success path under
// the same bound: the child exits 0 while a grandchild still holds stdout. The
// captured stdout is returned as a success, not reported as a failed execution.
func TestRunCommandChildExitWithGrandchildHoldingStdout(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	t.Cleanup(func() { killPidFile(t, pidFile) })
	argv := []string{"sh", "-c", `sleep 30 & echo $! >"$1"; echo ok`, "sh", pidFile}
	type result struct {
		out  string
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		out, code, err := runCommand(context.Background(), argv, false)
		done <- result{out, code, err}
	}()
	select {
	case r := <-done:
		if r.err != nil || r.code != 0 {
			t.Fatalf("exit 0 child: code=%d err=%v, want a success", r.code, r.err)
		}
		if !strings.HasPrefix(r.out, "ok\n") {
			t.Fatalf("stdout = %q, want the child output", r.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run still waits on the grandchild after the child exited")
	}
}

// killPidFile kills the process whose pid a test wrote to path, if it started.
func killPidFile(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// waitForFile polls until path exists or the timeout passes.
func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// A child that dies of SIGINT was interrupted: it maps to the cancelled status
// 130, not to a plain failure of 1.
func TestRunCommandSigintKilledChildExits130(t *testing.T) {
	_, code, err := runCommand(context.Background(), []string{"sh", "-c", "kill -INT $$"}, false)
	if err == nil {
		t.Fatal("a signalled child must be an error")
	}
	if code != 130 {
		t.Fatalf("exit code = %d, want 130 for a SIGINT-killed child (err: %v)", code, err)
	}
}
