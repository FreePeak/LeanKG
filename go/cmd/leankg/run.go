package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/compress"
)

// cmdRun executes one command and streams its stdout back, optionally
// compressed. Ported from the Rust `leankg run` verb
// (cli::shell_runner::ShellRunner + main.rs run_shell_command):
//
//	leankg run [--compress] -- <command> [args...]
//
// The command runs as an argv vector — never through a shell — so quoting,
// globbing, and redirection belong to the caller.
func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	compressOut := fs.Bool("compress", false, "RTK-style compression of the command's stdout")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "run: no command provided. Usage: leankg run [--compress] -- <command> [args...]")
		os.Exit(2)
	}
	stdout, code, err := runCommand(fs.Args(), *compressOut)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(code)
	}
	_, _ = os.Stdout.WriteString(stdout)
}

// runCommand runs argv[0] with argv[1:] and returns the captured stdout plus
// the process exit code. A non-zero child exit is an error carrying the exit
// code and the child's stderr (the Rust ShellRunner::run message shape); the
// code is the child's own exit status, so `leankg run cargo test` reports
// cargo's code instead of a constant.
//
// stderr is captured for the failure message only: like the Rust runner, a
// successful command contributes its stdout alone.
func runCommand(argv []string, compressOut bool) (string, int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	cmdLine := strings.Join(argv, " ")
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			// The command never started (missing binary, bad shebang...).
			return string(raw), 1, fmt.Errorf("failed to execute %s: %w", argv[0], err)
		}
		code := ee.ExitCode()
		if code <= 0 {
			code = 1 // killed by a signal: report a plain failure
		}
		return string(raw), code, fmt.Errorf("%s failed with exit code %d\n%s", cmdLine, code, stderr.String())
	}
	if compressOut {
		return compress.New().Compress(cmdLine, string(raw)), 0, nil
	}
	return string(raw), 0, nil
}
