// Package cliexit maps a verb's error to the process exit status. The two
// command binaries (cmd/leankg and cmd/leankg-embed) return errors from their
// verbs and call Finish once, in main.
package cliexit

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

// ExitCancelled is the status of a run stopped by SIGINT or SIGTERM, the
// shell's 128 + SIGINT convention.
const ExitCancelled = 130

// Error is an exit status the verb has settled on. Its message, if any, was
// already written by the verb, so Finish prints nothing for it.
type Error struct{ Code int }

func (e *Error) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// Code returns the silent exit status n, or nil for 0.
func Code(n int) error {
	if n == 0 {
		return nil
	}
	return &Error{Code: n}
}

// usageError is a bad invocation: its message is printed and the status is 2.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// Usage marks err as a bad invocation: Finish prints it and exits 2.
func Usage(err error) error { return &usageError{err: err} }

// Parse runs fs.Parse. The flag package has already printed a bad flag and the
// usage, so that case becomes a silent status 2; -h stays flag.ErrHelp, which
// Finish maps to success.
func Parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return Code(2)
	}
	return nil
}

// Finish prints err to stderr unless it is silent, and returns the exit status:
// 0 for nil and flag.ErrHelp, 130 for a cancelled context, the code of an Error,
// 2 for a usage error, and 1 for anything else.
func Finish(err error, stderr io.Writer) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var coded *Error
	if !errors.As(err, &coded) {
		fmt.Fprintln(stderr, err)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return ExitCancelled
	case errors.As(err, &coded):
		return coded.Code
	case errors.As(err, new(*usageError)):
		return 2
	default:
		return 1
	}
}
