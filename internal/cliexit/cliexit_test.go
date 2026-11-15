package cliexit

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"testing"
)

// Finish maps a verb's error to the exit status and writes the message, unless
// the error is silent (the verb already reported it, or only sets the status).
func TestFinishMapsErrorToStatusAndMessage(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		code   int
		stderr string
	}{
		{"nil is success", nil, 0, ""},
		{"code zero is success", Code(0), 0, ""},
		{"help is success and silent", flag.ErrHelp, 0, ""},
		{"plain error exits 1 and prints", errors.New("boom"), 1, "boom\n"},
		{"deadline is a plain failure", context.DeadlineExceeded, 1, "context deadline exceeded\n"},
		{"cancel exits 130 and prints", fmt.Errorf("index: %w", context.Canceled), 130, "index: context canceled\n"},
		{"usage exits 2 and prints", Usage(errors.New("bad flag")), 2, "bad flag\n"},
		{"code is silent", Code(3), 3, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := Finish(tc.err, &stderr); got != tc.code {
				t.Fatalf("Finish = %d, want %d", got, tc.code)
			}
			if stderr.String() != tc.stderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.stderr)
			}
		})
	}
}

// Parse reports a bad flag as exit 2 (the flag package already printed why)
// and -h as success, so a verb returns the error and main decides the status.
func TestParseMapsFlagErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
	}{
		{"valid flags", []string{"-n", "3"}, 0},
		{"unknown flag", []string{"-bogus"}, 2},
		{"help", []string{"-h"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			fs.Int("n", 0, "")
			if got := Finish(Parse(fs, tc.args), io.Discard); got != tc.code {
				t.Fatalf("Parse(%v) exit = %d, want %d", tc.args, got, tc.code)
			}
		})
	}
}
