package cliargs

import (
	"flag"
	"io"
	"reflect"
	"testing"

	"github.com/FreePeak/LeanKG/internal/cliexit"
)

// newFS returns a verb FlagSet shaped like the command binaries': it fails
// with an error instead of exiting, and stays quiet on the error path.
func newFS() (*flag.FlagSet, *string, *bool) {
	fs := flag.NewFlagSet("verb", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	model := fs.String("model", "", "")
	full := fs.Bool("full", false, "")
	return fs, model, full
}

func TestInterspersed(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		max         int
		wantPos     []string
		wantModel   string
		wantFull    bool
		wantErrCode int // 0 means no error
	}{
		{name: "no args", args: nil, max: 1, wantPos: nil},
		{name: "positional only", args: []string{"x"}, max: 1, wantPos: []string{"x"}},
		{name: "flag after positional", args: []string{"x", "--model", "m"}, max: 1, wantPos: []string{"x"}, wantModel: "m"},
		{name: "flag before positional", args: []string{"--model", "m", "x"}, max: 1, wantPos: []string{"x"}, wantModel: "m"},
		{name: "flag between positionals", args: []string{"a", "--full", "b"}, max: 2, wantPos: []string{"a", "b"}, wantFull: true},
		{name: "equals form", args: []string{"x", "--model=m"}, max: 1, wantPos: []string{"x"}, wantModel: "m"},
		{name: "bool flag after positional", args: []string{"x", "--full"}, max: 1, wantPos: []string{"x"}, wantFull: true},
		{name: "terminator ends flags", args: []string{"--", "--model"}, max: 1, wantPos: []string{"--model"}},
		{name: "terminator after positional", args: []string{"x", "--", "--model"}, max: 2, wantPos: []string{"x", "--model"}},
		{name: "terminator after positional past max", args: []string{"x", "--", "--model"}, max: 1, wantErrCode: 2},
		{name: "unknown flag is a silent usage status", args: []string{"x", "--nope"}, max: 1, wantErrCode: 2},
		{name: "surplus positional is a usage error", args: []string{"a", "b"}, max: 1, wantErrCode: 2},
		{name: "unlimited accepts many positionals", args: []string{"a", "b", "c"}, max: -1, wantPos: []string{"a", "b", "c"}},
		{name: "unlimited still parses flags", args: []string{"a", "--model", "m", "b"}, max: -1, wantPos: []string{"a", "b"}, wantModel: "m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, model, full := newFS()
			pos, err := Interspersed("verb", fs, tc.args, tc.max)
			if tc.wantErrCode != 0 {
				if err == nil {
					t.Fatalf("Interspersed(%v) error = nil, want an error", tc.args)
				}
				if got := cliexit.Finish(err, io.Discard); got != tc.wantErrCode {
					t.Fatalf("exit status = %d, want %d (err %v)", got, tc.wantErrCode, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Interspersed(%v) error = %v", tc.args, err)
			}
			if !reflect.DeepEqual(pos, tc.wantPos) {
				t.Fatalf("positionals = %q, want %q", pos, tc.wantPos)
			}
			if *model != tc.wantModel {
				t.Fatalf("--model = %q, want %q", *model, tc.wantModel)
			}
			if *full != tc.wantFull {
				t.Fatalf("--full = %v, want %v", *full, tc.wantFull)
			}
		})
	}
}

// A surplus positional names the verb and the argument, so the operator sees
// which token was not accepted.
func TestInterspersedSurplusMessage(t *testing.T) {
	fs, _, _ := newFS()
	_, err := Interspersed("query", fs, []string{"a", "b"}, 1)
	const want = `query: unexpected argument "b"`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}
