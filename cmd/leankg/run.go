package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/cliexit"
	"github.com/FreePeak/LeanKG/internal/compress"
)

// cmdRun executes one command and streams its stdout back, optionally
// compressed. Ported from the Rust `leankg run` verb
// (cli::shell_runner::ShellRunner + main.rs run_shell_command):
//
//	leankg run [--compress] -- <command> [args...]
//
// The command runs as an argv vector — never through a shell — so quoting,
// globbing, and redirection belong to the caller.
func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	compressOut := fs.Bool("compress", false, "RTK-style compression of the command's stdout")
	// `run` is a trailing-var-arg verb (clap: flags, then `--`, then the child
	// argv verbatim). Go's flag package already stops at the first positional and
	// strips a `--` terminator, so its parse IS the clap semantics here — the
	// interspersed walk must NOT be used: re-parsing after the first positional
	// would strip the child's own flags (`run -- echo -n hi`) as leankg flags.
	if err := cliexit.Parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "run: no command provided. Usage: leankg run [--compress] -- <command> [args...]")
		return cliexit.Code(2)
	}
	stdout, code, err := runCommand(ctx, fs.Args(), *compressOut)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err // cliexit.Finish prints it and exits 130
		}
		fmt.Fprintln(os.Stderr, err)
		return cliexit.Code(code)
	}
	_, _ = os.Stdout.WriteString(stdout)
	return nil
}

// runWaitDelay bounds how long Wait keeps reading the child's stdout after the
// child has exited or been killed. A grandchild that inherited stdout holds the
// pipe open, so without the bound a cancel waits for that grandchild to finish.
const runWaitDelay = 2 * time.Second

// runCommand runs argv[0] with argv[1:] and returns the captured stdout plus
// the process exit code. A non-zero child exit is an error carrying the exit
// code and the child's stderr (the Rust ShellRunner::run message shape); the
// code is the child's own exit status, so `leankg run cargo test` reports
// cargo's code instead of a constant.
//
// stderr is captured for the failure message only: like the Rust runner, a
// successful command contributes its stdout alone.
//
// A cancelled ctx kills the child and returns an error wrapping
// context.Canceled. That check runs before the exit status is read, because a
// child killed by the cancel (or by a terminal SIGINT to the process group)
// would otherwise look like a plain exit-1 failure.
func runCommand(ctx context.Context, argv []string, compressOut bool) (string, int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = runWaitDelay
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	cmdLine := strings.Join(argv, " ")
	if errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil && cmd.ProcessState.Success() {
		// The child exited 0; only a grandchild still held stdout past the
		// bound. Its output is what the child wrote, so the run succeeds.
		err = nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return string(raw), 1, fmt.Errorf("%s: %w", cmdLine, ctx.Err())
		}
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
